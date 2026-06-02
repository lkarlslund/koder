package milestonetool

import (
	"context"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/modeltest"
	"github.com/lkarlslund/koder/internal/planning"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
	"github.com/lkarlslund/koder/internal/tools/tooltest"
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
	session, err := modeltest.CreateSession(context.Background(), st, "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	return tools.Runtime{Store: st, SessionID: session.ID, SessionControl: tooltest.NewSessionControl(st), ChatRole: chatrole.Orchestrator}, st, session
}

func seedPlan(t *testing.T, st *store.Store, sessionID id.ID) {
	t.Helper()
	if err := modeltest.PutPlan(context.Background(), st, planning.Plan{SessionID: sessionID, Summary: "Ship it", Milestones: []planning.Milestone{
		{Ref: "alpha", Title: "Alpha", Status: planning.MilestoneStatusPending, Position: 0},
	}}); err != nil {
		t.Fatal(err)
	}
}

func TestNormalizeArgsAndDefinitions(t *testing.T) {
	listed, err := (listTool{}).NormalizeArgs(map[string]string{"completed": "true"})
	if err != nil {
		t.Fatal(err)
	}
	if listed["completed"] != "true" {
		t.Fatalf("expected completed=true, got %#v", listed)
	}
	if _, err := (listTool{}).NormalizeArgs(map[string]string{"completed": "sometimes"}); err == nil {
		t.Fatal("expected invalid completed boolean error")
	}
	added, err := (addItemsTool{}).NormalizeArgs(map[string]string{"ref": "alpha", "title": "Alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if added["ref"] != "alpha" || added["title"] != "Alpha" {
		t.Fatalf("unexpected add args: %#v", added)
	}
	if _, err := (addItemsTool{}).NormalizeArgs(map[string]string{}); err == nil {
		t.Fatal("expected missing ref error")
	}
	if _, err := (updateItemTool{}).NormalizeArgs(map[string]string{"ref": "alpha"}); err == nil {
		t.Fatal("expected missing status error")
	}
	if _, enabled := tools.DefinitionFor(domain.ToolKindMilestoneAdd, tools.Runtime{ChatRole: chatrole.Execution}); enabled {
		t.Fatal("expected add-items definition to be disabled in execution chats")
	}
	if _, enabled := tools.DefinitionFor(domain.ToolKindMilestonePlan, tools.Runtime{ChatRole: chatrole.Execution}); enabled {
		t.Fatal("expected plan definition to be disabled in execution chats")
	}
	updated, err := (updateItemTool{}).NormalizeArgs(map[string]string{"ref": "alpha", "status": "cancelled"})
	if err != nil {
		t.Fatal(err)
	}
	if updated["status"] != planning.MilestoneStatusCancelled.String() {
		t.Fatalf("expected cancelled status, got %#v", updated)
	}
	def, enabled := tools.DefinitionFor(domain.ToolKindMilestoneUpdate, tools.Runtime{ChatRole: chatrole.Orchestrator})
	if !enabled {
		t.Fatal("expected update milestone definition to be enabled")
	}
	if !strings.Contains(string(def.Function.Parameters), `"cancelled"`) || !strings.Contains(def.Function.Description, "created by accident") {
		t.Fatalf("expected cancelled status and guidance in LLM definition: %#v", def)
	}
}

func TestAppendAndValidationHelpers(t *testing.T) {
	parsed, err := planning.ParseMilestones(`[{"ref":"alpha","title":"Alpha","status":"cancelled"}]`)
	if err != nil {
		t.Fatal(err)
	}
	if parsed[0].Status != planning.MilestoneStatusCancelled {
		t.Fatalf("expected cancelled milestone status, got %#v", parsed[0])
	}

	existing := []planning.Milestone{{Ref: "alpha", Title: "Alpha", Position: 0}}
	added := []planning.Milestone{{Ref: "beta", Title: "Beta"}}
	got := appendMilestones(existing, added)
	if len(got) != 2 || got[1].Position != 1 {
		t.Fatalf("unexpected appended milestones: %#v", got)
	}
	if err := ensureMilestoneRefsAvailable(existing, added); err != nil {
		t.Fatal(err)
	}
	if err := ensureMilestoneRefsAvailable(existing, []planning.Milestone{{Ref: "alpha", Title: "dup"}}); err == nil {
		t.Fatal("expected duplicate ref error")
	}
	if err := ensureMilestoneRefsAvailable(existing, []planning.Milestone{{Ref: "beta", Title: "  alpha  "}}); err == nil {
		t.Fatal("expected duplicate title error")
	}
	if err := planning.ValidateMilestoneProgress([]planning.Milestone{
		{Ref: "alpha", Title: "Alpha", Status: planning.MilestoneStatusExecuting},
		{Ref: "beta", Title: "Beta", Status: planning.MilestoneStatusExecuting},
	}); err != nil {
		t.Fatalf("expected multiple active milestones to be allowed, got %v", err)
	}
	if err := planning.ValidateMilestoneProgress([]planning.Milestone{
		{Ref: "alpha", Title: "Alpha", Status: planning.MilestoneStatusPending},
		{Ref: "beta", Title: " alpha ", Status: planning.MilestoneStatusPending},
	}); err == nil {
		t.Fatal("expected duplicate title validation error")
	}
}

func TestMilestoneAddRejectsDuplicateTitle(t *testing.T) {
	runtime, st, session := newMilestoneRuntime(t)
	seedPlan(t, st, session.ID)
	req := tools.Request{
		Tool: domain.ToolKindMilestoneAdd,
		Args: map[string]string{"ref": "beta", "title": "  alpha  "},
	}
	_, err := tools.Execute(context.Background(), runtime, req)
	if err == nil || !strings.Contains(err.Error(), "duplicate milestone title") {
		t.Fatalf("expected duplicate milestone title error, got %v", err)
	}
}

func TestUpsertAndUpdatedPlanHelpers(t *testing.T) {
	items := upsertMilestone([]planning.Milestone{{Ref: "alpha", Title: "Alpha", Position: 0}}, planning.Milestone{
		Ref:    "alpha",
		Title:  "Alpha updated",
		Status: planning.MilestoneStatusReady,
	})
	if items[0].Title != "Alpha updated" || items[0].Position != 0 {
		t.Fatalf("unexpected updated milestone: %#v", items[0])
	}
	items = upsertMilestone(items, planning.Milestone{Ref: "beta", Title: "Beta"})
	if len(items) != 2 || items[1].Position != 1 {
		t.Fatalf("unexpected appended milestone: %#v", items)
	}

	plan, err := updatedMilestonePlan(planning.Plan{
		Summary: "Ship it",
		Milestones: []planning.Milestone{
			{Ref: "alpha", Title: "Alpha", Status: planning.MilestoneStatusPending, Position: 0},
		},
	}, tools.Request{Args: map[string]string{"ref": "alpha", "status": "completed", "notes": "done"}}, domain.Chat{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Milestones[0].Status != planning.MilestoneStatusCompleted || plan.Milestones[0].Notes != "done" {
		t.Fatalf("unexpected updated plan: %#v", plan)
	}
	if _, err := updatedMilestonePlan(plan, tools.Request{Args: map[string]string{"ref": "missing", "status": "completed"}}, domain.Chat{}); err == nil {
		t.Fatal("expected missing milestone error")
	}
}

func TestUpdateItemAllowsMultipleActiveMilestones(t *testing.T) {
	plan := planning.Plan{
		Summary: "Ship it",
		Milestones: []planning.Milestone{
			{Ref: "alpha", Title: "Alpha", Status: planning.MilestoneStatusExecuting, Position: 0},
			{Ref: "beta", Title: "Beta", Status: planning.MilestoneStatusPending, Position: 1},
		},
	}

	updated, err := updatedMilestonePlan(plan, tools.Request{Args: map[string]string{"ref": "beta", "status": "executing"}}, domain.Chat{})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Milestones[0].Status != planning.MilestoneStatusExecuting || updated.Milestones[1].Status != planning.MilestoneStatusExecuting {
		t.Fatalf("expected both milestones to remain active, got %#v", updated.Milestones)
	}
}

func TestUpdateItemEnforcesMilestoneOwnership(t *testing.T) {
	ownerID := id.New()
	otherID := id.New()
	plan := planning.Plan{
		Summary: "Ship it",
		Milestones: []planning.Milestone{
			{Ref: "alpha", Title: "Alpha", Status: planning.MilestoneStatusExecuting, OwnerChatID: &ownerID, Position: 0},
		},
	}

	if _, err := updatedMilestonePlan(plan, tools.Request{Args: map[string]string{"ref": "alpha", "status": "completed"}}, domain.Chat{
		ID:           otherID,
		WorkflowRole: chatrole.Execution,
	}); err == nil || !strings.Contains(err.Error(), "owned by chat") {
		t.Fatalf("expected ownership error, got %v", err)
	}

	updated, err := updatedMilestonePlan(plan, tools.Request{Args: map[string]string{"ref": "alpha", "status": "completed"}}, domain.Chat{
		ID:           ownerID,
		WorkflowRole: chatrole.Execution,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Milestones[0].OwnerChatID != nil {
		t.Fatalf("expected completed milestone to release owner, got %#v", updated.Milestones[0].OwnerChatID)
	}
}

func TestUpdateItemAssignsOwnerForActiveScopedMilestone(t *testing.T) {
	ownerID := id.New()
	plan := planning.Plan{
		Summary: "Ship it",
		Milestones: []planning.Milestone{
			{Ref: "alpha", Title: "Alpha", Status: planning.MilestoneStatusReady, Position: 0},
		},
	}

	updated, err := updatedMilestonePlan(plan, tools.Request{Args: map[string]string{"ref": "alpha", "status": "executing"}}, domain.Chat{
		ID:           ownerID,
		WorkflowRole: chatrole.Execution,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Milestones[0].OwnerChatID == nil || *updated.Milestones[0].OwnerChatID != ownerID {
		t.Fatalf("expected executing milestone to be owned by actor, got %#v", updated.Milestones[0].OwnerChatID)
	}
}

func TestScopedExecutionChatSeesOnlyAssignedMilestone(t *testing.T) {
	runtime, st, session := newMilestoneRuntime(t)
	runtime.ChatRole = chatrole.Execution
	runtime.ActiveMilestoneRef = "beta"
	if err := modeltest.PutPlan(context.Background(), st, planning.Plan{SessionID: session.ID, Summary: "Ship it", Milestones: []planning.Milestone{
		{Ref: "alpha", Title: "Alpha", Status: planning.MilestoneStatusExecuting, Position: 0},
		{Ref: "beta", Title: "Beta", Status: planning.MilestoneStatusExecuting, Position: 1},
	}}); err != nil {
		t.Fatal(err)
	}

	result, err := (listTool{}).Execute(context.Background(), runtime, tools.Request{Tool: domain.ToolKindMilestoneList})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.Output, "Alpha") || !strings.Contains(result.Output, "Beta") {
		t.Fatalf("expected scoped milestone list, got %q", result.Output)
	}

	result, err = (updateItemTool{}).Execute(context.Background(), runtime, tools.Request{
		Tool: domain.ToolKindMilestoneUpdate,
		Args: map[string]string{"ref": "beta", "status": "completed"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.Output, "Alpha") || !strings.Contains(result.Output, "Beta") {
		t.Fatalf("expected scoped milestone update output, got %q", result.Output)
	}
	if _, err := (updateItemTool{}).Execute(context.Background(), runtime, tools.Request{
		Tool: domain.ToolKindMilestoneUpdate,
		Args: map[string]string{"ref": "alpha", "status": "completed"},
	}); err == nil || !strings.Contains(err.Error(), `scoped to milestone "beta"`) {
		t.Fatalf("expected scoped milestone error, got %v", err)
	}

	events, err := (updateItemTool{}).PersistResult(context.Background(), runtime, tools.Request{
		Tool: domain.ToolKindMilestoneUpdate,
		Args: map[string]string{"ref": "beta", "status": "executing"},
	}, tools.Result{Output: result.Output, Stored: result.Stored})
	if err != nil {
		t.Fatal(err)
	}
	event := <-events
	if strings.Contains(event.Text, "Alpha") || !strings.Contains(event.Text, "Beta") {
		t.Fatalf("expected persisted scoped milestone result, got %q", event.Text)
	}
}

func TestUpdateItemRefusesCompletedMilestoneWithIncompleteTodos(t *testing.T) {
	runtime, st, session := newMilestoneRuntime(t)
	seedPlan(t, st, session.ID)
	if _, err := modeltest.AddTodoItems(context.Background(), st, session.ID, "alpha", []string{"Write tests"}); err != nil {
		t.Fatal(err)
	}

	_, err := (updateItemTool{}).Execute(context.Background(), runtime, tools.Request{
		Tool: domain.ToolKindMilestoneUpdate,
		Args: map[string]string{"ref": "alpha", "status": "completed"},
	})
	if err == nil || !strings.Contains(err.Error(), "cannot complete milestone") {
		t.Fatalf("expected completion guard error, got %v", err)
	}

	if _, err := (updateItemTool{}).PersistResult(context.Background(), runtime, tools.Request{
		Tool: domain.ToolKindMilestoneUpdate,
		Args: map[string]string{"ref": "alpha", "status": "completed"},
	}, tools.Result{Output: "done"}); err == nil || !strings.Contains(err.Error(), "cannot complete milestone") {
		t.Fatalf("expected persist completion guard error, got %v", err)
	}
}

func TestUpdateItemAllowsCompletedMilestoneWhenTodosAreComplete(t *testing.T) {
	runtime, st, session := newMilestoneRuntime(t)
	seedPlan(t, st, session.ID)
	items, err := modeltest.AddTodoItems(context.Background(), st, session.ID, "alpha", []string{"Write tests"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := modeltest.UpdateTodo(context.Background(), st, items[0].ID, planning.TodoStatusCompleted, items[0].Content, "completed in setup"); err != nil {
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
	if err := modeltest.PutPlan(context.Background(), st, planning.Plan{SessionID: session.ID, Summary: "Ship it", Milestones: []planning.Milestone{
		{Ref: "alpha", Title: "Alpha", Status: planning.MilestoneStatusCompleted, Position: 0},
		{Ref: "beta", Title: "Beta", Status: planning.MilestoneStatusReady, Position: 1},
		{Ref: "gamma", Title: "Gamma", Status: planning.MilestoneStatusExecuting, Position: 2},
	}}); err != nil {
		t.Fatal(err)
	}
	betaTodos, err := modeltest.AddTodoItems(context.Background(), st, session.ID, "beta", []string{"First", "Second"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := modeltest.UpdateTodo(context.Background(), st, betaTodos[0].ID, planning.TodoStatusCompleted, "", "done in setup"); err != nil {
		t.Fatal(err)
	}

	result, err := (listTool{}).Execute(context.Background(), runtime, tools.Request{Tool: domain.ToolKindMilestoneList})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.Output, "Alpha") || !strings.Contains(result.Output, "Beta") || !strings.Contains(result.Output, "Gamma") {
		t.Fatalf("expected default list output to hide completed milestones, got %q", result.Output)
	}
	if !strings.Contains(result.Output, "Milestones summary: 1 ready, 1 executing, 1 completed") {
		t.Fatalf("expected milestone status summary, got %q", result.Output)
	}
	if !strings.Contains(result.Output, "[ready] Beta (beta) - todos: 1 pending, 1 completed") {
		t.Fatalf("expected beta todo summary, got %q", result.Output)
	}
	if !strings.Contains(result.Output, "[executing] Gamma (gamma) - no todos added to milestone") {
		t.Fatalf("expected empty gamma todo summary, got %q", result.Output)
	}

	result, err = (listTool{}).Execute(context.Background(), runtime, tools.Request{
		Tool: domain.ToolKindMilestoneList,
		Args: map[string]string{"completed": "true"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "Alpha") {
		t.Fatalf("expected completed=true list output to include completed milestone, got %q", result.Output)
	}

	result, err = (addItemsTool{}).Execute(context.Background(), runtime, tools.Request{
		Tool: domain.ToolKindMilestoneAdd,
		Args: map[string]string{"ref": "delta", "title": "Delta"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "Delta") {
		t.Fatalf("expected add output to contain new milestone, got %q", result.Output)
	}
}

func TestAddAndWritePersist(t *testing.T) {
	runtime, st, session := newMilestoneRuntime(t)
	seedPlan(t, st, session.ID)

	if _, err := (addItemsTool{}).PersistResult(context.Background(), runtime, tools.Request{
		Tool: domain.ToolKindMilestoneAdd,
		Args: map[string]string{"ref": "beta", "title": "Beta"},
	}, tools.Result{Output: "added"}); err != nil {
		t.Fatal(err)
	}
	plan, err := modeltest.GetPlan(context.Background(), st, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Milestones) != 2 || plan.Milestones[1].Ref != "beta" || plan.Milestones[1].Status != planning.MilestoneStatusPending {
		t.Fatalf("expected blank pending milestone to be created, got %#v", plan)
	}

	if _, err := (writeTool{}).PersistResult(context.Background(), runtime, tools.Request{
		Tool: domain.ToolKindMilestoneWrite,
		Args: map[string]string{
			"summary":    "New plan",
			"milestones": `[{"ref":"gamma","title":"Gamma","status":"pending"}]`,
		},
	}, tools.Result{Output: "rewritten"}); err != nil {
		t.Fatal(err)
	}
	plan, err = modeltest.GetPlan(context.Background(), st, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Summary != "New plan" || len(plan.Milestones) != 1 || plan.Milestones[0].Ref != "gamma" {
		t.Fatalf("unexpected persisted plan: %#v", plan)
	}
}
