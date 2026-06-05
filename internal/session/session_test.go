package session

import (
	"context"
	"strings"
	"testing"

	chatpkg "github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/chatrole"
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
		{Ref: "alpha", Title: "Alpha", Status: planning.MilestoneStatusReady},
		{Ref: "beta", Title: "Beta", Status: planning.MilestoneStatusReady},
	}}); err != nil {
		t.Fatal(err)
	}
	alphaTodos, err := AddTodoItems(ctx, st, sessionRecord.ID, "alpha", []string{"alpha task"})
	if err != nil {
		t.Fatal(err)
	}
	betaTodos, err := AddTodoItems(ctx, st, sessionRecord.ID, "beta", []string{"beta task"})
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
		t.Fatalf("expected alpha-only tasks, got %#v", todos)
	}
	if _, err := control.ListTodos(ctx, sessionRecord.ID, "beta"); err == nil || !strings.Contains(err.Error(), `scoped to milestone "alpha"`) {
		t.Fatalf("expected beta scope error, got %v", err)
	}
	if _, err := control.UpdateTodoItem(ctx, betaTodos[0].ID, planning.TodoStatusInProgress, "", "starting work"); err == nil || !strings.Contains(err.Error(), `scoped to milestone "alpha"`) {
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
	if err := PutPlan(ctx, st, planning.Plan{SessionID: sessionRecord.ID, Summary: "Plan", Milestones: []planning.Milestone{{Ref: "alpha", Title: "Alpha", Status: planning.MilestoneStatusReady}}}); err != nil {
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
		t.Fatalf("expected assigned task only, got %#v", listed)
	}
	if _, err := control.AddTodoItems(ctx, sessionRecord.ID, "alpha", []string{"third"}); err == nil || !strings.Contains(err.Error(), "scoped to task") {
		t.Fatalf("expected add task scope error, got %v", err)
	}
}

func TestSessionHydratesAllChatRuntimesOnce(t *testing.T) {
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
	first, err := CreateChat(ctx, st, sessionRecord.ID, "first", chatrole.Orchestrator, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := CreateChat(ctx, st, sessionRecord.ID, "second", chatrole.Execution, &first.ID)
	if err != nil {
		t.Fatal(err)
	}
	loads := map[domain.ID]int{}
	owner, err := Load(ctx, st, func(_ context.Context, session domain.Session, chatRecord domain.Chat) (*chatpkg.Chat, error) {
		loads[chatRecord.ID]++
		return chatpkg.New(session, chatRecord, nil, nil, chatpkg.Deps{Store: st}, nil)
	}, sessionRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Chat(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Chat(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if loads[first.ID] != 1 {
		t.Fatalf("expected first chat to load once from memory cache, got loads=%#v", loads)
	}
	if loads[second.ID] != 1 {
		t.Fatalf("expected second chat to hydrate during session load, got loads=%#v", loads)
	}
}

func TestForkChatAtCopiesTimelinePrefix(t *testing.T) {
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
	chats, err := ListChats(ctx, st, sessionRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(chats) != 1 {
		t.Fatalf("expected initial chat, got %#v", chats)
	}
	source := chats[0]
	if _, err := chatpkg.AppendTimeline(ctx, st, source.ID, domain.UserMessage{Text: "first"}); err != nil {
		t.Fatal(err)
	}
	anchor, err := chatpkg.AppendTimeline(ctx, st, source.ID, domain.AssistantMessage{Text: "second"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := chatpkg.AppendTimeline(ctx, st, source.ID, domain.UserMessage{Text: "third"}); err != nil {
		t.Fatal(err)
	}
	owner, err := Load(ctx, st, func(_ context.Context, session domain.Session, chatRecord domain.Chat) (*chatpkg.Chat, error) {
		return chatpkg.Load(ctx, session, chatRecord, chatpkg.Deps{Store: st}, nil)
	}, sessionRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	fork, err := owner.ForkChatAt(ctx, source.ID, anchor.ID, "forked")
	if err != nil {
		t.Fatal(err)
	}
	forkedTimeline, err := chatpkg.TimelineForChat(ctx, st, fork.Snapshot().Chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(forkedTimeline) != 2 {
		t.Fatalf("expected two forked items, got %#v", forkedTimeline)
	}
	if forkedTimeline[0].ChatID != fork.Snapshot().Chat.ID || forkedTimeline[1].ChatID != fork.Snapshot().Chat.ID {
		t.Fatalf("forked items have wrong chat id: %#v", forkedTimeline)
	}
	if forkedTimeline[0].ID == anchor.ID || forkedTimeline[1].ID == anchor.ID {
		t.Fatalf("expected copied timeline items to get new ids, got %#v", forkedTimeline)
	}
	if got := forkedTimeline[1].Content.(domain.AssistantMessage).Text; got != "second" {
		t.Fatalf("expected copied anchor content, got %q", got)
	}
	sourceTimeline, err := chatpkg.TimelineForChat(ctx, st, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(sourceTimeline) != 3 {
		t.Fatalf("expected source timeline to remain intact, got %#v", sourceTimeline)
	}
}
