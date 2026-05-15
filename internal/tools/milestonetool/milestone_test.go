package milestonetool

import (
	"context"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/chatrole"
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
	return tools.Runtime{Store: st, SessionID: session.ID, ChatRole: chatrole.Orchestrator}, st, session
}

func seedPlan(t *testing.T, st *store.Store, sessionID domain.ID) {
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
	if _, enabled := tools.DefinitionFor(domain.ToolKindMilestoneAdd, tools.Runtime{ChatRole: chatrole.Execution}); enabled {
		t.Fatal("expected add-items definition to be disabled in execution chats")
	}
	if _, enabled := tools.DefinitionFor(domain.ToolKindMilestonePlan, tools.Runtime{ChatRole: chatrole.Decomposition}); enabled {
		t.Fatal("expected plan definition to be disabled in decomposition chats")
	}
	updated, err := (updateItemTool{}).NormalizeArgs(map[string]string{"ref": "alpha", "status": "cancelled"})
	if err != nil {
		t.Fatal(err)
	}
	if updated["status"] != string(domain.MilestoneStatusCancelled) {
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
	parsed, err := tools.ParseMilestones(`[{"ref":"alpha","title":"Alpha","status":"cancelled"}]`)
	if err != nil {
		t.Fatal(err)
	}
	if parsed[0].Status != domain.MilestoneStatusCancelled {
		t.Fatalf("expected cancelled milestone status, got %#v", parsed[0])
	}

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
	if err := tools.ValidateMilestoneProgress([]store.Milestone{
		{Ref: "alpha", Title: "Alpha", Status: domain.MilestoneStatusExecuting},
		{Ref: "beta", Title: "Beta", Status: domain.MilestoneStatusExecuting},
	}); err != nil {
		t.Fatalf("expected multiple active milestones to be allowed, got %v", err)
	}
}

func TestUpsertAndUpdatedPlanHelpers(t *testing.T) {
	items := upsertMilestone([]store.Milestone{{Ref: "alpha", Title: "Alpha", Position: 0}}, store.Milestone{
		Ref:    "alpha",
		Title:  "Alpha updated",
		Status: domain.MilestoneStatusReady,
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
	}, tools.Request{Args: map[string]string{"ref": "alpha", "status": "completed", "notes": "done"}}, domain.Chat{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Milestones[0].Status != domain.MilestoneStatusCompleted || plan.Milestones[0].Notes != "done" {
		t.Fatalf("unexpected updated plan: %#v", plan)
	}
	if _, err := updatedMilestonePlan(plan, tools.Request{Args: map[string]string{"ref": "missing", "status": "completed"}}, domain.Chat{}); err == nil {
		t.Fatal("expected missing milestone error")
	}
}

func TestUpdateItemAllowsMultipleActiveMilestones(t *testing.T) {
	plan := store.MilestonePlan{
		Summary: "Ship it",
		Milestones: []store.Milestone{
			{Ref: "alpha", Title: "Alpha", Status: domain.MilestoneStatusExecuting, Position: 0},
			{Ref: "beta", Title: "Beta", Status: domain.MilestoneStatusPending, Position: 1},
		},
	}

	updated, err := updatedMilestonePlan(plan, tools.Request{Args: map[string]string{"ref": "beta", "status": "executing"}}, domain.Chat{})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Milestones[0].Status != domain.MilestoneStatusExecuting || updated.Milestones[1].Status != domain.MilestoneStatusExecuting {
		t.Fatalf("expected both milestones to remain active, got %#v", updated.Milestones)
	}
}

func TestUpdateItemEnforcesMilestoneOwnership(t *testing.T) {
	ownerID := domain.NewID()
	otherID := domain.NewID()
	plan := store.MilestonePlan{
		Summary: "Ship it",
		Milestones: []store.Milestone{
			{Ref: "alpha", Title: "Alpha", Status: domain.MilestoneStatusExecuting, OwnerChatID: &ownerID, Position: 0},
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
	ownerID := domain.NewID()
	plan := store.MilestonePlan{
		Summary: "Ship it",
		Milestones: []store.Milestone{
			{Ref: "alpha", Title: "Alpha", Status: domain.MilestoneStatusReady, Position: 0},
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
	if _, err := st.SetMilestonePlan(context.Background(), session.ID, "Ship it", []store.Milestone{
		{Ref: "alpha", Title: "Alpha", Status: domain.MilestoneStatusExecuting, Position: 0},
		{Ref: "beta", Title: "Beta", Status: domain.MilestoneStatusExecuting, Position: 1},
	}); err != nil {
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

	events, err := (updateItemTool{}).PersistResult(context.Background(), st, session.ID, tools.Request{
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
	if !strings.Contains(result.Output, "# one") {
		t.Fatalf("expected execute preview to contain todo content, got %q", result.Output)
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
	if !strings.Contains(event.Text, " one") || !strings.Contains(event.Text, " two") {
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
	if !strings.Contains(body, " one") || !strings.Contains(body, " two") {
		t.Fatalf("expected persisted body to contain real todo ids, got %q", body)
	}
}
