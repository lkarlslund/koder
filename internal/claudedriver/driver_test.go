package claudedriver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/claudeapp"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/store"
)

// helperFactory runs this test binary as a fake Claude Code process.
type helperFactory struct {
	mode    string
	removed []domain.ID
	args    [][]string
}

func (f *helperFactory) config(args []string) claudeapp.Config {
	return claudeapp.Config{
		Executable: os.Args[0],
		Args:       append([]string{"-test.run=TestClaudeDriverHelperProcess", "--"}, args...),
		Env:        []string{"KODER_CLAUDE_DRIVER_HELPER=" + f.mode},
	}
}

func (f *helperFactory) DiscoveryConfig(context.Context) (claudeapp.Config, error) {
	return f.config(nil), nil
}

func (f *helperFactory) ChatConfig(_ context.Context, _ domain.Session, _ domain.Chat, args []string) (ChatProcessConfig, error) {
	f.args = append(f.args, args)
	return ChatProcessConfig{Client: f.config(args), Fingerprint: "fixed"}, nil
}

func (f *helperFactory) RemoveChat(chatID domain.ID) error {
	f.removed = append(f.removed, chatID)
	return nil
}

type fakeToolBridge struct{ calls []string }

func (b *fakeToolBridge) Definitions(context.Context, *chat.Chat) ([]Tool, error) {
	return []Tool{{Name: "milestone_list", Description: "List milestones", InputSchema: json.RawMessage(`{"type":"object"}`)}}, nil
}

func (b *fakeToolBridge) Call(_ context.Context, _ *chat.Chat, name, callID string, arguments json.RawMessage) (domain.ToolResult, error) {
	b.calls = append(b.calls, name+" "+callID+" "+string(arguments))
	return domain.ToolResult{Text: "No milestones", Status: domain.ToolResultStatusOK}, nil
}

type harness struct {
	manager *Manager
	factory *helperFactory
	bridge  *fakeToolBridge
	runtime *chat.Chat
	chat    domain.Chat
}

func newHarness(t *testing.T, mode string) *harness {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	factory := &helperFactory{mode: mode}
	manager := New(factory, st, func(domain.Session, domain.Chat) string { return "Be concise." })
	bridge := &fakeToolBridge{}
	manager.SetToolBridge(bridge)
	t.Cleanup(func() { _ = manager.Close() })
	sessionRecord := domain.Session{ID: id.ID("session-1"), ProjectRoot: t.TempDir()}
	chatRecord := domain.Chat{ID: id.ID("chat-1"), SessionID: sessionRecord.ID, Backend: domain.ChatBackendClaude, InteractionMode: domain.InteractionModeText, ModelID: "sonnet"}
	source := chat.NewSource(func() chat.Deps { return chat.Deps{Store: st} })
	if err := source.PutRecord(context.Background(), chatRecord); err != nil {
		t.Fatal(err)
	}
	runtime, err := source.Load(context.Background(), sessionRecord, chatRecord)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runtime.Close() })
	return &harness{manager: manager, factory: factory, bridge: bridge, runtime: runtime, chat: chatRecord}
}

func (h *harness) process() *chatProcess {
	h.manager.processMu.Lock()
	defer h.manager.processMu.Unlock()
	return h.manager.processes[h.chat.ID]
}

func (h *harness) run(ctx context.Context, text string) error {
	events := make(chan domain.Event, 256)
	return h.manager.RunTurn(ctx, h.runtime, chat.DriverTurn{Input: domain.QueuedInput{ID: id.New(), Text: text}}, events)
}

func TestManagerPersistsClaudeTurnInKoderTimeline(t *testing.T) {
	h := newHarness(t, "turn")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := h.run(ctx, "Implement it"); err != nil {
		t.Fatal(err)
	}
	args := h.factory.args[0]
	for _, want := range [][]string{{"--model", "sonnet"}, {"--append-system-prompt", "Be concise."}, {"--permission-mode", "bypassPermissions"}} {
		if index := slices.Index(args, want[0]); index < 0 || args[index+1] != want[1] {
			t.Fatalf("args %q lack %q", args, want)
		}
	}

	timeline := h.runtime.SnapshotTimeline()
	var texts []string
	tools := map[domain.ToolKind]domain.ToolCall{}
	for _, item := range timeline {
		switch content := item.Content.(type) {
		case domain.UserMessage:
			texts = append(texts, "user:"+content.Text)
		case domain.AssistantMessage:
			if content.Text != "" || content.Reasoning.Text != "" {
				texts = append(texts, "assistant:"+content.Reasoning.Text+"|"+content.Text)
			}
			for _, call := range content.Tools {
				tools[call.Tool] = call
				texts = append(texts, "tool:"+string(call.Tool))
			}
		}
	}
	want := []string{"user:Implement it", "assistant:Plan first.|Checking.", "tool:milestone_list", "tool:bash", "tool:file_edit", "assistant:|Done."}
	if !slices.Equal(texts, want) {
		t.Fatalf("timeline = %q, want %q", texts, want)
	}
	if call := tools[domain.ToolKindMilestoneList]; call.Result == nil || call.Result.Text != "No milestones" {
		t.Fatalf("Koder tool = %#v", call)
	}
	if len(h.bridge.calls) != 1 || h.bridge.calls[0] != `milestone_list toolu_koder {"action":"list"}` {
		t.Fatalf("bridge calls = %q", h.bridge.calls)
	}
	if call := tools[domain.ToolKindBash]; call.Args["command"] != "echo hello" || call.Result == nil || fmt.Sprint(call.Result.Data) != "map[output:hello]" {
		t.Fatalf("bash tool = %#v", call)
	}
	if call := tools[domain.ToolKindFileEdit]; call.Args["path"] != "/p/main.go" || call.Error == nil || call.Error.Message != "old_string not found" {
		t.Fatalf("edit tool = %#v", call)
	}
	binding, ok, err := h.manager.bindings.find(ctx, h.chat.ID)
	sessionArg := slices.Index(args, "--session-id")
	if err != nil || !ok || sessionArg >= 0 || binding.SessionID == "" {
		// The session flag is appended by the manager, after the factory.
		t.Fatalf("binding = %#v, %v, %v; args %q", binding, ok, err, args)
	}

	// The next turn reuses the running process and its session.
	first := h.process()
	if err := h.run(ctx, "Again"); err != nil {
		t.Fatal(err)
	}
	if first == nil || h.process() != first {
		t.Fatal("process restarted for the second turn")
	}
}

func TestManagerResumesBoundSessionAndReplacesLostOne(t *testing.T) {
	h := newHarness(t, "turn")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := h.manager.bindings.put(ctx, Binding{ChatID: h.chat.ID, SessionID: "lost-session"}); err != nil {
		t.Fatal(err)
	}
	if err := h.run(ctx, "Hello"); err != nil {
		t.Fatal(err)
	}
	binding, _, err := h.manager.bindings.find(ctx, h.chat.ID)
	if err != nil || binding.SessionID == "lost-session" || binding.SessionID == "" {
		t.Fatalf("binding = %#v, %v", binding, err)
	}

	// A restarted process resumes the session the binding now names.
	if err := h.manager.Close(); err != nil {
		t.Fatal(err)
	}
	if err := h.run(ctx, "Hello again"); err != nil {
		t.Fatal(err)
	}
	resumed, _, _ := h.manager.bindings.find(ctx, h.chat.ID)
	if resumed.SessionID != binding.SessionID {
		t.Fatalf("session changed from %q to %q", binding.SessionID, resumed.SessionID)
	}
}

func (h *harness) interrupt(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.run(ctx, "Work slowly") }()
	time.Sleep(300 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("interrupted turn = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("interrupt did not end the turn")
	}
}

func TestManagerInterruptKeepsProcessThatStopped(t *testing.T) {
	h := newHarness(t, "hang")
	h.interrupt(t)
	process := h.process()
	if process == nil {
		t.Fatal("process that acknowledged the interrupt was discarded")
	}
	// The interrupted turn's result must not end the next turn early.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := h.run(ctx, "Now finish"); err != nil {
		t.Fatal(err)
	}
	if h.process() != process {
		t.Fatal("process restarted after a clean interrupt")
	}
}

func TestManagerInterruptDiscardsProcessThatKeepsRunning(t *testing.T) {
	h := newHarness(t, "stuck")
	h.manager.interruptTimeout = 200 * time.Millisecond
	h.interrupt(t)
	if h.process() != nil {
		t.Fatal("process that ignored the interrupt was kept")
	}
}

func TestManagerReportsFailedTurn(t *testing.T) {
	h := newHarness(t, "fail")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := h.run(ctx, "Hello")
	if err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("failed turn = %v", err)
	}
}

func TestManagerDeleteChatRemovesBindingAndConfiguration(t *testing.T) {
	h := newHarness(t, "turn")
	ctx := context.Background()
	if err := h.manager.bindings.put(ctx, Binding{ChatID: h.chat.ID, SessionID: "s"}); err != nil {
		t.Fatal(err)
	}
	if err := h.manager.DeleteChat(ctx, domain.Chat{ID: h.chat.ID, Backend: domain.ChatBackendCodex}); err != nil || len(h.factory.removed) != 0 {
		t.Fatalf("deleted a Codex chat: %v %v", err, h.factory.removed)
	}
	if err := h.manager.DeleteChat(ctx, h.chat); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := h.manager.bindings.find(ctx, h.chat.ID); ok || !slices.Equal(h.factory.removed, []domain.ID{h.chat.ID}) {
		t.Fatalf("binding kept %v, removed %v", ok, h.factory.removed)
	}
}

func TestManagerModelsReadsInitializeResponse(t *testing.T) {
	h := newHarness(t, "turn")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	models, err := h.manager.Models(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].Value != "default" || models[1].DisplayName != "Sonnet" || !slices.Equal(models[1].SupportedEffortLevels, []string{"low", "high"}) {
		t.Fatalf("models = %#v", models)
	}
}

func TestToolArgsAndEditDiff(t *testing.T) {
	input := map[string]any{"file_path": "/p/a.go", "old_string": "a\nb\n", "new_string": "c\n", "replace_all": false}
	args := toolArgs(domain.ToolKindFileEdit, "Edit", input, nil)
	if args["path"] != "/p/a.go" || args["replace_all"] != "false" {
		t.Fatalf("args = %#v", args)
	}
	if diff := editDiff("/p/a.go", input); diff != "--- /p/a.go\n+++ /p/a.go\n@@\n-a\n-b\n+c\n" {
		t.Fatalf("diff = %q", diff)
	}
	mcp := toolArgs(domain.ToolKindMCP, "mcp__github__search", nil, json.RawMessage(`{"q":"x"}`))
	if mcp["server"] != "github" || mcp["tool"] != "search" || mcp["arguments_raw"] != `{"q":"x"}` {
		t.Fatalf("mcp args = %#v", mcp)
	}
	if native := toolArgs(domain.ToolKindMCP, "TodoWrite", nil, json.RawMessage(`{}`)); native["server"] != "claude" || native["tool"] != "TodoWrite" {
		t.Fatalf("native args = %#v", native)
	}
	if toolKind("mcp__koder__chats") != domain.ToolKindChats || toolKind("mcp__other__chats") != domain.ToolKindMCP || toolKind("Read") != domain.ToolKindFileRead {
		t.Fatal("tool kinds are wrong")
	}
}

// TestClaudeDriverHelperProcess plays Claude Code's side of stream-json.
func TestClaudeDriverHelperProcess(t *testing.T) {
	mode := os.Getenv("KODER_CLAUDE_DRIVER_HELPER")
	if mode == "" {
		return
	}
	args := os.Args[slices.Index(os.Args, "--")+1:]
	sessionID := ""
	if index := slices.Index(args, "--session-id"); index >= 0 {
		sessionID = args[index+1]
	}
	if index := slices.Index(args, "--resume"); index >= 0 {
		sessionID = args[index+1]
		if sessionID == "lost-session" {
			fmt.Println("No conversation found with session ID: lost-session")
			os.Exit(0)
		}
	}
	lines := bufio.NewScanner(os.Stdin)
	lines.Buffer(make([]byte, 64*1024), 1<<20)
	enc := json.NewEncoder(os.Stdout)
	next := func() map[string]any {
		if !lines.Scan() {
			os.Exit(0)
		}
		var msg map[string]any
		if json.Unmarshal(lines.Bytes(), &msg) != nil {
			os.Exit(2)
		}
		return msg
	}
	nextRequest := 0
	// mcp sends a request to the SDK-hosted server and returns its result.
	mcp := func(method string, params any) map[string]any {
		nextRequest++
		requestID := fmt.Sprintf("cli-%d", nextRequest)
		_ = enc.Encode(map[string]any{"type": "control_request", "request_id": requestID, "request": map[string]any{
			"subtype": "mcp_message", "server_name": "koder",
			"message": map[string]any{"jsonrpc": "2.0", "id": nextRequest, "method": method, "params": params},
		}})
		reply := next()
		response, _ := reply["response"].(map[string]any)
		if reply["type"] != "control_response" || response["request_id"] != requestID {
			os.Exit(3)
		}
		body, _ := response["response"].(map[string]any)
		mcpResponse, _ := body["mcp_response"].(map[string]any)
		result, _ := mcpResponse["result"].(map[string]any)
		return result
	}
	emit := func(value map[string]any) {
		value["session_id"] = sessionID
		if _, ok := value["parent_tool_use_id"]; !ok {
			value["parent_tool_use_id"] = nil
		}
		_ = enc.Encode(value)
	}
	delta := func(kind, field, text string) {
		emit(map[string]any{"type": "stream_event", "event": map[string]any{"type": "content_block_delta", "delta": map[string]any{"type": kind, field: text}}})
	}
	assistant := func(blocks ...map[string]any) {
		emit(map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant", "content": blocks}})
	}
	toolResult := func(id string, content any, isError bool) {
		emit(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": []map[string]any{{"type": "tool_result", "tool_use_id": id, "content": content, "is_error": isError}}}})
	}
	turns := 0
	for {
		msg := next()
		request, _ := msg["request"].(map[string]any)
		switch {
		case msg["type"] == "control_request" && request["subtype"] == "initialize":
			if servers, _ := request["sdkMcpServers"].([]any); len(servers) == 1 {
				if result := mcp("initialize", map[string]any{"protocolVersion": "2025-11-25"}); result["protocolVersion"] != "2025-11-25" {
					os.Exit(4)
				}
				tools, _ := mcp("tools/list", map[string]any{})["tools"].([]any)
				if len(tools) != 1 {
					os.Exit(5)
				}
			}
			_ = enc.Encode(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": msg["request_id"], "response": map[string]any{
				"models": []map[string]any{
					{"value": "default", "displayName": "Default (recommended)"},
					{"value": "sonnet", "displayName": "Sonnet", "supportedEffortLevels": []string{"low", "high"}},
				},
			}}})
		case msg["type"] == "control_request" && request["subtype"] == "interrupt":
			if mode == "stuck" {
				continue
			}
			_ = enc.Encode(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": msg["request_id"], "response": map[string]any{}}})
			emit(map[string]any{"type": "result", "subtype": "error_during_execution", "is_error": true})
		case msg["type"] == "user":
			emit(map[string]any{"type": "system", "subtype": "init"})
			turns++
			switch {
			case (mode == "hang" && turns == 1) || mode == "stuck":
				delta("text_delta", "text", "Working")
				continue
			case mode == "fail":
				emit(map[string]any{"type": "result", "subtype": "error_during_execution", "is_error": true, "errors": []string{"rate limited"}})
				continue
			}
			delta("thinking_delta", "thinking", "Plan first.")
			delta("text_delta", "text", "Check")
			delta("text_delta", "text", "ing.")
			assistant(map[string]any{"type": "thinking", "thinking": "Plan first."})
			assistant(map[string]any{"type": "text", "text": "Checking."})
			// A subagent's output is not part of the chat.
			emit(map[string]any{"type": "assistant", "parent_tool_use_id": "toolu_task", "message": map[string]any{"content": []map[string]any{{"type": "text", "text": "subagent"}}}})
			assistant(map[string]any{"type": "tool_use", "id": "toolu_search", "name": "ToolSearch", "input": map[string]any{"query": "select:mcp__koder__milestone_list"}})
			toolResult("toolu_search", "loaded", false)
			assistant(map[string]any{"type": "tool_use", "id": "toolu_koder", "name": "mcp__koder__milestone_list", "input": map[string]any{"action": "list"}})
			result := mcp("tools/call", map[string]any{"name": "milestone_list", "arguments": map[string]any{"action": "list"}, "_meta": map[string]any{"claudecode/toolUseId": "toolu_koder"}})
			content, _ := result["content"].([]any)
			if len(content) != 1 || result["isError"] != false {
				os.Exit(6)
			}
			toolResult("toolu_koder", content, false)
			assistant(map[string]any{"type": "tool_use", "id": "toolu_bash", "name": "Bash", "input": map[string]any{"command": "echo hello"}})
			toolResult("toolu_bash", []map[string]any{{"type": "text", "text": "hello"}}, false)
			assistant(map[string]any{"type": "tool_use", "id": "toolu_edit", "name": "Edit", "input": map[string]any{"file_path": "/p/main.go", "old_string": "a", "new_string": "b"}})
			toolResult("toolu_edit", "old_string not found", true)
			delta("text_delta", "text", "Done.")
			assistant(map[string]any{"type": "text", "text": "Done."})
			emit(map[string]any{"type": "result", "subtype": "success", "is_error": false, "result": "Done."})
		}
	}
}
