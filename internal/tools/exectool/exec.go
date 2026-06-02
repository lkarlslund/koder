package exectool

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/execruntime"
	"github.com/lkarlslund/koder/internal/tools"
)

const (
	defaultYieldTime = 250 * time.Millisecond
)

func init() {
	tools.Register(commandTool{}, tools.ToolSpec{
		Title:       "Start exec session",
		Description: "Start a persistent shell command session.",
		Usage:       "Start a persistent shell command session for long-running, interactive, or background work. Use this instead of bash when you may need to inspect output later, write stdin, resize a tty, or terminate the process in a later turn. Keep cmd executable-only: do not include reasoning, commentary, plans, status updates, or explanatory shell comments. Put explanations in normal assistant text.",
		Parameters:  `{"type":"object","properties":{"cmd":{"type":"string","description":"Exact executable shell command. Keep it small; do not include reasoning, commentary, plans, status updates, or explanatory comments."},"workdir":{"type":"string","description":"Optional workspace-relative working directory; use this instead of cd."},"timeout_ms":{"type":"integer","description":"Optional timeout in milliseconds; omit for no timeout"},"tty":{"type":"boolean","description":"Enable tty mode for interactive commands"},"shell":{"type":"string","description":"Optional shell binary name or path"},"login":{"type":"boolean","description":"Use login shell semantics; defaults to true"},"yield_time_ms":{"type":"integer","description":"Optional short wait before returning so initial output can be captured"}},"required":["cmd"],"additionalProperties":false}`,
		ExposeToLLM: true,
	})
	tools.Register(statusTool{}, tools.ToolSpec{
		Title:       "Exec status",
		Description: "Get state and recent output for a persistent exec session.",
		Usage:       "Inspect the latest state and accumulated output tail for one persistent exec session. This is a non-consuming status snapshot; do not repeatedly poll it for long-running commands. Use exec_write_stdin with empty chars and yield_time_ms to wait for new output.",
		Parameters:  `{"type":"object","properties":{"process_id":{"type":"string","description":"Process id returned by exec_command"},"max_output_bytes":{"type":"integer","description":"Optional output tail size to return"}},"required":["process_id"],"additionalProperties":false}`,
		ExposeToLLM: true,
	})
	tools.Register(listTool{}, tools.ToolSpec{
		Title:       "Exec sessions",
		Description: "List persistent exec sessions.",
		Usage:       "List persistent exec sessions in the current chat or session.",
		Parameters:  `{"type":"object","properties":{"scope":{"type":"string","description":"Listing scope. Omit for current chat.","enum":["chat","session"]},"max_output_bytes":{"type":"integer","description":"Optional output tail size for each item"}},"additionalProperties":false}`,
		ExposeToLLM: true,
	})
	tools.Register(writeStdinTool{}, tools.ToolSpec{
		Title:       "Write exec stdin",
		Description: "Write stdin text to, or wait for output from, a running persistent exec session.",
		Usage:       "Write stdin text to a persistent exec session. Pass empty chars to wait for new output or process completion without writing input. Prefer this over repeated exec_status polling for long-running commands; returned output is newly drained output since the previous consuming exec result.",
		Parameters:  `{"type":"object","properties":{"process_id":{"type":"string","description":"Process id returned by exec_command"},"chars":{"type":"string","description":"Text to write to stdin. Use an empty string to wait/poll for new output without writing input."},"close_stdin":{"type":"boolean","description":"Close stdin after writing"},"yield_time_ms":{"type":"integer","description":"Optional wait in milliseconds for new output before returning. Defaults to a short wait; empty chars may use longer waits."},"max_output_bytes":{"type":"integer","description":"Optional output size to return"}},"required":["process_id"],"additionalProperties":false}`,
		ExposeToLLM: true,
	})
	tools.Register(resizeTool{}, tools.ToolSpec{
		Title:       "Resize exec tty",
		Description: "Resize a tty-backed persistent exec session.",
		Usage:       "Resize a tty-backed persistent exec session.",
		Parameters:  `{"type":"object","properties":{"process_id":{"type":"string","description":"Process id returned by exec_command"},"rows":{"type":"integer","description":"Terminal rows"},"cols":{"type":"integer","description":"Terminal columns"},"max_output_bytes":{"type":"integer","description":"Optional output tail size to return"}},"required":["process_id","rows","cols"],"additionalProperties":false}`,
		ExposeToLLM: true,
	})
	tools.Register(terminateTool{}, tools.ToolSpec{
		Title:       "Terminate exec session",
		Description: "Terminate a persistent exec session.",
		Usage:       "Terminate a persistent exec session.",
		Parameters:  `{"type":"object","properties":{"process_id":{"type":"string","description":"Process id returned by exec_command"},"max_output_bytes":{"type":"integer","description":"Optional output tail size to return"}},"required":["process_id"],"additionalProperties":false}`,
		ExposeToLLM: true,
	})
	tools.Register(cleanupTool{}, tools.ToolSpec{
		Title:       "Cleanup exec sessions",
		Description: "Terminate persistent exec sessions in scope.",
		Usage:       "Terminate running persistent exec sessions in the current chat or session and report their final states.",
		Parameters:  `{"type":"object","properties":{"scope":{"type":"string","description":"Cleanup scope. Omit for current chat.","enum":["chat","session"]},"max_output_bytes":{"type":"integer","description":"Optional output tail size for each item"}},"additionalProperties":false}`,
		ExposeToLLM: true,
	})
}

type commandTool struct{}
type statusTool struct{}
type listTool struct{}
type writeStdinTool struct{}
type resizeTool struct{}
type terminateTool struct{}
type cleanupTool struct{}

func (commandTool) Kind() domain.ToolKind    { return domain.ToolKindExecCommand }
func (statusTool) Kind() domain.ToolKind     { return domain.ToolKindExecStatus }
func (listTool) Kind() domain.ToolKind       { return domain.ToolKindExecList }
func (writeStdinTool) Kind() domain.ToolKind { return domain.ToolKindExecWriteStdin }
func (resizeTool) Kind() domain.ToolKind     { return domain.ToolKindExecResize }
func (terminateTool) Kind() domain.ToolKind  { return domain.ToolKindExecTerminate }
func (cleanupTool) Kind() domain.ToolKind    { return domain.ToolKindExecCleanup }

func (commandTool) BypassesPermission() bool    { return false }
func (statusTool) BypassesPermission() bool     { return true }
func (listTool) BypassesPermission() bool       { return true }
func (writeStdinTool) BypassesPermission() bool { return true }
func (resizeTool) BypassesPermission() bool     { return true }
func (terminateTool) BypassesPermission() bool  { return true }
func (cleanupTool) BypassesPermission() bool    { return true }

func (commandTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	cmd := strings.TrimSpace(tools.FirstArg(args, "cmd", "command"))
	if cmd == "" {
		return nil, errors.New("cmd is empty")
	}
	for _, key := range []string{"cwd", "dir"} {
		if strings.TrimSpace(args[key]) != "" {
			return nil, fmt.Errorf("%s is no longer supported; use workdir", key)
		}
	}
	out := map[string]string{"cmd": cmd}
	if workdir := tools.NormalizePathInput(tools.FirstArg(args, "workdir")); workdir != "" {
		out["workdir"] = workdir
	}
	if timeout := strings.TrimSpace(tools.FirstArg(args, "timeout_ms")); timeout != "" {
		ms, err := tools.ParseFlexibleInt(timeout)
		if err != nil || ms < 0 {
			return nil, errors.New("timeout_ms must be a non-negative integer")
		}
		out["timeout_ms"] = strconv.Itoa(ms)
	}
	if tty := parseBoolArg(args, "tty"); tty != "" {
		out["tty"] = tty
	}
	if shell := strings.TrimSpace(tools.FirstArg(args, "shell")); shell != "" {
		out["shell"] = shell
	}
	if login := parseBoolArg(args, "login"); login != "" {
		out["login"] = login
	}
	if yield := strings.TrimSpace(tools.FirstArg(args, "yield_time_ms")); yield != "" {
		ms, err := tools.ParseFlexibleInt(yield)
		if err != nil || ms < 0 {
			return nil, errors.New("yield_time_ms must be a non-negative integer")
		}
		out["yield_time_ms"] = strconv.Itoa(ms)
	}
	return out, nil
}

func (statusTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	return normalizeProcessArgs(args, false)
}

func (listTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	out := map[string]string{}
	if scope := normalizeScope(args); scope != "" {
		out["scope"] = scope
	}
	if maxOutput := normalizeOptionalInt(args, "max_output_bytes"); maxOutput != "" {
		out["max_output_bytes"] = maxOutput
	}
	return out, nil
}

func (writeStdinTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	out, err := normalizeProcessArgs(args, false)
	if err != nil {
		return nil, err
	}
	if chars, ok := args["chars"]; ok {
		out["chars"] = chars
	}
	if yield := strings.TrimSpace(tools.FirstArg(args, "yield_time_ms")); yield != "" {
		ms, err := tools.ParseFlexibleInt(yield)
		if err != nil || ms < 0 {
			return nil, errors.New("yield_time_ms must be a non-negative integer")
		}
		out["yield_time_ms"] = strconv.Itoa(ms)
	}
	if closeStdin := parseBoolArg(args, "close_stdin"); closeStdin != "" {
		out["close_stdin"] = closeStdin
	}
	return out, nil
}

func (resizeTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	out, err := normalizeProcessArgs(args, false)
	if err != nil {
		return nil, err
	}
	rows, err := requirePositiveInt(args, "rows")
	if err != nil {
		return nil, err
	}
	cols, err := requirePositiveInt(args, "cols")
	if err != nil {
		return nil, err
	}
	out["rows"] = rows
	out["cols"] = cols
	return out, nil
}

func (terminateTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	return normalizeProcessArgs(args, false)
}

func (cleanupTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	return (listTool{}).NormalizeArgs(args)
}

func (commandTool) Preview(req tools.Request) string { return req.Args["cmd"] }
func (statusTool) Preview(req tools.Request) string  { return "Inspect " + req.Args["process_id"] }
func (listTool) Preview(req tools.Request) string    { return "List exec sessions" }
func (writeStdinTool) Preview(req tools.Request) string {
	if req.Args["chars"] == "" && req.Args["close_stdin"] == "" {
		return "Wait for output from " + req.Args["process_id"]
	}
	return "Write stdin to " + req.Args["process_id"]
}
func (resizeTool) Preview(req tools.Request) string    { return "Resize " + req.Args["process_id"] }
func (terminateTool) Preview(req tools.Request) string { return "Terminate " + req.Args["process_id"] }
func (cleanupTool) Preview(req tools.Request) string   { return "Cleanup exec sessions" }

func (commandTool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	control, err := tools.RequireExecControl(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	workdir, rel, err := tools.WorkspaceDir(runtime.Workdir, req.Args["workdir"])
	if err != nil && strings.TrimSpace(req.Args["workdir"]) != "" {
		return tools.Result{}, err
	}
	if rel == "" {
		rel = "."
	}
	settings := runtime.AccessSettings
	settings.TmpDir = runtime.SessionTmpDir()
	if err := tools.EnsureSessionTmpDir(settings); err != nil {
		return tools.Result{}, err
	}
	snap, err := control.Start(ctx, execruntime.StartRequest{
		SessionID:      runtime.SessionID,
		ChatID:         runtime.ChatID,
		ToolCallID:     req.ToolCallID,
		Command:        req.Args["cmd"],
		Workdir:        workdir,
		Shell:          req.Args["shell"],
		Login:          firstBool(req.Args["login"], true),
		TTY:            firstBool(req.Args["tty"], false),
		Timeout:        time.Duration(firstInt(req.Args["timeout_ms"])) * time.Millisecond,
		YieldTime:      durationOrDefault(req.Args["yield_time_ms"], defaultYieldTime),
		PreviewBytes:   firstInt(req.Args["max_output_bytes"]),
		AccessSettings: settings,
	})
	if err != nil {
		return tools.Result{}, err
	}
	stored := storedFromSnapshot(snap, execStartMessage(snap))
	stored.Workdir = rel
	meta := map[string]string{
		"process_id": snap.ProcessID,
		"command":    snap.Command,
		"state":      string(snap.State),
		"tty":        strconv.FormatBool(snap.TTY),
	}
	if snap.ExitCode != nil {
		meta["exit_code"] = strconv.Itoa(*snap.ExitCode)
	}
	return tools.Result{
		Output: stored.Message,
		Meta:   meta,
		Stored: stored,
	}, nil
}

func (statusTool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	control, err := tools.RequireExecControl(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	snap, err := control.Status(ctx, execruntime.StatusRequest{
		SessionID: runtime.SessionID,
		ChatID:    runtime.ChatID,
		ProcessID: req.Args["process_id"],
		MaxBytes:  firstInt(req.Args["max_output_bytes"]),
	})
	if err != nil {
		return tools.Result{}, err
	}
	stored := storedFromSnapshot(snap, "Fetched exec session status")
	return execResult(stored), nil
}

func (listTool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	control, err := tools.RequireExecControl(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	scope := execruntime.Scope(normalizeScope(req.Args))
	snaps, err := control.List(ctx, execruntime.ListRequest{
		SessionID: runtime.SessionID,
		ChatID:    runtime.ChatID,
		Scope:     scope,
		MaxBytes:  firstInt(req.Args["max_output_bytes"]),
	})
	if err != nil {
		return tools.Result{}, err
	}
	stored := storedListFromSnapshots(snaps, string(scope), "Listed exec sessions")
	return tools.Result{
		Output: tools.DisplayTextForStored(req.Tool, stored),
		Stored: stored,
	}, nil
}

func (writeStdinTool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	control, err := tools.RequireExecControl(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	snap, err := control.WriteStdin(ctx, execruntime.WriteStdinRequest{
		SessionID:  runtime.SessionID,
		ChatID:     runtime.ChatID,
		ProcessID:  req.Args["process_id"],
		Chars:      req.Args["chars"],
		CloseStdin: firstBool(req.Args["close_stdin"], false),
		MaxBytes:   firstInt(req.Args["max_output_bytes"]),
		YieldTime:  durationOrDefault(req.Args["yield_time_ms"], 0),
	})
	if err != nil {
		return tools.Result{}, err
	}
	message := "Updated exec session stdin"
	if req.Args["chars"] == "" && !firstBool(req.Args["close_stdin"], false) {
		message = "Waited for exec session output"
	}
	stored := storedFromSnapshot(snap, message)
	return execResult(stored), nil
}

func (resizeTool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	control, err := tools.RequireExecControl(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	rows, _ := strconv.Atoi(req.Args["rows"])
	cols, _ := strconv.Atoi(req.Args["cols"])
	snap, err := control.Resize(ctx, execruntime.ResizeRequest{
		SessionID: runtime.SessionID,
		ChatID:    runtime.ChatID,
		ProcessID: req.Args["process_id"],
		Size:      execruntime.TerminalSize{Rows: rows, Cols: cols},
		MaxBytes:  firstInt(req.Args["max_output_bytes"]),
	})
	if err != nil {
		return tools.Result{}, err
	}
	stored := storedFromSnapshot(snap, "Resized exec session tty")
	return execResult(stored), nil
}

func (terminateTool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	control, err := tools.RequireExecControl(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	snap, err := control.Terminate(ctx, execruntime.TerminateRequest{
		SessionID: runtime.SessionID,
		ChatID:    runtime.ChatID,
		ProcessID: req.Args["process_id"],
		MaxBytes:  firstInt(req.Args["max_output_bytes"]),
	})
	if err != nil {
		return tools.Result{}, err
	}
	stored := storedFromSnapshot(snap, "Terminated exec session")
	return execResult(stored), nil
}

func (cleanupTool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	control, err := tools.RequireExecControl(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	scope := execruntime.Scope(normalizeScope(req.Args))
	snaps, err := control.Cleanup(ctx, execruntime.CleanupRequest{
		SessionID: runtime.SessionID,
		ChatID:    runtime.ChatID,
		Scope:     scope,
		MaxBytes:  firstInt(req.Args["max_output_bytes"]),
	})
	if err != nil {
		return tools.Result{}, err
	}
	stored := storedListFromSnapshots(snaps, string(scope), "Cleaned up exec sessions")
	return tools.Result{
		Output: tools.DisplayTextForStored(req.Tool, stored),
		Stored: stored,
	}, nil
}

func (commandTool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "Started exec session", tools.DisplayTextForStored(req.Tool, result.Stored)
}
func (statusTool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "Fetched exec status", tools.DisplayTextForStored(req.Tool, result.Stored)
}
func (listTool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "Listed exec sessions", result.Output
}
func (writeStdinTool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	if req.Args["chars"] == "" && req.Args["close_stdin"] == "" {
		return "Waited for exec output", tools.DisplayTextForStored(req.Tool, result.Stored)
	}
	return "Updated exec stdin", tools.DisplayTextForStored(req.Tool, result.Stored)
}
func (resizeTool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "Resized exec tty", tools.DisplayTextForStored(req.Tool, result.Stored)
}
func (terminateTool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "Terminated exec session", tools.DisplayTextForStored(req.Tool, result.Stored)
}
func (cleanupTool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "Cleaned up exec sessions", result.Output
}

func execResult(stored tools.ExecStoredResult) tools.Result {
	meta := map[string]string{
		"process_id": stored.ProcessID,
		"state":      stored.State,
		"command":    stored.Command,
		"tty":        strconv.FormatBool(stored.TTY),
	}
	if stored.ExitCode != nil {
		meta["exit_code"] = strconv.Itoa(*stored.ExitCode)
	}
	return tools.Result{
		Output: tools.DisplayTextForStored(domain.ToolKindExecStatus, stored),
		Meta:   meta,
		Stored: stored,
	}
}

func storedFromSnapshot(snap execruntime.Snapshot, message string) tools.ExecStoredResult {
	return tools.ExecStoredResult{
		ProcessID:   snap.ProcessID,
		Command:     snap.Command,
		Workdir:     snap.Workdir,
		Shell:       snap.Shell,
		TTY:         snap.TTY,
		State:       string(snap.State),
		ExitCode:    snap.ExitCode,
		TimeoutMS:   snap.TimeoutMS,
		Output:      snap.Output,
		OutputBytes: snap.OutputBytes,
		OutputMode:  outputMode(snap),
		StdinClosed: snap.StdinClosed,
		Message:     message,
	}
}

func execStartMessage(snap execruntime.Snapshot) string {
	if snap.State == execruntime.StateRunning {
		return "Exec session is still running. Use exec_write_stdin with empty chars to wait for new output, exec_write_stdin with chars to interact with stdin, exec_status for one-off inspection, or exec_terminate to stop it."
	}
	return "Exec session completed during startup grace period."
}

func outputMode(snap execruntime.Snapshot) string {
	if snap.Drained {
		return "incremental"
	}
	return "tail"
}

func storedListFromSnapshots(snaps []execruntime.Snapshot, scope, message string) tools.ExecListStoredResult {
	items := make([]tools.ExecListStoredItem, 0, len(snaps))
	for _, snap := range snaps {
		items = append(items, tools.ExecListStoredItem{
			ProcessID: snap.ProcessID,
			Command:   snap.Command,
			State:     string(snap.State),
			TTY:       snap.TTY,
			ExitCode:  snap.ExitCode,
			Output:    snap.Output,
		})
	}
	return tools.ExecListStoredResult{
		Scope:   scope,
		Message: message,
		Items:   items,
	}
}

func normalizeProcessArgs(args map[string]string, allowScope bool) (map[string]string, error) {
	id := strings.TrimSpace(tools.FirstArg(args, "process_id"))
	if id == "" {
		return nil, errors.New("process_id is empty")
	}
	out := map[string]string{"process_id": id}
	if allowScope {
		if scope := normalizeScope(args); scope != "" {
			out["scope"] = scope
		}
	}
	if maxOutput := normalizeOptionalInt(args, "max_output_bytes"); maxOutput != "" {
		out["max_output_bytes"] = maxOutput
	}
	return out, nil
}

func normalizeScope(args map[string]string) string {
	scope := strings.TrimSpace(tools.FirstArg(args, "scope"))
	switch scope {
	case "", string(execruntime.ScopeChat):
		return string(execruntime.ScopeChat)
	case string(execruntime.ScopeSession):
		return string(execruntime.ScopeSession)
	default:
		return ""
	}
}

func normalizeOptionalInt(args map[string]string, key string) string {
	raw := strings.TrimSpace(tools.FirstArg(args, key))
	if raw == "" {
		return ""
	}
	ms, err := tools.ParseFlexibleInt(raw)
	if err != nil || ms < 0 {
		return ""
	}
	return strconv.Itoa(ms)
}

func parseBoolArg(args map[string]string, key string) string {
	raw := strings.TrimSpace(args[key])
	if raw == "" {
		return ""
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return ""
	}
	return strconv.FormatBool(value)
}

func firstBool(raw string, fallback bool) bool {
	if strings.TrimSpace(raw) == "" {
		return fallback
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback
	}
	return value
}

func firstInt(raw string) int {
	if strings.TrimSpace(raw) == "" {
		return 0
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0
	}
	return value
}

func requirePositiveInt(args map[string]string, key string) (string, error) {
	raw := strings.TrimSpace(tools.FirstArg(args, key))
	value, err := tools.ParseFlexibleInt(raw)
	if err != nil || value <= 0 {
		return "", fmt.Errorf("%s must be a positive integer", key)
	}
	return strconv.Itoa(value), nil
}

func durationOrDefault(raw string, fallback time.Duration) time.Duration {
	if strings.TrimSpace(raw) == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return fallback
	}
	return time.Duration(value) * time.Millisecond
}
