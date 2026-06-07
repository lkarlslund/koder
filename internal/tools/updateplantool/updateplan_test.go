package updateplantool

import (
	"context"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/modeltest"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
	"github.com/lkarlslund/koder/internal/tools/tooltest"
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

func TestFinalizeResultStoresPlanUpdate(t *testing.T) {
	st := openPlanStore(t)
	session, err := modeltest.CreateSession(context.Background(), st, "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	runtime := tools.Runtime{SessionID: session.ID, SessionControl: tooltest.NewSessionControl(st)}
	req := tools.Request{
		Tool:       domain.ToolKindUpdatePlan,
		ToolCallID: "call_plan",
		Args: map[string]string{
			"plan":        `[{"step":"Inspect","status":"completed"}]`,
			"explanation": "Done",
		},
	}
	result, err := tool{}.FinalizeResult(context.Background(), runtime, req, tools.Result{
		Output: "Done\n[completed] Inspect",
		Meta:   map[string]string{"step_count": "1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	toolResult, body, err := tools.BuildToolResult(req, result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "[completed] Inspect") {
		t.Fatalf("unexpected plan result body: %q", body)
	}
	if _, ok := toolResult.Data.(tools.UpdatePlanStoredResult); !ok {
		t.Fatalf("expected typed plan result, got %#v", toolResult.Data)
	}
}

func TestFinalizeResultRejectsInvalidPlanBeforeWriting(t *testing.T) {
	st := openPlanStore(t)
	session, err := modeltest.CreateSession(context.Background(), st, "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}

	runtime := tools.Runtime{SessionID: session.ID, SessionControl: tooltest.NewSessionControl(st)}
	_, err = tool{}.FinalizeResult(context.Background(), runtime, tools.Request{
		Tool: domain.ToolKindUpdatePlan,
		Args: map[string]string{
			"plan": `[{"step":"one","status":"in_progress"},{"step":"two","status":"in_progress"}]`,
		},
	}, tools.Result{})
	if err == nil {
		t.Fatal("expected invalid plan error")
	}

	chat, err := modeltest.DefaultChat(context.Background(), st, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	items, err := modeltest.TimelineForChat(context.Background(), st, chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("expected no partial plan item on error, got %#v", items)
	}
}
