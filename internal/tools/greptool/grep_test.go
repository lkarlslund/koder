package greptool

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

func TestPresentationForPreviewOmitsQueryPrefix(t *testing.T) {
	got := tool{}.PresentationForPreview("needle")
	if got.Title != "Search text" {
		t.Fatalf("unexpected title: %#v", got)
	}
	if got.Subtitle != "needle" {
		t.Fatalf("expected plain subtitle without query prefix, got %#v", got)
	}
}

func TestPresentationIncludesPathScope(t *testing.T) {
	got := tool{}.Presentation(tools.Request{
		Tool: domain.ToolKindGrep,
		Args: map[string]string{
			"pattern": "needle",
			"path":    "internal",
		},
	})
	if got.Subtitle != "needle in internal" {
		t.Fatalf("unexpected subtitle: %#v", got)
	}
}

func TestPresentationIncludesIncludeScope(t *testing.T) {
	got := tool{}.Presentation(tools.Request{
		Tool: domain.ToolKindGrep,
		Args: map[string]string{
			"pattern": "needle",
			"include": "*.go",
		},
	})
	if got.Subtitle != "needle in *.go" {
		t.Fatalf("unexpected subtitle: %#v", got)
	}
}

func TestPresentationIncludesPathAndIncludeScope(t *testing.T) {
	got := tool{}.Presentation(tools.Request{
		Tool: domain.ToolKindGrep,
		Args: map[string]string{
			"pattern": "needle",
			"path":    "internal",
			"include": "*.go",
		},
	})
	if got.Subtitle != "needle in internal (*.go)" {
		t.Fatalf("unexpected subtitle: %#v", got)
	}
}

func TestExecuteSearchesSingleFilePath(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("rg not installed")
	}
	workdir := t.TempDir()
	target := filepath.Join(workdir, "pkg", "file.go")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir target dir: %v", err)
	}
	if err := os.WriteFile(target, []byte("alpha\nneedle here\nomega\n"), 0o644); err != nil {
		t.Fatalf("write target file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "pkg", "other.go"), []byte("needle elsewhere\n"), 0o644); err != nil {
		t.Fatalf("write sibling file: %v", err)
	}

	result, err := tool{}.Execute(context.Background(), tools.Runtime{Workdir: workdir}, tools.Request{
		Tool: domain.ToolKindGrep,
		Args: map[string]string{
			"pattern": "needle",
			"path":    "pkg/file.go",
		},
	})
	if err != nil {
		t.Fatalf("execute grep on file path: %v", err)
	}
	if !strings.Contains(result.Output, "file.go:2:needle here") {
		t.Fatalf("expected file match in output, got %q", result.Output)
	}
	if strings.Contains(result.Output, "other.go") {
		t.Fatalf("expected grep to stay scoped to single file, got %q", result.Output)
	}
}

func TestExecuteSearchesDirectoryPath(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("rg not installed")
	}
	workdir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workdir, "pkg"), 0o755); err != nil {
		t.Fatalf("mkdir pkg: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "pkg", "a.go"), []byte("needle a\n"), 0o644); err != nil {
		t.Fatalf("write a.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "pkg", "b.go"), []byte("needle b\n"), 0o644); err != nil {
		t.Fatalf("write b.go: %v", err)
	}

	result, err := tool{}.Execute(context.Background(), tools.Runtime{Workdir: workdir}, tools.Request{
		Tool: domain.ToolKindGrep,
		Args: map[string]string{
			"pattern": "needle",
			"path":    "pkg",
		},
	})
	if err != nil {
		t.Fatalf("execute grep on directory path: %v", err)
	}
	if !strings.Contains(result.Output, "a.go:1:needle a") || !strings.Contains(result.Output, "b.go:1:needle b") {
		t.Fatalf("expected both directory matches in output, got %q", result.Output)
	}
}
