package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
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

	var inputs []string
	var saveDisabled bool
	if err := chromedp.Run(browserCtx,
		chromedp.EmulateViewport(1440, 1000),
		chromedp.Navigate(server.URL()),
		chromedp.WaitVisible(`[data-settings-open]`, chromedp.ByQuery),
		chromedp.Click(`[data-settings-open]`, chromedp.ByQuery),
		chromedp.Click(`[data-settings-tab="integrations"]`, chromedp.ByQuery),
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

	if err := chromedp.Run(browserCtx,
		chromedp.SendKeys(`#settings-provider-url`, upstream.URL, chromedp.ByQuery),
		chromedp.Click(`[data-provider-test]`, chromedp.ByQuery),
		chromedp.Poll(`!document.querySelector('[data-provider-save]').disabled`, nil),
		chromedp.Click(`[data-provider-save]`, chromedp.ByQuery),
		chromedp.Poll(`!!document.querySelector('[data-provider-reprobe="probe-model"]')`, nil),
		chromedp.WaitNotVisible(`#settings-provider-url`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("test and save provider: %v", err)
	}
	state := ctrl.Providers()
	idx := slices.IndexFunc(state.Providers, func(p app.ProviderConfigItem) bool { return p.ID == "probe-model" })
	if idx < 0 || state.Providers[idx].BaseURL != upstream.URL+"/v1" || !state.Providers[idx].PromptProgressSupported {
		t.Fatalf("saved providers = %+v", state.Providers)
	}

	before := modelLists.Load()
	if err := chromedp.Run(browserCtx, chromedp.Click(`[data-provider-reprobe="probe-model"]`, chromedp.ByQuery)); err != nil {
		t.Fatalf("reprobe provider: %v", err)
	}
	for deadline := time.Now().Add(10 * time.Second); modelLists.Load() <= before; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the list's Test did not probe the server again")
		}
	}
}
