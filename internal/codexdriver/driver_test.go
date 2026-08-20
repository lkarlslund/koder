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

func (b *fakeToolBridge) Call(_ context.Context, _ *chat.Chat, name, callID string, _ json.RawMessage) (string, error) {
	b.called = name == "milestone_list" && callID == "call-1"
	return "No milestones", nil
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
	if len(timeline) != 2 {
		t.Fatalf("timeline = %#v", timeline)
	}
	assistant, ok := timeline[1].Content.(domain.AssistantMessage)
	if !ok || assistant.Text != "Done." {
		t.Fatalf("assistant = %#v", timeline[1].Content)
	}
	binding, ok, err := manager.bindings.find(ctx, chatRecord.ID)
	if err != nil || !ok || binding.ThreadID != "thread-1" {
		t.Fatalf("binding = %#v, %v, %v", binding, ok, err)
	}
	if !bridge.called {
		t.Fatal("dynamic Koder tool was not executed")
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
			_ = enc.Encode(map[string]any{"id": json.RawMessage(msg.ID), "result": map[string]any{"thread": map[string]string{"id": "thread-1"}, "model": "fake"}})
		case "turn/start":
			_ = enc.Encode(map[string]any{"id": json.RawMessage(msg.ID), "result": map[string]any{"turn": map[string]string{"id": "turn-1"}}})
			_ = enc.Encode(map[string]any{"method": "item/started", "params": map[string]any{"threadId": "thread-1", "turnId": "turn-1", "item": map[string]any{"id": "call-1", "type": "dynamicToolCall", "tool": "milestone_list"}}})
			_ = enc.Encode(map[string]any{"id": 99, "method": "item/tool/call", "params": map[string]any{"threadId": "thread-1", "turnId": "turn-1", "callId": "call-1", "tool": "milestone_list", "arguments": map[string]any{}}})
		case "turn/interrupt":
			_ = enc.Encode(map[string]any{"id": json.RawMessage(msg.ID), "result": map[string]any{}})
		case "":
			if string(msg.ID) == "99" {
				_ = enc.Encode(map[string]any{"method": "item/completed", "params": map[string]any{"threadId": "thread-1", "turnId": "turn-1", "item": map[string]any{"id": "call-1", "type": "dynamicToolCall", "status": "completed"}}})
				_ = enc.Encode(map[string]any{"method": "item/agentMessage/delta", "params": map[string]string{"threadId": "thread-1", "turnId": "turn-1", "itemId": "message-1", "delta": "Done."}})
				_ = enc.Encode(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "thread-1", "turn": map[string]any{"id": "turn-1", "status": "completed", "items": []any{}}}})
			}
		}
	}
	os.Exit(0)
}
