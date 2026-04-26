package milestonetool

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
	tools.Register(listTool{})
	tools.Register(addItemsTool{})
	tools.Register(updateItemTool{})
	tools.Register(writeTool{})
}

type listTool struct{}
type addItemsTool struct{}
type updateItemTool struct{}
type writeTool struct{}

func (listTool) Kind() domain.ToolKind       { return domain.ToolKindMilestoneList }
func (addItemsTool) Kind() domain.ToolKind   { return domain.ToolKindMilestoneAdd }
func (updateItemTool) Kind() domain.ToolKind { return domain.ToolKindMilestoneUpdate }
func (writeTool) Kind() domain.ToolKind      { return domain.ToolKindMilestoneWrite }

func (listTool) BypassesPermission() bool       { return true }
func (addItemsTool) BypassesPermission() bool   { return true }
func (updateItemTool) BypassesPermission() bool { return true }
func (writeTool) BypassesPermission() bool      { return true }

func (listTool) Definition(tools.Runtime) (provider.ToolDefinition, bool) {
	return tools.FunctionDefinition(domain.ToolKindMilestoneList, "Read the current session milestone plan. Use this to understand the long-horizon plan before choosing or breaking down work.", `{"type":"object","properties":{},"additionalProperties":false}`), true
}

func (addItemsTool) Definition(tools.Runtime) (provider.ToolDefinition, bool) {
	return tools.FunctionDefinition(domain.ToolKindMilestoneAdd, "Append new pending milestones to the current session plan. Use milestones for larger chunks of work. Each milestone requires a stable ref so its todo bucket can be tracked separately.", `{"type":"object","properties":{"items":{"type":"array","description":"New milestones to append as pending","items":{"type":"object","properties":{"ref":{"type":"string"},"title":{"type":"string"},"notes":{"type":"string"}},"required":["ref","title"]}}},"required":["items"],"additionalProperties":false}`), true
}

func (updateItemTool) Definition(tools.Runtime) (provider.ToolDefinition, bool) {
	return tools.FunctionDefinition(domain.ToolKindMilestoneUpdate, "Update one milestone's status, and optionally its title or notes. Keep at most one milestone in_progress in the plan.", `{"type":"object","properties":{"ref":{"type":"string","description":"Milestone ref"},"status":{"type":"string","enum":["pending","in_progress","completed","blocked"]},"title":{"type":"string","description":"Optional replacement title"},"notes":{"type":"string","description":"Optional replacement notes"}},"required":["ref","status"],"additionalProperties":false}`), true
}

func (writeTool) Definition(tools.Runtime) (provider.ToolDefinition, bool) {
	return provider.ToolDefinition{}, false
}

func (listTool) NormalizeArgs(map[string]string) (map[string]string, error) {
	return map[string]string{}, nil
}

func (addItemsTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	raw := strings.TrimSpace(tools.FirstArg(args, "items"))
	if raw == "" {
		return nil, errors.New("items is empty")
	}
	if _, err := tools.ParseMilestoneAddItems(raw); err != nil {
		return nil, err
	}
	return map[string]string{"items": raw}, nil
}

func (updateItemTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	ref, err := tools.ParseMilestoneRef(tools.FirstArg(args, "ref"))
	if err != nil {
		return nil, err
	}
	status, err := tools.ParseMilestoneStatus(tools.FirstArg(args, "status"))
	if err != nil {
		return nil, err
	}
	out := map[string]string{
		"ref":    ref,
		"status": string(status),
	}
	if title := strings.TrimSpace(tools.FirstArg(args, "title")); title != "" {
		out["title"] = title
	}
	if notes, ok := args["notes"]; ok {
		out["notes"] = strings.TrimSpace(notes)
	}
	return out, nil
}

func (writeTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
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

func (listTool) LegacyArgs(raw string) map[string]string       { return map[string]string{} }
func (addItemsTool) LegacyArgs(raw string) map[string]string   { return map[string]string{"items": raw} }
func (updateItemTool) LegacyArgs(raw string) map[string]string { return map[string]string{"ref": raw} }
func (writeTool) LegacyArgs(raw string) map[string]string {
	return map[string]string{"milestones": raw}
}

func (listTool) Preview(req tools.Request) string       { return "Read milestones" }
func (addItemsTool) Preview(req tools.Request) string   { return "Add milestones" }
func (updateItemTool) Preview(req tools.Request) string { return "Update milestone " + req.Args["ref"] }
func (writeTool) Preview(req tools.Request) string      { return "Replace milestones" }

func (listTool) PresentationForPreview(preview string) tools.Presentation {
	return tools.Presentation{Title: "List milestones", Preview: preview}
}

func (addItemsTool) PresentationForPreview(preview string) tools.Presentation {
	return tools.Presentation{Title: "Added milestones", Preview: preview}
}

func (updateItemTool) PresentationForPreview(preview string) tools.Presentation {
	return tools.Presentation{Title: "Updated milestone", Preview: preview}
}

func (writeTool) PresentationForPreview(preview string) tools.Presentation {
	return tools.Presentation{Title: "Updated milestones", Preview: preview}
}

func (listTool) Presentation(req tools.Request) tools.Presentation {
	return listTool{}.PresentationForPreview(listTool{}.Preview(req))
}

func (addItemsTool) Presentation(req tools.Request) tools.Presentation {
	return addItemsTool{}.PresentationForPreview(addItemsTool{}.Preview(req))
}

func (updateItemTool) Presentation(req tools.Request) tools.Presentation {
	return updateItemTool{}.PresentationForPreview(updateItemTool{}.Preview(req))
}

func (writeTool) Presentation(req tools.Request) tools.Presentation {
	return writeTool{}.PresentationForPreview(writeTool{}.Preview(req))
}

func (listTool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	st, err := tools.RequireSessionStore(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	plan, err := st.GetMilestonePlan(ctx, runtime.SessionID)
	if err != nil {
		return tools.Result{}, err
	}
	return tools.MilestonePlanResult(plan), nil
}

func (addItemsTool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	items, err := tools.ParseMilestoneAddItems(req.Args["items"])
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
	if err := ensureMilestoneRefsAvailable(plan.Milestones, items); err != nil {
		return tools.Result{}, err
	}
	return tools.MilestonePlanResult(store.MilestonePlan{
		Summary:    plan.Summary,
		Milestones: appendMilestones(plan.Milestones, items),
	}), nil
}

func (updateItemTool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	st, err := tools.RequireSessionStore(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	plan, err := st.GetMilestonePlan(ctx, runtime.SessionID)
	if err != nil {
		return tools.Result{}, err
	}
	updated, err := updatedMilestonePlan(plan, req)
	if err != nil {
		return tools.Result{}, err
	}
	return tools.MilestonePlanResult(updated), nil
}

func (writeTool) Execute(_ context.Context, _ tools.Runtime, req tools.Request) (tools.Result, error) {
	milestones, err := tools.ParseMilestones(req.Args["milestones"])
	if err != nil {
		return tools.Result{}, err
	}
	return tools.MilestonePlanResult(store.MilestonePlan{
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

func (writeTool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "Updated milestones", result.Output
}

func (listTool) PersistResult(ctx context.Context, st *store.Store, sessionID int64, req tools.Request, result tools.Result) (<-chan domain.Event, error) {
	plan, err := st.GetMilestonePlan(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	result.Stored = tools.MilestoneStoredResult(plan)
	return tools.PersistStandardResult(ctx, st, sessionID, req, result)
}

func (addItemsTool) PersistResult(ctx context.Context, st *store.Store, sessionID int64, req tools.Request, result tools.Result) (<-chan domain.Event, error) {
	items, err := tools.ParseMilestoneAddItems(req.Args["items"])
	if err != nil {
		return nil, err
	}
	plan, err := st.GetMilestonePlan(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if err := ensureMilestoneRefsAvailable(plan.Milestones, items); err != nil {
		return nil, err
	}
	plan, err = st.SetMilestonePlan(ctx, sessionID, plan.Summary, appendMilestones(plan.Milestones, items))
	if err != nil {
		return nil, err
	}
	result.Stored = tools.MilestoneStoredResult(plan)
	return tools.PersistStandardResult(ctx, st, sessionID, req, result)
}

func (updateItemTool) PersistResult(ctx context.Context, st *store.Store, sessionID int64, req tools.Request, result tools.Result) (<-chan domain.Event, error) {
	plan, err := st.GetMilestonePlan(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	updated, err := updatedMilestonePlan(plan, req)
	if err != nil {
		return nil, err
	}
	plan, err = st.SetMilestonePlan(ctx, sessionID, updated.Summary, updated.Milestones)
	if err != nil {
		return nil, err
	}
	result.Stored = tools.MilestoneStoredResult(plan)
	return tools.PersistStandardResult(ctx, st, sessionID, req, result)
}

func (writeTool) PersistResult(ctx context.Context, st *store.Store, sessionID int64, req tools.Request, result tools.Result) (<-chan domain.Event, error) {
	milestones, err := tools.ParseMilestones(req.Args["milestones"])
	if err != nil {
		return nil, err
	}
	plan, err := st.SetMilestonePlan(ctx, sessionID, req.Args["summary"], milestones)
	if err != nil {
		return nil, err
	}
	result.Stored = tools.MilestoneStoredResult(plan)
	return tools.PersistStandardResult(ctx, st, sessionID, req, result)
}

func appendMilestones(existing, added []store.Milestone) []store.Milestone {
	out := make([]store.Milestone, 0, len(existing)+len(added))
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

func ensureMilestoneRefsAvailable(existing, added []store.Milestone) error {
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

func updatedMilestonePlan(plan store.MilestonePlan, req tools.Request) (store.MilestonePlan, error) {
	ref := req.Args["ref"]
	status := domain.MilestoneStatus(req.Args["status"])
	milestones := append([]store.Milestone(nil), plan.Milestones...)
	found := false
	for idx := range milestones {
		if milestones[idx].Ref != ref {
			continue
		}
		found = true
		milestones[idx].Status = status
		if title := strings.TrimSpace(req.Args["title"]); title != "" {
			milestones[idx].Title = title
		}
		if notes, ok := req.Args["notes"]; ok {
			milestones[idx].Notes = strings.TrimSpace(notes)
		}
		break
	}
	if !found {
		return store.MilestonePlan{}, fmt.Errorf("milestone %q not found", ref)
	}
	if err := tools.ValidateMilestoneProgress(milestones); err != nil {
		return store.MilestonePlan{}, err
	}
	return store.MilestonePlan{
		Summary:    plan.Summary,
		Milestones: milestones,
	}, nil
}
