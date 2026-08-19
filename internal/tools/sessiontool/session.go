package sessiontool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/tools"
	"github.com/lkarlslund/koder/internal/voice"
)

const serviceKey = "voice_sessions"

// Control is the process-wide session capability exposed only to voice chats.
type Control interface {
	ListVoiceSessions(context.Context) ([]voice.Session, error)
	DelegateVoice(context.Context, string, string) (voice.DelegationResult, error)
	CreateVoiceTarget(context.Context, string, bool) (voice.Session, error)
}

func RuntimeService(control Control) map[string]any {
	if control == nil {
		return nil
	}
	return map[string]any{serviceKey: control}
}

func init() {
	tools.Register(listTool{}, tools.ToolSpec{
		Title: "List work sessions", Description: "List ordinary work sessions available to this voice chat.",
		Usage:      "Use before choosing where prior work lives. Results contain bounded titles and recent summaries, not full transcripts.",
		Parameters: `{"type":"object","properties":{},"additionalProperties":false}`, ExposeToLLM: true,
	})
	tools.Register(delegateTool{}, tools.ToolSpec{
		Title: "Ask work session", Description: "Send a request to an existing work session and wait for its answer.",
		Usage:      "Use the exact session_id returned by session_list. The target chat retains its own history, tools, permissions, and approvals.",
		Parameters: `{"type":"object","properties":{"session_id":{"type":"string"},"message":{"type":"string"}},"required":["session_id","message"],"additionalProperties":false}`, ExposeToLLM: true,
	})
	tools.Register(startTool{}, tools.ToolSpec{
		Title: "Start work session", Description: "Create a temporary or persistent work session.",
		Usage:      "Use temporary=true for self-contained one-off work. Use temporary=false only for ongoing work the user should retain. Then use session_delegate to perform the work.",
		Parameters: `{"type":"object","properties":{"title":{"type":"string"},"temporary":{"type":"boolean"}},"required":["title","temporary"],"additionalProperties":false}`, ExposeToLLM: true,
	})
}

type listTool struct{}
type delegateTool struct{}
type startTool struct{}

func (listTool) ID() tools.ID     { return tools.SessionList }
func (delegateTool) ID() tools.ID { return tools.SessionDelegate }
func (startTool) ID() tools.ID    { return tools.SessionStart }

func (listTool) BypassesPermission() bool     { return true }
func (delegateTool) BypassesPermission() bool { return true }
func (startTool) BypassesPermission() bool    { return true }

func (listTool) Definition(runtime tools.Runtime, spec tools.ToolSpec) (tools.ToolSpec, bool) {
	return voiceDefinition(runtime, spec)
}
func (delegateTool) Definition(runtime tools.Runtime, spec tools.ToolSpec) (tools.ToolSpec, bool) {
	return voiceDefinition(runtime, spec)
}
func (startTool) Definition(runtime tools.Runtime, spec tools.ToolSpec) (tools.ToolSpec, bool) {
	return voiceDefinition(runtime, spec)
}
func voiceDefinition(runtime tools.Runtime, spec tools.ToolSpec) (tools.ToolSpec, bool) {
	if runtime.ChatRole != chatrole.Voice {
		return tools.ToolSpec{}, false
	}
	if _, err := control(runtime); err != nil {
		return tools.ToolSpec{}, false
	}
	return spec, true
}

func (listTool) NormalizeArgs(map[string]string) (map[string]string, error) {
	return map[string]string{}, nil
}
func (delegateTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	sessionID := strings.TrimSpace(args["session_id"])
	message := strings.TrimSpace(args["message"])
	if sessionID == "" || message == "" {
		return nil, errors.New("session_id and message are required")
	}
	return map[string]string{"session_id": sessionID, "message": message}, nil
}
func (startTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	title := strings.TrimSpace(args["title"])
	if title == "" {
		return nil, errors.New("title is required")
	}
	temporary, err := strconv.ParseBool(strings.TrimSpace(args["temporary"]))
	if err != nil {
		return nil, fmt.Errorf("temporary: %w", err)
	}
	return map[string]string{"title": title, "temporary": strconv.FormatBool(temporary)}, nil
}

func (listTool) Preview(tools.Request) string { return "List work sessions" }
func (delegateTool) Preview(req tools.Request) string {
	return "Ask work session " + req.Args["session_id"]
}
func (startTool) Preview(req tools.Request) string { return "Start work session " + req.Args["title"] }

func (listTool) Call(ctx context.Context, opts tools.Options) (tools.Result, error) {
	service, err := control(opts.Runtime)
	if err != nil {
		return tools.Result{}, err
	}
	sessions, err := service.ListVoiceSessions(ctx)
	if err != nil {
		return tools.Result{}, err
	}
	slices.SortStableFunc(sessions, func(a, b voice.Session) int {
		return b.UpdatedAt.Compare(a.UpdatedAt)
	})
	sessions = sessions[:min(len(sessions), 25)]
	for index := range sessions {
		sessions[index].Title = boundedText(sessions[index].Title, 100)
		sessions[index].LastMessage = boundedText(sessions[index].LastMessage, 240)
	}
	return jsonResult(sessions)
}

func (delegateTool) Call(ctx context.Context, opts tools.Options) (tools.Result, error) {
	service, err := control(opts.Runtime)
	if err != nil {
		return tools.Result{}, err
	}
	result, err := service.DelegateVoice(ctx, opts.Request.Args["session_id"], opts.Request.Args["message"])
	if err != nil {
		return tools.Result{}, err
	}
	return jsonResult(result)
}

func (startTool) Call(ctx context.Context, opts tools.Options) (tools.Result, error) {
	service, err := control(opts.Runtime)
	if err != nil {
		return tools.Result{}, err
	}
	temporary, _ := strconv.ParseBool(opts.Request.Args["temporary"])
	session, err := service.CreateVoiceTarget(ctx, opts.Request.Args["title"], !temporary)
	if err != nil {
		return tools.Result{}, err
	}
	return jsonResult(session)
}

func control(runtime tools.Runtime) (Control, error) {
	return tools.RequireService[Control](runtime, serviceKey)
}

func jsonResult(value any) (tools.Result, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return tools.Result{}, err
	}
	return tools.Result{Output: string(data), Stored: value}, nil
}

func boundedText(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return strings.TrimSpace(string(runes[:maxRunes])) + "…"
}
