package taskstool

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/planning"
	"github.com/lkarlslund/koder/internal/tools"
)

func init() {
	tools.Register(tools.ActionTool{
		Kind: tools.Tasks,
		Routes: []tools.ActionRoute{
			{Action: "list", Tool: tools.TaskList},
			{Action: "create", Tool: tools.TasksAdd},
			{Action: "update", Tool: tools.TasksUpdate},
			{Action: "next", Tool: tools.TaskFetchNext},
			{Action: "archive", Tool: tools.TaskArchive, FixedArgs: map[string]string{"archived": "true"}},
			{Action: "restore", Tool: tools.TaskArchive, FixedArgs: map[string]string{"archived": "false"}},
			{Action: "delete", Tool: tools.TaskDelete},
		},
		BypassPermissions: true,
	}, tools.ToolSpec{
		Title:       "Tasks",
		Description: "Read and maintain milestone task lists.",
		Usage:       "Manage concrete tasks within a milestone. Use list before changing unfamiliar tasks and copy task_key values exactly. create appends pending items; update changes one task's status, note, or content; next returns the current or next actionable task without claiming it; archive hides inactive work; restore makes archived work visible; delete permanently erases only an archived task.",
		Parameters:  `{"type":"object","properties":{"action":{"type":"string","enum":["list","create","update","next","archive","restore","delete"]},"milestone_key":{"type":"string","description":"Milestone owning the tasks; defaults to the assigned milestone where supported"},"task_key":{"type":"string","description":"Task key returned by this tool"},"items":{"type":"array","items":{"type":"object","properties":{"content":{"type":"string"}},"required":["content"],"additionalProperties":false}},"status":{"type":"string","enum":["pending","in_progress","completed","cancelled"]},"note":{"type":"string"},"content":{"type":"string"},"archived":{"type":"boolean","description":"For list, include archived tasks"}},"required":["action"],"additionalProperties":false}`,
		ExposeToLLM: true,
	})
	tools.Register(listTool{}, tools.ToolSpec{
		Title:       "List tasks",
		Description: "Read the task list for a milestone.",
		Usage:       "Read the task list for a milestone. Archived tasks are hidden by default; pass archived=true when you need to inspect or restore them. If milestone_key is omitted, this reads the current assigned milestone's tasks.",
		Parameters:  `{"type":"object","properties":{"milestone_key":{"type":"string","description":"Optional milestone key; defaults to the assigned milestone"},"archived":{"type":"boolean","description":"Include archived tasks. Defaults to false."}},"additionalProperties":false}`,
		ExposeToLLM: true, Legacy: true,
	})
	tools.Register(addItemsTool{}, tools.ToolSpec{
		Title:       "Add tasks",
		Description: "Append new pending tasks to a milestone.",
		Usage:       "Append new pending tasks to a milestone's task list. Use this to break down the current milestone into concrete execution steps. This rejects duplicate task content already present in the milestone; update existing tasks instead of adding duplicates.",
		Parameters:  `{"type":"object","properties":{"milestone_key":{"type":"string","description":"Milestone key that owns these tasks"},"items":{"type":"array","description":"New tasks to append as pending","items":{"type":"object","properties":{"content":{"type":"string"}},"required":["content"]}}},"required":["milestone_key","items"],"additionalProperties":false}`,
		ExposeToLLM: true, Legacy: true,
	})
	tools.Register(updateItemTool{}, tools.ToolSpec{
		Title:       "Update task",
		Description: "Update one task's status, note, or content.",
		Usage:       "Update one task's status and add a short note explaining what changed or why. Use the exact task_key returned by task_list, task_fetch_next, or tasks_add. Do not invent keys. Keep at most one task in_progress in a milestone. When marking completed, note what was completed in one concise sentence.",
		Parameters:  `{"type":"object","properties":{"task_key":{"type":"string","description":"Task key returned by task_list, task_fetch_next, or tasks_add"},"status":{"type":"string","enum":["pending","in_progress","completed","cancelled"]},"note":{"type":"string","description":"Required short summary of what was done or why the status changed"},"content":{"type":"string","description":"Optional replacement content"}},"required":["task_key","status","note"],"additionalProperties":false}`,
		ExposeToLLM: true, Legacy: true,
	})
	tools.Register(fetchNextTool{}, tools.ToolSpec{
		Title:       "Fetch next task",
		Description: "Find the next task to work on.",
		Usage:       "Find the next task to work on for a milestone. If there is already an in_progress task, it is returned. Otherwise the first pending task is returned. If all tasks are done, this returns the finished list and a message telling you to move to the next milestone or break it down into tasks.",
		Parameters:  `{"type":"object","properties":{"milestone_key":{"type":"string","description":"Optional milestone key; defaults to the assigned milestone"}},"additionalProperties":false}`,
		ExposeToLLM: true, Legacy: true,
	})
	tools.Register(archiveTool{}, tools.ToolSpec{
		Title:       "Archive task",
		Description: "Archive or restore a task.",
		Usage:       "Set archived=true to hide an inactive task without deleting it, or archived=false to restore it. An in-progress task cannot be archived. Use task_list archived=true to find archived tasks.",
		Parameters:  `{"type":"object","properties":{"task_key":{"type":"string","description":"Task key returned by task_list"},"archived":{"type":"boolean","description":"true archives the task; false restores it"}},"required":["task_key","archived"],"additionalProperties":false}`,
		ExposeToLLM: true, Legacy: true,
	})
	tools.Register(deleteTool{}, tools.ToolSpec{
		Title:       "Delete task",
		Description: "Permanently delete an archived task.",
		Usage:       "Permanently erase an archived task. This cannot be undone. Archive the task first; in-progress or visible tasks cannot be deleted.",
		Parameters:  `{"type":"object","properties":{"task_key":{"type":"string","description":"Archived task key returned by task_list archived=true"}},"required":["task_key"],"additionalProperties":false}`,
		ExposeToLLM: true, Legacy: true,
	})
}

type listTool struct{}
type addItemsTool struct{}
type updateItemTool struct{}
type fetchNextTool struct{}
type archiveTool struct{}
type deleteTool struct{}

func (listTool) ID() tools.ID       { return tools.TaskList }
func (addItemsTool) ID() tools.ID   { return tools.TasksAdd }
func (updateItemTool) ID() tools.ID { return tools.TasksUpdate }
func (fetchNextTool) ID() tools.ID  { return tools.TaskFetchNext }
func (archiveTool) ID() tools.ID    { return tools.TaskArchive }
func (deleteTool) ID() tools.ID     { return tools.TaskDelete }

func (listTool) BypassesPermission() bool       { return true }
func (addItemsTool) BypassesPermission() bool   { return true }
func (updateItemTool) BypassesPermission() bool { return true }
func (fetchNextTool) BypassesPermission() bool  { return true }
func (archiveTool) BypassesPermission() bool    { return true }
func (deleteTool) BypassesPermission() bool     { return true }

func (addItemsTool) Definition(runtime tools.Runtime, spec tools.ToolSpec) (tools.ToolSpec, bool) {
	if tools.AssignedTaskRef(runtime) != "" {
		return tools.ToolSpec{}, false
	}
	return spec, true
}

func (archiveTool) Definition(runtime tools.Runtime, spec tools.ToolSpec) (tools.ToolSpec, bool) {
	return taskLifecycleDefinition(runtime, spec)
}

func (deleteTool) Definition(runtime tools.Runtime, spec tools.ToolSpec) (tools.ToolSpec, bool) {
	return taskLifecycleDefinition(runtime, spec)
}

func taskLifecycleDefinition(runtime tools.Runtime, spec tools.ToolSpec) (tools.ToolSpec, bool) {
	if runtime.ChatRole == chatrole.Execution || tools.AssignedMilestoneKey(runtime) != "" || tools.AssignedTaskRef(runtime) != "" {
		return tools.ToolSpec{}, false
	}
	return spec, true
}

func (listTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	out := map[string]string{}
	if ref := strings.TrimSpace(args["milestone_key"]); ref != "" {
		out["milestone_key"] = ref
	}
	if raw := strings.TrimSpace(args["archived"]); raw != "" {
		archived, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("archived: %w", err)
		}
		out["archived"] = strconv.FormatBool(archived)
	}
	return out, nil
}

func (addItemsTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	ref := strings.TrimSpace(args["milestone_key"])
	if ref == "" {
		return nil, fmt.Errorf("milestone_key is empty")
	}
	raw := strings.TrimSpace(args["items"])
	if raw == "" {
		return nil, fmt.Errorf("items is empty")
	}
	if _, err := planning.ParseTaskAddItems(raw); err != nil {
		return nil, err
	}
	return map[string]string{"milestone_key": ref, "items": raw}, nil
}

func (updateItemTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	key, err := planning.ParseTaskKey(args["task_key"])
	if err != nil {
		return nil, err
	}
	status, err := planning.ParseTaskStatus(args["status"])
	if err != nil {
		return nil, err
	}
	out := map[string]string{
		"task_key": tools.FormatTaskID(key),
		"status":   status.String(),
	}
	note := strings.TrimSpace(args["note"])
	if note == "" {
		return nil, fmt.Errorf("note is required")
	}
	out["note"] = note
	if content := strings.TrimSpace(args["content"]); content != "" {
		out["content"] = content
	}
	return out, nil
}

func (fetchNextTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	out := map[string]string{}
	if ref := strings.TrimSpace(args["milestone_key"]); ref != "" {
		out["milestone_key"] = ref
	}
	return out, nil
}

func (archiveTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	key, err := planning.ParseTaskKey(args["task_key"])
	if err != nil {
		return nil, err
	}
	archived, err := strconv.ParseBool(strings.TrimSpace(args["archived"]))
	if err != nil {
		return nil, fmt.Errorf("archived: %w", err)
	}
	return map[string]string{"task_key": key, "archived": strconv.FormatBool(archived)}, nil
}

func (deleteTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	key, err := planning.ParseTaskKey(args["task_key"])
	if err != nil {
		return nil, err
	}
	return map[string]string{"task_key": key}, nil
}

func (listTool) Preview(req tools.Request) string {
	return milestonePreview(req.Args["milestone_key"], "List tasks")
}
func (addItemsTool) Preview(req tools.Request) string {
	return milestonePreview(req.Args["milestone_key"], "Add tasks")
}
func (updateItemTool) Preview(req tools.Request) string { return "Update task " + req.Args["task_key"] }
func (fetchNextTool) Preview(req tools.Request) string {
	return milestonePreview(req.Args["milestone_key"], "Fetch next task")
}
func (archiveTool) Preview(req tools.Request) string {
	if req.Args["archived"] == "false" {
		return "Restore task " + req.Args["task_key"]
	}
	return "Archive task " + req.Args["task_key"]
}
func (deleteTool) Preview(req tools.Request) string {
	return "Delete task " + req.Args["task_key"] + " permanently"
}

func (listTool) Call(ctx context.Context, opts tools.Options) (tools.Result, error) {
	runtime, req := opts.Runtime, opts.Request
	control, err := tools.RequireSessionControl(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	ref, err := tools.AllowedMilestoneKey(runtime, req.Args["milestone_key"])
	if err != nil {
		return tools.Result{}, err
	}
	plan, tasks, ref, err := persistedTaskBucket(ctx, control, runtime.SessionID, ref)
	if err != nil {
		return tools.Result{}, err
	}
	if req.Args["archived"] != "true" {
		if err := ensureMilestoneListed(plan, ref); err != nil {
			return tools.Result{}, err
		}
	}
	tasks = tools.ScopedTasks(runtime, tasks)
	if req.Args["archived"] != "true" {
		tasks = filterArchivedTasks(tasks)
	}
	return tools.TaskBucketResult(plan, ref, tasks, ""), nil
}

func (addItemsTool) Call(ctx context.Context, opts tools.Options) (tools.Result, error) {
	runtime, req := opts.Runtime, opts.Request
	if tools.AssignedTaskRef(runtime) != "" {
		return tools.Result{}, fmt.Errorf("chat is scoped to task %q", tools.AssignedTaskRef(runtime))
	}
	items, err := planning.ParseTaskAddItems(req.Args["items"])
	if err != nil {
		return tools.Result{}, err
	}
	ref, err := tools.AllowedMilestoneKey(runtime, req.Args["milestone_key"])
	if err != nil {
		return tools.Result{}, err
	}
	control, err := tools.RequireSessionControl(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	plan, err := control.GetMilestonePlan(ctx, runtime.SessionID)
	if err != nil {
		return tools.Result{}, err
	}
	if err := ensureMilestoneAcceptsTasks(plan, ref); err != nil {
		return tools.Result{}, err
	}
	existing, err := control.ListTasks(ctx, runtime.SessionID, ref)
	if err != nil {
		return tools.Result{}, err
	}
	if err := planning.ValidateNoDuplicateTaskContent(existing, items); err != nil {
		return tools.Result{}, err
	}
	title := planning.MilestoneTitle(plan, ref)
	tasks := make([]planning.Task, 0, len(items))
	for _, content := range items {
		tasks = append(tasks, planning.Task{
			Content: content,
			Status:  planning.TaskStatusPending,
		})
	}
	return tools.TaskBucketResultWithTitle(ref, title, tasks, ""), nil
}

func (updateItemTool) Call(ctx context.Context, opts tools.Options) (tools.Result, error) {
	update, err := prepareTaskUpdate(ctx, opts.Runtime, opts.Request)
	if err != nil {
		return tools.Result{}, err
	}
	return tools.TaskBucketResultWithTitle(update.milestoneKey, update.milestoneTitle, []planning.Task{update.task}, ""), nil
}

func (fetchNextTool) Call(ctx context.Context, opts tools.Options) (tools.Result, error) {
	runtime, req := opts.Runtime, opts.Request
	control, err := tools.RequireSessionControl(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	ref, err := tools.AllowedMilestoneKey(runtime, req.Args["milestone_key"])
	if err != nil {
		return tools.Result{}, err
	}
	plan, tasks, ref, err := persistedTaskBucket(ctx, control, runtime.SessionID, ref)
	if err != nil {
		return tools.Result{}, err
	}
	if err := ensureMilestoneListed(plan, ref); err != nil {
		return tools.Result{}, err
	}
	tasks = tools.ScopedTasks(runtime, tasks)
	tasks = filterArchivedTasks(tasks)
	for _, item := range tasks {
		if item.Status == planning.TaskStatusInProgress {
			return tools.TaskBucketResult(plan, ref, []planning.Task{item}, ""), nil
		}
	}
	for _, item := range tasks {
		if item.Status == planning.TaskStatusPending {
			return tools.TaskBucketResult(plan, ref, []planning.Task{item}, ""), nil
		}
	}
	message := "All tasks for this milestone are done. If you have more planned tasks, move to the next milestone or break it down into tasks and start working on them."
	return tools.TaskBucketResult(plan, ref, tasks, message), nil
}

func (archiveTool) Call(ctx context.Context, opts tools.Options) (tools.Result, error) {
	control, err := tools.RequireSessionControl(opts.Runtime)
	if err != nil {
		return tools.Result{}, err
	}
	item, err := planningTaskByKey(ctx, control, opts.Runtime.SessionID, opts.Request.Args["task_key"])
	if err != nil {
		return tools.Result{}, err
	}
	archived := opts.Request.Args["archived"] == "true"
	if archived && item.Status == planning.TaskStatusInProgress {
		return tools.Result{}, fmt.Errorf("task %s is in_progress; finish or stop it before archiving", planning.TaskKey(item))
	}
	if !archived {
		plan, err := control.GetMilestonePlan(ctx, opts.Runtime.SessionID)
		if err != nil {
			return tools.Result{}, err
		}
		if err := ensureMilestoneListed(plan, item.MilestoneKey); err != nil {
			return tools.Result{}, fmt.Errorf("restore task %s: %w", planning.TaskKey(item), err)
		}
	}
	action := "archived"
	if !archived {
		action = "restored"
	}
	return taskLifecycleResult(opts.Request.Args["task_key"], action), nil
}

func (deleteTool) Call(ctx context.Context, opts tools.Options) (tools.Result, error) {
	control, err := tools.RequireSessionControl(opts.Runtime)
	if err != nil {
		return tools.Result{}, err
	}
	item, err := planningTaskByKey(ctx, control, opts.Runtime.SessionID, opts.Request.Args["task_key"])
	if err != nil {
		return tools.Result{}, err
	}
	if !item.Archived {
		return tools.Result{}, fmt.Errorf("archive task %s before deleting it", planning.TaskKey(item))
	}
	return taskLifecycleResult(opts.Request.Args["task_key"], "deleted"), nil
}

func filterArchivedTasks(tasks []planning.Task) []planning.Task {
	out := make([]planning.Task, 0, len(tasks))
	for _, item := range tasks {
		if !item.Archived {
			out = append(out, item)
		}
	}
	return out
}

func planningTaskByKey(ctx context.Context, control tools.SessionControl, sessionID id.ID, taskKey string) (planning.Task, error) {
	tasks, err := control.ListTasks(ctx, sessionID, "")
	if err != nil {
		return planning.Task{}, err
	}
	for _, item := range tasks {
		if planning.TaskKey(item) == taskKey {
			return item, nil
		}
	}
	return planning.Task{}, fmt.Errorf("task %s not found", taskKey)
}

func taskLifecycleResult(key, action string) tools.Result {
	stored := tools.PlanningLifecycleStoredResult{Entity: "task", Key: key, Action: action}
	return tools.Result{Output: tools.DisplayTextForStored(tools.TaskArchive, stored), Stored: stored}
}

func (listTool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "Listed tasks", result.Output
}

func (addItemsTool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "Added tasks", result.Output
}

func (updateItemTool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "Updated task", result.Output
}

func (fetchNextTool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "Fetched next task", result.Output
}

func (archiveTool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	if req.Args["archived"] == "false" {
		return "Restored task", result.Output
	}
	return "Archived task", result.Output
}

func (deleteTool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "Deleted task", result.Output
}

func (listTool) FinalizeResult(ctx context.Context, runtime tools.Runtime, req tools.Request, result tools.Result) (tools.Result, error) {
	control, err := tools.RequireSessionControl(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	plan, tasks, ref, err := persistedTaskBucket(ctx, control, runtime.SessionID, req.Args["milestone_key"])
	if err != nil {
		return tools.Result{}, err
	}
	if req.Args["archived"] != "true" {
		if err := ensureMilestoneListed(plan, ref); err != nil {
			return tools.Result{}, err
		}
	}
	if req.Args["archived"] != "true" {
		tasks = filterArchivedTasks(tasks)
	}
	result.Stored = tools.BuildTaskListStoredResult(plan, ref, tasks, "")
	return result, nil
}
func (addItemsTool) FinalizeResult(ctx context.Context, runtime tools.Runtime, req tools.Request, result tools.Result) (tools.Result, error) {
	control, err := tools.RequireSessionControl(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	items, err := planning.ParseTaskAddItems(req.Args["items"])
	if err != nil {
		return tools.Result{}, err
	}
	plan, err := control.GetMilestonePlan(ctx, runtime.SessionID)
	if err != nil {
		return tools.Result{}, err
	}
	if err := ensureMilestoneAcceptsTasks(plan, req.Args["milestone_key"]); err != nil {
		return tools.Result{}, err
	}
	existing, err := control.ListTasks(ctx, runtime.SessionID, req.Args["milestone_key"])
	if err != nil {
		return tools.Result{}, err
	}
	if err := planning.ValidateNoDuplicateTaskContent(existing, items); err != nil {
		return tools.Result{}, err
	}
	created, err := control.AddTasks(ctx, runtime.SessionID, req.Args["milestone_key"], items)
	if err != nil {
		return tools.Result{}, err
	}
	stored := tools.BuildTaskListStoredResult(plan, req.Args["milestone_key"], created, "")
	result.Stored = stored
	result.Output = tools.FormatTaskOutput(stored)
	return result, nil
}
func (updateItemTool) FinalizeResult(ctx context.Context, runtime tools.Runtime, req tools.Request, result tools.Result) (tools.Result, error) {
	update, err := prepareTaskUpdate(ctx, runtime, req)
	if err != nil {
		return tools.Result{}, err
	}
	updated, err := update.control.UpdateTask(ctx, update.taskKey, update.status, req.Args["content"], req.Args["note"])
	if err != nil {
		return tools.Result{}, err
	}
	stored := tools.BuildTaskListStoredResult(update.plan, update.milestoneKey, []planning.Task{updated}, "")
	result.Stored = stored
	result.Output = tools.FormatTaskOutput(stored)
	return result, nil
}
func (archiveTool) FinalizeResult(ctx context.Context, runtime tools.Runtime, req tools.Request, result tools.Result) (tools.Result, error) {
	control, err := tools.RequireSessionControl(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	archived := req.Args["archived"] == "true"
	if _, err := control.ArchiveTask(ctx, req.Args["task_key"], archived); err != nil {
		return tools.Result{}, err
	}
	action := "archived"
	if !archived {
		action = "restored"
	}
	return taskLifecycleResult(req.Args["task_key"], action), nil
}
func (deleteTool) FinalizeResult(ctx context.Context, runtime tools.Runtime, req tools.Request, result tools.Result) (tools.Result, error) {
	control, err := tools.RequireSessionControl(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	if err := control.DeleteTask(ctx, req.Args["task_key"]); err != nil {
		return tools.Result{}, err
	}
	return taskLifecycleResult(req.Args["task_key"], "deleted"), nil
}

type preparedTaskUpdate struct {
	control        tools.SessionControl
	plan           planning.Plan
	taskKey        string
	status         planning.TaskStatus
	milestoneKey   string
	milestoneTitle string
	task           planning.Task
}

func prepareTaskUpdate(ctx context.Context, runtime tools.Runtime, req tools.Request) (preparedTaskUpdate, error) {
	control, err := tools.RequireSessionControl(runtime)
	if err != nil {
		return preparedTaskUpdate{}, err
	}
	taskKey, err := planning.ParseTaskKey(req.Args["task_key"])
	if err != nil {
		return preparedTaskUpdate{}, err
	}
	if err := tools.TaskScopeAllows(runtime, taskKey); err != nil {
		return preparedTaskUpdate{}, err
	}
	status, err := planning.ParseTaskStatus(req.Args["status"])
	if err != nil {
		return preparedTaskUpdate{}, fmt.Errorf("invalid task status %q", req.Args["status"])
	}
	plan, err := control.GetMilestonePlan(ctx, runtime.SessionID)
	if err != nil {
		return preparedTaskUpdate{}, err
	}
	allowedRef, err := tools.AllowedMilestoneKey(runtime, "")
	if err != nil {
		return preparedTaskUpdate{}, err
	}
	for _, milestone := range plan.Milestones {
		milestoneKey := planning.MilestoneKey(milestone)
		if allowedRef != "" && milestoneKey != allowedRef {
			continue
		}
		tasks, err := control.ListTasks(ctx, runtime.SessionID, milestoneKey)
		if err != nil {
			return preparedTaskUpdate{}, err
		}
		for idx := range tasks {
			if planning.TaskKey(tasks[idx]) != taskKey {
				continue
			}
			if tasks[idx].Archived {
				return preparedTaskUpdate{}, fmt.Errorf("task %s is archived; restore it before updating", taskKey)
			}
			if err := ensureTaskUpdateAllowed(runtime, milestone, tasks[idx]); err != nil {
				return preparedTaskUpdate{}, err
			}
			tasks[idx].Status = status
			if content := strings.TrimSpace(req.Args["content"]); content != "" {
				tasks[idx].Content = content
			}
			tasks[idx].Note = strings.TrimSpace(req.Args["note"])
			if err := planning.ValidateTaskProgress(tasks); err != nil {
				return preparedTaskUpdate{}, err
			}
			return preparedTaskUpdate{
				control:        control,
				plan:           plan,
				taskKey:        taskKey,
				status:         status,
				milestoneKey:   milestoneKey,
				milestoneTitle: milestone.Title,
				task:           tasks[idx],
			}, nil
		}
	}
	return preparedTaskUpdate{}, fmt.Errorf("task %s not found", taskKey)
}
func (fetchNextTool) FinalizeResult(ctx context.Context, runtime tools.Runtime, req tools.Request, result tools.Result) (tools.Result, error) {
	control, err := tools.RequireSessionControl(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	plan, tasks, ref, err := persistedTaskBucket(ctx, control, runtime.SessionID, req.Args["milestone_key"])
	if err != nil {
		return tools.Result{}, err
	}
	if err := ensureMilestoneListed(plan, ref); err != nil {
		return tools.Result{}, err
	}
	tasks = filterArchivedTasks(tasks)
	message := ""
	for _, item := range tasks {
		if item.Status == planning.TaskStatusInProgress {
			result.Stored = tools.BuildTaskListStoredResult(plan, ref, []planning.Task{item}, message)
			return result, nil
		}
	}
	for _, item := range tasks {
		if item.Status == planning.TaskStatusPending {
			result.Stored = tools.BuildTaskListStoredResult(plan, ref, []planning.Task{item}, message)
			return result, nil
		}
	}
	message = "All tasks for this milestone are done. If you have more planned tasks, move to the next milestone or break it down into tasks and start working on them."
	result.Stored = tools.BuildTaskListStoredResult(plan, ref, tasks, message)
	return result, nil
}
func ensureMilestoneAcceptsTasks(plan planning.Plan, ref string) error {
	ref = strings.TrimSpace(ref)
	for _, milestone := range plan.Milestones {
		if planning.MilestoneKey(milestone) != ref {
			continue
		}
		if milestone.Archived {
			return fmt.Errorf("milestone %q is archived; restore it before adding tasks", planning.MilestoneKey(milestone))
		}
		switch milestone.Status {
		case planning.MilestoneStatusCompleted, planning.MilestoneStatusCancelled:
			return fmt.Errorf("milestone %q is %s; cannot add tasks. To reopen it, first use milestones with action=update and status=ready, then add tasks", planning.MilestoneKey(milestone), milestone.Status.String())
		}
		return nil
	}
	return fmt.Errorf("milestone %q not found", ref)
}

func ensureMilestoneListed(plan planning.Plan, ref string) error {
	ref = strings.TrimSpace(ref)
	for _, milestone := range plan.Milestones {
		if planning.MilestoneKey(milestone) != ref {
			continue
		}
		if milestone.Archived {
			return fmt.Errorf("milestone %q is archived; pass archived=true to inspect its tasks or restore the milestone before continuing work", ref)
		}
		return nil
	}
	return nil
}

func ensureTaskUpdateAllowed(runtime tools.Runtime, milestone planning.Milestone, task planning.Task) error {
	if runtime.ChatRole != chatrole.Orchestrator || task.Status != planning.TaskStatusInProgress {
		return nil
	}
	if milestone.OwnerChatID == nil || *milestone.OwnerChatID == runtime.ChatID {
		return nil
	}
	key := planning.TaskKey(task)
	if key == "" {
		key = string(task.ID)
	}
	return fmt.Errorf("task %s is in_progress in milestone %q owned by chat %s; use chats with action=send to steer the worker instead of mutating the running task", key, planning.MilestoneKey(milestone), *milestone.OwnerChatID)
}

func persistedTaskBucket(ctx context.Context, control tools.SessionControl, sessionID id.ID, ref string) (planning.Plan, []planning.Task, string, error) {
	plan, err := control.GetMilestonePlan(ctx, sessionID)
	if err != nil {
		return planning.Plan{}, nil, "", err
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		active, ok := planning.ActiveMilestone(plan)
		if !ok {
			return planning.Plan{}, nil, "", fmt.Errorf("no active milestone; read milestones first or provide milestone_key")
		}
		ref = planning.MilestoneKey(active)
	}
	tasks, err := control.ListTasks(ctx, sessionID, ref)
	if err != nil {
		return planning.Plan{}, nil, "", err
	}
	return plan, tasks, ref, nil
}

func milestonePreview(ref, fallback string) string {
	if strings.TrimSpace(ref) == "" {
		return fallback
	}
	return ref
}
