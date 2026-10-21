package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	chatpkg "github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/codexdriver"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/tools"
)

// These are Koder capabilities that complement rather than duplicate Codex's
// native coding, shell, filesystem, search, and MCP tools.
var codexAdditionalTools = map[tools.ID]struct{}{
	tools.Milestones: {}, tools.Tasks: {},
	tools.Chats: {}, tools.ChatStatus: {},
	tools.Sessions:    {},
	tools.Present:     {},
	tools.Memory:      {},
	tools.PhoneDevice: {}, tools.PhoneLocation: {}, tools.PhoneContacts: {}, tools.PhoneCalendar: {}, tools.PhoneMessages: {}, tools.PhoneCalls: {}, tools.PhoneNotifications: {},
	tools.PhoneClock: {}, tools.PhoneClipboard: {}, tools.PhoneApps: {}, tools.PhoneMedia: {}, tools.PhoneShare: {}, tools.PhoneOpen: {}, tools.PhonePhotos: {},
}

// CodexAdditionalToolIDs returns the Koder tools that may complement Codex's
// native tool set. Availability for a particular role and interaction mode is
// still enforced when the thread is started and when a call is executed.
func CodexAdditionalToolIDs() []tools.ID {
	ids := make([]tools.ID, 0, len(codexAdditionalTools))
	for _, toolID := range tools.RegisteredIDs() {
		if _, ok := codexAdditionalTools[toolID]; ok {
			ids = append(ids, toolID)
		}
	}
	return slices.Clone(ids)
}

func (e *Engine) codexToolDefinitions(ctx context.Context, rt *chatpkg.Chat) ([]codexdriver.DynamicTool, error) {
	if e == nil || e.toolsRuntime == nil {
		return nil, fmt.Errorf("koder tool runtime is unavailable")
	}
	runtime, err := e.toolsRuntime.ToolRuntime(ctx, rt)
	if err != nil {
		return nil, err
	}
	var definitions []codexdriver.DynamicTool
	for _, id := range tools.RegisteredIDs() {
		if _, allowed := codexAdditionalTools[id]; !allowed {
			continue
		}
		definition, ok := codexAdditionalToolDefinition(id, runtime)
		if !ok {
			continue
		}
		definitions = append(definitions, definition)
	}
	return definitions, nil
}

func codexAdditionalToolDefinition(id tools.ID, runtime tools.Runtime) (codexdriver.DynamicTool, bool) {
	if _, allowed := codexAdditionalTools[id]; !allowed {
		return codexdriver.DynamicTool{}, false
	}
	definition, ok := tools.DefinitionFor(id, runtime)
	if !ok {
		return codexdriver.DynamicTool{}, false
	}
	return codexdriver.DynamicTool{
		Type:        "function",
		Name:        definition.Function.Name,
		Description: definition.Function.Description,
		InputSchema: append(json.RawMessage(nil), definition.Function.Parameters...),
	}, true
}

func (e *Engine) codexToolCall(ctx context.Context, rt *chatpkg.Chat, name, callID string, arguments json.RawMessage) (domain.ToolResult, error) {
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
	if _, ok := codexAdditionalTools[request.Tool]; !ok {
		return domain.ToolResult{}, fmt.Errorf("%s is not an enabled Koder addition for Codex", request.Tool)
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

type codexToolBridge struct{ engine *Engine }

func (b codexToolBridge) Definitions(ctx context.Context, rt *chatpkg.Chat) ([]codexdriver.DynamicTool, error) {
	return b.engine.codexToolDefinitions(ctx, rt)
}

func (b codexToolBridge) Call(ctx context.Context, rt *chatpkg.Chat, name, callID string, arguments json.RawMessage) (domain.ToolResult, error) {
	return b.engine.codexToolCall(ctx, rt, name, callID, arguments)
}
