package execruntime

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestManagerStartStatusAndWriteStdin(t *testing.T) {
	mgr := NewManager()
	snap, err := mgr.Start(context.Background(), StartRequest{
		SessionID: "session-1",
		ChatID:    "chat-2",
		Command:   "read line; printf 'got:%s' \"$line\"",
		Workdir:   t.TempDir(),
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

func TestManagerWriteStdinEmptyWaitsAndDrainsNewOutput(t *testing.T) {
	mgr := NewManager()
	snap, err := mgr.Start(context.Background(), StartRequest{
		SessionID: "session-1",
		ChatID:    "chat-2",
		Command:   "printf first; sleep 0.2; printf second; sleep 0.2",
		Workdir:   t.TempDir(),
		YieldTime: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if !strings.Contains(snap.Output, "first") {
		t.Fatalf("expected initial drain to contain first output, got %q", snap.Output)
	}
	if strings.Contains(snap.Output, "second") {
		t.Fatalf("expected initial drain to omit later output, got %q", snap.Output)
	}

	waited, err := mgr.WriteStdin(context.Background(), WriteStdinRequest{
		SessionID: "session-1",
		ChatID:    "chat-2",
		ProcessID: snap.ProcessID,
		YieldTime: time.Second,
		MaxBytes:  1024,
	})
	if err != nil {
		t.Fatalf("wait stdin: %v", err)
	}
	if !waited.Drained {
		t.Fatal("expected write stdin wait to return drained output")
	}
	if !strings.Contains(waited.Output, "second") {
		t.Fatalf("expected wait drain to contain second output, got %q", waited.Output)
	}
	if strings.Contains(waited.Output, "first") {
		t.Fatalf("expected wait drain not to repeat first output, got %q", waited.Output)
	}

	again, err := mgr.WriteStdin(context.Background(), WriteStdinRequest{
		SessionID: "session-1",
		ChatID:    "chat-2",
		ProcessID: snap.ProcessID,
		YieldTime: 50 * time.Millisecond,
		MaxBytes:  1024,
	})
	if err != nil {
		t.Fatalf("second wait stdin: %v", err)
	}
	if strings.Contains(again.Output, "first") || strings.Contains(again.Output, "second") {
		t.Fatalf("expected second wait not to repeat drained output, got %q", again.Output)
	}

	status, err := mgr.Status(context.Background(), StatusRequest{
		SessionID: "session-1",
		ChatID:    "chat-2",
		ProcessID: snap.ProcessID,
		MaxBytes:  1024,
	})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(status.Output, "first") || !strings.Contains(status.Output, "second") {
		t.Fatalf("expected status tail to retain full output, got %q", status.Output)
	}
	if status.Drained {
		t.Fatal("expected status snapshot not to be marked drained")
	}
}

func TestManagerExecPreviewUsesSixteenKiBAndLineBoundary(t *testing.T) {
	var output strings.Builder
	for i := 1; i <= 1200; i++ {
		output.WriteString(fmt.Sprintf("line-%04d %s\n", i, strings.Repeat("x", 32)))
	}
	p := &process{output: output.String()}
	snap := p.snapshot(0)
	if len(snap.Output) > defaultPreviewBytes {
		t.Fatalf("expected output <= %d bytes, got %d", defaultPreviewBytes, len(snap.Output))
	}
	if strings.HasPrefix(snap.Output, "bcdef") || strings.HasPrefix(snap.Output, "ollection") {
		t.Fatalf("expected output to start at a line boundary, got %q", snap.Output[:min(len(snap.Output), 32)])
	}
	if !strings.HasPrefix(snap.Output, "line-") {
		t.Fatalf("expected output to start with a full line, got %q", snap.Output[:min(len(snap.Output), 32)])
	}
	if !strings.Contains(snap.Output, "line-1200") {
		t.Fatalf("expected preview to keep tail output, got suffix %q", snap.Output[max(0, len(snap.Output)-64):])
	}
}

func TestTailOnLineBoundaryDropsPartialAndOversizedSingleLine(t *testing.T) {
	got := tailOnLineBoundary("first\nsecond\nthird\n", 13)
	if got != "second\nthird\n" {
		t.Fatalf("unexpected line-boundary tail: %q", got)
	}
	if got := tailOnLineBoundary(strings.Repeat("x", 20), 10); got != "" {
		t.Fatalf("expected oversized single line to be dropped, got %q", got)
	}
}

func TestManagerListAndTerminate(t *testing.T) {
	mgr := NewManager()
	snap, err := mgr.Start(context.Background(), StartRequest{
		SessionID: "session-1",
		ChatID:    "chat-2",
		Command:   "sleep 10",
		Workdir:   t.TempDir(),
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
		Workdir:   t.TempDir(),
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

func TestManagerSubscribeCancelIsIdempotent(t *testing.T) {
	mgr := NewManager()
	events, cancel := mgr.Subscribe("chat-2")
	cancel()
	cancel()
	if _, ok := <-events; ok {
		t.Fatal("expected subscription channel to close")
	}
}

func TestManagerSubscribeCancelIsConcurrentSafe(t *testing.T) {
	mgr := NewManager()
	_, cancel := mgr.Subscribe("chat-2")
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cancel()
		}()
	}
	wg.Wait()
}
