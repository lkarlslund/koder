package modelruntime

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	chatpkg "github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/tools"
)

func (r *Runtime) ClientForChat(chat domain.Chat) (*provider.Client, error) {
	model, err := r.settings.Model(chat)
	if err != nil {
		return nil, err
	}
	return provider.New(model.SourceProviderID, model.Provider, r.debug)
}

func (r *Runtime) ProviderStreamingEnabled(chat domain.Chat) bool {
	return r.providerConfigForChat(chat).Stream
}

func (r *Runtime) ChatRequest(session domain.Session, chat domain.Chat, messages []provider.Message, stream bool) provider.ChatRequest {
	modelID := strings.TrimSpace(chat.ModelID)
	providerCfg := config.Provider{}
	modelCfg := config.ModelConfig{}
	if model, err := r.settings.Model(chat); err == nil {
		modelID = model.SourceModelID
		providerCfg = model.Provider
		modelCfg = model.Model
	} else {
		providerID, fallbackModelID, _ := chatModel(chat)
		_, modelID = r.cfg.ResolveModel(providerID, fallbackModelID)
		providerCfg = r.providerConfigForChat(chat)
		modelCfg = r.modelConfigForChat(chat)
	}
	extraBody := provider.RequestExtraBody(providerCfg, modelCfg)
	extraBody = provider.WithLlamaCacheAffinity(extraBody, providerCfg, session.ID, chat.ID)
	req := provider.ChatRequest{
		SessionID:          session.ID,
		ChatID:             chat.ID,
		Model:              modelID,
		Messages:           messages,
		Stream:             stream,
		ExtraBody:          extraBody,
		ToolArgumentLimits: tools.ArgumentByteLimits(),
	}
	if len(messages) > 0 && (chat.ID != "" || chat.WorkflowRole != "") {
		if r.tools != nil {
			req.Tools = r.tools.Definitions(session, chat)
		}
		if len(req.Tools) > 0 {
			req.ToolChoice = "auto"
		}
	}
	return req
}

func (r *Runtime) ParseProviderToolCallsForTranscript(raw []provider.ToolCall, sessionID id.ID) chatpkg.ToolCallParseResult {
	var out chatpkg.ToolCallParseResult
	var parseErr error
	for _, item := range raw {
		call, err := r.parseProviderToolCall(item)
		if err != nil {
			if parseErr == nil {
				parseErr = err
			}
			r.RecordLifecycle(sessionID, "provider_tool_call_parse_error", err.Error(), map[string]string{
				"tool_call_id": strings.TrimSpace(item.ID),
				"tool_type":    strings.TrimSpace(item.Type),
			})
			if failed, ok := r.failedProviderToolCall(item, err); ok {
				out.ToolCalls = append(out.ToolCalls, failed)
			}
			continue
		}
		r.RecordLifecycle(sessionID, "tool_call_parsed", call.ContextString(), map[string]string{"tool": call.Tool.String(), "tool_call_id": call.ToolCallID})
		out.Requests = append(out.Requests, call)
		out.ToolCalls = append(out.ToolCalls, toolCallRecord(call))
	}
	out.Err = parseErr
	return out
}

func (r *Runtime) FailedStreamedProviderToolCall(callErr provider.ToolCallError) domain.ToolCall {
	call := callErr.ToolCall
	kind := domain.ToolKind(strings.TrimSpace(call.Function.Name))
	now := time.Now().UTC()
	toolCallID := strings.TrimSpace(call.ID)
	if toolCallID == "" {
		toolCallID = "stream_argument_limit_" + strconv.FormatInt(now.UnixNano(), 10)
	}
	return domain.ToolCall{
		ToolCallID:  domain.ToolCallID(toolCallID),
		Tool:        kind,
		Status:      domain.ToolStatusErrored,
		Error:       &domain.ToolError{Message: callErr.Message},
		CompletedAt: now,
	}
}

func (r *Runtime) RecordLifecycle(sessionID id.ID, kind, text string, meta map[string]string) {
	if r == nil || r.debug == nil {
		return
	}
	r.debug.RecordLifecycle(sessionID, kind, text, meta)
}

func (r *Runtime) providerConfigForChat(chat domain.Chat) config.Provider {
	if model, err := r.settings.Model(chat); err == nil {
		return model.Provider
	}
	providerID, modelID, _ := chatModel(chat)
	providerID, _ = r.cfg.ResolveModel(providerID, modelID)
	cfg, _ := r.cfg.Provider(providerID)
	return cfg
}

func (r *Runtime) modelConfigForChat(chat domain.Chat) config.ModelConfig {
	if model, err := r.settings.Model(chat); err == nil {
		return model.Model
	}
	return modelConfigForRequest(r.cfg, chat.ProviderID, chat.ModelID)
}

func chatModel(chat domain.Chat) (string, string, error) {
	providerID := strings.TrimSpace(chat.ProviderID)
	modelID := strings.TrimSpace(chat.ModelID)
	if providerID == "" {
		return "", "", fmt.Errorf("chat %s has no provider", chat.ID)
	}
	if modelID == "" {
		return "", "", fmt.Errorf("chat %s has no model", chat.ID)
	}
	return providerID, modelID, nil
}

func modelConfigForRequest(cfg config.Config, providerID, modelID string) config.ModelConfig {
	model := cfg.ModelRequestOptions(providerID, modelID)
	if strings.TrimSpace(model.ProviderID) == "" {
		model.ProviderID = strings.TrimSpace(providerID)
	}
	if strings.TrimSpace(model.ModelID) == "" {
		model.ModelID = strings.TrimSpace(modelID)
	}
	return model
}

func (r *Runtime) parseProviderToolCall(item provider.ToolCall) (tools.Request, error) {
	name := strings.TrimSpace(item.Function.Name)
	ok := false
	serverID, toolName := "", ""
	if r.mcp != nil {
		localDefs := tools.Definitions(tools.Runtime{})
		if r.tools != nil {
			localDefs = r.tools.Definitions(domain.Session{}, domain.Chat{})
		}
		serverID, toolName, ok = r.mcp.ResolveToolName(name, localDefs)
	}
	if !ok {
		return tools.ParseProviderCall(item)
	}
	rawArgs := strings.TrimSpace(item.Function.Arguments)
	if rawArgs == "" {
		rawArgs = "{}"
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(rawArgs), &parsed); err != nil {
		return tools.Request{}, fmt.Errorf("decode mcp tool arguments for %s: %w", name, err)
	}
	req := tools.Request{
		Tool:       domain.ToolKindMCP,
		ToolCallID: strings.TrimSpace(item.ID),
		Args: map[string]string{
			"server":        serverID,
			"tool":          toolName,
			"arguments_raw": rawArgs,
		},
	}
	if req.ToolCallID == "" {
		return tools.Request{}, fmt.Errorf("provider MCP tool call for %s missing id", name)
	}
	normalized, err := tools.Normalize(req)
	if err != nil {
		return tools.Request{}, tools.ProviderCallError{Request: req, Err: err}
	}
	return normalized, nil
}

func (r *Runtime) failedProviderToolCall(item provider.ToolCall, parseErr error) (domain.ToolCall, bool) {
	var callErr tools.ProviderCallError
	if !errors.As(parseErr, &callErr) {
		return domain.ToolCall{}, false
	}
	req := callErr.Request
	if req.Tool == "" || strings.TrimSpace(req.ToolCallID) == "" {
		return domain.ToolCall{}, false
	}
	now := time.Now().UTC()
	return domain.ToolCall{
		ToolCallID:  domain.ToolCallID(req.ToolCallID),
		Tool:        req.Tool,
		Args:        req.Args,
		Status:      domain.ToolStatusErrored,
		Error:       &domain.ToolError{Message: "Invalid tool call: " + parseErr.Error()},
		CompletedAt: now,
	}, true
}

func toolCallRecord(call tools.Request) domain.ToolCall {
	return domain.ToolCall{
		ToolCallID: domain.ToolCallID(call.ToolCallID),
		Tool:       call.Tool,
		Args:       call.Args,
		Status:     domain.ToolStatusPending,
	}
}
