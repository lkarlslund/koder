package bashtool

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/tools"
)

type tool struct{}

func init() {
	tools.Register(tool{}, tools.ToolSpec{
		Title:       "Run command",
		Description: "Run a shell command in the workspace.",
		Usage:       "Run a shell command in the workspace",
		Parameters:  `{"type":"object","properties":{"command":{"type":"string","description":"Shell command to execute"},"workdir":{"type":"string","description":"Optional workspace-relative working directory"},"timeout_ms":{"type":"integer","description":"Optional timeout in milliseconds"}},"required":["command"],"additionalProperties":false}`,
		ExposeToLLM: true,
	})
}

func (tool) Kind() domain.ToolKind    { return domain.ToolKindBash }
func (tool) BypassesPermission() bool { return false }
func (tool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	command := strings.TrimSpace(tools.FirstArg(args, "command", "cmd"))
	if command == "" {
		return nil, errors.New("command is empty")
	}
	for _, key := range []string{"cwd", "dir"} {
		if strings.TrimSpace(args[key]) != "" {
			return nil, fmt.Errorf("%s is no longer supported; use workdir", key)
		}
	}
	out := map[string]string{"command": command}
	if workdir := tools.NormalizePathInput(tools.FirstArg(args, "workdir")); workdir != "" {
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
func (tool) Preview(req tools.Request) string { return req.Args["command"] }
func (tool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	if _, err := exec.LookPath("bash"); err != nil {
		return tools.Result{}, errors.New("bash is not available on this system")
	}
	workdir, rel, err := tools.WorkspaceDir(runtime.Workdir, req.Args["workdir"])
	if err != nil {
		return tools.Result{}, err
	}
	settings := runtime.AccessSettings
	settings.TmpDir = runtime.SessionTmpDir()
	if err := tools.EnsureSessionTmpDir(settings); err != nil {
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
	output, exitCode, err := tools.ShellResult(ctx, workdir, timeout, req.Args["command"], settings)
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
