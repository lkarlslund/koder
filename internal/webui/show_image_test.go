package webui

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestShowImageEndpointServesLocalImage(t *testing.T) {
	ctrl := newTestController(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	path := filepath.Join(t.TempDir(), "screen.png")
	if err := os.WriteFile(path, []byte("\x89PNG\r\n\x1a\nfake"), 0o644); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(srv.URL() + "/api/show-image?path=" + url.QueryEscape(path))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %s", resp.Status)
	}
	if got := resp.Header.Get("Content-Type"); got != "image/png" {
		t.Fatalf("expected image/png content type, got %q", got)
	}
}

func TestShowImageEndpointRejectsNonImage(t *testing.T) {
	ctrl := newTestController(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	path := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(path, []byte("plain text"), 0o644); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(srv.URL() + "/api/show-image?path=" + url.QueryEscape(path))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("expected 415, got %s", resp.Status)
	}
}
