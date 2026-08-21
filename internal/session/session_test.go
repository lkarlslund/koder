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
	"github.com/lkarlslund/koder/internal/tools/chattool"
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
	sessionRecord, err := createSessionRecord(ctx, st, chatsSrc, "test", "provider", "model", "", nil)
	return sessionRecord, chatsSrc, planSrc, err
}

func testLoadSession(ctx context.Context, st *store.Store, chatsSrc *chatpkg.Source, planSrc *planning.Source, sessionID id.ID) (*Session, error) {
	return Load(ctx, st, chatsSrc, planSrc, sessionID)
}

func TestUpdateChatCallsArchiveLifecycleHook(t *testing.T) {
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
	owner, err := testLoadSession(ctx, st, chatsSrc, planSrc, sessionRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	child, err := owner.NewChat(ctx, owner.Snapshot().Chats[0].ID, "archive me")
	if err != nil {
		t.Fatal(err)
	}
	var archived id.ID
	var lifecycleChat id.ID
	owner.UpdateConfig(RegistryConfig{
		OnChatArchived: func(_ context.Context, chatID id.ID) { archived = chatID },
		BeforeChatUpdate: func(_ context.Context, before domain.Chat, update chattool.UpdateRequest) error {
			lifecycleChat = before.ID
			if update.Archived == nil || !*update.Archived {
				t.Fatal("archive lifecycle did not receive update")
			}
			return nil
		},
	})
	value := true
	if _, _, err := owner.UpdateChat(ctx, child.Snapshot().Chat.ID, chattool.UpdateRequest{Archived: &value}); err != nil {
		t.Fatal(err)
	}
	if archived != child.Snapshot().Chat.ID {
		t.Fatalf("archive hook got %q, want %q", archived, child.Snapshot().Chat.ID)
	}
	if lifecycleChat != child.Snapshot().Chat.ID {
		t.Fatalf("backend lifecycle hook got %q, want %q", lifecycleChat, child.Snapshot().Chat.ID)
	}
}

func TestNewRootChatInheritsSessionChatSettings(t *testing.T) {
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
	owner, err := testLoadSession(ctx, st, chatsSrc, planSrc, sessionRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	original := owner.Snapshot().Chats[0]
	if !sessionRecord.TitleUserDefined || original.TitleUserDefined {
		t.Fatalf("initial title ownership: session=%v chat=%v", sessionRecord.TitleUserDefined, original.TitleUserDefined)
	}
	created, err := owner.NewRootChat(ctx, "Voice", chatrole.Voice)
	if err != nil {
		t.Fatal(err)
	}
	chatRecord := created.Snapshot().Chat
	if chatRecord.ParentChatID != nil || chatRecord.WorkflowRole != chatrole.Orchestrator || chatRecord.InteractionMode != domain.InteractionModeVoice {
		t.Fatalf("unexpected root voice chat: %#v", chatRecord)
	}
	if chatRecord.ProviderID != original.ProviderID || chatRecord.ModelID != original.ModelID || chatRecord.PermissionProfile != original.PermissionProfile {
		t.Fatalf("voice chat did not inherit settings: original=%#v created=%#v", original, chatRecord)
	}
	if !chatRecord.TitleUserDefined {
		t.Fatal("explicit voice chat title was not marked as user-defined")
	}
}

func TestNewChatWithSpecDoesNotInheritKoderModelIntoCodex(t *testing.T) {
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
	owner, err := testLoadSession(ctx, st, chatsSrc, planSrc, sessionRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	root := owner.Snapshot().Chats[0]
	created, err := owner.NewChatWithSpec(ctx, &root.ID, domain.ChatCreateSpec{
		Title: "Codex worker", Backend: domain.ChatBackendCodex, WorkflowRole: domain.WorkflowRoleExecution, TaskRef: "T008",
	})
	if err != nil {
		t.Fatal(err)
	}
	chatRecord := created.Snapshot().Chat
	if chatRecord.Backend != domain.ChatBackendCodex || chatRecord.ProviderID != "" || chatRecord.ModelID != "" || chatRecord.AssignedTaskRef != "T008" {
		t.Fatalf("created chat = %#v", chatRecord)
	}
}

func TestNewChatWithSpecUsesExplicitKoderProviderAndModel(t *testing.T) {
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
	owner, err := testLoadSession(ctx, st, chatsSrc, planSrc, sessionRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	root := owner.Snapshot().Chats[0]
	created, err := owner.NewChatWithSpec(ctx, &root.ID, domain.ChatCreateSpec{
		Title: "Other model", Backend: domain.ChatBackendKoder, ProviderID: "other-provider", ModelID: "other-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	chatRecord := created.Snapshot().Chat
	if chatRecord.ProviderID != "other-provider" || chatRecord.ModelID != "other-model" {
		t.Fatalf("created chat model = %s/%s", chatRecord.ProviderID, chatRecord.ModelID)
	}
}

func TestNewChatWithSpecResolvesBackendDefaultModel(t *testing.T) {
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
	owner, err := testLoadSession(ctx, st, chatsSrc, planSrc, sessionRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	owner.UpdateConfig(RegistryConfig{PrepareChatSpec: func(_ context.Context, spec domain.ChatCreateSpec) (domain.ChatCreateSpec, error) {
		if spec.Backend == domain.ChatBackendCodex && spec.ModelID == "" {
			spec.ModelID = "codex-default"
		}
		return spec, nil
	}})
	root := owner.Snapshot().Chats[0]
	created, err := owner.NewChatWithSpec(ctx, &root.ID, domain.ChatCreateSpec{
		Title: "Codex worker", Backend: domain.ChatBackendCodex, WorkflowRole: domain.WorkflowRoleExecution,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := created.Snapshot().Chat.ModelID; got != "codex-default" {
		t.Fatalf("created codex model = %q, want codex-default", got)
	}
}

func TestEnsureChatModelDoesNotReplaceCodexModelWithKoderDefault(t *testing.T) {
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
	owner, err := testLoadSession(ctx, st, chatsSrc, planSrc, sessionRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	root := owner.Snapshot().Chats[0]
	created, err := owner.NewChatWithSpec(ctx, &root.ID, domain.ChatCreateSpec{
		Title: "Codex worker", Backend: domain.ChatBackendCodex, WorkflowRole: domain.WorkflowRoleExecution, ModelID: "gpt-codex",
	})
	if err != nil {
		t.Fatal(err)
	}
	ensured, err := owner.EnsureChatModel(ctx, created.Snapshot().Chat.ID, "native-provider", "native-model")
	if err != nil {
		t.Fatal(err)
	}
	if ensured.ProviderID != "" || ensured.ModelID != "gpt-codex" {
		t.Fatalf("ensured codex identity = %q/%q, want empty provider and gpt-codex", ensured.ProviderID, ensured.ModelID)
	}
	if runtimeModel := created.Snapshot().Chat.ModelID; runtimeModel != "gpt-codex" {
		t.Fatalf("runtime model = %q, want gpt-codex", runtimeModel)
	}
}

func TestVoiceChatCanCoordinateSiblingChats(t *testing.T) {
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
	owner, err := testLoadSession(ctx, st, chatsSrc, planSrc, sessionRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	original := owner.Snapshot().Chats[0]
	voiceRuntime, err := owner.NewRootChat(ctx, "Voice", chatrole.Voice)
	if err != nil {
		t.Fatal(err)
	}
	voiceChat := voiceRuntime.Snapshot().Chat
	status, err := owner.ChatToolControl(voiceChat.ID).UpdateChat(ctx, sessionRecord.ID, voiceChat.ID, original.ID, chattool.UpdateRequest{Title: "Inspected by voice"})
	if err != nil {
		t.Fatalf("voice chat could not coordinate sibling: %v", err)
	}
	if status.Title != "Inspected by voice" {
		t.Fatalf("updated sibling status = %#v", status)
	}
}

func testAddTasks(ctx context.Context, planSrc *planning.Source, sessionID id.ID, milestoneKey string, contents []string) ([]planning.Task, error) {
	existing, err := planSrc.ListTasks(ctx, sessionID, milestoneKey)
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
			MilestoneKey: milestoneKey,
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
		{Key: "M001", Title: "Alpha", Status: planning.MilestoneStatusReady},
		{Key: "M002", Title: "Beta", Status: planning.MilestoneStatusReady},
	}}); err != nil {
		t.Fatal(err)
	}
	alphaTasks, err := testAddTasks(ctx, planSrc, sessionRecord.ID, "M001", []string{"alpha task"})
	if err != nil {
		t.Fatal(err)
	}
	betaTasks, err := testAddTasks(ctx, planSrc, sessionRecord.ID, "M002", []string{"beta task"})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := testLoadSession(ctx, st, chatsSrc, planSrc, sessionRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	control := owner.PlanningForChat(domain.Chat{SessionID: sessionRecord.ID, ActiveMilestoneKey: "M001"})
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
	if err := planSrc.SavePlan(ctx, planning.Plan{SessionID: sessionRecord.ID, Summary: "Plan", Milestones: []planning.Milestone{{Key: "M001", Title: "Alpha", Status: planning.MilestoneStatusReady}}}); err != nil {
		t.Fatal(err)
	}
	tasks, err := testAddTasks(ctx, planSrc, sessionRecord.ID, "M001", []string{"first", "second"})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := testLoadSession(ctx, st, chatsSrc, planSrc, sessionRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	control := owner.PlanningForChat(domain.Chat{SessionID: sessionRecord.ID, ActiveMilestoneKey: "M001", AssignedTaskRef: planning.TaskKey(tasks[0])})
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

func TestPlanningArchiveAndDeleteLifecycle(t *testing.T) {
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
	owner, err := testLoadSession(ctx, st, chatsSrc, planSrc, sessionRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := owner.SetMilestonePlan(ctx, sessionRecord.ID, "Lifecycle", []planning.Milestone{{Key: "M001", Title: "Ship", Status: planning.MilestoneStatusReady}})
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := owner.AddTasks(ctx, sessionRecord.ID, "M001", []string{"Implement"})
	if err != nil {
		t.Fatal(err)
	}
	taskKey := planning.TaskKey(tasks[0])
	if _, err := owner.UpdateTask(ctx, taskKey, planning.TaskStatusInProgress, "", "started"); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.ArchiveTask(ctx, taskKey, true); err == nil || !strings.Contains(err.Error(), "in_progress") {
		t.Fatalf("archive in-progress task error = %v", err)
	}
	plan.Milestones[0].Status = planning.MilestoneStatusExecuting
	if _, err := owner.SetMilestonePlan(ctx, sessionRecord.ID, plan.Summary, plan.Milestones); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.ArchiveMilestone(ctx, sessionRecord.ID, "M001", true); err == nil || !strings.Contains(err.Error(), "executing") {
		t.Fatalf("archive executing milestone error = %v", err)
	}
	if _, err := owner.UpdateTask(ctx, taskKey, planning.TaskStatusCompleted, "", "done"); err != nil {
		t.Fatal(err)
	}
	plan, err = owner.GetMilestonePlan(ctx, sessionRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	plan.Milestones[0].Status = planning.MilestoneStatusCompleted
	if _, err := owner.SetMilestonePlan(ctx, sessionRecord.ID, plan.Summary, plan.Milestones); err != nil {
		t.Fatal(err)
	}
	archivedTask, err := owner.ArchiveTask(ctx, taskKey, true)
	if err != nil {
		t.Fatal(err)
	}
	if !archivedTask.Archived {
		t.Fatal("task was not archived")
	}
	if _, err := owner.UpdateTask(ctx, taskKey, planning.TaskStatusPending, "", "reopen"); err == nil || !strings.Contains(err.Error(), "restore") {
		t.Fatalf("update archived task error = %v", err)
	}
	archivedMilestone, err := owner.ArchiveMilestone(ctx, sessionRecord.ID, "M001", true)
	if err != nil {
		t.Fatal(err)
	}
	if !archivedMilestone.Archived {
		t.Fatal("milestone was not archived")
	}
	if err := owner.DeleteMilestone(ctx, sessionRecord.ID, "M001"); err == nil || !strings.Contains(err.Error(), "still has 1 task") {
		t.Fatalf("delete milestone with task error = %v", err)
	}
	if err := owner.DeleteTask(ctx, taskKey); err != nil {
		t.Fatal(err)
	}
	if err := owner.DeleteMilestone(ctx, sessionRecord.ID, "M001"); err != nil {
		t.Fatal(err)
	}
	reloaded, err := testLoadSession(ctx, st, chatsSrc, planSrc, sessionRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	gotPlan, err := reloaded.GetMilestonePlan(ctx, sessionRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	gotTasks, err := reloaded.ListTasks(ctx, sessionRecord.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(gotPlan.Milestones) != 0 || len(gotTasks) != 0 {
		t.Fatalf("deleted planning data reappeared: plan=%#v tasks=%#v", gotPlan, gotTasks)
	}
}

func TestArchivedMilestonesCannotReceiveWorkOrExposeDependentWork(t *testing.T) {
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
	owner, err := testLoadSession(ctx, st, chatsSrc, planSrc, sessionRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owner.SetMilestonePlan(ctx, sessionRecord.ID, "Dependencies", []planning.Milestone{
		{Key: "M001", Title: "Parent", Status: planning.MilestoneStatusReady},
		{Key: "M002", Title: "Child", Status: planning.MilestoneStatusReady, DependsOnKey: "M001"},
		{Key: "M003", Title: "Other", Status: planning.MilestoneStatusReady},
	}); err != nil {
		t.Fatal(err)
	}
	parentTasks, err := owner.AddTasks(ctx, sessionRecord.ID, "M001", []string{"Parent task"})
	if err != nil {
		t.Fatal(err)
	}
	otherTasks, err := owner.AddTasks(ctx, sessionRecord.ID, "M003", []string{"Other task"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owner.ArchiveMilestone(ctx, sessionRecord.ID, "M002", true); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.ArchiveMilestone(ctx, sessionRecord.ID, "M001", true); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.UpdateTask(ctx, planning.TaskKey(parentTasks[0]), planning.TaskStatusCompleted, "", "done"); err == nil || !strings.Contains(err.Error(), "restore it before updating") {
		t.Fatalf("update under archived milestone error = %v", err)
	}
	if _, err := owner.MoveTask(ctx, sessionRecord.ID, planning.TaskKey(otherTasks[0]), "M001", planning.TaskStatusPending, 0, ""); err == nil || !strings.Contains(err.Error(), "restore it before moving") {
		t.Fatalf("move into archived milestone error = %v", err)
	}
	if _, err := owner.ArchiveMilestone(ctx, sessionRecord.ID, "M002", false); err == nil || !strings.Contains(err.Error(), "restore the dependency first") {
		t.Fatalf("restore dependent milestone error = %v", err)
	}
	if _, err := owner.ArchiveMilestone(ctx, sessionRecord.ID, "M001", false); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.ArchiveMilestone(ctx, sessionRecord.ID, "M002", false); err != nil {
		t.Fatal(err)
	}
}

func TestSetMilestonePlanRequiresExplicitDelete(t *testing.T) {
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
	owner, err := testLoadSession(ctx, st, chatsSrc, planSrc, sessionRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owner.SetMilestonePlan(ctx, sessionRecord.ID, "", []planning.Milestone{{Key: "M001", Title: "Keep", Status: planning.MilestoneStatusPending}}); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.SetMilestonePlan(ctx, sessionRecord.ID, "", nil); err == nil || !strings.Contains(err.Error(), "archive and delete it explicitly") {
		t.Fatalf("replace-removal error = %v", err)
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

func TestStartChatRejectsDuplicateMilestoneChild(t *testing.T) {
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
	if err := planSrc.SavePlan(ctx, planning.Plan{SessionID: sessionRecord.ID, Milestones: []planning.Milestone{
		{Key: "M001", Title: "Alpha", Status: planning.MilestoneStatusReady},
	}}); err != nil {
		t.Fatal(err)
	}
	owner, err := testLoadSession(ctx, st, chatsSrc, planSrc, sessionRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	owner.UpdateConfig(RegistryConfig{MaxChildChats: 2})
	parentID := owner.Snapshot().Chats[0].ID
	control := owner.ChatToolControl(parentID)
	if _, err := control.StartChat(ctx, sessionRecord.ID, parentID, chattool.StartRequest{
		Profile:      chatrole.Execution,
		Objective:    "Implement alpha",
		MilestoneKey: "M001",
	}); err != nil {
		t.Fatal(err)
	}
	_, err = control.StartChat(ctx, sessionRecord.ID, parentID, chattool.StartRequest{
		Profile:      chatrole.Execution,
		Objective:    "Implement alpha again",
		MilestoneKey: "M001",
	})
	if err == nil || !strings.Contains(err.Error(), "use chat_send") {
		t.Fatalf("expected duplicate milestone steer error, got %v", err)
	}
}

func TestStartChatCanSelectCodexBackendIndependently(t *testing.T) {
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
	owner, err := testLoadSession(ctx, st, chatsSrc, planSrc, sessionRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	owner.UpdateConfig(RegistryConfig{
		MaxChildChats: 2, BackendAvailable: func(domain.ChatBackend) error { return nil },
		PrepareChatSpec: func(_ context.Context, spec domain.ChatCreateSpec) (domain.ChatCreateSpec, error) {
			if spec.Backend == domain.ChatBackendCodex && spec.ModelID == "" {
				spec.ModelID = "gpt-test"
			}
			return spec, nil
		},
	})
	parentID := owner.Snapshot().Chats[0].ID
	_, err = owner.ChatToolControl(parentID).StartChat(ctx, sessionRecord.ID, parentID, chattool.StartRequest{
		Profile: chatrole.Planning, Objective: "Plan milestone five", Backend: domain.ChatBackendCodex,
		PermissionProfile: "readonly", ToolStates: domain.ToolStates{domain.ToolKindSessionStart: false},
	})
	if err != nil {
		t.Fatal(err)
	}
	chats := owner.Snapshot().Chats
	child := chats[len(chats)-1]
	if child.Backend != domain.ChatBackendCodex || child.WorkflowRole != chatrole.Planning || child.InteractionMode != domain.InteractionModeText || child.ModelID != "gpt-test" || child.PermissionProfile != "readonly" || child.ToolStates[domain.ToolKindSessionStart] {
		t.Fatalf("codex child = %#v", child)
	}
}

func TestStartChatRespectsMaxNonIdleChildren(t *testing.T) {
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
	if err := planSrc.SavePlan(ctx, planning.Plan{SessionID: sessionRecord.ID, Milestones: []planning.Milestone{
		{Key: "M001", Title: "Alpha", Status: planning.MilestoneStatusReady, Position: 0},
		{Key: "M002", Title: "Beta", Status: planning.MilestoneStatusReady, Position: 1},
	}}); err != nil {
		t.Fatal(err)
	}
	owner, err := testLoadSession(ctx, st, chatsSrc, planSrc, sessionRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	owner.UpdateConfig(RegistryConfig{MaxChildChats: 1})
	parentID := owner.Snapshot().Chats[0].ID
	control := owner.ChatToolControl(parentID)
	if _, err := control.StartChat(ctx, sessionRecord.ID, parentID, chattool.StartRequest{
		Profile:      chatrole.Execution,
		Objective:    "Implement alpha",
		MilestoneKey: "M001",
	}); err != nil {
		t.Fatal(err)
	}
	_, err = control.StartChat(ctx, sessionRecord.ID, parentID, chattool.StartRequest{
		Profile:      chatrole.Execution,
		Objective:    "Implement beta",
		MilestoneKey: "M002",
	})
	if err == nil || !strings.Contains(err.Error(), "limit is 1") || !strings.Contains(err.Error(), "chat_send") {
		t.Fatalf("expected max child chat error, got %v", err)
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
	got := owner.childIdleNotification(ctx, domain.Chat{ID: chatID, SessionID: sessionRecord.ID, ActiveMilestoneKey: "alpha", ParentChatID: &chatID}, chatID, "Idle")
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
	got := owner.childIdleNotification(ctx, domain.Chat{ID: chatID, SessionID: sessionRecord.ID, ActiveMilestoneKey: "alpha", ParentChatID: &chatID}, chatID, "Idle")
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
