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
	tools.Register(tools.ActionTool{
		Kind: tools.Chats,
		Routes: []tools.ActionRoute{
			{Action: "list", Tool: tools.ChatList},
			{Action: "start", Tool: tools.ChatStart},
			{Action: "send", Tool: tools.ChatSend},
			{Action: "cancel", Tool: tools.ChatCancel},
			{Action: "archive", Tool: tools.ChatArchive, FixedArgs: map[string]string{"archived": "true"}},
			{Action: "restore", Tool: tools.ChatArchive, FixedArgs: map[string]string{"archived": "false"}},
			{Action: "rename", Tool: tools.ChatRename},
			{Action: "cleanup", Tool: tools.ChatCleanup},
		},
		BypassPermissions: true,
	}, tools.ToolSpec{
		Title:       "Chats",
		Description: "List, create, coordinate, rename, archive, restore, and cancel chats in this session.",
		Usage:       "Use list to discover chats. Use start for a new child, send to give a direct child work, and cancel to stop work. Archive only idle direct children; restore makes an archived child visible again. Cleanup archives eligible idle execution children. Child chats report back automatically, so do not poll them.",
		Parameters:  `{"type":"object","properties":{"action":{"type":"string"},"archived":{"type":"boolean","description":"For list, include archived chats"},"profile":{"type":"string","description":"Registered chat profile"},"objective":{"type":"string"},"title":{"type":"string"},"backend":{"type":"string","enum":["koder","codex"]},"interaction_mode":{"type":"string","enum":["text","voice"]},"model_id":{"type":"string"},"permission_profile":{"type":"string"},"milestone_key":{"type":"string"},"task_ref":{"type":"string"},"disabled_tools":{"type":"string"},"chat_id":{"type":"string"},"message":{"type":"string"},"steer":{"type":"boolean"},"wait":{"type":"boolean"},"hard":{"type":"boolean"}},"required":["action"],"additionalProperties":false}`,
		ExposeToLLM: true,
	})
	tools.Register(listTool{}, tools.ToolSpec{
		Title:       "List chats",
		Description: "List chats in the current session.",
		Usage:       "List chats in the current session, including worker chats started for execution. Archived chats are hidden by default; pass archived=true when you need to inspect or restore hidden chats.",
		Parameters:  `{"type":"object","properties":{"archived":{"type":"boolean","description":"Include archived chats. Defaults to false."}},"additionalProperties":false}`,
		ExposeToLLM: true, Legacy: true,
	})
	tools.Register(startTool{}, tools.ToolSpec{
		Title:       "Start chat",
		Description: "Start a background child chat using a registered chat profile.",
		Usage:       "Start a background child chat using a registered chat profile and either the Koder or Codex backend. Omit backend to use the current chat's backend. A child may be scoped to one milestone or one task. If an existing child owns that scope, use chat_send instead. After starting a child, go idle unless you have unrelated work; it reports back automatically. Do not poll child chats.",
		Parameters:  `{"type":"object","properties":{"profile":{"type":"string","description":"Registered chat profile such as orchestrator, planning, or execution"},"objective":{"type":"string","description":"Specific objective for the child chat"},"title":{"type":"string","description":"Optional chat title"},"backend":{"type":"string","enum":["koder","codex"],"description":"Optional turn backend; defaults to the current chat's backend"},"interaction_mode":{"type":"string","enum":["text","voice"],"description":"Optional interaction mode; defaults to text"},"model_id":{"type":"string","description":"Optional backend model id"},"permission_profile":{"type":"string","description":"Optional Koder permission profile"},"milestone_key":{"type":"string","description":"Optional milestone scope; mutually exclusive with task_ref"},"task_ref":{"type":"string","description":"Optional single task scope; mutually exclusive with milestone_key"},"disabled_tools":{"type":"string","description":"Optional comma-separated Koder tool ids to disable for this chat"}},"required":["profile","objective"],"additionalProperties":false}`,
		ExposeToLLM: true, Legacy: true,
	})
	tools.Register(sendTool{}, tools.ToolSpec{
		Title:       "Send chat message",
		Description: "Send a message to a direct child chat.",
		Usage:       "Send work instructions to a chat you may coordinate. Do not message the current chat with this tool. Pass steer=true when the message should be delivered at a turn boundary to a busy chat; otherwise it is queued as the next user turn. Pass wait=true when the current response needs the target chat's sealed answer; voice chats wait by default.",
		Parameters:  `{"type":"object","properties":{"chat_id":{"type":"string","description":"Chat UUID to message"},"message":{"type":"string","description":"Message to queue for the chat"},"steer":{"type":"boolean","description":"Deliver as a turn-boundary steer instead of the next user turn"},"wait":{"type":"boolean","description":"Wait for and return the target chat's next sealed answer"}},"required":["chat_id","message"],"additionalProperties":false}`,
		ExposeToLLM: true, Legacy: true,
	})
	tools.Register(cancelTool{}, tools.ToolSpec{
		Title:       "Cancel chat",
		Description: "Ask the current chat or a direct child chat to stop.",
		Usage:       "Ask the current chat or a direct child chat you own to stop. Omit chat_id to cancel the current chat. Pass hard=true only when it must cancel active streaming or tools immediately; otherwise it stops after the current turn.",
		Parameters:  `{"type":"object","properties":{"chat_id":{"type":"string","description":"Chat UUID to cancel; defaults to the current chat when omitted"},"hard":{"type":"boolean","description":"Immediately cancel active streaming or tools instead of stopping after the current turn"}},"additionalProperties":false}`,
		ExposeToLLM: true, Legacy: true,
	})
	tools.Register(archiveTool{}, tools.ToolSpec{
		Title:       "Archive chat",
		Description: "Archive or restore a chat.",
		Usage:       "Set archived=true for a completed or no-longer-needed direct child chat, archived=false to restore an archived direct child chat. chat_id is required; this tool cannot target the current chat. Only idle chats can be archived. If you need to find archived chats first, call chat_list with archived=true.",
		Parameters:  `{"type":"object","properties":{"chat_id":{"type":"string","description":"Direct child chat UUID to archive or restore"},"archived":{"type":"boolean","description":"true hides the chat; false restores it"}},"required":["chat_id","archived"],"additionalProperties":false}`,
		ExposeToLLM: true, Legacy: true,
	})
	tools.Register(cleanupTool{}, tools.ToolSpec{
		Title:       "Cleanup chats",
		Description: "Archive idle execution child chats.",
		Usage:       "Archive idle direct child chats owned by the current chat. This only archives execution chats that are idle, have no queued inputs, and have no pending approvals. It skips the current chat, non-child chats, non-execution chats, running chats, waiting-approval chats, queued chats, and already archived chats.",
		Parameters:  `{"type":"object","properties":{},"additionalProperties":false}`,
		ExposeToLLM: true, Legacy: true,
	})
	tools.Register(renameTool{}, tools.ToolSpec{
		Title:       "Rename chat",
		Description: "Rename a chat.",
		Usage:       "Rename the current chat or a direct child chat you own. Omit chat_id to target the current chat.",
		Parameters:  `{"type":"object","properties":{"chat_id":{"type":"string","description":"Chat UUID to rename; defaults to the current chat when omitted"},"title":{"type":"string","description":"Replacement title"}},"required":["title"],"additionalProperties":false}`,
		ExposeToLLM: true, Legacy: true,
	})
}

type listTool struct{}
type startTool struct{}
type sendTool struct{}
type cancelTool struct{}
type archiveTool struct{}
type cleanupTool struct{}
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
	ParentChatID       id.ID
	Title              string
	Role               chatrole.Role
	Backend            domain.ChatBackend
	InteractionMode    domain.InteractionMode
	ModelID            string
	PermissionProfile  string
	Archived           bool
	ActiveMilestoneKey string
	AssignedTaskRef    string
	State              RunState
	Status             string
	Busy               bool
	QueuedInputs       int
	PendingApprovals   int
	LastError          string
	StatusText         string
	Response           string
}

type StartRequest struct {
	Profile           chatrole.Role
	Objective         string
	Title             string
	Backend           domain.ChatBackend
	InteractionMode   domain.InteractionMode
	ModelID           string
	PermissionProfile string
	MilestoneKey      string
	TaskRef           string
	ToolStates        domain.ToolStates
}

type UpdateRequest struct {
	Archived  *bool
	Title     string
	Message   string
	Steer     bool
	Wait      bool
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
			Backend:            string(status.Backend),
			InteractionMode:    string(status.InteractionMode),
			ModelID:            status.ModelID,
			PermissionProfile:  status.PermissionProfile,
			State:              string(status.State),
			Archived:           status.Archived,
			QueuedInputs:       status.QueuedInputs,
			ActiveMilestoneKey: status.ActiveMilestoneKey,
			AssignedTaskRef:    status.AssignedTaskRef,
			StatusText:         status.StatusText,
			Response:           status.Response,
		})
	}
	return tools.ChatListStoredResult{Items: items}
}

func (listTool) ID() tools.ID    { return tools.ChatList }
func (startTool) ID() tools.ID   { return tools.ChatStart }
func (sendTool) ID() tools.ID    { return tools.ChatSend }
func (cancelTool) ID() tools.ID  { return tools.ChatCancel }
func (archiveTool) ID() tools.ID { return tools.ChatArchive }
func (cleanupTool) ID() tools.ID { return tools.ChatCleanup }
func (renameTool) ID() tools.ID  { return tools.ChatRename }

func (listTool) BypassesPermission() bool    { return true }
func (startTool) BypassesPermission() bool   { return true }
func (sendTool) BypassesPermission() bool    { return true }
func (cancelTool) BypassesPermission() bool  { return true }
func (archiveTool) BypassesPermission() bool { return true }
func (cleanupTool) BypassesPermission() bool { return true }
func (renameTool) BypassesPermission() bool  { return true }

func (startTool) Definition(runtime tools.Runtime, spec tools.ToolSpec) (tools.ToolSpec, bool) {
	switch runtime.ChatRole {
	case "", chatrole.General, chatrole.Orchestrator, chatrole.Planning, chatrole.Voice:
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
	if ref := strings.TrimSpace(args["task_ref"]); ref != "" {
		if out["milestone_key"] != "" {
			return nil, errors.New("milestone_key and task_ref are mutually exclusive")
		}
		out["task_ref"] = ref
	}
	if backend := domain.ChatBackend(strings.TrimSpace(args["backend"])); backend != "" {
		if backend != domain.ChatBackendKoder && backend != domain.ChatBackendCodex {
			return nil, fmt.Errorf("unsupported backend %q", backend)
		}
		out["backend"] = string(backend)
	}
	if mode := domain.InteractionMode(strings.TrimSpace(args["interaction_mode"])); mode != "" {
		if mode != domain.InteractionModeText && mode != domain.InteractionModeVoice {
			return nil, fmt.Errorf("unsupported interaction mode %q", mode)
		}
		out["interaction_mode"] = string(mode)
	}
	for _, key := range []string{"model_id", "permission_profile"} {
		if value := strings.TrimSpace(args[key]); value != "" {
			out[key] = value
		}
	}
	if disabled := strings.TrimSpace(args["disabled_tools"]); disabled != "" {
		for _, raw := range strings.Split(disabled, ",") {
			kind := domain.ToolKind(strings.TrimSpace(raw))
			if kind == "" || !domain.IsBuiltinToolKind(kind) {
				return nil, fmt.Errorf("unknown disabled tool %q", strings.TrimSpace(raw))
			}
		}
		out["disabled_tools"] = disabled
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
	if wait := strings.TrimSpace(args["wait"]); wait != "" {
		value, err := strconv.ParseBool(wait)
		if err != nil {
			return nil, fmt.Errorf("wait: %w", err)
		}
		out["wait"] = strconv.FormatBool(value)
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
	chatID := requiredChatID(args)
	if chatID == "" {
		return nil, errors.New("chat_id is required")
	}
	out := map[string]string{"chat_id": chatID}
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

func (cleanupTool) NormalizeArgs(map[string]string) (map[string]string, error) {
	return map[string]string{}, nil
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
func (cleanupTool) Preview(tools.Request) string { return "Archive idle execution child chats" }
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
		Profile:           chatrole.Role(req.Args["profile"]),
		Objective:         req.Args["objective"],
		Title:             req.Args["title"],
		Backend:           domain.ChatBackend(req.Args["backend"]),
		InteractionMode:   domain.InteractionMode(req.Args["interaction_mode"]),
		ModelID:           req.Args["model_id"],
		PermissionProfile: req.Args["permission_profile"],
		MilestoneKey:      req.Args["milestone_key"],
		TaskRef:           req.Args["task_ref"],
		ToolStates:        disabledToolStates(req.Args["disabled_tools"]),
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

func disabledToolStates(value string) domain.ToolStates {
	states := domain.ToolStates{}
	for _, raw := range strings.Split(value, ",") {
		if kind := domain.ToolKind(strings.TrimSpace(raw)); kind != "" && domain.IsBuiltinToolKind(kind) {
			states[kind] = false
		}
	}
	return states
}

func childReportGuidance(output string) string {
	return strings.TrimSpace(output + "\nThe child chat will report back automatically when it becomes idle, including task or milestone progress. Do not poll it.")
}

func (sendTool) Call(ctx context.Context, opts tools.Options) (tools.Result, error) {
	runtime, req := opts.Runtime, opts.Request
	status, err := updateChat(ctx, runtime, req, UpdateRequest{
		Message: req.Args["message"],
		Steer:   req.Args["steer"] == "true",
		Wait:    req.Args["wait"] == "true" || runtime.VoiceInteraction(),
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
	chatID := id.ID(strings.TrimSpace(req.Args["chat_id"]))
	if chatID == "" {
		return tools.Result{}, errors.New("chat_id is required")
	}
	if chatID == runtime.ChatID {
		return tools.Result{}, fmt.Errorf("chat_archive cannot target the current chat %s; target a direct child chat instead", runtime.ChatID)
	}
	archived := req.Args["archived"] == "true"
	status, err := updateChat(ctx, runtime, req, UpdateRequest{Archived: &archived})
	if err != nil {
		return tools.Result{}, err
	}
	return chatResult(req.Tool, status)
}

func (cleanupTool) Call(ctx context.Context, opts tools.Options) (tools.Result, error) {
	runtime := opts.Runtime
	control, err := requireControl(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	statuses, err := control.ListChats(ctx, runtime.SessionID)
	if err != nil {
		return tools.Result{}, err
	}
	archived := true
	archivedStatuses := make([]Status, 0)
	skipped := make([]string, 0)
	for _, status := range statuses {
		if skip := cleanupSkipReason(runtime.ChatID, status); skip != "" {
			skipped = append(skipped, fmt.Sprintf("#%s %s: %s", status.ID, strings.TrimSpace(status.Title), skip))
			continue
		}
		updated, err := control.UpdateChat(ctx, runtime.SessionID, runtime.ChatID, status.ID, UpdateRequest{Archived: &archived})
		if err != nil {
			skipped = append(skipped, fmt.Sprintf("#%s %s: %s", status.ID, strings.TrimSpace(status.Title), err.Error()))
			continue
		}
		archivedStatuses = append(archivedStatuses, updated)
	}
	return tools.Result{
		Output: cleanupOutput(archivedStatuses, skipped),
		Stored: storedResult(archivedStatuses),
	}, nil
}

func cleanupSkipReason(currentChatID id.ID, status Status) string {
	switch {
	case status.ID == "":
		return "missing chat id"
	case status.ID == currentChatID:
		return "current chat"
	case status.Archived:
		return "already archived"
	case status.ParentChatID != currentChatID:
		return "not a direct child"
	case status.Role != chatrole.Execution:
		return "not an execution chat"
	case status.State != RunStateIdle || status.Busy:
		return "not idle"
	case status.QueuedInputs > 0:
		return fmt.Sprintf("%d queued input(s)", status.QueuedInputs)
	case status.PendingApprovals > 0:
		return fmt.Sprintf("%d pending approval(s)", status.PendingApprovals)
	default:
		return ""
	}
}

func cleanupOutput(archived []Status, skipped []string) string {
	lines := []string{fmt.Sprintf("Archived %d idle execution child chat(s).", len(archived))}
	for _, status := range archived {
		lines = append(lines, fmt.Sprintf("archived: #%s %s", status.ID, strings.TrimSpace(status.Title)))
	}
	if len(skipped) > 0 {
		lines = append(lines, fmt.Sprintf("Skipped %d chat(s):", len(skipped)))
		lines = append(lines, skipped...)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
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
	output := tools.DisplayTextForStored(tool, stored)
	return tools.Result{
		Output: output,
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

func (cleanupTool) SummarizeResult(_ tools.Request, result tools.Result) (string, string) {
	return "Cleaned up chats", result.Output
}

func (renameTool) SummarizeResult(_ tools.Request, result tools.Result) (string, string) {
	return "Renamed chat", result.Output
}
