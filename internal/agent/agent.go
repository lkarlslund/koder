package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/agents"
	"github.com/lkarlslund/koder/internal/attachment"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/debugsrv"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/permission"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/reference"
	"github.com/lkarlslund/koder/internal/sessionctx"
	"github.com/lkarlslund/koder/internal/skills"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
	_ "github.com/lkarlslund/koder/internal/tools/all"
)

type Engine struct {
	cfg        config.Config
	store      *store.Store
	registry   *tools.Registry
	debug      *debugsrv.Recorder
	files      *attachment.Manager
	caps       *provider.CapabilityStore
	agents     *agents.Manager
	workdir    string
	retryPause func(context.Context, time.Duration, func(time.Duration)) error
}

var patchPathPattern = regexp.MustCompile(`(?m)^(?:\+\+\+|---)\s+(?:a/|b/)?([^\t\n]+)`)

const (
	maxRateLimitRetries       = 3
	defaultRateLimitRetryWait = 5 * time.Second
)

func New(cfg config.Config, st *store.Store, registry *tools.Registry, debug *debugsrv.Recorder, workdir string) *Engine {
	return &Engine{
		cfg:        cfg,
		store:      st,
		registry:   registry,
		debug:      debug,
		files:      attachment.NewManager(cfg.StateDir()),
		caps:       provider.NewCapabilityStore(cfg.StateDir()),
		agents:     agents.NewManager(cfg.StateDir(), filepath.Join(filepath.Dir(cfg.Path()), "AGENTS.md")),
		workdir:    workdir,
		retryPause: waitForRetry,
	}
}

func (e *Engine) UpdateConfig(cfg config.Config) {
	e.cfg = cfg
	e.agents = agents.NewManager(cfg.StateDir(), filepath.Join(filepath.Dir(cfg.Path()), "AGENTS.md"))
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

func (e *Engine) SetPermissionProfile(ctx context.Context, sessionID int64, profile string) (<-chan domain.Event, error) {
	return e.setPermissionProfile(ctx, sessionID, 0, profile)
}

func (e *Engine) SetPermissionProfileAndReevaluateApproval(ctx context.Context, sessionID, approvalID int64, profile string) (<-chan domain.Event, error) {
	return e.setPermissionProfileAndReevaluateApproval(ctx, sessionID, 0, approvalID, profile)
}

func (e *Engine) SetPermissionProfileInChat(ctx context.Context, sessionID, chatID int64, profile string) (<-chan domain.Event, error) {
	return e.setPermissionProfile(ctx, sessionID, chatID, profile)
}

func (e *Engine) SetPermissionProfileInChatAndReevaluateApproval(ctx context.Context, sessionID, chatID, approvalID int64, profile string) (<-chan domain.Event, error) {
	return e.setPermissionProfileAndReevaluateApproval(ctx, sessionID, chatID, approvalID, profile)
}

func (e *Engine) ApproveInChatWithRule(ctx context.Context, sessionID, chatID, approvalID int64, rule domain.PermissionOverride) (<-chan domain.Event, error) {
	return e.approveInChatWithRule(ctx, sessionID, chatID, approvalID, rule)
}

func (e *Engine) Approve(ctx context.Context, sessionID, approvalID int64) (<-chan domain.Event, error) {
	return e.approve(ctx, sessionID, 0, strconv.FormatInt(approvalID, 10))
}

func (e *Engine) ApproveInChat(ctx context.Context, sessionID, chatID, approvalID int64) (<-chan domain.Event, error) {
	return e.approve(ctx, sessionID, chatID, strconv.FormatInt(approvalID, 10))
}

func (e *Engine) Deny(ctx context.Context, sessionID, approvalID int64) (<-chan domain.Event, error) {
	return e.deny(ctx, sessionID, 0, strconv.FormatInt(approvalID, 10))
}

func (e *Engine) DenyInChat(ctx context.Context, sessionID, chatID, approvalID int64) (<-chan domain.Event, error) {
	return e.deny(ctx, sessionID, chatID, strconv.FormatInt(approvalID, 10))
}

func (e *Engine) Compact(ctx context.Context, sessionID int64) (<-chan domain.Event, error) {
	chat, err := e.store.DefaultChat(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return e.CompactChat(ctx, sessionID, chat.ID)
}

func (e *Engine) CompactChat(ctx context.Context, sessionID, chatID int64) (<-chan domain.Event, error) {
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
		if session.ID > 0 {
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
		userMsg, err := e.persistUserPrompt(ctx, session, chat.ID, prompt, drafts, refs)
		if err != nil {
			if interruptedErr(err) {
				e.emitInterrupted(out, chat.ID, session.ID)
				return
			}
			out <- domain.Event{Kind: domain.EventKindError, Err: err}
			return
		}
		e.recordLifecycle(session.ID, "user_message_persisted", prompt, map[string]string{"message_id": strconv.FormatInt(userMsg.ID, 10)})
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
		if session.ID > 0 {
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

func (e *Engine) RefreshAgents(ctx context.Context, sessionID int64) (domain.Session, error) {
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
	if strings.TrimSpace(session.ProjectChecksum) != "" && (strings.TrimSpace(session.AgentsResolved) != "" || strings.TrimSpace(session.AgentsSummary) != "") {
		return session, nil
	}
	return e.refreshSessionAgents(ctx, session, client)
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

func (e *Engine) persistUserPrompt(ctx context.Context, session domain.Session, chatID int64, prompt string, drafts []attachment.Draft, refs []reference.Draft) (domain.Message, error) {
	userMsg, err := e.store.AddChatMessage(ctx, chatID, domain.MessageRoleUser, prompt)
	if err != nil {
		return domain.Message{}, err
	}
	if strings.TrimSpace(prompt) != "" {
		if _, err := e.store.AddPart(ctx, userMsg.ID, domain.PartKindText, prompt, ""); err != nil {
			return domain.Message{}, err
		}
	}
	for _, draft := range drafts {
		meta, err := e.files.AdoptDraft(draft, session.ID)
		if err != nil {
			return domain.Message{}, err
		}
		raw, err := attachment.EncodeMeta(meta)
		if err != nil {
			return domain.Message{}, err
		}
		if _, err := e.store.AddPart(ctx, userMsg.ID, domain.PartKindAttachment, meta.Name, raw); err != nil {
			return domain.Message{}, err
		}
	}
	for _, ref := range refs {
		raw, err := reference.EncodeMeta(reference.Metadata(ref))
		if err != nil {
			return domain.Message{}, err
		}
		if _, err := e.store.AddPart(ctx, userMsg.ID, domain.PartKindReference, ref.Display, raw); err != nil {
			return domain.Message{}, err
		}
	}
	return userMsg, nil
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
		transient = nil

		req := e.chatRequest(session, chat, messages, false)
		resp, completeErr := e.completeChatWithRetry(ctx, session.ID, client, out, req)
		if completeErr != nil {
			return completeErr
		}

		if len(resp.ToolCalls) > 0 {
			calls, err := e.parseProviderToolCalls(resp.ToolCalls, session.ID)
			if err != nil {
				return err
			}
			if err := e.persistAssistantToolCalls(ctx, chat.ID, session.ID, calls, strings.TrimSpace(resp.Text), resp.Usage); err != nil {
				return err
			}
			if resp.Usage.TotalTokens > 0 {
				out <- domain.Event{Kind: domain.EventKindUsage, Usage: resp.Usage}
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

		text, reasoning, usage := resp.Text, resp.Reasoning, resp.Usage
		call, plain := parseToolCall(text)
		if call != nil {
			e.recordLifecycle(session.ID, "tool_call_parsed", call.ContextString(), map[string]string{"tool": string(call.Tool), "tool_call_id": call.ToolCallID})
			assistantMsg, err := e.store.AddChatMessage(ctx, chat.ID, domain.MessageRoleAssistant, fmt.Sprintf("tool:%s", call.Tool))
			if err != nil {
				return err
			}
			meta, _ := json.Marshal(call)
			if _, err := e.store.AddPart(ctx, assistantMsg.ID, domain.PartKindToolCall, strings.TrimSpace(text), string(meta)); err != nil {
				return err
			}
			if strings.TrimSpace(plain) != "" {
				if _, err := e.store.AddPart(ctx, assistantMsg.ID, domain.PartKindText, strings.TrimSpace(plain), ""); err != nil {
					return err
				}
			}
			if pause, ok := tracker.trackCalls([]tools.Request{*call}); ok {
				e.pauseContinuation(ctx, chat.ID, session.ID, pause, out)
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
			e.pauseContinuation(ctx, chat.ID, session.ID, continuationPause{
				Reason: continuationPauseReasonProviderRefusal,
				Body:   providerRefusalPauseBody(reasoning),
			}, out)
			return nil
		}

		assistantMsg, err := e.store.AddChatMessage(ctx, chat.ID, domain.MessageRoleAssistant, strings.TrimSpace(text))
		if err != nil {
			return err
		}
		if strings.TrimSpace(text) != "" {
			if _, err := e.store.AddPart(ctx, assistantMsg.ID, domain.PartKindText, text, ""); err != nil {
				return err
			}
		}
		if strings.TrimSpace(reasoning) != "" {
			if _, err := e.store.AddPart(ctx, assistantMsg.ID, domain.PartKindReasoning, reasoning, ""); err != nil {
				return err
			}
		}
		if usage.TotalTokens > 0 {
			meta, _ := json.Marshal(usage)
			if _, err := e.store.AddPart(ctx, assistantMsg.ID, domain.PartKindSystemNotice, "usage", string(meta)); err != nil {
				return err
			}
			out <- domain.Event{Kind: domain.EventKindUsage, Usage: usage}
		}
		if strings.TrimSpace(text) != "" {
			out <- domain.Event{Kind: domain.EventKindMessageDelta, Text: text}
		}
		if strings.TrimSpace(reasoning) != "" {
			out <- domain.Event{Kind: domain.EventKindReasoning, Text: reasoning}
		}
		e.recordLifecycle(session.ID, "assistant_message_persisted", strings.TrimSpace(text), map[string]string{"message_id": strconv.FormatInt(assistantMsg.ID, 10)})
		title, titleErr := e.maybeUpdateSessionTitle(ctx, session, client)
		if titleErr != nil {
			return titleErr
		}
		if strings.TrimSpace(title) != "" {
			out <- domain.Event{
				Kind: domain.EventKindSessionTitle,
				Text: title,
				Meta: map[string]string{"session_id": strconv.FormatInt(session.ID, 10)},
			}
		}
		out <- domain.Event{Kind: domain.EventKindMessageDone}
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

func (e *Engine) maybeUpdateSessionTitle(ctx context.Context, session domain.Session, client *provider.Client) (string, error) {
	count, err := e.store.CountMessagesByRole(ctx, session.ID, domain.MessageRoleUser)
	if err != nil {
		return "", err
	}
	if !shouldRefreshSessionTitle(count) {
		return "", nil
	}
	messages, err := e.titleSummaryMessages(ctx, session.ID)
	if err != nil {
		return "", err
	}
	resp, err := client.CompleteChat(ctx, e.chatRequest(session, domain.Chat{}, messages, false))
	if err != nil {
		return "", err
	}
	title := normalizeSessionTitle(resp.Text)
	if title == "" {
		return "", nil
	}
	if err := e.store.UpdateSessionTitle(ctx, session.ID, title); err != nil {
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
	if len(messages) > 0 && (chat.ID != 0 || chat.WorkflowRole != "") {
		req.Tools = tools.Definitions(e.toolRuntime(session, chat))
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

func (e *Engine) preserveThinkingEnabled(session domain.Session) bool {
	return provider.PreserveThinkingEnabled(session.ModelID, e.modelPresetForSession(session))
}

func shouldRefreshSessionTitle(promptCount int) bool {
	return promptCount == 1 || promptCount == 3 || promptCount == 10
}

func (e *Engine) titleSummaryMessages(ctx context.Context, sessionID int64) ([]provider.Message, error) {
	messages, partsByMessage, err := e.store.PartsForSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	start := max(0, len(messages)-8)
	var transcript []string
	for _, msg := range messages[start:] {
		content := stringifyParts(partsByMessage[msg.ID], false)
		if strings.TrimSpace(content) == "" {
			content = msg.Summary
		}
		if strings.TrimSpace(content) == "" {
			continue
		}
		transcript = append(transcript, fmt.Sprintf("%s: %s", msg.Role, content))
	}
	return []provider.Message{
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

func (e *Engine) setPermissionProfile(ctx context.Context, sessionID, chatID int64, raw string) (<-chan domain.Event, error) {
	profile := strings.TrimSpace(raw)
	if profile == "" {
		return nil, fmt.Errorf("permission profile is required; choose one of: %s", strings.Join(permission.ProfileNames(e.cfg.Permissions), "|"))
	}
	if !permission.IsBuiltinProfile(profile) {
		if _, ok := e.cfg.Permissions.Profiles[profile]; !ok {
			return nil, fmt.Errorf("unknown permission profile %q", profile)
		}
	}
	if chatID == 0 && sessionID == 0 {
		return emitOnce(domain.Event{
			Kind: domain.EventKindStatus,
			Text: fmt.Sprintf("permission profile set to %s", permission.DisplayName(profile)),
			Meta: map[string]string{"permission_profile": profile},
		}), nil
	}
	if chatID > 0 {
		chat, err := e.store.GetChat(ctx, chatID)
		if err != nil {
			return nil, err
		}
		chat.PermissionProfile = profile
		if err := e.store.UpdateChat(ctx, chat); err != nil {
			return nil, err
		}
	} else {
		if err := e.store.SetSessionPermissionProfile(ctx, sessionID, profile); err != nil {
			return nil, err
		}
	}
	return emitOnce(domain.Event{
		Kind: domain.EventKindStatus,
		Text: fmt.Sprintf("permission profile set to %s", permission.DisplayName(profile)),
		Meta: map[string]string{"permission_profile": profile},
	}), nil
}

func (e *Engine) setPermissionProfileAndReevaluateApproval(ctx context.Context, sessionID, chatID, approvalID int64, raw string) (<-chan domain.Event, error) {
	item, err := e.store.GetApproval(ctx, approvalID)
	if err != nil {
		return nil, err
	}
	targetSessionID := item.SessionID
	if sessionID != 0 {
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

	decision := permission.Decision{Mode: domain.PermissionModeAllow}
	if toolSpec, ok := tools.Lookup(req.Tool); !ok {
		return nil, fmt.Errorf("unsupported tool %q", req.Tool)
	} else if !toolSpec.BypassesPermission() {
		decision = permission.Evaluate(e.cfg.Permissions, effectivePermissionProfile(e.cfg, session, chat), session.PermissionRules, e.permissionRequest(session, req))
	}

	var next <-chan domain.Event
	switch decision.Mode {
	case domain.PermissionModeAllow:
		next, err = e.approve(ctx, item.SessionID, item.ChatID, strconv.FormatInt(approvalID, 10))
	case domain.PermissionModeDeny:
		next, err = e.deny(ctx, item.SessionID, item.ChatID, strconv.FormatInt(approvalID, 10))
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

func (e *Engine) approveInChatWithRule(ctx context.Context, sessionID, chatID, approvalID int64, rule domain.PermissionOverride) (<-chan domain.Event, error) {
	rule.Pattern = strings.TrimSpace(rule.Pattern)
	if rule.Pattern == "" {
		rule.Pattern = "*"
	}
	if err := permission.Validate(rule.Action); err != nil {
		return nil, err
	}
	item, err := e.store.GetApproval(ctx, approvalID)
	if err != nil {
		return nil, err
	}
	targetSessionID := item.SessionID
	if sessionID != 0 {
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
	decision := permission.Decision{Mode: domain.PermissionModeAllow}
	if toolSpec, ok := tools.Lookup(req.Tool); !ok {
		return nil, fmt.Errorf("unsupported tool %q", req.Tool)
	} else if !toolSpec.BypassesPermission() {
		decision = permission.Evaluate(e.cfg.Permissions, effectivePermissionProfile(e.cfg, session, chat), session.PermissionRules, e.permissionRequest(session, req))
	}
	var next <-chan domain.Event
	switch decision.Mode {
	case domain.PermissionModeAllow:
		next, err = e.approve(ctx, item.SessionID, item.ChatID, strconv.FormatInt(approvalID, 10))
	case domain.PermissionModeDeny:
		next, err = e.deny(ctx, item.SessionID, item.ChatID, strconv.FormatInt(approvalID, 10))
	default:
		next = emitOnce(domain.Event{Kind: domain.EventKindStatus, Text: fmt.Sprintf("%s still requires approval", item.Tool)})
	}
	if err != nil {
		return nil, err
	}
	return concatEvents(setEvents, next), nil
}

func (e *Engine) approve(ctx context.Context, sessionID, chatID int64, rawID string) (<-chan domain.Event, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(rawID), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parse approval id: %w", err)
	}
	item, err := e.store.GetApproval(ctx, id)
	if err != nil {
		return nil, err
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
		if err := e.recordApprovalReply(ctx, item.ChatID, item.SessionID, item.Tool, id, "denied", text, req.ToolCallID); err != nil {
			return nil, err
		}
		return emitOnce(domain.Event{Kind: domain.EventKindApprovalReply, Text: text, Tool: req.Tool}), nil
	}
	if err := e.recordApprovalReply(ctx, item.ChatID, sessionID, item.Tool, id, "approved", tools.Preview(req), req.ToolCallID); err != nil {
		return nil, err
	}
	e.recordLifecycle(sessionID, "tool_execution_started", item.Command, map[string]string{"tool": string(item.Tool), "approval_id": strconv.FormatInt(id, 10)})
	chat, chatErr := e.store.GetChat(ctx, item.ChatID)
	if chatErr != nil {
		return nil, chatErr
	}
	result, execErr := e.registry.ExecuteWithChat(ctx, e.store, sessionID, chat, req)
	if execErr != nil {
		e.recordLifecycle(sessionID, "tool_execution_failed", execErr.Error(), map[string]string{"tool": string(item.Tool), "approval_id": strconv.FormatInt(id, 10)})
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
	e.recordLifecycle(sessionID, "tool_execution_finished", item.Command, map[string]string{"tool": string(item.Tool), "approval_id": strconv.FormatInt(id, 10)})
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
		for evt := range toolEvents {
			out <- evt
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

func (e *Engine) deny(ctx context.Context, _, _ int64, rawID string) (<-chan domain.Event, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(rawID), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parse approval id: %w", err)
	}
	item, err := e.store.GetApproval(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := e.store.UpdateApproval(ctx, id, domain.ApprovalStatusDenied); err != nil {
		return nil, err
	}
	toolCallID := ""
	if req, err := requestFromStoredApproval(item.Tool, item.Command); err == nil {
		toolCallID = req.ToolCallID
	}
	if err := e.recordApprovalReply(ctx, item.ChatID, item.SessionID, item.Tool, id, "denied", approvalPreviewFromStored(item.Tool, item.Command), toolCallID); err != nil {
		return nil, err
	}
	return emitOnce(domain.Event{Kind: domain.EventKindApprovalReply, Text: fmt.Sprintf("approval %d denied", id)}), nil
}

func (e *Engine) persistToolResult(ctx context.Context, chatID, sessionID int64, req tools.Request, result tools.Result) (<-chan domain.Event, error) {
	events, err := e.registry.PersistResultInChat(ctx, e.store, sessionID, chatID, req, result)
	if err != nil {
		return nil, err
	}
	summary, _ := tools.SummarizeResult(req, result)
	e.recordLifecycle(sessionID, "tool_result_persisted", summary, map[string]string{"tool": string(req.Tool)})
	return events, nil
}

func (e *Engine) persistToolFailure(ctx context.Context, chatID, sessionID int64, req tools.Request, execErr error) (<-chan domain.Event, error) {
	if execErr == nil {
		return nil, errors.New("tool failure error is nil")
	}
	text := fmt.Sprintf("%s failed: %v", req.Tool, execErr)
	if sessionID == 0 {
		return emitOnce(domain.Event{Kind: domain.EventKindToolResult, Tool: req.Tool, Text: text}), nil
	}
	msg, err := e.store.AddChatMessage(ctx, chatID, domain.MessageRoleTool, string(req.Tool))
	if err != nil {
		return nil, err
	}
	meta, _ := json.Marshal(tools.MetaWithStoredResult(req.Meta(), domain.PartKindToolOutput, req.Tool, tools.StoredResultStatusError, tools.ErrorStoredResult{
		Message: text,
	}))
	if _, err := e.store.AddPart(ctx, msg.ID, domain.PartKindToolOutput, text, string(meta)); err != nil {
		return nil, err
	}
	e.recordLifecycle(sessionID, "tool_result_persisted", text, map[string]string{"tool": string(req.Tool), "status": "error"})
	return emitOnce(domain.Event{Kind: domain.EventKindToolResult, Tool: req.Tool, Text: text}), nil
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

func (e *Engine) recordAssistantError(ctx context.Context, chatID, sessionID int64, err error) {
	if err == nil || sessionID == 0 {
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

func (e *Engine) recordLifecycle(sessionID int64, kind, text string, meta map[string]string) {
	if e.debug == nil {
		return
	}
	e.debug.RecordLifecycle(sessionID, kind, text, meta)
}

func (e *Engine) emitInterrupted(out chan<- domain.Event, chatID, sessionID int64) {
	e.recordLifecycle(sessionID, "interrupted", "Interrupted", nil)
	e.persistTranscriptNotice(context.Background(), chatID, sessionID, "Interrupted", transcriptNotice{
		Kind:     "interrupted",
		Severity: "warning",
	})
	out <- domain.Event{Kind: domain.EventKindStatus, Text: "Interrupted"}
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

func (e *Engine) pauseContinuation(ctx context.Context, chatID, sessionID int64, pause continuationPause, out chan<- domain.Event) {
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
	e.persistTranscriptNotice(ctx, chatID, sessionID, body, transcriptNotice{
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
		out <- domain.Event{Kind: domain.EventKindStatus, Text: body}
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

func (e *Engine) persistTranscriptNotice(ctx context.Context, chatID, sessionID int64, body string, meta transcriptNotice) {
	if sessionID == 0 || chatID == 0 || e.store == nil {
		return
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return
	}
	msg, err := e.store.AddChatMessage(ctx, chatID, domain.MessageRoleAssistant, body)
	if err != nil {
		return
	}
	raw, _ := json.Marshal(meta)
	_, _ = e.store.AddPart(ctx, msg.ID, domain.PartKindEventNotice, body, string(raw))
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

func (e *Engine) completeChatWithRetry(ctx context.Context, sessionID int64, client *provider.Client, out chan<- domain.Event, req provider.ChatRequest) (provider.ChatResponse, error) {
	for attempt := 0; ; attempt++ {
		resp, err := client.CompleteChat(ctx, req)
		if err == nil {
			return resp, nil
		}
		var apiErr *provider.APIError
		if !errors.As(err, &apiErr) || apiErr.StatusCode != 429 || attempt >= maxRateLimitRetries {
			return provider.ChatResponse{}, err
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
			return provider.ChatResponse{}, err
		}
	}
}

func formatRateLimitRetryStatus(delay time.Duration, retryNumber int) string {
	delay = roundRetryDelay(delay)
	return fmt.Sprintf("Working (rate limit hit, retrying in %s, retry %d)", delay, retryNumber)
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

func (e *Engine) buildConversation(ctx context.Context, sessionID, chatID int64) ([]provider.Message, error) {
	session, err := e.store.GetSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return e.buildConversationPreview(ctx, session, chatID, "", nil, nil, nil)
}

func (e *Engine) buildConversationPreview(ctx context.Context, session domain.Session, chatID int64, prompt string, drafts []attachment.Draft, refs []reference.Draft, transient []provider.InstructionBlock) ([]provider.Message, error) {
	envelope, err := e.buildPromptEnvelopePreview(ctx, session, chatID, prompt, drafts, refs, transient)
	if err != nil {
		return nil, err
	}
	return provider.SerializePromptEnvelope(envelope), nil
}

func (e *Engine) buildPromptEnvelopePreview(ctx context.Context, session domain.Session, chatID int64, prompt string, drafts []attachment.Draft, refs []reference.Draft, transient []provider.InstructionBlock) (provider.PromptEnvelope, error) {
	chat := domain.Chat{WorkflowRole: domain.WorkflowRoleGeneral}
	if chatID > 0 {
		stored, err := e.store.GetChat(ctx, chatID)
		if err != nil {
			return provider.PromptEnvelope{}, err
		}
		chat = stored
	}
	envelope := provider.PromptEnvelope{
		Instructions: e.baseInstructionsForChat(session, chat),
	}
	if chatID > 0 {
		messages, partsByMessage, err := e.store.PartsForChat(ctx, chatID)
		if err != nil {
			return provider.PromptEnvelope{}, err
		}
		for _, msg := range messages {
			if summary, ok := compactionSummary(partsByMessage[msg.ID]); ok {
				envelope.Instructions = e.baseInstructionsForChat(session, chat)
				envelope.Items = append(envelope.Items[:0], compactedHistoryMessage(summary))
				continue
			}
			items, err := e.conversationMessagesForStoredMessage(msg, partsByMessage[msg.ID], e.preserveThinkingEnabled(session))
			if err != nil {
				return provider.PromptEnvelope{}, err
			}
			envelope.Items = append(envelope.Items, items...)
		}
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

func (e *Engine) baseInstructionsForChat(session domain.Session, chat domain.Chat) []provider.InstructionBlock {
	instructions := []provider.InstructionBlock{{
		Kind: provider.InstructionKindBaseSystem,
		Text: systemPrompt(),
	}}
	if roleText := strings.TrimSpace(chatRoleInstructions(chat)); roleText != "" {
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

func chatRoleInstructions(chat domain.Chat) string {
	switch chat.WorkflowRole {
	case domain.WorkflowRoleDecomposition:
		return strings.TrimSpace(`This chat is a decomposition worker.

Focus on one assigned milestone and its todo bucket.
- Break that milestone into concrete todo items.
- Update only that milestone and its todo bucket.
- Do not edit code in this chat unless the user explicitly changes the workflow.`)
	case domain.WorkflowRoleExecution:
		return strings.TrimSpace(`This chat is an execution worker.

Focus only on the assigned milestone and todo bucket.
- Implement the work using available coding tools.
- Keep todo item status updated as you progress.
- Do not rewrite unrelated milestones or todo buckets.`)
	case domain.WorkflowRoleOrchestrator, domain.WorkflowRoleGeneral, domain.WorkflowRolePlanning:
		return strings.TrimSpace(`This chat is the main orchestration thread.

You may discuss, ask clarifying questions, manage milestones, decompose work inline, and start background decomposition or execution chats when helpful.
- Use milestones for longer-horizon work.
- Use todos for concrete execution steps.
- For small changes, inline decomposition is fine; a separate decomposition chat is optional.`)
	default:
		return ""
	}
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
		parts = append(parts, domain.Part{Kind: domain.PartKindText, Body: prompt})
	}
	for _, draft := range drafts {
		raw, err := attachment.EncodeMeta(draft.Metadata)
		if err != nil {
			return provider.Message{}, false, err
		}
		parts = append(parts, domain.Part{
			Kind:     domain.PartKindAttachment,
			Body:     draft.Name,
			MetaJSON: raw,
		})
	}
	for _, ref := range refs {
		raw, err := reference.EncodeMeta(reference.Metadata(ref))
		if err != nil {
			return provider.Message{}, false, err
		}
		parts = append(parts, domain.Part{
			Kind:     domain.PartKindReference,
			Body:     ref.Display,
			MetaJSON: raw,
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

func (e *Engine) conversationMessagesForStoredMessage(msg domain.Message, parts []domain.Part, preserveThinking bool) ([]provider.Message, error) {
	switch msg.Role {
	case domain.MessageRoleAssistant:
		if assistantMsg, ok := structuredAssistantMessage(parts, preserveThinking); ok {
			return []provider.Message{assistantMsg}, nil
		}
	case domain.MessageRoleTool:
		if toolMsg, ok := structuredToolMessage(parts); ok {
			return []provider.Message{toolMsg}, nil
		}
	case domain.MessageRoleUser:
		msg, ok, err := e.userMessageWithContext(parts)
		if err != nil {
			return nil, err
		}
		if ok {
			return []provider.Message{msg}, nil
		}
	}
	content := stringifyParts(parts, preserveThinking)
	if strings.TrimSpace(content) == "" {
		if msg.Role == domain.MessageRoleAssistant && assistantSummaryExcludedFromModel(parts) {
			return nil, nil
		}
		content = msg.Summary
	}
	if strings.TrimSpace(content) == "" {
		return nil, nil
	}
	role := msg.Role
	if msg.Role == domain.MessageRoleTool {
		role = domain.MessageRoleUser
	}
	return []provider.Message{{Role: role, Content: content}}, nil
}

func assistantSummaryExcludedFromModel(parts []domain.Part) bool {
	if len(parts) == 0 {
		return false
	}
	for _, part := range parts {
		switch part.Kind {
		case domain.PartKindSystemNotice, domain.PartKindEventNotice:
			continue
		default:
			return false
		}
	}
	return true
}

func (e *Engine) userMessageWithContext(parts []domain.Part) (provider.Message, bool, error) {
	contentParts := make([]provider.ContentPart, 0, len(parts)+1)
	attachmentParts := make([]provider.ContentPart, 0, len(parts))
	var prompt string
	var refs []reference.Metadata
	var hasStructured bool
	for _, part := range parts {
		switch part.Kind {
		case domain.PartKindText:
			if strings.TrimSpace(part.Body) != "" {
				prompt = part.Body
			}
		case domain.PartKindReference:
			hasStructured = true
			meta, err := reference.DecodeMeta(part.MetaJSON)
			if err != nil {
				return provider.Message{}, false, err
			}
			refs = append(refs, meta)
		case domain.PartKindAttachment:
			hasStructured = true
			meta, err := attachment.DecodeMeta(part.MetaJSON)
			if err != nil {
				return provider.Message{}, false, err
			}
			switch attachment.ClassifyMIME(meta.MIME) {
			case attachment.KindText:
				body, err := e.files.ReadText(meta)
				if err != nil {
					return provider.Message{}, false, err
				}
				attachmentParts = append(attachmentParts, provider.TextPart("Attached file "+meta.Name+":\n"+body))
			case attachment.KindImage:
				data, err := e.files.ReadBytes(meta)
				if err != nil {
					return provider.Message{}, false, err
				}
				attachmentParts = append(attachmentParts, provider.ImagePart(meta.MIME, data))
			default:
				return provider.Message{}, false, fmt.Errorf("unsupported attachment in conversation: %s", meta.MIME)
			}
		}
	}
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
	contentParts = append(contentParts, attachmentParts...)
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

func structuredAssistantMessage(parts []domain.Part, preserveThinking bool) (provider.Message, bool) {
	var toolCalls []provider.ToolCall
	textChunks := make([]string, 0, 2)
	reasoningChunks := make([]string, 0, 1)
	for _, part := range parts {
		switch part.Kind {
		case domain.PartKindToolCall:
			call, ok := storedToolCall(part)
			if !ok || strings.TrimSpace(call.ToolCallID) == "" {
				return provider.Message{}, false
			}
			toolCalls = append(toolCalls, tools.ToolCall(call))
		case domain.PartKindText:
			if strings.TrimSpace(part.Body) != "" {
				textChunks = append(textChunks, strings.TrimSpace(part.Body))
			}
		case domain.PartKindReasoning:
			if preserveThinking && strings.TrimSpace(part.Body) != "" {
				reasoningChunks = append(reasoningChunks, strings.TrimSpace(part.Body))
			}
		case domain.PartKindSystemNotice:
			if strings.TrimSpace(part.Body) != "" && strings.TrimSpace(part.Body) != "usage" {
				textChunks = append(textChunks, strings.TrimSpace(part.Body))
			}
		case domain.PartKindEventNotice:
			continue
		}
	}
	if len(toolCalls) == 0 {
		return provider.Message{}, false
	}
	return provider.Message{
		Role:      domain.MessageRoleAssistant,
		Content:   assistantConversationContent(textChunks, reasoningChunks, preserveThinking),
		ToolCalls: toolCalls,
	}, true
}

func structuredToolMessage(parts []domain.Part) (provider.Message, bool) {
	for _, part := range parts {
		if part.Kind != domain.PartKindToolOutput {
			continue
		}
		meta := partMetaMap(part)
		toolCallID := strings.TrimSpace(meta["tool_call_id"])
		if toolCallID == "" {
			return provider.Message{}, false
		}
		body := strings.TrimSpace(part.Body)
		if formatted, ok := tools.ModelTextForPart(part, diffBody(parts)); ok {
			body = strings.TrimSpace(formatted)
		} else if diff := diffBody(parts); diff != "" {
			if body != "" {
				body += "\n\nDiff:\n" + diff
			} else {
				body = "Diff:\n" + diff
			}
		}
		return provider.Message{
			Role:       domain.MessageRoleTool,
			Content:    body,
			ToolCallID: toolCallID,
		}, true
	}
	return provider.Message{}, false
}

func diffBody(parts []domain.Part) string {
	for _, part := range parts {
		if part.Kind == domain.PartKindDiff && strings.TrimSpace(part.Body) != "" {
			return part.Body
		}
	}
	return ""
}

func stringifyParts(parts []domain.Part, preserveThinking bool) string {
	var chunks []string
	var reasoningChunks []string
	for _, part := range parts {
		switch part.Kind {
		case domain.PartKindCompaction:
			continue
		case domain.PartKindAttachment:
			continue
		case domain.PartKindReference:
			continue
		case domain.PartKindReasoning:
			if preserveThinking && strings.TrimSpace(part.Body) != "" {
				reasoningChunks = append(reasoningChunks, strings.TrimSpace(part.Body))
				continue
			}
			chunks = append(chunks, "Reasoning:\n"+part.Body)
		case domain.PartKindToolCall:
			if callText := toolCallContext(part); callText != "" {
				chunks = append(chunks, callText)
			}
		case domain.PartKindToolOutput:
			body := part.Body
			if formatted, ok := tools.ModelTextForPart(part, ""); ok {
				body = formatted
			}
			chunks = append(chunks, "Tool output:\n"+body)
		case domain.PartKindDiff:
			chunks = append(chunks, "Diff:\n"+part.Body)
		case domain.PartKindTaskUpdate:
			body := part.Body
			if formatted, ok := tools.ModelTextForPart(part, ""); ok {
				body = formatted
			}
			chunks = append(chunks, "Task update:\n"+body)
		case domain.PartKindPlanUpdate:
			body := part.Body
			if formatted, ok := tools.ModelTextForPart(part, ""); ok {
				body = formatted
			}
			chunks = append(chunks, "Plan update:\n"+body)
		case domain.PartKindApprovalRequest, domain.PartKindSystemNotice, domain.PartKindEventNotice:
			continue
		default:
			chunks = append(chunks, part.Body)
		}
	}
	if preserveThinking && len(reasoningChunks) > 0 {
		chunks = append([]string{formatThinkingBlock(strings.Join(reasoningChunks, "\n\n"))}, chunks...)
	}
	return strings.TrimSpace(strings.Join(chunks, "\n\n"))
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

func providerCfgForSession(cfg config.Config, session domain.Session) config.Provider {
	if providerCfg, ok := cfg.Provider(session.ProviderID); ok {
		return providerCfg
	}
	return config.Provider{}
}

func compactionSummary(parts []domain.Part) (string, bool) {
	for _, part := range parts {
		if part.Kind == domain.PartKindCompaction && strings.TrimSpace(part.Body) != "" {
			return strings.TrimSpace(part.Body), true
		}
	}
	return "", false
}

func toolCallContext(part domain.Part) string {
	if call, ok := storedToolCall(part); ok {
		return "Tool call:\n" + call.ContextString()
	}
	if call, _ := parseToolCall(part.Body); call != nil {
		return "Tool call:\n" + call.ContextString()
	}
	body := strings.TrimSpace(part.Body)
	if body == "" {
		return ""
	}
	return "Tool call:\n" + body
}

func storedToolCall(part domain.Part) (tools.Request, bool) {
	if strings.TrimSpace(part.MetaJSON) == "" {
		return tools.Request{}, false
	}
	call, err := tools.RequestFromMeta(part.MetaJSON)
	if err != nil || call.Tool == "" {
		return tools.Request{}, false
	}
	return call, true
}

func partMetaMap(part domain.Part) map[string]string {
	if strings.TrimSpace(part.MetaJSON) == "" {
		return nil
	}
	var meta map[string]string
	if err := json.Unmarshal([]byte(part.MetaJSON), &meta); err != nil {
		return nil
	}
	return meta
}

func systemPrompt() string {
	return strings.TrimSpace(`
You are koder, a terminal coding agent.

Use the provided tools whenever they are needed to inspect files, search the workspace, run commands, edit files, or fetch URLs.

Rules:
- Use tools instead of claiming you cannot run commands.
- Use one tool call at a time.
- After receiving tool output, continue the task.
- If no tool is needed, answer normally.
- Paths are relative to the current workspace.
- Keep tool arguments precise and minimal.
`)
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

func (e *Engine) autoCompactChatIfNeeded(ctx context.Context, session domain.Session, chatID int64, client *provider.Client, out chan<- domain.Event) (bool, error) {
	messages, parts, err := e.store.PartsForChat(ctx, chatID)
	if err != nil {
		return false, err
	}
	metrics, ok := sessionctx.FromMessages(e.cfg, session, messages, parts)
	if !ok {
		return false, nil
	}
	providerCfg, ok := e.cfg.Provider(session.ProviderID)
	if !ok {
		return false, nil
	}
	threshold := providerCfg.AutoCompactAt
	if threshold <= 0 {
		threshold = 85
	}
	if metrics.UsagePercent < threshold {
		return false, nil
	}
	if out != nil {
		out <- domain.Event{Kind: domain.EventKindStatus, Text: fmt.Sprintf("Auto-compacting at %d%% context used", metrics.UsagePercent)}
	}
	if err := e.compactSession(ctx, session, chatID, client, "auto", out); err != nil {
		return false, err
	}
	return true, nil
}

func (e *Engine) compactSession(ctx context.Context, session domain.Session, chatID int64, client *provider.Client, trigger string, out chan<- domain.Event) error {
	messages, err := e.buildConversation(ctx, session.ID, chatID)
	if err != nil {
		return err
	}
	if len(messages) <= 1 {
		return nil
	}
	resp, err := client.CompleteChat(ctx, e.chatRequest(session, domain.Chat{}, append(messages, provider.Message{
		Role:    domain.MessageRoleUser,
		Content: compactPrompt(),
	}), false))
	if err != nil {
		return err
	}
	summary, usage := resp.Text, resp.Usage
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return nil
	}
	msg, err := e.store.AddChatMessage(ctx, chatID, domain.MessageRoleAssistant, "Compacted session summary")
	if err != nil {
		return err
	}
	meta, _ := json.Marshal(map[string]string{"trigger": trigger})
	if _, err := e.store.AddPart(ctx, msg.ID, domain.PartKindCompaction, summary, string(meta)); err != nil {
		return err
	}
	if usage.TotalTokens > 0 {
		usageMeta, _ := json.Marshal(usage)
		if _, err := e.store.AddPart(ctx, msg.ID, domain.PartKindSystemNotice, "usage", string(usageMeta)); err != nil {
			return err
		}
		if out != nil {
			out <- domain.Event{Kind: domain.EventKindUsage, Usage: usage}
		}
	}
	if out != nil {
		out <- domain.Event{Kind: domain.EventKindStatus, Text: "Session compacted"}
	}
	return nil
}

func (e *Engine) handleModelToolCall(ctx context.Context, session domain.Session, chat domain.Chat, req tools.Request) (domain.Event, error) {
	sessionID := session.ID
	if sessionID > 0 {
		if latest, err := e.store.GetSession(ctx, sessionID); err == nil {
			if strings.TrimSpace(latest.PermissionProfile) == "" {
				latest.PermissionProfile = session.PermissionProfile
			}
			if strings.TrimSpace(latest.ProjectRoot) == "" {
				latest.ProjectRoot = session.ProjectRoot
			}
			session = latest
		}
	}
	if chat.ID > 0 {
		if latest, err := e.store.GetChat(ctx, chat.ID); err == nil {
			if strings.TrimSpace(latest.PermissionProfile) == "" {
				latest.PermissionProfile = chat.PermissionProfile
			}
			chat = latest
		}
	}
	req, err := tools.Normalize(req)
	if err != nil {
		events, persistErr := e.persistToolFailure(ctx, chat.ID, sessionID, req, err)
		if persistErr != nil {
			return domain.Event{}, persistErr
		}
		final := <-events
		return final, nil
	}
	toolSpec, ok := tools.Lookup(req.Tool)
	if !ok {
		return domain.Event{}, fmt.Errorf("unsupported model tool %q", req.Tool)
	}
	if !toolEnabledForSession(e.cfg, session, req.Tool) {
		return e.recordDisabledToolResult(ctx, chat.ID, sessionID, req)
	}

	decision := permission.Decision{Mode: domain.PermissionModeAllow}
	if !toolSpec.BypassesPermission() {
		decision = permission.Evaluate(e.cfg.Permissions, effectivePermissionProfile(e.cfg, session, chat), session.PermissionRules, e.permissionRequest(session, req))
	}
	if decision.Mode == domain.PermissionModeDeny {
		text := fmt.Sprintf("%s denied by policy", req.Tool)
		if strings.TrimSpace(decision.Reason) != "" {
			text = fmt.Sprintf("%s denied by policy: %s", req.Tool, decision.Reason)
		}
		msg, err := e.store.AddChatMessage(ctx, chat.ID, domain.MessageRoleTool, string(req.Tool))
		if err != nil {
			return domain.Event{}, err
		}
		meta, _ := json.Marshal(tools.MetaWithStoredResult(req.Meta(), domain.PartKindToolOutput, req.Tool, tools.StoredResultStatusDenied, tools.DeniedStoredResult{
			Message: text,
		}))
		if _, err := e.store.AddPart(ctx, msg.ID, domain.PartKindToolOutput, text, string(meta)); err != nil {
			return domain.Event{}, err
		}
		return domain.Event{Kind: domain.EventKindToolResult, Tool: req.Tool, Text: text}, nil
	}
	if decision.Mode == domain.PermissionModeAsk {
		storedArgs, err := serializeRequest(req)
		if err != nil {
			return domain.Event{}, err
		}
		approval, err := e.store.CreateChatApproval(ctx, chat.ID, req.Tool, storedArgs)
		if err != nil {
			return domain.Event{}, err
		}
		preview := tools.Preview(req)
		if err := e.recordApprovalRequest(ctx, chat.ID, sessionID, req.Tool, approval.ID, preview, req.ToolCallID); err != nil {
			return domain.Event{}, err
		}
		text := fmt.Sprintf("%s requires approval", req.Tool)
		if strings.TrimSpace(decision.Reason) != "" {
			text += ": " + decision.Reason
		}
		return domain.Event{
			Kind: domain.EventKindApprovalAsk,
			Text: text,
			Tool: req.Tool,
			Meta: map[string]string{
				"approval_id":  strconv.FormatInt(approval.ID, 10),
				"tool":         string(req.Tool),
				"command":      preview,
				"reason":       decision.Reason,
				"tool_call_id": req.ToolCallID,
			},
		}, nil
	}
	result, err := e.registry.ExecuteWithChat(ctx, e.store, sessionID, chat, req)
	if err != nil {
		events, persistErr := e.persistToolFailure(ctx, chat.ID, sessionID, req, err)
		if persistErr != nil {
			return domain.Event{}, persistErr
		}
		final := <-events
		return final, nil
	}
	evt, err := e.persistToolResult(ctx, chat.ID, sessionID, req, result)
	if err != nil {
		return domain.Event{}, err
	}
	final := <-evt
	return final, nil
}

type preparedToolCall struct {
	req   tools.Request
	event domain.Event
	run   bool
}

type completedToolCall struct {
	events []domain.Event
	err    error
}

func (e *Engine) parseProviderToolCalls(raw []provider.ToolCall, sessionID int64) ([]tools.Request, error) {
	calls := make([]tools.Request, 0, len(raw))
	for _, item := range raw {
		call, err := tools.ParseProviderCall(item)
		if err != nil {
			return nil, err
		}
		e.recordLifecycle(sessionID, "tool_call_parsed", call.ContextString(), map[string]string{"tool": string(call.Tool), "tool_call_id": call.ToolCallID})
		calls = append(calls, call)
	}
	return calls, nil
}

func (e *Engine) persistAssistantToolCalls(ctx context.Context, chatID, sessionID int64, calls []tools.Request, text string, usage domain.Usage) error {
	summary := "tool"
	if len(calls) == 1 {
		summary = fmt.Sprintf("tool:%s", calls[0].Tool)
	} else if len(calls) > 1 {
		summary = fmt.Sprintf("tools:%d", len(calls))
	}
	assistantMsg, err := e.store.AddChatMessage(ctx, chatID, domain.MessageRoleAssistant, summary)
	if err != nil {
		return err
	}
	for _, call := range calls {
		meta, _ := json.Marshal(call)
		body := call.ContextString()
		if _, err := e.store.AddPart(ctx, assistantMsg.ID, domain.PartKindToolCall, body, string(meta)); err != nil {
			return err
		}
	}
	if text != "" {
		if _, err := e.store.AddPart(ctx, assistantMsg.ID, domain.PartKindText, text, ""); err != nil {
			return err
		}
	}
	if usage.TotalTokens > 0 {
		usageMeta, _ := json.Marshal(usage)
		if _, err := e.store.AddPart(ctx, assistantMsg.ID, domain.PartKindSystemNotice, "usage", string(usageMeta)); err != nil {
			return err
		}
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
		out <- domain.Event{Kind: domain.EventKindToolStart, Tool: item.req.Tool, Text: tools.Preview(item.req)}
		go func(req tools.Request) {
			events, err := e.executePreparedToolCall(ctx, chat.ID, session.ID, req)
			results <- completedToolCall{events: events, err: err}
		}(item.req)
	}

	var firstErr error
	for i := 0; i < execCount; i++ {
		completed := <-results
		if completed.err != nil {
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
	return needsApproval, nil
}

func (e *Engine) prepareModelToolCall(ctx context.Context, session domain.Session, chat domain.Chat, req tools.Request) (preparedToolCall, error) {
	sessionID := session.ID
	if sessionID > 0 {
		if latest, err := e.store.GetSession(ctx, sessionID); err == nil {
			if strings.TrimSpace(latest.PermissionProfile) == "" {
				latest.PermissionProfile = session.PermissionProfile
			}
			if strings.TrimSpace(latest.ProjectRoot) == "" {
				latest.ProjectRoot = session.ProjectRoot
			}
			session = latest
		}
	}
	if chat.ID > 0 {
		if latest, err := e.store.GetChat(ctx, chat.ID); err == nil {
			if strings.TrimSpace(latest.PermissionProfile) == "" {
				latest.PermissionProfile = chat.PermissionProfile
			}
			chat = latest
		}
	}
	req, err := tools.Normalize(req)
	if err != nil {
		events, persistErr := e.persistToolFailure(ctx, chat.ID, sessionID, req, err)
		if persistErr != nil {
			return preparedToolCall{}, persistErr
		}
		final := <-events
		return preparedToolCall{
			req:   req,
			event: final,
			run:   false,
		}, nil
	}
	toolSpec, ok := tools.Lookup(req.Tool)
	if !ok {
		return preparedToolCall{}, fmt.Errorf("unsupported model tool %q", req.Tool)
	}
	if !toolEnabledForSession(e.cfg, session, req.Tool) {
		evt, err := e.recordDisabledToolResult(ctx, chat.ID, sessionID, req)
		if err != nil {
			return preparedToolCall{}, err
		}
		return preparedToolCall{
			req:   req,
			event: evt,
			run:   false,
		}, nil
	}

	decision := permission.Decision{Mode: domain.PermissionModeAllow}
	if !toolSpec.BypassesPermission() {
		decision = permission.Evaluate(e.cfg.Permissions, effectivePermissionProfile(e.cfg, session, chat), session.PermissionRules, e.permissionRequest(session, req))
	}
	if decision.Mode == domain.PermissionModeDeny {
		text := fmt.Sprintf("%s denied by policy", req.Tool)
		if strings.TrimSpace(decision.Reason) != "" {
			text = fmt.Sprintf("%s denied by policy: %s", req.Tool, decision.Reason)
		}
		msg, err := e.store.AddChatMessage(ctx, chat.ID, domain.MessageRoleTool, string(req.Tool))
		if err != nil {
			return preparedToolCall{}, err
		}
		meta, _ := json.Marshal(tools.MetaWithStoredResult(req.Meta(), domain.PartKindToolOutput, req.Tool, tools.StoredResultStatusDenied, tools.DeniedStoredResult{
			Message: text,
		}))
		if _, err := e.store.AddPart(ctx, msg.ID, domain.PartKindToolOutput, text, string(meta)); err != nil {
			return preparedToolCall{}, err
		}
		return preparedToolCall{
			req:   req,
			event: domain.Event{Kind: domain.EventKindToolResult, Tool: req.Tool, Text: text},
		}, nil
	}
	if decision.Mode == domain.PermissionModeAsk {
		storedArgs, err := serializeRequest(req)
		if err != nil {
			return preparedToolCall{}, err
		}
		approval, err := e.store.CreateChatApproval(ctx, chat.ID, req.Tool, storedArgs)
		if err != nil {
			return preparedToolCall{}, err
		}
		preview := tools.Preview(req)
		if err := e.recordApprovalRequest(ctx, chat.ID, sessionID, req.Tool, approval.ID, preview, req.ToolCallID); err != nil {
			return preparedToolCall{}, err
		}
		text := fmt.Sprintf("%s requires approval", req.Tool)
		if strings.TrimSpace(decision.Reason) != "" {
			text += ": " + decision.Reason
		}
		return preparedToolCall{
			req: req,
			event: domain.Event{
				Kind: domain.EventKindApprovalAsk,
				Text: text,
				Tool: req.Tool,
				Meta: map[string]string{
					"approval_id":  strconv.FormatInt(approval.ID, 10),
					"tool":         string(req.Tool),
					"command":      preview,
					"reason":       decision.Reason,
					"tool_call_id": req.ToolCallID,
				},
			},
		}, nil
	}
	return preparedToolCall{req: req, run: true}, nil
}

func (e *Engine) recordDisabledToolResult(ctx context.Context, chatID, sessionID int64, req tools.Request) (domain.Event, error) {
	text := fmt.Sprintf("%s disabled for this session", req.Tool)
	if sessionID == 0 {
		return domain.Event{Kind: domain.EventKindToolResult, Tool: req.Tool, Text: text}, nil
	}
	msg, err := e.store.AddChatMessage(ctx, chatID, domain.MessageRoleTool, string(req.Tool))
	if err != nil {
		return domain.Event{}, err
	}
	meta, _ := json.Marshal(tools.MetaWithStoredResult(req.Meta(), domain.PartKindToolOutput, req.Tool, tools.StoredResultStatusDenied, tools.DeniedStoredResult{
		Message: text,
	}))
	if _, err := e.store.AddPart(ctx, msg.ID, domain.PartKindToolOutput, text, string(meta)); err != nil {
		return domain.Event{}, err
	}
	return domain.Event{Kind: domain.EventKindToolResult, Tool: req.Tool, Text: text}, nil
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

func (e *Engine) executePreparedToolCall(ctx context.Context, chatID, sessionID int64, req tools.Request) ([]domain.Event, error) {
	e.recordLifecycle(sessionID, "tool_execution_started", req.ContextString(), map[string]string{"tool": string(req.Tool), "tool_call_id": req.ToolCallID})
	chat, chatErr := e.store.GetChat(ctx, chatID)
	if chatErr != nil {
		return nil, chatErr
	}
	result, err := e.registry.ExecuteWithChat(ctx, e.store, sessionID, chat, req)
	if err != nil {
		e.recordLifecycle(sessionID, "tool_execution_failed", err.Error(), map[string]string{"tool": string(req.Tool), "tool_call_id": req.ToolCallID})
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

func effectivePermissionProfile(cfg config.Config, session domain.Session, chat domain.Chat) string {
	if strings.TrimSpace(chat.PermissionProfile) != "" {
		return chat.PermissionProfile
	}
	if strings.TrimSpace(session.PermissionProfile) != "" {
		return session.PermissionProfile
	}
	return cfg.Permissions.Profile
}

func (e *Engine) permissionRequest(session domain.Session, req tools.Request) permission.Request {
	projectRoot := strings.TrimSpace(session.ProjectRoot)
	if projectRoot == "" {
		projectRoot = agents.FindProjectRoot(e.workdir)
	}
	targets, outsideProject, ambiguous := e.resolvePermissionTargets(projectRoot, req)
	return permission.Request{
		Tool:           req.Tool,
		Pattern:        tools.Preview(req),
		ProjectRoot:    projectRoot,
		Targets:        targets,
		OutsideProject: outsideProject,
		Ambiguous:      ambiguous,
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
	case domain.ToolKindRead, domain.ToolKindEdit, domain.ToolKindWrite:
		raws = append(raws, req.Args["path"])
	case domain.ToolKindGlob, domain.ToolKindGrep:
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

func (e *Engine) recordApprovalRequest(ctx context.Context, chatID, sessionID int64, tool domain.ToolKind, approvalID int64, preview, toolCallID string) error {
	msg, err := e.store.AddChatMessage(ctx, chatID, domain.MessageRoleTool, fmt.Sprintf("approval:%s", tool))
	if err != nil {
		return err
	}
	payload := map[string]string{
		"approval_id": strconv.FormatInt(approvalID, 10),
		"tool":        string(tool),
		"status":      "pending",
		"command":     preview,
	}
	if strings.TrimSpace(toolCallID) != "" {
		payload["tool_call_id"] = toolCallID
	}
	meta, _ := json.Marshal(payload)
	body := fmt.Sprintf("Approval required for %s: %s", tool, preview)
	_, err = e.store.AddPart(ctx, msg.ID, domain.PartKindApprovalRequest, body, string(meta))
	return err
}

func (e *Engine) recordApprovalReply(ctx context.Context, chatID, sessionID int64, tool domain.ToolKind, approvalID int64, status, preview, toolCallID string) error {
	msg, err := e.store.AddChatMessage(ctx, chatID, domain.MessageRoleTool, fmt.Sprintf("approval:%s:%s", tool, status))
	if err != nil {
		return err
	}
	body := fmt.Sprintf("Approval %d %s for %s: %s", approvalID, status, tool, preview)
	payload := map[string]string{
		"approval_id": strconv.FormatInt(approvalID, 10),
		"tool":        string(tool),
		"status":      status,
		"command":     preview,
	}
	if strings.TrimSpace(toolCallID) != "" {
		payload["tool_call_id"] = toolCallID
	}
	if status == "denied" {
		payload = tools.MetaWithStoredResult(payload, domain.PartKindToolOutput, tool, tools.StoredResultStatusDenied, tools.DeniedStoredResult{
			Message: body,
		})
	}
	meta, _ := json.Marshal(payload)
	if status == "denied" {
		_, err = e.store.AddPart(ctx, msg.ID, domain.PartKindToolOutput, body, string(meta))
		return err
	}
	_, err = e.store.AddPart(ctx, msg.ID, domain.PartKindSystemNotice, body, string(meta))
	return err
}

func approvalPreviewFromStored(tool domain.ToolKind, raw string) string {
	req, err := requestFromStoredApproval(tool, raw)
	if err != nil {
		return raw
	}
	return tools.Preview(req)
}
