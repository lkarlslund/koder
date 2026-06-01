package session

import (
	"context"
	"strings"
	"testing"

	chatpkg "github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/planning"
	"github.com/lkarlslund/koder/internal/store"
)

func TestScopedPlanningLimitsMilestonesAndTodos(t *testing.T) {
	ctx := context.Background()
	st, err := store.OpenWithOptions(t.TempDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	sessionRecord, err := CreateSession(ctx, st, "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := PutPlan(ctx, st, planning.Plan{SessionID: sessionRecord.ID, Summary: "Plan", Milestones: []planning.Milestone{
		{Ref: "alpha", Title: "Alpha", Status: domain.MilestoneStatusReady},
		{Ref: "beta", Title: "Beta", Status: domain.MilestoneStatusReady},
	}}); err != nil {
		t.Fatal(err)
	}
	alphaTodos, err := AddTodoItems(ctx, st, sessionRecord.ID, "alpha", []string{"alpha todo"})
	if err != nil {
		t.Fatal(err)
	}
	betaTodos, err := AddTodoItems(ctx, st, sessionRecord.ID, "beta", []string{"beta todo"})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := Load(ctx, st, func(context.Context, domain.Session, domain.Chat) (*chatpkg.Chat, error) { return nil, nil }, sessionRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	control := owner.PlanningForChat(domain.Chat{SessionID: sessionRecord.ID, ActiveMilestoneRef: "alpha"})
	plan, err := control.GetMilestonePlan(ctx, sessionRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Milestones) != 1 || plan.Milestones[0].Ref != "alpha" {
		t.Fatalf("expected alpha-only plan, got %#v", plan.Milestones)
	}
	todos, err := control.ListTodos(ctx, sessionRecord.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(todos) != 1 || todos[0].ID != alphaTodos[0].ID {
		t.Fatalf("expected alpha-only todos, got %#v", todos)
	}
	if _, err := control.ListTodos(ctx, sessionRecord.ID, "beta"); err == nil || !strings.Contains(err.Error(), `scoped to milestone "alpha"`) {
		t.Fatalf("expected beta scope error, got %v", err)
	}
	if _, err := control.UpdateTodoItem(ctx, betaTodos[0].ID, domain.TodoStatusInProgress, ""); err == nil || !strings.Contains(err.Error(), `scoped to milestone "alpha"`) {
		t.Fatalf("expected beta update scope error, got %v", err)
	}
}

func TestScopedPlanningLimitsAssignedTodo(t *testing.T) {
	ctx := context.Background()
	st, err := store.OpenWithOptions(t.TempDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	sessionRecord, err := CreateSession(ctx, st, "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := PutPlan(ctx, st, planning.Plan{SessionID: sessionRecord.ID, Summary: "Plan", Milestones: []planning.Milestone{{Ref: "alpha", Title: "Alpha", Status: domain.MilestoneStatusReady}}}); err != nil {
		t.Fatal(err)
	}
	todos, err := AddTodoItems(ctx, st, sessionRecord.ID, "alpha", []string{"first", "second"})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := Load(ctx, st, func(context.Context, domain.Session, domain.Chat) (*chatpkg.Chat, error) { return nil, nil }, sessionRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	control := owner.PlanningForChat(domain.Chat{SessionID: sessionRecord.ID, ActiveMilestoneRef: "alpha", AssignedTodoRef: todos[0].ID})
	listed, err := control.ListTodos(ctx, sessionRecord.ID, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ID != todos[0].ID {
		t.Fatalf("expected assigned todo only, got %#v", listed)
	}
	if _, err := control.AddTodoItems(ctx, sessionRecord.ID, "alpha", []string{"third"}); err == nil || !strings.Contains(err.Error(), "scoped to todo") {
		t.Fatalf("expected add todo scope error, got %v", err)
	}
}
