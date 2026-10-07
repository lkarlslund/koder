package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/agents"
	"github.com/lkarlslund/koder/internal/attachment"
	"github.com/lkarlslund/koder/internal/browser"
	"github.com/lkarlslund/koder/internal/browserapi"
	chatpkg "github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/chatinteraction"
	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/codexapp"
	"github.com/lkarlslund/koder/internal/codexdriver"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/debugsrv"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/execruntime"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/mcp"
	"github.com/lkarlslund/koder/internal/modeloverlay"
	"github.com/lkarlslund/koder/internal/modelruntime"
	"github.com/lkarlslund/koder/internal/offeredfile"
	"github.com/lkarlslund/koder/internal/phonedevice"
	"github.com/lkarlslund/koder/internal/planning"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/reference"
	sessionpkg "github.com/lkarlslund/koder/internal/session"
	"github.com/lkarlslund/koder/internal/settings"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/textutil"
	"github.com/lkarlslund/koder/internal/tokenestimate"
	"github.com/lkarlslund/koder/internal/toolruntime"
	_ "github.com/lkarlslund/koder/internal/tools/all"
	"github.com/lkarlslund/koder/internal/tools/sessiontool"
)

type Engine struct {
	cfg           config.Config
	store         *store.Store
	debug         *debugsrv.Recorder
	files         *attachment.Manager
	offeredFiles  *offeredfile.Manager
	caps          *provider.CapabilityStore
	health        *provider.HealthTracker
	agents        *agents.Manager
	mcp           *mcp.Manager
	settings      *settings.Store
	modelOverlays modeloverlay.Catalog
	*modelruntime.Runtime
	toolsRuntime *toolruntime.Runtime
	browser      *browser.Manager
	codex        *codexdriver.Manager
	registry     *sessionpkg.Registry
	retryPause   func(context.Context, time.Duration, func(time.Duration)) error
}

// SetVoiceSessionControl connects the native voice profile to process-wide
// work-session coordination after the application controller is constructed.
func (e *Engine) SetVoiceSessionControl(control sessiontool.Control) {
	if e != nil && e.toolsRuntime != nil {
		e.toolsRuntime.SetVoiceSessionControl(control)
	}
}

// SetPhoneDeviceControl connects permission-gated Android capabilities to
// voice chats without exposing them to other chat roles.
func (e *Engine) SetPhoneDeviceControl(control phonedevice.Control) {
	if e != nil && e.toolsRuntime != nil {
		e.toolsRuntime.SetPhoneDeviceControl(control)
	}
}

const (
	// Bound only the visible summary. Provider generation limits commonly include
	// hidden reasoning, so compaction requests deliberately do not set one.
	compactionMaxBytes                = 64 * 1024
	compactionReductionCheckMinTokens = 32 * 1024
)

func New(cfg config.Config, st *store.Store, debug *debugsrv.Recorder, mcpManagers ...*mcp.Manager) *Engine {
	var mcpManager *mcp.Manager
	if len(mcpManagers) > 0 {
		mcpManager = mcpManagers[0]
	}
	execRuntime := execruntime.NewManager()
	settingsStore := settings.New(cfg)
	healthTracker := provider.NewHealthTracker()
	e := &Engine{
		cfg:           cfg,
		store:         st,
		debug:         debug,
		files:         attachment.NewManager(cfg.StateDir()),
		offeredFiles:  offeredfile.NewManager(st),
		caps:          provider.NewCapabilityStore(cfg.StateDir()),
		health:        healthTracker,
		agents:        agents.NewManager(cfg.StateDir(), filepath.Join(filepath.Dir(cfg.Path()), "AGENTS.md")),
		mcp:           mcpManager,
		settings:      settingsStore,
		modelOverlays: modeloverlay.Load(cfg.ManagedAssetsDir()),
		retryPause:    modelruntime.DefaultRetryPause,
	}
	e.browser = browser.NewManager(cfg.Browser, cfg.StateDir())
	e.browser.SetDeciderResolver(e.resolveBrowserDecider)
	e.codex = codexdriver.New(codexdriver.NewSandboxProcessFactory(codexdriver.SandboxProcessConfig{
		Client: codexapp.Config{
			Executable: cfg.Codex.Executable,
			CodexHome:  cfg.Codex.Home,
		},
		StateDir: cfg.StateDir(),
		Access:   settingsStore.Access,
	}), st, func(_ domain.Session, chatRecord domain.Chat) string {
		parts := []string{
			strings.TrimSpace(chatrole.SystemPromptForChat(chatRecord)),
			strings.TrimSpace(chatinteraction.SystemPrompt(chatRecord.EffectiveInteractionMode())),
		}
		return strings.TrimSpace(strings.Join(parts, "\n\n"))
	})
	e.Runtime = modelruntime.New(modelruntime.Config{
		Config:   cfg,
		Store:    st,
		Debug:    debug,
		Files:    e.files,
		Caps:     e.caps,
		Health:   healthTracker,
		Agents:   e.agents,
		Settings: settingsStore,
		MCP:      e.mcp,
	})
	e.SetRetryPause(func(ctx context.Context, delay time.Duration, onTick func(time.Duration)) error {
		if e.retryPause == nil {
			return modelruntime.DefaultRetryPause(ctx, delay, onTick)
		}
		return e.retryPause(ctx, delay, onTick)
	})
	chatSource := chatpkg.NewSource(e.ChatDeps)
	planSource := planning.NewSource(st)
	e.registry = sessionpkg.NewRegistry(st, chatSource, planSource, e.sessionRegistryConfig(settingsStore.NewSessionDefaults()))
	e.SetSessionSource(e)
	e.toolsRuntime = toolruntime.New(toolruntime.Config{
		Settings:         settingsStore,
		Debug:            debug,
		Sessions:         e.registry,
		Exec:             execRuntime,
		MCP:              e.mcp,
		Browser:          e.browser,
		Attachments:      e.files,
		OfferedFiles:     e.offeredFiles,
		ManagedSkillsDir: filepath.Join(cfg.ManagedAssetsDir(), "skills"),
		DisabledSkills:   cfg.Skills.Disabled,
		SkillCatalogMax:  cfg.Skills.CatalogMaxChars,
	})
	e.SetToolsRuntime(e.toolsRuntime)
	if e.codex != nil {
		e.codex.SetToolBridge(codexToolBridge{engine: e})
	}
	return e
}

// ProviderHealthTracker returns the process-local provider runtime health
// shared by chats, voice, discovery, and settings.
func (e *Engine) ProviderHealthTracker() *provider.HealthTracker {
	if e == nil {
		return nil
	}
	return e.health
}

func (e *Engine) UpdateConfig(cfg config.Config) {
	if err := e.updateConfig(cfg, true); err != nil {
		slog.Warn("reload MCP configuration", "error", err)
	}
}

// UpdateConfigAndReloadMCP applies configuration and waits for MCP discovery.
// Settings saves use this path so their response reflects terminal runtime state.
func (e *Engine) UpdateConfigAndReloadMCP(ctx context.Context, cfg config.Config) error {
	if err := e.updateConfig(cfg, false); err != nil {
		return err
	}
	if e.mcp == nil {
		return nil
	}
	return e.mcp.ConnectAll(ctx)
}

func (e *Engine) updateConfig(cfg config.Config, connectMCPAsync bool) error {
	e.cfg = cfg
	e.modelOverlays = modeloverlay.Load(cfg.ManagedAssetsDir())
	if e.settings != nil {
		e.settings.Update(cfg)
	} else {
		e.settings = settings.New(cfg)
	}
	e.agents = agents.NewManager(cfg.StateDir(), filepath.Join(filepath.Dir(cfg.Path()), "AGENTS.md"))
	if e.Runtime != nil {
		e.Runtime.UpdateConfig(cfg)
	}
	if e.registry != nil {
		e.registry.UpdateConfig(e.sessionRegistryConfig(e.settings.NewSessionDefaults()))
	}
	if e.toolsRuntime != nil {
		e.toolsRuntime.UpdateSettings(e.settings)
		e.toolsRuntime.UpdateSkills(cfg.Skills.Disabled, cfg.Skills.CatalogMaxChars)
	}
	if e.browser != nil {
		e.browser.UpdateConfig(cfg.Browser)
	}
	if e.codex != nil {
		if err := e.codex.UpdateProcessFactory(codexdriver.NewSandboxProcessFactory(codexdriver.SandboxProcessConfig{
			Client: codexapp.Config{
				Executable: cfg.Codex.Executable,
				CodexHome:  cfg.Codex.Home,
			},
			StateDir: cfg.StateDir(),
			Access:   e.settings.Access,
		})); err != nil {
			slog.Warn("stop stale Codex chat processes after configuration change", "error", err)
		}
	}
	if e.mcp != nil {
		if err := e.mcp.LoadConfig(cfg.MCPServers); err != nil {
			return err
		}
		if connectMCPAsync {
			go func() {
				if err := e.mcp.ConnectAll(context.Background()); err != nil {
					slog.Warn("connect MCP servers after configuration change", "error", err)
				}
			}()
		}
	}
	return nil
}

// CancelActiveProviderRequests cancels in-flight provider HTTP requests for one chat.
func (e *Engine) CancelActiveProviderRequests(sessionID, chatID id.ID) int {
	if e == nil || e.debug == nil || sessionID == "" || chatID == "" {
		return 0
	}
	return e.debug.CancelActiveHTTPTraces(debugsrv.HTTPTraceFilter{SessionID: sessionID, ChatID: chatID})
}

func (e *Engine) ListMCPServers() []mcp.ServerState {
	if e.mcp == nil {
		return nil
	}
	return e.mcp.ListServers()
}

func (e *Engine) BrowserStatus(chat browserapi.Chat) browserapi.Status {
	if e == nil || e.browser == nil {
		return browserapi.Status{State: "unavailable"}
	}
	return e.browser.Status(context.Background(), chat)
}

func (e *Engine) BrowserAction(ctx context.Context, action string, chat browserapi.Chat) (browserapi.Status, error) {
	if e == nil || e.browser == nil {
		return browserapi.Status{}, fmt.Errorf("browser service is unavailable")
	}
	var err error
	switch action {
	case "status":
		// Status is side-effect free.
	case "start":
		err = e.browser.Start(ctx)
	case "show":
		err = e.browser.Show(ctx, chat)
	case "stop":
		err = e.browser.Stop(ctx)
	case "restart":
		err = e.browser.Restart(ctx)
	case "reset_profile":
		err = e.browser.ResetProfile(ctx)
	default:
		err = fmt.Errorf("unknown browser action %q", action)
	}
	return e.browser.Status(ctx, chat), err
}

func (e *Engine) BrowserTabs(ctx context.Context, chat browserapi.Chat) ([]browserapi.Tab, error) {
	if e == nil || e.browser == nil {
		return nil, fmt.Errorf("browser service is unavailable")
	}
	return e.browser.Tabs(ctx, chat)
}

func (e *Engine) BrowserTabScreenshot(ctx context.Context, chat browserapi.Chat, tabID string) (browserapi.Binary, error) {
	if e == nil || e.browser == nil {
		return browserapi.Binary{}, fmt.Errorf("browser service is unavailable")
	}
	return e.browser.ScreenshotTab(ctx, chat, tabID, "jpeg", 72)
}

func (e *Engine) ReloadMCP(ctx context.Context) error {
	if e.mcp == nil {
		return nil
	}
	if err := e.mcp.LoadConfig(e.cfg.MCPServers); err != nil {
		return err
	}
	return e.mcp.ConnectAll(ctx)
}

func (e *Engine) ExecManager() *execruntime.Manager {
	if e.toolsRuntime != nil {
		return e.toolsRuntime.ExecManager()
	}
	return nil
}

func (e *Engine) SetExecManager(manager *execruntime.Manager) {
	if e == nil || manager == nil {
		return
	}
	if e.toolsRuntime != nil {
		e.toolsRuntime.SetExecManager(manager)
	}
}

func (e *Engine) clientForChat(chat domain.Chat) (*provider.Client, error) {
	model, err := e.settings.Model(chat)
	if err != nil {
		return nil, err
	}
	return provider.New(model.SourceProviderID, model.Provider, e.debug, e.health)
}

func (e *Engine) CompactChat(ctx context.Context, rt *chatpkg.Chat, instructions string, out chan<- domain.Event) error {
	if rt == nil {
		return fmt.Errorf("chat is required")
	}
	snapshot := rt.Snapshot()
	session := snapshot.Session
	chatRecord := snapshot.Chat
	client, err := e.clientForChat(chatRecord)
	if err != nil {
		return err
	}
	if out != nil {
		out <- domain.Event{Kind: domain.EventKindStatus, Text: "Compacting session..."}
	}
	if err := e.compactChat(ctx, session, rt, client, "manual", instructions, out); err != nil {
		return err
	}
	if out != nil {
		out <- domain.Event{Kind: domain.EventKindMessageDone}
	}
	return nil
}

func (e *Engine) PreviewNextRequest(ctx context.Context, session domain.Session, prompt string, drafts []attachment.Draft, refs []reference.Draft, note string) (provider.ChatRequest, error) {
	owner, err := e.LoadSession(ctx, session.ID)
	if err != nil {
		return provider.ChatRequest{}, err
	}
	chat, err := owner.EnsureDefaultChat(ctx)
	if err != nil {
		return provider.ChatRequest{}, err
	}
	return e.PreviewNextRequestForChat(ctx, session, chat, prompt, drafts, refs, note)
}

func (e *Engine) PreviewNextRequestForChat(ctx context.Context, session domain.Session, chat domain.Chat, prompt string, drafts []attachment.Draft, refs []reference.Draft, note string) (provider.ChatRequest, error) {
	if err := e.ValidatePromptAttachments(chat, drafts); err != nil {
		return provider.ChatRequest{}, err
	}
	messages, err := e.buildConversationPreview(ctx, session, chat.ID, prompt, drafts, refs, chatpkg.TurnInstructionBlocks(note, ""))
	if err != nil {
		return provider.ChatRequest{}, err
	}
	return e.ChatRequest(session, chat, messages, false), nil
}

func (e *Engine) AutoCompactAtTurnBoundary(ctx context.Context, rt *chatpkg.Chat, client *provider.Client, messages []provider.Message, out chan<- domain.Event) (bool, error) {
	if rt == nil {
		return false, fmt.Errorf("chat runtime is required")
	}
	snapshot := rt.Snapshot()
	compacted, err := e.autoCompactAtTurnBoundary(ctx, snapshot.Session, snapshot.Chat, rt, client, messages, out)
	if err != nil || !compacted {
		return compacted, err
	}
	owner, err := e.LoadSession(ctx, snapshot.Session.ID)
	if err != nil {
		return false, err
	}
	rt.SetSession(owner.Snapshot().Session)
	return true, nil
}

func (e *Engine) NextAssistantTimelineItemForTurn(ctx context.Context, rt *chatpkg.Chat) (domain.TimelineItem, error) {
	return e.nextAssistantTimelineItemForTurn(ctx, "", rt)
}

func (e *Engine) MaybeUpdateChatTitle(ctx context.Context, chatID id.ID) (string, error) {
	return e.maybeUpdateChatTitle(ctx, chatID)
}

func (e *Engine) MaybeUpdateSessionTitle(ctx context.Context, session domain.Session, chat domain.Chat, client *provider.Client) (string, error) {
	return e.maybeUpdateSessionTitle(ctx, session, chat, client)
}

func (e *Engine) AutoContinueBadStopEnabled() bool {
	return e.cfg.UI.AutoContinue
}

func (e *Engine) RefreshAgents(ctx context.Context, sessionID id.ID) (domain.Session, error) {
	owner, err := e.LoadSession(ctx, sessionID)
	if err != nil {
		return domain.Session{}, err
	}
	session := owner.Snapshot().Session
	chat, err := owner.EnsureDefaultChat(ctx)
	if err != nil {
		return domain.Session{}, err
	}
	client, err := e.clientForChat(chat)
	if err != nil {
		return domain.Session{}, err
	}
	return e.refreshSessionAgents(ctx, session, chat, client)
}

func (e *Engine) refreshSessionAgents(ctx context.Context, session domain.Session, chat domain.Chat, client *provider.Client) (domain.Session, error) {
	projectRoot := sessionProjectRoot(session)
	snapshot, err := e.agents.DiscoverProject(ctx, projectRoot, projectRoot)
	if err != nil {
		return domain.Session{}, err
	}
	model, err := e.settings.Model(chat)
	if err != nil {
		return domain.Session{}, err
	}
	resolution, err := e.agents.Resolve(ctx, client, session.ID, chat.ID, model.SourceModelID, snapshot)
	if err != nil {
		return domain.Session{}, err
	}
	files := make([]domain.AgentsFile, 0, len(resolution.Snapshot.Files))
	for _, item := range resolution.Snapshot.Files {
		files = append(files, domain.AgentsFile{
			Path:         item.Path,
			Kind:         item.Kind,
			Priority:     item.Priority,
			ModTime:      item.ModTime,
			Checksum:     item.Checksum,
			Size:         item.Size,
			DiscoveredBy: item.DiscoveredBy,
		})
	}
	owner, err := e.LoadSession(ctx, session.ID)
	if err != nil {
		return domain.Session{}, err
	}
	return owner.UpdateSession(ctx, func(session *domain.Session) {
		session.ProjectChecksum = resolution.Snapshot.Checksum
		session.AgentsResolved = resolution.ResolvedAgents
		session.AgentsSummary = resolution.ConflictSummary
		session.AgentsFiles = append([]domain.AgentsFile(nil), files...)
		session.AgentsGeneratedAt = resolution.GeneratedAt
	})
}

func (e *Engine) maybeUpdateSessionTitle(ctx context.Context, session domain.Session, chat domain.Chat, client *provider.Client) (string, error) {
	now := time.Now().UTC()
	timeline, prompt, err := e.titleSummaryMessages(ctx, session.ID)
	if err != nil {
		return "", err
	}
	if !shouldRefreshSessionTitle(session, timeline) {
		return "", nil
	}
	resp, err := client.CompleteChat(ctx, e.ChatRequest(session, chat, prompt, false))
	if err != nil {
		return "", err
	}
	title := normalizeSessionTitle(resp.Text)
	if title == "" {
		return "", nil
	}
	owner, err := e.LoadSession(ctx, session.ID)
	if err != nil {
		return "", err
	}
	if _, err := owner.UpdateSession(ctx, func(session *domain.Session) {
		session.Title = title
		session.TitleUserDefined = false
		session.TitleGeneratedAt = now
		session.TitleRefreshCount = 1
	}); err != nil {
		return "", err
	}
	return title, nil
}

func shouldRefreshSessionTitle(session domain.Session, timeline []domain.TimelineItem) bool {
	if session.TitleUserDefined || session.TitleRefreshCount != 0 || hasCustomSessionTitle(session.Title) {
		return false
	}
	return hasCompletedUserAssistantExchange(timeline)
}

func (e *Engine) maybeUpdateChatTitle(ctx context.Context, chatID id.ID) (string, error) {
	if chatID == "" {
		return "", nil
	}
	chatRecord, err := e.chatByID(ctx, chatID)
	if err != nil {
		return "", err
	}
	rt, err := e.chatOwner(ctx, chatRecord.SessionID, chatID)
	if err != nil {
		return "", err
	}
	timeline, err := rt.Timeline(ctx)
	if err != nil {
		return "", err
	}
	if !shouldRefreshChatTitle(chatRecord, timeline) {
		return "", nil
	}
	title := titleFromTimeline(timeline)
	if title == "" {
		return "", nil
	}
	if _, err := rt.UpdateMetadata(ctx, chatpkg.MetadataUpdate{Title: title, GeneratedTitle: true}); err != nil {
		return "", err
	}
	return title, nil
}

func shouldRefreshChatTitle(chat domain.Chat, timeline []domain.TimelineItem) bool {
	return !chat.TitleUserDefined && isGeneratedChatTitle(chat.Title) && hasCompletedUserAssistantExchange(timeline)
}

func isGeneratedChatTitle(title string) bool {
	title = strings.TrimSpace(title)
	if title == "" || title == "Main" || title == "Chat" {
		return true
	}
	if strings.HasPrefix(title, "Chat ") {
		_, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(title, "Chat ")), 10, 64)
		return err == nil
	}
	return false
}

func titleFromTimeline(timeline []domain.TimelineItem) string {
	for _, item := range timeline {
		if user, ok := item.Content.(domain.UserMessage); ok {
			if title := normalizeSessionTitle(user.Text); title != "" {
				return title
			}
		}
	}
	for _, item := range timeline {
		role, content := timelineTitleEntry(item)
		if role != "" {
			if title := normalizeSessionTitle(content); title != "" {
				return title
			}
		}
	}
	return ""
}

func hasCompletedUserAssistantExchange(timeline []domain.TimelineItem) bool {
	var sawUser, sawAssistant bool
	for _, item := range timeline {
		switch item.Content.(type) {
		case domain.UserMessage:
			sawUser = true
		case domain.AssistantMessage:
			sawAssistant = true
		}
		if sawUser && sawAssistant {
			return true
		}
	}
	return false
}

func hasCustomSessionTitle(title string) bool {
	title = strings.TrimSpace(title)
	return title != "" && title != "New Session"
}

func (e *Engine) titleSummaryMessages(ctx context.Context, sessionID id.ID) ([]domain.TimelineItem, []provider.Message, error) {
	owner, err := e.LoadSession(ctx, sessionID)
	if err != nil {
		return nil, nil, err
	}
	chatRecord, err := owner.EnsureDefaultChat(ctx)
	if err != nil {
		return nil, nil, err
	}
	rt, err := owner.Chat(ctx, chatRecord.ID)
	if err != nil {
		return nil, nil, err
	}
	timeline, err := rt.Timeline(ctx)
	if err != nil {
		return nil, nil, err
	}
	start := max(0, len(timeline)-8)
	var transcript []string
	for _, item := range timeline[start:] {
		role, content := timelineTitleEntry(item)
		if strings.TrimSpace(role) == "" || strings.TrimSpace(content) == "" {
			continue
		}
		transcript = append(transcript, fmt.Sprintf("%s: %s", role, content))
	}
	return timeline, []provider.Message{
		{
			Role: provider.RoleSystem,
			Content: "Write a concise session title of exactly 5 or 6 words. " +
				"Return only the title text with no quotes, punctuation suffix, or explanation.",
		},
		{
			Role:    provider.RoleUser,
			Content: strings.Join(transcript, "\n\n"),
		},
	}, nil
}

func timelineTitleEntry(item domain.TimelineItem) (string, string) {
	switch content := item.Content.(type) {
	case domain.UserMessage:
		return provider.RoleUser.String(), content.Text
	case domain.AssistantMessage:
		if strings.TrimSpace(content.Text) != "" {
			return provider.RoleAssistant.String(), content.Text
		}
		return provider.RoleAssistant.String(), content.Reasoning.ReplayText()
	case domain.ToolExecution:
		text := ""
		if content.Result != nil {
			text = content.Result.Text
		}
		if content.Error != nil {
			text = content.Error.Message
		}
		return provider.RoleTool.String(), text
	case domain.Notice:
		return "notice", content.Text
	case domain.Compaction:
		return "compaction", content.Summary
	case domain.LintMessage:
		return "lint", content.Text
	default:
		return "", ""
	}
}

func normalizeSessionTitle(raw string) string {
	title := strings.TrimSpace(raw)
	title = strings.Trim(title, "\"'`")
	title = strings.Join(strings.Fields(title), " ")
	if title == "" {
		return ""
	}
	words := strings.Fields(title)
	if len(words) > 6 {
		words = words[:6]
	}
	return strings.Join(words, " ")
}

func (e *Engine) nextAssistantTimelineItemForTurn(_ context.Context, _ id.ID, rt *chatpkg.Chat) (domain.TimelineItem, error) {
	if rt == nil {
		return domain.TimelineItem{}, fmt.Errorf("chat runtime is required")
	}
	return rt.NextAssistantItem(), nil
}

func (e *Engine) buildConversationPreview(ctx context.Context, session domain.Session, chatID id.ID, prompt string, drafts []attachment.Draft, refs []reference.Draft, turnInstructions []provider.InstructionBlock) ([]provider.Message, error) {
	envelope, err := e.buildPromptEnvelopePreview(ctx, session, chatID, prompt, drafts, refs, turnInstructions)
	if err != nil {
		return nil, err
	}
	return provider.SerializePromptEnvelope(envelope), nil
}

func (e *Engine) buildPromptEnvelopePreview(ctx context.Context, session domain.Session, chatID id.ID, prompt string, drafts []attachment.Draft, refs []reference.Draft, turnInstructions []provider.InstructionBlock) (provider.PromptEnvelope, error) {
	chat := domain.Chat{WorkflowRole: chatrole.General}
	if chatID != "" {
		stored, err := e.chatByID(ctx, chatID)
		if err != nil {
			return provider.PromptEnvelope{}, err
		}
		chat = stored
	}
	var timeline []domain.TimelineItem
	if chatID != "" {
		var err error
		rt, err := e.chatOwner(ctx, chat.SessionID, chatID)
		if err != nil {
			return provider.PromptEnvelope{}, err
		}
		timeline, err = rt.Timeline(ctx)
		if err != nil {
			return provider.PromptEnvelope{}, err
		}
	}
	timeline = chatpkg.FilterQueuedTimelineItems(timeline)
	return e.BuildPromptEnvelopeForTimeline(session, chat, timeline, prompt, drafts, refs, turnInstructions)
}

func (e *Engine) compactPrompt() string {
	return modelruntime.ManagedPrompt(e.cfg.ManagedAssetsDir(), "compaction-prompt.md")
}

func (e *Engine) compactPromptWithInstructions(instructions string) string {
	prompt := e.compactPrompt()
	instructions = strings.TrimSpace(instructions)
	if instructions == "" {
		return prompt
	}
	return strings.TrimSpace(prompt + "\n\nAdditional compaction instructions:\n" + instructions)
}

func (e *Engine) autoCompactAtTurnBoundary(ctx context.Context, session domain.Session, chat domain.Chat, rt *chatpkg.Chat, client *provider.Client, messages []provider.Message, out chan<- domain.Event) (bool, error) {
	threshold := e.autoCompactThreshold()
	used, ok := e.autoCompactUsagePercent(chat, messages)
	if !ok || used < threshold {
		return false, nil
	}
	if out != nil {
		out <- domain.Event{Kind: domain.EventKindStatus, Text: fmt.Sprintf("Auto-compacting at %d%% known context used", used)}
	}
	if err := e.compactChat(ctx, session, rt, client, "auto", "", out); err != nil {
		return false, err
	}
	return true, nil
}

func (e *Engine) autoCompactThreshold() int {
	return max(1, e.settings.Snapshot().Compaction.AutoAtPercent)
}

func (e *Engine) autoCompactUsagePercent(chat domain.Chat, messages []provider.Message) (int, bool) {
	return e.knownContextUsagePercent(chat)
}

func (e *Engine) knownContextUsagePercent(chat domain.Chat) (int, bool) {
	if !chat.ContextTokensKnown {
		return 0, false
	}
	model, err := e.settings.Model(chat)
	if err != nil {
		return 0, false
	}
	return contextUsagePercent(chat.LastKnownContextTokens, model.ContextWindow)
}

func contextUsagePercent(tokens, contextWindow int) (int, bool) {
	if tokens <= 0 || contextWindow <= 0 {
		return 0, false
	}
	return min(100, (tokens*100)/contextWindow), true
}

// compactChat summarizes a chat's history in a temporary chat on the
// compaction model and records the summary as a compaction item.
//
// The temporary chat is an ordinary chat that exists only for this request:
// no ID, backend, tools, or stored data. Its history is the chat's timeline up
// to the cut point, rendered exactly as for a normal turn (earlier compaction
// summaries included), and the compaction prompt is its user message.
func (e *Engine) compactChat(ctx context.Context, session domain.Session, rt *chatpkg.Chat, client *provider.Client, trigger, instructions string, out chan<- domain.Event) error {
	if rt == nil {
		return fmt.Errorf("chat runtime is required")
	}
	chat := rt.Snapshot().Chat
	timeline, err := rt.Timeline(ctx)
	if err != nil {
		return err
	}
	timeline = chatpkg.FilterQueuedTimelineItems(timeline)
	tempChat, tempClient, err := e.compactionChat(chat, client)
	if err != nil {
		return err
	}
	req, firstKeptItemID, err := e.buildCompactionRequestForTimeline(session, tempChat, timeline, instructions, e.ProviderStreamingEnabled(tempChat))
	if err != nil || len(req.Messages) == 0 {
		return err // nothing new since the last compaction
	}

	beforeContextTokens, _ := e.EstimateContextTokensForTimeline(session, chat, timeline)
	compactionItem, err := rt.AppendCompaction(ctx, domain.Compaction{
		Trigger:             trigger,
		Status:              "pending",
		FirstKeptItemID:     firstKeptItemID,
		BeforeContextTokens: beforeContextTokens,
	})
	if err != nil {
		return err
	}
	var performance *domain.ModelPerformance
	record := func(summary, status string, afterContextTokens int) error {
		var err error
		compactionItem, err = rt.UpdateCompaction(context.WithoutCancel(ctx), compactionItem, domain.Compaction{
			Summary:             summary,
			Trigger:             trigger,
			Status:              status,
			FirstKeptItemID:     firstKeptItemID,
			BeforeContextTokens: beforeContextTokens,
			AfterContextTokens:  afterContextTokens,
			Performance:         performance,
		})
		return err
	}
	emit(out, domain.Event{Kind: domain.EventKindStatus, Text: "Compacting session...", Item: compactionItem, Meta: map[string]string{"refresh": "details", "compaction": "started"}})

	summary, perf, err := e.summarizeInTemporaryChat(ctx, session, tempChat, tempClient, req, out)
	if perf.HasAny() {
		performance = &perf
	}
	var afterContextTokens int
	if err == nil {
		afterContextTokens = e.estimateCompactedTimelineContextTokens(session, chat, timeline, compactionItem, firstKeptItemID, summary)
		err = checkCompactionSummary(summary, beforeContextTokens, afterContextTokens)
	}
	if err != nil {
		_ = record("", "failed", 0)
		return err
	}
	if err := record(summary, "completed", afterContextTokens); err != nil {
		return err
	}
	if err := rt.ResetContextAndTokenUsage(ctx); err != nil {
		return err
	}
	emit(out, domain.Event{Kind: domain.EventKindStatus, Text: "Session compacted", Item: compactionItem, Meta: map[string]string{"refresh": "details", "compaction": "completed"}})
	return nil
}

// compactionChat returns the temporary chat that runs compaction, and its
// client. The compaction settings pick the model; by default that is the
// chat's own model and client.
func (e *Engine) compactionChat(chat domain.Chat, client *provider.Client) (domain.Chat, *provider.Client, error) {
	temp := domain.Chat{SessionID: chat.SessionID, ProviderID: chat.ProviderID, ModelID: chat.ModelID}
	cfg := e.settings.Snapshot()
	if strings.TrimSpace(cfg.Compaction.ProviderID) == "" && strings.TrimSpace(cfg.Compaction.ModelID) == "" {
		return temp, client, nil
	}
	compaction, err := e.settings.Compaction(chat, e.compactPrompt())
	if err != nil {
		providerID := strings.TrimSpace(cfg.Compaction.ProviderID)
		if providerID == "" {
			providerID = strings.TrimSpace(chat.ProviderID)
		}
		return domain.Chat{}, nil, fmt.Errorf("compaction provider %q is not configured or is disabled: %w", providerID, err)
	}
	temp.ProviderID, temp.ModelID = compaction.ProviderID, compaction.ModelID
	compactionClient, err := provider.New(compaction.Model.ProviderID, compaction.Provider, e.debug, e.health)
	if err != nil {
		return domain.Chat{}, nil, fmt.Errorf("create compaction provider %q: %w", compaction.Model.ProviderID, err)
	}
	return temp, compactionClient, nil
}

// buildCompactionRequestForTimeline builds the temporary chat's request: the
// timeline up to the cut point, rendered like a normal turn (earlier
// compaction summaries included), with the compaction prompt as the user
// message. It also returns the first item kept after the summary. The request
// has no messages when nothing is new since the last compaction.
func (e *Engine) buildCompactionRequestForTimeline(session domain.Session, tempChat domain.Chat, timeline []domain.TimelineItem, instructions string, stream bool) (provider.ChatRequest, string, error) {
	segmentStart := compactionSegmentStartForNextCut(timeline, len(timeline))
	keepStart := segmentStart + modelruntime.PreservedTimelineToolCallTailStart(timeline[segmentStart:], e.CompactionKeepToolCalls())
	if keepStart <= segmentStart {
		return provider.ChatRequest{}, "", nil
	}
	envelope, err := e.BuildPromptEnvelopeForTimeline(session, tempChat, timeline[:keepStart], e.compactPromptWithInstructions(instructions), nil, nil, nil)
	if err != nil {
		return provider.ChatRequest{}, "", err
	}
	req := e.ChatRequest(session, tempChat, provider.SerializePromptEnvelope(envelope), stream)
	return req, firstKeptItemIDForCompactionCut(timeline, keepStart), nil
}

// summarizeInTemporaryChat sends the temporary chat's request and returns the
// reply. Streamed text stays out of the real chat; only progress is reported.
// If the prompt overflows the context window, the oldest message is dropped
// and the request retried.
func (e *Engine) summarizeInTemporaryChat(ctx context.Context, session domain.Session, tempChat domain.Chat, client *provider.Client, req provider.ChatRequest, out chan<- domain.Event) (string, domain.ModelPerformance, error) {
	for {
		resp, err := e.completeWithCompactionProgress(ctx, session, tempChat, client, req, out)
		if err == nil {
			// Reasoning is never a summary: replacing history with it loses
			// the history. Fail instead, which keeps the history intact.
			summary := strings.TrimSpace(resp.Text)
			if summary == "" && strings.TrimSpace(resp.RawReasoning) != "" {
				return "", resp.Performance, errors.New("compaction model ended after thinking without writing a summary")
			}
			return summary, resp.Performance, nil
		}
		if !provider.IsContextWindowExceeded(err) || !dropOldestCompactionMessage(&req) {
			return "", domain.ModelPerformance{}, err
		}
		emit(out, domain.Event{Kind: domain.EventKindStatus, Text: "Compaction prompt exceeded the context window; retrying without its oldest item", Meta: map[string]string{"compaction": "progress"}})
	}
}

// completeWithCompactionProgress sends req through the normal model request
// path and turns its stream into compaction progress for the real chat.
func (e *Engine) completeWithCompactionProgress(ctx context.Context, session domain.Session, tempChat domain.Chat, client *provider.Client, req provider.ChatRequest, out chan<- domain.Event) (chatpkg.ModelResponse, error) {
	events := make(chan domain.Event, 64)
	forwarded := make(chan struct{})
	go func() {
		defer close(forwarded)
		summaryBytes := 0
		for evt := range events {
			switch {
			case evt.Kind == domain.EventKindMessageDelta:
				summaryBytes += len(evt.Text)
				emit(out, domain.Event{Kind: domain.EventKindStatus, Text: fmt.Sprintf("Streaming compacted results (%s)", textutil.FormatBytes(summaryBytes)), Meta: map[string]string{"compaction": "streaming"}})
			case evt.Kind == domain.EventKindStatus && evt.Meta[domain.EventMetaPromptProgress] == "true":
				evt.Meta["compaction"] = "progress"
				evt.Text = compactionPromptProgressText(evt.Meta)
				emit(out, evt)
			}
		}
	}()
	resp, err := e.CompleteModelRequest(ctx, session, tempChat, client, events, req, domain.TimelineItem{})
	close(events)
	<-forwarded
	return resp, err
}

func emit(out chan<- domain.Event, evt domain.Event) {
	if out != nil {
		out <- evt
	}
}

// checkCompactionSummary rejects summaries that would damage the chat: empty,
// oversized, or failing to shrink a large context.
func checkCompactionSummary(summary string, beforeContextTokens, afterContextTokens int) error {
	switch {
	case summary == "":
		return fmt.Errorf("empty compaction summary")
	case len(summary) > compactionMaxBytes:
		return fmt.Errorf("compaction output exceeded %s", textutil.FormatBytes(compactionMaxBytes))
	case beforeContextTokens > compactionReductionCheckMinTokens && (afterContextTokens <= 0 || afterContextTokens >= beforeContextTokens):
		return fmt.Errorf("compaction did not reduce context (%d tokens before, %d after)", beforeContextTokens, afterContextTokens)
	}
	return nil
}

func firstKeptItemIDForCompactionCut(timeline []domain.TimelineItem, keepStart int) string {
	if keepStart < 0 || keepStart >= len(timeline) {
		return ""
	}
	return timeline[keepStart].ID
}

func compactionSegmentStartForNextCut(timeline []domain.TimelineItem, keepStart int) int {
	keepStart = max(0, min(keepStart, len(timeline)))
	segmentStart := 0
	for idx, item := range timeline[:keepStart] {
		compacted, ok := item.Content.(domain.Compaction)
		if !ok || strings.TrimSpace(compacted.Summary) == "" {
			continue
		}
		if !modelruntime.ValidCompactionBoundary(timeline[segmentStart:idx], compacted.FirstKeptItemID) {
			continue
		}
		segmentStart = idx + 1
	}
	return segmentStart
}

func dropOldestCompactionMessage(req *provider.ChatRequest) bool {
	if req == nil || len(req.Messages) < 2 {
		return false
	}
	last := len(req.Messages) - 1 // Preserve the final compaction instruction.
	for index := 0; index < last; index++ {
		if req.Messages[index].Role == provider.RoleSystem {
			continue
		}
		req.Messages = append(req.Messages[:index], req.Messages[index+1:]...)
		return true
	}
	return false
}

func compactionPromptProgressText(meta map[string]string) string {
	total, totalErr := strconv.Atoi(strings.TrimSpace(meta["total"]))
	processed, processedErr := strconv.Atoi(strings.TrimSpace(meta["processed"]))
	if totalErr == nil && processedErr == nil && total > 0 {
		percent := processed * 100 / total
		if percent < 0 {
			percent = 0
		}
		if percent > 100 {
			percent = 100
		}
		return fmt.Sprintf("Compaction pre-processing %d%%", percent)
	}
	return "Compaction pre-processing"
}

func (e *Engine) estimateCompactedTimelineContextTokens(session domain.Session, chat domain.Chat, timeline []domain.TimelineItem, compactionItem domain.TimelineItem, firstKeptItemID string, summary string) int {
	simulated := make([]domain.TimelineItem, 0, len(timeline)+1)
	simulated = append(simulated, timeline...)
	compactionItem.Content = domain.Compaction{Summary: summary, Status: "completed", FirstKeptItemID: firstKeptItemID}
	simulated = append(simulated, compactionItem)
	estimated, err := e.EstimateContextTokensForTimeline(session, chat, simulated)
	if err != nil || estimated <= 0 {
		return tokenestimate.Text(summary)
	}
	return estimated
}
