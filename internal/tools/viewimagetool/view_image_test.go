package viewimagetool

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
	if _, err := (tool{}).NormalizeArgs(map[string]string{"path": "screen.png", "detail": "full"}); err == nil {
		t.Fatal("expected invalid detail error")
	}
}

func TestExecuteAcceptsImageAndStoresSourcePath(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, "screen.png")
	if err := os.WriteFile(target, []byte("\x89PNG\r\n\x1a\nfake"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := tool{}.Execute(context.Background(), tools.Runtime{Workdir: workspace}, tools.Request{
		Args: map[string]string{"path": "screen.png", "detail": "original"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Meta["path"]; got != "screen.png" {
		t.Fatalf("expected relative path metadata, got %q", got)
	}
	if got := result.Meta["mime_type"]; got != "image/png" {
		t.Fatalf("expected image/png mime type, got %q", got)
	}
	stored, ok := result.Stored.(tools.ViewImageStoredResult)
	if !ok {
		t.Fatalf("expected view image stored result, got %#v", result.Stored)
	}
	if stored.SourcePath != filepath.ToSlash(target) && stored.SourcePath != target {
		t.Fatalf("expected stored source path %q, got %q", target, stored.SourcePath)
	}
	if stored.Detail != "original" {
		t.Fatalf("expected original detail, got %#v", stored)
	}
}

func TestExecuteRejectsNonImageFile(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, "note.txt")
	if err := os.WriteFile(target, []byte("plain text"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := tool{}.Execute(context.Background(), tools.Runtime{Workdir: workspace}, tools.Request{
		Args: map[string]string{"path": "note.txt"},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported image type") {
		t.Fatalf("expected non-image rejection, got %v", err)
	}
}
