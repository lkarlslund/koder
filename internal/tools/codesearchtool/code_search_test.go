package codesearchtool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/tools"
)

func TestExecuteFindsGoDeclarations(t *testing.T) {
	workdir := t.TempDir()
	writeFile(t, workdir, "pkg/sample.go", `package pkg

// Widget stores values.
type Widget struct{}

func NewWidget() Widget { return Widget{} }

func (w *Widget) Run(ctx string) error { return nil }
`)

	result, err := tool{}.Execute(context.Background(), tools.Runtime{Workdir: workdir}, tools.Request{
		Tool: domain.ToolKindCodeSearch,
		Args: map[string]string{"query": "Widget"},
	})
	if err != nil {
		t.Fatalf("execute code search: %v", err)
	}
	for _, want := range []string{
		"pkg/sample.go:4: struct Widget struct{} - Widget stores values.",
		"pkg/sample.go:6: function NewWidget func() Widget",
		"pkg/sample.go:8: method Widget.Run func(ctx string) error",
	} {
		if !strings.Contains(result.Output, want) {
			t.Fatalf("expected %q in output:\n%s", want, result.Output)
		}
	}
}

func TestExecuteFiltersByKindAndExactShortName(t *testing.T) {
	workdir := t.TempDir()
	writeFile(t, workdir, "pkg/sample.go", `package pkg

type Runner interface { Run() error }

type worker struct{}

func (worker) Run() error { return nil }
`)

	result, err := tool{}.Execute(context.Background(), tools.Runtime{Workdir: workdir}, tools.Request{
		Tool: domain.ToolKindCodeSearch,
		Args: map[string]string{
			"query": "Run",
			"kind":  "method",
			"exact": "true",
		},
	})
	if err != nil {
		t.Fatalf("execute code search: %v", err)
	}
	if !strings.Contains(result.Output, "method worker.Run") {
		t.Fatalf("expected exact short method match, got:\n%s", result.Output)
	}
	if strings.Contains(result.Output, "interface Runner") {
		t.Fatalf("did not expect interface when kind is method, got:\n%s", result.Output)
	}
}

func TestExecuteCanSearchSingleFilePath(t *testing.T) {
	workdir := t.TempDir()
	writeFile(t, workdir, "a.go", "package main\nfunc Target() {}\n")
	writeFile(t, workdir, "b.go", "package main\nfunc TargetOther() {}\n")

	result, err := tool{}.Execute(context.Background(), tools.Runtime{Workdir: workdir}, tools.Request{
		Tool: domain.ToolKindCodeSearch,
		Args: map[string]string{
			"query": "Target",
			"path":  "a.go",
		},
	})
	if err != nil {
		t.Fatalf("execute code search: %v", err)
	}
	if !strings.Contains(result.Output, "a.go:2: function Target") {
		t.Fatalf("expected a.go target, got:\n%s", result.Output)
	}
	if strings.Contains(result.Output, "TargetOther") {
		t.Fatalf("did not expect b.go result, got:\n%s", result.Output)
	}
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}
