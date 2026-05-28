package linttool

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/tools"
)

func TestExecuteReportsSyntaxDiagnostics(t *testing.T) {
	workdir := t.TempDir()
	path := filepath.Join(workdir, "main.go")
	if err := os.WriteFile(path, []byte("package main\nfunc broken( {\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := tool{}.Execute(t.Context(), tools.Runtime{Workdir: workdir}, tools.Request{
		Tool: domain.ToolKindLint,
		Args: map[string]string{"path": "main.go", "mode": "syntax"},
	})
	if err != nil {
		t.Fatalf("execute lint: %v", err)
	}
	if !strings.Contains(result.Output, "Diagnostics for main.go") || !strings.Contains(result.Output, "syntax: main.go") {
		t.Fatalf("expected syntax diagnostics, got %q", result.Output)
	}
	stored, ok := result.Stored.(tools.LintStoredResult)
	if !ok || len(stored.DiagnosticReport.Diagnostics) != 1 {
		t.Fatalf("expected structured diagnostics, got %#v", result.Stored)
	}
}

func TestNormalizeRejectsInvalidMode(t *testing.T) {
	_, err := tool{}.NormalizeArgs(map[string]string{"path": "main.go", "mode": "project"})
	if err == nil {
		t.Fatal("expected invalid mode error")
	}
}
