package applypatchtool

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/tools"
)

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, string(out))
		}
	}
	return dir
}

func TestPatchPathsSkipsDuplicatesAndDevNull(t *testing.T) {
	patch := strings.Join([]string{
		"--- a/one.txt",
		"+++ b/one.txt",
		"--- /dev/null",
		"+++ b/two.txt",
	}, "\n")
	got := patchPaths(patch)
	if len(got) != 2 || got[0] != "one.txt" || got[1] != "two.txt" {
		t.Fatalf("unexpected patch paths: %#v", got)
	}
}

func TestNormalizePreviewAndSummarize(t *testing.T) {
	req, err := tool{}.NormalizeArgs(map[string]string{"diff": "--- a/a.txt\n+++ b/a.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if req["patch"] == "" {
		t.Fatal("expected patch alias to normalize")
	}
	preview := tool{}.Preview(tools.Request{Args: req})
	if preview != "a.txt" {
		t.Fatalf("unexpected preview: %q", preview)
	}
	summary, body := tool{}.SummarizeResult(tools.Request{}, tools.Result{Output: "Applied patch"})
	if summary != "apply_patch" || body != "Applied patch" {
		t.Fatalf("unexpected summary/body: %q %q", summary, body)
	}
}

func TestExecuteAppliesPatch(t *testing.T) {
	dir := initRepo(t)
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "file.txt"}, {"commit", "-m", "initial"}} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, string(out))
		}
	}
	if err := os.WriteFile(path, []byte("after\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	diffCmd := exec.Command("git", "-C", dir, "diff", "--", "file.txt")
	diffOut, err := diffCmd.CombinedOutput()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 1 {
			t.Fatalf("git diff: %v: %s", err, string(diffOut))
		}
	}
	patch := string(diffOut)
	if err := os.WriteFile(path, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := tool{}.Execute(context.Background(), tools.Runtime{Workdir: dir}, tools.Request{
		Tool: domain.ToolKindApplyPatch,
		Args: map[string]string{"patch": patch},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Meta["file_count"] != "1" {
		t.Fatalf("expected one changed file, got %#v", result.Meta)
	}
	if result.DiffText == "" {
		t.Fatal("expected diff text")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "after\n" {
		t.Fatalf("unexpected file contents: %q", string(body))
	}
}
