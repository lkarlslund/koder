package webui

import (
	"context"
	"testing"
	"time"

	"github.com/chromedp/cdproto/input"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// A long chat of one-line tool rows. Scrolling up a little from the bottom
// must load older items without dropping the newest ones, which made the
// view jump and showed only the latest turn. Scrolling to the top keeps
// loading older pages.
func TestTranscriptPagingKeepsNewestWhenScrollingUpALittle(t *testing.T) {
	chromium := chromiumForTest(t)
	ctrl, _, _ := newTestControllerAndEngine(t, t.TempDir())
	state := selectedTestState(t, ctrl)
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
	browserCtx, cancel := context.WithTimeout(browserCtx, 90*time.Second)
	t.Cleanup(cancel)

	wheel := func(dy float64) chromedp.Action {
		return chromedp.Tasks{
			chromedp.MouseEvent(input.MouseWheel, 500, 400, func(p *input.DispatchMouseEventParams) *input.DispatchMouseEventParams {
				return p.WithDeltaX(0).WithDeltaY(dy)
			}),
			chromedp.Sleep(400 * time.Millisecond),
		}
	}
	type view struct {
		Items        int
		NewestLoaded bool
		HasNewer     bool
		FirstSeq     int
		Loads        int
	}
	const probe = `(() => {
		const app = document.documentElement._x_dataStack[0];
		const timeline = app.timeline();
		return {
			Items: timeline.length,
			NewestLoaded: timeline.some(item => item.id === window.__items[window.__items.length - 1].id),
			HasNewer: app.timelineHasNewer(),
			FirstSeq: timeline[0]?.seq || 0,
			Loads: window.__loads,
		};
	})()`
	var afterSmall, afterTop view
	if err := chromedp.Run(browserCtx,
		chromedp.EmulateViewport(1440, 900),
		chromedp.Navigate(server.URL()+"/s/"+string(state.Session.ID)+"/c/"+string(state.ActiveChatID)),
		chromedp.WaitReady(`.transcript`, chromedp.ByQuery),
		chromedp.Poll(`document.documentElement._x_dataStack?.[0]?.connected === true && String(document.documentElement._x_dataStack[0].activeChatID() || '').length > 10`, nil),
		chromedp.Evaluate(`(async () => {
			const app = document.documentElement._x_dataStack[0];
			const chatID = String(app.activeChatID());
			const items = [];
			for (let i = 1; i <= 300; i++) {
				items.push({id: 'item-' + String(i).padStart(4, '0'), seq: i, kind: 'assistant', created_at: new Date(Date.UTC(2026, 10, 3, 10, 0, i)).toISOString(),
					content: {tools: [{tool_call_id: 't' + i, tool: 'exec_command', status: 'done', args: {cmd: 'true', description: 'Step ' + i}, result: {data: {exit_code: 0}}}]}});
			}
			window.__items = items;
			window.__loads = 0;
			const page = (from, to) => ({chat_id: chatID, items: items.slice(from, to), has_more: from > 0, has_newer: to < items.length,
				before: items[from]?.id, after: items[to - 1]?.id});
			const realRPC = app.rpc.bind(app);
			app.rpc = (method, params) => {
				if (method !== 'load_timeline') return realRPC(method, params);
				window.__loads++;
				const limit = params.limit;
				if (params.before) {
					const end = items.findIndex(item => item.id === params.before);
					return Promise.resolve(page(Math.max(0, end - limit), end));
				}
				if (params.after) {
					const start = items.findIndex(item => item.id === params.after) + 1;
					return Promise.resolve(page(start, Math.min(items.length, start + limit)));
				}
				return Promise.resolve(page(items.length - limit, items.length));
			};
			app.loadLatestTimeline();
			await new Promise(r => setTimeout(r, 300));
			return true;
		})()`, nil, func(p *cdpruntime.EvaluateParams) *cdpruntime.EvaluateParams { return p.WithAwaitPromise(true) }),
		chromedp.Poll(`document.documentElement._x_dataStack[0].timeline().length > 0`, nil),
		wheel(-300),
		wheel(-300),
		chromedp.Sleep(500*time.Millisecond),
		chromedp.Evaluate(probe, &afterSmall),
	); err != nil {
		t.Fatalf("scroll up a little: %v", err)
	}
	if !afterSmall.NewestLoaded || afterSmall.HasNewer {
		t.Errorf("scrolling up 600px dropped the newest item: %+v", afterSmall)
	}

	for i := 0; i < 12; i++ {
		if err := chromedp.Run(browserCtx, wheel(-3000)); err != nil {
			t.Fatal(err)
		}
	}
	if err := chromedp.Run(browserCtx, chromedp.Sleep(500*time.Millisecond), chromedp.Evaluate(probe, &afterTop)); err != nil {
		t.Fatal(err)
	}
	if afterTop.FirstSeq >= afterSmall.FirstSeq || afterTop.FirstSeq > 300-120 {
		t.Errorf("scrolling to the top did not keep loading older items: small=%+v top=%+v", afterSmall, afterTop)
	}
}
