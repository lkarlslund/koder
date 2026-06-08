package session

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	chatpkg "github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/planning"
	"github.com/lkarlslund/koder/internal/store"
)

func testAppendTimeline(ctx context.Context, st *store.Store, sessionRecord domain.Session, chatRecord domain.Chat, content domain.TimelineContent) (domain.TimelineItem, error) {
	rt, err := chatpkg.Load(ctx, sessionRecord, chatRecord, chatpkg.Deps{Store: st}, nil)
	if err != nil {
		return domain.TimelineItem{}, err
	}
	return rt.AppendTimelineContent(ctx, content)
}

func testAddTodoItems(ctx context.Context, st *store.Store, sessionID id.ID, milestoneRef string, contents []string) ([]planning.TodoItem, error) {
	existing, err := planning.ListTodos(ctx, st, sessionID, milestoneRef)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	items := make([]planning.TodoItem, 0, len(contents))
	for _, content := range contents {
		content = strings.TrimSpace(content)
		if content == "" {
			continue
		}
		items = append(items, planning.TodoItem{
			ID:           id.NewAt(now),
			SessionID:    sessionID,
			MilestoneRef: milestoneRef,
			Content:      content,
			Status:       planning.TodoStatusPending,
			Position:     len(existing) + len(items),
			CreatedAt:    now,
			UpdatedAt:    now,
		})
	}
	for _, item := range items {
		if err := planning.SaveTodo(ctx, st, item); err != nil {
			return nil, err
		}
	}
	return items, nil
}

func testUpdateTodo(ctx context.Context, st *store.Store, sessionID, todoID id.ID, status planning.TodoStatus, note string) (planning.TodoItem, error) {
	todos, err := planning.ListTodos(ctx, st, sessionID, "")
	if err != nil {
		return planning.TodoItem{}, err
	}
	for _, item := range todos {
		if item.ID != todoID {
			continue
		}
		item.Status = status
		item.Note = note
		item.UpdatedAt = time.Now().UTC()
		return item, planning.SaveTodo(ctx, st, item)
	}
	return planning.TodoItem{}, fmt.Errorf("task %s not found", todoID)
}

func TestScopedPlanningLimitsMilestonesAndTodos(t *testing.T) {
	ctx := context.Background()
	st, err := store.OpenWithOptions(t.TempDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	sessionRecord, err := createSessionRecord(ctx, st, "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := planning.SavePlan(ctx, st, planning.Plan{SessionID: sessionRecord.ID, Summary: "Plan", Milestones: []planning.Milestone{
		{Ref: "alpha", Title: "Alpha", Status: planning.MilestoneStatusReady},
		{Ref: "beta", Title: "Beta", Status: planning.MilestoneStatusReady},
	}}); err != nil {
		t.Fatal(err)
	}
	alphaTodos, err := testAddTodoItems(ctx, st, sessionRecord.ID, "alpha", []string{"alpha task"})
	if err != nil {
		t.Fatal(err)
	}
	betaTodos, err := testAddTodoItems(ctx, st, sessionRecord.ID, "beta", []string{"beta task"})
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
	sessionRecord, err := createSessionRecord(ctx, st, "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := planning.SavePlan(ctx, st, planning.Plan{SessionID: sessionRecord.ID, Summary: "Plan", Milestones: []planning.Milestone{{Ref: "alpha", Title: "Alpha", Status: planning.MilestoneStatusReady}}}); err != nil {
		t.Fatal(err)
	}
	todos, err := testAddTodoItems(ctx, st, sessionRecord.ID, "alpha", []string{"first", "second"})
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

func TestSessionHydratesTodosWithoutPlanMilestone(t *testing.T) {
	ctx := context.Background()
	st, err := store.OpenWithOptions(t.TempDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	sessionRecord, err := createSessionRecord(ctx, st, "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	created, err := testAddTodoItems(ctx, st, sessionRecord.ID, "alpha", []string{"first", "second"})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := Load(ctx, st, func(context.Context, domain.Session, domain.Chat) (*chatpkg.Chat, error) { return nil, nil }, sessionRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	listed, err := owner.ListTodos(ctx, sessionRecord.ID, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != len(created) {
		t.Fatalf("expected %d tasks, got %#v", len(created), listed)
	}
	for idx := range created {
		if listed[idx].ID != created[idx].ID {
			t.Fatalf("task %d: expected %s, got %s", idx, created[idx].ID, listed[idx].ID)
		}
	}
}

func TestSessionChildIdleNotificationSummarizesMilestoneProgress(t *testing.T) {
	ctx := context.Background()
	st, err := store.OpenWithOptions(t.TempDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	sessionRecord, err := createSessionRecord(ctx, st, "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	todos, err := testAddTodoItems(ctx, st, sessionRecord.ID, "alpha", []string{"first", "second"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := testUpdateTodo(ctx, st, sessionRecord.ID, todos[0].ID, planning.TodoStatusCompleted, "completed in setup"); err != nil {
		t.Fatal(err)
	}
	owner, err := Load(ctx, st, func(context.Context, domain.Session, domain.Chat) (*chatpkg.Chat, error) { return nil, nil }, sessionRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	chatID := id.New()
	got := owner.childIdleNotification(ctx, domain.Chat{ID: chatID, SessionID: sessionRecord.ID, ActiveMilestoneRef: "alpha", ParentChatID: &chatID}, chatID, "Idle")
	want := "Chat " + chatID + " is now idle. Chat completed 1 out of 2 tasks for milestone alpha, but is now stopped."
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestSessionChildIdleNotificationSummarizesCompletedMilestone(t *testing.T) {
	ctx := context.Background()
	st, err := store.OpenWithOptions(t.TempDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	sessionRecord, err := createSessionRecord(ctx, st, "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	todos, err := testAddTodoItems(ctx, st, sessionRecord.ID, "alpha", []string{"first", "second"})
	if err != nil {
		t.Fatal(err)
	}
	for _, todo := range todos {
		if _, err := testUpdateTodo(ctx, st, sessionRecord.ID, todo.ID, planning.TodoStatusCompleted, "completed in setup"); err != nil {
			t.Fatal(err)
		}
	}
	owner, err := Load(ctx, st, func(context.Context, domain.Session, domain.Chat) (*chatpkg.Chat, error) { return nil, nil }, sessionRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	chatID := id.New()
	got := owner.childIdleNotification(ctx, domain.Chat{ID: chatID, SessionID: sessionRecord.ID, ActiveMilestoneRef: "alpha", ParentChatID: &chatID}, chatID, "Idle")
	want := "Chat " + chatID + " is now idle. All 2 tasks for milestone alpha are done."
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestSessionHydratesAllChatRuntimesOnce(t *testing.T) {
	ctx := context.Background()
	st, err := store.OpenWithOptions(t.TempDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	sessionRecord, err := createSessionRecord(ctx, st, "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	first, err := chatpkg.CreateRecord(ctx, st, chatpkg.CreateRecordRequest{Session: sessionRecord, Title: "first", Role: chatrole.Orchestrator, Position: -1})
	if err != nil {
		t.Fatal(err)
	}
	second, err := chatpkg.CreateRecord(ctx, st, chatpkg.CreateRecordRequest{Session: sessionRecord, Title: "second", Role: chatrole.Execution, ParentID: &first.ID, Position: -1})
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
	sessionRecord, err := createSessionRecord(ctx, st, "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	chats, err := chatpkg.ListRecordsForSession(ctx, st, sessionRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(chats) != 1 {
		t.Fatalf("expected initial chat, got %#v", chats)
	}
	source := chats[0]
	if _, err := testAppendTimeline(ctx, st, sessionRecord, source, domain.UserMessage{Text: "first"}); err != nil {
		t.Fatal(err)
	}
	anchor, err := testAppendTimeline(ctx, st, sessionRecord, source, domain.AssistantMessage{Text: "second"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := testAppendTimeline(ctx, st, sessionRecord, source, domain.UserMessage{Text: "third"}); err != nil {
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
	forkedPage, err := fork.TimelinePage(ctx, "", 0, true)
	if err != nil {
		t.Fatal(err)
	}
	forkedTimeline := forkedPage.Items
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
	sourceRuntime, err := owner.Chat(ctx, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	sourcePage, err := sourceRuntime.TimelinePage(ctx, "", 0, true)
	if err != nil {
		t.Fatal(err)
	}
	sourceTimeline := sourcePage.Items
	if len(sourceTimeline) != 3 {
		t.Fatalf("expected source timeline to remain intact, got %#v", sourceTimeline)
	}
}
