package codexdriver

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/codexapp"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/store"
)

type fakeToolBridge struct{ called bool }

type fixedProcessFactory struct {
	cfg         codexapp.Config
	fingerprint string
	removed     []domain.ID
}

func (f *fixedProcessFactory) DiscoveryConfig(context.Context) (codexapp.Config, error) {
	return f.cfg, nil
}

func (f *fixedProcessFactory) ChatConfig(_ context.Context, _ domain.Session, chatRecord domain.Chat, _ string) (ChatProcessConfig, error) {
	fingerprint := f.fingerprint
	if fingerprint == "" {
		fingerprint = string(chatRecord.ID)
	}
	return ChatProcessConfig{Client: f.cfg, Fingerprint: fingerprint, Network: true}, nil
}

func (f *fixedProcessFactory) RemoveChat(chatID domain.ID) error {
	f.removed = append(f.removed, chatID)
	return nil
}

func (b *fakeToolBridge) Definitions(context.Context, *chat.Chat) ([]DynamicTool, error) {
	return []DynamicTool{{Type: "function", Name: "milestone_list", Description: "List milestones", InputSchema: json.RawMessage(`{"type":"object"}`)}}, nil
}

func (b *fakeToolBridge) Call(_ context.Context, _ *chat.Chat, name, callID string, _ json.RawMessage) (domain.ToolResult, error) {
	b.called = name == "milestone_list" && callID == "call-1"
	return domain.ToolResult{Text: "No milestones", Status: domain.ToolResultStatusOK}, nil
}

func TestManagerPersistsCodexTurnInKoderTimeline(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	factory := &fixedProcessFactory{cfg: codexapp.Config{
		Executable: os.Args[0],
		Args:       []string{"-test.run=TestCodexDriverHelperProcess"},
		Env:        []string{"KODER_CODEX_DRIVER_HELPER=1", "KODER_CODEX_DRIVER_MODEL=codex-test"},
	}}
	manager := New(factory, st, func(domain.Session, domain.Chat) string { return "Be concise." })
	bridge := &fakeToolBridge{}
	manager.SetToolBridge(bridge)
	t.Cleanup(func() { _ = manager.Close() })
	sessionRecord := domain.Session{ID: id.ID("session-1"), ProjectRoot: t.TempDir()}
	chatRecord := domain.Chat{ID: id.ID("chat-1"), SessionID: sessionRecord.ID, Backend: domain.ChatBackendCodex, InteractionMode: domain.InteractionModeText, ModelID: "codex-test"}
	source := chat.NewSource(func() chat.Deps { return chat.Deps{Store: st} })
	if err := source.PutRecord(context.Background(), chatRecord); err != nil {
		t.Fatal(err)
	}
	runtime, err := source.Load(context.Background(), sessionRecord, chatRecord)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runtime.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	events := make(chan domain.Event, 32)
	if err := manager.RunTurn(ctx, runtime, chat.DriverTurn{Input: domain.QueuedInput{ID: id.ID("input-1"), Text: "Implement it"}}, events); err != nil {
		t.Fatal(err)
	}
	timeline := runtime.SnapshotTimeline()
	if len(timeline) != 3 {
		t.Fatalf("timeline = %#v", timeline)
	}
	toolMessage, ok := timeline[1].Content.(domain.AssistantMessage)
	if !ok || len(toolMessage.Tools) != 1 || toolMessage.Tools[0].Tool != domain.ToolKindMilestoneList || toolMessage.Tools[0].Result == nil || toolMessage.Tools[0].Result.Text != "No milestones" {
		t.Fatalf("tool transcript = %#v", timeline[1].Content)
	}
	assistant, ok := timeline[2].Content.(domain.AssistantMessage)
	if !ok || assistant.Text != "Done." {
		t.Fatalf("assistant = %#v", timeline[2].Content)
	}
	binding, ok, err := manager.bindings.find(ctx, chatRecord.ID)
	if err != nil || !ok || binding.ThreadID != "thread-1" {
		t.Fatalf("binding = %#v, %v, %v", binding, ok, err)
	}
	if !bridge.called {
		t.Fatal("dynamic Koder tool was not executed")
	}
}

func TestManagerPreservesMessagesAroundCodexToolCall(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	factory := &fixedProcessFactory{cfg: codexapp.Config{
		Executable: os.Args[0],
		Args:       []string{"-test.run=TestCodexDriverHelperProcess"},
		Env:        []string{"KODER_CODEX_DRIVER_HELPER=1", "KODER_CODEX_DRIVER_MULTI_MESSAGE=1"},
	}}
	manager := New(factory, st, nil)
	manager.SetToolBridge(&fakeToolBridge{})
	t.Cleanup(func() { _ = manager.Close() })
	sessionRecord := domain.Session{ID: "session-messages", ProjectRoot: t.TempDir()}
	chatRecord := domain.Chat{ID: "chat-messages", SessionID: sessionRecord.ID, Backend: domain.ChatBackendCodex}
	source := chat.NewSource(func() chat.Deps { return chat.Deps{Store: st} })
	if err := source.PutRecord(context.Background(), chatRecord); err != nil {
		t.Fatal(err)
	}
	runtime, err := source.Load(context.Background(), sessionRecord, chatRecord)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtime.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := manager.RunTurn(ctx, runtime, chat.DriverTurn{Input: domain.QueuedInput{ID: "input-messages", Text: "Check it"}}, make(chan domain.Event, 32)); err != nil {
		t.Fatal(err)
	}
	timeline := runtime.SnapshotTimeline()
	if len(timeline) != 4 {
		t.Fatalf("timeline = %#v", timeline)
	}
	before, beforeOK := timeline[1].Content.(domain.AssistantMessage)
	tool, toolOK := timeline[2].Content.(domain.AssistantMessage)
	after, afterOK := timeline[3].Content.(domain.AssistantMessage)
	if !beforeOK || before.Text != "Let me check." || !toolOK || len(tool.Tools) != 1 || !afterOK || after.Text != "It worked." {
		t.Fatalf("message/tool ordering = %#v", timeline)
	}
	for index, item := range timeline {
		if item.Seq != int64(index+1) {
			t.Fatalf("timeline sequence at index %d = %d, want %d", index, item.Seq, index+1)
		}
	}
}

func TestExternalSandboxPolicyDescribesNetworkBoundary(t *testing.T) {
	if got := externalSandboxPolicy(true); got["type"] != "externalSandbox" || got["networkAccess"] != "enabled" {
		t.Fatalf("enabled policy = %#v", got)
	}
	if got := externalSandboxPolicy(false); got["networkAccess"] != "restricted" {
		t.Fatalf("locked-down policy = %#v", got)
	}
}

func TestManagerRunsOneCodexProcessPerChat(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	factory := &fixedProcessFactory{cfg: codexapp.Config{
		Executable: os.Args[0],
		Args:       []string{"-test.run=TestCodexDriverHelperProcess"},
		Env:        []string{"KODER_CODEX_DRIVER_HELPER=1"},
	}}
	manager := New(factory, st, nil)
	t.Cleanup(func() { _ = manager.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sessionRecord := domain.Session{ID: "session-processes", ProjectRoot: t.TempDir()}
	first, _, releaseFirst, err := manager.acquireProcess(ctx, sessionRecord, domain.Chat{ID: "chat-a"})
	if err != nil {
		t.Fatal(err)
	}
	defer releaseFirst()
	second, _, releaseSecond, err := manager.acquireProcess(ctx, sessionRecord, domain.Chat{ID: "chat-b"})
	if err != nil {
		t.Fatal(err)
	}
	defer releaseSecond()
	if first == second {
		t.Fatal("two Codex chats shared one app-server client")
	}
	manager.processMu.Lock()
	processCount := len(manager.processes)
	manager.processMu.Unlock()
	if processCount != 2 {
		t.Fatalf("process count = %d, want 2", processCount)
	}
}

func TestManagerRestartsChatProcessWhenPolicyFingerprintChanges(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	factory := &fixedProcessFactory{
		cfg: codexapp.Config{
			Executable: os.Args[0],
			Args:       []string{"-test.run=TestCodexDriverHelperProcess"},
			Env:        []string{"KODER_CODEX_DRIVER_HELPER=1"},
		},
		fingerprint: "policy-a",
	}
	manager := New(factory, st, nil)
	t.Cleanup(func() { _ = manager.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sessionRecord := domain.Session{ID: "session-policy-restart", ProjectRoot: t.TempDir()}
	chatRecord := domain.Chat{ID: "chat-policy-restart"}
	first, _, releaseFirst, err := manager.acquireProcess(ctx, sessionRecord, chatRecord)
	if err != nil {
		t.Fatal(err)
	}
	releaseFirst()
	factory.fingerprint = "policy-b"
	second, _, releaseSecond, err := manager.acquireProcess(ctx, sessionRecord, chatRecord)
	if err != nil {
		t.Fatal(err)
	}
	releaseSecond()
	if first == second {
		t.Fatal("policy change reused stale Codex app-server client")
	}
}

func TestManagerArchiveStopsRunningChatProcess(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	factory := &fixedProcessFactory{cfg: codexapp.Config{
		Executable: os.Args[0],
		Args:       []string{"-test.run=TestCodexDriverHelperProcess"},
		Env:        []string{"KODER_CODEX_DRIVER_HELPER=1"},
	}}
	manager := New(factory, st, nil)
	t.Cleanup(func() { _ = manager.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	chatRecord := domain.Chat{ID: "chat-archive-process", Backend: domain.ChatBackendCodex}
	if err := manager.bindings.put(ctx, Binding{ChatID: chatRecord.ID, ThreadID: "thread-archive-process"}); err != nil {
		t.Fatal(err)
	}
	_, _, release, err := manager.acquireProcess(ctx, domain.Session{ID: "session-archive-process", ProjectRoot: t.TempDir()}, chatRecord)
	if err != nil {
		t.Fatal(err)
	}
	release()
	archived := true
	if err := manager.UpdateChat(ctx, chatRecord, "", &archived); err != nil {
		t.Fatal(err)
	}
	manager.processMu.Lock()
	_, running := manager.processes[chatRecord.ID]
	manager.processMu.Unlock()
	if running {
		t.Fatal("archived chat retained its Codex app-server process")
	}
}

func TestCodexToolFailurePreservesOutputAndExitCode(t *testing.T) {
	exitCode := 128
	message := codexToolFailure(json.RawMessage(`{"message":"git failed"}`), "fatal: unable to access repository", &exitCode)
	for _, want := range []string{"git failed", "fatal: unable to access repository", "Exit code: 128"} {
		if !strings.Contains(message, want) {
			t.Fatalf("failure %q does not contain %q", message, want)
		}
	}
}

func TestTruncateCodexToolOutputPreservesHeadTailAndUTF8(t *testing.T) {
	input := "start-" + strings.Repeat("ø", maxCodexToolOutputBytes) + "-end"
	output, truncated := truncateCodexToolOutput(input)
	if !truncated || !strings.HasPrefix(output, "start-") || !strings.HasSuffix(output, "-end") || !strings.Contains(output, "output truncated") {
		t.Fatalf("truncated output shape = %q...%q", output[:20], output[len(output)-20:])
	}
	if !utf8.ValidString(output) || len(output) > maxCodexToolOutputBytes {
		t.Fatalf("truncated output bytes = %d, valid utf8 = %v", len(output), utf8.ValidString(output))
	}
}

func TestManagerMirrorsCodexChatLifecycle(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	factory := &fixedProcessFactory{cfg: codexapp.Config{Executable: os.Args[0], Args: []string{"-test.run=TestCodexDriverHelperProcess"}, Env: []string{"KODER_CODEX_DRIVER_HELPER=1"}}}
	manager := New(factory, st, nil)
	t.Cleanup(func() { _ = manager.Close() })
	chatRecord := domain.Chat{ID: "chat-lifecycle", Title: "Before", Backend: domain.ChatBackendCodex}
	if err := manager.bindings.put(context.Background(), Binding{ChatID: chatRecord.ID, ThreadID: "thread-lifecycle"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	archived := true
	if err := manager.UpdateChat(ctx, chatRecord, "After", &archived); err != nil {
		t.Fatal(err)
	}
	chatRecord.Title, chatRecord.Archived = "After", true
	archived = false
	if err := manager.UpdateChat(ctx, chatRecord, "", &archived); err != nil {
		t.Fatal(err)
	}
	if err := manager.DeleteChat(ctx, chatRecord); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := manager.bindings.find(ctx, chatRecord.ID); err != nil || ok {
		t.Fatalf("binding survived delete: ok=%v err=%v", ok, err)
	}
	if len(factory.removed) != 1 || factory.removed[0] != chatRecord.ID {
		t.Fatalf("removed homes = %#v", factory.removed)
	}
}

func TestManagerRejectsUnexpectedCodexApproval(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	factory := &fixedProcessFactory{cfg: codexapp.Config{Executable: os.Args[0], Args: []string{"-test.run=TestCodexDriverHelperProcess"}, Env: []string{"KODER_CODEX_DRIVER_HELPER=1", "KODER_CODEX_DRIVER_APPROVAL=1"}}}
	manager := New(factory, st, nil)
	t.Cleanup(func() { _ = manager.Close() })
	sessionRecord := domain.Session{ID: "session-approval", ProjectRoot: t.TempDir(), PermissionProfile: "default"}
	chatRecord := domain.Chat{ID: "chat-approval", SessionID: sessionRecord.ID, Title: "Approval", Backend: domain.ChatBackendCodex}
	source := chat.NewSource(func() chat.Deps { return chat.Deps{Store: st} })
	if err := source.PutRecord(context.Background(), chatRecord); err != nil {
		t.Fatal(err)
	}
	runtime, err := source.Load(context.Background(), sessionRecord, chatRecord)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtime.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = manager.RunTurn(ctx, runtime, chat.DriverTurn{Input: domain.QueuedInput{ID: "input-approval", Text: "Run it"}}, make(chan domain.Event, 32))
	if err == nil || !strings.Contains(err.Error(), "requested interactive approval") {
		t.Fatalf("approval error = %v", err)
	}
	approvals, pendingErr := runtime.PendingApprovals(ctx)
	if pendingErr != nil {
		t.Fatal(pendingErr)
	}
	if len(approvals) != 0 {
		t.Fatalf("unexpected Koder approval records: %#v", approvals)
	}
}

func TestCodexDriverHelperProcess(t *testing.T) {
	if os.Getenv("KODER_CODEX_DRIVER_HELPER") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var msg codexapp.Message
		if json.Unmarshal(scanner.Bytes(), &msg) != nil {
			os.Exit(2)
		}
		switch msg.Method {
		case "initialize":
			_ = enc.Encode(map[string]any{"id": json.RawMessage(msg.ID), "result": map[string]string{"userAgent": "fake"}})
		case "initialized":
		case "thread/start":
			var params map[string]any
			if json.Unmarshal(msg.Params, &params) != nil || params["sandbox"] != "danger-full-access" || params["approvalPolicy"] != "never" {
				os.Exit(4)
			}
			if expected := os.Getenv("KODER_CODEX_DRIVER_MODEL"); expected != "" && params["model"] != expected {
				os.Exit(6)
			}
			responseModel := "fake"
			if expected := os.Getenv("KODER_CODEX_DRIVER_MODEL"); expected != "" {
				responseModel = expected
			}
			_ = enc.Encode(map[string]any{"id": json.RawMessage(msg.ID), "result": map[string]any{"thread": map[string]string{"id": "thread-1"}, "model": responseModel}})
		case "turn/start":
			var params map[string]any
			if json.Unmarshal(msg.Params, &params) != nil || params["approvalPolicy"] != "never" {
				os.Exit(4)
			}
			sandboxPolicy, _ := params["sandboxPolicy"].(map[string]any)
			if sandboxPolicy["type"] != "externalSandbox" {
				os.Exit(4)
			}
			if expected := os.Getenv("KODER_CODEX_DRIVER_MODEL"); expected != "" && params["model"] != expected {
				os.Exit(6)
			}
			_ = enc.Encode(map[string]any{"id": json.RawMessage(msg.ID), "result": map[string]any{"turn": map[string]string{"id": "turn-1"}}})
			if os.Getenv("KODER_CODEX_DRIVER_APPROVAL") == "1" {
				_ = enc.Encode(map[string]any{"method": "item/started", "params": map[string]any{"threadId": "thread-1", "turnId": "turn-1", "item": map[string]any{"id": "command-1", "type": "commandExecution", "command": "touch marker", "cwd": "/workspace", "status": "inProgress"}}})
				_ = enc.Encode(map[string]any{"id": 77, "method": "item/commandExecution/requestApproval", "params": map[string]any{"threadId": "thread-1", "turnId": "turn-1", "itemId": "command-1", "reason": "Create marker", "command": "touch marker"}})
				continue
			}
			if os.Getenv("KODER_CODEX_DRIVER_MULTI_MESSAGE") == "1" {
				_ = enc.Encode(map[string]any{"method": "item/started", "params": map[string]any{"threadId": "thread-1", "turnId": "turn-1", "item": map[string]any{"id": "message-before", "type": "agentMessage", "text": ""}}})
				_ = enc.Encode(map[string]any{"method": "item/agentMessage/delta", "params": map[string]string{"threadId": "thread-1", "turnId": "turn-1", "itemId": "message-before", "delta": "Let me check."}})
				_ = enc.Encode(map[string]any{"method": "item/completed", "params": map[string]any{"threadId": "thread-1", "turnId": "turn-1", "item": map[string]any{"id": "message-before", "type": "agentMessage", "text": "Let me check."}}})
			}
			_ = enc.Encode(map[string]any{"method": "item/started", "params": map[string]any{"threadId": "thread-1", "turnId": "turn-1", "item": map[string]any{"id": "call-1", "type": "dynamicToolCall", "tool": "milestone_list"}}})
			_ = enc.Encode(map[string]any{"id": 99, "method": "item/tool/call", "params": map[string]any{"threadId": "thread-1", "turnId": "turn-1", "callId": "call-1", "tool": "milestone_list", "arguments": map[string]any{}}})
		case "turn/interrupt":
			_ = enc.Encode(map[string]any{"id": json.RawMessage(msg.ID), "result": map[string]any{}})
		case "thread/name/set", "thread/archive", "thread/unarchive", "thread/delete":
			var params map[string]string
			if json.Unmarshal(msg.Params, &params) != nil || params["threadId"] == "" || (msg.Method == "thread/name/set" && params["name"] == "") {
				_ = enc.Encode(map[string]any{"id": json.RawMessage(msg.ID), "error": map[string]any{"code": -32602, "message": "invalid lifecycle params"}})
				continue
			}
			_ = enc.Encode(map[string]any{"id": json.RawMessage(msg.ID), "result": map[string]any{}})
		case "":
			if string(msg.ID) == "77" {
				var result struct {
					Decision string `json:"decision"`
				}
				if json.Unmarshal(msg.Result, &result) != nil || result.Decision != "decline" {
					os.Exit(5)
				}
				continue
			}
			if string(msg.ID) == "99" {
				_ = enc.Encode(map[string]any{"method": "item/completed", "params": map[string]any{"threadId": "thread-1", "turnId": "turn-1", "item": map[string]any{"id": "call-1", "type": "dynamicToolCall", "status": "completed"}}})
				messageText := "Done."
				messageID := "message-1"
				if os.Getenv("KODER_CODEX_DRIVER_MULTI_MESSAGE") == "1" {
					messageText = "It worked."
					messageID = "message-after"
					_ = enc.Encode(map[string]any{"method": "item/started", "params": map[string]any{"threadId": "thread-1", "turnId": "turn-1", "item": map[string]any{"id": messageID, "type": "agentMessage", "text": ""}}})
				}
				_ = enc.Encode(map[string]any{"method": "item/agentMessage/delta", "params": map[string]string{"threadId": "thread-1", "turnId": "turn-1", "itemId": messageID, "delta": messageText}})
				if os.Getenv("KODER_CODEX_DRIVER_MULTI_MESSAGE") == "1" {
					_ = enc.Encode(map[string]any{"method": "item/completed", "params": map[string]any{"threadId": "thread-1", "turnId": "turn-1", "item": map[string]any{"id": messageID, "type": "agentMessage", "text": messageText}}})
				}
				_ = enc.Encode(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "thread-1", "turn": map[string]any{"id": "turn-1", "status": "completed", "items": []any{}}}})
			}
		}
	}
	os.Exit(0)
}
