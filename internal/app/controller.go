package app

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lkarlslund/koder/internal/accesssettings"
	"github.com/lkarlslund/koder/internal/agent"
	"github.com/lkarlslund/koder/internal/attachment"
	"github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/execruntime"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/planning"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/reference"
	sessionpkg "github.com/lkarlslund/koder/internal/session"
	"github.com/lkarlslund/koder/internal/skills"
	"github.com/lkarlslund/koder/internal/tools/chattool"
	"github.com/lkarlslund/koder/internal/version"
	workspacepkg "github.com/lkarlslund/koder/internal/workspace"
)

// StartupMode selects the initial session behavior for browser app UI.
type StartupMode int

const (
	StartupModeNew StartupMode = iota
	StartupModeResume
)

const defaultWorkspaceRefreshMinInterval = 10 * time.Second

// Event is a browser app pushed UI update.
type Event struct {
	Seq     uint64 `json:"seq"`
	Type    string `json:"type"`
	Payload any    `json:"payload"`
}

// State is the browser app UI snapshot consumed by browser clients.
type State struct {
	Session       domain.Session             `json:"session"`
	Sessions      []domain.Session           `json:"sessions"`
	Chats         []domain.Chat              `json:"chats"`
	ChatStatuses  []ChatSidebarStatus        `json:"chat_statuses"`
	ActiveChatID  id.ID                      `json:"active_chat_id"`
	Access        AccessState                `json:"access"`
	Snapshot      chat.Snapshot              `json:"snapshot"`
	Snapshots     map[id.ID]chat.Snapshot    `json:"snapshots"`
	Milestones    planning.Plan              `json:"milestones"`
	Tasks         []planning.Task            `json:"tasks"`
	TasksByKey    map[string][]planning.Task `json:"tasks_by_milestone"`
	Workspace     workspacepkg.Status        `json:"workspace_status"`
	ContextWindow int                        `json:"context_window"`
	ModelInfo     ModelInfo                  `json:"model_info"`
	Theme         string                     `json:"theme"`
	TTS           TTSPreferences             `json:"tts"`
	ProjectRoot   string                     `json:"project_root"`
	Build         version.Info               `json:"build"`
	RestartNeeded bool                       `json:"restart_needed"`
	RestartBuild  RestartBuildInfo           `json:"restart_build,omitempty"`
	Error         string                     `json:"error,omitempty"`
}

// Selection identifies the browser client's selected session/chat.
type Selection struct {
	SessionID id.ID
	ChatID    id.ID
}

// RestartBuildInfo describes the already-built binary waiting for a process restart.
type RestartBuildInfo struct {
	Version   string `json:"version,omitempty"`
	Commit    string `json:"commit,omitempty"`
	Dirty     string `json:"dirty,omitempty"`
	BuildTime string `json:"build_time,omitempty"`
	BuildID   string `json:"build_id,omitempty"`
}

// ChatSidebarStatus is the browser app run state for one chat in the sidebar.
type ChatSidebarStatus struct {
	ChatID           id.ID  `json:"chat_id"`
	Status           string `json:"status"`
	Busy             bool   `json:"busy"`
	QueuedInputs     int    `json:"queued_inputs,omitempty"`
	PendingApprovals int    `json:"pending_approvals,omitempty"`
	StatusText       string `json:"status_text,omitempty"`
	LastError        string `json:"last_error,omitempty"`
}

// ModelOption is a selectable provider/model pair exposed to web clients.
type ModelOption struct {
	ProviderID       string `json:"provider_id"`
	ProviderLabel    string `json:"provider_label"`
	ModelID          string `json:"model_id"`
	SourceProviderID string `json:"source_provider_id,omitempty"`
	SourceModelID    string `json:"source_model_id,omitempty"`
	OwnedBy          string `json:"owned_by,omitempty"`
	ContextWindow    int    `json:"context_window,omitempty"`
	SupportsChat     bool   `json:"supports_chat"`
	SupportsTTS      bool   `json:"supports_tts"`
	Detected         bool   `json:"detected"`
	Custom           bool   `json:"custom"`
	BackingDetected  bool   `json:"backing_detected"`
	Editable         bool   `json:"editable"`
	Current          bool   `json:"current"`
}

// ModelInfo describes the active model capabilities shown by web clients.
type ModelInfo struct {
	ProviderID        string `json:"provider_id"`
	ModelID           string `json:"model_id"`
	SourceProviderID  string `json:"source_provider_id,omitempty"`
	SourceModelID     string `json:"source_model_id,omitempty"`
	BackingDetected   bool   `json:"backing_detected"`
	ContextWindow     int    `json:"context_window"`
	SupportsChat      bool   `json:"supports_chat"`
	SupportsTTS       bool   `json:"supports_tts"`
	SupportsTools     bool   `json:"supports_tools"`
	SupportsImages    bool   `json:"supports_images"`
	SupportsPDFs      bool   `json:"supports_pdfs"`
	CapabilitiesKnown bool   `json:"capabilities_known"`
	CapabilitySource  string `json:"capability_source,omitempty"`
}

// TTSSpeech is synthesized audio returned for browser playback.
type TTSSpeech struct {
	ProviderID  string `json:"provider_id"`
	ModelID     string `json:"model_id"`
	ContentType string `json:"content_type"`
	Audio       []byte `json:"audio"`
}

// AccessState describes the active session sandbox access settings.
type AccessState struct {
	Settings accesssettings.Settings `json:"settings"`
	Presets  []accesssettings.Preset `json:"presets"`
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
	OriginalProviderID string         `json:"original_provider_id"`
	OriginalModelID    string         `json:"original_model_id"`
	ProviderID         string         `json:"provider_id"`
	ModelID            string         `json:"model_id"`
	SourceProviderID   string         `json:"source_provider_id,omitempty"`
	SourceModelID      string         `json:"source_model_id,omitempty"`
	Custom             bool           `json:"custom"`
	Editable           bool           `json:"editable"`
	BackingDetected    bool           `json:"backing_detected"`
	ContextWindow      int            `json:"context_window"`
	ModelPreset        string         `json:"model_preset"`
	ExtraBody          map[string]any `json:"extra_body,omitempty"`
	Temperature        *float64       `json:"temperature,omitempty"`
	TopP               *float64       `json:"top_p,omitempty"`
	MinP               *float64       `json:"min_p,omitempty"`
	TopK               int            `json:"top_k,omitempty"`
	RepeatPenalty      *float64       `json:"repeat_penalty,omitempty"`
	ThinkingMode       string         `json:"thinking_mode"`
	ThinkingBudget     int            `json:"thinking_budget,omitempty"`
}

// PreferencesState is the complete settings payload exposed to browser clients.
type PreferencesState struct {
	General      GeneralPreferences      `json:"general"`
	UI           BrowserPreferences      `json:"ui"`
	Compaction   CompactionPreferences   `json:"compaction"`
	Thinking     ThinkingPreferences     `json:"thinking"`
	Prompts      []PromptPreference      `json:"prompts"`
	Providers    ProviderState           `json:"providers"`
	Models       []ModelOption           `json:"models"`
	ModelConfigs []ModelConfigPreference `json:"model_configs"`
	MCPServers   []MCPServerPreference   `json:"mcp_servers"`
	Access       AccessPreferences       `json:"access"`
	ToolDefaults []ToolDefaultPreference `json:"tool_defaults"`
	RestartKeys  []string                `json:"restart_keys,omitempty"`
}

// GeneralPreferences contains global non-provider settings.
type GeneralPreferences struct {
	DefaultProvider  string `json:"default_provider"`
	DefaultModel     string `json:"default_model"`
	MaxToolLoopSteps int    `json:"max_tool_loop_steps"`
	MaxChildChats    int    `json:"max_child_chats"`
}

// BrowserPreferences contains browser behavior settings persisted in config.
type BrowserPreferences struct {
	Theme        string         `json:"theme"`
	AutoContinue bool           `json:"auto_continue"`
	TTS          TTSPreferences `json:"tts"`
}

type TTSPreferences struct {
	Enabled        bool    `json:"enabled"`
	ProviderID     string  `json:"provider_id"`
	ModelID        string  `json:"model_id"`
	Voice          string  `json:"voice"`
	ResponseFormat string  `json:"response_format"`
	Speed          float64 `json:"speed"`
	PCMSampleRate  int     `json:"pcm_sample_rate"`
}

// CompactionPreferences contains global compaction controls.
type CompactionPreferences struct {
	AutoCompactAt        int    `json:"auto_compact_at"`
	KeepToolCalls        int    `json:"keep_tool_calls"`
	ProviderID           string `json:"provider_id"`
	ModelID              string `json:"model_id"`
	UseChatModel         bool   `json:"use_chat_model"`
	CurrentSelectionText string `json:"current_selection_text"`
}

type ThinkingPreferences struct {
	CavemanEnabled       bool   `json:"caveman_enabled"`
	ProviderID           string `json:"provider_id"`
	ModelID              string `json:"model_id"`
	UseChatModel         bool   `json:"use_chat_model"`
	CavemanPrompt        string `json:"caveman_prompt"`
	CavemanMinTokens     int    `json:"caveman_min_tokens"`
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

// AccessPreferences is the default sandbox access settings for new sessions.
type AccessPreferences struct {
	Settings accesssettings.Settings `json:"settings"`
	Presets  []accesssettings.Preset `json:"presets"`
}

// ToolDefaultPreference is one default per-session tool enabled toggle.
type ToolDefaultPreference struct {
	Tool       domain.ToolKind `json:"tool"`
	Enabled    bool            `json:"enabled"`
	Label      string          `json:"label,omitempty"`
	Group      string          `json:"group,omitempty"`
	GroupLabel string          `json:"group_label,omitempty"`
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
	ProjectRoot string           `json:"project_root"`
	Sessions    []domain.Session `json:"sessions"`
}

// Controller owns process-wide configuration and session runtimes.
type Controller struct {
	cfg   config.Config
	agent *agent.Engine

	shutdownMu                  sync.Mutex
	mu                          sync.RWMutex
	projectRoot                 string
	workspaceSnapshot           func(context.Context, string) (workspacepkg.Status, error)
	workspaceRefreshMinInterval time.Duration
	theme                       string
	lastErr                     string
	restartNeeded               bool
	restartBuild                RestartBuildInfo
	clearedStartupRunningTools  bool

	subMu   sync.Mutex
	nextSub int
	nextSeq uint64
	subs    map[int]chan Event
}

// New constructs a browser app controller.
func New(cfg config.Config, engine *agent.Engine) *Controller {
	return &Controller{
		cfg:                         cfg,
		agent:                       engine,
		theme:                       normalizeTheme(cfg.UI.Theme),
		subs:                        map[int]chan Event{},
		workspaceSnapshot:           workspacepkg.Snapshot,
		workspaceRefreshMinInterval: defaultWorkspaceRefreshMinInterval,
	}
}

// Start initializes global browser state. Sessions are activated by explicit
// client selection or creation, not by process startup.
func (c *Controller) Start(ctx context.Context, mode StartupMode, projectRoot string) error {
	_ = ctx
	_ = mode
	if c == nil {
		return fmt.Errorf("controller is nil")
	}
	if c.agent == nil {
		return fmt.Errorf("no chat agent")
	}
	c.mu.Lock()
	c.projectRoot = strings.TrimSpace(projectRoot)
	c.lastErr = ""
	c.mu.Unlock()
	return nil
}

// State returns process-wide browser app metadata. It never includes a selected
// session or chat; callers that render a session must use StateForSelection.
func (c *Controller) State() State {
	c.mu.RLock()
	base := State{
		Theme:         c.theme,
		Build:         version.Current(),
		RestartNeeded: c.restartNeeded,
		RestartBuild:  c.restartBuild,
		Error:         c.lastErr,
		TTS:           ttsPreferencesFromConfig(c.cfg.UI.TTS),
		ProjectRoot:   c.projectRoot,
	}
	c.mu.RUnlock()
	ctx := context.Background()
	if sessions, err := c.workspaceSessions(ctx); err == nil {
		base.Sessions = sessions
	}
	return base
}

// StateForSelection returns a detached browser state for a single client selection.
func (c *Controller) StateForSelection(ctx context.Context, selection Selection) (State, error) {
	return c.stateForSelection(ctx, selection)
}

// SessionByID returns a session snapshot without changing controller selection.
func (c *Controller) SessionByID(ctx context.Context, sessionID id.ID) (domain.Session, error) {
	if sessionID == "" {
		return domain.Session{}, fmt.Errorf("session id is required")
	}
	_, session, _, _, err := c.resolveStateRuntime(ctx, Selection{SessionID: sessionID})
	if err != nil {
		return domain.Session{}, err
	}
	return session, nil
}

func (c *Controller) stateForSelection(ctx context.Context, selection Selection) (State, error) {
	c.mu.RLock()
	base := State{
		Theme:         c.theme,
		Build:         version.Current(),
		RestartNeeded: c.restartNeeded,
		RestartBuild:  c.restartBuild,
		Error:         c.lastErr,
		TTS:           ttsPreferencesFromConfig(c.cfg.UI.TTS),
	}
	c.mu.RUnlock()
	if sessions, err := c.workspaceSessions(ctx); err == nil {
		base.Sessions = sessions
	} else if len(base.Sessions) == 0 {
		return State{}, err
	}
	if selection.SessionID == "" {
		return base, nil
	}
	owner, session, chatRecord, rt, err := c.resolveStateRuntime(ctx, selection)
	if err != nil {
		return State{}, err
	}
	ownerSnapshot := owner.Snapshot()
	snapshot := chat.Snapshot{}
	if rt != nil {
		snapshot = rt.Snapshot()
	} else if existing, ok := ownerSnapshot.Snapshots[chatRecord.ID]; ok {
		snapshot = existing
	}
	if snapshot.Chat.ID == "" {
		snapshot.Chat = chatRecord
	}
	if snapshot.Session.ID == "" {
		snapshot.Session = session
	}
	snapshot = c.snapshotWithExecProcessesForSession(session, snapshot)
	statuses := idleStatusesForChats(ownerSnapshot.Chats)
	snapshots := make(map[id.ID]chat.Snapshot, len(ownerSnapshot.Snapshots)+1)
	for chatID, item := range ownerSnapshot.Snapshots {
		item = c.snapshotWithExecProcessesForSession(session, item)
		snapshots[chatID] = item
		if _, ok := statuses[chatID]; ok {
			statuses[chatID] = sidebarStatusFromSnapshot(item)
		}
	}
	if chatRecord.ID != "" {
		snapshots[chatRecord.ID] = snapshot
		statuses[chatRecord.ID] = sidebarStatusFromSnapshot(snapshot)
	}
	return State{
		Session:       session,
		Sessions:      base.Sessions,
		Chats:         slices.Clone(ownerSnapshot.Chats),
		ChatStatuses:  chatStatusesForChats(ownerSnapshot.Chats, statuses),
		ActiveChatID:  chatRecord.ID,
		Access:        c.accessStateForSession(session),
		Snapshot:      snapshot,
		Snapshots:     snapshots,
		Milestones:    ownerSnapshot.Plan,
		Tasks:         slices.Clone(ownerSnapshot.Tasks),
		TasksByKey:    cloneTasksByKey(ownerSnapshot.TasksByKey),
		Workspace:     owner.WorkspaceStatus(),
		ContextWindow: c.contextWindowForChat(chatRecord),
		ModelInfo:     c.modelInfoForChat(chatRecord),
		Theme:         base.Theme,
		TTS:           base.TTS,
		ProjectRoot:   session.ProjectRoot,
		Build:         base.Build,
		RestartNeeded: base.RestartNeeded,
		RestartBuild:  base.RestartBuild,
		Error:         base.Error,
	}, nil
}

func (c *Controller) resolveStateRuntime(ctx context.Context, selection Selection) (*sessionpkg.Session, domain.Session, domain.Chat, *chat.Chat, error) {
	if selection.SessionID == "" {
		return nil, domain.Session{}, domain.Chat{}, nil, fmt.Errorf("session id is required")
	}
	if c.agent == nil {
		return nil, domain.Session{}, domain.Chat{}, nil, fmt.Errorf("no chat agent")
	}
	owner, err := c.agent.LoadSession(ctx, selection.SessionID)
	if err != nil {
		return nil, domain.Session{}, domain.Chat{}, nil, err
	}
	ownerSnapshot := owner.Snapshot()
	session := ownerSnapshot.Session
	if session.ID == "" {
		return nil, domain.Session{}, domain.Chat{}, nil, fmt.Errorf("session %s not found", selection.SessionID)
	}
	if !c.sessionInWorkspace(session) {
		return nil, domain.Session{}, domain.Chat{}, nil, fmt.Errorf("session %s does not belong to this workspace", selection.SessionID)
	}
	chatRecord := domain.Chat{}
	if selection.ChatID != "" {
		var ok bool
		chatRecord, ok = chatByID(ownerSnapshot.Chats, selection.ChatID)
		if !ok {
			return nil, domain.Session{}, domain.Chat{}, nil, fmt.Errorf("chat %s not found", selection.ChatID)
		}
	} else {
		chatRecord = newestOpenChat(ownerSnapshot.Chats)
	}
	if chatRecord.ID == "" {
		return owner, session, domain.Chat{}, nil, nil
	}
	return owner, session, chatRecord, nil, nil
}

// TimelinePage returns a transcript page for a chat in an explicitly selected session.
func (c *Controller) TimelinePage(ctx context.Context, sessionID, chatID, before id.ID, limit int, all bool) (chat.TimelinePage, error) {
	if sessionID == "" {
		return chat.TimelinePage{}, fmt.Errorf("session id is required")
	}
	if chatID == "" {
		return chat.TimelinePage{}, fmt.Errorf("chat id is required")
	}
	if c.agent == nil {
		return chat.TimelinePage{}, fmt.Errorf("no chat agent")
	}
	owner, err := c.agent.LoadSession(ctx, sessionID)
	if err != nil {
		return chat.TimelinePage{}, err
	}
	return owner.TimelinePage(ctx, chatID, before, limit, all)
}

func (c *Controller) RewindLiveChat(ctx context.Context, sessionID, chatID, anchorItemID id.ID) (any, error) {
	if c == nil {
		return nil, fmt.Errorf("controller is nil")
	}
	if c.agent == nil {
		return nil, fmt.Errorf("no chat agent")
	}
	if sessionID == "" {
		return nil, fmt.Errorf("session id is required")
	}
	if chatID == "" {
		return nil, fmt.Errorf("chat id is required")
	}
	owner, err := c.agent.LoadSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	rt, err := owner.Chat(ctx, chatID)
	if err != nil {
		return nil, err
	}
	result, err := rt.RewindLiveTimelineFrom(ctx, anchorItemID)
	if err != nil {
		return nil, err
	}
	snapshot := rt.Snapshot()
	c.broadcast("chat_delta", chat.Update{
		Snapshot:        snapshot,
		Status:          snapshot.Status,
		StatusText:      snapshot.StatusText,
		Context:         snapshot.Context,
		Active:          snapshot.Active,
		ReplaceTimeline: true,
	})
	return result, nil
}

// RollbackChatForSelection removes transcript items from anchorItemID through
// the end of the selected chat.
func (c *Controller) RollbackChatForSelection(ctx context.Context, selection Selection, chatID, anchorItemID id.ID) (any, error) {
	if selection.SessionID == "" {
		return nil, fmt.Errorf("session id is required")
	}
	if chatID == "" {
		return nil, fmt.Errorf("chat id is required")
	}
	if anchorItemID == "" {
		return nil, fmt.Errorf("anchor item id is required")
	}
	return c.RewindLiveChat(ctx, selection.SessionID, chatID, anchorItemID)
}

// ForkChatForSelection creates a new chat from the selected chat transcript
// start through anchorItemID, inclusive.
func (c *Controller) ForkChatForSelection(ctx context.Context, selection Selection, chatID, anchorItemID id.ID, title string) (domain.Chat, error) {
	if c == nil {
		return domain.Chat{}, fmt.Errorf("controller is nil")
	}
	if c.agent == nil {
		return domain.Chat{}, fmt.Errorf("no chat agent")
	}
	if selection.SessionID == "" {
		return domain.Chat{}, fmt.Errorf("session id is required")
	}
	if chatID == "" {
		return domain.Chat{}, fmt.Errorf("chat id is required")
	}
	if anchorItemID == "" {
		return domain.Chat{}, fmt.Errorf("anchor item id is required")
	}
	owner, err := c.agent.LoadSession(ctx, selection.SessionID)
	if err != nil {
		return domain.Chat{}, err
	}
	rt, err := owner.ForkChatAt(ctx, chatID, anchorItemID, title)
	if err != nil {
		return domain.Chat{}, err
	}
	snapshot := rt.Snapshot()
	return snapshot.Chat, nil
}

// MarkRestartNeeded tells web clients that a newer binary is ready but not running.
func (c *Controller) MarkRestartNeeded(build RestartBuildInfo) {
	if c == nil {
		return
	}
	slog.Info("restart needed received",
		"build_id", build.BuildID,
		"commit", build.Commit,
		"dirty", build.Dirty,
		"build_time", build.BuildTime,
	)
	c.mu.Lock()
	c.restartNeeded = true
	c.restartBuild = build
	c.mu.Unlock()
	c.broadcast("restart_delta", map[string]any{"restart_needed": true, "restart_build": build})
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

// SubscribeSelection subscribes to events from the selected session owner.
func (c *Controller) SubscribeSelection(ctx context.Context, selection Selection) (<-chan Event, func(), error) {
	if c == nil || c.agent == nil {
		return nil, nil, fmt.Errorf("no chat agent")
	}
	if selection.SessionID == "" {
		return nil, nil, fmt.Errorf("session id is required")
	}
	owner, err := c.agent.LoadSession(ctx, selection.SessionID)
	if err != nil {
		return nil, nil, err
	}
	sessionEvents, sessionUnsub := owner.Subscribe()
	var execEvents <-chan execruntime.Event
	var execUnsub func()
	if selection.ChatID != "" {
		c.mu.RLock()
		manager := c.execManagerLocked()
		c.mu.RUnlock()
		if manager != nil {
			execEvents, execUnsub = manager.Subscribe(selection.ChatID)
		}
	}
	out := make(chan Event, 64)
	done := make(chan struct{})
	var unsubOnce sync.Once
	go func() {
		defer close(out)
		for {
			select {
			case event, ok := <-sessionEvents:
				if !ok {
					return
				}
				if converted, ok := c.eventForSelectedSession(event, selection.ChatID); ok {
					select {
					case out <- converted:
					default:
					}
				}
			case _, ok := <-execEvents:
				if !ok {
					execEvents = nil
					continue
				}
				if converted, ok := c.eventForSelectedExec(ctx, owner, selection); ok {
					select {
					case out <- converted:
					default:
					}
				}
			case <-done:
				return
			}
		}
	}()
	unsub := func() {
		unsubOnce.Do(func() {
			close(done)
			sessionUnsub()
			if execUnsub != nil {
				execUnsub()
			}
		})
	}
	return out, unsub, nil
}

func (c *Controller) eventForSelectedSession(event sessionpkg.Event, selectedChatID id.ID) (Event, bool) {
	switch event.Kind {
	case sessionpkg.EventChatAdded, sessionpkg.EventChatChanged, sessionpkg.EventChatArchived:
		update := event.Update
		if update.Snapshot.Chat.ID == "" {
			update.Snapshot = event.Snapshot
		}
		if update.Snapshot.Chat.ID == "" {
			update.Snapshot.Chat = event.Chat
		}
		if update.Snapshot.Chat.ID == "" {
			return Event{}, false
		}
		session := event.Session
		if session.ID == "" {
			session = update.Snapshot.Session
		}
		if session.ID == "" {
			session.ID = event.SessionID
		}
		update.Snapshot = c.snapshotWithExecProcessesForSession(session, update.Snapshot)
		if update.Status == "" {
			update.Status = update.Snapshot.Status
		}
		if update.StatusText == "" {
			update.StatusText = update.Snapshot.StatusText
		}
		update.Active = update.Active || update.Snapshot.Active
		if selectedChatID != "" && update.Snapshot.Chat.ID != selectedChatID {
			update.Event = nil
			update.TranscriptChanged = false
			update.ContextChanged = false
		}
		return Event{Type: "chat_delta", Payload: update}, true
	case sessionpkg.EventPlanningChanged:
		return Event{Type: "planning_delta", Payload: map[string]any{
			"milestones":         event.Plan,
			"tasks":              slices.Clone(event.Tasks),
			"tasks_by_milestone": cloneTasksByKey(event.TasksByKey),
		}}, true
	case sessionpkg.EventTasksChanged:
		return Event{Type: "legacy_tasks_delta", Payload: map[string]any{"legacy_tasks": slices.Clone(event.LegacyTasks)}}, true
	case sessionpkg.EventSessionChanged:
		return Event{Type: "session_delta", Payload: map[string]any{"session": event.Session}}, true
	default:
		return Event{}, false
	}
}

func (c *Controller) eventForSelectedExec(ctx context.Context, owner *sessionpkg.Session, selection Selection) (Event, bool) {
	if owner == nil || selection.SessionID == "" || selection.ChatID == "" {
		return Event{}, false
	}
	sessionSnapshot := owner.Snapshot()
	snapshot := sessionSnapshot.Snapshots[selection.ChatID]
	if snapshot.Chat.ID == "" {
		if rt, err := owner.Chat(ctx, selection.ChatID); err == nil && rt != nil {
			snapshot = rt.Snapshot()
		}
	}
	if snapshot.Chat.ID == "" {
		for _, item := range sessionSnapshot.Chats {
			if item.ID == selection.ChatID {
				snapshot.Chat = item
				break
			}
		}
	}
	if snapshot.Chat.ID == "" {
		return Event{}, false
	}
	session := sessionSnapshot.Session
	if session.ID == "" {
		session.ID = selection.SessionID
	}
	if snapshot.Session.ID == "" {
		snapshot.Session = session
	}
	snapshot = c.snapshotWithExecProcessesForSession(session, snapshot)
	return Event{Type: "chat_delta", Payload: chat.Update{
		Snapshot:   snapshot,
		Status:     snapshot.Status,
		StatusText: snapshot.StatusText,
		Context:    snapshot.Context,
		TokenUsage: snapshot.TokenUsage,
		Active:     snapshot.Active,
	}}, true
}

// SendPromptWithKindSelection enqueues a prompt with the given delivery kind for the selected chat.
func (c *Controller) SendPromptWithKindSelection(ctx context.Context, selection Selection, kind chat.QueueKind, text string, drafts []attachment.Draft) error {
	_, _, _, rt, err := c.resolveSelectedRuntime(ctx, selection, true)
	if err != nil {
		return err
	}
	return c.enqueuePrompt(rt, text, kind, drafts)
}

func (c *Controller) enqueuePrompt(rt *chat.Chat, text string, kind chat.QueueKind, drafts []attachment.Draft) error {
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
	if rt == nil {
		return fmt.Errorf("no active chat")
	}
	source := domain.UserMessageSourceUser
	rt.Enqueue(chat.QueueItem{Kind: kind, Source: source, Text: text, Attachments: validated})
	return nil
}

// ReorderQueueForSelection reorders the selected chat queued inputs by ID.
func (c *Controller) ReorderQueueForSelection(ctx context.Context, selection Selection, ids []id.ID) error {
	_, _, _, rt, err := c.resolveSelectedRuntime(ctx, selection, true)
	if err != nil {
		return err
	}
	return reorderRuntimeQueue(rt, ids)
}

func reorderRuntimeQueue(rt *chat.Chat, ids []id.ID) error {
	if rt == nil {
		return fmt.Errorf("no active chat")
	}
	rt.ReorderQueue(ids)
	return nil
}

// DeleteQueueItemForSelection removes a queued input from the selected chat.
func (c *Controller) DeleteQueueItemForSelection(ctx context.Context, selection Selection, id id.ID) error {
	_, _, _, rt, err := c.resolveSelectedRuntime(ctx, selection, true)
	if err != nil {
		return err
	}
	return deleteRuntimeQueueItem(rt, id)
}

func deleteRuntimeQueueItem(rt *chat.Chat, id id.ID) error {
	if rt == nil {
		return fmt.Errorf("no active chat")
	}
	rt.DeleteQueueItem(id)
	return nil
}

// ToggleQueueItemKindForSelection switches a selected queued input between normal and steer delivery.
func (c *Controller) ToggleQueueItemKindForSelection(ctx context.Context, selection Selection, id id.ID) error {
	_, _, _, rt, err := c.resolveSelectedRuntime(ctx, selection, true)
	if err != nil {
		return err
	}
	return toggleRuntimeQueueItemKind(rt, id)
}

func toggleRuntimeQueueItemKind(rt *chat.Chat, id id.ID) error {
	if rt == nil {
		return fmt.Errorf("no active chat")
	}
	rt.ToggleQueueItemKind(id)
	return nil
}

// SendQueueItemNowForSelection promotes a held queued input for the selected chat.
func (c *Controller) SendQueueItemNowForSelection(ctx context.Context, selection Selection, id id.ID) error {
	_, _, _, rt, err := c.resolveSelectedRuntime(ctx, selection, true)
	if err != nil {
		return err
	}
	return sendRuntimeQueueItemNow(rt, id)
}

func sendRuntimeQueueItemNow(rt *chat.Chat, id id.ID) error {
	if rt == nil {
		return fmt.Errorf("no active chat")
	}
	rt.SendQueueItemNow(id)
	return nil
}

// AbortAndSendQueueItemNowForSelection cancels the active turn and dispatches the selected queued input.
func (c *Controller) AbortAndSendQueueItemNowForSelection(ctx context.Context, selection Selection, id id.ID) error {
	_, _, _, rt, err := c.resolveSelectedRuntime(ctx, selection, true)
	if err != nil {
		return err
	}
	return abortAndSendRuntimeQueueItemNow(rt, id)
}

func abortAndSendRuntimeQueueItemNow(rt *chat.Chat, id id.ID) error {
	if rt == nil {
		return fmt.Errorf("no active chat")
	}
	rt.AbortAndSendQueueItemNow(id)
	return nil
}

// ImportClipboardImage stores a pasted image as a draft attachment for the web composer.
func (c *Controller) ImportClipboardImage(data []byte, name string, mimeType string) (attachment.Draft, error) {
	return attachment.NewManager(c.cfg.StateDir()).ImportClipboardImageData(data, name, mimeType)
}

// ContinueForSelection asks the selected chat to continue.
func (c *Controller) ContinueForSelection(ctx context.Context, selection Selection, note string) error {
	_, _, _, rt, err := c.resolveSelectedRuntime(ctx, selection, true)
	if err != nil {
		return err
	}
	return continueRuntime(rt, note)
}

func continueRuntime(rt *chat.Chat, note string) error {
	if rt == nil {
		return fmt.Errorf("no active chat")
	}
	rt.Enqueue(chat.QueueItem{Kind: chat.QueueKindContinue, Note: note})
	return nil
}

// StopForSelection cancels the selected chat turn.
func (c *Controller) StopForSelection(ctx context.Context, selection Selection) error {
	_, session, chatRecord, rt, err := c.resolveSelectedRuntime(ctx, selection, true)
	if err != nil {
		return err
	}
	if c.agent != nil {
		c.agent.CancelActiveProviderRequests(session.ID, chatRecord.ID)
	}
	return stopRuntime(rt, chat.CancelReasonUserInterruptHard)
}

func stopRuntime(rt *chat.Chat, reason chat.CancelReason) error {
	if rt == nil {
		return fmt.Errorf("no active chat")
	}
	rt.Cancel(reason)
	return nil
}

// StopAfterCurrentTurnForSelection asks the selected chat to stop at the next turn boundary.
func (c *Controller) StopAfterCurrentTurnForSelection(ctx context.Context, selection Selection) error {
	_, _, _, rt, err := c.resolveSelectedRuntime(ctx, selection, true)
	if err != nil {
		return err
	}
	return stopRuntime(rt, chat.CancelReasonUserInterrupt)
}

// CompactForSelection starts compaction on the selected chat.
func (c *Controller) CompactForSelection(ctx context.Context, selection Selection, instructions string) error {
	_, _, _, rt, err := c.resolveSelectedRuntime(ctx, selection, true)
	if err != nil {
		return err
	}
	return compactRuntime(rt, instructions)
}

func compactRuntime(rt *chat.Chat, instructions string) error {
	if rt == nil {
		return fmt.Errorf("no active chat")
	}
	return rt.Compact(instructions)
}

// Shutdown gracefully drains the active runtime and releases subscriptions.
func (c *Controller) Shutdown(ctx context.Context) error {
	return c.ShutdownWithCancelReason(ctx, "")
}

// ShutdownWithInterruptReason closes runtimes and records an interrupt reason for active chats.
func (c *Controller) ShutdownWithInterruptReason(ctx context.Context, reason string) error {
	switch strings.TrimSpace(reason) {
	case domain.NoticeReasonProcessRestart:
		return c.ShutdownWithCancelReason(ctx, chat.CancelReasonRestartInterrupt)
	case domain.NoticeReasonProcessTerminating:
		return c.ShutdownWithCancelReason(ctx, chat.CancelReasonShutdownInterrupt)
	case domain.NoticeReasonUserInterrupted:
		return c.ShutdownWithCancelReason(ctx, chat.CancelReasonUserInterrupt)
	default:
		return c.ShutdownWithCancelReason(ctx, "")
	}
}

func (c *Controller) ShutdownWithCancelReason(ctx context.Context, reason chat.CancelReason) error {
	started := time.Now()
	c.shutdownMu.Lock()
	defer c.shutdownMu.Unlock()

	c.mu.Lock()
	agent := c.agent
	c.mu.Unlock()
	if agent == nil {
		slog.Info("controller shutdown complete", "reason", reason, "agent", false, "elapsed_ms", time.Since(started).Milliseconds())
		return nil
	}
	slog.Info("controller shutdown requested", "reason", reason, "agent", true)
	if err := agent.Shutdown(ctx, reason); err != nil {
		slog.Error("controller shutdown failed", "reason", reason, "error", err, "elapsed_ms", time.Since(started).Milliseconds())
		return err
	}
	slog.Info("controller shutdown complete", "reason", reason, "agent", true, "elapsed_ms", time.Since(started).Milliseconds())
	return nil
}

// ApproveForSelection approves a pending tool call in the selected chat.
func (c *Controller) ApproveForSelection(ctx context.Context, selection Selection, toolCallID string) error {
	_, _, _, rt, err := c.resolveSelectedRuntime(ctx, selection, true)
	if err != nil {
		return err
	}
	return approveRuntimeTool(rt, toolCallID)
}

func approveRuntimeTool(rt *chat.Chat, toolCallID string) error {
	if rt == nil {
		return fmt.Errorf("no active chat")
	}
	rt.ApproveTool(toolCallID)
	return nil
}

// DenyForSelection denies a pending tool call in the selected chat.
func (c *Controller) DenyForSelection(ctx context.Context, selection Selection, toolCallID string) error {
	_, _, _, rt, err := c.resolveSelectedRuntime(ctx, selection, true)
	if err != nil {
		return err
	}
	return denyRuntimeTool(rt, toolCallID)
}

func denyRuntimeTool(rt *chat.Chat, toolCallID string) error {
	if rt == nil {
		return fmt.Errorf("no active chat")
	}
	rt.DenyTool(toolCallID)
	return nil
}

// NewChatForSelection creates a chat in the selected session without changing controller selection.
func (c *Controller) NewChatForSelection(ctx context.Context, selection Selection, title string) (domain.Chat, error) {
	owner, session, parent, _, err := c.resolveSelectedRuntime(ctx, selection, true)
	if err != nil {
		return domain.Chat{}, err
	}
	rt, err := owner.NewChat(ctx, parent.ID, title)
	if err != nil {
		return domain.Chat{}, err
	}
	snapshot := rt.Snapshot()
	if snapshot.Chat.ID == "" {
		return domain.Chat{}, fmt.Errorf("created chat has no id")
	}
	c.broadcast("chats_delta", map[string]any{"session_id": session.ID, "chat": snapshot.Chat})
	return snapshot.Chat, nil
}

// ListChats returns the owning session's live chat list.
func (c *Controller) ListChats(ctx context.Context, sessionID id.ID) ([]chattool.Status, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("session id is required")
	}
	if c.agent == nil {
		return nil, fmt.Errorf("no chat agent")
	}
	owner, err := c.agent.LoadSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return owner.ChatToolControl("").ListChats(ctx, sessionID)
}

// StartChat creates a child chat and adds it to the live session before broadcasting.
func (c *Controller) StartChat(ctx context.Context, sessionID, parentChatID id.ID, req chattool.StartRequest) (chattool.Status, error) {
	if c.agent == nil {
		return chattool.Status{}, fmt.Errorf("no chat agent")
	}
	owner, err := c.agent.LoadSession(ctx, sessionID)
	if err != nil {
		return chattool.Status{}, err
	}
	return owner.ChatToolControl(parentChatID).StartChat(ctx, sessionID, parentChatID, req)
}

func (c *Controller) GetMilestonePlan(ctx context.Context, sessionID id.ID) (planning.Plan, error) {
	if c.agent != nil {
		if owner, err := c.agent.LoadSession(ctx, sessionID); err == nil {
			return owner.GetMilestonePlan(ctx, sessionID)
		}
	}
	return planning.Plan{}, fmt.Errorf("no live session owner")
}

func (c *Controller) SetMilestonePlan(ctx context.Context, sessionID id.ID, summary string, milestones []planning.Milestone) (planning.Plan, error) {
	if c.agent != nil {
		if owner, err := c.agent.LoadSession(ctx, sessionID); err == nil {
			plan, err := owner.SetMilestonePlan(ctx, sessionID, summary, milestones)
			if err != nil {
				return planning.Plan{}, err
			}
			return plan, nil
		}
	}
	return planning.Plan{}, fmt.Errorf("no live session owner")
}

func (c *Controller) AddTasks(ctx context.Context, sessionID id.ID, milestoneKey string, contents []string) ([]planning.Task, error) {
	if c.agent != nil {
		if owner, err := c.agent.LoadSession(ctx, sessionID); err == nil {
			created, err := owner.AddTasks(ctx, sessionID, milestoneKey, contents)
			if err != nil {
				return nil, err
			}
			return created, nil
		}
	}
	return nil, fmt.Errorf("no live session owner")
}

func (c *Controller) UpdateTask(ctx context.Context, sessionID, taskID id.ID, status planning.TaskStatus, content, note string) (planning.Task, error) {
	if c.agent != nil && sessionID != "" {
		if owner, err := c.agent.LoadSession(ctx, sessionID); err == nil {
			updated, err := owner.UpdateTask(ctx, taskID, status, content, note)
			if err != nil {
				return planning.Task{}, err
			}
			return updated, nil
		}
	}
	return planning.Task{}, fmt.Errorf("no live session owner")
}

func (c *Controller) MoveTask(ctx context.Context, sessionID id.ID, taskKey, milestoneKey string, status planning.TaskStatus, position int, note string) (planning.Task, error) {
	if c.agent != nil {
		if owner, err := c.agent.LoadSession(ctx, sessionID); err == nil {
			updated, err := owner.MoveTask(ctx, sessionID, taskKey, milestoneKey, status, position, note)
			if err != nil {
				return planning.Task{}, err
			}
			return updated, nil
		}
	}
	return planning.Task{}, fmt.Errorf("no live session owner")
}

func (c *Controller) ListTasks(ctx context.Context, sessionID id.ID, milestoneKey string) ([]planning.Task, error) {
	if c.agent != nil {
		if owner, err := c.agent.LoadSession(ctx, sessionID); err == nil {
			return owner.ListTasks(ctx, sessionID, milestoneKey)
		}
	}
	return nil, fmt.Errorf("no live session owner")
}

func (c *Controller) AddTask(ctx context.Context, sessionID id.ID, body string, status planning.LegacyTaskStatus) (planning.LegacyTask, error) {
	if c.agent != nil {
		if owner, err := c.agent.LoadSession(ctx, sessionID); err == nil {
			task, err := owner.AddTask(ctx, sessionID, body, status)
			if err != nil {
				return planning.LegacyTask{}, err
			}
			return task, nil
		}
	}
	return planning.LegacyTask{}, fmt.Errorf("no live session owner")
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

// DeleteChatForSelection archives a chat in the selected session.
func (c *Controller) DeleteChatForSelection(ctx context.Context, selection Selection, chatID id.ID) error {
	archived := true
	_, err := c.UpdateChat(ctx, selection.SessionID, selection.ChatID, chatID, chattool.UpdateRequest{Archived: &archived})
	return err
}

// UpdateChat updates chat metadata through the owning session.
func (c *Controller) UpdateChat(ctx context.Context, sessionID id.ID, ownerChatID id.ID, chatID id.ID, update chattool.UpdateRequest) (chattool.Status, error) {
	if chatID == "" {
		return chattool.Status{}, fmt.Errorf("chat id is required")
	}
	if sessionID == "" {
		return chattool.Status{}, fmt.Errorf("session id is required")
	}
	if c.agent == nil {
		return chattool.Status{}, fmt.Errorf("no chat agent")
	}
	owner, err := c.agent.LoadSession(ctx, sessionID)
	if err != nil {
		return chattool.Status{}, err
	}
	if strings.TrimSpace(update.Message) != "" || update.Interrupt {
		return owner.ChatToolControl(ownerChatID).UpdateChat(ctx, sessionID, ownerChatID, chatID, update)
	}
	status, nextChatID, err := owner.UpdateChat(ctx, chatID, update)
	if err != nil {
		return chattool.Status{}, err
	}
	if nextChatID != "" {
		_, _, _, touchErr := owner.TouchSelection(ctx, nextChatID)
		if touchErr != nil {
			return chattool.Status{}, touchErr
		}
	}
	return status, nil
}

// ReorderChatsForSelection persists sidebar chat order for the selected session.
func (c *Controller) ReorderChatsForSelection(ctx context.Context, selection Selection, chatIDs []id.ID) error {
	if selection.SessionID == "" {
		return fmt.Errorf("session id is required")
	}
	if c.agent == nil {
		return fmt.Errorf("no chat agent")
	}
	owner, err := c.agent.LoadSession(ctx, selection.SessionID)
	if err != nil {
		return err
	}
	ordered, err := owner.ReorderChats(ctx, chatIDs)
	if err != nil {
		return err
	}
	c.broadcast("chats_delta", map[string]any{"session_id": selection.SessionID, "chats": ordered})
	return nil
}

// Sessions returns sessions for the current workspace.
func (c *Controller) Sessions(ctx context.Context) (SessionState, error) {
	sessions, err := c.workspaceSessions(ctx)
	if err != nil {
		return SessionState{}, err
	}
	c.mu.RLock()
	projectRoot := c.projectRoot
	c.mu.RUnlock()
	return SessionState{ProjectRoot: projectRoot, Sessions: sessions}, nil
}

// CreateSession creates a new session without changing controller selection.
func (c *Controller) CreateSession(ctx context.Context, title string, projectRoot string, createProjectRoot bool) (domain.Session, error) {
	if strings.TrimSpace(projectRoot) == "" {
		c.mu.RLock()
		projectRoot = c.projectRoot
		c.mu.RUnlock()
	}
	if c.agent == nil {
		return domain.Session{}, fmt.Errorf("no chat agent")
	}
	owner, err := c.agent.CreateSession(ctx, title, projectRoot, createProjectRoot)
	if err != nil {
		return domain.Session{}, err
	}
	session := owner.Snapshot().Session
	sessions, err := c.workspaceSessions(ctx)
	if err != nil {
		return domain.Session{}, err
	}
	c.broadcast("sessions_delta", map[string]any{"sessions": sessions})
	return session, nil
}

// RenameSession updates a session title.
func (c *Controller) RenameSession(ctx context.Context, sessionID id.ID, title string) error {
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
	c.broadcast("sessions_delta", map[string]any{"session": updated})
	return nil
}

// DeleteSession deletes an idle session and switches away when it is selected.
func (c *Controller) DeleteSession(ctx context.Context, sessionID id.ID) error {
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
	if err := c.ensureSessionIdle(ctx, owner, chats); err != nil {
		return err
	}
	if err := c.agent.DeleteSession(ctx, sessionID); err != nil {
		return err
	}
	sessions, err := c.workspaceSessions(ctx)
	if err != nil {
		return err
	}
	c.broadcast("sessions_delta", map[string]any{"sessions": sessions, "deleted_session_id": sessionID})
	return nil
}

func (c *Controller) ensureSessionIdle(ctx context.Context, owner *sessionpkg.Session, chats []domain.Chat) error {
	if owner == nil {
		return fmt.Errorf("session owner is required")
	}
	for _, chatRecord := range chats {
		if len(chatRecord.QueuedInputs) > 0 {
			return fmt.Errorf("session has active chats and cannot be deleted")
		}
		status, err := owner.ChatStatus(ctx, chatRecord.ID)
		if err != nil {
			return err
		}
		if status.Busy || status.QueuedInputs > 0 || status.PendingApprovals > 0 {
			return fmt.Errorf("session has active chats and cannot be deleted")
		}
	}
	return nil
}

type workspaceRefreshTrigger uint8

const (
	workspaceRefreshInitial workspaceRefreshTrigger = iota
	workspaceRefreshUser
	workspaceRefreshWatcher
	workspaceRefreshTimer
)

// RefreshWorkspaceForSelection requests a workspace scan for the selected session.
func (c *Controller) RefreshWorkspaceForSelection(ctx context.Context, selection Selection) (workspacepkg.Status, error) {
	if selection.SessionID == "" {
		return workspacepkg.Status{}, fmt.Errorf("session id is required")
	}
	if c.agent == nil {
		return workspacepkg.Status{}, fmt.Errorf("no chat agent")
	}
	owner, err := c.agent.LoadSession(ctx, selection.SessionID)
	if err != nil {
		return workspacepkg.Status{}, err
	}
	session := owner.Snapshot().Session
	if !c.sessionInWorkspace(session) {
		return workspacepkg.Status{}, fmt.Errorf("session %s does not belong to this workspace", selection.SessionID)
	}
	if err := c.refreshSessionWorkspace(ctx, owner, workspaceRefreshUser); err != nil {
		return workspacepkg.Status{}, err
	}
	return owner.WorkspaceStatus(), nil
}

// EnsureSessionWorkspace starts workspace monitoring for a selected session.
func (c *Controller) EnsureSessionWorkspace(ctx context.Context, sessionID id.ID) error {
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
	session := owner.Snapshot().Session
	if !c.sessionInWorkspace(session) {
		return fmt.Errorf("session %s does not belong to this workspace", sessionID)
	}
	c.ensureSessionWorkspace(owner)
	return nil
}

func (c *Controller) refreshSessionWorkspace(ctx context.Context, owner *sessionpkg.Session, trigger workspaceRefreshTrigger) error {
	if owner == nil {
		return fmt.Errorf("session is required")
	}
	sessionID := owner.Snapshot().Session.ID
	minInterval := c.workspaceRefreshMinInterval
	if minInterval <= 0 {
		minInterval = defaultWorkspaceRefreshMinInterval
	}
	snapshot := c.workspaceSnapshot
	if snapshot == nil {
		snapshot = workspacepkg.Snapshot
	}
	force := trigger == workspaceRefreshInitial || trigger == workspaceRefreshUser || trigger == workspaceRefreshTimer
	return owner.RefreshWorkspace(ctx, snapshot, minInterval, force, func(status workspacepkg.Status) {
		c.broadcastWorkspace(sessionID, status)
	})
}

func (c *Controller) ensureSessionWorkspace(owner *sessionpkg.Session) {
	if owner == nil {
		return
	}
	status := owner.WorkspaceStatus()
	c.replaceWorkspaceWatcher(owner)
	if status.RefreshedAt.IsZero() {
		go func() {
			if err := c.refreshSessionWorkspace(context.Background(), owner, workspaceRefreshInitial); err != nil {
				snapshot := owner.Snapshot()
				slog.Warn("initial workspace refresh failed", "session_id", snapshot.Session.ID, "project_root", snapshot.Session.ProjectRoot, "error", err)
			}
		}()
	}
}

func (c *Controller) broadcastWorkspace(sessionID id.ID, status workspacepkg.Status) {
	c.broadcast("workspace_delta", map[string]any{"session_id": sessionID, "workspace_status": status})
}

func (c *Controller) replaceWorkspaceWatcher(owner *sessionpkg.Session) {
	if owner == nil {
		return
	}
	sessionID := owner.Snapshot().Session.ID
	minInterval := c.workspaceRefreshMinInterval
	if minInterval <= 0 {
		minInterval = defaultWorkspaceRefreshMinInterval
	}
	snapshot := c.workspaceSnapshot
	if snapshot == nil {
		snapshot = workspacepkg.Snapshot
	}
	owner.ReplaceWorkspaceWatcher(snapshot, minInterval, func(status workspacepkg.Status) {
		c.broadcastWorkspace(sessionID, status)
	})
}

// CompleteComposerForSelection returns command, skill, and reference completions
// for a composer token in an explicitly selected session.
func (c *Controller) CompleteComposerForSelection(ctx context.Context, selection Selection, text string, cursor int) (ComposerCompletions, error) {
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
	projectRoot := ""
	if selection.SessionID != "" {
		session, err := c.SessionByID(ctx, selection.SessionID)
		if err != nil {
			return ComposerCompletions{}, err
		}
		projectRoot = session.ProjectRoot
	}
	if query, start, ok := composerSkillQuery(text, cursor); ok {
		if projectRoot == "" {
			return ComposerCompletions{}, fmt.Errorf("session id is required for skill completions")
		}
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
		if projectRoot == "" {
			return ComposerCompletions{}, fmt.Errorf("session id is required for file completions")
		}
		var matches []reference.Entry
		var err error
		if pathMode {
			matches, err = reference.PathCompletions(projectRoot, query, 8)
		} else {
			var catalog []reference.Entry
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
	{Command: "/compact", Description: "Compact the active chat; append instructions to guide the summary"},
	{Command: "/model", Description: "Select the chat model"},
	{Command: "/permissions", Description: "Change access settings"},
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
	if c.agent == nil {
		return domain.Session{}, fmt.Errorf("no chat agent")
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

func (c *Controller) loadSession(ctx context.Context, sessionID, chatID id.ID) error {
	if c.agent == nil {
		return fmt.Errorf("no chat agent")
	}
	owner, err := c.agent.LoadSession(ctx, sessionID)
	if err != nil {
		return err
	}
	ownerSnapshot := owner.Snapshot()
	session := ownerSnapshot.Session
	if session.ID == "" {
		return fmt.Errorf("session %s not found", sessionID)
	}
	if !c.sessionInWorkspace(session) {
		return fmt.Errorf("session %s does not belong to this workspace", sessionID)
	}
	chats := ownerSnapshot.Chats
	if err := c.failStartupRunningToolCallsOnce(ctx, chats); err != nil {
		return err
	}
	if err := c.failProcessInterruptedToolCalls(ctx, chats); err != nil {
		return err
	}
	ownerSnapshot = owner.Snapshot()
	chats = ownerSnapshot.Chats
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
		chatRecord, err = owner.EnsureChatModel(ctx, chatRecord.ID, c.cfg.Defaults.ProviderID, c.cfg.Defaults.ModelID)
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
	rt, err := owner.Chat(ctx, chatRecord.ID)
	if err != nil {
		return err
	}
	runtimes := map[id.ID]*chat.Chat{chatRecord.ID: rt}
	for _, item := range chats {
		if !item.AutoRestart || item.ID == chatRecord.ID {
			continue
		}
		loaded, err := owner.Chat(ctx, item.ID)
		if err != nil {
			return err
		}
		runtimes[item.ID] = loaded
	}
	ownerSnapshot = owner.Snapshot()
	snapshots := make(map[id.ID]chat.Snapshot, len(ownerSnapshot.Snapshots)+1)
	for id, snapshot := range ownerSnapshot.Snapshots {
		snapshots[id] = snapshot
	}
	if _, ok := snapshots[chatRecord.ID]; !ok {
		snapshots[chatRecord.ID] = rt.Snapshot()
	}
	c.mu.Lock()
	c.lastErr = ""
	c.mu.Unlock()

	c.ensureSessionWorkspace(owner)
	c.autoResumeRestartInterruptedChats(runtimes, snapshots)
	for _, loaded := range runtimes {
		loaded.Kick()
	}
	return nil
}

func (c *Controller) createWorkspaceSession(ctx context.Context, title string, projectRoot string) (domain.Session, error) {
	if c.agent == nil {
		return domain.Session{}, fmt.Errorf("no chat agent")
	}
	owner, err := c.agent.CreateSession(ctx, title, projectRoot, false)
	if err != nil {
		return domain.Session{}, err
	}
	return owner.Snapshot().Session, nil
}

func (c *Controller) workspaceSessions(ctx context.Context) ([]domain.Session, error) {
	if c.agent == nil {
		return nil, fmt.Errorf("no chat agent")
	}
	return c.agent.Sessions(ctx)
}

func (c *Controller) sessionInWorkspace(session domain.Session) bool {
	return session.ID != ""
}

func cloneTasksByKey(in map[string][]planning.Task) map[string][]planning.Task {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string][]planning.Task, len(in))
	for ref, tasks := range in {
		out[ref] = slices.Clone(tasks)
	}
	return out
}

func idleStatusesForChats(chats []domain.Chat) map[id.ID]ChatSidebarStatus {
	out := make(map[id.ID]ChatSidebarStatus, len(chats))
	for _, item := range chats {
		if item.ID == "" {
			continue
		}
		out[item.ID] = idleChatSidebarStatus(item.ID)
	}
	return out
}

func chatStatusesForChats(chats []domain.Chat, statuses map[id.ID]ChatSidebarStatus) []ChatSidebarStatus {
	out := make([]ChatSidebarStatus, 0, len(chats))
	for _, item := range chats {
		status, ok := statuses[item.ID]
		if !ok {
			status = idleChatSidebarStatus(item.ID)
		}
		out = append(out, status)
	}
	return out
}

func idleChatSidebarStatus(chatID id.ID) ChatSidebarStatus {
	return ChatSidebarStatus{ChatID: chatID, Status: string(chat.StatusIdle), StatusText: "Idle"}
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
		QueuedInputs:     len(snapshot.QueuedInputs),
		PendingApprovals: len(snapshot.Approvals),
		StatusText:       text,
	}
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
	case string(chattool.RunStateFailed):
		return "Failed"
	case string(chattool.RunStateRunning):
		return "Running"
	case string(chattool.RunStateCompleted):
		return "Completed"
	case string(chattool.RunStateCancelled):
		return "Cancelled"
	default:
		return "Idle"
	}
}

func (c *Controller) resolveSelectedRuntime(ctx context.Context, selection Selection, allowDefaultChat bool) (*sessionpkg.Session, domain.Session, domain.Chat, *chat.Chat, error) {
	owner, session, chatRecord, err := c.resolveSelectedChat(ctx, selection, allowDefaultChat)
	if err != nil {
		return nil, domain.Session{}, domain.Chat{}, nil, err
	}
	rt, err := owner.Chat(ctx, chatRecord.ID)
	if err != nil {
		return nil, domain.Session{}, domain.Chat{}, nil, err
	}
	return owner, session, chatRecord, rt, nil
}

func (c *Controller) resolveSelectedChat(ctx context.Context, selection Selection, allowDefaultChat bool) (*sessionpkg.Session, domain.Session, domain.Chat, error) {
	if selection.SessionID == "" {
		return nil, domain.Session{}, domain.Chat{}, fmt.Errorf("session id is required")
	}
	if c.agent == nil {
		return nil, domain.Session{}, domain.Chat{}, fmt.Errorf("no chat agent")
	}
	owner, err := c.agent.LoadSession(ctx, selection.SessionID)
	if err != nil {
		return nil, domain.Session{}, domain.Chat{}, err
	}
	ownerSnapshot := owner.Snapshot()
	session := ownerSnapshot.Session
	if session.ID == "" {
		return nil, domain.Session{}, domain.Chat{}, fmt.Errorf("session %s not found", selection.SessionID)
	}
	if !c.sessionInWorkspace(session) {
		return nil, domain.Session{}, domain.Chat{}, fmt.Errorf("session %s does not belong to this workspace", selection.SessionID)
	}
	chatID := selection.ChatID
	if chatID == "" {
		if !allowDefaultChat {
			return nil, domain.Session{}, domain.Chat{}, fmt.Errorf("chat id is required")
		}
		chatRecord := newestOpenChat(ownerSnapshot.Chats)
		if chatRecord.ID == "" {
			chatRecord, err = owner.EnsureDefaultChat(ctx)
			if err != nil {
				return nil, domain.Session{}, domain.Chat{}, err
			}
		}
		chatID = chatRecord.ID
	}
	chatRecord, err := owner.EnsureChatModel(ctx, chatID, c.cfg.Defaults.ProviderID, c.cfg.Defaults.ModelID)
	if err != nil {
		return nil, domain.Session{}, domain.Chat{}, err
	}
	session, chatRecord, _, err = owner.TouchSelection(ctx, chatRecord.ID)
	if err != nil {
		return nil, domain.Session{}, domain.Chat{}, err
	}
	chatRecord.PermissionProfile = ""
	c.ensureModelConfig(ctx, chatRecord.ProviderID, chatRecord.ModelID)
	return owner, session, chatRecord, nil
}

func (c *Controller) accessStateForSession(session domain.Session) AccessState {
	settings := session.AccessSettings
	if accesssettings.IsZero(settings) {
		c.mu.RLock()
		settings = c.cfg.Access
		c.mu.RUnlock()
	}
	return AccessState{
		Settings: accesssettings.Normalize(settings),
		Presets:  accesssettings.Presets(),
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

func (c *Controller) contextWindowForChat(chatRecord domain.Chat) int {
	providerID := strings.TrimSpace(chatRecord.ProviderID)
	modelID := strings.TrimSpace(chatRecord.ModelID)
	c.mu.RLock()
	cfg := c.cfg
	c.mu.RUnlock()
	if providerID == "" || modelID == "" {
		if strings.TrimSpace(cfg.Defaults.ProviderID) != "" && strings.TrimSpace(cfg.Defaults.ModelID) != "" {
			return cfg.ContextWindow(cfg.Defaults.ProviderID, cfg.Defaults.ModelID)
		}
		return 32768
	}
	return cfg.ContextWindow(providerID, modelID)
}

func (c *Controller) modelInfoForChat(chatRecord domain.Chat) ModelInfo {
	providerID := strings.TrimSpace(chatRecord.ProviderID)
	modelID := strings.TrimSpace(chatRecord.ModelID)
	c.mu.RLock()
	cfg := c.cfg
	c.mu.RUnlock()
	sourceProviderID, sourceModelID := cfg.ResolveModel(providerID, modelID)
	providerCfg, ok := cfg.Provider(sourceProviderID)
	if !ok {
		providerCfg = config.Provider{}
	}
	info := ModelInfo{
		ProviderID:       providerID,
		ModelID:          modelID,
		SourceProviderID: sourceProviderID,
		SourceModelID:    sourceModelID,
		ContextWindow:    c.contextWindowForChat(chatRecord),
		SupportsChat:     true,
		SupportsTools:    true,
	}
	if sourceModelID == "" {
		return info
	}
	enriched, err := provider.NewCapabilityStore(cfg.StateDir()).EnrichModel(sourceProviderID, providerCfg, domain.Model{ID: sourceModelID})
	if err != nil {
		return info
	}
	info.SupportsChat = enriched.SupportsChat
	info.SupportsTTS = enriched.SupportsTTS
	info.SupportsTools = enriched.SupportsChat
	info.SupportsImages = enriched.SupportsImages
	info.SupportsPDFs = enriched.SupportsPDFs
	info.CapabilitiesKnown = enriched.CapabilitiesKnown
	info.CapabilitySource = strings.TrimSpace(enriched.CapabilitySource)
	return info
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

func newestOpenChat(chats []domain.Chat) domain.Chat {
	var best domain.Chat
	for _, item := range chats {
		if item.Archived || item.ID == "" {
			continue
		}
		if best.ID == "" || item.UpdatedAt.After(best.UpdatedAt) || (item.UpdatedAt.Equal(best.UpdatedAt) && item.ID > best.ID) {
			best = item
		}
	}
	return best
}

func chatByID(chats []domain.Chat, chatID id.ID) (domain.Chat, bool) {
	for _, item := range chats {
		if item.ID == chatID {
			return item, true
		}
	}
	return domain.Chat{}, false
}

func fallbackChatID(chats []domain.Chat, deleting domain.Chat) id.ID {
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

// Touch avoids stale-session ordering when a renderer action changes state.
func Touch(now time.Time, chat *domain.Chat) {
	if chat != nil {
		chat.UpdatedAt = now
	}
}
