package agent

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
)

const environmentProbeTimeout = 500 * time.Millisecond

type environmentSnapshot struct {
	WorkspaceRoot string
	Workdir       string
	Platform      string
	OS            string
	Shell         string
	Git           gitSnapshot
}

type gitSnapshot struct {
	Repository bool
}

func (e *Engine) environmentPrompt(session domain.Session) string {
	workspaceRoot := strings.TrimSpace(session.ProjectRoot)
	snapshot := environmentSnapshot{
		WorkspaceRoot: workspaceRoot,
		Workdir:       workspaceRoot,
		Platform:      runtime.GOOS + "/" + runtime.GOARCH,
		OS:            osDescription(),
		Shell:         shellDescription(),
		Git:           gitInfo(workspaceRoot),
	}
	return formatEnvironmentPrompt(snapshot)
}

func sessionProjectRoot(session domain.Session) string {
	return strings.TrimSpace(session.ProjectRoot)
}

func (e *Engine) sessionEnvironmentPrompt(session domain.Session) string {
	e.envMu.Lock()
	defer e.envMu.Unlock()
	if e.envPrompts == nil {
		e.envPrompts = map[id.ID]string{}
	}
	if text := e.envPrompts[session.ID]; text != "" {
		return text
	}
	text := e.environmentPrompt(session)
	e.envPrompts[session.ID] = text
	return text
}

func formatEnvironmentPrompt(snapshot environmentSnapshot) string {
	var b strings.Builder
	b.WriteString("Runtime environment:")
	writeEnvironmentLine(&b, "Workspace root", fallbackUnknown(snapshot.WorkspaceRoot))
	writeEnvironmentLine(&b, "Current working directory", fallbackUnknown(snapshot.Workdir))
	writeEnvironmentLine(&b, "Current date and time", "not included; use a tool if the exact system time is needed")
	writeEnvironmentLine(&b, "Platform", fallbackUnknown(snapshot.Platform))
	writeEnvironmentLine(&b, "OS", fallbackUnknown(snapshot.OS))
	writeEnvironmentLine(&b, "Shell", fallbackUnknown(snapshot.Shell))
	if !snapshot.Git.Repository {
		writeEnvironmentLine(&b, "Git repository", "no")
		return b.String()
	}
	writeEnvironmentLine(&b, "Git repository", "yes")
	return b.String()
}

func writeEnvironmentLine(b *strings.Builder, key string, value string) {
	b.WriteString("\n- ")
	b.WriteString(key)
	b.WriteString(": ")
	b.WriteString(value)
}

func fallbackUnknown(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	return value
}

func osDescription() string {
	if runtime.GOOS == "windows" {
		if out, ok := commandOutput("", "cmd", "/C", "ver"); ok {
			return out
		}
		return runtime.GOOS
	}
	if out, ok := commandOutput("", "uname", "-sr"); ok {
		return out
	}
	return runtime.GOOS
}

func shellDescription() string {
	for _, key := range []string{"SHELL", "COMSPEC"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return "unknown"
}

func gitInfo(workdir string) gitSnapshot {
	_, ok := gitOutput(workdir, "rev-parse", "--is-inside-work-tree")
	if !ok {
		return gitSnapshot{}
	}
	return gitSnapshot{Repository: true}
}

func gitOutput(workdir string, args ...string) (string, bool) {
	return commandOutput(workdir, "git", args...)
}

func commandOutput(workdir string, name string, args ...string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), environmentProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	if strings.TrimSpace(workdir) != "" {
		cmd.Dir = workdir
	}
	out, err := cmd.Output()
	if ctx.Err() != nil || err != nil {
		return "", false
	}
	return strings.TrimRight(string(out), "\r\n"), true
}
