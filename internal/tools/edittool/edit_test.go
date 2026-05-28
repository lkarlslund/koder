package edittool

import (
	"context"
	"io/fs"
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

func TestExecuteEditInlineWhitespaceNormalized(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.go")
	if err := os.WriteFile(path, []byte("package main\n\nvar value = call(foo,\tbar)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := tool{}.Execute(context.Background(), tools.Runtime{Workdir: dir}, tools.Request{
		Args: map[string]string{
			"path":       "file.go",
			"old_string": "call(foo, bar)",
			"new_string": "call(foo, baz)",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Meta["matcher"] != "whitespace_normalized" {
		t.Fatalf("expected whitespace_normalized matcher, got %#v", result.Meta)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "package main\n\nvar value = call(foo, baz)\n" {
		t.Fatalf("unexpected file contents: %q", string(body))
	}
}

func TestExecuteEditInlineUnicodeNormalizedExpandedRune(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.go")
	if err := os.WriteFile(path, []byte("package main\n\nvar msg = \"a — b\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := tool{}.Execute(context.Background(), tools.Runtime{Workdir: dir}, tools.Request{
		Args: map[string]string{
			"path":       "file.go",
			"old_string": "\"a -- b\"",
			"new_string": "\"a - b\"",
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
	if string(body) != "package main\n\nvar msg = \"a - b\"\n" {
		t.Fatalf("unexpected file contents: %q", string(body))
	}
}

func TestExecuteEditReportsIntroducedGoDiagnostics(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc main() {\n\tprintln(\"ok\")\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := tool{}.Execute(context.Background(), tools.Runtime{Workdir: dir}, tools.Request{
		Args: map[string]string{
			"path":       "file.go",
			"old_string": "println(\"ok\")",
			"new_string": "println(",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "Diagnostics introduced by this edit") {
		t.Fatalf("expected diagnostics in output, got %q", result.Output)
	}
	stored, ok := result.Stored.(tools.EditStoredResult)
	if !ok {
		t.Fatalf("unexpected stored result type %T", result.Stored)
	}
	if !strings.Contains(stored.Diagnostics, "file.go") {
		t.Fatalf("expected stored diagnostics, got %#v", stored)
	}
}

func TestExecuteEditReportsStructuredFileDiagnostics(t *testing.T) {
	tests := []struct {
		name      string
		file      string
		before    string
		oldString string
		newString string
	}{
		{
			name:      "json",
			file:      "data.json",
			before:    "{\"enabled\": true}\n",
			oldString: "true",
			newString: "}",
		},
		{
			name:      "toml",
			file:      "config.toml",
			before:    "enabled = true\n",
			oldString: "true",
			newString: "\"unterminated",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, tt.file)
			if err := os.WriteFile(path, []byte(tt.before), 0o644); err != nil {
				t.Fatal(err)
			}
			result, err := tool{}.Execute(context.Background(), tools.Runtime{Workdir: dir}, tools.Request{
				Args: map[string]string{
					"path":       tt.file,
					"old_string": tt.oldString,
					"new_string": tt.newString,
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			stored, ok := result.Stored.(tools.EditStoredResult)
			if !ok {
				t.Fatalf("unexpected stored result type %T", result.Stored)
			}
			if !strings.Contains(result.Output, "Diagnostics introduced by this edit") || !strings.Contains(stored.Diagnostics, tt.file) {
				t.Fatalf("expected diagnostics for %s, output=%q stored=%#v", tt.file, result.Output, stored)
			}
		})
	}
}

func TestExecuteEditVerifiesWritePersisted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldWriteTextFile := writeTextFile
	writeTextFile = func(string, string, fs.FileMode) error { return nil }
	t.Cleanup(func() { writeTextFile = oldWriteTextFile })

	_, err := tool{}.Execute(context.Background(), tools.Runtime{Workdir: dir}, tools.Request{
		Args: map[string]string{
			"path":       "file.txt",
			"old_string": "beta",
			"new_string": "gamma",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "post-write verification failed") {
		t.Fatalf("expected verification failure, got %v", err)
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
			"old_string": "func alpah() {\n\tmissing",
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
