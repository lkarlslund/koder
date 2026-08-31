package tools

import (
	"encoding/json"
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

func TestNormalizeMediaArgsCollection(t *testing.T) {
	got, err := NormalizeMediaArgs(map[string]string{
		"title": " Results ",
		"items": `[{"path":" https://example.test/one.png ","title":" One "},{"path":"media/two.mp4"}]`,
	})
	if err != nil {
		t.Fatal(err)
	}
	items, err := MediaInputs(got)
	if err != nil {
		t.Fatal(err)
	}
	if got["title"] != "Results" || len(items) != 2 || items[0].Path != "https://example.test/one.png" || items[0].Title != "One" || items[1].Path != "media/two.mp4" {
		t.Fatalf("normalized media collection = %#v, items %#v", got, items)
	}
	if _, err := NormalizeMediaArgs(map[string]string{"path": "one.png", "items": `[{"path":"two.png"}]`}); err == nil {
		t.Fatal("expected path and items combination to be rejected")
	}
	tooMany := make([]MediaInput, MaxPresentedMediaItems+1)
	for index := range tooMany {
		tooMany[index].Path = "image.png"
	}
	encoded, err := json.Marshal(tooMany)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NormalizeMediaArgs(map[string]string{"items": string(encoded)}); err == nil {
		t.Fatal("expected oversized media collection to be rejected")
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
	request.Args = map[string]string{"items": `[{"path":"https://example.test/one.png"},{"path":"https://example.test/two.png"}]`}
	if err := checkRuntimeAccess(Runtime{AccessSettings: accesssettings.LockedDown()}, request); err == nil {
		t.Fatal("expected network-disabled remote media collection to be rejected")
	}
}
