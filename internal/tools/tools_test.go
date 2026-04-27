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

func TestBashZeroTimeoutUsesDefault(t *testing.T) {
	dir := t.TempDir()
	registry := tools.NewRegistry(dir)

	result, err := registry.Execute(context.Background(), tools.Request{
		Tool: domain.ToolKindBash,
		Args: map[string]string{
			"command":    "printf ok",
			"timeout_ms": "0",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(result.Output) != "ok" {
		t.Fatalf("unexpected bash output: %q", result.Output)
	}
	if got := result.Meta["timeout_ms"]; got != "120000" {
		t.Fatalf("expected default timeout metadata, got %q", got)
	}
}

func TestBashWholeFloatStringTimeoutIsAccepted(t *testing.T) {
	dir := t.TempDir()
	registry := tools.NewRegistry(dir)

	result, err := registry.Execute(context.Background(), tools.Request{
		Tool: domain.ToolKindBash,
		Args: map[string]string{
			"command":    "printf ok",
			"timeout_ms": "60000.00000",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(result.Output) != "ok" {
		t.Fatalf("unexpected bash output: %q", result.Output)
	}
	if got := result.Meta["timeout_ms"]; got != "60000" {
		t.Fatalf("expected normalized timeout metadata, got %q", got)
	}
}

func TestBashFractionalTimeoutStillFails(t *testing.T) {
	dir := t.TempDir()
	registry := tools.NewRegistry(dir)

	_, err := registry.Execute(context.Background(), tools.Request{
		Tool: domain.ToolKindBash,
		Args: map[string]string{
			"command":    "printf ok",
			"timeout_ms": "60000.5",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "timeout_ms must be a positive integer") {
		t.Fatalf("expected positive integer error, got %v", err)
	}
}

func TestReadWholeFloatStringOffsetAndLimitAreAccepted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("1\n2\n3\n4\n5\n6\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry(dir)

	result, err := registry.Execute(context.Background(), tools.Request{
		Tool: domain.ToolKindRead,
		Args: map[string]string{
			"path":   "file.txt",
			"offset": "3.00000",
			"limit":  "2.00000",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "3") || !strings.Contains(result.Output, "4") {
		t.Fatalf("expected sliced lines in output, got %q", result.Output)
	}
	if strings.Contains(result.Output, "1: 1") || strings.Contains(result.Output, "6: 6") {
		t.Fatalf("expected read window to apply, got %q", result.Output)
	}
	if !strings.Contains(result.Output, "Showing lines 3-4 of 6. Use offset=5 limit=2 to continue.") {
		t.Fatalf("expected continuation footer, got %q", result.Output)
	}
	if got := result.Meta["offset"]; got != "3" {
		t.Fatalf("expected normalized offset metadata, got %q", got)
	}
	if got := result.Meta["limit"]; got != "2" {
		t.Fatalf("expected normalized limit metadata, got %q", got)
	}
}

func TestReadFractionalOffsetFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("1\n2\n3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry(dir)

	_, err := registry.Execute(context.Background(), tools.Request{
		Tool: domain.ToolKindRead,
		Args: map[string]string{
			"path":   "file.txt",
			"offset": "3.5",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "offset must be a positive integer") {
		t.Fatalf("expected positive integer error, got %v", err)
	}
}

func TestWebSearchWholeFloatStringLimitIsAccepted(t *testing.T) {
	req, err := tools.Normalize(tools.Request{
		Tool: domain.ToolKindWebSearch,
		Args: map[string]string{
			"query": "golang",
			"limit": "4.00000",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := req.Args["limit"]; got != "4" {
		t.Fatalf("expected normalized limit, got %q", got)
	}
}

func TestReadTextFileUsesColonLinePrefix(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	output, truncated, err := tools.ReadTextFile(path, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if truncated {
		t.Fatal("expected untruncated output")
	}
	if output != "1: alpha\n2: beta\n3: " {
		t.Fatalf("unexpected read output: %q", output)
	}
}

func TestParseReadStoredLinesAcceptsColonAndLegacyFormats(t *testing.T) {
	lines, footer := tools.ParseReadStoredLines("1: alpha\n2: beta")
	if footer != "" {
		t.Fatalf("expected empty footer, got %q", footer)
	}
	if len(lines) != 2 || lines[0].Number != 1 || lines[0].Text != "alpha" || lines[1].Number != 2 || lines[1].Text != "beta" {
		t.Fatalf("unexpected colon parsed lines: %#v", lines)
	}

	lines, footer = tools.ParseReadStoredLines("     1\talpha\n     2\tbeta")
	if footer != "" {
		t.Fatalf("expected empty legacy footer, got %q", footer)
	}
	if len(lines) != 2 || lines[0].Number != 1 || lines[0].Text != "alpha" || lines[1].Number != 2 || lines[1].Text != "beta" {
		t.Fatalf("unexpected legacy parsed lines: %#v", lines)
	}
}
