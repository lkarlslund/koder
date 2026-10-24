// Package claudedriver runs Koder chat turns through Claude Code. Each chat
// gets its own sandboxed `claude` process in stream-json mode, with a private
// configuration directory that holds the chat's Claude Code session. Koder's
// additional tools are offered as an MCP server hosted over the same pipe, so
// they work even when the sandbox has no network.
package claudedriver

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/claudeapp"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/store"
)

// MCPServerName is the name Claude Code knows Koder's tools by; it calls them
// mcp__koder__<tool>.
const MCPServerName = "koder"

type Instructions func(domain.Session, domain.Chat) string

// Tool is a Koder tool offered to Claude Code.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type ToolBridge interface {
	Definitions(context.Context, *chat.Chat) ([]Tool, error)
	Call(context.Context, *chat.Chat, string, string, json.RawMessage) (domain.ToolResult, error)
}

// Model is one model Claude Code offers.
type Model struct {
	Value                 string   `json:"value"`
	ResolvedModel         string   `json:"resolvedModel"`
	DisplayName           string   `json:"displayName"`
	Description           string   `json:"description"`
	SupportedEffortLevels []string `json:"supportedEffortLevels"`
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
	// interruptTimeout bounds the wait for an interrupted turn to end.
	interruptTimeout time.Duration
}

type chatProcess struct {
	client      *claudeapp.Client
	fingerprint string
	tools       []Tool
	sessionID   string
	active      int
	idle        *time.Timer
}

func New(factory ProcessFactory, st *store.Store, instructions Instructions) *Manager {
	return &Manager{
		factory:      factory,
		bindings:     newBindingStore(st),
		instructions: instructions,
		processes:    map[domain.ID]*chatProcess{},
		idleTimeout:  10 * time.Minute,

		interruptTimeout: 5 * time.Second,
	}
}

func (m *Manager) SetToolBridge(bridge ToolBridge) {
	if m != nil {
		m.tools = bridge
	}
}

// Models starts a short-lived process to read the models Claude Code offers
// to the account.
func (m *Manager) Models(ctx context.Context) ([]Model, error) {
	if m == nil {
		return nil, errors.New("claude backend is not configured")
	}
	factory := m.processFactory()
	if factory == nil {
		return nil, errors.New("claude backend is not configured")
	}
	cfg, err := factory.DiscoveryConfig(ctx)
	if err != nil {
		return nil, err
	}
	client := claudeapp.New(cfg, nil)
	defer func() { _ = client.Close() }()
	raw, err := client.Start(ctx, nil)
	if err != nil {
		return nil, err
	}
	var response struct {
		Models []Model `json:"models"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, fmt.Errorf("decode Claude models: %w", err)
	}
	return response.Models, nil
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

// UpdateProcessFactory applies executable and access changes to future
// processes. Running processes are stopped so no chat continues under a stale
// sandbox policy.
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

// UpdateChat stops the process of an archived chat. Claude Code sessions have
// no title to mirror; Koder owns the chat's name.
func (m *Manager) UpdateChat(_ context.Context, before domain.Chat, _ string, archived *bool) error {
	if m == nil || before.EffectiveBackend() != domain.ChatBackendClaude {
		return nil
	}
	if archived != nil && *archived && !before.Archived {
		return m.closeChatProcess(before.ID)
	}
	return nil
}

func (m *Manager) DeleteChat(ctx context.Context, chatRecord domain.Chat) error {
	if m == nil || chatRecord.EffectiveBackend() != domain.ChatBackendClaude {
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
		return errors.New("claude backend is not configured")
	}
	if rt == nil {
		return errors.New("chat runtime is required")
	}
	snapshot := rt.Snapshot()
	if snapshot.Chat.EffectiveBackend() != domain.ChatBackendClaude {
		return fmt.Errorf("claude driver cannot run backend %q", snapshot.Chat.EffectiveBackend())
	}
	var tools []Tool
	if m.tools != nil {
		var err error
		if tools, err = m.tools.Definitions(ctx, rt); err != nil {
			return fmt.Errorf("build Koder tools for Claude: %w", err)
		}
	}
	process, release, err := m.acquireProcess(ctx, snapshot.Session, snapshot.Chat, tools)
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

	inputText := text
	if source := domain.UserMessageSourceForQueuedInput(turn.Input); domain.IsNotificationSource(source) {
		inputText = domain.NotificationMessageText(source, text)
	}
	for _, block := range turn.EphemeralInstructions {
		if instruction := strings.TrimSpace(block.Text); instruction != "" {
			inputText = instruction + "\n\n" + inputText
		}
	}
	discardStale(process.client)
	if err := process.client.SendUser(inputText); err != nil {
		return fmt.Errorf("send Claude turn: %w", err)
	}
	out <- domain.Event{Kind: domain.EventKindStatus, Text: "Claude is working"}
	turnErr := m.consumeTurn(ctx, process, rt, out)
	// The session exists once Claude Code has seen a message, even if the
	// turn failed, so later processes resume it.
	if process.sessionID != "" {
		if binding, ok, err := m.bindings.find(context.WithoutCancel(ctx), snapshot.Chat.ID); err == nil && (!ok || binding.SessionID != process.sessionID) {
			if err := m.bindings.put(context.WithoutCancel(ctx), Binding{ChatID: snapshot.Chat.ID, SessionID: process.sessionID, CreatedAt: binding.CreatedAt}); err != nil {
				return errors.Join(turnErr, err)
			}
		}
	}
	return turnErr
}

// discardStale drops output left over from an earlier turn, such as the
// result of a turn that was interrupted after Koder stopped waiting for it.
func discardStale(client *claudeapp.Client) {
	for {
		select {
		case msg := <-client.Messages():
			if msg.Type == "control_request" {
				var request claudeapp.ControlRequest
				if json.Unmarshal(msg.Raw, &request) == nil {
					_ = client.Respond(request.RequestID, nil, errors.New("no Koder turn is running"))
				}
			}
		default:
			return
		}
	}
}

func (m *Manager) chatArgs(session domain.Session, chatRecord domain.Chat) []string {
	args := append([]string(nil), claudeapp.StreamArgs...)
	mcpConfig, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{MCPServerName: map[string]string{"type": "sdk", "name": MCPServerName}}})
	args = append(args,
		"--include-partial-messages",
		// Koder's bubblewrap sandbox is the boundary; there is nobody to
		// answer Claude Code's own permission prompts.
		"--permission-mode", "bypassPermissions",
		"--mcp-config", string(mcpConfig),
	)
	if model := strings.TrimSpace(chatRecord.ModelID); model != "" {
		args = append(args, "--model", model)
	}
	if m.instructions != nil {
		if instruction := strings.TrimSpace(m.instructions(session, chatRecord)); instruction != "" {
			args = append(args, "--append-system-prompt", instruction)
		}
	}
	return args
}

func (m *Manager) acquireProcess(ctx context.Context, session domain.Session, chatRecord domain.Chat, tools []Tool) (*chatProcess, func(), error) {
	factory := m.processFactory()
	if factory == nil {
		return nil, nil, errors.New("claude backend is not configured")
	}
	cfg, err := factory.ChatConfig(ctx, session, chatRecord, m.chatArgs(session, chatRecord))
	if err != nil {
		return nil, nil, err
	}
	toolsJSON, err := json.Marshal(tools)
	if err != nil {
		return nil, nil, err
	}
	digest := sha256.Sum256([]byte(cfg.Fingerprint + "\x00" + string(toolsJSON)))
	fingerprint := hex.EncodeToString(digest[:])

	m.processMu.Lock()
	process := m.processes[chatRecord.ID]
	if process != nil && (process.fingerprint != fingerprint || exited(process.client)) {
		delete(m.processes, chatRecord.ID)
		if process.idle != nil {
			process.idle.Stop()
		}
		stale := process
		process = nil
		// Closed outside the lock so teardown cannot block other chats.
		defer func() { _ = stale.client.Close() }()
	}
	if process != nil {
		if process.idle != nil {
			process.idle.Stop()
			process.idle = nil
		}
		process.active++
		m.processMu.Unlock()
		return process, m.releaser(chatRecord.ID, process), nil
	}
	m.processMu.Unlock()

	process, err = m.startProcess(ctx, chatRecord.ID, cfg.Client, fingerprint, tools)
	if err != nil {
		return nil, nil, err
	}
	m.processMu.Lock()
	if existing := m.processes[chatRecord.ID]; existing != nil {
		// Another caller started one first; keep theirs.
		existing.active++
		if existing.idle != nil {
			existing.idle.Stop()
			existing.idle = nil
		}
		m.processMu.Unlock()
		_ = process.client.Close()
		return existing, m.releaser(chatRecord.ID, existing), nil
	}
	process.active = 1
	m.processes[chatRecord.ID] = process
	m.processMu.Unlock()
	return process, m.releaser(chatRecord.ID, process), nil
}

func exited(client *claudeapp.Client) bool {
	select {
	case <-client.Done():
		return true
	default:
		return false
	}
}

// startProcess resumes the chat's session, or starts a new one. A session
// that can no longer be resumed is replaced rather than leaving the chat
// unusable.
func (m *Manager) startProcess(ctx context.Context, chatID domain.ID, cfg claudeapp.Config, fingerprint string, tools []Tool) (*chatProcess, error) {
	binding, resumable, err := m.bindings.find(ctx, chatID)
	if err != nil {
		return nil, err
	}
	resumable = resumable && binding.SessionID != ""
	for {
		process := &chatProcess{fingerprint: fingerprint, tools: tools}
		sessionCfg := cfg
		sessionCfg.Args = append([]string(nil), cfg.Args...)
		if resumable {
			process.sessionID = binding.SessionID
			sessionCfg.Args = append(sessionCfg.Args, "--resume", binding.SessionID)
		} else {
			process.sessionID = newSessionID()
			sessionCfg.Args = append(sessionCfg.Args, "--session-id", process.sessionID)
		}
		process.client = claudeapp.New(sessionCfg, process.handleControl)
		_, err := process.client.Start(ctx, map[string]any{"sdkMcpServers": []string{MCPServerName}})
		if err == nil {
			return process, nil
		}
		if !resumable || ctx.Err() != nil {
			return nil, err
		}
		slog.Warn("claude session could not be resumed; starting a new one", "chat_id", chatID, "session_id", binding.SessionID, "error", err)
		resumable = false
	}
}

func newSessionID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

func (m *Manager) releaser(chatID domain.ID, process *chatProcess) func() {
	var once sync.Once
	return func() { once.Do(func() { m.releaseProcess(chatID, process) }) }
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
		m.processMu.Lock()
		if m.processes[chatID] != process || process.active != 0 {
			m.processMu.Unlock()
			return
		}
		delete(m.processes, chatID)
		m.processMu.Unlock()
		_ = process.client.Close()
	})
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

// handleControl answers Claude Code's MCP traffic for Koder's tools on the
// reading goroutine. Tool calls are left to the running turn, which records
// them in the chat's timeline in order.
func (p *chatProcess) handleControl(request claudeapp.ControlRequest) (any, bool, error) {
	var body struct {
		Subtype    string          `json:"subtype"`
		ServerName string          `json:"server_name"`
		Message    json.RawMessage `json:"message"`
	}
	if err := json.Unmarshal(request.Request, &body); err != nil {
		return nil, true, err
	}
	if body.Subtype != "mcp_message" {
		return nil, true, fmt.Errorf("koder does not handle %q requests", body.Subtype)
	}
	if body.ServerName != MCPServerName {
		return nil, true, fmt.Errorf("unknown MCP server %q", body.ServerName)
	}
	var message mcpMessage
	if err := json.Unmarshal(body.Message, &message); err != nil {
		return nil, true, err
	}
	if len(message.ID) == 0 {
		return mcpReply(nil, map[string]any{}), true, nil
	}
	switch message.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(message.Params, &params)
		return mcpReply(message.ID, map[string]any{
			"protocolVersion": params.ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]string{"name": MCPServerName, "version": "1"},
		}), true, nil
	case "tools/list":
		tools := p.tools
		if tools == nil {
			tools = []Tool{}
		}
		return mcpReply(message.ID, map[string]any{"tools": tools}), true, nil
	case "tools/call":
		return nil, false, nil
	case "ping":
		return mcpReply(message.ID, map[string]any{}), true, nil
	default:
		return map[string]any{"mcp_response": map[string]any{
			"jsonrpc": "2.0", "id": message.ID,
			"error": map[string]any{"code": -32601, "message": "method not found: " + message.Method},
		}}, true, nil
	}
}

type mcpMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func mcpReply(id json.RawMessage, result any) map[string]any {
	response := map[string]any{"jsonrpc": "2.0", "result": result}
	if len(id) > 0 {
		response["id"] = id
	}
	return map[string]any{"mcp_response": response}
}
