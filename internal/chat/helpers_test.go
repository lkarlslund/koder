package chat

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/store"
)

func (r *Chat) appendOptimisticUserMessage(item domain.QueuedInput, session domain.Session, chat domain.Chat) {
	_ = session
	if domain.DeliveryForQueuedInput(item) == domain.QueuedInputDeliveryContinue || r.state == nil {
		return
	}
	now := time.Now().UTC()
	summary := strings.TrimSpace(item.Text)
	user := domain.UserMessage{Text: summary, Source: domain.UserMessageSourceForQueuedInput(item)}
	for _, draft := range item.Attachments {
		user.Attachments = append(user.Attachments, domain.Attachment(draft))
	}
	for _, ref := range item.References {
		user.References = append(user.References, domain.Reference(ref))
	}
	requiresImages := userMessageRequiresImages(user)
	r.mu.Lock()
	timelineItem := domain.TimelineItem{
		ID:        NewTimelineID(now),
		ChatID:    chat.ID,
		Seq:       r.state.NextTimelineSequence(),
		Content:   user,
		CreatedAt: now,
		UpdatedAt: now,
		SealedAt:  now,
	}
	r.state.AppendTimelineItem(timelineItem)
	if requiresImages {
		r.chat.RequiresImages = true
	}
	r.chat.LastMessage = summary
	r.state.UpdateChat(func(chat *domain.Chat) {
		if requiresImages {
			chat.RequiresImages = true
		}
		chat.LastMessage = summary
	})
	r.mu.Unlock()
}

func timelinePageAfter(items []domain.TimelineItem, after id.ID, limit int) TimelinePage {
	total := len(items)
	start := 0
	if after != "" {
		idx := slices.IndexFunc(items, func(item domain.TimelineItem) bool { return item.ID == after })
		if idx >= 0 {
			start = idx + 1
		}
	}
	if start >= total {
		return TimelinePage{HasMore: total > 0, LoadedAll: total == 0, Total: total}
	}
	if limit <= 0 {
		limit = total
	}
	end := min(total, start+limit)
	return timelinePage(items[start:end], start > 0, end < total, total)
}

func appendAssistantToolCalls(ctx context.Context, st *store.Store, chatID id.ID, calls []domain.ToolCall, text string, usage domain.Usage) (domain.TimelineItem, error) {
	return appendAssistantToolCallsWithItem(ctx, st, chatID, domain.TimelineItem{}, calls, text, domain.ReasoningContent{}, usage)
}

func appendAssistantToolCallsWithItem(ctx context.Context, st *store.Store, chatID id.ID, item domain.TimelineItem, calls []domain.ToolCall, text string, reasoning domain.ReasoningContent, usage domain.Usage) (domain.TimelineItem, error) {
	if len(calls) == 0 && strings.TrimSpace(text) == "" {
		return domain.TimelineItem{}, fmt.Errorf("assistant item needs text or tool calls")
	}
	assistant := domain.AssistantMessage{Text: text, Reasoning: reasoning}
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
		item, err = appendTimeline(ctx, st, chatID, assistant)
		if err != nil {
			return domain.TimelineItem{}, err
		}
	} else {
		unlock := store.LockTimelineMutation()
		defer unlock()
		now := time.Now().UTC()
		if item.ChatID == "" {
			item.ChatID = chatID
		}
		if item.Seq == 0 {
			items, err := timelineForChat(ctx, st, chatID)
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
		if _, err := insertTimelineItem(ctx, st, item); err != nil {
			return domain.TimelineItem{}, err
		}
	}
	item.Seal(time.Now().UTC())
	if err := putTimelineItem(ctx, st, item); err != nil {
		return domain.TimelineItem{}, err
	}
	return item, nil
}
