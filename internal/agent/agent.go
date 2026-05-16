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
	"regexp"
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
	"github.com/lkarlslund/koder/internal/mcp"
	"github.com/lkarlslund/koder/internal/permissionprofile"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/reference"
	"github.com/lkarlslund/koder/internal/skills"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tokenestimate"
	"github.com/lkarlslund/koder/internal/tools"
	_ "github.com/lkarlslund/koder/internal/tools/all"
	"github.com/lkarlslund/koder/internal/turncontrol"
)

type Engine struct {
	cfg        config.Config
	store      *store.Store
	registry   *tools.Registry
	debug      *debugsrv.Recorder
	files      *attachment.Manager
	caps       *provider.CapabilityStore
	agents     *agents.Manager
	mcp        *mcp.Manager
	exec       *execruntime.Manager
	workdir    string
	envMu      sync.Mutex
	envPrompts map[domain.ID]string
	chatMu     sync.RWMutex
	chats      map[domain.ID]*chatpkg.Chat
	runs       map[domain.ID]chatRunState
	retryPause func(context.Context, time.Duration, func(time.Duration)) error
}

var patchPathPattern = regexp.MustCompile(`(?m)^(?:\+\+\+|---)\s+(?:a/|b/)?([^\t\n]+)`)

const (
	maxRateLimitRetries       = 3
	maxTransientChatRetries   = 3
	defaultRateLimitRetryWait = 5 * time.Second
	defaultTransientRetryWait = 250 * time.Millisecond
)

func New(cfg config.Config, st *store.Store, registry *tools.Registry, debug *debugsrv.Recorder, workdir string, mcpManagers ...*mcp.Manager) *Engine {
	var mcpManager *mcp.Manager
	if len(mcpManagers) > 0 {
		mcpManager = mcpManagers[0]
	}
	if registry != nil {
		registry.SetMCP(mcpManager)
		registry.SetEditForgiveness(cfg.UI.EditForgiveness)
	}
	var execManager *execruntime.Manager
	if registry != nil {
		if candidate, ok := registry.ExecControl().(*execruntime.Manager); ok {
			execManager = candidate
		}
	}
	return &Engine{
		cfg:        cfg,
		store:      st,
		registry:   registry,
		debug:      debug,
		files:      attachment.NewManager(cfg.StateDir()),
		caps:       provider.NewCapabilityStore(cfg.StateDir()),
		agents:     agents.NewManager(cfg.StateDir(), filepath.Join(filepath.Dir(cfg.Path()), "AGENTS.md")),
		mcp:        mcpManager,
		exec:       execManager,
		workdir:    workdir,
		chats:      map[domain.ID]*chatpkg.Chat{},
		runs:       map[domain.ID]chatRunState{},
		retryPause: waitForRetry,
	}
}

func (e *Engine) UpdateConfig(cfg config.Config) {
	e.cfg = cfg
	e.agents = agents.NewManager(cfg.StateDir(), filepath.Join(filepath.Dir(cfg.Path()), "AGENTS.md"))
	if e.registry != nil {
		e.registry.SetEditForgiveness(cfg.UI.EditForgiveness)
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
	return e.exec
}

func (e *Engine) RunPrompt(ctx context.Context, session domain.Session, prompt string) (<-chan domain.Event, error) {
	return e.RunPromptWithInputs(ctx, session, prompt, nil, nil, "")
}

func (e *Engine) RunPromptWithAttachments(ctx context.Context, session domain.Session, prompt string, drafts []attachment.Draft, note string) (<-chan domain.Event, error) {
	return e.RunPromptWithInputs(ctx, session, prompt, drafts, nil, note)
}

func (e *Engine) RunPromptWithInputs(ctx context.Context, session domain.Session, prompt string, drafts []attachment.Draft, refs []reference.Draft, note string) (<-chan domain.Event, error) {
	chat, err := e.store.DefaultChat(ctx, session.ID)
	if err != nil {
		return nil, err
	}
	return e.RunPromptInChat(ctx, session, chat, prompt, drafts, refs, note)
}

func (e *Engine) RunPromptInChat(ctx context.Context, session domain.Session, chat domain.Chat, prompt string, drafts []attachment.Draft, refs []reference.Draft, note string) (<-chan domain.Event, error) {
	return e.runModelPrompt(ctx, session, chat, prompt, drafts, refs, note)
}

func (e *Engine) SetPermissionProfile(ctx context.Context, sessionID domain.ID, profile string) (<-chan domain.Event, error) {
	return e.setPermissionProfile(ctx, sessionID, "", profile)
}

func (e *Engine) SetPermissionProfileAndReevaluateApproval(ctx context.Context, sessionID, approvalID domain.ID, profile string) (<-chan domain.Event, error) {
	return e.setPermissionProfileAndReevaluateApproval(ctx, sessionID, "", approvalID, profile)
}

func (e *Engine) SetPermissionProfileInChat(ctx context.Context, sessionID, chatID domain.ID, profile string) (<-chan domain.Event, error) {
	return e.setPermissionProfile(ctx, sessionID, chatID, profile)
}

func (e *Engine) SetPermissionProfileInChatAndReevaluateApproval(ctx context.Context, sessionID, chatID, approvalID domain.ID, profile string) (<-chan domain.Event, error) {
	return e.setPermissionProfileAndReevaluateApproval(ctx, sessionID, chatID, approvalID, profile)
}

func (e *Engine) ApproveInChatWithRule(ctx context.Context, sessionID, chatID, approvalID domain.ID, rule domain.PermissionOverride) (<-chan domain.Event, error) {
	return e.approveInChatWithRule(ctx, sessionID, chatID, approvalID, rule)
}

func (e *Engine) ApproveToolInChatWithRule(ctx context.Context, sessionID, chatID domain.ID, toolCallID string, rule domain.PermissionOverride) (<-chan domain.Event, error) {
	return e.approveToolInChatWithRule(ctx, sessionID, chatID, toolCallID, rule)
}

func (e *Engine) Approve(ctx context.Context, sessionID, approvalID domain.ID) (<-chan domain.Event, error) {
	return e.approve(ctx, sessionID, "", approvalID)
}

func (e *Engine) ApproveInChat(ctx context.Context, sessionID, chatID, approvalID domain.ID) (<-chan domain.Event, error) {
	return e.approve(ctx, sessionID, chatID, approvalID)
}

func (e *Engine) ApproveToolInChat(ctx context.Context, sessionID, chatID domain.ID, toolCallID string) (<-chan domain.Event, error) {
	return e.approveTool(ctx, sessionID, chatID, toolCallID)
}

func (e *Engine) Deny(ctx context.Context, sessionID, approvalID domain.ID) (<-chan domain.Event, error) {
	return e.deny(ctx, sessionID, "", approvalID)
}

func (e *Engine) DenyInChat(ctx context.Context, sessionID, chatID, approvalID domain.ID) (<-chan domain.Event, error) {
	return e.deny(ctx, sessionID, chatID, approvalID)
}

func (e *Engine) DenyToolInChat(ctx context.Context, sessionID, chatID domain.ID, toolCallID string) (<-chan domain.Event, error) {
	return e.denyTool(ctx, sessionID, chatID, toolCallID)
}

func (e *Engine) Compact(ctx context.Context, sessionID domain.ID) (<-chan domain.Event, error) {
	chat, err := e.store.DefaultChat(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return e.CompactChat(ctx, sessionID, chat.ID)
}

func (e *Engine) CompactChat(ctx context.Context, sessionID, chatID domain.ID) (<-chan domain.Event, error) {
	session, err := e.store.GetSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	providerCfg, ok := e.cfg.Provider(session.ProviderID)
	if !ok {
		return nil, fmt.Errorf("provider %q not found", session.ProviderID)
	}
	client, err := provider.New(session.ProviderID, providerCfg, e.debug)
	if err != nil {
		return nil, err
	}
	out := make(chan domain.Event)
	go func() {
		defer close(out)
		out <- domain.Event{Kind: domain.EventKindStatus, Text: "Compacting session..."}
		if err := e.compactSession(ctx, session, chatID, client, "manual", out); err != nil {
			if interruptedErr(err) {
				e.emitInterrupted(out, chatID, session.ID)
				return
			}
			e.recordAssistantError(ctx, chatID, session.ID, err)
			out <- domain.Event{Kind: domain.EventKindError, Err: err}
			return
		}
		out <- domain.Event{Kind: domain.EventKindMessageDone}
	}()
	return out, nil
}

func (e *Engine) RunContinue(ctx context.Context, session domain.Session, note string) (<-chan domain.Event, error) {
	chat, err := e.store.DefaultChat(ctx, session.ID)
	if err != nil {
		return nil, err
	}
	return e.RunContinueInChat(ctx, session, chat, note)
}

func (e *Engine) RunContinueInChat(ctx context.Context, session domain.Session, chat domain.Chat, note string) (<-chan domain.Event, error) {
	return e.runContinue(ctx, session, chat, note)
}

// ResumePendingToolCallsInChat resumes durable tool calls that were saved before shutdown.
func (e *Engine) ResumePendingToolCallsInChat(ctx context.Context, session domain.Session, chat domain.Chat) (<-chan domain.Event, error) {
	calls, err := e.pendingExecutableToolCalls(ctx, chat.ID)
	if err != nil {
		return nil, err
	}
	if len(calls) == 0 {
		return nil, nil
	}
	providerCfg, ok := e.cfg.Provider(session.ProviderID)
	if !ok {
		return nil, fmt.Errorf("provider %q not found", session.ProviderID)
	}
	client, err := provider.New(session.ProviderID, providerCfg, e.debug)
	if err != nil {
		return nil, err
	}
	out := make(chan domain.Event)
	go func() {
		defer close(out)
		needsApproval, err := e.handleModelToolCalls(ctx, session, chat, calls, out)
		if err != nil {
			if interruptedErr(err) {
				e.emitInterrupted(out, chat.ID, session.ID)
				return
			}
			e.recordAssistantError(ctx, chat.ID, session.ID, err)
			out <- domain.Event{Kind: domain.EventKindError, Err: err}
			return
		}
		if needsApproval || turncontrol.ShouldStop(ctx) {
			return
		}
		session, err = e.store.GetSession(ctx, session.ID)
		if err != nil {
			if interruptedErr(err) {
				e.emitInterrupted(out, chat.ID, session.ID)
				return
			}
			out <- domain.Event{Kind: domain.EventKindError, Err: err}
			return
		}
		if err := e.continueModelTurn(ctx, session, chat, client, out, nil); err != nil {
			if interruptedErr(err) {
				e.emitInterrupted(out, chat.ID, session.ID)
				return
			}
			e.recordAssistantError(ctx, chat.ID, session.ID, err)
			out <- domain.Event{Kind: domain.EventKindError, Err: err}
		}
	}()
	return out, nil
}

func (e *Engine) pendingExecutableToolCalls(ctx context.Context, chatID domain.ID) ([]tools.Request, error) {
	if chatID == "" {
		return nil, nil
	}
	items, err := e.store.TimelineForChat(ctx, chatID)
	if err != nil {
		return nil, err
	}
	for idx := len(items) - 1; idx >= 0; idx-- {
		item := items[idx]
		if _, ok := item.Content.(domain.UserMessage); ok {
			return nil, nil
		}
		assistant, ok := item.Content.(domain.AssistantMessage)
		if !ok {
			continue
		}
		var calls []tools.Request
		for _, call := range assistant.Tools {
			if call.Status != domain.ToolStatusPending || call.Result != nil || call.Error != nil || call.Approval != nil {
				continue
			}
			calls = append(calls, tools.Request{
				Tool:       call.Tool,
				ToolCallID: string(call.ToolCallID),
				Args:       maps.Clone(call.Args),
			})
		}
		return calls, nil
	}
	return nil, nil
}

func (e *Engine) PreviewNextRequest(ctx context.Context, session domain.Session, prompt string, drafts []attachment.Draft, refs []reference.Draft, note string) (provider.ChatRequest, error) {
	chat, err := e.store.DefaultChat(ctx, session.ID)
	if err != nil {
		return provider.ChatRequest{}, err
	}
	return e.PreviewNextRequestForChat(ctx, session, chat, prompt, drafts, refs, note)
}

func (e *Engine) PreviewNextRequestForChat(ctx context.Context, session domain.Session, chat domain.Chat, prompt string, drafts []attachment.Draft, refs []reference.Draft, note string) (provider.ChatRequest, error) {
	if err := e.validatePromptAttachments(session, drafts); err != nil {
		return provider.ChatRequest{}, err
	}
	messages, err := e.buildConversationPreview(ctx, session, chat.ID, prompt, drafts, refs, transientTurnMessages(note, ""))
	if err != nil {
		return provider.ChatRequest{}, err
	}
	return e.chatRequest(session, chat, messages, false), nil
}

func (e *Engine) runModelPrompt(ctx context.Context, session domain.Session, chat domain.Chat, prompt string, drafts []attachment.Draft, refs []reference.Draft, note string) (<-chan domain.Event, error) {
	if err := e.validatePromptAttachments(session, drafts); err != nil {
		return nil, err
	}
	providerCfg, ok := e.cfg.Provider(session.ProviderID)
	if !ok {
		return nil, fmt.Errorf("provider %q not found", session.ProviderID)
	}
	client, err := provider.New(session.ProviderID, providerCfg, e.debug)
	if err != nil {
		return nil, err
	}

	out := make(chan domain.Event)
	go func() {
		defer close(out)
		if session.ID != "" && needsSessionAgentsRefresh(session) {
			out <- domain.Event{Kind: domain.EventKindStatus, Text: "Checking project instructions..."}
		}
		session, err = e.ensureSessionAgents(ctx, session, client)
		if err != nil {
			if interruptedErr(err) {
				e.emitInterrupted(out, chat.ID, session.ID)
				return
			}
			e.recordAssistantError(ctx, chat.ID, session.ID, err)
			out <- domain.Event{Kind: domain.EventKindError, Err: err}
			return
		}
		e.recordLifecycle(session.ID, "prompt_started", prompt, map[string]string{"provider": session.ProviderID, "model": session.ModelID})
		compacted, err := e.autoCompactIfNeeded(ctx, session, client, out)
		if err != nil {
			if interruptedErr(err) {
				e.emitInterrupted(out, chat.ID, session.ID)
				return
			}
			e.recordAssistantError(ctx, chat.ID, session.ID, err)
			out <- domain.Event{Kind: domain.EventKindError, Err: err}
			return
		}
		if compacted {
			session, err = e.store.GetSession(ctx, session.ID)
			if err != nil {
				if interruptedErr(err) {
					e.emitInterrupted(out, chat.ID, session.ID)
					return
				}
				out <- domain.Event{Kind: domain.EventKindError, Err: err}
				return
			}
		}
		userItem, err := e.persistUserPrompt(ctx, session, chat.ID, prompt, drafts, refs)
		if err != nil {
			if interruptedErr(err) {
				e.emitInterrupted(out, chat.ID, session.ID)
				return
			}
			out <- domain.Event{Kind: domain.EventKindError, Err: err}
			return
		}
		e.recordLifecycle(session.ID, "user_message_persisted", prompt, map[string]string{"item_id": userItem.ID})
		if err := e.continueModelTurn(ctx, session, chat, client, out, transientTurnMessages(note, "")); err != nil {
			if interruptedErr(err) {
				e.emitInterrupted(out, chat.ID, session.ID)
				return
			}
			e.recordAssistantError(ctx, chat.ID, session.ID, err)
			out <- domain.Event{Kind: domain.EventKindError, Err: err}
			return
		}
	}()
	return out, nil
}

func (e *Engine) runContinue(ctx context.Context, session domain.Session, chat domain.Chat, note string) (<-chan domain.Event, error) {
	providerCfg, ok := e.cfg.Provider(session.ProviderID)
	if !ok {
		return nil, fmt.Errorf("provider %q not found", session.ProviderID)
	}
	client, err := provider.New(session.ProviderID, providerCfg, e.debug)
	if err != nil {
		return nil, err
	}
	out := make(chan domain.Event)
	go func() {
		defer close(out)
		if session.ID != "" && needsSessionAgentsRefresh(session) {
			out <- domain.Event{Kind: domain.EventKindStatus, Text: "Checking project instructions..."}
		}
		session, err = e.ensureSessionAgents(ctx, session, client)
		if err != nil {
			if interruptedErr(err) {
				e.emitInterrupted(out, chat.ID, session.ID)
				return
			}
			e.recordAssistantError(ctx, chat.ID, session.ID, err)
			out <- domain.Event{Kind: domain.EventKindError, Err: err}
			return
		}
		if strings.TrimSpace(note) != "" {
			e.recordLifecycle(session.ID, "continue_with_note", note, nil)
		} else {
			e.recordLifecycle(session.ID, "continue", "", nil)
		}
		compacted, err := e.autoCompactIfNeeded(ctx, session, client, out)
		if err != nil {
			if interruptedErr(err) {
				e.emitInterrupted(out, chat.ID, session.ID)
				return
			}
			e.recordAssistantError(ctx, chat.ID, session.ID, err)
			out <- domain.Event{Kind: domain.EventKindError, Err: err}
			return
		}
		if compacted {
			session, err = e.store.GetSession(ctx, session.ID)
			if err != nil {
				if interruptedErr(err) {
					e.emitInterrupted(out, chat.ID, session.ID)
					return
				}
				out <- domain.Event{Kind: domain.EventKindError, Err: err}
				return
			}
		}
		if err := e.continueModelTurn(ctx, session, chat, client, out, transientTurnMessages(note, "Continue from where you left off.")); err != nil {
			if interruptedErr(err) {
				e.emitInterrupted(out, chat.ID, session.ID)
				return
			}
			e.recordAssistantError(ctx, chat.ID, session.ID, err)
			out <- domain.Event{Kind: domain.EventKindError, Err: err}
			return
		}
	}()
	return out, nil
}

func (e *Engine) RefreshAgents(ctx context.Context, sessionID domain.ID) (domain.Session, error) {
	session, err := e.store.GetSession(ctx, sessionID)
	if err != nil {
		return domain.Session{}, err
	}
	providerCfg, ok := e.cfg.Provider(session.ProviderID)
	if !ok {
		return domain.Session{}, fmt.Errorf("provider %q not found", session.ProviderID)
	}
	client, err := provider.New(session.ProviderID, providerCfg, e.debug)
	if err != nil {
		return domain.Session{}, err
	}
	return e.refreshSessionAgents(ctx, session, client)
}

func (e *Engine) ensureSessionAgents(ctx context.Context, session domain.Session, client *provider.Client) (domain.Session, error) {
	if !needsSessionAgentsRefresh(session) {
		return session, nil
	}
	return e.refreshSessionAgents(ctx, session, client)
}

func needsSessionAgentsRefresh(session domain.Session) bool {
	if strings.TrimSpace(session.ProjectChecksum) == "" {
		return true
	}
	return strings.TrimSpace(session.AgentsResolved) == "" && strings.TrimSpace(session.AgentsSummary) == ""
}

func (e *Engine) refreshSessionAgents(ctx context.Context, session domain.Session, client *provider.Client) (domain.Session, error) {
	snapshot, err := e.agents.Discover(ctx, e.workdir)
	if err != nil {
		return domain.Session{}, err
	}
	resolution, err := e.agents.Resolve(ctx, client, session.ModelID, snapshot)
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
	if err := e.store.UpdateSessionAgents(
		ctx,
		session.ID,
		resolution.Snapshot.ProjectRoot,
		resolution.Snapshot.Checksum,
		resolution.ResolvedAgents,
		resolution.ConflictSummary,
		files,
		resolution.GeneratedAt,
	); err != nil {
		return domain.Session{}, err
	}
	return e.store.GetSession(ctx, session.ID)
}

func (e *Engine) persistUserPrompt(ctx context.Context, session domain.Session, chatID domain.ID, prompt string, drafts []attachment.Draft, refs []reference.Draft) (domain.TimelineItem, error) {
	user := domain.UserMessage{Text: prompt}
	for _, draft := range drafts {
		meta, err := e.files.AdoptDraft(draft, session.ID)
		if err != nil {
			return domain.TimelineItem{}, err
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
	item, err := e.store.AppendTimeline(ctx, chatID, user)
	if err != nil {
		return domain.TimelineItem{}, err
	}
	item.Seal(time.Now().UTC())
	if err := e.store.Timeline().Put(ctx, item); err != nil {
		return domain.TimelineItem{}, err
	}
	return item, nil
}

func queuedAttachmentDrafts(src []domain.QueuedAttachment) []attachment.Draft {
	if len(src) == 0 {
		return nil
	}
	dst := make([]attachment.Draft, 0, len(src))
	for _, item := range src {
		dst = append(dst, attachment.Draft{Metadata: attachment.Metadata{
			ID:       item.ID,
			Name:     item.Name,
			MIME:     item.MIME,
			Path:     item.Path,
			Size:     item.Size,
			Source:   item.Source,
			Original: item.Original,
		}})
	}
	return dst
}

func queuedReferenceDrafts(src []domain.QueuedReference) []reference.Draft {
	if len(src) == 0 {
		return nil
	}
	dst := make([]reference.Draft, 0, len(src))
	for _, item := range src {
		dst = append(dst, reference.Draft{
			Kind:    reference.Kind(item.Kind),
			Path:    item.Path,
			Display: item.Display,
			Start:   item.Start,
			End:     item.End,
		})
	}
	return dst
}

func (e *Engine) applyQueuedSteer(ctx context.Context, session domain.Session, chat *domain.Chat, out chan<- domain.Event) (bool, error) {
	refreshed, err := e.store.GetChat(ctx, chat.ID)
	if err != nil {
		return false, err
	}
	*chat = refreshed
	idx := -1
	for i, item := range chat.QueuedInputs {
		if item.Held || item.Kind != domain.QueuedInputKindSteer {
			continue
		}
		idx = i
		break
	}
	if idx < 0 {
		return false, nil
	}
	item := chat.QueuedInputs[idx]
	remaining := append(slices.Clone(chat.QueuedInputs[:idx]), slices.Clone(chat.QueuedInputs[idx+1:])...)
	if err := e.store.SetChatQueuedInputs(ctx, chat.ID, remaining); err != nil {
		return false, err
	}
	chat.QueuedInputs = remaining
	out <- domain.Event{Kind: domain.EventKindStatus, Text: "Applying queued steer..."}
	if _, err := e.persistUserPrompt(ctx, session, chat.ID, item.Text, queuedAttachmentDrafts(item.Attachments), queuedReferenceDrafts(item.References)); err != nil {
		return false, err
	}
	return true, nil
}

func (e *Engine) continueModelTurn(ctx context.Context, session domain.Session, chat domain.Chat, client *provider.Client, out chan<- domain.Event, transient []provider.InstructionBlock) error {
	tracker := toolLoopTracker{}
	autoContinuedBadStop := false
	for steps := 0; steps < e.maxToolLoopSteps(); steps++ {
		e.recordLifecycle(session.ID, "model_turn_started", "", map[string]string{"step": strconv.Itoa(steps + 1)})
		if applied, err := e.applyQueuedSteer(ctx, session, &chat, out); err != nil {
			return err
		} else if applied {
			transient = nil
		}
		messages, buildErr := e.buildConversationPreview(ctx, session, chat.ID, "", nil, nil, transient)
		if buildErr != nil {
			return buildErr
		}
		if steps > 0 {
			compacted, compactErr := e.autoCompactPreparedMessagesIfNeeded(ctx, session, chat.ID, client, messages, out)
			if compactErr != nil {
				return compactErr
			}
			if compacted {
				session, buildErr = e.store.GetSession(ctx, session.ID)
				if buildErr != nil {
					return buildErr
				}
				transient = transientTurnMessages("", "Continue from the compacted session summary. Do not restart, greet, or restate the summary. Continue the pending task from the latest tool result.")
				continue
			}
		}
		transient = nil

		stream := e.providerStreamingEnabled(session)
		req := e.chatRequest(session, chat, messages, stream)
		assistantItem, itemErr := e.nextAssistantTimelineItem(ctx, chat.ID)
		if itemErr != nil {
			return itemErr
		}
		resp, streamed, completeErr := e.chatWithRetry(ctx, session.ID, client, out, req, assistantItem)
		if completeErr != nil {
			return completeErr
		}

		text, reasoning, usage := resp.Text, resp.Reasoning, resp.Usage
		if len(resp.ToolCalls) > 0 {
			calls, err := e.parseProviderToolCalls(resp.ToolCalls, session.ID)
			if err != nil {
				if strings.TrimSpace(text) == "" && strings.TrimSpace(reasoning) == "" {
					return err
				}
				e.recordLifecycle(session.ID, "provider_tool_call_parse_ignored", err.Error(), map[string]string{
					"tool_calls": strconv.Itoa(len(resp.ToolCalls)),
				})
			} else if len(calls) > 0 {
				assistantItem, err := e.persistAssistantToolCalls(ctx, chat.ID, session.ID, assistantItem, calls, strings.TrimSpace(resp.Text), resp.Usage)
				if err != nil {
					return err
				}
				out <- domain.Event{Kind: domain.EventKindToolCallDelta, Text: "tool calls persisted", Item: assistantItem}
				if resp.Usage.HasAnyTokens() {
					if err := e.saveChatContextUsage(ctx, chat.ID, resp.Usage); err != nil {
						return err
					}
					out <- domain.Event{Kind: domain.EventKindUsage, Usage: resp.Usage}
				}
				if turncontrol.ShouldStop(ctx) {
					return nil
				}
				if pause, ok := tracker.trackCalls(calls); ok {
					e.pauseContinuation(ctx, chat.ID, session.ID, pause, out)
					return nil
				}
				needsApproval, handledErr := e.handleModelToolCalls(ctx, session, chat, calls, out)
				if handledErr != nil {
					return handledErr
				}
				if needsApproval {
					return nil
				}
				continue
			}
		}

		call, plain := parseToolCall(text)
		if call != nil {
			e.recordLifecycle(session.ID, "tool_call_parsed", call.ContextString(), map[string]string{"tool": string(call.Tool), "tool_call_id": call.ToolCallID})
			assistantItem, err := e.store.AppendAssistantToolCalls(ctx, chat.ID, []domain.ToolCall{{
				ToolCallID: domain.ToolCallID(call.ToolCallID),
				Tool:       call.Tool,
				Args:       call.Args,
				Status:     domain.ToolStatusPending,
			}}, strings.TrimSpace(plain), domain.Usage{})
			if err != nil {
				return err
			}
			out <- domain.Event{Kind: domain.EventKindToolCallDelta, Text: "tool call persisted", Item: assistantItem}
			if pause, ok := tracker.trackCalls([]tools.Request{*call}); ok {
				e.pauseContinuation(ctx, chat.ID, session.ID, pause, out)
				return nil
			}
			if turncontrol.ShouldStop(ctx) {
				return nil
			}

			evt, handledErr := e.handleModelToolCall(ctx, session, chat, *call)
			if handledErr != nil {
				return handledErr
			}
			out <- evt
			if evt.Kind == domain.EventKindApprovalAsk {
				return nil
			}
			continue
		}
		tracker.reset()

		if steps > 0 && strings.TrimSpace(text) == "" && len(resp.ToolCalls) == 0 {
			if strings.TrimSpace(reasoning) != "" {
				transient = transientTurnMessages("", "Continue from the latest tool result. Do not stop at hidden reasoning. Either produce a visible answer for the user or make the next tool call.")
				continue
			}
			e.pauseContinuation(ctx, chat.ID, session.ID, continuationPause{
				Reason: continuationPauseReasonProviderRefusal,
				Body:   providerRefusalPauseBody(reasoning),
			}, out)
			return nil
		}
		if steps > 0 && e.cfg.UI.AutoContinue && !autoContinuedBadStop && len(resp.ToolCalls) == 0 && shouldAutoContinueBadStop(text) {
			autoContinuedBadStop = true
			e.recordLifecycle(session.ID, "auto_continue_bad_stop", strings.TrimSpace(text), map[string]string{"step": strconv.Itoa(steps + 1)})
			transient = transientTurnMessages("", "Continue by issuing the tool call now. Do not describe intent. If no tool call is needed, provide the final user-facing answer instead.")
			continue
		}
		autoContinuedBadStop = false

		assistant := domain.AssistantMessage{Text: text}
		if strings.TrimSpace(reasoning) != "" {
			assistant.Reasoning.Text = reasoning
		}
		usage = usage.Normalized()
		if usage.HasAnyTokens() {
			assistant.Usage = &usage
			if err := e.saveChatContextUsage(ctx, chat.ID, usage); err != nil {
				return err
			}
			if !streamed {
				out <- domain.Event{Kind: domain.EventKindUsage, Usage: usage}
			}
		}
		if !streamed && strings.TrimSpace(text) != "" {
			out <- domain.Event{Kind: domain.EventKindMessageDelta, Text: text, Item: assistantItem}
		}
		if !streamed && strings.TrimSpace(reasoning) != "" {
			out <- domain.Event{Kind: domain.EventKindReasoning, Text: reasoning, Item: assistantItem}
		}
		now := time.Now().UTC()
		assistantItem.Content = assistant
		if assistantItem.CreatedAt.IsZero() {
			assistantItem.CreatedAt = now
		}
		assistantItem.UpdatedAt = now
		if _, err := e.store.InsertTimelineItem(ctx, assistantItem); err != nil {
			return err
		}
		assistantItem.Seal(time.Now().UTC())
		if err := e.store.Timeline().Put(ctx, assistantItem); err != nil {
			return err
		}
		e.recordLifecycle(session.ID, "assistant_message_persisted", strings.TrimSpace(text), map[string]string{"item_id": assistantItem.ID})
		chatTitle, chatTitleErr := e.maybeUpdateChatTitle(ctx, chat.ID)
		if chatTitleErr != nil {
			e.recordLifecycle(session.ID, "chat_title_update_failed", chatTitleErr.Error(), map[string]string{"chat_id": chat.ID})
		}
		if strings.TrimSpace(chatTitle) != "" {
			e.recordLifecycle(session.ID, "chat_title_updated", chatTitle, map[string]string{"chat_id": chat.ID})
			out <- domain.Event{
				Kind: domain.EventKindChatTitle,
				Text: chatTitle,
				Meta: map[string]string{"chat_id": chat.ID},
			}
		}
		title, titleErr := e.maybeUpdateSessionTitle(ctx, session, client)
		if titleErr != nil {
			e.recordLifecycle(session.ID, "session_title_update_failed", titleErr.Error(), nil)
		}
		if strings.TrimSpace(title) != "" {
			e.recordLifecycle(session.ID, "session_title_updated", title, nil)
			out <- domain.Event{
				Kind: domain.EventKindSessionTitle,
				Text: title,
				Meta: map[string]string{"session_id": session.ID},
			}
		}
		out <- domain.Event{Kind: domain.EventKindMessageDone, Item: assistantItem}
		return nil
	}
	e.pauseContinuation(ctx, chat.ID, session.ID, continuationPause{
		Reason: continuationPauseReasonTurnLimit,
		Limit:  e.maxToolLoopSteps(),
		Body:   fmt.Sprintf("Paused continuation after reaching the model tool-turn limit (%d).", e.maxToolLoopSteps()),
	}, out)
	return nil
}

func (e *Engine) maxToolLoopSteps() int {
	if e.cfg.MaxToolLoopSteps > 0 {
		return e.cfg.MaxToolLoopSteps
	}
	return config.Default().MaxToolLoopSteps
}

func shouldAutoContinueBadStop(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || !strings.HasSuffix(trimmed, ":") {
		return false
	}
	lower := strings.ToLower(trimmed)
	for _, marker := range []string{
		"let me ",
		"i need to ",
		"i'll ",
		"i will ",
		"i am going to ",
		"i'm going to ",
		"i’m going to ",
		"next i ",
		"now i ",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func (e *Engine) maybeUpdateSessionTitle(ctx context.Context, session domain.Session, client *provider.Client) (string, error) {
	now := time.Now().UTC()
	timeline, prompt, err := e.titleSummaryMessages(ctx, session.ID)
	if err != nil {
		return "", err
	}
	if !shouldRefreshSessionTitle(session, timeline, now) {
		return "", nil
	}
	resp, err := client.CompleteChat(ctx, e.chatRequest(session, domain.Chat{}, prompt, false))
	if err != nil {
		return "", err
	}
	title := normalizeSessionTitle(resp.Text)
	if title == "" {
		return "", nil
	}
	refreshCount, _ := sessionTitleRefreshState(session)
	if err := e.store.UpdateSessionTitle(ctx, session.ID, title, now, refreshCount+1); err != nil {
		return "", err
	}
	return title, nil
}

func (e *Engine) chatRequest(session domain.Session, chat domain.Chat, messages []provider.Message, stream bool) provider.ChatRequest {
	req := provider.ChatRequest{
		Model:     session.ModelID,
		Messages:  messages,
		Stream:    stream,
		ExtraBody: provider.RequestExtraBody(e.providerConfigForSession(session), session.ModelID, e.modelPresetForSession(session)),
	}
	if len(messages) > 0 && (chat.ID != "" || chat.WorkflowRole != "") {
		req.Tools = tools.Definitions(e.toolRuntime(session, chat))
		if e.mcp != nil && toolEnabledForSession(e.cfg, session, domain.ToolKindMCP) && chatrole.AllowsTool(chat.WorkflowRole, domain.ToolKindMCP) {
			req.Tools = append(req.Tools, e.mcp.ToolDefinitionsWithReserved(req.Tools)...)
		}
		req.ToolChoice = "auto"
	}
	return req
}

func (e *Engine) providerConfigForSession(session domain.Session) config.Provider {
	cfg, _ := e.cfg.Provider(session.ProviderID)
	return cfg
}

func (e *Engine) modelPresetForSession(session domain.Session) string {
	return e.providerConfigForSession(session).ModelPreset
}

func (e *Engine) providerStreamingEnabled(session domain.Session) bool {
	return e.providerConfigForSession(session).Stream
}

func (e *Engine) preserveThinkingEnabled(session domain.Session) bool {
	return provider.PreserveThinkingEnabled(session.ModelID, e.modelPresetForSession(session))
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

func (e *Engine) maybeUpdateChatTitle(ctx context.Context, chatID domain.ID) (string, error) {
	if chatID == "" {
		return "", nil
	}
	chat, err := e.store.GetChat(ctx, chatID)
	if err != nil {
		return "", err
	}
	timeline, err := e.store.TimelineForChat(ctx, chatID)
	if err != nil {
		return "", err
	}
	if !shouldRefreshChatTitle(chat, timeline) {
		return "", nil
	}
	title := titleFromTimeline(timeline)
	if title == "" {
		return "", nil
	}
	chat.Title = title
	if err := e.store.UpdateChat(ctx, chat); err != nil {
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

func (e *Engine) titleSummaryMessages(ctx context.Context, sessionID domain.ID) ([]domain.TimelineItem, []provider.Message, error) {
	chat, err := e.store.DefaultChat(ctx, sessionID)
	if err != nil {
		return nil, nil, err
	}
	timeline, err := e.store.TimelineForChat(ctx, chat.ID)
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
			Role: domain.MessageRoleSystem,
			Content: "Write a concise session title of exactly 5 or 6 words. " +
				"Return only the title text with no quotes, punctuation suffix, or explanation.",
		},
		{
			Role:    domain.MessageRoleUser,
			Content: strings.Join(transcript, "\n\n"),
		},
	}, nil
}

func timelineTitleEntry(item domain.TimelineItem) (string, string) {
	switch content := item.Content.(type) {
	case domain.UserMessage:
		return string(domain.MessageRoleUser), content.Text
	case domain.AssistantMessage:
		if strings.TrimSpace(content.Text) != "" {
			return string(domain.MessageRoleAssistant), content.Text
		}
		return string(domain.MessageRoleAssistant), strings.TrimSpace(content.Reasoning.Text)
	case domain.ToolExecution:
		text := ""
		if content.Result != nil {
			text = content.Result.Text
		}
		if content.Error != nil {
			text = content.Error.Message
		}
		return string(domain.MessageRoleTool), text
	case domain.Notice:
		return "notice", content.Text
	case domain.Compaction:
		return "compaction", content.Summary
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

func (e *Engine) setPermissionProfile(ctx context.Context, sessionID, chatID domain.ID, raw string) (<-chan domain.Event, error) {
	profile := strings.TrimSpace(raw)
	if profile == "" {
		return nil, fmt.Errorf("permission profile is required; choose one of: %s", strings.Join(permissionprofile.ProfileNames(e.cfg.Permissions), "|"))
	}
	if !permissionprofile.IsBuiltinProfile(profile) {
		if _, ok := e.cfg.Permissions.Profiles[profile]; !ok {
			return nil, fmt.Errorf("unknown permission profile %q", profile)
		}
	}
	if sessionID == "" {
		return emitOnce(domain.Event{
			Kind: domain.EventKindStatus,
			Text: fmt.Sprintf("permission profile set to %s", permissionprofile.DisplayName(profile)),
			Meta: map[string]string{"permission_profile": profile},
		}), nil
	}
	if err := e.store.SetSessionPermissionProfile(ctx, sessionID, profile); err != nil {
		return nil, err
	}
	return emitOnce(domain.Event{
		Kind: domain.EventKindStatus,
		Text: fmt.Sprintf("permission profile set to %s", permissionprofile.DisplayName(profile)),
		Meta: map[string]string{"permission_profile": profile},
	}), nil
}

func (e *Engine) setPermissionProfileAndReevaluateApproval(ctx context.Context, sessionID, chatID, approvalID domain.ID, raw string) (<-chan domain.Event, error) {
	item, err := e.store.GetApproval(ctx, approvalID)
	if err != nil {
		session, chat, req, synthErr := e.syntheticApprovalRequest(ctx, sessionID, chatID, approvalID)
		if synthErr != nil {
			return nil, err
		}
		setEvents, err := e.setPermissionProfile(ctx, session.ID, chatID, raw)
		if err != nil {
			return nil, err
		}
		session, err = e.store.GetSession(ctx, session.ID)
		if err != nil {
			return nil, err
		}
		chat, err = e.store.GetChat(ctx, chat.ID)
		if err != nil {
			return nil, err
		}
		decision := permissionprofile.Decision{Mode: domain.PermissionModeAllow}
		if toolSpec, ok := tools.Lookup(req.Tool); !ok {
			return nil, fmt.Errorf("unsupported tool %q", req.Tool)
		} else if !toolSpec.BypassesPermission() {
			decision = permissionprofile.Evaluate(e.cfg.Permissions, e.effectivePermissionProfile(ctx, session, chat), session.PermissionRules, e.permissionRequest(session, req))
		}
		var next <-chan domain.Event
		switch decision.Mode {
		case domain.PermissionModeAllow:
			next, err = e.approveTool(ctx, session.ID, chat.ID, req.ToolCallID)
		case domain.PermissionModeDeny:
			next, err = e.denyTool(ctx, session.ID, chat.ID, req.ToolCallID)
		default:
			next = emitOnce(domain.Event{Kind: domain.EventKindStatus, Text: fmt.Sprintf("%s still requires approval", req.Tool)})
		}
		if err != nil {
			return nil, err
		}
		return concatEvents(setEvents, next), nil
	}
	targetSessionID := item.SessionID
	if sessionID != "" {
		targetSessionID = sessionID
	}
	targetChatID := chatID
	setEvents, err := e.setPermissionProfile(ctx, targetSessionID, targetChatID, raw)
	if err != nil {
		return nil, err
	}
	session, err := e.store.GetSession(ctx, item.SessionID)
	if err != nil {
		return nil, err
	}
	chat, err := e.store.GetChat(ctx, item.ChatID)
	if err != nil {
		return nil, err
	}
	req, err := requestFromStoredApproval(item.Tool, item.Command)
	if err != nil {
		return nil, err
	}

	decision := permissionprofile.Decision{Mode: domain.PermissionModeAllow}
	if toolSpec, ok := tools.Lookup(req.Tool); !ok {
		return nil, fmt.Errorf("unsupported tool %q", req.Tool)
	} else if !toolSpec.BypassesPermission() {
		decision = permissionprofile.Evaluate(e.cfg.Permissions, e.effectivePermissionProfile(ctx, session, chat), session.PermissionRules, e.permissionRequest(session, req))
	}

	var next <-chan domain.Event
	switch decision.Mode {
	case domain.PermissionModeAllow:
		next, err = e.approve(ctx, item.SessionID, item.ChatID, approvalID)
	case domain.PermissionModeDeny:
		next, err = e.deny(ctx, item.SessionID, item.ChatID, approvalID)
	default:
		status := fmt.Sprintf("%s still requires approval", item.Tool)
		if strings.TrimSpace(decision.Reason) != "" {
			status += ": " + decision.Reason
		}
		next = emitOnce(domain.Event{Kind: domain.EventKindStatus, Text: status})
	}
	if err != nil {
		return nil, err
	}
	return concatEvents(setEvents, next), nil
}

func (e *Engine) approveInChatWithRule(ctx context.Context, sessionID, chatID, approvalID domain.ID, rule domain.PermissionOverride) (<-chan domain.Event, error) {
	rule.Pattern = strings.TrimSpace(rule.Pattern)
	if rule.Pattern == "" {
		rule.Pattern = "*"
	}
	if err := permissionprofile.Validate(rule.Action); err != nil {
		return nil, err
	}
	item, err := e.store.GetApproval(ctx, approvalID)
	if err != nil {
		session, chat, req, synthErr := e.syntheticApprovalRequest(ctx, sessionID, chatID, approvalID)
		if synthErr != nil {
			return nil, err
		}
		targetSessionID := session.ID
		if sessionID != "" {
			targetSessionID = sessionID
		}
		if err := e.store.AddSessionPermissionRule(ctx, targetSessionID, rule); err != nil {
			return nil, err
		}
		setEvents := emitOnce(domain.Event{Kind: domain.EventKindStatus, Text: fmt.Sprintf("approved all %s requests matching %s for this session", rule.Tool, rule.Pattern)})
		session, err = e.store.GetSession(ctx, session.ID)
		if err != nil {
			return nil, err
		}
		decision := permissionprofile.Decision{Mode: domain.PermissionModeAllow}
		if toolSpec, ok := tools.Lookup(req.Tool); !ok {
			return nil, fmt.Errorf("unsupported tool %q", req.Tool)
		} else if !toolSpec.BypassesPermission() {
			decision = permissionprofile.Evaluate(e.cfg.Permissions, e.effectivePermissionProfile(ctx, session, chat), session.PermissionRules, e.permissionRequest(session, req))
		}
		var next <-chan domain.Event
		switch decision.Mode {
		case domain.PermissionModeAllow:
			next, err = e.approveTool(ctx, session.ID, chat.ID, req.ToolCallID)
		case domain.PermissionModeDeny:
			next, err = e.denyTool(ctx, session.ID, chat.ID, req.ToolCallID)
		default:
			next = emitOnce(domain.Event{Kind: domain.EventKindStatus, Text: fmt.Sprintf("%s still requires approval", req.Tool)})
		}
		if err != nil {
			return nil, err
		}
		return concatEvents(setEvents, next), nil
	}
	targetSessionID := item.SessionID
	if sessionID != "" {
		targetSessionID = sessionID
	}
	if err := e.store.AddSessionPermissionRule(ctx, targetSessionID, rule); err != nil {
		return nil, err
	}
	statusText := fmt.Sprintf("approved all %s requests matching %s for this session", rule.Tool, rule.Pattern)
	setEvents := emitOnce(domain.Event{
		Kind: domain.EventKindStatus,
		Text: statusText,
		Meta: map[string]string{
			"permission_tool":    string(rule.Tool),
			"permission_pattern": rule.Pattern,
		},
	})
	session, err := e.store.GetSession(ctx, item.SessionID)
	if err != nil {
		return nil, err
	}
	chat, err := e.store.GetChat(ctx, item.ChatID)
	if err != nil {
		return nil, err
	}
	req, err := requestFromStoredApproval(item.Tool, item.Command)
	if err != nil {
		return nil, err
	}
	decision := permissionprofile.Decision{Mode: domain.PermissionModeAllow}
	if toolSpec, ok := tools.Lookup(req.Tool); !ok {
		return nil, fmt.Errorf("unsupported tool %q", req.Tool)
	} else if !toolSpec.BypassesPermission() {
		decision = permissionprofile.Evaluate(e.cfg.Permissions, e.effectivePermissionProfile(ctx, session, chat), session.PermissionRules, e.permissionRequest(session, req))
	}
	var next <-chan domain.Event
	switch decision.Mode {
	case domain.PermissionModeAllow:
		next, err = e.approve(ctx, item.SessionID, item.ChatID, approvalID)
	case domain.PermissionModeDeny:
		next, err = e.deny(ctx, item.SessionID, item.ChatID, approvalID)
	default:
		next = emitOnce(domain.Event{Kind: domain.EventKindStatus, Text: fmt.Sprintf("%s still requires approval", item.Tool)})
	}
	if err != nil {
		return nil, err
	}
	return concatEvents(setEvents, next), nil
}

func (e *Engine) approveToolInChatWithRule(ctx context.Context, sessionID, chatID domain.ID, toolCallID string, rule domain.PermissionOverride) (<-chan domain.Event, error) {
	rule.Pattern = strings.TrimSpace(rule.Pattern)
	if rule.Pattern == "" {
		rule.Pattern = "*"
	}
	if err := permissionprofile.Validate(rule.Action); err != nil {
		return nil, err
	}
	session, err := e.store.GetSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	chat, err := e.store.GetChat(ctx, chatID)
	if err != nil {
		return nil, err
	}
	req, err := e.requestForToolCall(ctx, chat.ID, toolCallID)
	if err != nil {
		return nil, err
	}
	if err := e.store.AddSessionPermissionRule(ctx, session.ID, rule); err != nil {
		return nil, err
	}
	session, err = e.store.GetSession(ctx, session.ID)
	if err != nil {
		return nil, err
	}
	statusText := fmt.Sprintf("approved all %s requests matching %s for this session", rule.Tool, rule.Pattern)
	setEvents := emitOnce(domain.Event{
		Kind: domain.EventKindStatus,
		Text: statusText,
		Meta: map[string]string{
			"permission_tool":    string(rule.Tool),
			"permission_pattern": rule.Pattern,
		},
	})
	decision := permissionprofile.Decision{Mode: domain.PermissionModeAllow}
	if toolSpec, ok := tools.Lookup(req.Tool); !ok {
		return nil, fmt.Errorf("unsupported tool %q", req.Tool)
	} else if !toolSpec.BypassesPermission() {
		decision = permissionprofile.Evaluate(e.cfg.Permissions, e.effectivePermissionProfile(ctx, session, chat), session.PermissionRules, e.permissionRequest(session, req))
	}
	var next <-chan domain.Event
	switch decision.Mode {
	case domain.PermissionModeAllow:
		next, err = e.approveTool(ctx, session.ID, chat.ID, req.ToolCallID)
	case domain.PermissionModeDeny:
		next, err = e.denyTool(ctx, session.ID, chat.ID, req.ToolCallID)
	default:
		next = emitOnce(domain.Event{Kind: domain.EventKindStatus, Text: fmt.Sprintf("%s still requires approval", req.Tool)})
	}
	if err != nil {
		return nil, err
	}
	return concatEvents(setEvents, next), nil
}

func (e *Engine) approve(ctx context.Context, sessionID, chatID domain.ID, rawID string) (<-chan domain.Event, error) {
	id := domain.ID(strings.TrimSpace(rawID))
	if id == "" {
		return nil, fmt.Errorf("approval id is required")
	}
	item, err := e.store.GetApproval(ctx, id)
	if err != nil {
		session, chat, req, findErr := e.syntheticApprovalRequest(ctx, sessionID, chatID, id)
		if findErr != nil {
			return nil, err
		}
		return e.approveTool(ctx, session.ID, chat.ID, req.ToolCallID)
	}
	if err := e.store.UpdateApproval(ctx, id, domain.ApprovalStatusApproved); err != nil {
		return nil, err
	}
	req, err := requestFromStoredApproval(item.Tool, item.Command)
	if err != nil {
		return nil, err
	}
	session, err := e.store.GetSession(ctx, item.SessionID)
	if err != nil {
		return nil, err
	}
	if !toolEnabledForSession(e.cfg, session, req.Tool) {
		if err := e.store.UpdateApproval(ctx, id, domain.ApprovalStatusDenied); err != nil {
			return nil, err
		}
		text := fmt.Sprintf("%s disabled for this session", req.Tool)
		replyItem, err := e.recordApprovalReply(ctx, item.ChatID, item.SessionID, item.Tool, id, "denied", text, req.ToolCallID)
		if err != nil {
			return nil, err
		}
		return emitOnce(domain.Event{Kind: domain.EventKindApprovalReply, Text: text, Tool: req.Tool, Item: replyItem, Meta: map[string]string{"approval_id": id}}), nil
	}
	chat, chatErr := e.store.GetChat(ctx, item.ChatID)
	if chatErr != nil {
		return nil, chatErr
	}
	if !chatrole.AllowsTool(chat.WorkflowRole, req.Tool) {
		if err := e.store.UpdateApproval(ctx, id, domain.ApprovalStatusDenied); err != nil {
			return nil, err
		}
		text := fmt.Sprintf("%s is not available to %s chats", req.Tool, chat.WorkflowRole)
		replyItem, err := e.recordApprovalReply(ctx, item.ChatID, item.SessionID, item.Tool, id, "denied", text, req.ToolCallID)
		if err != nil {
			return nil, err
		}
		return emitOnce(domain.Event{Kind: domain.EventKindApprovalReply, Text: text, Tool: req.Tool, Item: replyItem, Meta: map[string]string{"approval_id": id}}), nil
	}
	replyItem, err := e.recordApprovalReply(ctx, item.ChatID, sessionID, item.Tool, id, "approved", tools.Preview(req), req.ToolCallID)
	if err != nil {
		return nil, err
	}
	e.recordLifecycle(sessionID, "tool_execution_started", item.Command, map[string]string{"tool": string(item.Tool), "approval_id": id})
	result, execErr := e.registry.ExecuteWithChat(ctx, e.store, sessionID, chat, req)
	if execErr != nil {
		e.recordLifecycle(sessionID, "tool_execution_failed", execErr.Error(), map[string]string{"tool": string(item.Tool), "approval_id": id})
		if interruptedErr(execErr) {
			return emitOnce(domain.Event{Kind: domain.EventKindStatus, Text: "Interrupted"}), nil
		}
		toolEvents, err := e.persistToolFailure(ctx, item.ChatID, sessionID, req, execErr)
		if err != nil {
			return nil, err
		}
		out := make(chan domain.Event)
		go func() {
			defer close(out)
			out <- domain.Event{Kind: domain.EventKindApprovalReply, Text: fmt.Sprintf("approval %s approved", id), Tool: req.Tool, Item: replyItem, Meta: map[string]string{"approval_id": id}}
			for evt := range toolEvents {
				out <- evt
			}
			session, err = e.store.GetSession(ctx, sessionID)
			if err != nil {
				if interruptedErr(err) {
					e.emitInterrupted(out, item.ChatID, sessionID)
					return
				}
				out <- domain.Event{Kind: domain.EventKindError, Err: err}
				return
			}
			providerCfg, ok := e.cfg.Provider(session.ProviderID)
			if !ok {
				out <- domain.Event{Kind: domain.EventKindError, Err: fmt.Errorf("provider %q not found", session.ProviderID)}
				return
			}
			client, err := provider.New(session.ProviderID, providerCfg, e.debug)
			if err != nil {
				out <- domain.Event{Kind: domain.EventKindError, Err: err}
				return
			}
			if err := e.continueModelTurn(ctx, session, chat, client, out, nil); err != nil {
				if interruptedErr(err) {
					e.emitInterrupted(out, item.ChatID, session.ID)
					return
				}
				e.recordAssistantError(ctx, item.ChatID, session.ID, err)
				out <- domain.Event{Kind: domain.EventKindError, Err: err}
			}
		}()
		return out, nil
	}
	e.recordLifecycle(sessionID, "tool_execution_finished", item.Command, map[string]string{"tool": string(item.Tool), "approval_id": id})
	toolEvents, err := e.persistToolResult(ctx, item.ChatID, sessionID, req, result)
	if err != nil {
		return nil, err
	}
	session, err = e.store.GetSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	providerCfg, ok := e.cfg.Provider(session.ProviderID)
	if !ok {
		return nil, fmt.Errorf("provider %q not found", session.ProviderID)
	}
	client, err := provider.New(session.ProviderID, providerCfg, e.debug)
	if err != nil {
		return nil, err
	}
	out := make(chan domain.Event)
	go func() {
		defer close(out)
		out <- domain.Event{Kind: domain.EventKindApprovalReply, Text: fmt.Sprintf("approval %s approved", id), Tool: req.Tool, Item: replyItem, Meta: map[string]string{"approval_id": id}}
		for evt := range toolEvents {
			out <- evt
		}
		if turncontrol.ShouldStop(ctx) {
			e.emitInterrupted(out, item.ChatID, session.ID)
			return
		}
		compacted, err := e.autoCompactChatIfNeeded(ctx, session, item.ChatID, client, out)
		if err != nil {
			if interruptedErr(err) {
				e.emitInterrupted(out, item.ChatID, session.ID)
				return
			}
			e.recordAssistantError(ctx, item.ChatID, session.ID, err)
			out <- domain.Event{Kind: domain.EventKindError, Err: err}
			return
		}
		if compacted {
			session, err = e.store.GetSession(ctx, session.ID)
			if err != nil {
				if interruptedErr(err) {
					e.emitInterrupted(out, item.ChatID, session.ID)
					return
				}
				out <- domain.Event{Kind: domain.EventKindError, Err: err}
				return
			}
		}
		var transient []provider.InstructionBlock
		if compacted {
			transient = transientTurnMessages("", "Continue from the compacted session summary. Do not restart, greet, or restate the summary. Continue the pending task from the latest tool result.")
		}
		if err := e.continueModelTurn(ctx, session, domain.Chat{ID: item.ChatID}, client, out, transient); err != nil {
			if interruptedErr(err) {
				e.emitInterrupted(out, item.ChatID, session.ID)
				return
			}
			e.recordAssistantError(ctx, item.ChatID, session.ID, err)
			out <- domain.Event{Kind: domain.EventKindError, Err: err}
		}
	}()
	return out, nil
}

func (e *Engine) approveTool(ctx context.Context, sessionID, chatID domain.ID, toolCallID string) (<-chan domain.Event, error) {
	req, err := e.requestForToolCall(ctx, chatID, toolCallID)
	if err != nil {
		return nil, err
	}
	if _, err := e.store.MarkToolRunning(ctx, chatID, req.ToolCallID); err != nil {
		return nil, err
	}
	e.recordLifecycle(sessionID, "tool_execution_started", req.ContextString(), map[string]string{"tool": string(req.Tool), "tool_call_id": req.ToolCallID})
	chat, err := e.store.GetChat(ctx, chatID)
	if err != nil {
		return nil, err
	}
	result, execErr := e.registry.ExecuteWithChat(ctx, e.store, sessionID, chat, req)
	if execErr != nil {
		e.recordLifecycle(sessionID, "tool_execution_failed", execErr.Error(), map[string]string{"tool": string(req.Tool), "tool_call_id": req.ToolCallID})
		if interruptedErr(execErr) {
			return emitOnce(domain.Event{Kind: domain.EventKindStatus, Text: "Interrupted"}), nil
		}
		toolEvents, err := e.persistToolFailure(ctx, chatID, sessionID, req, execErr)
		if err != nil {
			return nil, err
		}
		session, err := e.store.GetSession(ctx, sessionID)
		if err != nil {
			return nil, err
		}
		providerCfg, ok := e.cfg.Provider(session.ProviderID)
		if !ok {
			return nil, fmt.Errorf("provider %q not found", session.ProviderID)
		}
		client, err := provider.New(session.ProviderID, providerCfg, e.debug)
		if err != nil {
			return nil, err
		}
		out := make(chan domain.Event)
		go func() {
			defer close(out)
			for evt := range toolEvents {
				out <- evt
			}
			if err := e.continueModelTurn(ctx, session, domain.Chat{ID: chatID}, client, out, nil); err != nil {
				if interruptedErr(err) {
					e.emitInterrupted(out, chatID, session.ID)
					return
				}
				e.recordAssistantError(ctx, chatID, session.ID, err)
				out <- domain.Event{Kind: domain.EventKindError, Err: err}
			}
		}()
		return out, nil
	}
	e.recordLifecycle(sessionID, "tool_execution_finished", req.ContextString(), map[string]string{"tool": string(req.Tool), "tool_call_id": req.ToolCallID})
	toolEvents, err := e.persistToolResult(ctx, chatID, sessionID, req, result)
	if err != nil {
		return nil, err
	}
	session, err := e.store.GetSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	providerCfg, ok := e.cfg.Provider(session.ProviderID)
	if !ok {
		return nil, fmt.Errorf("provider %q not found", session.ProviderID)
	}
	client, err := provider.New(session.ProviderID, providerCfg, e.debug)
	if err != nil {
		return nil, err
	}
	out := make(chan domain.Event)
	go func() {
		defer close(out)
		for evt := range toolEvents {
			out <- evt
		}
		if turncontrol.ShouldStop(ctx) {
			e.emitInterrupted(out, chatID, session.ID)
			return
		}
		compacted, err := e.autoCompactChatIfNeeded(ctx, session, chatID, client, out)
		if err != nil {
			if interruptedErr(err) {
				e.emitInterrupted(out, chatID, session.ID)
				return
			}
			e.recordAssistantError(ctx, chatID, session.ID, err)
			out <- domain.Event{Kind: domain.EventKindError, Err: err}
			return
		}
		var transient []provider.InstructionBlock
		if compacted {
			session, err = e.store.GetSession(ctx, session.ID)
			if err != nil {
				out <- domain.Event{Kind: domain.EventKindError, Err: err}
				return
			}
			transient = transientTurnMessages("", "Continue from the compacted session summary. Do not restart, greet, or restate the summary. Continue the pending task from the latest tool result.")
		}
		if err := e.continueModelTurn(ctx, session, domain.Chat{ID: chatID}, client, out, transient); err != nil {
			if interruptedErr(err) {
				e.emitInterrupted(out, chatID, session.ID)
				return
			}
			e.recordAssistantError(ctx, chatID, session.ID, err)
			out <- domain.Event{Kind: domain.EventKindError, Err: err}
		}
	}()
	return out, nil
}

func (e *Engine) denyTool(ctx context.Context, sessionID, chatID domain.ID, toolCallID string) (<-chan domain.Event, error) {
	req, err := e.requestForToolCall(ctx, chatID, toolCallID)
	if err != nil {
		return nil, err
	}
	text := fmt.Sprintf("%s denied", req.Tool)
	item, err := e.recordDeniedToolResult(ctx, chatID, req, text)
	if err != nil {
		return nil, err
	}
	return emitOnce(domain.Event{Kind: domain.EventKindToolResult, Text: text, Tool: req.Tool, ToolCallID: req.ToolCallID, Item: item}), nil
}

func (e *Engine) deny(ctx context.Context, sessionID, chatID domain.ID, rawID string) (<-chan domain.Event, error) {
	id := domain.ID(strings.TrimSpace(rawID))
	if id == "" {
		return nil, fmt.Errorf("approval id is required")
	}
	item, err := e.store.GetApproval(ctx, id)
	if err != nil {
		session, chat, req, findErr := e.syntheticApprovalRequest(ctx, sessionID, chatID, id)
		if findErr != nil {
			return nil, err
		}
		return e.denyTool(ctx, session.ID, chat.ID, req.ToolCallID)
	}
	if err := e.store.UpdateApproval(ctx, id, domain.ApprovalStatusDenied); err != nil {
		return nil, err
	}
	toolCallID := ""
	if req, err := requestFromStoredApproval(item.Tool, item.Command); err == nil {
		toolCallID = req.ToolCallID
	}
	replyItem, err := e.recordApprovalReply(ctx, item.ChatID, item.SessionID, item.Tool, id, "denied", approvalPreviewFromStored(item.Tool, item.Command), toolCallID)
	if err != nil {
		return nil, err
	}
	return emitOnce(domain.Event{Kind: domain.EventKindApprovalReply, Text: fmt.Sprintf("approval %s denied", id), Item: replyItem, Meta: map[string]string{"approval_id": id}}), nil
}

func (e *Engine) persistToolResult(ctx context.Context, chatID, sessionID domain.ID, req tools.Request, result tools.Result) (<-chan domain.Event, error) {
	beforePlan, trackMilestones := e.milestonePlanForNotification(ctx, sessionID, req.Tool)
	events, err := e.registry.PersistResultInChat(ctx, e.store, sessionID, chatID, req, result)
	if err != nil {
		return nil, err
	}
	if trackMilestones {
		e.notifyMilestoneChanges(ctx, chatID, beforePlan)
	}
	summary, _ := tools.SummarizeResult(req, result)
	e.recordLifecycle(sessionID, "tool_result_persisted", summary, map[string]string{"tool": string(req.Tool)})
	return events, nil
}

func (e *Engine) milestonePlanForNotification(ctx context.Context, sessionID domain.ID, tool domain.ToolKind) (store.MilestonePlan, bool) {
	switch tool {
	case domain.ToolKindMilestoneUpdate, domain.ToolKindMilestonePlan, domain.ToolKindMilestoneWrite, domain.ToolKindMilestoneAdd:
	default:
		return store.MilestonePlan{}, false
	}
	plan, err := e.store.GetMilestonePlan(ctx, sessionID)
	if err != nil {
		return store.MilestonePlan{}, false
	}
	return plan, true
}

func (e *Engine) notifyMilestoneChanges(ctx context.Context, chatID domain.ID, before store.MilestonePlan) {
	chatRecord, err := e.store.GetChat(ctx, chatID)
	if err != nil || chatRecord.ParentChatID == nil {
		return
	}
	after, err := e.store.GetMilestonePlan(ctx, chatRecord.SessionID)
	if err != nil {
		return
	}
	beforeByRef := make(map[string]store.Milestone, len(before.Milestones))
	for _, milestone := range before.Milestones {
		beforeByRef[milestone.Ref] = milestone
	}
	for _, milestone := range after.Milestones {
		prev, ok := beforeByRef[milestone.Ref]
		if !ok || prev.Status == milestone.Status {
			continue
		}
		text := fmt.Sprintf("Milestone %s changed from %s to %s by chat %s.", milestone.Ref, prev.Status, milestone.Status, chatID)
		e.enqueueSteer(ctx, *chatRecord.ParentChatID, text)
	}
}

func (e *Engine) persistToolFailure(ctx context.Context, chatID, sessionID domain.ID, req tools.Request, execErr error) (<-chan domain.Event, error) {
	if execErr == nil {
		return nil, errors.New("tool failure error is nil")
	}
	text := fmt.Sprintf("%s failed: %v", req.Tool, execErr)
	if sessionID == "" {
		return emitOnce(domain.Event{Kind: domain.EventKindToolResult, Tool: req.Tool, ToolCallID: req.ToolCallID, Text: text}), nil
	}
	var item domain.TimelineItem
	var err error
	if strings.TrimSpace(req.ToolCallID) != "" {
		item, err = e.store.AttachToolError(ctx, chatID, req.ToolCallID, domain.ToolError{Message: text})
	} else {
		now := time.Now().UTC()
		item, err = e.store.AppendTimeline(ctx, chatID, domain.ToolExecution{
			Tool: req.Tool,
			Args: req.Args,
			Error: &domain.ToolError{
				Message: text,
			},
			StartedAt: now,
			EndedAt:   now,
		})
		if err == nil {
			item.Seal(now)
			err = e.store.Timeline().Put(ctx, item)
		}
	}
	if err != nil {
		return nil, err
	}
	e.recordLifecycle(sessionID, "tool_result_persisted", text, map[string]string{"tool": string(req.Tool), "status": "error"})
	return emitOnce(domain.Event{Kind: domain.EventKindToolResult, Tool: req.Tool, ToolCallID: req.ToolCallID, Text: text, Item: item}), nil
}

func emitOnce(evt domain.Event) <-chan domain.Event {
	out := make(chan domain.Event, 1)
	out <- evt
	close(out)
	return out
}

func concatEvents(streams ...<-chan domain.Event) <-chan domain.Event {
	out := make(chan domain.Event)
	go func() {
		defer close(out)
		for _, stream := range streams {
			if stream == nil {
				continue
			}
			for evt := range stream {
				out <- evt
			}
		}
	}()
	return out
}

func (e *Engine) recordAssistantError(ctx context.Context, chatID, sessionID domain.ID, err error) {
	if err == nil || sessionID == "" {
		return
	}
	if interruptedErr(err) {
		return
	}
	e.recordLifecycle(sessionID, "assistant_error", err.Error(), nil)
	e.persistTranscriptNotice(ctx, chatID, sessionID, errorSummary(err), transcriptNotice{
		Kind:     "model_error",
		Severity: "error",
	})
}

func (e *Engine) recordLifecycle(sessionID domain.ID, kind, text string, meta map[string]string) {
	if e.debug == nil {
		return
	}
	e.debug.RecordLifecycle(sessionID, kind, text, meta)
}

func (e *Engine) emitInterrupted(out chan<- domain.Event, chatID, sessionID domain.ID) {
	e.recordLifecycle(sessionID, "interrupted", "Interrupted", nil)
	item, ok := e.persistTranscriptNotice(context.Background(), chatID, sessionID, "Interrupted", transcriptNotice{
		Kind:     "interrupted",
		Severity: "warning",
		Reason:   domain.NoticeReasonUserInterrupted,
	})
	evt := domain.Event{Kind: domain.EventKindStatus, Text: "Interrupted"}
	if ok {
		evt.Item = item
	}
	out <- evt
}

func interruptedErr(err error) bool {
	return errors.Is(err, context.Canceled)
}

func errorSummary(err error) string {
	return "Error: " + strings.TrimSpace(err.Error())
}

type transcriptNotice struct {
	Kind     string `json:"kind,omitempty"`
	Severity string `json:"severity,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Title    string `json:"title,omitempty"`
	Subtitle string `json:"subtitle,omitempty"`
	Tool     string `json:"tool,omitempty"`
	Count    int    `json:"count,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

type continuationPauseReason string

const (
	continuationPauseReasonRepeatedTool    continuationPauseReason = "repeated_tool"
	continuationPauseReasonTurnLimit       continuationPauseReason = "turn_limit"
	continuationPauseReasonProviderRefusal continuationPauseReason = "provider_refusal"
)

const repeatedToolLoopThreshold = 3

type continuationPause struct {
	Reason   continuationPauseReason
	Tool     domain.ToolKind
	Count    int
	Limit    int
	Body     string
	Subtitle string
}

type toolLoopTracker struct {
	lastSignature string
	lastTool      domain.ToolKind
	repeatCount   int
}

func (t *toolLoopTracker) reset() {
	t.lastSignature = ""
	t.lastTool = ""
	t.repeatCount = 0
}

func (t *toolLoopTracker) trackCalls(calls []tools.Request) (continuationPause, bool) {
	if len(calls) != 1 {
		t.reset()
		return continuationPause{}, false
	}
	signature := toolLoopSignature(calls[0])
	if signature == "" {
		t.reset()
		return continuationPause{}, false
	}
	if signature == t.lastSignature {
		t.repeatCount++
	} else {
		t.lastSignature = signature
		t.lastTool = calls[0].Tool
		t.repeatCount = 1
	}
	if t.repeatCount < repeatedToolLoopThreshold {
		return continuationPause{}, false
	}
	return continuationPause{
		Reason:   continuationPauseReasonRepeatedTool,
		Tool:     calls[0].Tool,
		Count:    t.repeatCount,
		Subtitle: fmt.Sprintf("Repeated identical %s calls", calls[0].Tool),
		Body: fmt.Sprintf(
			"Paused continuation after %d identical %s calls with the same input. The model kept retrying the same tool instead of reacting to the result.",
			t.repeatCount,
			calls[0].Tool,
		),
	}, true
}

func toolLoopSignature(req tools.Request) string {
	return string(req.Tool) + "\x00" + req.ArgumentsJSON()
}

func providerRefusalPauseBody(reasoning string) string {
	body := "Paused continuation because the provider ended the turn without any text or tool call after tool results."
	if strings.TrimSpace(reasoning) == "" {
		return body
	}
	return body + "\n\nProvider reasoning:\n" + strings.TrimSpace(reasoning)
}

func (e *Engine) pauseContinuation(ctx context.Context, chatID, sessionID domain.ID, pause continuationPause, out chan<- domain.Event) {
	body := strings.TrimSpace(pause.Body)
	if body == "" {
		body = "Paused continuation."
	}
	title := "Continuation paused"
	subtitle := strings.TrimSpace(pause.Subtitle)
	if subtitle == "" {
		subtitle = continuationPauseSubtitle(pause)
	}
	e.recordLifecycle(sessionID, "model_turn_paused", body, map[string]string{
		"reason": string(pause.Reason),
		"tool":   string(pause.Tool),
		"count":  strconv.Itoa(pause.Count),
		"limit":  strconv.Itoa(pause.Limit),
	})
	item, ok := e.persistTranscriptNotice(ctx, chatID, sessionID, body, transcriptNotice{
		Kind:     "loop_pause",
		Severity: "warning",
		Reason:   string(pause.Reason),
		Title:    title,
		Subtitle: subtitle,
		Tool:     string(pause.Tool),
		Count:    pause.Count,
		Limit:    pause.Limit,
	})
	if out != nil {
		evt := domain.Event{Kind: domain.EventKindStatus, Text: body}
		if ok {
			evt.Item = item
		}
		out <- evt
		out <- domain.Event{Kind: domain.EventKindMessageDone}
	}
}

func continuationPauseSubtitle(pause continuationPause) string {
	switch pause.Reason {
	case continuationPauseReasonRepeatedTool:
		if pause.Tool != "" {
			return fmt.Sprintf("Repeated identical %s calls", pause.Tool)
		}
		return "Repeated identical tool calls"
	case continuationPauseReasonTurnLimit:
		if pause.Limit > 0 {
			return fmt.Sprintf("Turn limit reached (%d)", pause.Limit)
		}
		return "Turn limit reached"
	case continuationPauseReasonProviderRefusal:
		return "Provider stopped continuation"
	default:
		return "Continuation stopped"
	}
}

func (e *Engine) persistTranscriptNotice(ctx context.Context, chatID, sessionID domain.ID, body string, meta transcriptNotice) (domain.TimelineItem, bool) {
	if sessionID == "" || chatID == "" || e.store == nil {
		return domain.TimelineItem{}, false
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return domain.TimelineItem{}, false
	}
	item, err := e.store.AppendTimeline(ctx, chatID, domain.Notice{
		Level:    strings.TrimSpace(meta.Severity),
		Text:     body,
		Kind:     strings.TrimSpace(meta.Kind),
		Reason:   strings.TrimSpace(meta.Reason),
		Title:    strings.TrimSpace(meta.Title),
		Subtitle: strings.TrimSpace(meta.Subtitle),
		Tool:     domain.ToolKind(meta.Tool),
		Count:    meta.Count,
		Limit:    meta.Limit,
	})
	if err != nil {
		return domain.TimelineItem{}, false
	}
	item.Seal(time.Now().UTC())
	if err := e.store.Timeline().Put(ctx, item); err != nil {
		return domain.TimelineItem{}, false
	}
	return item, true
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

func (e *Engine) chatWithRetry(ctx context.Context, sessionID domain.ID, client *provider.Client, out chan<- domain.Event, req provider.ChatRequest, streamItem domain.TimelineItem) (provider.ChatResponse, bool, error) {
	for attempt := 0; ; attempt++ {
		var (
			resp           provider.ChatResponse
			err            error
			streamed       bool
			receivedStream bool
		)
		if req.Stream {
			resp, err = client.StreamChatResponse(ctx, req, func(evt domain.Event) {
				receivedStream = true
				if (evt.Kind == domain.EventKindMessageDelta || evt.Kind == domain.EventKindReasoning) && evt.Item.ID == "" {
					evt.Item = streamItem
				}
				if out != nil {
					out <- evt
				}
			})
			streamed = true
		} else {
			resp, err = client.CompleteChat(ctx, req)
		}
		if err == nil {
			return resp, streamed, nil
		}
		var apiErr *provider.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == 429 {
			if attempt >= maxRateLimitRetries {
				return provider.ChatResponse{}, streamed, err
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
				return provider.ChatResponse{}, streamed, err
			}
			continue
		}
		if !shouldRetryTransientChatError(err, req.Stream, receivedStream) || attempt >= maxTransientChatRetries {
			return provider.ChatResponse{}, streamed, err
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
			return provider.ChatResponse{}, streamed, err
		}
	}
}

func (e *Engine) nextAssistantTimelineItem(ctx context.Context, chatID domain.ID) (domain.TimelineItem, error) {
	now := time.Now().UTC()
	items, err := e.store.TimelineForChat(ctx, chatID)
	if err != nil {
		return domain.TimelineItem{}, err
	}
	return domain.TimelineItem{
		ID:        domain.NewTimelineID(now),
		ChatID:    chatID,
		Seq:       int64(len(items) + 1),
		Content:   domain.AssistantMessage{},
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
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

func transientTurnMessages(note string, continuePrompt string) []provider.InstructionBlock {
	var out []provider.InstructionBlock
	if strings.TrimSpace(note) != "" {
		out = append(out, provider.InstructionBlock{
			Kind:      provider.InstructionKindSessionNote,
			Text:      "Session update:\n" + strings.TrimSpace(note),
			Ephemeral: true,
		})
	}
	if strings.TrimSpace(continuePrompt) != "" {
		out = append(out, provider.InstructionBlock{
			Kind:      provider.InstructionKindContinuation,
			Text:      strings.TrimSpace(continuePrompt),
			Ephemeral: true,
		})
	}
	return out
}

func (e *Engine) buildConversation(ctx context.Context, sessionID, chatID domain.ID) ([]provider.Message, error) {
	session, err := e.store.GetSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return e.buildConversationPreview(ctx, session, chatID, "", nil, nil, nil)
}

func (e *Engine) buildConversationPreview(ctx context.Context, session domain.Session, chatID domain.ID, prompt string, drafts []attachment.Draft, refs []reference.Draft, transient []provider.InstructionBlock) ([]provider.Message, error) {
	envelope, err := e.buildPromptEnvelopePreview(ctx, session, chatID, prompt, drafts, refs, transient)
	if err != nil {
		return nil, err
	}
	return provider.SerializePromptEnvelope(envelope), nil
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

func (e *Engine) buildPromptEnvelopePreview(ctx context.Context, session domain.Session, chatID domain.ID, prompt string, drafts []attachment.Draft, refs []reference.Draft, transient []provider.InstructionBlock) (provider.PromptEnvelope, error) {
	chat := domain.Chat{WorkflowRole: chatrole.General}
	if chatID != "" {
		stored, err := e.store.GetChat(ctx, chatID)
		if err != nil {
			return provider.PromptEnvelope{}, err
		}
		chat = stored
	}
	var timeline []domain.TimelineItem
	if chatID != "" {
		var err error
		timeline, err = e.store.TimelineForChat(ctx, chatID)
		if err != nil {
			return provider.PromptEnvelope{}, err
		}
	}
	return e.buildPromptEnvelopeForTimeline(session, chat, timeline, prompt, drafts, refs, transient)
}

func (e *Engine) buildPromptEnvelopeForTimeline(session domain.Session, chat domain.Chat, timeline []domain.TimelineItem, prompt string, drafts []attachment.Draft, refs []reference.Draft, transient []provider.InstructionBlock) (provider.PromptEnvelope, error) {
	baseInstructions := e.baseInstructionsForChat(session, chat)
	envelope := provider.PromptEnvelope{Instructions: baseInstructions}
	segmentStart := 0
	for idx, item := range timeline {
		if compacted, ok := item.Content.(domain.Compaction); ok {
			if strings.TrimSpace(compacted.Summary) == "" {
				segmentStart = idx + 1
				continue
			}
			envelope.Instructions = baseInstructions
			envelope.Items = append(envelope.Items[:0], compactedHistoryMessage(compacted.Summary))
			if segmentStart < idx {
				preserved, err := e.timelineMessagesForCompactionTail(session, timeline[segmentStart:idx], compacted.FirstKeptItemID)
				if err != nil {
					return provider.PromptEnvelope{}, err
				}
				envelope.Items = append(envelope.Items, preserved...)
			}
			segmentStart = idx + 1
			continue
		}
		messages, err := e.conversationMessagesForTimelineItem(session, item, e.preserveThinkingEnabled(session))
		if err != nil {
			return provider.PromptEnvelope{}, err
		}
		envelope.Items = append(envelope.Items, messages...)
	}
	envelope.Instructions = append(envelope.Instructions, transient...)
	if strings.TrimSpace(prompt) != "" || len(drafts) > 0 {
		msg, ok, err := e.previewUserMessage(prompt, drafts, refs)
		if err != nil {
			return provider.PromptEnvelope{}, err
		}
		if ok {
			envelope.Items = append(envelope.Items, msg)
		}
	}
	return envelope, nil
}

func (e *Engine) timelineMessagesForCompactionTail(session domain.Session, items []domain.TimelineItem, firstKeptItemID string) ([]provider.Message, error) {
	start := firstKeptTimelineIndex(items, firstKeptItemID)
	if start < 0 {
		start = preservedTimelineToolTailStart(items, e.compactionKeepToolBatches())
	}
	if start >= len(items) {
		return nil, nil
	}
	out := make([]provider.Message, 0, len(items)-start)
	for _, item := range items[start:] {
		messages, err := e.conversationMessagesForTimelineItem(session, item, e.preserveThinkingEnabled(session))
		if err != nil {
			return nil, err
		}
		out = append(out, messages...)
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

func preservedTimelineToolTailStart(items []domain.TimelineItem, keepBatches int) int {
	if keepBatches <= 0 || len(items) == 0 {
		return len(items)
	}
	starts := make([]int, 0, keepBatches)
	for idx, item := range items {
		assistant, ok := item.Content.(domain.AssistantMessage)
		if !ok || len(assistant.Tools) == 0 {
			continue
		}
		starts = append(starts, idx)
	}
	if len(starts) == 0 {
		return len(items)
	}
	if keepBatches >= len(starts) {
		return starts[0]
	}
	return starts[len(starts)-keepBatches]
}

func (e *Engine) conversationMessagesForTimelineItem(session domain.Session, item domain.TimelineItem, preserveThinking bool) ([]provider.Message, error) {
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
		msg, ok, err := e.userMessageWithContext(parts)
		if err != nil {
			return nil, err
		}
		if ok {
			return []provider.Message{msg}, nil
		}
		if strings.TrimSpace(content.Text) == "" {
			return nil, nil
		}
		return []provider.Message{{Role: domain.MessageRoleUser, Content: content.Text}}, nil
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
		if preserveThinking && strings.TrimSpace(content.Reasoning.Text) != "" {
			reasoningChunks = append(reasoningChunks, content.Reasoning.Text)
		}
		if strings.TrimSpace(content.Text) != "" {
			textChunks = append(textChunks, content.Text)
		}
		out := []provider.Message{{
			Role:      domain.MessageRoleAssistant,
			Content:   assistantConversationContent(textChunks, reasoningChunks, preserveThinking),
			ToolCalls: toolCalls,
		}}
		if strings.TrimSpace(out[0].Content) == "" && len(out[0].ToolCalls) == 0 {
			out = out[:0]
		}
		for _, tool := range content.Tools {
			msg, ok := e.timelineToolResultMessage(session, tool)
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
		return []provider.Message{{Role: domain.MessageRoleUser, Content: fmt.Sprintf("%s output:\n%s", content.Tool, body)}}, nil
	case domain.Notice:
		return nil, nil
	default:
		return nil, fmt.Errorf("unsupported timeline item %s content %T", item.ID, item.Content)
	}
}

func (e *Engine) timelineToolResultMessage(session domain.Session, tool domain.ToolCall) (provider.Message, bool) {
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
		data = domain.ErrorStoredResult{Message: tool.Error.Message}
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
	if imageMsg, ok := e.toolImageMessage(session, part, string(tool.ToolCallID), text); ok {
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
	return provider.Message{Role: domain.MessageRoleTool, Content: body, ToolCallID: string(tool.ToolCallID)}, true
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
	if skillText := strings.TrimSpace(skills.PromptContext(e.workdir)); skillText != "" {
		instructions = append(instructions, provider.InstructionBlock{
			Kind: provider.InstructionKindSkills,
			Text: skillText,
		})
	}
	return instructions
}

func (e *Engine) toolRuntime(session domain.Session, chat domain.Chat) tools.Runtime {
	return tools.Runtime{
		Workdir:               e.workdir,
		Store:                 e.store,
		SessionID:             session.ID,
		ChatID:                chat.ID,
		ChatRole:              chat.WorkflowRole,
		ActiveMilestoneRef:    chat.ActiveMilestoneRef,
		AssignedTodoBucketRef: chat.AssignedTodoBucketRef,
		MCP:                   e.mcp,
	}
}

func compactedHistoryMessage(summary string) provider.Message {
	return provider.Message{
		Role: domain.MessageRoleUser,
		Content: strings.TrimSpace(
			"Compacted session summary for continuation:\n" +
				summary +
				"\n\nUse this summary as replacement history for the earlier conversation. Continue the task from the preserved context instead of restarting.",
		),
	}
}

func (e *Engine) previewUserMessage(prompt string, drafts []attachment.Draft, refs []reference.Draft) (provider.Message, bool, error) {
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
	if msg, ok, err := e.userMessageWithContext(parts); ok || err != nil {
		return msg, ok, err
	}
	if len(parts) == 0 {
		return provider.Message{}, false, nil
	}
	return provider.Message{
		Role:    domain.MessageRoleUser,
		Content: strings.TrimSpace(prompt),
	}, true, nil
}

func (e *Engine) userMessageWithContext(parts []domain.Part) (provider.Message, bool, error) {
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
			resolved, err := e.resolveReference(ref)
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
	message := provider.Message{Role: domain.MessageRoleUser, ContentParts: contentParts}
	if len(contentParts) == 0 && strings.TrimSpace(prompt) != "" {
		message.Content = prompt
	}
	return message, true, nil
}

func (e *Engine) resolveReference(meta reference.Metadata) (string, error) {
	switch meta.Kind {
	case reference.KindFile:
		return reference.ResolveFile(e.workdir, meta)
	case reference.KindDirectory:
		return reference.ResolveDirectory(e.workdir, meta)
	default:
		return "", fmt.Errorf("unsupported reference kind %q", meta.Kind)
	}
}

func (e *Engine) toolImageMessage(session domain.Session, part domain.Part, toolCallID string, body string) (provider.Message, bool) {
	stored, ok := tools.ViewImageStoredResultForPart(part)
	if !ok {
		return provider.Message{}, false
	}
	if !e.sessionSupportsImageAttachments(session) {
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
		Role:         domain.MessageRoleTool,
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

func (e *Engine) validatePromptAttachments(session domain.Session, drafts []attachment.Draft) error {
	for _, draft := range drafts {
		kind := attachment.ClassifyMIME(draft.MIME)
		switch kind {
		case attachment.KindText:
			continue
		case attachment.KindImage, attachment.KindPDF:
			supported, err := e.caps.SupportsAttachment(session.ProviderID, providerCfgForSession(e.cfg, session), session.ModelID, kind)
			if err != nil {
				return err
			}
			if supported {
				continue
			}
			return fmt.Errorf("provider %s model %s does not support %s attachments", session.ProviderID, session.ModelID, kind)
		default:
			return fmt.Errorf("unsupported attachment type %q", draft.MIME)
		}
	}
	return nil
}

func (e *Engine) sessionSupportsImageAttachments(session domain.Session) bool {
	supported, err := e.caps.SupportsAttachment(session.ProviderID, providerCfgForSession(e.cfg, session), session.ModelID, attachment.KindImage)
	return err == nil && supported
}

func providerCfgForSession(cfg config.Config, session domain.Session) config.Provider {
	if providerCfg, ok := cfg.Provider(session.ProviderID); ok {
		return providerCfg
	}
	return config.Provider{}
}

func (e *Engine) compactionKeepToolBatches() int {
	return config.NormalizeCompactionKeepToolBatches(e.cfg.CompactionKeepToolBatches)
}

func (e *Engine) systemPrompt() string {
	if root := managedAssetRoot(); root != "" {
		data, err := os.ReadFile(filepath.Join(root, "system-prompt.md"))
		if err == nil {
			return strings.TrimSpace(string(data))
		}
	}
	return systemPrompt()
}

func systemPrompt() string {
	data, err := assets.DefaultContent("system-prompt.md")
	if err != nil {
		panic(err)
	}
	return strings.TrimSpace(string(data))
}

func managedAssetRoot() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, ".koder")
}

func compactPrompt() string {
	return strings.TrimSpace(`
Summarize this coding session so another agent can continue it with minimal loss.

Return only the summary text. Do not call tools.

Use this structure:
## Goal
[current user goal]

## Constraints
- [important instructions or preferences]

## Progress
- [finished work]
- [work still in progress]

## Relevant Files
- [important files and why they matter]

## Next Step
- [best immediate continuation]
`)
}

func parseToolCall(text string) (*tools.Request, string) {
	re := regexp.MustCompile(`(?s)<koder_tool>\s*(\{.*?\})\s*</koder_tool>`)
	match := re.FindStringSubmatch(text)
	if len(match) != 2 {
		return nil, text
	}
	call, err := tools.RequestFromMeta(match[1])
	if err != nil || call.Tool == "" {
		return nil, text
	}
	plain := strings.TrimSpace(re.ReplaceAllString(text, ""))
	return &call, plain
}

func (e *Engine) autoCompactIfNeeded(ctx context.Context, session domain.Session, client *provider.Client, out chan<- domain.Event) (bool, error) {
	chat, err := e.store.DefaultChat(ctx, session.ID)
	if err != nil {
		return false, err
	}
	return e.autoCompactChatIfNeeded(ctx, session, chat.ID, client, out)
}

func (e *Engine) autoCompactPreparedMessagesIfNeeded(ctx context.Context, session domain.Session, chatID domain.ID, client *provider.Client, messages []provider.Message, out chan<- domain.Event) (bool, error) {
	providerCfg, ok := e.cfg.Provider(session.ProviderID)
	if !ok {
		return false, nil
	}
	threshold := providerCfg.AutoCompactAt
	if threshold <= 0 {
		threshold = max(1, e.cfg.AutoCompactAt)
	}
	estimated, ok := e.estimateRequestUsagePercent(session, domain.Chat{ID: chatID}, messages)
	if !ok || estimated < threshold {
		return false, nil
	}
	if out != nil {
		out <- domain.Event{Kind: domain.EventKindStatus, Text: fmt.Sprintf("Auto-compacting at ~%d%% estimated context used", estimated)}
	}
	if err := e.compactSession(ctx, session, chatID, client, "auto", out); err != nil {
		return false, err
	}
	return true, nil
}

func (e *Engine) autoCompactChatIfNeeded(ctx context.Context, session domain.Session, chatID domain.ID, client *provider.Client, out chan<- domain.Event) (bool, error) {
	chat, err := e.store.GetChat(ctx, chatID)
	if err != nil {
		return false, err
	}
	envelope, err := e.buildPromptEnvelopePreview(ctx, session, chatID, "", nil, nil, nil)
	if err != nil {
		return false, err
	}
	estimated, ok := e.estimateRequestUsagePercent(session, chat, provider.SerializePromptEnvelope(envelope))
	if !ok {
		return false, nil
	}
	providerCfg, ok := e.cfg.Provider(session.ProviderID)
	if !ok {
		return false, nil
	}
	threshold := providerCfg.AutoCompactAt
	if threshold <= 0 {
		threshold = max(1, e.cfg.AutoCompactAt)
	}
	if estimated < threshold {
		return false, nil
	}
	if out != nil {
		out <- domain.Event{Kind: domain.EventKindStatus, Text: fmt.Sprintf("Auto-compacting at ~%d%% estimated context used", estimated)}
	}
	return e.autoCompactPreparedMessagesIfNeeded(ctx, session, chatID, client, provider.SerializePromptEnvelope(envelope), nil)
}

func (e *Engine) estimateRequestUsagePercent(session domain.Session, _ domain.Chat, messages []provider.Message) (int, bool) {
	providerCfg, ok := e.cfg.Provider(session.ProviderID)
	if !ok || providerCfg.ContextWindow <= 0 {
		return 0, false
	}
	body, err := json.Marshal(messages)
	if err != nil || len(body) == 0 {
		return 0, false
	}
	// Rough byte-based estimate over the replayed conversation payload only.
	// Ignore static request/tool schema overhead so auto-compaction reacts to
	// message churn rather than repeatedly compacting tiny summaries.
	estimatedTokens := len(body) / 4
	if estimatedTokens <= 0 {
		return 0, false
	}
	percent := (estimatedTokens * 100) / providerCfg.ContextWindow
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	return percent, true
}

func (e *Engine) compactSession(ctx context.Context, session domain.Session, chatID domain.ID, client *provider.Client, trigger string, out chan<- domain.Event) error {
	timeline, err := e.store.TimelineForChat(ctx, chatID)
	if err != nil {
		return err
	}
	chat, err := e.store.GetChat(ctx, chatID)
	if err != nil {
		return err
	}
	messages, firstKeptItemID, err := e.buildCompactionConversationForTimeline(session, chat, timeline)
	if err != nil {
		return err
	}
	if len(messages) <= 1 {
		return nil
	}
	beforeContextTokens := e.estimateContextTokensForTimeline(session, chat, timeline)
	compactionItem, err := e.store.AppendTimeline(ctx, chatID, domain.Compaction{
		Trigger:             trigger,
		Status:              "pending",
		FirstKeptItemID:     firstKeptItemID,
		BeforeContextTokens: beforeContextTokens,
	})
	if err != nil {
		return err
	}
	updateCompactionState := func(summary, status string, afterContextTokens int) error {
		compactionItem.Content = domain.Compaction{
			Summary:             summary,
			Trigger:             trigger,
			Status:              status,
			FirstKeptItemID:     firstKeptItemID,
			BeforeContextTokens: beforeContextTokens,
			AfterContextTokens:  afterContextTokens,
		}
		compactionItem.UpdatedAt = time.Now().UTC()
		if status == "completed" || status == "failed" {
			compactionItem.Seal(compactionItem.UpdatedAt)
		}
		return e.store.Timeline().Put(ctx, compactionItem)
	}
	if out != nil {
		out <- domain.Event{
			Kind: domain.EventKindStatus,
			Text: "Compacting session...",
			Item: compactionItem,
			Meta: map[string]string{"refresh": "details", "compaction": "started"},
		}
	}
	resp, err := client.CompleteChat(ctx, e.chatRequest(session, domain.Chat{}, append(messages, provider.Message{
		Role:    domain.MessageRoleUser,
		Content: compactPrompt(),
	}), false))
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
	if chat, err := e.store.GetChat(ctx, chatID); err == nil {
		chat.LastKnownContextTokens = afterContextTokens
		chat.ContextTokensKnown = false
		if err := e.store.UpdateChat(ctx, chat); err != nil {
			return err
		}
	} else {
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

func (e *Engine) buildCompactionConversationForTimeline(session domain.Session, chat domain.Chat, timeline []domain.TimelineItem) ([]provider.Message, string, error) {
	keepStart := preservedTimelineToolTailStart(timeline, e.compactionKeepToolBatches())
	head := timeline
	firstKeptItemID := ""
	if keepStart < len(timeline) {
		head = timeline[:keepStart]
		firstKeptItemID = timeline[keepStart].ID
	}
	envelope, err := e.buildPromptEnvelopeForTimeline(session, chat, head, "", nil, nil, nil)
	if err != nil {
		return nil, "", err
	}
	return provider.SerializePromptEnvelope(envelope), firstKeptItemID, nil
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

func (e *Engine) handleModelToolCall(ctx context.Context, session domain.Session, chat domain.Chat, req tools.Request) (domain.Event, error) {
	prepared, err := e.prepareModelToolCall(ctx, session, chat, req)
	if err != nil {
		return domain.Event{}, err
	}
	if !prepared.run {
		return prepared.event, nil
	}
	events, err := e.executePreparedToolCall(ctx, prepared.chatID, prepared.sessionID, prepared.req)
	if err != nil {
		return domain.Event{}, err
	}
	var final domain.Event
	for _, evt := range events {
		final = evt
	}
	return final, nil
}

type preparedToolCall struct {
	req       tools.Request
	event     domain.Event
	run       bool
	sessionID domain.ID
	chatID    domain.ID
}

type completedToolCall struct {
	events []domain.Event
	err    error
}

func (e *Engine) parseProviderToolCalls(raw []provider.ToolCall, sessionID domain.ID) ([]tools.Request, error) {
	calls := make([]tools.Request, 0, len(raw))
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
			continue
		}
		e.recordLifecycle(sessionID, "tool_call_parsed", call.ContextString(), map[string]string{"tool": string(call.Tool), "tool_call_id": call.ToolCallID})
		calls = append(calls, call)
	}
	if len(calls) == 0 {
		return nil, parseErr
	}
	return calls, nil
}

func (e *Engine) parseProviderToolCall(item provider.ToolCall) (tools.Request, error) {
	name := strings.TrimSpace(item.Function.Name)
	ok := false
	serverID, toolName := "", ""
	if e.mcp != nil {
		localDefs := tools.Definitions(e.toolRuntime(domain.Session{}, domain.Chat{}))
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
	return tools.Normalize(req)
}

func (e *Engine) persistAssistantToolCalls(ctx context.Context, chatID, sessionID domain.ID, item domain.TimelineItem, calls []tools.Request, text string, usage domain.Usage) (domain.TimelineItem, error) {
	toolCalls := make([]domain.ToolCall, 0, len(calls))
	for _, call := range calls {
		toolCalls = append(toolCalls, domain.ToolCall{
			ToolCallID: domain.ToolCallID(call.ToolCallID),
			Tool:       call.Tool,
			Args:       call.Args,
			Status:     domain.ToolStatusPending,
		})
	}
	item, err := e.store.AppendAssistantToolCallsWithItem(ctx, chatID, item, toolCalls, text, usage)
	if err != nil {
		return domain.TimelineItem{}, err
	}
	return item, nil
}

func (e *Engine) saveChatContextUsage(ctx context.Context, chatID domain.ID, usage domain.Usage) error {
	if e == nil || e.store == nil || chatID == "" {
		return nil
	}
	contextTokens, ok := usage.ContextTokens()
	if !ok {
		return nil
	}
	chat, err := e.store.GetChat(ctx, chatID)
	if err != nil {
		return fmt.Errorf("load chat context usage state: %w", err)
	}
	chat.LastKnownContextTokens = contextTokens
	chat.ContextTokensKnown = true
	if err := e.store.UpdateChat(ctx, chat); err != nil {
		return fmt.Errorf("save chat context usage state: %w", err)
	}
	return nil
}

func (e *Engine) handleModelToolCalls(ctx context.Context, session domain.Session, chat domain.Chat, calls []tools.Request, out chan<- domain.Event) (bool, error) {
	if len(calls) == 0 {
		return false, nil
	}
	prepared := make([]preparedToolCall, 0, len(calls))
	needsApproval := false
	for _, call := range calls {
		next, err := e.prepareModelToolCall(ctx, session, chat, call)
		if err != nil {
			return false, err
		}
		prepared = append(prepared, next)
	}

	execCount := 0
	for _, item := range prepared {
		if item.run {
			execCount++
			continue
		}
		out <- item.event
		if item.event.Kind == domain.EventKindApprovalAsk {
			needsApproval = true
		}
	}

	if execCount == 0 {
		return needsApproval, nil
	}

	results := make(chan completedToolCall, execCount)
	for _, item := range prepared {
		if !item.run {
			continue
		}
		out <- domain.Event{Kind: domain.EventKindToolStart, Tool: item.req.Tool, ToolCallID: item.req.ToolCallID, Text: tools.Preview(item.req)}
		go func(req tools.Request) {
			events, err := e.executePreparedToolCall(ctx, item.chatID, item.sessionID, req)
			results <- completedToolCall{events: events, err: err}
		}(item.req)
	}

	var firstErr error
	for i := 0; i < execCount; i++ {
		completed := <-results
		if completed.err != nil {
			if interruptedErr(completed.err) {
				firstErr = completed.err
				continue
			}
			if firstErr == nil {
				firstErr = completed.err
			}
			continue
		}
		for _, evt := range completed.events {
			out <- evt
			if evt.Kind == domain.EventKindApprovalAsk {
				needsApproval = true
			}
		}
	}
	if firstErr != nil {
		return needsApproval, firstErr
	}
	if turncontrol.ShouldStop(ctx) {
		return needsApproval, context.Canceled
	}
	return needsApproval, nil
}

func (e *Engine) prepareModelToolCall(ctx context.Context, session domain.Session, chat domain.Chat, req tools.Request) (preparedToolCall, error) {
	session, chat, err := e.persistedToolCallState(ctx, session, chat)
	if err != nil {
		return preparedToolCall{}, err
	}
	out := preparedToolCall{sessionID: session.ID, chatID: chat.ID}
	req, err = tools.Normalize(req)
	if err != nil {
		events, persistErr := e.persistToolFailure(ctx, chat.ID, session.ID, req, err)
		if persistErr != nil {
			return preparedToolCall{}, persistErr
		}
		final := <-events
		out.req = req
		out.event = final
		return out, nil
	}
	out.req = req
	toolSpec, ok := tools.Lookup(req.Tool)
	if !ok {
		return preparedToolCall{}, fmt.Errorf("unsupported model tool %q", req.Tool)
	}
	if !toolEnabledForSession(e.cfg, session, req.Tool) {
		evt, err := e.recordDisabledToolResult(ctx, chat.ID, session.ID, req)
		if err != nil {
			return preparedToolCall{}, err
		}
		out.event = evt
		return out, nil
	}
	if !chatrole.AllowsTool(chat.WorkflowRole, req.Tool) {
		evt, err := e.recordRoleDeniedToolResult(ctx, chat.ID, session.ID, req, chat.WorkflowRole)
		if err != nil {
			return preparedToolCall{}, err
		}
		out.event = evt
		return out, nil
	}

	decision := permissionprofile.Decision{Mode: domain.PermissionModeAllow}
	if !toolSpec.BypassesPermission() {
		decision = permissionprofile.Evaluate(e.cfg.Permissions, e.effectivePermissionProfile(ctx, session, chat), session.PermissionRules, e.permissionRequest(session, req))
	}
	if decision.Mode == domain.PermissionModeDeny {
		text := fmt.Sprintf("%s denied by policy", req.Tool)
		if strings.TrimSpace(decision.Reason) != "" {
			text = fmt.Sprintf("%s denied by policy: %s", req.Tool, decision.Reason)
		}
		item, err := e.recordDeniedToolResult(ctx, chat.ID, req, text)
		if err != nil {
			return preparedToolCall{}, err
		}
		out.event = domain.Event{Kind: domain.EventKindToolResult, Tool: req.Tool, ToolCallID: req.ToolCallID, Text: text, Item: item}
		return out, nil
	}
	if decision.Mode == domain.PermissionModeAsk {
		preview := tools.Preview(req)
		approvalItem, err := e.recordApprovalRequest(ctx, chat.ID, session.ID, req.Tool, req.ToolCallID, preview, req.ToolCallID)
		if err != nil {
			return preparedToolCall{}, err
		}
		text := fmt.Sprintf("%s requires approval", req.Tool)
		if strings.TrimSpace(decision.Reason) != "" {
			text += ": " + decision.Reason
		}
		out.event = domain.Event{
			Kind: domain.EventKindApprovalAsk,
			Text: text,
			Tool: req.Tool,
			Item: approvalItem,
			Meta: map[string]string{
				"approval_id":  store.SyntheticApprovalID(req.ToolCallID),
				"tool":         string(req.Tool),
				"command":      preview,
				"reason":       decision.Reason,
				"tool_call_id": req.ToolCallID,
			},
		}
		return out, nil
	}
	out.run = true
	return out, nil
}

func (e *Engine) persistedToolCallState(ctx context.Context, session domain.Session, chat domain.Chat) (domain.Session, domain.Chat, error) {
	if session.ID != "" {
		latest, err := e.store.GetSession(ctx, session.ID)
		if err != nil {
			return domain.Session{}, domain.Chat{}, err
		}
		session = latest
	}
	if chat.ID != "" {
		latest, err := e.store.GetChat(ctx, chat.ID)
		if err != nil {
			return domain.Session{}, domain.Chat{}, err
		}
		chat = latest
	}
	return session, chat, nil
}

func (e *Engine) recordDisabledToolResult(ctx context.Context, chatID, sessionID domain.ID, req tools.Request) (domain.Event, error) {
	text := fmt.Sprintf("%s disabled for this session", req.Tool)
	if sessionID == "" {
		return domain.Event{Kind: domain.EventKindToolResult, Tool: req.Tool, ToolCallID: req.ToolCallID, Text: text}, nil
	}
	item, err := e.recordDeniedToolResult(ctx, chatID, req, text)
	if err != nil {
		return domain.Event{}, err
	}
	return domain.Event{Kind: domain.EventKindToolResult, Tool: req.Tool, ToolCallID: req.ToolCallID, Text: text, Item: item}, nil
}

func (e *Engine) recordRoleDeniedToolResult(ctx context.Context, chatID, sessionID domain.ID, req tools.Request, role domain.WorkflowRole) (domain.Event, error) {
	text := fmt.Sprintf("%s is not available to %s chats", req.Tool, role)
	if sessionID == "" {
		return domain.Event{Kind: domain.EventKindToolResult, Tool: req.Tool, ToolCallID: req.ToolCallID, Text: text}, nil
	}
	item, err := e.recordDeniedToolResult(ctx, chatID, req, text)
	if err != nil {
		return domain.Event{}, err
	}
	return domain.Event{Kind: domain.EventKindToolResult, Tool: req.Tool, ToolCallID: req.ToolCallID, Text: text, Item: item}, nil
}

func (e *Engine) recordDeniedToolResult(ctx context.Context, chatID domain.ID, req tools.Request, text string) (domain.TimelineItem, error) {
	result := domain.ToolResult{
		Text:   text,
		Status: domain.ToolResultStatusDenied,
		Data:   domain.DeniedStoredResult{Message: text},
	}
	if strings.TrimSpace(req.ToolCallID) != "" {
		return e.store.AttachToolResult(ctx, chatID, req.ToolCallID, result)
	}
	now := time.Now().UTC()
	item, err := e.store.AppendTimeline(ctx, chatID, domain.ToolExecution{
		Tool:      req.Tool,
		Args:      req.Args,
		Result:    &result,
		StartedAt: now,
		EndedAt:   now,
	})
	if err != nil {
		return domain.TimelineItem{}, err
	}
	item.Seal(now)
	if err := e.store.Timeline().Put(ctx, item); err != nil {
		return domain.TimelineItem{}, err
	}
	return item, nil
}

func toolEnabledForSession(cfg config.Config, session domain.Session, kind domain.ToolKind) bool {
	if enabled, ok := session.ToolStates[kind]; ok {
		return enabled
	}
	if enabled, ok := cfg.ToolDefaults[kind]; ok {
		return enabled
	}
	return true
}

func (e *Engine) executePreparedToolCall(ctx context.Context, chatID, sessionID domain.ID, req tools.Request) ([]domain.Event, error) {
	e.recordLifecycle(sessionID, "tool_execution_started", req.ContextString(), map[string]string{"tool": string(req.Tool), "tool_call_id": req.ToolCallID})
	if strings.TrimSpace(req.ToolCallID) != "" {
		if _, err := e.store.MarkToolRunning(ctx, chatID, req.ToolCallID); err != nil {
			return nil, err
		}
	}
	chat, chatErr := e.store.GetChat(ctx, chatID)
	if chatErr != nil {
		return nil, chatErr
	}
	result, err := e.registry.ExecuteWithChat(ctx, e.store, sessionID, chat, req)
	if err != nil {
		e.recordLifecycle(sessionID, "tool_execution_failed", err.Error(), map[string]string{"tool": string(req.Tool), "tool_call_id": req.ToolCallID})
		if interruptedErr(err) {
			return nil, err
		}
		events, persistErr := e.persistToolFailure(ctx, chatID, sessionID, req, err)
		if persistErr != nil {
			return nil, persistErr
		}
		var out []domain.Event
		for evt := range events {
			out = append(out, evt)
		}
		return out, nil
	}
	e.recordLifecycle(sessionID, "tool_execution_finished", req.ContextString(), map[string]string{"tool": string(req.Tool), "tool_call_id": req.ToolCallID})
	events, err := e.persistToolResult(ctx, chatID, sessionID, req, result)
	if err != nil {
		return nil, err
	}
	var out []domain.Event
	for evt := range events {
		out = append(out, evt)
	}
	return out, nil
}

func (e *Engine) effectivePermissionProfile(_ context.Context, session domain.Session, _ domain.Chat) string {
	cfg := config.Config{}
	if e != nil {
		cfg = e.cfg
	}
	if strings.TrimSpace(session.PermissionProfile) != "" {
		return session.PermissionProfile
	}
	return cfg.Permissions.Profile
}

func (e *Engine) permissionRequest(session domain.Session, req tools.Request) permissionprofile.Request {
	projectRoot := strings.TrimSpace(session.ProjectRoot)
	if projectRoot == "" {
		projectRoot = agents.FindProjectRoot(e.workdir)
	}
	pattern := tools.Preview(req)
	if req.Tool == domain.ToolKindMCP {
		pattern = strings.TrimSpace(req.Args["server"]) + "/" + strings.TrimSpace(req.Args["tool"])
	}
	targets, outsideProject, ambiguous := e.resolvePermissionTargets(projectRoot, req)
	return permissionprofile.Request{
		Tool:           req.Tool,
		Access:         permissionAccessForTool(req.Tool),
		Pattern:        pattern,
		ProjectRoot:    projectRoot,
		Targets:        targets,
		OutsideProject: outsideProject,
		Ambiguous:      ambiguous,
	}
}

func permissionAccessForTool(kind domain.ToolKind) permissionprofile.AccessKind {
	switch kind {
	case domain.ToolKindBash, domain.ToolKindExecCommand:
		return permissionprofile.AccessShell
	case domain.ToolKindRead, domain.ToolKindViewImage, domain.ToolKindShowImage, domain.ToolKindGlob, domain.ToolKindGrep, domain.ToolKindCodeSearch:
		return permissionprofile.AccessRead
	case domain.ToolKindApplyPatch, domain.ToolKindEdit, domain.ToolKindWrite:
		return permissionprofile.AccessWrite
	default:
		return permissionprofile.AccessUnknown
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

func (e *Engine) resolvePermissionTargets(projectRoot string, req tools.Request) ([]string, bool, bool) {
	baseDir := e.workdir
	if strings.TrimSpace(projectRoot) != "" {
		baseDir = projectRoot
	}
	var raws []string
	switch req.Tool {
	case domain.ToolKindRead, domain.ToolKindViewImage, domain.ToolKindShowImage, domain.ToolKindEdit, domain.ToolKindWrite:
		raws = append(raws, req.Args["path"])
	case domain.ToolKindGlob, domain.ToolKindGrep, domain.ToolKindCodeSearch:
		if root := strings.TrimSpace(req.Args["path"]); root != "" {
			raws = append(raws, root)
		} else {
			raws = append(raws, ".")
		}
	case domain.ToolKindApplyPatch:
		raws = append(raws, patchPaths(req.Args["patch"])...)
	default:
		return nil, false, false
	}
	if len(raws) == 0 {
		return nil, false, true
	}
	projectRoot = filepath.Clean(projectRoot)
	var targets []string
	outsideProject := false
	for _, raw := range raws {
		target, ok := resolvePermissionTarget(baseDir, raw)
		if !ok {
			return nil, false, true
		}
		targets = append(targets, target)
		if strings.TrimSpace(projectRoot) == "" {
			outsideProject = true
			continue
		}
		rel, err := filepath.Rel(projectRoot, target)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			outsideProject = true
		}
	}
	return targets, outsideProject, false
}

func resolvePermissionTarget(baseDir, raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	raw = tools.NormalizePathInput(raw)
	if raw == "" {
		return "", false
	}
	if filepath.IsAbs(raw) {
		return maybeResolveExistingPath(filepath.Clean(raw)), true
	}
	abs := filepath.Join(baseDir, raw)
	return maybeResolveExistingPath(filepath.Clean(abs)), true
}

func maybeResolveExistingPath(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved
	}
	if os.IsNotExist(err) {
		return path
	}
	return path
}

func patchPaths(patch string) []string {
	seen := map[string]struct{}{}
	var paths []string
	for _, match := range patchPathPattern.FindAllStringSubmatch(patch, -1) {
		if len(match) < 2 {
			continue
		}
		path := strings.TrimSpace(match[1])
		if path == "" || path == "/dev/null" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	return paths
}

func requestFromStoredApproval(tool domain.ToolKind, raw string) (tools.Request, error) {
	return tools.RequestFromStored(tool, raw)
}

func max(a, b int) int {
	return slices.Max([]int{a, b})
}

func (e *Engine) recordApprovalRequest(ctx context.Context, chatID, sessionID domain.ID, tool domain.ToolKind, approvalID, preview, toolCallID string) (domain.TimelineItem, error) {
	body := fmt.Sprintf("Approval required for %s: %s", tool, preview)
	item, err := e.store.AttachToolApproval(ctx, chatID, toolCallID, domain.ApprovalRequest{
		Body: body,
	})
	if err != nil {
		return domain.TimelineItem{}, err
	}
	return item, nil
}

func (e *Engine) recordApprovalReply(ctx context.Context, chatID, sessionID domain.ID, tool domain.ToolKind, approvalID domain.ID, status, preview, toolCallID string) (domain.TimelineItem, error) {
	body := fmt.Sprintf("Approval %s %s for %s: %s", approvalID, status, tool, preview)
	payload := map[string]string{
		"approval_id": approvalID,
		"tool":        string(tool),
		"status":      status,
		"command":     preview,
	}
	if strings.TrimSpace(toolCallID) != "" {
		payload["tool_call_id"] = toolCallID
	}
	resultStatus := domain.ToolResultStatusOK
	var data domain.ToolResultPayload
	if status == "denied" {
		resultStatus = domain.ToolResultStatusDenied
		data = domain.DeniedStoredResult{Message: body}
	}
	_ = payload
	item, err := e.store.AttachToolResult(ctx, chatID, toolCallID, domain.ToolResult{
		Text:   body,
		Data:   data,
		Status: resultStatus,
	})
	if err != nil {
		return domain.TimelineItem{}, err
	}
	return item, nil
}

func approvalPreviewFromStored(tool domain.ToolKind, raw string) string {
	req, err := requestFromStoredApproval(tool, raw)
	if err != nil {
		return raw
	}
	return tools.Preview(req)
}

func (e *Engine) requestForToolCall(ctx context.Context, chatID domain.ID, toolCallID string) (tools.Request, error) {
	toolCallID = strings.TrimSpace(toolCallID)
	if chatID == "" {
		return tools.Request{}, fmt.Errorf("chat id is required")
	}
	if toolCallID == "" {
		return tools.Request{}, fmt.Errorf("tool call id is required")
	}
	items, err := e.store.TimelineForChat(ctx, chatID)
	if err != nil {
		return tools.Request{}, err
	}
	for idx := len(items) - 1; idx >= 0; idx-- {
		assistant, ok := items[idx].Content.(domain.AssistantMessage)
		if !ok {
			continue
		}
		call := assistant.ToolByID(domain.ToolCallID(toolCallID))
		if call == nil {
			continue
		}
		if call.Status != domain.ToolStatusAwaitingApproval {
			return tools.Request{}, fmt.Errorf("tool call %q is %s, not awaiting approval", toolCallID, call.Status)
		}
		return tools.Normalize(tools.Request{
			Tool:       call.Tool,
			ToolCallID: string(call.ToolCallID),
			Args:       maps.Clone(call.Args),
		})
	}
	return tools.Request{}, fmt.Errorf("tool call %q not found", toolCallID)
}

func (e *Engine) syntheticApprovalRequest(ctx context.Context, sessionID, chatID, approvalID domain.ID) (domain.Session, domain.Chat, tools.Request, error) {
	var chats []domain.Chat
	if chatID != "" {
		chat, err := e.store.GetChat(ctx, chatID)
		if err != nil {
			return domain.Session{}, domain.Chat{}, tools.Request{}, err
		}
		chats = []domain.Chat{chat}
	} else {
		listed, err := e.store.ListChats(ctx, sessionID)
		if err != nil {
			return domain.Session{}, domain.Chat{}, tools.Request{}, err
		}
		chats = listed
	}
	for _, chat := range chats {
		approvals, err := e.store.PendingApprovalsForChat(ctx, chat.ID)
		if err != nil {
			return domain.Session{}, domain.Chat{}, tools.Request{}, err
		}
		for _, approval := range approvals {
			if approval.ID != approvalID {
				continue
			}
			session, err := e.store.GetSession(ctx, chat.SessionID)
			if err != nil {
				return domain.Session{}, domain.Chat{}, tools.Request{}, err
			}
			req, err := e.requestForToolCall(ctx, chat.ID, approval.ToolCallID)
			return session, chat, req, err
		}
	}
	return domain.Session{}, domain.Chat{}, tools.Request{}, fmt.Errorf("approval %s not found", approvalID)
}
