package readtool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/tools"
)

func TestExecuteAllowsAbsolutePathOutsideWorkspace(t *testing.T) {
	workspace := t.TempDir()
	outsideDir := t.TempDir()
	target := filepath.Join(outsideDir, "rules.md")
	if err := os.WriteFile(target, []byte("# Rules\nhello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := tool{}.Execute(context.Background(), tools.Runtime{Workdir: workspace}, tools.Request{
		Args: map[string]string{"path": target},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "hello") {
		t.Fatalf("expected absolute path read output, got %q", result.Output)
	}
	if got := result.Meta["path"]; got != filepath.ToSlash(target) {
		t.Fatalf("expected absolute path label %q, got %q", filepath.ToSlash(target), got)
	}
}

func TestPresentationIncludesPathAndLineRange(t *testing.T) {
	got := tool{}.Presentation(tools.Request{
		Tool: domain.ToolKindRead,
		Args: map[string]string{
			"path":   "internal/tools/readtool/read.go",
			"offset": "10",
			"limit":  "5",
		},
	})
	if got.Title != "Read file internal/tools/readtool/read.go, lines 10-14" {
		t.Fatalf("unexpected title: %q", got.Title)
	}
	if got.Subtitle != "" {
		t.Fatalf("expected empty subtitle, got %q", got.Subtitle)
	}
}
