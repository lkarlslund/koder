package session

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lkarlslund/koder/internal/accesssettings"
	chatpkg "github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/planning"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
	"github.com/lkarlslund/koder/internal/tools/chattool"
)

// EventKind identifies a session-owned state mutation.
type EventKind string

const (
	EventChatAdded       EventKind = "chat_added"
	EventChatChanged     EventKind = "chat_changed"
	EventChatArchived    EventKind = "chat_archived"
	EventSessionChanged  EventKind = "session_changed"
	EventPlanningChanged EventKind = "planning_changed"
	EventTasksChanged    EventKind = "tasks_changed"
)

// Event reports a mutation made by the session owner.
type Event struct {
	Kind       EventKind
	SessionID  id.ID
	Chat       domain.Chat
	Snapshot   chatpkg.Snapshot
	Update     chatpkg.Update
	NextChatID id.ID
	Session    domain.Session
	Plan       planning.Plan
	Todos      []planning.TodoItem
	TodosByRef map[string][]planning.TodoItem
	Tasks      []planning.Task
	Err        error
}

// Session owns the live state for one persisted session.
type Session struct {
	store    *store.Store
	chatsSrc *chatpkg.Source
	planSrc  *planning.Source

	mu         sync.RWMutex
	session    domain.Session
	chats      []domain.Chat
	runtimes   map[id.ID]*chatpkg.Chat
	unsubs     map[id.ID]func()
	plan       planning.Plan
	todosByRef map[string][]planning.TodoItem
	tasks      []planning.Task

	subsMu  sync.Mutex
	nextSub int
	subs    map[int]chan Event
}

// Load hydrates a live session owner from persisted state.
func Load(ctx context.Context, st *store.Store, chatsSrc *chatpkg.Source, planSrc *planning.Source, sessionID id.ID) (*Session, error) {
	if st == nil {
		return nil, fmt.Errorf("store is required")
	}
	if chatsSrc == nil {
		return nil, fmt.Errorf("chat source is required")
	}
	if planSrc == nil {
		return nil, fmt.Errorf("planning source is required")
	}
	if sessionID == "" {
		return nil, fmt.Errorf("session id is required")
	}
	session, err := getSessionRecord(ctx, st, sessionID)
	if err != nil {
		return nil, err
	}
	chats, err := chatsSrc.ListRecordsForSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	plan, err := planSrc.LoadPlan(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	plan, planChanged := planning.NormalizePlanKeys(plan)
	if planChanged {
		if err := planSrc.SavePlan(ctx, plan); err != nil {
			return nil, err
		}
	}
	todosByRef, err := loadTodosByRef(ctx, planSrc, sessionID, plan)
	if err != nil {
		return nil, err
	}
	tasks, err := planSrc.ListTasks(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	owner := &Session{
		store:      st,
		chatsSrc:   chatsSrc,
		planSrc:    planSrc,
		session:    session,
		chats:      slices.Clone(chats),
		runtimes:   map[id.ID]*chatpkg.Chat{},
		unsubs:     map[id.ID]func(){},
		plan:       plan,
		todosByRef: todosByRef,
		tasks:      slices.Clone(tasks),
		subs:       map[int]chan Event{},
	}
	return owner, nil
}

func loadTodosByRef(ctx context.Context, planSrc *planning.Source, sessionID id.ID, plan planning.Plan) (map[string][]planning.TodoItem, error) {
	out := map[string][]planning.TodoItem{}
	seen := map[string]struct{}{}
	for _, milestone := range plan.Milestones {
		ref := strings.TrimSpace(planning.MilestoneKey(milestone))
		if ref == "" {
			continue
		}
		if _, ok := seen[ref]; ok {
			continue
		}
		seen[ref] = struct{}{}
		out[ref] = nil
	}
	items, err := planSrc.ListTodos(ctx, sessionID, "")
	if err != nil {
		return nil, err
	}
	items, changed := planning.NormalizeTodosKeys(items, planning.MilestoneKeyAliases(plan))
	if changed {
		for _, item := range items {
			if err := planSrc.SaveTodo(ctx, item); err != nil {
				return nil, err
			}
		}
	}
	for _, item := range items {
		ref := strings.TrimSpace(item.MilestoneRef)
		out[ref] = append(out[ref], item)
	}
	for ref, items := range out {
		planning.SortTodos(items)
		out[ref] = items
	}
	return out, nil
}

// SessionSnapshot is a detached view of a live session.
type SessionSnapshot struct {
	Session    domain.Session
	Chats      []domain.Chat
	Snapshots  map[id.ID]chatpkg.Snapshot
	Plan       planning.Plan
	Todos      []planning.TodoItem
	TodosByRef map[string][]planning.TodoItem
	Tasks      []planning.Task
}

// Snapshot returns a detached snapshot of the live session.
func (s *Session) Snapshot() SessionSnapshot {
	if s == nil {
		return SessionSnapshot{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	snapshots := make(map[id.ID]chatpkg.Snapshot, len(s.runtimes))
	for id, rt := range s.runtimes {
		if rt != nil {
			snapshots[id] = rt.Snapshot()
		}
	}
	return SessionSnapshot{
		Session:    s.session,
		Chats:      slices.Clone(s.chats),
		Snapshots:  snapshots,
		Plan:       cloneMilestonePlan(s.plan),
		Todos:      flattenTodos(s.todosByRef),
		TodosByRef: cloneTodosByRef(s.todosByRef),
		Tasks:      slices.Clone(s.tasks),
	}
}

// Subscribe registers for session-owned state mutations.
func (s *Session) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 128)
	if s == nil {
		close(ch)
		return ch, func() {}
	}
	s.subsMu.Lock()
	if s.subs == nil {
		s.subs = map[int]chan Event{}
	}
	id := s.nextSub
	s.nextSub++
	s.subs[id] = ch
	s.subsMu.Unlock()
	unsub := func() {
		s.subsMu.Lock()
		if existing, ok := s.subs[id]; ok {
			delete(s.subs, id)
			close(existing)
		}
		s.subsMu.Unlock()
	}
	return ch, unsub
}

func (s *Session) emit(event Event) {
	if s == nil {
		return
	}
	s.subsMu.Lock()
	defer s.subsMu.Unlock()
	for _, ch := range s.subs {
		ch <- event
	}
}

// Chat returns the live chat runtime owned by this session.
func (s *Session) Chat(ctx context.Context, chatID id.ID) (*chatpkg.Chat, error) {
	if s == nil {
		return nil, fmt.Errorf("session is required")
	}
	if chatID == "" {
		return nil, fmt.Errorf("chat id is required")
	}
	s.mu.RLock()
	if rt := s.runtimes[chatID]; rt != nil {
		s.mu.RUnlock()
		return rt, nil
	}
	session := s.session
	chatRecord, ok := chatByID(s.chats, chatID)
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("chat %s not found", chatID)
	}
	rt, err := s.chatsSrc.LoadMetadata(ctx, session, chatRecord)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.trackRuntimeLocked(chatID, rt)
	s.mu.Unlock()
	return rt, nil
}

func (s *Session) runtime(chatID id.ID) *chatpkg.Chat {
	if s == nil || chatID == "" {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.runtimes[chatID]
}

// TimelinePage returns persisted transcript items for a chat owned by this session.
func (s *Session) TimelinePage(ctx context.Context, chatID, before id.ID, limit int, all bool) (chatpkg.TimelinePage, error) {
	if s == nil {
		return chatpkg.TimelinePage{}, fmt.Errorf("session is required")
	}
	if chatID == "" {
		return chatpkg.TimelinePage{}, fmt.Errorf("chat id is required")
	}
	s.mu.RLock()
	_, ok := chatByID(s.chats, chatID)
	s.mu.RUnlock()
	if !ok {
		return chatpkg.TimelinePage{}, fmt.Errorf("chat %s not found", chatID)
	}
	if rt := s.runtime(chatID); rt != nil && rt.HasLoadedTimeline() {
		return rt.TimelinePage(ctx, before, limit, all)
	}
	return s.chatsSrc.TimelinePage(ctx, chatID, before, limit, all)
}

// NewChat creates a new orchestrator chat under parentChatID.
func (s *Session) NewChat(ctx context.Context, parentChatID id.ID, title string) (*chatpkg.Chat, error) {
	if s == nil {
		return nil, fmt.Errorf("session is required")
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Chat"
	}
	s.mu.RLock()
	session := s.session
	parent, ok := chatByID(s.chats, parentChatID)
	position := len(s.chats)
	s.mu.RUnlock()
	if session.ID == "" {
		return nil, fmt.Errorf("session id is required")
	}
	if !ok {
		return nil, fmt.Errorf("parent chat %s not found", parentChatID)
	}
	parentID := parent.ID
	now := time.Now().UTC()
	chatRecord := domain.Chat{
		ID:           id.New(),
		SessionID:    session.ID,
		ParentChatID: &parentID,
		Title:        title,
		WorkflowRole: chatrole.Orchestrator,
		ProviderID:   strings.TrimSpace(parent.ProviderID),
		ModelID:      strings.TrimSpace(parent.ModelID),
		ToolStates:   cloneToolStateMap(session.ToolStates),
		Position:     position,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	return s.createChat(ctx, session, chatRecord)
}

// ForkChatAt creates a sibling chat containing transcript items from the source
// chat start through anchorItemID, inclusive.
func (s *Session) ForkChatAt(ctx context.Context, sourceChatID, anchorItemID id.ID, title string) (*chatpkg.Chat, error) {
	if s == nil {
		return nil, fmt.Errorf("session is required")
	}
	if sourceChatID == "" {
		return nil, fmt.Errorf("source chat id is required")
	}
	if anchorItemID == "" {
		return nil, fmt.Errorf("anchor item id is required")
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Fork"
	}
	s.mu.RLock()
	session := s.session
	source, ok := chatByID(s.chats, sourceChatID)
	position := len(s.chats)
	s.mu.RUnlock()
	if session.ID == "" {
		return nil, fmt.Errorf("session id is required")
	}
	if !ok {
		return nil, fmt.Errorf("chat %s not found", sourceChatID)
	}
	if source.Archived {
		return nil, fmt.Errorf("cannot fork archived chat %s", sourceChatID)
	}
	sourceRuntime, err := s.Chat(ctx, sourceChatID)
	if err != nil {
		return nil, err
	}
	if err := sourceRuntime.EnsureTimeline(ctx); err != nil {
		return nil, err
	}
	sourceSnapshot := sourceRuntime.Snapshot()
	chatRecord, err := s.chatsSrc.ForkRecordAt(ctx, source, sourceSnapshot.Timeline, anchorItemID, title, position)
	if err != nil {
		return nil, err
	}
	rt, err := s.chatsSrc.LoadMetadata(ctx, session, chatRecord)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	upsertSessionChatLocked(&s.chats, chatRecord)
	s.trackRuntimeLocked(chatRecord.ID, rt)
	snapshot := rt.Snapshot()
	s.mu.Unlock()
	s.emit(Event{Kind: EventChatAdded, SessionID: session.ID, Chat: chatRecord, Snapshot: snapshot})
	return rt, nil
}

// AddPreparedChat adds an already validated chat record to the live session.
func (s *Session) AddPreparedChat(ctx context.Context, chatRecord domain.Chat) (*chatpkg.Chat, error) {
	if s == nil {
		return nil, fmt.Errorf("session is required")
	}
	if chatRecord.ID == "" {
		return nil, fmt.Errorf("chat id is required")
	}
	s.mu.RLock()
	session := s.session
	s.mu.RUnlock()
	if chatRecord.SessionID != session.ID {
		return nil, fmt.Errorf("chat %s does not belong to session %s", chatRecord.ID, session.ID)
	}
	return s.createChat(ctx, session, chatRecord)
}

func (s *Session) createChat(ctx context.Context, session domain.Session, chatRecord domain.Chat) (*chatpkg.Chat, error) {
	if session.ID == "" {
		return nil, fmt.Errorf("session id is required")
	}
	if chatRecord.ID == "" {
		return nil, fmt.Errorf("chat id is required")
	}
	if chatRecord.SessionID != session.ID {
		return nil, fmt.Errorf("chat %s does not belong to session %s", chatRecord.ID, session.ID)
	}
	if err := s.chatsSrc.PutRecord(ctx, chatRecord); err != nil {
		return nil, err
	}
	rt, err := s.chatsSrc.LoadMetadata(ctx, session, chatRecord)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	upsertSessionChatLocked(&s.chats, chatRecord)
	s.trackRuntimeLocked(chatRecord.ID, rt)
	snapshot := rt.Snapshot()
	s.mu.Unlock()
	s.emit(Event{Kind: EventChatAdded, SessionID: session.ID, Chat: chatRecord, Snapshot: snapshot})
	return rt, nil
}

func (s *Session) trackRuntimeLocked(chatID id.ID, rt *chatpkg.Chat) {
	if s.runtimes == nil {
		s.runtimes = map[id.ID]*chatpkg.Chat{}
	}
	if s.unsubs == nil {
		s.unsubs = map[id.ID]func(){}
	}
	if existing := s.runtimes[chatID]; existing == rt && s.unsubs[chatID] != nil {
		return
	}
	if unsub := s.unsubs[chatID]; unsub != nil {
		unsub()
	}
	updates, unsub := rt.Subscribe()
	s.runtimes[chatID] = rt
	s.unsubs[chatID] = unsub
	go s.forwardRuntime(chatID, updates)
}

func (s *Session) forwardRuntime(chatID id.ID, updates <-chan chatpkg.Update) {
	for update := range updates {
		if update.Snapshot.Chat.ID == "" {
			update.Snapshot.Chat.ID = chatID
		}
		s.mu.Lock()
		sessionID := s.session.ID
		chatRecord := update.Snapshot.Chat
		if chatRecord.ID == "" {
			chatRecord, _ = chatByID(s.chats, chatID)
			update.Snapshot.Chat = chatRecord
		}
		if chatRecord.ID != "" {
			if existing, ok := chatByID(s.chats, chatRecord.ID); ok && strings.TrimSpace(chatRecord.Title) == "" {
				chatRecord = existing
				update.Snapshot.Chat = existing
			}
			upsertSessionChatLocked(&s.chats, chatRecord)
		}
		s.mu.Unlock()
		s.emit(Event{
			Kind:      EventChatChanged,
			SessionID: sessionID,
			Chat:      chatRecord,
			Snapshot:  update.Snapshot,
			Update:    update,
		})
	}
}

// EnsureDefaultChat returns the newest chat, creating the session default when empty.
func (s *Session) EnsureDefaultChat(ctx context.Context) (domain.Chat, error) {
	if s == nil {
		return domain.Chat{}, fmt.Errorf("session is required")
	}
	s.mu.RLock()
	session := s.session
	chats := slices.Clone(s.chats)
	s.mu.RUnlock()
	if best := newestSessionChat(chats); best.ID != "" {
		return best, nil
	}
	chatRecord, err := s.chatsSrc.DefaultRecord(ctx, session.ID)
	if err != nil {
		return domain.Chat{}, err
	}
	s.mu.Lock()
	upsertSessionChatLocked(&s.chats, chatRecord)
	s.mu.Unlock()
	return chatRecord, nil
}

// UpdateSession mutates live and persisted session metadata.
func (s *Session) UpdateSession(ctx context.Context, update func(*domain.Session)) (domain.Session, error) {
	if s == nil {
		return domain.Session{}, fmt.Errorf("session is required")
	}
	if update == nil {
		return domain.Session{}, fmt.Errorf("session update is required")
	}
	s.mu.Lock()
	updated := s.session
	update(&updated)
	updated.UpdatedAt = time.Now().UTC()
	if err := putSessionRecord(ctx, s.store, updated); err != nil {
		s.mu.Unlock()
		return domain.Session{}, err
	}
	s.session = updated
	for _, rt := range s.runtimes {
		if rt != nil {
			rt.SetSession(updated)
		}
	}
	s.mu.Unlock()
	s.emit(Event{Kind: EventSessionChanged, SessionID: updated.ID, Session: updated})
	return updated, nil
}

// UpdateChat updates chat metadata, preserving its history.
func (s *Session) UpdateChat(ctx context.Context, chatID id.ID, update chattool.UpdateRequest) (chattool.Status, id.ID, error) {
	if s == nil {
		return chattool.Status{}, "", fmt.Errorf("session is required")
	}
	if chatID == "" {
		return chattool.Status{}, "", fmt.Errorf("chat id is required")
	}
	if update.Archived == nil && strings.TrimSpace(update.Title) == "" {
		return chattool.Status{}, "", fmt.Errorf("archived or title is required")
	}
	s.mu.RLock()
	session := s.session
	target, ok := chatByID(s.chats, chatID)
	if !ok {
		s.mu.RUnlock()
		return chattool.Status{}, "", fmt.Errorf("chat %s not found", chatID)
	}
	nextChatID := id.ID("")
	archivingVisibleChat := update.Archived != nil && *update.Archived && !target.Archived
	if archivingVisibleChat {
		nextChatID = fallbackVisibleChatID(s.chats, target)
	}
	s.mu.RUnlock()
	if archivingVisibleChat && nextChatID == "" {
		return chattool.Status{}, "", fmt.Errorf("cannot archive the only visible chat in a session")
	}
	s.mu.Lock()
	rt := s.runtimes[target.ID]
	s.mu.Unlock()
	if rt == nil {
		loaded, err := s.chatsSrc.LoadMetadata(ctx, session, target)
		if err != nil {
			return chattool.Status{}, "", err
		}
		s.mu.Lock()
		s.trackRuntimeLocked(target.ID, loaded)
		rt = loaded
		s.mu.Unlock()
	}
	updated, err := rt.UpdateMetadata(ctx, chatpkg.MetadataUpdate{
		Archived: update.Archived,
		Title:    update.Title,
	})
	if err != nil {
		return chattool.Status{}, "", err
	}
	target = updated
	s.mu.Lock()
	upsertSessionChatLocked(&s.chats, target)
	status := s.chatStatusLocked(target.ID)
	statusText := "Updated"
	if update.Archived != nil {
		if *update.Archived {
			statusText = "Archived"
		} else {
			statusText = "Restored"
		}
	}
	snapshot := chatpkg.Snapshot{Session: session, Chat: target, Status: chatpkg.StatusIdle, StatusText: statusText}
	if rt != nil {
		snapshot = rt.Snapshot()
		snapshot.Chat = target
		snapshot.StatusText = statusText
	}
	s.mu.Unlock()
	status.ID = target.ID
	status.Title = target.Title
	status.Role = target.WorkflowRole
	status.Archived = target.Archived
	status.ActiveMilestoneRef = target.ActiveMilestoneRef
	status.AssignedTodoRef = target.AssignedTodoRef
	status.StatusText = statusText
	kind := EventChatChanged
	if archivingVisibleChat {
		kind = EventChatArchived
	}
	s.emit(Event{Kind: kind, SessionID: target.SessionID, Chat: target, Snapshot: snapshot, NextChatID: nextChatID})
	return status, nextChatID, nil
}

// ReorderChats persists and applies the complete chat order.
func (s *Session) ReorderChats(ctx context.Context, ids []id.ID) ([]domain.Chat, error) {
	if s == nil {
		return nil, fmt.Errorf("session is required")
	}
	s.mu.Lock()
	sessionID := s.session.ID
	if len(ids) != len(s.chats) {
		s.mu.Unlock()
		return nil, fmt.Errorf("reorder chats: expected %d chat ids, got %d", len(s.chats), len(ids))
	}
	byID := make(map[id.ID]domain.Chat, len(s.chats))
	for _, chatRecord := range s.chats {
		byID[chatRecord.ID] = chatRecord
	}
	seen := make(map[id.ID]bool, len(ids))
	ordered := make([]domain.Chat, 0, len(ids))
	for idx, chatID := range ids {
		if chatID == "" {
			s.mu.Unlock()
			return nil, fmt.Errorf("reorder chats: empty chat id at position %d", idx)
		}
		if seen[chatID] {
			s.mu.Unlock()
			return nil, fmt.Errorf("reorder chats: duplicate chat id %s", chatID)
		}
		chatRecord, ok := byID[chatID]
		if !ok {
			s.mu.Unlock()
			return nil, fmt.Errorf("reorder chats: chat %s not found in session %s", chatID, sessionID)
		}
		seen[chatID] = true
		chatRecord.Position = idx
		if err := s.chatsSrc.UpdateRecord(ctx, chatRecord); err != nil {
			s.mu.Unlock()
			return nil, err
		}
		ordered = append(ordered, chatRecord)
	}
	s.chats = slices.Clone(ordered)
	for _, item := range ordered {
		if rt := s.runtimes[item.ID]; rt != nil {
			rt.SetChat(item)
		}
	}
	s.mu.Unlock()
	for _, item := range ordered {
		s.emit(Event{Kind: EventChatChanged, SessionID: sessionID, Chat: item, Snapshot: s.snapshotForChat(item.ID)})
	}
	return slices.Clone(ordered), nil
}

// Rename updates the live and persisted session title.
func (s *Session) Rename(ctx context.Context, title string) (domain.Session, error) {
	if s == nil {
		return domain.Session{}, fmt.Errorf("session is required")
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return domain.Session{}, fmt.Errorf("session title is required")
	}
	updated, err := s.UpdateSession(ctx, func(session *domain.Session) {
		session.Title = strings.TrimSpace(title)
		session.TitleGeneratedAt = time.Time{}
		session.TitleRefreshCount = 0
	})
	if err != nil {
		return domain.Session{}, err
	}
	slog.Info("session renamed", "session_id", updated.ID, "title", updated.Title)
	return updated, nil
}

// SetAccessSettings updates the session access settings and loaded runtimes.
func (s *Session) SetAccessSettings(ctx context.Context, settings accesssettings.Settings) (domain.Session, error) {
	if s == nil {
		return domain.Session{}, fmt.Errorf("session is required")
	}
	settings = accesssettings.Normalize(settings)
	if err := accesssettings.Validate(settings); err != nil {
		return domain.Session{}, err
	}
	updated, err := s.UpdateSession(ctx, func(session *domain.Session) { session.AccessSettings = settings })
	if err != nil {
		return domain.Session{}, err
	}
	slog.Info("session access settings stored", "session_id", updated.ID)
	return updated, nil
}

// SetChatModel persists the provider/model used by a chat and updates its runtime.
func (s *Session) SetChatModel(ctx context.Context, chatID id.ID, providerID, modelID string) (domain.Chat, error) {
	if s == nil {
		return domain.Chat{}, fmt.Errorf("session is required")
	}
	providerID = strings.TrimSpace(providerID)
	modelID = strings.TrimSpace(modelID)
	if providerID == "" {
		return domain.Chat{}, fmt.Errorf("provider id is required")
	}
	if modelID == "" {
		return domain.Chat{}, fmt.Errorf("model id is required")
	}
	s.mu.RLock()
	chatRecord, ok := chatByID(s.chats, chatID)
	s.mu.RUnlock()
	if !ok {
		return domain.Chat{}, fmt.Errorf("chat %s not found", chatID)
	}
	chatRecord.ProviderID = providerID
	chatRecord.ModelID = modelID
	chatRecord.UpdatedAt = time.Now().UTC()
	if err := s.chatsSrc.UpdateRecord(ctx, chatRecord); err != nil {
		return domain.Chat{}, err
	}
	s.mu.Lock()
	upsertSessionChatLocked(&s.chats, chatRecord)
	if rt := s.runtimes[chatID]; rt != nil {
		rt.SetChat(chatRecord)
	}
	snapshot := s.snapshotForChatLocked(chatID)
	s.mu.Unlock()
	s.emit(Event{Kind: EventChatChanged, SessionID: chatRecord.SessionID, Chat: chatRecord, Snapshot: snapshot})
	return chatRecord, nil
}

// EnsureChatModels fills missing chat provider/model fields from session defaults.
func (s *Session) EnsureChatModels(ctx context.Context, defaultProvider, defaultModel string) ([]domain.Chat, error) {
	if s == nil {
		return nil, fmt.Errorf("session is required")
	}
	defaultProvider = strings.TrimSpace(defaultProvider)
	defaultModel = strings.TrimSpace(defaultModel)
	s.mu.RLock()
	chats := slices.Clone(s.chats)
	s.mu.RUnlock()
	for idx := range chats {
		chatRecord, err := s.EnsureChatModel(ctx, chats[idx].ID, defaultProvider, defaultModel)
		if err != nil {
			return nil, err
		}
		chats[idx] = chatRecord
	}
	return chats, nil
}

// EnsureChatModel fills missing provider/model fields from session defaults.
func (s *Session) EnsureChatModel(ctx context.Context, chatID id.ID, defaultProvider, defaultModel string) (domain.Chat, error) {
	if s == nil {
		return domain.Chat{}, fmt.Errorf("session is required")
	}
	s.mu.RLock()
	chatRecord, ok := chatByID(s.chats, chatID)
	s.mu.RUnlock()
	if !ok {
		return domain.Chat{}, fmt.Errorf("chat %s not found", chatID)
	}
	if strings.TrimSpace(chatRecord.ProviderID) != "" && strings.TrimSpace(chatRecord.ModelID) != "" {
		return chatRecord, nil
	}
	defaultProvider = strings.TrimSpace(defaultProvider)
	defaultModel = strings.TrimSpace(defaultModel)
	if defaultProvider == "" || defaultModel == "" {
		return chatRecord, nil
	}
	return s.SetChatModel(ctx, chatID, defaultProvider, defaultModel)
}

// TouchSelection marks the session and selected chat as recently used.
func (s *Session) TouchSelection(ctx context.Context, chatID id.ID) (domain.Session, domain.Chat, []domain.Chat, error) {
	if s == nil {
		return domain.Session{}, domain.Chat{}, nil, fmt.Errorf("session is required")
	}
	now := time.Now().UTC()
	s.mu.Lock()
	session := s.session
	session.UpdatedAt = now
	chatRecord, ok := chatByID(s.chats, chatID)
	if !ok {
		s.mu.Unlock()
		return domain.Session{}, domain.Chat{}, nil, fmt.Errorf("chat %s not found", chatID)
	}
	chatRecord.UpdatedAt = now
	if err := putSessionRecord(ctx, s.store, session); err != nil {
		s.mu.Unlock()
		return domain.Session{}, domain.Chat{}, nil, err
	}
	if err := s.chatsSrc.UpdateRecord(ctx, chatRecord); err != nil {
		s.mu.Unlock()
		return domain.Session{}, domain.Chat{}, nil, err
	}
	s.session = session
	upsertSessionChatLocked(&s.chats, chatRecord)
	for _, rt := range s.runtimes {
		if rt != nil {
			rt.SetSession(session)
		}
	}
	if rt := s.runtimes[chatRecord.ID]; rt != nil {
		rt.SetChat(chatRecord)
	}
	chats := slices.Clone(s.chats)
	s.mu.Unlock()
	s.emit(Event{Kind: EventSessionChanged, SessionID: session.ID, Session: session})
	s.emit(Event{Kind: EventChatChanged, SessionID: session.ID, Chat: chatRecord, Snapshot: s.snapshotForChat(chatRecord.ID)})
	return session, chatRecord, chats, nil
}

func newestSessionChat(chats []domain.Chat) domain.Chat {
	var best domain.Chat
	for _, item := range chats {
		if item.ID == "" {
			continue
		}
		if best.ID == "" || item.UpdatedAt.After(best.UpdatedAt) || (item.UpdatedAt.Equal(best.UpdatedAt) && item.ID > best.ID) {
			best = item
		}
	}
	return best
}

func (s *Session) ChatStatus(ctx context.Context, chatID id.ID) (chattool.Status, error) {
	if s == nil {
		return chattool.Status{}, fmt.Errorf("session is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := chatByID(s.chats, chatID); !ok {
		return chattool.Status{}, fmt.Errorf("chat %s not found", chatID)
	}
	return s.chatStatusLocked(chatID), nil
}

// Close closes all chat runtimes currently owned by this session.
func (s *Session) Close(ctx context.Context) error {
	return s.shutdownRuntimes(ctx, "")
}

func (s *Session) Shutdown(ctx context.Context, reason chatpkg.CancelReason) error {
	return s.shutdownRuntimes(ctx, reason)
}

// FailRunningToolCalls marks running tool calls failed for the selected chats.
func (s *Session) FailRunningToolCalls(ctx context.Context, chatIDs []id.ID, message string) (int, error) {
	return s.failToolCalls(ctx, chatIDs, message, func(rt *chatpkg.Chat) (int, error) {
		return rt.FailRunningToolCalls(ctx, message)
	})
}

// FailInterruptedToolCalls marks pending or running tool calls failed for the selected chats.
func (s *Session) FailInterruptedToolCalls(ctx context.Context, chatIDs []id.ID, message string) (int, error) {
	return s.failToolCalls(ctx, chatIDs, message, func(rt *chatpkg.Chat) (int, error) {
		return rt.FailInterruptedToolCalls(ctx, message)
	})
}

func (s *Session) failToolCalls(ctx context.Context, chatIDs []id.ID, _ string, fail func(*chatpkg.Chat) (int, error)) (int, error) {
	if s == nil {
		return 0, fmt.Errorf("session is required")
	}
	if fail == nil {
		return 0, nil
	}
	total := 0
	for _, chatID := range chatIDs {
		if chatID == "" {
			continue
		}
		rt, err := s.Chat(ctx, chatID)
		if err != nil {
			return total, err
		}
		count, err := fail(rt)
		total += count
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func (s *Session) shutdownRuntimes(ctx context.Context, reason chatpkg.CancelReason) error {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	sessionID := s.session.ID
	runtimes := make([]*chatpkg.Chat, 0, len(s.runtimes))
	for _, rt := range s.runtimes {
		if rt != nil {
			runtimes = append(runtimes, rt)
		}
	}
	s.mu.RUnlock()
	s.subsMu.Lock()
	subs := make([]chan Event, 0, len(s.subs))
	for _, ch := range s.subs {
		subs = append(subs, ch)
	}
	s.subsMu.Unlock()
	slog.Info("session shutdown requested", "session_id", sessionID, "reason", reason, "runtimes", len(runtimes), "subscribers", len(subs))
	for _, rt := range runtimes {
		var err error
		if reason == "" {
			err = rt.DrainAndClose(ctx)
		} else {
			err = rt.Shutdown(ctx, reason)
		}
		if err != nil {
			slog.Error("session shutdown failed", "session_id", sessionID, "reason", reason, "error", err)
			return err
		}
	}
	slog.Info("session shutdown complete", "session_id", sessionID, "reason", reason, "runtimes", len(runtimes))
	return nil
}

func (s *Session) GetMilestonePlan(ctx context.Context, sessionID id.ID) (planning.Plan, error) {
	if err := s.requireSession(sessionID); err != nil {
		return planning.Plan{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneMilestonePlan(s.plan), nil
}

func (s *Session) SetMilestonePlan(ctx context.Context, sessionID id.ID, summary string, milestones []planning.Milestone) (planning.Plan, error) {
	if err := s.requireSession(sessionID); err != nil {
		return planning.Plan{}, err
	}
	plan := planning.Plan{
		SessionID:  sessionID,
		Summary:    summary,
		Milestones: cloneMilestones(milestones),
		UpdatedAt:  time.Now().UTC(),
	}
	plan, _ = planning.NormalizePlanKeys(plan)
	if err := s.planSrc.SavePlan(ctx, plan); err != nil {
		return planning.Plan{}, err
	}
	s.mu.Lock()
	s.plan = plan
	todos := flattenTodos(s.todosByRef)
	todosByRef := cloneTodosByRef(s.todosByRef)
	s.mu.Unlock()
	slog.Info("milestone plan stored", "session_id", sessionID, "milestones", len(plan.Milestones), "summary_bytes", len(plan.Summary))
	s.emit(Event{Kind: EventPlanningChanged, SessionID: sessionID, Plan: cloneMilestonePlan(plan), Todos: todos, TodosByRef: todosByRef})
	return cloneMilestonePlan(plan), nil
}

func (s *Session) AddTodoItems(ctx context.Context, sessionID id.ID, milestoneRef string, contents []string) ([]planning.TodoItem, error) {
	if err := s.requireSession(sessionID); err != nil {
		return nil, err
	}
	milestoneRef = strings.TrimSpace(milestoneRef)
	now := time.Now().UTC()
	s.mu.RLock()
	existing := slices.Clone(s.todosByRef[milestoneRef])
	position := len(existing)
	allTodos := flattenTodos(s.todosByRef)
	s.mu.RUnlock()
	if err := planning.ValidateNoDuplicateTodoContent(existing, contents); err != nil {
		return nil, err
	}
	items := make([]planning.TodoItem, 0, len(contents))
	nextKey := nextTodoKey(allTodos, milestoneRef)
	for _, content := range contents {
		content = strings.TrimSpace(content)
		if content == "" {
			continue
		}
		items = append(items, planning.TodoItem{
			ID:           id.New(),
			Key:          nextKey,
			SessionID:    sessionID,
			MilestoneRef: milestoneRef,
			Content:      content,
			Status:       planning.TodoStatusPending,
			Position:     position + len(items),
			CreatedAt:    now,
			UpdatedAt:    now,
		})
		nextKey = incrementTodoKey(nextKey, milestoneRef)
	}
	for _, item := range items {
		if err := s.planSrc.SaveTodo(ctx, item); err != nil {
			return nil, err
		}
	}
	s.mu.Lock()
	if s.todosByRef == nil {
		s.todosByRef = map[string][]planning.TodoItem{}
	}
	s.todosByRef[milestoneRef] = append(s.todosByRef[milestoneRef], items...)
	plan := cloneMilestonePlan(s.plan)
	todos := flattenTodos(s.todosByRef)
	todosByRef := cloneTodosByRef(s.todosByRef)
	s.mu.Unlock()
	slog.Info("tasks added", "session_id", sessionID, "milestone_key", milestoneRef, "count", len(items))
	s.emit(Event{Kind: EventPlanningChanged, SessionID: sessionID, Plan: plan, Todos: todos, TodosByRef: todosByRef})
	return slices.Clone(items), nil
}

func (s *Session) UpdateTodoItem(ctx context.Context, todoID string, status planning.TodoStatus, content, note string) (planning.TodoItem, error) {
	if s == nil {
		return planning.TodoItem{}, fmt.Errorf("session is required")
	}
	now := time.Now().UTC()
	s.mu.RLock()
	var item planning.TodoItem
	var ref string
	found := false
	for milestoneRef, todos := range s.todosByRef {
		for _, candidate := range todos {
			if planning.TodoKey(candidate) == todoID {
				item = candidate
				ref = milestoneRef
				found = true
				break
			}
		}
		if found {
			break
		}
	}
	s.mu.RUnlock()
	if !found {
		return planning.TodoItem{}, fmt.Errorf("task %s not found", todoID)
	}
	item.Status = status
	if strings.TrimSpace(content) != "" {
		item.Content = content
	}
	if strings.TrimSpace(note) != "" {
		item.Note = strings.TrimSpace(note)
	}
	item.UpdatedAt = now
	if err := s.planSrc.SaveTodo(ctx, item); err != nil {
		return planning.TodoItem{}, err
	}
	s.mu.Lock()
	todos := slices.Clone(s.todosByRef[ref])
	for idx := range todos {
		if planning.TodoKey(todos[idx]) == todoID {
			todos[idx] = item
			break
		}
	}
	s.todosByRef[ref] = todos
	sessionID := s.session.ID
	plan := cloneMilestonePlan(s.plan)
	allTodos := flattenTodos(s.todosByRef)
	todosByRef := cloneTodosByRef(s.todosByRef)
	s.mu.Unlock()
	slog.Info("task stored", "session_id", sessionID, "task_id", item.ID, "task_key", item.Key, "milestone_key", ref, "status", item.Status, "note_bytes", len(item.Note))
	s.emit(Event{Kind: EventPlanningChanged, SessionID: sessionID, Plan: plan, Todos: allTodos, TodosByRef: todosByRef})
	return item, nil
}

func (s *Session) ListTodos(ctx context.Context, sessionID id.ID, milestoneRef string) ([]planning.TodoItem, error) {
	if err := s.requireSession(sessionID); err != nil {
		return nil, err
	}
	milestoneRef = strings.TrimSpace(milestoneRef)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if milestoneRef == "" {
		return flattenTodos(s.todosByRef), nil
	}
	return slices.Clone(s.todosByRef[milestoneRef]), nil
}

func (s *Session) AddTask(ctx context.Context, sessionID id.ID, body string, status planning.TaskStatus) (planning.Task, error) {
	if err := s.requireSession(sessionID); err != nil {
		return planning.Task{}, err
	}
	task := planning.Task{
		ID:        id.New(),
		SessionID: sessionID,
		Body:      strings.TrimSpace(body),
		Status:    status,
		CreatedAt: time.Now().UTC(),
	}
	if err := s.planSrc.SaveTask(ctx, task); err != nil {
		return planning.Task{}, err
	}
	s.mu.Lock()
	s.tasks = append(s.tasks, task)
	tasks := slices.Clone(s.tasks)
	s.mu.Unlock()
	s.emit(Event{Kind: EventTasksChanged, SessionID: sessionID, Tasks: tasks})
	return task, nil
}

func (s *Session) PlanningForChat(chat domain.Chat) tools.SessionControl {
	return scopedPlanning{session: s, chat: chat}
}

type scopedPlanning struct {
	session *Session
	chat    domain.Chat
}

func (p scopedPlanning) GetMilestonePlan(ctx context.Context, sessionID id.ID) (planning.Plan, error) {
	plan, err := p.session.GetMilestonePlan(ctx, sessionID)
	if err != nil {
		return planning.Plan{}, err
	}
	if ref := assignedMilestoneRef(p.chat); ref != "" {
		return planning.PlanForRef(plan, ref), nil
	}
	return plan, nil
}

func (p scopedPlanning) SetMilestonePlan(ctx context.Context, sessionID id.ID, summary string, milestones []planning.Milestone) (planning.Plan, error) {
	ref := assignedMilestoneRef(p.chat)
	if ref == "" {
		return p.session.SetMilestonePlan(ctx, sessionID, summary, milestones)
	}
	if len(milestones) != 1 || planning.MilestoneKey(milestones[0]) != ref {
		return planning.Plan{}, fmt.Errorf("chat is scoped to milestone %q", ref)
	}
	current, err := p.session.GetMilestonePlan(ctx, sessionID)
	if err != nil {
		return planning.Plan{}, err
	}
	found := false
	for idx := range current.Milestones {
		if planning.MilestoneKey(current.Milestones[idx]) == ref {
			current.Milestones[idx] = milestones[0]
			found = true
			break
		}
	}
	if !found {
		return planning.Plan{}, fmt.Errorf("milestone %q not found", ref)
	}
	return p.session.SetMilestonePlan(ctx, sessionID, current.Summary, current.Milestones)
}

func (p scopedPlanning) AddTodoItems(ctx context.Context, sessionID id.ID, milestoneRef string, contents []string) ([]planning.TodoItem, error) {
	if assignedTodoRef(p.chat) != "" {
		return nil, fmt.Errorf("chat is scoped to task %q", assignedTodoRef(p.chat))
	}
	ref, err := p.allowedMilestoneRef(milestoneRef)
	if err != nil {
		return nil, err
	}
	return p.session.AddTodoItems(ctx, sessionID, ref, contents)
}

func (p scopedPlanning) UpdateTodoItem(ctx context.Context, todoID string, status planning.TodoStatus, content, note string) (planning.TodoItem, error) {
	if assigned := assignedTodoRef(p.chat); assigned != "" && todoID != assigned {
		return planning.TodoItem{}, fmt.Errorf("chat is scoped to task %q", assigned)
	}
	if ref := assignedMilestoneRef(p.chat); ref != "" {
		todos, err := p.session.ListTodos(ctx, p.chat.SessionID, ref)
		if err != nil {
			return planning.TodoItem{}, err
		}
		found := false
		for _, item := range todos {
			if planning.TodoKey(item) == todoID {
				found = true
				break
			}
		}
		if !found {
			return planning.TodoItem{}, fmt.Errorf("chat is scoped to milestone %q", ref)
		}
	}
	updated, err := p.session.UpdateTodoItem(ctx, todoID, status, content, note)
	if err != nil {
		return planning.TodoItem{}, err
	}
	return updated, nil
}

func (p scopedPlanning) ListTodos(ctx context.Context, sessionID id.ID, milestoneRef string) ([]planning.TodoItem, error) {
	ref, err := p.allowedMilestoneRef(milestoneRef)
	if err != nil {
		return nil, err
	}
	todos, err := p.session.ListTodos(ctx, sessionID, ref)
	if err != nil {
		return nil, err
	}
	if assigned := assignedTodoRef(p.chat); assigned != "" {
		for _, item := range todos {
			if item.ID == assigned {
				return []planning.TodoItem{item}, nil
			}
		}
		return nil, nil
	}
	return todos, nil
}

func (p scopedPlanning) allowedMilestoneRef(requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	assigned := assignedMilestoneRef(p.chat)
	if assigned == "" {
		return requested, nil
	}
	if requested == "" || requested == assigned {
		return assigned, nil
	}
	return "", fmt.Errorf("chat is scoped to milestone %q", assigned)
}

func assignedMilestoneRef(chat domain.Chat) string {
	assigned := strings.TrimSpace(chat.ActiveMilestoneRef)
	if assigned == "" {
		assigned = strings.TrimSpace(chat.AssignedTodoBucketRef)
	}
	return assigned
}

func assignedTodoRef(chat domain.Chat) string {
	return strings.TrimSpace(chat.AssignedTodoRef)
}

func (s *Session) requireSession(sessionID id.ID) error {
	if s == nil {
		return fmt.Errorf("session is required")
	}
	s.mu.RLock()
	current := s.session.ID
	s.mu.RUnlock()
	if sessionID == "" || sessionID != current {
		return fmt.Errorf("session %s is not active", sessionID)
	}
	return nil
}

func (s *Session) chatStatusLocked(chatID id.ID) chattool.Status {
	chatRecord, _ := chatByID(s.chats, chatID)
	status := chattool.RunStateIdle
	statusText := string(chatpkg.StatusIdle)
	busy := false
	pending := 0
	queuedInputs := len(chatRecord.QueuedInputs)
	if rt := s.runtimes[chatID]; rt != nil {
		snapshot := rt.Snapshot()
		chatRecord = snapshot.Chat
		pending = len(snapshot.Approvals)
		queuedInputs = len(snapshot.QueuedInputs)
		statusText = snapshot.StatusText
		switch snapshot.Status {
		case chatpkg.StatusWaitingApproval:
			status = chattool.RunStateWaitingApproval
			busy = true
		case chatpkg.StatusErrored:
			status = chattool.RunStateFailed
		default:
			if snapshot.Active {
				status = chattool.RunStateRunning
				busy = true
			}
		}
		if strings.TrimSpace(statusText) == "" {
			statusText = string(snapshot.Status)
		}
	}
	if pending > 0 && status == chattool.RunStateIdle {
		status = chattool.RunStateWaitingApproval
		busy = true
		statusText = "Waiting for approval"
	}
	return chattool.Status{
		ID:                 chatRecord.ID,
		Title:              chatRecord.Title,
		Role:               chatRecord.WorkflowRole,
		Archived:           chatRecord.Archived,
		ActiveMilestoneRef: chatRecord.ActiveMilestoneRef,
		AssignedTodoRef:    chatRecord.AssignedTodoRef,
		State:              status,
		Status:             string(status),
		Busy:               busy,
		QueuedInputs:       queuedInputs,
		PendingApprovals:   pending,
		StatusText:         statusText,
	}
}

func (s *Session) snapshotForChat(chatID id.ID) chatpkg.Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshotForChatLocked(chatID)
}

func (s *Session) snapshotForChatLocked(chatID id.ID) chatpkg.Snapshot {
	chatRecord, _ := chatByID(s.chats, chatID)
	if rt := s.runtimes[chatID]; rt != nil {
		snapshot := rt.Snapshot()
		if snapshot.Chat.ID == "" {
			snapshot.Chat = chatRecord
		}
		return snapshot
	}
	return chatpkg.Snapshot{Session: s.session, Chat: chatRecord, Status: chatpkg.StatusIdle, StatusText: string(chatpkg.StatusIdle)}
}

func chatByID(chats []domain.Chat, id id.ID) (domain.Chat, bool) {
	for _, item := range chats {
		if item.ID == id {
			return item, true
		}
	}
	return domain.Chat{}, false
}

func upsertSessionChatLocked(chats *[]domain.Chat, chatRecord domain.Chat) {
	for idx := range *chats {
		if (*chats)[idx].ID == chatRecord.ID {
			(*chats)[idx] = chatRecord
			return
		}
	}
	*chats = append(*chats, chatRecord)
	slices.SortFunc(*chats, func(a, b domain.Chat) int {
		if a.Position != b.Position {
			return a.Position - b.Position
		}
		return strings.Compare(string(a.ID), string(b.ID))
	})
}

func fallbackVisibleChatID(chats []domain.Chat, archiving domain.Chat) id.ID {
	if archiving.ParentChatID != nil {
		for _, item := range chats {
			if item.ID == *archiving.ParentChatID && item.ID != archiving.ID && !item.Archived {
				return item.ID
			}
		}
	}
	for _, item := range chats {
		if item.ID != archiving.ID && !item.Archived {
			return item.ID
		}
	}
	return ""
}

func cloneToolStateMap(src map[domain.ToolKind]bool) map[domain.ToolKind]bool {
	if len(src) == 0 {
		return map[domain.ToolKind]bool{}
	}
	out := make(map[domain.ToolKind]bool, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}

func cloneMilestonePlan(plan planning.Plan) planning.Plan {
	plan.Milestones = cloneMilestones(plan.Milestones)
	return plan
}

func cloneMilestones(src []planning.Milestone) []planning.Milestone {
	out := slices.Clone(src)
	for idx := range out {
		if src[idx].OwnerChatID != nil {
			id := *src[idx].OwnerChatID
			out[idx].OwnerChatID = &id
		}
	}
	return out
}

func cloneTodosByRef(src map[string][]planning.TodoItem) map[string][]planning.TodoItem {
	if len(src) == 0 {
		return map[string][]planning.TodoItem{}
	}
	out := make(map[string][]planning.TodoItem, len(src))
	for ref, items := range src {
		out[ref] = slices.Clone(items)
	}
	return out
}

func flattenTodos(src map[string][]planning.TodoItem) []planning.TodoItem {
	var out []planning.TodoItem
	for _, items := range src {
		out = append(out, items...)
	}
	slices.SortFunc(out, func(a, b planning.TodoItem) int {
		if a.MilestoneRef != b.MilestoneRef {
			return strings.Compare(a.MilestoneRef, b.MilestoneRef)
		}
		if a.Position != b.Position {
			return a.Position - b.Position
		}
		return strings.Compare(string(a.ID), string(b.ID))
	})
	return out
}

func nextTodoKey(items []planning.TodoItem, milestoneKey string) string {
	next := 1
	for _, item := range items {
		key := strings.TrimSpace(item.Key)
		prefix := strings.TrimSpace(milestoneKey) + "T"
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		var n int
		if _, err := fmt.Sscanf(strings.TrimPrefix(key, prefix), "%d", &n); err == nil && n >= next {
			next = n + 1
		}
	}
	return planning.ScopedTodoKey(milestoneKey, next)
}

func incrementTodoKey(key, milestoneKey string) string {
	prefix := strings.TrimSpace(milestoneKey) + "T"
	var n int
	if _, err := fmt.Sscanf(strings.TrimPrefix(strings.TrimSpace(key), prefix), "%d", &n); err != nil || n <= 0 {
		return planning.ScopedTodoKey(milestoneKey, 1)
	}
	return planning.ScopedTodoKey(milestoneKey, n+1)
}

var _ tools.SessionControl = (*Session)(nil)
