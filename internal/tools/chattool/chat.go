package chattool

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/tools"
)

func init() {
	tools.Register(listTool{})
	tools.Register(startDecompositionTool{})
	tools.Register(startExecutionTool{})
	tools.Register(pollTool{})
}

type listTool struct{}
type startDecompositionTool struct{}
type startExecutionTool struct{}
type pollTool struct{}

func (listTool) Kind() domain.ToolKind               { return domain.ToolKindChatList }
func (startDecompositionTool) Kind() domain.ToolKind { return domain.ToolKindChatStartDecomp }
func (startExecutionTool) Kind() domain.ToolKind     { return domain.ToolKindChatStartExec }
func (pollTool) Kind() domain.ToolKind               { return domain.ToolKindChatPoll }

func (listTool) BypassesPermission() bool               { return true }
func (startDecompositionTool) BypassesPermission() bool { return true }
func (startExecutionTool) BypassesPermission() bool     { return true }
func (pollTool) BypassesPermission() bool               { return true }

func (listTool) Definition(runtime tools.Runtime) (provider.ToolDefinition, bool) {
	if !chatToolAllowed(runtime.ChatRole) {
		return provider.ToolDefinition{}, false
	}
	return tools.FunctionDefinition(domain.ToolKindChatList, "List chats in the current session, including worker chats started for decomposition or execution.", `{"type":"object","properties":{},"additionalProperties":false}`), true
}

func (startDecompositionTool) Definition(runtime tools.Runtime) (provider.ToolDefinition, bool) {
	if !chatToolAllowed(runtime.ChatRole) {
		return provider.ToolDefinition{}, false
	}
	return tools.FunctionDefinition(domain.ToolKindChatStartDecomp, "Start a new background decomposition chat for one milestone. Use this when a milestone needs dedicated todo synthesis instead of inline decomposition.", `{"type":"object","properties":{"milestone_ref":{"type":"string","description":"Milestone ref to decompose"},"title":{"type":"string","description":"Optional chat title"}},"required":["milestone_ref"],"additionalProperties":false}`), true
}

func (startExecutionTool) Definition(runtime tools.Runtime) (provider.ToolDefinition, bool) {
	if !chatToolAllowed(runtime.ChatRole) {
		return provider.ToolDefinition{}, false
	}
	return tools.FunctionDefinition(domain.ToolKindChatStartExec, "Start a new background execution chat for one milestone. Use this after a milestone has enough todo context to implement independently.", `{"type":"object","properties":{"milestone_ref":{"type":"string","description":"Milestone ref to execute"},"title":{"type":"string","description":"Optional chat title"}},"required":["milestone_ref"],"additionalProperties":false}`), true
}

func (pollTool) Definition(runtime tools.Runtime) (provider.ToolDefinition, bool) {
	if !chatToolAllowed(runtime.ChatRole) {
		return provider.ToolDefinition{}, false
	}
	return tools.FunctionDefinition(domain.ToolKindChatPoll, "Read the latest runtime state for one chat by id, including whether it is running, waiting for approval, completed, or failed.", `{"type":"object","properties":{"chat_id":{"type":"integer","description":"Chat id to inspect"}},"required":["chat_id"],"additionalProperties":false}`), true
}

func (listTool) NormalizeArgs(map[string]string) (map[string]string, error) {
	return map[string]string{}, nil
}

func (startDecompositionTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	return normalizeStartArgs(args)
}

func (startExecutionTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	return normalizeStartArgs(args)
}

func (pollTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	id, err := tools.ParseFlexibleInt(tools.FirstArg(args, "chat_id", "id"))
	if err != nil || id <= 0 {
		return nil, errors.New("chat_id must be a positive integer")
	}
	return map[string]string{"chat_id": fmt.Sprintf("%d", id)}, nil
}

func (listTool) LegacyArgs(raw string) map[string]string { return map[string]string{} }
func (startDecompositionTool) LegacyArgs(raw string) map[string]string {
	return map[string]string{"milestone_ref": raw}
}
func (startExecutionTool) LegacyArgs(raw string) map[string]string {
	return map[string]string{"milestone_ref": raw}
}
func (pollTool) LegacyArgs(raw string) map[string]string { return map[string]string{"chat_id": raw} }

func (listTool) Preview(req tools.Request) string { return "List chats" }
func (startDecompositionTool) Preview(req tools.Request) string {
	return "Start decomposition chat for " + req.Args["milestone_ref"]
}
func (startExecutionTool) Preview(req tools.Request) string {
	return "Start execution chat for " + req.Args["milestone_ref"]
}
func (pollTool) Preview(req tools.Request) string { return "Poll chat #" + req.Args["chat_id"] }

func (listTool) PresentationForPreview(preview string) tools.Presentation {
	return tools.Presentation{Title: "Listed chats", Preview: preview}
}

func (startDecompositionTool) PresentationForPreview(preview string) tools.Presentation {
	return tools.Presentation{Title: "Started decomposition chat", Preview: preview}
}

func (startExecutionTool) PresentationForPreview(preview string) tools.Presentation {
	return tools.Presentation{Title: "Started execution chat", Preview: preview}
}

func (pollTool) PresentationForPreview(preview string) tools.Presentation {
	return tools.Presentation{Title: "Polled chat", Preview: preview}
}

func (listTool) Presentation(req tools.Request) tools.Presentation {
	return listTool{}.PresentationForPreview(listTool{}.Preview(req))
}
func (startDecompositionTool) Presentation(req tools.Request) tools.Presentation {
	return startDecompositionTool{}.PresentationForPreview(startDecompositionTool{}.Preview(req))
}
func (startExecutionTool) Presentation(req tools.Request) tools.Presentation {
	return startExecutionTool{}.PresentationForPreview(startExecutionTool{}.Preview(req))
}
func (pollTool) Presentation(req tools.Request) tools.Presentation {
	return pollTool{}.PresentationForPreview(pollTool{}.Preview(req))
}

func (listTool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	control, err := tools.RequireChatControl(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	statuses, err := control.ListChats(ctx, runtime.SessionID)
	if err != nil {
		return tools.Result{}, err
	}
	return tools.Result{
		Output: tools.DisplayTextForStored(domain.ToolKindChatList, tools.ChatListStored(statuses)),
		Stored: tools.ChatListStored(statuses),
	}, nil
}

func (startDecompositionTool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	return startChat(ctx, runtime, req, true)
}

func (startExecutionTool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	return startChat(ctx, runtime, req, false)
}

func (pollTool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	control, err := tools.RequireChatControl(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	chatID, _ := tools.ParseFlexibleInt(req.Args["chat_id"])
	status, err := control.PollChat(ctx, runtime.SessionID, int64(chatID))
	if err != nil {
		return tools.Result{}, err
	}
	stored := tools.ChatListStored([]tools.ChatStatus{status})
	return tools.Result{
		Output: tools.DisplayTextForStored(domain.ToolKindChatPoll, stored),
		Stored: stored,
	}, nil
}

func (listTool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "Listed chats", result.Output
}

func (startDecompositionTool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "Started decomposition chat", result.Output
}

func (startExecutionTool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "Started execution chat", result.Output
}

func (pollTool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "Polled chat", result.Output
}

func normalizeStartArgs(args map[string]string) (map[string]string, error) {
	ref, err := tools.ParseMilestoneRef(tools.FirstArg(args, "milestone_ref", "ref"))
	if err != nil {
		return nil, err
	}
	out := map[string]string{"milestone_ref": ref}
	if title := strings.TrimSpace(tools.FirstArg(args, "title")); title != "" {
		out["title"] = title
	}
	return out, nil
}

func startChat(ctx context.Context, runtime tools.Runtime, req tools.Request, decomposition bool) (tools.Result, error) {
	control, err := tools.RequireChatControl(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	var status tools.ChatStatus
	if decomposition {
		status, err = control.StartDecomposition(ctx, runtime.SessionID, runtime.ChatID, req.Args["milestone_ref"], req.Args["title"])
	} else {
		status, err = control.StartExecution(ctx, runtime.SessionID, runtime.ChatID, req.Args["milestone_ref"], req.Args["title"])
	}
	if err != nil {
		return tools.Result{}, err
	}
	stored := tools.ChatListStored([]tools.ChatStatus{status})
	return tools.Result{
		Output: tools.DisplayTextForStored(req.Tool, stored),
		Stored: stored,
	}, nil
}

func chatToolAllowed(role domain.WorkflowRole) bool {
	switch role {
	case domain.WorkflowRoleDecomposition, domain.WorkflowRoleExecution, domain.WorkflowRoleGeneral, domain.WorkflowRoleOrchestrator, domain.WorkflowRolePlanning:
		return true
	default:
		return true
	}
}
