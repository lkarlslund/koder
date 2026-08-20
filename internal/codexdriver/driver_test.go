package codexdriver

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/codexapp"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/store"
)

type fakeToolBridge struct{ called bool }

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
	client := codexapp.New(codexapp.Config{
		Executable: os.Args[0],
		Args:       []string{"-test.run=TestCodexDriverHelperProcess"},
		Env:        []string{"KODER_CODEX_DRIVER_HELPER=1"},
	})
	manager := New(client, st, func(domain.Session, domain.Chat) string { return "Be concise." })
	bridge := &fakeToolBridge{}
	manager.SetToolBridge(bridge)
	t.Cleanup(func() { _ = manager.Close() })
	sessionRecord := domain.Session{ID: id.ID("session-1"), ProjectRoot: t.TempDir()}
	chatRecord := domain.Chat{ID: id.ID("chat-1"), SessionID: sessionRecord.ID, Backend: domain.ChatBackendCodex, InteractionMode: domain.InteractionModeText}
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

func TestCodexSandboxUsesAppServerProtocolValues(t *testing.T) {
	tests := []struct {
		profile string
		want    string
	}{
		{profile: "readonly", want: "read-only"},
		{profile: "default", want: "workspace-write"},
		{profile: "full-access", want: "danger-full-access"},
	}
	for _, test := range tests {
		chatRecord := domain.Chat{PermissionProfile: test.profile}
		if got := codexSandbox(domain.Session{}, chatRecord); got != test.want {
			t.Fatalf("profile %q sandbox = %q, want %q", test.profile, got, test.want)
		}
	}
}

func TestManagerMirrorsCodexChatLifecycle(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	client := codexapp.New(codexapp.Config{Executable: os.Args[0], Args: []string{"-test.run=TestCodexDriverHelperProcess"}, Env: []string{"KODER_CODEX_DRIVER_HELPER=1"}})
	manager := New(client, st, nil)
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
}

func TestManagerBridgesCodexApprovalIntoKoderTimeline(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	client := codexapp.New(codexapp.Config{Executable: os.Args[0], Args: []string{"-test.run=TestCodexDriverHelperProcess"}, Env: []string{"KODER_CODEX_DRIVER_HELPER=1", "KODER_CODEX_DRIVER_APPROVAL=1"}})
	manager := New(client, st, nil)
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
	done := make(chan error, 1)
	go func() {
		done <- manager.RunTurn(ctx, runtime, chat.DriverTurn{Input: domain.QueuedInput{ID: "input-approval", Text: "Run it"}}, make(chan domain.Event, 32))
	}()
	for {
		approvals, pendingErr := runtime.PendingApprovals(ctx)
		if pendingErr != nil {
			t.Fatal(pendingErr)
		}
		if len(approvals) == 1 {
			if handled, resolveErr := manager.ResolveTurnApproval(ctx, runtime, approvals[0].ToolCallID, true, nil); resolveErr != nil || !handled {
				t.Fatalf("resolve approval: handled=%v err=%v", handled, resolveErr)
			}
			break
		}
		select {
		case runErr := <-done:
			t.Fatalf("turn ended before approval: %v", runErr)
		case <-ctx.Done():
			t.Fatal("approval was not published")
		case <-time.After(5 * time.Millisecond):
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	timeline := runtime.SnapshotTimeline()
	toolMessage, ok := timeline[1].Content.(domain.AssistantMessage)
	if !ok || len(toolMessage.Tools) != 1 || toolMessage.Tools[0].Tool != domain.ToolKindExecCommand || toolMessage.Tools[0].Status != domain.ToolStatusDone {
		t.Fatalf("approval tool transcript = %#v", timeline)
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
			if json.Unmarshal(msg.Params, &params) != nil || params["sandbox"] != "workspace-write" {
				os.Exit(4)
			}
			if os.Getenv("KODER_CODEX_DRIVER_APPROVAL") == "1" {
				if json.Unmarshal(msg.Params, &params) != nil || params["approvalPolicy"] != "on-request" {
					os.Exit(4)
				}
			}
			_ = enc.Encode(map[string]any{"id": json.RawMessage(msg.ID), "result": map[string]any{"thread": map[string]string{"id": "thread-1"}, "model": "fake"}})
		case "turn/start":
			_ = enc.Encode(map[string]any{"id": json.RawMessage(msg.ID), "result": map[string]any{"turn": map[string]string{"id": "turn-1"}}})
			if os.Getenv("KODER_CODEX_DRIVER_APPROVAL") == "1" {
				_ = enc.Encode(map[string]any{"method": "item/started", "params": map[string]any{"threadId": "thread-1", "turnId": "turn-1", "item": map[string]any{"id": "command-1", "type": "commandExecution", "command": "touch marker", "cwd": "/workspace", "status": "inProgress"}}})
				_ = enc.Encode(map[string]any{"id": 77, "method": "item/commandExecution/requestApproval", "params": map[string]any{"threadId": "thread-1", "turnId": "turn-1", "itemId": "command-1", "reason": "Create marker", "command": "touch marker"}})
				continue
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
				if json.Unmarshal(msg.Result, &result) != nil || result.Decision != "accept" {
					os.Exit(5)
				}
				_ = enc.Encode(map[string]any{"method": "item/completed", "params": map[string]any{"threadId": "thread-1", "turnId": "turn-1", "item": map[string]any{"id": "command-1", "type": "commandExecution", "status": "completed", "aggregatedOutput": "created", "exitCode": 0}}})
				_ = enc.Encode(map[string]any{"method": "item/agentMessage/delta", "params": map[string]string{"threadId": "thread-1", "turnId": "turn-1", "itemId": "message-1", "delta": "Done."}})
				_ = enc.Encode(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "thread-1", "turn": map[string]any{"id": "turn-1", "status": "completed"}}})
				continue
			}
			if string(msg.ID) == "99" {
				_ = enc.Encode(map[string]any{"method": "item/completed", "params": map[string]any{"threadId": "thread-1", "turnId": "turn-1", "item": map[string]any{"id": "call-1", "type": "dynamicToolCall", "status": "completed"}}})
				_ = enc.Encode(map[string]any{"method": "item/agentMessage/delta", "params": map[string]string{"threadId": "thread-1", "turnId": "turn-1", "itemId": "message-1", "delta": "Done."}})
				_ = enc.Encode(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "thread-1", "turn": map[string]any{"id": "turn-1", "status": "completed", "items": []any{}}}})
			}
		}
	}
	os.Exit(0)
}
