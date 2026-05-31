package app

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lkarlslund/koder/internal/agent"
	"github.com/lkarlslund/koder/internal/attachment"
	"github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/permissionprofile"
	"github.com/lkarlslund/koder/internal/planning"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/reference"
	sessionpkg "github.com/lkarlslund/koder/internal/session"
	"github.com/lkarlslund/koder/internal/sessionstore"
	"github.com/lkarlslund/koder/internal/skills"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
	workspacepkg "github.com/lkarlslund/koder/internal/workspace"
)

// StartupMode selects the initial session behavior for browser app UI.
type StartupMode int

const (
	StartupModeNew StartupMode = iota
	StartupModeResume
)

// Event is a browser app pushed UI update.
type Event struct {
	Seq     uint64 `json:"seq"`
	Type    string `json:"type"`
	Payload any    `json:"payload"`
}

// State is the browser app UI snapshot consumed by browser clients.
type State struct {
	Session       domain.Session                 `json:"session"`
	Sessions      []domain.Session               `json:"sessions"`
	Chats         []domain.Chat                  `json:"chats"`
	ChatStatuses  []ChatSidebarStatus            `json:"chat_statuses"`
	ActiveChatID  domain.ID                      `json:"active_chat_id"`
	Permissions   PermissionsState               `json:"permissions"`
	Snapshot      chat.Snapshot                  `json:"snapshot"`
	Snapshots     map[domain.ID]chat.Snapshot    `json:"snapshots"`
	Milestones    planning.Plan                  `json:"milestones"`
	Todos         []planning.TodoItem            `json:"todos"`
	TodosByRef    map[string][]planning.TodoItem `json:"todos_by_milestone"`
	Workspace     workspacepkg.Status            `json:"workspace_status"`
	ContextWindow int                            `json:"context_window"`
	ModelInfo     ModelInfo                      `json:"model_info"`
	Theme         string                         `json:"theme"`
	ProjectRoot   string                         `json:"project_root"`
	RestartNeeded bool                           `json:"restart_needed"`
	Error         string                         `json:"error,omitempty"`
}

// ChatSidebarStatus is the browser app run state for one chat in the sidebar.
type ChatSidebarStatus struct {
	ChatID           domain.ID `json:"chat_id"`
	Status           string    `json:"status"`
	Busy             bool      `json:"busy"`
	PendingApprovals int       `json:"pending_approvals,omitempty"`
	StatusText       string    `json:"status_text,omitempty"`
	LastError        string    `json:"last_error,omitempty"`
}

// ModelOption is a selectable provider/model pair exposed to web clients.
type ModelOption struct {
	ProviderID    string `json:"provider_id"`
	ProviderLabel string `json:"provider_label"`
	ModelID       string `json:"model_id"`
	OwnedBy       string `json:"owned_by,omitempty"`
	Current       bool   `json:"current"`
}

// ModelInfo describes the active model capabilities shown by web clients.
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

// PermissionsState describes permission profiles available to web clients.
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
	ID                      string `json:"id"`
	Name                    string `json:"name"`
	TemplateID              string `json:"template_id"`
	Kind                    string `json:"kind"`
	BaseURL                 string `json:"base_url"`
	Disabled                bool   `json:"disabled"`
	Default                 bool   `json:"default"`
	PromptProgressMode      string `json:"prompt_progress_mode"`
	PromptProgressProbed    bool   `json:"prompt_progress_probed"`
	PromptProgressSupported bool   `json:"prompt_progress_supported"`
}

// ProviderDraft is the JSON-friendly provider edit shape used by web clients.
type ProviderDraft struct {
	OriginalProviderID      string            `json:"original_provider_id"`
	ProviderID              string            `json:"provider_id"`
	TemplateID              string            `json:"template_id"`
	Kind                    string            `json:"kind"`
	AuthMethod              string            `json:"auth_method"`
	Name                    string            `json:"name"`
	BaseURL                 string            `json:"base_url"`
	APIKey                  string            `json:"api_key"`
	APIKeyEnv               string            `json:"api_key_env"`
	Model                   string            `json:"model"`
	Stream                  bool              `json:"stream"`
	Timeout                 string            `json:"timeout"`
	Disabled                bool              `json:"disabled"`
	Headers                 map[string]string `json:"headers"`
	PromptProgressMode      string            `json:"prompt_progress_mode"`
	PromptProgressProbed    bool              `json:"prompt_progress_probed"`
	PromptProgressSupported bool              `json:"prompt_progress_supported"`
}

// ProviderProbeResult reports a provider test outcome.
type ProviderProbeResult struct {
	ModelCount              int      `json:"model_count"`
	Models                  []string `json:"models"`
	SelectedModel           string   `json:"selected_model"`
	PromptProgressProbed    bool     `json:"prompt_progress_probed"`
	PromptProgressSupported bool     `json:"prompt_progress_supported"`
}

type ModelConfigPreference struct {
	OriginalProviderID string `json:"original_provider_id"`
	OriginalModelID    string `json:"original_model_id"`
	ProviderID         string `json:"provider_id"`
	ModelID            string `json:"model_id"`
	ContextWindow      int    `json:"context_window"`
	ModelPreset        string `json:"model_preset"`
}

// PreferencesState is the complete settings payload exposed to browser clients.
type PreferencesState struct {
	General      GeneralPreferences      `json:"general"`
	UI           BrowserPreferences      `json:"ui"`
	Compaction   CompactionPreferences   `json:"compaction"`
	Prompts      []PromptPreference      `json:"prompts"`
	Providers    ProviderState           `json:"providers"`
	Models       []ModelOption           `json:"models"`
	ModelConfigs []ModelConfigPreference `json:"model_configs"`
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

// BrowserPreferences contains browser behavior settings persisted in config.
type BrowserPreferences struct {
	Theme        string `json:"theme"`
	AutoContinue bool   `json:"auto_continue"`
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
	ActiveID    domain.ID        `json:"active_id"`
	ProjectRoot string           `json:"project_root"`
	Sessions    []domain.Session `json:"sessions"`
}

// Controller owns session/chat state independently from any renderer.
type Controller struct {
	cfg   config.Config
	store *store.Store
	agent *agent.Engine

	mu                         sync.RWMutex
	session                    domain.Session
	sessions                   []domain.Session
	chats                      []domain.Chat
	statuses                   map[domain.ID]ChatSidebarStatus
	chat                       domain.Chat
	runtime                    *chat.Chat
	unsub                      func()
	runtimes                   map[domain.ID]*chat.Chat
	unsubs                     map[domain.ID]func()
	execUnsubs                 map[domain.ID]func()
	snapshots                  map[domain.ID]chat.Snapshot
	milestone                  planning.Plan
	todos                      []planning.TodoItem
	todosByRef                 map[string][]planning.TodoItem
	workspace                  workspacepkg.Status
	theme                      string
	lastErr                    string
	restartNeeded              bool
	clearedStartupRunningTools bool

	subMu   sync.Mutex
	nextSub int
	nextSeq uint64
	subs    map[int]chan Event
}

// New constructs a browser app controller.
func New(cfg config.Config, st *store.Store, engine *agent.Engine) *Controller {
	return &Controller{
		cfg:        cfg,
		store:      st,
		agent:      engine,
		theme:      normalizeTheme(cfg.UI.Theme),
		statuses:   map[domain.ID]ChatSidebarStatus{},
		runtimes:   map[domain.ID]*chat.Chat{},
		unsubs:     map[domain.ID]func(){},
		execUnsubs: map[domain.ID]func(){},
		snapshots:  map[domain.ID]chat.Snapshot{},
		subs:       map[int]chan Event{},
	}
}

// Start loads the initial session/chat and attaches the live chat runtime.
func (c *Controller) Start(ctx context.Context, mode StartupMode, projectRoot string) error {
	if c == nil {
		return fmt.Errorf("controller is nil")
	}
	session, err := c.initialSession(ctx, mode, projectRoot)
	if err != nil {
		return err
	}
	if err := c.loadSession(ctx, session.ID, ""); err != nil {
		return err
	}
	return nil
}

// State returns a detached snapshot of current browser app UI state.
func (c *Controller) State() State {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.stateLocked()
}

func (c *Controller) populateStateSnapshotsLocked(state *State) {
	for idx := range state.Chats {
		if state.Chats[idx].ID == c.chat.ID {
			state.Chats[idx] = c.chat
			break
		}
	}
	selectedChats := make(map[domain.ID]bool, len(state.Chats))
	for _, chatRecord := range state.Chats {
		selectedChats[chatRecord.ID] = true
	}
	for chatID, snapshot := range c.snapshots {
		if snapshot.Chat.ID == "" {
			snapshot.Chat.ID = chatID
		}
		snapshot = c.snapshotWithExecProcessesLocked(snapshot)
		state.Snapshots[chatID] = snapshot
		if chatID == c.chat.ID {
			state.Snapshot = snapshot
		}
		if selectedChats[snapshot.Chat.ID] && !hasChatSidebarStatus(state.ChatStatuses, snapshot.Chat.ID) {
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
		snapshot = c.snapshotWithExecProcessesLocked(snapshot)
		state.Snapshots[chatID] = snapshot
		if chatID == c.chat.ID {
			state.Snapshot = snapshot
		}
		if selectedChats[snapshot.Chat.ID] && !hasChatSidebarStatus(state.ChatStatuses, snapshot.Chat.ID) {
			state.ChatStatuses = mergeChatSidebarStatus(state.ChatStatuses, sidebarStatusFromSnapshot(snapshot))
		}
	}
	if state.Snapshot.Chat.ID == "" && c.runtime != nil {
		snapshot := c.runtime.Snapshot()
		snapshot = c.snapshotWithExecProcessesLocked(snapshot)
		state.Snapshot = snapshot
		if !hasChatSidebarStatus(state.ChatStatuses, snapshot.Chat.ID) {
			state.ChatStatuses = mergeChatSidebarStatus(state.ChatStatuses, sidebarStatusFromSnapshot(snapshot))
		}
	}
}

// MarkRestartNeeded tells web clients that a newer binary is ready but not running.
func (c *Controller) MarkRestartNeeded() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.restartNeeded = true
	state := c.stateLocked()
	c.mu.Unlock()
	c.broadcast("snapshot", state)
}

func (c *Controller) stateLocked() State {
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
		ProjectRoot:   c.session.ProjectRoot,
		RestartNeeded: c.restartNeeded,
		Error:         c.lastErr,
	}
	c.populateStateSnapshotsLocked(&state)
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
	source := ""
	if kind == chat.QueueKindSteer {
		source = domain.UserMessageSourceUser
	}
	rt.Enqueue(chat.QueueItem{Kind: kind, Source: source, Text: text, Attachments: validated})
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

// SendQueueItemNow promotes a queued input to a steer item for the active chat.
func (c *Controller) SendQueueItemNow(id domain.ID) error {
	rt := c.currentRuntime()
	if rt == nil {
		return fmt.Errorf("no active chat")
	}
	rt.SendQueueItemNow(id)
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
	unsubs := make([]func(), 0, len(c.unsubs)+len(c.execUnsubs)+1)
	for _, unsub := range c.unsubs {
		if unsub != nil {
			unsubs = append(unsubs, unsub)
		}
	}
	for _, unsub := range c.execUnsubs {
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
	session := c.session
	parent := c.chat
	c.mu.RUnlock()
	if session.ID == "" {
		return fmt.Errorf("no active session")
	}
	if parent.ID == "" {
		return fmt.Errorf("no active chat")
	}
	if c.agent == nil {
		return fmt.Errorf("no chat agent")
	}
	owner, err := c.agent.LoadSession(ctx, session.ID)
	if err != nil {
		return err
	}
	rt, err := owner.NewChat(ctx, parent.ID, title)
	if err != nil {
		return err
	}
	return c.loadSession(ctx, session.ID, rt.Snapshot().Chat.ID)
}

// ListChats returns the controller's live chat list for the active session.
func (c *Controller) ListChats(ctx context.Context, sessionID domain.ID) ([]tools.ChatStatus, error) {
	c.mu.RLock()
	if sessionID == "" {
		sessionID = c.session.ID
	}
	if sessionID != c.session.ID {
		c.mu.RUnlock()
		return nil, fmt.Errorf("session %s is not active", sessionID)
	}
	chats := slices.Clone(c.chats)
	c.mu.RUnlock()
	out := make([]tools.ChatStatus, 0, len(chats))
	for _, item := range chats {
		status, err := c.PollChat(ctx, sessionID, item.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, status)
	}
	return out, nil
}

// StartChat creates a child chat and adds it to the live session before broadcasting.
func (c *Controller) StartChat(ctx context.Context, sessionID, parentChatID domain.ID, req tools.ChatStartRequest) (tools.ChatStatus, error) {
	if c.agent == nil {
		return tools.ChatStatus{}, fmt.Errorf("no chat agent")
	}
	status, err := c.agent.StartChat(ctx, sessionID, parentChatID, req)
	if err != nil {
		return tools.ChatStatus{}, err
	}
	if err := c.addStartedChat(ctx, status); err != nil {
		return tools.ChatStatus{}, err
	}
	return status, nil
}

// PollChat returns the current status for a live chat.
func (c *Controller) PollChat(ctx context.Context, sessionID, chatID domain.ID) (tools.ChatStatus, error) {
	if c.agent == nil {
		return tools.ChatStatus{}, fmt.Errorf("no chat agent")
	}
	return c.agent.PollChat(ctx, sessionID, chatID)
}

func (c *Controller) GetMilestonePlan(ctx context.Context, sessionID domain.ID) (planning.Plan, error) {
	if c.agent != nil {
		if owner, err := c.agent.LoadSession(ctx, sessionID); err == nil {
			return owner.GetMilestonePlan(ctx, sessionID)
		}
	}
	return planning.Plan{}, fmt.Errorf("no live session owner")
}

func (c *Controller) SetMilestonePlan(ctx context.Context, sessionID domain.ID, summary string, milestones []planning.Milestone) (planning.Plan, error) {
	if c.agent != nil {
		if owner, err := c.agent.LoadSession(ctx, sessionID); err == nil {
			plan, err := owner.SetMilestonePlan(ctx, sessionID, summary, milestones)
			if err != nil {
				return planning.Plan{}, err
			}
			c.refreshPlanningFromOwner(owner)
			return plan, nil
		}
	}
	return planning.Plan{}, fmt.Errorf("no live session owner")
}

func (c *Controller) AddTodoItems(ctx context.Context, sessionID domain.ID, milestoneRef string, contents []string) ([]planning.TodoItem, error) {
	if c.agent != nil {
		if owner, err := c.agent.LoadSession(ctx, sessionID); err == nil {
			created, err := owner.AddTodoItems(ctx, sessionID, milestoneRef, contents)
			if err != nil {
				return nil, err
			}
			c.refreshPlanningFromOwner(owner)
			return created, nil
		}
	}
	return nil, fmt.Errorf("no live session owner")
}

func (c *Controller) UpdateTodoItem(ctx context.Context, todoID domain.ID, status domain.TodoStatus, content string) (planning.TodoItem, error) {
	c.mu.RLock()
	sessionID := c.session.ID
	c.mu.RUnlock()
	if c.agent != nil && sessionID != "" {
		if owner, err := c.agent.LoadSession(ctx, sessionID); err == nil {
			updated, err := owner.UpdateTodoItem(ctx, todoID, status, content)
			if err != nil {
				return planning.TodoItem{}, err
			}
			c.refreshPlanningFromOwner(owner)
			return updated, nil
		}
	}
	return planning.TodoItem{}, fmt.Errorf("no live session owner")
}

func (c *Controller) ListTodos(ctx context.Context, sessionID domain.ID, milestoneRef string) ([]planning.TodoItem, error) {
	if c.agent != nil {
		if owner, err := c.agent.LoadSession(ctx, sessionID); err == nil {
			return owner.ListTodos(ctx, sessionID, milestoneRef)
		}
	}
	return nil, fmt.Errorf("no live session owner")
}

func (c *Controller) AddTask(ctx context.Context, sessionID domain.ID, body string, status domain.TaskStatus) (planning.Task, error) {
	if c.agent != nil {
		if owner, err := c.agent.LoadSession(ctx, sessionID); err == nil {
			task, err := owner.AddTask(ctx, sessionID, body, status)
			if err != nil {
				return planning.Task{}, err
			}
			c.refreshPlanningFromOwner(owner)
			return task, nil
		}
	}
	return planning.Task{}, fmt.Errorf("no live session owner")
}

func (c *Controller) refreshPlanningFromOwner(owner *sessionpkg.Session) {
	if owner == nil {
		return
	}
	snapshot := owner.Snapshot()
	c.mu.Lock()
	if c.session.ID == snapshot.Session.ID {
		c.milestone = snapshot.Plan
		c.todos = snapshot.Todos
		c.todosByRef = snapshot.TodosByRef
	}
	c.mu.Unlock()
}

func (c *Controller) addStartedChat(ctx context.Context, status tools.ChatStatus) error {
	chatRecord := status.Chat
	if chatRecord.ID == "" {
		return fmt.Errorf("started chat has no id")
	}
	c.mu.RLock()
	session := c.session
	_, exists := c.runtimes[chatRecord.ID]
	c.mu.RUnlock()
	if session.ID == "" || chatRecord.SessionID != session.ID {
		return nil
	}
	rt, err := c.agent.Chat(ctx, session, chatRecord)
	if err != nil {
		return err
	}
	var updates <-chan chat.Update
	var unsub func()
	if !exists {
		updates, unsub = rt.Subscribe()
	} else {
		rt.SetSession(session)
		rt.SetChat(chatRecord)
	}
	snapshot := rt.Snapshot()
	if snapshot.Chat.ID == "" {
		snapshot.Chat = chatRecord
	}
	snapshot = c.snapshotWithExecProcessesForSession(session, snapshot)
	milestone, todos, todosByRef := c.planningState(ctx, session.ID)

	c.mu.Lock()
	if c.session.ID != session.ID {
		c.mu.Unlock()
		if unsub != nil {
			unsub()
		}
		return nil
	}
	upsertChat(&c.chats, chatRecord)
	if c.runtimes == nil {
		c.runtimes = map[domain.ID]*chat.Chat{}
	}
	if c.unsubs == nil {
		c.unsubs = map[domain.ID]func(){}
	}
	if c.snapshots == nil {
		c.snapshots = map[domain.ID]chat.Snapshot{}
	}
	if c.statuses == nil {
		c.statuses = map[domain.ID]ChatSidebarStatus{}
	}
	c.runtimes[chatRecord.ID] = rt
	if unsub != nil {
		c.unsubs[chatRecord.ID] = unsub
	}
	c.snapshots[chatRecord.ID] = snapshot
	c.statuses[chatRecord.ID] = sidebarStatusFromToolStatus(status)
	c.milestone = milestone
	c.todos = todos
	c.todosByRef = todosByRef
	execSubscriptions := c.ensureExecSubscriptionsLocked([]domain.Chat{chatRecord})
	c.mu.Unlock()

	if updates != nil {
		go c.forwardRuntime(chatRecord.ID, updates)
	}
	for _, sub := range execSubscriptions {
		go c.forwardExecRuntime(sub.chatID, sub.events)
	}
	c.broadcast("snapshot", c.State())
	return nil
}

func (c *Controller) snapshotWithExecProcessesForSession(session domain.Session, snapshot chat.Snapshot) chat.Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if snapshot.Session.ID == "" {
		snapshot.Session = session
	}
	return c.snapshotWithExecProcessesLocked(snapshot)
}

func upsertChat(chats *[]domain.Chat, chatRecord domain.Chat) {
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

// DeleteChat archives a chat and switches away first when it is active.
func (c *Controller) DeleteChat(ctx context.Context, chatID domain.ID) error {
	_, err := c.ArchiveChat(ctx, "", chatID)
	return err
}

// ArchiveChat hides a chat from the default sidebar without deleting its history.
func (c *Controller) ArchiveChat(ctx context.Context, sessionID domain.ID, chatID domain.ID) (tools.ChatStatus, error) {
	if chatID == "" {
		return tools.ChatStatus{}, fmt.Errorf("chat id is required")
	}
	c.mu.RLock()
	if sessionID == "" {
		sessionID = c.session.ID
	}
	activeChatID := c.chat.ID
	c.mu.RUnlock()
	if sessionID == "" {
		return tools.ChatStatus{}, fmt.Errorf("no active session")
	}
	if c.agent == nil {
		return tools.ChatStatus{}, fmt.Errorf("no chat agent")
	}
	owner, err := c.agent.LoadSession(ctx, sessionID)
	if err != nil {
		return tools.ChatStatus{}, err
	}
	archivingActive := chatID == activeChatID
	status, nextChatID, err := owner.ArchiveChat(ctx, chatID)
	if err != nil {
		return tools.ChatStatus{}, err
	}
	target := status.Chat
	c.mu.Lock()
	upsertChat(&c.chats, target)
	if c.chat.ID == target.ID {
		c.chat = target
	}
	if snapshot, ok := c.snapshots[target.ID]; ok {
		snapshot.Chat = target
		c.snapshots[target.ID] = snapshot
	}
	c.mu.Unlock()
	if archivingActive {
		if err := c.loadSession(ctx, sessionID, nextChatID); err != nil {
			return tools.ChatStatus{}, err
		}
	} else {
		c.refreshChatStatuses(ctx, sessionID)
		c.broadcast("snapshot", c.State())
	}
	return status, nil
}

// ReorderChats persists the sidebar chat order for the active session.
func (c *Controller) ReorderChats(ctx context.Context, chatIDs []domain.ID) error {
	c.mu.RLock()
	sessionID := c.session.ID
	activeChatID := c.chat.ID
	c.mu.RUnlock()
	if sessionID == "" {
		return fmt.Errorf("no active session")
	}
	if c.agent == nil {
		return fmt.Errorf("no chat agent")
	}
	owner, err := c.agent.LoadSession(ctx, sessionID)
	if err != nil {
		return err
	}
	ordered, err := owner.ReorderChats(ctx, chatIDs)
	if err != nil {
		return err
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
	projectRoot := c.session.ProjectRoot
	c.mu.RUnlock()
	return SessionState{ActiveID: activeID, ProjectRoot: projectRoot, Sessions: sessions}, nil
}

// SwitchSession switches the active session within the current workspace.
func (c *Controller) SwitchSession(ctx context.Context, sessionID domain.ID) error {
	if sessionID == "" {
		return fmt.Errorf("session id is required")
	}
	session, err := sessionstore.GetSession(ctx, c.store, sessionID)
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
	return c.NewSessionWithProjectRoot(ctx, title, "")
}

// NewSessionWithProjectRoot creates and switches to a new session with an explicit project root.
func (c *Controller) NewSessionWithProjectRoot(ctx context.Context, title string, projectRoot string) error {
	if strings.TrimSpace(projectRoot) == "" {
		c.mu.RLock()
		projectRoot = c.session.ProjectRoot
		c.mu.RUnlock()
	}
	if c.agent == nil {
		return fmt.Errorf("no chat agent")
	}
	owner, err := c.agent.CreateSession(ctx, title, projectRoot)
	if err != nil {
		return err
	}
	return c.loadSession(ctx, owner.Snapshot().Session.ID, "")
}

// RenameSession updates a session title.
func (c *Controller) RenameSession(ctx context.Context, sessionID domain.ID, title string) error {
	if sessionID == "" {
		return fmt.Errorf("session id is required")
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return fmt.Errorf("session title is required")
	}
	if c.agent == nil {
		return fmt.Errorf("no chat agent")
	}
	owner, err := c.agent.LoadSession(ctx, sessionID)
	if err != nil {
		return err
	}
	session := owner.Snapshot().Session
	if !c.sessionInWorkspace(session) {
		return fmt.Errorf("session %s does not belong to this workspace", sessionID)
	}
	updated, err := owner.Rename(ctx, title)
	if err != nil {
		return err
	}
	sessions, err := c.workspaceSessions(ctx)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.sessions = sessions
	if c.session.ID == sessionID {
		c.session = updated
	}
	c.mu.Unlock()
	c.broadcast("snapshot", c.State())
	return nil
}

// DeleteSession deletes an idle session and switches away when it is selected.
func (c *Controller) DeleteSession(ctx context.Context, sessionID domain.ID) error {
	if sessionID == "" {
		return fmt.Errorf("session id is required")
	}
	if c.agent == nil {
		return fmt.Errorf("no chat agent")
	}
	owner, err := c.agent.LoadSession(ctx, sessionID)
	if err != nil {
		return err
	}
	snapshot := owner.Snapshot()
	session := snapshot.Session
	if !c.sessionInWorkspace(session) {
		return fmt.Errorf("session %s does not belong to this workspace", sessionID)
	}
	chats := snapshot.Chats
	if err := c.ensureSessionIdle(ctx, sessionID, chats); err != nil {
		return err
	}
	c.mu.RLock()
	deletingSelected := c.session.ID == sessionID
	unsubs := make([]func(), 0, len(chats))
	for _, chatRecord := range chats {
		if unsub := c.unsubs[chatRecord.ID]; unsub != nil {
			unsubs = append(unsubs, unsub)
		}
	}
	c.mu.RUnlock()
	for _, unsub := range unsubs {
		unsub()
	}
	if err := c.agent.DeleteSession(ctx, sessionID); err != nil {
		return err
	}
	if deletingSelected {
		sessions, err := c.workspaceSessions(ctx)
		if err != nil {
			return err
		}
		if len(sessions) == 0 {
			next, err := c.agent.CreateSession(ctx, "New Session", session.ProjectRoot)
			if err != nil {
				return err
			}
			return c.loadSession(ctx, next.Snapshot().Session.ID, "")
		}
		return c.loadSession(ctx, newestSession(sessions).ID, "")
	}
	sessions, err := c.workspaceSessions(ctx)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.sessions = sessions
	for _, chatRecord := range chats {
		delete(c.runtimes, chatRecord.ID)
		delete(c.unsubs, chatRecord.ID)
		delete(c.snapshots, chatRecord.ID)
		delete(c.statuses, chatRecord.ID)
	}
	c.mu.Unlock()
	c.broadcast("snapshot", c.State())
	return nil
}

func (c *Controller) ensureSessionIdle(ctx context.Context, sessionID domain.ID, chats []domain.Chat) error {
	for _, chatRecord := range chats {
		if len(chatRecord.QueuedInputs) > 0 {
			return fmt.Errorf("session has active chats and cannot be deleted")
		}
		if c.agent != nil {
			status, err := c.agent.PollChat(ctx, sessionID, chatRecord.ID)
			if err != nil {
				return err
			}
			if status.Busy || status.PendingApprovals > 0 {
				return fmt.Errorf("session has active chats and cannot be deleted")
			}
		}
		c.mu.RLock()
		rt := c.runtimes[chatRecord.ID]
		c.mu.RUnlock()
		if rt != nil {
			snapshot := rt.Snapshot()
			if snapshot.Active || len(snapshot.QueuedInputs) > 0 || len(snapshot.Approvals) > 0 {
				return fmt.Errorf("session has active chats and cannot be deleted")
			}
		}
	}
	return nil
}

// RefreshWorkspace refreshes workspace metadata and publishes a snapshot on change.
func (c *Controller) RefreshWorkspace(ctx context.Context) error {
	c.mu.RLock()
	projectRoot := c.session.ProjectRoot
	c.mu.RUnlock()
	status, err := workspacepkg.Snapshot(ctx, projectRoot)
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
		c.mu.RLock()
		projectRoot := c.session.ProjectRoot
		c.mu.RUnlock()
		items := matchingComposerSkills(projectRoot, query)
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
			c.mu.RLock()
			projectRoot := c.session.ProjectRoot
			c.mu.RUnlock()
			matches, err = reference.PathCompletions(projectRoot, query, 8)
		} else {
			var catalog []reference.Entry
			c.mu.RLock()
			projectRoot := c.session.ProjectRoot
			c.mu.RUnlock()
			catalog, err = reference.Entries(projectRoot)
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

func (c *Controller) initialSession(ctx context.Context, mode StartupMode, projectRoot string) (domain.Session, error) {
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
		return c.createWorkspaceSession(ctx, "New Session", projectRoot)
	}
	return newestSession(sessions), nil
}

func (c *Controller) loadSession(ctx context.Context, sessionID, chatID domain.ID) error {
	session, err := sessionstore.GetSession(ctx, c.store, sessionID)
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
	if c.agent == nil {
		return fmt.Errorf("no chat agent")
	}
	owner, err := c.agent.LoadSession(ctx, session.ID)
	if err != nil {
		return err
	}
	if err := owner.Reload(ctx); err != nil {
		return err
	}
	chats, err := owner.EnsureChatModels(ctx, c.cfg.DefaultProvider, c.cfg.DefaultModel)
	if err != nil {
		return err
	}
	if err := c.failStartupRunningToolCallsOnce(ctx, chats); err != nil {
		return err
	}
	if err := c.failProcessInterruptedToolCalls(ctx, chats); err != nil {
		return err
	}
	var chatRecord domain.Chat
	if chatID != "" {
		var ok bool
		chatRecord, ok = chatByID(chats, chatID)
		if !ok {
			return fmt.Errorf("chat %s not found", chatID)
		}
		if chatRecord.SessionID != session.ID {
			return fmt.Errorf("chat %s does not belong to session %s", chatID, session.ID)
		}
		chatRecord, err = owner.EnsureChatModel(ctx, chatRecord.ID, c.cfg.DefaultProvider, c.cfg.DefaultModel)
		if err != nil {
			return err
		}
	} else {
		chatRecord = newestChat(chats)
		if chatRecord.ID == "" {
			chatRecord, err = owner.EnsureDefaultChat(ctx)
			if err != nil {
				return err
			}
		}
	}
	c.ensureModelConfig(ctx, chatRecord.ProviderID, chatRecord.ModelID)
	session, chatRecord, chats, err = owner.TouchSelection(ctx, chatRecord.ID)
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
			rt, err = owner.Chat(ctx, item.ID)
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
	ownerSnapshot := owner.Snapshot()
	milestone = ownerSnapshot.Plan
	todos = ownerSnapshot.Todos
	todosByRef = ownerSnapshot.TodosByRef
	workspaceStatus, _ := workspacepkg.Snapshot(ctx, session.ProjectRoot)
	statuses := c.chatStatuses(ctx, session.ID)
	snapshots := make(map[domain.ID]chat.Snapshot, len(runtimes))
	for id, loaded := range runtimes {
		snapshot := loaded.Snapshot()
		snapshots[id] = snapshot
		statuses[id] = sidebarStatusFromSnapshot(snapshot)
	}

	c.mu.Lock()
	if c.runtimes == nil {
		c.runtimes = map[domain.ID]*chat.Chat{}
	}
	if c.unsubs == nil {
		c.unsubs = map[domain.ID]func(){}
	}
	if c.execUnsubs == nil {
		c.execUnsubs = map[domain.ID]func(){}
	}
	if c.snapshots == nil {
		c.snapshots = map[domain.ID]chat.Snapshot{}
	}
	if c.statuses == nil {
		c.statuses = map[domain.ID]ChatSidebarStatus{}
	}
	c.session = session
	c.sessions = sessions
	c.chats = chats
	c.chat = chatRecord
	c.runtime = rt
	c.unsub = nil
	for id, loaded := range runtimes {
		c.runtimes[id] = loaded
	}
	for id, unsub := range unsubs {
		c.unsubs[id] = unsub
	}
	execSubscriptions := c.ensureExecSubscriptionsLocked(chats)
	for id, snapshot := range snapshots {
		snapshot = c.snapshotWithExecProcessesLocked(snapshot)
		c.snapshots[id] = snapshot
	}
	for id, status := range statuses {
		c.statuses[id] = status
	}
	c.milestone = milestone
	c.todos = todos
	c.todosByRef = todosByRef
	c.workspace = workspaceStatus
	c.lastErr = ""
	c.mu.Unlock()

	for _, sub := range subscriptions {
		go c.forwardRuntime(sub.chatID, sub.updates)
	}
	for _, sub := range execSubscriptions {
		go c.forwardExecRuntime(sub.chatID, sub.events)
	}
	c.autoResumeRestartInterruptedChats(runtimes, snapshots)
	c.broadcast("snapshot", c.State())
	return nil
}

func (c *Controller) createWorkspaceSession(ctx context.Context, title string, projectRoot string) (domain.Session, error) {
	if c.agent == nil {
		return domain.Session{}, fmt.Errorf("no chat agent")
	}
	owner, err := c.agent.CreateSession(ctx, title, projectRoot)
	if err != nil {
		return domain.Session{}, err
	}
	return owner.Snapshot().Session, nil
}

func (c *Controller) workspaceSessions(ctx context.Context) ([]domain.Session, error) {
	return sessionstore.ListSessions(ctx, c.store)
}

func (c *Controller) sessionInWorkspace(session domain.Session) bool {
	return session.ID != ""
}

func (c *Controller) planningState(ctx context.Context, sessionID domain.ID) (planning.Plan, []planning.TodoItem, map[string][]planning.TodoItem) {
	plan, err := planning.GetPlan(ctx, c.store, sessionID)
	if err != nil {
		return planning.Plan{}, nil, nil
	}
	todosByRef := make(map[string][]planning.TodoItem, len(plan.Milestones))
	for _, milestone := range plan.Milestones {
		ref := strings.TrimSpace(milestone.Ref)
		if ref == "" {
			continue
		}
		todos, err := planning.ListTodos(ctx, c.store, sessionID, ref)
		if err != nil {
			todosByRef[ref] = nil
			continue
		}
		todosByRef[ref] = slices.Clone(todos)
	}
	active, ok := planning.ActiveMilestone(plan)
	if !ok {
		return plan, nil, todosByRef
	}
	return plan, slices.Clone(todosByRef[active.Ref]), todosByRef
}

func cloneTodosByRef(in map[string][]planning.TodoItem) map[string][]planning.TodoItem {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string][]planning.TodoItem, len(in))
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
	chats, err := sessionstore.ListChats(ctx, c.store, sessionID)
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
		if loaded, err := sessionstore.ListChats(ctx, c.store, sessionID); err == nil {
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
	providerID := strings.TrimSpace(c.chat.ProviderID)
	modelID := strings.TrimSpace(c.chat.ModelID)
	if providerID != "" && modelID != "" {
		return c.cfg.ContextWindow(providerID, modelID)
	}
	if strings.TrimSpace(c.cfg.DefaultProvider) != "" && strings.TrimSpace(c.cfg.DefaultModel) != "" {
		return c.cfg.ContextWindow(c.cfg.DefaultProvider, c.cfg.DefaultModel)
	}
	return 32768
}

func (c *Controller) modelInfoLocked() ModelInfo {
	providerID := strings.TrimSpace(c.chat.ProviderID)
	modelID := strings.TrimSpace(c.chat.ModelID)
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
