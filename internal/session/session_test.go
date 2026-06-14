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

func testSources(st *store.Store) (*chatpkg.Source, *planning.Source) {
	return chatpkg.NewSource(func() chatpkg.Deps { return chatpkg.Deps{Store: st} }), planning.NewSource(st)
}

func testCreateSessionRecord(ctx context.Context, st *store.Store) (domain.Session, *chatpkg.Source, *planning.Source, error) {
	chatsSrc, planSrc := testSources(st)
	sessionRecord, err := createSessionRecord(ctx, st, chatsSrc, "test", "provider", "model", nil)
	return sessionRecord, chatsSrc, planSrc, err
}

func testLoadSession(ctx context.Context, st *store.Store, chatsSrc *chatpkg.Source, planSrc *planning.Source, sessionID id.ID) (*Session, error) {
	return Load(ctx, st, chatsSrc, planSrc, sessionID)
}

func testAddTasks(ctx context.Context, planSrc *planning.Source, sessionID id.ID, milestoneRef string, contents []string) ([]planning.Task, error) {
	existing, err := planSrc.ListTasks(ctx, sessionID, milestoneRef)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	items := make([]planning.Task, 0, len(contents))
	for _, content := range contents {
		content = strings.TrimSpace(content)
		if content == "" {
			continue
		}
		items = append(items, planning.Task{
			ID:           id.NewAt(now),
			SessionID:    sessionID,
			MilestoneRef: milestoneRef,
			Content:      content,
			Status:       planning.TaskStatusPending,
			Position:     len(existing) + len(items),
			CreatedAt:    now,
			UpdatedAt:    now,
		})
	}
	for _, item := range items {
		if err := planSrc.SaveTask(ctx, item); err != nil {
			return nil, err
		}
	}
	return items, nil
}

func testUpdateTask(ctx context.Context, planSrc *planning.Source, sessionID, taskID id.ID, status planning.TaskStatus, note string) (planning.Task, error) {
	tasks, err := planSrc.ListTasks(ctx, sessionID, "")
	if err != nil {
		return planning.Task{}, err
	}
	for _, item := range tasks {
		if item.ID != taskID {
			continue
		}
		item.Status = status
		item.Note = note
		item.UpdatedAt = time.Now().UTC()
		return item, planSrc.SaveTask(ctx, item)
	}
	return planning.Task{}, fmt.Errorf("task %s not found", taskID)
}

func TestScopedPlanningLimitsMilestonesAndTasks(t *testing.T) {
	ctx := context.Background()
	st, err := store.OpenWithOptions(t.TempDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	sessionRecord, chatsSrc, planSrc, err := testCreateSessionRecord(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	if err := planSrc.SavePlan(ctx, planning.Plan{SessionID: sessionRecord.ID, Summary: "Plan", Milestones: []planning.Milestone{
		{Ref: "alpha", Title: "Alpha", Status: planning.MilestoneStatusReady},
		{Ref: "beta", Title: "Beta", Status: planning.MilestoneStatusReady},
	}}); err != nil {
		t.Fatal(err)
	}
	alphaTasks, err := testAddTasks(ctx, planSrc, sessionRecord.ID, "alpha", []string{"alpha task"})
	if err != nil {
		t.Fatal(err)
	}
	betaTasks, err := testAddTasks(ctx, planSrc, sessionRecord.ID, "beta", []string{"beta task"})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := testLoadSession(ctx, st, chatsSrc, planSrc, sessionRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	control := owner.PlanningForChat(domain.Chat{SessionID: sessionRecord.ID, ActiveMilestoneRef: "M001"})
	plan, err := control.GetMilestonePlan(ctx, sessionRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Milestones) != 1 || planning.MilestoneKey(plan.Milestones[0]) != "M001" {
		t.Fatalf("expected alpha-only plan, got %#v", plan.Milestones)
	}
	tasks, err := control.ListTasks(ctx, sessionRecord.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].ID != alphaTasks[0].ID {
		t.Fatalf("expected alpha-only tasks, got %#v", tasks)
	}
	if _, err := control.ListTasks(ctx, sessionRecord.ID, "M002"); err == nil || !strings.Contains(err.Error(), `scoped to milestone "M001"`) {
		t.Fatalf("expected beta scope error, got %v", err)
	}
	if _, err := control.UpdateTask(ctx, betaTasks[0].ID, planning.TaskStatusInProgress, "", "starting work"); err == nil || !strings.Contains(err.Error(), `scoped to milestone "M001"`) {
		t.Fatalf("expected beta update scope error, got %v", err)
	}
}

func TestScopedPlanningLimitsAssignedTask(t *testing.T) {
	ctx := context.Background()
	st, err := store.OpenWithOptions(t.TempDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	sessionRecord, chatsSrc, planSrc, err := testCreateSessionRecord(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	if err := planSrc.SavePlan(ctx, planning.Plan{SessionID: sessionRecord.ID, Summary: "Plan", Milestones: []planning.Milestone{{Ref: "alpha", Title: "Alpha", Status: planning.MilestoneStatusReady}}}); err != nil {
		t.Fatal(err)
	}
	tasks, err := testAddTasks(ctx, planSrc, sessionRecord.ID, "alpha", []string{"first", "second"})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := testLoadSession(ctx, st, chatsSrc, planSrc, sessionRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	control := owner.PlanningForChat(domain.Chat{SessionID: sessionRecord.ID, ActiveMilestoneRef: "M001", AssignedTaskRef: planning.TaskKey(tasks[0])})
	listed, err := control.ListTasks(ctx, sessionRecord.ID, "M001")
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ID != tasks[0].ID {
		t.Fatalf("expected assigned task only, got %#v", listed)
	}
	if _, err := control.AddTasks(ctx, sessionRecord.ID, "M001", []string{"third"}); err == nil || !strings.Contains(err.Error(), "scoped to task") {
		t.Fatalf("expected add task scope error, got %v", err)
	}
}

func TestSessionHydratesTasksWithoutPlanMilestone(t *testing.T) {
	ctx := context.Background()
	st, err := store.OpenWithOptions(t.TempDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	sessionRecord, chatsSrc, planSrc, err := testCreateSessionRecord(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	created, err := testAddTasks(ctx, planSrc, sessionRecord.ID, "alpha", []string{"first", "second"})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := testLoadSession(ctx, st, chatsSrc, planSrc, sessionRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	listed, err := owner.ListTasks(ctx, sessionRecord.ID, "alpha")
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
	sessionRecord, chatsSrc, planSrc, err := testCreateSessionRecord(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := testAddTasks(ctx, planSrc, sessionRecord.ID, "alpha", []string{"first", "second"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := testUpdateTask(ctx, planSrc, sessionRecord.ID, tasks[0].ID, planning.TaskStatusCompleted, "completed in setup"); err != nil {
		t.Fatal(err)
	}
	owner, err := testLoadSession(ctx, st, chatsSrc, planSrc, sessionRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	chatID := id.New()
	got := owner.childIdleNotification(ctx, domain.Chat{ID: chatID, SessionID: sessionRecord.ID, ActiveMilestoneRef: "alpha", ParentChatID: &chatID}, chatID, "Idle")
	want := "Chat " + chatID + " is now idle. Chat completed 1 out of 2 tasks for milestone alpha, but is now stopped. Remaining tasks: alphaT002 is pending."
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
	sessionRecord, chatsSrc, planSrc, err := testCreateSessionRecord(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := testAddTasks(ctx, planSrc, sessionRecord.ID, "alpha", []string{"first", "second"})
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		if _, err := testUpdateTask(ctx, planSrc, sessionRecord.ID, task.ID, planning.TaskStatusCompleted, "completed in setup"); err != nil {
			t.Fatal(err)
		}
	}
	owner, err := testLoadSession(ctx, st, chatsSrc, planSrc, sessionRecord.ID)
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

func TestSessionLoadDoesNotHydrateChatRuntimes(t *testing.T) {
	ctx := context.Background()
	st, err := store.OpenWithOptions(t.TempDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	sessionRecord, chatsSrc, planSrc, err := testCreateSessionRecord(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	first, err := chatsSrc.CreateRecord(ctx, chatpkg.CreateRecordRequest{Session: sessionRecord, Title: "first", Role: chatrole.Orchestrator, Position: -1})
	if err != nil {
		t.Fatal(err)
	}
	second, err := chatsSrc.CreateRecord(ctx, chatpkg.CreateRecordRequest{Session: sessionRecord, Title: "second", Role: chatrole.Execution, ParentID: &first.ID, Position: -1})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := testLoadSession(ctx, st, chatsSrc, planSrc, sessionRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(owner.Snapshot().Snapshots); got != 0 {
		t.Fatalf("expected no hydrated chat runtimes on session load, got %d", got)
	}
	firstRuntime, err := owner.Chat(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	firstRuntimeAgain, err := owner.Chat(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if firstRuntime != firstRuntimeAgain {
		t.Fatalf("expected first chat runtime to be reused from memory")
	}
	snapshot := owner.Snapshot()
	if _, ok := snapshot.Snapshots[first.ID]; !ok {
		t.Fatalf("expected first chat to hydrate on demand")
	}
	if _, ok := snapshot.Snapshots[second.ID]; ok {
		t.Fatalf("expected second chat to stay stored until requested")
	}
}

func TestSessionTimelinePageDoesNotHydrateChatRuntime(t *testing.T) {
	ctx := context.Background()
	st, err := store.OpenWithOptions(t.TempDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	sessionRecord, chatsSrc, planSrc, err := testCreateSessionRecord(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	chatRecord, err := chatsSrc.CreateRecord(ctx, chatpkg.CreateRecordRequest{Session: sessionRecord, Title: "stored", Role: chatrole.Orchestrator, Position: -1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := testAppendTimeline(ctx, st, sessionRecord, chatRecord, domain.UserMessage{Text: "from store"}); err != nil {
		t.Fatal(err)
	}
	owner, err := testLoadSession(ctx, st, chatsSrc, planSrc, sessionRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	page, err := owner.TimelinePage(ctx, chatRecord.ID, "", 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("expected one stored timeline item, got %d", len(page.Items))
	}
	if got := len(owner.Snapshot().Snapshots); got != 0 {
		t.Fatalf("expected transcript paging to avoid hydrating chat runtimes, got %d runtimes", got)
	}
}

func TestSessionShutdownOnlyTouchesHydratedRuntimes(t *testing.T) {
	ctx := context.Background()
	st, err := store.OpenWithOptions(t.TempDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	sessionRecord, chatsSrc, planSrc, err := testCreateSessionRecord(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := chatsSrc.CreateRecord(ctx, chatpkg.CreateRecordRequest{Session: sessionRecord, Title: "stored", Role: chatrole.Orchestrator, Position: -1}); err != nil {
		t.Fatal(err)
	}
	owner, err := testLoadSession(ctx, st, chatsSrc, planSrc, sessionRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(owner.Snapshot().Snapshots); got != 0 {
		t.Fatalf("expected no runtimes before shutdown, got %d", got)
	}
	if err := owner.Shutdown(ctx, chatpkg.CancelReasonRestartInterrupt); err != nil {
		t.Fatal(err)
	}
	if got := len(owner.Snapshot().Snapshots); got != 0 {
		t.Fatalf("expected shutdown to avoid hydrating stored chats, got %d runtimes", got)
	}
}

func TestForkChatAtCopiesTimelinePrefix(t *testing.T) {
	ctx := context.Background()
	st, err := store.OpenWithOptions(t.TempDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	sessionRecord, chatsSrc, planSrc, err := testCreateSessionRecord(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	chats, err := chatsSrc.ListRecordsForSession(ctx, sessionRecord.ID)
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
	owner, err := testLoadSession(ctx, st, chatsSrc, planSrc, sessionRecord.ID)
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
