package chattool

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/planning"
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
		Usage:       "Start a background child chat using a registered chat profile. Use milestone_key or task_key to scope what the child chat can see. A task_key scopes the child to that single task. After starting it, go idle unless you have unrelated work; the child chat will automatically report back when it becomes idle, including task or milestone progress. Do not poll child chats.",
		Parameters:  `{"type":"object","properties":{"profile":{"type":"string","description":"Registered chat profile to use, such as execution or planning"},"objective":{"type":"string","description":"Specific objective for the child chat"},"title":{"type":"string","description":"Optional chat title"},"milestone_key":{"type":"string","description":"Optional milestone key to scope the child chat"},"task_key":{"type":"string","description":"Optional task key to scope the child chat to one task"}},"required":["profile","objective"],"additionalProperties":false}`,
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

const serviceKey = "chat"

type RunState string

const (
	RunStateIdle            RunState = "idle"
	RunStateRunning         RunState = "running"
	RunStateWaitingApproval RunState = "waiting_approval"
	RunStateCompleted       RunState = "completed"
	RunStateFailed          RunState = "failed"
	RunStateCancelled       RunState = "cancelled"
)

type Status struct {
	ID                 id.ID
	Title              string
	Role               chatrole.Role
	Archived           bool
	ActiveMilestoneRef string
	AssignedTaskRef    string
	State              RunState
	Status             string
	Busy               bool
	QueuedInputs       int
	PendingApprovals   int
	LastError          string
	StatusText         string
}

type StartRequest struct {
	Profile      chatrole.Role
	Objective    string
	Title        string
	MilestoneRef string
	TaskRef      string
}

type UpdateRequest struct {
	Archived  *bool
	Title     string
	Message   string
	Steer     bool
	Interrupt bool
	Hard      bool
}

type Control interface {
	ListChats(context.Context, id.ID) ([]Status, error)
	StartChat(context.Context, id.ID, id.ID, StartRequest) (Status, error)
	UpdateChat(context.Context, id.ID, id.ID, id.ID, UpdateRequest) (Status, error)
}

func RuntimeService(control Control) map[string]any {
	return map[string]any{serviceKey: control}
}

func requireControl(runtime tools.Runtime) (Control, error) {
	if runtime.SessionID == "" || runtime.ChatID == "" {
		return nil, errors.New("chat orchestration requires an active persisted chat")
	}
	return tools.RequireService[Control](runtime, serviceKey)
}

func storedResult(statuses []Status) tools.ChatListStoredResult {
	items := make([]tools.ChatStoredItem, 0, len(statuses))
	for _, status := range statuses {
		items = append(items, tools.ChatStoredItem{
			ID:                 status.ID,
			Title:              status.Title,
			Role:               string(status.Role),
			State:              string(status.State),
			Archived:           status.Archived,
			QueuedInputs:       status.QueuedInputs,
			ActiveMilestoneRef: status.ActiveMilestoneRef,
			AssignedTaskRef:    status.AssignedTaskRef,
			StatusText:         status.StatusText,
		})
	}
	return tools.ChatListStoredResult{Items: items}
}

func (listTool) ID() tools.ID    { return tools.ChatList }
func (startTool) ID() tools.ID   { return tools.ChatStart }
func (sendTool) ID() tools.ID    { return tools.ChatSend }
func (cancelTool) ID() tools.ID  { return tools.ChatCancel }
func (archiveTool) ID() tools.ID { return tools.ChatArchive }
func (renameTool) ID() tools.ID  { return tools.ChatRename }

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
	profile := strings.TrimSpace(args["profile"])
	if profile == "" {
		return nil, errors.New("profile is required")
	}
	if _, ok := chatrole.DefaultRegistry().Lookup(chatrole.Role(profile)); !ok {
		return nil, errors.New("profile is not registered")
	}
	objective := strings.TrimSpace(args["objective"])
	if objective == "" {
		return nil, errors.New("objective is required")
	}
	out := map[string]string{
		"profile":   profile,
		"objective": objective,
	}
	if title := strings.TrimSpace(args["title"]); title != "" {
		out["title"] = title
	}
	if ref := strings.TrimSpace(args["milestone_key"]); ref != "" {
		out["milestone_key"] = ref
	}
	if taskRef := strings.TrimSpace(args["task_key"]); taskRef != "" {
		key, err := planning.ParseTaskKey(taskRef)
		if err != nil {
			return nil, err
		}
		out["task_key"] = key
	}
	return out, nil
}

func (sendTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	chatID := requiredChatID(args)
	if chatID == "" {
		return nil, errors.New("chat_id is required")
	}
	message := strings.TrimSpace(args["message"])
	if message == "" {
		return nil, errors.New("message is required")
	}
	out := map[string]string{"chat_id": chatID, "message": message}
	if steer := strings.TrimSpace(args["steer"]); steer != "" {
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
	if hard := strings.TrimSpace(args["hard"]); hard != "" {
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
	archived := strings.TrimSpace(args["archived"])
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
	title := strings.TrimSpace(args["title"])
	if title == "" {
		return nil, errors.New("title is required")
	}
	out["title"] = title
	return out, nil
}

func requiredChatID(args map[string]string) string {
	return strings.TrimPrefix(strings.TrimSpace(args["chat_id"]), "#")
}

func optionalChatIDArg(args map[string]string) map[string]string {
	out := map[string]string{}
	if chatID := requiredChatID(args); chatID != "" {
		out["chat_id"] = chatID
	}
	return out
}

func normalizeOptionalBool(args map[string]string, key string) (map[string]string, error) {
	raw := strings.TrimSpace(args[key])
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
func (sendTool) Preview(req tools.Request) string {
	message := strings.TrimSpace(req.Args["message"])
	if message == "" {
		return "Message chat " + req.Args["chat_id"]
	}
	return "Message chat " + req.Args["chat_id"] + ": " + message
}
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

func (listTool) Call(ctx context.Context, opts tools.Options) (tools.Result, error) {
	runtime, req := opts.Runtime, opts.Request
	control, err := requireControl(runtime)
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
	stored := storedResult(statuses)
	return tools.Result{
		Output: tools.DisplayTextForStored(tools.ChatList, stored),
		Stored: stored,
	}, nil
}

func filterArchivedChats(statuses []Status) []Status {
	out := make([]Status, 0, len(statuses))
	for _, status := range statuses {
		if !status.Archived {
			out = append(out, status)
		}
	}
	return out
}

func (startTool) Call(ctx context.Context, opts tools.Options) (tools.Result, error) {
	runtime, req := opts.Runtime, opts.Request
	control, err := requireControl(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	status, err := control.StartChat(ctx, runtime.SessionID, runtime.ChatID, StartRequest{
		Profile:      chatrole.Role(req.Args["profile"]),
		Objective:    req.Args["objective"],
		Title:        req.Args["title"],
		MilestoneRef: req.Args["milestone_key"],
		TaskRef:      req.Args["task_key"],
	})
	if err != nil {
		return tools.Result{}, err
	}
	stored := storedResult([]Status{status})
	return tools.Result{
		Output: childReportGuidance(tools.DisplayTextForStored(req.Tool, stored)),
		Stored: stored,
	}, nil
}

func childReportGuidance(output string) string {
	return strings.TrimSpace(output + "\nThe child chat will report back automatically when it becomes idle, including task or milestone progress. Do not poll it.")
}

func (sendTool) Call(ctx context.Context, opts tools.Options) (tools.Result, error) {
	runtime, req := opts.Runtime, opts.Request
	status, err := updateChat(ctx, runtime, req, UpdateRequest{
		Message: req.Args["message"],
		Steer:   req.Args["steer"] == "true",
	})
	if err != nil {
		return tools.Result{}, err
	}
	return chatResult(req.Tool, status)
}

func (cancelTool) Call(ctx context.Context, opts tools.Options) (tools.Result, error) {
	runtime, req := opts.Runtime, opts.Request
	status, err := updateChat(ctx, runtime, req, UpdateRequest{
		Interrupt: true,
		Hard:      req.Args["hard"] == "true",
	})
	if err != nil {
		return tools.Result{}, err
	}
	return chatResult(req.Tool, status)
}

func (archiveTool) Call(ctx context.Context, opts tools.Options) (tools.Result, error) {
	runtime, req := opts.Runtime, opts.Request
	archived := req.Args["archived"] == "true"
	status, err := updateChat(ctx, runtime, req, UpdateRequest{Archived: &archived})
	if err != nil {
		return tools.Result{}, err
	}
	return chatResult(req.Tool, status)
}

func (renameTool) Call(ctx context.Context, opts tools.Options) (tools.Result, error) {
	runtime, req := opts.Runtime, opts.Request
	status, err := updateChat(ctx, runtime, req, UpdateRequest{Title: req.Args["title"]})
	if err != nil {
		return tools.Result{}, err
	}
	return chatResult(req.Tool, status)
}

func updateChat(ctx context.Context, runtime tools.Runtime, req tools.Request, update UpdateRequest) (Status, error) {
	control, err := requireControl(runtime)
	if err != nil {
		return Status{}, err
	}
	chatID := id.ID(strings.TrimSpace(req.Args["chat_id"]))
	if chatID == "" {
		chatID = runtime.ChatID
	}
	return control.UpdateChat(ctx, runtime.SessionID, runtime.ChatID, chatID, update)
}

func chatResult(tool tools.ID, status Status) (tools.Result, error) {
	stored := storedResult([]Status{status})
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
