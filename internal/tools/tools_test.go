package tools_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/tools"
	_ "github.com/lkarlslund/koder/internal/tools/all"
)

func TestReadAndPatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if output, err := exec.Command("git", "-C", dir, "init").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, string(output))
	}
	if output, err := exec.Command("git", "-C", dir, "config", "user.email", "test@example.com").CombinedOutput(); err != nil {
		t.Fatalf("git config email: %v: %s", err, string(output))
	}
	if output, err := exec.Command("git", "-C", dir, "config", "user.name", "Test").CombinedOutput(); err != nil {
		t.Fatalf("git config name: %v: %s", err, string(output))
	}
	if err := os.WriteFile(path, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("git", "-C", dir, "add", "file.txt").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, string(output))
	}
	if output, err := exec.Command("git", "-C", dir, "commit", "-m", "initial").CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v: %s", err, string(output))
	}
	registry := tools.NewRegistry(dir)
	readResult, err := registry.Execute(context.Background(), tools.Request{
		Tool: domain.ToolKindRead,
		Args: map[string]string{"path": "file.txt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readResult.Output, "before") {
		t.Fatalf("unexpected read result: %q", readResult.Output)
	}
	if err := os.WriteFile(path, []byte("after\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	diffCmd := exec.Command("git", "-C", dir, "diff", "--", "file.txt")
	diffOut, err := diffCmd.CombinedOutput()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 1 {
			t.Fatalf("build diff: %v: %s", err, string(diffOut))
		}
	}
	patch := string(diffOut)
	if err := os.WriteFile(path, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	patchResult, err := registry.Execute(context.Background(), tools.Request{
		Tool: domain.ToolKindApplyPatch,
		Args: map[string]string{"patch": patch},
	})
	if err != nil {
		t.Fatal(err)
	}
	if patchResult.DiffText == "" {
		t.Fatal("expected diff text")
	}
	afterBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterBytes) != "after\n" {
		t.Fatalf("unexpected file contents: %q", string(afterBytes))
	}
}

func TestReadCurrentDirectoryListsFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "alpha.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}

	registry := tools.NewRegistry(dir)
	result, err := registry.Execute(context.Background(), tools.Request{
		Tool: domain.ToolKindRead,
		Args: map[string]string{"path": "."},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Meta["mode"] != "dir" {
		t.Fatalf("expected directory mode, got %#v", result.Meta)
	}
	if result.Meta["path"] != "." {
		t.Fatalf("expected current directory path, got %#v", result.Meta)
	}
	if !strings.Contains(result.Output, "alpha.txt") {
		t.Fatalf("expected file listing to include alpha.txt, got %q", result.Output)
	}
	if !strings.Contains(result.Output, "nested/") {
		t.Fatalf("expected file listing to include nested directory, got %q", result.Output)
	}
}
