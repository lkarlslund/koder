package sessionstore

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/accesssettings"
	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/chatstore"
	"github.com/lkarlslund/koder/internal/domain"
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

func CreateSession(ctx context.Context, st *store.Store, title, providerID, modelID string, parentID *domain.ID) (domain.Session, error) {
	now := time.Now().UTC()
	session := domain.Session{
		ID:                domain.NewIDAt(now),
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
		ID:                domain.NewIDAt(now),
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
	if err := st.Transaction(ctx, func(tx *store.Tx) error {
		if err := SessionCollection(st).PutTx(tx, ctx, session); err != nil {
			return err
		}
		return chatstore.ChatCollection(st).PutTx(tx, ctx, chatRecord)
	}); err != nil {
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

func GetSession(ctx context.Context, st *store.Store, sessionID domain.ID) (domain.Session, error) {
	return SessionCollection(st).Get(ctx, sessionID)
}

func PutSession(ctx context.Context, st *store.Store, session domain.Session) error {
	if session.ID == "" {
		return fmt.Errorf("put session: id is required")
	}
	return SessionCollection(st).Put(ctx, session)
}

func TouchSession(ctx context.Context, st *store.Store, sessionID domain.ID) (domain.Session, error) {
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

func ListChats(ctx context.Context, st *store.Store, sessionID domain.ID) ([]domain.Chat, error) {
	chats, err := chatstore.ChatCollection(st).List(ctx, store.ByIndex[domain.Chat]("session", string(sessionID)))
	if err != nil {
		return nil, err
	}
	sortChatsForSidebar(chats)
	return chats, nil
}

func DefaultChat(ctx context.Context, st *store.Store, sessionID domain.ID) (domain.Chat, error) {
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

func CreateChat(ctx context.Context, st *store.Store, sessionID domain.ID, title string, role domain.WorkflowRole, parentID *domain.ID) (domain.Chat, error) {
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
		ID:                domain.NewIDAt(now),
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
	if err := chatstore.PutChat(ctx, st, chatRecord); err != nil {
		return domain.Chat{}, err
	}
	return chatRecord, nil
}

func ReorderChats(ctx context.Context, st *store.Store, sessionID domain.ID, orderedIDs []domain.ID) ([]domain.Chat, error) {
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
	byID := make(map[domain.ID]domain.Chat, len(chats))
	for _, chatRecord := range chats {
		byID[chatRecord.ID] = chatRecord
	}
	seen := make(map[domain.ID]bool, len(orderedIDs))
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
		if err := chatstore.UpdateChat(ctx, st, chatRecord); err != nil {
			return nil, err
		}
	}
	return ordered, nil
}

func UpdateSession(ctx context.Context, st *store.Store, sessionID domain.ID, update func(*domain.Session)) error {
	session, err := GetSession(ctx, st, sessionID)
	if err != nil {
		return err
	}
	update(&session)
	session.UpdatedAt = time.Now().UTC()
	return PutSession(ctx, st, session)
}

func DeleteSession(ctx context.Context, st *store.Store, sessionID domain.ID) error {
	if sessionID == "" {
		return fmt.Errorf("delete session: session id is required")
	}
	chats, err := ListChats(ctx, st, sessionID)
	if err != nil {
		return err
	}
	for _, chatRecord := range chats {
		timeline, err := chatstore.TimelineForChat(ctx, st, chatRecord.ID)
		if err != nil {
			return err
		}
		for _, item := range timeline {
			if err := chatstore.TimelineCollection(st).Delete(ctx, item.ID); err != nil {
				return err
			}
		}
		approvals, err := chatstore.ApprovalCollection(st).List(ctx, store.ByIndex[chatstore.Approval]("chat", string(chatRecord.ID)))
		if err != nil {
			return err
		}
		for _, approval := range approvals {
			if err := chatstore.ApprovalCollection(st).Delete(ctx, approval.ID); err != nil {
				return err
			}
		}
		if err := chatstore.ChatCollection(st).Delete(ctx, chatRecord.ID); err != nil {
			return err
		}
	}
	if tasks, err := planning.ListTasks(ctx, st, sessionID); err != nil {
		return err
	} else {
		for _, task := range tasks {
			if err := planning.TaskCollection(st).Delete(ctx, task.ID); err != nil {
				return err
			}
		}
	}
	if todos, err := planning.ListTodos(ctx, st, sessionID, ""); err != nil {
		return err
	} else {
		for _, todo := range todos {
			if err := planning.TodoCollection(st).Delete(ctx, todo.ID); err != nil {
				return err
			}
		}
	}
	_ = planning.PlanCollection(st).Delete(ctx, sessionID)
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

func AppendPermissionRule(rules []domain.PermissionOverride, rule domain.PermissionOverride) []domain.PermissionOverride {
	rule.Pattern = strings.TrimSpace(rule.Pattern)
	if rule.Pattern == "" {
		rule.Pattern = "*"
	}
	next := make([]domain.PermissionOverride, 0, len(rules)+1)
	for _, existing := range rules {
		if existing.Tool == rule.Tool && strings.TrimSpace(existing.Pattern) == rule.Pattern {
			continue
		}
		next = append(next, existing)
	}
	return append(next, rule)
}
