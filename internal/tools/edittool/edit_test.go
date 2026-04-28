package edittool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/tools"
)

func TestNormalizeArgsValidatesInputs(t *testing.T) {
	if _, err := (tool{}).NormalizeArgs(map[string]string{}); err == nil {
		t.Fatal("expected empty path error")
	}
	if _, err := (tool{}).NormalizeArgs(map[string]string{"path": "file.txt"}); err == nil {
		t.Fatal("expected empty old_string error")
	}
	if _, err := (tool{}).NormalizeArgs(map[string]string{"path": "file.txt", "old_string": "a", "new_string": "a"}); err == nil {
		t.Fatal("expected identical strings error")
	}
}

func TestExecuteEditsSingleOccurrence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := tool{}.Execute(context.Background(), tools.Runtime{Workdir: dir}, tools.Request{
		Args: map[string]string{
			"path":       "file.txt",
			"old_string": "beta",
			"new_string": "gamma",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "Edited file.txt") {
		t.Fatalf("unexpected summary: %q", result.Output)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "alpha\ngamma\n" {
		t.Fatalf("unexpected file contents: %q", string(body))
	}
}

func TestExecuteRejectsMultipleOccurrencesWithoutReplaceAll(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("beta\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := tool{}.Execute(context.Background(), tools.Runtime{Workdir: dir}, tools.Request{
		Args: map[string]string{
			"path":       "file.txt",
			"old_string": "beta",
			"new_string": "gamma",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "use replace_all") {
		t.Fatalf("expected replace_all guidance, got %v", err)
	}
}

func TestExecuteEditForgivenessLevel1StaysStrict(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("func demo() {\n    beta\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := tool{}.Execute(context.Background(), tools.Runtime{Workdir: dir, EditForgiveness: 1}, tools.Request{
		Args: map[string]string{
			"path":       "file.txt",
			"old_string": "func demo() {\nbeta\n}",
			"new_string": "func demo() {\ngamma\n}",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "target text not found") {
		t.Fatalf("expected strict level to reject indentation mismatch, got %v", err)
	}
}

func TestExecuteEditForgivenessLineEndings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("alpha\r\nbeta\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := tool{}.Execute(context.Background(), tools.Runtime{Workdir: dir, EditForgiveness: 2}, tools.Request{
		Args: map[string]string{
			"path":       "file.txt",
			"old_string": "alpha\nbeta\n",
			"new_string": "alpha\ngamma\n",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Meta["matcher"] != "line_endings" {
		t.Fatalf("expected line_endings matcher, got %#v", result.Meta)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "alpha\r\ngamma\r\n" {
		t.Fatalf("unexpected file contents: %q", string(body))
	}
}

func TestExecuteEditForgivenessQuotes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("title = “hello”\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := tool{}.Execute(context.Background(), tools.Runtime{Workdir: dir, EditForgiveness: 3}, tools.Request{
		Args: map[string]string{
			"path":       "file.txt",
			"old_string": "title = \"hello\"\n",
			"new_string": "title = \"bye\"\n",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Meta["matcher"] != "quotes" {
		t.Fatalf("expected quotes matcher, got %#v", result.Meta)
	}
}

func TestExecuteEditForgivenessIndentationFlexible(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("func demo() {\n    beta\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := tool{}.Execute(context.Background(), tools.Runtime{Workdir: dir, EditForgiveness: 4}, tools.Request{
		Args: map[string]string{
			"path":       "file.txt",
			"old_string": "func demo() {\nbeta\n}",
			"new_string": "func demo() {\ngamma\n}",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Meta["matcher"] != "indentation_flexible" && result.Meta["matcher"] != "line_trimmed" {
		t.Fatalf("expected indentation_flexible matcher, got %#v", result.Meta)
	}
}

func TestExecuteEditForgivenessContextAware(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	content := "start\nalpha\none\ntwo changed\nomega\nend\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := tool{}.Execute(context.Background(), tools.Runtime{Workdir: dir, EditForgiveness: 5}, tools.Request{
		Args: map[string]string{
			"path":       "file.txt",
			"old_string": "alpha\none\ntwo\nomega",
			"new_string": "alpha\none\ntwo final\nomega",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Meta["matcher"] != "block_anchor" && result.Meta["matcher"] != "context_aware" {
		t.Fatalf("expected forgiving matcher, got %#v", result.Meta)
	}
}
