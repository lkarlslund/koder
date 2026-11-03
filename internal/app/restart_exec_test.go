package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
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
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/store"
)

// An idle chat whose process a restart stopped is told so in a new turn,
// once, so the model can restart the work if it is still needed.
func TestStartupExplainsTerminatedExecJobsOnceToTheirOwner(t *testing.T) {
	ctx := context.Background()
	heard := make(chan string, 4)
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		heard <- string(body)
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"Restarting the import."},"finish_reason":"stop"}],"usage":{"total_tokens":2}}`)
	}))
	defer modelServer.Close()
	cfg := config.Default().WithStateDir(t.TempDir())
	cfg.Providers = map[string]config.Provider{
		"test": {Kind: provider.ProviderKindCompatible, BaseURL: modelServer.URL + "/v1", Timeout: 5 * time.Second},
	}
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
	select {
	case request := <-heard:
		if !strings.Contains(request, "Koder was restarted") || !strings.Contains(request, started.ProcessID) {
			t.Fatalf("the model's turn lacks the restart notice: %s", request)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the idle chat was not given a turn to hear about the restart")
	}
	var timeline []domain.TimelineItem
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		timeline, err = modeltest.TimelineForChat(ctx, st, owner.ID)
		if err != nil {
			t.Fatal(err)
		}
		if msg, ok := timeline[len(timeline)-1].Content.(domain.AssistantMessage); ok && msg.Text == "Restarting the import." {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the chat did not answer the restart notice: %+v", timeline)
		}
	}
	// Simulate a crash after delivering the notice but before acknowledging the journal.
	if err := os.WriteFile(journalPath, savedJournal, 0600); err != nil {
		t.Fatal(err)
	}
	if err := restarted.ExplainRestartedExecs(ctx, nil); err != nil {
		t.Fatal(err)
	}
	timeline, err = modeltest.TimelineForChat(ctx, st, owner.ID)
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
