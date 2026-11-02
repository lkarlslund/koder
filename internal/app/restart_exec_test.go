package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/agent"
	"github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/execruntime"
	"github.com/lkarlslund/koder/internal/modeltest"
	"github.com/lkarlslund/koder/internal/store"
)

func TestStartupExplainsTerminatedExecJobsOnceToTheirOwner(t *testing.T) {
	ctx := context.Background()
	cfg := config.Default().WithStateDir(t.TempDir())
	st, err := store.OpenWithOptions(cfg.StateDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	session, err := modeltest.CreateSession(ctx, st, "restart", "test", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := modeltest.DefaultChat(ctx, st, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	first := agent.New(cfg, st, nil, nil)
	started, err := first.ExecManager().Start(ctx, execruntime.StartRequest{SessionID: session.ID, ChatID: owner.ID, Command: "read value", Label: "Long import", Workdir: t.TempDir(), YieldTime: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Shutdown(ctx, chat.CancelReasonShutdownInterrupt); err != nil {
		t.Fatal(err)
	}
	journalPath := filepath.Join(cfg.StateDir(), "running-execs.json")
	savedJournal, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	restarted := agent.New(cfg, st, nil, nil)
	ctrl := New(cfg, restarted)
	defer ctrl.ShutdownWithCancelReason(ctx, chat.CancelReasonShutdownInterrupt)
	if err := ctrl.Start(ctx, StartupModeNew, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	// Simulate a crash after appending the notice but before acknowledging the journal.
	if err := os.WriteFile(journalPath, savedJournal, 0600); err != nil {
		t.Fatal(err)
	}
	if err := restarted.ExplainRestartedExecs(ctx); err != nil {
		t.Fatal(err)
	}
	timeline, err := modeltest.TimelineForChat(ctx, st, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, item := range timeline {
		if user, ok := item.Content.(domain.UserMessage); ok && user.Source == domain.UserMessageSourceExec && strings.Contains(user.Text, "Koder was restarted") {
			count++
			if !strings.Contains(user.Text, started.ProcessID) || !strings.Contains(user.Text, "Long import") || !strings.Contains(user.Text, "terminated during shutdown") {
				t.Fatalf("restart notice: %s", user.Text)
			}
		}
	}
	if count != 1 {
		t.Fatalf("restart notices=%d, want 1", count)
	}
}
