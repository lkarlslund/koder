package milestonetool

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/planning"
	"github.com/lkarlslund/koder/internal/tools"
)

func init() {
	tools.Register(listTool{}, tools.ToolSpec{
		Title:       "List milestones",
		Description: "Read the current session milestone plan.",
		Usage:       "Read the current session milestone plan. Use this to understand the long-horizon plan before choosing or breaking down work. If a milestone was created by accident or is no longer wanted, update it to cancelled at any time.",
		Parameters:  `{"type":"object","properties":{},"additionalProperties":false}`,
		ExposeToLLM: true,
	})
	tools.Register(addItemsTool{}, tools.ToolSpec{
		Title:       "Add milestones",
		Description: "Append new pending milestones to the current plan.",
		Usage:       "Append new pending milestones to the current session plan. Use milestones for larger chunks of work. Each milestone requires a stable ref so its todo bucket can be tracked separately. If a milestone is later found to be accidental or unwanted, cancel it with milestone_update_item.",
		Parameters:  `{"type":"object","properties":{"items":{"type":"array","description":"New milestones to append as pending","items":{"type":"object","properties":{"ref":{"type":"string"},"title":{"type":"string"},"notes":{"type":"string"}},"required":["ref","title"]}}},"required":["items"],"additionalProperties":false}`,
		ExposeToLLM: true,
	})
	tools.Register(updateItemTool{}, tools.ToolSpec{
		Title:       "Update milestone",
		Description: "Update one milestone's status or details.",
		Usage:       "Update one milestone's status, and optionally its title or notes. Use ready when decomposition is done and execution can start. Set completed, blocked, or cancelled when work is finished, blocked, created by accident, or no longer wanted.",
		Parameters:  `{"type":"object","properties":{"ref":{"type":"string","description":"Milestone ref"},"status":{"type":"string","enum":["pending","decomposing","ready","executing","completed","blocked","cancelled"]},"title":{"type":"string","description":"Optional replacement title"},"notes":{"type":"string","description":"Optional replacement notes"}},"required":["ref","status"],"additionalProperties":false}`,
		ExposeToLLM: true,
	})
	tools.Register(planTool{}, tools.ToolSpec{
		Title:       "Plan milestone",
		Description: "Create or update a milestone and append todo items.",
		Usage:       "Create or update one milestone and append concrete todo items for it in one step. Use ready when this creates an executable todo bucket. If a milestone was created by accident or is no longer wanted, update it to cancelled at any time.",
		Parameters:  `{"type":"object","properties":{"ref":{"type":"string","description":"Milestone ref"},"title":{"type":"string","description":"Milestone title"},"notes":{"type":"string","description":"Optional milestone notes"},"status":{"type":"string","enum":["pending","decomposing","ready","executing","completed","blocked","cancelled"]},"items":{"type":"array","description":"Todo items to append for this milestone","items":{"type":"object","properties":{"content":{"type":"string"}},"required":["content"]}}},"required":["ref","title","items"],"additionalProperties":false}`,
		ExposeToLLM: true,
	})
	tools.Register(writeTool{}, tools.ToolSpec{
		Title:       "Updated milestones",
		Description: "Replace the current milestone plan.",
	})
}

type listTool struct{}
type addItemsTool struct{}
type updateItemTool struct{}
type planTool struct{}
type writeTool struct{}

func (listTool) Kind() domain.ToolKind       { return domain.ToolKindMilestoneList }
func (addItemsTool) Kind() domain.ToolKind   { return domain.ToolKindMilestoneAdd }
func (updateItemTool) Kind() domain.ToolKind { return domain.ToolKindMilestoneUpdate }
func (planTool) Kind() domain.ToolKind       { return domain.ToolKindMilestonePlan }
func (writeTool) Kind() domain.ToolKind      { return domain.ToolKindMilestoneWrite }

func (listTool) BypassesPermission() bool       { return true }
func (addItemsTool) BypassesPermission() bool   { return true }
func (updateItemTool) BypassesPermission() bool { return true }
func (planTool) BypassesPermission() bool       { return true }
func (writeTool) BypassesPermission() bool      { return true }

func (addItemsTool) Definition(runtime tools.Runtime, spec tools.ToolSpec) (tools.ToolSpec, bool) {
	if tools.AssignedMilestoneRef(runtime) != "" {
		return tools.ToolSpec{}, false
	}
	if runtime.ChatRole == chatrole.Execution {
		return tools.ToolSpec{}, false
	}
	return spec, true
}

func (planTool) Definition(runtime tools.Runtime, spec tools.ToolSpec) (tools.ToolSpec, bool) {
	return addItemsTool{}.Definition(runtime, spec)
}

func (listTool) NormalizeArgs(map[string]string) (map[string]string, error) {
	return map[string]string{}, nil
}

func (addItemsTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	raw := strings.TrimSpace(tools.FirstArg(args, "items"))
	if raw == "" {
		return nil, errors.New("items is empty")
	}
	if _, err := planning.ParseMilestoneAddItems(raw); err != nil {
		return nil, err
	}
	return map[string]string{"items": raw}, nil
}

func (updateItemTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	ref, err := planning.ParseMilestoneRef(tools.FirstArg(args, "ref"))
	if err != nil {
		return nil, err
	}
	status, err := planning.ParseMilestoneStatus(tools.FirstArg(args, "status"))
	if err != nil {
		return nil, err
	}
	out := map[string]string{
		"ref":    ref,
		"status": status.String(),
	}
	if title := strings.TrimSpace(tools.FirstArg(args, "title")); title != "" {
		out["title"] = title
	}
	if notes, ok := args["notes"]; ok {
		out["notes"] = strings.TrimSpace(notes)
	}
	return out, nil
}

func (planTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	ref, err := planning.ParseMilestoneRef(tools.FirstArg(args, "ref"))
	if err != nil {
		return nil, err
	}
	title := strings.TrimSpace(tools.FirstArg(args, "title"))
	if title == "" {
		return nil, errors.New("title is empty")
	}
	rawItems := strings.TrimSpace(tools.FirstArg(args, "items"))
	if rawItems == "" {
		return nil, errors.New("items is empty")
	}
	if _, err := planning.ParseTodoAddItems(rawItems); err != nil {
		return nil, err
	}
	out := map[string]string{
		"ref":   ref,
		"title": title,
		"items": rawItems,
	}
	if notes, ok := args["notes"]; ok {
		out["notes"] = strings.TrimSpace(notes)
	}
	if status := strings.TrimSpace(tools.FirstArg(args, "status")); status != "" {
		parsed, err := planning.ParseMilestoneStatus(status)
		if err != nil {
			return nil, err
		}
		out["status"] = parsed.String()
	}
	return out, nil
}

func (writeTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	raw := strings.TrimSpace(tools.FirstArg(args, "milestones", "plan"))
	if raw == "" {
		return nil, errors.New("milestones is empty")
	}
	if _, err := planning.ParseMilestones(raw); err != nil {
		return nil, err
	}
	out := map[string]string{"milestones": raw}
	if summary := strings.TrimSpace(tools.FirstArg(args, "summary", "explanation")); summary != "" {
		out["summary"] = summary
	}
	return out, nil
}

func (listTool) Preview(req tools.Request) string       { return "Read milestones" }
func (addItemsTool) Preview(req tools.Request) string   { return "Add milestones" }
func (updateItemTool) Preview(req tools.Request) string { return "Update milestone " + req.Args["ref"] }
func (planTool) Preview(req tools.Request) string {
	return "Plan and decompose milestone " + req.Args["ref"]
}
func (writeTool) Preview(req tools.Request) string { return "Replace milestones" }

func (listTool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	control, err := tools.RequireSessionControl(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	plan, err := control.GetMilestonePlan(ctx, runtime.SessionID)
	if err != nil {
		return tools.Result{}, err
	}
	return tools.MilestonePlanResult(tools.ScopedMilestonePlan(runtime, plan)), nil
}

func (addItemsTool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	if tools.AssignedMilestoneRef(runtime) != "" {
		return tools.Result{}, fmt.Errorf("chat is scoped to milestone %q", tools.AssignedMilestoneRef(runtime))
	}
	items, err := planning.ParseMilestoneAddItems(req.Args["items"])
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
	if err := ensureMilestoneRefsAvailable(plan.Milestones, items); err != nil {
		return tools.Result{}, err
	}
	return tools.MilestonePlanResult(planning.Plan{
		Summary:    plan.Summary,
		Milestones: appendMilestones(plan.Milestones, items),
	}), nil
}

func (updateItemTool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	control, err := tools.RequireSessionControl(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	ref, err := tools.AllowedMilestoneRef(runtime, req.Args["ref"])
	if err != nil {
		return tools.Result{}, err
	}
	req.Args["ref"] = ref
	plan, err := control.GetMilestonePlan(ctx, runtime.SessionID)
	if err != nil {
		return tools.Result{}, err
	}
	updated, err := updatedMilestonePlan(plan, req, actorChatFromRuntime(runtime))
	if err != nil {
		return tools.Result{}, err
	}
	if err := validateCompletedMilestoneTodos(ctx, control, runtime.SessionID, updated.Milestones); err != nil {
		return tools.Result{}, err
	}
	return tools.MilestonePlanResult(tools.ScopedMilestonePlan(runtime, updated)), nil
}

func (planTool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	control, err := tools.RequireSessionControl(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	ref, err := tools.AllowedMilestoneRef(runtime, req.Args["ref"])
	if err != nil {
		return tools.Result{}, err
	}
	plan, err := control.GetMilestonePlan(ctx, runtime.SessionID)
	if err != nil {
		return tools.Result{}, err
	}
	status, err := domain.MilestoneStatusString(strings.TrimSpace(req.Args["status"]))
	if err != nil {
		status = domain.MilestoneStatusReady
	}
	nextMilestones := upsertMilestone(plan.Milestones, planning.Milestone{
		Ref:    ref,
		Title:  strings.TrimSpace(req.Args["title"]),
		Status: status,
		Notes:  strings.TrimSpace(req.Args["notes"]),
	})
	if err := planning.ValidateMilestoneProgress(nextMilestones); err != nil {
		return tools.Result{}, err
	}
	if err := validateCompletedMilestoneTodos(ctx, control, runtime.SessionID, nextMilestones); err != nil {
		return tools.Result{}, err
	}
	items, err := planning.ParseTodoAddItems(req.Args["items"])
	if err != nil {
		return tools.Result{}, err
	}
	todos := make([]planning.TodoItem, 0, len(items))
	for _, item := range items {
		todos = append(todos, planning.TodoItem{Content: item, Status: domain.TodoStatusPending})
	}
	return tools.TodoBucketResultWithTitle(ref, strings.TrimSpace(req.Args["title"]), todos, "Updated milestone and appended todo items"), nil
}

func (writeTool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	milestones, err := planning.ParseMilestones(req.Args["milestones"])
	if err != nil {
		return tools.Result{}, err
	}
	control, err := tools.RequireSessionControl(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	if err := validateCompletedMilestoneTodos(ctx, control, runtime.SessionID, milestones); err != nil {
		return tools.Result{}, err
	}
	return tools.MilestonePlanResult(planning.Plan{
		Summary:    strings.TrimSpace(req.Args["summary"]),
		Milestones: milestones,
	}), nil
}

func (listTool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "Listed milestones", result.Output
}

func (addItemsTool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "Added milestones", result.Output
}

func (updateItemTool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "Updated milestone", result.Output
}

func (planTool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "Planned and decomposed milestone", result.Output
}

func (writeTool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "Updated milestones", result.Output
}

func (listTool) PersistResult(ctx context.Context, runtime tools.Runtime, req tools.Request, result tools.Result) (<-chan domain.Event, error) {
	control, err := tools.RequireSessionControl(runtime)
	if err != nil {
		return nil, err
	}
	plan, err := control.GetMilestonePlan(ctx, runtime.SessionID)
	if err != nil {
		return nil, err
	}
	if result.Stored == nil {
		result.Stored = tools.MilestoneStoredResult(plan)
	}
	return tools.PersistStandardResult(ctx, runtime, req, result)
}

func (addItemsTool) PersistResult(ctx context.Context, runtime tools.Runtime, req tools.Request, result tools.Result) (<-chan domain.Event, error) {
	control, err := tools.RequireSessionControl(runtime)
	if err != nil {
		return nil, err
	}
	items, err := planning.ParseMilestoneAddItems(req.Args["items"])
	if err != nil {
		return nil, err
	}
	plan, err := control.GetMilestonePlan(ctx, runtime.SessionID)
	if err != nil {
		return nil, err
	}
	if err := ensureMilestoneRefsAvailable(plan.Milestones, items); err != nil {
		return nil, err
	}
	plan, err = control.SetMilestonePlan(ctx, runtime.SessionID, plan.Summary, appendMilestones(plan.Milestones, items))
	if err != nil {
		return nil, err
	}
	result.Stored = tools.MilestoneStoredResult(plan)
	return tools.PersistStandardResult(ctx, runtime, req, result)
}

func (updateItemTool) PersistResult(ctx context.Context, runtime tools.Runtime, req tools.Request, result tools.Result) (<-chan domain.Event, error) {
	control, err := tools.RequireSessionControl(runtime)
	if err != nil {
		return nil, err
	}
	plan, err := control.GetMilestonePlan(ctx, runtime.SessionID)
	if err != nil {
		return nil, err
	}
	updated, err := updatedMilestonePlan(plan, req, actorChatFromRuntime(runtime))
	if err != nil {
		return nil, err
	}
	if err := validateCompletedMilestoneTodos(ctx, control, runtime.SessionID, updated.Milestones); err != nil {
		return nil, err
	}
	plan, err = control.SetMilestonePlan(ctx, runtime.SessionID, updated.Summary, updated.Milestones)
	if err != nil {
		return nil, err
	}
	stored := tools.MilestoneStoredResult(tools.MilestonePlanForRef(plan, req.Args["ref"]))
	result.Stored = stored
	result.Output = tools.FormatMilestoneOutput(stored)
	return tools.PersistStandardResult(ctx, runtime, req, result)
}

func (planTool) PersistResult(ctx context.Context, runtime tools.Runtime, req tools.Request, result tools.Result) (<-chan domain.Event, error) {
	control, err := tools.RequireSessionControl(runtime)
	if err != nil {
		return nil, err
	}
	plan, err := control.GetMilestonePlan(ctx, runtime.SessionID)
	if err != nil {
		return nil, err
	}
	status, err := domain.MilestoneStatusString(strings.TrimSpace(req.Args["status"]))
	if err != nil {
		status = domain.MilestoneStatusReady
	}
	nextMilestones := upsertMilestone(plan.Milestones, planning.Milestone{
		Ref:    req.Args["ref"],
		Title:  strings.TrimSpace(req.Args["title"]),
		Status: status,
		Notes:  strings.TrimSpace(req.Args["notes"]),
	})
	if err := planning.ValidateMilestoneProgress(nextMilestones); err != nil {
		return nil, err
	}
	if err := validateCompletedMilestoneTodos(ctx, control, runtime.SessionID, nextMilestones); err != nil {
		return nil, err
	}
	if _, err := control.SetMilestonePlan(ctx, runtime.SessionID, plan.Summary, nextMilestones); err != nil {
		return nil, err
	}
	items, err := planning.ParseTodoAddItems(req.Args["items"])
	if err != nil {
		return nil, err
	}
	if _, err := control.AddTodoItems(ctx, runtime.SessionID, req.Args["ref"], items); err != nil {
		return nil, err
	}
	todos, err := control.ListTodos(ctx, runtime.SessionID, req.Args["ref"])
	if err != nil {
		return nil, err
	}
	stored := tools.TodoStoredResult(planning.Plan{Summary: plan.Summary, Milestones: nextMilestones}, req.Args["ref"], todos, "Updated milestone and appended todo items")
	result.Stored = stored
	result.Output = tools.FormatTodoOutput(stored)
	return tools.PersistStandardResult(ctx, runtime, req, result)
}

func (writeTool) PersistResult(ctx context.Context, runtime tools.Runtime, req tools.Request, result tools.Result) (<-chan domain.Event, error) {
	control, err := tools.RequireSessionControl(runtime)
	if err != nil {
		return nil, err
	}
	milestones, err := planning.ParseMilestones(req.Args["milestones"])
	if err != nil {
		return nil, err
	}
	if err := validateCompletedMilestoneTodos(ctx, control, runtime.SessionID, milestones); err != nil {
		return nil, err
	}
	plan, err := control.SetMilestonePlan(ctx, runtime.SessionID, req.Args["summary"], milestones)
	if err != nil {
		return nil, err
	}
	result.Stored = tools.MilestoneStoredResult(plan)
	return tools.PersistStandardResult(ctx, runtime, req, result)
}

func appendMilestones(existing, added []planning.Milestone) []planning.Milestone {
	out := make([]planning.Milestone, 0, len(existing)+len(added))
	for _, item := range existing {
		item.Position = len(out)
		out = append(out, item)
	}
	for _, item := range added {
		item.Position = len(out)
		out = append(out, item)
	}
	return out
}

func ensureMilestoneRefsAvailable(existing, added []planning.Milestone) error {
	seenRefs := make(map[string]struct{}, len(existing)+len(added))
	for _, item := range existing {
		seenRefs[item.Ref] = struct{}{}
	}
	for _, item := range added {
		if _, exists := seenRefs[item.Ref]; exists {
			return fmt.Errorf("duplicate milestone ref %q", item.Ref)
		}
		seenRefs[item.Ref] = struct{}{}
	}
	return nil
}

func upsertMilestone(existing []planning.Milestone, next planning.Milestone) []planning.Milestone {
	out := append([]planning.Milestone(nil), existing...)
	for idx := range out {
		if out[idx].Ref != next.Ref {
			continue
		}
		next.Position = out[idx].Position
		out[idx] = next
		return out
	}
	next.Position = len(out)
	return append(out, next)
}

func actorChatFromRuntime(runtime tools.Runtime) domain.Chat {
	return domain.Chat{
		ID:                    runtime.ChatID,
		WorkflowRole:          runtime.ChatRole,
		ActiveMilestoneRef:    runtime.ActiveMilestoneRef,
		AssignedTodoBucketRef: runtime.AssignedTodoBucketRef,
	}
}

func updatedMilestonePlan(plan planning.Plan, req tools.Request, actor domain.Chat) (planning.Plan, error) {
	ref := req.Args["ref"]
	status, err := domain.MilestoneStatusString(req.Args["status"])
	if err != nil {
		return plan, fmt.Errorf("invalid milestone status %q", req.Args["status"])
	}
	milestones := append([]planning.Milestone(nil), plan.Milestones...)
	found := false
	for idx := range milestones {
		if milestones[idx].Ref != ref {
			continue
		}
		found = true
		if err := validateMilestoneOwner(milestones[idx], status, actor); err != nil {
			return planning.Plan{}, err
		}
		milestones[idx].Status = status
		applyMilestoneOwner(&milestones[idx], status, actor)
		if title := strings.TrimSpace(req.Args["title"]); title != "" {
			milestones[idx].Title = title
		}
		if notes, ok := req.Args["notes"]; ok {
			milestones[idx].Notes = strings.TrimSpace(notes)
		}
		break
	}
	if !found {
		return planning.Plan{}, fmt.Errorf("milestone %q not found", ref)
	}
	if err := planning.ValidateMilestoneProgress(milestones); err != nil {
		return planning.Plan{}, err
	}
	return planning.Plan{
		Summary:    plan.Summary,
		Milestones: milestones,
	}, nil
}

func validateMilestoneOwner(milestone planning.Milestone, next domain.MilestoneStatus, actor domain.Chat) error {
	if actor.ID == "" || actor.WorkflowRole == chatrole.Orchestrator {
		return nil
	}
	if milestone.OwnerChatID != nil && *milestone.OwnerChatID != actor.ID {
		return fmt.Errorf("milestone %q is owned by chat %s", milestone.Ref, *milestone.OwnerChatID)
	}
	switch next {
	case domain.MilestoneStatusExecuting:
		if actor.WorkflowRole != chatrole.Execution {
			return fmt.Errorf("milestone %q can only be set to executing by an execution chat", milestone.Ref)
		}
	}
	return nil
}

func applyMilestoneOwner(milestone *planning.Milestone, status domain.MilestoneStatus, actor domain.Chat) {
	switch status {
	case domain.MilestoneStatusExecuting:
		if actor.ID != "" && actor.WorkflowRole != chatrole.Orchestrator {
			owner := actor.ID
			milestone.OwnerChatID = &owner
		}
	default:
		milestone.OwnerChatID = nil
	}
}

func validateCompletedMilestoneTodos(ctx context.Context, control tools.SessionControl, sessionID domain.ID, milestones []planning.Milestone) error {
	for _, milestone := range milestones {
		if milestone.Status != domain.MilestoneStatusCompleted {
			continue
		}
		todos, err := control.ListTodos(ctx, sessionID, milestone.Ref)
		if err != nil {
			return err
		}
		for _, todo := range todos {
			if todo.Status == domain.TodoStatusCompleted {
				continue
			}
			name := strings.TrimSpace(milestone.Title)
			if name == "" {
				name = milestone.Ref
			}
			return fmt.Errorf("cannot complete milestone %q while todo %s is %s", name, todo.ID, todo.Status.String())
		}
	}
	return nil
}
