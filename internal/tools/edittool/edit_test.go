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
	if err == nil || !strings.Contains(err.Error(), "replace_all") {
		t.Fatalf("expected replace_all guidance, got %v", err)
	}
}

func TestExecuteEditLineEndings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("alpha\r\nbeta\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := tool{}.Execute(context.Background(), tools.Runtime{Workdir: dir}, tools.Request{
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

func TestExecuteEditUnicodeNormalized(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("title = “hello”\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := tool{}.Execute(context.Background(), tools.Runtime{Workdir: dir}, tools.Request{
		Args: map[string]string{
			"path":       "file.txt",
			"old_string": "title = \"hello\"\n",
			"new_string": "title = \"bye\"\n",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Meta["matcher"] != "unicode_normalized" {
		t.Fatalf("expected unicode_normalized matcher, got %#v", result.Meta)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "title = \"bye\"\n" {
		t.Fatalf("unexpected file contents: %q", string(body))
	}
}

func TestExecuteEditIndentationFlexible(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("func demo() {\n    beta\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := tool{}.Execute(context.Background(), tools.Runtime{Workdir: dir}, tools.Request{
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

func TestExecuteEditContextAware(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	content := "start\nalpha\none\ntwo changed\nomega\nend\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := tool{}.Execute(context.Background(), tools.Runtime{Workdir: dir}, tools.Request{
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

func TestExecuteEditTabsVsSpaces(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.go")
	content := "type Handler struct {\n\tcfg *config.Config\n\ttmpl *template.Template\n}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := tool{}.Execute(context.Background(), tools.Runtime{Workdir: dir}, tools.Request{
		Args: map[string]string{
			"path":       "file.go",
			"old_string": "type Handler struct {\n    cfg *config.Config\n    tmpl *template.Template\n}",
			"new_string": "type Handler struct {\n\tcfg *config.Config\n\ttmpl map[string]*template.Template\n}",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Meta["matcher"] != "line_trimmed" && result.Meta["matcher"] != "whitespace_normalized" && result.Meta["matcher"] != "indentation_flexible" {
		t.Fatalf("expected whitespace-tolerant matcher, got %#v", result.Meta)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "type Handler struct {\n\tcfg *config.Config\n\ttmpl map[string]*template.Template\n}\n" {
		t.Fatalf("unexpected file contents: %q", string(body))
	}
}

func TestExecuteEditReplaceAllFuzzyMatches(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("foo\tbar\nfoo  bar\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := tool{}.Execute(context.Background(), tools.Runtime{Workdir: dir}, tools.Request{
		Args: map[string]string{
			"path":        "file.txt",
			"old_string":  "foo bar",
			"new_string":  "baz",
			"replace_all": "true",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Meta["occurrences"] != "2" {
		t.Fatalf("expected two fuzzy replacements, got %#v", result.Meta)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "baz\nbaz\n" {
		t.Fatalf("unexpected file contents: %q", string(body))
	}
}

func TestExecuteEditNoMatchShowsClosestSections(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.go")
	if err := os.WriteFile(path, []byte("func alpha() {\n\treturn\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := tool{}.Execute(context.Background(), tools.Runtime{Workdir: dir}, tools.Request{
		Args: map[string]string{
			"path":       "file.go",
			"old_string": "func alpah() {\n\treturn\n}",
			"new_string": "func beta() {\n\treturn\n}",
		},
	})
	if err == nil {
		t.Fatal("expected edit failure")
	}
	msg := err.Error()
	if !strings.Contains(msg, "Did you mean one of these sections?") || !strings.Contains(msg, "   1| func alpha()") {
		t.Fatalf("expected closest-section feedback, got %v", err)
	}
}
