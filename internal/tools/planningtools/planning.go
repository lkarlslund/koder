package planningtools

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
)

func init() {
	tools.Register(milestoneListTool{})
	tools.Register(milestoneWriteTool{})
	tools.Register(todoListTool{})
	tools.Register(todoAddItemsTool{})
	tools.Register(todoUpdateItemTool{})
	tools.Register(todoFetchNextTool{})
}

type milestoneListTool struct{}
type milestoneWriteTool struct{}
type todoListTool struct{}
type todoAddItemsTool struct{}
type todoUpdateItemTool struct{}
type todoFetchNextTool struct{}

func (milestoneListTool) Kind() domain.ToolKind     { return domain.ToolKindMilestoneList }
func (milestoneWriteTool) Kind() domain.ToolKind    { return domain.ToolKindMilestoneWrite }
func (todoListTool) Kind() domain.ToolKind          { return domain.ToolKindTodoList }
func (todoAddItemsTool) Kind() domain.ToolKind      { return domain.ToolKindTodoAddItems }
func (todoUpdateItemTool) Kind() domain.ToolKind    { return domain.ToolKindTodoUpdateItem }
func (todoFetchNextTool) Kind() domain.ToolKind     { return domain.ToolKindTodoFetchNext }
func (milestoneListTool) BypassesPermission() bool  { return true }
func (milestoneWriteTool) BypassesPermission() bool { return true }
func (todoListTool) BypassesPermission() bool       { return true }
func (todoAddItemsTool) BypassesPermission() bool   { return true }
func (todoUpdateItemTool) BypassesPermission() bool { return true }
func (todoFetchNextTool) BypassesPermission() bool  { return true }

func (milestoneListTool) Definition(tools.Runtime) (provider.ToolDefinition, bool) {
	return tools.FunctionDefinition(domain.ToolKindMilestoneList, "Read the current session milestone plan. Use this to understand the long-horizon plan before choosing or breaking down work.", `{"type":"object","properties":{},"additionalProperties":false}`), true
}

func (milestoneWriteTool) Definition(tools.Runtime) (provider.ToolDefinition, bool) {
	return tools.FunctionDefinition(domain.ToolKindMilestoneWrite, "Replace the current session milestone plan. Use milestones for larger chunks of work and keep at most one milestone in_progress. Each milestone requires a stable ref so its todo bucket can be tracked separately.", `{"type":"object","properties":{"summary":{"type":"string","description":"Optional summary of the overall plan"},"milestones":{"type":"array","description":"Ordered milestone list","items":{"type":"object","properties":{"ref":{"type":"string"},"title":{"type":"string"},"status":{"type":"string","enum":["pending","in_progress","completed","blocked"]},"notes":{"type":"string"}},"required":["ref","title","status"]}}},"required":["milestones"],"additionalProperties":false}`), true
}

func (todoListTool) Definition(tools.Runtime) (provider.ToolDefinition, bool) {
	return tools.FunctionDefinition(domain.ToolKindTodoList, "Read the todo bucket for a milestone. If milestone_ref is omitted, this reads the current in_progress milestone's todos.", `{"type":"object","properties":{"milestone_ref":{"type":"string","description":"Optional milestone ref; defaults to the in_progress milestone"}},"additionalProperties":false}`), true
}

func (todoAddItemsTool) Definition(tools.Runtime) (provider.ToolDefinition, bool) {
	return tools.FunctionDefinition(domain.ToolKindTodoAddItems, "Append new pending todo items to a milestone's todo bucket. Use this to break down the current milestone into concrete execution steps.", `{"type":"object","properties":{"milestone_ref":{"type":"string","description":"Milestone ref that owns these todo items"},"items":{"type":"array","description":"New todo items to append as pending","items":{"type":"object","properties":{"content":{"type":"string"}},"required":["content"]}}},"required":["milestone_ref","items"],"additionalProperties":false}`), true
}

func (todoUpdateItemTool) Definition(tools.Runtime) (provider.ToolDefinition, bool) {
	return tools.FunctionDefinition(domain.ToolKindTodoUpdateItem, "Update one todo item's status, and optionally its content. Keep at most one todo item in_progress in a milestone bucket.", `{"type":"object","properties":{"id":{"type":"integer","description":"Todo item id"},"status":{"type":"string","enum":["pending","in_progress","completed"]},"content":{"type":"string","description":"Optional replacement content"}},"required":["id","status"],"additionalProperties":false}`), true
}

func (todoFetchNextTool) Definition(tools.Runtime) (provider.ToolDefinition, bool) {
	return tools.FunctionDefinition(domain.ToolKindTodoFetchNext, "Find the next todo item to work on for a milestone. If there is already an in_progress item, it is returned. Otherwise the first pending item is returned. If all items are done, this returns the finished bucket and a message telling you to move to the next milestone or break it down into todos.", `{"type":"object","properties":{"milestone_ref":{"type":"string","description":"Optional milestone ref; defaults to the in_progress milestone"}},"additionalProperties":false}`), true
}

func (milestoneListTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	return map[string]string{}, nil
}

func (milestoneWriteTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	raw := strings.TrimSpace(tools.FirstArg(args, "milestones", "plan"))
	if raw == "" {
		return nil, errors.New("milestones is empty")
	}
	if _, err := tools.ParseMilestones(raw); err != nil {
		return nil, err
	}
	out := map[string]string{"milestones": raw}
	if summary := strings.TrimSpace(tools.FirstArg(args, "summary", "explanation")); summary != "" {
		out["summary"] = summary
	}
	return out, nil
}

func (todoListTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	out := map[string]string{}
	if ref := strings.TrimSpace(tools.FirstArg(args, "milestone_ref", "ref")); ref != "" {
		out["milestone_ref"] = ref
	}
	return out, nil
}

func (todoAddItemsTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	ref := strings.TrimSpace(tools.FirstArg(args, "milestone_ref", "ref"))
	if ref == "" {
		return nil, errors.New("milestone_ref is empty")
	}
	raw := strings.TrimSpace(tools.FirstArg(args, "items"))
	if raw == "" {
		return nil, errors.New("items is empty")
	}
	if _, err := tools.ParseTodoAddItems(raw); err != nil {
		return nil, err
	}
	return map[string]string{"milestone_ref": ref, "items": raw}, nil
}

func (todoUpdateItemTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
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

func (todoFetchNextTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	return todoListTool{}.NormalizeArgs(args)
}

func (milestoneListTool) LegacyArgs(raw string) map[string]string { return map[string]string{} }
func (milestoneWriteTool) LegacyArgs(raw string) map[string]string {
	return map[string]string{"milestones": raw}
}
func (todoListTool) LegacyArgs(raw string) map[string]string {
	return map[string]string{"milestone_ref": raw}
}
func (todoAddItemsTool) LegacyArgs(raw string) map[string]string {
	return map[string]string{"items": raw}
}
func (todoUpdateItemTool) LegacyArgs(raw string) map[string]string {
	return map[string]string{"id": raw}
}
func (todoFetchNextTool) LegacyArgs(raw string) map[string]string {
	return map[string]string{"milestone_ref": raw}
}

func (milestoneListTool) Preview(req tools.Request) string  { return "Read milestones" }
func (milestoneWriteTool) Preview(req tools.Request) string { return "Replace milestones" }
func (todoListTool) Preview(req tools.Request) string {
	return milestonePreview(req.Args["milestone_ref"], "List todos")
}
func (todoAddItemsTool) Preview(req tools.Request) string {
	return milestonePreview(req.Args["milestone_ref"], "Add todo items")
}
func (todoUpdateItemTool) Preview(req tools.Request) string { return "Update todo #" + req.Args["id"] }
func (todoFetchNextTool) Preview(req tools.Request) string {
	return milestonePreview(req.Args["milestone_ref"], "Fetch next todo")
}

func (milestoneListTool) PresentationForPreview(preview string) tools.Presentation {
	return tools.Presentation{Title: "List milestones", Preview: preview}
}

func (milestoneWriteTool) PresentationForPreview(preview string) tools.Presentation {
	return tools.Presentation{Title: "Updated milestones", Preview: preview}
}

func (todoListTool) PresentationForPreview(preview string) tools.Presentation {
	return tools.Presentation{Title: "Listed todos", Preview: preview}
}

func (todoAddItemsTool) PresentationForPreview(preview string) tools.Presentation {
	return tools.Presentation{Title: "Added todo items", Preview: preview}
}

func (todoUpdateItemTool) PresentationForPreview(preview string) tools.Presentation {
	return tools.Presentation{Title: "Updated todo item", Preview: preview}
}

func (todoFetchNextTool) PresentationForPreview(preview string) tools.Presentation {
	return tools.Presentation{Title: "Fetched next todo", Preview: preview}
}

func (milestoneListTool) Presentation(req tools.Request) tools.Presentation {
	return milestoneListTool{}.PresentationForPreview(milestoneListTool{}.Preview(req))
}
func (milestoneWriteTool) Presentation(req tools.Request) tools.Presentation {
	return milestoneWriteTool{}.PresentationForPreview(milestoneWriteTool{}.Preview(req))
}
func (todoListTool) Presentation(req tools.Request) tools.Presentation {
	return todoListTool{}.PresentationForPreview(todoListTool{}.Preview(req))
}
func (todoAddItemsTool) Presentation(req tools.Request) tools.Presentation {
	return todoAddItemsTool{}.PresentationForPreview(todoAddItemsTool{}.Preview(req))
}
func (todoUpdateItemTool) Presentation(req tools.Request) tools.Presentation {
	return todoUpdateItemTool{}.PresentationForPreview(todoUpdateItemTool{}.Preview(req))
}
func (todoFetchNextTool) Presentation(req tools.Request) tools.Presentation {
	return todoFetchNextTool{}.PresentationForPreview(todoFetchNextTool{}.Preview(req))
}

func (milestoneListTool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	st, err := requireSessionStore(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	plan, err := st.GetMilestonePlan(ctx, runtime.SessionID)
	if err != nil {
		return tools.Result{}, err
	}
	return milestonePlanResult(plan), nil
}

func (milestoneWriteTool) Execute(_ context.Context, _ tools.Runtime, req tools.Request) (tools.Result, error) {
	milestones, err := tools.ParseMilestones(req.Args["milestones"])
	if err != nil {
		return tools.Result{}, err
	}
	return milestonePlanResult(store.MilestonePlan{
		Summary:    strings.TrimSpace(req.Args["summary"]),
		Milestones: milestones,
	}), nil
}

func (todoListTool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	st, err := requireSessionStore(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	plan, todos, ref, err := persistedTodoBucket(ctx, st, runtime.SessionID, req.Args["milestone_ref"])
	if err != nil {
		return tools.Result{}, err
	}
	return todoBucketResult(plan, ref, todos, ""), nil
}

func (todoAddItemsTool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	items, err := tools.ParseTodoAddItems(req.Args["items"])
	if err != nil {
		return tools.Result{}, err
	}
	st, err := requireSessionStore(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	plan, err := st.GetMilestonePlan(ctx, runtime.SessionID)
	if err != nil {
		return tools.Result{}, err
	}
	title := tools.MilestoneTitle(plan, req.Args["milestone_ref"])
	todos := make([]store.TodoItem, 0, len(items))
	for _, content := range items {
		todos = append(todos, store.TodoItem{
			Content: content,
			Status:  domain.TodoStatusPending,
		})
	}
	return todoBucketResultWithTitle(req.Args["milestone_ref"], title, todos, ""), nil
}

func (todoUpdateItemTool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	st, err := requireSessionStore(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	id, _ := tools.ParseTodoID(req.Args["id"])
	plan, err := st.GetMilestonePlan(ctx, runtime.SessionID)
	if err != nil {
		return tools.Result{}, err
	}
	allMilestones := plan.Milestones
	for _, milestone := range allMilestones {
		todos, err := st.ListTodos(ctx, runtime.SessionID, milestone.Ref)
		if err != nil {
			return tools.Result{}, err
		}
		matched := false
		for _, item := range todos {
			if item.ID != id {
				continue
			}
			matched = true
			for idx := range todos {
				if todos[idx].ID == id {
					todos[idx].Status = domain.TodoStatus(req.Args["status"])
					if content := strings.TrimSpace(req.Args["content"]); content != "" {
						todos[idx].Content = content
					}
				}
			}
			if err := tools.ValidateTodoProgress(todos); err != nil {
				return tools.Result{}, err
			}
			updated, err := st.UpdateTodoItem(ctx, id, domain.TodoStatus(req.Args["status"]), req.Args["content"])
			if err != nil {
				return tools.Result{}, err
			}
			todos, err = st.ListTodos(ctx, runtime.SessionID, milestone.Ref)
			if err != nil {
				return tools.Result{}, err
			}
			_ = updated
			return todoBucketResultWithTitle(milestone.Ref, milestone.Title, todos, ""), nil
		}
		if matched {
			break
		}
	}
	return tools.Result{}, fmt.Errorf("todo item %d not found", id)
}

func (todoFetchNextTool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	st, err := requireSessionStore(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	plan, todos, ref, err := persistedTodoBucket(ctx, st, runtime.SessionID, req.Args["milestone_ref"])
	if err != nil {
		return tools.Result{}, err
	}
	for _, item := range todos {
		if item.Status == domain.TodoStatusInProgress {
			return todoBucketResult(plan, ref, []store.TodoItem{item}, ""), nil
		}
	}
	for _, item := range todos {
		if item.Status == domain.TodoStatusPending {
			return todoBucketResult(plan, ref, []store.TodoItem{item}, ""), nil
		}
	}
	message := "All todo items for this milestone are done. If you have more planned tasks, move to the next milestone or break it down into todo items and start working on them."
	return todoBucketResult(plan, ref, todos, message), nil
}

func (milestoneListTool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "Listed milestones", result.Output
}
func (milestoneWriteTool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "Updated milestones", result.Output
}
func (todoListTool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "Listed todos", result.Output
}
func (todoAddItemsTool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "Added todo items", result.Output
}
func (todoUpdateItemTool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "Updated todo item", result.Output
}
func (todoFetchNextTool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "Fetched next todo", result.Output
}

func (milestoneListTool) PersistResult(ctx context.Context, st *store.Store, sessionID int64, req tools.Request, result tools.Result) (<-chan domain.Event, error) {
	plan, err := st.GetMilestonePlan(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	result.Stored = milestoneStoredResult(plan)
	return tools.PersistStandardResult(ctx, st, sessionID, req, result)
}

func (milestoneWriteTool) PersistResult(ctx context.Context, st *store.Store, sessionID int64, req tools.Request, result tools.Result) (<-chan domain.Event, error) {
	milestones, err := tools.ParseMilestones(req.Args["milestones"])
	if err != nil {
		return nil, err
	}
	plan, err := st.SetMilestonePlan(ctx, sessionID, req.Args["summary"], milestones)
	if err != nil {
		return nil, err
	}
	result.Stored = milestoneStoredResult(plan)
	return tools.PersistStandardResult(ctx, st, sessionID, req, result)
}

func (todoListTool) PersistResult(ctx context.Context, st *store.Store, sessionID int64, req tools.Request, result tools.Result) (<-chan domain.Event, error) {
	plan, todos, ref, err := persistedTodoBucket(ctx, st, sessionID, req.Args["milestone_ref"])
	if err != nil {
		return nil, err
	}
	result.Stored = todoStoredResult(plan, ref, todos, "")
	return tools.PersistStandardResult(ctx, st, sessionID, req, result)
}

func (todoAddItemsTool) PersistResult(ctx context.Context, st *store.Store, sessionID int64, req tools.Request, result tools.Result) (<-chan domain.Event, error) {
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
	result.Stored = todoStoredResult(plan, req.Args["milestone_ref"], created, "")
	return tools.PersistStandardResult(ctx, st, sessionID, req, result)
}

func (todoUpdateItemTool) PersistResult(ctx context.Context, st *store.Store, sessionID int64, req tools.Request, result tools.Result) (<-chan domain.Event, error) {
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
		found := false
		for _, item := range todos {
			if item.ID != id {
				continue
			}
			found = true
			for idx := range todos {
				if todos[idx].ID == id {
					todos[idx].Status = domain.TodoStatus(req.Args["status"])
					if content := strings.TrimSpace(req.Args["content"]); content != "" {
						todos[idx].Content = content
					}
				}
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
			result.Stored = todoStoredResult(plan, milestone.Ref, todos, "")
			return tools.PersistStandardResult(ctx, st, sessionID, req, result)
		}
		if found {
			break
		}
	}
	return nil, fmt.Errorf("todo item %d not found", id)
}

func (todoFetchNextTool) PersistResult(ctx context.Context, st *store.Store, sessionID int64, req tools.Request, result tools.Result) (<-chan domain.Event, error) {
	plan, todos, ref, err := persistedTodoBucket(ctx, st, sessionID, req.Args["milestone_ref"])
	if err != nil {
		return nil, err
	}
	message := ""
	display := todos
	for _, item := range todos {
		if item.Status == domain.TodoStatusInProgress {
			display = []store.TodoItem{item}
			result.Stored = todoStoredResult(plan, ref, display, message)
			return tools.PersistStandardResult(ctx, st, sessionID, req, result)
		}
	}
	for _, item := range todos {
		if item.Status == domain.TodoStatusPending {
			display = []store.TodoItem{item}
			result.Stored = todoStoredResult(plan, ref, display, message)
			return tools.PersistStandardResult(ctx, st, sessionID, req, result)
		}
	}
	message = "All todo items for this milestone are done. If you have more planned tasks, move to the next milestone or break it down into todo items and start working on them."
	result.Stored = todoStoredResult(plan, ref, todos, message)
	return tools.PersistStandardResult(ctx, st, sessionID, req, result)
}

func milestonePreview(ref string, fallback string) string {
	if strings.TrimSpace(ref) == "" {
		return fallback
	}
	return ref
}

func requireSessionStore(runtime tools.Runtime) (*store.Store, error) {
	if runtime.Store == nil || runtime.SessionID == 0 {
		return nil, errors.New("planning tools require a persisted session")
	}
	return runtime.Store, nil
}

func persistedTodoBucket(ctx context.Context, st *store.Store, sessionID int64, ref string) (store.MilestonePlan, []store.TodoItem, string, error) {
	plan, err := st.GetMilestonePlan(ctx, sessionID)
	if err != nil {
		return store.MilestonePlan{}, nil, "", err
	}
	if ref == "" {
		active, ok := tools.ActiveMilestone(plan)
		if !ok {
			return store.MilestonePlan{}, nil, "", errors.New("no active milestone; read milestones first or provide milestone_ref")
		}
		ref = active.Ref
	}
	todos, err := st.ListTodos(ctx, sessionID, ref)
	if err != nil {
		return store.MilestonePlan{}, nil, "", err
	}
	return plan, todos, ref, nil
}

func milestonePlanResult(plan store.MilestonePlan) tools.Result {
	stored := milestoneStoredResult(plan)
	output := formatMilestoneOutput(stored)
	if strings.TrimSpace(output) == "" {
		output = "No milestones defined."
	}
	return tools.Result{
		Output: output,
		Meta:   map[string]string{"milestone_count": fmt.Sprintf("%d", len(stored.Milestones))},
		Stored: stored,
	}
}

func todoBucketResult(plan store.MilestonePlan, ref string, todos []store.TodoItem, message string) tools.Result {
	return todoBucketResultWithTitle(ref, tools.MilestoneTitle(plan, ref), todos, message)
}

func todoBucketResultWithTitle(ref, title string, todos []store.TodoItem, message string) tools.Result {
	stored := todoStoredResult(store.MilestonePlan{Milestones: []store.Milestone{{Ref: ref, Title: title}}}, ref, todos, message)
	output := formatTodoOutput(stored)
	if strings.TrimSpace(output) == "" {
		output = "No todo items found."
	}
	return tools.Result{
		Output: output,
		Meta: map[string]string{
			"milestone_ref": ref,
			"todo_count":    fmt.Sprintf("%d", len(stored.Items)),
		},
		Stored: stored,
	}
}

func milestoneStoredResult(plan store.MilestonePlan) tools.MilestonePlanStoredResult {
	items := make([]tools.MilestoneStoredItem, 0, len(plan.Milestones))
	for _, item := range plan.Milestones {
		items = append(items, tools.MilestoneStoredItem{
			Ref:    item.Ref,
			Title:  item.Title,
			Status: string(item.Status),
			Notes:  item.Notes,
		})
	}
	return tools.MilestonePlanStoredResult{
		Summary:    plan.Summary,
		Milestones: items,
	}
}

func todoStoredResult(plan store.MilestonePlan, ref string, todos []store.TodoItem, message string) tools.TodoListStoredResult {
	items := make([]tools.TodoStoredItem, 0, len(todos))
	for _, item := range todos {
		items = append(items, tools.TodoStoredItem{
			ID:      item.ID,
			Content: item.Content,
			Status:  string(item.Status),
		})
	}
	return tools.TodoListStoredResult{
		MilestoneRef:   ref,
		MilestoneTitle: tools.MilestoneTitle(plan, ref),
		Message:        message,
		Items:          items,
	}
}

func formatMilestoneOutput(result tools.MilestonePlanStoredResult) string {
	text, _ := tools.DisplayTextForPart(domain.Part{
		Kind:     domain.PartKindToolOutput,
		MetaJSON: tools.JSONMeta(tools.MetaWithStoredResult(nil, domain.PartKindToolOutput, domain.ToolKindMilestoneList, tools.StoredResultStatusOK, result)),
	})
	return text
}

func formatTodoOutput(result tools.TodoListStoredResult) string {
	text, _ := tools.DisplayTextForPart(domain.Part{
		Kind:     domain.PartKindToolOutput,
		MetaJSON: tools.JSONMeta(tools.MetaWithStoredResult(nil, domain.PartKindToolOutput, domain.ToolKindTodoList, tools.StoredResultStatusOK, result)),
	})
	return text
}
