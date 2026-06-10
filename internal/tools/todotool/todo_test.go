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

func TestTodoUpdateItemParsesTaskKey(t *testing.T) {
	id, err := planning.ParseTodoKey("M001T001")
	if err != nil {
		t.Fatalf("expected task key to parse, got %v", err)
	}
	if id != "M001T001" {
		t.Fatalf("expected parsed task key, got %s", id)
	}
	if _, err := planning.ParseTodoKey("M001"); err == nil {
		t.Fatal("expected milestone key to be rejected as task key")
	}
}

func TestTodoUpdateItemDefinitionUsesTaskKey(t *testing.T) {
	defs := tools.Definitions(tools.Runtime{})
	for _, def := range defs {
		if def.Function.Name != domain.ToolKindTasksUpdate.String() {
			continue
		}
		params := string(def.Function.Parameters)
		if !strings.Contains(params, `"task_key":{"type":"string"`) || strings.Contains(params, `"id"`) {
			t.Fatalf("expected tasks_update task_key string, got %s", params)
		}
		if !strings.Contains(params, `"enum":["pending","in_progress","completed"]`) || strings.Contains(params, "InProgress") {
			t.Fatalf("expected tasks_update status enum to match task status strings, got %s", params)
		}
		if !strings.Contains(params, `"required":["task_key","status","note"]`) {
			t.Fatalf("expected tasks_update to require note, got %s", params)
		}
		if !strings.Contains(def.Function.Description, "exact task_key") {
			t.Fatalf("expected tasks_update description to tell model to use task keys, got %q", def.Function.Description)
		}
		return
	}
	t.Fatal("tasks_update definition not found")
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
	runtime := tools.Runtime{SessionID: session.ID, SessionControl: tooltest.NewSessionControl(st)}

	_, err = executeAndPersist(ctx, t, runtime, tools.Request{
		Tool: domain.ToolKindMilestoneAdd,
		Args: map[string]string{"title": "Investigate"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = executeAndPersist(ctx, t, runtime, tools.Request{
		Tool: domain.ToolKindMilestoneAdd,
		Args: map[string]string{"title": "Implement"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executeAndPersist(ctx, t, runtime, tools.Request{
		Tool: domain.ToolKindMilestoneUpdate,
		Args: map[string]string{"milestone_key": "M001", "status": "completed"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := executeAndPersist(ctx, t, runtime, tools.Request{
		Tool: domain.ToolKindMilestoneUpdate,
		Args: map[string]string{"milestone_key": "M002", "status": "ready"},
	}); err != nil {
		t.Fatal(err)
	}

	_, err = executeAndPersist(ctx, t, runtime, tools.Request{
		Tool: domain.ToolKindTasksAdd,
		Args: map[string]string{
			"milestone_key": "M002",
			"items":         `[{"content":"Write tests"},{"content":"Fix bug"}]`,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	next, err := tools.Call(ctx, tools.Options{Runtime: runtime, Request: tools.Request{
		Tool: domain.ToolKindTaskFetchNext,
		Args: map[string]string{"milestone_key": "M002"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(next.Output, "Write tests") {
		t.Fatalf("expected first pending task, got %q", next.Output)
	}

	todos, err := modeltest.ListTodos(ctx, st, session.ID, "M002")
	if err != nil {
		t.Fatal(err)
	}
	if len(todos) != 2 {
		t.Fatalf("unexpected tasks: %#v", todos)
	}
	if _, err := executeAndPersist(ctx, t, runtime, tools.Request{
		Tool: domain.ToolKindTasksUpdate,
		Args: map[string]string{"task_key": tools.FormatTodoID(planning.TodoKey(todos[0])), "status": "in_progress", "note": "Started writing tests."},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := executeAndPersist(ctx, t, runtime, tools.Request{
		Tool: domain.ToolKindTasksUpdate,
		Args: map[string]string{"task_key": tools.FormatTodoID(planning.TodoKey(todos[0])), "status": "completed", "note": "Completed the tests."},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := executeAndPersist(ctx, t, runtime, tools.Request{
		Tool: domain.ToolKindTasksUpdate,
		Args: map[string]string{"task_key": tools.FormatTodoID(planning.TodoKey(todos[1])), "status": "completed", "note": "Fixed the bug."},
	}); err != nil {
		t.Fatal(err)
	}

	done, err := tools.Call(ctx, tools.Options{Runtime: runtime, Request: tools.Request{
		Tool: domain.ToolKindTaskFetchNext,
		Args: map[string]string{"milestone_key": "M002"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(done.Output, "All tasks for this milestone are done") {
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
	runtime := tools.Runtime{SessionID: session.ID, SessionControl: tooltest.NewSessionControl(st)}

	if _, err := executeAndPersist(ctx, t, runtime, tools.Request{
		Tool: domain.ToolKindMilestoneAdd,
		Args: map[string]string{"title": "Implement"},
	}); err != nil {
		t.Fatal(err)
	}
	req := tools.Request{
		Tool: domain.ToolKindTasksAdd,
		Args: map[string]string{
			"milestone_key": "M001",
			"items":         `[{"content":"Write tests"},{"content":"Fix bug"}]`,
		},
	}
	result, err := tools.Call(ctx, tools.Options{Runtime: runtime, Request: req})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "Write tests") {
		t.Fatalf("expected execute preview to contain task content, got %q", result.Output)
	}
	_, body, err := tools.FinalizeResult(ctx, runtime, req, result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "Write tests") || !strings.Contains(body, "Fix bug") {
		t.Fatalf("expected finalized result to contain real task ids, got %q", body)
	}
}

func TestTodoAddRejectsDuplicateContent(t *testing.T) {
	ctx := context.Background()
	st := openPlanningTestStore(t)
	session, err := modeltest.CreateSession(ctx, st, "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := modeltest.PutPlan(ctx, st, planning.Plan{SessionID: session.ID, Milestones: []planning.Milestone{{Ref: "implement", Title: "Implement", Status: planning.MilestoneStatusReady}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := modeltest.AddTodoItems(ctx, st, session.ID, "M001", []string{"Write tests"}); err != nil {
		t.Fatal(err)
	}
	runtime := tools.Runtime{SessionID: session.ID, SessionControl: tooltest.NewSessionControl(st)}

	_, err = tools.Call(ctx, tools.Options{Runtime: runtime, Request: tools.Request{
		Tool: domain.ToolKindTasksAdd,
		Args: map[string]string{
			"milestone_key": "M001",
			"items":         `[{"content":"  write   tests "}]`,
		},
	}})
	if err == nil || !strings.Contains(err.Error(), "duplicate task content") {
		t.Fatalf("expected duplicate task content error, got %v", err)
	}
}

func TestTodoAddRejectsClosedMilestones(t *testing.T) {
	ctx := context.Background()
	st := openPlanningTestStore(t)
	session, err := modeltest.CreateSession(ctx, st, "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := modeltest.PutPlan(ctx, st, planning.Plan{SessionID: session.ID, Milestones: []planning.Milestone{
		{Ref: "done", Title: "Done", Status: planning.MilestoneStatusCompleted},
		{Ref: "cancelled", Title: "Cancelled", Status: planning.MilestoneStatusCancelled},
	}}); err != nil {
		t.Fatal(err)
	}
	runtime := tools.Runtime{SessionID: session.ID, SessionControl: tooltest.NewSessionControl(st)}

	for _, ref := range []string{"M001", "M002"} {
		req := tools.Request{
			Tool: domain.ToolKindTasksAdd,
			Args: map[string]string{
				"milestone_key": ref,
				"items":         `[{"content":"Reopen work"}]`,
			},
		}
		_, err := tools.Call(ctx, tools.Options{Runtime: runtime, Request: req})
		if err == nil {
			t.Fatalf("expected closed milestone error for %s", ref)
		}
		if !strings.Contains(err.Error(), "cannot add tasks") || !strings.Contains(err.Error(), "milestone_update with status=ready") {
			t.Fatalf("expected reopen guidance for %s, got %v", ref, err)
		}
		if _, _, err := tools.FinalizeResult(ctx, runtime, req, tools.Result{}); err == nil || !strings.Contains(err.Error(), "cannot add tasks") {
			t.Fatalf("expected persist closed milestone error for %s, got %v", ref, err)
		}
	}
}

func TestTodoUpdateRequiresAndPersistsNote(t *testing.T) {
	ctx := context.Background()
	st := openPlanningTestStore(t)
	session, err := modeltest.CreateSession(ctx, st, "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := modeltest.PutPlan(ctx, st, planning.Plan{SessionID: session.ID, Milestones: []planning.Milestone{{Ref: "implement", Title: "Implement", Status: planning.MilestoneStatusExecuting}}}); err != nil {
		t.Fatal(err)
	}
	todos, err := modeltest.AddTodoItems(ctx, st, session.ID, "M001", []string{"Wire endpoint"})
	if err != nil {
		t.Fatal(err)
	}
	runtime := tools.Runtime{SessionID: session.ID, SessionControl: tooltest.NewSessionControl(st)}

	if _, err := tools.Normalize(tools.Request{
		Tool: domain.ToolKindTasksUpdate,
		Args: map[string]string{"task_key": planning.TodoKey(todos[0]), "status": "completed"},
	}); err == nil || !strings.Contains(err.Error(), "note is required") {
		t.Fatalf("expected missing note error, got %v", err)
	}

	result, err := executeAndPersist(ctx, t, runtime, tools.Request{
		Tool: domain.ToolKindTasksUpdate,
		Args: map[string]string{"task_key": planning.TodoKey(todos[0]), "status": "completed", "note": "Endpoint was wired and tested."},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "note: Endpoint was wired and tested.") {
		t.Fatalf("expected output to include note, got %q", result.Output)
	}
	updated, err := modeltest.GetTodo(ctx, st, todos[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Note != "Endpoint was wired and tested." {
		t.Fatalf("expected persisted note, got %#v", updated)
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
	todos, err := modeltest.AddTodoItems(ctx, st, session.ID, "M001", []string{"First", "Second"})
	if err != nil {
		t.Fatal(err)
	}
	chat := domain.Chat{
		ID:                    "chat-1",
		WorkflowRole:          chatrole.Execution,
		ActiveMilestoneRef:    "M001",
		AssignedTodoBucketRef: "M001",
		AssignedTodoRef:       planning.TodoKey(todos[0]),
	}
	runtime := tools.Runtime{
		SessionID:             session.ID,
		ChatID:                chat.ID,
		ChatRole:              chat.WorkflowRole,
		ActiveMilestoneRef:    chat.ActiveMilestoneRef,
		AssignedTodoBucketRef: chat.AssignedTodoBucketRef,
		AssignedTodoRef:       chat.AssignedTodoRef,
		SessionControl:        tooltest.NewSessionControl(st),
	}

	listed, err := tools.Call(ctx, tools.Options{Runtime: runtime, Request: tools.Request{
		Tool: domain.ToolKindTaskList,
		Args: map[string]string{"milestone_key": "M001"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(listed.Output, "First") || strings.Contains(listed.Output, "Second") {
		t.Fatalf("expected single scoped task, got %q", listed.Output)
	}

	if _, err := tools.Call(ctx, tools.Options{Runtime: runtime, Request: tools.Request{
		Tool: domain.ToolKindTasksUpdate,
		Args: map[string]string{"task_key": planning.TodoKey(todos[1]), "status": planning.TodoStatusCompleted.String(), "note": "Tried to complete scoped task."},
	}}); err == nil || !strings.Contains(err.Error(), "scoped to task") {
		t.Fatalf("expected scoped task error, got %v", err)
	}
	if _, err := tools.Call(ctx, tools.Options{Runtime: runtime, Request: tools.Request{
		Tool: domain.ToolKindTasksAdd,
		Args: map[string]string{"milestone_key": "M001", "items": `[{"content":"Third"}]`},
	}}); err == nil || !strings.Contains(err.Error(), "scoped to task") {
		t.Fatalf("expected add task scoped error, got %v", err)
	}
}

func TestMilestoneAddUpdateExposedInDefinitions(t *testing.T) {
	defs := tools.Definitions(tools.Runtime{})
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
	result, err := tools.Call(ctx, tools.Options{Runtime: runtime, Request: req})
	if err != nil {
		return tools.Result{}, err
	}
	if _, _, err := tools.FinalizeResult(ctx, runtime, req, result); err != nil {
		return tools.Result{}, err
	}
	return result, nil
}
