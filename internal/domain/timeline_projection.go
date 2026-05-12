package domain

import (
	"slices"
	"strings"
	"time"
)

// LegacyMessageFromTimeline projects a timeline item into the old message view.
func LegacyMessageFromTimeline(sessionID int64, item TimelineItem) Message {
	return Message{
		ID:        item.ID,
		SessionID: sessionID,
		ChatID:    item.ChatID,
		Role:      LegacyTimelineRole(item),
		Summary:   LegacyTimelineSummary(item),
		CreatedAt: item.CreatedAt,
	}
}

// LegacyTranscriptFromTimeline projects timeline items into old message and part views.
func LegacyTranscriptFromTimeline(sessionID int64, items []TimelineItem) ([]Message, map[int64][]Part) {
	messages := make([]Message, 0, len(items))
	parts := make(map[int64][]Part, len(items))
	for _, item := range items {
		msg := LegacyMessageFromTimeline(sessionID, item)
		messages = append(messages, msg)
		parts[msg.ID] = LegacyPartsFromTimeline(item)
	}
	return messages, parts
}

// LegacyTimelineRole returns the old message role represented by a timeline item.
func LegacyTimelineRole(item TimelineItem) MessageRole {
	switch payload := item.Content.(type) {
	case LegacyMessage:
		return payload.Role
	case UserMessage:
		return MessageRoleUser
	case Notice, Compaction:
		return MessageRoleTool
	default:
		return MessageRoleAssistant
	}
}

// LegacyTimelineSummary returns the old summary represented by a timeline item.
func LegacyTimelineSummary(item TimelineItem) string {
	switch payload := item.Content.(type) {
	case LegacyMessage:
		return payload.Summary
	case UserMessage:
		return payload.Text
	case AssistantMessage:
		return payload.Text
	case Notice:
		return payload.Text
	case Compaction:
		return payload.Summary
	default:
		return ""
	}
}

// LegacyPartsFromTimeline projects typed timeline content into old message parts.
func LegacyPartsFromTimeline(item TimelineItem) []Part {
	var parts []Part
	add := func(kind PartKind, payload PartPayload, offset int64) {
		part := Part{ID: item.ID*1000 + offset, MessageID: item.ID, Kind: kind, Payload: payload, CreatedAt: item.CreatedAt}
		part.Body = part.Text()
		part.MetaJSON = part.LegacyMetaJSON()
		parts = append(parts, part)
	}
	switch payload := item.Content.(type) {
	case LegacyMessage:
		return normalizeLegacyParts(item, payload.Parts)
	case UserMessage:
		if strings.TrimSpace(payload.Text) != "" {
			add(PartKindText, TextPayload{Text: payload.Text}, 1)
		}
		for idx, attachment := range payload.Attachments {
			add(PartKindAttachment, AttachmentPayload(attachment), int64(2+idx))
		}
		for idx, ref := range payload.References {
			add(PartKindReference, ReferencePayload(ref), int64(2+len(payload.Attachments)+idx))
		}
	case AssistantMessage:
		offset := int64(1)
		if strings.TrimSpace(payload.Reasoning.Text) != "" {
			add(PartKindReasoning, ReasoningPayload{Text: payload.Reasoning.Text}, offset)
			offset++
		}
		if strings.TrimSpace(payload.Text) != "" {
			add(PartKindText, TextPayload{Text: payload.Text}, offset)
			offset++
		}
		for _, tool := range payload.Tools {
			add(PartKindToolCall, ToolCallPayload{Tool: tool.Tool, ToolCallID: string(tool.ToolCallID), Args: tool.Args}, offset)
			offset++
			if tool.Result != nil {
				add(PartKindToolOutput, ToolOutputPayload{Tool: tool.Tool, ToolCallID: string(tool.ToolCallID), Args: tool.Args, Status: tool.Result.Status, Text: tool.Result.Text, Diff: tool.Result.Diff}, offset)
				offset++
			}
			if tool.Error != nil {
				add(PartKindToolOutput, ToolOutputPayload{Tool: tool.Tool, ToolCallID: string(tool.ToolCallID), Args: tool.Args, Status: ToolResultStatusError, Text: tool.Error.Message}, offset)
				offset++
			}
		}
		if payload.Usage != nil {
			add(PartKindUsage, UsagePayload{Usage: *payload.Usage}, offset)
		}
	case Notice:
		add(PartKindEventNotice, EventNoticePayload{Text: payload.Text, Kind: payload.Kind, Severity: payload.Level, Tool: payload.Tool, ToolCallID: payload.ToolCallID}, 1)
	case Compaction:
		add(PartKindCompaction, CompactionPayload{Summary: payload.Summary, Trigger: payload.Trigger, Status: payload.Status, BeforeContextTokens: payload.BeforeContextTokens, AfterContextTokens: payload.AfterContextTokens}, 1)
	}
	return parts
}

func normalizeLegacyParts(item TimelineItem, input []Part) []Part {
	parts := slices.Clone(input)
	for idx := range parts {
		normalizeLegacyPart(item, &parts[idx])
	}
	return parts
}

func normalizeLegacyPart(item TimelineItem, part *Part) {
	if part == nil {
		return
	}
	part.MessageID = item.ID
	if part.Kind == "" && part.Payload != nil {
		part.Kind = part.Payload.PartKind()
	}
	if part.CreatedAt.IsZero() {
		part.CreatedAt = firstNonZeroTime(part.CreatedAt, item.CreatedAt)
	}
	if part.Body == "" {
		part.Body = part.Text()
	}
	if part.MetaJSON == "" {
		part.MetaJSON = part.LegacyMetaJSON()
	}
}

func firstNonZeroTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}
