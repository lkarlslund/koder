package taskstool

import (
	"context"
	"fmt"
	"strings"

	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/planning"
	"github.com/lkarlslund/koder/internal/tools"
)

func init() {
	tools.Register(listTool{}, tools.ToolSpec{
		Title:       "List tasks",
		Description: "Read the task list for a milestone.",
		Usage:       "Read the task list for a milestone. If milestone_key is omitted, this reads the current assigned milestone's tasks.",
		Parameters:  `{"type":"object","properties":{"milestone_key":{"type":"string","description":"Optional milestone key; defaults to the assigned milestone"}},"additionalProperties":false}`,
		ExposeToLLM: true,
	})
	tools.Register(addItemsTool{}, tools.ToolSpec{
		Title:       "Add tasks",
		Description: "Append new pending tasks to a milestone.",
		Usage:       "Append new pending tasks to a milestone's task list. Use this to break down the current milestone into concrete execution steps. This rejects duplicate task content already present in the milestone; update existing tasks instead of adding duplicates.",
		Parameters:  `{"type":"object","properties":{"milestone_key":{"type":"string","description":"Milestone key that owns these tasks"},"items":{"type":"array","description":"New tasks to append as pending","items":{"type":"object","properties":{"content":{"type":"string"}},"required":["content"]}}},"required":["milestone_key","items"],"additionalProperties":false}`,
		ExposeToLLM: true,
	})
	tools.Register(updateItemTool{}, tools.ToolSpec{
		Title:       "Update task",
		Description: "Update one task's status, note, or content.",
		Usage:       "Update one task's status and add a short note explaining what changed or why. Use the exact task_key returned by task_list, task_fetch_next, or tasks_add. Do not invent keys. Keep at most one task in_progress in a milestone. When marking completed, note what was completed in one concise sentence.",
		Parameters:  `{"type":"object","properties":{"task_key":{"type":"string","description":"Task key returned by task_list, task_fetch_next, or tasks_add"},"status":{"type":"string","enum":["pending","in_progress","completed","cancelled"]},"note":{"type":"string","description":"Required short summary of what was done or why the status changed"},"content":{"type":"string","description":"Optional replacement content"}},"required":["task_key","status","note"],"additionalProperties":false}`,
		ExposeToLLM: true,
	})
	tools.Register(fetchNextTool{}, tools.ToolSpec{
		Title:       "Fetch next task",
		Description: "Find the next task to work on.",
		Usage:       "Find the next task to work on for a milestone. If there is already an in_progress task, it is returned. Otherwise the first pending task is returned. If all tasks are done, this returns the finished list and a message telling you to move to the next milestone or break it down into tasks.",
		Parameters:  `{"type":"object","properties":{"milestone_key":{"type":"string","description":"Optional milestone key; defaults to the assigned milestone"}},"additionalProperties":false}`,
		ExposeToLLM: true,
	})
}

type listTool struct{}
type addItemsTool struct{}
type updateItemTool struct{}
type fetchNextTool struct{}

func (listTool) ID() tools.ID       { return tools.TaskList }
func (addItemsTool) ID() tools.ID   { return tools.TasksAdd }
func (updateItemTool) ID() tools.ID { return tools.TasksUpdate }
func (fetchNextTool) ID() tools.ID  { return tools.TaskFetchNext }

func (listTool) BypassesPermission() bool       { return true }
func (addItemsTool) BypassesPermission() bool   { return true }
func (updateItemTool) BypassesPermission() bool { return true }
func (fetchNextTool) BypassesPermission() bool  { return true }

func (addItemsTool) Definition(runtime tools.Runtime, spec tools.ToolSpec) (tools.ToolSpec, bool) {
	if tools.AssignedTaskRef(runtime) != "" {
		return tools.ToolSpec{}, false
	}
	return spec, true
}

func (listTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	out := map[string]string{}
	if ref := strings.TrimSpace(args["milestone_key"]); ref != "" {
		out["milestone_key"] = ref
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
	return listTool{}.NormalizeArgs(args)
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

func (listTool) Call(ctx context.Context, opts tools.Options) (tools.Result, error) {
	runtime, req := opts.Runtime, opts.Request
	control, err := tools.RequireSessionControl(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	ref, err := tools.AllowedMilestoneRef(runtime, req.Args["milestone_key"])
	if err != nil {
		return tools.Result{}, err
	}
	plan, tasks, ref, err := persistedTaskBucket(ctx, control, runtime.SessionID, ref)
	if err != nil {
		return tools.Result{}, err
	}
	return tools.TaskBucketResult(plan, ref, tools.ScopedTasks(runtime, tasks), ""), nil
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
	ref, err := tools.AllowedMilestoneRef(runtime, req.Args["milestone_key"])
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
	runtime, req := opts.Runtime, opts.Request
	control, err := tools.RequireSessionControl(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	taskKey, _ := planning.ParseTaskKey(req.Args["task_key"])
	if err := tools.TaskScopeAllows(runtime, taskKey); err != nil {
		return tools.Result{}, err
	}
	plan, err := control.GetMilestonePlan(ctx, runtime.SessionID)
	if err != nil {
		return tools.Result{}, err
	}
	allowedRef, err := tools.AllowedMilestoneRef(runtime, "")
	if err != nil {
		return tools.Result{}, err
	}
	for _, milestone := range plan.Milestones {
		milestoneKey := planning.MilestoneKey(milestone)
		if allowedRef != "" && milestoneKey != allowedRef {
			continue
		}
		tasks, err := control.ListTasks(ctx, runtime.SessionID, milestoneKey)
		if err != nil {
			return tools.Result{}, err
		}
		for idx := range tasks {
			if planning.TaskKey(tasks[idx]) != taskKey {
				continue
			}
			taskStatus, err := planning.ParseTaskStatus(req.Args["status"])
			if err != nil {
				return tools.Result{}, fmt.Errorf("invalid task status %q", req.Args["status"])
			}
			tasks[idx].Status = taskStatus
			if content := strings.TrimSpace(req.Args["content"]); content != "" {
				tasks[idx].Content = content
			}
			if err := planning.ValidateTaskProgress(tasks); err != nil {
				return tools.Result{}, err
			}
			if _, err := control.UpdateTask(ctx, taskKey, taskStatus, req.Args["content"], req.Args["note"]); err != nil {
				return tools.Result{}, err
			}
			tasks, err = control.ListTasks(ctx, runtime.SessionID, milestoneKey)
			if err != nil {
				return tools.Result{}, err
			}
			return tools.TaskBucketResultWithTitle(milestoneKey, milestone.Title, tasks, ""), nil
		}
	}
	return tools.Result{}, fmt.Errorf("task %s not found", taskKey)
}

func (fetchNextTool) Call(ctx context.Context, opts tools.Options) (tools.Result, error) {
	runtime, req := opts.Runtime, opts.Request
	control, err := tools.RequireSessionControl(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	ref, err := tools.AllowedMilestoneRef(runtime, req.Args["milestone_key"])
	if err != nil {
		return tools.Result{}, err
	}
	plan, tasks, ref, err := persistedTaskBucket(ctx, control, runtime.SessionID, ref)
	if err != nil {
		return tools.Result{}, err
	}
	tasks = tools.ScopedTasks(runtime, tasks)
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

func (listTool) FinalizeResult(ctx context.Context, runtime tools.Runtime, req tools.Request, result tools.Result) (tools.Result, error) {
	control, err := tools.RequireSessionControl(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	plan, tasks, ref, err := persistedTaskBucket(ctx, control, runtime.SessionID, req.Args["milestone_key"])
	if err != nil {
		return tools.Result{}, err
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
	control, err := tools.RequireSessionControl(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	taskKey, _ := planning.ParseTaskKey(req.Args["task_key"])
	plan, err := control.GetMilestonePlan(ctx, runtime.SessionID)
	if err != nil {
		return tools.Result{}, err
	}
	for _, milestone := range plan.Milestones {
		milestoneKey := planning.MilestoneKey(milestone)
		tasks, err := control.ListTasks(ctx, runtime.SessionID, milestoneKey)
		if err != nil {
			return tools.Result{}, err
		}
		taskStatus, err := planning.ParseTaskStatus(req.Args["status"])
		if err != nil {
			return tools.Result{}, fmt.Errorf("invalid task status %q", req.Args["status"])
		}
		for idx := range tasks {
			if planning.TaskKey(tasks[idx]) != taskKey {
				continue
			}
			tasks[idx].Status = taskStatus
			if content := strings.TrimSpace(req.Args["content"]); content != "" {
				tasks[idx].Content = content
			}
			if err := planning.ValidateTaskProgress(tasks); err != nil {
				return tools.Result{}, err
			}
			if _, err := control.UpdateTask(ctx, taskKey, taskStatus, req.Args["content"], req.Args["note"]); err != nil {
				return tools.Result{}, err
			}
			tasks, err = control.ListTasks(ctx, runtime.SessionID, milestoneKey)
			if err != nil {
				return tools.Result{}, err
			}
			result.Stored = tools.BuildTaskListStoredResult(plan, milestoneKey, tasks, "")
			return result, nil
		}
	}
	return tools.Result{}, fmt.Errorf("task %s not found", taskKey)
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
		switch milestone.Status {
		case planning.MilestoneStatusCompleted, planning.MilestoneStatusCancelled:
			return fmt.Errorf("milestone %q is %s; cannot add tasks. To reopen this milestone, first call milestone_update with status=ready, then add tasks", planning.MilestoneKey(milestone), milestone.Status.String())
		}
		return nil
	}
	return nil
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
