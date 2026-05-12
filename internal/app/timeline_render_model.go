package app

import (
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/domain"
)

func renderTranscriptFromTimeline(sessionID int64, items []domain.TimelineItem) ([]domain.Message, map[int64][]domain.Part) {
	messages := make([]domain.Message, 0, len(items))
	parts := make(map[int64][]domain.Part, len(items))
	for _, item := range items {
		msg, itemParts := renderTimelineItem(sessionID, item)
		if msg.ID == 0 {
			continue
		}
		messages = append(messages, msg)
		parts[msg.ID] = itemParts
	}
	return messages, parts
}

func renderTimelineItem(sessionID int64, item domain.TimelineItem) (domain.Message, []domain.Part) {
	msg := domain.Message{
		ID:        item.ID,
		SessionID: sessionID,
		ChatID:    item.ChatID,
		Role:      renderTimelineRole(item),
		Summary:   renderTimelineSummary(item),
		CreatedAt: item.CreatedAt,
	}
	return msg, renderTimelineParts(item)
}

func renderTimelineRole(item domain.TimelineItem) domain.MessageRole {
	switch item.Content.(type) {
	case domain.UserMessage:
		return domain.MessageRoleUser
	case domain.ToolExecution, domain.Notice, domain.Compaction:
		return domain.MessageRoleTool
	default:
		return domain.MessageRoleAssistant
	}
}

func renderTimelineSummary(item domain.TimelineItem) string {
	switch payload := item.Content.(type) {
	case domain.UserMessage:
		return payload.Text
	case domain.AssistantMessage:
		return payload.Text
	case domain.Notice:
		return payload.Text
	case domain.ToolExecution:
		return string(payload.Tool)
	case domain.Compaction:
		return payload.Summary
	default:
		return ""
	}
}

func renderTimelineParts(item domain.TimelineItem) []domain.Part {
	var parts []domain.Part
	add := func(kind domain.PartKind, payload domain.PartPayload, offset int64) {
		part := domain.Part{ID: item.ID*1000 + offset, MessageID: item.ID, Kind: kind, Payload: payload, CreatedAt: item.CreatedAt}
		normalizeRenderPart(item, &part)
		parts = append(parts, part)
	}
	switch payload := item.Content.(type) {
	case domain.UserMessage:
		if strings.TrimSpace(payload.Text) != "" {
			add(domain.PartKindText, domain.TextPayload{Text: payload.Text}, 1)
		}
		for idx, attachment := range payload.Attachments {
			add(domain.PartKindAttachment, domain.AttachmentPayload(attachment), int64(2+idx))
		}
		for idx, ref := range payload.References {
			add(domain.PartKindReference, domain.ReferencePayload(ref), int64(2+len(payload.Attachments)+idx))
		}
	case domain.AssistantMessage:
		offset := int64(1)
		if strings.TrimSpace(payload.Reasoning.Text) != "" {
			add(domain.PartKindReasoning, domain.ReasoningPayload{Text: payload.Reasoning.Text}, offset)
			offset++
		}
		if strings.TrimSpace(payload.Text) != "" {
			add(domain.PartKindText, domain.TextPayload{Text: payload.Text}, offset)
			offset++
		}
		for _, tool := range payload.Tools {
			add(domain.PartKindToolCall, domain.ToolCallPayload{Tool: tool.Tool, ToolCallID: string(tool.ToolCallID), Args: tool.Args}, offset)
			offset++
			if tool.Approval != nil {
				add(domain.PartKindApprovalRequest, domain.ApprovalRequestPayload{ApprovalID: tool.Approval.ID, Tool: tool.Tool, ToolCallID: string(tool.ToolCallID), Command: tool.Args["command"], Status: tool.Approval.Status, Body: tool.Approval.Body}, offset)
				offset++
			}
			if tool.Result != nil {
				add(domain.PartKindToolOutput, domain.ToolOutputPayload{Tool: tool.Tool, ToolCallID: string(tool.ToolCallID), Args: tool.Args, Status: tool.Result.Status, Text: tool.Result.Text, Diff: tool.Result.Diff, Result: tool.Result.Data}, offset)
				offset++
			}
			if tool.Error != nil {
				add(domain.PartKindToolOutput, domain.ToolOutputPayload{Tool: tool.Tool, ToolCallID: string(tool.ToolCallID), Args: tool.Args, Status: domain.ToolResultStatusError, Text: tool.Error.Message, Result: domain.ErrorStoredResult{Message: tool.Error.Message}}, offset)
				offset++
			}
		}
		if payload.Usage != nil {
			add(domain.PartKindUsage, domain.UsagePayload{Usage: *payload.Usage}, offset)
		}
	case domain.Notice:
		switch payload.Kind {
		case "approval_request":
			add(domain.PartKindApprovalRequest, domain.ApprovalRequestPayload{Tool: payload.Tool, ToolCallID: payload.ToolCallID, Body: payload.Text}, 1)
		case "system_notice":
			add(domain.PartKindSystemNotice, domain.SystemNoticePayload{Text: payload.Text}, 1)
		default:
			add(domain.PartKindEventNotice, domain.EventNoticePayload{
				Text: payload.Text, Kind: payload.Kind, Severity: payload.Level, Reason: payload.Reason,
				Title: payload.Title, Subtitle: payload.Subtitle, Tool: payload.Tool, ToolCallID: payload.ToolCallID,
				Count: payload.Count, Limit: payload.Limit,
			}, 1)
		}
	case domain.ToolExecution:
		if payload.Result != nil {
			add(domain.PartKindToolOutput, domain.ToolOutputPayload{Tool: payload.Tool, Args: payload.Args, Status: payload.Result.Status, Text: payload.Result.Text, Diff: payload.Result.Diff, Result: payload.Result.Data}, 1)
		}
		if payload.Error != nil {
			add(domain.PartKindToolOutput, domain.ToolOutputPayload{Tool: payload.Tool, Args: payload.Args, Status: domain.ToolResultStatusError, Text: payload.Error.Message, Result: domain.ErrorStoredResult{Message: payload.Error.Message}}, 1)
		}
	case domain.Compaction:
		add(domain.PartKindCompaction, domain.CompactionPayload{Summary: payload.Summary, Trigger: payload.Trigger, Status: payload.Status, BeforeContextTokens: payload.BeforeContextTokens, AfterContextTokens: payload.AfterContextTokens}, 1)
	}
	return parts
}

func normalizeRenderPart(item domain.TimelineItem, part *domain.Part) {
	if part == nil {
		return
	}
	part.MessageID = item.ID
	if part.Kind == "" && part.Payload != nil {
		part.Kind = part.Payload.PartKind()
	}
	if part.CreatedAt.IsZero() {
		part.CreatedAt = firstNonZeroRenderTime(part.CreatedAt, item.CreatedAt)
	}
	if part.Body == "" {
		part.Body = part.Text()
	}
	if part.MetaJSON == "" && part.Kind == domain.PartKindEventNotice {
		part.MetaJSON = part.LegacyMetaJSON()
	}
}

func firstNonZeroRenderTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}
