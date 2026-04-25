package bashtool

import (
	"context"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
)

type tool struct{}

func init() { tools.Register(tool{}) }

func (tool) Kind() domain.ToolKind    { return domain.ToolKindBash }
func (tool) BypassesPermission() bool { return false }
func (tool) Definition(tools.Runtime) (provider.ToolDefinition, bool) {
	return tools.FunctionDefinition(domain.ToolKindBash, "Run a shell command in the workspace", `{"type":"object","properties":{"command":{"type":"string","description":"Shell command to execute"},"workdir":{"type":"string","description":"Optional workspace-relative working directory"},"timeout_ms":{"type":"integer","description":"Optional timeout in milliseconds"}},"required":["command"],"additionalProperties":false}`), true
}
func (tool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	command := strings.TrimSpace(tools.FirstArg(args, "command", "cmd"))
	if command == "" {
		return nil, errors.New("command is empty")
	}
	out := map[string]string{"command": command}
	if workdir := tools.NormalizePathInput(tools.FirstArg(args, "workdir", "cwd", "dir")); workdir != "" {
		out["workdir"] = workdir
	}
	if timeout := strings.TrimSpace(tools.FirstArg(args, "timeout_ms", "timeout")); timeout != "" {
		ms, err := tools.ParseFlexibleInt(timeout)
		if err != nil {
			return nil, errors.New("timeout_ms must be a positive integer")
		}
		out["timeout_ms"] = strconv.Itoa(ms)
	}
	return out, nil
}
func (tool) LegacyArgs(raw string) map[string]string { return map[string]string{"command": raw} }
func (tool) Preview(req tools.Request) string        { return req.Args["command"] }
func (tool) PresentationForPreview(preview string) tools.Presentation {
	preview = strings.TrimSpace(preview)
	return tools.Presentation{Title: "Run command", Subtitle: preview, Preview: preview}
}
func (tool) Presentation(req tools.Request) tools.Presentation {
	return tool{}.PresentationForPreview(req.Args["command"])
}
func (tool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	if _, err := exec.LookPath("bash"); err != nil {
		return tools.Result{}, errors.New("bash is not available on this system")
	}
	workdir, rel, err := tools.WorkspaceDir(runtime.Workdir, req.Args["workdir"])
	if err != nil {
		return tools.Result{}, err
	}
	timeout := tools.DefaultBashTimeout
	if raw := strings.TrimSpace(req.Args["timeout_ms"]); raw != "" {
		ms, err := strconv.Atoi(raw)
		if err != nil {
			return tools.Result{}, errors.New("timeout_ms must be a positive integer")
		}
		if ms > 0 {
			timeout = time.Duration(ms) * time.Millisecond
		}
	}
	output, exitCode, err := tools.ShellResult(ctx, workdir, timeout, req.Args["command"])
	result := tools.Result{
		Output: output,
		Meta: map[string]string{
			"command":    req.Args["command"],
			"workdir":    rel,
			"timeout_ms": strconv.FormatInt(timeout.Milliseconds(), 10),
			"exit_code":  strconv.Itoa(exitCode),
		},
		Stored: tools.BashStoredResult{
			Command:   req.Args["command"],
			Workdir:   rel,
			TimeoutMS: timeout.Milliseconds(),
			ExitCode:  exitCode,
			Output:    output,
		},
	}
	if err != nil {
		return result, err
	}
	return result, nil
}
func (tool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return tools.DefaultSummarizeResult(req, result)
}
func (tool) PersistResult(ctx context.Context, st *store.Store, sessionID int64, req tools.Request, result tools.Result) (<-chan domain.Event, error) {
	return tools.PersistStandardResult(ctx, st, sessionID, req, result)
}
