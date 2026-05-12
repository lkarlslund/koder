package milestonetool

import (
	"context"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
)

func openMilestoneStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.OpenWithOptions(t.TempDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func newMilestoneRuntime(t *testing.T) (tools.Runtime, *store.Store, domain.Session) {
	t.Helper()
	st := openMilestoneStore(t)
	session, err := st.CreateSession(context.Background(), "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	return tools.Runtime{Store: st, SessionID: session.ID, ChatRole: domain.WorkflowRoleOrchestrator}, st, session
}

func seedPlan(t *testing.T, st *store.Store, sessionID int64) {
	t.Helper()
	if _, err := st.SetMilestonePlan(context.Background(), sessionID, "Ship it", []store.Milestone{
		{Ref: "alpha", Title: "Alpha", Status: domain.MilestoneStatusPending, Position: 0},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestNormalizeArgsAndDefinitions(t *testing.T) {
	if _, err := (addItemsTool{}).NormalizeArgs(map[string]string{}); err == nil {
		t.Fatal("expected empty items error")
	}
	if _, err := (updateItemTool{}).NormalizeArgs(map[string]string{"ref": "alpha"}); err == nil {
		t.Fatal("expected missing status error")
	}
	args, err := (planTool{}).NormalizeArgs(map[string]string{
		"ref":   "alpha",
		"title": "Alpha",
		"items": `[{"content":"one"},{"content":"two"}]`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if args["ref"] != "alpha" || args["title"] != "Alpha" {
		t.Fatalf("unexpected normalized args: %#v", args)
	}
	if _, enabled := tools.DefinitionFor(domain.ToolKindMilestoneAdd, tools.Runtime{ChatRole: domain.WorkflowRoleExecution}); enabled {
		t.Fatal("expected add-items definition to be disabled in execution chats")
	}
	if _, enabled := tools.DefinitionFor(domain.ToolKindMilestonePlan, tools.Runtime{ChatRole: domain.WorkflowRoleDecomposition}); enabled {
		t.Fatal("expected plan definition to be disabled in decomposition chats")
	}
}

func TestAppendAndValidationHelpers(t *testing.T) {
	existing := []store.Milestone{{Ref: "alpha", Title: "Alpha", Position: 0}}
	added := []store.Milestone{{Ref: "beta", Title: "Beta"}}
	got := appendMilestones(existing, added)
	if len(got) != 2 || got[1].Position != 1 {
		t.Fatalf("unexpected appended milestones: %#v", got)
	}
	if err := ensureMilestoneRefsAvailable(existing, added); err != nil {
		t.Fatal(err)
	}
	if err := ensureMilestoneRefsAvailable(existing, []store.Milestone{{Ref: "alpha", Title: "dup"}}); err == nil {
		t.Fatal("expected duplicate ref error")
	}
}

func TestUpsertAndUpdatedPlanHelpers(t *testing.T) {
	items := upsertMilestone([]store.Milestone{{Ref: "alpha", Title: "Alpha", Position: 0}}, store.Milestone{
		Ref:    "alpha",
		Title:  "Alpha updated",
		Status: domain.MilestoneStatusInProgress,
	})
	if items[0].Title != "Alpha updated" || items[0].Position != 0 {
		t.Fatalf("unexpected updated milestone: %#v", items[0])
	}
	items = upsertMilestone(items, store.Milestone{Ref: "beta", Title: "Beta"})
	if len(items) != 2 || items[1].Position != 1 {
		t.Fatalf("unexpected appended milestone: %#v", items)
	}

	plan, err := updatedMilestonePlan(store.MilestonePlan{
		Summary: "Ship it",
		Milestones: []store.Milestone{
			{Ref: "alpha", Title: "Alpha", Status: domain.MilestoneStatusPending, Position: 0},
		},
	}, tools.Request{Args: map[string]string{"ref": "alpha", "status": "completed", "notes": "done"}})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Milestones[0].Status != domain.MilestoneStatusCompleted || plan.Milestones[0].Notes != "done" {
		t.Fatalf("unexpected updated plan: %#v", plan)
	}
	if _, err := updatedMilestonePlan(plan, tools.Request{Args: map[string]string{"ref": "missing", "status": "completed"}}); err == nil {
		t.Fatal("expected missing milestone error")
	}
}

func TestUpdateItemActiveMilestoneErrorExplainsSwitching(t *testing.T) {
	plan := store.MilestonePlan{
		Summary: "Ship it",
		Milestones: []store.Milestone{
			{Ref: "alpha", Title: "Alpha", Status: domain.MilestoneStatusInProgress, Position: 0},
			{Ref: "beta", Title: "Beta", Status: domain.MilestoneStatusPending, Position: 1},
		},
	}

	_, err := updatedMilestonePlan(plan, tools.Request{Args: map[string]string{"ref": "beta", "status": "in_progress"}})
	if err == nil {
		t.Fatal("expected active milestone error")
	}
	for _, want := range []string{
		"active milestones: alpha (in_progress), beta (in_progress)",
		"To switch milestones, first update the current active milestone to pending, blocked, or completed",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected %q in error %q", want, err.Error())
		}
	}
}

func TestUpdateItemRefusesCompletedMilestoneWithIncompleteTodos(t *testing.T) {
	runtime, st, session := newMilestoneRuntime(t)
	seedPlan(t, st, session.ID)
	if _, err := st.AddTodoItems(context.Background(), session.ID, "alpha", []string{"Write tests"}); err != nil {
		t.Fatal(err)
	}

	_, err := (updateItemTool{}).Execute(context.Background(), runtime, tools.Request{
		Tool: domain.ToolKindMilestoneUpdate,
		Args: map[string]string{"ref": "alpha", "status": "completed"},
	})
	if err == nil || !strings.Contains(err.Error(), "cannot complete milestone") {
		t.Fatalf("expected completion guard error, got %v", err)
	}

	if _, err := (updateItemTool{}).PersistResult(context.Background(), st, session.ID, tools.Request{
		Tool: domain.ToolKindMilestoneUpdate,
		Args: map[string]string{"ref": "alpha", "status": "completed"},
	}, tools.Result{Output: "done"}); err == nil || !strings.Contains(err.Error(), "cannot complete milestone") {
		t.Fatalf("expected persist completion guard error, got %v", err)
	}
}

func TestUpdateItemAllowsCompletedMilestoneWhenTodosAreComplete(t *testing.T) {
	runtime, st, session := newMilestoneRuntime(t)
	seedPlan(t, st, session.ID)
	items, err := st.AddTodoItems(context.Background(), session.ID, "alpha", []string{"Write tests"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpdateTodoItem(context.Background(), items[0].ID, domain.TodoStatusCompleted, items[0].Content); err != nil {
		t.Fatal(err)
	}

	result, err := (updateItemTool{}).Execute(context.Background(), runtime, tools.Request{
		Tool: domain.ToolKindMilestoneUpdate,
		Args: map[string]string{"ref": "alpha", "status": "completed"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "completed") {
		t.Fatalf("expected completed milestone output, got %q", result.Output)
	}
}

func TestListAndAddExecute(t *testing.T) {
	runtime, st, session := newMilestoneRuntime(t)
	seedPlan(t, st, session.ID)

	result, err := (listTool{}).Execute(context.Background(), runtime, tools.Request{Tool: domain.ToolKindMilestoneList})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "Alpha") {
		t.Fatalf("expected list output to contain milestone title, got %q", result.Output)
	}

	result, err = (addItemsTool{}).Execute(context.Background(), runtime, tools.Request{
		Tool: domain.ToolKindMilestoneAdd,
		Args: map[string]string{"items": `[{"ref":"beta","title":"Beta"}]`},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "Beta") {
		t.Fatalf("expected add output to contain new milestone, got %q", result.Output)
	}
}

func TestPlanAndWritePersist(t *testing.T) {
	_, st, session := newMilestoneRuntime(t)
	seedPlan(t, st, session.ID)

	if _, err := (planTool{}).PersistResult(context.Background(), st, session.ID, tools.Request{
		Tool: domain.ToolKindMilestonePlan,
		Args: map[string]string{
			"ref":   "alpha",
			"title": "Alpha",
			"items": `[{"content":"one"},{"content":"two"}]`,
		},
	}, tools.Result{Output: "planned"}); err != nil {
		t.Fatal(err)
	}
	todos, err := st.ListTodos(context.Background(), session.ID, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if len(todos) != 2 {
		t.Fatalf("expected todo items to be created, got %#v", todos)
	}

	if _, err := (writeTool{}).PersistResult(context.Background(), st, session.ID, tools.Request{
		Tool: domain.ToolKindMilestoneWrite,
		Args: map[string]string{
			"summary":    "New plan",
			"milestones": `[{"ref":"gamma","title":"Gamma","status":"pending"}]`,
		},
	}, tools.Result{Output: "rewritten"}); err != nil {
		t.Fatal(err)
	}
	plan, err := st.GetMilestonePlan(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Summary != "New plan" || len(plan.Milestones) != 1 || plan.Milestones[0].Ref != "gamma" {
		t.Fatalf("unexpected persisted plan: %#v", plan)
	}
}

func TestPlanPersistStoresRealTodoIDsInOutput(t *testing.T) {
	_, st, session := newMilestoneRuntime(t)
	seedPlan(t, st, session.ID)

	result, err := (planTool{}).Execute(context.Background(), tools.Runtime{Store: st, SessionID: session.ID}, tools.Request{
		Tool: domain.ToolKindMilestonePlan,
		Args: map[string]string{
			"ref":   "alpha",
			"title": "Alpha",
			"items": `[{"content":"one"},{"content":"two"}]`,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "#0 one") {
		t.Fatalf("expected execute preview to contain zero placeholder id, got %q", result.Output)
	}

	events, err := (planTool{}).PersistResult(context.Background(), st, session.ID, tools.Request{
		Tool: domain.ToolKindMilestonePlan,
		Args: map[string]string{
			"ref":   "alpha",
			"title": "Alpha",
			"items": `[{"content":"one"},{"content":"two"}]`,
		},
	}, result)
	if err != nil {
		t.Fatal(err)
	}
	event := <-events
	if strings.Contains(event.Text, "#0") || !strings.Contains(event.Text, "#1 one") || !strings.Contains(event.Text, "#2 two") {
		t.Fatalf("expected persisted event to contain real todo ids, got %q", event.Text)
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
		t.Fatalf("expected one tool item, got %d", len(items))
	}
	exec, ok := items[0].Content.(domain.ToolExecution)
	if !ok || exec.Result == nil {
		t.Fatalf("expected tool execution, got %#v", items[0])
	}
	body := exec.Result.Text
	if strings.Contains(body, "#0") || !strings.Contains(body, "#1 one") || !strings.Contains(body, "#2 two") {
		t.Fatalf("expected persisted body to contain real todo ids, got %q", body)
	}
}
