package showimagetool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/tools"
)

func TestNormalizeArgsRequiresPath(t *testing.T) {
	if _, err := (tool{}).NormalizeArgs(map[string]string{}); err == nil {
		t.Fatal("expected empty path error")
	}
	got, err := (tool{}).NormalizeArgs(map[string]string{"file_path": "images/screen.png"})
	if err != nil {
		t.Fatal(err)
	}
	if got["path"] != filepath.Join("images", "screen.png") {
		t.Fatalf("unexpected normalized path %#v", got)
	}
}

func TestExecuteAcceptsImageAndStoresRenderablePath(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, "screen.png")
	if err := os.WriteFile(target, []byte("\x89PNG\r\n\x1a\nfake"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := tool{}.Execute(context.Background(), tools.Runtime{Workdir: workspace}, tools.Request{
		Args: map[string]string{"path": "screen.png"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Meta["mime_type"]; got != "image/png" {
		t.Fatalf("expected image/png mime type, got %q", got)
	}
	stored, ok := result.Stored.(tools.ShowImageStoredResult)
	if !ok {
		t.Fatalf("expected show image stored result, got %#v", result.Stored)
	}
	if stored.Path != "screen.png" || stored.SourcePath == "" {
		t.Fatalf("unexpected stored result %#v", stored)
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
