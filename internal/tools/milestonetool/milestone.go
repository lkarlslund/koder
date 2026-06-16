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
		Usage:       "Create one blank pending milestone with no tasks. Koder generates the milestone_key; copy that key exactly in later tool calls. Use depends_on_key to make it a child of another milestone. Use tasks_add afterwards to add concrete tasks, then milestone_update status=ready when the milestone is ready for execution. Fails if depends_on_key is unknown or if the dependency would create a cycle.",
		Parameters:  `{"type":"object","properties":{"title":{"type":"string","description":"Milestone title"},"notes":{"type":"string","description":"Optional milestone notes"},"depends_on_key":{"type":"string","description":"Optional parent milestone key for tree/dependency structure"}},"required":["title"],"additionalProperties":false}`,
		ExposeToLLM: true,
	})
	tools.Register(updateItemTool{}, tools.ToolSpec{
		Title:       "Update milestone",
		Description: "Update one milestone's status or details.",
		Usage:       "Update one milestone's status, title, or notes. Use milestone_depend, not milestone_update, when you only need to change depends_on_key. Use ready when decomposition is done and execution can start. Set completed, blocked, or cancelled when work is finished, blocked, created by accident, or no longer wanted.",
		Parameters:  `{"type":"object","properties":{"milestone_key":{"type":"string","description":"Milestone key returned by milestone_list or milestone_add"},"status":{"type":"string","enum":["pending","decomposing","ready","executing","completed","blocked","cancelled"]},"title":{"type":"string","description":"Optional replacement title"},"notes":{"type":"string","description":"Optional replacement notes"},"depends_on_key":{"type":"string","description":"Optional parent milestone key. Pass an empty string to make this a root milestone."}},"required":["milestone_key"],"additionalProperties":false}`,
		ExposeToLLM: true,
	})
	tools.Register(dependTool{}, tools.ToolSpec{
		Title:       "Set milestone dependency",
		Description: "Move one milestone under another milestone or back to the root.",
		Usage:       "Set exactly one milestone dependency parent. Use this instead of milestone_update for dependency-only changes. Pass milestone_key for the milestone to move and depends_on_key for its parent. Pass depends_on_key as an empty string to make the milestone a root milestone. Fails if either milestone key is unknown or if the dependency would create a cycle.",
		Parameters:  `{"type":"object","properties":{"milestone_key":{"type":"string","description":"Milestone key to move, returned by milestone_list or milestone_add"},"depends_on_key":{"type":"string","description":"Parent milestone key. Use an empty string to make this milestone a root milestone."}},"required":["milestone_key","depends_on_key"],"additionalProperties":false}`,
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
type dependTool struct{}
type writeTool struct{}

func (listTool) ID() tools.ID       { return tools.MilestoneList }
func (addItemsTool) ID() tools.ID   { return tools.MilestoneAdd }
func (updateItemTool) ID() tools.ID { return tools.MilestoneUpdate }
func (dependTool) ID() tools.ID     { return tools.MilestoneDepend }
func (writeTool) ID() tools.ID      { return tools.MilestoneWrite }

func (listTool) BypassesPermission() bool       { return true }
func (addItemsTool) BypassesPermission() bool   { return true }
func (updateItemTool) BypassesPermission() bool { return true }
func (dependTool) BypassesPermission() bool     { return true }
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

func (dependTool) Definition(runtime tools.Runtime, spec tools.ToolSpec) (tools.ToolSpec, bool) {
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
	title := strings.TrimSpace(args["title"])
	if title == "" {
		return nil, errors.New("title is empty")
	}
	out := map[string]string{"title": title}
	if notes, ok := args["notes"]; ok {
		out["notes"] = strings.TrimSpace(notes)
	}
	if dependsOnKey, ok := args["depends_on_key"]; ok {
		out["depends_on_key"] = strings.TrimSpace(dependsOnKey)
	}
	return out, nil
}

func (updateItemTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	key, err := planning.ParseMilestoneKey(args["milestone_key"])
	if err != nil {
		return nil, err
	}
	out := map[string]string{"milestone_key": key}
	changed := false
	if raw, ok := args["status"]; ok {
		status, err := planning.ParseMilestoneStatus(raw)
		if err != nil {
			return nil, err
		}
		out["status"] = status.String()
		changed = true
	}
	if title := strings.TrimSpace(args["title"]); title != "" {
		out["title"] = title
		changed = true
	}
	if notes, ok := args["notes"]; ok {
		out["notes"] = strings.TrimSpace(notes)
		changed = true
	}
	if dependsOnKey, ok := args["depends_on_key"]; ok {
		out["depends_on_key"] = strings.TrimSpace(dependsOnKey)
		changed = true
	}
	if !changed {
		return nil, errors.New("milestone update requires status, title, notes, or depends_on_key")
	}
	return out, nil
}

func (dependTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	key, err := planning.ParseMilestoneKey(args["milestone_key"])
	if err != nil {
		return nil, err
	}
	return map[string]string{
		"milestone_key":  key,
		"depends_on_key": strings.TrimSpace(args["depends_on_key"]),
	}, nil
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

func (listTool) Preview(req tools.Request) string     { return "Read milestones" }
func (addItemsTool) Preview(req tools.Request) string { return "Add milestone " + req.Args["title"] }
func (updateItemTool) Preview(req tools.Request) string {
	return "Update milestone " + req.Args["milestone_key"]
}
func (dependTool) Preview(req tools.Request) string {
	parent := strings.TrimSpace(req.Args["depends_on_key"])
	if parent == "" {
		parent = "root"
	}
	return "Set milestone " + req.Args["milestone_key"] + " dependency to " + parent
}
func (writeTool) Preview(req tools.Request) string { return "Replace milestones" }

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
	taskSummaries, err := milestoneTaskSummaries(ctx, control, runtime.SessionID, listed.Milestones)
	if err != nil {
		return tools.Result{}, err
	}
	result := tools.MilestonePlanResultWithTaskSummaries(listed, taskSummaries)
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

func milestoneTaskSummaries(ctx context.Context, control tools.SessionControl, sessionID id.ID, milestones []planning.Milestone) (map[string]string, error) {
	summaries := make(map[string]string, len(milestones))
	for _, milestone := range milestones {
		key := planning.MilestoneKey(milestone)
		tasks, err := control.ListTasks(ctx, sessionID, key)
		if err != nil {
			return nil, err
		}
		summaries[key] = taskSummary(tasks)
	}
	return summaries, nil
}

func taskSummary(tasks []planning.Task) string {
	if len(tasks) == 0 {
		return "no tasks added to milestone"
	}
	counts := make(map[planning.TaskStatus]int)
	for _, task := range tasks {
		counts[task.Status]++
	}
	parts := make([]string, 0, len(counts))
	for _, status := range planning.TaskStatusValues() {
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
		Title:        strings.TrimSpace(req.Args["title"]),
		Status:       planning.MilestoneStatusPending,
		Notes:        strings.TrimSpace(req.Args["notes"]),
		DependsOnRef: strings.TrimSpace(req.Args["depends_on_key"]),
	}
	nextMilestones := appendMilestones(plan.Milestones, []planning.Milestone{item})
	nextPlan, _ := planning.NormalizePlanKeys(planning.Plan{Summary: plan.Summary, Milestones: nextMilestones})
	nextMilestones = nextPlan.Milestones
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
	return callMilestoneUpdate(ctx, runtime, req)
}

func (dependTool) Call(ctx context.Context, opts tools.Options) (tools.Result, error) {
	runtime, req := opts.Runtime, opts.Request
	return callMilestoneUpdate(ctx, runtime, req)
}

func callMilestoneUpdate(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	control, err := tools.RequireSessionControl(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	ref, err := tools.AllowedMilestoneRef(runtime, req.Args["milestone_key"])
	if err != nil {
		return tools.Result{}, err
	}
	req.Args["milestone_key"] = ref
	plan, err := control.GetMilestonePlan(ctx, runtime.SessionID)
	if err != nil {
		return tools.Result{}, err
	}
	before, ok := milestoneForKey(plan.Milestones, req.Args["milestone_key"])
	if !ok {
		return tools.Result{}, fmt.Errorf("milestone %q not found", req.Args["milestone_key"])
	}
	updated, err := updatedMilestonePlan(plan, req, actorFromRuntime(runtime))
	if err != nil {
		return tools.Result{}, err
	}
	after, ok := milestoneForKey(updated.Milestones, req.Args["milestone_key"])
	if !ok {
		return tools.Result{}, fmt.Errorf("milestone %q not found", req.Args["milestone_key"])
	}
	if milestonesEquivalent(before, after) {
		return tools.Result{}, noMilestoneChangeError(before, req)
	}
	if err := validateCompletedMilestoneTasks(ctx, control, runtime.SessionID, updated.Milestones); err != nil {
		return tools.Result{}, err
	}
	result := tools.MilestonePlanResult(tools.ScopedMilestonePlan(runtime, updated))
	if summary := milestoneUpdateSummary(before, after); summary != "" {
		result.Output = summary + "\n" + result.Output
	}
	return result, nil
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
	if err := validateCompletedMilestoneTasks(ctx, control, runtime.SessionID, milestones); err != nil {
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

func (dependTool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "Updated milestone dependency", result.Output
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
		Title:        strings.TrimSpace(req.Args["title"]),
		Status:       planning.MilestoneStatusPending,
		Notes:        strings.TrimSpace(req.Args["notes"]),
		DependsOnRef: strings.TrimSpace(req.Args["depends_on_key"]),
	}
	nextMilestones := appendMilestones(plan.Milestones, []planning.Milestone{item})
	nextPlan, _ := planning.NormalizePlanKeys(planning.Plan{Summary: plan.Summary, Milestones: nextMilestones})
	nextMilestones = nextPlan.Milestones
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
	return finalizeMilestoneUpdate(ctx, runtime, req, result)
}
func (dependTool) FinalizeResult(ctx context.Context, runtime tools.Runtime, req tools.Request, result tools.Result) (tools.Result, error) {
	return finalizeMilestoneUpdate(ctx, runtime, req, result)
}
func finalizeMilestoneUpdate(ctx context.Context, runtime tools.Runtime, req tools.Request, result tools.Result) (tools.Result, error) {
	control, err := tools.RequireSessionControl(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	plan, err := control.GetMilestonePlan(ctx, runtime.SessionID)
	if err != nil {
		return tools.Result{}, err
	}
	before, ok := milestoneForKey(plan.Milestones, req.Args["milestone_key"])
	if !ok {
		return tools.Result{}, fmt.Errorf("milestone %q not found", req.Args["milestone_key"])
	}
	updated, err := updatedMilestonePlan(plan, req, actorFromRuntime(runtime))
	if err != nil {
		return tools.Result{}, err
	}
	after, ok := milestoneForKey(updated.Milestones, req.Args["milestone_key"])
	if !ok {
		return tools.Result{}, fmt.Errorf("milestone %q not found", req.Args["milestone_key"])
	}
	if milestonesEquivalent(before, after) {
		return tools.Result{}, noMilestoneChangeError(before, req)
	}
	if err := validateCompletedMilestoneTasks(ctx, control, runtime.SessionID, updated.Milestones); err != nil {
		return tools.Result{}, err
	}
	plan, err = control.SetMilestonePlan(ctx, runtime.SessionID, updated.Summary, updated.Milestones)
	if err != nil {
		return tools.Result{}, err
	}
	stored := tools.MilestoneStoredResult(tools.MilestonePlanForRef(plan, req.Args["milestone_key"]))
	result.Stored = stored
	result.Output = tools.FormatMilestoneOutput(stored)
	if summary := milestoneUpdateSummary(before, after); summary != "" {
		result.Output = summary + "\n" + result.Output
	}
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
	if err := validateCompletedMilestoneTasks(ctx, control, runtime.SessionID, milestones); err != nil {
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

func upsertMilestone(existing []planning.Milestone, next planning.Milestone) []planning.Milestone {
	out := append([]planning.Milestone(nil), existing...)
	for idx := range out {
		if planning.MilestoneKey(out[idx]) != planning.MilestoneKey(next) {
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
	key := req.Args["milestone_key"]
	var status planning.MilestoneStatus
	statusChanged := false
	if raw, ok := req.Args["status"]; ok {
		parsed, err := planning.MilestoneStatusString(raw)
		if err != nil {
			return plan, fmt.Errorf("invalid milestone status %q", raw)
		}
		status = parsed
		statusChanged = true
	}
	milestones := append([]planning.Milestone(nil), plan.Milestones...)
	found := false
	for idx := range milestones {
		if planning.MilestoneKey(milestones[idx]) != key {
			continue
		}
		found = true
		if statusChanged {
			if err := validateMilestoneOwner(milestones[idx], status, actor); err != nil {
				return planning.Plan{}, err
			}
			milestones[idx].Status = status
			applyMilestoneOwner(&milestones[idx], status, actor)
		}
		if title := strings.TrimSpace(req.Args["title"]); title != "" {
			milestones[idx].Title = title
		}
		if notes, ok := req.Args["notes"]; ok {
			milestones[idx].Notes = strings.TrimSpace(notes)
		}
		if dependsOnKey, ok := req.Args["depends_on_key"]; ok {
			milestones[idx].DependsOnRef = strings.TrimSpace(dependsOnKey)
		}
		break
	}
	if !found {
		return planning.Plan{}, fmt.Errorf("milestone %q not found", key)
	}
	if err := planning.ValidateMilestoneProgress(milestones); err != nil {
		return planning.Plan{}, err
	}
	return planning.Plan{
		Summary:    plan.Summary,
		Milestones: milestones,
	}, nil
}

func validateMilestoneOwner(milestone planning.Milestone, next planning.MilestoneStatus, actor milestoneActor) error {
	if actor.ID == "" || actor.Role == chatrole.Orchestrator {
		return nil
	}
	if milestone.OwnerChatID != nil && *milestone.OwnerChatID != actor.ID {
		return fmt.Errorf("milestone %q is owned by chat %s", planning.MilestoneKey(milestone), *milestone.OwnerChatID)
	}
	switch next {
	case planning.MilestoneStatusExecuting:
		if actor.Role != chatrole.Execution {
			return fmt.Errorf("milestone %q can only be set to executing by an execution chat", planning.MilestoneKey(milestone))
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

func validateCompletedMilestoneTasks(ctx context.Context, control tools.SessionControl, sessionID id.ID, milestones []planning.Milestone) error {
	for _, milestone := range milestones {
		if milestone.Status != planning.MilestoneStatusCompleted {
			continue
		}
		tasks, err := control.ListTasks(ctx, sessionID, planning.MilestoneKey(milestone))
		if err != nil {
			return err
		}
		for _, task := range tasks {
			if task.Status == planning.TaskStatusCompleted {
				continue
			}
			name := strings.TrimSpace(milestone.Title)
			if name == "" {
				name = planning.MilestoneKey(milestone)
			}
			return fmt.Errorf("cannot complete milestone %q while task %s is %s", name, task.ID, task.Status.String())
		}
	}
	return nil
}

func milestoneForKey(items []planning.Milestone, key string) (planning.Milestone, bool) {
	key = strings.TrimSpace(key)
	for _, item := range items {
		if planning.MilestoneKey(item) == key {
			return item, true
		}
	}
	return planning.Milestone{}, false
}

func milestonesEquivalent(a, b planning.Milestone) bool {
	return a.Status == b.Status &&
		strings.TrimSpace(a.Title) == strings.TrimSpace(b.Title) &&
		strings.TrimSpace(a.Notes) == strings.TrimSpace(b.Notes) &&
		strings.TrimSpace(a.DependsOnRef) == strings.TrimSpace(b.DependsOnRef)
}

func noMilestoneChangeError(current planning.Milestone, req tools.Request) error {
	key := planning.MilestoneKey(current)
	parts := []string{fmt.Sprintf("no changes applied to milestone %s", key)}
	if status, ok := req.Args["status"]; ok {
		parts = append(parts, fmt.Sprintf("status is already %s", strings.TrimSpace(status)))
	}
	if _, ok := req.Args["depends_on_key"]; !ok {
		currentParent := strings.TrimSpace(current.DependsOnRef)
		if currentParent == "" {
			currentParent = "unset"
		}
		parts = append(parts, fmt.Sprintf("depends_on_key is %s", currentParent))
		parts = append(parts, fmt.Sprintf("to change dependency, call milestone_depend with milestone_key=%s and depends_on_key=<parent milestone key>; do not retry milestone_update with only status", key))
	}
	return errors.New(strings.Join(parts, "; "))
}

func milestoneUpdateSummary(before, after planning.Milestone) string {
	key := planning.MilestoneKey(after)
	changes := make([]string, 0, 4)
	if before.Status != after.Status {
		changes = append(changes, fmt.Sprintf("status=%s", after.Status.String()))
	}
	if strings.TrimSpace(before.Title) != strings.TrimSpace(after.Title) {
		changes = append(changes, fmt.Sprintf("title=%q", strings.TrimSpace(after.Title)))
	}
	if strings.TrimSpace(before.Notes) != strings.TrimSpace(after.Notes) {
		changes = append(changes, "notes updated")
	}
	if strings.TrimSpace(before.DependsOnRef) != strings.TrimSpace(after.DependsOnRef) {
		parent := strings.TrimSpace(after.DependsOnRef)
		if parent == "" {
			parent = "root"
		}
		changes = append(changes, "depends_on_key="+parent)
	}
	if len(changes) == 0 {
		return ""
	}
	return fmt.Sprintf("Updated milestone %s: %s", key, strings.Join(changes, ", "))
}
