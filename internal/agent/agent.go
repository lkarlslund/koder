package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lkarlslund/koder/internal/agents"
	"github.com/lkarlslund/koder/internal/assets"
	"github.com/lkarlslund/koder/internal/attachment"
	chatpkg "github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/debugsrv"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/execruntime"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/mcp"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/reference"
	sessionpkg "github.com/lkarlslund/koder/internal/session"
	"github.com/lkarlslund/koder/internal/settings"
	"github.com/lkarlslund/koder/internal/skills"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tokenestimate"
	"github.com/lkarlslund/koder/internal/toolruntime"
	"github.com/lkarlslund/koder/internal/tools"
	_ "github.com/lkarlslund/koder/internal/tools/all"
)

type Engine struct {
	cfg          config.Config
	store        *store.Store
	debug        *debugsrv.Recorder
	files        *attachment.Manager
	caps         *provider.CapabilityStore
	agents       *agents.Manager
	mcp          *mcp.Manager
	settings     *settings.Store
	toolsRuntime *toolruntime.Runtime
	envMu        sync.Mutex
	envPrompts   map[id.ID]string
	caveman      *cavemanService
	cavemanMu    sync.Mutex
	cavemanJobs  map[id.ID]cavemanJob
	registry     *sessionpkg.Registry
	retryPause   func(context.Context, time.Duration, func(time.Duration)) error
}

const (
	maxRateLimitRetries       = 3
	maxTransientChatRetries   = 3
	defaultRateLimitRetryWait = 5 * time.Second
	defaultTransientRetryWait = 250 * time.Millisecond
	cavemanThinkingMaxBytes   = 4 * 1024
	cavemanThinkingMaxTokens  = 256
)

func New(cfg config.Config, st *store.Store, debug *debugsrv.Recorder, mcpManagers ...*mcp.Manager) *Engine {
	var mcpManager *mcp.Manager
	if len(mcpManagers) > 0 {
		mcpManager = mcpManagers[0]
	}
	execRuntime := execruntime.NewManager()
	settingsStore := settings.New(cfg)
	e := &Engine{
		cfg:         cfg,
		store:       st,
		debug:       debug,
		files:       attachment.NewManager(cfg.StateDir()),
		caps:        provider.NewCapabilityStore(cfg.StateDir()),
		agents:      agents.NewManager(cfg.StateDir(), filepath.Join(filepath.Dir(cfg.Path()), "AGENTS.md")),
		mcp:         mcpManager,
		settings:    settingsStore,
		caveman:     newCavemanService(cfg.Thinking.CavemanParallelism),
		cavemanJobs: map[id.ID]cavemanJob{},
		retryPause:  waitForRetry,
	}
	e.registry = sessionpkg.NewRegistry(st, e.MetadataChat, sessionRegistryConfig(settingsStore.NewSessionDefaults()))
	e.toolsRuntime = toolruntime.New(toolruntime.Config{
		Settings: settingsStore,
		Debug:    debug,
		Sessions: e.registry,
		Exec:     execRuntime,
		MCP:      e.mcp,
	})
	return e
}

func (e *Engine) UpdateConfig(cfg config.Config) {
	e.cfg = cfg
	if e.settings != nil {
		e.settings.Update(cfg)
	} else {
		e.settings = settings.New(cfg)
	}
	e.agents = agents.NewManager(cfg.StateDir(), filepath.Join(filepath.Dir(cfg.Path()), "AGENTS.md"))
	e.caveman = newCavemanService(cfg.Thinking.CavemanParallelism)
	if e.registry != nil {
		e.registry.UpdateConfig(sessionRegistryConfig(e.settings.NewSessionDefaults()))
	}
	if e.toolsRuntime != nil {
		e.toolsRuntime.UpdateSettings(e.settings)
	}
	if e.mcp != nil {
		_ = e.mcp.LoadConfig(cfg.MCPServers)
		go func() {
			_ = e.mcp.ConnectAll(context.Background())
		}()
	}
}

func (e *Engine) ListMCPServers() []mcp.ServerState {
	if e.mcp == nil {
		return nil
	}
	return e.mcp.ListServers()
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

func chatModel(chat domain.Chat) (string, string, error) {
	providerID := strings.TrimSpace(chat.ProviderID)
	modelID := strings.TrimSpace(chat.ModelID)
	if providerID == "" {
		return "", "", fmt.Errorf("chat %s has no provider", chat.ID)
	}
	if modelID == "" {
		return "", "", fmt.Errorf("chat %s has no model", chat.ID)
	}
	return providerID, modelID, nil
}

func (e *Engine) clientForChat(chat domain.Chat) (*provider.Client, error) {
	model, err := e.settings.Model(chat)
	if err != nil {
		return nil, err
	}
	return provider.New(model.SourceProviderID, model.Provider, e.debug)
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
	if err := e.compactChatRuntime(ctx, session, rt, client, "manual", instructions, out); err != nil {
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
	if err := e.validatePromptAttachments(chat, drafts); err != nil {
		return provider.ChatRequest{}, err
	}
	messages, err := e.buildConversationPreview(ctx, session, chat.ID, prompt, drafts, refs, chatpkg.TurnInstructionBlocks(note, ""))
	if err != nil {
		return provider.ChatRequest{}, err
	}
	return e.chatRequest(session, chat, messages, false), nil
}

func (e *Engine) PreparePromptTurn(ctx context.Context, rt *chatpkg.Chat, input domain.QueuedInput, prompt string, drafts []attachment.Draft, refs []reference.Draft, note string, out chan<- domain.Event) ([]provider.InstructionBlock, error) {
	if rt == nil {
		return nil, fmt.Errorf("chat runtime is required")
	}
	snapshot := rt.Snapshot()
	session := snapshot.Session
	chat := snapshot.Chat
	if err := e.validatePromptAttachments(chat, drafts); err != nil {
		return nil, err
	}
	user, err := e.userMessageForPrompt(session, prompt, drafts, refs)
	if err != nil {
		return nil, err
	}
	userItem, err := rt.AppendUserMessageForInput(ctx, input, user)
	if err != nil {
		return nil, err
	}
	out <- domain.Event{Kind: domain.EventKindStatus, Text: "User message added", Item: userItem}
	e.recordLifecycle(session.ID, "user_message_persisted", prompt, map[string]string{"item_id": userItem.ID})
	chat = rt.Snapshot().Chat
	client, err := e.clientForChat(chat)
	if err != nil {
		return nil, err
	}
	if session.ID != "" && needsSessionAgentsRefresh(session) {
		out <- domain.Event{Kind: domain.EventKindStatus, Text: "Checking project instructions..."}
	}
	session, err = e.ensureSessionAgents(ctx, session, chat, client)
	if err != nil {
		return nil, err
	}
	rt.SetSession(session)
	chat = rt.Snapshot().Chat
	e.recordLifecycle(session.ID, "prompt_started", prompt, map[string]string{"provider": chat.ProviderID, "model": chat.ModelID})
	return chatpkg.TurnInstructionBlocks(note, ""), nil
}

func (e *Engine) PrepareContinueTurn(ctx context.Context, rt *chatpkg.Chat, note string, out chan<- domain.Event) ([]provider.InstructionBlock, error) {
	if rt == nil {
		return nil, fmt.Errorf("chat runtime is required")
	}
	snapshot := rt.Snapshot()
	session := snapshot.Session
	chat := snapshot.Chat
	client, err := e.clientForChat(chat)
	if err != nil {
		return nil, err
	}
	if session.ID != "" && needsSessionAgentsRefresh(session) {
		out <- domain.Event{Kind: domain.EventKindStatus, Text: "Checking project instructions..."}
	}
	session, err = e.ensureSessionAgents(ctx, session, chat, client)
	if err != nil {
		return nil, err
	}
	rt.SetSession(session)
	if strings.TrimSpace(note) != "" {
		e.recordLifecycle(session.ID, "continue_with_note", note, nil)
	} else {
		e.recordLifecycle(session.ID, "continue", "", nil)
	}
	return chatpkg.TurnInstructionBlocks(note, "Continue from where you left off."), nil
}

func (e *Engine) MaxToolLoopSteps() int {
	return e.maxToolLoopSteps()
}

func (e *Engine) ClientForChat(chat domain.Chat) (*provider.Client, error) {
	return e.clientForChat(chat)
}

func (e *Engine) BeginModelTurn(ctx context.Context, sessionID, chatID id.ID, step int, out chan<- domain.Event) error {
	if err := e.awaitOutstandingCaveman(ctx, chatID, out); err != nil {
		return err
	}
	e.recordLifecycle(sessionID, "model_turn_started", "", map[string]string{"step": strconv.Itoa(step)})
	return nil
}

func (e *Engine) BuildConversationForTurn(ctx context.Context, rt *chatpkg.Chat, turnInstructions []provider.InstructionBlock) ([]provider.Message, error) {
	if rt == nil {
		return nil, fmt.Errorf("chat runtime is required")
	}
	snapshot := rt.Snapshot()
	return e.buildConversationForTurn(ctx, snapshot.Session, snapshot.Chat, rt, turnInstructions)
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

func (e *Engine) ProviderStreamingEnabled(chat domain.Chat) bool {
	return e.providerStreamingEnabled(chat)
}

func (e *Engine) ChatRequest(session domain.Session, chat domain.Chat, messages []provider.Message, stream bool) provider.ChatRequest {
	return e.chatRequest(session, chat, messages, stream)
}

func (e *Engine) NextAssistantTimelineItemForTurn(ctx context.Context, rt *chatpkg.Chat) (domain.TimelineItem, error) {
	return e.nextAssistantTimelineItemForTurn(ctx, "", rt)
}

func (e *Engine) CompleteModelRequest(ctx context.Context, session domain.Session, chat domain.Chat, client *provider.Client, out chan<- domain.Event, req provider.ChatRequest, assistantItem domain.TimelineItem) (chatpkg.ModelResponse, error) {
	resp, streamed, cavemanJob, err := e.chatWithRetry(ctx, session, chat, client, out, req, assistantItem)
	if err != nil {
		return chatpkg.ModelResponse{}, err
	}
	reasoning, err := e.reasoningContentForResponse(ctx, chat, client, resp.Reasoning, cavemanJob, out)
	if err != nil {
		return chatpkg.ModelResponse{}, err
	}
	return chatpkg.ModelResponse{
		Text:           resp.Text,
		RawReasoning:   resp.Reasoning,
		Reasoning:      reasoning,
		Usage:          resp.Usage,
		ToolCalls:      resp.ToolCalls,
		ToolCallErrors: resp.ToolCallErrors,
		Streamed:       streamed,
	}, nil
}

func (e *Engine) ParseProviderToolCallsForTranscript(raw []provider.ToolCall, sessionID id.ID) chatpkg.ToolCallParseResult {
	parsed := e.parseProviderToolCallsForTranscript(raw, sessionID)
	return chatpkg.ToolCallParseResult{
		Requests:  parsed.Requests,
		ToolCalls: parsed.ToolCalls,
		Err:       parsed.Err,
	}
}

func (e *Engine) FailedStreamedProviderToolCall(callErr provider.ToolCallError) domain.ToolCall {
	return e.failedStreamedProviderToolCall(callErr)
}

func (e *Engine) RecordLifecycle(sessionID id.ID, kind, text string, meta map[string]string) {
	e.recordLifecycle(sessionID, kind, text, meta)
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

func (e *Engine) ensureSessionAgents(ctx context.Context, session domain.Session, chat domain.Chat, client *provider.Client) (domain.Session, error) {
	if !needsSessionAgentsRefresh(session) {
		return session, nil
	}
	return e.refreshSessionAgents(ctx, session, chat, client)
}

func needsSessionAgentsRefresh(session domain.Session) bool {
	if strings.TrimSpace(session.ProjectChecksum) == "" {
		return true
	}
	return strings.TrimSpace(session.AgentsResolved) == "" && strings.TrimSpace(session.AgentsSummary) == ""
}

func (e *Engine) refreshSessionAgents(ctx context.Context, session domain.Session, chat domain.Chat, client *provider.Client) (domain.Session, error) {
	snapshot, err := e.agents.Discover(ctx, sessionProjectRoot(session))
	if err != nil {
		return domain.Session{}, err
	}
	_, modelID, err := chatModel(chat)
	if err != nil {
		return domain.Session{}, err
	}
	resolution, err := e.agents.Resolve(ctx, client, modelID, snapshot)
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

func (e *Engine) userMessageForPrompt(session domain.Session, prompt string, drafts []attachment.Draft, refs []reference.Draft) (domain.UserMessage, error) {
	user := domain.UserMessage{Text: prompt}
	for _, draft := range drafts {
		meta, err := e.files.AdoptDraft(draft, session.ID)
		if err != nil {
			return domain.UserMessage{}, err
		}
		user.Attachments = append(user.Attachments, domain.Attachment{
			ID: meta.ID, Name: meta.Name, MIME: meta.MIME, Path: meta.Path, Size: meta.Size, Source: meta.Source, Original: meta.Original,
		})
	}
	for _, ref := range refs {
		user.References = append(user.References, domain.Reference{
			Kind:    string(ref.Kind),
			Path:    ref.Path,
			Display: ref.Display,
			Start:   ref.Start,
			End:     ref.End,
		})
	}
	return user, nil
}

func (e *Engine) maxToolLoopSteps() int {
	if e.cfg.MaxToolLoopSteps > 0 {
		return e.cfg.MaxToolLoopSteps
	}
	return config.Default().MaxToolLoopSteps
}

func (e *Engine) maybeUpdateSessionTitle(ctx context.Context, session domain.Session, chat domain.Chat, client *provider.Client) (string, error) {
	now := time.Now().UTC()
	timeline, prompt, err := e.titleSummaryMessages(ctx, session.ID)
	if err != nil {
		return "", err
	}
	if !shouldRefreshSessionTitle(session, timeline, now) {
		return "", nil
	}
	resp, err := client.CompleteChat(ctx, e.chatRequest(session, chat, prompt, false))
	if err != nil {
		return "", err
	}
	title := normalizeSessionTitle(resp.Text)
	if title == "" {
		return "", nil
	}
	refreshCount, _ := sessionTitleRefreshState(session)
	owner, err := e.LoadSession(ctx, session.ID)
	if err != nil {
		return "", err
	}
	if _, err := owner.UpdateSession(ctx, func(session *domain.Session) {
		session.Title = title
		session.TitleGeneratedAt = now
		session.TitleRefreshCount = refreshCount + 1
	}); err != nil {
		return "", err
	}
	return title, nil
}

func (e *Engine) chatRequest(session domain.Session, chat domain.Chat, messages []provider.Message, stream bool) provider.ChatRequest {
	modelID := strings.TrimSpace(chat.ModelID)
	providerCfg := config.Provider{}
	modelCfg := config.ModelConfig{}
	if model, err := e.settings.Model(chat); err == nil {
		modelID = model.SourceModelID
		providerCfg = model.Provider
		modelCfg = model.Model
	} else {
		providerID, fallbackModelID, _ := chatModel(chat)
		_, modelID = e.cfg.ResolveModel(providerID, fallbackModelID)
		providerCfg = e.providerConfigForChat(chat)
		modelCfg = e.modelConfigForChat(chat)
	}
	extraBody := provider.RequestExtraBody(providerCfg, modelCfg)
	extraBody = provider.WithLlamaCacheAffinity(extraBody, providerCfg, session.ID, chat.ID)
	req := provider.ChatRequest{
		SessionID:          session.ID,
		ChatID:             chat.ID,
		Model:              modelID,
		Messages:           messages,
		Stream:             stream,
		ExtraBody:          extraBody,
		ToolArgumentLimits: tools.ArgumentByteLimits(),
	}
	if len(messages) > 0 && (chat.ID != "" || chat.WorkflowRole != "") {
		if e.toolsRuntime != nil {
			req.Tools = e.toolsRuntime.Definitions(session, chat)
		}
		if len(req.Tools) > 0 {
			req.ToolChoice = "auto"
		}
	}
	return req
}

func (e *Engine) providerConfigForChat(chat domain.Chat) config.Provider {
	if model, err := e.settings.Model(chat); err == nil {
		return model.Provider
	}
	providerID, modelID, _ := chatModel(chat)
	providerID, _ = e.cfg.ResolveModel(providerID, modelID)
	cfg, _ := e.cfg.Provider(providerID)
	return cfg
}

func (e *Engine) providerConfig(providerID id.ID) (config.Provider, bool) {
	return e.cfg.Provider(string(providerID))
}

func (e *Engine) promptProgressProbePending(providerID id.ID) bool {
	cfg, ok := e.providerConfig(providerID)
	return ok && provider.PromptProgressProbePending(cfg)
}

func (e *Engine) setPromptProgressSupport(providerID id.ID, supported bool) {
	id := strings.TrimSpace(string(providerID))
	if id == "" || e.cfg.Providers == nil {
		return
	}
	cfg := e.cfg
	providerCfg, ok := cfg.Providers[id]
	if !ok {
		return
	}
	if providerCfg.PromptProgressProbed && providerCfg.PromptProgressSupported == supported {
		return
	}
	providerCfg.PromptProgressMode = config.NormalizePromptProgressMode(providerCfg.PromptProgressMode)
	providerCfg.PromptProgressProbed = true
	providerCfg.PromptProgressSupported = supported
	providers := make(map[string]config.Provider, len(cfg.Providers))
	for key, value := range cfg.Providers {
		providers[key] = value
	}
	providers[id] = providerCfg
	cfg.Providers = providers
	e.cfg = cfg
	if strings.TrimSpace(cfg.Path()) == "" {
		return
	}
	if err := cfg.Save(); err != nil {
		e.recordLifecycle("", "prompt_progress_probe_save_failed", err.Error(), map[string]string{
			"provider":  id,
			"supported": strconv.FormatBool(supported),
		})
	}
}

func (e *Engine) modelPresetForChat(chat domain.Chat) string {
	return strings.TrimSpace(e.modelConfigForChat(chat).ModelPreset)
}

func (e *Engine) modelConfigForChat(chat domain.Chat) config.ModelConfig {
	if model, err := e.settings.Model(chat); err == nil {
		return model.Model
	}
	return modelConfigForRequest(e.cfg, chat.ProviderID, chat.ModelID)
}

func (e *Engine) reasoningContentForResponse(ctx context.Context, chat domain.Chat, chatClient *provider.Client, reasoning string, job cavemanJob, events chan<- domain.Event) (domain.ReasoningContent, error) {
	result := domain.ReasoningContent{Text: reasoning, Tokens: tokenestimate.Text(reasoning)}
	thinking, err := e.settings.Thinking(chat, e.cfg.Thinking.CavemanPrompt, e.preserveThinkingEnabled(chat))
	if err != nil {
		return domain.ReasoningContent{}, err
	}
	if strings.TrimSpace(reasoning) == "" || !thinking.CavemanEnabled {
		return result, nil
	}
	if !job.Valid() {
		var err error
		job, err = e.startCavemanThinking(ctx, chat, chatClient, reasoning, events)
		if err != nil {
			return domain.ReasoningContent{}, err
		}
	}
	if !job.Valid() {
		return result, nil
	}
	caveman, err := job.Await(ctx)
	e.clearOutstandingCaveman(chat.ID, job)
	if err != nil {
		return domain.ReasoningContent{}, fmt.Errorf("convert reasoning to caveman: %w", err)
	}
	result.Caveman = strings.TrimSpace(caveman)
	result.CavemanTokens = tokenestimate.Text(result.Caveman)
	return result, nil
}

func (e *Engine) startCavemanThinking(ctx context.Context, chat domain.Chat, chatClient *provider.Client, reasoning string, events chan<- domain.Event) (cavemanJob, error) {
	if !e.shouldCavemanThinking(reasoning) {
		return cavemanJob{}, nil
	}
	thinking, err := e.settings.Thinking(chat, e.cfg.Thinking.CavemanPrompt, e.preserveThinkingEnabled(chat))
	if err != nil {
		return cavemanJob{}, err
	}
	providerID := thinking.Model.ProviderID
	modelID := thinking.Model.ModelID
	client := chatClient
	chatModel, err := e.settings.Model(chat)
	if err != nil {
		return cavemanJob{}, err
	}
	chatProviderID, chatModelID := chatModel.Model.ProviderID, chatModel.Model.ModelID
	if providerID != chatProviderID || modelID != chatModelID || client == nil {
		client, err = provider.New(providerID, thinking.Provider, e.debug)
		if err != nil {
			return cavemanJob{}, err
		}
	}
	req := provider.ChatRequest{
		SessionID: chat.SessionID,
		ChatID:    chat.ID,
		Model:     modelID,
		Messages:  cavemanThinkingMessages(thinking.Prompt, reasoning),
		Stream:    true,
		ExtraBody: cavemanThinkingExtraBody(thinking.Provider, thinking.Model),
	}
	if e.caveman == nil {
		e.caveman = newCavemanService(thinking.Parallelism)
	}
	job := e.caveman.Submit(ctx, func(jobCtx context.Context) (string, error) {
		resp, err := e.completeCavemanThinking(jobCtx, providerID, client, req, events)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(resp.Text), nil
	})
	e.setOutstandingCaveman(chat.ID, job)
	return job, nil
}

func (e *Engine) setOutstandingCaveman(chatID id.ID, job cavemanJob) {
	if e == nil || chatID == "" || !job.Valid() {
		return
	}
	e.cavemanMu.Lock()
	if e.cavemanJobs == nil {
		e.cavemanJobs = map[id.ID]cavemanJob{}
	}
	e.cavemanJobs[chatID] = job
	e.cavemanMu.Unlock()
}

func (e *Engine) clearOutstandingCaveman(chatID id.ID, job cavemanJob) {
	if e == nil || chatID == "" || !job.Valid() {
		return
	}
	e.cavemanMu.Lock()
	if existing, ok := e.cavemanJobs[chatID]; ok && existing.state == job.state {
		delete(e.cavemanJobs, chatID)
	}
	e.cavemanMu.Unlock()
}

func (e *Engine) awaitOutstandingCaveman(ctx context.Context, chatID id.ID, out chan<- domain.Event) error {
	if e == nil || chatID == "" {
		return nil
	}
	e.cavemanMu.Lock()
	job := e.cavemanJobs[chatID]
	e.cavemanMu.Unlock()
	if !job.Valid() {
		return nil
	}
	if out != nil {
		out <- domain.Event{
			Kind: domain.EventKindStatus,
			Text: "Waiting for caveman thinking...",
			Meta: map[string]string{"caveman": "started"},
		}
	}
	_, err := job.Await(ctx)
	e.clearOutstandingCaveman(chatID, job)
	if err != nil {
		return fmt.Errorf("wait for outstanding caveman: %w", err)
	}
	return nil
}

func (e *Engine) shouldCavemanThinking(reasoning string) bool {
	thinking := e.settings.Snapshot().Thinking
	if strings.TrimSpace(reasoning) == "" || !thinking.CavemanEnabled {
		return false
	}
	minTokens := thinking.CavemanMinTokens
	if minTokens <= 0 {
		minTokens = config.DefaultCavemanMinTokens
	}
	return tokenestimate.Text(reasoning) >= minTokens
}

func (e *Engine) completeCavemanThinking(ctx context.Context, providerID id.ID, client *provider.Client, req provider.ChatRequest, out chan<- domain.Event) (provider.ChatResponse, error) {
	promptProgressPending := e.promptProgressProbePending(providerID) && provider.RequestsPromptProgress(req)
	stream := func(req provider.ChatRequest) (provider.ChatResponse, error) {
		streamCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		streamedBytes := 0
		limited := false
		onEvent := func(evt domain.Event) {
			switch evt.Kind {
			case domain.EventKindMessageDelta, domain.EventKindReasoning:
				streamedBytes += len(evt.Text)
				if streamedBytes > cavemanThinkingMaxBytes && !limited {
					limited = true
					if out != nil {
						out <- domain.Event{
							Kind: domain.EventKindStatus,
							Text: fmt.Sprintf("Caveman thinking exceeded %s; stopping rewrite", formatCompactionBytes(cavemanThinkingMaxBytes)),
							Meta: map[string]string{"caveman": "streaming"},
						}
					}
					cancel()
					return
				}
				if out != nil && streamedBytes > 0 {
					out <- domain.Event{
						Kind: domain.EventKindStatus,
						Text: fmt.Sprintf("Streaming caveman thinking (%s)", formatCompactionBytes(streamedBytes)),
						Meta: map[string]string{"caveman": "streaming"},
					}
				}
			case domain.EventKindStatus:
				if out == nil || evt.Meta[domain.EventMetaPromptProgress] != "true" {
					return
				}
				if evt.Meta == nil {
					evt.Meta = map[string]string{}
				}
				evt.Meta["caveman"] = "progress"
				out <- evt
			}
		}
		resp, err := client.StreamChatResponse(streamCtx, req, onEvent)
		if limited && errors.Is(err, context.Canceled) {
			resp.Reasoning = ""
			return resp, nil
		}
		return resp, err
	}
	if out != nil {
		out <- domain.Event{
			Kind: domain.EventKindStatus,
			Text: "Converting thinking to caveman...",
			Meta: map[string]string{"caveman": "started"},
		}
	}
	resp, err := stream(req)
	if err == nil {
		if promptProgressPending {
			e.setPromptProgressSupport(providerID, true)
		}
		return resp, nil
	}
	if promptProgressPending && provider.ShouldRetryWithoutPromptProgress(err) {
		e.setPromptProgressSupport(providerID, false)
		return stream(provider.WithoutPromptProgress(req))
	}
	return resp, err
}

func cavemanThinkingExtraBody(cfg config.Provider, model config.ModelConfig) map[string]any {
	body := provider.RequestExtraBody(cfg, model)
	if body == nil {
		body = map[string]any{}
	}
	body["max_tokens"] = cavemanThinkingMaxTokens
	if strings.Contains(strings.ToLower(cfg.BaseURL), "dashscope") {
		body["enable_thinking"] = false
		body["preserve_thinking"] = false
		return body
	}
	kwargs, ok := body["chat_template_kwargs"].(map[string]any)
	if !ok {
		kwargs = map[string]any{}
		body["chat_template_kwargs"] = kwargs
	}
	kwargs["enable_thinking"] = false
	kwargs["preserve_thinking"] = false
	return body
}

func cavemanThinkingMessages(prompt, reasoning string) []provider.Message {
	system := cavemanSystemPrompt(prompt)
	user := strings.TrimSpace(reasoning)
	if user != "" {
		user = "MODEL_THINKING:\n" + user
	}
	return []provider.Message{
		{Role: provider.RoleSystem, Content: system},
		{Role: provider.RoleUser, Content: user},
	}
}

func cavemanSystemPrompt(prompt string) string {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return config.DefaultCavemanThinkingPrompt
	}
	if strings.Contains(prompt, "{{thinking}}") {
		prompt = strings.ReplaceAll(prompt, "{{thinking}}", "The MODEL_THINKING payload is provided in the user message.")
	}
	return strings.TrimSpace(prompt)
}

func modelConfigForRequest(cfg config.Config, providerID, modelID string) config.ModelConfig {
	model := cfg.ModelRequestOptions(providerID, modelID)
	if strings.TrimSpace(model.ProviderID) == "" {
		model.ProviderID = strings.TrimSpace(providerID)
	}
	if strings.TrimSpace(model.ModelID) == "" {
		model.ModelID = strings.TrimSpace(modelID)
	}
	return model
}

func (e *Engine) providerStreamingEnabled(chat domain.Chat) bool {
	return e.providerConfigForChat(chat).Stream
}

func (e *Engine) preserveThinkingEnabled(chat domain.Chat) bool {
	model, err := e.settings.Model(chat)
	if err != nil {
		return false
	}
	return provider.PreserveThinkingEnabled(model.SourceModelID, strings.TrimSpace(model.Model.ModelPreset))
}

func shouldRefreshSessionTitle(session domain.Session, timeline []domain.TimelineItem, now time.Time) bool {
	refreshCount, generatedAt := sessionTitleRefreshState(session)
	if refreshCount == 0 {
		return hasCompletedUserAssistantExchange(timeline)
	}
	if refreshCount == 1 && len(timeline) > 5 {
		if generatedAt.IsZero() {
			return true
		}
		return now.Sub(generatedAt) >= time.Minute
	}
	return false
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
	if _, err := rt.UpdateMetadata(ctx, chatpkg.MetadataUpdate{Title: title}); err != nil {
		return "", err
	}
	return title, nil
}

func shouldRefreshChatTitle(chat domain.Chat, timeline []domain.TimelineItem) bool {
	return isGeneratedChatTitle(chat.Title) && hasCompletedUserAssistantExchange(timeline)
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

func sessionTitleRefreshState(session domain.Session) (int, time.Time) {
	if session.TitleRefreshCount > 0 {
		return session.TitleRefreshCount, session.TitleGeneratedAt
	}
	if hasCustomSessionTitle(session.Title) {
		generatedAt := session.TitleGeneratedAt
		if generatedAt.IsZero() {
			generatedAt = session.UpdatedAt
		}
		return 1, generatedAt
	}
	return 0, time.Time{}
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

func (e *Engine) recordLifecycle(sessionID id.ID, kind, text string, meta map[string]string) {
	if e.debug == nil {
		return
	}
	e.debug.RecordLifecycle(sessionID, kind, text, meta)
}

func waitForRetry(ctx context.Context, delay time.Duration, onTick func(time.Duration)) error {
	if delay <= 0 {
		delay = defaultRateLimitRetryWait
	}
	remaining := roundRetryDelay(delay)
	if onTick != nil {
		onTick(remaining)
	}
	if remaining <= 0 {
		return nil
	}
	deadline := time.Now().Add(delay)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			next := time.Until(deadline)
			if next <= 0 {
				if onTick != nil {
					onTick(0)
				}
				return nil
			}
			rounded := roundRetryDelay(next)
			if onTick != nil {
				onTick(rounded)
			}
		}
	}
}

func (e *Engine) chatWithRetry(ctx context.Context, session domain.Session, chat domain.Chat, client *provider.Client, out chan<- domain.Event, req provider.ChatRequest, streamItem domain.TimelineItem) (provider.ChatResponse, bool, cavemanJob, error) {
	sessionID := session.ID
	providerID := chat.ProviderID
	promptProgressPending := e.promptProgressProbePending(providerID) && provider.RequestsPromptProgress(req)
	promptProgressRetried := false
	for attempt := 0; ; attempt++ {
		if attempt > 0 {
			if err := e.awaitOutstandingCaveman(ctx, chat.ID, out); err != nil {
				return provider.ChatResponse{}, false, cavemanJob{}, err
			}
		}
		var (
			resp           provider.ChatResponse
			err            error
			streamed       bool
			receivedStream bool
			caveman        cavemanJob
			cavemanErr     error
		)
		if req.Stream {
			var reasoning strings.Builder
			reasoningSeen := false
			cavemanStarted := false
			startCaveman := func() {
				if cavemanStarted || !reasoningSeen {
					return
				}
				if !e.shouldCavemanThinking(reasoning.String()) {
					return
				}
				cavemanStarted = true
				caveman, cavemanErr = e.startCavemanThinking(ctx, chat, client, reasoning.String(), out)
			}
			resp, err = client.StreamChatResponse(ctx, req, func(evt domain.Event) {
				receivedStream = true
				switch evt.Kind {
				case domain.EventKindReasoning:
					if evt.Text != "" {
						reasoningSeen = true
						reasoning.WriteString(evt.Text)
					}
				case domain.EventKindMessageDelta, domain.EventKindToolCallDelta:
					startCaveman()
				}
				if (evt.Kind == domain.EventKindMessageDelta || evt.Kind == domain.EventKindReasoning) && evt.Item.ID == "" {
					evt.Item = streamItem
				}
				if out != nil {
					out <- evt
				}
			})
			if cavemanErr == nil {
				startCaveman()
			}
			streamed = true
		} else {
			resp, err = client.CompleteChat(ctx, req)
		}
		if cavemanErr != nil {
			return provider.ChatResponse{}, streamed, cavemanJob{}, cavemanErr
		}
		if err == nil {
			if promptProgressPending {
				e.setPromptProgressSupport(providerID, true)
			}
			return resp, streamed, caveman, nil
		}
		if promptProgressPending && !promptProgressRetried && provider.ShouldRetryWithoutPromptProgress(err) {
			promptProgressRetried = true
			e.setPromptProgressSupport(providerID, false)
			promptProgressPending = false
			req = provider.WithoutPromptProgress(req)
			if out != nil {
				out <- domain.Event{Kind: domain.EventKindStatus, Text: "Prompt progress unsupported; retrying without it..."}
			}
			continue
		}
		var apiErr *provider.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == 429 {
			if attempt >= maxRateLimitRetries {
				return provider.ChatResponse{}, streamed, cavemanJob{}, err
			}
			delay := apiErr.RetryAfter
			if delay <= 0 {
				delay = defaultRateLimitRetryWait
			}
			retryNumber := attempt + 1
			initialStatus := formatRateLimitRetryStatus(delay, retryNumber)
			e.recordLifecycle(sessionID, "rate_limit_retry", initialStatus, map[string]string{
				"retry":       strconv.Itoa(retryNumber),
				"retry_after": delay.String(),
			})
			lastRemaining := time.Duration(-1)
			if err := e.retryPause(ctx, delay, func(remaining time.Duration) {
				if remaining == lastRemaining {
					return
				}
				lastRemaining = remaining
				if out != nil {
					out <- domain.Event{Kind: domain.EventKindStatus, Text: formatRateLimitRetryStatus(remaining, retryNumber)}
				}
			}); err != nil {
				return provider.ChatResponse{}, streamed, cavemanJob{}, err
			}
			continue
		}
		if !shouldRetryTransientChatError(err, req.Stream, receivedStream) || attempt >= maxTransientChatRetries {
			return provider.ChatResponse{}, streamed, cavemanJob{}, err
		}
		delay := transientChatRetryDelay(attempt)
		retryNumber := attempt + 1
		initialStatus := formatTransientRetryStatus(delay, retryNumber)
		e.recordLifecycle(sessionID, "transport_retry", initialStatus, map[string]string{
			"retry":       strconv.Itoa(retryNumber),
			"retry_after": delay.String(),
			"error":       err.Error(),
		})
		lastRemaining := time.Duration(-1)
		if err := e.retryPause(ctx, delay, func(remaining time.Duration) {
			if remaining == lastRemaining {
				return
			}
			lastRemaining = remaining
			if out != nil {
				out <- domain.Event{Kind: domain.EventKindStatus, Text: formatTransientRetryStatus(remaining, retryNumber)}
			}
		}); err != nil {
			return provider.ChatResponse{}, streamed, cavemanJob{}, err
		}
	}
}

func (e *Engine) nextAssistantTimelineItemForTurn(_ context.Context, _ id.ID, rt *chatpkg.Chat) (domain.TimelineItem, error) {
	if rt == nil {
		return domain.TimelineItem{}, fmt.Errorf("chat runtime is required")
	}
	return rt.NextAssistantItem(), nil
}

func formatRateLimitRetryStatus(delay time.Duration, retryNumber int) string {
	delay = roundRetryDelay(delay)
	return fmt.Sprintf("Working (rate limit hit, retrying in %s, retry %d)", delay, retryNumber)
}

func formatTransientRetryStatus(delay time.Duration, retryNumber int) string {
	delay = roundRetryDelay(delay)
	return fmt.Sprintf("Working (connection dropped, retrying in %s, retry %d)", delay, retryNumber)
}

func transientChatRetryDelay(attempt int) time.Duration {
	delay := defaultTransientRetryWait
	for i := 0; i < attempt; i++ {
		delay *= 3
	}
	return delay
}

func shouldRetryTransientChatError(err error, stream bool, receivedStream bool) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if stream && receivedStream {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var apiErr *provider.APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == 502 || apiErr.StatusCode == 503 || apiErr.StatusCode == 504
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func roundRetryDelay(delay time.Duration) time.Duration {
	if delay <= 0 {
		return 0
	}
	delay = delay.Round(time.Second)
	if delay <= 0 {
		return time.Second
	}
	return delay
}

func (e *Engine) buildConversation(ctx context.Context, sessionID, chatID id.ID) ([]provider.Message, error) {
	owner, err := e.LoadSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	session := owner.Snapshot().Session
	return e.buildConversationPreview(ctx, session, chatID, "", nil, nil, nil)
}

func (e *Engine) buildConversationPreview(ctx context.Context, session domain.Session, chatID id.ID, prompt string, drafts []attachment.Draft, refs []reference.Draft, turnInstructions []provider.InstructionBlock) ([]provider.Message, error) {
	envelope, err := e.buildPromptEnvelopePreview(ctx, session, chatID, prompt, drafts, refs, turnInstructions)
	if err != nil {
		return nil, err
	}
	return provider.SerializePromptEnvelope(envelope), nil
}

func (e *Engine) buildConversationForTurn(ctx context.Context, session domain.Session, chat domain.Chat, rt *chatpkg.Chat, turnInstructions []provider.InstructionBlock) ([]provider.Message, error) {
	if rt == nil {
		return nil, fmt.Errorf("chat runtime is required")
	}
	timeline := filterQueuedTimelineItems(rt.SnapshotTimeline())
	envelope, err := e.buildPromptEnvelopeForTimeline(session, chat, timeline, "", nil, nil, nil)
	if err != nil {
		return nil, err
	}
	return provider.SerializePromptEnvelope(envelope), nil
}

func filterQueuedTimelineItems(timeline []domain.TimelineItem) []domain.TimelineItem {
	if len(timeline) == 0 {
		return timeline
	}
	out := make([]domain.TimelineItem, 0, len(timeline))
	waitingToolResult := false
	for _, item := range timeline {
		if _, ok := item.Content.(domain.UserMessage); ok && waitingToolResult {
			continue
		}
		out = append(out, item)
		if assistant, ok := item.Content.(domain.AssistantMessage); ok {
			waitingToolResult = assistantHasUnfinishedToolCall(assistant)
			continue
		}
		if _, ok := item.Content.(domain.ToolExecution); ok {
			waitingToolResult = false
		}
	}
	return out
}

func filterFutureUserMessagesAfterToolCall(timeline []domain.TimelineItem) []domain.TimelineItem {
	lastToolAssistant := -1
	for idx, item := range timeline {
		assistant, ok := item.Content.(domain.AssistantMessage)
		if !ok || len(assistant.Tools) == 0 {
			continue
		}
		lastToolAssistant = idx
	}
	if lastToolAssistant < 0 {
		return timeline
	}
	out := make([]domain.TimelineItem, 0, len(timeline))
	for idx, item := range timeline {
		if idx > lastToolAssistant {
			if _, ok := item.Content.(domain.UserMessage); ok {
				continue
			}
		}
		out = append(out, item)
	}
	return out
}

func assistantHasUnfinishedToolCall(assistant domain.AssistantMessage) bool {
	for _, call := range assistant.Tools {
		if call.Result == nil && call.Error == nil {
			return true
		}
	}
	return false
}

func (e *Engine) EstimateContextTokensForTimeline(session domain.Session, chat domain.Chat, timeline []domain.TimelineItem) (int, error) {
	envelope, err := e.buildPromptEnvelopeForTimeline(session, chat, timeline, "", nil, nil, nil)
	if err != nil {
		return 0, err
	}
	payload, err := json.Marshal(provider.SerializePromptEnvelope(envelope))
	if err != nil {
		return 0, err
	}
	if len(payload) == 0 {
		return 0, nil
	}
	return len(payload) / 4, nil
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
	timeline = filterQueuedTimelineItems(timeline)
	return e.buildPromptEnvelopeForTimeline(session, chat, timeline, prompt, drafts, refs, turnInstructions)
}

func (e *Engine) buildPromptEnvelopeForTimeline(session domain.Session, chat domain.Chat, timeline []domain.TimelineItem, prompt string, drafts []attachment.Draft, refs []reference.Draft, turnInstructions []provider.InstructionBlock) (provider.PromptEnvelope, error) {
	baseInstructions := e.baseInstructionsForChat(session, chat)
	envelope := provider.PromptEnvelope{Instructions: baseInstructions}
	segmentStart := 0
	for idx, item := range timeline {
		if compacted, ok := item.Content.(domain.Compaction); ok {
			if strings.TrimSpace(compacted.Summary) == "" {
				continue
			}
			if !validCompactionBoundary(timeline[segmentStart:idx], compacted.FirstKeptItemID) {
				continue
			}
			envelope.Instructions = baseInstructions
			envelope.Items = append(envelope.Items[:0], compactedHistoryMessage(compacted.Summary))
			if segmentStart < idx {
				preserved, err := e.timelineMessagesForCompactionTail(session, chat, timeline[segmentStart:idx], compacted.FirstKeptItemID)
				if err != nil {
					return provider.PromptEnvelope{}, err
				}
				envelope.Items = append(envelope.Items, preserved...)
			}
			segmentStart = idx + 1
			continue
		}
		messages, err := e.conversationMessagesForTimelineItem(session, chat, item, e.preserveThinkingEnabled(chat))
		if err != nil {
			return provider.PromptEnvelope{}, err
		}
		envelope.Items = appendTimelinePromptMessages(envelope.Items, item, messages...)
	}
	for _, msg := range previewTurnInstructionMessages(turnInstructions) {
		envelope.Items = append(envelope.Items, msg)
	}
	if strings.TrimSpace(prompt) != "" || len(drafts) > 0 {
		msg, ok, err := e.previewUserMessage(session, prompt, drafts, refs)
		if err != nil {
			return provider.PromptEnvelope{}, err
		}
		if ok {
			envelope.Items = append(envelope.Items, msg)
		}
	}
	return envelope, nil
}

func previewTurnInstructionMessages(blocks []provider.InstructionBlock) []provider.Message {
	var out []provider.Message
	for _, block := range blocks {
		user, ok := chatpkg.TurnInstructionUserMessage(block)
		if !ok {
			continue
		}
		out = append(out, provider.Message{Role: provider.RoleUser, Content: user.Text})
	}
	return out
}

func appendTimelinePromptMessages(items []provider.Message, item domain.TimelineItem, messages ...provider.Message) []provider.Message {
	return append(items, messages...)
}

func (e *Engine) timelineMessagesForCompactionTail(session domain.Session, chat domain.Chat, items []domain.TimelineItem, firstKeptItemID string) ([]provider.Message, error) {
	start := firstKeptTimelineIndex(items, firstKeptItemID)
	if start < 0 {
		start = preservedTimelineToolCallTailStart(items, e.compactionKeepToolCalls())
	}
	if start >= len(items) {
		return nil, nil
	}
	out := make([]provider.Message, 0, len(items)-start)
	for _, item := range items[start:] {
		messages, err := e.conversationMessagesForTimelineItem(session, chat, item, e.preserveThinkingEnabled(chat))
		if err != nil {
			return nil, err
		}
		out = appendTimelinePromptMessages(out, item, messages...)
	}
	return out, nil
}

func firstKeptTimelineIndex(items []domain.TimelineItem, firstKeptItemID string) int {
	if strings.TrimSpace(firstKeptItemID) == "" {
		return -1
	}
	for idx, item := range items {
		if item.ID == firstKeptItemID {
			return idx
		}
	}
	return -1
}

func preservedTimelineToolCallTailStart(items []domain.TimelineItem, keepCalls int) int {
	if keepCalls <= 0 || len(items) == 0 {
		return len(items)
	}
	remaining := keepCalls
	firstToolIdx := len(items)
	for idx := len(items) - 1; idx >= 0; idx-- {
		item := items[idx]
		assistant, ok := item.Content.(domain.AssistantMessage)
		if !ok || len(assistant.Tools) == 0 {
			continue
		}
		firstToolIdx = idx
		remaining -= len(assistant.Tools)
		if remaining <= 0 {
			return idx
		}
	}
	return firstToolIdx
}

func (e *Engine) conversationMessagesForTimelineItem(session domain.Session, chat domain.Chat, item domain.TimelineItem, preserveThinking bool) ([]provider.Message, error) {
	switch content := item.Content.(type) {
	case domain.UserMessage:
		parts := make([]domain.Part, 0, 1+len(content.Attachments)+len(content.References))
		if strings.TrimSpace(content.Text) != "" {
			parts = append(parts, domain.Part{Kind: domain.PartKindText, Payload: domain.TextPayload{Text: content.Text}})
		}
		for _, attachment := range content.Attachments {
			parts = append(parts, domain.Part{Kind: domain.PartKindAttachment, Payload: domain.AttachmentPayload(attachment)})
		}
		for _, ref := range content.References {
			parts = append(parts, domain.Part{Kind: domain.PartKindReference, Payload: domain.ReferencePayload(ref)})
		}
		msg, ok, err := e.userMessageWithContext(session, parts)
		if err != nil {
			return nil, err
		}
		if ok {
			if userMessageIsSteer(content) {
				msg = wrapStructuredSteerMessage(msg)
			}
			return []provider.Message{msg}, nil
		}
		if strings.TrimSpace(content.Text) == "" {
			return nil, nil
		}
		text := content.Text
		if userMessageIsSteer(content) {
			text = steerUserMessageText(text)
		}
		return []provider.Message{{Role: provider.RoleUser, Content: text}}, nil
	case domain.AssistantMessage:
		var toolCalls []provider.ToolCall
		for _, tool := range content.Tools {
			if strings.TrimSpace(string(tool.ToolCallID)) == "" {
				return nil, fmt.Errorf("assistant item %s has tool call without id", item.ID)
			}
			toolCalls = append(toolCalls, tools.ToolCall(tools.Request{
				Tool:       tool.Tool,
				ToolCallID: string(tool.ToolCallID),
				Args:       tool.Args,
			}))
		}
		textChunks := []string{}
		reasoningChunks := []string{}
		if preserveThinking && content.Reasoning.ReplayText() != "" {
			reasoningChunks = append(reasoningChunks, content.Reasoning.ReplayText())
		}
		if strings.TrimSpace(content.Text) != "" {
			textChunks = append(textChunks, content.Text)
		}
		out := []provider.Message{{
			Role:      provider.RoleAssistant,
			Content:   assistantConversationContent(textChunks, reasoningChunks, preserveThinking),
			ToolCalls: toolCalls,
		}}
		if strings.TrimSpace(out[0].Content) == "" && len(out[0].ToolCalls) == 0 {
			out = out[:0]
		}
		for _, tool := range content.Tools {
			msg, ok := e.timelineToolResultMessage(chat, tool)
			if ok {
				out = append(out, msg)
			}
		}
		return out, nil
	case domain.Compaction:
		return nil, fmt.Errorf("compaction item %s must be handled at envelope boundary", item.ID)
	case domain.ToolExecution:
		body := ""
		if content.Result != nil {
			body = strings.TrimSpace(content.Result.Text)
		}
		if content.Error != nil {
			body = strings.TrimSpace(content.Error.Message)
		}
		if body == "" {
			return nil, nil
		}
		return []provider.Message{{Role: provider.RoleUser, Content: fmt.Sprintf("%s output:\n%s", content.Tool, body)}}, nil
	case domain.Notice:
		return nil, nil
	case domain.LintMessage:
		body := strings.TrimSpace(content.Text)
		if body == "" {
			return nil, nil
		}
		return []provider.Message{{Role: provider.RoleUser, Content: "Post-edit diagnostics:\n" + body}}, nil
	default:
		return nil, fmt.Errorf("unsupported timeline item %s content %T", item.ID, item.Content)
	}
}

func userMessageIsSteer(msg domain.UserMessage) bool {
	return msg.Delivery == domain.QueuedInputDeliveryTurnBoundary
}

func steerUserMessageText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	return "User steering update:\n" +
		"<user_input>\n" + text + "\n</user_input>\n\n" +
		"Apply this update to the active turn before choosing the next action."
}

func wrapStructuredSteerMessage(msg provider.Message) provider.Message {
	if strings.TrimSpace(msg.Content) != "" {
		msg.Content = steerUserMessageText(msg.Content)
	}
	if len(msg.ContentParts) > 0 {
		wrapped := make([]provider.ContentPart, 0, len(msg.ContentParts)+2)
		wrapped = append(wrapped, provider.TextPart("User steering update:\n"))
		wrapped = append(wrapped, msg.ContentParts...)
		wrapped = append(wrapped, provider.TextPart("\n\nApply this update to the active turn before choosing the next action."))
		msg.ContentParts = wrapped
	}
	return msg
}

func (e *Engine) timelineToolResultMessage(chat domain.Chat, tool domain.ToolCall) (provider.Message, bool) {
	if tool.Result == nil && tool.Error == nil {
		return provider.Message{}, false
	}
	status := domain.ToolResultStatusOK
	text := ""
	diff := ""
	var data any
	if tool.Result != nil {
		status = tool.Result.Status
		text = tool.Result.Text
		diff = tool.Result.Diff
		data = tool.Result.Data
	}
	if tool.Error != nil {
		status = domain.ToolResultStatusError
		text = tool.Error.Message
		data = tools.ErrorStoredResult{Message: tool.Error.Message}
	}
	part := domain.Part{
		Kind: domain.PartKindToolOutput,
		Payload: domain.ToolOutputPayload{
			Tool:       tool.Tool,
			ToolCallID: string(tool.ToolCallID),
			Args:       tool.Args,
			Status:     status,
			Text:       text,
			Diff:       diff,
			Result:     data,
		},
	}
	part.Body = part.Text()
	if imageMsg, ok := e.toolImageMessage(chat, part, string(tool.ToolCallID), text); ok {
		return imageMsg, true
	}
	body := strings.TrimSpace(part.Text())
	if formatted, ok := tools.ModelTextForPart(part, diff); ok {
		body = strings.TrimSpace(formatted)
	} else if diff != "" {
		if body != "" {
			body += "\n\nDiff:\n" + diff
		} else {
			body = "Diff:\n" + diff
		}
	}
	return provider.Message{Role: provider.RoleTool, Content: body, ToolCallID: string(tool.ToolCallID)}, true
}

func (e *Engine) baseInstructionsForChat(session domain.Session, chat domain.Chat) []provider.InstructionBlock {
	environmentPrompt := e.sessionEnvironmentPrompt(session)
	instructions := []provider.InstructionBlock{{
		Kind: provider.InstructionKindBaseSystem,
		Text: e.systemPrompt(),
	}, {
		Kind: provider.InstructionKindEnvironment,
		Text: environmentPrompt,
	}}
	if roleText := strings.TrimSpace(chatrole.SystemPrompt(chat.WorkflowRole)); roleText != "" {
		instructions = append(instructions, provider.InstructionBlock{
			Kind: provider.InstructionKindProjectInstructions,
			Text: roleText,
		})
	}
	if agentsText := strings.TrimSpace(session.AgentsResolved); agentsText != "" {
		instructions = append(instructions, provider.InstructionBlock{
			Kind: provider.InstructionKindProjectInstructions,
			Text: "Resolved project AGENTS.md instructions:\n" + agentsText,
		})
	}
	if skillText := strings.TrimSpace(skills.PromptContext(sessionProjectRoot(session))); skillText != "" {
		instructions = append(instructions, provider.InstructionBlock{
			Kind: provider.InstructionKindSkills,
			Text: skillText,
		})
	}
	return instructions
}

func compactedHistoryMessage(summary string) provider.Message {
	return provider.Message{
		Role: provider.RoleUser,
		Content: strings.TrimSpace(
			"Compacted session summary for continuation:\n" +
				summary +
				"\n\nUse this summary as replacement history for the earlier conversation. Continue the task from the preserved context instead of restarting.",
		),
	}
}

func (e *Engine) previewUserMessage(session domain.Session, prompt string, drafts []attachment.Draft, refs []reference.Draft) (provider.Message, bool, error) {
	parts := make([]domain.Part, 0, len(drafts)+len(refs)+1)
	if strings.TrimSpace(prompt) != "" {
		parts = append(parts, domain.Part{Kind: domain.PartKindText, Payload: domain.TextPayload{Text: prompt}})
	}
	for _, draft := range drafts {
		parts = append(parts, domain.Part{
			Kind: domain.PartKindAttachment,
			Payload: domain.AttachmentPayload{
				ID: draft.ID, Name: draft.Name, MIME: draft.MIME, Path: draft.Path, Size: draft.Size, Source: draft.Source, Original: draft.Original,
			},
		})
	}
	for _, ref := range refs {
		parts = append(parts, domain.Part{
			Kind: domain.PartKindReference,
			Payload: domain.ReferencePayload{
				Kind: string(ref.Kind), Path: ref.Path, Display: ref.Display, Start: ref.Start, End: ref.End,
			},
		})
	}
	if msg, ok, err := e.userMessageWithContext(session, parts); ok || err != nil {
		return msg, ok, err
	}
	if len(parts) == 0 {
		return provider.Message{}, false, nil
	}
	return provider.Message{
		Role:    provider.RoleUser,
		Content: strings.TrimSpace(prompt),
	}, true, nil
}

func (e *Engine) userMessageWithContext(session domain.Session, parts []domain.Part) (provider.Message, bool, error) {
	contentParts := make([]provider.ContentPart, 0, len(parts)+1)
	imageParts := make([]provider.ContentPart, 0, len(parts))
	attachmentTextParts := make([]provider.ContentPart, 0, len(parts))
	var prompt string
	var refs []reference.Metadata
	var hasStructured bool
	for _, part := range parts {
		switch part.Kind {
		case domain.PartKindText:
			if text := strings.TrimSpace(part.Text()); text != "" {
				prompt = part.Text()
			}
		case domain.PartKindReference:
			hasStructured = true
			if payload, ok := part.Payload.(domain.ReferencePayload); ok {
				refs = append(refs, reference.Metadata{
					Kind: reference.Kind(payload.Kind), Path: payload.Path, Display: payload.Display, Start: payload.Start, End: payload.End,
				})
			}
		case domain.PartKindAttachment:
			hasStructured = true
			payload, ok := part.Payload.(domain.AttachmentPayload)
			if !ok {
				return provider.Message{}, false, fmt.Errorf("attachment part has %T payload", part.Payload)
			}
			meta := attachment.Metadata{
				ID: payload.ID, Name: payload.Name, MIME: payload.MIME, Path: payload.Path, Size: payload.Size, Source: payload.Source, Original: payload.Original,
			}
			switch attachment.ClassifyMIME(meta.MIME) {
			case attachment.KindText:
				body, err := e.files.ReadText(meta)
				if err != nil {
					return provider.Message{}, false, err
				}
				attachmentTextParts = append(attachmentTextParts, provider.TextPart("Attached file "+meta.Name+":\n"+body))
			case attachment.KindImage:
				data, err := e.files.ReadBytes(meta)
				if err != nil {
					return provider.Message{}, false, err
				}
				imageParts = append(imageParts, provider.ImagePart(meta.MIME, data))
			default:
				return provider.Message{}, false, fmt.Errorf("unsupported attachment in conversation: %s", meta.MIME)
			}
		}
	}
	contentParts = append(contentParts, imageParts...)
	if len(refs) > 0 {
		slices.SortFunc(refs, func(a, b reference.Metadata) int {
			if a.Start != b.Start {
				return a.Start - b.Start
			}
			if a.End != b.End {
				return a.End - b.End
			}
			return strings.Compare(a.Path, b.Path)
		})
		cursor := 0
		for _, ref := range refs {
			start := max(0, min(ref.Start, len(prompt)))
			end := max(start, min(ref.End, len(prompt)))
			if start > cursor {
				contentParts = append(contentParts, provider.TextPart(prompt[cursor:start]))
			}
			resolved, err := e.resolveReference(session, ref)
			if err != nil {
				return provider.Message{}, false, err
			}
			contentParts = append(contentParts, provider.TextPart(resolved))
			cursor = end
		}
		if cursor < len(prompt) {
			contentParts = append(contentParts, provider.TextPart(prompt[cursor:]))
		}
	} else if strings.TrimSpace(prompt) != "" {
		contentParts = append(contentParts, provider.TextPart(prompt))
	}
	contentParts = append(contentParts, attachmentTextParts...)
	if !hasStructured {
		return provider.Message{}, false, nil
	}
	message := provider.Message{Role: provider.RoleUser, ContentParts: contentParts}
	if len(contentParts) == 0 && strings.TrimSpace(prompt) != "" {
		message.Content = prompt
	}
	return message, true, nil
}

func (e *Engine) resolveReference(session domain.Session, meta reference.Metadata) (string, error) {
	root := sessionProjectRoot(session)
	switch meta.Kind {
	case reference.KindFile:
		return reference.ResolveFile(root, meta)
	case reference.KindDirectory:
		return reference.ResolveDirectory(root, meta)
	default:
		return "", fmt.Errorf("unsupported reference kind %q", meta.Kind)
	}
}

func (e *Engine) toolImageMessage(chat domain.Chat, part domain.Part, toolCallID string, body string) (provider.Message, bool) {
	stored, ok := tools.ViewImageStoredResultForPart(part)
	if !ok {
		return provider.Message{}, false
	}
	if !e.chatSupportsImageAttachments(chat) {
		return provider.Message{}, false
	}
	sourcePath := strings.TrimSpace(stored.SourcePath)
	mimeType := strings.TrimSpace(stored.MIMEType)
	if sourcePath == "" || mimeType == "" {
		return provider.Message{}, false
	}
	data, err := os.ReadFile(sourcePath)
	if err != nil || len(data) == 0 {
		return provider.Message{}, false
	}
	contentParts := make([]provider.ContentPart, 0, 2)
	if strings.TrimSpace(body) != "" {
		contentParts = append(contentParts, provider.TextPart(body))
	}
	contentParts = append(contentParts, provider.ImagePart(mimeType, data))
	return provider.Message{
		Role:         provider.RoleTool,
		ContentParts: contentParts,
		ToolCallID:   toolCallID,
	}, true
}

func assistantConversationContent(textChunks, reasoningChunks []string, preserveThinking bool) string {
	body := strings.TrimSpace(strings.Join(textChunks, "\n\n"))
	if !preserveThinking || len(reasoningChunks) == 0 {
		return body
	}
	thinking := formatThinkingBlock(strings.Join(reasoningChunks, "\n\n"))
	if body == "" {
		return thinking
	}
	return thinking + "\n\n" + body
}

func formatThinkingBlock(reasoning string) string {
	reasoning = strings.TrimSpace(reasoning)
	if reasoning == "" {
		return ""
	}
	return "<think>\n" + reasoning + "\n</think>"
}

func (e *Engine) validatePromptAttachments(chat domain.Chat, drafts []attachment.Draft) error {
	for _, draft := range drafts {
		kind := attachment.ClassifyMIME(draft.MIME)
		switch kind {
		case attachment.KindText:
			continue
		case attachment.KindImage, attachment.KindPDF:
			supported, err := e.caps.SupportsAttachment(chat.ProviderID, providerCfgForChat(e.cfg, chat), chat.ModelID, kind)
			if err != nil {
				return err
			}
			if supported {
				continue
			}
			return fmt.Errorf("provider %s model %s does not support %s attachments", chat.ProviderID, chat.ModelID, kind)
		default:
			return fmt.Errorf("unsupported attachment type %q", draft.MIME)
		}
	}
	return nil
}

func (e *Engine) chatSupportsImageAttachments(chat domain.Chat) bool {
	supported, err := e.caps.SupportsAttachment(chat.ProviderID, providerCfgForChat(e.cfg, chat), chat.ModelID, attachment.KindImage)
	return err == nil && supported
}

func providerCfgForChat(cfg config.Config, chat domain.Chat) config.Provider {
	if providerCfg, ok := cfg.Provider(chat.ProviderID); ok {
		return providerCfg
	}
	return config.Provider{}
}

func (e *Engine) compactionKeepToolCalls() int {
	return config.NormalizeCompactionKeepToolCalls(e.settings.Snapshot().Compaction.KeepToolCalls)
}

func (e *Engine) systemPrompt() string {
	return managedPrompt("system-prompt.md")
}

func systemPrompt() string {
	return managedPrompt("system-prompt.md")
}

func managedAssetRoot() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, ".koder")
}

func (e *Engine) compactPrompt() string {
	return managedPrompt("compaction-prompt.md")
}

func (e *Engine) compactPromptWithInstructions(instructions string) string {
	prompt := e.compactPrompt()
	instructions = strings.TrimSpace(instructions)
	if instructions == "" {
		return prompt
	}
	return strings.TrimSpace(prompt + "\n\nAdditional compaction instructions:\n" + instructions)
}

func managedPrompt(name string) string {
	if root := managedAssetRoot(); root != "" {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err == nil {
			return strings.TrimSpace(string(data))
		}
	}
	data, err := assets.DefaultContent(name)
	if err != nil {
		panic(err)
	}
	return strings.TrimSpace(string(data))
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
	if err := e.compactTurnSession(ctx, session, chat, rt, client, "auto", "", out); err != nil {
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

func (e *Engine) compactChatRuntime(ctx context.Context, session domain.Session, rt *chatpkg.Chat, client *provider.Client, trigger, instructions string, out chan<- domain.Event) error {
	if rt == nil {
		return fmt.Errorf("chat is required")
	}
	chat := rt.Snapshot().Chat
	compactionChat, compactionClient, err := e.compactionSessionClient(chat, client)
	if err != nil {
		return err
	}

	timeline, err := rt.Timeline(ctx)
	if err != nil {
		return err
	}
	req, firstKeptItemID, err := e.buildCompactionRequestForTimeline(session, compactionChat, timeline, instructions, e.providerStreamingEnabled(compactionChat))
	if err != nil {
		return err
	}
	if len(req.Messages) <= 1 {
		return nil
	}
	beforeContextTokens := e.estimateContextTokensForTimeline(session, chat, timeline)
	compactionItem, err := rt.AppendCompaction(ctx, domain.Compaction{
		Trigger:             trigger,
		Status:              "pending",
		FirstKeptItemID:     firstKeptItemID,
		BeforeContextTokens: beforeContextTokens,
	})
	if err != nil {
		return err
	}
	updateCompactionState := func(summary, status string, afterContextTokens int) error {
		next := domain.Compaction{
			Summary:             summary,
			Trigger:             trigger,
			Status:              status,
			FirstKeptItemID:     firstKeptItemID,
			BeforeContextTokens: beforeContextTokens,
			AfterContextTokens:  afterContextTokens,
		}
		var err error
		compactionItem, err = rt.UpdateCompaction(ctx, compactionItem, next)
		return err
	}
	if out != nil {
		out <- domain.Event{
			Kind: domain.EventKindStatus,
			Text: "Compacting session...",
			Item: compactionItem,
			Meta: map[string]string{"refresh": "details", "compaction": "started"},
		}
	}
	resp, err := e.completeCompactionChat(ctx, compactionChat, compactionClient, req, out)
	if err != nil {
		_ = updateCompactionState("", "failed", 0)
		return err
	}
	summary := strings.TrimSpace(resp.Text)
	if summary == "" {
		summary = strings.TrimSpace(resp.Reasoning)
	}
	if summary == "" {
		_ = updateCompactionState("", "failed", 0)
		return fmt.Errorf("empty compaction summary")
	}
	afterContextTokens := e.estimateCompactedTimelineContextTokens(session, chat, timeline, compactionItem, firstKeptItemID, summary)
	if err := updateCompactionState(summary, "completed", afterContextTokens); err != nil {
		return err
	}
	if err := rt.ResetContextAndTokenUsage(ctx); err != nil {
		return err
	}
	if out != nil {
		completed := compactionItem
		completed.Content = domain.Compaction{
			Summary:             summary,
			Trigger:             trigger,
			Status:              "completed",
			FirstKeptItemID:     firstKeptItemID,
			BeforeContextTokens: beforeContextTokens,
			AfterContextTokens:  afterContextTokens,
		}
		completed.Seal(time.Now().UTC())
		out <- domain.Event{
			Kind: domain.EventKindStatus,
			Text: "Session compacted",
			Item: completed,
			Meta: map[string]string{"refresh": "details", "compaction": "completed"},
		}
	}
	return nil
}

func (e *Engine) compactTurnSession(ctx context.Context, session domain.Session, chat domain.Chat, rt *chatpkg.Chat, client *provider.Client, trigger, instructions string, out chan<- domain.Event) error {
	if rt == nil {
		return fmt.Errorf("chat runtime is required")
	}
	compactionChat, compactionClient, err := e.compactionSessionClient(chat, client)
	if err != nil {
		return err
	}

	timeline := rt.SnapshotTimeline()
	req, firstKeptItemID, err := e.buildCompactionRequestForTimeline(session, compactionChat, timeline, instructions, e.providerStreamingEnabled(compactionChat))
	if err != nil {
		return err
	}
	if len(req.Messages) <= 1 {
		return nil
	}
	beforeContextTokens := e.estimateContextTokensForTimeline(session, chat, timeline)
	compactionItem, err := rt.AppendCompaction(ctx, domain.Compaction{
		Trigger:             trigger,
		Status:              "pending",
		FirstKeptItemID:     firstKeptItemID,
		BeforeContextTokens: beforeContextTokens,
	})
	if err != nil {
		return err
	}
	updateCompactionState := func(summary, status string, afterContextTokens int) error {
		next := domain.Compaction{
			Summary:             summary,
			Trigger:             trigger,
			Status:              status,
			FirstKeptItemID:     firstKeptItemID,
			BeforeContextTokens: beforeContextTokens,
			AfterContextTokens:  afterContextTokens,
		}
		var updateErr error
		compactionItem, updateErr = rt.UpdateCompaction(ctx, compactionItem, next)
		return updateErr
	}
	if out != nil {
		out <- domain.Event{
			Kind: domain.EventKindStatus,
			Text: "Compacting session...",
			Item: compactionItem,
			Meta: map[string]string{"refresh": "details", "compaction": "started"},
		}
	}
	resp, err := e.completeCompactionChat(ctx, compactionChat, compactionClient, req, out)
	if err != nil {
		_ = updateCompactionState("", "failed", 0)
		return err
	}
	summary := strings.TrimSpace(resp.Text)
	if summary == "" {
		summary = strings.TrimSpace(resp.Reasoning)
	}
	if summary == "" {
		_ = updateCompactionState("", "failed", 0)
		return fmt.Errorf("empty compaction summary")
	}
	afterContextTokens := e.estimateCompactedTimelineContextTokens(session, chat, timeline, compactionItem, firstKeptItemID, summary)
	if err := updateCompactionState(summary, "completed", afterContextTokens); err != nil {
		return err
	}
	if err := rt.ResetContextAndTokenUsage(ctx); err != nil {
		return err
	}
	if out != nil {
		out <- domain.Event{
			Kind: domain.EventKindStatus,
			Text: "Session compacted",
			Item: compactionItem,
			Meta: map[string]string{"refresh": "details", "compaction": "completed"},
		}
	}
	return nil
}

func (e *Engine) compactionSessionClient(chat domain.Chat, client *provider.Client) (domain.Chat, *provider.Client, error) {
	next := chat
	next.WorkflowRole = chatrole.Compaction
	cfg := e.settings.Snapshot()
	if strings.TrimSpace(cfg.Compaction.ProviderID) == "" && strings.TrimSpace(cfg.Compaction.ModelID) == "" {
		return next, client, nil
	}
	compaction, err := e.settings.Compaction(chat, e.compactPrompt())
	if err != nil {
		providerID := strings.TrimSpace(cfg.Compaction.ProviderID)
		if providerID == "" {
			providerID = strings.TrimSpace(chat.ProviderID)
		}
		return domain.Chat{}, nil, fmt.Errorf("compaction provider %q is not configured or is disabled: %w", providerID, err)
	}
	if compaction.Provider.Disabled {
		return domain.Chat{}, nil, fmt.Errorf("compaction provider %q is disabled", compaction.Model.ProviderID)
	}
	next.ProviderID = compaction.ProviderID
	next.ModelID = compaction.ModelID
	compactionClient, err := provider.New(compaction.Model.ProviderID, compaction.Provider, e.debug)
	if err != nil {
		return domain.Chat{}, nil, fmt.Errorf("create compaction provider %q: %w", compaction.Model.ProviderID, err)
	}
	return next, compactionClient, nil
}

func (e *Engine) buildCompactionRequestForTimeline(session domain.Session, chat domain.Chat, timeline []domain.TimelineItem, instructions string, stream bool) (provider.ChatRequest, string, error) {
	keepStart := preservedTimelineToolCallTailStart(timeline, e.compactionKeepToolCalls())
	base := compactionBaseForNextCut(timeline, len(timeline))
	keepStart = max(keepStart, base.MinKeepStart)
	messages, firstKeptItemID, err := e.buildCompactionConversationForTimelinePrefix(session, chat, timeline, keepStart, base)
	if err != nil {
		return provider.ChatRequest{}, "", err
	}
	req := e.compactionChatRequest(session, chat, messages, instructions, stream)
	if e.compactionRequestWithinContext(chat, req) {
		return req, firstKeptItemID, nil
	}
	fittedReq, fittedFirstKeptItemID, ok, err := e.fitCompactionRequestToContext(session, chat, timeline, instructions, stream, base, keepStart)
	if err != nil {
		return provider.ChatRequest{}, "", err
	}
	if ok {
		return fittedReq, fittedFirstKeptItemID, nil
	}
	return req, firstKeptItemID, nil
}

func (e *Engine) compactionChatRequest(session domain.Session, chat domain.Chat, messages []provider.Message, instructions string, stream bool) provider.ChatRequest {
	return e.chatRequest(session, chat, append(messages, provider.Message{
		Role:    provider.RoleUser,
		Content: e.compactPromptWithInstructions(instructions),
	}), stream)
}

func (e *Engine) fitCompactionRequestToContext(session domain.Session, chat domain.Chat, timeline []domain.TimelineItem, instructions string, stream bool, base compactionCutBase, keepStart int) (provider.ChatRequest, string, bool, error) {
	if e.compactionRequestTokenBudget(chat) <= 0 {
		return provider.ChatRequest{}, "", false, nil
	}
	low := max(0, min(base.MinKeepStart, keepStart))
	high := max(0, min(keepStart, len(timeline)))
	var bestReq provider.ChatRequest
	var bestFirstKeptItemID string
	var bestFound bool
	for low <= high {
		mid := low + (high-low)/2
		messages, firstKeptItemID, err := e.buildCompactionConversationForTimelinePrefix(session, chat, timeline, mid, base)
		if err != nil {
			return provider.ChatRequest{}, "", false, err
		}
		req := e.compactionChatRequest(session, chat, messages, instructions, stream)
		if e.compactionRequestWithinContext(chat, req) {
			bestReq = req
			bestFirstKeptItemID = firstKeptItemID
			bestFound = true
			low = mid + 1
			continue
		}
		high = mid - 1
	}
	return bestReq, bestFirstKeptItemID, bestFound, nil
}

func (e *Engine) compactionRequestWithinContext(chat domain.Chat, req provider.ChatRequest) bool {
	budget := e.compactionRequestTokenBudget(chat)
	if budget <= 0 {
		return true
	}
	return estimatedRequestTokens(req) <= budget
}

func (e *Engine) compactionRequestTokenBudget(chat domain.Chat) int {
	model, err := e.settings.Model(chat)
	contextWindow := 0
	if err == nil {
		contextWindow = model.ContextWindow
	} else {
		contextWindow = e.cfg.ContextWindow(chat.ProviderID, chat.ModelID)
	}
	if contextWindow <= 0 {
		return 0
	}
	reserve := max(512, min(8192, contextWindow/50))
	if contextWindow <= reserve {
		return max(1, contextWindow)
	}
	return contextWindow - reserve
}

func estimatedRequestTokens(req provider.ChatRequest) int {
	data, err := json.Marshal(req)
	if err != nil || len(data) == 0 {
		return 0
	}
	return (len(data) + 2) / 3
}

func (e *Engine) buildCompactionConversationForTimeline(session domain.Session, chat domain.Chat, timeline []domain.TimelineItem) ([]provider.Message, string, error) {
	keepStart := preservedTimelineToolCallTailStart(timeline, e.compactionKeepToolCalls())
	base := compactionBaseForNextCut(timeline, len(timeline))
	keepStart = max(keepStart, base.MinKeepStart)
	return e.buildCompactionConversationForTimelinePrefix(session, chat, timeline, keepStart, base)
}

func (e *Engine) buildCompactionConversationForTimelinePrefix(session domain.Session, chat domain.Chat, timeline []domain.TimelineItem, keepStart int, base compactionCutBase) ([]provider.Message, string, error) {
	keepStart = max(0, min(keepStart, len(timeline)))
	if base.Start > keepStart {
		base.Start = keepStart
	}
	head := timeline[:keepStart]
	firstKeptItemID := firstKeptItemIDForCompactionCut(timeline, keepStart)
	var envelope provider.PromptEnvelope
	var err error
	if strings.TrimSpace(base.Summary) != "" {
		envelope, err = e.buildCompactionPromptEnvelopeForTimelineRange(session, chat, timeline[base.Start:keepStart], base.Summary)
	} else {
		envelope, err = e.buildCompactionPromptEnvelopeForTimeline(session, chat, head)
	}
	if err != nil {
		return nil, "", err
	}
	return provider.SerializePromptEnvelope(envelope), firstKeptItemID, nil
}

type compactionCutBase struct {
	Start        int
	MinKeepStart int
	Summary      string
}

func compactionBaseForNextCut(timeline []domain.TimelineItem, keepStart int) compactionCutBase {
	keepStart = max(0, min(keepStart, len(timeline)))
	segmentStart := compactionSegmentStartForNextCut(timeline, keepStart)
	base := compactionCutBase{Start: segmentStart, MinKeepStart: segmentStart}
	for idx, item := range timeline {
		compacted, ok := item.Content.(domain.Compaction)
		if !ok || strings.TrimSpace(compacted.Summary) == "" {
			continue
		}
		firstKeptIdx := firstKeptTimelineIndex(timeline, compacted.FirstKeptItemID)
		if firstKeptIdx < 0 || firstKeptIdx >= segmentStart || firstKeptIdx >= idx {
			continue
		}
		if !validCompactionBoundary(timeline[:idx], compacted.FirstKeptItemID) {
			continue
		}
		base.Start = firstKeptIdx
		base.Summary = compacted.Summary
	}
	return base
}

func firstKeptItemIDForCompactionCut(timeline []domain.TimelineItem, keepStart int) string {
	if keepStart < 0 || keepStart >= len(timeline) {
		return ""
	}
	return timeline[keepStart].ID
}

func (e *Engine) buildCompactionPromptEnvelopeForTimeline(session domain.Session, chat domain.Chat, timeline []domain.TimelineItem) (provider.PromptEnvelope, error) {
	envelope := provider.PromptEnvelope{Instructions: e.baseInstructionsForChat(session, chat)}
	segmentStart := 0
	for idx, item := range timeline {
		if compacted, ok := item.Content.(domain.Compaction); ok {
			if strings.TrimSpace(compacted.Summary) == "" {
				continue
			}
			if !validCompactionBoundary(timeline[segmentStart:idx], compacted.FirstKeptItemID) {
				continue
			}
			envelope.Items = append(envelope.Items[:0], compactedHistoryMessage(compacted.Summary))
			if segmentStart < idx {
				preserved, err := e.compactionMessagesForCompactionTail(session, timeline[segmentStart:idx], compacted.FirstKeptItemID, e.preserveThinkingEnabled(chat))
				if err != nil {
					return provider.PromptEnvelope{}, err
				}
				envelope.Items = append(envelope.Items, preserved...)
			}
			segmentStart = idx + 1
			continue
		}
		messages, err := e.compactionMessagesForTimelineItem(session, item, e.preserveThinkingEnabled(chat))
		if err != nil {
			return provider.PromptEnvelope{}, err
		}
		envelope.Items = append(envelope.Items, messages...)
	}
	return envelope, nil
}

func (e *Engine) buildCompactionPromptEnvelopeForTimelineRange(session domain.Session, chat domain.Chat, timeline []domain.TimelineItem, baseSummary string) (provider.PromptEnvelope, error) {
	envelope := provider.PromptEnvelope{
		Instructions: e.baseInstructionsForChat(session, chat),
		Items:        []provider.Message{compactedHistoryMessage(baseSummary)},
	}
	for _, item := range timeline {
		if _, ok := item.Content.(domain.Compaction); ok {
			continue
		}
		messages, err := e.compactionMessagesForTimelineItem(session, item, e.preserveThinkingEnabled(chat))
		if err != nil {
			return provider.PromptEnvelope{}, err
		}
		envelope.Items = append(envelope.Items, messages...)
	}
	return envelope, nil
}

func validCompactionBoundary(items []domain.TimelineItem, firstKeptItemID string) bool {
	if strings.TrimSpace(firstKeptItemID) == "" {
		return true
	}
	return firstKeptTimelineIndex(items, firstKeptItemID) >= 0
}

func compactionSegmentStartForNextCut(timeline []domain.TimelineItem, keepStart int) int {
	keepStart = max(0, min(keepStart, len(timeline)))
	segmentStart := 0
	for idx, item := range timeline[:keepStart] {
		compacted, ok := item.Content.(domain.Compaction)
		if !ok || strings.TrimSpace(compacted.Summary) == "" {
			continue
		}
		if !validCompactionBoundary(timeline[segmentStart:idx], compacted.FirstKeptItemID) {
			continue
		}
		segmentStart = idx + 1
	}
	return segmentStart
}

func (e *Engine) compactionMessagesForCompactionTail(session domain.Session, items []domain.TimelineItem, firstKeptItemID string, preserveThinking bool) ([]provider.Message, error) {
	start := firstKeptTimelineIndex(items, firstKeptItemID)
	if start < 0 {
		start = preservedTimelineToolCallTailStart(items, e.compactionKeepToolCalls())
	}
	if start >= len(items) {
		return nil, nil
	}
	out := make([]provider.Message, 0, len(items)-start)
	for _, item := range items[start:] {
		messages, err := e.compactionMessagesForTimelineItem(session, item, preserveThinking)
		if err != nil {
			return nil, err
		}
		out = append(out, messages...)
	}
	return out, nil
}

func (e *Engine) compactionMessagesForTimelineItem(session domain.Session, item domain.TimelineItem, preserveThinking bool) ([]provider.Message, error) {
	switch content := item.Content.(type) {
	case domain.UserMessage:
		body := e.compactionUserMessageText(session, content)
		if body == "" {
			return nil, nil
		}
		return []provider.Message{{Role: provider.RoleUser, Content: body}}, nil
	case domain.AssistantMessage:
		body := compactAssistantMessageText(content, preserveThinking)
		if body == "" {
			return nil, nil
		}
		out := []provider.Message{{Role: provider.RoleAssistant, Content: body}}
		for _, tool := range content.Tools {
			msg, ok := e.compactionToolResultMessage(tool)
			if ok {
				out = append(out, msg)
			}
		}
		return out, nil
	case domain.Compaction:
		if strings.TrimSpace(content.Summary) == "" {
			return nil, nil
		}
		return []provider.Message{compactedHistoryMessage(content.Summary)}, nil
	case domain.ToolExecution:
		body := ""
		if content.Result != nil {
			body = strings.TrimSpace(content.Result.Text)
		}
		if content.Error != nil {
			body = strings.TrimSpace(content.Error.Message)
		}
		if body == "" {
			return nil, nil
		}
		return []provider.Message{{Role: provider.RoleUser, Content: compactTextForCompaction(content.Tool.String()+" output:\n"+body, "tool execution")}}, nil
	case domain.Notice:
		return nil, nil
	case domain.LintMessage:
		body := strings.TrimSpace(content.Text)
		if body == "" {
			return nil, nil
		}
		return []provider.Message{{Role: provider.RoleUser, Content: compactTextForCompaction("Post-edit diagnostics:\n"+body, "lint diagnostics")}}, nil
	default:
		return nil, fmt.Errorf("unsupported timeline item %s content %T", item.ID, item.Content)
	}
}

func (e *Engine) compactionUserMessageText(session domain.Session, msg domain.UserMessage) string {
	blocks := make([]string, 0, 1+len(msg.Attachments)+len(msg.References))
	if text := strings.TrimSpace(msg.Text); text != "" {
		blocks = append(blocks, text)
	}
	for _, ref := range msg.References {
		meta := reference.Metadata{
			Kind:    reference.Kind(ref.Kind),
			Path:    ref.Path,
			Display: ref.Display,
			Start:   ref.Start,
			End:     ref.End,
		}
		resolved, err := e.resolveReference(session, meta)
		label := strings.TrimSpace(ref.Display)
		if label == "" {
			label = strings.TrimSpace(ref.Path)
		}
		if err != nil {
			blocks = append(blocks, fmt.Sprintf("Reference omitted for text-only compaction: %s (read failed: %v)", label, err))
			continue
		}
		if label == "" {
			label = "reference"
		}
		blocks = append(blocks, "Referenced "+label+":\n"+compactTextForCompaction(resolved, "reference"))
	}
	for _, item := range msg.Attachments {
		meta := attachment.Metadata{
			ID: item.ID, Name: item.Name, MIME: item.MIME, Path: item.Path, Size: item.Size, Source: item.Source, Original: item.Original,
		}
		name := strings.TrimSpace(meta.Name)
		if name == "" {
			name = strings.TrimSpace(meta.Path)
		}
		if name == "" {
			name = "attachment"
		}
		switch attachment.ClassifyMIME(meta.MIME) {
		case attachment.KindText:
			body, err := e.files.ReadText(meta)
			if err != nil {
				blocks = append(blocks, fmt.Sprintf("Attachment omitted for text-only compaction: %s (read failed: %v)", name, err))
				continue
			}
			blocks = append(blocks, "Attached text file "+name+":\n"+compactTextForCompaction(body, "attachment "+name))
		case attachment.KindImage:
			lines := []string{"Image attachment omitted for text-only compaction:", "- name: " + name}
			if mime := strings.TrimSpace(meta.MIME); mime != "" {
				lines = append(lines, "- mime: "+mime)
			}
			if meta.Size > 0 {
				lines = append(lines, fmt.Sprintf("- size: %d bytes", meta.Size))
			}
			blocks = append(blocks, strings.Join(lines, "\n"))
		default:
			blocks = append(blocks, fmt.Sprintf("Attachment omitted for text-only compaction: %s (unsupported MIME %s)", name, meta.MIME))
		}
	}
	return strings.TrimSpace(strings.Join(blocks, "\n\n"))
}

func compactAssistantMessageText(msg domain.AssistantMessage, preserveThinking bool) string {
	blocks := make([]string, 0, 3)
	if preserveThinking && msg.Reasoning.ReplayText() != "" {
		blocks = append(blocks, formatThinkingBlock(msg.Reasoning.ReplayText()))
	}
	if text := strings.TrimSpace(msg.Text); text != "" {
		blocks = append(blocks, text)
	}
	if len(msg.Tools) > 0 {
		lines := make([]string, 0, len(msg.Tools)+1)
		lines = append(lines, "Tool calls:")
		for _, tool := range msg.Tools {
			args, err := json.Marshal(tool.Args)
			if err != nil {
				lines = append(lines, fmt.Sprintf("- %s <args unavailable: %v>", tool.Tool, err))
				continue
			}
			lines = append(lines, fmt.Sprintf("- %s %s", tool.Tool, string(args)))
		}
		blocks = append(blocks, strings.Join(lines, "\n"))
	}
	return strings.TrimSpace(strings.Join(blocks, "\n\n"))
}

func (e *Engine) compactionToolResultMessage(tool domain.ToolCall) (provider.Message, bool) {
	if tool.Result == nil && tool.Error == nil {
		return provider.Message{}, false
	}
	status := domain.ToolResultStatusOK
	text := ""
	diff := ""
	var data any
	if tool.Result != nil {
		status = tool.Result.Status
		text = tool.Result.Text
		diff = tool.Result.Diff
		data = tool.Result.Data
	}
	if tool.Error != nil {
		status = domain.ToolResultStatusError
		text = tool.Error.Message
		data = tools.ErrorStoredResult{Message: tool.Error.Message}
	}
	part := domain.Part{
		Kind: domain.PartKindToolOutput,
		Payload: domain.ToolOutputPayload{
			Tool:       tool.Tool,
			ToolCallID: string(tool.ToolCallID),
			Args:       tool.Args,
			Status:     status,
			Text:       text,
			Diff:       diff,
			Result:     data,
		},
	}
	part.Body = part.Text()
	body := strings.TrimSpace(part.Text())
	if formatted, ok := tools.CompactModelTextForPart(part, diff, tools.DefaultCompactFormatLimits()); ok {
		body = strings.TrimSpace(formatted)
	} else if diff != "" {
		if body != "" {
			body += "\n\nDiff:\n" + diff
		} else {
			body = "Diff:\n" + diff
		}
		body = compactTextForCompaction(body, tool.Tool.String()+" result")
	}
	if body == "" {
		return provider.Message{}, false
	}
	return provider.Message{Role: provider.RoleUser, Content: "Tool result for " + tool.Tool.String() + ":\n" + body}, true
}

func compactTextForCompaction(text string, label string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	const maxBytes = 16 * 1024
	lines := strings.Split(text, "\n")
	if len([]byte(text)) <= maxBytes && len(lines) <= 160 {
		return text
	}
	if len(lines) <= 160 {
		data := []byte(text)
		if len(data) <= maxBytes {
			return text
		}
		return strings.TrimSpace(string(data[:maxBytes])) + fmt.Sprintf("\n[%s truncated for compaction: kept %d bytes]", label, maxBytes)
	}
	head := strings.Join(lines[:80], "\n")
	tail := strings.Join(lines[len(lines)-80:], "\n")
	return head + fmt.Sprintf("\n[%s truncated for compaction: kept first 80 and last 80 lines of %d lines]\n", label, len(lines)) + tail
}

func (e *Engine) completeCompactionChat(ctx context.Context, chat domain.Chat, client *provider.Client, req provider.ChatRequest, out chan<- domain.Event) (provider.ChatResponse, error) {
	promptProgressPending := e.promptProgressProbePending(chat.ProviderID) && provider.RequestsPromptProgress(req)
	streamedBytes := 0
	onEvent := func(evt domain.Event) {
		if out == nil {
			return
		}
		switch evt.Kind {
		case domain.EventKindMessageDelta, domain.EventKindReasoning:
			streamedBytes += len(evt.Text)
			if streamedBytes <= 0 {
				return
			}
			out <- domain.Event{
				Kind: domain.EventKindStatus,
				Text: fmt.Sprintf("Streaming compacted results (%s)", formatCompactionBytes(streamedBytes)),
				Meta: map[string]string{"compaction": "streaming"},
			}
		case domain.EventKindStatus:
			if evt.Meta[domain.EventMetaPromptProgress] != "true" {
				return
			}
			if evt.Meta == nil {
				evt.Meta = map[string]string{}
			}
			evt.Meta["compaction"] = "progress"
			evt.Text = compactionPromptProgressText(evt.Meta)
			out <- evt
		}
	}
	if req.Stream {
		resp, err := client.StreamChatResponse(ctx, req, onEvent)
		if err == nil {
			if promptProgressPending {
				e.setPromptProgressSupport(chat.ProviderID, true)
			}
			return resp, nil
		}
		if promptProgressPending && provider.ShouldRetryWithoutPromptProgress(err) {
			e.setPromptProgressSupport(chat.ProviderID, false)
			return client.StreamChatResponse(ctx, provider.WithoutPromptProgress(req), onEvent)
		}
		return resp, err
	}
	resp, err := client.CompleteChat(ctx, req)
	if err == nil {
		if promptProgressPending {
			e.setPromptProgressSupport(chat.ProviderID, true)
		}
		return resp, nil
	}
	if promptProgressPending && provider.ShouldRetryWithoutPromptProgress(err) {
		e.setPromptProgressSupport(chat.ProviderID, false)
		return client.CompleteChat(ctx, provider.WithoutPromptProgress(req))
	}
	return resp, err
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

func formatCompactionBytes(size int) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}
	value := float64(size) / 1024
	if value < 10 {
		return fmt.Sprintf("%.1f KB", value)
	}
	return fmt.Sprintf("%.0f KB", value)
}

func (e *Engine) estimateContextTokensForTimeline(session domain.Session, chat domain.Chat, timeline []domain.TimelineItem) int {
	envelope, err := e.buildPromptEnvelopeForTimeline(session, chat, timeline, "", nil, nil, nil)
	if err != nil {
		return 0
	}
	payload, err := json.Marshal(provider.SerializePromptEnvelope(envelope))
	if err != nil || len(payload) == 0 {
		return 0
	}
	return len(payload) / 4
}

func (e *Engine) estimateCompactedTimelineContextTokens(session domain.Session, chat domain.Chat, timeline []domain.TimelineItem, compactionItem domain.TimelineItem, firstKeptItemID string, summary string) int {
	firstKeptIdx := firstKeptTimelineIndex(timeline, firstKeptItemID)
	if firstKeptIdx < 0 {
		firstKeptIdx = len(timeline)
	}
	simulated := make([]domain.TimelineItem, 0, 1+max(0, len(timeline)-firstKeptIdx))
	compactionItem.Content = domain.Compaction{Summary: summary, Status: "completed", FirstKeptItemID: firstKeptItemID}
	simulated = append(simulated, compactionItem)
	if firstKeptIdx < len(timeline) {
		simulated = append(simulated, timeline[firstKeptIdx:]...)
	}
	estimated := e.estimateContextTokensForTimeline(session, chat, simulated)
	if estimated <= 0 {
		return tokenestimate.Text(summary)
	}
	return estimated
}

type providerToolCallParseResult struct {
	Requests  []tools.Request
	ToolCalls []domain.ToolCall
	Err       error
}

func (e *Engine) parseProviderToolCallsForTranscript(raw []provider.ToolCall, sessionID id.ID) providerToolCallParseResult {
	var out providerToolCallParseResult
	var parseErr error
	for _, item := range raw {
		call, err := e.parseProviderToolCall(item)
		if err != nil {
			if parseErr == nil {
				parseErr = err
			}
			e.recordLifecycle(sessionID, "provider_tool_call_parse_error", err.Error(), map[string]string{
				"tool_call_id": strings.TrimSpace(item.ID),
				"tool_type":    strings.TrimSpace(item.Type),
			})
			if failed, ok := e.failedProviderToolCall(item, err); ok {
				out.ToolCalls = append(out.ToolCalls, failed)
			}
			continue
		}
		e.recordLifecycle(sessionID, "tool_call_parsed", call.ContextString(), map[string]string{"tool": call.Tool.String(), "tool_call_id": call.ToolCallID})
		out.Requests = append(out.Requests, call)
		out.ToolCalls = append(out.ToolCalls, toolCallRecord(call))
	}
	out.Err = parseErr
	return out
}

func (e *Engine) parseProviderToolCall(item provider.ToolCall) (tools.Request, error) {
	name := strings.TrimSpace(item.Function.Name)
	ok := false
	serverID, toolName := "", ""
	if e.mcp != nil {
		localDefs := tools.Definitions(tools.Runtime{})
		if e.toolsRuntime != nil {
			localDefs = e.toolsRuntime.Definitions(domain.Session{}, domain.Chat{})
		}
		serverID, toolName, ok = e.mcp.ResolveToolName(name, localDefs)
	}
	if !ok {
		return tools.ParseProviderCall(item)
	}
	rawArgs := strings.TrimSpace(item.Function.Arguments)
	if rawArgs == "" {
		rawArgs = "{}"
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(rawArgs), &parsed); err != nil {
		return tools.Request{}, fmt.Errorf("decode mcp tool arguments for %s: %w", name, err)
	}
	req := tools.Request{
		Tool:       domain.ToolKindMCP,
		ToolCallID: strings.TrimSpace(item.ID),
		Args: map[string]string{
			"server":        serverID,
			"tool":          toolName,
			"arguments_raw": rawArgs,
		},
	}
	if req.ToolCallID == "" {
		return tools.Request{}, fmt.Errorf("provider MCP tool call for %s missing id", name)
	}
	normalized, err := tools.Normalize(req)
	if err != nil {
		return tools.Request{}, tools.ProviderCallError{Request: req, Err: err}
	}
	return normalized, nil
}

func (e *Engine) failedProviderToolCall(item provider.ToolCall, parseErr error) (domain.ToolCall, bool) {
	var callErr tools.ProviderCallError
	if !errors.As(parseErr, &callErr) {
		return domain.ToolCall{}, false
	}
	req := callErr.Request
	if req.Tool == "" || strings.TrimSpace(req.ToolCallID) == "" {
		return domain.ToolCall{}, false
	}
	now := time.Now().UTC()
	return domain.ToolCall{
		ToolCallID:  domain.ToolCallID(req.ToolCallID),
		Tool:        req.Tool,
		Args:        req.Args,
		Status:      domain.ToolStatusErrored,
		Error:       &domain.ToolError{Message: "Invalid tool call: " + parseErr.Error()},
		CompletedAt: now,
	}, true
}

func (e *Engine) failedStreamedProviderToolCall(callErr provider.ToolCallError) domain.ToolCall {
	call := callErr.ToolCall
	kind := domain.ToolKind(strings.TrimSpace(call.Function.Name))
	now := time.Now().UTC()
	toolCallID := strings.TrimSpace(call.ID)
	if toolCallID == "" {
		toolCallID = "stream_argument_limit_" + strconv.FormatInt(now.UnixNano(), 10)
	}
	return domain.ToolCall{
		ToolCallID:  domain.ToolCallID(toolCallID),
		Tool:        kind,
		Status:      domain.ToolStatusErrored,
		Error:       &domain.ToolError{Message: callErr.Message},
		CompletedAt: now,
	}
}

func toolCallRecord(call tools.Request) domain.ToolCall {
	return domain.ToolCall{
		ToolCallID: domain.ToolCallID(call.ToolCallID),
		Tool:       call.Tool,
		Args:       call.Args,
		Status:     domain.ToolStatusPending,
	}
}

func serializeRequest(req tools.Request) (string, error) {
	payload := maps.Clone(req.Args)
	if strings.TrimSpace(req.ToolCallID) != "" {
		payload["tool_call_id"] = req.ToolCallID
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("serialize request: %w", err)
	}
	return string(data), nil
}

func max(a, b int) int {
	return slices.Max([]int{a, b})
}
