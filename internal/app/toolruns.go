package app

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/execruntime"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
	_ "github.com/lkarlslund/koder/internal/tools/all"
	"github.com/lkarlslund/koder/internal/ui"
)

type transcriptBlockKind int

const (
	transcriptBlockMessage transcriptBlockKind = iota
	transcriptBlockToolRun
)

type transcriptBlock struct {
	Kind    transcriptBlockKind
	Message domain.Message
	Parts   []domain.Part
	ToolRun ui.ToolRun
	Pending bool
}

func (m *Model) transcriptBlocks() []transcriptBlock {
	var blocks []transcriptBlock

	appendMessage := func(msg domain.Message) {
		blocks = append(blocks, transcriptBlock{Kind: transcriptBlockMessage, Message: msg, Parts: m.parts[msg.ID]})
	}
	appendRun := func(run ui.ToolRun) *ui.ToolRun {
		blocks = append(blocks, transcriptBlock{Kind: transcriptBlockToolRun, ToolRun: run})
		return &blocks[len(blocks)-1].ToolRun
	}
	tracker := newToolRunTracker(appendRun)

	for _, msg := range m.messages {
		parts := m.parts[msg.ID]
		switch msg.Role {
		case domain.MessageRoleAssistant:
			if m.assistantMessageShouldExist(msg, parts) {
				appendMessage(msg)
			}
			if run, ok := compactionToolRun(parts, msg); ok {
				appendRun(run)
			}
			for _, part := range parts {
				if run := eventNoticeToolRun(part); strings.TrimSpace(run.ID) != "" {
					appendRun(run)
				}
			}
			for _, run := range toolRunsFromAssistantMessage(parts) {
				tracker.Upsert(run)
			}
		case domain.MessageRoleTool:
			consumed := false
			for _, run := range toolRunsFromToolMessage(parts, msg) {
				tracker.Upsert(run)
				consumed = true
			}
			if !consumed {
				appendMessage(msg)
			}
		default:
			appendMessage(msg)
		}
	}
	for _, run := range m.currentLiveExecRuns() {
		tracker.Upsert(run)
	}
	if pending := m.pendingAssistantParts(); len(pending) > 0 {
		blocks = append(blocks, transcriptBlock{
			Kind:    transcriptBlockMessage,
			Pending: true,
			Message: domain.Message{
				Role:      domain.MessageRoleAssistant,
				CreatedAt: m.pendingAssistant.CreatedAt,
			},
			Parts: pending,
		})
	}
	return blocks
}

func (m *Model) currentLiveExecRuns() []ui.ToolRun {
	if m == nil || m.exec == nil || m.currentSession.ID == 0 || m.currentChat.ID == 0 {
		return nil
	}
	snaps, err := m.exec.List(context.Background(), execruntime.ListRequest{
		SessionID: m.currentSession.ID,
		ChatID:    m.currentChat.ID,
		Scope:     execruntime.ScopeChat,
		MaxBytes:  4 * 1024,
	})
	if err != nil {
		return nil
	}
	runs := make([]ui.ToolRun, 0, len(snaps))
	for _, snap := range snaps {
		runs = append(runs, liveExecToolRun(snap))
	}
	return runs
}

func (m *Model) assistantMessageShouldExist(msg domain.Message, parts []domain.Part) bool {
	summary := strings.TrimSpace(msg.Summary)
	if summary != "" && !isSyntheticToolSummary(summary) {
		return true
	}
	for _, part := range parts {
		switch part.Kind {
		case domain.PartKindText,
			domain.PartKindReasoning,
			domain.PartKindSystemNotice,
			domain.PartKindAttachment:
			return true
		case domain.PartKindEventNotice:
			if eventNoticeToolRun(part).ID == "" {
				return true
			}
		case domain.PartKindCompaction,
			domain.PartKindToolCall,
			domain.PartKindToolOutput,
			domain.PartKindApprovalRequest,
			domain.PartKindReference,
			domain.PartKindUsage:
			continue
		default:
			if strings.TrimSpace(part.Text()) != "" {
				return true
			}
		}
	}
	return false
}

type toolRunTracker struct {
	append       func(ui.ToolRun) *ui.ToolRun
	byID         map[string]*ui.ToolRun
	byToolCallID map[string]*ui.ToolRun
	byApprovalID map[int64]*ui.ToolRun
}

func newToolRunTracker(append func(ui.ToolRun) *ui.ToolRun) *toolRunTracker {
	return &toolRunTracker{
		append:       append,
		byID:         map[string]*ui.ToolRun{},
		byToolCallID: map[string]*ui.ToolRun{},
		byApprovalID: map[int64]*ui.ToolRun{},
	}
}

func (t *toolRunTracker) Upsert(run ui.ToolRun) {
	if t == nil || strings.TrimSpace(run.ID) == "" {
		return
	}
	if existing := t.lookup(run); existing != nil {
		mergeToolRun(existing, run)
		t.index(existing)
		return
	}
	t.index(t.append(run))
}

func (t *toolRunTracker) lookup(run ui.ToolRun) *ui.ToolRun {
	if t == nil {
		return nil
	}
	if run.ToolCallID != "" {
		if existing := t.byToolCallID[run.ToolCallID]; existing != nil {
			return existing
		}
	}
	if run.ApprovalID > 0 {
		if existing := t.byApprovalID[run.ApprovalID]; existing != nil {
			return existing
		}
	}
	if existing := t.byID[run.ID]; existing != nil {
		return existing
	}
	return nil
}

func (t *toolRunTracker) index(run *ui.ToolRun) {
	if t == nil || run == nil {
		return
	}
	if run.ID != "" {
		t.byID[run.ID] = run
	}
	if run.ToolCallID != "" {
		t.byToolCallID[run.ToolCallID] = run
	}
	if run.ApprovalID > 0 {
		t.byApprovalID[run.ApprovalID] = run
	}
}

func compactionToolRun(parts []domain.Part, msg domain.Message) (ui.ToolRun, bool) {
	for _, part := range parts {
		if part.Kind != domain.PartKindCompaction {
			continue
		}
		body := strings.TrimSpace(part.Text())
		if body == "" {
			continue
		}
		return ui.ToolRun{
			ID:       fmt.Sprintf("compaction:%d", msg.ID),
			Title:    "Compacted context",
			Subtitle: "Replacement history sent to the model",
			Preview:  body,
			Status:   ui.ToolRunStatusCompleted,
		}, true
	}
	return ui.ToolRun{}, false
}

func eventNoticeToolRun(part domain.Part) ui.ToolRun {
	if part.Kind != domain.PartKindEventNotice {
		return ui.ToolRun{}
	}
	payload, ok := part.Payload.(domain.EventNoticePayload)
	if !ok && strings.TrimSpace(part.MetaJSON) == "" {
		return ui.ToolRun{}
	}
	meta := eventNoticeMeta{
		Kind: payload.Kind, Severity: payload.Severity, Reason: payload.Reason, Title: payload.Title,
		Subtitle: payload.Subtitle, Tool: string(payload.Tool), Count: payload.Count, Limit: payload.Limit,
	}
	if !ok {
		_ = json.Unmarshal([]byte(part.MetaJSON), &meta)
	}
	if strings.TrimSpace(meta.Kind) != "loop_pause" {
		return ui.ToolRun{}
	}
	title := firstNonEmptyString(strings.TrimSpace(meta.Title), "Continuation paused")
	subtitle := firstNonEmptyString(strings.TrimSpace(meta.Subtitle), eventNoticePauseSubtitle(meta))
	return ui.ToolRun{
		ID:       "pause:" + title + ":" + subtitle + ":" + strings.TrimSpace(meta.Reason),
		Tool:     domain.ToolKind(strings.TrimSpace(meta.Tool)),
		Title:    title,
		Subtitle: subtitle,
		Preview:  strings.TrimSpace(part.Text()),
		Status:   ui.ToolRunStatusPaused,
	}
}

func eventNoticePauseSubtitle(meta eventNoticeMeta) string {
	switch strings.TrimSpace(meta.Reason) {
	case "repeated_tool":
		if tool := strings.TrimSpace(meta.Tool); tool != "" {
			return "Repeated identical " + tool + " calls"
		}
		return "Repeated identical tool calls"
	case "turn_limit":
		if meta.Limit > 0 {
			return fmt.Sprintf("Turn limit reached (%d)", meta.Limit)
		}
		return "Turn limit reached"
	case "provider_refusal":
		return "Provider stopped continuation"
	default:
		return ""
	}
}

func toolRunsFromAssistantMessage(parts []domain.Part) []ui.ToolRun {
	var runs []ui.ToolRun
	for _, part := range parts {
		if part.Kind != domain.PartKindToolCall {
			continue
		}
		var req tools.Request
		payload, ok := part.Payload.(domain.ToolCallPayload)
		if ok {
			var err error
			req, err = tools.Normalize(tools.Request{Tool: payload.Tool, ToolCallID: payload.ToolCallID, Args: payload.Args})
			if err != nil {
				continue
			}
		} else {
			var err error
			req, err = tools.RequestFromMeta(part.MetaJSON)
			if err != nil {
				continue
			}
		}
		presentation := tools.PresentationForRequest(req)
		command := ""
		if req.Tool == domain.ToolKindBash {
			command = strings.TrimSpace(req.Args["command"])
			if command != "" {
				presentation.Title = "Ran command " + firstNonEmptyCommandLine(command)
				presentation.Subtitle = ""
			}
		}
		runs = append(runs, ui.ToolRun{
			ID:         firstNonEmptyString(strings.TrimSpace(req.ToolCallID), toolRunFallbackID(req.Tool, presentation.Preview)),
			Tool:       req.Tool,
			ToolCallID: strings.TrimSpace(req.ToolCallID),
			Title:      presentation.Title,
			Command:    command,
			Subtitle:   presentation.Subtitle,
			Preview:    presentation.Preview,
			Status:     ui.ToolRunStatusRequested,
		})
	}
	return runs
}

func toolRunsFromToolMessage(parts []domain.Part, msg domain.Message) []ui.ToolRun {
	var runs []ui.ToolRun
	for _, part := range parts {
		if run, ok := toolRunApprovalRequest(part); ok {
			runs = append(runs, run)
			continue
		}
		if run, ok := toolRunApprovalReply(part); ok {
			runs = append(runs, run)
			continue
		}
		if part.Kind != domain.PartKindToolOutput {
			continue
		}
		runs = append(runs, toolRunOutput(part, parts, msg))
	}
	return runs
}

func toolRunApprovalRequest(part domain.Part) (ui.ToolRun, bool) {
	if part.Kind != domain.PartKindApprovalRequest {
		return ui.ToolRun{}, false
	}
	payload, ok := part.Payload.(domain.ApprovalRequestPayload)
	if !ok {
		meta := stringMeta(part.MetaJSON)
		if len(meta) == 0 {
			return ui.ToolRun{}, false
		}
		approvalID, _ := strconv.ParseInt(strings.TrimSpace(meta["approval_id"]), 10, 64)
		tool := domain.ToolKind(strings.TrimSpace(meta["tool"]))
		preview := strings.TrimSpace(meta["command"])
		presentation := presentationFromPreview(tool, preview)
		return ui.ToolRun{
			ID:         approvalFallbackID(approvalID, tool, preview),
			Tool:       tool,
			ToolCallID: strings.TrimSpace(meta["tool_call_id"]),
			ApprovalID: approvalID,
			Title:      presentation.Title,
			Subtitle:   presentation.Subtitle,
			Preview:    preview,
			Status:     ui.ToolRunStatusPendingApproval,
		}, tool != ""
	}
	approvalID := payload.ApprovalID
	tool := payload.Tool
	preview := strings.TrimSpace(payload.Command)
	presentation := presentationFromPreview(tool, preview)
	run := ui.ToolRun{
		ID:         approvalFallbackID(approvalID, tool, preview),
		Tool:       tool,
		ToolCallID: strings.TrimSpace(payload.ToolCallID),
		ApprovalID: approvalID,
		Title:      presentation.Title,
		Subtitle:   presentation.Subtitle,
		Preview:    preview,
		Status:     ui.ToolRunStatusPendingApproval,
	}
	return run, tool != ""
}

func toolRunApprovalReply(part domain.Part) (ui.ToolRun, bool) {
	if part.Kind != domain.PartKindSystemNotice && part.Kind != domain.PartKindToolOutput {
		return ui.ToolRun{}, false
	}
	var approvalID int64
	var tool domain.ToolKind
	var toolCallID string
	var preview string
	statusText := ""
	if payload, ok := part.Payload.(domain.ToolOutputPayload); ok {
		tool = payload.Tool
		toolCallID = payload.ToolCallID
		preview = strings.TrimSpace(payload.Args["command"])
		statusText = string(payload.Status)
		approvalID, _ = strconv.ParseInt(strings.TrimSpace(payload.Args["approval_id"]), 10, 64)
	} else {
		meta := stringMeta(part.MetaJSON)
		if strings.TrimSpace(meta["approval_id"]) == "" || strings.TrimSpace(meta["status"]) == "" {
			return ui.ToolRun{}, false
		}
		approvalID, _ = strconv.ParseInt(strings.TrimSpace(meta["approval_id"]), 10, 64)
		tool = domain.ToolKind(strings.TrimSpace(meta["tool"]))
		toolCallID = strings.TrimSpace(meta["tool_call_id"])
		preview = strings.TrimSpace(meta["command"])
		statusText = strings.TrimSpace(meta["status"])
	}
	if approvalID == 0 || strings.TrimSpace(statusText) == "" {
		return ui.ToolRun{}, false
	}
	if strings.EqualFold(strings.TrimSpace(statusText), "pending") {
		return ui.ToolRun{}, false
	}
	presentation := presentationFromPreview(tool, preview)
	status := ui.ToolRunStatusApproved
	output := ""
	if strings.EqualFold(strings.TrimSpace(statusText), "denied") {
		status = ui.ToolRunStatusDenied
		output = strings.TrimSpace(part.Text())
	}
	run := ui.ToolRun{
		ID:         approvalFallbackID(approvalID, tool, preview),
		Tool:       tool,
		ToolCallID: strings.TrimSpace(toolCallID),
		ApprovalID: approvalID,
		Title:      presentation.Title,
		Subtitle:   presentation.Subtitle,
		Preview:    preview,
		Output:     output,
		Status:     status,
	}
	return run, tool != ""
}

func toolRunOutput(part domain.Part, parts []domain.Part, msg domain.Message) ui.ToolRun {
	payload, _ := part.Payload.(domain.ToolOutputPayload)
	req := tools.Request{Tool: payload.Tool, ToolCallID: payload.ToolCallID, Args: payload.Args}
	if req.Args == nil {
		req.Args = map[string]string{}
	}
	var err error
	req, err = tools.Normalize(req)
	meta := map[string]string{}
	if part.Payload == nil {
		meta = stringMeta(part.MetaJSON)
		req, err = tools.RequestFromMetaMap(meta)
	}
	output := strings.TrimSpace(part.Text())
	if display, ok := tools.DisplayTextForPart(part); ok {
		output = strings.TrimSpace(display)
	}
	diff := strings.TrimSpace(payload.Diff)
	status := ui.ToolRunStatusCompleted
	storedTool, storedStatus, hasStored := tools.StoredResultInfoForPart(part)
	if hasStored && req.Tool == "" {
		req.Tool = storedTool
	}
	switch storedStatus {
	case tools.StoredResultStatusDenied:
		status = ui.ToolRunStatusDenied
	case tools.StoredResultStatusError:
		status = ui.ToolRunStatusFailed
	}
	if strings.Contains(strings.ToLower(output), "denied") {
		status = ui.ToolRunStatusDenied
	}
	if strings.HasPrefix(output, "Error:") {
		status = ui.ToolRunStatusFailed
	}
	if err != nil {
		req = tools.Request{
			Tool:       firstNonEmptyTool(payload.Tool, domain.ToolKind(strings.TrimSpace(meta["tool"]))),
			ToolCallID: firstNonEmptyString(strings.TrimSpace(payload.ToolCallID), strings.TrimSpace(meta["tool_call_id"])),
			Args:       meta,
		}
	}
	presentation := tools.PresentationForRequest(req)
	if strings.TrimSpace(presentation.Preview) == "" {
		presentation.Preview = firstNonEmptyString(strings.TrimSpace(msg.Summary), output)
	}
	if status == ui.ToolRunStatusFailed && req.Tool != "" {
		presentation.Title = string(req.Tool)
		presentation.Subtitle = ""
	}
	switch req.Tool {
	case domain.ToolKindBash:
		command := strings.TrimSpace(req.Args["command"])
		if command != "" {
			presentation.Title = "Ran command " + firstNonEmptyCommandLine(command)
			presentation.Subtitle = ""
			presentation.Preview = output
			return ui.ToolRun{
				ID:         firstNonEmptyString(req.ToolCallID, toolRunFallbackID(req.Tool, presentation.Preview), msg.Summary),
				Tool:       req.Tool,
				ToolCallID: req.ToolCallID,
				Title:      presentation.Title,
				Command:    command,
				Subtitle:   presentation.Subtitle,
				Preview:    presentation.Preview,
				Output:     output,
				Diff:       diff,
				Status:     status,
			}
		}
	case domain.ToolKindExecCommand, domain.ToolKindExecStatus, domain.ToolKindExecWriteStdin, domain.ToolKindExecResize, domain.ToolKindExecTerminate:
		processID, command, state := strings.TrimSpace(meta["process_id"]), firstNonEmptyString(strings.TrimSpace(meta["command"]), strings.TrimSpace(req.Args["cmd"])), strings.TrimSpace(meta["state"])
		var exitCode *int
		exitCode = optionalIntPtr(strings.TrimSpace(meta["exit_code"]))
		tty := parseBoolString(strings.TrimSpace(meta["tty"]))
		if stored, ok := payload.Result.(domain.ExecStoredResult); ok {
			processID = strings.TrimSpace(stored.ProcessID)
			command = firstNonEmptyString(strings.TrimSpace(stored.Command), command)
			state = strings.TrimSpace(stored.State)
			exitCode = stored.ExitCode
			tty = stored.TTY
		}
		runStatus := toolRunStatusFromExecState(state)
		if command != "" {
			presentation.Title = execCommandTitle(command, runStatus)
			presentation.Subtitle = execToolRunSubtitle(processID, tty, exitCode)
			presentation.Preview = output
			return ui.ToolRun{
				ID:         firstNonEmptyString(processID, req.ToolCallID, toolRunFallbackID(req.Tool, command)),
				Tool:       domain.ToolKindExecCommand,
				ToolCallID: req.ToolCallID,
				Title:      presentation.Title,
				Command:    command,
				Subtitle:   presentation.Subtitle,
				ProcessID:  processID,
				TTY:        tty,
				ExitCode:   exitCode,
				Preview:    presentation.Preview,
				Output:     output,
				Status:     runStatus,
			}
		}
	case domain.ToolKindEdit:
		path := strings.TrimSpace(req.Args["path"])
		if stored, ok := tools.EditStoredResultForPart(part); ok {
			if strings.TrimSpace(stored.Diff) != "" {
				diff = strings.TrimSpace(stored.Diff)
			}
		}
		if strings.TrimSpace(diff) != "" {
			output = ui.EditDiffSummary(diff)
		} else {
			output = firstNonEmptyString(strings.TrimSpace(part.Text()), output)
		}
		if path != "" {
			presentation.Title = "Edited " + filepath.ToSlash(path)
			presentation.Subtitle = ""
		}
	}
	return ui.ToolRun{
		ID:         firstNonEmptyString(req.ToolCallID, toolRunFallbackID(req.Tool, presentation.Preview), msg.Summary),
		Tool:       req.Tool,
		ToolCallID: req.ToolCallID,
		Title:      presentation.Title,
		Subtitle:   presentation.Subtitle,
		Preview:    presentation.Preview,
		Output:     output,
		Diff:       diff,
		Status:     status,
	}
}

func liveExecToolRun(snap execruntime.Snapshot) ui.ToolRun {
	state := string(snap.State)
	return ui.ToolRun{
		ID:        strings.TrimSpace(snap.ProcessID),
		Tool:      domain.ToolKindExecCommand,
		Title:     execCommandTitle(snap.Command, toolRunStatusFromExecState(state)),
		Command:   strings.TrimSpace(snap.Command),
		Subtitle:  execToolRunSubtitle(snap.ProcessID, snap.TTY, snap.ExitCode),
		ProcessID: strings.TrimSpace(snap.ProcessID),
		TTY:       snap.TTY,
		ExitCode:  snap.ExitCode,
		Preview:   strings.TrimSpace(snap.Output),
		Output:    strings.TrimSpace(snap.Output),
		Status:    toolRunStatusFromExecState(state),
	}
}

func execCommandTitle(command string, status ui.ToolRunStatus) string {
	command = firstNonEmptyCommandLine(command)
	if command == "" {
		switch status {
		case ui.ToolRunStatusRunning:
			return "Running command"
		default:
			return "Ran command"
		}
	}
	switch status {
	case ui.ToolRunStatusRunning:
		return "Running command " + command
	default:
		return "Ran command " + command
	}
}

func toolRunStatusFromExecState(state string) ui.ToolRunStatus {
	switch strings.TrimSpace(state) {
	case string(execruntime.StateRunning):
		return ui.ToolRunStatusRunning
	case string(execruntime.StateCompleted):
		return ui.ToolRunStatusCompleted
	case string(execruntime.StateTerminated):
		return ui.ToolRunStatusTerminated
	case string(execruntime.StateLost):
		return ui.ToolRunStatusLost
	case string(execruntime.StateFailed):
		return ui.ToolRunStatusFailed
	default:
		return ui.ToolRunStatusRequested
	}
}

func mergeToolRun(dst *ui.ToolRun, src ui.ToolRun) {
	if dst == nil {
		return
	}
	terminal := toolRunHasTerminalStatus(src.Status)
	if strings.TrimSpace(src.ID) != "" {
		dst.ID = src.ID
	}
	if strings.TrimSpace(src.ToolCallID) != "" {
		dst.ToolCallID = src.ToolCallID
	}
	if src.ApprovalID > 0 {
		dst.ApprovalID = src.ApprovalID
	}
	if terminal || strings.TrimSpace(dst.Title) == "" {
		dst.Title = src.Title
	}
	if terminal || strings.TrimSpace(dst.Subtitle) == "" {
		dst.Subtitle = src.Subtitle
	}
	if terminal || strings.TrimSpace(dst.Preview) == "" {
		dst.Preview = src.Preview
	}
	if strings.TrimSpace(src.Output) != "" {
		dst.Output = src.Output
	}
	if strings.TrimSpace(src.Diff) != "" {
		dst.Diff = src.Diff
	}
	if strings.TrimSpace(src.ErrorText) != "" {
		dst.ErrorText = src.ErrorText
	}
	if src.Status != "" {
		dst.Status = src.Status
	}
}

func toolRunHasTerminalStatus(status ui.ToolRunStatus) bool {
	switch status {
	case ui.ToolRunStatusApproved, ui.ToolRunStatusCompleted, ui.ToolRunStatusTerminated, ui.ToolRunStatusLost, ui.ToolRunStatusDenied, ui.ToolRunStatusFailed:
		return true
	default:
		return false
	}
}

func (m *Model) transcriptBlockIdentityKey(block transcriptBlock) string {
	switch block.Kind {
	case transcriptBlockToolRun:
		if strings.TrimSpace(block.ToolRun.ID) != "" {
			return "toolrun:" + block.ToolRun.ID
		}
		if block.ToolRun.ApprovalID > 0 {
			return fmt.Sprintf("toolrun-approval:%d", block.ToolRun.ApprovalID)
		}
		if strings.TrimSpace(block.ToolRun.ToolCallID) != "" {
			return "toolrun-call:" + block.ToolRun.ToolCallID
		}
		return "toolrun-fallback:" + toolRunFallbackID(block.ToolRun.Tool, block.ToolRun.Preview)
	default:
		if block.Pending {
			return "pending-assistant"
		}
		return fmt.Sprintf("msg:%d", block.Message.ID)
	}
}

func (m *Model) approvalToolRun(item store.Approval) ui.ToolRun {
	run := ui.ToolRun{
		ID:         approvalFallbackID(item.ID, item.Tool, item.Command),
		Tool:       item.Tool,
		ApprovalID: item.ID,
		Title:      tools.PresentationForTool(item.Tool, item.Command).Title,
		Subtitle:   strings.TrimSpace(item.Command),
		Preview:    strings.TrimSpace(item.Command),
		Status:     ui.ToolRunStatusPendingApproval,
	}
	if req, err := tools.RequestFromStored(item.Tool, item.Command); err == nil {
		presentation := tools.PresentationForRequest(req)
		run.ToolCallID = req.ToolCallID
		run.Title = presentation.Title
		run.Subtitle = presentation.Subtitle
		run.Preview = firstNonEmptyString(presentation.Preview, run.Preview)
	}
	for _, itemController := range m.transcriptItems {
		candidateItem, ok := itemController.(toolRunTranscriptItem)
		if !ok {
			continue
		}
		var candidate ui.ToolRun
		switch concrete := candidateItem.(type) {
		case *bashToolRunTranscriptItem:
			candidate = concrete.run
		case *readToolRunTranscriptItem:
			candidate = concrete.run
		case *writeToolRunTranscriptItem:
			candidate = concrete.run
		case *editToolRunTranscriptItem:
			candidate = concrete.run
		case *genericToolRunTranscriptItem:
			candidate = concrete.run
		}
		if candidate.ApprovalID == item.ID {
			mergeToolRun(&run, candidate)
			run.Status = ui.ToolRunStatusPendingApproval
			return run
		}
		if run.ToolCallID != "" && candidate.ToolCallID == run.ToolCallID {
			mergeToolRun(&run, candidate)
			run.Status = ui.ToolRunStatusPendingApproval
			return run
		}
	}
	return run
}

func stringMeta(raw string) map[string]string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	meta := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &meta); err != nil {
		return nil
	}
	return meta
}

func firstNonEmptyCommandLine(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}
	for _, line := range strings.Split(input, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func toolRunFallbackID(tool domain.ToolKind, preview string) string {
	return fmt.Sprintf("%s:%s", tool, strings.TrimSpace(preview))
}

func approvalFallbackID(approvalID int64, tool domain.ToolKind, preview string) string {
	if approvalID > 0 {
		return fmt.Sprintf("approval:%d", approvalID)
	}
	return toolRunFallbackID(tool, preview)
}

func isSyntheticToolSummary(input string) bool {
	input = strings.TrimSpace(input)
	if input == "" {
		return false
	}
	return strings.HasPrefix(input, "tool:")
}

func toolRunDiffBody(parts []domain.Part) string {
	for _, part := range parts {
		if payload, ok := part.Payload.(domain.ToolOutputPayload); ok && strings.TrimSpace(payload.Diff) != "" {
			return payload.Diff
		}
	}
	return ""
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstNonEmptyTool(values ...domain.ToolKind) domain.ToolKind {
	for _, value := range values {
		if strings.TrimSpace(string(value)) != "" {
			return value
		}
	}
	return ""
}

func presentationFromPreview(tool domain.ToolKind, preview string) tools.Presentation {
	return tools.PresentationForTool(tool, preview)
}

func execToolRunSubtitle(processID string, tty bool, exitCode *int) string {
	parts := make([]string, 0, 3)
	if id := strings.TrimSpace(processID); id != "" {
		parts = append(parts, "id "+id)
	}
	if tty {
		parts = append(parts, "tty")
	}
	if exitCode != nil {
		parts = append(parts, fmt.Sprintf("exit %d", *exitCode))
	}
	return strings.Join(parts, "  ")
}

func optionalIntPtr(raw string) *int {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return nil
	}
	return &value
}

func parseBoolString(raw string) bool {
	value, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	return value
}
