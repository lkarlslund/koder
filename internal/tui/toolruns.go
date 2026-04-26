package tui

import (
	"encoding/json"
	"fmt"
	"hash"
	"hash/fnv"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/theme"
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
	ToolRun ui.ToolRun
}

func (m *Model) transcriptBlocks() []transcriptBlock {
	var blocks []transcriptBlock

	appendMessage := func(msg domain.Message) {
		blocks = append(blocks, transcriptBlock{Kind: transcriptBlockMessage, Message: msg})
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
			hasSpecialCard := false
			if _, ok := compactionToolRun(parts, msg); ok {
				hasSpecialCard = true
			}
			for _, part := range parts {
				if run := eventNoticeToolRun(part); strings.TrimSpace(run.ID) != "" {
					hasSpecialCard = true
					break
				}
			}
			body := m.renderMessageParts(parts)
			if strings.TrimSpace(body) == "" && !hasSpecialCard {
				body = strings.TrimSpace(msg.Summary)
			}
			if isSyntheticToolSummary(body) {
				body = ""
			}
			if strings.TrimSpace(body) != "" {
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
	return blocks
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
		body := strings.TrimSpace(part.Body)
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
	var meta eventNoticeMeta
	_ = json.Unmarshal([]byte(part.MetaJSON), &meta)
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
		Preview:  strings.TrimSpace(part.Body),
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
		req, err := tools.RequestFromMeta(part.MetaJSON)
		if err != nil {
			continue
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
	meta := stringMeta(part.MetaJSON)
	approvalID, _ := strconv.ParseInt(strings.TrimSpace(meta["approval_id"]), 10, 64)
	tool := domain.ToolKind(strings.TrimSpace(meta["tool"]))
	preview := strings.TrimSpace(meta["command"])
	presentation := presentationFromPreview(tool, preview)
	run := ui.ToolRun{
		ID:         approvalFallbackID(approvalID, tool, preview),
		Tool:       tool,
		ToolCallID: strings.TrimSpace(meta["tool_call_id"]),
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
	meta := stringMeta(part.MetaJSON)
	if strings.TrimSpace(meta["approval_id"]) == "" || strings.TrimSpace(meta["status"]) == "" {
		return ui.ToolRun{}, false
	}
	if strings.EqualFold(strings.TrimSpace(meta["status"]), "pending") {
		return ui.ToolRun{}, false
	}
	approvalID, _ := strconv.ParseInt(strings.TrimSpace(meta["approval_id"]), 10, 64)
	tool := domain.ToolKind(strings.TrimSpace(meta["tool"]))
	preview := strings.TrimSpace(meta["command"])
	presentation := presentationFromPreview(tool, preview)
	status := ui.ToolRunStatusApproved
	output := ""
	if strings.EqualFold(strings.TrimSpace(meta["status"]), "denied") {
		status = ui.ToolRunStatusDenied
		output = strings.TrimSpace(part.Body)
	}
	run := ui.ToolRun{
		ID:         approvalFallbackID(approvalID, tool, preview),
		Tool:       tool,
		ToolCallID: strings.TrimSpace(meta["tool_call_id"]),
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
	meta := stringMeta(part.MetaJSON)
	req, err := tools.RequestFromMetaMap(meta)
	output := strings.TrimSpace(part.Body)
	if display, ok := tools.DisplayTextForPart(part); ok {
		output = strings.TrimSpace(display)
	}
	diff := toolRunDiffBody(parts)
	status := ui.ToolRunStatusCompleted
	if strings.Contains(strings.ToLower(output), "denied") {
		status = ui.ToolRunStatusDenied
	}
	if strings.HasPrefix(output, "Error:") {
		status = ui.ToolRunStatusFailed
	}
	if err != nil {
		req = tools.Request{
			Tool:       domain.ToolKind(strings.TrimSpace(meta["tool"])),
			ToolCallID: strings.TrimSpace(meta["tool_call_id"]),
			Args:       map[string]string{},
		}
	}
	presentation := tools.PresentationForRequest(req)
	if strings.TrimSpace(presentation.Preview) == "" {
		presentation.Preview = firstNonEmptyString(strings.TrimSpace(msg.Summary), output)
	}
	switch req.Tool {
	case domain.ToolKindBash:
		command := firstNonEmptyString(strings.TrimSpace(req.Args["command"]), strings.TrimSpace(meta["command"]))
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
	case domain.ToolKindEdit:
		path := firstNonEmptyString(strings.TrimSpace(req.Args["path"]), strings.TrimSpace(meta["path"]))
		if path != "" {
			presentation.Title = "Edited file " + filepath.ToSlash(path)
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
	case ui.ToolRunStatusApproved, ui.ToolRunStatusCompleted, ui.ToolRunStatusDenied, ui.ToolRunStatusFailed:
		return true
	default:
		return false
	}
}

func (m *Model) cachedTranscriptBlock(block transcriptBlock) cachedTranscriptBlock {
	if m.transcriptCache == nil {
		m.transcriptCache = make(map[string]cachedTranscriptBlock)
	}
	key := m.transcriptBlockCacheKey(block)
	element := m.renderTranscriptBlockElement(block)
	lineCount := m.estimateTranscriptBlockHeight(block)
	if cached, ok := m.transcriptCache[key]; ok {
		if reusable, ok := cached.element.(*ui.CachedElement); ok {
			reusable.SetChild(element)
		} else {
			cached.element = element
		}
		cached.lineCount = lineCount
		if block.Kind == transcriptBlockToolRun && strings.TrimSpace(block.ToolRun.ID) != "" {
			cached.controlID = "toolrun:" + block.ToolRun.ID
		}
		m.transcriptCache[key] = cached
		return cached
	}
	if element != nil {
		element = ui.NewCachedElement(element, lineCount)
	}
	cached := cachedTranscriptBlock{
		element:   element,
		lineCount: lineCount,
	}
	if block.Kind == transcriptBlockToolRun && strings.TrimSpace(block.ToolRun.ID) != "" {
		cached.controlID = "toolrun:" + block.ToolRun.ID
	}
	m.transcriptCache[key] = cached
	return cached
}

func (m *Model) estimateTranscriptBlockHeight(block transcriptBlock) int {
	width := max(1, m.viewport.Width)
	switch block.Kind {
	case transcriptBlockToolRun:
		lines := 3
		lines += strings.Count(block.ToolRun.Title, "\n")
		lines += strings.Count(block.ToolRun.Subtitle, "\n")
		lines += strings.Count(block.ToolRun.Preview, "\n")
		lines += strings.Count(block.ToolRun.Output, "\n")
		lines += strings.Count(block.ToolRun.Diff, "\n")
		lines += strings.Count(block.ToolRun.ErrorText, "\n")
		return max(3, lines)
	case transcriptBlockMessage:
		summary := strings.TrimSpace(block.Message.Summary)
		if summary == "" {
			summary = "message"
		}
		lines := strings.Count(summary, "\n") + 1
		return max(1, lines+(len([]rune(summary))/max(24, width)))
	default:
		return 1
	}
}

func (m *Model) transcriptBlockCacheKey(block transcriptBlock) string {
	width := max(0, m.viewport.Width)
	hasher := fnv.New64a()
	switch block.Kind {
	case transcriptBlockToolRun:
		writeHashStrings(hasher,
			"toolrun",
			strconv.Itoa(width),
			strconv.FormatBool(m.expandedToolRuns[block.ToolRun.ID]),
			strconv.FormatBool(m.expandedToolRunCommands[block.ToolRun.ID]),
			string(block.ToolRun.Tool),
			block.ToolRun.ID,
			block.ToolRun.ToolCallID,
			strconv.FormatInt(block.ToolRun.ApprovalID, 10),
			block.ToolRun.Title,
			block.ToolRun.Command,
			block.ToolRun.Subtitle,
			block.ToolRun.Preview,
			block.ToolRun.Output,
			block.ToolRun.Diff,
			block.ToolRun.ErrorText,
			string(block.ToolRun.Status),
		)
	default:
		writeHashStrings(hasher,
			"message",
			strconv.Itoa(width),
			string(block.Message.Role),
			strconv.FormatBool(m.showReasoning),
			strconv.FormatBool(m.showSystem),
			strconv.FormatBool(m.halfBlocksEnabled()),
			block.Message.Summary,
			block.Message.CreatedAt.UTC().Format(time.RFC3339Nano),
		)
		for _, part := range m.parts[block.Message.ID] {
			writeHashStrings(hasher,
				strconv.FormatInt(part.ID, 10),
				string(part.Kind),
				part.Body,
				part.MetaJSON,
			)
		}
	}
	return fmt.Sprintf("%d:%x", block.Message.ID+int64(block.ToolRun.ApprovalID), hasher.Sum64())
}

func writeHashStrings(hasher hash.Hash64, values ...string) {
	for _, value := range values {
		_, _ = hasher.Write([]byte(value))
		_, _ = hasher.Write([]byte{0})
	}
}

func (m *Model) renderTranscriptBlockElement(block transcriptBlock) ui.Element {
	switch block.Kind {
	case transcriptBlockToolRun:
		return toolRunCardElement{
			Run:             block.ToolRun,
			Palette:         m.palette,
			Width:           m.viewport.Width,
			ExpandedOutput:  m.expandedToolRuns[block.ToolRun.ID],
			ExpandedCommand: m.expandedToolRunCommands[block.ToolRun.ID],
		}
	default:
		return m.renderTranscriptMessageElement(block.Message)
	}
}

type toolRunCardElement struct {
	Run             ui.ToolRun
	Palette         theme.Palette
	Width           int
	ExpandedOutput  bool
	ExpandedCommand bool
}

func (e toolRunCardElement) Measure(_ *ui.Context, constraints ui.Constraints) ui.Size {
	width := e.Width
	if width <= 0 {
		width = constraints.MaxW
	}
	return constraints.Clamp(e.Run.CardSurface(e.Palette, width, e.ExpandedOutput, e.ExpandedCommand).Size())
}

func (e toolRunCardElement) Render(ctx *ui.Context, bounds ui.Rect) ui.Surface {
	width := e.Width
	if width <= 0 {
		width = bounds.W
	}
	surface := e.Run.CardSurface(e.Palette, width, e.ExpandedOutput, e.ExpandedCommand)
	if ctx != nil && ctx.Runtime != nil {
		surface.RegisterControls(ctx.Runtime, bounds.X, bounds.Y)
	}
	return surface
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
	for _, block := range m.transcriptBlocks() {
		if block.Kind != transcriptBlockToolRun {
			continue
		}
		candidate := block.ToolRun
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
		if part.Kind == domain.PartKindDiff && strings.TrimSpace(part.Body) != "" {
			return part.Body
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

func presentationFromPreview(tool domain.ToolKind, preview string) tools.Presentation {
	return tools.PresentationForTool(tool, preview)
}
