package tools

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/accesssettings"
)

func TestNormalizePathOrHTTPURL(t *testing.T) {
	got, err := NormalizePathOrHTTPURL(" https://example.test/a%20b.png ")
	if err != nil || got != "https://example.test/a%20b.png" {
		t.Fatalf("normalized URL = %q, %v", got, err)
	}
	if _, err := NormalizePathOrHTTPURL("https:///missing-host.png"); err == nil {
		t.Fatal("expected missing host rejection")
	}
}

func TestFetchRemoteMediaFollowsRedirectAndBoundsBody(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/start", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/asset", http.StatusFound)
	})
	mux.HandleFunc("/asset", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png; charset=binary")
		w.Header().Set("Content-Disposition", `inline; filename="result.png"`)
		_, _ = w.Write([]byte("data"))
	})
	mux.HandleFunc("/large", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "26214401")
		w.WriteHeader(http.StatusOK)
	})
	server := httptest.NewServer(mux)

	remote, err := FetchRemoteMedia(t.Context(), server.Client(), server.URL+"/start")
	if err != nil {
		t.Fatal(err)
	}
	if remote.URL != server.URL+"/asset" || remote.Name != "result.png" || remote.MIMEType != "image/png" || string(remote.Data) != "data" {
		t.Fatalf("unexpected remote media %#v", remote)
	}
	if _, err := FetchRemoteMedia(t.Context(), server.Client(), server.URL+"/large"); err == nil || !strings.Contains(err.Error(), "exceeds 25 MiB") {
		t.Fatalf("expected size rejection, got %v", err)
	}
}

func TestRemoteMediaUsesNetworkAccessPolicy(t *testing.T) {
	request := Request{Tool: ViewImage, Args: map[string]string{"path": "https://example.test/image.png"}}
	if err := checkRuntimeAccess(Runtime{AccessSettings: accesssettings.Default()}, request); err != nil {
		t.Fatalf("network-enabled remote image access = %v", err)
	}
	if err := checkRuntimeAccess(Runtime{AccessSettings: accesssettings.LockedDown()}, request); err == nil {
		t.Fatal("expected network-disabled remote image access to be rejected")
	}

	request.Tool = ShowMedia
	if err := checkRuntimeAccess(Runtime{AccessSettings: accesssettings.LockedDown()}, request); err == nil {
		t.Fatal("expected network-disabled remote media access to be rejected")
	}
}
