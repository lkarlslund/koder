package chatstore

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/store"
)

type Approval struct {
	ID         domain.ID
	SessionID  domain.ID
	ChatID     domain.ID
	Tool       domain.ToolKind
	ToolCallID string
	Command    string
	Status     domain.ApprovalStatus
	CreatedAt  time.Time
}

var toolCallStorageMu sync.Mutex

func TimelineCollection(st *store.Store) store.Collection[domain.TimelineItem] {
	return store.NewCollection(st, store.CollectionSpec[domain.TimelineItem]{
		Namespace: "timeline",
		GetID:     func(v domain.TimelineItem) string { return v.ID },
		SetID:     func(v *domain.TimelineItem, id string) { v.ID = id },
		Indexes: []store.IndexSpec[domain.TimelineItem]{
			{Name: "chat", Value: func(v domain.TimelineItem) string { return v.ChatID }},
		},
	})
}

func ApprovalCollection(st *store.Store) store.Collection[Approval] {
	return store.NewCollection(st, store.CollectionSpec[Approval]{
		Namespace: "approvals",
		GetID:     func(v Approval) string { return v.ID },
		SetID:     func(v *Approval, id string) { v.ID = id },
		Indexes: []store.IndexSpec[Approval]{
			{Name: "session", Value: func(v Approval) string { return v.SessionID }},
			{Name: "chat", Value: func(v Approval) string { return v.ChatID }},
			{Name: "status", Value: func(v Approval) string { return v.Status.String() }},
		},
	})
}

func ChatCollection(st *store.Store) store.Collection[domain.Chat] {
	return store.NewCollection(st, store.CollectionSpec[domain.Chat]{
		Namespace: "chats",
		GetID:     func(v domain.Chat) string { return v.ID },
		SetID:     func(v *domain.Chat, id string) { v.ID = id },
		Indexes: []store.IndexSpec[domain.Chat]{
			{Name: "session", Value: func(v domain.Chat) string { return v.SessionID }},
		},
	})
}

func GetChat(ctx context.Context, st *store.Store, chatID domain.ID) (domain.Chat, error) {
	return ChatCollection(st).Get(ctx, chatID)
}

func PutChat(ctx context.Context, st *store.Store, chatRecord domain.Chat) error {
	if chatRecord.ID == "" {
		return fmt.Errorf("put chat: id is required")
	}
	if chatRecord.SessionID == "" {
		return fmt.Errorf("put chat: session id is required")
	}
	return ChatCollection(st).Put(ctx, chatRecord)
}

func UpdateChat(ctx context.Context, st *store.Store, chatRecord domain.Chat) error {
	existing, err := GetChat(ctx, st, chatRecord.ID)
	if err != nil {
		return err
	}
	if chatRecord.Position == 0 && existing.Position != 0 && chatRecord.UpdatedAt.After(existing.UpdatedAt) {
		chatRecord.Position = existing.Position
	}
	return PutChat(ctx, st, chatRecord)
}

func SetChatQueuedInputs(ctx context.Context, st *store.Store, chatID domain.ID, items []domain.QueuedInput) error {
	chatRecord, err := GetChat(ctx, st, chatID)
	if err != nil {
		return err
	}
	chatRecord.QueuedInputs = cloneQueuedInputs(items)
	chatRecord.UpdatedAt = time.Now().UTC()
	return PutChat(ctx, st, chatRecord)
}

func cloneQueuedInputs(src []domain.QueuedInput) []domain.QueuedInput {
	if len(src) == 0 {
		return nil
	}
	dst := make([]domain.QueuedInput, 0, len(src))
	for _, item := range src {
		cloned := item
		cloned.Attachments = append([]domain.QueuedAttachment(nil), item.Attachments...)
		cloned.References = append([]domain.QueuedReference(nil), item.References...)
		dst = append(dst, cloned)
	}
	return dst
}

func TimelineForChat(ctx context.Context, st *store.Store, chatID domain.ID) ([]domain.TimelineItem, error) {
	items, err := TimelineCollection(st).List(ctx, store.ByIndex[domain.TimelineItem]("chat", string(chatID)))
	if err != nil {
		return nil, err
	}
	SortTimeline(items)
	return items, nil
}

func SortTimeline(items []domain.TimelineItem) {
	slices.SortFunc(items, func(a, b domain.TimelineItem) int {
		switch {
		case a.Seq < b.Seq:
			return -1
		case a.Seq > b.Seq:
			return 1
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		default:
			return 0
		}
	})
}

func PutTimelineItem(ctx context.Context, st *store.Store, item domain.TimelineItem) error {
	return TimelineCollection(st).Put(ctx, item)
}

func InsertTimelineItem(ctx context.Context, st *store.Store, item domain.TimelineItem) (domain.TimelineItem, error) {
	return TimelineCollection(st).Insert(ctx, item)
}

func AppendTimeline(ctx context.Context, st *store.Store, chatID domain.ID, content domain.TimelineContent) (domain.TimelineItem, error) {
	if chatID == "" {
		return domain.TimelineItem{}, fmt.Errorf("append timeline: chat id is required")
	}
	if content == nil {
		return domain.TimelineItem{}, fmt.Errorf("append timeline: content is required")
	}
	items, err := TimelineForChat(ctx, st, chatID)
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
	return InsertTimelineItem(ctx, st, item)
}

func AttachToolResult(ctx context.Context, st *store.Store, chatID domain.ID, toolCallID string, result domain.ToolResult) (domain.TimelineItem, error) {
	return updateToolCall(ctx, st, chatID, toolCallID, func(call *domain.ToolCall) error {
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

func AttachToolError(ctx context.Context, st *store.Store, chatID domain.ID, toolCallID string, toolErr domain.ToolError) (domain.TimelineItem, error) {
	return updateToolCall(ctx, st, chatID, toolCallID, func(call *domain.ToolCall) error {
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

func FailInterruptedToolCalls(ctx context.Context, st *store.Store, chatID domain.ID, message string) (int, error) {
	return failToolCallsMatching(ctx, st, chatID, message, interruptedToolStatus)
}

func FailRunningToolCalls(ctx context.Context, st *store.Store, chatID domain.ID, message string) (int, error) {
	return failToolCallsMatching(ctx, st, chatID, message, func(status domain.ToolStatus) bool {
		return status == domain.ToolStatusRunning
	})
}

func failToolCallsMatching(ctx context.Context, st *store.Store, chatID domain.ID, message string, match func(domain.ToolStatus) bool) (int, error) {
	if chatID == "" || match == nil {
		return 0, nil
	}
	message = strings.TrimSpace(message)
	if message == "" {
		message = "Tool execution failed because koder restarted before the tool completed."
	}
	toolCallStorageMu.Lock()
	defer toolCallStorageMu.Unlock()
	items, err := TimelineForChat(ctx, st, chatID)
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
		if err := PutTimelineItem(ctx, st, item); err != nil {
			return count, err
		}
	}
	return count, nil
}

func interruptedToolStatus(status domain.ToolStatus) bool {
	return status == domain.ToolStatusPending || status == domain.ToolStatusRunning
}

func AttachToolApproval(ctx context.Context, st *store.Store, chatID domain.ID, toolCallID string, approval domain.ApprovalRequest) (domain.TimelineItem, error) {
	_ = approval
	return updateToolCall(ctx, st, chatID, toolCallID, func(call *domain.ToolCall) error {
		call.Approval = nil
		call.ApprovalID = strings.TrimSpace(toolCallID)
		call.Status = domain.ToolStatusAwaitingApproval
		return nil
	})
}

func MarkToolRunning(ctx context.Context, st *store.Store, chatID domain.ID, toolCallID string) (domain.TimelineItem, error) {
	return updateToolCall(ctx, st, chatID, toolCallID, func(call *domain.ToolCall) error {
		call.Status = domain.ToolStatusRunning
		call.Approval = nil
		call.ApprovalID = ""
		if call.StartedAt.IsZero() {
			call.StartedAt = time.Now().UTC()
		}
		return nil
	})
}

func updateToolCall(ctx context.Context, st *store.Store, chatID domain.ID, toolCallID string, update func(*domain.ToolCall) error) (domain.TimelineItem, error) {
	toolCallID = strings.TrimSpace(toolCallID)
	if chatID == "" {
		return domain.TimelineItem{}, fmt.Errorf("update tool call: chat id is required")
	}
	if toolCallID == "" {
		return domain.TimelineItem{}, fmt.Errorf("update tool call: tool call id is required")
	}
	toolCallStorageMu.Lock()
	defer toolCallStorageMu.Unlock()
	items, err := TimelineForChat(ctx, st, chatID)
	if err != nil {
		return domain.TimelineItem{}, err
	}
	for idx := len(items) - 1; idx >= 0; idx-- {
		item := items[idx]
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
		if err := PutTimelineItem(ctx, st, item); err != nil {
			return domain.TimelineItem{}, err
		}
		return item, nil
	}
	return domain.TimelineItem{}, fmt.Errorf("tool call %q has no owning assistant item", toolCallID)
}

func AppendAssistantToolCalls(ctx context.Context, st *store.Store, chatID domain.ID, calls []domain.ToolCall, text string, usage domain.Usage) (domain.TimelineItem, error) {
	return AppendAssistantToolCallsWithItem(ctx, st, chatID, domain.TimelineItem{}, calls, text, usage)
}

func AppendAssistantToolCallsWithItem(ctx context.Context, st *store.Store, chatID domain.ID, item domain.TimelineItem, calls []domain.ToolCall, text string, usage domain.Usage) (domain.TimelineItem, error) {
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
		item, err = AppendTimeline(ctx, st, chatID, assistant)
		if err != nil {
			return domain.TimelineItem{}, err
		}
	} else {
		now := time.Now().UTC()
		if item.ChatID == "" {
			item.ChatID = chatID
		}
		if item.Seq == 0 {
			items, err := TimelineForChat(ctx, st, chatID)
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
		if _, err := InsertTimelineItem(ctx, st, item); err != nil {
			return domain.TimelineItem{}, err
		}
	}
	item.Seal(time.Now().UTC())
	if err := PutTimelineItem(ctx, st, item); err != nil {
		return domain.TimelineItem{}, err
	}
	return item, nil
}

func PendingApprovalsForChat(ctx context.Context, st *store.Store, chatID domain.ID) ([]Approval, error) {
	chatRecord, err := GetChat(ctx, st, chatID)
	if err != nil {
		return nil, err
	}
	items, err := TimelineForChat(ctx, st, chatID)
	if err != nil {
		return nil, err
	}
	var approvals []Approval
	for _, item := range items {
		assistant, ok := item.Content.(domain.AssistantMessage)
		if !ok {
			continue
		}
		for _, call := range assistant.Tools {
			if call.Status != domain.ToolStatusAwaitingApproval {
				continue
			}
			approvals = append(approvals, Approval{
				ID:         SyntheticApprovalID(string(call.ToolCallID)),
				SessionID:  chatRecord.SessionID,
				ChatID:     chatRecord.ID,
				Tool:       call.Tool,
				ToolCallID: string(call.ToolCallID),
				Command:    toolCallPreview(call),
				Status:     domain.ApprovalStatusPending,
				CreatedAt:  item.UpdatedAt,
			})
		}
	}
	return approvals, nil
}

func SyntheticApprovalID(toolCallID string) domain.ID {
	return strings.TrimSpace(toolCallID)
}

func toolCallPreview(call domain.ToolCall) string {
	if command := strings.TrimSpace(call.Args["command"]); command != "" {
		return command
	}
	if path := strings.TrimSpace(call.Args["path"]); path != "" {
		return path
	}
	if pattern := strings.TrimSpace(call.Args["pattern"]); pattern != "" {
		return pattern
	}
	return strings.TrimSpace(call.Tool.String())
}
