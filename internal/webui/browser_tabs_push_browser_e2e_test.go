package webui

import (
	"context"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// A browser_tabs report makes the page list the chat's tabs at once, not on
// its next poll, and a report that arrives during a refresh is not lost.
func TestBrowserTabsReportRefreshesActivity(t *testing.T) {
	chromium := chromiumForTest(t)
	ctrl, _, _ := newTestControllerAndEngine(t, t.TempDir())
	state := selectedTestState(t, ctrl)
	serverCtx, stopServer := context.WithCancel(context.Background())
	srv := startBrowserTestServer(t, serverCtx, ctrl)
	t.Cleanup(func() { stopServer(); _ = srv.server.Close() })
	opts := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(chromium), chromedp.Flag("no-sandbox", true), chromedp.Flag("disable-dev-shm-usage", true))
	allocCtx, stopAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	t.Cleanup(stopAlloc)
	ctx, stopBrowser := chromedp.NewContext(allocCtx)
	t.Cleanup(stopBrowser)
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	t.Cleanup(cancel)
	const tabRow = `[...document.querySelectorAll('[data-activity="tab"]')].filter(el => el.offsetParent).map(el => el.textContent.trim()).join('|')`
	var rows string
	if err := chromedp.Run(ctx,
		chromedp.EmulateViewport(1440, 900),
		chromedp.Navigate(srv.URL()+"/s/"+string(state.Session.ID)+"/c/"+string(state.ActiveChatID)),
		chromedp.Poll(`document.documentElement._x_dataStack?.[0]?.connected === true`, nil),
		// Answer the two browser requests from a list the test controls, and
		// stop the poll so only the report can bring tabs in.
		chromedp.Evaluate(`(() => {
			const app = document.documentElement._x_dataStack[0];
			clearInterval(app.browserStatusTimer);
			window.__tabs = [];
			window.__delay = 0;
			const rpc = app.rpc.bind(app);
			const later = value => new Promise(resolve => setTimeout(() => resolve(value), window.__delay));
			app.rpc = (method, params) => {
				const status = {state: 'running', owned_tabs: window.__tabs.length};
				if (method === 'browser_action' && params?.action === 'status') return later(status);
				if (method === 'browser_tabs') return later({status, tabs: window.__tabs.map(tab => ({...tab}))});
				return rpc(method, params);
			};
			app.browserTabs = [];
		})()`, nil),
		chromedp.Sleep(300*time.Millisecond),
		chromedp.Evaluate(tabRow, &rows),
	); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if rows != "" {
		t.Fatalf("tab rows before any tab opened: %q", rows)
	}
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`(() => {
			window.__tabs = [{id: 'tab-1', title: 'Slow page', url: 'http://example.test/slow', owned: true, selected: true, opened_at: new Date().toISOString()}];
			document.documentElement._x_dataStack[0].onMessage({type: 'browser_tabs', payload: {}});
		})()`, nil),
		chromedp.Poll(tabRow+`.includes('Slow page')`, nil, chromedp.WithPollingTimeout(2*time.Second)),
	); err != nil {
		t.Fatalf("report did not bring the tab in: %v", err)
	}
	// A second report while the first refresh is still waiting for answers
	// leads to one more refresh, which sees the loaded page.
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`(() => {
			const app = document.documentElement._x_dataStack[0];
			window.__delay = 400;
			app.onMessage({type: 'browser_tabs', payload: {}});
			// At 550 ms the first refresh has its status (400 ms) and is
			// waiting for the tab list it asked for before the change.
			setTimeout(() => {
				window.__tabs = [{...window.__tabs[0], title: 'Loaded page'}];
				app.onMessage({type: 'browser_tabs', payload: {}});
			}, 550);
		})()`, nil),
		chromedp.Poll(tabRow+`.includes('Loaded page')`, nil, chromedp.WithPollingTimeout(4*time.Second)),
	); err != nil {
		var got string
		_ = chromedp.Run(ctx, chromedp.Evaluate(tabRow, &got))
		t.Fatalf("report during a refresh was lost: %v; rows %q", err, got)
	}
}
