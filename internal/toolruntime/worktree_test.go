package toolruntime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/lkarlslund/koder/internal/accesssettings"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/tools"
)

func TestIsolatedRuntimeExecAndGitCommit(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bubblewrap unavailable")
	}
	parent := t.TempDir()
	child := filepath.Join(t.TempDir(), "checkout")
	git := func(args ...string) {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", parent}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	git("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(parent, "shared.txt"), []byte("parent"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "base")
	git("worktree", "add", "-b", "worker", child, "HEAD")
	access := accesssettings.LockedDown()
	access.Project = accesssettings.ModeReadWrite
	session := domain.Session{ID: "isolated-session", ProjectRoot: parent, AccessSettings: access}
	chat := domain.Chat{ID: "isolated-chat", Worktree: &domain.ChatWorktree{Path: child, Branch: "worker", GitDir: filepath.Join(parent, ".git")}}
	runtime := New(Config{}).Runtime(session, chat)
	if runtime.Workdir != child {
		t.Fatalf("runtime cwd = %s", runtime.Workdir)
	}
	result, err := tools.Call(context.Background(), tools.Options{Runtime: runtime, Request: tools.Request{
		Tool: tools.ExecCommand, ToolCallID: "isolation-test",
		Args: map[string]string{"cmd": "printf isolated > shared.txt && git add shared.txt && git -c user.name=Test -c user.email=test@example.com commit -m isolated", "description": "Edit and commit in the isolated checkout"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Meta["exit_code"] != "0" {
		t.Fatalf("isolated git failed: %#v %s", result.Meta, result.Output)
	}
	data, err := os.ReadFile(filepath.Join(parent, "shared.txt"))
	if err != nil || string(data) != "parent" {
		t.Fatalf("parent changed: %s %v", data, err)
	}
	data, err = os.ReadFile(filepath.Join(child, "shared.txt"))
	if err != nil || string(data) != "isolated" {
		t.Fatalf("child write failed: %s %v", data, err)
	}
}
