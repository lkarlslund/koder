package uicore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lkarlslund/koder/internal/agent"
	"github.com/lkarlslund/koder/internal/assets"
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
	"github.com/lkarlslund/koder/internal/theme"
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
	ModelInfo     ModelInfo                   `json:"model_info"`
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

// ModelInfo describes the active model capabilities shown by renderers.
type ModelInfo struct {
	ProviderID        string `json:"provider_id"`
	ModelID           string `json:"model_id"`
	ContextWindow     int    `json:"context_window"`
	SupportsTools     bool   `json:"supports_tools"`
	SupportsImages    bool   `json:"supports_images"`
	SupportsPDFs      bool   `json:"supports_pdfs"`
	CapabilitiesKnown bool   `json:"capabilities_known"`
	CapabilitySource  string `json:"capability_source,omitempty"`
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
	AuthMethod         string            `json:"auth_method"`
	Name               string            `json:"name"`
	BaseURL            string            `json:"base_url"`
	APIKey             string            `json:"api_key"`
	APIKeyEnv          string            `json:"api_key_env"`
	Model              string            `json:"model"`
	ModelPreset        string            `json:"model_preset"`
	ContextWindow      int               `json:"context_window"`
	AutoCompactAt      int               `json:"auto_compact_at"`
	Stream             bool              `json:"stream"`
	Timeout            string            `json:"timeout"`
	Disabled           bool              `json:"disabled"`
	Headers            map[string]string `json:"headers"`
}

// ProviderProbeResult reports a provider test outcome.
type ProviderProbeResult struct {
	ModelCount int      `json:"model_count"`
	Models     []string `json:"models"`
}

// PreferencesState is the complete settings payload exposed to web renderers.
type PreferencesState struct {
	General      GeneralPreferences      `json:"general"`
	UI           UIPreferences           `json:"ui"`
	Compaction   CompactionPreferences   `json:"compaction"`
	Prompts      []PromptPreference      `json:"prompts"`
	Providers    ProviderState           `json:"providers"`
	Models       []ModelOption           `json:"models"`
	MCPServers   []MCPServerPreference   `json:"mcp_servers"`
	Permissions  PermissionPreferences   `json:"permissions"`
	ToolDefaults []ToolDefaultPreference `json:"tool_defaults"`
	RestartKeys  []string                `json:"restart_keys,omitempty"`
}

// GeneralPreferences contains global non-provider settings.
type GeneralPreferences struct {
	DefaultProvider  string `json:"default_provider"`
	DefaultModel     string `json:"default_model"`
	MaxToolLoopSteps int    `json:"max_tool_loop_steps"`
	StoreBackend     string `json:"store_backend"`
}

// UIPreferences contains renderer behavior settings persisted in config.
type UIPreferences struct {
	Theme            string   `json:"theme"`
	CodeStyle        string   `json:"code_style"`
	CodeStyleOptions []string `json:"code_style_options"`
	EditForgiveness  int      `json:"edit_forgiveness"`
	CursorBlink      bool     `json:"cursor_blink"`
	HalfBlocks       bool     `json:"half_blocks"`
	ShowSidebar      bool     `json:"show_sidebar"`
	SidebarWidth     int      `json:"sidebar_width"`
	ShowTimestamps   bool     `json:"show_timestamps"`
	ShowReasoning    bool     `json:"show_reasoning"`
	ShowSystem       bool     `json:"show_system"`
	Mouse            bool     `json:"mouse"`
	AutoContinue     bool     `json:"auto_continue"`
}

// CompactionPreferences contains global compaction controls.
type CompactionPreferences struct {
	AutoCompactAt        int    `json:"auto_compact_at"`
	KeepToolBatches      int    `json:"keep_tool_batches"`
	ProviderID           string `json:"provider_id"`
	ModelID              string `json:"model_id"`
	UseChatModel         bool   `json:"use_chat_model"`
	CurrentSelectionText string `json:"current_selection_text"`
}

// PromptPreference is one editable managed prompt file.
type PromptPreference struct {
	Name    string `json:"name"`
	Target  string `json:"target"`
	Path    string `json:"path"`
	Content string `json:"content"`
}

// MCPServerPreference is one editable MCP server config row.
type MCPServerPreference struct {
	ID                   string            `json:"id"`
	Name                 string            `json:"name"`
	URL                  string            `json:"url"`
	Headers              map[string]string `json:"headers"`
	Disabled             bool              `json:"disabled"`
	StartupTimeout       string            `json:"startup_timeout"`
	RequestTimeout       string            `json:"request_timeout"`
	DisableStandaloneSSE bool              `json:"disable_standalone_sse"`
	BearerToken          string            `json:"bearer_token"`
	BearerTokenEnv       string            `json:"bearer_token_env"`
}

// PermissionPreferences is the editable permission profile config.
type PermissionPreferences struct {
	Active   string                        `json:"active"`
	Profiles []PermissionProfilePreference `json:"profiles"`
}

// PermissionProfilePreference is one named permission profile.
type PermissionProfilePreference struct {
	Name      string                      `json:"name"`
	Network   bool                        `json:"network"`
	Root      string                      `json:"root"`
	Workspace string                      `json:"workspace"`
	Mounts    []PermissionMountPreference `json:"mounts"`
}

// PermissionMountPreference is one extra sandbox folder mount.
type PermissionMountPreference struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
}

// ToolDefaultPreference is one default per-session tool enabled toggle.
type ToolDefaultPreference struct {
	Tool    domain.ToolKind `json:"tool"`
	Enabled bool            `json:"enabled"`
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
}

// New constructs a renderer-neutral controller.
func New(cfg config.Config, st *store.Store, engine *agent.Engine, workdir string) *Controller {
	return &Controller{
		cfg:       cfg,
		store:     st,
		agent:     engine,
		workdir:   strings.TrimSpace(workdir),
		theme:     normalizeTheme(cfg.UI.Theme),
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
		ModelInfo:     c.modelInfoLocked(),
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
	return c.SendPromptWithKindAndAttachments(text, chat.QueueKindSteer, drafts)
}

// SendPromptWithKindAndAttachments enqueues a prompt with the given queue kind (steer or queue).
func (c *Controller) SendPromptWithKindAndAttachments(text string, kind chat.QueueKind, drafts []attachment.Draft) error {
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
	rt.Enqueue(chat.QueueItem{Kind: kind, Text: text, Attachments: validated})
	return nil
}

// ReorderQueue reorders the queued inputs in the active chat by their IDs.
func (c *Controller) ReorderQueue(ids []domain.ID) error {
	rt := c.currentRuntime()
	if rt == nil {
		return fmt.Errorf("no active chat")
	}
	rt.ReorderQueue(ids)
	return nil
}

// DeleteQueueItem removes a queued input from the active chat by ID.
func (c *Controller) DeleteQueueItem(id domain.ID) error {
	rt := c.currentRuntime()
	if rt == nil {
		return fmt.Errorf("no active chat")
	}
	rt.DeleteQueueItem(id)
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
	return c.ShutdownWithInterruptReason(ctx, "")
}

// ShutdownWithInterruptReason closes runtimes and records an interrupt reason for active chats.
func (c *Controller) ShutdownWithInterruptReason(ctx context.Context, reason string) error {
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
	reason = strings.TrimSpace(reason)
	var firstErr error
	for _, rt := range runtimes {
		var err error
		if reason == "" {
			err = rt.DrainAndClose(ctx)
		} else {
			err = rt.InterruptAndClose(ctx, reason)
		}
		if err != nil && firstErr == nil {
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

// ReorderChats persists the sidebar chat order for the active session.
func (c *Controller) ReorderChats(ctx context.Context, chatIDs []domain.ID) error {
	c.mu.RLock()
	sessionID := c.session.ID
	activeChatID := c.chat.ID
	runtimes := make(map[domain.ID]*chat.Chat, len(c.runtimes))
	for id, rt := range c.runtimes {
		runtimes[id] = rt
	}
	c.mu.RUnlock()
	if sessionID == "" {
		return fmt.Errorf("no active session")
	}
	ordered, err := c.store.ReorderChats(ctx, sessionID, chatIDs)
	if err != nil {
		return err
	}
	for _, item := range ordered {
		if rt := runtimes[item.ID]; rt != nil {
			rt.SetChat(item)
		}
	}
	c.mu.Lock()
	c.chats = ordered
	for _, item := range ordered {
		if item.ID == activeChatID {
			c.chat = item
		}
		if snapshot, ok := c.snapshots[item.ID]; ok {
			snapshot.Chat = item
			c.snapshots[item.ID] = snapshot
		}
	}
	c.mu.Unlock()
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

// CompleteComposer returns command, skill, and reference completions for the current composer token.
func (c *Controller) CompleteComposer(text string, cursor int) (ComposerCompletions, error) {
	if cursor < 0 || cursor > len(text) {
		cursor = len(text)
	}
	if query, start, end, ok := composerCommandQuery(text, cursor); ok {
		items := matchingComposerCommands(query)
		if len(items) == 1 && items[0].Command == strings.TrimSpace(text[start:end]) {
			items = nil
		}
		out := ComposerCompletions{Kind: "command", Query: query, Start: start, End: end}
		for _, item := range items {
			out.Items = append(out.Items, ComposerCompletionItem{
				Label:       item.Command,
				InsertText:  item.Command,
				Description: item.Description,
				Kind:        "command",
			})
		}
		return out, nil
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
	applyProviderDraftPreferences(&next, draft)
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

// Preferences returns the complete editable settings state.
func (c *Controller) Preferences(ctx context.Context) (PreferencesState, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.preferencesStateLocked(ctx)
}

// SavePreferences validates and persists the complete settings state.
func (c *Controller) SavePreferences(ctx context.Context, prefs PreferencesState) (PreferencesState, error) {
	next := config.Default()
	c.mu.Lock()
	next = c.cfg
	if err := applyGeneralPreferences(&next, prefs.General); err != nil {
		c.mu.Unlock()
		return PreferencesState{}, err
	}
	if err := applyUIPreferences(&next, prefs.UI); err != nil {
		c.mu.Unlock()
		return PreferencesState{}, err
	}
	if err := applyCompactionPreferences(&next, prefs.Compaction); err != nil {
		c.mu.Unlock()
		return PreferencesState{}, err
	}
	if err := applyMCPPreferences(&next, prefs.MCPServers); err != nil {
		c.mu.Unlock()
		return PreferencesState{}, err
	}
	if err := applyPermissionPreferences(&next, prefs.Permissions); err != nil {
		c.mu.Unlock()
		return PreferencesState{}, err
	}
	applyToolDefaultPreferences(&next, prefs.ToolDefaults)
	if err := writePromptPreferences(prefs.Prompts); err != nil {
		c.mu.Unlock()
		return PreferencesState{}, err
	}
	c.cfg = next
	c.theme = normalizeTheme(next.UI.Theme)
	if err := c.cfg.Save(); err != nil {
		c.mu.Unlock()
		return PreferencesState{}, err
	}
	if c.agent != nil {
		c.agent.UpdateConfig(c.cfg)
	}
	state, err := c.preferencesStateLocked(ctx)
	c.mu.Unlock()
	if err != nil {
		return PreferencesState{}, err
	}
	c.broadcast("snapshot", c.State())
	c.broadcast("theme", map[string]string{"theme": c.theme})
	return state, nil
}

// ResetPrompt restores one managed prompt file from embedded defaults.
func (c *Controller) ResetPrompt(target string) (PromptPreference, error) {
	target = strings.TrimSpace(target)
	if target != "system-prompt.md" && target != "compaction-prompt.md" {
		return PromptPreference{}, fmt.Errorf("unknown prompt %q", target)
	}
	content, err := assets.DefaultContent(target)
	if err != nil {
		return PromptPreference{}, err
	}
	path, err := managedPromptPath(target)
	if err != nil {
		return PromptPreference{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return PromptPreference{}, fmt.Errorf("create prompt dir: %w", err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return PromptPreference{}, fmt.Errorf("write prompt %s: %w", target, err)
	}
	return promptPreference(target)
}

// ModelOptions lists selectable models across configured providers.
func (c *Controller) ModelOptions(ctx context.Context) ([]ModelOption, error) {
	c.mu.RLock()
	cfg := c.cfg
	currentProvider := strings.TrimSpace(c.session.ProviderID)
	currentModel := strings.TrimSpace(c.session.ModelID)
	c.mu.RUnlock()
	return modelOptionsForConfig(ctx, cfg, currentProvider, currentModel)
}

func (c *Controller) modelOptionsLocked(ctx context.Context) ([]ModelOption, error) {
	return modelOptionsForConfig(ctx, c.cfg, strings.TrimSpace(c.session.ProviderID), strings.TrimSpace(c.session.ModelID))
}

func modelOptionsForConfig(ctx context.Context, cfg config.Config, currentProvider, currentModel string) ([]ModelOption, error) {
	seen := map[string]struct{}{}
	options := make([]ModelOption, 0, len(cfg.Providers))
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

	ids := make([]string, 0, len(cfg.Providers))
	for id, providerCfg := range cfg.Providers {
		if providerCfg.Disabled {
			continue
		}
		ids = append(ids, id)
	}
	slices.Sort(ids)

	var failures []string
	for _, providerID := range ids {
		providerCfg, ok := cfg.Provider(providerID)
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

func (c *Controller) preferencesStateLocked(ctx context.Context) (PreferencesState, error) {
	models, _ := c.modelOptionsLocked(ctx)
	models = ensureModelOption(models, c.cfg, strings.TrimSpace(c.cfg.CompactionProvider), strings.TrimSpace(c.cfg.CompactionModel))
	prompts, err := promptPreferences()
	if err != nil {
		return PreferencesState{}, err
	}
	state := PreferencesState{
		General: GeneralPreferences{
			DefaultProvider:  strings.TrimSpace(c.cfg.DefaultProvider),
			DefaultModel:     strings.TrimSpace(c.cfg.DefaultModel),
			MaxToolLoopSteps: c.cfg.MaxToolLoopSteps,
			StoreBackend:     strings.TrimSpace(c.cfg.Store.Backend),
		},
		UI:           uiPreferencesFromConfig(c.cfg.UI),
		Compaction:   compactionPreferencesFromConfig(c.cfg),
		Prompts:      prompts,
		Providers:    c.providerStateLocked(),
		Models:       models,
		MCPServers:   mcpPreferencesFromConfig(c.cfg.MCPServers),
		Permissions:  permissionPreferencesFromConfig(c.cfg.Permissions),
		ToolDefaults: toolDefaultPreferencesFromConfig(c.cfg.ToolDefaults),
	}
	if c.cfg.Store.Backend != config.Default().Store.Backend {
		state.RestartKeys = append(state.RestartKeys, "store.backend")
	}
	return state, nil
}

func ensureModelOption(options []ModelOption, cfg config.Config, providerID, modelID string) []ModelOption {
	if providerID == "" || modelID == "" {
		return options
	}
	for _, option := range options {
		if option.ProviderID == providerID && option.ModelID == modelID {
			return options
		}
	}
	providerCfg, ok := cfg.Provider(providerID)
	label := providerID
	if ok {
		label = providerEntryLabel(providerID, providerCfg)
	}
	options = append(options, ModelOption{
		ProviderID:    providerID,
		ProviderLabel: label,
		ModelID:       modelID,
	})
	slices.SortFunc(options, func(a, b ModelOption) int {
		if cmp := strings.Compare(a.ProviderLabel, b.ProviderLabel); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.ModelID, b.ModelID)
	})
	return options
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
		AuthMethod:         strings.TrimSpace(draft.AuthMethod),
		Name:               strings.TrimSpace(draft.Name),
		BaseURL:            strings.TrimSpace(draft.BaseURL),
		APIKey:             strings.TrimSpace(draft.APIKey),
		APIKeyEnv:          strings.TrimSpace(draft.APIKeyEnv),
		Model:              strings.TrimSpace(draft.Model),
		ModelPreset:        strings.TrimSpace(draft.ModelPreset),
		ContextWindow:      draft.ContextWindow,
		AutoCompactAt:      draft.AutoCompactAt,
		Stream:             draft.Stream,
		Timeout:            durationString(draft.Timeout),
		Disabled:           draft.Disabled,
		Headers:            cloneHeaderMap(draft.Headers),
	}
}

func providerDraftToCatalog(draft ProviderDraft) provider.ConnectDraft {
	return provider.ConnectDraft{
		OriginalProviderID: strings.TrimSpace(draft.OriginalProviderID),
		ProviderID:         strings.TrimSpace(draft.ProviderID),
		TemplateID:         strings.TrimSpace(draft.TemplateID),
		Kind:               strings.TrimSpace(draft.Kind),
		AuthMethod:         strings.TrimSpace(draft.AuthMethod),
		Name:               strings.TrimSpace(draft.Name),
		BaseURL:            strings.TrimSpace(draft.BaseURL),
		APIKey:             strings.TrimSpace(draft.APIKey),
		APIKeyEnv:          strings.TrimSpace(draft.APIKeyEnv),
		Model:              strings.TrimSpace(draft.Model),
		ModelPreset:        strings.TrimSpace(draft.ModelPreset),
		ContextWindow:      draft.ContextWindow,
		AutoCompactAt:      draft.AutoCompactAt,
		Stream:             draft.Stream,
		Timeout:            parseDurationOrZero(draft.Timeout),
		Disabled:           draft.Disabled,
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
	if strings.TrimSpace(next.AuthMethod) == "" {
		next.AuthMethod = existing.AuthMethod
	}
	if strings.TrimSpace(next.APIKeyEnv) == "" {
		next.APIKeyEnv = existing.APIKeyEnv
	}
	if strings.TrimSpace(next.ModelPreset) == "" {
		next.ModelPreset = existing.ModelPreset
	}
	if next.ContextWindow == 0 {
		next.ContextWindow = existing.ContextWindow
	}
	if next.AutoCompactAt == 0 {
		next.AutoCompactAt = existing.AutoCompactAt
	}
	if next.Timeout == 0 {
		next.Timeout = existing.Timeout
	}
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

func applyProviderDraftPreferences(next *config.Provider, draft ProviderDraft) {
	next.AuthMethod = strings.TrimSpace(draft.AuthMethod)
	next.APIKeyEnv = strings.TrimSpace(draft.APIKeyEnv)
	next.ModelPreset = strings.TrimSpace(draft.ModelPreset)
	if draft.ContextWindow > 0 {
		next.ContextWindow = draft.ContextWindow
	}
	if draft.AutoCompactAt > 0 {
		next.AutoCompactAt = draft.AutoCompactAt
	}
	if timeout := parseDurationOrZero(draft.Timeout); timeout > 0 {
		next.Timeout = timeout
	}
	next.Stream = draft.Stream
	next.Disabled = draft.Disabled
}

func uiPreferencesFromConfig(ui config.UI) UIPreferences {
	codeStyle := firstNonEmpty(strings.TrimSpace(ui.CodeStyle), config.Default().UI.CodeStyle)
	return UIPreferences{
		Theme:            normalizeTheme(ui.Theme),
		CodeStyle:        codeStyle,
		CodeStyleOptions: codeStyleOptions(codeStyle),
		EditForgiveness:  config.NormalizeEditForgiveness(ui.EditForgiveness),
		CursorBlink:      ui.CursorBlink,
		HalfBlocks:       ui.HalfBlocks,
		ShowSidebar:      ui.ShowSidebar,
		SidebarWidth:     ui.SidebarWidth,
		ShowTimestamps:   ui.ShowTimestamps,
		ShowReasoning:    ui.ShowReasoning,
		ShowSystem:       ui.ShowSystem,
		Mouse:            ui.Mouse,
		AutoContinue:     ui.AutoContinue,
	}
}

func compactionPreferencesFromConfig(cfg config.Config) CompactionPreferences {
	providerID := strings.TrimSpace(cfg.CompactionProvider)
	modelID := strings.TrimSpace(cfg.CompactionModel)
	text := "Chat model"
	if providerID != "" || modelID != "" {
		text = providerID + " / " + modelID
	}
	return CompactionPreferences{
		AutoCompactAt:        cfg.AutoCompactAt,
		KeepToolBatches:      config.NormalizeCompactionKeepToolBatches(cfg.CompactionKeepToolBatches),
		ProviderID:           providerID,
		ModelID:              modelID,
		UseChatModel:         providerID == "" && modelID == "",
		CurrentSelectionText: text,
	}
}

func mcpPreferencesFromConfig(src map[string]config.MCPServer) []MCPServerPreference {
	ids := make([]string, 0, len(src))
	for id := range src {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	out := make([]MCPServerPreference, 0, len(ids))
	for _, id := range ids {
		server := src[id]
		out = append(out, MCPServerPreference{
			ID:                   id,
			Name:                 strings.TrimSpace(server.Name),
			URL:                  strings.TrimSpace(server.URL),
			Headers:              cloneHeaderMap(server.Headers),
			Disabled:             server.Disabled,
			StartupTimeout:       durationString(server.StartupTimeout),
			RequestTimeout:       durationString(server.RequestTimeout),
			DisableStandaloneSSE: server.DisableStandaloneSSE,
			BearerToken:          strings.TrimSpace(server.BearerToken),
			BearerTokenEnv:       strings.TrimSpace(server.BearerTokenEnv),
		})
	}
	return out
}

func permissionPreferencesFromConfig(src config.PermissionRules) PermissionPreferences {
	names := make([]string, 0, len(src.Profiles))
	for name := range src.Profiles {
		names = append(names, name)
	}
	slices.Sort(names)
	profiles := make([]PermissionProfilePreference, 0, len(names))
	for _, name := range names {
		profile := permissionprofile.Normalize(src.Profiles[name])
		profiles = append(profiles, PermissionProfilePreference{
			Name:      name,
			Network:   profile.Network,
			Root:      profile.Root,
			Workspace: profile.Workspace,
			Mounts:    permissionMountPreferences(profile.Mounts),
		})
	}
	return PermissionPreferences{Active: strings.TrimSpace(src.Profile), Profiles: profiles}
}

func permissionMountPreferences(src []permissionprofile.Mount) []PermissionMountPreference {
	out := make([]PermissionMountPreference, 0, len(src))
	for _, mount := range src {
		out = append(out, PermissionMountPreference{Path: mount.Path, Mode: string(mount.Mode)})
	}
	return out
}

func toolDefaultPreferencesFromConfig(src map[domain.ToolKind]bool) []ToolDefaultPreference {
	kinds := domain.AllToolKinds()
	out := make([]ToolDefaultPreference, 0, len(kinds))
	for _, kind := range kinds {
		enabled := true
		if value, ok := src[kind]; ok {
			enabled = value
		}
		out = append(out, ToolDefaultPreference{Tool: kind, Enabled: enabled})
	}
	return out
}

func codeStyleOptions(current string) []string {
	options := theme.Names()
	current = strings.TrimSpace(current)
	if current == "" {
		return options
	}
	if !slices.Contains(options, current) {
		options = append(options, current)
		slices.Sort(options)
	}
	return options
}

func applyGeneralPreferences(cfg *config.Config, prefs GeneralPreferences) error {
	cfg.DefaultProvider = strings.TrimSpace(prefs.DefaultProvider)
	cfg.DefaultModel = strings.TrimSpace(prefs.DefaultModel)
	if cfg.DefaultProvider != "" && !cfg.HasUsableProvider(cfg.DefaultProvider) {
		return fmt.Errorf("default provider %q is not configured or is disabled", cfg.DefaultProvider)
	}
	if prefs.MaxToolLoopSteps <= 0 {
		return fmt.Errorf("max tool loop steps must be greater than zero")
	}
	cfg.MaxToolLoopSteps = prefs.MaxToolLoopSteps
	if backend := strings.TrimSpace(prefs.StoreBackend); backend != "" {
		cfg.Store.Backend = backend
	}
	return nil
}

func applyUIPreferences(cfg *config.Config, prefs UIPreferences) error {
	codeStyle := firstNonEmpty(strings.TrimSpace(prefs.CodeStyle), config.Default().UI.CodeStyle)
	cfg.UI = config.UI{
		Theme:           normalizeTheme(prefs.Theme),
		CodeStyle:       codeStyle,
		EditForgiveness: config.NormalizeEditForgiveness(prefs.EditForgiveness),
		CursorBlink:     prefs.CursorBlink,
		HalfBlocks:      prefs.HalfBlocks,
		ShowSidebar:     prefs.ShowSidebar,
		SidebarWidth:    prefs.SidebarWidth,
		ShowTimestamps:  prefs.ShowTimestamps,
		ShowReasoning:   prefs.ShowReasoning,
		ShowSystem:      prefs.ShowSystem,
		Mouse:           prefs.Mouse,
		AutoContinue:    prefs.AutoContinue,
	}
	return nil
}

func applyCompactionPreferences(cfg *config.Config, prefs CompactionPreferences) error {
	if prefs.AutoCompactAt <= 0 {
		return fmt.Errorf("auto compact threshold must be greater than zero")
	}
	cfg.AutoCompactAt = prefs.AutoCompactAt
	cfg.CompactionKeepToolBatches = config.NormalizeCompactionKeepToolBatches(prefs.KeepToolBatches)
	if prefs.UseChatModel {
		cfg.CompactionProvider = ""
		cfg.CompactionModel = ""
		return nil
	}
	providerID := strings.TrimSpace(prefs.ProviderID)
	modelID := strings.TrimSpace(prefs.ModelID)
	if providerID == "" && modelID == "" {
		cfg.CompactionProvider = ""
		cfg.CompactionModel = ""
		return nil
	}
	if providerID == "" || modelID == "" {
		return fmt.Errorf("compaction provider and model must both be set, or both empty for chat model")
	}
	if !cfg.HasUsableProvider(providerID) {
		return fmt.Errorf("compaction provider %q is not configured or is disabled", providerID)
	}
	cfg.CompactionProvider = providerID
	cfg.CompactionModel = modelID
	return nil
}

func applyMCPPreferences(cfg *config.Config, prefs []MCPServerPreference) error {
	next := map[string]config.MCPServer{}
	for _, item := range prefs {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			continue
		}
		startup := parseDurationOrZero(item.StartupTimeout)
		request := parseDurationOrZero(item.RequestTimeout)
		next[id] = config.MCPServer{
			Name:                 strings.TrimSpace(item.Name),
			URL:                  strings.TrimSpace(item.URL),
			Headers:              cloneHeaderMap(item.Headers),
			Disabled:             item.Disabled,
			StartupTimeout:       startup,
			RequestTimeout:       request,
			DisableStandaloneSSE: item.DisableStandaloneSSE,
			BearerToken:          strings.TrimSpace(item.BearerToken),
			BearerTokenEnv:       strings.TrimSpace(item.BearerTokenEnv),
		}
	}
	cfg.MCPServers = next
	return nil
}

func applyPermissionPreferences(cfg *config.Config, prefs PermissionPreferences) error {
	profiles := map[string]config.PermissionProfile{}
	for _, item := range prefs.Profiles {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			continue
		}
		profile := config.PermissionProfile{
			Network:   item.Network,
			Root:      strings.TrimSpace(item.Root),
			Workspace: strings.TrimSpace(item.Workspace),
		}
		for _, mount := range item.Mounts {
			path := strings.TrimSpace(mount.Path)
			if path == "" {
				continue
			}
			profile.Mounts = append(profile.Mounts, permissionprofile.Mount{
				Path: path,
				Mode: permissionprofile.MountMode(strings.TrimSpace(mount.Mode)),
			})
		}
		profile = permissionprofile.Normalize(profile)
		if err := permissionprofile.ValidateSandbox(profile); err != nil {
			return fmt.Errorf("permission profile %q: %w", name, err)
		}
		profiles[name] = profile
	}
	if len(profiles) == 0 {
		profiles = config.Default().Permissions.Profiles
	}
	active := strings.TrimSpace(prefs.Active)
	if active == "" {
		active = config.Default().Permissions.Profile
	}
	cfg.Permissions = config.PermissionRules{Profile: active, Profiles: profiles}
	return nil
}

func applyToolDefaultPreferences(cfg *config.Config, prefs []ToolDefaultPreference) {
	next := map[domain.ToolKind]bool{}
	for _, item := range prefs {
		next[item.Tool] = item.Enabled
	}
	for _, kind := range domain.AllToolKinds() {
		if _, ok := next[kind]; !ok {
			next[kind] = true
		}
	}
	cfg.ToolDefaults = next
}

func promptPreferences() ([]PromptPreference, error) {
	out := make([]PromptPreference, 0, 2)
	for _, target := range []string{"system-prompt.md", "compaction-prompt.md"} {
		item, err := promptPreference(target)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

func promptPreference(target string) (PromptPreference, error) {
	path, err := managedPromptPath(target)
	if err != nil {
		return PromptPreference{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		data, err = assets.DefaultContent(target)
		if err != nil {
			return PromptPreference{}, err
		}
	}
	return PromptPreference{
		Name:    strings.TrimSuffix(target, ".md"),
		Target:  target,
		Path:    path,
		Content: string(data),
	}, nil
}

func writePromptPreferences(prompts []PromptPreference) error {
	for _, prompt := range prompts {
		target := strings.TrimSpace(prompt.Target)
		if target != "system-prompt.md" && target != "compaction-prompt.md" {
			continue
		}
		path, err := managedPromptPath(target)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("create prompt dir: %w", err)
		}
		if err := os.WriteFile(path, []byte(prompt.Content), 0o644); err != nil {
			return fmt.Errorf("write prompt %s: %w", target, err)
		}
	}
	return nil
}

func managedPromptPath(target string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return "", fmt.Errorf("locate home directory for prompt assets: %w", err)
	}
	return filepath.Join(home, ".koder", target), nil
}

func normalizeTheme(theme string) string {
	theme = strings.ToLower(strings.TrimSpace(theme))
	if theme != "dark" && theme != "light" {
		return "auto"
	}
	return theme
}

func durationString(value time.Duration) string {
	if value <= 0 {
		return ""
	}
	return value.String()
}

func parseDurationOrZero(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0
	}
	return duration
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

type composerCommand struct {
	Command     string
	Description string
}

var composerCommands = []composerCommand{
	{Command: "/chat new", Description: "Start a new chat"},
	{Command: "/compact", Description: "Compact the active chat"},
	{Command: "/model", Description: "Select the chat model"},
	{Command: "/permissions", Description: "Change permission profile"},
	{Command: "/providers", Description: "Configure providers"},
	{Command: "/sessions", Description: "Switch sessions"},
	{Command: "/settings", Description: "Open settings"},
}

func composerCommandQuery(value string, cursor int) (query string, start int, end int, ok bool) {
	if cursor < 0 || cursor > len(value) {
		cursor = len(value)
	}
	if cursor == 0 {
		return "", 0, 0, false
	}
	prefix := value[:cursor]
	start = 0
	for start < len(prefix) && isComposerWhitespace(rune(prefix[start])) {
		start++
	}
	if start >= len(value) || value[start] != '/' {
		return "", 0, 0, false
	}
	if strings.ContainsAny(prefix[start:cursor], "\n\t") {
		return "", 0, 0, false
	}
	end = cursor
	query = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(value[start:cursor], "/")))
	return query, start, end, true
}

func matchingComposerCommands(query string) []composerCommand {
	query = strings.ToLower(strings.TrimSpace(query))
	out := make([]composerCommand, 0, len(composerCommands))
	for _, item := range composerCommands {
		command := strings.ToLower(strings.TrimPrefix(item.Command, "/"))
		if query == "" || strings.HasPrefix(command, query) {
			out = append(out, item)
		}
	}
	return out
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
		if session, ok, err := c.restartInterruptedSession(ctx); err != nil {
			return domain.Session{}, err
		} else if ok {
			return session, nil
		}
	}
	sessions, err := c.workspaceSessions(ctx)
	if err != nil {
		return domain.Session{}, err
	}
	if len(sessions) == 0 {
		return c.createWorkspaceSession(ctx, "New Session")
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
	session, chatRecord, chats, err = c.touchLoadedSelection(ctx, session, chatRecord, chats)
	if err != nil {
		return err
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
	c.autoResumeRestartInterruptedChats(runtimes, snapshots)
	c.broadcast("snapshot", c.State())
	return nil
}

func (c *Controller) touchLoadedSelection(ctx context.Context, session domain.Session, chatRecord domain.Chat, chats []domain.Chat) (domain.Session, domain.Chat, []domain.Chat, error) {
	session, err := c.store.TouchSession(ctx, session.ID)
	if err != nil {
		return domain.Session{}, domain.Chat{}, nil, err
	}
	chatRecord.UpdatedAt = time.Now().UTC()
	if err := c.store.UpdateChat(ctx, chatRecord); err != nil {
		return domain.Session{}, domain.Chat{}, nil, err
	}
	for idx, item := range chats {
		if item.ID == chatRecord.ID {
			chats[idx] = chatRecord
			break
		}
	}
	return session, chatRecord, chats, nil
}

const processRestartResumeNote = "The previous turn was interrupted because the koder process was restarting. Continue from the persisted transcript and pending tool state without restating the interruption."

func (c *Controller) restartInterruptedSession(ctx context.Context) (domain.Session, bool, error) {
	sessions, err := c.workspaceSessions(ctx)
	if err != nil {
		return domain.Session{}, false, err
	}
	var matches []domain.Session
	for _, session := range sessions {
		chats, err := c.store.ListChats(ctx, session.ID)
		if err != nil {
			return domain.Session{}, false, err
		}
		for _, chatRecord := range chats {
			if ok, err := c.chatEndsWithRestartInterrupt(ctx, chatRecord.ID); err != nil {
				return domain.Session{}, false, err
			} else if ok {
				matches = append(matches, session)
				break
			}
		}
	}
	session := newestSession(matches)
	return session, session.ID != "", nil
}

func (c *Controller) chatEndsWithRestartInterrupt(ctx context.Context, chatID domain.ID) (bool, error) {
	timeline, err := c.store.TimelineForChat(ctx, chatID)
	if err != nil {
		return false, err
	}
	if len(timeline) == 0 {
		return false, nil
	}
	notice, ok := timeline[len(timeline)-1].Content.(domain.Notice)
	return ok && notice.Kind == domain.NoticeKindInterrupted && notice.Reason == domain.NoticeReasonProcessRestart, nil
}

func (c *Controller) autoResumeRestartInterruptedChats(runtimes map[domain.ID]*chat.Chat, snapshots map[domain.ID]chat.Snapshot) {
	for id, snapshot := range snapshots {
		if !shouldAutoResumeRestartInterrupted(snapshot) {
			continue
		}
		rt := runtimes[id]
		if rt == nil {
			continue
		}
		rt.Enqueue(chat.QueueItem{Kind: chat.QueueKindContinue, Note: processRestartResumeNote})
	}
}

func shouldAutoResumeRestartInterrupted(snapshot chat.Snapshot) bool {
	if snapshot.Active || snapshot.Status == chat.StatusWaitingApproval {
		return false
	}
	for _, item := range snapshot.QueuedInputs {
		if item.Kind == domain.QueuedInputKindContinue {
			return false
		}
	}
	if len(snapshot.Timeline) == 0 {
		return false
	}
	notice, ok := snapshot.Timeline[len(snapshot.Timeline)-1].Content.(domain.Notice)
	return ok && notice.Kind == domain.NoticeKindInterrupted && notice.Reason == domain.NoticeReasonProcessRestart
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
					c.chats = append(c.chats, update.Snapshot.Chat)
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
			left[idx].AssignedTodoRef != right[idx].AssignedTodoRef ||
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
	names := permissionprofile.ProfileNames(c.cfg.Permissions)
	profiles := make([]permissionprofile.ProfileOption, 0, len(names))
	for _, name := range names {
		profiles = append(profiles, permissionprofile.ProfileOption{
			Name:        name,
			Label:       permissionprofile.DisplayName(name),
			Description: permissionprofile.Description(name, c.cfg.Permissions),
		})
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

func (c *Controller) modelInfoLocked() ModelInfo {
	providerID := strings.TrimSpace(c.session.ProviderID)
	modelID := strings.TrimSpace(c.session.ModelID)
	providerCfg, ok := c.cfg.Provider(providerID)
	if !ok {
		providerCfg = config.Provider{}
	}
	info := ModelInfo{
		ProviderID:    providerID,
		ModelID:       modelID,
		ContextWindow: c.contextWindowLocked(),
		SupportsTools: true,
	}
	if modelID == "" {
		return info
	}
	enriched, err := provider.NewCapabilityStore(c.cfg.StateDir()).EnrichModel(providerID, providerCfg, domain.Model{ID: modelID})
	if err != nil {
		return info
	}
	info.SupportsImages = enriched.SupportsImages
	info.SupportsPDFs = enriched.SupportsPDFs
	info.CapabilitiesKnown = enriched.CapabilitiesKnown
	info.CapabilitySource = strings.TrimSpace(enriched.CapabilitySource)
	return info
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
