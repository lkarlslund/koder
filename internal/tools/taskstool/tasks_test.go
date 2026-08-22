package taskstool_test

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
	_ "github.com/lkarlslund/koder/internal/tools/all"
	"github.com/lkarlslund/koder/internal/tools/tooltest"
)

func TestTaskUpdateItemParsesTaskKey(t *testing.T) {
	id, err := planning.ParseTaskKey("M001T001")
	if err != nil {
		t.Fatalf("expected task key to parse, got %v", err)
	}
	if id != "M001T001" {
		t.Fatalf("expected parsed task key, got %s", id)
	}
	if _, err := planning.ParseTaskKey("M001"); err == nil {
		t.Fatal("expected milestone key to be rejected as task key")
	}
}

func TestTaskUpdateItemDefinitionUsesTaskKey(t *testing.T) {
	defs := tools.Definitions(tools.Runtime{})
	for _, def := range defs {
		if def.Function.Name != domain.ToolKindTasks.String() {
			continue
		}
		params := string(def.Function.Parameters)
		if !strings.Contains(params, `"task_key":{`) || !strings.Contains(params, `"Task key returned by this tool"`) || strings.Contains(params, `"id"`) {
			t.Fatalf("expected tasks_update task_key string, got %s", params)
		}
		if !strings.Contains(params, `"enum":["pending","in_progress","completed","cancelled"]`) || strings.Contains(params, "InProgress") {
			t.Fatalf("expected tasks_update status enum to match task status strings, got %s", params)
		}
		if !strings.Contains(params, `"action"`) || !strings.Contains(params, `"update"`) {
			t.Fatalf("expected tasks action schema, got %s", params)
		}
		if !strings.Contains(def.Function.Description, "task_key") {
			t.Fatalf("expected tasks description to tell model to use task keys, got %q", def.Function.Description)
		}
		return
	}
	t.Fatal("tasks definition not found")
}

func TestTaskStatusUsesSnakeCase(t *testing.T) {
	if _, err := planning.ParseTaskStatus("InProgress"); err == nil {
		t.Fatal("expected InProgress to be rejected")
	}
	status, err := planning.ParseTaskStatus("in_progress")
	if err != nil {
		t.Fatal(err)
	}
	if status != planning.TaskStatusInProgress {
		t.Fatalf("expected in_progress, got %s", status)
	}
}

func TestMilestoneAndTaskWorkflow(t *testing.T) {
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

	tasks, err := modeltest.ListTasks(ctx, st, session.ID, "M002")
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 {
		t.Fatalf("unexpected tasks: %#v", tasks)
	}
	if _, err := executeAndPersist(ctx, t, runtime, tools.Request{
		Tool: domain.ToolKindTasksUpdate,
		Args: map[string]string{"task_key": tools.FormatTaskID(planning.TaskKey(tasks[0])), "status": "in_progress", "note": "Started writing tests."},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := executeAndPersist(ctx, t, runtime, tools.Request{
		Tool: domain.ToolKindTasksUpdate,
		Args: map[string]string{"task_key": tools.FormatTaskID(planning.TaskKey(tasks[0])), "status": "completed", "note": "Completed the tests."},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := executeAndPersist(ctx, t, runtime, tools.Request{
		Tool: domain.ToolKindTasksUpdate,
		Args: map[string]string{"task_key": tools.FormatTaskID(planning.TaskKey(tasks[1])), "status": "completed", "note": "Fixed the bug."},
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

func TestTaskAddPersistReturnsRealTaskIDs(t *testing.T) {
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

func TestTaskAddRejectsDuplicateContent(t *testing.T) {
	ctx := context.Background()
	st := openPlanningTestStore(t)
	session, err := modeltest.CreateSession(ctx, st, "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := modeltest.PutPlan(ctx, st, planning.Plan{SessionID: session.ID, Milestones: []planning.Milestone{{Key: "M001", Title: "Implement", Status: planning.MilestoneStatusReady}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := modeltest.AddTasks(ctx, st, session.ID, "M001", []string{"Write tests"}); err != nil {
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

func TestTaskAddRejectsClosedMilestones(t *testing.T) {
	ctx := context.Background()
	st := openPlanningTestStore(t)
	session, err := modeltest.CreateSession(ctx, st, "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := modeltest.PutPlan(ctx, st, planning.Plan{SessionID: session.ID, Milestones: []planning.Milestone{
		{Key: "M001", Title: "Done", Status: planning.MilestoneStatusCompleted},
		{Key: "M002", Title: "Cancelled", Status: planning.MilestoneStatusCancelled},
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

func TestTaskArchiveListAndDeleteTools(t *testing.T) {
	ctx := context.Background()
	st := openPlanningTestStore(t)
	session, err := modeltest.CreateSession(ctx, st, "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := modeltest.PutPlan(ctx, st, planning.Plan{SessionID: session.ID, Milestones: []planning.Milestone{{Key: "M001", Title: "Implement", Status: planning.MilestoneStatusReady}}}); err != nil {
		t.Fatal(err)
	}
	items, err := modeltest.AddTasks(ctx, st, session.ID, "M001", []string{"Hide me", "Keep me"})
	if err != nil {
		t.Fatal(err)
	}
	runtime := tools.Runtime{SessionID: session.ID, SessionControl: tooltest.NewSessionControl(st), ChatRole: chatrole.Orchestrator}
	taskKey := planning.TaskKey(items[0])
	archiveRequest := tools.Request{Tool: domain.ToolKindTaskArchive, Args: map[string]string{"task_key": taskKey, "archived": "true"}}
	archived, err := executeAndPersist(ctx, t, runtime, archiveRequest)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(archived.Output, "Archived task "+taskKey) {
		t.Fatalf("archive output = %q", archived.Output)
	}
	listed, err := tools.Call(ctx, tools.Options{Runtime: runtime, Request: tools.Request{Tool: domain.ToolKindTaskList, Args: map[string]string{"milestone_key": "M001"}}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(listed.Output, "Hide me") || !strings.Contains(listed.Output, "Keep me") {
		t.Fatalf("default task list = %q", listed.Output)
	}
	listed, err = tools.Call(ctx, tools.Options{Runtime: runtime, Request: tools.Request{Tool: domain.ToolKindTaskList, Args: map[string]string{"milestone_key": "M001", "archived": "true"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(listed.Output, "Hide me [archived]") {
		t.Fatalf("archived task list = %q", listed.Output)
	}
	deleteRequest := tools.Request{Tool: domain.ToolKindTaskDelete, Args: map[string]string{"task_key": taskKey}}
	if _, err := executeAndPersist(ctx, t, runtime, deleteRequest); err != nil {
		t.Fatal(err)
	}
	remaining, err := modeltest.ListTasks(ctx, st, session.ID, "M001")
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || planning.TaskKey(remaining[0]) != planning.TaskKey(items[1]) {
		t.Fatalf("deleted task remained: %#v", remaining)
	}
	if _, enabled := tools.DefinitionFor(domain.ToolKindTaskDelete, tools.Runtime{ChatRole: chatrole.Execution}); enabled {
		t.Fatal("task delete should be hidden from execution chats")
	}
}

func TestTaskArchiveRejectsInProgressTask(t *testing.T) {
	ctx := context.Background()
	st := openPlanningTestStore(t)
	session, err := modeltest.CreateSession(ctx, st, "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := modeltest.PutPlan(ctx, st, planning.Plan{SessionID: session.ID, Milestones: []planning.Milestone{{Key: "M001", Title: "Implement", Status: planning.MilestoneStatusExecuting}}}); err != nil {
		t.Fatal(err)
	}
	items, err := modeltest.AddTasks(ctx, st, session.ID, "M001", []string{"Running"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := modeltest.UpdateTask(ctx, st, items[0].ID, planning.TaskStatusInProgress, "", "started"); err != nil {
		t.Fatal(err)
	}
	runtime := tools.Runtime{SessionID: session.ID, SessionControl: tooltest.NewSessionControl(st), ChatRole: chatrole.Orchestrator}
	_, err = tools.Call(ctx, tools.Options{Runtime: runtime, Request: tools.Request{Tool: domain.ToolKindTaskArchive, Args: map[string]string{"task_key": planning.TaskKey(items[0]), "archived": "true"}}})
	if err == nil || !strings.Contains(err.Error(), "in_progress") {
		t.Fatalf("archive in-progress task error = %v", err)
	}
}

func TestArchivedMilestoneTasksRequireExplicitArchivedListing(t *testing.T) {
	ctx := context.Background()
	st := openPlanningTestStore(t)
	session, err := modeltest.CreateSession(ctx, st, "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := modeltest.PutPlan(ctx, st, planning.Plan{SessionID: session.ID, Milestones: []planning.Milestone{{Key: "M001", Title: "Hidden milestone", Status: planning.MilestoneStatusReady, Archived: true}}}); err != nil {
		t.Fatal(err)
	}
	items, err := modeltest.AddTasks(ctx, st, session.ID, "M001", []string{"Hidden with parent"})
	if err != nil {
		t.Fatal(err)
	}
	runtime := tools.Runtime{SessionID: session.ID, SessionControl: tooltest.NewSessionControl(st), ChatRole: chatrole.Orchestrator}

	_, err = tools.Call(ctx, tools.Options{Runtime: runtime, Request: tools.Request{Tool: domain.ToolKindTaskList, Args: map[string]string{"milestone_key": "M001"}}})
	if err == nil || !strings.Contains(err.Error(), "pass archived=true") {
		t.Fatalf("default task list should hide archived milestone tasks, got %v", err)
	}
	listed, err := tools.Call(ctx, tools.Options{Runtime: runtime, Request: tools.Request{Tool: domain.ToolKindTaskList, Args: map[string]string{"milestone_key": "M001", "archived": "true"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(listed.Output, "Hidden with parent") {
		t.Fatalf("archived=true should include tasks under archived milestones, got %q", listed.Output)
	}
	_, err = tools.Call(ctx, tools.Options{Runtime: runtime, Request: tools.Request{Tool: domain.ToolKindTaskFetchNext, Args: map[string]string{"milestone_key": "M001"}}})
	if err == nil || !strings.Contains(err.Error(), "archived") {
		t.Fatalf("fetch next should reject archived milestone, got %v", err)
	}

	item := items[0]
	item.Archived = true
	if err := modeltest.PutTask(ctx, st, item); err != nil {
		t.Fatal(err)
	}
	_, err = tools.Call(ctx, tools.Options{Runtime: runtime, Request: tools.Request{Tool: domain.ToolKindTaskArchive, Args: map[string]string{"task_key": planning.TaskKey(item), "archived": "false"}}})
	if err == nil || !strings.Contains(err.Error(), "restore the milestone") {
		t.Fatalf("restoring task under archived milestone should fail, got %v", err)
	}
}

func TestTaskUpdateRequiresAndPersistsNote(t *testing.T) {
	ctx := context.Background()
	st := openPlanningTestStore(t)
	session, err := modeltest.CreateSession(ctx, st, "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := modeltest.PutPlan(ctx, st, planning.Plan{SessionID: session.ID, Milestones: []planning.Milestone{{Key: "M001", Title: "Implement", Status: planning.MilestoneStatusExecuting}}}); err != nil {
		t.Fatal(err)
	}
	tasks, err := modeltest.AddTasks(ctx, st, session.ID, "M001", []string{"Wire endpoint", "Unrelated follow-up"})
	if err != nil {
		t.Fatal(err)
	}
	runtime := tools.Runtime{SessionID: session.ID, SessionControl: tooltest.NewSessionControl(st)}

	if _, err := tools.Normalize(tools.Request{
		Tool: domain.ToolKindTasksUpdate,
		Args: map[string]string{"task_key": planning.TaskKey(tasks[0]), "status": "completed"},
	}); err == nil || !strings.Contains(err.Error(), "note is required") {
		t.Fatalf("expected missing note error, got %v", err)
	}

	req := tools.Request{
		Tool: domain.ToolKindTasksUpdate,
		Args: map[string]string{"task_key": planning.TaskKey(tasks[0]), "status": "completed", "note": "Endpoint was wired and tested."},
	}
	result, err := tools.Call(ctx, tools.Options{Runtime: runtime, Request: req})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "note: Endpoint was wired and tested.") {
		t.Fatalf("expected output to include note, got %q", result.Output)
	}
	if strings.Contains(result.Output, "Unrelated follow-up") {
		t.Fatalf("expected output to include only the updated task, got %q", result.Output)
	}
	beforeFinalize, err := modeltest.GetTask(ctx, st, tasks[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if beforeFinalize.Status == planning.TaskStatusCompleted || beforeFinalize.Note != "" {
		t.Fatalf("tasks_update persisted before result finalization: %#v", beforeFinalize)
	}
	toolResult, body, err := tools.FinalizeResult(ctx, runtime, req, result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body, "Unrelated follow-up") {
		t.Fatalf("expected finalized output to include only the updated task, got %q", body)
	}
	stored, ok := toolResult.Data.(tools.TaskListStoredResult)
	if !ok || len(stored.Items) != 1 || stored.Items[0].Key != planning.TaskKey(tasks[0]) {
		t.Fatalf("expected one updated task in stored result, got %#v", toolResult.Data)
	}
	updated, err := modeltest.GetTask(ctx, st, tasks[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Note != "Endpoint was wired and tested." {
		t.Fatalf("expected persisted note, got %#v", updated)
	}
}

func TestOrchestratorCanMutateOwnInProgressTask(t *testing.T) {
	ctx := context.Background()
	st := openPlanningTestStore(t)
	session, err := modeltest.CreateSession(ctx, st, "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := modeltest.PutPlan(ctx, st, planning.Plan{SessionID: session.ID, Milestones: []planning.Milestone{{Key: "M001", Title: "Implement", Status: planning.MilestoneStatusExecuting}}}); err != nil {
		t.Fatal(err)
	}
	tasks, err := modeltest.AddTasks(ctx, st, session.ID, "M001", []string{"Wire endpoint"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := modeltest.UpdateTask(ctx, st, tasks[0].ID, planning.TaskStatusInProgress, tasks[0].Content, "worker started"); err != nil {
		t.Fatal(err)
	}
	runtime := tools.Runtime{SessionID: session.ID, ChatID: "orchestrator-chat", ChatRole: chatrole.Orchestrator, SessionControl: tooltest.NewSessionControl(st)}

	if _, err = executeAndPersist(ctx, t, runtime, tools.Request{
		Tool: domain.ToolKindTasksUpdate,
		Args: map[string]string{"task_key": planning.TaskKey(tasks[0]), "status": "completed", "note": "Orchestrator completed its own running task."},
	}); err != nil {
		t.Fatalf("expected orchestrator to mutate unowned in-progress task, got %v", err)
	}
}

func TestOrchestratorCannotMutateWorkerOwnedInProgressTask(t *testing.T) {
	ctx := context.Background()
	st := openPlanningTestStore(t)
	session, err := modeltest.CreateSession(ctx, st, "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	workerID := id.ID("worker-chat")
	if err := modeltest.PutPlan(ctx, st, planning.Plan{SessionID: session.ID, Milestones: []planning.Milestone{{Key: "M001", Title: "Implement", Status: planning.MilestoneStatusExecuting, OwnerChatID: &workerID}}}); err != nil {
		t.Fatal(err)
	}
	tasks, err := modeltest.AddTasks(ctx, st, session.ID, "M001", []string{"Wire endpoint"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := modeltest.UpdateTask(ctx, st, tasks[0].ID, planning.TaskStatusInProgress, tasks[0].Content, "worker started"); err != nil {
		t.Fatal(err)
	}
	runtime := tools.Runtime{SessionID: session.ID, ChatID: "orchestrator-chat", ChatRole: chatrole.Orchestrator, SessionControl: tooltest.NewSessionControl(st)}

	_, err = tools.Call(ctx, tools.Options{Runtime: runtime, Request: tools.Request{
		Tool: domain.ToolKindTasksUpdate,
		Args: map[string]string{"task_key": planning.TaskKey(tasks[0]), "status": "completed", "note": "Orchestrator tried to complete worker task."},
	}})
	if err == nil || !strings.Contains(err.Error(), "in_progress") || !strings.Contains(err.Error(), "chat_send") {
		t.Fatalf("expected running task steering error, got %v", err)
	}
}

func TestTaskUpdateDetectsWorkerClaimBetweenCallAndFinalize(t *testing.T) {
	ctx := context.Background()
	st := openPlanningTestStore(t)
	session, err := modeltest.CreateSession(ctx, st, "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	workerID := id.ID("worker-chat")
	if err := modeltest.PutPlan(ctx, st, planning.Plan{SessionID: session.ID, Milestones: []planning.Milestone{{Key: "M001", Title: "Implement", Status: planning.MilestoneStatusExecuting, OwnerChatID: &workerID}}}); err != nil {
		t.Fatal(err)
	}
	tasks, err := modeltest.AddTasks(ctx, st, session.ID, "M001", []string{"Wire endpoint"})
	if err != nil {
		t.Fatal(err)
	}
	runtime := tools.Runtime{SessionID: session.ID, ChatID: "orchestrator-chat", ChatRole: chatrole.Orchestrator, SessionControl: tooltest.NewSessionControl(st)}
	req := tools.Request{
		Tool: domain.ToolKindTasksUpdate,
		Args: map[string]string{"task_key": planning.TaskKey(tasks[0]), "status": "in_progress", "note": "Orchestrator started work."},
	}
	result, err := tools.Call(ctx, tools.Options{Runtime: runtime, Request: req})
	if err != nil {
		t.Fatalf("initial validation failed: %v", err)
	}
	if _, err := modeltest.UpdateTask(ctx, st, tasks[0].ID, planning.TaskStatusInProgress, tasks[0].Content, "worker claimed task"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := tools.FinalizeResult(ctx, runtime, req, result); err == nil || !strings.Contains(err.Error(), "chat_send") {
		t.Fatalf("expected ownership recovery guidance, got %v", err)
	}
	stored, err := modeltest.GetTask(ctx, st, tasks[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Note != "worker claimed task" {
		t.Fatalf("finalizer overwrote worker-owned task: %#v", stored)
	}
}

func TestTaskScopedChatSeesAndUpdatesOnlyAssignedTask(t *testing.T) {
	ctx := context.Background()
	st := openPlanningTestStore(t)
	session, err := modeltest.CreateSession(ctx, st, "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := modeltest.PutPlan(ctx, st, planning.Plan{SessionID: session.ID, Summary: "Ship it", Milestones: []planning.Milestone{
		{Key: "implement", Title: "Implement", Status: planning.MilestoneStatusExecuting},
	}}); err != nil {
		t.Fatal(err)
	}
	tasks, err := modeltest.AddTasks(ctx, st, session.ID, "M001", []string{"First", "Second"})
	if err != nil {
		t.Fatal(err)
	}
	chat := domain.Chat{
		ID:                    "chat-1",
		WorkflowRole:          chatrole.Execution,
		ActiveMilestoneKey:    "M001",
		AssignedTaskBucketKey: "M001",
		AssignedTaskRef:       planning.TaskKey(tasks[0]),
	}
	runtime := tools.Runtime{
		SessionID:             session.ID,
		ChatID:                chat.ID,
		ChatRole:              chat.WorkflowRole,
		ActiveMilestoneKey:    chat.ActiveMilestoneKey,
		AssignedTaskBucketKey: chat.AssignedTaskBucketKey,
		AssignedTaskRef:       chat.AssignedTaskRef,
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
		Args: map[string]string{"task_key": planning.TaskKey(tasks[1]), "status": planning.TaskStatusCompleted.String(), "note": "Tried to complete scoped task."},
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

func TestMilestonesResourceExposedInDefinitions(t *testing.T) {
	defs := tools.Definitions(tools.Runtime{})
	found := false
	for _, def := range defs {
		if def.Function.Name == domain.ToolKindMilestones.String() {
			found = strings.Contains(string(def.Function.Parameters), `"create"`) && strings.Contains(string(def.Function.Parameters), `"update"`)
		}
		if def.Function.Name == domain.ToolKindMilestoneAdd.String() || def.Function.Name == domain.ToolKindMilestoneUpdate.String() {
			t.Fatalf("legacy milestone tool remained exposed: %s", def.Function.Name)
		}
	}
	if !found {
		t.Fatal("milestones resource definition missing create/update actions")
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
