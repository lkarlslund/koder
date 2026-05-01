package greptool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/tools"
)

func TestSharedPresentationOmitsQueryPrefix(t *testing.T) {
	got := tools.PresentationForTool(domain.ToolKindGrep, "needle")
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

func TestNormalizeArgsAcceptsExtendedSearchOptions(t *testing.T) {
	got, err := tool{}.NormalizeArgs(map[string]string{
		"pattern":     "needle",
		"type":        "go",
		"output_mode": "files_with_matches",
		"ignore_case": "true",
		"head_limit":  "5.00000",
		"path":        "pkg",
		"include":     "*.go",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got["type"] != "go" || got["output_mode"] != "files_with_matches" || got["ignore_case"] != "true" || got["head_limit"] != "5" {
		t.Fatalf("unexpected normalized args: %#v", got)
	}
}

func TestExecuteFilesWithMatchesMode(t *testing.T) {
	workdir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workdir, "pkg"), 0o755); err != nil {
		t.Fatalf("mkdir pkg: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "pkg", "a.go"), []byte("needle a\n"), 0o644); err != nil {
		t.Fatalf("write a.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "pkg", "b.txt"), []byte("needle b\n"), 0o644); err != nil {
		t.Fatalf("write b.txt: %v", err)
	}

	result, err := tool{}.Execute(context.Background(), tools.Runtime{Workdir: workdir}, tools.Request{
		Tool: domain.ToolKindGrep,
		Args: map[string]string{
			"pattern":     "needle",
			"path":        "pkg",
			"output_mode": "files_with_matches",
			"type":        "go",
		},
	})
	if err != nil {
		t.Fatalf("execute grep files_with_matches: %v", err)
	}
	if strings.TrimSpace(result.Output) != "a.go" {
		t.Fatalf("expected only matching go file, got %q", result.Output)
	}
}

func TestExecuteCountMode(t *testing.T) {
	workdir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workdir, "pkg"), 0o755); err != nil {
		t.Fatalf("mkdir pkg: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "pkg", "a.go"), []byte("needle\nneedle\n"), 0o644); err != nil {
		t.Fatalf("write a.go: %v", err)
	}

	result, err := tool{}.Execute(context.Background(), tools.Runtime{Workdir: workdir}, tools.Request{
		Tool: domain.ToolKindGrep,
		Args: map[string]string{
			"pattern":     "needle",
			"path":        "pkg",
			"output_mode": "count",
		},
	})
	if err != nil {
		t.Fatalf("execute grep count mode: %v", err)
	}
	if !strings.Contains(strings.TrimSpace(result.Output), "a.go:2") {
		t.Fatalf("expected count output, got %q", result.Output)
	}
}

func TestExecuteFallsBackWithoutRipgrep(t *testing.T) {
	t.Setenv("PATH", "")
	workdir := t.TempDir()
	target := filepath.Join(workdir, "pkg", "file.go")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir target dir: %v", err)
	}
	if err := os.WriteFile(target, []byte("Alpha\nneedle here\nomega\n"), 0o644); err != nil {
		t.Fatalf("write target file: %v", err)
	}

	result, err := tool{}.Execute(context.Background(), tools.Runtime{Workdir: workdir}, tools.Request{
		Tool: domain.ToolKindGrep,
		Args: map[string]string{
			"pattern":     "alpha|needle",
			"path":        "pkg/file.go",
			"ignore_case": "true",
		},
	})
	if err != nil {
		t.Fatalf("execute fallback grep on file path: %v", err)
	}
	if !strings.Contains(result.Output, "pkg/file.go:1:Alpha") || !strings.Contains(result.Output, "pkg/file.go:2:needle here") {
		t.Fatalf("expected fallback matches in output, got %q", result.Output)
	}
}

func TestExecuteReturnsErrorWhenRipgrepFails(t *testing.T) {
	workdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workdir, "file.txt"), []byte("needle\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	binDir := t.TempDir()
	rgPath := filepath.Join(binDir, "rg")
	script := "#!/bin/sh\n" +
		"echo 'regex parse failure' >&2\n" +
		"exit 2\n"
	if err := os.WriteFile(rgPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake rg: %v", err)
	}
	t.Setenv("PATH", binDir)

	_, err := tool{}.Execute(context.Background(), tools.Runtime{Workdir: workdir}, tools.Request{
		Tool: domain.ToolKindGrep,
		Args: map[string]string{
			"pattern": "needle",
		},
	})
	if err == nil {
		t.Fatal("expected ripgrep failure to be returned")
	}
	if !strings.Contains(err.Error(), "regex parse failure") {
		t.Fatalf("expected ripgrep stderr in error, got %v", err)
	}
}

func TestExecuteFallbackReturnsErrorForInvalidIncludeGlob(t *testing.T) {
	t.Setenv("PATH", "")
	workdir := t.TempDir()
	target := filepath.Join(workdir, "pkg", "file.go")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir target dir: %v", err)
	}
	if err := os.WriteFile(target, []byte("needle\n"), 0o644); err != nil {
		t.Fatalf("write target file: %v", err)
	}

	_, err := tool{}.Execute(context.Background(), tools.Runtime{Workdir: workdir}, tools.Request{
		Tool: domain.ToolKindGrep,
		Args: map[string]string{
			"pattern": "needle",
			"include": "[",
		},
	})
	if err == nil {
		t.Fatal("expected invalid include glob error")
	}
	if !strings.Contains(err.Error(), "invalid include glob") {
		t.Fatalf("unexpected error: %v", err)
	}
}
