package chattool

import (
	"context"
	"errors"
	"strings"

	"github.com/lkarlslund/koder/internal/chatrole"
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
	tools.Register(startTool{}, tools.ToolSpec{
		Title:       "Start chat",
		Description: "Start a background child chat using a registered chat profile.",
		Usage:       "Start a background child chat using a registered chat profile. Use milestone_ref or todo_ref to scope what the child chat can see. A todo_ref scopes the child to that single todo item. After starting it, go idle unless you have unrelated work; subchat idle/completion updates will be sent to you automatically.",
		Parameters:  `{"type":"object","properties":{"profile":{"type":"string","description":"Registered chat profile to use, such as decomposition, execution, or planning"},"objective":{"type":"string","description":"Specific objective for the child chat"},"title":{"type":"string","description":"Optional chat title"},"milestone_ref":{"type":"string","description":"Optional milestone ref to scope the child chat"},"todo_ref":{"type":"string","description":"Optional todo item UUID to scope the child chat to one todo"}},"required":["profile","objective"],"additionalProperties":false}`,
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
type startTool struct{}
type pollTool struct{}

func (listTool) Kind() domain.ToolKind  { return domain.ToolKindChatList }
func (startTool) Kind() domain.ToolKind { return domain.ToolKindChatStart }
func (pollTool) Kind() domain.ToolKind  { return domain.ToolKindChatPoll }

func (listTool) BypassesPermission() bool  { return true }
func (startTool) BypassesPermission() bool { return true }
func (pollTool) BypassesPermission() bool  { return true }

func (startTool) Definition(runtime tools.Runtime, spec tools.ToolSpec) (tools.ToolSpec, bool) {
	switch runtime.ChatRole {
	case "", chatrole.General, chatrole.Orchestrator, chatrole.Planning:
		return spec, true
	default:
		return tools.ToolSpec{}, false
	}
}

func (listTool) NormalizeArgs(map[string]string) (map[string]string, error) {
	return map[string]string{}, nil
}

func (startTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	profile := strings.TrimSpace(tools.FirstArg(args, "profile", "role"))
	if profile == "" {
		return nil, errors.New("profile is required")
	}
	if _, ok := chatrole.DefaultRegistry().Lookup(domain.WorkflowRole(profile)); !ok {
		return nil, errors.New("profile is not registered")
	}
	objective := strings.TrimSpace(tools.FirstArg(args, "objective", "prompt", "task"))
	if objective == "" {
		return nil, errors.New("objective is required")
	}
	out := map[string]string{
		"profile":   profile,
		"objective": objective,
	}
	if title := strings.TrimSpace(tools.FirstArg(args, "title")); title != "" {
		out["title"] = title
	}
	if ref := strings.TrimSpace(tools.FirstArg(args, "milestone_ref", "ref")); ref != "" {
		out["milestone_ref"] = ref
	}
	if todoRef := strings.TrimSpace(tools.FirstArg(args, "todo_ref", "todo_id")); todoRef != "" {
		out["todo_ref"] = todoRef
	}
	return out, nil
}

func (pollTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	id := strings.TrimSpace(tools.FirstArg(args, "chat_id", "id"))
	id = strings.TrimPrefix(id, "#")
	if id == "" {
		return nil, errors.New("chat_id is required")
	}
	return map[string]string{"chat_id": id}, nil
}

func (listTool) Preview(req tools.Request) string  { return "List chats" }
func (startTool) Preview(req tools.Request) string { return "Start " + req.Args["profile"] + " chat" }
func (pollTool) Preview(req tools.Request) string  { return "Poll chat " + req.Args["chat_id"] }

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

func (startTool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	control, err := tools.RequireChatControl(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	status, err := control.StartChat(ctx, runtime.SessionID, runtime.ChatID, tools.ChatStartRequest{
		Profile:      domain.WorkflowRole(req.Args["profile"]),
		Objective:    req.Args["objective"],
		Title:        req.Args["title"],
		MilestoneRef: req.Args["milestone_ref"],
		TodoRef:      domain.ID(req.Args["todo_ref"]),
	})
	if err != nil {
		return tools.Result{}, err
	}
	stored := tools.ChatListStored([]tools.ChatStatus{status})
	return tools.Result{
		Output: tools.DisplayTextForStored(req.Tool, stored),
		Stored: stored,
	}, nil
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

func (startTool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "Started chat", result.Output
}

func (pollTool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "Polled chat", result.Output
}
