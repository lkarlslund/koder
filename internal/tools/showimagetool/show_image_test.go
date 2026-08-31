package showimagetool

import (
	"bytes"
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

func TestNormalizeArgsRequiresPath(t *testing.T) {
	if _, err := (mediaTool{}).NormalizeArgs(map[string]string{}); err == nil {
		t.Fatal("expected empty path error")
	}
	got, err := (mediaTool{}).NormalizeArgs(map[string]string{"path": "media/demo.mp4", "title": " Demo "})
	if err != nil {
		t.Fatal(err)
	}
	if got["path"] != filepath.Join("media", "demo.mp4") || got["title"] != "Demo" {
		t.Fatalf("unexpected normalized arguments %#v", got)
	}
}

func TestShowMediaIsExposedAndShowImageIsLegacy(t *testing.T) {
	if !tools.Info(tools.ShowMedia).ExposeToLLM {
		t.Fatal("show_media should be exposed")
	}
	if tools.Info(tools.ShowImage).ExposeToLLM {
		t.Fatal("show_image should be hidden from the model")
	}
}

func TestExecuteAcceptsImageAudioAndVideo(t *testing.T) {
	tests := []struct {
		name string
		file string
		data []byte
		kind attachment.Kind
	}{
		{name: "image", file: "screen.png", data: []byte("\x89PNG\r\n\x1a\nfake"), kind: attachment.KindImage},
		{name: "audio", file: "sound.mp3", data: []byte("not-real-audio"), kind: attachment.KindAudio},
		{name: "video", file: "demo.mp4", data: []byte("not-real-video"), kind: attachment.KindVideo},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workspace := t.TempDir()
			target := filepath.Join(workspace, tt.file)
			if err := os.WriteFile(target, tt.data, 0o644); err != nil {
				t.Fatal(err)
			}
			manager := attachment.NewManager(t.TempDir())
			result, err := (mediaTool{}).Call(context.Background(), tools.Options{Runtime: tools.Runtime{Workdir: workspace, SessionID: "session-1", Attachments: manager}, Request: tools.Request{Args: map[string]string{"path": tt.file, "title": "Demo"}}})
			if err != nil {
				t.Fatal(err)
			}
			stored, ok := result.Stored.(tools.ShowMediaStoredResult)
			if !ok || stored.MediaKind != string(tt.kind) || stored.Attachment == nil || stored.SessionID != "session-1" {
				t.Fatalf("unexpected stored result %#v", result.Stored)
			}
			if stored.SourcePath != "" || stored.Attachment.Path != "" || stored.Attachment.Original != "" {
				t.Fatalf("durable stored result exposed a filesystem path: %#v", stored)
			}
			matches, err := filepath.Glob(filepath.Join(manager.SessionDir("session-1"), stored.Attachment.ID+".*"))
			if err != nil || len(matches) != 1 {
				t.Fatalf("locate durable media copy: %v, %v", matches, err)
			}
			if _, err := os.Stat(matches[0]); err != nil {
				t.Fatalf("durable media copy: %v", err)
			}
		})
	}
}

func TestLegacyShowImageRejectsAudio(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "sound.mp3"), []byte("audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := (legacyImageTool{}).Call(context.Background(), tools.Options{Runtime: tools.Runtime{Workdir: workspace, SessionID: id.New(), Attachments: attachment.NewManager(t.TempDir())}, Request: tools.Request{Args: map[string]string{"path": "sound.mp3"}}})
	if err == nil || !strings.Contains(err.Error(), "unsupported image type") {
		t.Fatalf("expected legacy image rejection, got %v", err)
	}
}

func TestExecuteRejectsNonMediaFile(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "note.txt"), []byte("plain text"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := (mediaTool{}).Call(context.Background(), tools.Options{Runtime: tools.Runtime{Workdir: workspace, SessionID: id.New(), Attachments: attachment.NewManager(t.TempDir())}, Request: tools.Request{Args: map[string]string{"path": "note.txt"}}})
	if err == nil || !strings.Contains(err.Error(), "unsupported media type") {
		t.Fatalf("expected non-media rejection, got %v", err)
	}
}

func TestExecuteDownloadsRemoteMediaIntoTimelineAttachment(t *testing.T) {
	var imageData bytes.Buffer
	photo := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	photo.SetNRGBA(0, 0, color.NRGBA{R: 0xff, A: 0xff})
	if err := png.Encode(&imageData, photo); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(imageData.Bytes())
	}))
	manager := attachment.NewManager(t.TempDir())
	sessionID := id.New()
	result, err := (mediaTool{}).Call(t.Context(), tools.Options{
		Runtime: tools.Runtime{HTTPClient: server.Client(), SessionID: sessionID, Attachments: manager},
		Request: tools.Request{Args: map[string]string{"path": server.URL + "/artwork", "title": "Artwork"}},
	})
	server.Close()
	if err != nil {
		t.Fatal(err)
	}
	stored, ok := result.Stored.(tools.ShowMediaStoredResult)
	if !ok || stored.Attachment == nil || stored.SourcePath != "" || stored.MIMEType != "image/png" || stored.Title != "Artwork" {
		t.Fatalf("remote media result = %#v", result.Stored)
	}
	if _, err := manager.SessionFile(sessionID, stored.Attachment.ID); err != nil {
		t.Fatalf("remote media was not retained after server closed: %v", err)
	}
}
