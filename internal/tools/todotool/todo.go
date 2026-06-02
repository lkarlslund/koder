package todotool

import (
	"context"
	"fmt"
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/planning"
	"github.com/lkarlslund/koder/internal/tools"
)

func init() {
	tools.Register(listTool{}, tools.ToolSpec{
		Title:       "List todos",
		Description: "Read the todo bucket for a milestone.",
		Usage:       "Read the todo bucket for a milestone. If milestone_ref is omitted, this reads the current assigned milestone's todos.",
		Parameters:  `{"type":"object","properties":{"milestone_ref":{"type":"string","description":"Optional milestone ref; defaults to the assigned milestone"}},"additionalProperties":false}`,
		ExposeToLLM: true,
	})
	tools.Register(addItemsTool{}, tools.ToolSpec{
		Title:       "Add todo items",
		Description: "Append new pending todo items to a milestone.",
		Usage:       "Append new pending todo items to a milestone's todo bucket. Use this to break down the current milestone into concrete execution steps. This rejects duplicate todo content already present in the milestone; update existing todos instead of adding duplicates.",
		Parameters:  `{"type":"object","properties":{"milestone_ref":{"type":"string","description":"Milestone ref that owns these todo items"},"items":{"type":"array","description":"New todo items to append as pending","items":{"type":"object","properties":{"content":{"type":"string"}},"required":["content"]}}},"required":["milestone_ref","items"],"additionalProperties":false}`,
		ExposeToLLM: true,
	})
	tools.Register(updateItemTool{}, tools.ToolSpec{
		Title:       "Update todo item",
		Description: "Update one todo item's status, note, or content.",
		Usage:       "Update one todo item's status and add a short note explaining what changed or why. Use the exact UUID id returned by todo_list, todo_fetch_next, or todos_add. Do not invent numeric ids. Keep at most one todo item in_progress in a milestone bucket. When marking completed, note what was completed in one concise sentence.",
		Parameters:  `{"type":"object","properties":{"id":{"type":"string","description":"Todo item UUID returned by todo_list, todo_fetch_next, or todos_add"},"status":{"type":"string","enum":["pending","in_progress","completed"]},"note":{"type":"string","description":"Required short summary of what was done or why the status changed"},"content":{"type":"string","description":"Optional replacement content"}},"required":["id","status","note"],"additionalProperties":false}`,
		ExposeToLLM: true,
	})
	tools.Register(fetchNextTool{}, tools.ToolSpec{
		Title:       "Fetch next todo",
		Description: "Find the next todo item to work on.",
		Usage:       "Find the next todo item to work on for a milestone. If there is already an in_progress item, it is returned. Otherwise the first pending item is returned. If all items are done, this returns the finished bucket and a message telling you to move to the next milestone or break it down into todos.",
		Parameters:  `{"type":"object","properties":{"milestone_ref":{"type":"string","description":"Optional milestone ref; defaults to the assigned milestone"}},"additionalProperties":false}`,
		ExposeToLLM: true,
	})
}

type listTool struct{}
type addItemsTool struct{}
type updateItemTool struct{}
type fetchNextTool struct{}

func (listTool) Kind() domain.ToolKind       { return domain.ToolKindTodoList }
func (addItemsTool) Kind() domain.ToolKind   { return domain.ToolKindTodosAdd }
func (updateItemTool) Kind() domain.ToolKind { return domain.ToolKindTodosUpdate }
func (fetchNextTool) Kind() domain.ToolKind  { return domain.ToolKindTodoFetchNext }

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
	if ref := strings.TrimSpace(tools.FirstArg(args, "milestone_ref", "ref")); ref != "" {
		out["milestone_ref"] = ref
	}
	return out, nil
}

func (addItemsTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	ref := strings.TrimSpace(tools.FirstArg(args, "milestone_ref", "ref"))
	if ref == "" {
		return nil, fmt.Errorf("milestone_ref is empty")
	}
	raw := strings.TrimSpace(tools.FirstArg(args, "items"))
	if raw == "" {
		return nil, fmt.Errorf("items is empty")
	}
	if _, err := planning.ParseTodoAddItems(raw); err != nil {
		return nil, err
	}
	return map[string]string{"milestone_ref": ref, "items": raw}, nil
}

func (updateItemTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	id, err := planning.ParseTodoID(tools.FirstArg(args, "id"))
	if err != nil {
		return nil, err
	}
	status, err := planning.ParseTodoStatus(tools.FirstArg(args, "status"))
	if err != nil {
		return nil, err
	}
	out := map[string]string{
		"id":     tools.FormatTodoID(id),
		"status": status.String(),
	}
	note := strings.TrimSpace(tools.FirstArg(args, "note"))
	if note == "" {
		return nil, fmt.Errorf("note is required")
	}
	out["note"] = note
	if content := strings.TrimSpace(tools.FirstArg(args, "content")); content != "" {
		out["content"] = content
	}
	return out, nil
}

func (fetchNextTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	return listTool{}.NormalizeArgs(args)
}

func (listTool) Preview(req tools.Request) string {
	return milestonePreview(req.Args["milestone_ref"], "List todos")
}
func (addItemsTool) Preview(req tools.Request) string {
	return milestonePreview(req.Args["milestone_ref"], "Add todo items")
}
func (updateItemTool) Preview(req tools.Request) string { return "Update todo " + req.Args["id"] }
func (fetchNextTool) Preview(req tools.Request) string {
	return milestonePreview(req.Args["milestone_ref"], "Fetch next todo")
}

func (listTool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
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

func (addItemsTool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	if tools.AssignedTodoRef(runtime) != "" {
		return tools.Result{}, fmt.Errorf("chat is scoped to todo %q", tools.AssignedTodoRef(runtime))
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

func (updateItemTool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
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
				return tools.Result{}, fmt.Errorf("invalid todo status %q", req.Args["status"])
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
	return tools.Result{}, fmt.Errorf("todo item %s not found", id)
}

func (fetchNextTool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
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
	message := "All todo items for this milestone are done. If you have more planned tasks, move to the next milestone or break it down into todo items and start working on them."
	return tools.TodoBucketResult(plan, ref, todos, message), nil
}

func (listTool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "Listed todos", result.Output
}

func (addItemsTool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "Added todo items", result.Output
}

func (updateItemTool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "Updated todo item", result.Output
}

func (fetchNextTool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "Fetched next todo", result.Output
}

func (listTool) PersistResult(ctx context.Context, runtime tools.Runtime, req tools.Request, result tools.Result) (<-chan domain.Event, error) {
	control, err := tools.RequireSessionControl(runtime)
	if err != nil {
		return nil, err
	}
	plan, todos, ref, err := persistedTodoBucket(ctx, control, runtime.SessionID, req.Args["milestone_ref"])
	if err != nil {
		return nil, err
	}
	result.Stored = tools.TodoStoredResult(plan, ref, todos, "")
	return tools.PersistStandardResult(ctx, runtime, req, result)
}

func (addItemsTool) PersistResult(ctx context.Context, runtime tools.Runtime, req tools.Request, result tools.Result) (<-chan domain.Event, error) {
	control, err := tools.RequireSessionControl(runtime)
	if err != nil {
		return nil, err
	}
	items, err := planning.ParseTodoAddItems(req.Args["items"])
	if err != nil {
		return nil, err
	}
	plan, err := control.GetMilestonePlan(ctx, runtime.SessionID)
	if err != nil {
		return nil, err
	}
	if err := ensureMilestoneAcceptsTodos(plan, req.Args["milestone_ref"]); err != nil {
		return nil, err
	}
	existing, err := control.ListTodos(ctx, runtime.SessionID, req.Args["milestone_ref"])
	if err != nil {
		return nil, err
	}
	if err := planning.ValidateNoDuplicateTodoContent(existing, items); err != nil {
		return nil, err
	}
	created, err := control.AddTodoItems(ctx, runtime.SessionID, req.Args["milestone_ref"], items)
	if err != nil {
		return nil, err
	}
	stored := tools.TodoStoredResult(plan, req.Args["milestone_ref"], created, "")
	result.Stored = stored
	result.Output = tools.FormatTodoOutput(stored)
	return tools.PersistStandardResult(ctx, runtime, req, result)
}

func (updateItemTool) PersistResult(ctx context.Context, runtime tools.Runtime, req tools.Request, result tools.Result) (<-chan domain.Event, error) {
	control, err := tools.RequireSessionControl(runtime)
	if err != nil {
		return nil, err
	}
	id, _ := planning.ParseTodoID(req.Args["id"])
	plan, err := control.GetMilestonePlan(ctx, runtime.SessionID)
	if err != nil {
		return nil, err
	}
	for _, milestone := range plan.Milestones {
		todos, err := control.ListTodos(ctx, runtime.SessionID, milestone.Ref)
		if err != nil {
			return nil, err
		}
		todoStatus, err := planning.ParseTodoStatus(req.Args["status"])
		if err != nil {
			return nil, fmt.Errorf("invalid todo status %q", req.Args["status"])
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
				return nil, err
			}
			if _, err := control.UpdateTodoItem(ctx, id, todoStatus, req.Args["content"], req.Args["note"]); err != nil {
				return nil, err
			}
			todos, err = control.ListTodos(ctx, runtime.SessionID, milestone.Ref)
			if err != nil {
				return nil, err
			}
			result.Stored = tools.TodoStoredResult(plan, milestone.Ref, todos, "")
			return tools.PersistStandardResult(ctx, runtime, req, result)
		}
	}
	return nil, fmt.Errorf("todo item %s not found", id)
}

func (fetchNextTool) PersistResult(ctx context.Context, runtime tools.Runtime, req tools.Request, result tools.Result) (<-chan domain.Event, error) {
	control, err := tools.RequireSessionControl(runtime)
	if err != nil {
		return nil, err
	}
	plan, todos, ref, err := persistedTodoBucket(ctx, control, runtime.SessionID, req.Args["milestone_ref"])
	if err != nil {
		return nil, err
	}
	message := ""
	for _, item := range todos {
		if item.Status == planning.TodoStatusInProgress {
			result.Stored = tools.TodoStoredResult(plan, ref, []planning.TodoItem{item}, message)
			return tools.PersistStandardResult(ctx, runtime, req, result)
		}
	}
	for _, item := range todos {
		if item.Status == planning.TodoStatusPending {
			result.Stored = tools.TodoStoredResult(plan, ref, []planning.TodoItem{item}, message)
			return tools.PersistStandardResult(ctx, runtime, req, result)
		}
	}
	message = "All todo items for this milestone are done. If you have more planned tasks, move to the next milestone or break it down into todo items and start working on them."
	result.Stored = tools.TodoStoredResult(plan, ref, todos, message)
	return tools.PersistStandardResult(ctx, runtime, req, result)
}

func ensureMilestoneAcceptsTodos(plan planning.Plan, ref string) error {
	ref = strings.TrimSpace(ref)
	for _, milestone := range plan.Milestones {
		if milestone.Ref != ref {
			continue
		}
		switch milestone.Status {
		case planning.MilestoneStatusCompleted, planning.MilestoneStatusCancelled:
			return fmt.Errorf("milestone %q is %s; cannot add todos. To reopen this milestone, first call milestone_update with status=ready, then add todos", milestone.Ref, milestone.Status.String())
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
