package updateplantool

import (
	"context"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
)

func openPlanStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.OpenWithOptions(t.TempDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestNormalizeArgsValidatesPlan(t *testing.T) {
	if _, err := (tool{}).NormalizeArgs(map[string]string{}); err == nil {
		t.Fatal("expected empty plan error")
	}
	if _, err := (tool{}).NormalizeArgs(map[string]string{"plan": `[{"step":"one","status":"in_progress"},{"step":"two","status":"in_progress"}]`}); err == nil {
		t.Fatal("expected multiple in_progress validation error")
	}
}

func TestExecuteFormatsPlan(t *testing.T) {
	result, err := tool{}.Execute(context.Background(), tools.Runtime{}, tools.Request{
		Args: map[string]string{
			"explanation": "Do the work",
			"plan":        `[{"step":"Inspect","status":"completed"},{"step":"Implement","status":"in_progress"}]`,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "Do the work") || !strings.Contains(result.Output, "[completed] Inspect") {
		t.Fatalf("unexpected plan output: %q", result.Output)
	}
}

func TestPersistResultStoresPlanUpdate(t *testing.T) {
	st := openPlanStore(t)
	session, err := st.CreateSession(context.Background(), "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	events, err := tool{}.PersistResult(context.Background(), st, session.ID, tools.Request{
		Tool: domain.ToolKindUpdatePlan,
		Args: map[string]string{
			"plan":        `[{"step":"Inspect","status":"completed"}]`,
			"explanation": "Done",
		},
	}, tools.Result{
		Output: "Done\n[completed] Inspect",
		Meta:   map[string]string{"step_count": "1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	evt := <-events
	if evt.Kind != domain.EventKindStatus || evt.Text != "Plan updated" {
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
		t.Fatalf("unexpected stored plan output: %#v", items)
	}
	exec, ok := items[0].Content.(domain.ToolExecution)
	if !ok || exec.Result == nil {
		t.Fatalf("expected plan tool execution, got %#v", items[0])
	}
	if _, ok := exec.Result.Data.(domain.UpdatePlanStoredResult); !ok {
		t.Fatalf("expected typed plan result, got %#v", exec.Result.Data)
	}
}

func TestPersistResultRejectsInvalidPlanBeforeWriting(t *testing.T) {
	st := openPlanStore(t)
	session, err := st.CreateSession(context.Background(), "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}

	_, err = tool{}.PersistResult(context.Background(), st, session.ID, tools.Request{
		Tool: domain.ToolKindUpdatePlan,
		Args: map[string]string{
			"plan": `[{"step":"one","status":"in_progress"},{"step":"two","status":"in_progress"}]`,
		},
	}, tools.Result{})
	if err == nil {
		t.Fatal("expected invalid plan error")
	}

	chat, err := st.DefaultChat(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	items, err := st.TimelineForChat(context.Background(), chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("expected no partial plan item on error, got %#v", items)
	}
}
