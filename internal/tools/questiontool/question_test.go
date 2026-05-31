package questiontool

import (
	"context"
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
)

func openQuestionStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.OpenWithOptions(t.TempDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestNormalizeAndExecute(t *testing.T) {
	if _, err := (tool{}).NormalizeArgs(map[string]string{}); err == nil {
		t.Fatal("expected empty question error")
	}
	req, err := (tool{}).NormalizeArgs(map[string]string{"question": " What next? "})
	if err != nil {
		t.Fatal(err)
	}
	result, err := tool{}.Execute(context.Background(), tools.Runtime{}, tools.Request{Args: req})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "What next?" {
		t.Fatalf("unexpected output: %q", result.Output)
	}
}

func TestPersistResult(t *testing.T) {
	st := openQuestionStore(t)
	session, err := st.CreateSession(context.Background(), "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	events, err := tools.PersistResult(context.Background(), tools.Runtime{Store: st, SessionID: session.ID}, tools.Request{
		Tool: domain.ToolKindQuestion,
		Args: map[string]string{"question": "What next?"},
	}, tools.Result{
		Output: "What next?",
		Stored: tools.QuestionStoredResult{Question: "What next?"},
	})
	if err != nil {
		t.Fatal(err)
	}
	evt := <-events
	if evt.Kind != domain.EventKindToolResult {
		t.Fatalf("unexpected event: %#v", evt)
	}
	chat, err := st.DefaultChat(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	items, err := st.TimelineForChat(context.Background(), chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("unexpected stored output: %#v", items)
	}
	exec, ok := items[0].Content.(domain.ToolExecution)
	if !ok || exec.Tool != domain.ToolKindQuestion || exec.Result == nil {
		t.Fatalf("expected question tool execution, got %#v", items[0])
	}
	if _, ok := exec.Result.Data.(domain.QuestionStoredResult); !ok {
		t.Fatalf("expected typed question tool payload, got %#v", exec.Result.Data)
	}
}
