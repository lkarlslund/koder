package updateplantool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
)

type tool struct{}

type step struct {
	Step   string `json:"step"`
	Status string `json:"status"`
}

func init() {
	tools.Register(tool{}, tools.ToolSpec{
		Title:       "Update plan",
		Description: "Update the current task plan.",
	})
}

func (tool) Kind() domain.ToolKind    { return domain.ToolKindUpdatePlan }
func (tool) BypassesPermission() bool { return true }
func (tool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	plan := strings.TrimSpace(tools.FirstArg(args, "plan", "steps"))
	if plan == "" {
		return nil, errors.New("plan is empty")
	}
	if _, err := normalizePlan(plan); err != nil {
		return nil, err
	}
	out := map[string]string{"plan": plan}
	if explanation := strings.TrimSpace(tools.FirstArg(args, "explanation", "summary")); explanation != "" {
		out["explanation"] = explanation
	}
	return out, nil
}
func (tool) LegacyArgs(raw string) map[string]string { return map[string]string{"plan": raw} }
func (tool) Preview(req tools.Request) string {
	steps, err := normalizePlan(req.Args["plan"])
	if err != nil || len(steps) == 0 {
		return "Update plan"
	}
	return steps[0].Step
}
func (tool) Execute(_ context.Context, _ tools.Runtime, req tools.Request) (tools.Result, error) {
	steps, err := normalizePlan(req.Args["plan"])
	if err != nil {
		return tools.Result{}, err
	}
	lines := make([]string, 0, len(steps)+1)
	if explanation := strings.TrimSpace(req.Args["explanation"]); explanation != "" {
		lines = append(lines, explanation)
	}
	for _, item := range steps {
		lines = append(lines, fmt.Sprintf("[%s] %s", item.Status, item.Step))
	}
	return tools.Result{
		Output: strings.Join(lines, "\n"),
		Meta: map[string]string{
			"plan":        req.Args["plan"],
			"explanation": req.Args["explanation"],
			"step_count":  fmt.Sprintf("%d", len(steps)),
		},
	}, nil
}
func (tool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "plan", result.Output
}
func (tool) PersistResult(ctx context.Context, st *store.Store, sessionID int64, req tools.Request, result tools.Result) (<-chan domain.Event, error) {
	steps, err := normalizePlan(req.Args["plan"])
	if err != nil {
		return nil, err
	}
	msg, err := st.AddMessage(ctx, sessionID, domain.MessageRoleTool, "plan")
	if err != nil {
		return nil, err
	}
	storedSteps := make([]domain.PlanStepPayload, 0, len(steps))
	for _, item := range steps {
		storedSteps = append(storedSteps, domain.PlanStepPayload{
			Step:   item.Step,
			Status: item.Status,
		})
	}
	if _, err := st.AddPart(ctx, msg.ID, domain.PlanUpdatePayload{
		Explanation: req.Args["explanation"],
		Steps:       storedSteps,
		Output:      result.Output,
	}); err != nil {
		return nil, err
	}
	return tools.EmitOnce(domain.Event{Kind: domain.EventKindStatus, Text: "Plan updated", Tool: req.Tool}), nil
}

func normalizePlan(raw string) ([]step, error) {
	var steps []step
	if err := json.Unmarshal([]byte(raw), &steps); err != nil {
		return nil, errors.New("plan must be a JSON array of step objects")
	}
	filtered := make([]step, 0, len(steps))
	inProgress := 0
	for _, item := range steps {
		item.Step = strings.TrimSpace(item.Step)
		item.Status = strings.TrimSpace(item.Status)
		if item.Step == "" {
			continue
		}
		switch item.Status {
		case "pending", "in_progress", "completed":
		default:
			return nil, fmt.Errorf("invalid step status %q", item.Status)
		}
		if item.Status == "in_progress" {
			inProgress++
		}
		filtered = append(filtered, item)
	}
	if len(filtered) == 0 {
		return nil, errors.New("plan has no steps")
	}
	if inProgress > 1 {
		return nil, errors.New("plan may contain at most one in_progress step")
	}
	return filtered, nil
}
