package codexdriver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

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
	Call(context.Context, *chat.Chat, string, string, json.RawMessage) (string, error)
}

type Manager struct {
	client       *codexapp.Client
	bindings     bindingStore
	instructions Instructions
	tools        ToolBridge
	threadMu     sync.Mutex
}

func (m *Manager) SetToolBridge(bridge ToolBridge) {
	if m != nil {
		m.tools = bridge
	}
}

func New(client *codexapp.Client, st *store.Store, instructions Instructions) *Manager {
	return &Manager{client: client, bindings: newBindingStore(st), instructions: instructions}
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
		return err
	}
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
			return binding.ThreadID, nil
		}
	}
	params := map[string]any{
		"cwd":            session.ProjectRoot,
		"approvalPolicy": "never",
		"sandbox":        codexSandbox(session, chatRecord),
		"ephemeral":      false,
	}
	if len(dynamicTools) > 0 {
		params["dynamicTools"] = dynamicTools
	}
	if model := strings.TrimSpace(chatRecord.ModelID); model != "" {
		params["model"] = model
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
		return "", err
	}
	if started.Thread.ID == "" {
		return "", fmt.Errorf("codex thread/start returned no thread id")
	}
	if err := m.bindings.put(ctx, Binding{ChatID: chatRecord.ID, ThreadID: started.Thread.ID, Model: started.Model}); err != nil {
		return "", err
	}
	return started.Thread.ID, nil
}

func codexSandbox(session domain.Session, chatRecord domain.Chat) string {
	profile := strings.TrimSpace(chatRecord.PermissionProfile)
	if profile == "" {
		profile = strings.TrimSpace(session.PermissionProfile)
	}
	switch profile {
	case "readonly":
		return "readOnly"
	case "full-access":
		return "dangerFullAccess"
	default:
		return "workspaceWrite"
	}
}

func (m *Manager) consumeTurn(ctx context.Context, rt *chat.Chat, threadID, turnID string, events <-chan codexapp.Message, out chan<- domain.Event) error {
	assistantItem := rt.NextAssistantItem()
	var text, reasoning strings.Builder
	running := map[string]domain.ToolKind{}
	for {
		select {
		case <-ctx.Done():
			interruptCtx, cancel := context.WithTimeout(context.Background(), defaultInterruptTimeout)
			defer cancel()
			_ = m.client.Call(interruptCtx, "turn/interrupt", map[string]string{"threadId": threadID, "turnId": turnID}, nil)
			return ctx.Err()
		case msg := <-events:
			if !messageMatches(msg.Params, threadID, turnID) {
				continue
			}
			switch msg.Method {
			case "item/tool/call":
				if err := m.handleDynamicTool(ctx, rt, msg); err != nil {
					return err
				}
			case "item/agentMessage/delta":
				delta := stringField(msg.Params, "delta")
				text.WriteString(delta)
				out <- domain.Event{Kind: domain.EventKindMessageDelta, Text: delta, Item: assistantItem}
			case "item/reasoning/summaryTextDelta", "item/reasoning/textDelta":
				delta := stringField(msg.Params, "delta")
				reasoning.WriteString(delta)
				out <- domain.Event{Kind: domain.EventKindReasoning, Text: delta, Item: assistantItem}
			case "item/started":
				itemID, kind := toolFromItem(msg.Params)
				if itemID != "" && kind != "" {
					running[itemID] = kind
					out <- domain.Event{Kind: domain.EventKindToolStart, Tool: kind, ToolCallID: itemID}
				}
			case "item/completed":
				if completed := agentTextFromItem(msg.Params); completed != "" && text.Len() == 0 {
					text.WriteString(completed)
					out <- domain.Event{Kind: domain.EventKindMessageDelta, Text: completed, Item: assistantItem}
				}
				itemID := itemIDFromParams(msg.Params)
				if kind := running[itemID]; kind != "" {
					delete(running, itemID)
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
				if strings.TrimSpace(text.String()) == "" && strings.TrimSpace(reasoning.String()) == "" {
					return fmt.Errorf("codex turn completed without an assistant response")
				}
				item, err := rt.AppendAssistantMessage(ctx, assistantItem, domain.AssistantMessage{
					Text:      text.String(),
					Reasoning: domain.ReasoningContent{Text: reasoning.String()},
				})
				if err != nil {
					return err
				}
				out <- domain.Event{Kind: domain.EventKindMessageDone, Item: item}
				return nil
			}
		}
	}
}

func (m *Manager) handleDynamicTool(ctx context.Context, rt *chat.Chat, msg codexapp.Message) error {
	if m.tools == nil {
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
	output, err := m.tools.Call(ctx, rt, request.Tool, request.CallID, request.Arguments)
	success := err == nil
	if err != nil {
		output = "Tool error: " + err.Error()
	}
	return m.client.Respond(msg.ID, map[string]any{
		"contentItems": []map[string]string{{"type": "inputText", "text": output}},
		"success":      success,
	}, nil)
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
		} `json:"item"`
	}
	_ = json.Unmarshal(raw, &value)
	switch value.Item.Type {
	case "commandExecution":
		return value.Item.ID, domain.ToolKindExecCommand
	case "fileChange":
		return value.Item.ID, domain.ToolKindFileEdit
	case "mcpToolCall", "dynamicToolCall":
		return value.Item.ID, domain.ToolKindMCP
	case "webSearch":
		return value.Item.ID, domain.ToolKindWebSearch
	default:
		return "", ""
	}
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
