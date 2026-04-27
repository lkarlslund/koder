package questiontool

import (
	"context"
	"strings"
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
	events, err := tool{}.PersistResult(context.Background(), st, session.ID, tools.Request{
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
	messages, partsByMessage, err := st.PartsForSession(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || len(partsByMessage[messages[0].ID]) != 1 {
		t.Fatalf("unexpected stored output: %#v %#v", messages, partsByMessage)
	}
	if !strings.Contains(partsByMessage[messages[0].ID][0].MetaJSON, `"tool":"question"`) {
		t.Fatalf("expected stored metadata to mention question tool, got %q", partsByMessage[messages[0].ID][0].MetaJSON)
	}
}
