package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/attachment"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/markdown"
	"github.com/lkarlslund/koder/internal/theme"
	"github.com/lkarlslund/koder/internal/ui"
)

const reasoningOnlyPlaceholder = "<no text from LLM, only reasoning>"

type transcriptRenderer struct {
	palette              theme.Palette
	width                int
	renderer             *markdown.Renderer
	showReasoning        bool
	showSystem           bool
	showTimestamps       bool
	halfBlocks           bool
	promptGlyph          string
	pendingReasoningLine string
}

func newTranscriptRenderer(m *Model) transcriptRenderer {
	return transcriptRenderer{
		palette:        m.palette,
		width:          m.viewport.Width,
		renderer:       m.renderer,
		showReasoning:  m.showReasoning,
		showSystem:     m.showSystem,
		showTimestamps: m.cfg.UI.ShowTimestamps,
		halfBlocks:     m.halfBlocksEnabled(),
		promptGlyph:    m.promptGlyph(),
	}
}

func (r transcriptRenderer) renderTranscriptMessageElement(msg domain.Message, parts []domain.Part) ui.Node {
	body := r.renderMessageParts(parts)
	styledBody := r.renderStyledMessageParts(parts)
	stamp := timestamp(msg.CreatedAt, r.showTimestamps)
	switch msg.Role {
	case domain.MessageRoleUser:
		userBody := r.renderUserMessageParts(parts)
		if strings.TrimSpace(userBody) == "" {
			userBody = strings.TrimSpace(msg.Summary)
		}
		if strings.TrimSpace(userBody) == "" {
			return nil
		}
		return r.renderUserMessageElement(userBody, stamp)
	default:
		if strings.TrimSpace(body) == "" {
			body = strings.TrimSpace(msg.Summary)
		}
		if isSyntheticToolSummary(body) {
			body = ""
		}
		if len(styledBody) == 0 && strings.TrimSpace(body) == "" {
			return nil
		}
		if len(styledBody) == 0 && body != "" {
			styledBody = []ui.StyledSpan{{Text: body}}
		}
		return r.renderStyledAssistantMessageElement(styledBody, stamp)
	}
}

func (r transcriptRenderer) renderUserMessage(body, stamp string) string {
	element := r.renderUserMessageElement(body, stamp)
	ctx := &ui.Context{Palette: r.palette}
	return strings.Join(ui.RenderSurface(ctx, element, r.userMessageWidth(body, stamp), 0).Lines(), "\n")
}

func (r transcriptRenderer) renderUserMessageElement(body, stamp string) ui.Node {
	return ui.AsNode(ui.NewUserMessage(ui.UserMessageProps{
		Palette:     r.palette,
		Body:        body,
		Stamp:       stamp,
		Width:       r.userMessageWidth(body, stamp),
		HalfBlocks:  r.halfBlocks,
		PromptGlyph: r.promptGlyph,
	}))
}

func (r transcriptRenderer) userMessageWidth(body, stamp string) int {
	if r.width > 0 {
		return r.width
	}
	lines := []string{""}
	if strings.TrimSpace(body) != "" {
		lines = append(lines, strings.Split(strings.TrimSpace(body), "\n")...)
	}
	if stamp != "" {
		lines = append(lines, stamp)
	}
	lines = append(lines, "")
	return ui.UserMessageWidth(lines)
}

func (r transcriptRenderer) renderStyledAssistantMessageElement(body []ui.StyledSpan, stamp string) ui.Node {
	return ui.AsNode(ui.AssistantMessage{
		StyledBody: body,
		BaseStyle:  ui.CellStyle{FG: r.palette.MarkdownText},
		Stamp:      stamp,
		Width:      r.width,
		Palette:    r.palette,
	})
}

func (r transcriptRenderer) attachmentLabel(meta attachment.Metadata) string {
	return attachmentLabel(meta)
}

func attachmentLabel(meta attachment.Metadata) string {
	switch attachment.ClassifyMIME(meta.MIME) {
	case attachment.KindImage:
		return "[Image] " + meta.Name
	case attachment.KindPDF:
		return "[PDF] " + meta.Name
	case attachment.KindText:
		return "[Text] " + meta.Name
	default:
		return "[File] " + meta.Name
	}
}

func (r transcriptRenderer) renderMessageParts(parts []domain.Part) string {
	var blocks []string
	var reasoningBlocks []string
	var systemBlocks []string
	var textBlocks []string
	var textBuf strings.Builder
	var reasoningBuf strings.Builder

	flushText := func() {
		if textBuf.Len() == 0 {
			return
		}
		textBlocks = append(textBlocks, r.renderer.RenderPlainWidth(textBuf.String(), r.markdownRenderWidth()))
		textBuf.Reset()
	}
	flushReasoning := func() {
		if reasoningBuf.Len() == 0 {
			return
		}
		reasoningBlocks = append(reasoningBlocks, r.renderReasoningBlock(reasoningBuf.String()))
		reasoningBuf.Reset()
	}

	for _, part := range parts {
		switch part.Kind {
		case domain.PartKindText:
			flushReasoning()
			textBuf.WriteString(part.Body)
		case domain.PartKindReasoning:
			flushText()
			reasoningBuf.WriteString(part.Body)
		case domain.PartKindCompaction:
			flushText()
			flushReasoning()
			continue
		case domain.PartKindSystemNotice:
			flushText()
			flushReasoning()
			if r.showSystem {
				if block := r.renderSystemNoticeBlock(part); block != "" {
					systemBlocks = append(systemBlocks, block)
				}
			}
			continue
		case domain.PartKindEventNotice:
			flushText()
			flushReasoning()
			if eventNoticeToolRun(part).ID != "" {
				continue
			}
			if block := r.renderEventNoticeBlock(part); block != "" {
				systemBlocks = append(systemBlocks, block)
			}
			continue
		case domain.PartKindToolCall, domain.PartKindToolOutput, domain.PartKindDiff, domain.PartKindApprovalRequest:
			flushText()
			flushReasoning()
			continue
		case domain.PartKindAttachment:
			flushText()
			flushReasoning()
			meta, err := attachment.DecodeMeta(part.MetaJSON)
			if err != nil {
				if body := strings.TrimSpace(part.Body); body != "" {
					blocks = append(blocks, body)
				}
				continue
			}
			blocks = append(blocks, r.attachmentLabel(meta))
		case domain.PartKindReference:
			flushText()
			flushReasoning()
			continue
		default:
			flushText()
			flushReasoning()
			blocks = append(blocks, part.Body)
		}
	}

	flushText()
	flushReasoning()

	blocks = append(blocks, systemBlocks...)
	visibleReasoning := reasoningBlocks
	if !r.showReasoning && len(textBlocks) > 0 {
		visibleReasoning = nil
	}
	if len(textBlocks) == 0 && len(reasoningBlocks) > 0 {
		if strings.TrimSpace(r.pendingReasoningLine) != "" {
			textBlocks = append(textBlocks, strings.TrimSpace(r.pendingReasoningLine))
		} else {
			textBlocks = append(textBlocks, reasoningOnlyPlaceholder)
		}
	}
	blocks = append(blocks, visibleReasoning...)
	if len(visibleReasoning) > 0 && len(textBlocks) > 0 {
		blocks = append(blocks, "")
	}
	blocks = append(blocks, textBlocks...)

	return strings.TrimSpace(strings.Join(blocks, "\n"))
}

func (r transcriptRenderer) renderStyledMessageParts(parts []domain.Part) []ui.StyledSpan {
	var blocks [][]ui.StyledSpan
	var reasoningBlocks [][]ui.StyledSpan
	var systemBlocks [][]ui.StyledSpan
	var textBlocks [][]ui.StyledSpan
	var textBuf strings.Builder
	var reasoningBuf strings.Builder

	flushText := func() {
		if textBuf.Len() == 0 {
			return
		}
		block := r.renderer.RenderStyledWidth(textBuf.String(), r.markdownRenderWidth())
		if len(block) > 0 {
			textBlocks = append(textBlocks, block)
		}
		textBuf.Reset()
	}
	flushReasoning := func() {
		if reasoningBuf.Len() == 0 {
			return
		}
		if block := r.renderStyledReasoningBlock(reasoningBuf.String()); len(block) > 0 {
			reasoningBlocks = append(reasoningBlocks, block)
		}
		reasoningBuf.Reset()
	}

	for _, part := range parts {
		switch part.Kind {
		case domain.PartKindText:
			flushReasoning()
			textBuf.WriteString(part.Body)
		case domain.PartKindReasoning:
			flushText()
			reasoningBuf.WriteString(part.Body)
		case domain.PartKindCompaction:
			flushText()
			flushReasoning()
			continue
		case domain.PartKindSystemNotice:
			flushText()
			flushReasoning()
			if r.showSystem {
				if block := r.renderStyledSystemNoticeBlock(part); len(block) > 0 {
					systemBlocks = append(systemBlocks, block)
				}
			}
		case domain.PartKindEventNotice:
			flushText()
			flushReasoning()
			if eventNoticeToolRun(part).ID != "" {
				continue
			}
			if block := r.renderStyledEventNoticeBlock(part); len(block) > 0 {
				systemBlocks = append(systemBlocks, block)
			}
		case domain.PartKindToolCall, domain.PartKindToolOutput, domain.PartKindDiff, domain.PartKindApprovalRequest, domain.PartKindReference:
			flushText()
			flushReasoning()
		case domain.PartKindAttachment:
			flushText()
			flushReasoning()
			meta, err := attachment.DecodeMeta(part.MetaJSON)
			if err != nil {
				if body := strings.TrimSpace(part.Body); body != "" {
					blocks = append(blocks, []ui.StyledSpan{{Text: body}})
				}
				continue
			}
			blocks = append(blocks, []ui.StyledSpan{{Text: r.attachmentLabel(meta)}})
		default:
			flushText()
			flushReasoning()
			if body := strings.TrimSpace(part.Body); body != "" {
				blocks = append(blocks, []ui.StyledSpan{{Text: body}})
			}
		}
	}

	flushText()
	flushReasoning()
	blocks = append(blocks, systemBlocks...)
	visibleReasoning := reasoningBlocks
	if !r.showReasoning && len(textBlocks) > 0 {
		visibleReasoning = nil
	}
	if len(textBlocks) == 0 && len(reasoningBlocks) > 0 {
		placeholder := reasoningOnlyPlaceholder
		if strings.TrimSpace(r.pendingReasoningLine) != "" {
			placeholder = strings.TrimSpace(r.pendingReasoningLine)
		}
		textBlocks = append(textBlocks, []ui.StyledSpan{{
			Text:  placeholder,
			Style: ui.CellStyle{FG: r.palette.ReasoningText}.WithItalic(true),
		}})
	}
	blocks = append(blocks, visibleReasoning...)
	if len(visibleReasoning) > 0 && len(textBlocks) > 0 {
		blocks = append(blocks, nil)
	}
	blocks = append(blocks, textBlocks...)

	var out []ui.StyledSpan
	for idx, block := range blocks {
		if idx > 0 {
			out = ui.AppendStyledSpan(out, "\n", ui.CellStyle{})
		}
		out = append(out, block...)
	}
	return out
}

func (r transcriptRenderer) renderSystemNoticeBlock(part domain.Part) string {
	title := strings.TrimSpace(part.Body)
	if strings.EqualFold(title, "usage") {
		return ""
	}
	switch {
	case title == "" && strings.TrimSpace(part.MetaJSON) == "":
		return ""
	case title == "":
		title = "system notice"
	}
	var body strings.Builder
	body.WriteString("### System\n\n")
	body.WriteString(title)
	if meta := strings.TrimSpace(part.MetaJSON); meta != "" {
		body.WriteString("\n\n```json\n")
		body.WriteString(meta)
		body.WriteString("\n```")
	}
	return r.renderer.RenderPlainWidth(body.String(), r.markdownRenderWidth())
}

func (r transcriptRenderer) renderStyledSystemNoticeBlock(part domain.Part) []ui.StyledSpan {
	title := strings.TrimSpace(part.Body)
	if strings.EqualFold(title, "usage") {
		return nil
	}
	switch {
	case title == "" && strings.TrimSpace(part.MetaJSON) == "":
		return nil
	case title == "":
		title = "system notice"
	}
	var body strings.Builder
	body.WriteString("### System\n\n")
	body.WriteString(title)
	if meta := strings.TrimSpace(part.MetaJSON); meta != "" {
		body.WriteString("\n\n```json\n")
		body.WriteString(meta)
		body.WriteString("\n```")
	}
	return r.renderer.RenderStyledWidth(body.String(), r.markdownRenderWidth())
}

type eventNoticeMeta struct {
	Kind     string `json:"kind,omitempty"`
	Severity string `json:"severity,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Title    string `json:"title,omitempty"`
	Subtitle string `json:"subtitle,omitempty"`
	Tool     string `json:"tool,omitempty"`
	Count    int    `json:"count,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

func (r transcriptRenderer) renderEventNoticeBlock(part domain.Part) string {
	title, body := eventNoticePresentation(part)
	if body == "" {
		return ""
	}
	var text strings.Builder
	text.WriteString("### ")
	text.WriteString(title)
	if body != title {
		text.WriteString("\n\n")
		text.WriteString(body)
	}
	return r.renderer.RenderPlainWidth(text.String(), r.markdownRenderWidth())
}

func (r transcriptRenderer) renderStyledEventNoticeBlock(part domain.Part) []ui.StyledSpan {
	title, body := eventNoticePresentation(part)
	if body == "" {
		return nil
	}
	var text strings.Builder
	text.WriteString("### ")
	text.WriteString(title)
	if body != title {
		text.WriteString("\n\n")
		text.WriteString(body)
	}
	return r.renderer.RenderStyledWidth(text.String(), r.markdownRenderWidth())
}

func (r transcriptRenderer) markdownRenderWidth() int {
	if r.width > 0 {
		return r.width
	}
	return 0
}

func eventNoticePresentation(part domain.Part) (string, string) {
	body := strings.TrimSpace(part.Body)
	if body == "" {
		return "", ""
	}
	var meta eventNoticeMeta
	_ = json.Unmarshal([]byte(part.MetaJSON), &meta)
	switch strings.TrimSpace(meta.Kind) {
	case "interrupted":
		return "Interrupted", body
	}
	switch strings.TrimSpace(meta.Severity) {
	case "error":
		return "Error", body
	case "warning":
		return "Interrupted", body
	default:
		return "Notice", body
	}
}

func (r transcriptRenderer) renderUserMessageParts(parts []domain.Part) string {
	var blocks []string
	var textBuf strings.Builder

	flushText := func() {
		if textBuf.Len() == 0 {
			return
		}
		blocks = append(blocks, strings.TrimSpace(textBuf.String()))
		textBuf.Reset()
	}

	for _, part := range parts {
		switch part.Kind {
		case domain.PartKindText:
			textBuf.WriteString(part.Body)
		case domain.PartKindAttachment:
			flushText()
			meta, err := attachment.DecodeMeta(part.MetaJSON)
			if err != nil {
				if body := strings.TrimSpace(part.Body); body != "" {
					blocks = append(blocks, body)
				}
				continue
			}
			blocks = append(blocks, r.attachmentLabel(meta))
		case domain.PartKindReference:
			continue
		default:
			flushText()
			if body := strings.TrimSpace(part.Body); body != "" {
				blocks = append(blocks, body)
			}
		}
	}

	flushText()

	return strings.TrimSpace(strings.Join(blocks, "\n"))
}

func (r transcriptRenderer) renderReasoningBlock(input string) string {
	element := r.renderReasoningBlockElement(input)
	ctx := &ui.Context{Palette: r.palette}
	width := max(0, r.width)
	trimLines := width <= 0
	if width <= 0 {
		width = element.Measure(ctx, ui.Constraints{}).W
	}
	rendered := strings.Join(ui.RenderSurface(ctx, element, width, 0).Lines(), "\n")
	if !trimLines {
		return rendered
	}
	lines := strings.Split(rendered, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	return strings.Join(lines, "\n")
}

func (r transcriptRenderer) renderReasoningBlockElement(input string) ui.Node {
	return ui.AsNode(ui.ReasoningBlock{
		Body:    input,
		Width:   r.width,
		Palette: r.palette,
	})
}

func (r transcriptRenderer) renderStyledReasoningBlock(input string) []ui.StyledSpan {
	rendered := r.renderReasoningBlock(input)
	if strings.TrimSpace(rendered) == "" {
		return nil
	}
	style := ui.CellStyle{
		BG: r.palette.ReasoningBackground,
		FG: r.palette.ReasoningText,
	}.WithItalic(true)
	lines := strings.Split(rendered, "\n")
	out := make([]ui.StyledSpan, 0, len(lines)*2)
	for idx, line := range lines {
		if idx > 0 {
			out = ui.AppendStyledSpan(out, "\n", ui.CellStyle{})
		}
		if line != "" {
			out = ui.AppendStyledSpan(out, line, style)
		}
	}
	return out
}

func formatSessionTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04")
}

func formatRelativeSessionTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	delta := time.Since(t)
	if delta < 0 {
		delta = 0
	}
	switch {
	case delta < time.Minute:
		return "now"
	case delta < time.Hour:
		return fmt.Sprintf("%dm ago", int(delta/time.Minute))
	case delta < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(delta/time.Hour))
	default:
		return fmt.Sprintf("%dd ago", int(delta/(24*time.Hour)))
	}
}
