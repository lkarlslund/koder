package todotool

import (
	"context"
	"fmt"
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
)

func init() {
	tools.Register(listTool{})
	tools.Register(addItemsTool{})
	tools.Register(updateItemTool{})
	tools.Register(fetchNextTool{})
}

type listTool struct{}
type addItemsTool struct{}
type updateItemTool struct{}
type fetchNextTool struct{}

func (listTool) Kind() domain.ToolKind       { return domain.ToolKindTodoList }
func (addItemsTool) Kind() domain.ToolKind   { return domain.ToolKindTodoAddItems }
func (updateItemTool) Kind() domain.ToolKind { return domain.ToolKindTodoUpdateItem }
func (fetchNextTool) Kind() domain.ToolKind  { return domain.ToolKindTodoFetchNext }

func (listTool) BypassesPermission() bool       { return true }
func (addItemsTool) BypassesPermission() bool   { return true }
func (updateItemTool) BypassesPermission() bool { return true }
func (fetchNextTool) BypassesPermission() bool  { return true }

func (listTool) Definition(tools.Runtime) (provider.ToolDefinition, bool) {
	return tools.FunctionDefinition(domain.ToolKindTodoList, "Read the todo bucket for a milestone. If milestone_ref is omitted, this reads the current in_progress milestone's todos.", `{"type":"object","properties":{"milestone_ref":{"type":"string","description":"Optional milestone ref; defaults to the in_progress milestone"}},"additionalProperties":false}`), true
}

func (addItemsTool) Definition(tools.Runtime) (provider.ToolDefinition, bool) {
	return tools.FunctionDefinition(domain.ToolKindTodoAddItems, "Append new pending todo items to a milestone's todo bucket. Use this to break down the current milestone into concrete execution steps.", `{"type":"object","properties":{"milestone_ref":{"type":"string","description":"Milestone ref that owns these todo items"},"items":{"type":"array","description":"New todo items to append as pending","items":{"type":"object","properties":{"content":{"type":"string"}},"required":["content"]}}},"required":["milestone_ref","items"],"additionalProperties":false}`), true
}

func (updateItemTool) Definition(tools.Runtime) (provider.ToolDefinition, bool) {
	return tools.FunctionDefinition(domain.ToolKindTodoUpdateItem, "Update one todo item's status, and optionally its content. Keep at most one todo item in_progress in a milestone bucket.", `{"type":"object","properties":{"id":{"type":"integer","description":"Todo item id"},"status":{"type":"string","enum":["pending","in_progress","completed"]},"content":{"type":"string","description":"Optional replacement content"}},"required":["id","status"],"additionalProperties":false}`), true
}

func (fetchNextTool) Definition(tools.Runtime) (provider.ToolDefinition, bool) {
	return tools.FunctionDefinition(domain.ToolKindTodoFetchNext, "Find the next todo item to work on for a milestone. If there is already an in_progress item, it is returned. Otherwise the first pending item is returned. If all items are done, this returns the finished bucket and a message telling you to move to the next milestone or break it down into todos.", `{"type":"object","properties":{"milestone_ref":{"type":"string","description":"Optional milestone ref; defaults to the in_progress milestone"}},"additionalProperties":false}`), true
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
	if _, err := tools.ParseTodoAddItems(raw); err != nil {
		return nil, err
	}
	return map[string]string{"milestone_ref": ref, "items": raw}, nil
}

func (updateItemTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	id, err := tools.ParseTodoID(tools.FirstArg(args, "id"))
	if err != nil {
		return nil, err
	}
	status, err := tools.ParseTodoStatus(tools.FirstArg(args, "status"))
	if err != nil {
		return nil, err
	}
	out := map[string]string{
		"id":     tools.FormatTodoID(id),
		"status": string(status),
	}
	if content := strings.TrimSpace(tools.FirstArg(args, "content")); content != "" {
		out["content"] = content
	}
	return out, nil
}

func (fetchNextTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	return listTool{}.NormalizeArgs(args)
}

func (listTool) LegacyArgs(raw string) map[string]string {
	return map[string]string{"milestone_ref": raw}
}
func (addItemsTool) LegacyArgs(raw string) map[string]string   { return map[string]string{"items": raw} }
func (updateItemTool) LegacyArgs(raw string) map[string]string { return map[string]string{"id": raw} }
func (fetchNextTool) LegacyArgs(raw string) map[string]string {
	return map[string]string{"milestone_ref": raw}
}

func (listTool) Preview(req tools.Request) string {
	return milestonePreview(req.Args["milestone_ref"], "List todos")
}
func (addItemsTool) Preview(req tools.Request) string {
	return milestonePreview(req.Args["milestone_ref"], "Add todo items")
}
func (updateItemTool) Preview(req tools.Request) string { return "Update todo #" + req.Args["id"] }
func (fetchNextTool) Preview(req tools.Request) string {
	return milestonePreview(req.Args["milestone_ref"], "Fetch next todo")
}

func (listTool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	st, err := tools.RequireSessionStore(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	ref, err := tools.AllowedMilestoneRef(runtime, req.Args["milestone_ref"])
	if err != nil {
		return tools.Result{}, err
	}
	plan, todos, ref, err := tools.PersistedTodoBucket(ctx, st, runtime.SessionID, ref)
	if err != nil {
		return tools.Result{}, err
	}
	return tools.TodoBucketResult(plan, ref, todos, ""), nil
}

func (addItemsTool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	items, err := tools.ParseTodoAddItems(req.Args["items"])
	if err != nil {
		return tools.Result{}, err
	}
	ref, err := tools.AllowedMilestoneRef(runtime, req.Args["milestone_ref"])
	if err != nil {
		return tools.Result{}, err
	}
	st, err := tools.RequireSessionStore(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	plan, err := st.GetMilestonePlan(ctx, runtime.SessionID)
	if err != nil {
		return tools.Result{}, err
	}
	title := tools.MilestoneTitle(plan, ref)
	todos := make([]store.TodoItem, 0, len(items))
	for _, content := range items {
		todos = append(todos, store.TodoItem{
			Content: content,
			Status:  domain.TodoStatusPending,
		})
	}
	return tools.TodoBucketResultWithTitle(ref, title, todos, ""), nil
}

func (updateItemTool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	st, err := tools.RequireSessionStore(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	id, _ := tools.ParseTodoID(req.Args["id"])
	plan, err := st.GetMilestonePlan(ctx, runtime.SessionID)
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
		todos, err := st.ListTodos(ctx, runtime.SessionID, milestone.Ref)
		if err != nil {
			return tools.Result{}, err
		}
		for idx := range todos {
			if todos[idx].ID != id {
				continue
			}
			todos[idx].Status = domain.TodoStatus(req.Args["status"])
			if content := strings.TrimSpace(req.Args["content"]); content != "" {
				todos[idx].Content = content
			}
			if err := tools.ValidateTodoProgress(todos); err != nil {
				return tools.Result{}, err
			}
			if _, err := st.UpdateTodoItem(ctx, id, domain.TodoStatus(req.Args["status"]), req.Args["content"]); err != nil {
				return tools.Result{}, err
			}
			todos, err = st.ListTodos(ctx, runtime.SessionID, milestone.Ref)
			if err != nil {
				return tools.Result{}, err
			}
			return tools.TodoBucketResultWithTitle(milestone.Ref, milestone.Title, todos, ""), nil
		}
	}
	return tools.Result{}, fmt.Errorf("todo item %d not found", id)
}

func (fetchNextTool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	st, err := tools.RequireSessionStore(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	ref, err := tools.AllowedMilestoneRef(runtime, req.Args["milestone_ref"])
	if err != nil {
		return tools.Result{}, err
	}
	plan, todos, ref, err := tools.PersistedTodoBucket(ctx, st, runtime.SessionID, ref)
	if err != nil {
		return tools.Result{}, err
	}
	for _, item := range todos {
		if item.Status == domain.TodoStatusInProgress {
			return tools.TodoBucketResult(plan, ref, []store.TodoItem{item}, ""), nil
		}
	}
	for _, item := range todos {
		if item.Status == domain.TodoStatusPending {
			return tools.TodoBucketResult(plan, ref, []store.TodoItem{item}, ""), nil
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

func (listTool) PersistResult(ctx context.Context, st *store.Store, sessionID int64, req tools.Request, result tools.Result) (<-chan domain.Event, error) {
	plan, todos, ref, err := tools.PersistedTodoBucket(ctx, st, sessionID, req.Args["milestone_ref"])
	if err != nil {
		return nil, err
	}
	result.Stored = tools.TodoStoredResult(plan, ref, todos, "")
	return tools.PersistStandardResult(ctx, st, sessionID, req, result)
}

func (addItemsTool) PersistResult(ctx context.Context, st *store.Store, sessionID int64, req tools.Request, result tools.Result) (<-chan domain.Event, error) {
	items, err := tools.ParseTodoAddItems(req.Args["items"])
	if err != nil {
		return nil, err
	}
	created, err := st.AddTodoItems(ctx, sessionID, req.Args["milestone_ref"], items)
	if err != nil {
		return nil, err
	}
	plan, err := st.GetMilestonePlan(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	result.Stored = tools.TodoStoredResult(plan, req.Args["milestone_ref"], created, "")
	return tools.PersistStandardResult(ctx, st, sessionID, req, result)
}

func (updateItemTool) PersistResult(ctx context.Context, st *store.Store, sessionID int64, req tools.Request, result tools.Result) (<-chan domain.Event, error) {
	id, _ := tools.ParseTodoID(req.Args["id"])
	plan, err := st.GetMilestonePlan(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	for _, milestone := range plan.Milestones {
		todos, err := st.ListTodos(ctx, sessionID, milestone.Ref)
		if err != nil {
			return nil, err
		}
		for idx := range todos {
			if todos[idx].ID != id {
				continue
			}
			todos[idx].Status = domain.TodoStatus(req.Args["status"])
			if content := strings.TrimSpace(req.Args["content"]); content != "" {
				todos[idx].Content = content
			}
			if err := tools.ValidateTodoProgress(todos); err != nil {
				return nil, err
			}
			if _, err := st.UpdateTodoItem(ctx, id, domain.TodoStatus(req.Args["status"]), req.Args["content"]); err != nil {
				return nil, err
			}
			todos, err = st.ListTodos(ctx, sessionID, milestone.Ref)
			if err != nil {
				return nil, err
			}
			result.Stored = tools.TodoStoredResult(plan, milestone.Ref, todos, "")
			return tools.PersistStandardResult(ctx, st, sessionID, req, result)
		}
	}
	return nil, fmt.Errorf("todo item %d not found", id)
}

func (fetchNextTool) PersistResult(ctx context.Context, st *store.Store, sessionID int64, req tools.Request, result tools.Result) (<-chan domain.Event, error) {
	plan, todos, ref, err := tools.PersistedTodoBucket(ctx, st, sessionID, req.Args["milestone_ref"])
	if err != nil {
		return nil, err
	}
	message := ""
	for _, item := range todos {
		if item.Status == domain.TodoStatusInProgress {
			result.Stored = tools.TodoStoredResult(plan, ref, []store.TodoItem{item}, message)
			return tools.PersistStandardResult(ctx, st, sessionID, req, result)
		}
	}
	for _, item := range todos {
		if item.Status == domain.TodoStatusPending {
			result.Stored = tools.TodoStoredResult(plan, ref, []store.TodoItem{item}, message)
			return tools.PersistStandardResult(ctx, st, sessionID, req, result)
		}
	}
	message = "All todo items for this milestone are done. If you have more planned tasks, move to the next milestone or break it down into todo items and start working on them."
	result.Stored = tools.TodoStoredResult(plan, ref, todos, message)
	return tools.PersistStandardResult(ctx, st, sessionID, req, result)
}

func milestonePreview(ref, fallback string) string {
	if strings.TrimSpace(ref) == "" {
		return fallback
	}
	return ref
}
