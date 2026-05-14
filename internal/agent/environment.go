package agent

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/agents"
	"github.com/lkarlslund/koder/internal/domain"
)

const environmentProbeTimeout = 500 * time.Millisecond

type environmentSnapshot struct {
	WorkspaceRoot string
	Workdir       string
	DateTime      time.Time
	Platform      string
	OS            string
	Shell         string
	Git           gitSnapshot
}

type gitSnapshot struct {
	Repository bool
	Root       string
	Branch     string
	Commit     string
	Upstream   string
	Staged     int
	Unstaged   int
	Untracked  int
}

func (e *Engine) environmentPrompt(session domain.Session) string {
	workspaceRoot := strings.TrimSpace(session.ProjectRoot)
	if workspaceRoot == "" {
		workspaceRoot = agents.FindProjectRoot(e.workdir)
	}
	snapshot := environmentSnapshot{
		WorkspaceRoot: workspaceRoot,
		Workdir:       e.workdir,
		DateTime:      time.Now(),
		Platform:      runtime.GOOS + "/" + runtime.GOARCH,
		OS:            osDescription(),
		Shell:         shellDescription(),
		Git:           gitInfo(e.workdir),
	}
	return formatEnvironmentPrompt(snapshot)
}

func (e *Engine) sessionEnvironmentPrompt(session domain.Session) string {
	e.envMu.Lock()
	defer e.envMu.Unlock()
	if e.envPrompts == nil {
		e.envPrompts = map[domain.ID]string{}
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
	writeEnvironmentLine(&b, "Current date and time", formatLocalDateTime(snapshot.DateTime))
	writeEnvironmentLine(&b, "Platform", fallbackUnknown(snapshot.Platform))
	writeEnvironmentLine(&b, "OS", fallbackUnknown(snapshot.OS))
	writeEnvironmentLine(&b, "Shell", fallbackUnknown(snapshot.Shell))
	if !snapshot.Git.Repository {
		writeEnvironmentLine(&b, "Git repository", "no")
		return b.String()
	}
	writeEnvironmentLine(&b, "Git repository", "yes")
	writeEnvironmentLine(&b, "Git root", fallbackUnknown(snapshot.Git.Root))
	writeEnvironmentLine(&b, "Git branch", fallbackUnknown(snapshot.Git.Branch))
	writeEnvironmentLine(&b, "Git commit", fallbackUnknown(snapshot.Git.Commit))
	writeEnvironmentLine(&b, "Git upstream", fallbackUnknown(snapshot.Git.Upstream))
	writeEnvironmentLine(&b, "Git status", formatGitStatus(snapshot.Git))
	return b.String()
}

func writeEnvironmentLine(b *strings.Builder, key string, value string) {
	b.WriteString("\n- ")
	b.WriteString(key)
	b.WriteString(": ")
	b.WriteString(value)
}

func formatLocalDateTime(t time.Time) string {
	return t.Format("2006-01-02 15:04:05 MST (UTC-07:00)")
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
	root, ok := gitOutput(workdir, "rev-parse", "--show-toplevel")
	if !ok {
		return gitSnapshot{}
	}
	branch, branchOK := gitOutput(workdir, "symbolic-ref", "--quiet", "--short", "HEAD")
	if !branchOK {
		if commit, ok := gitOutput(workdir, "rev-parse", "--short", "HEAD"); ok {
			branch = "detached at " + commit
		}
	}
	commit, _ := gitOutput(workdir, "rev-parse", "--short", "HEAD")
	upstream, _ := gitOutput(workdir, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
	status, _ := gitOutput(workdir, "status", "--porcelain")
	staged, unstaged, untracked := parseGitPorcelain(status)
	return gitSnapshot{
		Repository: true,
		Root:       root,
		Branch:     branch,
		Commit:     commit,
		Upstream:   upstream,
		Staged:     staged,
		Unstaged:   unstaged,
		Untracked:  untracked,
	}
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

func parseGitPorcelain(status string) (staged int, unstaged int, untracked int) {
	for _, line := range strings.Split(status, "\n") {
		if len(line) < 2 {
			continue
		}
		if strings.HasPrefix(line, "??") {
			untracked++
			continue
		}
		if line[0] != ' ' {
			staged++
		}
		if line[1] != ' ' {
			unstaged++
		}
	}
	return staged, unstaged, untracked
}

func formatGitStatus(git gitSnapshot) string {
	if git.Staged == 0 && git.Unstaged == 0 && git.Untracked == 0 {
		return "clean"
	}
	parts := []string{"dirty"}
	if git.Staged > 0 {
		parts = append(parts, "staged "+strconv.Itoa(git.Staged))
	}
	if git.Unstaged > 0 {
		parts = append(parts, "unstaged "+strconv.Itoa(git.Unstaged))
	}
	if git.Untracked > 0 {
		parts = append(parts, "untracked "+strconv.Itoa(git.Untracked))
	}
	return parts[0] + " (" + strings.Join(parts[1:], ", ") + ")"
}
