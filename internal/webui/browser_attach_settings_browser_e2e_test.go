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

// The browser settings choose between Koder's own Chrome and the user's
// running one; the running one asks for its address and hides the settings
// that only apply to a Chrome Koder starts.
func TestBrowserSettingsChooseRunningChrome(t *testing.T) {
	chromium := chromiumForTest(t)
	ctrl := newTestController(t)
	serverCtx, stopServer := context.WithCancel(context.Background())
	server := startBrowserTestServer(t, serverCtx, ctrl)
	t.Cleanup(func() { stopServer(); _ = server.server.Close() })
	allocCtx, stopAlloc := chromedp.NewExecAllocator(context.Background(), append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(chromium), chromedp.Flag("no-sandbox", true), chromedp.Flag("disable-dev-shm-usage", true))...)
	t.Cleanup(stopAlloc)
	ctx, stopBrowser := chromedp.NewContext(allocCtx)
	t.Cleanup(stopBrowser)
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	t.Cleanup(cancel)

	const visible = `(selector => { const el = document.querySelector(selector); return !!el && !!el.offsetParent; })`
	var shown map[string]bool
	read := chromedp.Evaluate(`Object.fromEntries(['#settings-browser-attach', '#settings-browser-executable', '#settings-browser-mode'].map(s => { const el = document.querySelector(s); return [s, !!el && !!el.offsetParent]; }))`, &shown)
	if err := chromedp.Run(ctx,
		chromedp.EmulateViewport(1440, 1000),
		chromedp.Navigate(server.URL()),
		chromedp.WaitVisible(`[data-settings-open]`, chromedp.ByQuery),
		chromedp.Evaluate(`document.documentElement._x_dataStack[0].openSettingsDialog('tools')`, nil),
		chromedp.Poll(`!!document.documentElement._x_dataStack[0].settings?.browser`, nil),
		chromedp.Evaluate(`document.documentElement._x_dataStack[0].openBrowserEditor()`, nil),
		chromedp.Poll(visible+`('#settings-browser-mode')`, nil),
		read,
	); err != nil {
		t.Fatalf("open browser settings: %v", err)
	}
	if !shown["#settings-browser-executable"] || shown["#settings-browser-attach"] {
		t.Errorf("Koder's own Chrome: shown = %v, want the executable and no address", shown)
	}
	if err := chromedp.Run(ctx,
		chromedp.SetValue(`#settings-browser-mode`, "attach", chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector('#settings-browser-mode').dispatchEvent(new Event('change'))`, nil),
		chromedp.Poll(visible+`('#settings-browser-attach')`, nil),
		chromedp.Poll(`document.querySelector('#settings-browser-attach').value === 'auto'`, nil),
		read,
	); err != nil {
		t.Fatalf("choose the running Chrome: %v", err)
	}
	if shown["#settings-browser-executable"] {
		t.Errorf("running Chrome: the executable setting is still shown")
	}
	if dir := os.Getenv("KODER_UI_SHOTS"); dir != "" {
		for _, width := range []int64{1440, 820, 420} {
			var shot []byte
			if err := chromedp.Run(ctx, chromedp.EmulateViewport(width, 1000), chromedp.Sleep(250*time.Millisecond), chromedp.CaptureScreenshot(&shot)); err != nil {
				t.Fatal(err)
			}
			_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("browser-attach-%d.png", width)), shot, 0o644)
		}
	}
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`document.documentElement._x_dataStack[0].saveSettings()`, nil),
		chromedp.Poll(`(() => { const app = document.documentElement._x_dataStack[0]; return !app.settingsSaving && !app.settingsDirty(); })()`, nil),
	); err != nil {
		t.Fatalf("save: %v", err)
	}
	saved, err := ctrl.Preferences(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if saved.Browser.Attach != "auto" {
		t.Fatalf("saved browser attach = %q, want auto", saved.Browser.Attach)
	}
}
