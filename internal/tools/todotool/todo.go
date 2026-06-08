package todotool

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
		Usage:       "Read the task list for a milestone. If milestone_ref is omitted, this reads the current assigned milestone's tasks.",
		Parameters:  `{"type":"object","properties":{"milestone_ref":{"type":"string","description":"Optional milestone ref; defaults to the assigned milestone"}},"additionalProperties":false}`,
		ExposeToLLM: true,
	})
	tools.Register(addItemsTool{}, tools.ToolSpec{
		Title:       "Add tasks",
		Description: "Append new pending tasks to a milestone.",
		Usage:       "Append new pending tasks to a milestone's task list. Use this to break down the current milestone into concrete execution steps. This rejects duplicate task content already present in the milestone; update existing tasks instead of adding duplicates.",
		Parameters:  `{"type":"object","properties":{"milestone_ref":{"type":"string","description":"Milestone ref that owns these tasks"},"items":{"type":"array","description":"New tasks to append as pending","items":{"type":"object","properties":{"content":{"type":"string"}},"required":["content"]}}},"required":["milestone_ref","items"],"additionalProperties":false}`,
		ExposeToLLM: true,
	})
	tools.Register(updateItemTool{}, tools.ToolSpec{
		Title:       "Update task",
		Description: "Update one task's status, note, or content.",
		Usage:       "Update one task's status and add a short note explaining what changed or why. Use the exact UUID id returned by task_list, task_fetch_next, or tasks_add. Do not invent numeric ids. Keep at most one task in_progress in a milestone. When marking completed, note what was completed in one concise sentence.",
		Parameters:  `{"type":"object","properties":{"id":{"type":"string","description":"Task UUID returned by task_list, task_fetch_next, or tasks_add"},"status":{"type":"string","enum":["pending","in_progress","completed"]},"note":{"type":"string","description":"Required short summary of what was done or why the status changed"},"content":{"type":"string","description":"Optional replacement content"}},"required":["id","status","note"],"additionalProperties":false}`,
		ExposeToLLM: true,
	})
	tools.Register(fetchNextTool{}, tools.ToolSpec{
		Title:       "Fetch next task",
		Description: "Find the next task to work on.",
		Usage:       "Find the next task to work on for a milestone. If there is already an in_progress task, it is returned. Otherwise the first pending task is returned. If all tasks are done, this returns the finished list and a message telling you to move to the next milestone or break it down into tasks.",
		Parameters:  `{"type":"object","properties":{"milestone_ref":{"type":"string","description":"Optional milestone ref; defaults to the assigned milestone"}},"additionalProperties":false}`,
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
	if tools.AssignedTodoRef(runtime) != "" {
		return tools.ToolSpec{}, false
	}
	return spec, true
}

func (listTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	out := map[string]string{}
	if ref := strings.TrimSpace(args["milestone_ref"]); ref != "" {
		out["milestone_ref"] = ref
	}
	return out, nil
}

func (addItemsTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	ref := strings.TrimSpace(args["milestone_ref"])
	if ref == "" {
		return nil, fmt.Errorf("milestone_ref is empty")
	}
	raw := strings.TrimSpace(args["items"])
	if raw == "" {
		return nil, fmt.Errorf("items is empty")
	}
	if _, err := planning.ParseTodoAddItems(raw); err != nil {
		return nil, err
	}
	return map[string]string{"milestone_ref": ref, "items": raw}, nil
}

func (updateItemTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	id, err := planning.ParseTodoID(args["id"])
	if err != nil {
		return nil, err
	}
	status, err := planning.ParseTodoStatus(args["status"])
	if err != nil {
		return nil, err
	}
	out := map[string]string{
		"id":     tools.FormatTodoID(id),
		"status": status.String(),
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
	return milestonePreview(req.Args["milestone_ref"], "List tasks")
}
func (addItemsTool) Preview(req tools.Request) string {
	return milestonePreview(req.Args["milestone_ref"], "Add tasks")
}
func (updateItemTool) Preview(req tools.Request) string { return "Update task " + req.Args["id"] }
func (fetchNextTool) Preview(req tools.Request) string {
	return milestonePreview(req.Args["milestone_ref"], "Fetch next task")
}

func (listTool) Call(ctx context.Context, opts tools.Options) (tools.Result, error) {
	runtime, req := opts.Runtime, opts.Request
	control, err := tools.RequireSessionControl(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	ref, err := tools.AllowedMilestoneRef(runtime, req.Args["milestone_ref"])
	if err != nil {
		return tools.Result{}, err
	}
	plan, todos, ref, err := persistedTodoBucket(ctx, control, runtime.SessionID, ref)
	if err != nil {
		return tools.Result{}, err
	}
	return tools.TodoBucketResult(plan, ref, tools.ScopedTodos(runtime, todos), ""), nil
}

func (addItemsTool) Call(ctx context.Context, opts tools.Options) (tools.Result, error) {
	runtime, req := opts.Runtime, opts.Request
	if tools.AssignedTodoRef(runtime) != "" {
		return tools.Result{}, fmt.Errorf("chat is scoped to task %q", tools.AssignedTodoRef(runtime))
	}
	items, err := planning.ParseTodoAddItems(req.Args["items"])
	if err != nil {
		return tools.Result{}, err
	}
	ref, err := tools.AllowedMilestoneRef(runtime, req.Args["milestone_ref"])
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
	if err := ensureMilestoneAcceptsTodos(plan, ref); err != nil {
		return tools.Result{}, err
	}
	existing, err := control.ListTodos(ctx, runtime.SessionID, ref)
	if err != nil {
		return tools.Result{}, err
	}
	if err := planning.ValidateNoDuplicateTodoContent(existing, items); err != nil {
		return tools.Result{}, err
	}
	title := planning.MilestoneTitle(plan, ref)
	todos := make([]planning.TodoItem, 0, len(items))
	for _, content := range items {
		todos = append(todos, planning.TodoItem{
			Content: content,
			Status:  planning.TodoStatusPending,
		})
	}
	return tools.TodoBucketResultWithTitle(ref, title, todos, ""), nil
}

func (updateItemTool) Call(ctx context.Context, opts tools.Options) (tools.Result, error) {
	runtime, req := opts.Runtime, opts.Request
	control, err := tools.RequireSessionControl(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	id, _ := planning.ParseTodoID(req.Args["id"])
	if err := tools.TodoScopeAllows(runtime, id); err != nil {
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
		if allowedRef != "" && milestone.Ref != allowedRef {
			continue
		}
		todos, err := control.ListTodos(ctx, runtime.SessionID, milestone.Ref)
		if err != nil {
			return tools.Result{}, err
		}
		for idx := range todos {
			if todos[idx].ID != id {
				continue
			}
			todoStatus, err := planning.ParseTodoStatus(req.Args["status"])
			if err != nil {
				return tools.Result{}, fmt.Errorf("invalid task status %q", req.Args["status"])
			}
			todos[idx].Status = todoStatus
			if content := strings.TrimSpace(req.Args["content"]); content != "" {
				todos[idx].Content = content
			}
			if err := planning.ValidateTodoProgress(todos); err != nil {
				return tools.Result{}, err
			}
			if _, err := control.UpdateTodoItem(ctx, id, todoStatus, req.Args["content"], req.Args["note"]); err != nil {
				return tools.Result{}, err
			}
			todos, err = control.ListTodos(ctx, runtime.SessionID, milestone.Ref)
			if err != nil {
				return tools.Result{}, err
			}
			return tools.TodoBucketResultWithTitle(milestone.Ref, milestone.Title, todos, ""), nil
		}
	}
	return tools.Result{}, fmt.Errorf("task %s not found", id)
}

func (fetchNextTool) Call(ctx context.Context, opts tools.Options) (tools.Result, error) {
	runtime, req := opts.Runtime, opts.Request
	control, err := tools.RequireSessionControl(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	ref, err := tools.AllowedMilestoneRef(runtime, req.Args["milestone_ref"])
	if err != nil {
		return tools.Result{}, err
	}
	plan, todos, ref, err := persistedTodoBucket(ctx, control, runtime.SessionID, ref)
	if err != nil {
		return tools.Result{}, err
	}
	todos = tools.ScopedTodos(runtime, todos)
	for _, item := range todos {
		if item.Status == planning.TodoStatusInProgress {
			return tools.TodoBucketResult(plan, ref, []planning.TodoItem{item}, ""), nil
		}
	}
	for _, item := range todos {
		if item.Status == planning.TodoStatusPending {
			return tools.TodoBucketResult(plan, ref, []planning.TodoItem{item}, ""), nil
		}
	}
	message := "All tasks for this milestone are done. If you have more planned tasks, move to the next milestone or break it down into tasks and start working on them."
	return tools.TodoBucketResult(plan, ref, todos, message), nil
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
	plan, todos, ref, err := persistedTodoBucket(ctx, control, runtime.SessionID, req.Args["milestone_ref"])
	if err != nil {
		return tools.Result{}, err
	}
	result.Stored = tools.TodoStoredResult(plan, ref, todos, "")
	return result, nil
}
func (addItemsTool) FinalizeResult(ctx context.Context, runtime tools.Runtime, req tools.Request, result tools.Result) (tools.Result, error) {
	control, err := tools.RequireSessionControl(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	items, err := planning.ParseTodoAddItems(req.Args["items"])
	if err != nil {
		return tools.Result{}, err
	}
	plan, err := control.GetMilestonePlan(ctx, runtime.SessionID)
	if err != nil {
		return tools.Result{}, err
	}
	if err := ensureMilestoneAcceptsTodos(plan, req.Args["milestone_ref"]); err != nil {
		return tools.Result{}, err
	}
	existing, err := control.ListTodos(ctx, runtime.SessionID, req.Args["milestone_ref"])
	if err != nil {
		return tools.Result{}, err
	}
	if err := planning.ValidateNoDuplicateTodoContent(existing, items); err != nil {
		return tools.Result{}, err
	}
	created, err := control.AddTodoItems(ctx, runtime.SessionID, req.Args["milestone_ref"], items)
	if err != nil {
		return tools.Result{}, err
	}
	stored := tools.TodoStoredResult(plan, req.Args["milestone_ref"], created, "")
	result.Stored = stored
	result.Output = tools.FormatTodoOutput(stored)
	return result, nil
}
func (updateItemTool) FinalizeResult(ctx context.Context, runtime tools.Runtime, req tools.Request, result tools.Result) (tools.Result, error) {
	control, err := tools.RequireSessionControl(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	id, _ := planning.ParseTodoID(req.Args["id"])
	plan, err := control.GetMilestonePlan(ctx, runtime.SessionID)
	if err != nil {
		return tools.Result{}, err
	}
	for _, milestone := range plan.Milestones {
		todos, err := control.ListTodos(ctx, runtime.SessionID, milestone.Ref)
		if err != nil {
			return tools.Result{}, err
		}
		todoStatus, err := planning.ParseTodoStatus(req.Args["status"])
		if err != nil {
			return tools.Result{}, fmt.Errorf("invalid task status %q", req.Args["status"])
		}
		for idx := range todos {
			if todos[idx].ID != id {
				continue
			}
			todos[idx].Status = todoStatus
			if content := strings.TrimSpace(req.Args["content"]); content != "" {
				todos[idx].Content = content
			}
			if err := planning.ValidateTodoProgress(todos); err != nil {
				return tools.Result{}, err
			}
			if _, err := control.UpdateTodoItem(ctx, id, todoStatus, req.Args["content"], req.Args["note"]); err != nil {
				return tools.Result{}, err
			}
			todos, err = control.ListTodos(ctx, runtime.SessionID, milestone.Ref)
			if err != nil {
				return tools.Result{}, err
			}
			result.Stored = tools.TodoStoredResult(plan, milestone.Ref, todos, "")
			return result, nil
		}
	}
	return tools.Result{}, fmt.Errorf("task %s not found", id)
}
func (fetchNextTool) FinalizeResult(ctx context.Context, runtime tools.Runtime, req tools.Request, result tools.Result) (tools.Result, error) {
	control, err := tools.RequireSessionControl(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	plan, todos, ref, err := persistedTodoBucket(ctx, control, runtime.SessionID, req.Args["milestone_ref"])
	if err != nil {
		return tools.Result{}, err
	}
	message := ""
	for _, item := range todos {
		if item.Status == planning.TodoStatusInProgress {
			result.Stored = tools.TodoStoredResult(plan, ref, []planning.TodoItem{item}, message)
			return result, nil
		}
	}
	for _, item := range todos {
		if item.Status == planning.TodoStatusPending {
			result.Stored = tools.TodoStoredResult(plan, ref, []planning.TodoItem{item}, message)
			return result, nil
		}
	}
	message = "All tasks for this milestone are done. If you have more planned tasks, move to the next milestone or break it down into tasks and start working on them."
	result.Stored = tools.TodoStoredResult(plan, ref, todos, message)
	return result, nil
}
func ensureMilestoneAcceptsTodos(plan planning.Plan, ref string) error {
	ref = strings.TrimSpace(ref)
	for _, milestone := range plan.Milestones {
		if milestone.Ref != ref {
			continue
		}
		switch milestone.Status {
		case planning.MilestoneStatusCompleted, planning.MilestoneStatusCancelled:
			return fmt.Errorf("milestone %q is %s; cannot add tasks. To reopen this milestone, first call milestone_update with status=ready, then add tasks", milestone.Ref, milestone.Status.String())
		}
		return nil
	}
	return nil
}

func persistedTodoBucket(ctx context.Context, control tools.SessionControl, sessionID id.ID, ref string) (planning.Plan, []planning.TodoItem, string, error) {
	plan, err := control.GetMilestonePlan(ctx, sessionID)
	if err != nil {
		return planning.Plan{}, nil, "", err
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		active, ok := planning.ActiveMilestone(plan)
		if !ok {
			return planning.Plan{}, nil, "", fmt.Errorf("no active milestone; read milestones first or provide milestone_ref")
		}
		ref = active.Ref
	}
	todos, err := control.ListTodos(ctx, sessionID, ref)
	if err != nil {
		return planning.Plan{}, nil, "", err
	}
	return plan, todos, ref, nil
}

func milestonePreview(ref, fallback string) string {
	if strings.TrimSpace(ref) == "" {
		return fallback
	}
	return ref
}
