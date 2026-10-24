package execruntime

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

type notificationRecorder struct {
	mu    sync.Mutex
	items []Notification
	added chan struct{}
}

func newNotificationRecorder(mgr *Manager) *notificationRecorder {
	r := &notificationRecorder{added: make(chan struct{}, 64)}
	mgr.SetNotifier(func(n Notification) {
		r.mu.Lock()
		r.items = append(r.items, n)
		r.mu.Unlock()
		r.added <- struct{}{}
	})
	return r
}

func (r *notificationRecorder) wait(t *testing.T, count int) []Notification {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		r.mu.Lock()
		if len(r.items) >= count {
			items := append([]Notification(nil), r.items...)
			r.mu.Unlock()
			return items
		}
		r.mu.Unlock()
		select {
		case <-r.added:
		case <-deadline:
			t.Fatalf("timed out waiting for %d notifications", count)
		}
	}
}

func (r *notificationRecorder) snapshot() []Notification {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Notification(nil), r.items...)
}

func startTestProcess(t *testing.T, mgr *Manager, req StartRequest) Snapshot {
	t.Helper()
	req.SessionID, req.ChatID, req.Workdir = "session-1", "chat-1", t.TempDir()
	if req.YieldTime == 0 {
		// Commands that should still be running print a line at once, so the
		// startup wait ends when output settles, well before this.
		req.YieldTime = 5 * time.Second
	}
	snap, err := mgr.Start(context.Background(), req)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	return snap
}

func waitDone(t *testing.T, mgr *Manager, processID string) {
	t.Helper()
	p, err := mgr.lookup("session-1", "chat-1", processID)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.done:
	case <-time.After(10 * time.Second):
		t.Fatal("process did not exit")
	}
}

func TestExitIsReportedOnceAfterExecCommandReturnedRunning(t *testing.T) {
	mgr := NewManager()
	rec := newNotificationRecorder(mgr)
	snap := startTestProcess(t, mgr, StartRequest{Command: "echo started; sleep 1; echo built; exit 3", Label: "build the app"})
	if snap.State != StateRunning {
		t.Fatalf("state = %s, want running", snap.State)
	}
	items := rec.wait(t, 1)
	n := items[0]
	if n.Kind != NotificationExit || n.ProcessID != snap.ProcessID || n.ChatID != "chat-1" {
		t.Fatalf("notification = %+v", n)
	}
	for _, want := range []string{"event: exec session ended", "process_id: " + snap.ProcessID, "description: build the app", "state: failed", "exit_code: 3", "output:\nbuilt"} {
		if !strings.Contains(n.Text, want) {
			t.Fatalf("notification text missing %q:\n%s", want, n.Text)
		}
	}
	time.Sleep(200 * time.Millisecond)
	if got := len(rec.snapshot()); got != 1 {
		t.Fatalf("notifications = %d, want 1", got)
	}
	late, err := mgr.WriteStdin(context.Background(), WriteStdinRequest{SessionID: "session-1", ChatID: "chat-1", ProcessID: snap.ProcessID})
	if err != nil {
		t.Fatal(err)
	}
	if !late.ExitReported || late.Output != "" {
		t.Fatalf("wait after the report: reported = %v, output = %q", late.ExitReported, late.Output)
	}
}

func TestExitShownByToolResultIsNotReported(t *testing.T) {
	mgr := NewManager()
	rec := newNotificationRecorder(mgr)
	quick := startTestProcess(t, mgr, StartRequest{Command: "echo done"})
	if quick.State == StateRunning {
		t.Fatalf("quick command still running")
	}
	waited := startTestProcess(t, mgr, StartRequest{Command: "echo started; sleep 0.5; echo later"})
	snap, err := mgr.WriteStdin(context.Background(), WriteStdinRequest{SessionID: "session-1", ChatID: "chat-1", ProcessID: waited.ProcessID, YieldTime: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	for snap.State == StateRunning {
		snap, err = mgr.WriteStdin(context.Background(), WriteStdinRequest{SessionID: "session-1", ChatID: "chat-1", ProcessID: waited.ProcessID, YieldTime: 5 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
	}
	stopped := startTestProcess(t, mgr, StartRequest{Command: "echo started; sleep 30"})
	if _, err := mgr.Terminate(context.Background(), TerminateRequest{SessionID: "session-1", ChatID: "chat-1", ProcessID: stopped.ProcessID}); err != nil {
		t.Fatal(err)
	}
	waitDone(t, mgr, stopped.ProcessID)
	time.Sleep(200 * time.Millisecond)
	if items := rec.snapshot(); len(items) != 0 {
		t.Fatalf("unexpected notifications: %+v", items)
	}
}

func TestTimeoutIsReportedWithReason(t *testing.T) {
	mgr := NewManager()
	rec := newNotificationRecorder(mgr)
	startTestProcess(t, mgr, StartRequest{Command: "echo started; sleep 30", Timeout: 2 * time.Second})
	n := rec.wait(t, 1)[0]
	if !strings.Contains(n.Text, "state: terminated (timed out after 2s)") {
		t.Fatalf("notification text:\n%s", n.Text)
	}
}

func TestLinesModeReportsBatchesThenExit(t *testing.T) {
	mgr := NewManager()
	rec := newNotificationRecorder(mgr)
	snap := startTestProcess(t, mgr, StartRequest{
		Command: "echo startup; sleep 1; echo first; echo second; sleep 1; printf tail",
		Notify:  NotifyLines,
	})
	if !strings.Contains(snap.Output, "startup") {
		t.Fatalf("startup output = %q", snap.Output)
	}
	items := rec.wait(t, 2)
	if items[0].Kind != NotificationLines || !strings.HasSuffix(items[0].Text, "output:\nfirst\nsecond") {
		t.Fatalf("first notification = %+v", items[0])
	}
	exit := items[1]
	if exit.Kind != NotificationExit || !strings.Contains(exit.Text, "events_delivered: 1") || !strings.HasSuffix(exit.Text, "output:\ntail") {
		t.Fatalf("exit notification = %+v", exit)
	}
	if strings.Contains(exit.Text, "output:\nstartup") {
		t.Fatalf("exit report repeats output from the exec_command result:\n%s", exit.Text)
	}
}

func TestLinesModeExpires(t *testing.T) {
	mgr := NewManager()
	rec := newNotificationRecorder(mgr)
	startTestProcess(t, mgr, StartRequest{Command: "echo started; sleep 30", Notify: NotifyLines, Timeout: 2 * time.Second})
	n := rec.wait(t, 1)[0]
	if !strings.Contains(n.Text, "state: terminated (watch expired after 2s)") || !strings.Contains(n.Text, "events_delivered: 0") {
		t.Fatalf("notification text:\n%s", n.Text)
	}
}

func TestLineMonitorRateLimitsAndStopsAFlood(t *testing.T) {
	now := time.Unix(0, 0)
	var delivered []string
	var suppressedBefore []int
	flooded := 0
	l := newLineMonitor(func(lines string, before int) {
		delivered = append(delivered, lines)
		suppressedBefore = append(suppressedBefore, before)
	}, func() { flooded++ })
	l.now = func() time.Time { return now }
	l.schedule = func(time.Duration, func()) func() { return func() {} }
	send := func(line string) {
		l.feed(line + "\n")
		l.flush()
	}
	for i := range monitorBucketSize {
		send("line " + string(rune('a'+i)))
	}
	send("dropped")
	if len(delivered) != monitorBucketSize {
		t.Fatalf("delivered %d batches, want %d", len(delivered), monitorBucketSize)
	}
	now = now.Add(monitorRefill)
	send("after refill")
	if last := len(delivered) - 1; delivered[last] != "after refill" || suppressedBefore[last] != 1 {
		t.Fatalf("last delivery = %q, suppressed before = %d", delivered[last], suppressedBefore[last])
	}
	for range 40 {
		now = now.Add(time.Second)
		for range 5 {
			send("flood")
		}
	}
	if flooded != 1 {
		t.Fatalf("flood callbacks = %d, want 1", flooded)
	}
	long := strings.Repeat("x", monitorLineBytes+50)
	l.feed(long)
	if final := l.close(); final != long[:monitorLineBytes]+monitorTruncSuffix {
		t.Fatalf("final line = %q", final)
	}
}

func TestExitReportsPendingSaysWhetherAReportWasSent(t *testing.T) {
	mgr := NewManager()
	rec := newNotificationRecorder(mgr)
	startTestProcess(t, mgr, StartRequest{Command: "echo done"})
	if mgr.ExitReportsPending("session-1", "chat-1") != nil {
		t.Fatal("pending reports after a command whose exit was shown")
	}
	startTestProcess(t, mgr, StartRequest{Command: "echo started; sleep 1"})
	pending := mgr.ExitReportsPending("session-1", "chat-1")
	if pending == nil {
		t.Fatal("no pending report for a running command")
	}
	select {
	case reported := <-pending:
		if !reported || len(rec.snapshot()) != 1 {
			t.Fatalf("reported = %v, notifications = %d", reported, len(rec.snapshot()))
		}
	case <-time.After(10 * time.Second):
		t.Fatal("pending reports did not settle")
	}

	stopped := startTestProcess(t, mgr, StartRequest{Command: "echo started; sleep 30"})
	pending = mgr.ExitReportsPending("session-1", "chat-1")
	if pending == nil {
		t.Fatal("no pending report for a running command")
	}
	if _, err := mgr.Terminate(context.Background(), TerminateRequest{SessionID: "session-1", ChatID: "chat-1", ProcessID: stopped.ProcessID}); err != nil {
		t.Fatal(err)
	}
	select {
	case reported := <-pending:
		if reported {
			t.Fatal("a stopped command counted as reported")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("pending reports did not settle")
	}
}
