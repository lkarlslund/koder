package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/permission"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
)

type Engine struct {
	cfg      config.Config
	store    *store.Store
	registry *tools.Registry
}

type toolCall struct {
	Tool    domain.ToolKind `json:"tool"`
	Path    string          `json:"path,omitempty"`
	Pattern string          `json:"pattern,omitempty"`
	Command string          `json:"command,omitempty"`
	Body    string          `json:"body,omitempty"`
	URL     string          `json:"url,omitempty"`
	Content string          `json:"content,omitempty"`
}

func New(cfg config.Config, st *store.Store, registry *tools.Registry) *Engine {
	return &Engine{cfg: cfg, store: st, registry: registry}
}

func (e *Engine) RunPrompt(ctx context.Context, session domain.Session, prompt string) (<-chan domain.Event, error) {
	if strings.HasPrefix(strings.TrimSpace(prompt), "/") {
		return e.runSlashCommand(ctx, session, prompt)
	}
	return e.runModelPrompt(ctx, session, prompt)
}

func (e *Engine) runModelPrompt(ctx context.Context, session domain.Session, prompt string) (<-chan domain.Event, error) {
	providerCfg, ok := e.cfg.Provider(session.ProviderID)
	if !ok {
		return nil, fmt.Errorf("provider %q not found", session.ProviderID)
	}
	client, err := provider.New(session.ProviderID, providerCfg)
	if err != nil {
		return nil, err
	}

	userMsg, err := e.store.AddMessage(ctx, session.ID, domain.MessageRoleUser, prompt)
	if err != nil {
		return nil, err
	}
	if _, err := e.store.AddPart(ctx, userMsg.ID, domain.PartKindText, prompt, ""); err != nil {
		return nil, err
	}

	out := make(chan domain.Event)
	go func() {
		defer close(out)
		if err := e.continueModelTurn(ctx, session, client, out); err != nil {
			out <- domain.Event{Kind: domain.EventKindError, Err: err}
			return
		}
	}()
	return out, nil
}

func (e *Engine) continueModelTurn(ctx context.Context, session domain.Session, client *provider.Client, out chan<- domain.Event) error {
	for steps := 0; steps < 6; steps++ {
		messages, buildErr := e.buildConversation(ctx, session.ID)
		if buildErr != nil {
			return buildErr
		}

		text, reasoning, usage, completeErr := client.CompleteChat(ctx, provider.ChatRequest{
			Model:    session.ModelID,
			Messages: messages,
			Stream:   false,
		})
		if completeErr != nil {
			return completeErr
		}

		call, plain := parseToolCall(text)
		if call != nil {
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
		if titleErr := e.maybeUpdateSessionTitle(ctx, session, client); titleErr != nil {
			return titleErr
		}
		out <- domain.Event{Kind: domain.EventKindMessageDone}
		return nil
	}
	return fmt.Errorf("tool loop exceeded max steps")
}

func (e *Engine) maybeUpdateSessionTitle(ctx context.Context, session domain.Session, client *provider.Client) error {
	count, err := e.store.CountMessagesByRole(ctx, session.ID, domain.MessageRoleUser)
	if err != nil {
		return err
	}
	if !shouldRefreshSessionTitle(count) {
		return nil
	}
	messages, err := e.titleSummaryMessages(ctx, session.ID)
	if err != nil {
		return err
	}
	title, _, _, err := client.CompleteChat(ctx, provider.ChatRequest{
		Model:    session.ModelID,
		Messages: messages,
		Stream:   false,
	})
	if err != nil {
		return err
	}
	title = normalizeSessionTitle(title)
	if title == "" {
		return nil
	}
	return e.store.UpdateSessionTitle(ctx, session.ID, title)
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

func (e *Engine) runSlashCommand(ctx context.Context, session domain.Session, prompt string) (<-chan domain.Event, error) {
	fields := strings.Fields(prompt)
	if len(fields) == 0 {
		return nil, fmt.Errorf("empty command")
	}
	name := strings.TrimPrefix(fields[0], "/")
	args := strings.TrimSpace(strings.TrimPrefix(prompt, fields[0]))

	var req tools.Request
	switch name {
	case "read":
		req = tools.Request{Tool: domain.ToolKindRead, Args: map[string]string{"path": args}}
	case "glob":
		req = tools.Request{Tool: domain.ToolKindGlob, Args: map[string]string{"pattern": args}}
	case "grep":
		req = tools.Request{Tool: domain.ToolKindGrep, Args: map[string]string{"pattern": args}}
	case "bash":
		req = tools.Request{Tool: domain.ToolKindBash, Args: map[string]string{"command": args}}
	case "task":
		req = tools.Request{Tool: domain.ToolKindTask, Args: map[string]string{"body": args}}
	case "fetch":
		req = tools.Request{Tool: domain.ToolKindWebFetch, Args: map[string]string{"url": args}}
	case "patch":
		parts := strings.SplitN(args, " ", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("usage: /patch <path> <content>")
		}
		req = tools.Request{Tool: domain.ToolKindApplyPatch, Args: map[string]string{"path": parts[0], "content": parts[1]}}
	case "approve":
		return e.approve(ctx, session.ID, args)
	case "deny":
		return e.deny(ctx, session.ID, args)
	case "perm":
		return e.setPermissionProfile(ctx, session.ID, args)
	default:
		return nil, fmt.Errorf("unknown slash command %q", name)
	}

	mode := permission.Evaluate(e.cfg.Permissions, effectivePermissionProfile(e.cfg, session), permissionRequest(req))
	if mode == domain.PermissionModeDeny {
		return nil, fmt.Errorf("%s is denied by policy", req.Tool)
	}

	if req.Tool == domain.ToolKindTask {
		task, err := e.store.AddTask(ctx, session.ID, req.Args["body"], domain.TaskStatusPending)
		if err != nil {
			return nil, err
		}
		return emitOnce(domain.Event{Kind: domain.EventKindTaskUpdate, Text: task.Body, Meta: map[string]string{"id": strconv.FormatInt(task.ID, 10), "status": string(task.Status)}}), nil
	}

	if mode == domain.PermissionModeAsk {
		storedArgs, err := serializeRequest(req)
		if err != nil {
			return nil, err
		}
		approval, err := e.store.CreateApproval(ctx, session.ID, req.Tool, storedArgs)
		if err != nil {
			return nil, err
		}
		meta := map[string]string{
			"approval_id": strconv.FormatInt(approval.ID, 10),
			"tool":        string(req.Tool),
			"command":     approvalPreview(req),
		}
		if err := e.recordApprovalRequest(ctx, session.ID, req.Tool, approval.ID, approvalPreview(req)); err != nil {
			return nil, err
		}
		return emitOnce(domain.Event{Kind: domain.EventKindApprovalAsk, Text: fmt.Sprintf("%s requires approval", req.Tool), Tool: req.Tool, Meta: meta}), nil
	}

	result, err := e.registry.Execute(ctx, req)
	if err != nil {
		return emitOnce(domain.Event{Kind: domain.EventKindError, Err: err}), nil
	}
	return e.persistToolResult(ctx, session.ID, req.Tool, result)
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
	result, execErr := e.registry.Execute(ctx, req)
	if execErr != nil {
		return emitOnce(domain.Event{Kind: domain.EventKindError, Err: execErr}), nil
	}
	toolEvents, err := e.persistToolResult(ctx, sessionID, item.Tool, result)
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
	client, err := provider.New(session.ProviderID, providerCfg)
	if err != nil {
		return nil, err
	}
	out := make(chan domain.Event)
	go func() {
		defer close(out)
		for evt := range toolEvents {
			out <- evt
		}
		if err := e.continueModelTurn(ctx, session, client, out); err != nil {
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

func (e *Engine) persistToolResult(ctx context.Context, sessionID int64, tool domain.ToolKind, result tools.Result) (<-chan domain.Event, error) {
	msg, err := e.store.AddMessage(ctx, sessionID, domain.MessageRoleTool, string(tool))
	if err != nil {
		return nil, err
	}
	meta, _ := json.Marshal(map[string]string{"tool": string(tool)})
	if _, err := e.store.AddPart(ctx, msg.ID, domain.PartKindToolOutput, result.Output, string(meta)); err != nil {
		return nil, err
	}
	if result.DiffText != "" {
		if _, err := e.store.AddPart(ctx, msg.ID, domain.PartKindDiff, result.DiffText, ""); err != nil {
			return nil, err
		}
	}
	return emitOnce(domain.Event{Kind: domain.EventKindToolResult, Text: result.Output, Tool: tool}), nil
}

func emitOnce(evt domain.Event) <-chan domain.Event {
	out := make(chan domain.Event, 1)
	out <- evt
	close(out)
	return out
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
		content := stringifyParts(partsByMessage[msg.ID])
		if strings.TrimSpace(content) == "" {
			content = msg.Summary
		}
		if strings.TrimSpace(content) == "" {
			continue
		}
		conversation = append(conversation, provider.Message{
			Role:    msg.Role,
			Content: content,
		})
	}
	return conversation, nil
}

func stringifyParts(parts []domain.Part) string {
	var chunks []string
	for _, part := range parts {
		switch part.Kind {
		case domain.PartKindReasoning:
			chunks = append(chunks, "Reasoning:\n"+part.Body)
		case domain.PartKindToolCall:
			chunks = append(chunks, "Tool call:\n"+part.Body)
		case domain.PartKindToolOutput:
			chunks = append(chunks, "Tool output:\n"+part.Body)
		case domain.PartKindDiff:
			chunks = append(chunks, "Diff:\n"+part.Body)
		case domain.PartKindSystemNotice:
			if part.MetaJSON != "" {
				chunks = append(chunks, part.Body+"\n"+part.MetaJSON)
			} else {
				chunks = append(chunks, part.Body)
			}
		default:
			chunks = append(chunks, part.Body)
		}
	}
	return strings.TrimSpace(strings.Join(chunks, "\n\n"))
}

func systemPrompt() string {
	return strings.TrimSpace(`
You are koder, a terminal coding agent.

You can inspect files and run commands through koder tools. When you need a tool, respond with exactly one XML block in this format and no markdown fence:
<koder_tool>
{"tool":"read","path":"README.md"}
</koder_tool>

Supported tools and arguments:
- read: {"tool":"read","path":"relative/path"}
- glob: {"tool":"glob","pattern":"*.go"}
- grep: {"tool":"grep","pattern":"search text"}
- bash: {"tool":"bash","command":"pwd"}
- apply_patch: {"tool":"apply_patch","path":"file.txt","content":"new file content"}
- task: {"tool":"task","body":"short task text"}
- webfetch: {"tool":"webfetch","url":"https://example.com"}

Rules:
- Use tools instead of claiming you cannot run commands.
- Use one tool call at a time.
- After receiving tool output, continue the task.
- If no tool is needed, answer normally.
- Paths are relative to the current workspace.
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

func (e *Engine) handleModelToolCall(ctx context.Context, session domain.Session, call toolCall) (domain.Event, error) {
	sessionID := session.ID
	req := tools.Request{Tool: call.Tool, Args: map[string]string{}}
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
				"approval_id": strconv.FormatInt(approval.ID, 10),
				"tool":        string(req.Tool),
				"command":     preview,
			},
		}, nil
	}
	result, err := e.registry.Execute(ctx, req)
	if err != nil {
		return domain.Event{Kind: domain.EventKindError, Err: err}, nil
	}
	evt, err := e.persistToolResult(ctx, sessionID, req.Tool, result)
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

func approvalPreviewFromStored(tool domain.ToolKind, raw string) string {
	req, err := requestFromStoredApproval(tool, raw)
	if err != nil {
		return raw
	}
	return approvalPreview(req)
}
