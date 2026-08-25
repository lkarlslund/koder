package viewimagetool

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/id"
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
	writeTestPNG(t, target)

	result, err := tool{}.Call(context.Background(), tools.Options{Runtime: tools.Runtime{Workdir: workspace}, Request: tools.Request{
		Args: map[string]string{"path": "screen.png", "detail": "original"},
	}})
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

	_, err := tool{}.Call(context.Background(), tools.Options{Runtime: tools.Runtime{Workdir: workspace}, Request: tools.Request{
		Args: map[string]string{"path": "note.txt"},
	}})
	if err == nil || !strings.Contains(err.Error(), "unsupported image type") {
		t.Fatalf("expected non-image rejection, got %v", err)
	}
}

func TestExecuteReadsImageFromSessionTmp(t *testing.T) {
	runtime := tools.Runtime{Workdir: t.TempDir(), SessionID: id.New()}
	t.Cleanup(func() { _ = os.RemoveAll(runtime.SessionTmpDir()) })
	if err := os.MkdirAll(runtime.SessionTmpDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(runtime.SessionTmpDir(), "screen.png")
	writeTestPNG(t, target)

	result, err := tool{}.Call(t.Context(), tools.Options{
		Runtime: runtime,
		Request: tools.Request{Args: map[string]string{"path": "/tmp/screen.png"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Meta["path"] != "/tmp/screen.png" {
		t.Fatalf("path label = %q, want /tmp/screen.png", result.Meta["path"])
	}
	stored, ok := result.Stored.(tools.ViewImageStoredResult)
	if !ok || stored.SourcePath != target {
		t.Fatalf("stored image = %#v, want source %q", result.Stored, target)
	}
}

func writeTestPNG(t *testing.T, path string) {
	t.Helper()
	photo := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	photo.Set(0, 0, color.NRGBA{R: 0xff, G: 0x80, A: 0xff})
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, photo); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestExecuteRecognizesQEMUPortablePixmapWithPNGExtension(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, "screen.png")
	ppm := append([]byte("P6\n1 1\n255\n"), 0x20, 0x40, 0x80)
	if err := os.WriteFile(target, ppm, 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := tool{}.Call(t.Context(), tools.Options{
		Runtime: tools.Runtime{Workdir: workspace},
		Request: tools.Request{Args: map[string]string{"path": "screen.png"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	stored, ok := result.Stored.(tools.ViewImageStoredResult)
	if !ok || stored.MIMEType != "image/png" {
		t.Fatalf("stored image = %#v, want normalized image/png", result.Stored)
	}
}
