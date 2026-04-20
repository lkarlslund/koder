package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/lkarlslund/koder/internal/attachment"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/debugsrv"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/permission"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/sessionctx"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
)

type Engine struct {
	cfg      config.Config
	store    *store.Store
	registry *tools.Registry
	debug    *debugsrv.Recorder
	files    *attachment.Manager
}

type toolCall struct {
	ID      string          `json:"tool_call_id,omitempty"`
	Tool    domain.ToolKind `json:"tool"`
	Path    string          `json:"path,omitempty"`
	Pattern string          `json:"pattern,omitempty"`
	Command string          `json:"command,omitempty"`
	Body    string          `json:"body,omitempty"`
	URL     string          `json:"url,omitempty"`
	Content string          `json:"content,omitempty"`
	Reason  string          `json:"reason,omitempty"`
}

func New(cfg config.Config, st *store.Store, registry *tools.Registry, debug *debugsrv.Recorder) *Engine {
	return &Engine{
		cfg:      cfg,
		store:    st,
		registry: registry,
		debug:    debug,
		files:    attachment.NewManager(cfg.StateDir()),
	}
}

func (e *Engine) UpdateConfig(cfg config.Config) {
	e.cfg = cfg
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
			Tools:      modelToolDefinitions(),
			ToolChoice: "auto",
			Stream:     false,
		})
		if completeErr != nil {
			return completeErr
		}

		if len(resp.ToolCalls) > 0 {
			call, err := toolCallFromProvider(resp.ToolCalls[0])
			if err != nil {
				return err
			}
			e.recordLifecycle(session.ID, "tool_call_parsed", call.contextString(), map[string]string{"tool": string(call.Tool), "tool_call_id": call.ID})
			assistantMsg, err := e.store.AddMessage(ctx, session.ID, domain.MessageRoleAssistant, fmt.Sprintf("tool:%s", call.Tool))
			if err != nil {
				return err
			}
			meta, _ := json.Marshal(call)
			body := call.contextString()
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
			e.recordLifecycle(session.ID, "tool_call_parsed", call.contextString(), map[string]string{"tool": string(call.Tool)})
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
		return nil, fmt.Errorf("usage: /perm <%s>", strings.Join(permission.ProfileNames(e.cfg.Permissions), "|"))
	}
	if _, ok := e.cfg.Permissions.Profiles[profile]; !ok {
		return nil, fmt.Errorf("unknown permission profile %q", profile)
	}
	if err := e.store.SetSessionPermissionProfile(ctx, sessionID, profile); err != nil {
		return nil, err
	}
	return emitOnce(domain.Event{
		Kind: domain.EventKindStatus,
		Text: fmt.Sprintf("permission profile set to %s", profile),
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
	if err := e.recordApprovalReply(ctx, sessionID, item.Tool, id, "approved", approvalPreview(req)); err != nil {
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
	toolEvents, err := e.persistToolResult(ctx, sessionID, item.Tool, req.Args["tool_call_id"], result)
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
	if err := e.recordApprovalReply(ctx, item.SessionID, item.Tool, id, "denied", approvalPreviewFromStored(item.Tool, item.Command)); err != nil {
		return nil, err
	}
	return emitOnce(domain.Event{Kind: domain.EventKindApprovalReply, Text: fmt.Sprintf("approval %d denied", id)}), nil
}

func (e *Engine) persistToolResult(ctx context.Context, sessionID int64, tool domain.ToolKind, toolCallID string, result tools.Result) (<-chan domain.Event, error) {
	summary, body := toolResultSummary(tool, result)
	msg, err := e.store.AddMessage(ctx, sessionID, domain.MessageRoleTool, summary)
	if err != nil {
		return nil, err
	}
	metaPayload := map[string]string{"tool": string(tool)}
	if strings.TrimSpace(toolCallID) != "" {
		metaPayload["tool_call_id"] = toolCallID
	}
	meta, _ := json.Marshal(metaPayload)
	if _, err := e.store.AddPart(ctx, msg.ID, domain.PartKindToolOutput, body, string(meta)); err != nil {
		return nil, err
	}
	if result.DiffText != "" {
		if _, err := e.store.AddPart(ctx, msg.ID, domain.PartKindDiff, result.DiffText, ""); err != nil {
			return nil, err
		}
	}
	e.recordLifecycle(sessionID, "tool_result_persisted", summary, map[string]string{"tool": string(tool), "message_id": strconv.FormatInt(msg.ID, 10)})
	return emitOnce(domain.Event{Kind: domain.EventKindToolResult, Text: body, Tool: tool}), nil
}

func toolResultSummary(tool domain.ToolKind, result tools.Result) (string, string) {
	output := strings.TrimSpace(result.Output)
	switch {
	case output != "":
		return string(tool), result.Output
	case strings.TrimSpace(result.DiffText) != "":
		body := fmt.Sprintf("%s completed and produced a diff", tool)
		return body, body
	default:
		body := fmt.Sprintf("%s completed with no output", tool)
		return body, body
	}
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
	messages, partsByMessage, err := e.store.PartsForSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	conversation := []provider.Message{
		{Role: domain.MessageRoleSystem, Content: systemPrompt()},
	}
	for _, msg := range messages {
		if summary, ok := compactionSummary(partsByMessage[msg.ID]); ok {
			conversation = []provider.Message{
				{Role: domain.MessageRoleSystem, Content: systemPrompt()},
				{Role: domain.MessageRoleSystem, Content: "Compacted session summary:\n" + summary},
			}
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
			if !ok || strings.TrimSpace(call.ID) == "" {
				return provider.Message{}, false
			}
			toolCalls = append(toolCalls, provider.ToolCall{
				ID:   call.ID,
				Type: "function",
				Function: provider.FunctionCall{
					Name:      string(call.Tool),
					Arguments: toolCallArgumentsJSON(call),
				},
			})
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
			if provider.SupportsAttachment(session.ProviderID, session.ModelID, kind) {
				continue
			}
			return fmt.Errorf("provider %s model %s does not support %s attachments", session.ProviderID, session.ModelID, kind)
		default:
			return fmt.Errorf("unsupported attachment type %q", draft.MIME)
		}
	}
	return nil
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
		return "Tool call:\n" + call.contextString()
	}
	if call, _ := parseToolCall(part.Body); call != nil {
		return "Tool call:\n" + call.contextString()
	}
	body := strings.TrimSpace(part.Body)
	if body == "" {
		return ""
	}
	return "Tool call:\n" + body
}

func storedToolCall(part domain.Part) (toolCall, bool) {
	if strings.TrimSpace(part.MetaJSON) == "" {
		return toolCall{}, false
	}
	var call toolCall
	if err := json.Unmarshal([]byte(part.MetaJSON), &call); err != nil || call.Tool == "" {
		return toolCall{}, false
	}
	return call, true
}

func toolCallArgumentsJSON(call toolCall) string {
	args := map[string]string{}
	switch call.Tool {
	case domain.ToolKindRead:
		args["path"] = call.Path
	case domain.ToolKindGlob, domain.ToolKindGrep:
		args["pattern"] = call.Pattern
	case domain.ToolKindBash:
		args["command"] = call.Command
	case domain.ToolKindApplyPatch:
		args["path"] = call.Path
		args["content"] = call.Content
	case domain.ToolKindTask:
		args["body"] = call.Body
	case domain.ToolKindQuestion:
		args["question"] = call.Body
	case domain.ToolKindWebFetch:
		args["url"] = call.URL
	case domain.ToolKindWebSearch:
		args["query"] = call.Body
	}
	data, err := json.Marshal(args)
	if err != nil {
		return "{}"
	}
	return string(data)
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

func (c toolCall) contextString() string {
	payload := map[string]string{"tool": string(c.Tool)}
	if c.ID != "" {
		payload["tool_call_id"] = c.ID
	}
	switch c.Tool {
	case domain.ToolKindRead:
		payload["path"] = c.Path
	case domain.ToolKindGlob, domain.ToolKindGrep:
		payload["pattern"] = c.Pattern
	case domain.ToolKindBash:
		payload["command"] = c.Command
	case domain.ToolKindApplyPatch:
		payload["path"] = c.Path
		payload["content"] = c.Content
	case domain.ToolKindTask:
		payload["body"] = c.Body
	case domain.ToolKindWebFetch:
		payload["url"] = c.URL
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Sprintf(`{"tool":"%s"}`, c.Tool)
	}
	return string(data)
}

func toolCallFromProvider(call provider.ToolCall) (toolCall, error) {
	parsed := toolCall{
		ID:   strings.TrimSpace(call.ID),
		Tool: domain.ToolKind(strings.TrimSpace(call.Function.Name)),
	}
	if parsed.Tool == "" {
		return toolCall{}, fmt.Errorf("provider tool call missing function name")
	}
	var args map[string]string
	if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
		return toolCall{}, fmt.Errorf("decode tool arguments for %s: %w", parsed.Tool, err)
	}
	switch parsed.Tool {
	case domain.ToolKindRead:
		parsed.Path = args["path"]
	case domain.ToolKindGlob, domain.ToolKindGrep:
		parsed.Pattern = args["pattern"]
	case domain.ToolKindBash:
		parsed.Command = args["command"]
	case domain.ToolKindApplyPatch:
		parsed.Path = args["path"]
		parsed.Content = args["content"]
	case domain.ToolKindTask:
		parsed.Body = args["body"]
	case domain.ToolKindQuestion:
		parsed.Body = args["question"]
	case domain.ToolKindWebFetch:
		parsed.URL = args["url"]
	case domain.ToolKindWebSearch:
		parsed.Body = args["query"]
	default:
		return toolCall{}, fmt.Errorf("unsupported provider tool %q", parsed.Tool)
	}
	if parsed.ID == "" {
		parsed.ID = "call_" + strings.ToLower(string(parsed.Tool))
	}
	return parsed, nil
}

func modelToolDefinitions() []provider.ToolDefinition {
	return []provider.ToolDefinition{
		modelToolDefinition("read", "Read a file from the workspace", `{"type":"object","properties":{"path":{"type":"string","description":"Relative file path to read"}},"required":["path"],"additionalProperties":false}`),
		modelToolDefinition("glob", "Find workspace paths matching a glob pattern", `{"type":"object","properties":{"pattern":{"type":"string","description":"Glob pattern relative to the workspace"}},"required":["pattern"],"additionalProperties":false}`),
		modelToolDefinition("grep", "Search for text within workspace files", `{"type":"object","properties":{"pattern":{"type":"string","description":"Text or regex to search for"}},"required":["pattern"],"additionalProperties":false}`),
		modelToolDefinition("bash", "Run a shell command in the workspace", `{"type":"object","properties":{"command":{"type":"string","description":"Shell command to execute"}},"required":["command"],"additionalProperties":false}`),
		modelToolDefinition("apply_patch", "Write file content directly to a workspace path", `{"type":"object","properties":{"path":{"type":"string","description":"Relative file path to overwrite"},"content":{"type":"string","description":"Full new file contents"}},"required":["path","content"],"additionalProperties":false}`),
		modelToolDefinition("task", "Record a short task for later follow-up", `{"type":"object","properties":{"body":{"type":"string","description":"Short task description"}},"required":["body"],"additionalProperties":false}`),
		modelToolDefinition("webfetch", "Fetch the contents of a URL", `{"type":"object","properties":{"url":{"type":"string","description":"Fully qualified URL"}},"required":["url"],"additionalProperties":false}`),
	}
}

func modelToolDefinition(name, description, schema string) provider.ToolDefinition {
	return provider.ToolDefinition{
		Type: "function",
		Function: provider.FunctionDefinition{
			Name:        name,
			Description: description,
			Parameters:  json.RawMessage(schema),
		},
	}
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

func parseToolCall(text string) (*toolCall, string) {
	re := regexp.MustCompile(`(?s)<koder_tool>\s*(\{.*?\})\s*</koder_tool>`)
	match := re.FindStringSubmatch(text)
	if len(match) != 2 {
		return nil, text
	}
	var call toolCall
	if err := json.Unmarshal([]byte(match[1]), &call); err != nil || call.Tool == "" {
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

func (e *Engine) handleModelToolCall(ctx context.Context, session domain.Session, call toolCall) (domain.Event, error) {
	sessionID := session.ID
	req := tools.Request{Tool: call.Tool, Args: map[string]string{}}
	if call.ID != "" {
		req.Args["tool_call_id"] = call.ID
	}
	commandText := ""
	switch call.Tool {
	case domain.ToolKindRead:
		req.Args["path"] = call.Path
		commandText = call.Path
	case domain.ToolKindGlob:
		req.Args["pattern"] = call.Pattern
		commandText = call.Pattern
	case domain.ToolKindGrep:
		req.Args["pattern"] = call.Pattern
		commandText = call.Pattern
	case domain.ToolKindBash:
		req.Args["command"] = call.Command
		commandText = call.Command
	case domain.ToolKindApplyPatch:
		req.Args["path"] = call.Path
		req.Args["content"] = call.Content
		commandText = call.Path
	case domain.ToolKindTask:
		req.Args["body"] = call.Body
		commandText = call.Body
	case domain.ToolKindWebFetch:
		req.Args["url"] = call.URL
		commandText = call.URL
	default:
		return domain.Event{}, fmt.Errorf("unsupported model tool %q", call.Tool)
	}

	mode := permission.Evaluate(e.cfg.Permissions, effectivePermissionProfile(e.cfg, session), permissionRequest(req))
	if mode == domain.PermissionModeDeny {
		msg, err := e.store.AddMessage(ctx, sessionID, domain.MessageRoleTool, string(req.Tool))
		if err != nil {
			return domain.Event{}, err
		}
		if _, err := e.store.AddPart(ctx, msg.ID, domain.PartKindToolOutput, fmt.Sprintf("%s denied by policy", req.Tool), ""); err != nil {
			return domain.Event{}, err
		}
		return domain.Event{Kind: domain.EventKindToolResult, Tool: req.Tool, Text: fmt.Sprintf("%s denied by policy", req.Tool)}, nil
	}
	if req.Tool == domain.ToolKindTask {
		task, err := e.store.AddTask(ctx, sessionID, req.Args["body"], domain.TaskStatusPending)
		if err != nil {
			return domain.Event{}, err
		}
		if err := e.recordTaskUpdate(ctx, sessionID, task.Body, task.Status); err != nil {
			return domain.Event{}, err
		}
		return domain.Event{Kind: domain.EventKindTaskUpdate, Text: task.Body, Tool: req.Tool}, nil
	}
	if mode == domain.PermissionModeAsk {
		storedArgs, err := serializeRequest(req)
		if err != nil {
			return domain.Event{}, err
		}
		approval, err := e.store.CreateApproval(ctx, sessionID, req.Tool, storedArgs)
		if err != nil {
			return domain.Event{}, err
		}
		preview := firstNonEmpty(commandText, approvalPreview(req))
		if err := e.recordApprovalRequest(ctx, sessionID, req.Tool, approval.ID, preview); err != nil {
			return domain.Event{}, err
		}
		return domain.Event{
			Kind: domain.EventKindApprovalAsk,
			Text: fmt.Sprintf("%s requires approval", req.Tool),
			Tool: req.Tool,
			Meta: map[string]string{
				"approval_id":  strconv.FormatInt(approval.ID, 10),
				"tool":         string(req.Tool),
				"command":      preview,
				"tool_call_id": req.Args["tool_call_id"],
			},
		}, nil
	}
	result, err := e.registry.Execute(ctx, req)
	if err != nil {
		return domain.Event{Kind: domain.EventKindError, Err: err}, nil
	}
	evt, err := e.persistToolResult(ctx, sessionID, req.Tool, req.Args["tool_call_id"], result)
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

func permissionRequest(req tools.Request) permission.Request {
	return permission.Request{
		Tool:    req.Tool,
		Pattern: approvalPreview(req),
	}
}

func serializeRequest(req tools.Request) (string, error) {
	data, err := json.Marshal(req.Args)
	if err != nil {
		return "", fmt.Errorf("serialize request: %w", err)
	}
	return string(data), nil
}

func requestFromStoredApproval(tool domain.ToolKind, raw string) (tools.Request, error) {
	var args map[string]string
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		args = legacyArgs(tool, raw)
	}
	return tools.Request{Tool: tool, Args: args}, nil
}

func legacyArgs(tool domain.ToolKind, raw string) map[string]string {
	switch tool {
	case domain.ToolKindRead:
		return map[string]string{"path": raw}
	case domain.ToolKindGlob, domain.ToolKindGrep:
		return map[string]string{"pattern": raw}
	case domain.ToolKindBash:
		return map[string]string{"command": raw}
	case domain.ToolKindWebFetch:
		return map[string]string{"url": raw}
	case domain.ToolKindTask:
		return map[string]string{"body": raw}
	default:
		return map[string]string{"command": raw}
	}
}

func approvalPreview(req tools.Request) string {
	switch req.Tool {
	case domain.ToolKindRead:
		return req.Args["path"]
	case domain.ToolKindGlob, domain.ToolKindGrep:
		return req.Args["pattern"]
	case domain.ToolKindBash:
		return req.Args["command"]
	case domain.ToolKindApplyPatch:
		return req.Args["path"]
	case domain.ToolKindTask:
		return req.Args["body"]
	case domain.ToolKindWebFetch:
		return req.Args["url"]
	default:
		return string(req.Tool)
	}
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

func (e *Engine) recordApprovalRequest(ctx context.Context, sessionID int64, tool domain.ToolKind, approvalID int64, preview string) error {
	msg, err := e.store.AddMessage(ctx, sessionID, domain.MessageRoleTool, fmt.Sprintf("approval:%s", tool))
	if err != nil {
		return err
	}
	meta, _ := json.Marshal(map[string]string{
		"approval_id": strconv.FormatInt(approvalID, 10),
		"tool":        string(tool),
		"status":      "pending",
		"command":     preview,
	})
	body := fmt.Sprintf("Approval required for %s: %s", tool, preview)
	_, err = e.store.AddPart(ctx, msg.ID, domain.PartKindApprovalRequest, body, string(meta))
	return err
}

func (e *Engine) recordApprovalReply(ctx context.Context, sessionID int64, tool domain.ToolKind, approvalID int64, status, preview string) error {
	msg, err := e.store.AddMessage(ctx, sessionID, domain.MessageRoleTool, fmt.Sprintf("approval:%s:%s", tool, status))
	if err != nil {
		return err
	}
	meta, _ := json.Marshal(map[string]string{
		"approval_id": strconv.FormatInt(approvalID, 10),
		"tool":        string(tool),
		"status":      status,
		"command":     preview,
	})
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
	return approvalPreview(req)
}
