package todotool_test

import (
	"context"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/modeltest"
	"github.com/lkarlslund/koder/internal/planning"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
	_ "github.com/lkarlslund/koder/internal/tools/all"
	"github.com/lkarlslund/koder/internal/tools/tooltest"
)

func TestTodoUpdateItemParsesStringID(t *testing.T) {
	id, err := planning.ParseTodoID("019aa000-0000-7000-8000-000000000001")
	if err != nil {
		t.Fatalf("expected todo id to parse, got %v", err)
	}
	if id != "019aa000-0000-7000-8000-000000000001" {
		t.Fatalf("expected parsed todo id, got %s", id)
	}
}

func TestTodoUpdateItemDefinitionUsesUUIDStringID(t *testing.T) {
	defs := tools.Definitions(tools.Runtime{})
	for _, def := range defs {
		if def.Function.Name != domain.ToolKindTodoUpdateItem.String() {
			continue
		}
		params := string(def.Function.Parameters)
		if !strings.Contains(params, `"id":{"type":"string"`) || strings.Contains(params, `"id":{"type":"integer"`) {
			t.Fatalf("expected todo_update_item id to be a string UUID, got %s", params)
		}
		if !strings.Contains(params, `"enum":["pending","in_progress","completed"]`) || strings.Contains(params, "InProgress") {
			t.Fatalf("expected todo_update_item status enum to match TodoStatus strings, got %s", params)
		}
		if !strings.Contains(def.Function.Description, "exact UUID id") {
			t.Fatalf("expected todo_update_item description to tell model to use UUID ids, got %q", def.Function.Description)
		}
		return
	}
	t.Fatal("todo_update_item definition not found")
}

func TestTodoStatusUsesSnakeCase(t *testing.T) {
	if _, err := planning.ParseTodoStatus("InProgress"); err == nil {
		t.Fatal("expected InProgress to be rejected")
	}
	status, err := planning.ParseTodoStatus("in_progress")
	if err != nil {
		t.Fatal(err)
	}
	if status != planning.TodoStatusInProgress {
		t.Fatalf("expected in_progress, got %s", status)
	}
}

func TestMilestoneAndTodoWorkflow(t *testing.T) {
	ctx := context.Background()
	st := openPlanningTestStore(t)
	session, err := modeltest.CreateSession(ctx, st, "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	runtime := tools.Runtime{Store: st, SessionID: session.ID, SessionControl: tooltest.NewSessionControl(st)}

	_, err = executeAndPersist(ctx, t, runtime, tools.Request{
		Tool: domain.ToolKindMilestoneAdd,
		Args: map[string]string{
			"items": `[{"ref":"investigate","title":"Investigate"},{"ref":"implement","title":"Implement"}]`,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executeAndPersist(ctx, t, runtime, tools.Request{
		Tool: domain.ToolKindMilestoneUpdate,
		Args: map[string]string{"ref": "investigate", "status": "completed"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := executeAndPersist(ctx, t, runtime, tools.Request{
		Tool: domain.ToolKindMilestoneUpdate,
		Args: map[string]string{"ref": "implement", "status": "ready"},
	}); err != nil {
		t.Fatal(err)
	}

	_, err = executeAndPersist(ctx, t, runtime, tools.Request{
		Tool: domain.ToolKindTodoAddItems,
		Args: map[string]string{
			"milestone_ref": "implement",
			"items":         `[{"content":"Write tests"},{"content":"Fix bug"}]`,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	next, err := tools.Execute(ctx, runtime, tools.Request{
		Tool: domain.ToolKindTodoFetchNext,
		Args: map[string]string{"milestone_ref": "implement"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(next.Output, "Write tests") {
		t.Fatalf("expected first pending todo, got %q", next.Output)
	}

	todos, err := modeltest.ListTodos(ctx, st, session.ID, "implement")
	if err != nil {
		t.Fatal(err)
	}
	if len(todos) != 2 {
		t.Fatalf("unexpected todos: %#v", todos)
	}
	if _, err := executeAndPersist(ctx, t, runtime, tools.Request{
		Tool: domain.ToolKindTodoUpdateItem,
		Args: map[string]string{"id": tools.FormatTodoID(todos[0].ID), "status": "in_progress"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := executeAndPersist(ctx, t, runtime, tools.Request{
		Tool: domain.ToolKindTodoUpdateItem,
		Args: map[string]string{"id": tools.FormatTodoID(todos[0].ID), "status": "completed"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := executeAndPersist(ctx, t, runtime, tools.Request{
		Tool: domain.ToolKindTodoUpdateItem,
		Args: map[string]string{"id": tools.FormatTodoID(todos[1].ID), "status": "completed"},
	}); err != nil {
		t.Fatal(err)
	}

	done, err := tools.Execute(ctx, runtime, tools.Request{
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

func TestTodoAddPersistReturnsRealTodoIDs(t *testing.T) {
	ctx := context.Background()
	st := openPlanningTestStore(t)
	session, err := modeltest.CreateSession(ctx, st, "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	runtime := tools.Runtime{Store: st, SessionID: session.ID, SessionControl: tooltest.NewSessionControl(st)}

	if _, err := executeAndPersist(ctx, t, runtime, tools.Request{
		Tool: domain.ToolKindMilestoneAdd,
		Args: map[string]string{"items": `[{"ref":"implement","title":"Implement"}]`},
	}); err != nil {
		t.Fatal(err)
	}
	req := tools.Request{
		Tool: domain.ToolKindTodoAddItems,
		Args: map[string]string{
			"milestone_ref": "implement",
			"items":         `[{"content":"Write tests"},{"content":"Fix bug"}]`,
		},
	}
	result, err := tools.Execute(ctx, runtime, req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "# Write tests") {
		t.Fatalf("expected execute preview to contain todo content, got %q", result.Output)
	}
	events, err := tools.PersistResult(ctx, runtime, req, result)
	if err != nil {
		t.Fatal(err)
	}
	event := <-events
	if !strings.Contains(event.Text, " Write tests") || !strings.Contains(event.Text, " Fix bug") {
		t.Fatalf("expected persisted event to contain real todo ids, got %q", event.Text)
	}
}

func TestTodoScopedChatSeesAndUpdatesOnlyAssignedTodo(t *testing.T) {
	ctx := context.Background()
	st := openPlanningTestStore(t)
	session, err := modeltest.CreateSession(ctx, st, "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := modeltest.PutPlan(ctx, st, planning.Plan{SessionID: session.ID, Summary: "Ship it", Milestones: []planning.Milestone{
		{Ref: "implement", Title: "Implement", Status: planning.MilestoneStatusExecuting},
	}}); err != nil {
		t.Fatal(err)
	}
	todos, err := modeltest.AddTodoItems(ctx, st, session.ID, "implement", []string{"First", "Second"})
	if err != nil {
		t.Fatal(err)
	}
	chat := domain.Chat{
		ID:                    "chat-1",
		WorkflowRole:          chatrole.Execution,
		ActiveMilestoneRef:    "implement",
		AssignedTodoBucketRef: "implement",
		AssignedTodoRef:       todos[0].ID,
	}
	runtime := tools.Runtime{
		Store:                 st,
		SessionID:             session.ID,
		ChatID:                chat.ID,
		ChatRole:              chat.WorkflowRole,
		ActiveMilestoneRef:    chat.ActiveMilestoneRef,
		AssignedTodoBucketRef: chat.AssignedTodoBucketRef,
		AssignedTodoRef:       chat.AssignedTodoRef,
		SessionControl:        tooltest.NewSessionControl(st),
	}

	listed, err := tools.Execute(ctx, runtime, tools.Request{
		Tool: domain.ToolKindTodoList,
		Args: map[string]string{"milestone_ref": "implement"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(listed.Output, "First") || strings.Contains(listed.Output, "Second") {
		t.Fatalf("expected single scoped todo, got %q", listed.Output)
	}

	if _, err := tools.Execute(ctx, runtime, tools.Request{
		Tool: domain.ToolKindTodoUpdateItem,
		Args: map[string]string{"id": string(todos[1].ID), "status": planning.TodoStatusCompleted.String()},
	}); err == nil || !strings.Contains(err.Error(), "scoped to todo") {
		t.Fatalf("expected scoped todo error, got %v", err)
	}
	if _, err := tools.Execute(ctx, runtime, tools.Request{
		Tool: domain.ToolKindTodoAddItems,
		Args: map[string]string{"milestone_ref": "implement", "items": `[{"content":"Third"}]`},
	}); err == nil || !strings.Contains(err.Error(), "scoped to todo") {
		t.Fatalf("expected add todo scoped error, got %v", err)
	}
}

func TestMilestoneWriteHiddenFromDefinitions(t *testing.T) {
	defs := tools.Definitions(tools.Runtime{})
	for _, def := range defs {
		if def.Function.Name == domain.ToolKindMilestoneWrite.String() {
			t.Fatalf("milestone_write should not be exposed to the model")
		}
	}
	foundAdd := false
	foundUpdate := false
	for _, def := range defs {
		switch def.Function.Name {
		case domain.ToolKindMilestoneAdd.String():
			foundAdd = true
		case domain.ToolKindMilestoneUpdate.String():
			foundUpdate = true
		}
	}
	if !foundAdd || !foundUpdate {
		t.Fatalf("expected milestone add/update tools, got add=%v update=%v", foundAdd, foundUpdate)
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

func executeAndPersist(ctx context.Context, t *testing.T, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	t.Helper()
	result, err := tools.Execute(ctx, runtime, req)
	if err != nil {
		return tools.Result{}, err
	}
	if _, err := tools.PersistResult(ctx, runtime, req, result); err != nil {
		return tools.Result{}, err
	}
	return result, nil
}
