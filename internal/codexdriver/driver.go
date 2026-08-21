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

type Manager struct {
	factoryMu    sync.RWMutex
	factory      ProcessFactory
	bindings     bindingStore
	instructions Instructions
	tools        ToolBridge
	processMu    sync.Mutex
	processes    map[domain.ID]*chatProcess
	idleTimeout  time.Duration
}

type chatProcess struct {
	client      *codexapp.Client
	fingerprint string
	network     bool
	active      int
	idle        *time.Timer
}

func (m *Manager) SetToolBridge(bridge ToolBridge) {
	if m != nil {
		m.tools = bridge
	}
}

func New(factory ProcessFactory, st *store.Store, instructions Instructions) *Manager {
	return &Manager{
		factory:      factory,
		bindings:     newBindingStore(st),
		instructions: instructions,
		processes:    map[domain.ID]*chatProcess{},
		idleTimeout:  10 * time.Minute,
	}
}

func (m *Manager) Models(ctx context.Context) ([]codexapp.Model, error) {
	if m == nil {
		return nil, fmt.Errorf("codex backend is not configured")
	}
	factory := m.processFactory()
	if factory == nil {
		return nil, fmt.Errorf("codex backend is not configured")
	}
	cfg, err := factory.DiscoveryConfig(ctx)
	if err != nil {
		return nil, err
	}
	client := codexapp.New(cfg)
	defer client.Close()
	return client.Models(ctx)
}

func (m *Manager) Close() error {
	if m == nil {
		return nil
	}
	m.processMu.Lock()
	processes := m.processes
	m.processes = map[domain.ID]*chatProcess{}
	m.processMu.Unlock()
	errs := make([]error, 0, len(processes))
	for _, process := range processes {
		if process.idle != nil {
			process.idle.Stop()
		}
		errs = append(errs, process.client.Close())
	}
	return errors.Join(errs...)
}

// UpdateProcessFactory applies executable, authentication-home, and access
// resolver changes to future processes. Existing processes are stopped so no
// chat can continue under a stale sandbox policy.
func (m *Manager) UpdateProcessFactory(factory ProcessFactory) error {
	if m == nil {
		return nil
	}
	m.factoryMu.Lock()
	m.factory = factory
	m.factoryMu.Unlock()
	return m.Close()
}

func (m *Manager) processFactory() ProcessFactory {
	m.factoryMu.RLock()
	defer m.factoryMu.RUnlock()
	return m.factory
}

// UpdateChat mirrors user-facing chat metadata into an existing Codex thread.
// Chats that have never run have no thread yet and require no remote action.
func (m *Manager) UpdateChat(ctx context.Context, before domain.Chat, title string, archived *bool) error {
	if m == nil || before.EffectiveBackend() != domain.ChatBackendCodex {
		return nil
	}
	binding, ok, err := m.bindings.find(ctx, before.ID)
	if err != nil || !ok || binding.ThreadID == "" {
		return err
	}
	client, release := m.acquireRunningClient(before.ID)
	// Koder owns the canonical title/archive state. A stopped disposable
	// process does not need to be restarted solely to mirror metadata.
	if client == nil {
		return nil
	}
	defer release()
	if nextTitle := strings.TrimSpace(title); nextTitle != "" && nextTitle != strings.TrimSpace(before.Title) {
		if err := client.Call(ctx, "thread/name/set", map[string]string{"threadId": binding.ThreadID, "name": nextTitle}, nil); err != nil {
			return fmt.Errorf("rename Codex thread: %w", err)
		}
	}
	// Koder owns archive state. Archiving the underlying Codex thread would
	// move its rollout and make a later lazy restore unable to resume it
	// without launching a process during the metadata update.
	if archived != nil && *archived && *archived != before.Archived {
		return m.closeChatProcess(before.ID)
	}
	return nil
}

func (m *Manager) DeleteChat(ctx context.Context, chatRecord domain.Chat) error {
	if m == nil || chatRecord.EffectiveBackend() != domain.ChatBackendCodex {
		return nil
	}
	closeErr := m.closeChatProcess(chatRecord.ID)
	bindingErr := m.bindings.delete(ctx, chatRecord.ID)
	var removeErr error
	if factory := m.processFactory(); factory != nil {
		removeErr = factory.RemoveChat(chatRecord.ID)
	}
	return errors.Join(closeErr, bindingErr, removeErr)
}

func (m *Manager) RunTurn(ctx context.Context, rt *chat.Chat, turn chat.DriverTurn, out chan<- domain.Event) error {
	if m == nil {
		return fmt.Errorf("codex backend is not configured")
	}
	if rt == nil {
		return fmt.Errorf("chat runtime is required")
	}
	snapshot := rt.Snapshot()
	if snapshot.Chat.EffectiveBackend() != domain.ChatBackendCodex {
		return fmt.Errorf("codex driver cannot run backend %q", snapshot.Chat.EffectiveBackend())
	}
	client, network, release, err := m.acquireProcess(ctx, snapshot.Session, snapshot.Chat)
	if err != nil {
		return err
	}
	defer release()

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

	events, unsubscribe := client.Subscribe(1024)
	defer unsubscribe()
	threadID, err := m.ensureThread(ctx, client, rt, snapshot.Session, snapshot.Chat)
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
		"threadId":       threadID,
		"input":          []map[string]any{{"type": "text", "text": inputText}},
		"approvalPolicy": "never",
		"sandboxPolicy":  externalSandboxPolicy(network),
	}
	if model := strings.TrimSpace(snapshot.Chat.ModelID); model != "" {
		params["model"] = model
	}
	if err := client.Call(ctx, "turn/start", params, &started); err != nil {
		return fmt.Errorf("start Codex turn with model %q: %w", strings.TrimSpace(snapshot.Chat.ModelID), err)
	}
	slog.Debug("codex turn started", "chat_id", snapshot.Chat.ID, "thread_id", threadID, "turn_id", started.Turn.ID, "model", strings.TrimSpace(snapshot.Chat.ModelID))
	if started.Turn.ID == "" {
		return fmt.Errorf("codex turn/start returned no turn id")
	}
	out <- domain.Event{Kind: domain.EventKindStatus, Text: "Codex is working"}
	return m.consumeTurn(ctx, client, rt, threadID, started.Turn.ID, events, out)
}

func (m *Manager) ensureThread(ctx context.Context, client *codexapp.Client, rt *chat.Chat, session domain.Session, chatRecord domain.Chat) (string, error) {
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
		resumeParams := map[string]any{
			"threadId":       binding.ThreadID,
			"approvalPolicy": "never",
			"sandbox":        "danger-full-access",
		}
		if len(dynamicTools) > 0 {
			resumeParams["dynamicTools"] = dynamicTools
		}
		if err := client.Call(ctx, "thread/resume", resumeParams, &resumed); err == nil {
			slog.Debug("codex thread resumed", "chat_id", chatRecord.ID, "thread_id", binding.ThreadID, "binding_model", binding.Model, "requested_model", chatRecord.ModelID)
			return binding.ThreadID, nil
		}
	}
	params := map[string]any{
		"cwd":            session.ProjectRoot,
		"approvalPolicy": "never",
		// Codex must not apply a second, conflicting sandbox. The entire
		// app-server is already confined by Koder's bubblewrap process.
		"sandbox":   "danger-full-access",
		"ephemeral": false,
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
	if err := client.Call(ctx, "thread/start", params, &started); err != nil {
		return "", fmt.Errorf("start Codex thread with model %q: %w", requestedModel, err)
	}
	if started.Thread.ID == "" {
		return "", fmt.Errorf("codex thread/start returned no thread id")
	}
	if actualModel := strings.TrimSpace(started.Model); requestedModel != "" && actualModel != "" && actualModel != requestedModel {
		m.deleteThreadBestEffort(client, started.Thread.ID)
		return "", fmt.Errorf("codex thread started with model %q, requested %q", actualModel, requestedModel)
	}
	slog.Debug("codex thread started", "chat_id", chatRecord.ID, "thread_id", started.Thread.ID, "requested_model", requestedModel, "actual_model", started.Model)
	if title := strings.TrimSpace(chatRecord.Title); title != "" {
		if err := client.Call(ctx, "thread/name/set", map[string]string{"threadId": started.Thread.ID, "name": title}, nil); err != nil {
			m.deleteThreadBestEffort(client, started.Thread.ID)
			return "", fmt.Errorf("name Codex thread: %w", err)
		}
	}
	if err := m.bindings.put(ctx, Binding{ChatID: chatRecord.ID, ThreadID: started.Thread.ID, Model: started.Model}); err != nil {
		m.deleteThreadBestEffort(client, started.Thread.ID)
		return "", err
	}
	return started.Thread.ID, nil
}

func (m *Manager) deleteThreadBestEffort(client *codexapp.Client, threadID string) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultInterruptTimeout)
	defer cancel()
	_ = client.Call(ctx, "thread/delete", map[string]string{"threadId": threadID}, nil)
}

func externalSandboxPolicy(network bool) map[string]any {
	access := "restricted"
	if network {
		access = "enabled"
	}
	return map[string]any{"type": "externalSandbox", "networkAccess": access}
}

func (m *Manager) acquireProcess(ctx context.Context, session domain.Session, chatRecord domain.Chat) (*codexapp.Client, bool, func(), error) {
	factory := m.processFactory()
	if factory == nil {
		return nil, false, nil, fmt.Errorf("codex backend is not configured")
	}
	threadID := ""
	if binding, ok, err := m.bindings.find(ctx, chatRecord.ID); err != nil {
		return nil, false, nil, err
	} else if ok {
		threadID = binding.ThreadID
	}
	cfg, err := factory.ChatConfig(ctx, session, chatRecord, threadID)
	if err != nil {
		return nil, false, nil, err
	}

	m.processMu.Lock()
	process := m.processes[chatRecord.ID]
	var stale *chatProcess
	if process != nil && process.fingerprint != cfg.Fingerprint {
		stale = process
		delete(m.processes, chatRecord.ID)
		if process.idle != nil {
			process.idle.Stop()
		}
		process = nil
	}
	if process == nil {
		process = &chatProcess{client: codexapp.New(cfg.Client), fingerprint: cfg.Fingerprint, network: cfg.Network}
		m.processes[chatRecord.ID] = process
	}
	if process.idle != nil {
		process.idle.Stop()
		process.idle = nil
	}
	process.active++
	m.processMu.Unlock()

	// A stale process is closed outside the manager lock so process teardown
	// cannot block an unrelated Codex chat.
	if stale != nil {
		_ = stale.client.Close()
	}
	if err := process.client.Start(ctx); err != nil {
		m.releaseProcess(chatRecord.ID, process)
		return nil, false, nil, err
	}
	var once sync.Once
	release := func() { once.Do(func() { m.releaseProcess(chatRecord.ID, process) }) }
	return process.client, process.network, release, nil
}

func (m *Manager) releaseProcess(chatID domain.ID, process *chatProcess) {
	m.processMu.Lock()
	defer m.processMu.Unlock()
	if m.processes[chatID] != process {
		return
	}
	if process.active > 0 {
		process.active--
	}
	if process.active != 0 || process.idle != nil {
		return
	}
	timeout := m.idleTimeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	process.idle = time.AfterFunc(timeout, func() {
		_ = m.closeIdleProcess(chatID, process)
	})
}

func (m *Manager) closeIdleProcess(chatID domain.ID, process *chatProcess) error {
	m.processMu.Lock()
	if m.processes[chatID] != process || process.active != 0 {
		m.processMu.Unlock()
		return nil
	}
	delete(m.processes, chatID)
	m.processMu.Unlock()
	return process.client.Close()
}

func (m *Manager) acquireRunningClient(chatID domain.ID) (*codexapp.Client, func()) {
	m.processMu.Lock()
	process := m.processes[chatID]
	if process == nil {
		m.processMu.Unlock()
		return nil, func() {}
	}
	if process.idle != nil {
		process.idle.Stop()
		process.idle = nil
	}
	process.active++
	m.processMu.Unlock()
	var once sync.Once
	return process.client, func() { once.Do(func() { m.releaseProcess(chatID, process) }) }
}

func (m *Manager) closeChatProcess(chatID domain.ID) error {
	m.processMu.Lock()
	process := m.processes[chatID]
	delete(m.processes, chatID)
	if process != nil && process.idle != nil {
		process.idle.Stop()
	}
	m.processMu.Unlock()
	if process == nil {
		return nil
	}
	return process.client.Close()
}

func (m *Manager) consumeTurn(ctx context.Context, client *codexapp.Client, rt *chat.Chat, threadID, turnID string, events <-chan codexapp.Message, out chan<- domain.Event) error {
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
			_ = client.Call(interruptCtx, "turn/interrupt", map[string]string{"threadId": threadID, "turnId": turnID}, nil)
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
				if err := m.handleDynamicTool(ctx, client, rt, msg); err != nil {
					return err
				}
			case "item/commandExecution/requestApproval", "item/fileChange/requestApproval", "item/permissions/requestApproval":
				_ = client.Respond(msg.ID, map[string]string{"decision": "decline"}, nil)
				return fmt.Errorf("Codex requested interactive approval despite Koder's externally enforced sandbox")
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

func (m *Manager) handleDynamicTool(ctx context.Context, client *codexapp.Client, rt *chat.Chat, msg codexapp.Message) error {
	if m.tools == nil {
		var request struct {
			CallID string `json:"callId"`
		}
		_ = json.Unmarshal(msg.Params, &request)
		if request.CallID != "" {
			_, _ = rt.AttachToolError(ctx, request.CallID, domain.ToolError{Message: "Koder tool bridge is unavailable"})
		}
		return client.Respond(msg.ID, map[string]any{
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
		return client.Respond(msg.ID, nil, &codexapp.RPCError{Code: -32602, Message: err.Error()})
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
	return client.Respond(msg.ID, map[string]any{
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
		message := codexToolFailure(value.Item.Error, value.Item.AggregatedOutput, value.Item.ExitCode)
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
	output, truncated := truncateCodexToolOutput(value.Item.AggregatedOutput)
	if truncated {
		data["output_truncated"] = true
	}
	_, err := rt.AttachToolResult(ctx, itemID, domain.ToolResult{Text: output, Status: status, Data: data})
	return err
}

const maxCodexToolOutputBytes = 64 * 1024

func codexToolFailure(raw json.RawMessage, output string, exitCode *int) string {
	message := ""
	if len(raw) > 0 && string(raw) != "null" {
		var structured struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(raw, &structured) == nil {
			message = strings.TrimSpace(structured.Message)
		}
		if message == "" {
			var plain string
			if json.Unmarshal(raw, &plain) == nil {
				message = strings.TrimSpace(plain)
			}
		}
	}
	output, _ = truncateCodexToolOutput(output)
	if strings.TrimSpace(output) != "" && !strings.Contains(message, strings.TrimSpace(output)) {
		if message != "" {
			message += "\n\n"
		}
		message += strings.TrimSpace(output)
	}
	if message == "" {
		message = "Codex tool failed"
	}
	if exitCode != nil {
		message = fmt.Sprintf("%s\n\nExit code: %d", message, *exitCode)
	}
	return message
}

func truncateCodexToolOutput(value string) (string, bool) {
	if len(value) <= maxCodexToolOutputBytes {
		return value, false
	}
	marker := "\n\n... output truncated ...\n\n"
	half := (maxCodexToolOutputBytes - len(marker)) / 2
	headEnd := half
	for headEnd > 0 && value[headEnd]&0xc0 == 0x80 {
		headEnd--
	}
	tailStart := len(value) - half
	for tailStart < len(value) && value[tailStart]&0xc0 == 0x80 {
		tailStart++
	}
	return value[:headEnd] + marker + value[tailStart:], true
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
