package codexapp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestClientInitializesCallsAndReceivesNotifications(t *testing.T) {
	client := New(Config{
		Executable: os.Args[0],
		Args:       []string{"-test.run=TestCodexAppHelperProcess"},
		Env:        []string{"KODER_CODEX_HELPER=1"},
	})
	t.Cleanup(func() { _ = client.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	events, unsubscribe := client.Subscribe(4)
	defer unsubscribe()
	var result struct {
		Value string `json:"value"`
	}
	if err := client.Call(ctx, "test/call", map[string]string{"input": "hello"}, &result); err != nil {
		t.Fatal(err)
	}
	if result.Value != "ok" {
		t.Fatalf("result = %#v", result)
	}
	select {
	case event := <-events:
		if event.Method != "test/event" {
			t.Fatalf("event = %#v", event)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestClientReportsProcessExitToActiveSubscribers(t *testing.T) {
	client := New(Config{Executable: os.Args[0], Args: []string{"-test.run=TestCodexAppHelperProcess"}, Env: []string{"KODER_CODEX_HELPER=1"}})
	t.Cleanup(func() { _ = client.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	events, unsubscribe := client.Subscribe(4)
	defer unsubscribe()
	if err := client.Notify("test/crash", nil); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-events:
		if event.TransportErr == nil {
			t.Fatalf("event = %#v", event)
		}
	case <-ctx.Done():
		t.Fatal("process exit did not wake subscriber")
	}
}

func TestCodexAppHelperProcess(t *testing.T) {
	if os.Getenv("KODER_CODEX_HELPER") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var msg Message
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			os.Exit(2)
		}
		switch msg.Method {
		case "initialize":
			_ = enc.Encode(map[string]any{"id": json.RawMessage(msg.ID), "result": map[string]string{"userAgent": "fake"}})
		case "initialized":
		case "test/call":
			_ = enc.Encode(map[string]any{"method": "test/event", "params": map[string]string{"state": "working"}})
			_ = enc.Encode(map[string]any{"id": json.RawMessage(msg.ID), "result": map[string]string{"value": "ok"}})
		case "test/crash":
			os.Exit(3)
		default:
			_ = enc.Encode(map[string]any{"id": json.RawMessage(msg.ID), "error": RPCError{Code: -32601, Message: fmt.Sprintf("unknown method %s", msg.Method)}})
		}
	}
	os.Exit(0)
}
