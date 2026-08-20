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
	workspacepkg "github.com/lkarlslund/koder/internal/workspace"
)

// EventKind identifies a session-owned state mutation.
type EventKind string

const (
	EventChatAdded       EventKind = "chat_added"
	EventChatChanged     EventKind = "chat_changed"
	EventChatArchived    EventKind = "chat_archived"
	EventChatDeleted     EventKind = "chat_deleted"
	EventSessionChanged  EventKind = "session_changed"
	EventPlanningChanged EventKind = "planning_changed"
	EventTasksChanged    EventKind = "tasks_changed"
)

// Event reports a mutation made by the session owner.
type Event struct {
	Kind        EventKind
	SessionID   id.ID
	Chat        domain.Chat
	Snapshot    chatpkg.Snapshot
	Update      chatpkg.Update
	NextChatID  id.ID
	Session     domain.Session
	Plan        planning.Plan
	Tasks       []planning.Task
	TasksByKey  map[string][]planning.Task
	LegacyTasks []planning.LegacyTask
	Err         error
}

// Session owns the live state for one persisted session.
type Session struct {
	store    *store.Store
	chatsSrc *chatpkg.Source
	planSrc  *planning.Source

	mu          sync.RWMutex
	session     domain.Session
	chats       []domain.Chat
	runtimes    map[id.ID]*chatpkg.Chat
	unsubs      map[id.ID]func()
	plan        planning.Plan
	tasksByKey  map[string][]planning.Task
	legacyTasks []planning.LegacyTask
	workspace   workspacepkg.Status
	gitDiff     workspacepkg.Diff
	config      RegistryConfig

	workspaceRefreshTimer   *time.Timer
	workspaceRefreshPending bool
	lastWorkspaceRefresh    time.Time
	workspaceWatchCancel    context.CancelFunc

	subsMu  sync.Mutex
	nextSub int
	subs    map[int]chan Event
}

func (s *Session) UpdateConfig(cfg RegistryConfig) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.config = cloneRegistryConfig(cfg)
	s.mu.Unlock()
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
	tasksByKey, err := loadTasksByKey(ctx, planSrc, sessionID, plan)
	if err != nil {
		return nil, err
	}
	legacyTasks, err := planSrc.ListLegacyTasks(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	owner := &Session{
		store:       st,
		chatsSrc:    chatsSrc,
		planSrc:     planSrc,
		session:     session,
		chats:       slices.Clone(chats),
		runtimes:    map[id.ID]*chatpkg.Chat{},
		unsubs:      map[id.ID]func(){},
		plan:        plan,
		tasksByKey:  tasksByKey,
		legacyTasks: slices.Clone(legacyTasks),
		workspace:   workspacepkg.Status{ProjectRoot: session.ProjectRoot},
		subs:        map[int]chan Event{},
	}
	return owner, nil
}

func loadTasksByKey(ctx context.Context, planSrc *planning.Source, sessionID id.ID, plan planning.Plan) (map[string][]planning.Task, error) {
	out := map[string][]planning.Task{}
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
	items, err := planSrc.ListTasks(ctx, sessionID, "")
	if err != nil {
		return nil, err
	}
	items, changed := planning.NormalizeTaskKeys(items, planning.MilestoneKeyAliases(plan))
	if changed {
		for _, item := range items {
			if err := planSrc.SaveTask(ctx, item); err != nil {
				return nil, err
			}
		}
	}
	for _, item := range items {
		ref := strings.TrimSpace(item.MilestoneKey)
		out[ref] = append(out[ref], item)
	}
	for ref, items := range out {
		planning.SortTasks(items)
		out[ref] = items
	}
	return out, nil
}

// SessionSnapshot is a detached view of a live session.
type SessionSnapshot struct {
	Session     domain.Session
	Chats       []domain.Chat
	Snapshots   map[id.ID]chatpkg.Snapshot
	Plan        planning.Plan
	Tasks       []planning.Task
	TasksByKey  map[string][]planning.Task
	LegacyTasks []planning.LegacyTask
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
		Session:     s.session,
		Chats:       slices.Clone(s.chats),
		Snapshots:   snapshots,
		Plan:        cloneMilestonePlan(s.plan),
		Tasks:       flattenTasks(s.tasksByKey),
		TasksByKey:  cloneTasksByKey(s.tasksByKey),
		LegacyTasks: slices.Clone(s.legacyTasks),
	}
}

// WorkspaceStatus returns the latest workspace metadata known for this session.
func (s *Session) WorkspaceStatus() workspacepkg.Status {
	if s == nil {
		return workspacepkg.Status{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	status := s.workspace
	if status.ProjectRoot == "" {
		status.ProjectRoot = s.session.ProjectRoot
	}
	return status
}

// WorkspaceDiff returns the latest capped git diff preview known for this session.
func (s *Session) WorkspaceDiff() workspacepkg.Diff {
	if s == nil {
		return workspacepkg.Diff{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	diff := s.gitDiff
	if diff.ProjectRoot == "" {
		diff.ProjectRoot = s.session.ProjectRoot
	}
	diff.Files = slices.Clone(diff.Files)
	return diff
}

// RefreshWorkspace refreshes workspace metadata owned by this session.
func (s *Session) RefreshWorkspace(ctx context.Context, snapshot func(context.Context, string) (workspacepkg.SnapshotResult, error), minInterval time.Duration, force bool, onChange func(workspacepkg.SnapshotResult)) error {
	if s == nil {
		return fmt.Errorf("session is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if snapshot == nil {
		snapshot = workspacepkg.Snapshot
	}
	if minInterval <= 0 {
		minInterval = 10 * time.Second
	}
	now := time.Now()
	var staleResult workspacepkg.SnapshotResult
	var broadcastStale bool
	var delay time.Duration

	s.mu.Lock()
	projectRoot := strings.TrimSpace(s.session.ProjectRoot)
	if projectRoot == "" {
		s.mu.Unlock()
		return nil
	}
	if s.workspace.ProjectRoot == "" {
		s.workspace.ProjectRoot = projectRoot
	}
	due := force || s.lastWorkspaceRefresh.IsZero() || now.Sub(s.lastWorkspaceRefresh) >= minInterval
	if !due {
		if !s.workspace.Stale {
			s.workspace.Stale = true
			staleResult = workspacepkg.SnapshotResult{Status: s.workspace, Diff: s.gitDiff}
			broadcastStale = true
		}
		delay = minInterval - now.Sub(s.lastWorkspaceRefresh)
		if delay < 0 {
			delay = 0
		}
		if !s.workspaceRefreshPending {
			s.workspaceRefreshPending = true
			s.workspaceRefreshTimer = time.AfterFunc(delay, func() {
				_ = s.RefreshWorkspace(context.Background(), snapshot, minInterval, true, onChange)
			})
		}
		s.mu.Unlock()
		if broadcastStale && onChange != nil {
			onChange(staleResult)
		}
		return nil
	}
	if s.workspaceRefreshTimer != nil {
		s.workspaceRefreshTimer.Stop()
		s.workspaceRefreshTimer = nil
	}
	s.workspaceRefreshPending = false
	s.lastWorkspaceRefresh = now
	s.mu.Unlock()

	result, err := snapshot(ctx, projectRoot)
	if err != nil {
		return err
	}
	status := result.Status
	status.Stale = false
	result.Status = status

	s.mu.Lock()
	changed := workspaceSignature(s.workspace, s.gitDiff) != workspaceSignature(result.Status, result.Diff)
	s.workspace = status
	s.gitDiff = result.Diff
	if !status.RefreshedAt.IsZero() {
		s.lastWorkspaceRefresh = status.RefreshedAt
	}
	s.mu.Unlock()
	if changed && onChange != nil {
		onChange(result)
	}
	return nil
}

// ReplaceWorkspaceWatcher starts file watching for this session's workspace.
func (s *Session) ReplaceWorkspaceWatcher(snapshot func(context.Context, string) (workspacepkg.SnapshotResult, error), minInterval time.Duration, onChange func(workspacepkg.SnapshotResult)) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.workspaceWatchCancel != nil {
		s.mu.Unlock()
		return
	}
	projectRoot := strings.TrimSpace(s.session.ProjectRoot)
	s.mu.Unlock()
	if projectRoot == "" {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	watcher, err := workspacepkg.Watch(ctx, projectRoot)
	if err != nil {
		cancel()
		slog.Warn("workspace watcher disabled", "session_id", s.session.ID, "project_root", projectRoot, "error", err)
		return
	}
	s.mu.Lock()
	s.workspaceWatchCancel = cancel
	s.mu.Unlock()

	go func() {
		defer cancel()
		for range watcher.Events() {
			_ = s.RefreshWorkspace(context.Background(), snapshot, minInterval, false, onChange)
		}
	}()
}

func workspaceSignature(status workspacepkg.Status, diff workspacepkg.Diff) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%t\n%s\n%s\n%t\n%s\n%s\n%s\n%d/%d/%d/%d\n",
		status.Available,
		status.ProjectRoot,
		status.AgentsChecksum,
		status.Stale,
		status.Branch,
		status.Upstream,
		status.Summary,
		status.Added,
		status.Modified,
		status.Deleted,
		status.Untracked)
	for _, file := range diff.Files {
		b.WriteString(file.Code)
		b.WriteByte('\t')
		b.WriteString(file.Path)
		b.WriteByte('\t')
		fmt.Fprintf(&b, "%d/%d\n", file.Additions, file.Deletions)
	}
	return b.String()
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

// TimelinePageAfter returns the page immediately newer than after.
func (s *Session) TimelinePageAfter(ctx context.Context, chatID, after id.ID, limit int) (chatpkg.TimelinePage, error) {
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
		return rt.TimelinePageAfter(ctx, after, limit)
	}
	return s.chatsSrc.TimelinePageAfter(ctx, chatID, after, limit)
}

// NewChat creates a new orchestrator chat under parentChatID.
func (s *Session) NewChat(ctx context.Context, parentChatID id.ID, title string) (*chatpkg.Chat, error) {
	return s.newChat(ctx, &parentChatID, title, chatrole.Orchestrator)
}

// NewRootChat creates a top-level chat with the requested registered profile.
// It inherits model, permission, and tool settings from the session's first
// top-level chat so alternate surfaces such as voice remain in the same
// workspace and policy boundary.
func (s *Session) NewRootChat(ctx context.Context, title string, role chatrole.Role) (*chatpkg.Chat, error) {
	return s.newChat(ctx, nil, title, role)
}

func (s *Session) newChat(ctx context.Context, parentChatID *id.ID, title string, role chatrole.Role) (*chatpkg.Chat, error) {
	if s == nil {
		return nil, fmt.Errorf("session is required")
	}
	if _, ok := chatrole.DefaultRegistry().Lookup(role); !ok {
		return nil, fmt.Errorf("profile %q is not registered", role)
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Chat"
	}
	s.mu.RLock()
	session := s.session
	chats := slices.Clone(s.chats)
	position := len(s.chats)
	s.mu.RUnlock()
	if session.Kind == domain.SessionKindQuick {
		return nil, fmt.Errorf("quick sessions cannot create additional chats")
	}
	if session.ID == "" {
		return nil, fmt.Errorf("session id is required")
	}
	var template domain.Chat
	if parentChatID != nil {
		var ok bool
		template, ok = chatByID(chats, *parentChatID)
		if !ok {
			return nil, fmt.Errorf("parent chat %s not found", *parentChatID)
		}
	} else {
		for _, candidate := range chats {
			if candidate.ParentChatID == nil {
				template = candidate
				break
			}
		}
		if template.ID == "" && len(chats) > 0 {
			template = chats[0]
		}
		if template.ID == "" {
			return nil, fmt.Errorf("session %s has no chat to inherit settings from", session.ID)
		}
	}
	now := time.Now().UTC()
	chatRecord := domain.Chat{
		ID:                id.New(),
		SessionID:         session.ID,
		ParentChatID:      parentChatID,
		Title:             title,
		WorkflowRole:      role,
		ProviderID:        strings.TrimSpace(template.ProviderID),
		ModelID:           strings.TrimSpace(template.ModelID),
		PermissionProfile: strings.TrimSpace(template.PermissionProfile),
		ToolStates:        cloneToolStateMap(template.ToolStates),
		Position:          position,
		CreatedAt:         now,
		UpdatedAt:         now,
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
	if session.Kind == domain.SessionKindQuick {
		return nil, fmt.Errorf("quick sessions cannot fork chats")
	}
	if session.Kind == domain.SessionKindVoice {
		return nil, fmt.Errorf("voice sessions cannot fork chats")
	}
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
	sourceTimeline, err := sourceRuntime.FullTimeline(ctx)
	if err != nil {
		return nil, err
	}
	chatRecord, err := s.chatsSrc.ForkRecordAt(ctx, source, sourceTimeline, anchorItemID, title, position)
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
	s.mu.RLock()
	hasChat := len(s.chats) > 0
	s.mu.RUnlock()
	if session.Kind == domain.SessionKindQuick && hasChat {
		return nil, fmt.Errorf("quick sessions cannot create additional chats")
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
	return s.updateSession(ctx, update, true)
}

// UpdateSessionMetadata persists organization metadata without changing the
// activity timestamp shown as the session's last-used time.
func (s *Session) UpdateSessionMetadata(ctx context.Context, update func(*domain.Session)) (domain.Session, error) {
	return s.updateSession(ctx, update, false)
}

func (s *Session) updateSession(ctx context.Context, update func(*domain.Session), touch bool) (domain.Session, error) {
	if s == nil {
		return domain.Session{}, fmt.Errorf("session is required")
	}
	if update == nil {
		return domain.Session{}, fmt.Errorf("session update is required")
	}
	s.mu.Lock()
	updated := s.session
	update(&updated)
	if touch {
		updated.UpdatedAt = time.Now().UTC()
	}
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
	onChatArchived := s.config.OnChatArchived
	s.mu.Unlock()
	status.ID = target.ID
	status.Title = target.Title
	status.Role = target.WorkflowRole
	status.Archived = target.Archived
	status.ActiveMilestoneKey = target.ActiveMilestoneKey
	status.AssignedTaskRef = target.AssignedTaskRef
	status.StatusText = statusText
	kind := EventChatChanged
	if archivingVisibleChat {
		kind = EventChatArchived
	}
	s.emit(Event{Kind: kind, SessionID: target.SessionID, Chat: target, Snapshot: snapshot, NextChatID: nextChatID})
	if archivingVisibleChat && onChatArchived != nil {
		onChatArchived(ctx, target.ID)
	}
	return status, nextChatID, nil
}

// DeleteChat permanently removes an archived leaf chat and its history.
func (s *Session) DeleteChat(ctx context.Context, chatID id.ID) error {
	if s == nil {
		return fmt.Errorf("session is required")
	}
	if chatID == "" {
		return fmt.Errorf("chat id is required")
	}
	s.mu.RLock()
	target, ok := chatByID(s.chats, chatID)
	if !ok {
		s.mu.RUnlock()
		return fmt.Errorf("chat %s not found", chatID)
	}
	if !target.Archived {
		s.mu.RUnlock()
		return fmt.Errorf("archive chat %s before deleting it", chatID)
	}
	for _, item := range s.chats {
		if item.ParentChatID != nil && *item.ParentChatID == chatID {
			s.mu.RUnlock()
			return fmt.Errorf("cannot delete chat %s while it has child chats", chatID)
		}
	}
	runtime := s.runtimes[chatID]
	s.mu.RUnlock()
	if runtime != nil {
		if err := runtime.DrainAndClose(ctx); err != nil {
			return fmt.Errorf("close chat %s: %w", chatID, err)
		}
	}
	if err := s.chatsSrc.DeleteRecordData(ctx, chatID); err != nil {
		return fmt.Errorf("delete chat %s: %w", chatID, err)
	}
	s.mu.Lock()
	s.chats = slices.DeleteFunc(s.chats, func(item domain.Chat) bool { return item.ID == chatID })
	delete(s.runtimes, chatID)
	unsub := s.unsubs[chatID]
	delete(s.unsubs, chatID)
	s.mu.Unlock()
	if unsub != nil {
		unsub()
	}
	s.emit(Event{Kind: EventChatDeleted, SessionID: target.SessionID, Chat: target})
	return nil
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
	updated, err := s.UpdateSessionMetadata(ctx, func(session *domain.Session) {
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

// PromoteQuick converts a one-chat quick session into a regular orchestrating session.
func (s *Session) PromoteQuick(ctx context.Context, projectRoot string) (domain.Session, domain.Chat, error) {
	if s == nil {
		return domain.Session{}, domain.Chat{}, fmt.Errorf("session is required")
	}
	s.mu.Lock()
	if s.session.Kind != domain.SessionKindQuick {
		s.mu.Unlock()
		return domain.Session{}, domain.Chat{}, fmt.Errorf("session %s is not a quick session", s.session.ID)
	}
	if len(s.chats) != 1 {
		s.mu.Unlock()
		return domain.Session{}, domain.Chat{}, fmt.Errorf("quick session must contain exactly one chat")
	}
	previousChat := s.chats[0]
	updatedChat := previousChat
	updatedChat.WorkflowRole = chatrole.Orchestrator
	updatedChat.UpdatedAt = time.Now().UTC()
	updatedSession := s.session
	updatedSession.Kind = domain.SessionKindRegular
	updatedSession.ProjectRoot = strings.TrimSpace(projectRoot)
	updatedSession.ProjectRootManaged = false
	updatedSession.UpdatedAt = updatedChat.UpdatedAt
	if err := s.chatsSrc.UpdateRecord(ctx, updatedChat); err != nil {
		s.mu.Unlock()
		return domain.Session{}, domain.Chat{}, err
	}
	if err := putSessionRecord(ctx, s.store, updatedSession); err != nil {
		if rollbackErr := s.chatsSrc.UpdateRecord(ctx, previousChat); rollbackErr != nil {
			s.mu.Unlock()
			return domain.Session{}, domain.Chat{}, fmt.Errorf("promote quick session: %w (rollback chat: %v)", err, rollbackErr)
		}
		s.mu.Unlock()
		return domain.Session{}, domain.Chat{}, err
	}
	s.session = updatedSession
	s.chats[0] = updatedChat
	if s.workspaceWatchCancel != nil {
		s.workspaceWatchCancel()
		s.workspaceWatchCancel = nil
	}
	s.workspace = workspacepkg.Status{ProjectRoot: updatedSession.ProjectRoot}
	s.gitDiff = workspacepkg.Diff{ProjectRoot: updatedSession.ProjectRoot}
	for _, rt := range s.runtimes {
		if rt != nil {
			rt.SetSession(updatedSession)
		}
	}
	if rt := s.runtimes[updatedChat.ID]; rt != nil {
		rt.SetChat(updatedChat)
	}
	s.mu.Unlock()
	s.emit(Event{Kind: EventSessionChanged, SessionID: updatedSession.ID, Session: updatedSession})
	s.emit(Event{Kind: EventChatChanged, SessionID: updatedSession.ID, Chat: updatedChat, Snapshot: s.snapshotForChat(updatedChat.ID)})
	return updatedSession, updatedChat, nil
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
	started := time.Now()
	s.mu.RLock()
	sessionID := s.session.ID
	runtimes := make([]*chatpkg.Chat, 0, len(s.runtimes))
	for _, rt := range s.runtimes {
		if rt != nil {
			runtimes = append(runtimes, rt)
		}
	}
	s.mu.RUnlock()
	s.mu.Lock()
	if s.workspaceRefreshTimer != nil {
		s.workspaceRefreshTimer.Stop()
		s.workspaceRefreshTimer = nil
	}
	s.workspaceRefreshPending = false
	if s.workspaceWatchCancel != nil {
		s.workspaceWatchCancel()
		s.workspaceWatchCancel = nil
	}
	s.mu.Unlock()
	s.subsMu.Lock()
	subs := make([]chan Event, 0, len(s.subs))
	for _, ch := range s.subs {
		subs = append(subs, ch)
	}
	s.subsMu.Unlock()
	slog.Info("session shutdown requested", "session_id", sessionID, "reason", reason, "runtimes", len(runtimes), "subscribers", len(subs))
	errs := make(chan error, len(runtimes))
	for _, rt := range runtimes {
		go func(rt *chatpkg.Chat) {
			if reason == "" {
				errs <- rt.DrainAndClose(ctx)
				return
			}
			errs <- rt.Shutdown(ctx, reason)
		}(rt)
	}
	var firstErr error
	for range runtimes {
		if err := <-errs; err != nil {
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	if firstErr != nil {
		slog.Error("session shutdown failed", "session_id", sessionID, "reason", reason, "error", firstErr, "elapsed_ms", time.Since(started).Milliseconds())
		return firstErr
	}
	slog.Info("session shutdown complete", "session_id", sessionID, "reason", reason, "runtimes", len(runtimes), "elapsed_ms", time.Since(started).Milliseconds())
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
	tasks := flattenTasks(s.tasksByKey)
	tasksByKey := cloneTasksByKey(s.tasksByKey)
	s.mu.Unlock()
	slog.Info("milestone plan stored", "session_id", sessionID, "milestones", len(plan.Milestones), "summary_bytes", len(plan.Summary))
	s.emit(Event{Kind: EventPlanningChanged, SessionID: sessionID, Plan: cloneMilestonePlan(plan), Tasks: tasks, TasksByKey: tasksByKey})
	return cloneMilestonePlan(plan), nil
}

func (s *Session) AddTasks(ctx context.Context, sessionID id.ID, milestoneKey string, contents []string) ([]planning.Task, error) {
	if err := s.requireSession(sessionID); err != nil {
		return nil, err
	}
	milestoneKey = strings.TrimSpace(milestoneKey)
	now := time.Now().UTC()
	s.mu.RLock()
	existing := slices.Clone(s.tasksByKey[milestoneKey])
	position := len(existing)
	allTasks := flattenTasks(s.tasksByKey)
	s.mu.RUnlock()
	if err := planning.ValidateNoDuplicateTaskContent(existing, contents); err != nil {
		return nil, err
	}
	items := make([]planning.Task, 0, len(contents))
	nextKey := nextTaskKey(allTasks, milestoneKey)
	for _, content := range contents {
		content = strings.TrimSpace(content)
		if content == "" {
			continue
		}
		items = append(items, planning.Task{
			ID:           id.New(),
			Key:          nextKey,
			SessionID:    sessionID,
			MilestoneKey: milestoneKey,
			Content:      content,
			Status:       planning.TaskStatusPending,
			Position:     position + len(items),
			CreatedAt:    now,
			UpdatedAt:    now,
		})
		nextKey = incrementTaskKey(nextKey, milestoneKey)
	}
	for _, item := range items {
		if err := s.planSrc.SaveTask(ctx, item); err != nil {
			return nil, err
		}
	}
	s.mu.Lock()
	if s.tasksByKey == nil {
		s.tasksByKey = map[string][]planning.Task{}
	}
	s.tasksByKey[milestoneKey] = append(s.tasksByKey[milestoneKey], items...)
	plan := cloneMilestonePlan(s.plan)
	tasks := flattenTasks(s.tasksByKey)
	tasksByKey := cloneTasksByKey(s.tasksByKey)
	s.mu.Unlock()
	slog.Info("tasks added", "session_id", sessionID, "milestone_key", milestoneKey, "count", len(items))
	s.emit(Event{Kind: EventPlanningChanged, SessionID: sessionID, Plan: plan, Tasks: tasks, TasksByKey: tasksByKey})
	return slices.Clone(items), nil
}

func (s *Session) UpdateTask(ctx context.Context, taskID string, status planning.TaskStatus, content, note string) (planning.Task, error) {
	if s == nil {
		return planning.Task{}, fmt.Errorf("session is required")
	}
	now := time.Now().UTC()
	s.mu.RLock()
	var item planning.Task
	var ref string
	found := false
	for milestoneKey, tasks := range s.tasksByKey {
		for _, candidate := range tasks {
			if planning.TaskKey(candidate) == taskID {
				item = candidate
				ref = milestoneKey
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
		return planning.Task{}, fmt.Errorf("task %s not found", taskID)
	}
	item.Status = status
	if strings.TrimSpace(content) != "" {
		item.Content = content
	}
	if strings.TrimSpace(note) != "" {
		item.Note = strings.TrimSpace(note)
	}
	item.UpdatedAt = now
	if err := s.planSrc.SaveTask(ctx, item); err != nil {
		return planning.Task{}, err
	}
	s.mu.Lock()
	tasks := slices.Clone(s.tasksByKey[ref])
	for idx := range tasks {
		if planning.TaskKey(tasks[idx]) == taskID {
			tasks[idx] = item
			break
		}
	}
	s.tasksByKey[ref] = tasks
	sessionID := s.session.ID
	plan := cloneMilestonePlan(s.plan)
	allTasks := flattenTasks(s.tasksByKey)
	tasksByKey := cloneTasksByKey(s.tasksByKey)
	s.mu.Unlock()
	slog.Info("task stored", "session_id", sessionID, "task_id", item.ID, "task_key", item.Key, "milestone_key", ref, "status", item.Status, "note_bytes", len(item.Note))
	s.emit(Event{Kind: EventPlanningChanged, SessionID: sessionID, Plan: plan, Tasks: allTasks, TasksByKey: tasksByKey})
	return item, nil
}

func (s *Session) MoveTask(ctx context.Context, sessionID id.ID, taskKey, milestoneKey string, status planning.TaskStatus, position int, note string) (planning.Task, error) {
	if err := s.requireSession(sessionID); err != nil {
		return planning.Task{}, err
	}
	taskKey = strings.TrimSpace(taskKey)
	if taskKey == "" {
		return planning.Task{}, fmt.Errorf("task key is required")
	}
	milestoneKey = strings.TrimSpace(milestoneKey)
	now := time.Now().UTC()

	s.mu.RLock()
	var item planning.Task
	oldMilestoneKey := ""
	found := false
	for key, tasks := range s.tasksByKey {
		for _, candidate := range tasks {
			if planning.TaskKey(candidate) == taskKey {
				item = candidate
				oldMilestoneKey = key
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
		return planning.Task{}, fmt.Errorf("task %s not found", taskKey)
	}
	if milestoneKey == "" {
		milestoneKey = oldMilestoneKey
	}
	item.MilestoneKey = milestoneKey
	item.Status = status
	if strings.TrimSpace(note) != "" {
		item.Note = strings.TrimSpace(note)
	}
	item.UpdatedAt = now

	s.mu.Lock()
	oldTasks := removeTaskByKey(s.tasksByKey[oldMilestoneKey], taskKey)
	newTasks := oldTasks
	if oldMilestoneKey != milestoneKey {
		newTasks = slices.Clone(s.tasksByKey[milestoneKey])
	}
	insertAt := position
	if insertAt < 0 || insertAt > len(newTasks) {
		insertAt = len(newTasks)
	}
	newTasks = append(newTasks, planning.Task{})
	copy(newTasks[insertAt+1:], newTasks[insertAt:])
	newTasks[insertAt] = item
	normalizeTaskPositions(newTasks)
	s.tasksByKey[milestoneKey] = newTasks
	if oldMilestoneKey != milestoneKey {
		normalizeTaskPositions(oldTasks)
		s.tasksByKey[oldMilestoneKey] = oldTasks
	}
	toSave := slices.Clone(newTasks)
	if oldMilestoneKey != milestoneKey {
		toSave = append(toSave, oldTasks...)
	}
	plan := cloneMilestonePlan(s.plan)
	allTasks := flattenTasks(s.tasksByKey)
	tasksByKey := cloneTasksByKey(s.tasksByKey)
	s.mu.Unlock()

	for _, task := range toSave {
		if err := s.planSrc.SaveTask(ctx, task); err != nil {
			return planning.Task{}, err
		}
	}
	slog.Info("task moved", "session_id", sessionID, "task_key", taskKey, "milestone_key", milestoneKey, "status", item.Status, "position", position)
	s.emit(Event{Kind: EventPlanningChanged, SessionID: sessionID, Plan: plan, Tasks: allTasks, TasksByKey: tasksByKey})
	return item, nil
}

func (s *Session) ListTasks(ctx context.Context, sessionID id.ID, milestoneKey string) ([]planning.Task, error) {
	if err := s.requireSession(sessionID); err != nil {
		return nil, err
	}
	milestoneKey = strings.TrimSpace(milestoneKey)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if milestoneKey == "" {
		return flattenTasks(s.tasksByKey), nil
	}
	return slices.Clone(s.tasksByKey[milestoneKey]), nil
}

func (s *Session) AddTask(ctx context.Context, sessionID id.ID, body string, status planning.LegacyTaskStatus) (planning.LegacyTask, error) {
	if err := s.requireSession(sessionID); err != nil {
		return planning.LegacyTask{}, err
	}
	task := planning.LegacyTask{
		ID:        id.New(),
		SessionID: sessionID,
		Body:      strings.TrimSpace(body),
		Status:    status,
		CreatedAt: time.Now().UTC(),
	}
	if err := s.planSrc.SaveLegacyTask(ctx, task); err != nil {
		return planning.LegacyTask{}, err
	}
	s.mu.Lock()
	s.legacyTasks = append(s.legacyTasks, task)
	tasks := slices.Clone(s.legacyTasks)
	s.mu.Unlock()
	s.emit(Event{Kind: EventTasksChanged, SessionID: sessionID, LegacyTasks: tasks})
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
	if ref := assignedMilestoneKey(p.chat); ref != "" {
		return planning.PlanForKey(plan, ref), nil
	}
	return plan, nil
}

func (p scopedPlanning) SetMilestonePlan(ctx context.Context, sessionID id.ID, summary string, milestones []planning.Milestone) (planning.Plan, error) {
	ref := assignedMilestoneKey(p.chat)
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

func (p scopedPlanning) AddTasks(ctx context.Context, sessionID id.ID, milestoneKey string, contents []string) ([]planning.Task, error) {
	if assignedTaskRef(p.chat) != "" {
		return nil, fmt.Errorf("chat is scoped to task %q", assignedTaskRef(p.chat))
	}
	ref, err := p.allowedMilestoneKey(milestoneKey)
	if err != nil {
		return nil, err
	}
	return p.session.AddTasks(ctx, sessionID, ref, contents)
}

func (p scopedPlanning) UpdateTask(ctx context.Context, taskID string, status planning.TaskStatus, content, note string) (planning.Task, error) {
	if assigned := assignedTaskRef(p.chat); assigned != "" && taskID != assigned {
		return planning.Task{}, fmt.Errorf("chat is scoped to task %q", assigned)
	}
	if ref := assignedMilestoneKey(p.chat); ref != "" {
		tasks, err := p.session.ListTasks(ctx, p.chat.SessionID, ref)
		if err != nil {
			return planning.Task{}, err
		}
		found := false
		for _, item := range tasks {
			if planning.TaskKey(item) == taskID {
				found = true
				break
			}
		}
		if !found {
			return planning.Task{}, fmt.Errorf("chat is scoped to milestone %q", ref)
		}
	}
	updated, err := p.session.UpdateTask(ctx, taskID, status, content, note)
	if err != nil {
		return planning.Task{}, err
	}
	return updated, nil
}

func (p scopedPlanning) ListTasks(ctx context.Context, sessionID id.ID, milestoneKey string) ([]planning.Task, error) {
	ref, err := p.allowedMilestoneKey(milestoneKey)
	if err != nil {
		return nil, err
	}
	tasks, err := p.session.ListTasks(ctx, sessionID, ref)
	if err != nil {
		return nil, err
	}
	if assigned := assignedTaskRef(p.chat); assigned != "" {
		for _, item := range tasks {
			if item.ID == assigned {
				return []planning.Task{item}, nil
			}
		}
		return nil, nil
	}
	return tasks, nil
}

func (p scopedPlanning) allowedMilestoneKey(requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	assigned := assignedMilestoneKey(p.chat)
	if assigned == "" {
		return requested, nil
	}
	if requested == "" || requested == assigned {
		return assigned, nil
	}
	return "", fmt.Errorf("chat is scoped to milestone %q", assigned)
}

func assignedMilestoneKey(chat domain.Chat) string {
	assigned := strings.TrimSpace(chat.ActiveMilestoneKey)
	if assigned == "" {
		assigned = strings.TrimSpace(chat.AssignedTaskBucketKey)
	}
	return assigned
}

func assignedTaskRef(chat domain.Chat) string {
	return strings.TrimSpace(chat.AssignedTaskRef)
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
		ParentChatID:       chatParentID(chatRecord),
		Title:              chatRecord.Title,
		Role:               chatRecord.WorkflowRole,
		Archived:           chatRecord.Archived,
		ActiveMilestoneKey: chatRecord.ActiveMilestoneKey,
		AssignedTaskRef:    chatRecord.AssignedTaskRef,
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

func cloneTasksByKey(src map[string][]planning.Task) map[string][]planning.Task {
	if len(src) == 0 {
		return map[string][]planning.Task{}
	}
	out := make(map[string][]planning.Task, len(src))
	for ref, items := range src {
		out[ref] = slices.Clone(items)
	}
	return out
}

func flattenTasks(src map[string][]planning.Task) []planning.Task {
	var out []planning.Task
	for _, items := range src {
		out = append(out, items...)
	}
	slices.SortFunc(out, func(a, b planning.Task) int {
		if a.MilestoneKey != b.MilestoneKey {
			return strings.Compare(a.MilestoneKey, b.MilestoneKey)
		}
		if a.Position != b.Position {
			return a.Position - b.Position
		}
		return strings.Compare(string(a.ID), string(b.ID))
	})
	return out
}

func removeTaskByKey(tasks []planning.Task, taskKey string) []planning.Task {
	out := make([]planning.Task, 0, len(tasks))
	for _, task := range tasks {
		if planning.TaskKey(task) != taskKey {
			out = append(out, task)
		}
	}
	return out
}

func normalizeTaskPositions(tasks []planning.Task) {
	planning.SortTasks(tasks)
	for idx := range tasks {
		tasks[idx].Position = idx
	}
}

func nextTaskKey(items []planning.Task, milestoneKey string) string {
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
	return planning.ScopedTaskKey(milestoneKey, next)
}

func incrementTaskKey(key, milestoneKey string) string {
	prefix := strings.TrimSpace(milestoneKey) + "T"
	var n int
	if _, err := fmt.Sscanf(strings.TrimPrefix(strings.TrimSpace(key), prefix), "%d", &n); err != nil || n <= 0 {
		return planning.ScopedTaskKey(milestoneKey, 1)
	}
	return planning.ScopedTaskKey(milestoneKey, n+1)
}

var _ tools.SessionControl = (*Session)(nil)
