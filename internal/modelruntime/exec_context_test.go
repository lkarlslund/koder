package modelruntime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/execruntime"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/toolruntime"
)

func TestRunningExecInventoryIsLiveAndDoesNotMutateHistory(t *testing.T) {
	ctx := context.Background()
	manager := execruntime.NewManager()
	tr := toolruntime.New(toolruntime.Config{Exec: manager})
	runtime := New(Config{Config: config.Default().WithStateDir(t.TempDir()), Tools: tr})
	session := domain.Session{ID: "session", ProjectRoot: t.TempDir()}
	chat := domain.Chat{ID: "chat", SessionID: session.ID}
	started, err := manager.Start(ctx, execruntime.StartRequest{SessionID: session.ID, ChatID: "sibling", Command: "read value", Label: "Expensive proof already started", Workdir: session.ProjectRoot, YieldTime: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = manager.Cleanup(ctx, execruntime.CleanupRequest{SessionID: session.ID, Scope: execruntime.ScopeSession})
	})
	history := make([]provider.Message, 1, 8)
	history[0] = provider.Message{Role: provider.RoleUser, Content: "Compacted history"}
	request := runtime.ChatRequest(session, chat, history, false)
	if len(request.Messages) != 2 || !strings.Contains(request.Messages[1].Content, started.ProcessID) || !strings.Contains(request.Messages[1].Content, "Expensive proof already started") {
		t.Fatalf("missing live inventory: %#v", request.Messages)
	}
	if history[0].Content != "Compacted history" || history[:cap(history)][1].Content != "" {
		t.Fatal("request inventory mutated caller history")
	}
	if _, err := manager.Terminate(ctx, execruntime.TerminateRequest{SessionID: session.ID, ChatID: "sibling", ProcessID: started.ProcessID}); err != nil {
		t.Fatal(err)
	}
	next := runtime.ChatRequest(session, chat, history, false)
	if len(next.Messages) != 1 {
		t.Fatalf("completed work left stale reminder: %#v", next.Messages)
	}
}
