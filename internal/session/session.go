package session

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	chatpkg "github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
)

// ChatLoader builds live chat runtimes for session-owned chat records.
type ChatLoader func(context.Context, domain.Session, domain.Chat) (*chatpkg.Chat, error)

// Session owns the live state for one persisted session.
type Session struct {
	store      *store.Store
	chatLoader ChatLoader

	mu         sync.RWMutex
	session    domain.Session
	chats      []domain.Chat
	runtimes   map[domain.ID]*chatpkg.Chat
	plan       store.MilestonePlan
	todosByRef map[string][]store.TodoItem
	tasks      []store.Task
}

// Load hydrates a live session owner from persisted state.
func Load(ctx context.Context, st *store.Store, chatLoader ChatLoader, sessionID domain.ID) (*Session, error) {
	if st == nil {
		return nil, fmt.Errorf("store is required")
	}
	if chatLoader == nil {
		return nil, fmt.Errorf("chat loader is required")
	}
	if sessionID == "" {
		return nil, fmt.Errorf("session id is required")
	}
	session, err := st.GetSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	chats, err := st.ListChats(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	plan, err := st.GetMilestonePlan(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	todosByRef, err := loadTodosByRef(ctx, st, sessionID, plan)
	if err != nil {
		return nil, err
	}
	tasks, err := st.ListTasks(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return &Session{
		store:      st,
		chatLoader: chatLoader,
		session:    session,
		chats:      slices.Clone(chats),
		runtimes:   map[domain.ID]*chatpkg.Chat{},
		plan:       plan,
		todosByRef: todosByRef,
		tasks:      slices.Clone(tasks),
	}, nil
}

func loadTodosByRef(ctx context.Context, st *store.Store, sessionID domain.ID, plan store.MilestonePlan) (map[string][]store.TodoItem, error) {
	out := map[string][]store.TodoItem{}
	seen := map[string]struct{}{}
	for _, milestone := range plan.Milestones {
		ref := strings.TrimSpace(milestone.Ref)
		if ref == "" {
			continue
		}
		if _, ok := seen[ref]; ok {
			continue
		}
		seen[ref] = struct{}{}
		items, err := st.ListTodos(ctx, sessionID, ref)
		if err != nil {
			return nil, err
		}
		out[ref] = slices.Clone(items)
	}
	return out, nil
}

// SessionSnapshot is a detached view of a live session.
type SessionSnapshot struct {
	Session    domain.Session
	Chats      []domain.Chat
	Snapshots  map[domain.ID]chatpkg.Snapshot
	Plan       store.MilestonePlan
	Todos      []store.TodoItem
	TodosByRef map[string][]store.TodoItem
	Tasks      []store.Task
}

// Snapshot returns a detached snapshot of the live session.
func (s *Session) Snapshot() SessionSnapshot {
	if s == nil {
		return SessionSnapshot{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	snapshots := make(map[domain.ID]chatpkg.Snapshot, len(s.runtimes))
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

// Reload refreshes persisted session-owned metadata into the live owner.
func (s *Session) Reload(ctx context.Context) error {
	if s == nil {
		return fmt.Errorf("session is required")
	}
	s.mu.RLock()
	sessionID := s.session.ID
	s.mu.RUnlock()
	session, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}
	chats, err := s.store.ListChats(ctx, sessionID)
	if err != nil {
		return err
	}
	plan, err := s.store.GetMilestonePlan(ctx, sessionID)
	if err != nil {
		return err
	}
	todosByRef, err := loadTodosByRef(ctx, s.store, sessionID, plan)
	if err != nil {
		return err
	}
	tasks, err := s.store.ListTasks(ctx, sessionID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.session = session
	s.chats = slices.Clone(chats)
	s.plan = plan
	s.todosByRef = todosByRef
	s.tasks = slices.Clone(tasks)
	for _, rt := range s.runtimes {
		if rt != nil {
			rt.SetSession(session)
		}
	}
	for _, chatRecord := range chats {
		if rt := s.runtimes[chatRecord.ID]; rt != nil {
			rt.SetChat(chatRecord)
		}
	}
	s.mu.Unlock()
	return nil
}

// Chat returns the live chat runtime owned by this session.
func (s *Session) Chat(ctx context.Context, chatID domain.ID) (*chatpkg.Chat, error) {
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
	rt, err := s.chatLoader(ctx, session, chatRecord)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.runtimes[chatID] = rt
	s.mu.Unlock()
	return rt, nil
}

// NewChat creates a new orchestrator chat under parentChatID.
func (s *Session) NewChat(ctx context.Context, parentChatID domain.ID, title string) (*chatpkg.Chat, error) {
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
		ID:                domain.NewID(),
		SessionID:         session.ID,
		ParentChatID:      &parentID,
		Title:             title,
		WorkflowRole:      chatrole.Orchestrator,
		ProviderID:        strings.TrimSpace(parent.ProviderID),
		ModelID:           strings.TrimSpace(parent.ModelID),
		PermissionProfile: strings.TrimSpace(session.PermissionProfile),
		ToolStates:        cloneToolStateMap(session.ToolStates),
		Position:          position,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := s.store.PutChat(ctx, chatRecord); err != nil {
		return nil, err
	}
	rt, err := s.chatLoader(ctx, session, chatRecord)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	upsertSessionChatLocked(&s.chats, chatRecord)
	s.runtimes[chatRecord.ID] = rt
	s.mu.Unlock()
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
	if err := s.store.PutChat(ctx, chatRecord); err != nil {
		return nil, err
	}
	rt, err := s.chatLoader(ctx, session, chatRecord)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	upsertSessionChatLocked(&s.chats, chatRecord)
	s.runtimes[chatRecord.ID] = rt
	s.mu.Unlock()
	return rt, nil
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
	chatRecord, err := s.store.DefaultChat(ctx, session.ID)
	if err != nil {
		return domain.Chat{}, err
	}
	s.mu.Lock()
	upsertSessionChatLocked(&s.chats, chatRecord)
	s.mu.Unlock()
	return chatRecord, nil
}

// ArchiveChat marks a chat archived, preserving its history.
func (s *Session) ArchiveChat(ctx context.Context, chatID domain.ID) (tools.ChatStatus, domain.ID, error) {
	if s == nil {
		return tools.ChatStatus{}, "", fmt.Errorf("session is required")
	}
	if chatID == "" {
		return tools.ChatStatus{}, "", fmt.Errorf("chat id is required")
	}
	s.mu.RLock()
	target, ok := chatByID(s.chats, chatID)
	nextChatID := fallbackVisibleChatID(s.chats, target)
	if !ok {
		s.mu.RUnlock()
		return tools.ChatStatus{}, "", fmt.Errorf("chat %s not found", chatID)
	}
	s.mu.RUnlock()
	if nextChatID == "" {
		return tools.ChatStatus{}, "", fmt.Errorf("cannot archive the only visible chat in a session")
	}
	target.Archived = true
	target.UpdatedAt = time.Now().UTC()
	if err := s.store.UpdateChat(ctx, target); err != nil {
		return tools.ChatStatus{}, "", err
	}
	s.mu.Lock()
	upsertSessionChatLocked(&s.chats, target)
	if rt := s.runtimes[target.ID]; rt != nil {
		rt.SetChat(target)
	}
	status := s.chatStatusLocked(target.ID)
	s.mu.Unlock()
	status.Chat = target
	status.StatusText = "Archived"
	return status, nextChatID, nil
}

// ReorderChats persists and applies the complete chat order.
func (s *Session) ReorderChats(ctx context.Context, ids []domain.ID) ([]domain.Chat, error) {
	if s == nil {
		return nil, fmt.Errorf("session is required")
	}
	s.mu.RLock()
	sessionID := s.session.ID
	s.mu.RUnlock()
	ordered, err := s.store.ReorderChats(ctx, sessionID, ids)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.chats = slices.Clone(ordered)
	for _, item := range ordered {
		if rt := s.runtimes[item.ID]; rt != nil {
			rt.SetChat(item)
		}
	}
	s.mu.Unlock()
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
	s.mu.RLock()
	sessionID := s.session.ID
	s.mu.RUnlock()
	if err := s.store.UpdateSessionTitle(ctx, sessionID, title, time.Time{}, 0); err != nil {
		return domain.Session{}, err
	}
	updated, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		return domain.Session{}, err
	}
	s.mu.Lock()
	s.session = updated
	for _, rt := range s.runtimes {
		if rt != nil {
			rt.SetSession(updated)
		}
	}
	s.mu.Unlock()
	return updated, nil
}

// SetPermissionProfile updates the session permission profile and loaded runtimes.
func (s *Session) SetPermissionProfile(ctx context.Context, profile string) (domain.Session, error) {
	if s == nil {
		return domain.Session{}, fmt.Errorf("session is required")
	}
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return domain.Session{}, fmt.Errorf("permission profile is required")
	}
	s.mu.RLock()
	sessionID := s.session.ID
	s.mu.RUnlock()
	if err := s.store.SetSessionPermissionProfile(ctx, sessionID, profile); err != nil {
		return domain.Session{}, err
	}
	updated, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		return domain.Session{}, err
	}
	s.mu.Lock()
	s.session = updated
	for _, rt := range s.runtimes {
		if rt != nil {
			rt.SetSession(updated)
		}
	}
	s.mu.Unlock()
	return updated, nil
}

// SetChatModel persists the provider/model used by a chat and updates its runtime.
func (s *Session) SetChatModel(ctx context.Context, chatID domain.ID, providerID, modelID string) (domain.Chat, error) {
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
	if err := s.store.UpdateChat(ctx, chatRecord); err != nil {
		return domain.Chat{}, err
	}
	s.mu.Lock()
	upsertSessionChatLocked(&s.chats, chatRecord)
	if rt := s.runtimes[chatID]; rt != nil {
		rt.SetChat(chatRecord)
	}
	s.mu.Unlock()
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
func (s *Session) EnsureChatModel(ctx context.Context, chatID domain.ID, defaultProvider, defaultModel string) (domain.Chat, error) {
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
func (s *Session) TouchSelection(ctx context.Context, chatID domain.ID) (domain.Session, domain.Chat, []domain.Chat, error) {
	if s == nil {
		return domain.Session{}, domain.Chat{}, nil, fmt.Errorf("session is required")
	}
	s.mu.RLock()
	sessionID := s.session.ID
	chatRecord, ok := chatByID(s.chats, chatID)
	s.mu.RUnlock()
	if !ok {
		return domain.Session{}, domain.Chat{}, nil, fmt.Errorf("chat %s not found", chatID)
	}
	session, err := s.store.TouchSession(ctx, sessionID)
	if err != nil {
		return domain.Session{}, domain.Chat{}, nil, err
	}
	chatRecord.UpdatedAt = time.Now().UTC()
	if err := s.store.UpdateChat(ctx, chatRecord); err != nil {
		return domain.Session{}, domain.Chat{}, nil, err
	}
	s.mu.Lock()
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

func (s *Session) PollChat(ctx context.Context, chatID domain.ID) (tools.ChatStatus, error) {
	if s == nil {
		return tools.ChatStatus{}, fmt.Errorf("session is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := chatByID(s.chats, chatID); !ok {
		return tools.ChatStatus{}, fmt.Errorf("chat %s not found", chatID)
	}
	return s.chatStatusLocked(chatID), nil
}

// Close closes all chat runtimes currently owned by this session.
func (s *Session) Close(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	runtimes := make([]*chatpkg.Chat, 0, len(s.runtimes))
	for _, rt := range s.runtimes {
		if rt != nil {
			runtimes = append(runtimes, rt)
		}
	}
	s.mu.RUnlock()
	for _, rt := range runtimes {
		if err := rt.DrainAndClose(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (s *Session) GetMilestonePlan(ctx context.Context, sessionID domain.ID) (store.MilestonePlan, error) {
	if err := s.requireSession(sessionID); err != nil {
		return store.MilestonePlan{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneMilestonePlan(s.plan), nil
}

func (s *Session) SetMilestonePlan(ctx context.Context, sessionID domain.ID, summary string, milestones []store.Milestone) (store.MilestonePlan, error) {
	if err := s.requireSession(sessionID); err != nil {
		return store.MilestonePlan{}, err
	}
	plan := store.MilestonePlan{
		SessionID:  sessionID,
		Summary:    summary,
		Milestones: cloneMilestones(milestones),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := s.store.PutMilestonePlan(ctx, plan); err != nil {
		return store.MilestonePlan{}, err
	}
	s.mu.Lock()
	s.plan = plan
	s.mu.Unlock()
	return cloneMilestonePlan(plan), nil
}

func (s *Session) AddTodoItems(ctx context.Context, sessionID domain.ID, milestoneRef string, contents []string) ([]store.TodoItem, error) {
	if err := s.requireSession(sessionID); err != nil {
		return nil, err
	}
	milestoneRef = strings.TrimSpace(milestoneRef)
	now := time.Now().UTC()
	s.mu.RLock()
	position := len(s.todosByRef[milestoneRef])
	s.mu.RUnlock()
	items := make([]store.TodoItem, 0, len(contents))
	for _, content := range contents {
		content = strings.TrimSpace(content)
		if content == "" {
			continue
		}
		items = append(items, store.TodoItem{
			ID:           domain.NewID(),
			SessionID:    sessionID,
			MilestoneRef: milestoneRef,
			Content:      content,
			Status:       domain.TodoStatusPending,
			Position:     position + len(items),
			CreatedAt:    now,
			UpdatedAt:    now,
		})
	}
	for _, item := range items {
		if err := s.store.PutTodoItem(ctx, item); err != nil {
			return nil, err
		}
	}
	s.mu.Lock()
	if s.todosByRef == nil {
		s.todosByRef = map[string][]store.TodoItem{}
	}
	s.todosByRef[milestoneRef] = append(s.todosByRef[milestoneRef], items...)
	s.mu.Unlock()
	return slices.Clone(items), nil
}

func (s *Session) UpdateTodoItem(ctx context.Context, todoID domain.ID, status domain.TodoStatus, content string) (store.TodoItem, error) {
	if s == nil {
		return store.TodoItem{}, fmt.Errorf("session is required")
	}
	now := time.Now().UTC()
	s.mu.RLock()
	var item store.TodoItem
	var ref string
	found := false
	for milestoneRef, todos := range s.todosByRef {
		for _, candidate := range todos {
			if candidate.ID == todoID {
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
		return store.TodoItem{}, fmt.Errorf("todo %s not found", todoID)
	}
	item.Status = status
	if strings.TrimSpace(content) != "" {
		item.Content = content
	}
	item.UpdatedAt = now
	if err := s.store.PutTodoItem(ctx, item); err != nil {
		return store.TodoItem{}, err
	}
	s.mu.Lock()
	todos := slices.Clone(s.todosByRef[ref])
	for idx := range todos {
		if todos[idx].ID == todoID {
			todos[idx] = item
			break
		}
	}
	s.todosByRef[ref] = todos
	s.mu.Unlock()
	return item, nil
}

func (s *Session) ListTodos(ctx context.Context, sessionID domain.ID, milestoneRef string) ([]store.TodoItem, error) {
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

func (s *Session) AddTask(ctx context.Context, sessionID domain.ID, body string, status domain.TaskStatus) (store.Task, error) {
	if err := s.requireSession(sessionID); err != nil {
		return store.Task{}, err
	}
	task := store.Task{
		ID:        domain.NewID(),
		SessionID: sessionID,
		Body:      strings.TrimSpace(body),
		Status:    status,
		CreatedAt: time.Now().UTC(),
	}
	if err := s.store.PutTask(ctx, task); err != nil {
		return store.Task{}, err
	}
	s.mu.Lock()
	s.tasks = append(s.tasks, task)
	s.mu.Unlock()
	return task, nil
}

func (s *Session) requireSession(sessionID domain.ID) error {
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

func (s *Session) chatStatusLocked(chatID domain.ID) tools.ChatStatus {
	chatRecord, _ := chatByID(s.chats, chatID)
	status := tools.ChatRunStateIdle
	statusText := string(chatpkg.StatusIdle)
	busy := false
	pending := 0
	if rt := s.runtimes[chatID]; rt != nil {
		snapshot := rt.Snapshot()
		chatRecord = snapshot.Chat
		pending = len(snapshot.Approvals)
		statusText = snapshot.StatusText
		switch snapshot.Status {
		case chatpkg.StatusWaitingApproval:
			status = tools.ChatRunStateWaitingApproval
			busy = true
		case chatpkg.StatusErrored:
			status = tools.ChatRunStateFailed
		default:
			if snapshot.Active {
				status = tools.ChatRunStateRunning
				busy = true
			}
		}
		if strings.TrimSpace(statusText) == "" {
			statusText = string(snapshot.Status)
		}
	}
	if pending > 0 && status == tools.ChatRunStateIdle {
		status = tools.ChatRunStateWaitingApproval
		busy = true
		statusText = "Waiting for approval"
	}
	return tools.ChatStatus{
		Chat:             chatRecord,
		State:            status,
		Status:           string(status),
		Busy:             busy,
		PendingApprovals: pending,
		StatusText:       statusText,
	}
}

func chatByID(chats []domain.Chat, id domain.ID) (domain.Chat, bool) {
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

func fallbackVisibleChatID(chats []domain.Chat, archiving domain.Chat) domain.ID {
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

func cloneMilestonePlan(plan store.MilestonePlan) store.MilestonePlan {
	plan.Milestones = cloneMilestones(plan.Milestones)
	return plan
}

func cloneMilestones(src []store.Milestone) []store.Milestone {
	out := slices.Clone(src)
	for idx := range out {
		if src[idx].OwnerChatID != nil {
			id := *src[idx].OwnerChatID
			out[idx].OwnerChatID = &id
		}
	}
	return out
}

func cloneTodosByRef(src map[string][]store.TodoItem) map[string][]store.TodoItem {
	if len(src) == 0 {
		return map[string][]store.TodoItem{}
	}
	out := make(map[string][]store.TodoItem, len(src))
	for ref, items := range src {
		out[ref] = slices.Clone(items)
	}
	return out
}

func flattenTodos(src map[string][]store.TodoItem) []store.TodoItem {
	var out []store.TodoItem
	for _, items := range src {
		out = append(out, items...)
	}
	slices.SortFunc(out, func(a, b store.TodoItem) int {
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

var _ tools.SessionControl = (*Session)(nil)
