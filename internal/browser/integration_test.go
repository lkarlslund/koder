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
		if r.URL.Path == "/history-one" {
			_, _ = w.Write([]byte(`<!doctype html><title>history-one</title>
<label>History value: <input id="history-value"></label>
<button onclick="document.querySelector('output').textContent='history-clicked'">History action</button>
<input type="file" aria-label="History upload">
<img alt="History image" width="20" height="20" src="data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='20' height='20'%3E%3Crect width='20' height='20' fill='blue'/%3E%3C/svg%3E">
<output>waiting</output>`))
			return
		}
		if r.URL.Path == "/history-two" {
			_, _ = w.Write([]byte(`<!doctype html><title>history-two</title><p>/history-two</p>`))
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
		_, _ = w.Write([]byte(`<!doctype html><title>Browser test</title>
<button id="button" onclick="document.querySelector('output').textContent='clicked'">Run</button>
<a href="/download">Download</a>
<label>Customer name <input id="name" onkeydown="if(event.key==='Enter')document.querySelector('output').textContent='entered';if(event.key==='a'&&event.ctrlKey)document.querySelector('output').textContent='control-a'"></label>
<label><input id="terms" type="checkbox"> Accept terms</label>
<label>Pizza size: <select id="size"><option>Small</option><option>Large</option></select></label>
<button id="hover" onmouseover="document.querySelector('output').textContent='hovered'">Details</button>
<section aria-label="Alpha panel"><button onclick="document.querySelector('output').textContent='alpha'">Delete</button></section>
<section aria-label="Beta panel"><button onclick="document.querySelector('output').textContent='beta'">Delete</button></section>
<div aria-label="Drag source" draggable="true">Move me</div><div aria-label="Drop target" ondragover="event.preventDefault()" ondrop="document.querySelector('output').textContent='dropped'">Drop here</div>
<div aria-label="Scroll area" style="height:20px;overflow:auto"><div style="height:200px">Scrollable</div></div>
<input type="file" aria-label="Upload file">
<img alt="Photo" width="20" height="20" src="data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='20' height='20'%3E%3Crect width='20' height='20' fill='red'/%3E%3C/svg%3E">
<output>waiting</output><script>document.addEventListener('keydown',event=>{if(event.key==='F11')document.querySelector('output').textContent='global-f11'})</script>`))
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
	textbox, err := m.Find(t.Context(), chat, "Customer name", "textbox", 8*1024)
	if err != nil || !strings.Contains(textbox.Text, `textbox "Customer name"`) {
		t.Fatalf("informational snapshot omitted accessible label: %q, %v", textbox.Text, err)
	}
	if err := m.Interact(t.Context(), chat, "click", browserapi.Locator{Target: "Run", Role: "button", Exact: true}, ""); err != nil {
		t.Fatalf("click semantic target: %v", err)
	}
	value, err := m.Evaluate(t.Context(), chat, `document.querySelector('output').textContent`)
	if err != nil || value != `"clicked"` {
		t.Fatalf("unexpected output: %s, %v", value, err)
	}
	if _, err := m.Evaluate(t.Context(), chat, `document.querySelector('#button').outerHTML='<button id="button" onclick="document.querySelector(\'output\').textContent=\'rerendered\'">Run</button>'`); err != nil {
		t.Fatalf("replace semantic target: %v", err)
	}
	if err := m.Interact(t.Context(), chat, "click", browserapi.Locator{Target: "Run", Role: "button", Exact: true}, ""); err != nil {
		t.Fatalf("click rerendered semantic target: %v", err)
	}
	if value, err = m.Evaluate(t.Context(), chat, `document.querySelector('output').textContent`); err != nil || value != `"rerendered"` {
		t.Fatalf("semantic locator retained DOM state: %s, %v", value, err)
	}
	if value, err = m.Evaluate(t.Context(), chat, `document.querySelectorAll('[data-koder-ref]').length`); err != nil || value != "0" {
		t.Fatalf("browser snapshot mutated DOM: %s, %v", value, err)
	}
	if err := m.Interact(t.Context(), chat, "fill", browserapi.Locator{Target: "Customer name", Exact: true}, "Ada"); err != nil {
		t.Fatalf("fill semantic textbox: %v", err)
	}
	if err := m.Interact(t.Context(), chat, "type", browserapi.Locator{Target: "Customer name", Exact: true}, " Lovelace"); err != nil {
		t.Fatalf("type semantic textbox: %v", err)
	}
	if value, err = m.Evaluate(t.Context(), chat, `document.querySelector('#name').value`); err != nil || value != `"Ada Lovelace"` {
		t.Fatalf("unexpected semantic textbox value: %s, %v", value, err)
	}
	if err := m.Interact(t.Context(), chat, "press", browserapi.Locator{Target: "Customer name", Exact: true}, "Enter"); err != nil {
		t.Fatalf("press semantic textbox: %v", err)
	}
	if value, err = m.Evaluate(t.Context(), chat, `document.querySelector('output').textContent`); err != nil || value != `"entered"` {
		t.Fatalf("semantic key press did not dispatch Enter: %s, %v", value, err)
	}
	if err := m.Interact(t.Context(), chat, "press", browserapi.Locator{Target: "Customer name"}, "Control+a"); err != nil {
		t.Fatalf("press semantic key chord: %v", err)
	}
	if value, err = m.Evaluate(t.Context(), chat, `document.querySelector('output').textContent`); err != nil || value != `"control-a"` {
		t.Fatalf("semantic key chord was not dispatched: %s, %v", value, err)
	}
	if err := m.Interact(t.Context(), chat, "press", browserapi.Locator{}, "F11"); err != nil {
		t.Fatalf("global key press: %v", err)
	}
	if value, err = m.Evaluate(t.Context(), chat, `document.querySelector('output').textContent`); err != nil || value != `"global-f11"` {
		t.Fatalf("global key press was not dispatched: %s, %v", value, err)
	}
	if err := m.Interact(t.Context(), chat, "select", browserapi.Locator{Target: "Pizza size"}, "Large"); err != nil {
		t.Fatalf("select semantic control: %v", err)
	}
	if value, err = m.Evaluate(t.Context(), chat, `document.querySelector('#size').value`); err != nil || value != `"Large"` {
		t.Fatalf("semantic select did not change value: %s, %v", value, err)
	}
	if err := m.Interact(t.Context(), chat, "check", browserapi.Locator{Target: "Accept terms", Exact: true}, ""); err != nil {
		t.Fatalf("check semantic control: %v", err)
	}
	if err := m.Interact(t.Context(), chat, "uncheck", browserapi.Locator{Target: "Accept terms", Exact: true}, ""); err != nil {
		t.Fatalf("uncheck semantic control: %v", err)
	}
	if value, err = m.Evaluate(t.Context(), chat, `document.querySelector('#terms').checked`); err != nil || value != "false" {
		t.Fatalf("semantic uncheck did not clear control: %s, %v", value, err)
	}
	if err := m.Interact(t.Context(), chat, "hover", browserapi.Locator{Target: "Details"}, ""); err != nil {
		t.Fatalf("hover semantic target: %v", err)
	}
	if err := m.Interact(t.Context(), chat, "click", browserapi.Locator{Target: "Delete", Role: "button", Exact: true}, ""); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous semantic target error = %v", err)
	}
	if err := m.Interact(t.Context(), chat, "click", browserapi.Locator{Target: "Delete", Role: "button", Within: "Alpha panel", Exact: true}, ""); err != nil {
		t.Fatalf("click scoped semantic target: %v", err)
	}
	if err := m.Interact(t.Context(), chat, "click", browserapi.Locator{Target: "Delete", Role: "button", Exact: true, Occurrence: 2}, ""); err != nil {
		t.Fatalf("click semantic occurrence: %v", err)
	}
	if err := m.Interact(t.Context(), chat, "click", browserapi.Locator{Selector: "xpath=//button[@id='button']"}, ""); err != nil {
		t.Fatalf("click XPath selector: %v", err)
	}
	if err := m.Interact(t.Context(), chat, "click", browserapi.Locator{Selector: "#button"}, ""); err != nil {
		t.Fatalf("click CSS selector: %v", err)
	}
	if err := m.Drag(t.Context(), chat, browserapi.Locator{Target: "Drag source", Exact: true}, browserapi.Locator{Target: "Drop target", Exact: true}); err != nil {
		t.Fatalf("drag semantic targets: %v", err)
	}
	if value, err = m.Evaluate(t.Context(), chat, `document.querySelector('output').textContent`); err != nil || value != `"dropped"` {
		t.Fatalf("semantic drag did not dispatch drop: %s, %v", value, err)
	}
	if err := m.Scroll(t.Context(), chat, browserapi.Locator{Target: "Scroll area", Exact: true}, 0, 40); err != nil {
		t.Fatalf("scroll semantic target: %v", err)
	}
	if value, err = m.Evaluate(t.Context(), chat, `document.querySelector('[aria-label="Scroll area"]').scrollTop`); err != nil || value == "0" {
		t.Fatalf("semantic scroll did not move target: %s, %v", value, err)
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
	if err := m.Interact(t.Context(), chat, "fill", browserapi.Locator{Target: "History value"}, "restored"); err != nil {
		t.Fatalf("fill after browser back: %v", err)
	}
	if value, err = m.Evaluate(t.Context(), chat, `document.querySelector('#history-value').value`); err != nil || value != `"restored"` {
		t.Fatalf("fill after browser back did not update value: %s, %v", value, err)
	}
	if err := m.Interact(t.Context(), chat, "type", browserapi.Locator{Target: "History value"}, " value"); err != nil {
		t.Fatalf("type after browser back: %v", err)
	}
	if err := m.Interact(t.Context(), chat, "click", browserapi.Locator{Target: "History action"}, ""); err != nil {
		t.Fatalf("click after browser back: %v", err)
	}
	if value, err = m.Evaluate(t.Context(), chat, `document.querySelector('output').textContent`); err != nil || value != `"history-clicked"` {
		t.Fatalf("click after browser back did not run handler: %s, %v", value, err)
	}
	if _, err := m.Screenshot(t.Context(), chat, browserapi.Locator{Target: "History image"}, false, "png", 90); err != nil {
		t.Fatalf("element screenshot after browser back: %v", err)
	}
	if err := m.Upload(t.Context(), chat, browserapi.Locator{Target: "History upload"}, []string{uploadPath}); err != nil {
		t.Fatalf("upload after browser back: %v", err)
	}
	if value, err = m.Evaluate(t.Context(), chat, `document.querySelector('input[type=file]').files.length`); err != nil || value != "1" {
		t.Fatalf("upload after browser back did not select file: %s, %v", value, err)
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
