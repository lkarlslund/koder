package agent

import (
	"context"
	"encoding/json"
	"fmt"

	chatpkg "github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/codexdriver"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/tools"
)

// These are Koder capabilities that complement rather than duplicate Codex's
// native coding, shell, filesystem, search, and MCP tools.
var codexAdditionalTools = map[tools.ID]struct{}{
	tools.MilestoneList: {}, tools.MilestoneAdd: {}, tools.MilestoneUpdate: {}, tools.MilestoneDepend: {}, tools.MilestonePlan: {}, tools.MilestoneWrite: {},
	tools.TaskList: {}, tools.TaskAddItems: {}, tools.TaskUpdateItem: {}, tools.TaskFetchNext: {}, tools.TasksAdd: {}, tools.TasksUpdate: {},
	tools.ChatList: {}, tools.ChatStart: {}, tools.ChatSend: {}, tools.ChatCancel: {}, tools.ChatArchive: {}, tools.ChatRename: {}, tools.ChatCleanup: {}, tools.ChatStatus: {},
	tools.SessionList: {}, tools.SessionDelegate: {}, tools.SessionStart: {},
	tools.Present: {}, tools.ShowMedia: {}, tools.ShowImage: {}, tools.OfferFile: {},
	tools.PhonePhotosSearch: {}, tools.PhonePhotosThumbs: {}, tools.PhonePhotoView: {},
}

func (e *Engine) codexToolDefinitions(ctx context.Context, rt *chatpkg.Chat) ([]codexdriver.DynamicTool, error) {
	if e == nil || e.toolsRuntime == nil {
		return nil, fmt.Errorf("Koder tool runtime is unavailable")
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
		definition, ok := tools.DefinitionFor(id, runtime)
		if !ok {
			continue
		}
		definitions = append(definitions, codexdriver.DynamicTool{
			Type:        "function",
			Name:        definition.Function.Name,
			Description: definition.Function.Description,
			InputSchema: append(json.RawMessage(nil), definition.Function.Parameters...),
		})
	}
	return definitions, nil
}

func (e *Engine) codexToolCall(ctx context.Context, rt *chatpkg.Chat, name, callID string, arguments json.RawMessage) (string, error) {
	if e == nil || e.toolsRuntime == nil {
		return "", fmt.Errorf("Koder tool runtime is unavailable")
	}
	runtime, err := e.toolsRuntime.ToolRuntime(ctx, rt)
	if err != nil {
		return "", err
	}
	request, err := tools.ParseProviderCall(provider.ToolCall{
		ID: callID, Type: "function", Function: provider.FunctionCall{Name: name, Arguments: string(arguments)},
	})
	if err != nil {
		return "", err
	}
	if _, ok := codexAdditionalTools[request.Tool]; !ok {
		return "", fmt.Errorf("%s is not an enabled Koder addition for Codex", request.Tool)
	}
	e.toolsRuntime.ToolExecutionStarted(ctx, rt, request)
	result, err := tools.Call(ctx, tools.Options{Runtime: runtime, Request: request})
	if err != nil {
		e.toolsRuntime.ToolExecutionFailed(ctx, rt, request, err)
		return "", err
	}
	e.toolsRuntime.ToolExecutionFinished(ctx, rt, request)
	if result.Output != "" {
		return result.Output, nil
	}
	if result.Text != "" {
		return result.Text, nil
	}
	data, marshalErr := json.Marshal(result.Data)
	if marshalErr != nil {
		return "Tool completed.", nil
	}
	return string(data), nil
}

type codexToolBridge struct{ engine *Engine }

func (b codexToolBridge) Definitions(ctx context.Context, rt *chatpkg.Chat) ([]codexdriver.DynamicTool, error) {
	return b.engine.codexToolDefinitions(ctx, rt)
}

func (b codexToolBridge) Call(ctx context.Context, rt *chatpkg.Chat, name, callID string, arguments json.RawMessage) (string, error) {
	return b.engine.codexToolCall(ctx, rt, name, callID, arguments)
}
