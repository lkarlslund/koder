package planningtools_test

import (
	"context"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
	_ "github.com/lkarlslund/koder/internal/tools/all"
)

func TestMilestoneAndTodoWorkflow(t *testing.T) {
	ctx := context.Background()
	st := openPlanningTestStore(t)
	session, err := st.CreateSession(ctx, "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry(t.TempDir())

	_, err = executeAndPersist(ctx, t, registry, st, session.ID, tools.Request{
		Tool: domain.ToolKindMilestoneWrite,
		Args: map[string]string{
			"summary":    "Ship feature",
			"milestones": `[{"ref":"investigate","title":"Investigate","status":"completed"},{"ref":"implement","title":"Implement","status":"in_progress"}]`,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = executeAndPersist(ctx, t, registry, st, session.ID, tools.Request{
		Tool: domain.ToolKindTodoAddItems,
		Args: map[string]string{
			"milestone_ref": "implement",
			"items":         `[{"content":"Write tests"},{"content":"Fix bug"}]`,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	next, err := registry.ExecuteWithSession(ctx, st, session.ID, tools.Request{
		Tool: domain.ToolKindTodoFetchNext,
		Args: map[string]string{"milestone_ref": "implement"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(next.Output, "Write tests") {
		t.Fatalf("expected first pending todo, got %q", next.Output)
	}

	todos, err := st.ListTodos(ctx, session.ID, "implement")
	if err != nil {
		t.Fatal(err)
	}
	if len(todos) != 2 {
		t.Fatalf("unexpected todos: %#v", todos)
	}
	if _, err := executeAndPersist(ctx, t, registry, st, session.ID, tools.Request{
		Tool: domain.ToolKindTodoUpdateItem,
		Args: map[string]string{"id": tools.FormatTodoID(todos[0].ID), "status": "in_progress"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := executeAndPersist(ctx, t, registry, st, session.ID, tools.Request{
		Tool: domain.ToolKindTodoUpdateItem,
		Args: map[string]string{"id": tools.FormatTodoID(todos[0].ID), "status": "completed"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := executeAndPersist(ctx, t, registry, st, session.ID, tools.Request{
		Tool: domain.ToolKindTodoUpdateItem,
		Args: map[string]string{"id": tools.FormatTodoID(todos[1].ID), "status": "completed"},
	}); err != nil {
		t.Fatal(err)
	}

	done, err := registry.ExecuteWithSession(ctx, st, session.ID, tools.Request{
		Tool: domain.ToolKindTodoFetchNext,
		Args: map[string]string{"milestone_ref": "implement"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(done.Output, "All todo items for this milestone are done") {
		t.Fatalf("expected done coercion message, got %q", done.Output)
	}
}

func openPlanningTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.OpenWithOptions(t.TempDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func executeAndPersist(ctx context.Context, t *testing.T, registry *tools.Registry, st *store.Store, sessionID int64, req tools.Request) (tools.Result, error) {
	t.Helper()
	result, err := registry.ExecuteWithSession(ctx, st, sessionID, req)
	if err != nil {
		return tools.Result{}, err
	}
	if _, err := registry.PersistResult(ctx, st, sessionID, req, result); err != nil {
		return tools.Result{}, err
	}
	return result, nil
}
