package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/lkarlslund/koder/internal/app"
)

// Adding a provider asks only for its URL and an optional key; Test probes
// the server, Save keeps what it found, and the list's Test probes again.
func TestProviderEditorAsksOnlyForURLAndKey(t *testing.T) {
	chromium := chromiumForTest(t)
	var modelLists atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			modelLists.Add(1)
			_, _ = w.Write([]byte(`{"data":[{"id":"Probe-Model","owned_by":"someone"}]}`))
		case "/v1/chat/completions":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["stream"] == true {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte("data: {\"prompt_progress\":{\"total\":4,\"processed\":4}}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"p\"}}]}\n\ndata: [DONE]\n\n"))
				return
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"pong"}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	ctrl := newTestController(t)
	chat := selectedTestState(t, ctrl)
	serverCtx, stopServer := context.WithCancel(context.Background())
	server := startBrowserTestServer(t, serverCtx, ctrl)
	t.Cleanup(func() {
		stopServer()
		_ = server.server.Close()
	})
	allocatorCtx, stopAllocator := chromedp.NewExecAllocator(context.Background(), append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(chromium),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
	)...)
	t.Cleanup(stopAllocator)
	browserCtx, stopBrowser := chromedp.NewContext(allocatorCtx)
	t.Cleanup(stopBrowser)
	browserCtx, cancel := context.WithTimeout(browserCtx, 60*time.Second)
	t.Cleanup(cancel)

	const sidebarModel = `(() => { const el = document.querySelector('[data-sidebar-model]'); const p = el.querySelector('.model-trigger-provider'); return el.querySelector('.model-trigger-name').textContent + '|' + (p.offsetParent ? p.textContent : ''); })()`
	var sidebar string
	var inputs []string
	var saveDisabled bool
	if err := chromedp.Run(browserCtx,
		chromedp.EmulateViewport(1440, 1000),
		chromedp.Navigate(server.URL()+"/s/"+string(chat.Session.ID)+"/c/"+string(chat.ActiveChatID)),
		chromedp.Poll(`document.documentElement._x_dataStack?.[0]?.activeModel() === 'model'`, nil, chromedp.WithPollingTimeout(10*time.Second)),
		chromedp.Evaluate(sidebarModel, &sidebar),
		// The settings button is on the welcome page; a chat page opens
		// settings from its menu.
		chromedp.Evaluate(`document.documentElement._x_dataStack[0].openSettingsDialog('integrations')`, nil),
		chromedp.Click(`[aria-label="Add provider"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`#settings-provider-url`, chromedp.ByQuery),
		chromedp.Evaluate(`Array.from(document.querySelectorAll('.settings-item-dialog input')).filter(el => el.offsetParent).map(el => el.id)`, &inputs),
		chromedp.Evaluate(`document.querySelector('[data-provider-save]').disabled`, &saveDisabled),
	); err != nil {
		t.Fatalf("open provider editor: %v", err)
	}
	if !slices.Equal(inputs, []string{"settings-provider-url", "settings-provider-api-key"}) {
		t.Fatalf("provider editor inputs = %v, want only the URL and API key", inputs)
	}
	if !saveDisabled {
		t.Fatal("Save was enabled before the server was probed")
	}

	// With one provider the sidebar names the model alone.
	if sidebar != "model|" {
		t.Errorf("sidebar model with one provider = %q, want the model alone", sidebar)
	}

	// A new provider is named after its address; the name can be changed
	// before saving and afterwards.
	host := strings.TrimPrefix(upstream.URL, "http://")
	id := strings.NewReplacer(".", "-", ":", "-").Replace(host)
	setName := func(name string) chromedp.Action {
		return chromedp.Evaluate(fmt.Sprintf(`(() => { const el = document.querySelector('#settings-provider-name'); el.value = %q; el.dispatchEvent(new Event('input', {bubbles: true})); })()`, name), nil)
	}
	savedName := func() string {
		for _, item := range ctrl.Providers().Providers {
			if item.ID == id {
				return item.Name
			}
		}
		return ""
	}

	if err := chromedp.Run(browserCtx,
		chromedp.SendKeys(`#settings-provider-url`, upstream.URL, chromedp.ByQuery),
		chromedp.Click(`[data-provider-test]`, chromedp.ByQuery),
		chromedp.Poll(`!document.querySelector('[data-provider-save]').disabled`, nil),
		chromedp.WaitVisible(`#settings-provider-name`, chromedp.ByQuery),
		chromedp.Poll(fmt.Sprintf(`document.querySelector('#settings-provider-name').value === %q`, host), nil),
		setName("Lab GPU"),
		chromedp.Click(`[data-provider-save]`, chromedp.ByQuery),
		chromedp.Poll(fmt.Sprintf(`!!document.querySelector('[data-provider-reprobe=%q]')`, id), nil),
		chromedp.WaitNotVisible(`#settings-provider-url`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("test and save provider: %v", err)
	}
	if got := savedName(); got != "Lab GPU" {
		t.Errorf("saved provider name = %q, want the one typed", got)
	}
	if err := chromedp.Run(browserCtx,
		chromedp.Evaluate(fmt.Sprintf(`document.querySelector('[data-provider-reprobe=%q]').parentElement.querySelector('.bi-sliders').closest('button').click()`, id), nil),
		chromedp.WaitVisible(`#settings-provider-name`, chromedp.ByQuery),
		chromedp.Poll(`document.querySelector('#settings-provider-name').value === 'Lab GPU'`, nil),
		setName("Renamed GPU"),
		chromedp.Click(`[data-provider-save]`, chromedp.ByQuery),
		chromedp.WaitNotVisible(`#settings-provider-url`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("rename provider: %v", err)
	}
	if got := savedName(); got != "Renamed GPU" {
		t.Errorf("renamed provider name = %q, want %q", got, "Renamed GPU")
	}

	// With two providers the sidebar adds the provider after the model, and
	// a long provider name gives way before the model's name does.
	if err := chromedp.Run(browserCtx,
		chromedp.Poll(`document.documentElement._x_dataStack[0].activeModelInfo().show_provider === true`, nil),
		chromedp.Evaluate(sidebarModel, &sidebar),
	); err != nil {
		t.Fatalf("sidebar with two providers: %v", err)
	}
	if !strings.HasPrefix(sidebar, "model|") || sidebar == "model|" {
		t.Errorf("sidebar model with two providers = %q, want the model then its provider", sidebar)
	}
	var fits []bool
	if err := chromedp.Run(browserCtx,
		chromedp.Evaluate(`(() => {
			const app = document.documentElement._x_dataStack[0];
			app.showSettings = false;
			app.state.model_info = Object.assign({}, app.activeModelInfo(), {model_id: 'qwen3.6-27b-q8', provider_label: 'A provider with a very long name that does not fit', show_provider: true});
		})()`, nil),
		chromedp.Sleep(100*time.Millisecond),
		// Text width comes from a Range: scrollWidth rounds, and misses an
		// ellipsis that hides a fraction of a pixel.
		chromedp.Evaluate(`(() => {
			const textWidth = el => { const r = document.createRange(); r.selectNodeContents(el); return r.getBoundingClientRect().width; };
			const el = document.querySelector('[data-sidebar-model]');
			const n = el.querySelector('.model-trigger-name');
			const p = el.querySelector('.model-trigger-provider');
			return [textWidth(n) <= n.getBoundingClientRect().width, textWidth(p) > p.getBoundingClientRect().width, el.scrollWidth <= el.clientWidth];
		})()`, &fits),
	); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(fits, []bool{true, true, true}) {
		t.Errorf("long provider name: model fits, provider truncated, row fits = %v, want all true", fits)
	}
	if dir := os.Getenv("KODER_UI_SHOTS"); dir != "" {
		for _, width := range []int64{1440, 820, 420} {
			var shot []byte
			if err := chromedp.Run(browserCtx, chromedp.EmulateViewport(width, 900), chromedp.Sleep(250*time.Millisecond), chromedp.CaptureScreenshot(&shot)); err != nil {
				t.Fatal(err)
			}
			_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("sidebar-model-%d.png", width)), shot, 0o644)
		}
		var shot []byte
		if err := chromedp.Run(browserCtx,
			chromedp.EmulateViewport(1440, 900),
			chromedp.Evaluate(`document.documentElement._x_dataStack[0].openSettingsDialog('integrations')`, nil),
			chromedp.WaitVisible(fmt.Sprintf(`[data-provider-reprobe=%q]`, id), chromedp.ByQuery),
			chromedp.Evaluate(fmt.Sprintf(`document.querySelector('[data-provider-reprobe=%q]').parentElement.querySelector('.bi-sliders').closest('button').click()`, id), nil),
			chromedp.WaitVisible(`#settings-provider-name`, chromedp.ByQuery),
			chromedp.Sleep(250*time.Millisecond),
			chromedp.CaptureScreenshot(&shot),
			chromedp.Evaluate(`document.documentElement._x_dataStack[0].closeProviderEditor()`, nil),
		); err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(filepath.Join(dir, "provider-name-1440.png"), shot, 0o644)
	}

	state := ctrl.Providers()
	idx := slices.IndexFunc(state.Providers, func(p app.ProviderConfigItem) bool { return p.ID == id })
	if idx < 0 || state.Providers[idx].BaseURL != upstream.URL+"/v1" || !state.Providers[idx].PromptProgressSupported {
		t.Fatalf("saved providers = %+v", state.Providers)
	}

	before := modelLists.Load()
	if err := chromedp.Run(browserCtx,
		chromedp.Evaluate(`document.documentElement._x_dataStack[0].openSettingsDialog('integrations')`, nil),
		chromedp.Click(fmt.Sprintf(`[data-provider-reprobe=%q]`, id), chromedp.ByQuery)); err != nil {
		t.Fatalf("reprobe provider: %v", err)
	}
	for deadline := time.Now().Add(10 * time.Second); modelLists.Load() <= before; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the list's Test did not probe the server again")
		}
	}
}
