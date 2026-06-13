package tasktool

import (
	"context"
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/modeltest"
	"github.com/lkarlslund/koder/internal/planning"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
	"github.com/lkarlslund/koder/internal/tools/tooltest"
)

func openTaskStore(t *testing.T) *store.Store {
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
		t.Fatal("expected empty body error")
	}
	req, err := (tool{}).NormalizeArgs(map[string]string{"body": " Ship it "})
	if err != nil {
		t.Fatal(err)
	}
	result, err := tool{}.Call(context.Background(), tools.Options{Runtime: tools.Runtime{}, Request: tools.Request{Args: req}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "Ship it" {
		t.Fatalf("unexpected output: %q", result.Output)
	}
}

func TestFinalizeResultCreatesPendingTask(t *testing.T) {
	st := openTaskStore(t)
	session, err := modeltest.CreateSession(context.Background(), st, "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	control := tooltest.NewSessionControl(st)
	runtime := tools.Runtime{SessionID: session.ID, SessionControl: control, TaskControl: control}
	req := tools.Request{
		Tool:       domain.ToolKindTask,
		ToolCallID: "call_task",
		Args:       map[string]string{"body": "Ship it"},
	}
	result, err := tool{}.FinalizeResult(context.Background(), runtime, req, tools.Result{Output: "Ship it"})
	if err != nil {
		t.Fatal(err)
	}
	toolResult, body, err := tools.BuildToolResult(req, result)
	if err != nil {
		t.Fatal(err)
	}
	if body != "Ship it" || toolResult.Text != "Ship it" {
		t.Fatalf("unexpected task result body=%q result=%#v", body, toolResult)
	}
	if _, ok := toolResult.Data.(tools.TaskStoredResult); !ok {
		t.Fatalf("expected typed task result, got %#v", toolResult.Data)
	}
	tasks, err := modeltest.ListLegacyTasks(context.Background(), st, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].Status != planning.LegacyTaskStatusPending || tasks[0].Body != "Ship it" {
		t.Fatalf("unexpected tasks: %#v", tasks)
	}
}
