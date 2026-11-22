package webui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// A quick chat has a folder of its own, and the top bar offers to browse it
// as it does for a regular session.
func TestQuickChatOffersFileBrowser(t *testing.T) {
	chromium := chromiumForTest(t)
	ctrl := newTestController(t)
	ctx := context.Background()
	quick, _, err := ctrl.CreateQuickChat(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if quick.ProjectRoot == "" {
		t.Fatal("quick chat has no folder")
	}
	if err := os.WriteFile(filepath.Join(quick.ProjectRoot, "notes.txt"), []byte("quick chat notes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	serverCtx, stopServer := context.WithCancel(ctx)
	srv := startBrowserTestServer(t, serverCtx, ctrl)
	t.Cleanup(func() { stopServer(); _ = srv.server.Close() })
	opts := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(chromium), chromedp.Flag("no-sandbox", true), chromedp.Flag("disable-dev-shm-usage", true))
	allocCtx, stopAlloc := chromedp.NewExecAllocator(ctx, opts...)
	t.Cleanup(stopAlloc)
	browserCtx, stopBrowser := chromedp.NewContext(allocCtx)
	t.Cleanup(stopBrowser)
	browserCtx, cancel := context.WithTimeout(browserCtx, 60*time.Second)
	t.Cleanup(cancel)

	// visible reports whether the button is shown, enabled and on screen.
	const visible = `(() => {
		const b = document.querySelector('[data-browse-files]');
		if (!b || b.disabled) return false;
		const r = b.getBoundingClientRect();
		return r.width > 0 && r.left >= 0 && r.right <= innerWidth;
	})()`
	var opened string
	if err := chromedp.Run(browserCtx,
		chromedp.EmulateViewport(1440, 900),
		chromedp.Navigate(srv.URL()+"/s/"+string(quick.ID)),
		chromedp.Poll(`Alpine.$data(document.documentElement).quickChatMode()`, nil, chromedp.WithPollingTimeout(15*time.Second)),
		chromedp.Poll(visible, nil, chromedp.WithPollingTimeout(5*time.Second)),
		// Clicking opens the session's file browser in a new tab; record
		// the address instead of following it.
		chromedp.Evaluate(`(() => { const app = Alpine.$data(document.documentElement); app.openURLInNewTab = url => { window.__opened = url; }; })()`, nil),
		chromedp.Click(`[data-browse-files]`, chromedp.ByQuery),
		chromedp.Poll(`!!window.__opened`, nil),
		chromedp.Evaluate(`window.__opened`, &opened),
	); err != nil {
		t.Fatalf("browse files from the quick chat: %v", err)
	}
	if want := "/s/" + string(quick.ID) + "/files"; len(opened) < len(want) || opened[:len(want)] != want {
		t.Fatalf("opened %q, want the quick chat's file browser %s", opened, want)
	}

	// The file browser lists the quick chat's folder.
	if err := chromedp.Run(browserCtx,
		chromedp.Navigate(srv.URL()+opened),
		chromedp.Poll(`document.body.innerText.includes('notes.txt')`, nil, chromedp.WithPollingTimeout(15*time.Second)),
	); err != nil {
		t.Fatalf("quick chat file browser: %v", err)
	}

	if dir := os.Getenv("KODER_UI_SHOTS"); dir != "" {
		for _, width := range []int64{1440, 820, 420} {
			var shot []byte
			var shown, allShown bool
			if err := chromedp.Run(browserCtx,
				chromedp.EmulateViewport(width, 900),
				chromedp.Navigate(srv.URL()+"/s/"+string(quick.ID)),
				chromedp.Poll(`Alpine.$data(document.documentElement).quickChatMode()`, nil, chromedp.WithPollingTimeout(15*time.Second)),
				chromedp.Sleep(400*time.Millisecond),
				chromedp.Evaluate(visible, &shown),
				chromedp.Evaluate(`Array.from(document.querySelectorAll('.topbar-actions .btn')).filter(b => b.getBoundingClientRect().width > 0).every(b => b.getBoundingClientRect().right <= innerWidth)`, &allShown),
				chromedp.CaptureScreenshot(&shot),
			); err != nil {
				t.Fatal(err)
			}
			if !shown {
				t.Errorf("at %d px the Browse files button is not on screen", width)
			}
			if !allShown {
				t.Errorf("at %d px a top bar button runs off the screen", width)
			}
			_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("quick-chat-files-%d.png", width)), shot, 0o644)
		}
	}
}
