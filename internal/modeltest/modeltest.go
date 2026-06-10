package modeltest

import (
	"context"
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

func SessionCollection(st *store.Store) store.Collection[domain.Session] {
	return store.NewCollection(st, store.CollectionSpec[domain.Session]{
		Namespace: "sessions",
		GetID:     func(v domain.Session) string { return v.ID },
		SetID:     func(v *domain.Session, id string) { v.ID = id },
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

func PlanCollection(st *store.Store) store.Collection[planning.Plan] {
	return store.NewCollection(st, store.CollectionSpec[planning.Plan]{
		Namespace: "milestone-plans",
		GetID:     func(v planning.Plan) string { return v.SessionID },
		SetID:     func(v *planning.Plan, id string) { v.SessionID = id },
	})
}

func TodoCollection(st *store.Store) store.Collection[planning.TodoItem] {
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

func TaskCollection(st *store.Store) store.Collection[planning.Task] {
	return store.NewCollection(st, store.CollectionSpec[planning.Task]{
		Namespace: "tasks",
		GetID:     func(v planning.Task) string { return v.ID },
		SetID:     func(v *planning.Task, id string) { v.ID = id },
		Indexes: []store.IndexSpec[planning.Task]{
			{Name: "session", Value: func(v planning.Task) string { return v.SessionID }},
		},
	})
}

func CreateSession(ctx context.Context, st *store.Store, title, providerID, modelID string, parentID *id.ID) (domain.Session, error) {
	now := time.Now().UTC()
	session := domain.Session{
		ID:              id.NewAt(now),
		ParentID:        parentID,
		Title:           strings.TrimSpace(title),
		ToolStates:      map[domain.ToolKind]bool{},
		AccessSettings:  accesssettings.Default(),
		CreatedAt:       now,
		UpdatedAt:       now,
		PermissionRules: nil,
	}
	if session.Title == "" {
		session.Title = "test"
	}
	chat := domain.Chat{
		ID:           id.NewAt(now),
		SessionID:    session.ID,
		Title:        "Main",
		WorkflowRole: chatrole.Orchestrator,
		ProviderID:   providerID,
		ModelID:      modelID,
		ToolStates:   map[domain.ToolKind]bool{},
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := SessionCollection(st).Put(ctx, session); err != nil {
		return domain.Session{}, err
	}
	if err := ChatCollection(st).Put(ctx, chat); err != nil {
		return domain.Session{}, err
	}
	return session, nil
}

func GetSession(ctx context.Context, st *store.Store, sessionID id.ID) (domain.Session, error) {
	return SessionCollection(st).Get(ctx, sessionID)
}

func PutSession(ctx context.Context, st *store.Store, session domain.Session) error {
	return SessionCollection(st).Put(ctx, session)
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

func DefaultChat(ctx context.Context, st *store.Store, sessionID id.ID) (domain.Chat, error) {
	chats, err := ListChats(ctx, st, sessionID)
	if err != nil {
		return domain.Chat{}, err
	}
	for _, chat := range chats {
		if chat.ParentChatID == nil {
			return chat, nil
		}
	}
	if len(chats) == 0 {
		return domain.Chat{}, fmt.Errorf("session %s has no chats", sessionID)
	}
	return chats[0], nil
}

func CreateChat(ctx context.Context, st *store.Store, sessionID id.ID, title string, role domain.WorkflowRole, parentID *id.ID) (domain.Chat, error) {
	chats, err := ListChats(ctx, st, sessionID)
	if err != nil {
		return domain.Chat{}, err
	}
	now := time.Now().UTC()
	chat := domain.Chat{ID: id.NewAt(now), SessionID: sessionID, ParentChatID: parentID, Title: title, WorkflowRole: role, Position: len(chats), CreatedAt: now, UpdatedAt: now}
	if chat.Title == "" {
		chat.Title = "New Chat"
	}
	if chat.WorkflowRole == "" {
		chat.WorkflowRole = chatrole.General
	}
	if err := ChatCollection(st).Put(ctx, chat); err != nil {
		return domain.Chat{}, err
	}
	return chat, nil
}

func ListChats(ctx context.Context, st *store.Store, sessionID id.ID) ([]domain.Chat, error) {
	chats, err := ChatCollection(st).List(ctx, store.ByIndex[domain.Chat]("session", string(sessionID)))
	if err != nil {
		return nil, err
	}
	slices.SortFunc(chats, func(a, b domain.Chat) int {
		if a.Position != b.Position {
			return a.Position - b.Position
		}
		return strings.Compare(string(a.ID), string(b.ID))
	})
	return chats, nil
}

func PutPlan(ctx context.Context, st *store.Store, plan planning.Plan) error {
	if plan.UpdatedAt.IsZero() {
		plan.UpdatedAt = time.Now().UTC()
	}
	plan, _ = planning.NormalizePlanKeys(plan)
	return PlanCollection(st).Put(ctx, plan)
}

func GetPlan(ctx context.Context, st *store.Store, sessionID id.ID) (planning.Plan, error) {
	plan, err := PlanCollection(st).Get(ctx, sessionID)
	if err != nil {
		return planning.Plan{SessionID: sessionID}, nil
	}
	return plan, nil
}

func AddTodoItems(ctx context.Context, st *store.Store, sessionID id.ID, ref string, contents []string) ([]planning.TodoItem, error) {
	existing, err := ListTodos(ctx, st, sessionID, ref)
	if err != nil {
		return nil, err
	}
	all, err := ListTodos(ctx, st, sessionID, "")
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	items := make([]planning.TodoItem, 0, len(contents))
	nextKey := nextTodoKey(all)
	for _, content := range contents {
		content = strings.TrimSpace(content)
		if content == "" {
			continue
		}
		items = append(items, planning.TodoItem{ID: id.NewAt(now), Key: nextKey, SessionID: sessionID, MilestoneRef: ref, Content: content, Status: planning.TodoStatusPending, Position: len(existing) + len(items), CreatedAt: now, UpdatedAt: now})
		nextKey = incrementPlanningKey(nextKey, "T")
	}
	for _, item := range items {
		if err := TodoCollection(st).Put(ctx, item); err != nil {
			return nil, err
		}
	}
	return items, nil
}

func PutTodo(ctx context.Context, st *store.Store, item planning.TodoItem) error {
	return TodoCollection(st).Put(ctx, item)
}

func GetTodo(ctx context.Context, st *store.Store, todoID id.ID) (planning.TodoItem, error) {
	if strings.HasPrefix(strings.TrimSpace(string(todoID)), "T") {
		items, err := TodoCollection(st).List(ctx, store.All[planning.TodoItem]())
		if err != nil {
			return planning.TodoItem{}, err
		}
		for _, item := range items {
			if planning.TodoKey(item) == string(todoID) {
				return item, nil
			}
		}
	}
	return TodoCollection(st).Get(ctx, todoID)
}

func UpdateTodo(ctx context.Context, st *store.Store, todoID id.ID, status planning.TodoStatus, content, note string) (planning.TodoItem, error) {
	item, err := GetTodo(ctx, st, todoID)
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

func ListTodos(ctx context.Context, st *store.Store, sessionID id.ID, ref string) ([]planning.TodoItem, error) {
	query := store.ByIndex[planning.TodoItem]("session", string(sessionID))
	if strings.TrimSpace(ref) != "" {
		query = store.ByIndex[planning.TodoItem]("milestone", string(sessionID)+"/"+strings.TrimSpace(ref))
	}
	items, err := TodoCollection(st).List(ctx, query)
	if err != nil {
		return nil, err
	}
	planning.SortTodos(items)
	return items, nil
}

func nextTodoKey(items []planning.TodoItem) string {
	next := 1
	for _, item := range items {
		key := strings.TrimSpace(item.Key)
		if !strings.HasPrefix(key, "T") {
			continue
		}
		var n int
		if _, err := fmt.Sscanf(strings.TrimPrefix(key, "T"), "%d", &n); err == nil && n >= next {
			next = n + 1
		}
	}
	return fmt.Sprintf("T%03d", next)
}

func incrementPlanningKey(key, prefix string) string {
	var n int
	if _, err := fmt.Sscanf(strings.TrimPrefix(strings.TrimSpace(key), prefix), "%d", &n); err != nil || n <= 0 {
		return prefix + "001"
	}
	return fmt.Sprintf("%s%03d", prefix, n+1)
}

func AppendTimeline(ctx context.Context, st *store.Store, chatID id.ID, content domain.TimelineContent) (domain.TimelineItem, error) {
	items, err := TimelineCollection(st).List(ctx, store.ByIndex[domain.TimelineItem]("chat", string(chatID)))
	if err != nil {
		return domain.TimelineItem{}, err
	}
	now := time.Now().UTC()
	return TimelineCollection(st).Insert(ctx, domain.TimelineItem{ChatID: chatID, Seq: int64(len(items) + 1), Content: content, CreatedAt: now, UpdatedAt: now})
}

func TimelineForChat(ctx context.Context, st *store.Store, chatID id.ID) ([]domain.TimelineItem, error) {
	items, err := TimelineCollection(st).List(ctx, store.ByIndex[domain.TimelineItem]("chat", string(chatID)))
	if err != nil {
		return nil, err
	}
	slices.SortFunc(items, func(a, b domain.TimelineItem) int {
		if a.Seq != b.Seq {
			if a.Seq < b.Seq {
				return -1
			}
			return 1
		}
		return strings.Compare(string(a.ID), string(b.ID))
	})
	return items, nil
}

func PutTimelineItem(ctx context.Context, st *store.Store, item domain.TimelineItem) error {
	return TimelineCollection(st).Put(ctx, item)
}

func AppendAssistantToolCalls(ctx context.Context, st *store.Store, chatID id.ID, calls []domain.ToolCall, text string, usage domain.Usage) (domain.TimelineItem, error) {
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
	item, err := AppendTimeline(ctx, st, chatID, assistant)
	if err != nil {
		return domain.TimelineItem{}, err
	}
	item.Seal(time.Now().UTC())
	if err := PutTimelineItem(ctx, st, item); err != nil {
		return domain.TimelineItem{}, err
	}
	return item, nil
}

func SetChatQueuedInputs(ctx context.Context, st *store.Store, chatID id.ID, items []domain.QueuedInput) error {
	chat, err := ChatCollection(st).Get(ctx, chatID)
	if err != nil {
		return err
	}
	chat.QueuedInputs = append([]domain.QueuedInput(nil), items...)
	chat.UpdatedAt = time.Now().UTC()
	return ChatCollection(st).Put(ctx, chat)
}

func PutTask(ctx context.Context, st *store.Store, task planning.Task) error {
	return TaskCollection(st).Put(ctx, task)
}

func ListTasks(ctx context.Context, st *store.Store, sessionID id.ID) ([]planning.Task, error) {
	items, err := TaskCollection(st).List(ctx, store.ByIndex[planning.Task]("session", string(sessionID)))
	if err != nil {
		return nil, err
	}
	slices.SortFunc(items, func(a, b planning.Task) int {
		if !a.CreatedAt.Equal(b.CreatedAt) {
			if a.CreatedAt.Before(b.CreatedAt) {
				return -1
			}
			return 1
		}
		return strings.Compare(string(a.ID), string(b.ID))
	})
	return items, nil
}
