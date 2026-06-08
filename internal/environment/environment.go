package environment

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const probeTimeout = 500 * time.Millisecond

type Snapshot struct {
	WorkspaceRoot string
	Workdir       string
	Platform      string
	OS            string
	Shell         string
	Git           GitSnapshot
}

type GitSnapshot struct {
	Repository bool
}

func Prompt(projectRoot string) string {
	projectRoot = strings.TrimSpace(projectRoot)
	return Format(Snapshot{
		WorkspaceRoot: projectRoot,
		Workdir:       projectRoot,
		Platform:      runtime.GOOS + "/" + runtime.GOARCH,
		OS:            osDescription(),
		Shell:         shellDescription(),
		Git:           GitInfo(projectRoot),
	})
}

func Format(snapshot Snapshot) string {
	var b strings.Builder
	b.WriteString("Runtime environment:")
	writeLine(&b, "Workspace root", fallbackUnknown(snapshot.WorkspaceRoot))
	writeLine(&b, "Current working directory", fallbackUnknown(snapshot.Workdir))
	writeLine(&b, "Current date and time", "not included; use a tool if the exact system time is needed")
	writeLine(&b, "Platform", fallbackUnknown(snapshot.Platform))
	writeLine(&b, "OS", fallbackUnknown(snapshot.OS))
	writeLine(&b, "Shell", fallbackUnknown(snapshot.Shell))
	if !snapshot.Git.Repository {
		writeLine(&b, "Git repository", "no")
		return b.String()
	}
	writeLine(&b, "Git repository", "yes")
	return b.String()
}

func GitInfo(workdir string) GitSnapshot {
	_, ok := gitOutput(workdir, "rev-parse", "--is-inside-work-tree")
	if !ok {
		return GitSnapshot{}
	}
	return GitSnapshot{Repository: true}
}

func writeLine(b *strings.Builder, key string, value string) {
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

func gitOutput(workdir string, args ...string) (string, bool) {
	return commandOutput(workdir, "git", args...)
}

func commandOutput(workdir string, name string, args ...string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
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
