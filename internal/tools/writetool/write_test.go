package writetool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/tools"
)

func TestNormalizeArgsValidatesPath(t *testing.T) {
	if _, err := (tool{}).NormalizeArgs(map[string]string{}); err == nil {
		t.Fatal("expected empty path error")
	}
}

func TestExecuteCreatesFileWithoutForceOverwrite(t *testing.T) {
	dir := t.TempDir()
	req := tools.Request{Args: map[string]string{"path": "notes.txt", "content": "hello\n"}}
	result, err := tool{}.Execute(context.Background(), tools.Runtime{Workdir: dir}, req)
	if err != nil {
		t.Fatal(err)
	}
	if result.Meta["action"] != "created" {
		t.Fatalf("expected created action, got %#v", result.Meta)
	}
	body, err := os.ReadFile(filepath.Join(dir, "notes.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "hello\n" {
		t.Fatalf("unexpected file contents: %q", string(body))
	}
}

func TestExecuteRefusesExistingFileWithoutForceOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(path, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	req := tools.Request{Args: map[string]string{"path": "notes.txt", "content": "updated\n"}}
	_, err := tool{}.Execute(context.Background(), tools.Runtime{Workdir: dir}, req)
	if err == nil {
		t.Fatal("expected overwrite refusal")
	}
	for _, want := range []string{"force_overwrite=true", "Prefer the edit tool"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error to contain %q, got %v", want, err)
		}
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "hello\n" {
		t.Fatalf("unexpected file contents: %q", string(body))
	}
}

func TestExecuteRefusesExistingFileWithFalseForceOverwrite(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	req := tools.Request{Args: map[string]string{"path": "notes.txt", "content": "updated\n", "force_overwrite": "false"}}
	if _, err := (tool{}).Execute(context.Background(), tools.Runtime{Workdir: dir}, req); err == nil {
		t.Fatal("expected overwrite refusal")
	}
}

func TestExecuteOverwritesFileWithForceOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(path, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	req := tools.Request{Args: map[string]string{"path": "notes.txt", "content": "updated\n", "force_overwrite": "true"}}
	result, err := tool{}.Execute(context.Background(), tools.Runtime{Workdir: dir}, req)
	if err != nil {
		t.Fatal(err)
	}
	if result.Meta["action"] != "overwrote" {
		t.Fatalf("expected overwrite action, got %#v", result.Meta)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "updated\n" {
		t.Fatalf("unexpected file contents: %q", string(body))
	}
}

func TestExecuteReportsWrittenFileDiagnostics(t *testing.T) {
	dir := t.TempDir()
	req := tools.Request{Args: map[string]string{"path": "bad.json", "content": "{"}}
	result, err := tool{}.Execute(context.Background(), tools.Runtime{Workdir: dir}, req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "Problems detected after writing file") {
		t.Fatalf("expected diagnostics in output, got %q", result.Output)
	}
	stored, ok := result.Stored.(tools.WriteStoredResult)
	if !ok || !strings.Contains(stored.Diagnostics, "bad.json") {
		t.Fatalf("expected stored diagnostics, got %#v", result.Stored)
	}
}
