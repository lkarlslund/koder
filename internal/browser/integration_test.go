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
		_, _ = w.Write([]byte(`<!doctype html><title>Browser test</title><button id="button" onclick="document.querySelector('output').textContent='clicked'">Run</button><a href="/download">Download</a><output>waiting</output>`))
	}))
	defer server.Close()

	m := NewManager(config.Browser{Enabled: true, Headed: false, OperationTimeout: 15 * time.Second, MaxTabsPerChat: 4, MaxTabsGlobal: 8}, t.TempDir())
	t.Cleanup(func() { _ = m.Stop(t.Context()) })
	chat := browserapi.Chat{SessionID: id.ID("session"), ChatID: id.ID("chat")}
	if err := m.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Logf("browser context error after start: %v", m.browserCtx.Err())
	tab, err := m.NewTab(t.Context(), chat, server.URL)
	if err != nil {
		t.Fatalf("new tab: %v", err)
	}
	if tab.URL != server.URL+"/" {
		t.Fatalf("unexpected tab URL: %s", tab.URL)
	}
	snapshot, err := m.Snapshot(t.Context(), chat, "Run", 4, 32*1024)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if !strings.Contains(snapshot.Text, "Run") || !strings.Contains(snapshot.Text, "[") {
		t.Fatalf("unexpected snapshot: %s", snapshot.Text)
	}
	start := strings.Index(snapshot.Text, "[") + 1
	end := strings.Index(snapshot.Text[start:], "]") + start
	if start <= 0 || end < start {
		t.Fatalf("snapshot has no ref: %s", snapshot.Text)
	}
	if err := m.Interact(t.Context(), chat, "click", snapshot.Text[start:end], ""); err != nil {
		t.Fatalf("click: %v", err)
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
	downloadSnapshot, err := m.Find(t.Context(), chat, "Download", "", 8*1024)
	if err != nil {
		t.Fatalf("find download: %v", err)
	}
	downloadRef := snapshotRef(t, downloadSnapshot.Text)
	if err := m.Interact(t.Context(), chat, "click", downloadRef, ""); err != nil {
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
	shot, err := m.Screenshot(t.Context(), chat, "", false, "png", 90)
	if err != nil || len(shot.Data) < 100 || shot.MIME != "image/png" {
		t.Fatalf("unexpected screenshot: %d bytes %s, %v", len(shot.Data), shot.MIME, err)
	}
}

func snapshotRef(t *testing.T, snapshot string) string {
	t.Helper()
	start := strings.Index(snapshot, "[") + 1
	end := strings.Index(snapshot[start:], "]") + start
	if start <= 0 || end < start {
		t.Fatalf("snapshot has no ref: %s", snapshot)
	}
	return snapshot[start:end]
}
