package browser

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/browserapi"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/id"
)

func TestChromiumIntegration(t *testing.T) {
	if os.Getenv("KODER_BROWSER_TEST") == "" {
		t.Skip("set KODER_BROWSER_TEST=1 to run Chromium integration")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/history-one" || r.URL.Path == "/history-two" {
			_, _ = w.Write([]byte(`<!doctype html><title>` + strings.TrimPrefix(r.URL.Path, "/") + `</title><p>` + r.URL.Path + `</p>`))
			return
		}
		if r.URL.Path == "/data" {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("network-body"))
			return
		}
		if r.URL.Path == "/download" {
			w.Header().Set("Content-Disposition", `attachment; filename="browser-test.txt"`)
			_, _ = w.Write([]byte("download-body"))
			return
		}
		_, _ = w.Write([]byte(`<!doctype html><title>Browser test</title><button id="button" onclick="document.querySelector('output').textContent='clicked'">Run</button><a href="/download">Download</a><input type="file" aria-label="Upload file"><img alt="Photo" width="20" height="20" src="data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='20' height='20'%3E%3Crect width='20' height='20' fill='red'/%3E%3C/svg%3E"><output>waiting</output>`))
	}))
	defer server.Close()

	m := NewManager(config.Browser{Enabled: true, Headed: false, OperationTimeout: 15 * time.Second, MaxTabsPerChat: 4, MaxTabsGlobal: 8}, t.TempDir())
	t.Cleanup(func() { _ = m.Stop(t.Context()) })
	chat := browserapi.Chat{SessionID: id.ID("session"), ChatID: id.ID("chat")}
	tab, err := m.NewTab(t.Context(), chat, server.URL)
	if err != nil {
		t.Fatalf("new tab: %v", err)
	}
	if tab.URL != server.URL+"/" {
		t.Fatalf("unexpected tab URL: %s", tab.URL)
	}
	bounded, err := m.Snapshot(t.Context(), chat, "", -1, 50)
	if err != nil || !bounded.Truncated || len([]rune(bounded.Text)) > 50 {
		t.Fatalf("snapshot max_chars not enforced: %d chars, truncated=%v, %v", len([]rune(bounded.Text)), bounded.Truncated, err)
	}
	shallow, err := m.Snapshot(t.Context(), chat, "", 0, 32*1024)
	if err != nil || strings.Contains(shallow.Text, "\n") {
		t.Fatalf("snapshot depth=0 traversed descendants: %q, %v", shallow.Text, err)
	}
	tabs, err := m.Tabs(t.Context(), chat)
	if err != nil || len(tabs) != 1 || !tabs[0].Owned || !tabs[0].Selected {
		t.Fatalf("initial blank tab was not reused: %#v, %v", tabs, err)
	}
	visibility, err := m.Evaluate(t.Context(), chat, `document.visibilityState`)
	if err != nil || visibility != `"visible"` {
		t.Fatalf("new tab is not active: %s, %v", visibility, err)
	}
	snapshot, err := m.Snapshot(t.Context(), chat, "Run", 4, 32*1024)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if !strings.Contains(snapshot.Text, "Run") || strings.Contains(snapshot.Text, "data-koder-ref") {
		t.Fatalf("unexpected snapshot: %s", snapshot.Text)
	}
	if _, err := m.Find(t.Context(), chat, "Run", "button", 8*1024); err != nil {
		t.Fatalf("find informational button: %v", err)
	}
	if err := m.Interact(t.Context(), chat, "click", browserapi.Locator{Target: "Run", Role: "button", Exact: true}, ""); err != nil {
		t.Fatalf("click semantic target: %v", err)
	}
	value, err := m.Evaluate(t.Context(), chat, `document.querySelector('output').textContent`)
	if err != nil || value != `"clicked"` {
		t.Fatalf("unexpected output: %s, %v", value, err)
	}
	_, err = m.Evaluate(t.Context(), chat, `(()=>{console.warn('browser-test-console');return fetch('/data').then(r=>r.text())})()`)
	if err != nil {
		t.Fatalf("generate diagnostics: %v", err)
	}
	var records []browserapi.RequestRecord
	for range 20 {
		records, err = m.Requests(t.Context(), chat, 20)
		if err == nil && len(records) > 0 && records[len(records)-1].Finished {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if len(records) == 0 {
		t.Fatal("expected captured network records")
	}
	console, err := m.Console(t.Context(), chat, "warning", 20)
	if err != nil || len(console) == 0 || !strings.Contains(console[len(console)-1].Text, "browser-test-console") {
		t.Fatalf("unexpected console records: %#v, %v", console, err)
	}
	_, err = m.Find(t.Context(), chat, "Download", "link", 8*1024)
	if err != nil {
		t.Fatalf("find download: %v", err)
	}
	if err := m.Interact(t.Context(), chat, "click", browserapi.Locator{Target: "Download", Role: "link", Exact: true}, ""); err != nil {
		t.Fatalf("start download: %v", err)
	}
	var downloads []browserapi.DownloadRecord
	for range 40 {
		downloads, err = m.Downloads(t.Context(), chat)
		if err == nil && len(downloads) == 1 && downloads[0].State == "completed" {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if len(downloads) != 1 || downloads[0].State != "completed" {
		t.Fatalf("unexpected downloads: %#v, %v", downloads, err)
	}
	download, err := m.Download(t.Context(), chat, downloads[0].ID)
	if err != nil || string(download.Data) != "download-body" || download.Name != "browser-test.txt" {
		t.Fatalf("unexpected download: %#v, %v", download, err)
	}
	_, err = m.Find(t.Context(), chat, "Upload file", "input", 8*1024)
	if err != nil {
		t.Fatalf("find upload: %v", err)
	}
	uploadPath := t.TempDir() + "/upload.txt"
	if err := os.WriteFile(uploadPath, []byte("upload-body"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.Upload(t.Context(), chat, browserapi.Locator{Target: "Upload file", Exact: true}, []string{uploadPath}); err != nil {
		t.Fatalf("upload: %v", err)
	}
	uploadSize, err := m.Evaluate(t.Context(), chat, `document.querySelector('input[type=file]').files[0].size`)
	if err != nil || uploadSize != "11" {
		t.Fatalf("unexpected upload size: %s, %v", uploadSize, err)
	}
	shot, err := m.Screenshot(t.Context(), chat, browserapi.Locator{}, false, "png", 90)
	if err != nil || len(shot.Data) < 100 || shot.MIME != "image/png" {
		t.Fatalf("unexpected screenshot: %d bytes %s, %v", len(shot.Data), shot.MIME, err)
	}
	_, err = m.Find(t.Context(), chat, "Photo", "image", 8*1024)
	if err != nil {
		t.Fatalf("find image: %v", err)
	}
	if _, err := m.Evaluate(t.Context(), chat, `document.querySelector('img').style.visibility='hidden'`); err != nil {
		t.Fatalf("hide referenced image: %v", err)
	}
	elementShot, err := m.Screenshot(t.Context(), chat, browserapi.Locator{Target: "Photo", Role: "image", Exact: true}, false, "png", 90)
	if err != nil || len(elementShot.Data) == 0 || elementShot.MIME != "image/png" {
		t.Fatalf("unexpected direct element screenshot: %d bytes %s, %v", len(elementShot.Data), elementShot.MIME, err)
	}
	if _, err := m.Navigate(t.Context(), chat, server.URL+"/history-one", "load"); err != nil {
		t.Fatalf("navigate first history page: %v", err)
	}
	if _, err := m.Navigate(t.Context(), chat, server.URL+"/history-two", "load"); err != nil {
		t.Fatalf("navigate second history page: %v", err)
	}
	started := time.Now()
	back, err := m.History(t.Context(), chat, "back")
	if err != nil || back.URL != server.URL+"/history-one" || time.Since(started) > 5*time.Second {
		t.Fatalf("unexpected browser back result: %#v, %v", back, err)
	}
	forward, err := m.History(t.Context(), chat, "forward")
	if err != nil || forward.URL != server.URL+"/history-two" {
		t.Fatalf("unexpected browser forward result: %#v, %v", forward, err)
	}
	reloaded, err := m.History(t.Context(), chat, "reload")
	if err != nil || reloaded.URL != server.URL+"/history-two" {
		t.Fatalf("unexpected browser reload result: %#v, %v", reloaded, err)
	}
	second, err := m.NewTab(t.Context(), chat, server.URL)
	if err != nil {
		t.Fatalf("new second tab: %v", err)
	}
	if second.URL != server.URL+"/" {
		t.Fatalf("new tab returned stale URL: %s", second.URL)
	}
	visibility, err = m.Evaluate(t.Context(), chat, `document.visibilityState`)
	if err != nil || visibility != `"visible"` {
		t.Fatalf("second tab is not active: %s, %v", visibility, err)
	}
	if _, err := m.SelectTab(t.Context(), chat, tab.ID); err != nil {
		t.Fatalf("select first tab: %v", err)
	}
	visibility, err = m.Evaluate(t.Context(), chat, `document.visibilityState`)
	if err != nil || visibility != `"visible"` {
		t.Fatalf("selected tab is not active after %s: %s, %v", second.ID, visibility, err)
	}
	if _, err := m.Navigate(t.Context(), chat, "http://127.0.0.1:1/unreachable", "load"); err == nil {
		t.Fatal("failed navigation unexpectedly succeeded")
	}
	owned, err := m.ownedTab(chat, tab.ID)
	if err != nil {
		t.Fatalf("get selected tab after failed navigation: %v", err)
	}
	current, err := m.tabInfo(t.Context(), chat, owned)
	if err != nil || current.URL != server.URL+"/history-two" {
		t.Fatalf("failed navigation changed tab state: %#v, %v", current, err)
	}
	if err := m.CloseTab(t.Context(), chat, second.ID); err != nil {
		t.Fatalf("close tab: %v", err)
	}
	tabs, err = m.Tabs(t.Context(), chat)
	if err != nil || len(tabs) != 1 || tabs[0].ID != tab.ID {
		t.Fatalf("closed tab remains listed: %#v, %v", tabs, err)
	}
}
