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

	"github.com/lkarlslund/koder/internal/agents"
	"github.com/lkarlslund/koder/internal/attachment"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/debugsrv"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/permission"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/sessionctx"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
	_ "github.com/lkarlslund/koder/internal/tools/all"
)

type Engine struct {
	cfg      config.Config
	store    *store.Store
	registry *tools.Registry
	debug    *debugsrv.Recorder
	files    *attachment.Manager
	caps     *provider.CapabilityStore
	agents   *agents.Manager
	workdir  string
}

var patchPathPattern = regexp.MustCompile(`(?m)^(?:\+\+\+|---)\s+(?:a/|b/)?([^\t\n]+)`)

func New(cfg config.Config, st *store.Store, registry *tools.Registry, debug *debugsrv.Recorder, workdir string) *Engine {
	return &Engine{
		cfg:      cfg,
		store:    st,
		registry: registry,
		debug:    debug,
		files:    attachment.NewManager(cfg.StateDir()),
		caps:     provider.NewCapabilityStore(cfg.StateDir()),
		agents:   agents.NewManager(cfg.StateDir(), filepath.Join(filepath.Dir(cfg.Path()), "AGENTS.md")),
		workdir:  workdir,
	}
}

func (e *Engine) UpdateConfig(cfg config.Config) {
	e.cfg = cfg
	e.agents = agents.NewManager(cfg.StateDir(), filepath.Join(filepath.Dir(cfg.Path()), "AGENTS.md"))
}

func (e *Engine) RunPrompt(ctx context.Context, session domain.Session, prompt string) (<-chan domain.Event, error) {
	return e.RunPromptWithAttachments(ctx, session, prompt, nil)
}

func (e *Engine) RunPromptWithAttachments(ctx context.Context, session domain.Session, prompt string, drafts []attachment.Draft) (<-chan domain.Event, error) {
	return e.runModelPrompt(ctx, session, prompt, drafts)
}

func (e *Engine) SetPermissionProfile(ctx context.Context, sessionID int64, profile string) (<-chan domain.Event, error) {
	return e.setPermissionProfile(ctx, sessionID, profile)
}

func (e *Engine) Approve(ctx context.Context, sessionID, approvalID int64) (<-chan domain.Event, error) {
	return e.approve(ctx, sessionID, strconv.FormatInt(approvalID, 10))
}

func (e *Engine) Deny(ctx context.Context, sessionID, approvalID int64) (<-chan domain.Event, error) {
	return e.deny(ctx, sessionID, strconv.FormatInt(approvalID, 10))
}

func (e *Engine) Compact(ctx context.Context, sessionID int64) (<-chan domain.Event, error) {
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
		if err := e.compactSession(ctx, session, client, "manual", out); err != nil {
			if interruptedErr(err) {
				e.emitInterrupted(out, session.ID)
				return
			}
			e.recordAssistantError(ctx, session.ID, err)
			out <- domain.Event{Kind: domain.EventKindError, Err: err}
			return
		}
		out <- domain.Event{Kind: domain.EventKindMessageDone}
	}()
	return out, nil
}

func (e *Engine) runModelPrompt(ctx context.Context, session domain.Session, prompt string, drafts []attachment.Draft) (<-chan domain.Event, error) {
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
				e.emitInterrupted(out, session.ID)
				return
			}
			e.recordAssistantError(ctx, session.ID, err)
			out <- domain.Event{Kind: domain.EventKindError, Err: err}
			return
		}
		e.recordLifecycle(session.ID, "prompt_started", prompt, map[string]string{"provider": session.ProviderID, "model": session.ModelID})
		compacted, err := e.autoCompactIfNeeded(ctx, session, client, out)
		if err != nil {
			if interruptedErr(err) {
				e.emitInterrupted(out, session.ID)
				return
			}
			e.recordAssistantError(ctx, session.ID, err)
			out <- domain.Event{Kind: domain.EventKindError, Err: err}
			return
		}
		if compacted {
			session, err = e.store.GetSession(ctx, session.ID)
			if err != nil {
				if interruptedErr(err) {
					e.emitInterrupted(out, session.ID)
					return
				}
				out <- domain.Event{Kind: domain.EventKindError, Err: err}
				return
			}
		}
		userMsg, err := e.persistUserPrompt(ctx, session, prompt, drafts)
		if err != nil {
			if interruptedErr(err) {
				e.emitInterrupted(out, session.ID)
				return
			}
			out <- domain.Event{Kind: domain.EventKindError, Err: err}
			return
		}
		e.recordLifecycle(session.ID, "user_message_persisted", prompt, map[string]string{"message_id": strconv.FormatInt(userMsg.ID, 10)})
		if err := e.continueModelTurn(ctx, session, client, out); err != nil {
			if interruptedErr(err) {
				e.emitInterrupted(out, session.ID)
				return
			}
			e.recordAssistantError(ctx, session.ID, err)
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

func (e *Engine) persistUserPrompt(ctx context.Context, session domain.Session, prompt string, drafts []attachment.Draft) (domain.Message, error) {
	userMsg, err := e.store.AddMessage(ctx, session.ID, domain.MessageRoleUser, prompt)
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
	return userMsg, nil
}

func (e *Engine) continueModelTurn(ctx context.Context, session domain.Session, client *provider.Client, out chan<- domain.Event) error {
	for steps := 0; steps < 6; steps++ {
		e.recordLifecycle(session.ID, "model_turn_started", "", map[string]string{"step": strconv.Itoa(steps + 1)})
		messages, buildErr := e.buildConversation(ctx, session.ID)
		if buildErr != nil {
			return buildErr
		}

		resp, completeErr := client.CompleteChat(ctx, provider.ChatRequest{
			Model:      session.ModelID,
			Messages:   messages,
			Tools:      tools.Definitions(),
			ToolChoice: "auto",
			Stream:     false,
		})
		if completeErr != nil {
			return completeErr
		}

		if len(resp.ToolCalls) > 0 {
			call, err := tools.ParseProviderCall(resp.ToolCalls[0])
			if err != nil {
				return err
			}
			e.recordLifecycle(session.ID, "tool_call_parsed", call.ContextString(), map[string]string{"tool": string(call.Tool), "tool_call_id": call.ToolCallID})
			assistantMsg, err := e.store.AddMessage(ctx, session.ID, domain.MessageRoleAssistant, fmt.Sprintf("tool:%s", call.Tool))
			if err != nil {
				return err
			}
			meta, _ := json.Marshal(call)
			body := call.ContextString()
			if _, err := e.store.AddPart(ctx, assistantMsg.ID, domain.PartKindToolCall, body, string(meta)); err != nil {
				return err
			}
			if strings.TrimSpace(resp.Text) != "" {
				if _, err := e.store.AddPart(ctx, assistantMsg.ID, domain.PartKindSystemNotice, strings.TrimSpace(resp.Text), ""); err != nil {
					return err
				}
			}
			if resp.Usage.TotalTokens > 0 {
				usageMeta, _ := json.Marshal(resp.Usage)
				if _, err := e.store.AddPart(ctx, assistantMsg.ID, domain.PartKindSystemNotice, "usage", string(usageMeta)); err != nil {
					return err
				}
				out <- domain.Event{Kind: domain.EventKindUsage, Usage: resp.Usage}
			}
			evt, handledErr := e.handleModelToolCall(ctx, session, call)
			if handledErr != nil {
				return handledErr
			}
			out <- evt
			if evt.Kind == domain.EventKindApprovalAsk {
				return nil
			}
			continue
		}

		text, reasoning, usage := resp.Text, resp.Reasoning, resp.Usage
		call, plain := parseToolCall(text)
		if call != nil {
			e.recordLifecycle(session.ID, "tool_call_parsed", call.ContextString(), map[string]string{"tool": string(call.Tool), "tool_call_id": call.ToolCallID})
			assistantMsg, err := e.store.AddMessage(ctx, session.ID, domain.MessageRoleAssistant, fmt.Sprintf("tool:%s", call.Tool))
			if err != nil {
				return err
			}
			meta, _ := json.Marshal(call)
			if _, err := e.store.AddPart(ctx, assistantMsg.ID, domain.PartKindToolCall, strings.TrimSpace(text), string(meta)); err != nil {
				return err
			}
			if strings.TrimSpace(plain) != "" {
				if _, err := e.store.AddPart(ctx, assistantMsg.ID, domain.PartKindSystemNotice, strings.TrimSpace(plain), ""); err != nil {
					return err
				}
			}

			evt, handledErr := e.handleModelToolCall(ctx, session, *call)
			if handledErr != nil {
				return handledErr
			}
			out <- evt
			if evt.Kind == domain.EventKindApprovalAsk {
				return nil
			}
			continue
		}

		assistantMsg, err := e.store.AddMessage(ctx, session.ID, domain.MessageRoleAssistant, strings.TrimSpace(text))
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
	return fmt.Errorf("tool loop exceeded max steps")
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
	resp, err := client.CompleteChat(ctx, provider.ChatRequest{
		Model:    session.ModelID,
		Messages: messages,
		Stream:   false,
	})
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
		content := stringifyParts(partsByMessage[msg.ID])
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

func (e *Engine) setPermissionProfile(ctx context.Context, sessionID int64, raw string) (<-chan domain.Event, error) {
	profile := strings.TrimSpace(raw)
	if profile == "" {
		return nil, fmt.Errorf("permission profile is required; choose one of: %s", strings.Join(permission.ProfileNames(e.cfg.Permissions), "|"))
	}
	if !permission.IsBuiltinProfile(profile) {
		if _, ok := e.cfg.Permissions.Profiles[profile]; !ok {
			return nil, fmt.Errorf("unknown permission profile %q", profile)
		}
	}
	if sessionID == 0 {
		return emitOnce(domain.Event{
			Kind: domain.EventKindStatus,
			Text: fmt.Sprintf("permission profile set to %s", permission.DisplayName(profile)),
			Meta: map[string]string{"permission_profile": profile},
		}), nil
	}
	if err := e.store.SetSessionPermissionProfile(ctx, sessionID, profile); err != nil {
		return nil, err
	}
	return emitOnce(domain.Event{
		Kind: domain.EventKindStatus,
		Text: fmt.Sprintf("permission profile set to %s", permission.DisplayName(profile)),
		Meta: map[string]string{"permission_profile": profile},
	}), nil
}

func (e *Engine) approve(ctx context.Context, sessionID int64, rawID string) (<-chan domain.Event, error) {
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
	if err := e.recordApprovalReply(ctx, sessionID, item.Tool, id, "approved", tools.Preview(req), req.ToolCallID); err != nil {
		return nil, err
	}
	e.recordLifecycle(sessionID, "tool_execution_started", item.Command, map[string]string{"tool": string(item.Tool), "approval_id": strconv.FormatInt(id, 10)})
	result, execErr := e.registry.Execute(ctx, req)
	if execErr != nil {
		e.recordLifecycle(sessionID, "tool_execution_failed", execErr.Error(), map[string]string{"tool": string(item.Tool), "approval_id": strconv.FormatInt(id, 10)})
		if interruptedErr(execErr) {
			return emitOnce(domain.Event{Kind: domain.EventKindStatus, Text: "Interrupted"}), nil
		}
		return emitOnce(domain.Event{Kind: domain.EventKindError, Err: execErr}), nil
	}
	e.recordLifecycle(sessionID, "tool_execution_finished", item.Command, map[string]string{"tool": string(item.Tool), "approval_id": strconv.FormatInt(id, 10)})
	toolEvents, err := e.persistToolResult(ctx, sessionID, req, result)
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
		compacted, err := e.autoCompactIfNeeded(ctx, session, client, out)
		if err != nil {
			if interruptedErr(err) {
				e.emitInterrupted(out, session.ID)
				return
			}
			e.recordAssistantError(ctx, session.ID, err)
			out <- domain.Event{Kind: domain.EventKindError, Err: err}
			return
		}
		if compacted {
			session, err = e.store.GetSession(ctx, session.ID)
			if err != nil {
				if interruptedErr(err) {
					e.emitInterrupted(out, session.ID)
					return
				}
				out <- domain.Event{Kind: domain.EventKindError, Err: err}
				return
			}
		}
		if err := e.continueModelTurn(ctx, session, client, out); err != nil {
			if interruptedErr(err) {
				e.emitInterrupted(out, session.ID)
				return
			}
			e.recordAssistantError(ctx, session.ID, err)
			out <- domain.Event{Kind: domain.EventKindError, Err: err}
		}
	}()
	return out, nil
}

func (e *Engine) deny(ctx context.Context, _ int64, rawID string) (<-chan domain.Event, error) {
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
	if err := e.recordApprovalReply(ctx, item.SessionID, item.Tool, id, "denied", approvalPreviewFromStored(item.Tool, item.Command), toolCallID); err != nil {
		return nil, err
	}
	return emitOnce(domain.Event{Kind: domain.EventKindApprovalReply, Text: fmt.Sprintf("approval %d denied", id)}), nil
}

func (e *Engine) persistToolResult(ctx context.Context, sessionID int64, req tools.Request, result tools.Result) (<-chan domain.Event, error) {
	events, err := e.registry.PersistResult(ctx, e.store, sessionID, req, result)
	if err != nil {
		return nil, err
	}
	summary, _ := tools.SummarizeResult(req, result)
	e.recordLifecycle(sessionID, "tool_result_persisted", summary, map[string]string{"tool": string(req.Tool)})
	return events, nil
}

func emitOnce(evt domain.Event) <-chan domain.Event {
	out := make(chan domain.Event, 1)
	out <- evt
	close(out)
	return out
}

func (e *Engine) recordAssistantError(ctx context.Context, sessionID int64, err error) {
	if err == nil || sessionID == 0 {
		return
	}
	if interruptedErr(err) {
		return
	}
	e.recordLifecycle(sessionID, "assistant_error", err.Error(), nil)
	msg, addErr := e.store.AddMessage(ctx, sessionID, domain.MessageRoleAssistant, errorSummary(err))
	if addErr != nil {
		return
	}
	_, _ = e.store.AddPart(ctx, msg.ID, domain.PartKindText, errorSummary(err), "")
}

func (e *Engine) recordLifecycle(sessionID int64, kind, text string, meta map[string]string) {
	if e.debug == nil {
		return
	}
	e.debug.RecordLifecycle(sessionID, kind, text, meta)
}

func (e *Engine) emitInterrupted(out chan<- domain.Event, sessionID int64) {
	e.recordLifecycle(sessionID, "interrupted", "Interrupted", nil)
	out <- domain.Event{Kind: domain.EventKindStatus, Text: "Interrupted"}
}

func interruptedErr(err error) bool {
	return errors.Is(err, context.Canceled)
}

func errorSummary(err error) string {
	return "Error: " + strings.TrimSpace(err.Error())
}

func (e *Engine) buildConversation(ctx context.Context, sessionID int64) ([]provider.Message, error) {
	session, err := e.store.GetSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	messages, partsByMessage, err := e.store.PartsForSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	conversation := []provider.Message{
		{Role: domain.MessageRoleSystem, Content: systemPrompt()},
	}
	if agentsText := strings.TrimSpace(session.AgentsResolved); agentsText != "" {
		conversation = append(conversation, provider.Message{
			Role:    domain.MessageRoleSystem,
			Content: "Resolved project AGENTS.md instructions:\n" + agentsText,
		})
	}
	for _, msg := range messages {
		if summary, ok := compactionSummary(partsByMessage[msg.ID]); ok {
			conversation = []provider.Message{
				{Role: domain.MessageRoleSystem, Content: systemPrompt()},
			}
			if agentsText := strings.TrimSpace(session.AgentsResolved); agentsText != "" {
				conversation = append(conversation, provider.Message{
					Role:    domain.MessageRoleSystem,
					Content: "Resolved project AGENTS.md instructions:\n" + agentsText,
				})
			}
			conversation = append(conversation,
				provider.Message{Role: domain.MessageRoleSystem, Content: "Compacted session summary:\n" + summary},
			)
			continue
		}
		items, err := e.conversationMessagesForStoredMessage(msg, partsByMessage[msg.ID])
		if err != nil {
			return nil, err
		}
		conversation = append(conversation, items...)
	}
	return conversation, nil
}

func (e *Engine) conversationMessagesForStoredMessage(msg domain.Message, parts []domain.Part) ([]provider.Message, error) {
	switch msg.Role {
	case domain.MessageRoleAssistant:
		if assistantMsg, ok := structuredAssistantMessage(parts); ok {
			return []provider.Message{assistantMsg}, nil
		}
	case domain.MessageRoleTool:
		if toolMsg, ok := structuredToolMessage(parts); ok {
			return []provider.Message{toolMsg}, nil
		}
	case domain.MessageRoleUser:
		msg, ok, err := e.userMessageWithAttachments(parts)
		if err != nil {
			return nil, err
		}
		if ok {
			return []provider.Message{msg}, nil
		}
	}
	content := stringifyParts(parts)
	if strings.TrimSpace(content) == "" {
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

func (e *Engine) userMessageWithAttachments(parts []domain.Part) (provider.Message, bool, error) {
	contentParts := make([]provider.ContentPart, 0, len(parts)+1)
	plainText := make([]string, 0, 1)
	var hasAttachments bool
	for _, part := range parts {
		switch part.Kind {
		case domain.PartKindText:
			if strings.TrimSpace(part.Body) != "" {
				plainText = append(plainText, part.Body)
				contentParts = append(contentParts, provider.TextPart(part.Body))
			}
		case domain.PartKindAttachment:
			hasAttachments = true
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
				contentParts = append(contentParts, provider.TextPart("Attached file "+meta.Name+":\n"+body))
			case attachment.KindImage:
				data, err := e.files.ReadBytes(meta)
				if err != nil {
					return provider.Message{}, false, err
				}
				contentParts = append(contentParts, provider.ImagePart(meta.MIME, data))
			default:
				return provider.Message{}, false, fmt.Errorf("unsupported attachment in conversation: %s", meta.MIME)
			}
		}
	}
	if !hasAttachments {
		return provider.Message{}, false, nil
	}
	message := provider.Message{Role: domain.MessageRoleUser, ContentParts: contentParts}
	if len(contentParts) == 0 && len(plainText) > 0 {
		message.Content = strings.Join(plainText, "\n\n")
	}
	return message, true, nil
}

func structuredAssistantMessage(parts []domain.Part) (provider.Message, bool) {
	var toolCalls []provider.ToolCall
	textChunks := make([]string, 0, 2)
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
		case domain.PartKindSystemNotice:
			if strings.TrimSpace(part.Body) != "" && strings.TrimSpace(part.Body) != "usage" {
				textChunks = append(textChunks, strings.TrimSpace(part.Body))
			}
		}
	}
	if len(toolCalls) == 0 {
		return provider.Message{}, false
	}
	return provider.Message{
		Role:      domain.MessageRoleAssistant,
		Content:   strings.Join(textChunks, "\n\n"),
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
		if diff := diffBody(parts); diff != "" {
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

func stringifyParts(parts []domain.Part) string {
	var chunks []string
	for _, part := range parts {
		switch part.Kind {
		case domain.PartKindCompaction:
			continue
		case domain.PartKindAttachment:
			continue
		case domain.PartKindReasoning:
			chunks = append(chunks, "Reasoning:\n"+part.Body)
		case domain.PartKindToolCall:
			if callText := toolCallContext(part); callText != "" {
				chunks = append(chunks, callText)
			}
		case domain.PartKindToolOutput:
			chunks = append(chunks, "Tool output:\n"+part.Body)
		case domain.PartKindDiff:
			chunks = append(chunks, "Diff:\n"+part.Body)
		case domain.PartKindTaskUpdate:
			chunks = append(chunks, "Task update:\n"+part.Body)
		case domain.PartKindApprovalRequest, domain.PartKindSystemNotice:
			continue
		default:
			chunks = append(chunks, part.Body)
		}
	}
	return strings.TrimSpace(strings.Join(chunks, "\n\n"))
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

func toolCallArgumentsJSON(call tools.Request) string { return call.ArgumentsJSON() }

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
	messages, parts, err := e.store.PartsForSession(ctx, session.ID)
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
	if err := e.compactSession(ctx, session, client, "auto", out); err != nil {
		return false, err
	}
	return true, nil
}

func (e *Engine) compactSession(ctx context.Context, session domain.Session, client *provider.Client, trigger string, out chan<- domain.Event) error {
	messages, err := e.buildConversation(ctx, session.ID)
	if err != nil {
		return err
	}
	if len(messages) <= 1 {
		return nil
	}
	resp, err := client.CompleteChat(ctx, provider.ChatRequest{
		Model: session.ModelID,
		Messages: append(messages, provider.Message{
			Role:    domain.MessageRoleUser,
			Content: compactPrompt(),
		}),
		Stream: false,
	})
	if err != nil {
		return err
	}
	summary, usage := resp.Text, resp.Usage
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return nil
	}
	msg, err := e.store.AddMessage(ctx, session.ID, domain.MessageRoleAssistant, "Compacted session summary")
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

func (e *Engine) handleModelToolCall(ctx context.Context, session domain.Session, req tools.Request) (domain.Event, error) {
	sessionID := session.ID
	req, err := tools.Normalize(req)
	if err != nil {
		return domain.Event{}, err
	}
	toolSpec, ok := tools.Lookup(req.Tool)
	if !ok {
		return domain.Event{}, fmt.Errorf("unsupported model tool %q", req.Tool)
	}

	decision := permission.Decision{Mode: domain.PermissionModeAllow}
	if !toolSpec.BypassesPermission() {
		decision = permission.Evaluate(e.cfg.Permissions, effectivePermissionProfile(e.cfg, session), e.permissionRequest(session, req))
	}
	if decision.Mode == domain.PermissionModeDeny {
		text := fmt.Sprintf("%s denied by policy", req.Tool)
		if strings.TrimSpace(decision.Reason) != "" {
			text = fmt.Sprintf("%s denied by policy: %s", req.Tool, decision.Reason)
		}
		msg, err := e.store.AddMessage(ctx, sessionID, domain.MessageRoleTool, string(req.Tool))
		if err != nil {
			return domain.Event{}, err
		}
		meta, _ := json.Marshal(req)
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
		approval, err := e.store.CreateApproval(ctx, sessionID, req.Tool, storedArgs)
		if err != nil {
			return domain.Event{}, err
		}
		preview := tools.Preview(req)
		if err := e.recordApprovalRequest(ctx, sessionID, req.Tool, approval.ID, preview, req.ToolCallID); err != nil {
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
	result, err := e.registry.Execute(ctx, req)
	if err != nil {
		return domain.Event{Kind: domain.EventKindError, Err: err}, nil
	}
	evt, err := e.persistToolResult(ctx, sessionID, req, result)
	if err != nil {
		return domain.Event{}, err
	}
	final := <-evt
	return final, nil
}

func effectivePermissionProfile(cfg config.Config, session domain.Session) string {
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func max(a, b int) int {
	return slices.Max([]int{a, b})
}

func (e *Engine) recordApprovalRequest(ctx context.Context, sessionID int64, tool domain.ToolKind, approvalID int64, preview, toolCallID string) error {
	msg, err := e.store.AddMessage(ctx, sessionID, domain.MessageRoleTool, fmt.Sprintf("approval:%s", tool))
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

func (e *Engine) recordApprovalReply(ctx context.Context, sessionID int64, tool domain.ToolKind, approvalID int64, status, preview, toolCallID string) error {
	msg, err := e.store.AddMessage(ctx, sessionID, domain.MessageRoleTool, fmt.Sprintf("approval:%s:%s", tool, status))
	if err != nil {
		return err
	}
	payload := map[string]string{
		"approval_id": strconv.FormatInt(approvalID, 10),
		"tool":        string(tool),
		"status":      status,
		"command":     preview,
	}
	if strings.TrimSpace(toolCallID) != "" {
		payload["tool_call_id"] = toolCallID
	}
	meta, _ := json.Marshal(payload)
	body := fmt.Sprintf("Approval %d %s for %s: %s", approvalID, status, tool, preview)
	if status == "denied" {
		_, err = e.store.AddPart(ctx, msg.ID, domain.PartKindToolOutput, body, string(meta))
		return err
	}
	_, err = e.store.AddPart(ctx, msg.ID, domain.PartKindSystemNotice, body, string(meta))
	return err
}

func (e *Engine) recordTaskUpdate(ctx context.Context, sessionID int64, body string, status domain.TaskStatus) error {
	msg, err := e.store.AddMessage(ctx, sessionID, domain.MessageRoleTool, fmt.Sprintf("task:%s", status))
	if err != nil {
		return err
	}
	meta, _ := json.Marshal(map[string]string{
		"status": string(status),
	})
	_, err = e.store.AddPart(ctx, msg.ID, domain.PartKindTaskUpdate, body, string(meta))
	return err
}

func approvalPreviewFromStored(tool domain.ToolKind, raw string) string {
	req, err := requestFromStoredApproval(tool, raw)
	if err != nil {
		return raw
	}
	return tools.Preview(req)
}
