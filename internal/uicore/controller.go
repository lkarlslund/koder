package uicore

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lkarlslund/koder/internal/agent"
	"github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
)

// StartupMode selects the initial session behavior for renderer-neutral UI.
type StartupMode int

const (
	StartupModeNew StartupMode = iota
	StartupModeResume
)

// Event is a renderer-neutral pushed UI update.
type Event struct {
	Seq     uint64 `json:"seq"`
	Type    string `json:"type"`
	Payload any    `json:"payload"`
}

// State is the renderer-neutral UI snapshot consumed by web and TUI renderers.
type State struct {
	Session      domain.Session      `json:"session"`
	Sessions     []domain.Session    `json:"sessions"`
	Chats        []domain.Chat       `json:"chats"`
	ActiveChatID int64               `json:"active_chat_id"`
	Snapshot     chat.Snapshot       `json:"snapshot"`
	Milestones   store.MilestonePlan `json:"milestones"`
	Todos        []store.TodoItem    `json:"todos"`
	Theme        string              `json:"theme"`
	Workdir      string              `json:"workdir"`
	Error        string              `json:"error,omitempty"`
}

// Controller owns session/chat state independently from any renderer.
type Controller struct {
	cfg     config.Config
	store   *store.Store
	agent   *agent.Engine
	workdir string

	mu        sync.RWMutex
	session   domain.Session
	sessions  []domain.Session
	chats     []domain.Chat
	chat      domain.Chat
	runtime   *chat.Chat
	unsub     func()
	milestone store.MilestonePlan
	todos     []store.TodoItem
	theme     string
	lastErr   string

	subMu   sync.Mutex
	nextSub int
	nextSeq uint64
	subs    map[int]chan Event
}

// New constructs a renderer-neutral controller.
func New(cfg config.Config, st *store.Store, engine *agent.Engine, workdir string) *Controller {
	return &Controller{
		cfg:     cfg,
		store:   st,
		agent:   engine,
		workdir: strings.TrimSpace(workdir),
		theme:   "auto",
		subs:    map[int]chan Event{},
	}
}

// Start loads the initial session/chat and attaches the live chat runtime.
func (c *Controller) Start(ctx context.Context, mode StartupMode) error {
	if c == nil {
		return fmt.Errorf("controller is nil")
	}
	session, err := c.initialSession(ctx, mode)
	if err != nil {
		return err
	}
	return c.loadSession(ctx, session.ID, 0)
}

// State returns a detached snapshot of current renderer-neutral UI state.
func (c *Controller) State() State {
	c.mu.RLock()
	defer c.mu.RUnlock()
	state := State{
		Session:      c.session,
		Sessions:     slices.Clone(c.sessions),
		Chats:        slices.Clone(c.chats),
		ActiveChatID: c.chat.ID,
		Milestones:   c.milestone,
		Todos:        slices.Clone(c.todos),
		Theme:        c.theme,
		Workdir:      c.workdir,
		Error:        c.lastErr,
	}
	if c.runtime != nil {
		state.Snapshot = c.runtime.Snapshot()
	}
	return state
}

// Subscribe registers for pushed UI updates.
func (c *Controller) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 64)
	c.subMu.Lock()
	id := c.nextSub
	c.nextSub++
	c.subs[id] = ch
	c.subMu.Unlock()
	c.publishTo(ch, "snapshot", c.State())
	return ch, func() {
		c.subMu.Lock()
		if existing, ok := c.subs[id]; ok {
			delete(c.subs, id)
			close(existing)
		}
		c.subMu.Unlock()
	}
}

// SendPrompt appends a user prompt to the active chat queue.
func (c *Controller) SendPrompt(text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("prompt is empty")
	}
	rt := c.currentRuntime()
	if rt == nil {
		return fmt.Errorf("no active chat")
	}
	rt.Enqueue(chat.QueueItem{Kind: chat.QueueKindSteer, Text: text})
	return nil
}

// Continue asks the active chat to continue.
func (c *Controller) Continue(note string) error {
	rt := c.currentRuntime()
	if rt == nil {
		return fmt.Errorf("no active chat")
	}
	rt.Enqueue(chat.QueueItem{Kind: chat.QueueKindContinue, Note: note})
	return nil
}

// Stop cancels the active chat turn.
func (c *Controller) Stop() error {
	rt := c.currentRuntime()
	if rt == nil {
		return fmt.Errorf("no active chat")
	}
	rt.Cancel()
	return nil
}

// Compact starts compaction on the active chat.
func (c *Controller) Compact() error {
	rt := c.currentRuntime()
	if rt == nil {
		return fmt.Errorf("no active chat")
	}
	return rt.Compact()
}

// Approve approves a pending approval in the active chat.
func (c *Controller) Approve(id int64) error {
	rt := c.currentRuntime()
	if rt == nil {
		return fmt.Errorf("no active chat")
	}
	rt.Approve(id)
	return nil
}

// Deny denies a pending approval in the active chat.
func (c *Controller) Deny(id int64) error {
	rt := c.currentRuntime()
	if rt == nil {
		return fmt.Errorf("no active chat")
	}
	rt.Deny(id)
	return nil
}

// SwitchChat switches the active chat within the current session.
func (c *Controller) SwitchChat(ctx context.Context, chatID int64) error {
	c.mu.RLock()
	sessionID := c.session.ID
	c.mu.RUnlock()
	if sessionID == 0 {
		return fmt.Errorf("no active session")
	}
	return c.loadSession(ctx, sessionID, chatID)
}

// NewChat creates and switches to a chat in the current session.
func (c *Controller) NewChat(ctx context.Context, title string) error {
	c.mu.RLock()
	sessionID := c.session.ID
	parentID := c.chat.ID
	c.mu.RUnlock()
	if sessionID == 0 {
		return fmt.Errorf("no active session")
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Chat"
	}
	chatRecord, err := c.store.CreateChat(ctx, sessionID, title, domain.WorkflowRoleOrchestrator, &parentID)
	if err != nil {
		return err
	}
	return c.loadSession(ctx, sessionID, chatRecord.ID)
}

// SetTheme updates the web theme preference.
func (c *Controller) SetTheme(theme string) {
	theme = strings.ToLower(strings.TrimSpace(theme))
	if theme != "dark" && theme != "light" {
		theme = "auto"
	}
	c.mu.Lock()
	c.theme = theme
	c.mu.Unlock()
	c.broadcast("theme", map[string]string{"theme": theme})
}

func (c *Controller) initialSession(ctx context.Context, mode StartupMode) (domain.Session, error) {
	if c.store == nil {
		return domain.Session{}, fmt.Errorf("store is unavailable")
	}
	if mode == StartupModeNew {
		session, err := c.store.CreateSession(ctx, "New Session", c.cfg.DefaultProvider, c.cfg.DefaultModel, nil)
		if err != nil {
			return domain.Session{}, err
		}
		_ = c.store.UpdateSessionWorkspace(ctx, session.ID, c.workdir, c.workdir)
		_ = c.store.SetSessionPermissionProfile(ctx, session.ID, c.cfg.Permissions.Profile)
		_ = c.store.SetSessionToolStates(ctx, session.ID, c.cfg.ToolDefaults)
		return c.store.GetSession(ctx, session.ID)
	}
	sessions, err := c.store.ListSessions(ctx)
	if err != nil {
		return domain.Session{}, err
	}
	if len(sessions) == 0 {
		return c.initialSession(ctx, StartupModeNew)
	}
	return newestSession(sessions), nil
}

func (c *Controller) loadSession(ctx context.Context, sessionID, chatID int64) error {
	session, err := c.store.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}
	sessions, err := c.store.ListSessions(ctx)
	if err != nil {
		return err
	}
	chats, err := c.store.ListChats(ctx, session.ID)
	if err != nil {
		return err
	}
	var chatRecord domain.Chat
	if chatID != 0 {
		chatRecord, err = c.store.GetChat(ctx, chatID)
		if err != nil {
			return err
		}
		if chatRecord.SessionID != session.ID {
			return fmt.Errorf("chat %d does not belong to session %d", chatID, session.ID)
		}
	} else {
		chatRecord = newestChat(chats)
		if chatRecord.ID == 0 {
			chatRecord, err = c.store.DefaultChat(ctx, session.ID)
			if err != nil {
				return err
			}
		}
	}
	rt, err := c.agent.Chat(ctx, session, chatRecord)
	if err != nil {
		return err
	}
	milestone, todos := c.planningState(ctx, session.ID)
	updates, unsub := rt.Subscribe()

	c.mu.Lock()
	if c.unsub != nil {
		c.unsub()
	}
	c.session = session
	c.sessions = sessions
	c.chats = chats
	c.chat = chatRecord
	c.runtime = rt
	c.unsub = unsub
	c.milestone = milestone
	c.todos = todos
	c.lastErr = ""
	c.mu.Unlock()

	go c.forwardRuntime(chatRecord.ID, updates)
	c.broadcast("snapshot", c.State())
	return nil
}

func (c *Controller) planningState(ctx context.Context, sessionID int64) (store.MilestonePlan, []store.TodoItem) {
	plan, err := c.store.GetMilestonePlan(ctx, sessionID)
	if err != nil {
		return store.MilestonePlan{}, nil
	}
	active, ok := tools.ActiveMilestone(plan)
	if !ok {
		return plan, nil
	}
	todos, err := c.store.ListTodos(ctx, sessionID, active.Ref)
	if err != nil {
		return plan, nil
	}
	return plan, todos
}

func (c *Controller) forwardRuntime(chatID int64, updates <-chan chat.Update) {
	for update := range updates {
		c.mu.RLock()
		current := c.chat.ID
		c.mu.RUnlock()
		if current != chatID {
			return
		}
		if update.Event != nil && update.Event.Err != nil {
			c.mu.Lock()
			c.lastErr = update.Event.Err.Error()
			c.mu.Unlock()
		}
		c.broadcast("chat_update", update)
		c.broadcast("snapshot", c.State())
	}
}

func (c *Controller) currentRuntime() *chat.Chat {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.runtime
}

func (c *Controller) publishTo(ch chan Event, typ string, payload any) {
	c.subMu.Lock()
	c.nextSeq++
	evt := Event{Seq: c.nextSeq, Type: typ, Payload: payload}
	c.subMu.Unlock()
	select {
	case ch <- evt:
	default:
	}
}

func (c *Controller) broadcast(typ string, payload any) {
	c.subMu.Lock()
	c.nextSeq++
	evt := Event{Seq: c.nextSeq, Type: typ, Payload: payload}
	subs := make([]chan Event, 0, len(c.subs))
	for _, ch := range c.subs {
		subs = append(subs, ch)
	}
	c.subMu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- evt:
		default:
		}
	}
}

func newestSession(sessions []domain.Session) domain.Session {
	var best domain.Session
	for _, item := range sessions {
		if item.ID == 0 {
			continue
		}
		if best.ID == 0 || item.UpdatedAt.After(best.UpdatedAt) || (item.UpdatedAt.Equal(best.UpdatedAt) && item.ID > best.ID) {
			best = item
		}
	}
	return best
}

func newestChat(chats []domain.Chat) domain.Chat {
	var best domain.Chat
	for _, item := range chats {
		if item.ID == 0 {
			continue
		}
		if best.ID == 0 || item.UpdatedAt.After(best.UpdatedAt) || (item.UpdatedAt.Equal(best.UpdatedAt) && item.ID > best.ID) {
			best = item
		}
	}
	return best
}

// Touch avoids stale-session ordering when a renderer action changes state.
func Touch(now time.Time, chat *domain.Chat) {
	if chat != nil {
		chat.UpdatedAt = now
	}
}
