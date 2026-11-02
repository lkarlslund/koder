package execruntime

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRestartJournalTracksRunningJobsAndConfirmsShutdown(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state", "running-execs.json")
	manager := NewManager()
	manager.SetRestartJournal(path)
	t.Cleanup(func() { _ = manager.Shutdown(ctx) })
	snap := startTestProcess(t, manager, StartRequest{Command: "read value", Label: "Wait for input", YieldTime: time.Millisecond})
	journal, err := manager.PreviousJobs()
	if err != nil || len(journal.Jobs) != 1 || journal.Jobs[0].ProcessID != snap.ProcessID || journal.Terminated {
		t.Fatalf("live journal=%#v err=%v", journal, err)
	}
	if !strings.Contains(journal.ChatNotice("chat-1"), "could not be confirmed") {
		t.Fatal("unclean restart claimed confirmed termination")
	}
	if err := manager.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	next := NewManager()
	next.SetRestartJournal(path)
	journal, err = next.PreviousJobs()
	if err != nil || !journal.Terminated || len(journal.Jobs) != 1 {
		t.Fatalf("shutdown journal=%#v err=%v", journal, err)
	}
	notice := journal.ChatNotice("chat-1")
	for _, want := range []string{"Koder was restarted", "terminated during shutdown", snap.ProcessID, "Wait for input", "read value", snap.Workdir, "not live handles"} {
		if !strings.Contains(notice, want) {
			t.Fatalf("missing %q in %s", want, notice)
		}
	}
	if journal.ChatNotice("unrelated-chat") != "" {
		t.Fatal("job details leaked to unrelated owner")
	}
	if err := next.ClearPreviousJobs(); err != nil {
		t.Fatal(err)
	}
	if journal, err := next.PreviousJobs(); err != nil || len(journal.Jobs) != 0 {
		t.Fatalf("acknowledged journal remains: %#v %v", journal, err)
	}
}

func TestCompletedJobsLeaveRestartInventory(t *testing.T) {
	manager := NewManager()
	manager.SetRestartJournal(filepath.Join(t.TempDir(), "jobs.json"))
	snap := startTestProcess(t, manager, StartRequest{Command: "echo done"})
	p, err := manager.lookup("session-1", "chat-1", snap.ProcessID)
	if err != nil {
		t.Fatal(err)
	}
	<-p.settled
	journal, err := manager.PreviousJobs()
	if err != nil || len(journal.Jobs) != 0 {
		t.Fatalf("completed job retained: %#v %v", journal, err)
	}
}
