package store

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/domain"
)

// AppendTimeline appends one typed item to a chat timeline.
func (s *Store) AppendTimeline(ctx context.Context, chatID domain.ID, content domain.TimelineContent) (domain.TimelineItem, error) {
	if chatID == "" {
		return domain.TimelineItem{}, fmt.Errorf("append timeline: chat id is required")
	}
	if content == nil {
		return domain.TimelineItem{}, fmt.Errorf("append timeline: content is required")
	}
	items, err := s.TimelineForChat(ctx, chatID)
	if err != nil {
		return domain.TimelineItem{}, err
	}
	now := time.Now().UTC()
	item := domain.TimelineItem{
		ChatID:    chatID,
		Seq:       int64(len(items) + 1),
		Content:   content,
		CreatedAt: now,
		UpdatedAt: now,
	}
	return s.Timeline().Insert(ctx, item)
}

// AttachToolResult stores a tool result on the assistant item that requested it.
func (s *Store) AttachToolResult(ctx context.Context, chatID domain.ID, toolCallID string, result domain.ToolResult) (domain.TimelineItem, error) {
	return s.updateToolCall(ctx, chatID, toolCallID, func(call *domain.ToolCall) error {
		call.Result = &result
		call.Error = nil
		call.Approval = nil
		call.ApprovalID = ""
		if result.Status == domain.ToolResultStatusDenied {
			call.Status = domain.ToolStatusDenied
		} else {
			call.Status = domain.ToolStatusDone
		}
		if call.CompletedAt.IsZero() {
			call.CompletedAt = time.Now().UTC()
		}
		return nil
	})
}

// AttachToolError stores a tool error on the assistant item that requested it.
func (s *Store) AttachToolError(ctx context.Context, chatID domain.ID, toolCallID string, toolErr domain.ToolError) (domain.TimelineItem, error) {
	return s.updateToolCall(ctx, chatID, toolCallID, func(call *domain.ToolCall) error {
		call.Error = &toolErr
		call.Result = nil
		call.Approval = nil
		call.ApprovalID = ""
		call.Status = domain.ToolStatusErrored
		if call.CompletedAt.IsZero() {
			call.CompletedAt = time.Now().UTC()
		}
		return nil
	})
}

// FailInterruptedToolCalls marks uncompleted tool calls as errored after an interrupted process shutdown.
func (s *Store) FailInterruptedToolCalls(ctx context.Context, chatID domain.ID, message string) (int, error) {
	return s.failToolCallsMatching(ctx, chatID, message, interruptedToolStatus)
}

// FailRunningToolCalls marks running tool calls as errored after process startup.
func (s *Store) FailRunningToolCalls(ctx context.Context, chatID domain.ID, message string) (int, error) {
	return s.failToolCallsMatching(ctx, chatID, message, func(status domain.ToolStatus) bool {
		return status == domain.ToolStatusRunning
	})
}

func (s *Store) failToolCallsMatching(ctx context.Context, chatID domain.ID, message string, match func(domain.ToolStatus) bool) (int, error) {
	if chatID == "" {
		return 0, nil
	}
	if match == nil {
		return 0, nil
	}
	message = strings.TrimSpace(message)
	if message == "" {
		message = "Tool execution failed because koder restarted before the tool completed."
	}
	s.toolCallMu.Lock()
	defer s.toolCallMu.Unlock()
	items, err := s.TimelineForChat(ctx, chatID)
	if err != nil {
		return 0, err
	}
	count := 0
	now := time.Now().UTC()
	for _, item := range items {
		assistant, ok := item.Content.(domain.AssistantMessage)
		if !ok {
			continue
		}
		changed := false
		for idx := range assistant.Tools {
			call := &assistant.Tools[idx]
			if !match(call.Status) || call.Result != nil || call.Error != nil {
				continue
			}
			call.Status = domain.ToolStatusErrored
			call.Error = &domain.ToolError{Message: message, Code: domain.NoticeReasonProcessRestart}
			call.Approval = nil
			call.ApprovalID = ""
			if call.CompletedAt.IsZero() {
				call.CompletedAt = now
			}
			changed = true
			count++
		}
		if !changed {
			continue
		}
		item.Content = assistant
		item.UpdatedAt = now
		if err := s.Timeline().Put(ctx, item); err != nil {
			return count, err
		}
	}
	return count, nil
}

func interruptedToolStatus(status domain.ToolStatus) bool {
	return status == domain.ToolStatusPending || status == domain.ToolStatusRunning
}

// AttachToolApproval stores an approval request on the assistant item that requested it.
func (s *Store) AttachToolApproval(ctx context.Context, chatID domain.ID, toolCallID string, approval domain.ApprovalRequest) (domain.TimelineItem, error) {
	return s.updateToolCall(ctx, chatID, toolCallID, func(call *domain.ToolCall) error {
		call.Approval = nil
		call.ApprovalID = strings.TrimSpace(toolCallID)
		call.Status = domain.ToolStatusAwaitingApproval
		return nil
	})
}

// MarkToolRunning marks a requested tool call as executing.
func (s *Store) MarkToolRunning(ctx context.Context, chatID domain.ID, toolCallID string) (domain.TimelineItem, error) {
	return s.updateToolCall(ctx, chatID, toolCallID, func(call *domain.ToolCall) error {
		call.Status = domain.ToolStatusRunning
		call.Approval = nil
		call.ApprovalID = ""
		if call.StartedAt.IsZero() {
			call.StartedAt = time.Now().UTC()
		}
		return nil
	})
}

func (s *Store) updateToolCall(ctx context.Context, chatID domain.ID, toolCallID string, update func(*domain.ToolCall) error) (domain.TimelineItem, error) {
	toolCallID = strings.TrimSpace(toolCallID)
	if chatID == "" {
		return domain.TimelineItem{}, fmt.Errorf("update tool call: chat id is required")
	}
	if toolCallID == "" {
		return domain.TimelineItem{}, fmt.Errorf("update tool call: tool call id is required")
	}
	s.toolCallMu.Lock()
	defer s.toolCallMu.Unlock()
	items, err := s.TimelineForChat(ctx, chatID)
	if err != nil {
		return domain.TimelineItem{}, err
	}
	for idx := len(items) - 1; idx >= 0; idx-- {
		item := items[idx]
		switch item.Content.(type) {
		case domain.UserMessage:
			return domain.TimelineItem{}, fmt.Errorf("tool call %q has no owning assistant item", toolCallID)
		}
		assistant, ok := item.Content.(domain.AssistantMessage)
		if !ok {
			continue
		}
		call := assistant.ToolByID(domain.ToolCallID(toolCallID))
		if call == nil {
			continue
		}
		if err := update(call); err != nil {
			return domain.TimelineItem{}, err
		}
		item.Content = assistant
		item.UpdatedAt = time.Now().UTC()
		if err := s.Timeline().Put(ctx, item); err != nil {
			return domain.TimelineItem{}, err
		}
		return item, nil
	}
	return domain.TimelineItem{}, fmt.Errorf("tool call %q has no owning assistant item", toolCallID)
}

// AppendAssistantToolCalls appends an assistant item with direct child tool calls.
func (s *Store) AppendAssistantToolCalls(ctx context.Context, chatID domain.ID, calls []domain.ToolCall, text string, usage domain.Usage) (domain.TimelineItem, error) {
	return s.AppendAssistantToolCallsWithItem(ctx, chatID, domain.TimelineItem{}, calls, text, usage)
}

// AppendAssistantToolCallsWithItem appends an assistant item with a preassigned timeline identity.
func (s *Store) AppendAssistantToolCallsWithItem(ctx context.Context, chatID domain.ID, item domain.TimelineItem, calls []domain.ToolCall, text string, usage domain.Usage) (domain.TimelineItem, error) {
	if len(calls) == 0 && strings.TrimSpace(text) == "" {
		return domain.TimelineItem{}, fmt.Errorf("assistant item needs text or tool calls")
	}
	assistant := domain.AssistantMessage{Text: text}
	for _, call := range calls {
		if err := assistant.AddToolCall(call); err != nil {
			return domain.TimelineItem{}, err
		}
	}
	usage = usage.Normalized()
	if usage.HasAnyTokens() {
		assistant.Usage = &usage
	}
	if item.ID == "" {
		var err error
		item, err = s.AppendTimeline(ctx, chatID, assistant)
		if err != nil {
			return domain.TimelineItem{}, err
		}
	} else {
		now := time.Now().UTC()
		if item.ChatID == "" {
			item.ChatID = chatID
		}
		if item.Seq == 0 {
			items, err := s.TimelineForChat(ctx, chatID)
			if err != nil {
				return domain.TimelineItem{}, err
			}
			item.Seq = int64(len(items) + 1)
		}
		if item.CreatedAt.IsZero() {
			item.CreatedAt = now
		}
		item.UpdatedAt = now
		item.Content = assistant
		if _, err := s.Timeline().Insert(ctx, item); err != nil {
			return domain.TimelineItem{}, err
		}
	}
	item.Seal(time.Now().UTC())
	if err := s.Timeline().Put(ctx, item); err != nil {
		return domain.TimelineItem{}, err
	}
	return item, nil
}

// ForkTimeline copies timeline items from one session's default chat to another.
func (s *Store) ForkTimeline(ctx context.Context, sourceSessionID, destSessionID domain.ID) error {
	sourceChat, err := s.DefaultChat(ctx, sourceSessionID)
	if err != nil {
		return err
	}
	destChat, err := s.DefaultChat(ctx, destSessionID)
	if err != nil {
		return err
	}
	items, err := s.TimelineForChat(ctx, sourceChat.ID)
	if err != nil {
		return err
	}
	for idx, item := range slices.Clone(items) {
		item.ID = ""
		item.ChatID = destChat.ID
		item.Seq = int64(idx + 1)
		if _, err := s.Timeline().Insert(ctx, item); err != nil {
			return err
		}
	}
	return nil
}
