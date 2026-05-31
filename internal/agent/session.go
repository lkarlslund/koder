package agent

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	chatpkg "github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
)

// Session owns the live state for one persisted session.
type Session struct {
	engine *Engine

	mu         sync.RWMutex
	session    domain.Session
	chats      []domain.Chat
	runtimes   map[domain.ID]*chatpkg.Chat
	plan       store.MilestonePlan
	todosByRef map[string][]store.TodoItem
	tasks      []store.Task
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

// LoadSession returns the live owner for a persisted session, hydrating it on demand.
func (e *Engine) LoadSession(ctx context.Context, sessionID domain.ID) (*Session, error) {
	if e == nil || e.store == nil {
		return nil, fmt.Errorf("engine store is required")
	}
	if sessionID == "" {
		return nil, fmt.Errorf("session id is required")
	}
	e.sessionMu.RLock()
	if existing := e.sessions[sessionID]; existing != nil {
		e.sessionMu.RUnlock()
		return existing, nil
	}
	e.sessionMu.RUnlock()

	loaded, err := e.loadSessionOwner(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	e.sessionMu.Lock()
	if existing := e.sessions[sessionID]; existing != nil {
		e.sessionMu.Unlock()
		_ = loaded.Close(context.Background())
		return existing, nil
	}
	e.sessions[sessionID] = loaded
	e.sessionMu.Unlock()
	return loaded, nil
}

// Session returns an already loaded session owner, loading it if needed.
func (e *Engine) Session(ctx context.Context, sessionID domain.ID) (*Session, error) {
	return e.LoadSession(ctx, sessionID)
}

// Sessions returns persisted session metadata.
func (e *Engine) Sessions(ctx context.Context) ([]domain.Session, error) {
	if e == nil || e.store == nil {
		return nil, fmt.Errorf("engine store is required")
	}
	return e.store.ListSessions(ctx)
}

// CreateSession creates, configures, and loads a live session owner.
func (e *Engine) CreateSession(ctx context.Context, title, projectRoot string) (*Session, error) {
	if e == nil || e.store == nil {
		return nil, fmt.Errorf("engine store is required")
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = "New Session"
	}
	projectRoot = strings.TrimSpace(projectRoot)
	if projectRoot != "" {
		info, err := os.Stat(projectRoot)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("project root must be a directory: %s", projectRoot)
		}
	}
	session, err := e.store.CreateSession(ctx, title, e.cfg.DefaultProvider, e.cfg.DefaultModel, nil)
	if err != nil {
		return nil, err
	}
	if err := e.store.SetSessionProjectRoot(ctx, session.ID, projectRoot); err != nil {
		return nil, err
	}
	if err := e.store.SetSessionPermissionProfile(ctx, session.ID, e.cfg.Permissions.Profile); err != nil {
		return nil, err
	}
	if err := e.store.SetSessionToolStates(ctx, session.ID, e.cfg.ToolDefaults); err != nil {
		return nil, err
	}
	return e.LoadSession(ctx, session.ID)
}

// DeleteSession closes any live runtimes and deletes the persisted session.
func (e *Engine) DeleteSession(ctx context.Context, sessionID domain.ID) error {
	if e == nil || e.store == nil {
		return fmt.Errorf("engine store is required")
	}
	if sessionID == "" {
		return fmt.Errorf("session id is required")
	}
	e.sessionMu.Lock()
	owner := e.sessions[sessionID]
	delete(e.sessions, sessionID)
	e.sessionMu.Unlock()
	if owner != nil {
		if err := owner.Close(ctx); err != nil {
			return err
		}
	}
	return e.store.DeleteSession(ctx, sessionID)
}

func (e *Engine) loadSessionOwner(ctx context.Context, sessionID domain.ID) (*Session, error) {
	session, err := e.store.GetSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	chats, err := e.store.ListChats(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	plan, err := e.store.GetMilestonePlan(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	todosByRef, err := e.loadTodosByRef(ctx, sessionID, plan)
	if err != nil {
		return nil, err
	}
	tasks, err := e.store.ListTasks(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	owner := &Session{
		engine:     e,
		session:    session,
		chats:      slices.Clone(chats),
		runtimes:   map[domain.ID]*chatpkg.Chat{},
		plan:       plan,
		todosByRef: todosByRef,
		tasks:      slices.Clone(tasks),
	}
	for _, chatRecord := range chats {
		rt, err := e.Chat(ctx, session, chatRecord)
		if err != nil {
			return nil, err
		}
		owner.runtimes[chatRecord.ID] = rt
	}
	return owner, nil
}

func (e *Engine) loadTodosByRef(ctx context.Context, sessionID domain.ID, plan store.MilestonePlan) (map[string][]store.TodoItem, error) {
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
		items, err := e.store.ListTodos(ctx, sessionID, ref)
		if err != nil {
			return nil, err
		}
		out[ref] = slices.Clone(items)
	}
	return out, nil
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
	rt, err := s.engine.Chat(ctx, session, chatRecord)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.runtimes[chatID] = rt
	s.mu.Unlock()
	return rt, nil
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
	if err := s.engine.store.PutMilestonePlan(ctx, plan); err != nil {
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
		if err := s.engine.store.PutTodoItem(ctx, item); err != nil {
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
	if err := s.engine.store.PutTodoItem(ctx, item); err != nil {
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
	if err := s.engine.store.PutTask(ctx, task); err != nil {
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

func chatByID(chats []domain.Chat, id domain.ID) (domain.Chat, bool) {
	for _, item := range chats {
		if item.ID == id {
			return item, true
		}
	}
	return domain.Chat{}, false
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
