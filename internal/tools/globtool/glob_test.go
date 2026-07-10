package globtool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/tools"
)

func TestGlobSupportsRecursivePatternsAndLimit(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "root.go"))
	mustWriteFile(t, filepath.Join(root, "cmd", "one.go"))
	mustWriteFile(t, filepath.Join(root, "internal", "app", "two.go"))
	mustWriteFile(t, filepath.Join(root, "internal", "app", "three.txt"))

	req, err := tools.Normalize(tools.Request{
		Tool: domain.ToolKindFileGlob,
		Args: map[string]string{
			"pattern": "**/*.go",
			"limit":   "1.00000",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := tool{}.Call(context.Background(), tools.Options{Runtime: tools.Runtime{Workdir: root}, Request: req})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(result.Output), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected limited output, got %q", result.Output)
	}
	if !strings.HasSuffix(lines[0], ".go") {
		t.Fatalf("expected go file match, got %q", lines[0])
	}
	if result.Meta["limit"] != "1" {
		t.Fatalf("expected normalized limit metadata, got %#v", result.Meta)
	}
}

func TestGlobSkipsUnreadableDirectories(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read chmod 000 directories")
	}
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "visible", "match.txt"))
	unreadable := filepath.Join(root, "blocked")
	mustWriteFile(t, filepath.Join(unreadable, "hidden.txt"))
	if err := os.Chmod(unreadable, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(unreadable, 0o755)
	})
	if _, err := os.ReadDir(unreadable); err == nil {
		t.Skip("test filesystem still allows reading chmod 000 directory")
	}

	req, err := tools.Normalize(tools.Request{
		Tool: domain.ToolKindFileGlob,
		Args: map[string]string{
			"pattern": "**/*.txt",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := tool{}.Call(context.Background(), tools.Options{Runtime: tools.Runtime{Workdir: root}, Request: req})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "visible/match.txt") {
		t.Fatalf("expected readable match, got %q", result.Output)
	}
	if !strings.Contains(result.Output, "skipped unreadable paths: blocked") {
		t.Fatalf("expected skipped path footer, got %q", result.Output)
	}
	if result.Meta["skipped"] != "1" {
		t.Fatalf("expected skipped metadata, got %#v", result.Meta)
	}
}

func TestGlobPatternToRegexp(t *testing.T) {
	matched, err := matchGlobPattern("internal/**/two.go", "internal/webui/two.go")
	if err != nil {
		t.Fatal(err)
	}
	if !matched {
		t.Fatal("expected recursive glob to match")
	}

	matched, err = matchGlobPattern("internal/**/two.go", "internal/two.go")
	if err != nil {
		t.Fatal(err)
	}
	if !matched {
		t.Fatal("expected recursive glob to match zero nested directories")
	}

	matched, err = matchGlobPattern("**/*", "go.mod")
	if err != nil {
		t.Fatal(err)
	}
	if !matched {
		t.Fatal("expected recursive glob to match root-level files")
	}
}

func mustWriteFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
