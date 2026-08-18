// Package chatstatustool publishes descriptive chat activity to users and coordinators.
package chatstatustool

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/tools"
)

func init() {
	tools.Register(statusTool{}, tools.ToolSpec{
		Title:       "Set chat status",
		Description: "Publish a concise description of what this chat is currently trying to accomplish.",
		Usage:       "Call when starting a new objective, changing approach or phase, becoming blocked, or reaching a meaningful state. Keep summary concise and user-facing. This descriptive status is separate from runtime states such as reasoning, streaming, tool execution, errors, and idle; do not call it for routine runtime transitions.",
		Parameters:  `{"type":"object","properties":{"summary":{"type":"string","description":"Concise user-facing description of the current objective or state"},"phase":{"type":"string","description":"Optional short phase such as investigating, implementing, verifying, waiting, or complete"},"progress_percent":{"type":"integer","minimum":0,"maximum":100,"description":"Optional approximate progress when genuinely meaningful"},"blocked":{"type":"boolean","description":"Whether progress currently requires external input or a state change"}},"required":["summary"],"additionalProperties":false}`,
		ExposeToLLM: true,
	})
}

type statusTool struct{}

func (statusTool) ID() tools.ID                     { return tools.ChatStatus }
func (statusTool) BypassesPermission() bool         { return true }
func (statusTool) Preview(req tools.Request) string { return strings.TrimSpace(req.Args["summary"]) }

func (statusTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	summary := strings.TrimSpace(args["summary"])
	if summary == "" {
		return nil, fmt.Errorf("summary is required")
	}
	normalized := map[string]string{"summary": summary}
	if phase := strings.TrimSpace(args["phase"]); phase != "" {
		normalized["phase"] = phase
	}
	if raw := strings.TrimSpace(args["progress_percent"]); raw != "" {
		progress, err := strconv.Atoi(raw)
		if err != nil || progress < 0 || progress > 100 {
			return nil, fmt.Errorf("progress_percent must be an integer between 0 and 100")
		}
		normalized["progress_percent"] = strconv.Itoa(progress)
	}
	if raw := strings.TrimSpace(args["blocked"]); raw != "" {
		blocked, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("blocked must be true or false")
		}
		normalized["blocked"] = strconv.FormatBool(blocked)
	}
	return normalized, nil
}

func (statusTool) Call(ctx context.Context, options tools.Options) (tools.Result, error) {
	control, err := tools.RequireChatStatusControl(options.Runtime)
	if err != nil {
		return tools.Result{}, err
	}
	activity := domain.ChatActivity{
		Summary: strings.TrimSpace(options.Request.Args["summary"]),
		Phase:   strings.TrimSpace(options.Request.Args["phase"]),
	}
	if raw := strings.TrimSpace(options.Request.Args["progress_percent"]); raw != "" {
		progress, _ := strconv.Atoi(raw)
		activity.ProgressPercent = &progress
	}
	activity.Blocked, _ = strconv.ParseBool(options.Request.Args["blocked"])
	updated, err := control.SetChatActivity(ctx, activity)
	if err != nil {
		return tools.Result{}, err
	}
	return tools.Result{
		Output: "Chat status updated: " + updated.Activity.Summary,
		Meta: map[string]string{
			"summary": updated.Activity.Summary,
			"phase":   updated.Activity.Phase,
		},
	}, nil
}
