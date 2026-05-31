package execruntime

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestManagerStartStatusAndWriteStdin(t *testing.T) {
	mgr := NewManager()
	snap, err := mgr.Start(context.Background(), StartRequest{
		SessionID: "session-1",
		ChatID:    "chat-2",
		Command:   "read line; printf 'got:%s' \"$line\"",
		YieldTime: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if snap.ProcessID == "" {
		t.Fatal("expected process id")
	}
	snap, err = mgr.WriteStdin(context.Background(), WriteStdinRequest{
		SessionID:  "session-1",
		ChatID:     "chat-2",
		ProcessID:  snap.ProcessID,
		Chars:      "hello\n",
		CloseStdin: true,
		MaxBytes:   1024,
	})
	if err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	status, err := mgr.Status(context.Background(), StatusRequest{
		SessionID: "session-1",
		ChatID:    "chat-2",
		ProcessID: snap.ProcessID,
		MaxBytes:  1024,
	})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(status.Output, "got:hello") {
		t.Fatalf("expected output tail to contain stdin response, got %q", status.Output)
	}
}

func TestManagerListAndTerminate(t *testing.T) {
	mgr := NewManager()
	snap, err := mgr.Start(context.Background(), StartRequest{
		SessionID: "session-1",
		ChatID:    "chat-2",
		Command:   "sleep 10",
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	list, err := mgr.List(context.Background(), ListRequest{
		SessionID: "session-1",
		ChatID:    "chat-2",
		Scope:     ScopeChat,
		MaxBytes:  256,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].ProcessID != snap.ProcessID {
		t.Fatalf("unexpected list result: %#v", list)
	}
	terminated, err := mgr.Terminate(context.Background(), TerminateRequest{
		SessionID: "session-1",
		ChatID:    "chat-2",
		ProcessID: snap.ProcessID,
	})
	if err != nil {
		t.Fatalf("terminate: %v", err)
	}
	if terminated.State != StateTerminated {
		t.Fatalf("expected terminated state, got %s", terminated.State)
	}
}

func TestManagerSubscribeReceivesOutput(t *testing.T) {
	mgr := NewManager()
	events, cancel := mgr.Subscribe("chat-2")
	defer cancel()
	_, err := mgr.Start(context.Background(), StartRequest{
		SessionID: "session-1",
		ChatID:    "chat-2",
		Command:   "printf hi",
		YieldTime: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	timeout := time.After(2 * time.Second)
	for {
		select {
		case evt := <-events:
			if evt.Kind == EventKindOutput && strings.Contains(evt.Delta, "hi") {
				return
			}
		case <-timeout:
			t.Fatal("timed out waiting for output event")
		}
	}
}
