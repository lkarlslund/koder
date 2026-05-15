package chattool

import (
	"context"
	"errors"
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/tools"
)

func init() {
	tools.Register(listTool{}, tools.ToolSpec{
		Title:       "List chats",
		Description: "List chats in the current session.",
		Usage:       "List chats in the current session, including worker chats started for decomposition or execution.",
		Parameters:  `{"type":"object","properties":{},"additionalProperties":false}`,
		ExposeToLLM: true,
	})
	tools.Register(startDecompositionTool{}, tools.ToolSpec{
		Title:       "Start decomposition chat",
		Description: "Start a background decomposition chat for one milestone.",
		Usage:       "Start a new background decomposition chat for one milestone. Use this when a milestone needs dedicated todo synthesis instead of inline decomposition. The new chat owns the milestone until it sets the milestone to ready. After starting it, go idle unless you have unrelated work; milestone changes and subchat idle/completion updates will be sent to you automatically.",
		Parameters:  `{"type":"object","properties":{"milestone_ref":{"type":"string","description":"Milestone ref to decompose"},"title":{"type":"string","description":"Optional chat title"}},"required":["milestone_ref"],"additionalProperties":false}`,
		ExposeToLLM: true,
	})
	tools.Register(startExecutionTool{}, tools.ToolSpec{
		Title:       "Start execution chat",
		Description: "Start a background execution chat for one milestone.",
		Usage:       "Start a new background execution chat for one ready milestone. The execution chat owns the milestone until it sets the milestone to completed, blocked, or cancelled. After starting it, go idle unless you have unrelated work; milestone changes and subchat idle/completion updates will be sent to you automatically.",
		Parameters:  `{"type":"object","properties":{"milestone_ref":{"type":"string","description":"Milestone ref to execute"},"title":{"type":"string","description":"Optional chat title"}},"required":["milestone_ref"],"additionalProperties":false}`,
		ExposeToLLM: true,
	})
	tools.Register(pollTool{}, tools.ToolSpec{
		Title:       "Poll chat",
		Description: "Read the latest runtime state for one chat.",
		Usage:       "Read the latest runtime state for one chat by id, including whether it is running, waiting for approval, completed, or failed.",
		Parameters:  `{"type":"object","properties":{"chat_id":{"type":"string","description":"Chat UUID to inspect, as returned by chat_list"}},"required":["chat_id"],"additionalProperties":false}`,
		ExposeToLLM: true,
	})
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
	id := strings.TrimSpace(tools.FirstArg(args, "chat_id", "id"))
	id = strings.TrimPrefix(id, "#")
	if id == "" {
		return nil, errors.New("chat_id is required")
	}
	return map[string]string{"chat_id": id}, nil
}

func (listTool) Preview(req tools.Request) string { return "List chats" }
func (startDecompositionTool) Preview(req tools.Request) string {
	return "Start decomposition chat for " + req.Args["milestone_ref"]
}
func (startExecutionTool) Preview(req tools.Request) string {
	return "Start execution chat for " + req.Args["milestone_ref"]
}
func (pollTool) Preview(req tools.Request) string { return "Poll chat " + req.Args["chat_id"] }

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
	chatID := strings.TrimSpace(req.Args["chat_id"])
	status, err := control.PollChat(ctx, runtime.SessionID, chatID)
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
