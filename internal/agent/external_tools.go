package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	chatpkg "github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/chatinteraction"
	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/claudedriver"
	"github.com/lkarlslund/koder/internal/codexdriver"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/tools"
)

// These are Koder capabilities that complement rather than duplicate the
// native coding, shell, filesystem, search, and MCP tools of an external agent
// backend such as Codex or Claude Code.
var externalAgentTools = map[tools.ID]struct{}{
	tools.Milestones: {}, tools.Tasks: {},
	tools.Chats: {}, tools.ChatStatus: {},
	tools.Sessions:    {},
	tools.Present:     {},
	tools.Memory:      {},
	tools.PhoneDevice: {}, tools.PhoneLocation: {}, tools.PhoneContacts: {}, tools.PhoneCalendar: {}, tools.PhoneMessages: {}, tools.PhoneCalls: {}, tools.PhoneNotifications: {},
	tools.PhoneClock: {}, tools.PhoneClipboard: {}, tools.PhoneApps: {}, tools.PhoneMedia: {}, tools.PhoneShare: {}, tools.PhoneOpen: {}, tools.PhonePhotos: {}, tools.PhoneLogs: {}, tools.PhoneAlerts: {},
}

// ExternalAgentToolIDs returns the Koder tools that may complement an external
// agent's native tool set. Availability for a particular role and interaction
// mode is still enforced when the agent starts and when a call is executed.
func ExternalAgentToolIDs() []tools.ID {
	ids := make([]tools.ID, 0, len(externalAgentTools))
	for _, toolID := range tools.RegisteredIDs() {
		if _, ok := externalAgentTools[toolID]; ok {
			ids = append(ids, toolID)
		}
	}
	return slices.Clone(ids)
}

func (e *Engine) externalToolDefinitions(ctx context.Context, rt *chatpkg.Chat) ([]provider.ToolDefinition, error) {
	if e == nil || e.toolsRuntime == nil {
		return nil, fmt.Errorf("koder tool runtime is unavailable")
	}
	runtime, err := e.toolsRuntime.ToolRuntime(ctx, rt)
	if err != nil {
		return nil, err
	}
	var definitions []provider.ToolDefinition
	for _, id := range tools.RegisteredIDs() {
		if _, allowed := externalAgentTools[id]; !allowed {
			continue
		}
		if definition, ok := tools.DefinitionFor(id, runtime); ok {
			definitions = append(definitions, definition)
		}
	}
	return definitions, nil
}

func (e *Engine) externalToolCall(ctx context.Context, rt *chatpkg.Chat, name, callID string, arguments json.RawMessage) (domain.ToolResult, error) {
	if e == nil || e.toolsRuntime == nil {
		return domain.ToolResult{}, fmt.Errorf("koder tool runtime is unavailable")
	}
	runtime, err := e.toolsRuntime.ToolRuntime(ctx, rt)
	if err != nil {
		return domain.ToolResult{}, err
	}
	request, err := tools.ParseProviderCall(provider.ToolCall{
		ID: callID, Type: "function", Function: provider.FunctionCall{Name: name, Arguments: string(arguments)},
	})
	if err != nil {
		return domain.ToolResult{}, err
	}
	if _, ok := externalAgentTools[request.Tool]; !ok {
		return domain.ToolResult{}, fmt.Errorf("%s is not an enabled Koder addition for external agents", request.Tool)
	}
	e.toolsRuntime.ToolExecutionStarted(ctx, rt, request)
	result, err := tools.Call(ctx, tools.Options{Runtime: runtime, Request: request})
	if err != nil {
		e.toolsRuntime.ToolExecutionFailed(ctx, rt, request, err)
		return domain.ToolResult{}, err
	}
	e.toolsRuntime.ToolExecutionFinished(ctx, rt, request)
	finalized, _, err := tools.FinalizeResult(ctx, runtime, request, result)
	return finalized, err
}

// externalAgentInstructions are the Koder instructions an external agent
// receives on top of its own: the chat's workflow role and interaction mode.
func externalAgentInstructions(_ domain.Session, chatRecord domain.Chat) string {
	parts := []string{
		strings.TrimSpace(chatrole.SystemPromptForChat(chatRecord)),
		strings.TrimSpace(chatinteraction.SystemPrompt(chatRecord.EffectiveInteractionMode())),
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

type codexToolBridge struct{ engine *Engine }

func (b codexToolBridge) Definitions(ctx context.Context, rt *chatpkg.Chat) ([]codexdriver.DynamicTool, error) {
	definitions, err := b.engine.externalToolDefinitions(ctx, rt)
	out := make([]codexdriver.DynamicTool, 0, len(definitions))
	for _, definition := range definitions {
		out = append(out, codexdriver.DynamicTool{
			Type:        "function",
			Name:        definition.Function.Name,
			Description: definition.Function.Description,
			InputSchema: append(json.RawMessage(nil), definition.Function.Parameters...),
		})
	}
	return out, err
}

func (b codexToolBridge) Call(ctx context.Context, rt *chatpkg.Chat, name, callID string, arguments json.RawMessage) (domain.ToolResult, error) {
	return b.engine.externalToolCall(ctx, rt, name, callID, arguments)
}

type claudeToolBridge struct{ engine *Engine }

func (b claudeToolBridge) Definitions(ctx context.Context, rt *chatpkg.Chat) ([]claudedriver.Tool, error) {
	definitions, err := b.engine.externalToolDefinitions(ctx, rt)
	out := make([]claudedriver.Tool, 0, len(definitions))
	for _, definition := range definitions {
		out = append(out, claudedriver.Tool{
			Name:        definition.Function.Name,
			Description: definition.Function.Description,
			InputSchema: append(json.RawMessage(nil), definition.Function.Parameters...),
		})
	}
	return out, err
}

func (b claudeToolBridge) Call(ctx context.Context, rt *chatpkg.Chat, name, callID string, arguments json.RawMessage) (domain.ToolResult, error) {
	return b.engine.externalToolCall(ctx, rt, name, callID, arguments)
}
