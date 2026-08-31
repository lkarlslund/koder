package viewimagetool

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/attachment"
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

func TestExecuteAcceptsImageAndStoresDurableAttachment(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, "screen.png")
	writeTestPNG(t, target)
	manager := attachment.NewManager(t.TempDir())
	sessionID := id.New()

	result, err := tool{}.Call(context.Background(), tools.Options{Runtime: tools.Runtime{Workdir: workspace, SessionID: sessionID, Attachments: manager}, Request: tools.Request{
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
	if stored.SourcePath != "" || stored.Attachment == nil || stored.SessionID != string(sessionID) {
		t.Fatalf("expected durable attachment without source path, got %#v", stored)
	}
	if stored.Detail != "original" {
		t.Fatalf("expected original detail, got %#v", stored)
	}
	if _, err := manager.SessionFile(sessionID, stored.Attachment.ID); err != nil {
		t.Fatalf("resolve durable image: %v", err)
	}
}

func TestExecuteRejectsNonImageFile(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, "note.txt")
	if err := os.WriteFile(target, []byte("plain text"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := tool{}.Call(context.Background(), tools.Options{Runtime: tools.Runtime{Workdir: workspace, SessionID: id.New(), Attachments: attachment.NewManager(t.TempDir())}, Request: tools.Request{
		Args: map[string]string{"path": "note.txt"},
	}})
	if err == nil || !strings.Contains(err.Error(), "unsupported image type") {
		t.Fatalf("expected non-image rejection, got %v", err)
	}
}

func TestExecuteReadsImageFromSessionTmp(t *testing.T) {
	runtime := tools.Runtime{Workdir: t.TempDir(), SessionID: id.New(), Attachments: attachment.NewManager(t.TempDir())}
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
	if !ok || stored.SourcePath != "" || stored.Attachment == nil {
		t.Fatalf("stored image = %#v, want durable attachment", result.Stored)
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
		Runtime: tools.Runtime{Workdir: workspace, SessionID: id.New(), Attachments: attachment.NewManager(t.TempDir())},
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

func TestExecuteDownloadsAndPersistsRemoteImage(t *testing.T) {
	imagePath := filepath.Join(t.TempDir(), "source.png")
	writeTestPNG(t, imagePath)
	imageData, err := os.ReadFile(imagePath)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Disposition", `inline; filename="photo.png"`)
		_, _ = w.Write(imageData)
	}))
	manager := attachment.NewManager(t.TempDir())
	sessionID := id.New()
	result, err := tool{}.Call(t.Context(), tools.Options{
		Runtime: tools.Runtime{HTTPClient: server.Client(), SessionID: sessionID, Attachments: manager},
		Request: tools.Request{Args: map[string]string{"path": server.URL + "/photo"}},
	})
	server.Close()
	if err != nil {
		t.Fatal(err)
	}
	stored, ok := result.Stored.(tools.ViewImageStoredResult)
	if !ok || stored.Attachment == nil || stored.MIMEType != "image/png" || stored.Path != server.URL+"/photo" {
		t.Fatalf("remote stored result = %#v", result.Stored)
	}
	path, err := manager.SessionFile(sessionID, stored.Attachment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("remote image was not retained after server closed: %v", err)
	}
}
