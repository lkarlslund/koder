package codexdriver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/lkarlslund/koder/internal/accesssettings"
	"github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/codexapp"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/store"
)

type Instructions func(domain.Session, domain.Chat) string

type DynamicTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type ToolBridge interface {
	Definitions(context.Context, *chat.Chat) ([]DynamicTool, error)
	Call(context.Context, *chat.Chat, string, string, json.RawMessage) (domain.ToolResult, error)
}

type pendingApproval struct {
	requestID json.RawMessage
	method    string
}

type Manager struct {
	client       *codexapp.Client
	bindings     bindingStore
	instructions Instructions
	tools        ToolBridge
	threadMu     sync.Mutex
	approvalMu   sync.Mutex
	approvals    map[string]pendingApproval
}

func (m *Manager) SetToolBridge(bridge ToolBridge) {
	if m != nil {
		m.tools = bridge
	}
}

func New(client *codexapp.Client, st *store.Store, instructions Instructions) *Manager {
	return &Manager{client: client, bindings: newBindingStore(st), instructions: instructions, approvals: map[string]pendingApproval{}}
}

func (m *Manager) Models(ctx context.Context) ([]codexapp.Model, error) {
	if m == nil || m.client == nil {
		return nil, fmt.Errorf("codex backend is not configured")
	}
	return m.client.Models(ctx)
}

func (m *Manager) Close() error {
	if m == nil || m.client == nil {
		return nil
	}
	return m.client.Close()
}

// UpdateChat mirrors user-facing chat metadata into an existing Codex thread.
// Chats that have never run have no thread yet and require no remote action.
func (m *Manager) UpdateChat(ctx context.Context, before domain.Chat, title string, archived *bool) error {
	if m == nil || m.client == nil || before.EffectiveBackend() != domain.ChatBackendCodex {
		return nil
	}
	binding, ok, err := m.bindings.find(ctx, before.ID)
	if err != nil || !ok || binding.ThreadID == "" {
		return err
	}
	if err := m.client.Start(ctx); err != nil {
		return err
	}
	if nextTitle := strings.TrimSpace(title); nextTitle != "" && nextTitle != strings.TrimSpace(before.Title) {
		if err := m.client.Call(ctx, "thread/name/set", map[string]string{"threadId": binding.ThreadID, "name": nextTitle}, nil); err != nil {
			return fmt.Errorf("rename Codex thread: %w", err)
		}
	}
	if archived != nil && *archived != before.Archived {
		method := "thread/archive"
		if !*archived {
			method = "thread/unarchive"
		}
		if err := m.client.Call(ctx, method, map[string]string{"threadId": binding.ThreadID}, nil); err != nil {
			return fmt.Errorf("update Codex thread archive state: %w", err)
		}
	}
	return nil
}

func (m *Manager) DeleteChat(ctx context.Context, chatRecord domain.Chat) error {
	if m == nil || m.client == nil || chatRecord.EffectiveBackend() != domain.ChatBackendCodex {
		return nil
	}
	binding, ok, err := m.bindings.find(ctx, chatRecord.ID)
	if err != nil || !ok || binding.ThreadID == "" {
		return err
	}
	if err := m.client.Start(ctx); err != nil {
		return err
	}
	if err := m.client.Call(ctx, "thread/delete", map[string]string{"threadId": binding.ThreadID}, nil); err != nil {
		return fmt.Errorf("delete Codex thread: %w", err)
	}
	return m.bindings.delete(ctx, chatRecord.ID)
}

func (m *Manager) RunTurn(ctx context.Context, rt *chat.Chat, turn chat.DriverTurn, out chan<- domain.Event) error {
	if m == nil || m.client == nil {
		return fmt.Errorf("codex backend is not configured")
	}
	if rt == nil {
		return fmt.Errorf("chat runtime is required")
	}
	if err := m.client.Start(ctx); err != nil {
		return err
	}
	snapshot := rt.Snapshot()
	if snapshot.Chat.EffectiveBackend() != domain.ChatBackendCodex {
		return fmt.Errorf("codex driver cannot run backend %q", snapshot.Chat.EffectiveBackend())
	}

	text := strings.TrimSpace(turn.Input.Text)
	if domain.DeliveryForQueuedInput(turn.Input) == domain.QueuedInputDeliveryContinue && text == "" {
		text = strings.TrimSpace(turn.Note)
		if text == "" {
			text = "Continue the current task."
		}
	}
	userItem, err := rt.AppendUserMessageForInput(ctx, turn.Input, domain.UserMessage{Text: text})
	if err != nil {
		return err
	}
	out <- domain.Event{Kind: domain.EventKindMessageDone, Item: userItem}

	events, unsubscribe := m.client.Subscribe(1024)
	defer unsubscribe()
	threadID, err := m.ensureThread(ctx, rt, snapshot.Session, snapshot.Chat)
	if err != nil {
		return err
	}
	inputText := text
	for _, block := range turn.EphemeralInstructions {
		if instruction := strings.TrimSpace(block.Text); instruction != "" {
			inputText = instruction + "\n\n" + inputText
		}
	}
	var started struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	params := map[string]any{
		"threadId": threadID,
		"input":    []map[string]any{{"type": "text", "text": inputText}},
	}
	if model := strings.TrimSpace(snapshot.Chat.ModelID); model != "" {
		params["model"] = model
	}
	if err := m.client.Call(ctx, "turn/start", params, &started); err != nil {
		return fmt.Errorf("start Codex turn with model %q: %w", strings.TrimSpace(snapshot.Chat.ModelID), err)
	}
	slog.Debug("codex turn started", "chat_id", snapshot.Chat.ID, "thread_id", threadID, "turn_id", started.Turn.ID, "model", strings.TrimSpace(snapshot.Chat.ModelID))
	if started.Turn.ID == "" {
		return fmt.Errorf("codex turn/start returned no turn id")
	}
	out <- domain.Event{Kind: domain.EventKindStatus, Text: "Codex is working"}
	return m.consumeTurn(ctx, rt, threadID, started.Turn.ID, events, out)
}

func (m *Manager) ensureThread(ctx context.Context, rt *chat.Chat, session domain.Session, chatRecord domain.Chat) (string, error) {
	m.threadMu.Lock()
	defer m.threadMu.Unlock()
	var dynamicTools []DynamicTool
	if m.tools != nil {
		var err error
		dynamicTools, err = m.tools.Definitions(ctx, rt)
		if err != nil {
			return "", fmt.Errorf("build Koder tools for Codex: %w", err)
		}
	}
	if binding, ok, err := m.bindings.find(ctx, chatRecord.ID); err != nil {
		return "", err
	} else if ok && binding.ThreadID != "" {
		var resumed struct {
			Thread struct {
				ID string `json:"id"`
			} `json:"thread"`
		}
		resumeParams := map[string]any{"threadId": binding.ThreadID}
		if len(dynamicTools) > 0 {
			resumeParams["dynamicTools"] = dynamicTools
		}
		if err := m.client.Call(ctx, "thread/resume", resumeParams, &resumed); err == nil {
			slog.Debug("codex thread resumed", "chat_id", chatRecord.ID, "thread_id", binding.ThreadID, "binding_model", binding.Model, "requested_model", chatRecord.ModelID)
			return binding.ThreadID, nil
		}
	}
	params := map[string]any{
		"cwd":            session.ProjectRoot,
		"approvalPolicy": codexApprovalPolicy(session, chatRecord),
		"sandbox":        codexSandbox(session, chatRecord),
		"ephemeral":      false,
	}
	if len(dynamicTools) > 0 {
		params["dynamicTools"] = dynamicTools
	}
	requestedModel := strings.TrimSpace(chatRecord.ModelID)
	if requestedModel != "" {
		params["model"] = requestedModel
	}
	if m.instructions != nil {
		if instruction := strings.TrimSpace(m.instructions(session, chatRecord)); instruction != "" {
			params["developerInstructions"] = instruction
		}
	}
	var started struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
		Model string `json:"model"`
	}
	if err := m.client.Call(ctx, "thread/start", params, &started); err != nil {
		return "", fmt.Errorf("start Codex thread with model %q: %w", requestedModel, err)
	}
	if started.Thread.ID == "" {
		return "", fmt.Errorf("codex thread/start returned no thread id")
	}
	if actualModel := strings.TrimSpace(started.Model); requestedModel != "" && actualModel != "" && actualModel != requestedModel {
		m.deleteThreadBestEffort(started.Thread.ID)
		return "", fmt.Errorf("codex thread started with model %q, requested %q", actualModel, requestedModel)
	}
	slog.Debug("codex thread started", "chat_id", chatRecord.ID, "thread_id", started.Thread.ID, "requested_model", requestedModel, "actual_model", started.Model)
	if title := strings.TrimSpace(chatRecord.Title); title != "" {
		if err := m.client.Call(ctx, "thread/name/set", map[string]string{"threadId": started.Thread.ID, "name": title}, nil); err != nil {
			m.deleteThreadBestEffort(started.Thread.ID)
			return "", fmt.Errorf("name Codex thread: %w", err)
		}
	}
	if err := m.bindings.put(ctx, Binding{ChatID: chatRecord.ID, ThreadID: started.Thread.ID, Model: started.Model}); err != nil {
		m.deleteThreadBestEffort(started.Thread.ID)
		return "", err
	}
	return started.Thread.ID, nil
}

func (m *Manager) deleteThreadBestEffort(threadID string) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultInterruptTimeout)
	defer cancel()
	_ = m.client.Call(ctx, "thread/delete", map[string]string{"threadId": threadID}, nil)
}

func codexApprovalPolicy(session domain.Session, chatRecord domain.Chat) string {
	profile := strings.TrimSpace(chatRecord.PermissionProfile)
	if profile == "" {
		profile = strings.TrimSpace(session.PermissionProfile)
	}
	if profile == "full-access" {
		return "never"
	}
	return "on-request"
}

func codexSandbox(session domain.Session, chatRecord domain.Chat) string {
	profile := strings.TrimSpace(chatRecord.PermissionProfile)
	if profile == "" {
		profile = strings.TrimSpace(session.PermissionProfile)
	}
	switch profile {
	case "readonly":
		return "read-only"
	case "full-access":
		return "danger-full-access"
	default:
		return "workspace-write"
	}
}

func (m *Manager) consumeTurn(ctx context.Context, rt *chat.Chat, threadID, turnID string, events <-chan codexapp.Message, out chan<- domain.Event) error {
	chatID := rt.Snapshot().Chat.ID
	defer m.clearApprovals(chatID)
	var assistantItem domain.TimelineItem
	ensureAssistantItem := func() {
		if assistantItem.ID == "" {
			assistantItem = rt.NextAssistantItem()
		}
	}
	var text, reasoning strings.Builder
	activeMessageID := ""
	assistantMessages := 0
	flushAssistant := func(completedText string) error {
		if strings.TrimSpace(text.String()) == "" && strings.TrimSpace(completedText) != "" {
			text.WriteString(completedText)
		}
		if strings.TrimSpace(text.String()) == "" && strings.TrimSpace(reasoning.String()) == "" {
			return nil
		}
		ensureAssistantItem()
		item, err := rt.AppendAssistantMessage(ctx, assistantItem, domain.AssistantMessage{
			Text:      text.String(),
			Reasoning: domain.ReasoningContent{Text: reasoning.String()},
		})
		if err != nil {
			return err
		}
		out <- domain.Event{Kind: domain.EventKindMessageDone, Item: item}
		assistantMessages++
		text.Reset()
		reasoning.Reset()
		assistantItem = domain.TimelineItem{}
		return nil
	}
	running := map[string]domain.ToolKind{}
	for {
		select {
		case <-ctx.Done():
			interruptCtx, cancel := context.WithTimeout(context.Background(), defaultInterruptTimeout)
			defer cancel()
			_ = m.client.Call(interruptCtx, "turn/interrupt", map[string]string{"threadId": threadID, "turnId": turnID}, nil)
			return ctx.Err()
		case msg := <-events:
			if msg.TransportErr != nil {
				return msg.TransportErr
			}
			if !messageMatches(msg.Params, threadID, turnID) {
				continue
			}
			switch msg.Method {
			case "item/tool/call":
				if err := m.handleDynamicTool(ctx, rt, msg); err != nil {
					return err
				}
			case "item/commandExecution/requestApproval", "item/fileChange/requestApproval":
				if err := m.handleApprovalRequest(ctx, rt, msg, out); err != nil {
					return err
				}
			case "item/agentMessage/delta":
				delta := stringField(msg.Params, "delta")
				messageID := stringField(msg.Params, "itemId")
				if activeMessageID != "" && messageID != "" && messageID != activeMessageID && text.Len() > 0 {
					if err := flushAssistant(""); err != nil {
						return err
					}
				}
				if messageID != "" {
					activeMessageID = messageID
				}
				ensureAssistantItem()
				text.WriteString(delta)
				out <- domain.Event{Kind: domain.EventKindMessageDelta, Text: delta, Item: assistantItem}
			case "item/reasoning/summaryTextDelta", "item/reasoning/textDelta":
				delta := stringField(msg.Params, "delta")
				ensureAssistantItem()
				reasoning.WriteString(delta)
				out <- domain.Event{Kind: domain.EventKindReasoning, Text: delta, Item: assistantItem}
			case "item/started":
				itemID, kind := toolFromItem(msg.Params)
				if itemID != "" && kind != "" {
					call, ok := toolCallFromItem(msg.Params)
					if ok {
						if _, err := rt.AppendAssistantToolCalls(ctx, rt.NextAssistantItem(), []domain.ToolCall{call}, "", domain.ReasoningContent{}, domain.Usage{}, domain.ModelPerformance{}); err != nil {
							return err
						}
					}
					running[itemID] = kind
					out <- domain.Event{Kind: domain.EventKindToolStart, Tool: kind, ToolCallID: itemID}
				}
			case "item/completed":
				if completed := agentTextFromItem(msg.Params); completed != "" {
					messageID := itemIDFromParams(msg.Params)
					if activeMessageID != "" && messageID != "" && messageID != activeMessageID && text.Len() > 0 {
						if err := flushAssistant(""); err != nil {
							return err
						}
					}
					if text.Len() == 0 {
						ensureAssistantItem()
						text.WriteString(completed)
						out <- domain.Event{Kind: domain.EventKindMessageDelta, Text: completed, Item: assistantItem}
					}
					if err := flushAssistant(""); err != nil {
						return err
					}
					activeMessageID = ""
				}
				itemID := itemIDFromParams(msg.Params)
				if kind := running[itemID]; kind != "" {
					delete(running, itemID)
					if itemTypeFromParams(msg.Params) != "dynamicToolCall" {
						if err := completeCodexTool(ctx, rt, msg.Params, itemID); err != nil {
							return err
						}
					}
					out <- domain.Event{Kind: domain.EventKindToolResult, Tool: kind, ToolCallID: itemID}
				}
			case "turn/completed":
				status, failure := turnCompletion(msg.Params)
				if status == "failed" {
					if failure == "" {
						failure = "Codex turn failed"
					}
					return errors.New(failure)
				}
				if err := flushAssistant(""); err != nil {
					return err
				}
				if assistantMessages == 0 {
					return fmt.Errorf("codex turn completed without an assistant response")
				}
				return nil
			}
		}
	}
}

func (m *Manager) handleDynamicTool(ctx context.Context, rt *chat.Chat, msg codexapp.Message) error {
	if m.tools == nil {
		var request struct {
			CallID string `json:"callId"`
		}
		_ = json.Unmarshal(msg.Params, &request)
		if request.CallID != "" {
			_, _ = rt.AttachToolError(ctx, request.CallID, domain.ToolError{Message: "Koder tool bridge is unavailable"})
		}
		return m.client.Respond(msg.ID, map[string]any{
			"contentItems": []map[string]string{{"type": "inputText", "text": "Koder tool bridge is unavailable"}},
			"success":      false,
		}, nil)
	}
	var request struct {
		CallID    string          `json:"callId"`
		Tool      string          `json:"tool"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(msg.Params, &request); err != nil {
		return m.client.Respond(msg.ID, nil, &codexapp.RPCError{Code: -32602, Message: err.Error()})
	}
	result, err := m.tools.Call(ctx, rt, request.Tool, request.CallID, request.Arguments)
	success := err == nil
	output := strings.TrimSpace(result.Text)
	if output == "" {
		output = strings.TrimSpace(result.Output)
	}
	if output == "" && result.Data != nil {
		if data, marshalErr := json.Marshal(result.Data); marshalErr == nil {
			output = string(data)
		}
	}
	if output == "" {
		output = "Tool completed."
	}
	if err != nil {
		output = "Tool error: " + err.Error()
		if _, attachErr := rt.AttachToolError(ctx, request.CallID, domain.ToolError{Message: err.Error()}); attachErr != nil {
			return attachErr
		}
	} else {
		if _, attachErr := rt.AttachToolResult(ctx, request.CallID, result); attachErr != nil {
			return attachErr
		}
	}
	return m.client.Respond(msg.ID, map[string]any{
		"contentItems": []map[string]string{{"type": "inputText", "text": output}},
		"success":      success,
	}, nil)
}

func (m *Manager) handleApprovalRequest(ctx context.Context, rt *chat.Chat, msg codexapp.Message, out chan<- domain.Event) error {
	var request struct {
		ItemID  string `json:"itemId"`
		Reason  string `json:"reason"`
		Command string `json:"command"`
		CWD     string `json:"cwd"`
	}
	if err := json.Unmarshal(msg.Params, &request); err != nil {
		return err
	}
	if request.ItemID == "" {
		return fmt.Errorf("codex approval request omitted item id")
	}
	body := strings.TrimSpace(request.Reason)
	if body == "" {
		body = strings.TrimSpace(strings.Join([]string{request.Command, request.CWD}, " "))
	}
	if body == "" {
		body = "Codex requests permission to continue this operation."
	}
	key := approvalKey(rt.Snapshot().Chat.ID, request.ItemID)
	m.approvalMu.Lock()
	m.approvals[key] = pendingApproval{requestID: append(json.RawMessage(nil), msg.ID...), method: msg.Method}
	m.approvalMu.Unlock()
	if _, err := rt.AttachToolApproval(ctx, request.ItemID, domain.ApprovalRequest{ID: domain.ID(request.ItemID), Status: domain.ApprovalStatusPending, Body: body}); err != nil {
		m.approvalMu.Lock()
		delete(m.approvals, key)
		m.approvalMu.Unlock()
		return err
	}
	out <- domain.Event{Kind: domain.EventKindApprovalAsk, ToolCallID: request.ItemID, Text: body, Meta: map[string]string{"approval_driver": "external"}}
	return nil
}

func (m *Manager) ResolveTurnApproval(ctx context.Context, rt *chat.Chat, toolCallID string, approved bool, rule *accesssettings.PermissionOverride) (bool, error) {
	if m == nil || rt == nil {
		return false, nil
	}
	key := approvalKey(rt.Snapshot().Chat.ID, toolCallID)
	m.approvalMu.Lock()
	pending, ok := m.approvals[key]
	if ok {
		delete(m.approvals, key)
	}
	m.approvalMu.Unlock()
	if !ok {
		return false, nil
	}
	decision := "decline"
	if approved {
		decision = "accept"
		if rule != nil {
			decision = "acceptForSession"
		}
	}
	if err := m.client.Respond(pending.requestID, map[string]string{"decision": decision}, nil); err != nil {
		return true, err
	}
	if approved {
		_, err := rt.MarkToolRunning(ctx, toolCallID)
		return true, err
	}
	_, err := rt.AttachToolResult(ctx, toolCallID, domain.ToolResult{Text: "Declined by user", Status: domain.ToolResultStatusDenied})
	return true, err
}

func approvalKey(chatID domain.ID, toolCallID string) string {
	return string(chatID) + "\x00" + strings.TrimSpace(toolCallID)
}

func (m *Manager) clearApprovals(chatID domain.ID) {
	prefix := string(chatID) + "\x00"
	m.approvalMu.Lock()
	for key := range m.approvals {
		if strings.HasPrefix(key, prefix) {
			delete(m.approvals, key)
		}
	}
	m.approvalMu.Unlock()
}

const defaultInterruptTimeout = 3 * time.Second

func messageMatches(raw json.RawMessage, threadID, turnID string) bool {
	var envelope struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.ThreadID != threadID {
		return false
	}
	return envelope.TurnID == "" || envelope.TurnID == turnID
}

func stringField(raw json.RawMessage, key string) string {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return ""
	}
	var value string
	_ = json.Unmarshal(fields[key], &value)
	return value
}

func itemIDFromParams(raw json.RawMessage) string {
	var value struct {
		Item struct {
			ID string `json:"id"`
		} `json:"item"`
	}
	_ = json.Unmarshal(raw, &value)
	return value.Item.ID
}

func agentTextFromItem(raw json.RawMessage) string {
	var value struct {
		Item struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"item"`
	}
	_ = json.Unmarshal(raw, &value)
	if value.Item.Type == "agentMessage" {
		return value.Item.Text
	}
	return ""
}

func toolFromItem(raw json.RawMessage) (string, domain.ToolKind) {
	var value struct {
		Item struct {
			ID   string `json:"id"`
			Type string `json:"type"`
			Tool string `json:"tool"`
		} `json:"item"`
	}
	_ = json.Unmarshal(raw, &value)
	switch value.Item.Type {
	case "commandExecution":
		return value.Item.ID, domain.ToolKindExecCommand
	case "fileChange":
		return value.Item.ID, domain.ToolKindFileEdit
	case "dynamicToolCall":
		kind := domain.ToolKind(strings.TrimSpace(value.Item.Tool))
		if domain.IsBuiltinToolKind(kind) {
			return value.Item.ID, kind
		}
		return value.Item.ID, domain.ToolKindMCP
	case "mcpToolCall":
		return value.Item.ID, domain.ToolKindMCP
	case "webSearch":
		return value.Item.ID, domain.ToolKindWebSearch
	default:
		return "", ""
	}
}

func itemTypeFromParams(raw json.RawMessage) string {
	var value struct {
		Item struct {
			Type string `json:"type"`
		} `json:"item"`
	}
	_ = json.Unmarshal(raw, &value)
	return value.Item.Type
}

func toolCallFromItem(raw json.RawMessage) (domain.ToolCall, bool) {
	itemID, kind := toolFromItem(raw)
	if itemID == "" || kind == "" {
		return domain.ToolCall{}, false
	}
	var value struct {
		Item map[string]json.RawMessage `json:"item"`
	}
	if json.Unmarshal(raw, &value) != nil {
		return domain.ToolCall{}, false
	}
	args := map[string]string{}
	for _, key := range []string{"command", "cwd", "query", "path", "server", "tool", "arguments", "changes"} {
		rawValue := value.Item[key]
		if len(rawValue) == 0 || string(rawValue) == "null" {
			continue
		}
		var text string
		if json.Unmarshal(rawValue, &text) == nil {
			args[key] = text
		} else {
			args[key] = string(rawValue)
		}
	}
	return domain.ToolCall{ToolCallID: domain.ToolCallID(itemID), Tool: kind, Args: args, Status: domain.ToolStatusRunning, StartedAt: time.Now().UTC()}, true
}

func completeCodexTool(ctx context.Context, rt *chat.Chat, raw json.RawMessage, itemID string) error {
	var value struct {
		Item struct {
			Status           string          `json:"status"`
			AggregatedOutput string          `json:"aggregatedOutput"`
			ExitCode         *int            `json:"exitCode"`
			Changes          json.RawMessage `json:"changes"`
			Result           json.RawMessage `json:"result"`
			Error            json.RawMessage `json:"error"`
		} `json:"item"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	if value.Item.Status == "failed" {
		message := strings.TrimSpace(string(value.Item.Error))
		if message == "" || message == "null" {
			message = "Codex tool failed"
		}
		_, err := rt.AttachToolError(ctx, itemID, domain.ToolError{Message: message})
		return err
	}
	status := domain.ToolResultStatusOK
	if value.Item.Status == "declined" {
		status = domain.ToolResultStatusDenied
	}
	data := map[string]any{"status": value.Item.Status}
	if value.Item.ExitCode != nil {
		data["exit_code"] = *value.Item.ExitCode
	}
	if len(value.Item.Changes) > 0 && string(value.Item.Changes) != "null" {
		data["changes"] = json.RawMessage(value.Item.Changes)
	}
	if len(value.Item.Result) > 0 && string(value.Item.Result) != "null" {
		data["result"] = json.RawMessage(value.Item.Result)
	}
	_, err := rt.AttachToolResult(ctx, itemID, domain.ToolResult{Text: value.Item.AggregatedOutput, Status: status, Data: data})
	return err
}

func turnCompletion(raw json.RawMessage) (string, string) {
	var value struct {
		Turn struct {
			Status string `json:"status"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		} `json:"turn"`
	}
	_ = json.Unmarshal(raw, &value)
	if value.Turn.Error != nil {
		return value.Turn.Status, value.Turn.Error.Message
	}
	return value.Turn.Status, ""
}
