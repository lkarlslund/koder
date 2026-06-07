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
		Usage:       "Start a background child chat using a registered chat profile. Use milestone_ref or task_ref to scope what the child chat can see. A task_ref scopes the child to that single task. After starting it, go idle unless you have unrelated work; the child chat will automatically report back when it becomes idle, including task or milestone progress. Do not poll child chats.",
		Parameters:  `{"type":"object","properties":{"profile":{"type":"string","description":"Registered chat profile to use, such as execution or planning"},"objective":{"type":"string","description":"Specific objective for the child chat"},"title":{"type":"string","description":"Optional chat title"},"milestone_ref":{"type":"string","description":"Optional milestone ref to scope the child chat"},"task_ref":{"type":"string","description":"Optional task UUID to scope the child chat to one task"}},"required":["profile","objective"],"additionalProperties":false}`,
		ExposeToLLM: true,
	})
	tools.Register(sendTool{}, tools.ToolSpec{
		Title:       "Send chat message",
		Description: "Send a message to a direct child chat.",
		Usage:       "Send work instructions only to a direct child chat you own. Do not message the current chat with this tool. Pass steer=true when the message should be delivered at a turn boundary to a busy child chat; otherwise it is queued as the next user turn.",
		Parameters:  `{"type":"object","properties":{"chat_id":{"type":"string","description":"Direct child chat UUID to message"},"message":{"type":"string","description":"Message to queue for the child chat"},"steer":{"type":"boolean","description":"Deliver as a turn-boundary steer instead of the next user turn"}},"required":["chat_id","message"],"additionalProperties":false}`,
		ExposeToLLM: true,
	})
	tools.Register(cancelTool{}, tools.ToolSpec{
		Title:       "Cancel chat",
		Description: "Ask the current chat or a direct child chat to stop.",
		Usage:       "Ask the current chat or a direct child chat you own to stop. Omit chat_id to cancel the current chat. Pass hard=true only when it must cancel active streaming or tools immediately; otherwise it stops after the current turn.",
		Parameters:  `{"type":"object","properties":{"chat_id":{"type":"string","description":"Chat UUID to cancel; defaults to the current chat when omitted"},"hard":{"type":"boolean","description":"Immediately cancel active streaming or tools instead of stopping after the current turn"}},"additionalProperties":false}`,
		ExposeToLLM: true,
	})
	tools.Register(archiveTool{}, tools.ToolSpec{
		Title:       "Archive chat",
		Description: "Archive or restore a chat.",
		Usage:       "Set archived=true for completed or no-longer-needed chats, archived=false to restore an archived chat. Omit chat_id to target the current chat. Only idle chats can be archived. If you need to find archived chats first, call chat_list with archived=true.",
		Parameters:  `{"type":"object","properties":{"chat_id":{"type":"string","description":"Chat UUID to archive or restore; defaults to the current chat when omitted"},"archived":{"type":"boolean","description":"true hides the chat; false restores it"}},"required":["archived"],"additionalProperties":false}`,
		ExposeToLLM: true,
	})
	tools.Register(renameTool{}, tools.ToolSpec{
		Title:       "Rename chat",
		Description: "Rename a chat.",
		Usage:       "Rename the current chat or a direct child chat you own. Omit chat_id to target the current chat.",
		Parameters:  `{"type":"object","properties":{"chat_id":{"type":"string","description":"Chat UUID to rename; defaults to the current chat when omitted"},"title":{"type":"string","description":"Replacement title"}},"required":["title"],"additionalProperties":false}`,
		ExposeToLLM: true,
	})
}

type listTool struct{}
type startTool struct{}
type sendTool struct{}
type cancelTool struct{}
type archiveTool struct{}
type renameTool struct{}

func (listTool) ID() domain.ToolKind    { return domain.ToolKindChatList }
func (startTool) ID() domain.ToolKind   { return domain.ToolKindChatStart }
func (sendTool) ID() domain.ToolKind    { return domain.ToolKindChatSend }
func (cancelTool) ID() domain.ToolKind  { return domain.ToolKindChatCancel }
func (archiveTool) ID() domain.ToolKind { return domain.ToolKindChatArchive }
func (renameTool) ID() domain.ToolKind  { return domain.ToolKindChatRename }

func (listTool) BypassesPermission() bool    { return true }
func (startTool) BypassesPermission() bool   { return true }
func (sendTool) BypassesPermission() bool    { return true }
func (cancelTool) BypassesPermission() bool  { return true }
func (archiveTool) BypassesPermission() bool { return true }
func (renameTool) BypassesPermission() bool  { return true }

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

func (sendTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	chatID := requiredChatID(args)
	if chatID == "" {
		return nil, errors.New("chat_id is required")
	}
	message := strings.TrimSpace(tools.FirstArg(args, "message", "text"))
	if message == "" {
		return nil, errors.New("message is required")
	}
	out := map[string]string{"chat_id": chatID, "message": message}
	if steer := strings.TrimSpace(tools.FirstArg(args, "steer")); steer != "" {
		value, err := strconv.ParseBool(steer)
		if err != nil {
			return nil, fmt.Errorf("steer: %w", err)
		}
		out["steer"] = strconv.FormatBool(value)
	}
	return out, nil
}

func (cancelTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	out := optionalChatIDArg(args)
	if hard := strings.TrimSpace(tools.FirstArg(args, "hard")); hard != "" {
		value, err := strconv.ParseBool(hard)
		if err != nil {
			return nil, fmt.Errorf("hard: %w", err)
		}
		out["hard"] = strconv.FormatBool(value)
	}
	return out, nil
}

func (archiveTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	out := optionalChatIDArg(args)
	archived := strings.TrimSpace(tools.FirstArg(args, "archived"))
	if archived == "" {
		return nil, errors.New("archived is required")
	}
	value, err := strconv.ParseBool(archived)
	if err != nil {
		return nil, fmt.Errorf("archived: %w", err)
	}
	out["archived"] = strconv.FormatBool(value)
	return out, nil
}

func (renameTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	out := optionalChatIDArg(args)
	title := strings.TrimSpace(tools.FirstArg(args, "title"))
	if title == "" {
		return nil, errors.New("title is required")
	}
	out["title"] = title
	return out, nil
}

func requiredChatID(args map[string]string) string {
	return strings.TrimPrefix(strings.TrimSpace(tools.FirstArg(args, "chat_id", "id")), "#")
}

func optionalChatIDArg(args map[string]string) map[string]string {
	out := map[string]string{}
	if chatID := requiredChatID(args); chatID != "" {
		out["chat_id"] = chatID
	}
	return out
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

func (listTool) Preview(tools.Request) string      { return "List chats" }
func (startTool) Preview(req tools.Request) string { return "Start " + req.Args["profile"] + " chat" }
func (sendTool) Preview(req tools.Request) string  { return "Message chat " + req.Args["chat_id"] }
func (cancelTool) Preview(req tools.Request) string {
	return targetPreview("Cancel", req.Args["chat_id"])
}
func (archiveTool) Preview(req tools.Request) string {
	if req.Args["archived"] == "false" {
		return targetPreview("Restore", req.Args["chat_id"])
	}
	return targetPreview("Archive", req.Args["chat_id"])
}
func (renameTool) Preview(req tools.Request) string {
	return targetPreview("Rename", req.Args["chat_id"])
}

func targetPreview(action, chatID string) string {
	if strings.TrimSpace(chatID) == "" {
		return action + " current chat"
	}
	return action + " chat " + chatID
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
	stored := tools.ChatListStored(statuses)
	return tools.Result{
		Output: tools.DisplayTextForStored(domain.ToolKindChatList, stored),
		Stored: stored,
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
		Output: childReportGuidance(tools.DisplayTextForStored(req.Tool, stored)),
		Stored: stored,
	}, nil
}

func childReportGuidance(output string) string {
	return strings.TrimSpace(output + "\nThe child chat will report back automatically when it becomes idle, including task or milestone progress. Do not poll it.")
}

func (sendTool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	status, err := updateChat(ctx, runtime, req, tools.ChatUpdateRequest{
		Message: req.Args["message"],
		Steer:   req.Args["steer"] == "true",
	})
	if err != nil {
		return tools.Result{}, err
	}
	return chatResult(req.Tool, status)
}

func (cancelTool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	status, err := updateChat(ctx, runtime, req, tools.ChatUpdateRequest{
		Interrupt: true,
		Hard:      req.Args["hard"] == "true",
	})
	if err != nil {
		return tools.Result{}, err
	}
	return chatResult(req.Tool, status)
}

func (archiveTool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	archived := req.Args["archived"] == "true"
	status, err := updateChat(ctx, runtime, req, tools.ChatUpdateRequest{Archived: &archived})
	if err != nil {
		return tools.Result{}, err
	}
	return chatResult(req.Tool, status)
}

func (renameTool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	status, err := updateChat(ctx, runtime, req, tools.ChatUpdateRequest{Title: req.Args["title"]})
	if err != nil {
		return tools.Result{}, err
	}
	return chatResult(req.Tool, status)
}

func updateChat(ctx context.Context, runtime tools.Runtime, req tools.Request, update tools.ChatUpdateRequest) (tools.ChatStatus, error) {
	control, err := tools.RequireChatControl(runtime)
	if err != nil {
		return tools.ChatStatus{}, err
	}
	chatID := id.ID(strings.TrimSpace(req.Args["chat_id"]))
	if chatID == "" {
		chatID = runtime.ChatID
	}
	return control.UpdateChat(ctx, runtime.SessionID, runtime.ChatID, chatID, update)
}

func chatResult(tool domain.ToolKind, status tools.ChatStatus) (tools.Result, error) {
	stored := tools.ChatListStored([]tools.ChatStatus{status})
	return tools.Result{
		Output: tools.DisplayTextForStored(tool, stored),
		Stored: stored,
	}, nil
}

func (listTool) SummarizeResult(_ tools.Request, result tools.Result) (string, string) {
	return "Listed chats", result.Output
}

func (startTool) SummarizeResult(_ tools.Request, result tools.Result) (string, string) {
	return "Started chat", result.Output
}

func (sendTool) SummarizeResult(_ tools.Request, result tools.Result) (string, string) {
	return "Sent chat message", result.Output
}

func (cancelTool) SummarizeResult(_ tools.Request, result tools.Result) (string, string) {
	return "Cancelled chat", result.Output
}

func (archiveTool) SummarizeResult(_ tools.Request, result tools.Result) (string, string) {
	return "Archived chat", result.Output
}

func (renameTool) SummarizeResult(_ tools.Request, result tools.Result) (string, string) {
	return "Renamed chat", result.Output
}
