package milestonetool

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/planning"
	"github.com/lkarlslund/koder/internal/tools"
)

func init() {
	tools.Register(listTool{}, tools.ToolSpec{
		Title:       "List milestones",
		Description: "Read the current session milestone plan.",
		Usage:       "Read the current session milestone plan. Completed milestones are hidden by default; pass completed=true when you need to inspect finished work. Use this to understand the long-horizon plan before choosing or breaking down work. If a milestone was created by accident or is no longer wanted, update it to cancelled at any time.",
		Parameters:  `{"type":"object","properties":{"completed":{"type":"boolean","description":"Include completed milestones. Defaults to false."}},"additionalProperties":false}`,
		ExposeToLLM: true,
	})
	tools.Register(addItemsTool{}, tools.ToolSpec{
		Title:       "Add milestone",
		Description: "Create one blank pending milestone.",
		Usage:       "Create one blank pending milestone with no tasks. Use depends_on_ref to make it a child of another milestone. Use tasks_add afterwards to add concrete tasks, then milestone_update status=ready when the milestone is ready for execution. Fails if the milestone ref or title already exists, if depends_on_ref is unknown, or if the dependency would create a cycle.",
		Parameters:  `{"type":"object","properties":{"ref":{"type":"string","description":"Stable milestone ref"},"title":{"type":"string","description":"Milestone title"},"notes":{"type":"string","description":"Optional milestone notes"},"depends_on_ref":{"type":"string","description":"Optional parent milestone ref for tree/dependency structure"}},"required":["ref","title"],"additionalProperties":false}`,
		ExposeToLLM: true,
	})
	tools.Register(updateItemTool{}, tools.ToolSpec{
		Title:       "Update milestone",
		Description: "Update one milestone's status or details.",
		Usage:       "Update one milestone's status, title, notes, or dependency parent. Use depends_on_ref to move it under another milestone; pass an empty depends_on_ref to make it a root milestone. Use ready when decomposition is done and execution can start. Set completed, blocked, or cancelled when work is finished, blocked, created by accident, or no longer wanted.",
		Parameters:  `{"type":"object","properties":{"ref":{"type":"string","description":"Milestone ref"},"status":{"type":"string","enum":["pending","decomposing","ready","executing","completed","blocked","cancelled"]},"title":{"type":"string","description":"Optional replacement title"},"notes":{"type":"string","description":"Optional replacement notes"},"depends_on_ref":{"type":"string","description":"Optional parent milestone ref. Pass an empty string to make this a root milestone."}},"required":["ref","status"],"additionalProperties":false}`,
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
type writeTool struct{}

func (listTool) ID() tools.ID       { return tools.MilestoneList }
func (addItemsTool) ID() tools.ID   { return tools.MilestoneAdd }
func (updateItemTool) ID() tools.ID { return tools.MilestoneUpdate }
func (writeTool) ID() tools.ID      { return tools.MilestoneWrite }

func (listTool) BypassesPermission() bool       { return true }
func (addItemsTool) BypassesPermission() bool   { return true }
func (updateItemTool) BypassesPermission() bool { return true }
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

func (listTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	raw := strings.TrimSpace(args["completed"])
	if raw == "" {
		return map[string]string{}, nil
	}
	completed, err := strconv.ParseBool(raw)
	if err != nil {
		return nil, fmt.Errorf("completed: %w", err)
	}
	return map[string]string{"completed": strconv.FormatBool(completed)}, nil
}

func (addItemsTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	ref, err := planning.ParseMilestoneRef(args["ref"])
	if err != nil {
		return nil, err
	}
	title := strings.TrimSpace(args["title"])
	if title == "" {
		return nil, errors.New("title is empty")
	}
	out := map[string]string{"ref": ref, "title": title}
	if notes, ok := args["notes"]; ok {
		out["notes"] = strings.TrimSpace(notes)
	}
	if dependsOnRef, ok := args["depends_on_ref"]; ok {
		out["depends_on_ref"] = strings.TrimSpace(dependsOnRef)
	}
	return out, nil
}

func (updateItemTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	ref, err := planning.ParseMilestoneRef(args["ref"])
	if err != nil {
		return nil, err
	}
	status, err := planning.ParseMilestoneStatus(args["status"])
	if err != nil {
		return nil, err
	}
	out := map[string]string{
		"ref":    ref,
		"status": status.String(),
	}
	if title := strings.TrimSpace(args["title"]); title != "" {
		out["title"] = title
	}
	if notes, ok := args["notes"]; ok {
		out["notes"] = strings.TrimSpace(notes)
	}
	if dependsOnRef, ok := args["depends_on_ref"]; ok {
		out["depends_on_ref"] = strings.TrimSpace(dependsOnRef)
	}
	return out, nil
}

func (writeTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	raw := strings.TrimSpace(args["milestones"])
	if raw == "" {
		return nil, errors.New("milestones is empty")
	}
	if _, err := planning.ParseMilestones(raw); err != nil {
		return nil, err
	}
	out := map[string]string{"milestones": raw}
	if summary := strings.TrimSpace(args["summary"]); summary != "" {
		out["summary"] = summary
	}
	return out, nil
}

func (listTool) Preview(req tools.Request) string       { return "Read milestones" }
func (addItemsTool) Preview(req tools.Request) string   { return "Add milestone " + req.Args["ref"] }
func (updateItemTool) Preview(req tools.Request) string { return "Update milestone " + req.Args["ref"] }
func (writeTool) Preview(req tools.Request) string      { return "Replace milestones" }

func (listTool) Call(ctx context.Context, opts tools.Options) (tools.Result, error) {
	runtime, req := opts.Runtime, opts.Request
	control, err := tools.RequireSessionControl(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	plan, err := control.GetMilestonePlan(ctx, runtime.SessionID)
	if err != nil {
		return tools.Result{}, err
	}
	scoped := tools.ScopedMilestonePlan(runtime, plan)
	listed := filterListedMilestones(scoped, req.Args["completed"] == "true")
	todoSummaries, err := milestoneTodoSummaries(ctx, control, runtime.SessionID, listed.Milestones)
	if err != nil {
		return tools.Result{}, err
	}
	result := tools.MilestonePlanResultWithTodoSummaries(listed, todoSummaries)
	if summary := milestoneSummary(scoped.Milestones); summary != "" {
		result.Output = summary + "\n" + result.Output
	}
	return result, nil
}

func filterListedMilestones(plan planning.Plan, includeCompleted bool) planning.Plan {
	if includeCompleted {
		return plan
	}
	filtered := plan
	filtered.Milestones = make([]planning.Milestone, 0, len(plan.Milestones))
	for _, milestone := range plan.Milestones {
		if milestone.Status != planning.MilestoneStatusCompleted {
			filtered.Milestones = append(filtered.Milestones, milestone)
		}
	}
	return filtered
}

func milestoneSummary(milestones []planning.Milestone) string {
	if len(milestones) == 0 {
		return "Milestones summary: none"
	}
	counts := make(map[planning.MilestoneStatus]int)
	for _, milestone := range milestones {
		counts[milestone.Status]++
	}
	parts := make([]string, 0, len(counts))
	for _, status := range planning.MilestoneStatusValues() {
		count := counts[status]
		if count == 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("%d %s", count, status.String()))
	}
	return "Milestones summary: " + strings.Join(parts, ", ")
}

func milestoneTodoSummaries(ctx context.Context, control tools.SessionControl, sessionID id.ID, milestones []planning.Milestone) (map[string]string, error) {
	summaries := make(map[string]string, len(milestones))
	for _, milestone := range milestones {
		todos, err := control.ListTodos(ctx, sessionID, milestone.Ref)
		if err != nil {
			return nil, err
		}
		summaries[milestone.Ref] = todoSummary(todos)
	}
	return summaries, nil
}

func todoSummary(todos []planning.TodoItem) string {
	if len(todos) == 0 {
		return "no tasks added to milestone"
	}
	counts := make(map[planning.TodoStatus]int)
	for _, todo := range todos {
		counts[todo.Status]++
	}
	parts := make([]string, 0, len(counts))
	for _, status := range planning.TodoStatusValues() {
		count := counts[status]
		if count == 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("%d %s", count, status.String()))
	}
	return "tasks: " + strings.Join(parts, ", ")
}

func (addItemsTool) Call(ctx context.Context, opts tools.Options) (tools.Result, error) {
	runtime, req := opts.Runtime, opts.Request
	if tools.AssignedMilestoneRef(runtime) != "" {
		return tools.Result{}, fmt.Errorf("chat is scoped to milestone %q", tools.AssignedMilestoneRef(runtime))
	}
	control, err := tools.RequireSessionControl(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	plan, err := control.GetMilestonePlan(ctx, runtime.SessionID)
	if err != nil {
		return tools.Result{}, err
	}
	item := planning.Milestone{
		Ref:          req.Args["ref"],
		Title:        strings.TrimSpace(req.Args["title"]),
		Status:       planning.MilestoneStatusPending,
		Notes:        strings.TrimSpace(req.Args["notes"]),
		DependsOnRef: strings.TrimSpace(req.Args["depends_on_ref"]),
	}
	if err := ensureMilestoneRefsAvailable(plan.Milestones, []planning.Milestone{item}); err != nil {
		return tools.Result{}, err
	}
	nextMilestones := appendMilestones(plan.Milestones, []planning.Milestone{item})
	if err := planning.ValidateMilestoneProgress(nextMilestones); err != nil {
		return tools.Result{}, err
	}
	return tools.MilestonePlanResult(planning.Plan{
		Summary:    plan.Summary,
		Milestones: nextMilestones,
	}), nil
}

func (updateItemTool) Call(ctx context.Context, opts tools.Options) (tools.Result, error) {
	runtime, req := opts.Runtime, opts.Request
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
	updated, err := updatedMilestonePlan(plan, req, actorFromRuntime(runtime))
	if err != nil {
		return tools.Result{}, err
	}
	if err := validateCompletedMilestoneTodos(ctx, control, runtime.SessionID, updated.Milestones); err != nil {
		return tools.Result{}, err
	}
	return tools.MilestonePlanResult(tools.ScopedMilestonePlan(runtime, updated)), nil
}

func (writeTool) Call(ctx context.Context, opts tools.Options) (tools.Result, error) {
	runtime, req := opts.Runtime, opts.Request
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
	return "Added milestone", result.Output
}

func (updateItemTool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "Updated milestone", result.Output
}

func (writeTool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "Updated milestones", result.Output
}

func (listTool) FinalizeResult(ctx context.Context, runtime tools.Runtime, req tools.Request, result tools.Result) (tools.Result, error) {
	control, err := tools.RequireSessionControl(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	plan, err := control.GetMilestonePlan(ctx, runtime.SessionID)
	if err != nil {
		return tools.Result{}, err
	}
	if result.Stored == nil {
		result.Stored = tools.MilestoneStoredResult(plan)
	}
	return result, nil
}
func (addItemsTool) FinalizeResult(ctx context.Context, runtime tools.Runtime, req tools.Request, result tools.Result) (tools.Result, error) {
	control, err := tools.RequireSessionControl(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	plan, err := control.GetMilestonePlan(ctx, runtime.SessionID)
	if err != nil {
		return tools.Result{}, err
	}
	item := planning.Milestone{
		Ref:          req.Args["ref"],
		Title:        strings.TrimSpace(req.Args["title"]),
		Status:       planning.MilestoneStatusPending,
		Notes:        strings.TrimSpace(req.Args["notes"]),
		DependsOnRef: strings.TrimSpace(req.Args["depends_on_ref"]),
	}
	if err := ensureMilestoneRefsAvailable(plan.Milestones, []planning.Milestone{item}); err != nil {
		return tools.Result{}, err
	}
	nextMilestones := appendMilestones(plan.Milestones, []planning.Milestone{item})
	if err := planning.ValidateMilestoneProgress(nextMilestones); err != nil {
		return tools.Result{}, err
	}
	plan, err = control.SetMilestonePlan(ctx, runtime.SessionID, plan.Summary, nextMilestones)
	if err != nil {
		return tools.Result{}, err
	}
	result.Stored = tools.MilestoneStoredResult(plan)
	return result, nil
}
func (updateItemTool) FinalizeResult(ctx context.Context, runtime tools.Runtime, req tools.Request, result tools.Result) (tools.Result, error) {
	control, err := tools.RequireSessionControl(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	plan, err := control.GetMilestonePlan(ctx, runtime.SessionID)
	if err != nil {
		return tools.Result{}, err
	}
	updated, err := updatedMilestonePlan(plan, req, actorFromRuntime(runtime))
	if err != nil {
		return tools.Result{}, err
	}
	if err := validateCompletedMilestoneTodos(ctx, control, runtime.SessionID, updated.Milestones); err != nil {
		return tools.Result{}, err
	}
	plan, err = control.SetMilestonePlan(ctx, runtime.SessionID, updated.Summary, updated.Milestones)
	if err != nil {
		return tools.Result{}, err
	}
	stored := tools.MilestoneStoredResult(tools.MilestonePlanForRef(plan, req.Args["ref"]))
	result.Stored = stored
	result.Output = tools.FormatMilestoneOutput(stored)
	return result, nil
}
func (writeTool) FinalizeResult(ctx context.Context, runtime tools.Runtime, req tools.Request, result tools.Result) (tools.Result, error) {
	control, err := tools.RequireSessionControl(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	milestones, err := planning.ParseMilestones(req.Args["milestones"])
	if err != nil {
		return tools.Result{}, err
	}
	if err := validateCompletedMilestoneTodos(ctx, control, runtime.SessionID, milestones); err != nil {
		return tools.Result{}, err
	}
	plan, err := control.SetMilestonePlan(ctx, runtime.SessionID, req.Args["summary"], milestones)
	if err != nil {
		return tools.Result{}, err
	}
	result.Stored = tools.MilestoneStoredResult(plan)
	return result, nil
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
	seenTitles := make(map[string]struct{}, len(existing)+len(added))
	for _, item := range existing {
		if ref := strings.TrimSpace(item.Ref); ref != "" {
			seenRefs[ref] = struct{}{}
		}
		if title := normalizedMilestoneTitle(item.Title); title != "" {
			seenTitles[title] = struct{}{}
		}
	}
	for _, item := range added {
		ref := strings.TrimSpace(item.Ref)
		if _, exists := seenRefs[ref]; exists {
			return fmt.Errorf("duplicate milestone ref %q", item.Ref)
		}
		seenRefs[ref] = struct{}{}
		title := normalizedMilestoneTitle(item.Title)
		if _, exists := seenTitles[title]; exists {
			return fmt.Errorf("duplicate milestone title %q", item.Title)
		}
		seenTitles[title] = struct{}{}
	}
	return nil
}

func normalizedMilestoneTitle(title string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(title))), " ")
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

type milestoneActor struct {
	ID   id.ID
	Role chatrole.Role
}

func actorFromRuntime(runtime tools.Runtime) milestoneActor {
	return milestoneActor{
		ID:   runtime.ChatID,
		Role: runtime.ChatRole,
	}
}

func updatedMilestonePlan(plan planning.Plan, req tools.Request, actor milestoneActor) (planning.Plan, error) {
	ref := req.Args["ref"]
	status, err := planning.MilestoneStatusString(req.Args["status"])
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
			if err := ensureMilestoneTitleAvailable(milestones, ref, title); err != nil {
				return planning.Plan{}, err
			}
			milestones[idx].Title = title
		}
		if notes, ok := req.Args["notes"]; ok {
			milestones[idx].Notes = strings.TrimSpace(notes)
		}
		if dependsOnRef, ok := req.Args["depends_on_ref"]; ok {
			milestones[idx].DependsOnRef = strings.TrimSpace(dependsOnRef)
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

func ensureMilestoneTitleAvailable(existing []planning.Milestone, ref, title string) error {
	title = normalizedMilestoneTitle(title)
	if title == "" {
		return nil
	}
	for _, item := range existing {
		if item.Ref == ref {
			continue
		}
		if normalizedMilestoneTitle(item.Title) == title {
			return fmt.Errorf("duplicate milestone title %q", strings.TrimSpace(item.Title))
		}
	}
	return nil
}

func validateMilestoneOwner(milestone planning.Milestone, next planning.MilestoneStatus, actor milestoneActor) error {
	if actor.ID == "" || actor.Role == chatrole.Orchestrator {
		return nil
	}
	if milestone.OwnerChatID != nil && *milestone.OwnerChatID != actor.ID {
		return fmt.Errorf("milestone %q is owned by chat %s", milestone.Ref, *milestone.OwnerChatID)
	}
	switch next {
	case planning.MilestoneStatusExecuting:
		if actor.Role != chatrole.Execution {
			return fmt.Errorf("milestone %q can only be set to executing by an execution chat", milestone.Ref)
		}
	}
	return nil
}

func applyMilestoneOwner(milestone *planning.Milestone, status planning.MilestoneStatus, actor milestoneActor) {
	switch status {
	case planning.MilestoneStatusExecuting:
		if actor.ID != "" && actor.Role != chatrole.Orchestrator {
			owner := actor.ID
			milestone.OwnerChatID = &owner
		}
	default:
		milestone.OwnerChatID = nil
	}
}

func validateCompletedMilestoneTodos(ctx context.Context, control tools.SessionControl, sessionID id.ID, milestones []planning.Milestone) error {
	for _, milestone := range milestones {
		if milestone.Status != planning.MilestoneStatusCompleted {
			continue
		}
		todos, err := control.ListTodos(ctx, sessionID, milestone.Ref)
		if err != nil {
			return err
		}
		for _, todo := range todos {
			if todo.Status == planning.TodoStatusCompleted {
				continue
			}
			name := strings.TrimSpace(milestone.Title)
			if name == "" {
				name = milestone.Ref
			}
			return fmt.Errorf("cannot complete milestone %q while task %s is %s", name, todo.ID, todo.Status.String())
		}
	}
	return nil
}
