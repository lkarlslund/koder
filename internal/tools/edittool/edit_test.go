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
