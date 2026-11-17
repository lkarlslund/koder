package webui

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// A pasted image shows as a thumbnail in the composer, and again in the
// sent message, where clicking it opens the image.
func TestPastedImageShowsThumbnails(t *testing.T) {
	chromium := chromiumForTest(t)
	// Sending starts a model turn; a local provider that answers at once
	// lets it finish, so the controller shuts down cleanly.
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["stream"] != true {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n"))
	}))
	t.Cleanup(provider.Close)
	ctrl, _, _ := newTestControllerAndEngine(t, t.TempDir(), provider.URL+"/v1")
	state := selectedTestState(t, ctrl)
	serverCtx, stopServer := context.WithCancel(context.Background())
	srv := startBrowserTestServer(t, serverCtx, ctrl)
	t.Cleanup(func() { stopServer(); _ = srv.server.Close() })
	opts := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(chromium), chromedp.Flag("no-sandbox", true), chromedp.Flag("disable-dev-shm-usage", true))
	allocCtx, stopAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	t.Cleanup(stopAlloc)
	ctx, stopBrowser := chromedp.NewContext(allocCtx)
	t.Cleanup(stopBrowser)
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	t.Cleanup(cancel)

	img := image.NewRGBA(image.Rect(0, 0, 64, 48))
	for i := range img.Pix {
		img.Pix[i] = 0xc0
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(buf.Bytes())
	await := func(p *cdpruntime.EvaluateParams) *cdpruntime.EvaluateParams { return p.WithAwaitPromise(true) }
	const thumbLoaded = `(selector => { const img = document.querySelector(selector); return !!img && img.complete && img.naturalWidth === 64 && img.getBoundingClientRect().width > 20; })`

	var sentSrc string
	if err := chromedp.Run(ctx,
		chromedp.EmulateViewport(1440, 900),
		chromedp.Navigate(srv.URL()+"/s/"+string(state.Session.ID)+"/c/"+string(state.ActiveChatID)),
		chromedp.Poll(`document.documentElement._x_dataStack?.[0]?.connected === true && String(document.documentElement._x_dataStack[0].activeChatID() || '').length > 10`, nil),
		chromedp.Evaluate(fmt.Sprintf(`(() => {
			const bytes = Uint8Array.from(atob(%q), c => c.charCodeAt(0));
			document.documentElement._x_dataStack[0].uploadComposerImage(new File([bytes], 'image.png', {type: 'image/png'}));
		})()`, encoded), nil),
		chromedp.Poll(thumbLoaded+`('.composer-attachments .attachment-thumb img')`, nil),
		chromedp.Evaluate(`(() => {
			const app = document.documentElement._x_dataStack[0];
			app.draft = 'søren has jan bo\'s image?';
			app.send({steer: false});
		})()`, nil),
		chromedp.Poll(`!document.querySelector('.composer-attachments .attachment-chip')`, nil),
		chromedp.Poll(thumbLoaded+`('.transcript .attachment-thumb img')`, nil, chromedp.WithPollingTimeout(15*time.Second)),
		chromedp.Evaluate(`document.querySelector('.transcript .attachment-thumb img').getAttribute('src')`, &sentSrc),
		chromedp.Click(`.transcript .attachment-thumb`, chromedp.ByQuery),
		chromedp.Poll(`document.documentElement._x_dataStack[0].imageLightbox?.open === true`, nil),
	); err != nil {
		var ui string
		_ = chromedp.Run(ctx, chromedp.Evaluate(`JSON.stringify({composer: document.querySelector('.composer-attachments')?.outerHTML?.slice(0, 400), chips: [...document.querySelectorAll('.transcript .attachment-chip')].map(e => e.outerHTML.slice(0, 300))})`, &ui))
		t.Fatalf("thumbnails: %v; UI: %s", err, ui)
	}
	if want := "/api/attachments/session/" + string(state.Session.ID) + "/"; len(sentSrc) <= len(want) || sentSrc[:len(want)] != want {
		t.Errorf("sent thumbnail loads from %q, want the session's attachment", sentSrc)
	}
	var revoked bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(`(async () => {
		const src = [...document.querySelectorAll('img')].map(img => img.src).find(src => src.startsWith('blob:'));
		return !src;
	})()`, &revoked, await)); err != nil {
		t.Fatal(err)
	}
	if !revoked {
		t.Errorf("a draft preview is still shown after sending")
	}
	if dir := os.Getenv("KODER_UI_SHOTS"); dir != "" {
		for _, width := range []int64{1440, 820, 420} {
			var shot []byte
			if err := chromedp.Run(ctx,
				chromedp.Evaluate(`document.documentElement._x_dataStack[0].imageLightbox.open = false`, nil),
				chromedp.Evaluate(fmt.Sprintf(`(() => {
					const bytes = Uint8Array.from(atob(%q), c => c.charCodeAt(0));
					const app = document.documentElement._x_dataStack[0];
					if (!document.querySelector('.composer-attachments .attachment-chip')) app.uploadComposerImage(new File([bytes], 'pasted.png', {type: 'image/png'}));
				})()`, encoded), nil),
				chromedp.Poll(thumbLoaded+`('.composer-attachments .attachment-thumb img')`, nil),
				chromedp.EmulateViewport(width, 900),
				chromedp.Sleep(300*time.Millisecond),
				chromedp.CaptureScreenshot(&shot),
			); err != nil {
				t.Fatal(err)
			}
			_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("attachment-thumbs-%d.png", width)), shot, 0o644)
		}
	}
}
