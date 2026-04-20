package tui

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
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

type toolCallView struct {
	ID      string          `json:"tool_call_id,omitempty"`
	Tool    domain.ToolKind `json:"tool"`
	Path    string          `json:"path,omitempty"`
	Pattern string          `json:"pattern,omitempty"`
	Command string          `json:"command,omitempty"`
	Content string          `json:"content,omitempty"`
	Body    string          `json:"body,omitempty"`
	URL     string          `json:"url,omitempty"`
}

func (m *Model) transcriptBlocks() []transcriptBlock {
	var blocks []transcriptBlock
	openRuns := make([]*ui.ToolRun, 0, 4)
	byToolCallID := map[string]*ui.ToolRun{}
	byApprovalID := map[int64]*ui.ToolRun{}

	appendMessage := func(msg domain.Message) {
		blocks = append(blocks, transcriptBlock{Kind: transcriptBlockMessage, Message: msg})
	}
	appendRun := func(run ui.ToolRun) *ui.ToolRun {
		blocks = append(blocks, transcriptBlock{Kind: transcriptBlockToolRun, ToolRun: run})
		return &blocks[len(blocks)-1].ToolRun
	}

	for _, msg := range m.messages {
		parts := m.parts[msg.ID]
		switch msg.Role {
		case domain.MessageRoleAssistant:
			body := m.renderMessageParts(parts)
			if strings.TrimSpace(body) == "" {
				body = strings.TrimSpace(msg.Summary)
			}
			if isSyntheticToolSummary(body) {
				body = ""
			}
			if strings.TrimSpace(body) != "" {
				appendMessage(msg)
			}
			for _, run := range toolRunsFromAssistantMessage(parts) {
				ptr := appendRun(run)
				openRuns = append(openRuns, ptr)
				if ptr.ToolCallID != "" {
					byToolCallID[ptr.ToolCallID] = ptr
				}
			}
		case domain.MessageRoleTool:
			consumed := false
			if run, ok := toolRunApprovalRequest(parts); ok {
				target := findToolRun(run, openRuns, byToolCallID, byApprovalID)
				if target == nil {
					target = appendRun(run)
					openRuns = append(openRuns, target)
				}
				mergeToolRun(target, run)
				if target.ApprovalID > 0 {
					byApprovalID[target.ApprovalID] = target
				}
				consumed = true
			}
			if run, ok := toolRunApprovalReply(parts); ok {
				target := findToolRun(run, openRuns, byToolCallID, byApprovalID)
				if target == nil {
					target = appendRun(run)
					openRuns = append(openRuns, target)
				}
				mergeToolRun(target, run)
				if target.ApprovalID > 0 {
					byApprovalID[target.ApprovalID] = target
				}
				consumed = true
			}
			if run, ok := toolRunOutput(parts, msg); ok {
				target := findToolRun(run, openRuns, byToolCallID, byApprovalID)
				if target == nil {
					target = appendRun(run)
					openRuns = append(openRuns, target)
				}
				mergeToolRun(target, run)
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

func toolRunsFromAssistantMessage(parts []domain.Part) []ui.ToolRun {
	var runs []ui.ToolRun
	for _, part := range parts {
		if part.Kind != domain.PartKindToolCall {
			continue
		}
		call, ok := decodeToolCallView(part.MetaJSON)
		if !ok {
			continue
		}
		title, subtitle := summarizeToolCall(call.Tool, call)
		runs = append(runs, ui.ToolRun{
			ID:         firstNonEmptyString(strings.TrimSpace(call.ID), toolRunFallbackID(call.Tool, subtitle)),
			Tool:       call.Tool,
			ToolCallID: strings.TrimSpace(call.ID),
			Title:      title,
			Subtitle:   subtitle,
			Preview:    toolRunPreview(call.Tool, call),
			Status:     ui.ToolRunStatusRequested,
		})
	}
	return runs
}

func toolRunApprovalRequest(parts []domain.Part) (ui.ToolRun, bool) {
	for _, part := range parts {
		if part.Kind != domain.PartKindApprovalRequest {
			continue
		}
		meta := stringMeta(part.MetaJSON)
		approvalID, _ := strconv.ParseInt(strings.TrimSpace(meta["approval_id"]), 10, 64)
		tool := domain.ToolKind(strings.TrimSpace(meta["tool"]))
		preview := strings.TrimSpace(meta["command"])
		title, subtitle := summarizeToolSummary(tool, preview)
		return ui.ToolRun{
			ID:         approvalFallbackID(approvalID, tool, preview),
			Tool:       tool,
			ApprovalID: approvalID,
			Title:      title,
			Subtitle:   subtitle,
			Preview:    preview,
			Status:     ui.ToolRunStatusPendingApproval,
		}, tool != ""
	}
	return ui.ToolRun{}, false
}

func toolRunApprovalReply(parts []domain.Part) (ui.ToolRun, bool) {
	for _, part := range parts {
		if part.Kind != domain.PartKindSystemNotice && part.Kind != domain.PartKindToolOutput {
			continue
		}
		meta := stringMeta(part.MetaJSON)
		if strings.TrimSpace(meta["approval_id"]) == "" || strings.TrimSpace(meta["status"]) == "" {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(meta["status"]), "pending") {
			continue
		}
		approvalID, _ := strconv.ParseInt(strings.TrimSpace(meta["approval_id"]), 10, 64)
		tool := domain.ToolKind(strings.TrimSpace(meta["tool"]))
		preview := strings.TrimSpace(meta["command"])
		title, subtitle := summarizeToolSummary(tool, preview)
		status := ui.ToolRunStatusApproved
		if strings.EqualFold(strings.TrimSpace(meta["status"]), "denied") {
			status = ui.ToolRunStatusDenied
		}
		return ui.ToolRun{
			ID:         approvalFallbackID(approvalID, tool, preview),
			Tool:       tool,
			ApprovalID: approvalID,
			Title:      title,
			Subtitle:   subtitle,
			Preview:    preview,
			Output:     strings.TrimSpace(part.Body),
			Status:     status,
		}, tool != ""
	}
	return ui.ToolRun{}, false
}

func toolRunOutput(parts []domain.Part, msg domain.Message) (ui.ToolRun, bool) {
	for _, part := range parts {
		if part.Kind != domain.PartKindToolOutput {
			continue
		}
		meta := stringMeta(part.MetaJSON)
		tool := domain.ToolKind(strings.TrimSpace(meta["tool"]))
		toolCallID := strings.TrimSpace(meta["tool_call_id"])
		output := strings.TrimSpace(part.Body)
		diff := toolRunDiffBody(parts)
		status := ui.ToolRunStatusCompleted
		if strings.Contains(strings.ToLower(output), "denied") {
			status = ui.ToolRunStatusDenied
		}
		if strings.HasPrefix(output, "Error:") {
			status = ui.ToolRunStatusFailed
		}
		preview := firstNonEmptyString(toolPreviewFromMeta(tool, meta), strings.TrimSpace(msg.Summary))
		title, subtitle := summarizeToolSummary(tool, preview)
		if title == "" {
			title, subtitle = summarizeToolSummary(tool, output)
		}
		return ui.ToolRun{
			ID:         firstNonEmptyString(toolCallID, toolRunFallbackID(tool, preview), msg.Summary),
			Tool:       tool,
			ToolCallID: toolCallID,
			Title:      title,
			Subtitle:   subtitle,
			Preview:    preview,
			Output:     output,
			Diff:       diff,
			Status:     status,
		}, true
	}
	return ui.ToolRun{}, false
}

func findToolRun(run ui.ToolRun, openRuns []*ui.ToolRun, byToolCallID map[string]*ui.ToolRun, byApprovalID map[int64]*ui.ToolRun) *ui.ToolRun {
	if run.ToolCallID != "" {
		if existing := byToolCallID[run.ToolCallID]; existing != nil {
			return existing
		}
	}
	if run.ApprovalID > 0 {
		if existing := byApprovalID[run.ApprovalID]; existing != nil {
			return existing
		}
	}
	for i := len(openRuns) - 1; i >= 0; i-- {
		existing := openRuns[i]
		if existing == nil || existing.Status == ui.ToolRunStatusCompleted || existing.Status == ui.ToolRunStatusDenied || existing.Status == ui.ToolRunStatusFailed {
			continue
		}
		if run.Tool != "" && existing.Tool != run.Tool {
			continue
		}
		if previewsMatch(existing.Preview, run.Preview) || previewsMatch(existing.Subtitle, run.Subtitle) {
			return existing
		}
	}
	for i := len(openRuns) - 1; i >= 0; i-- {
		existing := openRuns[i]
		if existing != nil && run.Tool != "" && existing.Tool == run.Tool {
			return existing
		}
	}
	return nil
}

func mergeToolRun(dst *ui.ToolRun, src ui.ToolRun) {
	if dst == nil {
		return
	}
	if strings.TrimSpace(dst.ID) == "" {
		dst.ID = src.ID
	}
	if strings.TrimSpace(dst.ToolCallID) == "" {
		dst.ToolCallID = src.ToolCallID
	}
	if dst.ApprovalID == 0 {
		dst.ApprovalID = src.ApprovalID
	}
	if strings.TrimSpace(dst.Title) == "" {
		dst.Title = src.Title
	}
	if strings.TrimSpace(dst.Subtitle) == "" {
		dst.Subtitle = src.Subtitle
	}
	if strings.TrimSpace(dst.Preview) == "" {
		dst.Preview = src.Preview
	}
	if strings.TrimSpace(dst.Output) == "" {
		dst.Output = src.Output
	}
	if strings.TrimSpace(dst.Diff) == "" {
		dst.Diff = src.Diff
	}
	if strings.TrimSpace(dst.ErrorText) == "" {
		dst.ErrorText = src.ErrorText
	}
	if src.Status != "" {
		dst.Status = src.Status
	}
}

func (m *Model) renderTranscriptBlock(block transcriptBlock) string {
	switch block.Kind {
	case transcriptBlockToolRun:
		return ui.RenderToolRunCard(block.ToolRun, m.palette, m.viewport.Width)
	default:
		return m.renderTranscriptMessage(block.Message)
	}
}

func (m *Model) approvalToolRun(item store.Approval) ui.ToolRun {
	run := ui.ToolRun{
		ID:         approvalFallbackID(item.ID, item.Tool, item.Command),
		Tool:       item.Tool,
		ApprovalID: item.ID,
		Title:      toolTitle(item.Tool),
		Subtitle:   summarizePreview(item.Tool, item.Command),
		Preview:    strings.TrimSpace(item.Command),
		Status:     ui.ToolRunStatusPendingApproval,
	}
	if req, err := approvalRequestFromStored(item.Tool, item.Command); err == nil {
		if toolCallID := strings.TrimSpace(req.Args["tool_call_id"]); toolCallID != "" {
			run.ToolCallID = toolCallID
		}
		run.Preview = firstNonEmptyString(strings.TrimSpace(approvalRequestPreview(req)), run.Preview)
		run.Subtitle = summarizePreview(item.Tool, run.Preview)
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

func decodeToolCallView(raw string) (toolCallView, bool) {
	if strings.TrimSpace(raw) == "" {
		return toolCallView{}, false
	}
	var call toolCallView
	if err := json.Unmarshal([]byte(raw), &call); err != nil || call.Tool == "" {
		return toolCallView{}, false
	}
	return call, true
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

func summarizeToolCall(tool domain.ToolKind, call toolCallView) (string, string) {
	switch tool {
	case domain.ToolKindBash:
		return "Run command", summarizePreview(tool, call.Command)
	case domain.ToolKindRead:
		return "Read file", summarizePreview(tool, call.Path)
	case domain.ToolKindGlob:
		return "Find files", summarizePreview(tool, call.Pattern)
	case domain.ToolKindGrep:
		return "Search text", summarizePreview(tool, call.Pattern)
	case domain.ToolKindApplyPatch:
		return "Apply patch", summarizePreview(tool, call.Path)
	case domain.ToolKindTask:
		return "Create task", summarizePreview(tool, call.Body)
	case domain.ToolKindQuestion:
		return "Ask question", summarizePreview(tool, call.Body)
	case domain.ToolKindWebFetch:
		return "Fetch URL", summarizePreview(tool, call.URL)
	case domain.ToolKindWebSearch:
		return "Search web", summarizePreview(tool, call.Body)
	default:
		return toolTitle(tool), summarizePreview(tool, toolRunPreview(tool, call))
	}
}

func summarizeToolSummary(tool domain.ToolKind, preview string) (string, string) {
	return toolTitle(tool), summarizePreview(tool, preview)
}

func toolRunPreview(tool domain.ToolKind, call toolCallView) string {
	switch tool {
	case domain.ToolKindRead:
		return call.Path
	case domain.ToolKindGlob, domain.ToolKindGrep:
		return call.Pattern
	case domain.ToolKindBash:
		return call.Command
	case domain.ToolKindApplyPatch:
		return firstNonEmptyString(call.Path, call.Content)
	case domain.ToolKindTask, domain.ToolKindQuestion, domain.ToolKindWebSearch:
		return call.Body
	case domain.ToolKindWebFetch:
		return call.URL
	default:
		return ""
	}
}

func summarizePreview(tool domain.ToolKind, preview string) string {
	preview = strings.TrimSpace(preview)
	if preview == "" {
		return ""
	}
	switch tool {
	case domain.ToolKindBash:
		return preview
	case domain.ToolKindRead, domain.ToolKindApplyPatch:
		return preview
	case domain.ToolKindGlob:
		return "Pattern: " + preview
	case domain.ToolKindGrep:
		return "Query: " + preview
	case domain.ToolKindWebFetch:
		return preview
	case domain.ToolKindWebSearch:
		return "Query: " + preview
	default:
		return preview
	}
}

func toolTitle(tool domain.ToolKind) string {
	switch tool {
	case domain.ToolKindBash:
		return "Run command"
	case domain.ToolKindRead:
		return "Read file"
	case domain.ToolKindGlob:
		return "Find files"
	case domain.ToolKindGrep:
		return "Search text"
	case domain.ToolKindApplyPatch:
		return "Apply patch"
	case domain.ToolKindTask:
		return "Create task"
	case domain.ToolKindQuestion:
		return "Ask question"
	case domain.ToolKindWebFetch:
		return "Fetch URL"
	case domain.ToolKindWebSearch:
		return "Search web"
	default:
		if tool == "" {
			return "Tool"
		}
		return strings.ToUpper(string(tool[:1])) + string(tool[1:])
	}
}

func previewsMatch(left, right string) bool {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == "" || right == "" {
		return false
	}
	return left == right
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

func toolPreviewFromMeta(tool domain.ToolKind, meta map[string]string) string {
	switch tool {
	case domain.ToolKindRead:
		return strings.TrimSpace(meta["path"])
	case domain.ToolKindGlob, domain.ToolKindGrep:
		return strings.TrimSpace(meta["pattern"])
	case domain.ToolKindBash:
		return strings.TrimSpace(meta["command"])
	case domain.ToolKindApplyPatch:
		return firstNonEmptyString(strings.TrimSpace(meta["path"]), strings.TrimSpace(meta["content"]))
	case domain.ToolKindTask:
		return strings.TrimSpace(meta["body"])
	case domain.ToolKindQuestion:
		return strings.TrimSpace(meta["question"])
	case domain.ToolKindWebFetch:
		return strings.TrimSpace(meta["url"])
	case domain.ToolKindWebSearch:
		return strings.TrimSpace(meta["query"])
	default:
		return ""
	}
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

func approvalRequestFromStored(tool domain.ToolKind, raw string) (tools.Request, error) {
	var args map[string]string
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		args = legacyApprovalArgs(tool, raw)
	}
	return tools.Request{Tool: tool, Args: args}, nil
}

func legacyApprovalArgs(tool domain.ToolKind, raw string) map[string]string {
	switch tool {
	case domain.ToolKindRead:
		return map[string]string{"path": raw}
	case domain.ToolKindGlob, domain.ToolKindGrep:
		return map[string]string{"pattern": raw}
	case domain.ToolKindBash:
		return map[string]string{"command": raw}
	case domain.ToolKindWebFetch:
		return map[string]string{"url": raw}
	case domain.ToolKindTask:
		return map[string]string{"body": raw}
	default:
		return map[string]string{"command": raw}
	}
}

func approvalRequestPreview(req tools.Request) string {
	switch req.Tool {
	case domain.ToolKindRead:
		return req.Args["path"]
	case domain.ToolKindGlob, domain.ToolKindGrep:
		return req.Args["pattern"]
	case domain.ToolKindBash:
		return req.Args["command"]
	case domain.ToolKindApplyPatch:
		return req.Args["path"]
	case domain.ToolKindTask:
		return req.Args["body"]
	case domain.ToolKindWebFetch:
		return req.Args["url"]
	default:
		return string(req.Tool)
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
