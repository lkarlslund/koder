package tui

import (
	"encoding/json"
	"fmt"
	"hash"
	"hash/fnv"
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
		req, err := tools.RequestFromMeta(part.MetaJSON)
		if err != nil {
			continue
		}
		presentation := tools.PresentationForRequest(req)
		runs = append(runs, ui.ToolRun{
			ID:         firstNonEmptyString(strings.TrimSpace(req.ToolCallID), toolRunFallbackID(req.Tool, presentation.Preview)),
			Tool:       req.Tool,
			ToolCallID: strings.TrimSpace(req.ToolCallID),
			Title:      presentation.Title,
			Subtitle:   presentation.Subtitle,
			Preview:    presentation.Preview,
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
		presentation := presentationFromPreview(tool, preview)
		return ui.ToolRun{
			ID:         approvalFallbackID(approvalID, tool, preview),
			Tool:       tool,
			ApprovalID: approvalID,
			Title:      presentation.Title,
			Subtitle:   presentation.Subtitle,
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
		presentation := presentationFromPreview(tool, preview)
		status := ui.ToolRunStatusApproved
		output := ""
		if strings.EqualFold(strings.TrimSpace(meta["status"]), "denied") {
			status = ui.ToolRunStatusDenied
			output = strings.TrimSpace(part.Body)
		}
		return ui.ToolRun{
			ID:         approvalFallbackID(approvalID, tool, preview),
			Tool:       tool,
			ApprovalID: approvalID,
			Title:      presentation.Title,
			Subtitle:   presentation.Subtitle,
			Preview:    preview,
			Output:     output,
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
		if strings.TrimSpace(presentation.Subtitle) == "" {
			presentation.Subtitle = presentation.Preview
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
			string(block.ToolRun.Tool),
			block.ToolRun.ID,
			block.ToolRun.ToolCallID,
			strconv.FormatInt(block.ToolRun.ApprovalID, 10),
			block.ToolRun.Title,
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
			Run:      block.ToolRun,
			Palette:  m.palette,
			Width:    m.viewport.Width,
			Expanded: m.expandedToolRuns[block.ToolRun.ID],
		}
	default:
		return m.renderTranscriptMessageElement(block.Message)
	}
}

type toolRunCardElement struct {
	Run      ui.ToolRun
	Palette  theme.Palette
	Width    int
	Expanded bool
}

func (e toolRunCardElement) Measure(_ *ui.Context, constraints ui.Constraints) ui.Size {
	width := e.Width
	if width <= 0 {
		width = constraints.MaxW
	}
	return constraints.Clamp(e.Run.CardSurface(e.Palette, width, e.Expanded).Size())
}

func (e toolRunCardElement) Render(ctx *ui.Context, bounds ui.Rect) ui.Surface {
	width := e.Width
	if width <= 0 {
		width = bounds.W
	}
	if ctx != nil && ctx.Runtime != nil && strings.TrimSpace(e.Run.ID) != "" && e.Run.Expandable(width) {
		ctx.Runtime.Register(ui.Control{
			ID:      "toolrun:" + e.Run.ID,
			Rect:    ui.Rect{X: bounds.X, Y: bounds.Y, W: max(1, width), H: max(1, bounds.H)},
			Enabled: true,
		})
	}
	return e.Run.CardSurface(e.Palette, width, e.Expanded)
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
