package chattool

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/tools"
)

func init() {
	tools.Register(listTool{}, tools.ToolSpec{
		Title:       "List chats",
		Description: "List chats in the current session.",
		Usage:       "List chats in the current session, including worker chats started for execution. Archived chats are hidden by default; pass archived=true when you need to inspect or restore hidden chats.",
		Parameters:  `{"type":"object","properties":{"archived":{"type":"boolean","description":"Include archived chats. Defaults to false."}},"additionalProperties":false}`,
		ExposeToLLM: true,
	})
	tools.Register(startTool{}, tools.ToolSpec{
		Title:       "Start chat",
		Description: "Start a background child chat using a registered chat profile.",
		Usage:       "Start a background child chat using a registered chat profile. Use milestone_ref or task_ref to scope what the child chat can see. A task_ref scopes the child to that single task. After starting it, go idle unless you have unrelated work; the child chat will report back when it becomes idle, including task or milestone progress.",
		Parameters:  `{"type":"object","properties":{"profile":{"type":"string","description":"Registered chat profile to use, such as execution or planning"},"objective":{"type":"string","description":"Specific objective for the child chat"},"title":{"type":"string","description":"Optional chat title"},"milestone_ref":{"type":"string","description":"Optional milestone ref to scope the child chat"},"task_ref":{"type":"string","description":"Optional task UUID to scope the child chat to one task"}},"required":["profile","objective"],"additionalProperties":false}`,
		ExposeToLLM: true,
	})
	tools.Register(pollTool{}, tools.ToolSpec{
		Title:       "Poll chat",
		Description: "Read the latest runtime state for one chat.",
		Usage:       "Read the latest runtime state for one chat by id, including whether it is running, waiting for approval, completed, or failed.",
		Parameters:  `{"type":"object","properties":{"chat_id":{"type":"string","description":"Chat UUID to inspect, as returned by chat_list"}},"required":["chat_id"],"additionalProperties":false}`,
		ExposeToLLM: true,
	})
	tools.Register(updateTool{}, tools.ToolSpec{
		Title:       "Update chat",
		Description: "Update a chat, send a message to an owned child chat, or interrupt it.",
		Usage:       "Update chat metadata by id. Use archived=true for completed or no-longer-needed chats, archived=false to restore an archived chat, and title to rename a chat. Use message to send work instructions only to a direct child chat you own; do not message the current chat with this tool. Pass steer=true when the message should be delivered at a turn boundary to a busy child chat; otherwise it is queued as the next user turn. Use interrupt=true to ask a current or direct child chat to stop; pass hard=true only when it must cancel active streaming/tools immediately. If you need to find archived chats first, call chat_list with archived=true.",
		Parameters:  `{"type":"object","properties":{"chat_id":{"type":"string","description":"Chat UUID to update; defaults to the current chat when omitted"},"archived":{"type":"boolean","description":"Set archived visibility state. true hides the chat; false restores it."},"title":{"type":"string","description":"Optional replacement title"},"message":{"type":"string","description":"Optional message to queue for a direct child chat owned by the current chat. This cannot target the current chat."},"steer":{"type":"boolean","description":"When message is set, deliver it as a turn-boundary steer instead of the next user turn."},"interrupt":{"type":"boolean","description":"Ask the target chat to stop. The target must be the current chat or a direct child chat owned by the current chat."},"hard":{"type":"boolean","description":"With interrupt=true, immediately cancel active streaming or tools instead of stopping after the current turn."}},"additionalProperties":false}`,
		ExposeToLLM: true,
	})
}

type listTool struct{}
type startTool struct{}
type pollTool struct{}
type updateTool struct{}

func (listTool) Kind() domain.ToolKind  { return domain.ToolKindChatList }
func (startTool) Kind() domain.ToolKind { return domain.ToolKindChatStart }
func (pollTool) Kind() domain.ToolKind  { return domain.ToolKindChatPoll }
func (updateTool) Kind() domain.ToolKind {
	return domain.ToolKindChatUpdate
}

func (listTool) BypassesPermission() bool  { return true }
func (startTool) BypassesPermission() bool { return true }
func (pollTool) BypassesPermission() bool  { return true }
func (updateTool) BypassesPermission() bool {
	return true
}

func (startTool) Definition(runtime tools.Runtime, spec tools.ToolSpec) (tools.ToolSpec, bool) {
	switch runtime.ChatRole {
	case "", chatrole.General, chatrole.Orchestrator, chatrole.Planning:
		return spec, true
	default:
		return tools.ToolSpec{}, false
	}
}

func (listTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	return normalizeOptionalBool(args, "archived")
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
	if todoRef := strings.TrimSpace(tools.FirstArg(args, "task_ref", "task_id")); todoRef != "" {
		out["task_ref"] = todoRef
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

func (updateTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	out := map[string]string{}
	id := strings.TrimSpace(tools.FirstArg(args, "chat_id", "id"))
	id = strings.TrimPrefix(id, "#")
	if id != "" {
		out["chat_id"] = id
	}
	if archived := strings.TrimSpace(tools.FirstArg(args, "archived")); archived != "" {
		value, err := strconv.ParseBool(archived)
		if err != nil {
			return nil, fmt.Errorf("archived: %w", err)
		}
		out["archived"] = strconv.FormatBool(value)
	}
	if title := strings.TrimSpace(tools.FirstArg(args, "title")); title != "" {
		out["title"] = title
	}
	if message := strings.TrimSpace(tools.FirstArg(args, "message", "text")); message != "" {
		out["message"] = message
	}
	if steer := strings.TrimSpace(tools.FirstArg(args, "steer")); steer != "" {
		value, err := strconv.ParseBool(steer)
		if err != nil {
			return nil, fmt.Errorf("steer: %w", err)
		}
		out["steer"] = strconv.FormatBool(value)
	}
	if interrupt := strings.TrimSpace(tools.FirstArg(args, "interrupt")); interrupt != "" {
		value, err := strconv.ParseBool(interrupt)
		if err != nil {
			return nil, fmt.Errorf("interrupt: %w", err)
		}
		out["interrupt"] = strconv.FormatBool(value)
	}
	if hard := strings.TrimSpace(tools.FirstArg(args, "hard")); hard != "" {
		value, err := strconv.ParseBool(hard)
		if err != nil {
			return nil, fmt.Errorf("hard: %w", err)
		}
		out["hard"] = strconv.FormatBool(value)
	}
	if _, ok := out["archived"]; !ok && out["title"] == "" && out["message"] == "" && out["interrupt"] == "" {
		return nil, errors.New("archived, title, message, or interrupt is required")
	}
	return out, nil
}

func (listTool) Preview(req tools.Request) string  { return "List chats" }
func (startTool) Preview(req tools.Request) string { return "Start " + req.Args["profile"] + " chat" }
func (pollTool) Preview(req tools.Request) string  { return "Poll chat " + req.Args["chat_id"] }
func (updateTool) Preview(req tools.Request) string {
	var action string
	switch req.Args["archived"] {
	case "true":
		action = "Archive"
	case "false":
		action = "Restore"
	case "":
		if req.Args["interrupt"] == "true" {
			action = "Interrupt"
		} else if strings.TrimSpace(req.Args["message"]) != "" {
			action = "Message"
		} else {
			action = "Update"
		}
	default:
		action = "Update"
	}
	if strings.TrimSpace(req.Args["chat_id"]) == "" {
		return action + " current chat"
	}
	return action + " chat " + req.Args["chat_id"]
}

func normalizeOptionalBool(args map[string]string, key string) (map[string]string, error) {
	raw := strings.TrimSpace(tools.FirstArg(args, key))
	if raw == "" {
		return map[string]string{}, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", key, err)
	}
	return map[string]string{key: strconv.FormatBool(value)}, nil
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
	if req.Args["archived"] != "true" {
		statuses = filterArchivedChats(statuses)
	}
	return tools.Result{
		Output: tools.DisplayTextForStored(domain.ToolKindChatList, tools.ChatListStored(statuses)),
		Stored: tools.ChatListStored(statuses),
	}, nil
}

func filterArchivedChats(statuses []tools.ChatStatus) []tools.ChatStatus {
	out := make([]tools.ChatStatus, 0, len(statuses))
	for _, status := range statuses {
		if !status.Chat.Archived {
			out = append(out, status)
		}
	}
	return out
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
		TodoRef:      id.ID(req.Args["task_ref"]),
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
		Output: appendPollGuidance(tools.DisplayTextForStored(domain.ToolKindChatPoll, stored), status),
		Stored: stored,
	}, nil
}

func appendPollGuidance(output string, status tools.ChatStatus) string {
	if status.Busy || status.State == tools.ChatRunStateRunning || status.State == tools.ChatRunStateWaitingApproval {
		return strings.TrimSpace(output + "\nDo not repeatedly poll this chat. Busy chats report back to their parent chat when they become idle, including todo or milestone progress.")
	}
	return strings.TrimSpace(output + "\nDo not poll this chat again unless new work is queued or the user explicitly asks. This poll result is current.")
}

func (updateTool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	control, err := tools.RequireChatControl(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	chatID := id.ID(strings.TrimSpace(req.Args["chat_id"]))
	if chatID == "" {
		chatID = runtime.ChatID
	}
	update := tools.ChatUpdateRequest{Title: req.Args["title"]}
	if raw, ok := req.Args["archived"]; ok {
		value := raw == "true"
		update.Archived = &value
	}
	update.Message = req.Args["message"]
	update.Steer = req.Args["steer"] == "true"
	update.Interrupt = req.Args["interrupt"] == "true"
	update.Hard = req.Args["hard"] == "true"
	status, err := control.UpdateChat(ctx, runtime.SessionID, runtime.ChatID, chatID, update)
	if err != nil {
		return tools.Result{}, err
	}
	stored := tools.ChatListStored([]tools.ChatStatus{status})
	return tools.Result{
		Output: tools.DisplayTextForStored(domain.ToolKindChatUpdate, stored),
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

func (updateTool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "Updated chat", result.Output
}
