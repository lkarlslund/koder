package session

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/accesssettings"
	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/planning"
	"github.com/lkarlslund/koder/internal/store"
)

type approval struct {
	ID         id.ID
	SessionID  id.ID
	ChatID     id.ID
	Tool       domain.ToolKind
	ToolCallID string
	Command    string
	Status     domain.ApprovalStatus
	CreatedAt  time.Time
}

func SessionCollection(st *store.Store) store.Collection[domain.Session] {
	return store.NewCollection(st, store.CollectionSpec[domain.Session]{
		Namespace: "sessions",
		GetID:     func(v domain.Session) string { return v.ID },
		SetID:     func(v *domain.Session, id string) { v.ID = id },
	})
}

func planCollection(st *store.Store) store.Collection[planning.Plan] {
	return store.NewCollection(st, store.CollectionSpec[planning.Plan]{
		Namespace: "milestone-plans",
		GetID:     func(v planning.Plan) string { return v.SessionID },
		SetID:     func(v *planning.Plan, id string) { v.SessionID = id },
	})
}

func todoCollection(st *store.Store) store.Collection[planning.TodoItem] {
	return store.NewCollection(st, store.CollectionSpec[planning.TodoItem]{
		Namespace: "todos",
		GetID:     func(v planning.TodoItem) string { return v.ID },
		SetID:     func(v *planning.TodoItem, id string) { v.ID = id },
		Indexes: []store.IndexSpec[planning.TodoItem]{
			{Name: "session", Value: func(v planning.TodoItem) string { return v.SessionID }},
			{Name: "milestone", Value: func(v planning.TodoItem) string { return v.SessionID + "/" + v.MilestoneRef }},
		},
	})
}

func taskCollection(st *store.Store) store.Collection[planning.Task] {
	return store.NewCollection(st, store.CollectionSpec[planning.Task]{
		Namespace: "tasks",
		GetID:     func(v planning.Task) string { return v.ID },
		SetID:     func(v *planning.Task, id string) { v.ID = id },
		Indexes: []store.IndexSpec[planning.Task]{
			{Name: "session", Value: func(v planning.Task) string { return v.SessionID }},
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

func approvalCollection(st *store.Store) store.Collection[approval] {
	return store.NewCollection(st, store.CollectionSpec[approval]{
		Namespace: "approvals",
		GetID:     func(v approval) string { return v.ID },
		SetID:     func(v *approval, id string) { v.ID = id },
		Indexes: []store.IndexSpec[approval]{
			{Name: "session", Value: func(v approval) string { return v.SessionID }},
			{Name: "chat", Value: func(v approval) string { return v.ChatID }},
			{Name: "status", Value: func(v approval) string { return v.Status.String() }},
		},
	})
}

func EnsureSession(ctx context.Context, st *store.Store, providerID, modelID string) (domain.Session, error) {
	sessions, err := ListSessions(ctx, st)
	if err != nil {
		return domain.Session{}, err
	}
	if len(sessions) > 0 {
		return sessions[0], nil
	}
	return CreateSession(ctx, st, "New Session", providerID, modelID, nil)
}

func CreateSession(ctx context.Context, st *store.Store, title, providerID, modelID string, parentID *id.ID) (domain.Session, error) {
	now := time.Now().UTC()
	session := domain.Session{
		ID:                id.NewAt(now),
		ParentID:          parentID,
		Title:             title,
		PermissionProfile: "",
		PermissionRules:   nil,
		ToolStates:        map[domain.ToolKind]bool{},
		AccessSettings:    accesssettings.Default(),
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	chatRecord := domain.Chat{
		ID:                id.NewAt(now),
		SessionID:         session.ID,
		Title:             "Main",
		WorkflowRole:      chatrole.Orchestrator,
		ProviderID:        providerID,
		ModelID:           modelID,
		PermissionProfile: session.PermissionProfile,
		ToolStates:        map[domain.ToolKind]bool{},
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := SessionCollection(st).Put(ctx, session); err != nil {
		return domain.Session{}, err
	}
	if err := chatCollection(st).Put(ctx, chatRecord); err != nil {
		return domain.Session{}, err
	}
	return session, nil
}

func ListSessions(ctx context.Context, st *store.Store) ([]domain.Session, error) {
	sessions, err := SessionCollection(st).List(ctx, store.All[domain.Session]())
	if err != nil {
		return nil, err
	}
	slices.SortFunc(sessions, func(a, b domain.Session) int {
		switch {
		case a.UpdatedAt.After(b.UpdatedAt):
			return -1
		case a.UpdatedAt.Before(b.UpdatedAt):
			return 1
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		default:
			return 0
		}
	})
	return sessions, nil
}

func GetSession(ctx context.Context, st *store.Store, sessionID id.ID) (domain.Session, error) {
	return SessionCollection(st).Get(ctx, sessionID)
}

func PutSession(ctx context.Context, st *store.Store, session domain.Session) error {
	if session.ID == "" {
		return fmt.Errorf("put session: id is required")
	}
	return SessionCollection(st).Put(ctx, session)
}

func TouchSession(ctx context.Context, st *store.Store, sessionID id.ID) (domain.Session, error) {
	session, err := GetSession(ctx, st, sessionID)
	if err != nil {
		return domain.Session{}, err
	}
	session.UpdatedAt = time.Now().UTC()
	if err := PutSession(ctx, st, session); err != nil {
		return domain.Session{}, err
	}
	return session, nil
}

func PutPlan(ctx context.Context, st *store.Store, plan planning.Plan) error {
	if plan.SessionID == "" {
		return fmt.Errorf("put milestone plan: session id is required")
	}
	if plan.UpdatedAt.IsZero() {
		plan.UpdatedAt = time.Now().UTC()
	}
	return planCollection(st).Put(ctx, plan)
}

func GetPlan(ctx context.Context, st *store.Store, sessionID id.ID) (planning.Plan, error) {
	plan, err := planCollection(st).Get(ctx, sessionID)
	if err != nil {
		return planning.Plan{SessionID: sessionID}, nil
	}
	return plan, nil
}

func PutTodo(ctx context.Context, st *store.Store, item planning.TodoItem) error {
	if item.ID == "" {
		return fmt.Errorf("put task: id is required")
	}
	if item.SessionID == "" {
		return fmt.Errorf("put task: session id is required")
	}
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = time.Now().UTC()
	}
	return todoCollection(st).Put(ctx, item)
}

func GetTodo(ctx context.Context, st *store.Store, todoID id.ID) (planning.TodoItem, error) {
	return todoCollection(st).Get(ctx, todoID)
}

func ListTodos(ctx context.Context, st *store.Store, sessionID id.ID, milestoneRef string) ([]planning.TodoItem, error) {
	query := store.ByIndex[planning.TodoItem]("session", string(sessionID))
	milestoneRef = strings.TrimSpace(milestoneRef)
	if milestoneRef != "" {
		query = store.ByIndex[planning.TodoItem]("milestone", string(sessionID)+"/"+milestoneRef)
	}
	items, err := todoCollection(st).List(ctx, query)
	if err != nil {
		return nil, err
	}
	planning.SortTodos(items)
	return items, nil
}

func AddTodoItems(ctx context.Context, st *store.Store, sessionID id.ID, milestoneRef string, contents []string) ([]planning.TodoItem, error) {
	now := time.Now().UTC()
	milestoneRef = strings.TrimSpace(milestoneRef)
	existing, err := ListTodos(ctx, st, sessionID, milestoneRef)
	if err != nil {
		return nil, err
	}
	items := make([]planning.TodoItem, 0, len(contents))
	for _, content := range contents {
		content = strings.TrimSpace(content)
		if content == "" {
			continue
		}
		items = append(items, planning.TodoItem{
			ID:           id.NewAt(now),
			SessionID:    sessionID,
			MilestoneRef: milestoneRef,
			Content:      content,
			Status:       planning.TodoStatusPending,
			Position:     len(existing) + len(items),
			CreatedAt:    now,
			UpdatedAt:    now,
		})
	}
	for _, item := range items {
		if err := PutTodo(ctx, st, item); err != nil {
			return nil, err
		}
	}
	return items, nil
}

func UpdateTodo(ctx context.Context, st *store.Store, todoID id.ID, status planning.TodoStatus, content, note string) (planning.TodoItem, error) {
	item, err := todoCollection(st).Get(ctx, todoID)
	if err != nil {
		return planning.TodoItem{}, err
	}
	item.Status = status
	if strings.TrimSpace(content) != "" {
		item.Content = strings.TrimSpace(content)
	}
	if strings.TrimSpace(note) != "" {
		item.Note = strings.TrimSpace(note)
	}
	item.UpdatedAt = time.Now().UTC()
	if err := PutTodo(ctx, st, item); err != nil {
		return planning.TodoItem{}, err
	}
	return item, nil
}

func PutTask(ctx context.Context, st *store.Store, task planning.Task) error {
	if task.ID == "" {
		return fmt.Errorf("put task: id is required")
	}
	if task.SessionID == "" {
		return fmt.Errorf("put task: session id is required")
	}
	return taskCollection(st).Put(ctx, task)
}

func ListTasks(ctx context.Context, st *store.Store, sessionID id.ID) ([]planning.Task, error) {
	items, err := taskCollection(st).List(ctx, store.ByIndex[planning.Task]("session", string(sessionID)))
	if err != nil {
		return nil, err
	}
	slices.SortFunc(items, func(a, b planning.Task) int {
		switch {
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
	return items, nil
}

func ListChats(ctx context.Context, st *store.Store, sessionID id.ID) ([]domain.Chat, error) {
	chats, err := chatCollection(st).List(ctx, store.ByIndex[domain.Chat]("session", string(sessionID)))
	if err != nil {
		return nil, err
	}
	sortChatsForSidebar(chats)
	return chats, nil
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

func timelineForChat(ctx context.Context, st *store.Store, chatID id.ID) ([]domain.TimelineItem, error) {
	items, err := timelineCollection(st).List(ctx, store.ByIndex[domain.TimelineItem]("chat", string(chatID)))
	if err != nil {
		return nil, err
	}
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
	return items, nil
}

func putTimelineItem(ctx context.Context, st *store.Store, item domain.TimelineItem) error {
	if item.ID == "" {
		return fmt.Errorf("put timeline item: id is required")
	}
	if item.ChatID == "" {
		return fmt.Errorf("put timeline item: chat id is required")
	}
	return timelineCollection(st).Put(ctx, item)
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

func DefaultChat(ctx context.Context, st *store.Store, sessionID id.ID) (domain.Chat, error) {
	chats, err := ListChats(ctx, st, sessionID)
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

func CreateChat(ctx context.Context, st *store.Store, sessionID id.ID, title string, role domain.WorkflowRole, parentID *id.ID) (domain.Chat, error) {
	session, err := GetSession(ctx, st, sessionID)
	if err != nil {
		return domain.Chat{}, err
	}
	chats, err := ListChats(ctx, st, sessionID)
	if err != nil {
		return domain.Chat{}, err
	}
	now := time.Now().UTC()
	chatRecord := domain.Chat{
		ID:                id.NewAt(now),
		SessionID:         sessionID,
		ParentChatID:      parentID,
		Title:             strings.TrimSpace(title),
		WorkflowRole:      role,
		PermissionProfile: session.PermissionProfile,
		ToolStates:        map[domain.ToolKind]bool{},
		Position:          len(chats),
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if chatRecord.Title == "" {
		chatRecord.Title = "New Chat"
	}
	if chatRecord.WorkflowRole == "" {
		chatRecord.WorkflowRole = chatrole.General
	}
	if defaultChat, err := DefaultChat(ctx, st, sessionID); err == nil {
		chatRecord.ProviderID = defaultChat.ProviderID
		chatRecord.ModelID = defaultChat.ModelID
	}
	if err := putChat(ctx, st, chatRecord); err != nil {
		return domain.Chat{}, err
	}
	return chatRecord, nil
}

func ReorderChats(ctx context.Context, st *store.Store, sessionID id.ID, orderedIDs []id.ID) ([]domain.Chat, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("reorder chats: session id is required")
	}
	chats, err := ListChats(ctx, st, sessionID)
	if err != nil {
		return nil, err
	}
	if len(orderedIDs) != len(chats) {
		return nil, fmt.Errorf("reorder chats: expected %d chat ids, got %d", len(chats), len(orderedIDs))
	}
	byID := make(map[id.ID]domain.Chat, len(chats))
	for _, chatRecord := range chats {
		byID[chatRecord.ID] = chatRecord
	}
	seen := make(map[id.ID]bool, len(orderedIDs))
	ordered := make([]domain.Chat, 0, len(orderedIDs))
	for idx, chatID := range orderedIDs {
		if chatID == "" {
			return nil, fmt.Errorf("reorder chats: empty chat id at position %d", idx)
		}
		if seen[chatID] {
			return nil, fmt.Errorf("reorder chats: duplicate chat id %s", chatID)
		}
		chatRecord, ok := byID[chatID]
		if !ok {
			return nil, fmt.Errorf("reorder chats: chat %s not found in session %s", chatID, sessionID)
		}
		seen[chatID] = true
		chatRecord.Position = idx
		ordered = append(ordered, chatRecord)
	}
	for _, chatRecord := range ordered {
		if err := updateChat(ctx, st, chatRecord); err != nil {
			return nil, err
		}
	}
	return ordered, nil
}

func UpdateSession(ctx context.Context, st *store.Store, sessionID id.ID, update func(*domain.Session)) error {
	session, err := GetSession(ctx, st, sessionID)
	if err != nil {
		return err
	}
	update(&session)
	session.UpdatedAt = time.Now().UTC()
	return PutSession(ctx, st, session)
}

func DeleteSession(ctx context.Context, st *store.Store, sessionID id.ID) error {
	if sessionID == "" {
		return fmt.Errorf("delete session: session id is required")
	}
	chats, err := ListChats(ctx, st, sessionID)
	if err != nil {
		return err
	}
	for _, chatRecord := range chats {
		timeline, err := timelineForChat(ctx, st, chatRecord.ID)
		if err != nil {
			return err
		}
		for _, item := range timeline {
			if err := timelineCollection(st).Delete(ctx, item.ID); err != nil {
				return err
			}
		}
		approvals, err := approvalCollection(st).List(ctx, store.ByIndex[approval]("chat", string(chatRecord.ID)))
		if err != nil {
			return err
		}
		for _, approval := range approvals {
			if err := approvalCollection(st).Delete(ctx, approval.ID); err != nil {
				return err
			}
		}
		if err := chatCollection(st).Delete(ctx, chatRecord.ID); err != nil {
			return err
		}
	}
	if tasks, err := ListTasks(ctx, st, sessionID); err != nil {
		return err
	} else {
		for _, task := range tasks {
			if err := taskCollection(st).Delete(ctx, task.ID); err != nil {
				return err
			}
		}
	}
	if todos, err := ListTodos(ctx, st, sessionID, ""); err != nil {
		return err
	} else {
		for _, todo := range todos {
			if err := todoCollection(st).Delete(ctx, todo.ID); err != nil {
				return err
			}
		}
	}
	_ = planCollection(st).Delete(ctx, sessionID)
	return SessionCollection(st).Delete(ctx, sessionID)
}

func sortChatsForSidebar(chats []domain.Chat) {
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

func AppendPermissionRule(rules []accesssettings.PermissionOverride, rule accesssettings.PermissionOverride) []accesssettings.PermissionOverride {
	rule.Pattern = strings.TrimSpace(rule.Pattern)
	if rule.Pattern == "" {
		rule.Pattern = "*"
	}
	next := make([]accesssettings.PermissionOverride, 0, len(rules)+1)
	for _, existing := range rules {
		if existing.Tool == rule.Tool && strings.TrimSpace(existing.Pattern) == rule.Pattern {
			continue
		}
		next = append(next, existing)
	}
	return append(next, rule)
}
