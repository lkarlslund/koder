package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/store"
)

type Approval struct {
	ID         id.ID
	SessionID  id.ID
	ChatID     id.ID
	Tool       domain.ToolKind
	ToolCallID string
	Command    string
	Status     domain.ApprovalStatus
	CreatedAt  time.Time
}

type Source struct {
	deps func() Deps
}

func NewSource(deps func() Deps) *Source {
	return &Source{deps: deps}
}

func (s *Source) currentDeps() (Deps, error) {
	if s == nil || s.deps == nil {
		return Deps{}, fmt.Errorf("chat source is required")
	}
	deps := s.deps()
	if deps.Store == nil {
		return Deps{}, fmt.Errorf("store is required")
	}
	return deps, nil
}

func (s *Source) Load(ctx context.Context, session domain.Session, chatRecord domain.Chat) (*Chat, error) {
	deps, err := s.currentDeps()
	if err != nil {
		return nil, err
	}
	return Load(ctx, session, chatRecord, deps, nil)
}

func (s *Source) LoadMetadata(ctx context.Context, session domain.Session, chatRecord domain.Chat) (*Chat, error) {
	deps, err := s.currentDeps()
	if err != nil {
		return nil, err
	}
	return LoadMetadata(ctx, session, chatRecord, deps, nil)
}

func (s *Source) ListRecordsForSession(ctx context.Context, sessionID id.ID) ([]domain.Chat, error) {
	deps, err := s.currentDeps()
	if err != nil {
		return nil, err
	}
	return listRecordsForSession(ctx, deps.Store, sessionID)
}

func (s *Source) DefaultRecord(ctx context.Context, sessionID id.ID) (domain.Chat, error) {
	deps, err := s.currentDeps()
	if err != nil {
		return domain.Chat{}, err
	}
	return defaultRecord(ctx, deps.Store, sessionID)
}

func (s *Source) CreateRecord(ctx context.Context, req CreateRecordRequest) (domain.Chat, error) {
	deps, err := s.currentDeps()
	if err != nil {
		return domain.Chat{}, err
	}
	return createRecord(ctx, deps.Store, req)
}

func (s *Source) PutRecord(ctx context.Context, chatRecord domain.Chat) error {
	deps, err := s.currentDeps()
	if err != nil {
		return err
	}
	return putChat(ctx, deps.Store, chatRecord)
}

func (s *Source) UpdateRecord(ctx context.Context, chatRecord domain.Chat) error {
	deps, err := s.currentDeps()
	if err != nil {
		return err
	}
	return updateChat(ctx, deps.Store, chatRecord)
}

func (s *Source) ForkRecordAt(ctx context.Context, source domain.Chat, sourceTimeline []domain.TimelineItem, anchorItemID id.ID, title string, position int) (domain.Chat, error) {
	deps, err := s.currentDeps()
	if err != nil {
		return domain.Chat{}, err
	}
	return forkRecordAt(ctx, deps.Store, source, sourceTimeline, anchorItemID, title, position)
}

func (s *Source) DeleteSessionData(ctx context.Context, sessionID id.ID) error {
	deps, err := s.currentDeps()
	if err != nil {
		return err
	}
	return deleteSessionData(ctx, deps.Store, sessionID)
}

func (s *Source) TimelinePage(ctx context.Context, chatID, before id.ID, limit int, all bool) (TimelinePage, error) {
	deps, err := s.currentDeps()
	if err != nil {
		return TimelinePage{}, err
	}
	return timelinePageForChat(ctx, deps.Store, chatID, before, limit, all)
}

func timelineCollection(st *store.Store) store.Collection[domain.TimelineItem] {
	return store.NewCollection(st, store.CollectionSpec[domain.TimelineItem]{
		Namespace: "timeline",
		GetID:     func(v domain.TimelineItem) string { return v.ID },
		SetID:     func(v *domain.TimelineItem, id string) { v.ID = id },
		Indexes: []store.IndexSpec[domain.TimelineItem]{
			{Name: "chat", Value: func(v domain.TimelineItem) string { return v.ChatID }},
		},
	})
}

func approvalCollection(st *store.Store) store.Collection[Approval] {
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

func chatCollection(st *store.Store) store.Collection[domain.Chat] {
	return store.NewCollection(st, store.CollectionSpec[domain.Chat]{
		Namespace: "chats",
		GetID:     func(v domain.Chat) string { return v.ID },
		SetID:     func(v *domain.Chat, id string) { v.ID = id },
		Indexes: []store.IndexSpec[domain.Chat]{
			{Name: "session", Value: func(v domain.Chat) string { return v.SessionID }},
		},
	})
}

func listRecordsForSession(ctx context.Context, st *store.Store, sessionID id.ID) ([]domain.Chat, error) {
	chats, err := chatCollection(st).List(ctx, store.ByIndex[domain.Chat]("session", string(sessionID)))
	if err != nil {
		return nil, err
	}
	sortRecordsForSidebar(chats)
	return chats, nil
}

func sortRecordsForSidebar(chats []domain.Chat) {
	slices.SortFunc(chats, func(a, b domain.Chat) int {
		switch {
		case a.Position < b.Position:
			return -1
		case a.Position > b.Position:
			return 1
		case a.CreatedAt.Before(b.CreatedAt):
			return -1
		case a.CreatedAt.After(b.CreatedAt):
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

func defaultRecord(ctx context.Context, st *store.Store, sessionID id.ID) (domain.Chat, error) {
	chats, err := listRecordsForSession(ctx, st, sessionID)
	if err != nil {
		return domain.Chat{}, err
	}
	for _, chatRecord := range chats {
		if chatRecord.ParentChatID == nil {
			return chatRecord, nil
		}
	}
	if len(chats) > 0 {
		return chats[0], nil
	}
	return domain.Chat{}, fmt.Errorf("session %s has no chats", sessionID)
}

type CreateRecordRequest struct {
	Session           domain.Session
	Title             string
	Role              domain.WorkflowRole
	ParentID          *id.ID
	ProviderID        string
	ModelID           string
	PermissionProfile string
	ToolStates        map[domain.ToolKind]bool
	Position          int
}

func createRecord(ctx context.Context, st *store.Store, req CreateRecordRequest) (domain.Chat, error) {
	session := req.Session
	if session.ID == "" {
		return domain.Chat{}, fmt.Errorf("create chat: session id is required")
	}
	chats, err := listRecordsForSession(ctx, st, session.ID)
	if err != nil {
		return domain.Chat{}, err
	}
	position := req.Position
	if position < 0 {
		position = len(chats)
	}
	now := time.Now().UTC()
	chatRecord := domain.Chat{
		ID:                id.NewAt(now),
		SessionID:         session.ID,
		ParentChatID:      req.ParentID,
		Title:             strings.TrimSpace(req.Title),
		WorkflowRole:      req.Role,
		ProviderID:        strings.TrimSpace(req.ProviderID),
		ModelID:           strings.TrimSpace(req.ModelID),
		PermissionProfile: strings.TrimSpace(req.PermissionProfile),
		ToolStates:        cloneToolStates(req.ToolStates),
		Position:          position,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if chatRecord.Title == "" {
		chatRecord.Title = "New Chat"
	}
	if chatRecord.WorkflowRole == "" {
		chatRecord.WorkflowRole = chatrole.General
	}
	if chatRecord.PermissionProfile == "" {
		chatRecord.PermissionProfile = session.PermissionProfile
	}
	if len(chatRecord.ToolStates) == 0 {
		chatRecord.ToolStates = cloneToolStates(session.ToolStates)
	}
	if chatRecord.ProviderID == "" || chatRecord.ModelID == "" {
		if defaultChat, err := defaultRecord(ctx, st, session.ID); err == nil {
			if chatRecord.ProviderID == "" {
				chatRecord.ProviderID = defaultChat.ProviderID
			}
			if chatRecord.ModelID == "" {
				chatRecord.ModelID = defaultChat.ModelID
			}
		}
	}
	if err := putChat(ctx, st, chatRecord); err != nil {
		return domain.Chat{}, err
	}
	return chatRecord, nil
}

func deleteRecord(ctx context.Context, st *store.Store, chatID id.ID) error {
	return chatCollection(st).Delete(ctx, chatID)
}

func deleteSessionData(ctx context.Context, st *store.Store, sessionID id.ID) error {
	if sessionID == "" {
		return fmt.Errorf("delete chat session data: session id is required")
	}
	chats, err := listRecordsForSession(ctx, st, sessionID)
	if err != nil {
		return err
	}
	for _, chatRecord := range chats {
		if err := DeletePersistedData(ctx, st, chatRecord.ID); err != nil {
			return err
		}
		if err := deleteRecord(ctx, st, chatRecord.ID); err != nil {
			return err
		}
	}
	return nil
}

func forkRecordAt(ctx context.Context, st *store.Store, source domain.Chat, sourceTimeline []domain.TimelineItem, anchorItemID id.ID, title string, position int) (domain.Chat, error) {
	if source.ID == "" {
		return domain.Chat{}, fmt.Errorf("fork chat: source chat id is required")
	}
	if source.SessionID == "" {
		return domain.Chat{}, fmt.Errorf("fork chat: session id is required")
	}
	anchorIdx := slices.IndexFunc(sourceTimeline, func(item domain.TimelineItem) bool {
		return item.ID == anchorItemID
	})
	if anchorIdx < 0 {
		return domain.Chat{}, fmt.Errorf("timeline item %s not found in chat %s", anchorItemID, source.ID)
	}
	parentID := source.ID
	now := time.Now().UTC()
	chatRecord := domain.Chat{
		ID:                    id.NewAt(now),
		SessionID:             source.SessionID,
		ParentChatID:          &parentID,
		Title:                 strings.TrimSpace(title),
		WorkflowRole:          source.WorkflowRole,
		ProviderID:            strings.TrimSpace(source.ProviderID),
		ModelID:               strings.TrimSpace(source.ModelID),
		PermissionProfile:     source.PermissionProfile,
		ToolStates:            cloneToolStates(source.ToolStates),
		ActiveMilestoneRef:    source.ActiveMilestoneRef,
		AssignedTodoBucketRef: source.AssignedTodoBucketRef,
		AssignedTodoRef:       source.AssignedTodoRef,
		Position:              position,
		CreatedAt:             now,
		UpdatedAt:             now,
	}
	if chatRecord.Title == "" {
		chatRecord.Title = "Fork"
	}
	if err := putChat(ctx, st, chatRecord); err != nil {
		return domain.Chat{}, err
	}
	for idx, item := range sourceTimeline[:anchorIdx+1] {
		copied, err := cloneTimelineItemForChat(item, chatRecord.ID, int64(idx+1), now)
		if err != nil {
			return domain.Chat{}, err
		}
		if err := putTimelineItem(ctx, st, copied); err != nil {
			return domain.Chat{}, err
		}
	}
	return chatRecord, nil
}

func cloneTimelineItemForChat(item domain.TimelineItem, chatID id.ID, seq int64, now time.Time) (domain.TimelineItem, error) {
	raw, err := json.Marshal(item)
	if err != nil {
		return domain.TimelineItem{}, fmt.Errorf("clone timeline item %s: %w", item.ID, err)
	}
	var cloned domain.TimelineItem
	if err := json.Unmarshal(raw, &cloned); err != nil {
		return domain.TimelineItem{}, fmt.Errorf("clone timeline item %s: %w", item.ID, err)
	}
	itemTime := now.Add(time.Duration(seq-1) * time.Nanosecond)
	cloned.ID = id.NewAt(itemTime)
	cloned.ChatID = chatID
	cloned.Seq = seq
	cloned.CreatedAt = itemTime
	cloned.UpdatedAt = itemTime
	if !cloned.SealedAt.IsZero() {
		cloned.SealedAt = itemTime
	}
	return cloned, nil
}

func cloneToolStates(src map[domain.ToolKind]bool) map[domain.ToolKind]bool {
	if len(src) == 0 {
		return map[domain.ToolKind]bool{}
	}
	out := make(map[domain.ToolKind]bool, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}

func getChat(ctx context.Context, st *store.Store, chatID id.ID) (domain.Chat, error) {
	return chatCollection(st).Get(ctx, chatID)
}

func putChat(ctx context.Context, st *store.Store, chatRecord domain.Chat) error {
	if chatRecord.ID == "" {
		return fmt.Errorf("put chat: id is required")
	}
	if chatRecord.SessionID == "" {
		return fmt.Errorf("put chat: session id is required")
	}
	return chatCollection(st).Put(ctx, chatRecord)
}

func updateChat(ctx context.Context, st *store.Store, chatRecord domain.Chat) error {
	existing, err := getChat(ctx, st, chatRecord.ID)
	if err != nil {
		return err
	}
	if chatRecord.Position == 0 && existing.Position != 0 && chatRecord.UpdatedAt.After(existing.UpdatedAt) {
		chatRecord.Position = existing.Position
	}
	return putChat(ctx, st, chatRecord)
}

func setChatQueuedInputs(ctx context.Context, st *store.Store, chatID id.ID, items []domain.QueuedInput) error {
	chatRecord, err := getChat(ctx, st, chatID)
	if err != nil {
		return err
	}
	chatRecord.QueuedInputs = storageCloneQueuedInputs(items)
	chatRecord.UpdatedAt = time.Now().UTC()
	return putChat(ctx, st, chatRecord)
}

func storageCloneQueuedInputs(src []domain.QueuedInput) []domain.QueuedInput {
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

func timelineForChat(ctx context.Context, st *store.Store, chatID id.ID) ([]domain.TimelineItem, error) {
	items, err := timelineCollection(st).List(ctx, store.ByIndex[domain.TimelineItem]("chat", string(chatID)))
	if err != nil {
		return nil, err
	}
	sortTimeline(items)
	return items, nil
}

type TimelinePage struct {
	Items     []domain.TimelineItem
	HasMore   bool
	Before    id.ID
	LoadedAll bool
	Total     int
}

func timelinePageForChat(ctx context.Context, st *store.Store, chatID, before id.ID, limit int, all bool) (TimelinePage, error) {
	items, err := timelineForChat(ctx, st, chatID)
	if err != nil {
		return TimelinePage{}, err
	}
	total := len(items)
	if all || limit <= 0 || total <= limit {
		return timelinePage(items, false, true, total), nil
	}
	end := total
	if before != "" {
		idx := slices.IndexFunc(items, func(item domain.TimelineItem) bool {
			return item.ID == before
		})
		if idx >= 0 {
			end = idx
		}
	}
	if end <= 0 {
		return TimelinePage{LoadedAll: true, Total: total}, nil
	}
	start := max(0, end-limit)
	return timelinePage(items[start:end], start > 0, false, total), nil
}

func timelinePage(items []domain.TimelineItem, hasMore, loadedAll bool, total int) TimelinePage {
	page := TimelinePage{
		Items:     slices.Clone(items),
		HasMore:   hasMore,
		LoadedAll: loadedAll,
		Total:     total,
	}
	if len(page.Items) > 0 {
		page.Before = page.Items[0].ID
	}
	return page
}

func sortTimeline(items []domain.TimelineItem) {
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

func putTimelineItem(ctx context.Context, st *store.Store, item domain.TimelineItem) error {
	return timelineCollection(st).Put(ctx, item)
}

func deleteTimelineItem(ctx context.Context, st *store.Store, itemID id.ID) error {
	return timelineCollection(st).Delete(ctx, itemID)
}

func DeletePersistedData(ctx context.Context, st *store.Store, chatID id.ID) error {
	if chatID == "" {
		return fmt.Errorf("delete chat persisted data: chat id is required")
	}
	items, err := timelineForChat(ctx, st, chatID)
	if err != nil {
		return err
	}
	for _, item := range items {
		if err := timelineCollection(st).Delete(ctx, item.ID); err != nil {
			return err
		}
	}
	approvals, err := approvalCollection(st).List(ctx, store.ByIndex[Approval]("chat", string(chatID)))
	if err != nil {
		return err
	}
	for _, approval := range approvals {
		if err := approvalCollection(st).Delete(ctx, approval.ID); err != nil {
			return err
		}
	}
	return nil
}

func insertTimelineItem(ctx context.Context, st *store.Store, item domain.TimelineItem) (domain.TimelineItem, error) {
	return timelineCollection(st).Insert(ctx, item)
}

func appendTimeline(ctx context.Context, st *store.Store, chatID id.ID, content domain.TimelineContent) (domain.TimelineItem, error) {
	if chatID == "" {
		return domain.TimelineItem{}, fmt.Errorf("append timeline: chat id is required")
	}
	if content == nil {
		return domain.TimelineItem{}, fmt.Errorf("append timeline: content is required")
	}
	unlock := store.LockTimelineMutation()
	defer unlock()
	items, err := timelineForChat(ctx, st, chatID)
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
	return insertTimelineItem(ctx, st, item)
}

func attachToolResult(ctx context.Context, st *store.Store, chatID id.ID, toolCallID string, result domain.ToolResult) (domain.TimelineItem, error) {
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

func attachToolError(ctx context.Context, st *store.Store, chatID id.ID, toolCallID string, toolErr domain.ToolError) (domain.TimelineItem, error) {
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

func failInterruptedToolCalls(ctx context.Context, st *store.Store, chatID id.ID, message string) (int, error) {
	return failToolCallsMatching(ctx, st, chatID, message, interruptedToolStatus)
}

func failRunningToolCalls(ctx context.Context, st *store.Store, chatID id.ID, message string) (int, error) {
	return failToolCallsMatching(ctx, st, chatID, message, func(status domain.ToolStatus) bool {
		return status == domain.ToolStatusRunning
	})
}

func failToolCallsMatching(ctx context.Context, st *store.Store, chatID id.ID, message string, match func(domain.ToolStatus) bool) (int, error) {
	if chatID == "" || match == nil {
		return 0, nil
	}
	message = strings.TrimSpace(message)
	if message == "" {
		message = "Tool execution failed because koder restarted before the tool completed."
	}
	unlock := store.LockTimelineMutation()
	defer unlock()
	items, err := timelineForChat(ctx, st, chatID)
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
		if err := putTimelineItem(ctx, st, item); err != nil {
			return count, err
		}
	}
	return count, nil
}

func interruptedToolStatus(status domain.ToolStatus) bool {
	return status == domain.ToolStatusPending || status == domain.ToolStatusRunning
}

func attachToolApproval(ctx context.Context, st *store.Store, chatID id.ID, toolCallID string, approval domain.ApprovalRequest) (domain.TimelineItem, error) {
	_ = approval
	return updateToolCall(ctx, st, chatID, toolCallID, func(call *domain.ToolCall) error {
		call.Approval = nil
		call.ApprovalID = strings.TrimSpace(toolCallID)
		call.Status = domain.ToolStatusAwaitingApproval
		return nil
	})
}

func markToolRunning(ctx context.Context, st *store.Store, chatID id.ID, toolCallID string) (domain.TimelineItem, error) {
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

func updateToolCall(ctx context.Context, st *store.Store, chatID id.ID, toolCallID string, update func(*domain.ToolCall) error) (domain.TimelineItem, error) {
	toolCallID = strings.TrimSpace(toolCallID)
	if chatID == "" {
		return domain.TimelineItem{}, fmt.Errorf("update tool call: chat id is required")
	}
	if toolCallID == "" {
		return domain.TimelineItem{}, fmt.Errorf("update tool call: tool call id is required")
	}
	unlock := store.LockTimelineMutation()
	defer unlock()
	items, err := timelineForChat(ctx, st, chatID)
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
		if err := putTimelineItem(ctx, st, item); err != nil {
			return domain.TimelineItem{}, err
		}
		return item, nil
	}
	return domain.TimelineItem{}, fmt.Errorf("tool call %q has no owning assistant item", toolCallID)
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

func pendingApprovalsForChat(ctx context.Context, st *store.Store, chatID id.ID) ([]Approval, error) {
	chatRecord, err := getChat(ctx, st, chatID)
	if err != nil {
		return nil, err
	}
	items, err := timelineForChat(ctx, st, chatID)
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

func SyntheticApprovalID(toolCallID string) id.ID {
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
