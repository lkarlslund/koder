package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/domain"
)

// CountMessagesByRole counts timeline items projected as legacy messages.
func (s *Store) CountMessagesByRole(ctx context.Context, sessionID int64, role domain.MessageRole) (int, error) {
	chat, err := s.DefaultChat(ctx, sessionID)
	if err != nil {
		return 0, err
	}
	items, err := s.TimelineForChat(ctx, chat.ID)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, item := range items {
		if timelineRole(item) == role {
			count++
		}
	}
	return count, nil
}

// AddMessage appends a timeline item to the default chat and returns a legacy projection.
func (s *Store) AddMessage(ctx context.Context, sessionID int64, role domain.MessageRole, summary string) (domain.Message, error) {
	chat, err := s.DefaultChat(ctx, sessionID)
	if err != nil {
		return domain.Message{}, err
	}
	return s.AddChatMessage(ctx, chat.ID, role, summary)
}

// AddChatMessage appends a timeline item and returns a legacy projection.
func (s *Store) AddChatMessage(ctx context.Context, chatID int64, role domain.MessageRole, summary string) (domain.Message, error) {
	chat, err := s.GetChat(ctx, chatID)
	if err != nil {
		return domain.Message{}, err
	}
	items, err := s.TimelineForChat(ctx, chatID)
	if err != nil {
		return domain.Message{}, err
	}
	now := time.Now().UTC()
	item := domain.TimelineItem{
		ChatID:    chatID,
		Seq:       int64(len(items) + 1),
		Content:   timelineContentForRole(role, summary),
		CreatedAt: now,
		UpdatedAt: now,
	}
	if role == domain.MessageRoleUser || strings.TrimSpace(summary) != "" {
		item.SealedAt = now
	}
	item, err = s.InsertTimelineItem(ctx, item)
	if err != nil {
		return domain.Message{}, err
	}
	return legacyMessageFromTimeline(chat.SessionID, item), nil
}

// UpdateMessageSummary updates text on a timeline item projected as a legacy message.
func (s *Store) UpdateMessageSummary(ctx context.Context, messageID int64, summary string) error {
	item, err := s.Timeline().Get(ctx, messageID)
	if err != nil {
		return err
	}
	switch payload := item.Content.(type) {
	case domain.LegacyMessage:
		payload.Summary = summary
		item.Content = payload
	case domain.UserMessage:
		payload.Text = summary
		item.Content = payload
	case domain.AssistantMessage:
		payload.Text = summary
		item.Content = payload
	case domain.Notice:
		payload.Text = summary
		item.Content = payload
	case domain.Compaction:
		payload.Summary = summary
		item.Content = payload
	}
	item.UpdatedAt = time.Now().UTC()
	return s.PutTimelineItem(ctx, item)
}

// AddPart mutates a timeline item through the legacy part API.
func (s *Store) AddPart(ctx context.Context, messageID int64, payload domain.PartPayload) (domain.Part, error) {
	item, err := s.Timeline().Get(ctx, messageID)
	if err != nil {
		return domain.Part{}, err
	}
	offset := int64(len(legacyPartsFromTimeline(item)) + 1)
	part := domain.Part{
		ID:        messageID*1000 + offset,
		MessageID: messageID,
		Kind:      payload.PartKind(),
		Payload:   payload,
		Body:      domain.Part{Payload: payload}.Text(),
		MetaJSON:  domain.Part{Payload: payload}.LegacyMetaJSON(),
		CreatedAt: time.Now().UTC(),
	}
	if err := applyLegacyPartToTimeline(&item, part); err != nil {
		return domain.Part{}, err
	}
	if err := s.PutTimelineItem(ctx, item); err != nil {
		return domain.Part{}, err
	}
	return part, nil
}

func (s *Store) UpdatePartPayload(ctx context.Context, partID int64, payload domain.PartPayload) error {
	itemID := partID / 1000
	if itemID <= 0 {
		return fmt.Errorf("invalid legacy part id %d", partID)
	}
	item, err := s.Timeline().Get(ctx, itemID)
	if err != nil {
		return err
	}
	part := domain.Part{
		ID:        partID,
		MessageID: itemID,
		Kind:      payload.PartKind(),
		Payload:   payload,
		Body:      domain.Part{Payload: payload}.Text(),
		MetaJSON:  domain.Part{Payload: payload}.LegacyMetaJSON(),
		CreatedAt: time.Now().UTC(),
	}
	if err := applyLegacyPartToTimeline(&item, part); err != nil {
		return err
	}
	return s.PutTimelineItem(ctx, item)
}

// PartsForSession returns a legacy projection of default-chat timeline items.
func (s *Store) PartsForSession(ctx context.Context, sessionID int64) ([]domain.Message, map[int64][]domain.Part, error) {
	chat, err := s.DefaultChat(ctx, sessionID)
	if err != nil {
		return nil, nil, err
	}
	return s.PartsForChat(ctx, chat.ID)
}

// PartsForChat returns a legacy projection of timeline items.
func (s *Store) PartsForChat(ctx context.Context, chatID int64) ([]domain.Message, map[int64][]domain.Part, error) {
	chat, err := s.GetChat(ctx, chatID)
	if err != nil {
		return nil, nil, err
	}
	items, err := s.TimelineForChat(ctx, chatID)
	if err != nil {
		return nil, nil, err
	}
	messages := make([]domain.Message, 0, len(items))
	parts := make(map[int64][]domain.Part, len(items))
	for _, item := range items {
		msg := legacyMessageFromTimeline(chat.SessionID, item)
		messages = append(messages, msg)
		parts[msg.ID] = legacyPartsFromTimeline(item)
	}
	return messages, parts, nil
}

func timelineContentForRole(role domain.MessageRole, text string) domain.TimelineContent {
	return domain.LegacyMessage{Role: role, Summary: strings.TrimSpace(text)}
}

func legacyMessageFromTimeline(sessionID int64, item domain.TimelineItem) domain.Message {
	return domain.Message{
		ID:        item.ID,
		SessionID: sessionID,
		ChatID:    item.ChatID,
		Role:      timelineRole(item),
		Summary:   timelineSummary(item),
		CreatedAt: item.CreatedAt,
	}
}

func timelineRole(item domain.TimelineItem) domain.MessageRole {
	switch payload := item.Content.(type) {
	case domain.LegacyMessage:
		return payload.Role
	case domain.UserMessage:
		return domain.MessageRoleUser
	case domain.Notice, domain.Compaction:
		return domain.MessageRoleTool
	default:
		return domain.MessageRoleAssistant
	}
}

func timelineSummary(item domain.TimelineItem) string {
	switch payload := item.Content.(type) {
	case domain.LegacyMessage:
		return payload.Summary
	case domain.UserMessage:
		return payload.Text
	case domain.AssistantMessage:
		return payload.Text
	case domain.Notice:
		return payload.Text
	case domain.Compaction:
		return payload.Summary
	default:
		return ""
	}
}

func applyLegacyPartToTimeline(item *domain.TimelineItem, part domain.Part) error {
	if applyLegacyPartToLegacyMessage(item, part) {
		item.UpdatedAt = time.Now().UTC()
		return nil
	}
	switch payload := part.Payload.(type) {
	case domain.TextPayload:
		switch content := item.Content.(type) {
		case domain.UserMessage:
			content.Text = payload.Text
			item.Content = content
		case domain.AssistantMessage:
			content.Text = payload.Text
			item.Content = content
		default:
			item.Content = domain.AssistantMessage{Text: payload.Text}
		}
	case domain.ReasoningPayload:
		content, _ := item.Content.(domain.AssistantMessage)
		content.Reasoning.Text = payload.Text
		item.Content = content
	case domain.ToolCallPayload:
		content, _ := item.Content.(domain.AssistantMessage)
		if err := content.AddToolCall(domain.ToolCall{
			ToolCallID: domain.ToolCallID(payload.ToolCallID),
			Tool:       payload.Tool,
			Args:       payload.Args,
			Status:     domain.ToolStatusPending,
		}); err != nil {
			return err
		}
		item.Content = content
	case domain.ToolOutputPayload:
		content, _ := item.Content.(domain.AssistantMessage)
		result := domain.ToolResult{Text: payload.Text, Diff: payload.Diff, Status: payload.Status}
		if payload.Status == domain.ToolResultStatusError {
			err := content.SetToolError(domain.ToolCallID(payload.ToolCallID), domain.ToolError{Message: payload.Text})
			if err != nil {
				content.Tools = append(content.Tools, domain.ToolCall{ToolCallID: domain.ToolCallID(payload.ToolCallID), Tool: payload.Tool, Args: payload.Args, Status: domain.ToolStatusErrored, Error: &domain.ToolError{Message: payload.Text}})
			}
		} else if err := content.SetToolResult(domain.ToolCallID(payload.ToolCallID), result); err != nil {
			content.Tools = append(content.Tools, domain.ToolCall{ToolCallID: domain.ToolCallID(payload.ToolCallID), Tool: payload.Tool, Args: payload.Args, Status: domain.ToolStatusDone, Result: &result})
		}
		item.Content = content
	case domain.UsagePayload:
		content, _ := item.Content.(domain.AssistantMessage)
		usage := payload.Usage
		content.Usage = &usage
		item.Content = content
	case domain.AttachmentPayload:
		content, _ := item.Content.(domain.UserMessage)
		content.Attachments = upsertAttachment(content.Attachments, domain.Attachment(payload))
		item.Content = content
	case domain.ReferencePayload:
		content, _ := item.Content.(domain.UserMessage)
		content.References = append(content.References, domain.Reference(payload))
		item.Content = content
	case domain.CompactionPayload:
		item.Content = domain.Compaction{
			Summary: payload.Summary, Trigger: payload.Trigger, Status: payload.Status,
			BeforeContextTokens: payload.BeforeContextTokens, AfterContextTokens: payload.AfterContextTokens,
		}
	case domain.EventNoticePayload:
		item.Content = domain.Notice{Text: payload.Text, Kind: payload.Kind, Level: payload.Severity, Tool: payload.Tool, ToolCallID: payload.ToolCallID}
	case domain.SystemNoticePayload:
		item.Content = domain.Notice{Text: payload.Text, Kind: "system"}
	}
	item.UpdatedAt = time.Now().UTC()
	return nil
}

func applyLegacyPartToLegacyMessage(item *domain.TimelineItem, part domain.Part) bool {
	legacy, ok := item.Content.(domain.LegacyMessage)
	if !ok {
		return false
	}
	if part.Kind == "" && part.Payload != nil {
		part.Kind = part.Payload.PartKind()
	}
	if part.Body == "" {
		part.Body = part.Text()
	}
	if part.MetaJSON == "" {
		part.MetaJSON = part.LegacyMetaJSON()
	}
	for idx := range legacy.Parts {
		if legacy.Parts[idx].ID == part.ID {
			legacy.Parts[idx] = part
			item.Content = legacy
			return true
		}
	}
	legacy.Parts = append(legacy.Parts, part)
	item.Content = legacy
	return true
}

func upsertAttachment(items []domain.Attachment, next domain.Attachment) []domain.Attachment {
	for idx := range items {
		if items[idx].ID != "" && items[idx].ID == next.ID {
			items[idx] = next
			return items
		}
		if items[idx].Name != "" && items[idx].Name == next.Name {
			items[idx] = next
			return items
		}
	}
	return append(items, next)
}

func legacyPartsFromTimeline(item domain.TimelineItem) []domain.Part {
	var parts []domain.Part
	add := func(kind domain.PartKind, payload domain.PartPayload, offset int64) {
		part := domain.Part{ID: item.ID*1000 + offset, MessageID: item.ID, Kind: kind, Payload: payload, CreatedAt: item.CreatedAt}
		part.Body = part.Text()
		part.MetaJSON = part.LegacyMetaJSON()
		parts = append(parts, part)
	}
	switch payload := item.Content.(type) {
	case domain.LegacyMessage:
		parts = make([]domain.Part, 0, len(payload.Parts))
		for _, part := range payload.Parts {
			part.MessageID = item.ID
			if part.Kind == "" && part.Payload != nil {
				part.Kind = part.Payload.PartKind()
			}
			if part.CreatedAt.IsZero() {
				part.CreatedAt = item.CreatedAt
			}
			if part.Body == "" {
				part.Body = part.Text()
			}
			if part.MetaJSON == "" {
				part.MetaJSON = part.LegacyMetaJSON()
			}
			parts = append(parts, part)
		}
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
			if tool.Result != nil {
				add(domain.PartKindToolOutput, domain.ToolOutputPayload{Tool: tool.Tool, ToolCallID: string(tool.ToolCallID), Args: tool.Args, Status: tool.Result.Status, Text: tool.Result.Text, Diff: tool.Result.Diff}, offset)
				offset++
			}
			if tool.Error != nil {
				add(domain.PartKindToolOutput, domain.ToolOutputPayload{Tool: tool.Tool, ToolCallID: string(tool.ToolCallID), Args: tool.Args, Status: domain.ToolResultStatusError, Text: tool.Error.Message}, offset)
				offset++
			}
		}
		if payload.Usage != nil {
			add(domain.PartKindUsage, domain.UsagePayload{Usage: *payload.Usage}, offset)
		}
	case domain.Notice:
		add(domain.PartKindEventNotice, domain.EventNoticePayload{Text: payload.Text, Kind: payload.Kind, Severity: payload.Level, Tool: payload.Tool, ToolCallID: payload.ToolCallID}, 1)
	case domain.Compaction:
		add(domain.PartKindCompaction, domain.CompactionPayload{Summary: payload.Summary, Trigger: payload.Trigger, Status: payload.Status, BeforeContextTokens: payload.BeforeContextTokens, AfterContextTokens: payload.AfterContextTokens}, 1)
	}
	return parts
}
