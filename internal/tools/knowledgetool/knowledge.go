// Package knowledgetool exposes Koder's durable knowledge graph through one
// model-facing, multi-action tool.
package knowledgetool

import (
	"context"
	"errors"
	"strings"

	knowledgeService "github.com/lkarlslund/koder/internal/knowledge/service"
	"github.com/lkarlslund/koder/internal/tools"
)

const serviceKey = "knowledge"

// RuntimeService makes an available Knowledge service visible to a chat tool
// runtime. A nil service intentionally contributes no capability.
func RuntimeService(service *knowledgeService.Service) map[string]any {
	if service == nil {
		return nil
	}
	return map[string]any{serviceKey: service}
}

func init() {
	tools.Register(tool{}, tools.ToolSpec{
		Title:       "Knowledge",
		Description: "Search and maintain Koder's durable, linked knowledge.",
		Usage:       "Choose one action. The actions offered at runtime depend on the current chat's authorization and available Knowledge service.",
		Parameters:  `{"type":"object","properties":{"action":{"type":"string"}},"required":["action"],"additionalProperties":false}`,
		// Read and write actions enable model exposure incrementally. Keeping the
		// registered shell private prevents a model from calling unfinished actions.
		ExposeToLLM: false,
	})
}

type tool struct{}

func (tool) ID() tools.ID             { return tools.Knowledge }
func (tool) BypassesPermission() bool { return false }
func (tool) Preview(req tools.Request) string {
	return "Knowledge " + strings.TrimSpace(req.Args["action"])
}

func (tool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	action := strings.TrimSpace(args["action"])
	if action == "" {
		return nil, errors.New("action is required")
	}
	return map[string]string{"action": action}, nil
}

func (tool) Call(_ context.Context, options tools.Options) (tools.Result, error) {
	if _, err := requireService(options.Runtime); err != nil {
		return tools.Result{}, err
	}
	return tools.Result{}, errors.New("knowledge actions are not available in this build")
}

func requireService(runtime tools.Runtime) (*knowledgeService.Service, error) {
	return tools.RequireService[*knowledgeService.Service](runtime, serviceKey)
}
