package uicore

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lkarlslund/koder/internal/agent"
	"github.com/lkarlslund/koder/internal/attachment"
	"github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/permissionprofile"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/reference"
	"github.com/lkarlslund/koder/internal/skills"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
	workspacepkg "github.com/lkarlslund/koder/internal/workspace"
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
	Session       domain.Session              `json:"session"`
	Sessions      []domain.Session            `json:"sessions"`
	Chats         []domain.Chat               `json:"chats"`
	ChatStatuses  []ChatSidebarStatus         `json:"chat_statuses"`
	ActiveChatID  domain.ID                   `json:"active_chat_id"`
	Permissions   PermissionsState            `json:"permissions"`
	Snapshot      chat.Snapshot               `json:"snapshot"`
	Snapshots     map[domain.ID]chat.Snapshot `json:"snapshots"`
	Milestones    store.MilestonePlan         `json:"milestones"`
	Todos         []store.TodoItem            `json:"todos"`
	TodosByRef    map[string][]store.TodoItem `json:"todos_by_milestone"`
	Workspace     workspacepkg.Status         `json:"workspace_status"`
	ContextWindow int                         `json:"context_window"`
	Theme         string                      `json:"theme"`
	Workdir       string                      `json:"workdir"`
	Error         string                      `json:"error,omitempty"`
}

// ChatSidebarStatus is the renderer-neutral run state for one chat in the sidebar.
type ChatSidebarStatus struct {
	ChatID           domain.ID `json:"chat_id"`
	Status           string    `json:"status"`
	Busy             bool      `json:"busy"`
	PendingApprovals int       `json:"pending_approvals,omitempty"`
	StatusText       string    `json:"status_text,omitempty"`
	LastError        string    `json:"last_error,omitempty"`
}

// ModelOption is a selectable provider/model pair exposed to renderers.
type ModelOption struct {
	ProviderID    string `json:"provider_id"`
	ProviderLabel string `json:"provider_label"`
	ModelID       string `json:"model_id"`
	OwnedBy       string `json:"owned_by,omitempty"`
	Current       bool   `json:"current"`
}

// PermissionsState describes permission profiles available to renderers.
type PermissionsState struct {
	Active   string                            `json:"active"`
	Profiles []permissionprofile.ProfileOption `json:"profiles"`
}

// ProviderState describes configured and available provider templates.
type ProviderState struct {
	DefaultProvider string                   `json:"default_provider"`
	DefaultModel    string                   `json:"default_model"`
	Catalog         []ProviderCatalogItem    `json:"catalog"`
	Providers       []ProviderConfigItem     `json:"providers"`
	Drafts          map[string]ProviderDraft `json:"drafts"`
}

// ProviderCatalogItem is one provider template offered by the provider catalog.
type ProviderCatalogItem struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	Description    string `json:"description"`
	DefaultBaseURL string `json:"default_base_url"`
	ModelHint      string `json:"model_hint"`
	Local          bool   `json:"local"`
}

// ProviderConfigItem is one configured provider row.
type ProviderConfigItem struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	TemplateID   string `json:"template_id"`
	Kind         string `json:"kind"`
	BaseURL      string `json:"base_url"`
	DefaultModel string `json:"default_model"`
	Disabled     bool   `json:"disabled"`
	Default      bool   `json:"default"`
}

// ProviderDraft is the JSON-friendly provider edit shape used by renderers.
type ProviderDraft struct {
	OriginalProviderID string            `json:"original_provider_id"`
	ProviderID         string            `json:"provider_id"`
	TemplateID         string            `json:"template_id"`
	Kind               string            `json:"kind"`
	Name               string            `json:"name"`
	BaseURL            string            `json:"base_url"`
	APIKey             string            `json:"api_key"`
	Model              string            `json:"model"`
	Headers            map[string]string `json:"headers"`
}

// ProviderProbeResult reports a provider test outcome.
type ProviderProbeResult struct {
	ModelCount int      `json:"model_count"`
	Models     []string `json:"models"`
}

// ComposerCompletions describes completion candidates for composer trigger tokens.
type ComposerCompletions struct {
	Kind  string                   `json:"kind"`
	Query string                   `json:"query"`
	Start int                      `json:"start"`
	End   int                      `json:"end"`
	Items []ComposerCompletionItem `json:"items"`
}

// ComposerCompletionItem is one insertable composer completion.
type ComposerCompletionItem struct {
	Label       string `json:"label"`
	InsertText  string `json:"insert_text"`
	Description string `json:"description,omitempty"`
	Kind        string `json:"kind,omitempty"`
	Path        string `json:"path,omitempty"`
}

// SessionState describes workspace-scoped sessions.
type SessionState struct {
	ActiveID domain.ID        `json:"active_id"`
	Workdir  string           `json:"workdir"`
	Sessions []domain.Session `json:"sessions"`
}

// Controller owns session/chat state independently from any renderer.
type Controller struct {
	cfg     config.Config
	store   *store.Store
	agent   *agent.Engine
	workdir string

	mu         sync.RWMutex
	session    domain.Session
	sessions   []domain.Session
	chats      []domain.Chat
	statuses   map[domain.ID]ChatSidebarStatus
	chat       domain.Chat
	runtime    *chat.Chat
	unsub      func()
	runtimes   map[domain.ID]*chat.Chat
	unsubs     map[domain.ID]func()
	snapshots  map[domain.ID]chat.Snapshot
	milestone  store.MilestonePlan
	todos      []store.TodoItem
	todosByRef map[string][]store.TodoItem
	workspace  workspacepkg.Status
	theme      string
	lastErr    string

	subMu   sync.Mutex
	nextSub int
	nextSeq uint64
	subs    map[int]chan Event

	monitorOnce sync.Once
}

// New constructs a renderer-neutral controller.
func New(cfg config.Config, st *store.Store, engine *agent.Engine, workdir string) *Controller {
	return &Controller{
		cfg:       cfg,
		store:     st,
		agent:     engine,
		workdir:   strings.TrimSpace(workdir),
		theme:     "auto",
		statuses:  map[domain.ID]ChatSidebarStatus{},
		runtimes:  map[domain.ID]*chat.Chat{},
		unsubs:    map[domain.ID]func(){},
		snapshots: map[domain.ID]chat.Snapshot{},
		subs:      map[int]chan Event{},
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
	if err := c.loadSession(ctx, session.ID, ""); err != nil {
		return err
	}
	c.monitorOnce.Do(func() {
		go c.monitorWorkspace(ctx)
	})
	return nil
}

// State returns a detached snapshot of current renderer-neutral UI state.
func (c *Controller) State() State {
	c.mu.RLock()
	defer c.mu.RUnlock()
	state := State{
		Session:       c.session,
		Sessions:      slices.Clone(c.sessions),
		Chats:         slices.Clone(c.chats),
		ChatStatuses:  c.chatStatusesLocked(),
		ActiveChatID:  c.chat.ID,
		Permissions:   c.permissionsStateLocked(),
		Snapshots:     map[domain.ID]chat.Snapshot{},
		Milestones:    c.milestone,
		Todos:         slices.Clone(c.todos),
		TodosByRef:    cloneTodosByRef(c.todosByRef),
		Workspace:     c.workspace,
		ContextWindow: c.contextWindowLocked(),
		Theme:         c.theme,
		Workdir:       c.workdir,
		Error:         c.lastErr,
	}
	for idx := range state.Chats {
		if state.Chats[idx].ID == c.chat.ID {
			state.Chats[idx] = c.chat
			break
		}
	}
	for chatID, snapshot := range c.snapshots {
		if snapshot.Chat.ID == "" {
			snapshot.Chat.ID = chatID
		}
		state.Snapshots[chatID] = snapshot
		if chatID == c.chat.ID {
			state.Snapshot = snapshot
		}
		if !hasChatSidebarStatus(state.ChatStatuses, snapshot.Chat.ID) {
			state.ChatStatuses = mergeChatSidebarStatus(state.ChatStatuses, sidebarStatusFromSnapshot(snapshot))
		}
	}
	for chatID, rt := range c.runtimes {
		if rt == nil {
			continue
		}
		if _, ok := state.Snapshots[chatID]; ok {
			continue
		}
		snapshot := rt.Snapshot()
		if snapshot.Chat.ID == "" {
			snapshot.Chat.ID = chatID
		}
		state.Snapshots[chatID] = snapshot
		if chatID == c.chat.ID {
			state.Snapshot = snapshot
		}
		if !hasChatSidebarStatus(state.ChatStatuses, snapshot.Chat.ID) {
			state.ChatStatuses = mergeChatSidebarStatus(state.ChatStatuses, sidebarStatusFromSnapshot(snapshot))
		}
	}
	if state.Snapshot.Chat.ID == "" && c.runtime != nil {
		snapshot := c.runtime.Snapshot()
		state.Snapshot = snapshot
		if !hasChatSidebarStatus(state.ChatStatuses, snapshot.Chat.ID) {
			state.ChatStatuses = mergeChatSidebarStatus(state.ChatStatuses, sidebarStatusFromSnapshot(snapshot))
		}
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
	return c.SendPromptWithAttachments(text, nil)
}

// SendPromptWithAttachments appends a user prompt and uploaded attachment drafts to the active chat queue.
func (c *Controller) SendPromptWithAttachments(text string, drafts []attachment.Draft) error {
	text = strings.TrimSpace(text)
	validated := make([]attachment.Draft, 0, len(drafts))
	manager := attachment.NewManager(c.cfg.StateDir())
	for _, draft := range drafts {
		next, err := manager.ValidateDraft(draft)
		if err != nil {
			return err
		}
		validated = append(validated, next)
	}
	if text == "" && len(validated) == 0 {
		return fmt.Errorf("prompt is empty")
	}
	rt := c.currentRuntime()
	if rt == nil {
		return fmt.Errorf("no active chat")
	}
	rt.Enqueue(chat.QueueItem{Kind: chat.QueueKindSteer, Text: text, Attachments: validated})
	return nil
}

// ImportClipboardImage stores a pasted image as a draft attachment for the web composer.
func (c *Controller) ImportClipboardImage(data []byte, name string, mimeType string) (attachment.Draft, error) {
	return attachment.NewManager(c.cfg.StateDir()).ImportClipboardImageData(data, name, mimeType)
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

// StopAfterCurrentTurn asks the active chat to stop at the next persisted turn boundary.
func (c *Controller) StopAfterCurrentTurn() error {
	rt := c.currentRuntime()
	if rt == nil {
		return fmt.Errorf("no active chat")
	}
	rt.StopAfterCurrentTurn()
	return nil
}

// Shutdown gracefully drains the active runtime and releases subscriptions.
func (c *Controller) Shutdown(ctx context.Context) error {
	c.mu.RLock()
	runtimes := make([]*chat.Chat, 0, len(c.runtimes))
	for _, rt := range c.runtimes {
		if rt != nil {
			runtimes = append(runtimes, rt)
		}
	}
	unsubs := make([]func(), 0, len(c.unsubs)+1)
	for _, unsub := range c.unsubs {
		if unsub != nil {
			unsubs = append(unsubs, unsub)
		}
	}
	if c.unsub != nil {
		unsubs = append(unsubs, c.unsub)
	}
	c.mu.RUnlock()
	for _, unsub := range unsubs {
		unsub()
	}
	var firstErr error
	for _, rt := range runtimes {
		if err := rt.DrainAndClose(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Compact starts compaction on the active chat.
func (c *Controller) Compact() error {
	rt := c.currentRuntime()
	if rt == nil {
		return fmt.Errorf("no active chat")
	}
	return rt.Compact()
}

// Approve approves a pending tool call in the active chat.
func (c *Controller) Approve(toolCallID string) error {
	rt := c.currentRuntime()
	if rt == nil {
		return fmt.Errorf("no active chat")
	}
	rt.ApproveTool(toolCallID)
	return nil
}

// Deny denies a pending tool call in the active chat.
func (c *Controller) Deny(toolCallID string) error {
	rt := c.currentRuntime()
	if rt == nil {
		return fmt.Errorf("no active chat")
	}
	rt.DenyTool(toolCallID)
	return nil
}

// SwitchChat switches the active chat within the current session.
func (c *Controller) SwitchChat(ctx context.Context, chatID domain.ID) error {
	c.mu.RLock()
	sessionID := c.session.ID
	c.mu.RUnlock()
	if sessionID == "" {
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
	if sessionID == "" {
		return fmt.Errorf("no active session")
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Chat"
	}
	chatRecord, err := c.store.CreateChat(ctx, sessionID, title, chatrole.Orchestrator, &parentID)
	if err != nil {
		return err
	}
	return c.loadSession(ctx, sessionID, chatRecord.ID)
}

// DeleteChat deletes an idle chat and switches away first when it is active.
func (c *Controller) DeleteChat(ctx context.Context, chatID domain.ID) error {
	if chatID == "" {
		return fmt.Errorf("chat id is required")
	}
	c.mu.RLock()
	sessionID := c.session.ID
	activeChatID := c.chat.ID
	activeRuntime := c.runtime
	targetRuntime := c.runtimes[chatID]
	targetUnsub := c.unsubs[chatID]
	c.mu.RUnlock()
	if sessionID == "" {
		return fmt.Errorf("no active session")
	}
	if c.agent != nil {
		status, err := c.agent.PollChat(ctx, sessionID, chatID)
		if err != nil {
			return err
		}
		if status.Busy {
			return fmt.Errorf("busy chat can not be deleted")
		}
	}
	chats, err := c.store.ListChats(ctx, sessionID)
	if err != nil {
		return err
	}
	if len(chats) <= 1 {
		return fmt.Errorf("cannot delete the only chat in a session")
	}
	target, ok := chatByID(chats, chatID)
	if !ok {
		return fmt.Errorf("chat %s not found", chatID)
	}
	nextChatID := activeChatID
	deletingActive := chatID == activeChatID
	if deletingActive {
		nextChatID = fallbackChatID(chats, target)
		if nextChatID == "" {
			return fmt.Errorf("no chat to switch to after deletion")
		}
		if activeRuntime != nil {
			activeRuntime.Close()
		}
	} else if targetRuntime != nil {
		targetRuntime.Close()
	}
	if targetUnsub != nil {
		targetUnsub()
	}
	if err := c.store.DeleteChat(ctx, chatID); err != nil {
		return err
	}
	if deletingActive {
		return c.loadSession(ctx, sessionID, nextChatID)
	}
	chats, err = c.store.ListChats(ctx, sessionID)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.chats = chats
	delete(c.statuses, chatID)
	delete(c.runtimes, chatID)
	delete(c.unsubs, chatID)
	delete(c.snapshots, chatID)
	c.mu.Unlock()
	c.refreshChatStatuses(ctx, sessionID)
	c.broadcast("snapshot", c.State())
	return nil
}

// Sessions returns sessions for the current workspace.
func (c *Controller) Sessions(ctx context.Context) (SessionState, error) {
	sessions, err := c.workspaceSessions(ctx)
	if err != nil {
		return SessionState{}, err
	}
	c.mu.RLock()
	activeID := c.session.ID
	c.mu.RUnlock()
	return SessionState{ActiveID: activeID, Workdir: c.workdir, Sessions: sessions}, nil
}

// SwitchSession switches the active session within the current workspace.
func (c *Controller) SwitchSession(ctx context.Context, sessionID domain.ID) error {
	if sessionID == "" {
		return fmt.Errorf("session id is required")
	}
	session, err := c.store.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}
	if !c.sessionInWorkspace(session) {
		return fmt.Errorf("session %s does not belong to this workspace", sessionID)
	}
	return c.loadSession(ctx, sessionID, "")
}

// NewSession creates and switches to a new session in the current workspace.
func (c *Controller) NewSession(ctx context.Context, title string) error {
	session, err := c.createWorkspaceSession(ctx, title)
	if err != nil {
		return err
	}
	return c.loadSession(ctx, session.ID, "")
}

// RefreshWorkspace refreshes workspace metadata and publishes a snapshot on change.
func (c *Controller) RefreshWorkspace(ctx context.Context) error {
	status, err := workspacepkg.Snapshot(ctx, c.workdir)
	if err != nil {
		return err
	}
	c.mu.Lock()
	changed := workspaceSignature(c.workspace) != workspaceSignature(status)
	c.workspace = status
	c.mu.Unlock()
	if changed {
		c.broadcast("snapshot", c.State())
	}
	return nil
}

// CompleteComposer returns skill and reference completions for the current composer token.
func (c *Controller) CompleteComposer(text string, cursor int) (ComposerCompletions, error) {
	if cursor < 0 || cursor > len(text) {
		cursor = len(text)
	}
	if query, start, ok := composerSkillQuery(text, cursor); ok {
		items := matchingComposerSkills(c.workdir, query)
		if len(items) == 1 && strings.EqualFold(items[0].Name, query) {
			items = nil
		}
		out := ComposerCompletions{Kind: "skill", Query: query, Start: start, End: cursor}
		for _, item := range items {
			out.Items = append(out.Items, ComposerCompletionItem{
				Label:       "$" + item.Name,
				InsertText:  "$" + item.Name,
				Description: blankAsDash(item.Description),
				Kind:        string(item.Scope),
				Path:        item.Path,
			})
		}
		return out, nil
	}
	if query, start, end, pathMode, ok := composerMentionQuery(text, cursor); ok {
		var matches []reference.Entry
		var err error
		if pathMode {
			matches, err = reference.PathCompletions(c.workdir, query, 8)
		} else {
			var catalog []reference.Entry
			catalog, err = reference.Entries(c.workdir)
			matches = reference.Search(catalog, query, 8)
		}
		if err != nil {
			return ComposerCompletions{}, err
		}
		out := ComposerCompletions{Kind: "reference", Query: query, Start: start, End: end}
		for _, item := range matches {
			out.Items = append(out.Items, ComposerCompletionItem{
				Label:       reference.DisplayToken(item.Path),
				InsertText:  reference.DisplayToken(item.Path),
				Description: item.Description,
				Kind:        string(item.Kind),
				Path:        item.Path,
			})
		}
		return out, nil
	}
	return ComposerCompletions{}, nil
}

// Providers returns the configured providers and catalog templates.
func (c *Controller) Providers() ProviderState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.providerStateLocked()
}

// NewProviderDraft returns a draft initialized from a provider template.
func (c *Controller) NewProviderDraft(templateID string) (ProviderDraft, error) {
	c.mu.RLock()
	cfg := c.cfg
	c.mu.RUnlock()
	templateID = strings.TrimSpace(templateID)
	if templateID == "" {
		templateID = provider.ProviderKindCompatible
	}
	draft, err := provider.BuildDraft(templateID, cfg.Providers)
	if err != nil {
		return ProviderDraft{}, err
	}
	return providerDraftFromCatalog(draft), nil
}

// TestProvider probes a provider draft by listing models.
func (c *Controller) TestProvider(ctx context.Context, draft ProviderDraft) (ProviderProbeResult, error) {
	result, err := provider.Probe(ctx, providerDraftToCatalog(draft), nil)
	if err != nil {
		return ProviderProbeResult{}, err
	}
	limit := len(result.Models)
	if limit > 12 {
		limit = 12
	}
	models := make([]string, 0, limit)
	for idx, item := range result.Models {
		if idx >= 12 {
			break
		}
		models = append(models, item.ID)
	}
	return ProviderProbeResult{ModelCount: len(result.Models), Models: models}, nil
}

// SaveProvider validates and persists a provider draft.
func (c *Controller) SaveProvider(ctx context.Context, draft ProviderDraft) (ProviderState, error) {
	catalogDraft := providerDraftToCatalog(draft)
	if err := provider.ValidateDraft(catalogDraft); err != nil {
		return ProviderState{}, err
	}
	originalID := strings.TrimSpace(catalogDraft.OriginalProviderID)
	catalogDraft.ProviderID = strings.TrimSpace(catalogDraft.ProviderID)
	if catalogDraft.ProviderID == "" {
		return ProviderState{}, fmt.Errorf("provider id is required")
	}

	c.mu.Lock()
	if c.cfg.Providers == nil {
		c.cfg.Providers = map[string]config.Provider{}
	}
	if originalID != "" && originalID != catalogDraft.ProviderID {
		if _, exists := c.cfg.Providers[catalogDraft.ProviderID]; exists {
			c.mu.Unlock()
			return ProviderState{}, fmt.Errorf("provider %q already exists", catalogDraft.ProviderID)
		}
	}
	next := catalogDraft.ToConfig()
	lookupID := catalogDraft.ProviderID
	if originalID != "" {
		lookupID = originalID
	}
	existing, ok := c.cfg.Providers[lookupID]
	if ok {
		mergeProviderEditDefaults(&next, existing)
	} else {
		applyNewProviderDefaults(&next, c.cfg.AutoCompactAt)
	}
	if strings.TrimSpace(next.Name) == "" {
		if desc, found := provider.Lookup(catalogDraft.TemplateID); found {
			next.Name = desc.Title
		} else {
			next.Name = catalogDraft.ProviderID
		}
	}
	if originalID != "" && originalID != catalogDraft.ProviderID {
		delete(c.cfg.Providers, originalID)
	}
	c.cfg.Providers[catalogDraft.ProviderID] = next
	if strings.TrimSpace(c.cfg.DefaultProvider) == "" || c.cfg.DefaultProvider == originalID || c.cfg.DefaultProvider == catalogDraft.ProviderID {
		c.cfg.DefaultProvider = catalogDraft.ProviderID
	}
	c.cfg.DefaultModel = catalogDraft.Model
	if err := c.cfg.Save(); err != nil {
		c.mu.Unlock()
		return ProviderState{}, err
	}
	if c.agent != nil {
		c.agent.UpdateConfig(c.cfg)
	}
	if c.session.ID != "" && (strings.TrimSpace(c.session.ProviderID) == "" || !c.cfg.HasUsableProvider(c.session.ProviderID) || c.session.ProviderID == originalID) {
		if err := c.store.SetSessionModel(ctx, c.session.ID, catalogDraft.ProviderID, catalogDraft.Model); err != nil {
			c.mu.Unlock()
			return ProviderState{}, err
		}
		session, err := c.store.GetSession(ctx, c.session.ID)
		if err != nil {
			c.mu.Unlock()
			return ProviderState{}, err
		}
		c.session = session
		for idx := range c.sessions {
			if c.sessions[idx].ID == session.ID {
				c.sessions[idx] = session
			}
		}
		if c.runtime != nil {
			c.runtime.SetSession(session)
		}
	}
	state := c.providerStateLocked()
	c.mu.Unlock()
	c.broadcast("snapshot", c.State())
	return state, nil
}

// DeleteProvider removes a configured provider.
func (c *Controller) DeleteProvider(ctx context.Context, providerID string) (ProviderState, error) {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return ProviderState{}, fmt.Errorf("provider id is required")
	}
	c.mu.Lock()
	if _, ok := c.cfg.Providers[providerID]; !ok {
		c.mu.Unlock()
		return ProviderState{}, fmt.Errorf("provider %q is not configured", providerID)
	}
	delete(c.cfg.Providers, providerID)
	nextDefault := strings.TrimSpace(c.cfg.DefaultProvider)
	if nextDefault == providerID || !c.cfg.HasUsableProvider(nextDefault) {
		nextDefault = ""
		ids := make([]string, 0, len(c.cfg.Providers))
		for id := range c.cfg.Providers {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		if len(ids) > 0 {
			nextDefault = ids[0]
		}
	}
	c.cfg.DefaultProvider = nextDefault
	c.cfg.DefaultModel = ""
	if nextDefault != "" {
		if next, ok := c.cfg.Provider(nextDefault); ok {
			c.cfg.DefaultModel = next.DefaultModel
		}
	}
	if err := c.cfg.Save(); err != nil {
		c.mu.Unlock()
		return ProviderState{}, err
	}
	if c.agent != nil {
		c.agent.UpdateConfig(c.cfg)
	}
	if c.session.ID != "" && c.session.ProviderID == providerID {
		if err := c.store.SetSessionModel(ctx, c.session.ID, c.cfg.DefaultProvider, c.cfg.DefaultModel); err != nil {
			c.mu.Unlock()
			return ProviderState{}, err
		}
		session, err := c.store.GetSession(ctx, c.session.ID)
		if err != nil {
			c.mu.Unlock()
			return ProviderState{}, err
		}
		c.session = session
		for idx := range c.sessions {
			if c.sessions[idx].ID == session.ID {
				c.sessions[idx] = session
			}
		}
		if c.runtime != nil {
			c.runtime.SetSession(session)
		}
	}
	state := c.providerStateLocked()
	c.mu.Unlock()
	c.broadcast("snapshot", c.State())
	return state, nil
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

// ModelOptions lists selectable models across configured providers.
func (c *Controller) ModelOptions(ctx context.Context) ([]ModelOption, error) {
	c.mu.RLock()
	currentProvider := strings.TrimSpace(c.session.ProviderID)
	currentModel := strings.TrimSpace(c.session.ModelID)
	c.mu.RUnlock()

	seen := map[string]struct{}{}
	options := make([]ModelOption, 0, len(c.cfg.Providers))
	add := func(providerID string, providerCfg config.Provider, model domain.Model) {
		providerID = strings.TrimSpace(providerID)
		modelID := strings.TrimSpace(model.ID)
		if providerID == "" || modelID == "" {
			return
		}
		key := providerID + "\x00" + modelID
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		options = append(options, ModelOption{
			ProviderID:    providerID,
			ProviderLabel: providerEntryLabel(providerID, providerCfg),
			ModelID:       modelID,
			OwnedBy:       strings.TrimSpace(model.OwnedBy),
			Current:       providerID == currentProvider && modelID == currentModel,
		})
	}

	ids := make([]string, 0, len(c.cfg.Providers))
	for id, providerCfg := range c.cfg.Providers {
		if providerCfg.Disabled {
			continue
		}
		ids = append(ids, id)
	}
	slices.Sort(ids)

	var failures []string
	for _, providerID := range ids {
		providerCfg, ok := c.cfg.Provider(providerID)
		if !ok {
			continue
		}
		if strings.TrimSpace(providerCfg.DefaultModel) != "" {
			add(providerID, providerCfg, domain.Model{ID: providerCfg.DefaultModel})
		}
		client, err := provider.New(providerID, providerCfg, nil)
		if err != nil {
			failures = append(failures, providerID)
			continue
		}
		models, err := client.ListModels(ctx)
		if err != nil {
			failures = append(failures, providerID)
			continue
		}
		for _, model := range models {
			add(providerID, providerCfg, model)
		}
	}
	if currentProvider != "" && currentModel != "" {
		if providerCfg, ok := c.cfg.Provider(currentProvider); ok {
			add(currentProvider, providerCfg, domain.Model{ID: currentModel})
		} else {
			add(currentProvider, config.Provider{Name: currentProvider}, domain.Model{ID: currentModel})
		}
	}
	slices.SortFunc(options, func(a, b ModelOption) int {
		if cmp := strings.Compare(a.ProviderLabel, b.ProviderLabel); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.ModelID, b.ModelID)
	})
	if len(options) == 0 && len(failures) > 0 {
		return nil, fmt.Errorf("failed to load models from %s", strings.Join(failures, ", "))
	}
	return options, nil
}

// SetModel persists the active session model and updates the live chat runtime.
func (c *Controller) SetModel(ctx context.Context, providerID, modelID string) error {
	providerID = strings.TrimSpace(providerID)
	modelID = strings.TrimSpace(modelID)
	if providerID == "" {
		return fmt.Errorf("provider id is required")
	}
	if modelID == "" {
		return fmt.Errorf("model id is required")
	}
	if !c.cfg.HasUsableProvider(providerID) {
		return fmt.Errorf("provider %q is not configured", providerID)
	}
	c.mu.RLock()
	sessionID := c.session.ID
	runtimes := make([]*chat.Chat, 0, len(c.runtimes))
	for _, rt := range c.runtimes {
		if rt != nil {
			runtimes = append(runtimes, rt)
		}
	}
	c.mu.RUnlock()
	if sessionID == "" {
		return fmt.Errorf("no active session")
	}
	if err := c.store.SetSessionModel(ctx, sessionID, providerID, modelID); err != nil {
		return err
	}
	session, err := c.store.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.session = session
	for idx := range c.sessions {
		if c.sessions[idx].ID == session.ID {
			c.sessions[idx] = session
		}
	}
	for id, snapshot := range c.snapshots {
		snapshot.Session = session
		c.snapshots[id] = snapshot
	}
	c.mu.Unlock()
	for _, rt := range runtimes {
		rt.SetSession(session)
	}
	c.broadcast("snapshot", c.State())
	return nil
}

// SetPermissionProfile updates the active session permission profile.
func (c *Controller) SetPermissionProfile(ctx context.Context, profile string) error {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return fmt.Errorf("permission profile is required")
	}
	if !permissionprofile.IsBuiltinProfile(profile) {
		if _, ok := c.cfg.Permissions.Profiles[profile]; !ok {
			return fmt.Errorf("unknown permission profile %q", profile)
		}
	}
	c.mu.Lock()
	session := c.session
	chatRecord := c.chat
	runtimes := make([]*chat.Chat, 0, len(c.runtimes))
	for _, rt := range c.runtimes {
		if rt != nil {
			runtimes = append(runtimes, rt)
		}
	}
	c.mu.Unlock()
	if session.ID != "" {
		if err := c.store.SetSessionPermissionProfile(ctx, session.ID, profile); err != nil {
			return err
		}
	}
	session.PermissionProfile = profile
	chatRecord.PermissionProfile = ""
	c.mu.Lock()
	c.session = session
	c.chat = chatRecord
	for idx := range c.sessions {
		if c.sessions[idx].ID == session.ID {
			c.sessions[idx].PermissionProfile = profile
		}
	}
	for idx := range c.chats {
		c.chats[idx].PermissionProfile = ""
	}
	for id, snapshot := range c.snapshots {
		snapshot.Session = session
		if snapshot.Chat.ID == chatRecord.ID {
			snapshot.Chat = chatRecord
		} else {
			snapshot.Chat.PermissionProfile = ""
		}
		c.snapshots[id] = snapshot
	}
	c.mu.Unlock()
	for _, rt := range runtimes {
		rt.SetSession(session)
		if snapshot := rt.Snapshot(); snapshot.Chat.ID == chatRecord.ID {
			rt.SetChat(chatRecord)
		}
	}
	c.broadcast("snapshot", c.State())
	return nil
}

func providerEntryLabel(providerID string, cfg config.Provider) string {
	if label := strings.TrimSpace(cfg.Name); label != "" {
		return label
	}
	return providerID
}

func (c *Controller) providerStateLocked() ProviderState {
	catalog := make([]ProviderCatalogItem, 0, len(provider.Catalog()))
	for _, item := range provider.Catalog() {
		catalog = append(catalog, ProviderCatalogItem{
			ID:             item.ID,
			Title:          item.Title,
			Description:    item.Description,
			DefaultBaseURL: item.DefaultBaseURL,
			ModelHint:      item.ModelHint,
			Local:          item.Local,
		})
	}

	ids := make([]string, 0, len(c.cfg.Providers))
	for id := range c.cfg.Providers {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	providers := make([]ProviderConfigItem, 0, len(ids))
	drafts := make(map[string]ProviderDraft, len(ids))
	for _, id := range ids {
		cfg := c.cfg.Providers[id]
		templateID := strings.TrimSpace(cfg.TemplateID)
		if templateID == "" {
			if draft, err := provider.BuildDraftForExisting(id, cfg); err == nil {
				templateID = draft.TemplateID
			}
		}
		providers = append(providers, ProviderConfigItem{
			ID:           id,
			Name:         providerEntryLabel(id, cfg),
			TemplateID:   templateID,
			Kind:         strings.TrimSpace(cfg.Kind),
			BaseURL:      strings.TrimSpace(cfg.BaseURL),
			DefaultModel: strings.TrimSpace(cfg.DefaultModel),
			Disabled:     cfg.Disabled,
			Default:      id == c.cfg.DefaultProvider,
		})
		if draft, err := provider.BuildDraftForExisting(id, cfg); err == nil {
			drafts[id] = providerDraftFromCatalog(draft)
		}
	}

	return ProviderState{
		DefaultProvider: strings.TrimSpace(c.cfg.DefaultProvider),
		DefaultModel:    strings.TrimSpace(c.cfg.DefaultModel),
		Catalog:         catalog,
		Providers:       providers,
		Drafts:          drafts,
	}
}

func providerDraftFromCatalog(draft provider.ConnectDraft) ProviderDraft {
	return ProviderDraft{
		OriginalProviderID: strings.TrimSpace(draft.OriginalProviderID),
		ProviderID:         strings.TrimSpace(draft.ProviderID),
		TemplateID:         strings.TrimSpace(draft.TemplateID),
		Kind:               strings.TrimSpace(draft.Kind),
		Name:               strings.TrimSpace(draft.Name),
		BaseURL:            strings.TrimSpace(draft.BaseURL),
		APIKey:             strings.TrimSpace(draft.APIKey),
		Model:              strings.TrimSpace(draft.Model),
		Headers:            cloneHeaderMap(draft.Headers),
	}
}

func providerDraftToCatalog(draft ProviderDraft) provider.ConnectDraft {
	return provider.ConnectDraft{
		OriginalProviderID: strings.TrimSpace(draft.OriginalProviderID),
		ProviderID:         strings.TrimSpace(draft.ProviderID),
		TemplateID:         strings.TrimSpace(draft.TemplateID),
		Kind:               strings.TrimSpace(draft.Kind),
		Name:               strings.TrimSpace(draft.Name),
		BaseURL:            strings.TrimSpace(draft.BaseURL),
		APIKey:             strings.TrimSpace(draft.APIKey),
		Model:              strings.TrimSpace(draft.Model),
		Headers:            cloneHeaderMap(draft.Headers),
	}
}

func cloneHeaderMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return map[string]string{}
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		dst[key] = strings.TrimSpace(value)
	}
	return dst
}

func mergeProviderEditDefaults(next *config.Provider, existing config.Provider) {
	next.AuthMethod = existing.AuthMethod
	next.APIKeyEnv = existing.APIKeyEnv
	next.ModelPreset = existing.ModelPreset
	next.ContextWindow = existing.ContextWindow
	next.AutoCompactAt = existing.AutoCompactAt
	next.Stream = existing.Stream
	next.Timeout = existing.Timeout
	next.Disabled = false
}

func applyNewProviderDefaults(next *config.Provider, autoCompactAt int) {
	if next.ContextWindow == 0 {
		next.ContextWindow = 32768
	}
	if autoCompactAt <= 0 {
		autoCompactAt = 80
	}
	next.AutoCompactAt = autoCompactAt
	next.Stream = true
	next.Timeout = 2 * time.Minute
	next.Disabled = false
}

func composerSkillQuery(value string, cursor int) (query string, start int, ok bool) {
	if cursor < 0 || cursor > len(value) {
		cursor = len(value)
	}
	if cursor == 0 || strings.TrimSpace(value) == "" {
		return "", 0, false
	}
	start, _ = composerTokenBounds(value, cursor)
	if start >= len(value) || value[start] != '$' {
		return "", 0, false
	}
	for _, r := range value[start+1 : cursor] {
		if isComposerWhitespace(r) {
			return "", 0, false
		}
	}
	return strings.ToLower(strings.TrimSpace(strings.TrimPrefix(value[start:cursor], "$"))), start, true
}

func composerMentionQuery(value string, cursor int) (query string, start int, end int, pathMode bool, ok bool) {
	if cursor < 0 || cursor > len(value) {
		cursor = len(value)
	}
	if cursor == 0 || strings.TrimSpace(value) == "" {
		return "", 0, 0, false, false
	}
	start, end = composerTokenBounds(value, cursor)
	if start >= len(value) || value[start] != '@' {
		return "", 0, 0, false, false
	}
	token := value[start:cursor]
	if strings.HasPrefix(token, `@"`) {
		query = strings.TrimSuffix(strings.TrimPrefix(token, `@"`), `"`)
	} else {
		query = strings.TrimPrefix(token, "@")
	}
	query = strings.TrimSpace(query)
	pathMode = strings.HasPrefix(query, "./") || strings.HasPrefix(query, "../") || strings.HasPrefix(query, "/")
	if pathMode {
		return query, start, end, pathMode, true
	}
	return strings.ToLower(query), start, end, pathMode, true
}

func composerTokenBounds(value string, cursor int) (start, end int) {
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(value) {
		cursor = len(value)
	}
	start = cursor
	for start > 0 && !isComposerTokenBoundary(rune(value[start-1])) {
		start--
	}
	end = cursor
	for end < len(value) && !isComposerTokenBoundary(rune(value[end])) {
		end++
	}
	return start, end
}

func isComposerWhitespace(r rune) bool {
	switch r {
	case ' ', '\t', '\n':
		return true
	default:
		return false
	}
}

func isComposerTokenBoundary(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '(', '[', '{':
		return true
	default:
		return false
	}
}

func matchingComposerSkills(workdir string, query string) []skills.Skill {
	var matches []skills.Skill
	for _, item := range skills.Discover(workdir) {
		name := strings.ToLower(strings.TrimSpace(item.Name))
		if query == "" || strings.HasPrefix(name, query) {
			matches = append(matches, item)
		}
	}
	return matches
}

func blankAsDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func (c *Controller) initialSession(ctx context.Context, mode StartupMode) (domain.Session, error) {
	if c.store == nil {
		return domain.Session{}, fmt.Errorf("store is unavailable")
	}
	if mode == StartupModeNew {
		return c.createWorkspaceSession(ctx, "New Session")
	}
	sessions, err := c.workspaceSessions(ctx)
	if err != nil {
		return domain.Session{}, err
	}
	if len(sessions) == 0 {
		return c.initialSession(ctx, StartupModeNew)
	}
	return newestSession(sessions), nil
}

func (c *Controller) loadSession(ctx context.Context, sessionID, chatID domain.ID) error {
	session, err := c.store.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}
	if !c.sessionInWorkspace(session) {
		return fmt.Errorf("session %s does not belong to this workspace", sessionID)
	}
	sessions, err := c.workspaceSessions(ctx)
	if err != nil {
		return err
	}
	chats, err := c.store.ListChats(ctx, session.ID)
	if err != nil {
		return err
	}
	var chatRecord domain.Chat
	if chatID != "" {
		chatRecord, err = c.store.GetChat(ctx, chatID)
		if err != nil {
			return err
		}
		if chatRecord.SessionID != session.ID {
			return fmt.Errorf("chat %s does not belong to session %s", chatID, session.ID)
		}
	} else {
		chatRecord = newestChat(chats)
		if chatRecord.ID == "" {
			chatRecord, err = c.store.DefaultChat(ctx, session.ID)
			if err != nil {
				return err
			}
		}
	}
	chatRecord.PermissionProfile = ""
	c.mu.RLock()
	existingRuntimes := make(map[domain.ID]*chat.Chat, len(c.runtimes))
	for id, rt := range c.runtimes {
		existingRuntimes[id] = rt
	}
	existingUnsubs := make(map[domain.ID]func(), len(c.unsubs))
	for id, unsub := range c.unsubs {
		existingUnsubs[id] = unsub
	}
	c.mu.RUnlock()

	type runtimeSubscription struct {
		chatID  domain.ID
		updates <-chan chat.Update
	}
	runtimes := make(map[domain.ID]*chat.Chat, len(chats))
	unsubs := make(map[domain.ID]func(), len(chats))
	var subscriptions []runtimeSubscription
	for _, item := range chats {
		item.PermissionProfile = ""
		rt := existingRuntimes[item.ID]
		if rt == nil {
			var err error
			rt, err = c.agent.Chat(ctx, session, item)
			if err != nil {
				return err
			}
			updates, unsub := rt.Subscribe()
			unsubs[item.ID] = unsub
			subscriptions = append(subscriptions, runtimeSubscription{chatID: item.ID, updates: updates})
		} else {
			rt.SetSession(session)
			rt.SetChat(item)
			if unsub := existingUnsubs[item.ID]; unsub != nil {
				unsubs[item.ID] = unsub
			} else {
				updates, unsub := rt.Subscribe()
				unsubs[item.ID] = unsub
				subscriptions = append(subscriptions, runtimeSubscription{chatID: item.ID, updates: updates})
			}
		}
		runtimes[item.ID] = rt
	}
	rt := runtimes[chatRecord.ID]
	if rt == nil {
		return fmt.Errorf("chat %s runtime was not loaded", chatRecord.ID)
	}
	milestone, todos, todosByRef := c.planningState(ctx, session.ID)
	workspaceStatus, _ := workspacepkg.Snapshot(ctx, c.workdir)
	statuses := c.chatStatuses(ctx, session.ID)
	snapshots := make(map[domain.ID]chat.Snapshot, len(runtimes))
	for id, loaded := range runtimes {
		snapshot := loaded.Snapshot()
		snapshots[id] = snapshot
		statuses[id] = sidebarStatusFromSnapshot(snapshot)
	}

	c.mu.Lock()
	for id, unsub := range c.unsubs {
		if _, keep := runtimes[id]; !keep && unsub != nil {
			unsub()
		}
	}
	c.session = session
	c.sessions = sessions
	c.chats = chats
	c.chat = chatRecord
	c.runtime = rt
	c.unsub = nil
	c.runtimes = runtimes
	c.unsubs = unsubs
	c.snapshots = snapshots
	c.statuses = statuses
	c.milestone = milestone
	c.todos = todos
	c.todosByRef = todosByRef
	c.workspace = workspaceStatus
	c.lastErr = ""
	c.mu.Unlock()

	for _, sub := range subscriptions {
		go c.forwardRuntime(sub.chatID, sub.updates)
	}
	c.broadcast("snapshot", c.State())
	return nil
}

func (c *Controller) createWorkspaceSession(ctx context.Context, title string) (domain.Session, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		title = "New Session"
	}
	session, err := c.store.CreateSession(ctx, title, c.cfg.DefaultProvider, c.cfg.DefaultModel, nil)
	if err != nil {
		return domain.Session{}, err
	}
	_ = c.store.UpdateSessionWorkspace(ctx, session.ID, c.workdir, c.workdir)
	_ = c.store.SetSessionPermissionProfile(ctx, session.ID, c.cfg.Permissions.Profile)
	_ = c.store.SetSessionToolStates(ctx, session.ID, c.cfg.ToolDefaults)
	return c.store.GetSession(ctx, session.ID)
}

func (c *Controller) workspaceSessions(ctx context.Context) ([]domain.Session, error) {
	sessions, err := c.store.ListSessions(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Session, 0, len(sessions))
	for _, session := range sessions {
		if c.sessionInWorkspace(session) {
			out = append(out, session)
		}
	}
	return out, nil
}

func (c *Controller) sessionInWorkspace(session domain.Session) bool {
	workdir := normalizedWorkspacePath(c.workdir)
	if workdir == "" {
		return true
	}
	return normalizedWorkspacePath(session.CWD) == workdir || normalizedWorkspacePath(session.ProjectRoot) == workdir
}

func (c *Controller) planningState(ctx context.Context, sessionID domain.ID) (store.MilestonePlan, []store.TodoItem, map[string][]store.TodoItem) {
	plan, err := c.store.GetMilestonePlan(ctx, sessionID)
	if err != nil {
		return store.MilestonePlan{}, nil, nil
	}
	todosByRef := make(map[string][]store.TodoItem, len(plan.Milestones))
	for _, milestone := range plan.Milestones {
		ref := strings.TrimSpace(milestone.Ref)
		if ref == "" {
			continue
		}
		todos, err := c.store.ListTodos(ctx, sessionID, ref)
		if err != nil {
			todosByRef[ref] = nil
			continue
		}
		todosByRef[ref] = slices.Clone(todos)
	}
	active, ok := tools.ActiveMilestone(plan)
	if !ok {
		return plan, nil, todosByRef
	}
	return plan, slices.Clone(todosByRef[active.Ref]), todosByRef
}

func cloneTodosByRef(in map[string][]store.TodoItem) map[string][]store.TodoItem {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string][]store.TodoItem, len(in))
	for ref, todos := range in {
		out[ref] = slices.Clone(todos)
	}
	return out
}

func (c *Controller) chatStatuses(ctx context.Context, sessionID domain.ID) map[domain.ID]ChatSidebarStatus {
	out := map[domain.ID]ChatSidebarStatus{}
	if sessionID == "" {
		return out
	}
	if c.agent != nil {
		statuses, err := c.agent.ListChats(ctx, sessionID)
		if err == nil {
			for _, status := range statuses {
				out[status.Chat.ID] = sidebarStatusFromToolStatus(status)
			}
			return out
		}
	}
	if c.store == nil {
		return out
	}
	chats, err := c.store.ListChats(ctx, sessionID)
	if err != nil {
		return out
	}
	for _, item := range chats {
		out[item.ID] = idleChatSidebarStatus(item.ID)
	}
	return out
}

func (c *Controller) refreshChatStatuses(ctx context.Context, sessionID domain.ID) bool {
	var chats []domain.Chat
	if c.store != nil && sessionID != "" {
		if loaded, err := c.store.ListChats(ctx, sessionID); err == nil {
			chats = loaded
		}
	}
	statuses := c.chatStatuses(ctx, sessionID)
	if status, ok := c.activeChatSidebarStatus(sessionID); ok {
		statuses[status.ChatID] = status
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if sessionID == "" || c.session.ID != sessionID {
		return false
	}
	for idx := range chats {
		if snapshot, ok := c.snapshots[chats[idx].ID]; ok && snapshot.Chat.ID == chats[idx].ID {
			chats[idx] = snapshot.Chat
		}
	}
	changed := !chatSidebarStatusMapsEqual(c.statuses, statuses)
	if len(chats) > 0 && !chatListsSameForSidebar(c.chats, chats) {
		c.chats = chats
		changed = true
	}
	if !changed {
		return false
	}
	c.statuses = statuses
	return true
}

func (c *Controller) cachedChatStatuses(ctx context.Context, sessionID domain.ID) map[domain.ID]ChatSidebarStatus {
	c.mu.RLock()
	cached := make(map[domain.ID]ChatSidebarStatus, len(c.chats))
	for _, item := range c.chats {
		status, ok := c.statuses[item.ID]
		if !ok {
			status = idleChatSidebarStatus(item.ID)
		}
		cached[item.ID] = status
	}
	c.mu.RUnlock()
	if len(cached) > 0 || c.store == nil || sessionID == "" {
		return cached
	}
	chats, err := c.store.ListChats(ctx, sessionID)
	if err != nil {
		return cached
	}
	for _, item := range chats {
		cached[item.ID] = idleChatSidebarStatus(item.ID)
	}
	return cached
}

func (c *Controller) chatStatusesLocked() []ChatSidebarStatus {
	out := make([]ChatSidebarStatus, 0, len(c.chats))
	for _, item := range c.chats {
		status, ok := c.statuses[item.ID]
		if !ok {
			status = idleChatSidebarStatus(item.ID)
		}
		out = append(out, status)
	}
	return out
}

func (c *Controller) activeChatSidebarStatus(sessionID domain.ID) (ChatSidebarStatus, bool) {
	c.mu.RLock()
	rt := c.runtime
	activeSessionID := c.session.ID
	c.mu.RUnlock()
	if rt == nil || activeSessionID != sessionID {
		return ChatSidebarStatus{}, false
	}
	status := sidebarStatusFromSnapshot(rt.Snapshot())
	return status, status.ChatID != ""
}

func (c *Controller) forwardRuntime(chatID domain.ID, updates <-chan chat.Update) {
	for update := range updates {
		c.mu.RLock()
		sessionID := c.session.ID
		activeChatID := c.chat.ID
		_, subscribed := c.runtimes[chatID]
		_, hasSnapshot := c.snapshots[chatID]
		c.mu.RUnlock()
		if !subscribed && chatID != activeChatID && !hasSnapshot {
			return
		}
		if update.Event != nil && update.Event.Err != nil {
			c.mu.Lock()
			c.lastErr = update.Event.Err.Error()
			c.mu.Unlock()
		}
		if update.Snapshot.Chat.ID == "" {
			update.Snapshot.Chat.ID = chatID
		}
		if update.Snapshot.Chat.ID == chatID {
			c.mu.Lock()
			stalePassive := false
			if existing, ok := c.snapshots[chatID]; ok && runtimeUpdateIsPassive(update) && !update.Snapshot.Chat.UpdatedAt.After(existing.Chat.UpdatedAt) {
				stalePassive = true
			}
			if stalePassive {
				c.mu.Unlock()
			} else {
				if strings.TrimSpace(update.Snapshot.Chat.Title) == "" {
					if existing, ok := chatByID(c.chats, chatID); ok {
						update.Snapshot.Chat = existing
					} else if activeChatID == chatID {
						update.Snapshot.Chat = c.chat
					}
				}
				if activeChatID == chatID {
					c.chat = update.Snapshot.Chat
				}
				if c.statuses == nil {
					c.statuses = map[domain.ID]ChatSidebarStatus{}
				}
				if c.snapshots == nil {
					c.snapshots = map[domain.ID]chat.Snapshot{}
				}
				c.snapshots[chatID] = update.Snapshot
				c.statuses[chatID] = sidebarStatusFromUpdate(update)
				found := false
				for idx := range c.chats {
					if c.chats[idx].ID == update.Snapshot.Chat.ID {
						c.chats[idx] = update.Snapshot.Chat
						found = true
						break
					}
				}
				if !found {
					c.chats = append([]domain.Chat{update.Snapshot.Chat}, c.chats...)
				}
				c.mu.Unlock()
			}
		} else {
			c.mu.Lock()
			if c.statuses == nil {
				c.statuses = map[domain.ID]ChatSidebarStatus{}
			}
			c.statuses[chatID] = sidebarStatusFromUpdate(update)
			c.mu.Unlock()
		}
		c.refreshPlanningState(context.Background(), sessionID)
		c.broadcast("chat_update", update)
		if runtimeUpdateNeedsStateSnapshot(update) {
			c.broadcast("snapshot", c.State())
		}
	}
}

func runtimeUpdateIsPassive(update chat.Update) bool {
	return update.Event == nil && !update.Active && !update.QueueChanged && !update.ApprovalsChanged
}

func runtimeUpdateNeedsStateSnapshot(update chat.Update) bool {
	if update.QueueChanged || update.ApprovalsChanged {
		return true
	}
	if update.Event == nil {
		return false
	}
	switch update.Event.Kind {
	case domain.EventKindToolResult, domain.EventKindApprovalAsk, domain.EventKindApprovalReply, domain.EventKindChatTitle, domain.EventKindSessionTitle, domain.EventKindError, domain.EventKindMessageDone:
		return true
	default:
		return false
	}
}

func idleChatSidebarStatus(chatID domain.ID) ChatSidebarStatus {
	return ChatSidebarStatus{ChatID: chatID, Status: string(chat.StatusIdle), StatusText: "Idle"}
}

func sidebarStatusFromToolStatus(status tools.ChatStatus) ChatSidebarStatus {
	value := strings.TrimSpace(status.Status)
	if value == "" {
		value = string(status.State)
	}
	if value == "" {
		value = string(chat.StatusIdle)
	}
	text := strings.TrimSpace(status.StatusText)
	if text == "" {
		text = chatSidebarStatusText(value)
	}
	return ChatSidebarStatus{
		ChatID:           status.Chat.ID,
		Status:           value,
		Busy:             status.Busy,
		PendingApprovals: status.PendingApprovals,
		StatusText:       text,
		LastError:        status.LastError,
	}
}

func sidebarStatusFromUpdate(update chat.Update) ChatSidebarStatus {
	status := update.Status
	if status == "" {
		status = update.Snapshot.Status
	}
	text := strings.TrimSpace(update.StatusText)
	if text == "" {
		text = strings.TrimSpace(update.Snapshot.StatusText)
	}
	return sidebarStatusFromSnapshot(chat.Snapshot{
		Chat:       update.Snapshot.Chat,
		Status:     status,
		StatusText: text,
		Active:     update.Active || update.Snapshot.Active,
		Approvals:  update.Snapshot.Approvals,
	})
}

func sidebarStatusFromSnapshot(snapshot chat.Snapshot) ChatSidebarStatus {
	value := string(snapshot.Status)
	if value == "" {
		value = string(chat.StatusIdle)
	}
	text := strings.TrimSpace(snapshot.StatusText)
	if text == "" {
		text = chatSidebarStatusText(value)
	}
	return ChatSidebarStatus{
		ChatID:           snapshot.Chat.ID,
		Status:           value,
		Busy:             snapshot.Active || value == string(chat.StatusRunningTools) || value == string(chat.StatusWaitingLLM) || value == string(chat.StatusStreamingResponse) || value == string(chat.StatusStreamingThoughts) || value == string(chat.StatusWaitingApproval),
		PendingApprovals: len(snapshot.Approvals),
		StatusText:       text,
	}
}

func mergeChatSidebarStatus(statuses []ChatSidebarStatus, status ChatSidebarStatus) []ChatSidebarStatus {
	if status.ChatID == "" {
		return statuses
	}
	for idx := range statuses {
		if statuses[idx].ChatID == status.ChatID {
			statuses[idx] = status
			return statuses
		}
	}
	return append(statuses, status)
}

func hasChatSidebarStatus(statuses []ChatSidebarStatus, chatID domain.ID) bool {
	if chatID == "" {
		return false
	}
	for _, status := range statuses {
		if status.ChatID == chatID {
			return true
		}
	}
	return false
}

func chatSidebarStatusMapsEqual(left, right map[domain.ID]ChatSidebarStatus) bool {
	if len(left) != len(right) {
		return false
	}
	for id, leftStatus := range left {
		if right[id] != leftStatus {
			return false
		}
	}
	return true
}

func chatListsSameForSidebar(left, right []domain.Chat) bool {
	if len(left) != len(right) {
		return false
	}
	for idx := range left {
		if left[idx].ID != right[idx].ID ||
			left[idx].ParentChatID != right[idx].ParentChatID ||
			left[idx].Title != right[idx].Title ||
			left[idx].WorkflowRole != right[idx].WorkflowRole ||
			left[idx].ActiveMilestoneRef != right[idx].ActiveMilestoneRef ||
			left[idx].AssignedTodoBucketRef != right[idx].AssignedTodoBucketRef ||
			left[idx].LastMessage != right[idx].LastMessage ||
			!left[idx].UpdatedAt.Equal(right[idx].UpdatedAt) {
			return false
		}
	}
	return true
}

func chatSidebarStatusText(status string) string {
	switch status {
	case string(chat.StatusWaitingLLM):
		return "Waiting for LLM"
	case string(chat.StatusStreamingThoughts):
		return "Streaming reasoning"
	case string(chat.StatusStreamingResponse):
		return "Streaming response"
	case string(chat.StatusRunningTools):
		return "Running tools"
	case string(chat.StatusWaitingApproval):
		return "Waiting for approval"
	case string(chat.StatusErrored):
		return "Error"
	case string(tools.ChatRunStateFailed):
		return "Failed"
	case string(tools.ChatRunStateRunning):
		return "Running"
	case string(tools.ChatRunStateCompleted):
		return "Completed"
	case string(tools.ChatRunStateCancelled):
		return "Cancelled"
	default:
		return "Idle"
	}
}

func (c *Controller) refreshPlanningState(ctx context.Context, sessionID domain.ID) {
	if sessionID == "" {
		return
	}
	milestone, todos, todosByRef := c.planningState(ctx, sessionID)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session.ID != sessionID {
		return
	}
	c.milestone = milestone
	c.todos = todos
	c.todosByRef = todosByRef
}

func (c *Controller) monitorWorkspace(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = c.RefreshWorkspace(ctx)
			c.mu.RLock()
			sessionID := c.session.ID
			c.mu.RUnlock()
			if c.refreshChatStatuses(ctx, sessionID) {
				c.broadcast("snapshot", c.State())
			}
		}
	}
}

func (c *Controller) currentRuntime() *chat.Chat {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.runtime
}

func (c *Controller) permissionsStateLocked() PermissionsState {
	active := strings.TrimSpace(c.session.PermissionProfile)
	if active == "" {
		active = c.cfg.Permissions.Profile
	}
	profiles := permissionprofile.BuiltinProfiles()
	seen := map[string]struct{}{}
	for _, item := range profiles {
		seen[item.Name] = struct{}{}
	}
	for _, name := range permissionprofile.ProfileNames(c.cfg.Permissions) {
		if _, ok := seen[name]; ok {
			continue
		}
		profiles = append(profiles, permissionprofile.ProfileOption{Name: name, Label: permissionprofile.DisplayName(name)})
	}
	return PermissionsState{Active: active, Profiles: profiles}
}

func (c *Controller) contextWindowLocked() int {
	providerID := strings.TrimSpace(c.session.ProviderID)
	if providerID != "" {
		if providerCfg, ok := c.cfg.Providers[providerID]; ok && providerCfg.ContextWindow > 0 {
			return providerCfg.ContextWindow
		}
	}
	if providerCfg, ok := c.cfg.Providers[c.cfg.DefaultProvider]; ok && providerCfg.ContextWindow > 0 {
		return providerCfg.ContextWindow
	}
	return 32768
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
	defer c.subMu.Unlock()
	c.nextSeq++
	evt := Event{Seq: c.nextSeq, Type: typ, Payload: payload}
	for _, ch := range c.subs {
		select {
		case ch <- evt:
		default:
		}
	}
}

func newestSession(sessions []domain.Session) domain.Session {
	var best domain.Session
	for _, item := range sessions {
		if item.ID == "" {
			continue
		}
		if best.ID == "" || item.UpdatedAt.After(best.UpdatedAt) || (item.UpdatedAt.Equal(best.UpdatedAt) && item.ID > best.ID) {
			best = item
		}
	}
	return best
}

func newestChat(chats []domain.Chat) domain.Chat {
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

func chatByID(chats []domain.Chat, chatID domain.ID) (domain.Chat, bool) {
	for _, item := range chats {
		if item.ID == chatID {
			return item, true
		}
	}
	return domain.Chat{}, false
}

func fallbackChatID(chats []domain.Chat, deleting domain.Chat) domain.ID {
	if deleting.ParentChatID != nil {
		for _, item := range chats {
			if item.ID == *deleting.ParentChatID && item.ID != deleting.ID {
				return item.ID
			}
		}
	}
	for _, item := range chats {
		if item.ID != deleting.ID {
			return item.ID
		}
	}
	return ""
}

func normalizedWorkspacePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}

func workspaceSignature(status workspacepkg.Status) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%t\n%s\n%s\n%s\n%s\n%s\n%d/%d/%d/%d\n",
		status.Available,
		status.ProjectRoot,
		status.AgentsChecksum,
		status.Branch,
		status.Upstream,
		status.Summary,
		status.Added,
		status.Modified,
		status.Deleted,
		status.Untracked,
	))
	for _, file := range status.Files {
		b.WriteString(file.Code)
		b.WriteByte('\t')
		b.WriteString(file.Path)
		b.WriteByte('\t')
		b.WriteString(fmt.Sprintf("%d/%d\n", file.Additions, file.Deletions))
	}
	return b.String()
}

// Touch avoids stale-session ordering when a renderer action changes state.
func Touch(now time.Time, chat *domain.Chat) {
	if chat != nil {
		chat.UpdatedAt = now
	}
}
