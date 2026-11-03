package webui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// Exec notifications are written for the model. The transcript shows each
// as one line, and the inspector shows the full text.
func TestExecNoticesShowAsOneLine(t *testing.T) {
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
	browserCtx, cancel := context.WithTimeout(browserCtx, 60*time.Second)
	t.Cleanup(cancel)

	const timeline = `[
		{id: 'a1', kind: 'assistant', created_at: '2026-11-03T13:15:16Z', content: {text: 'Measuring the rate.'}},
		{id: 'n1', kind: 'user', created_at: '2026-11-03T13:16:06Z', content: {source: 'exec', text: 'event: exec session ended\nprocess_id: exec_42\ndescription: Measure rate and block profile after fix\nstate: completed\nexit_code: 0\nruntime: 50s\noutput:\ncurl: (28) Operation timed out\nTraceback (most recent call last):\n  File "<string>", line 2'}},
		{id: 'n2', kind: 'user', created_at: '2026-11-03T13:16:07Z', content: {source: 'exec', text: 'event: exec session ended\nprocess_id: exec_43\ncommand: go test ./...\nstate: completed\nexit_code: 1\nruntime: 9s\noutput:\nFAIL'}},
		{id: 'n3', kind: 'user', created_at: '2026-11-03T13:16:08Z', content: {source: 'exec', text: 'event: exec session still running (informational)\nprocess_id: exec_2\ndescription: Start the service again\nruntime: 30m0s\nThe process remains running.'}},
		{id: 'n4', kind: 'user', created_at: '2026-11-03T13:16:09Z', content: {source: 'exec', text: 'Koder was restarted. The following running processes owned by this chat were terminated during shutdown:\nPrevious runtime recorded at: 2026-11-03T13:22:14Z\n- exec_19 \u2014 Restart serve\n  command: serve\nThese are old exec IDs.'}},
		{id: 'n5', kind: 'user', created_at: '2026-11-03T13:16:10Z', content: {source: 'memory', text: 'Memory changed since the memory index in your instructions was written.'}}
	]`
	setup := fmt.Sprintf(`(() => {
		const app = document.documentElement._x_dataStack[0];
		const items = %s;
		app.renderedTimeline = () => items;
		return true;
	})()`, timeline)

	type row struct {
		Title, Detail, Badge string
		Height               float64
	}
	var got struct {
		Rows                   []row
		RawBlocks, CopyButtons int
		MemoryShown            bool
		Title, Output, Exit    string
	}
	if err := chromedp.Run(browserCtx,
		chromedp.EmulateViewport(1440, 900),
		chromedp.Navigate(server.URL()+"/s/"+string(state.Session.ID)+"/c/"+string(state.ActiveChatID)),
		chromedp.WaitReady(`.transcript`, chromedp.ByQuery),
		chromedp.Poll(`!!document.documentElement._x_dataStack?.[0]?.toolResultHTML`, nil),
		chromedp.Evaluate(setup, nil),
		chromedp.Poll(`[...document.querySelectorAll('.exec-notice-line')].filter(el => el.offsetParent).length === 4`, nil),
		chromedp.Evaluate(`(async () => {
			const visible = el => !!el && !!el.offsetParent;
			const text = el => visible(el) ? el.textContent.trim() : '';
			const lines = [...document.querySelectorAll('.exec-notice-line')].filter(visible);
			const result = {
				Rows: lines.map(line => ({
					Title: text(line.querySelector('.tool-line-title')),
					Detail: text(line.querySelector('.tool-line-detail')),
					Badge: text(line.querySelector('.badge')) + (line.querySelector('.badge.tool-status-badge-error') && visible(line.querySelector('.badge')) ? ' (error)' : ''),
					Height: line.getBoundingClientRect().height,
				})),
				CopyButtons: lines.map(line => [...line.closest('.user-turn').querySelectorAll('.copy-output-button')].filter(visible).length).reduce((a, b) => a + b, 0),
				RawBlocks: [...document.querySelectorAll('.transcript .notification-message-text')].filter(visible).length,
				MemoryShown: [...document.querySelectorAll('.transcript .notification-message-text')].some(el => visible(el) && el.textContent.startsWith('Memory changed')),
			};
			lines[0].click();
			await new Promise(r => setTimeout(r, 80));
			const modal = document.querySelector('.tool-inspector');
			result.Title = text(modal.querySelector('.tool-inspector-title'));
			result.Output = modal.querySelector('.tool-command-output-body')?.textContent || '';
			result.Exit = [...modal.querySelectorAll('.tool-command-meta-row')].find(r => r.children[0].textContent === 'exit code')?.children[1].textContent || '';
			return result;
		})()`, &got, func(p *cdpruntime.EvaluateParams) *cdpruntime.EvaluateParams { return p.WithAwaitPromise(true) }),
	); err != nil {
		t.Fatalf("exec notices: %v", err)
	}
	want := []row{
		{Title: "Measure rate and block profile after fix", Detail: "exec_42 \u00b7 ended \u00b7 50s"},
		{Title: "go test ./...", Detail: "exec_43 \u00b7 ended \u00b7 9s", Badge: "exit 1 (error)"},
		{Title: "Start the service again", Detail: "exec_2 \u00b7 still running \u00b7 30m0s", Badge: "running"},
		{Title: "Koder restarted: 1 process stopped", Badge: "stopped (error)"},
	}
	if len(got.Rows) != len(want) {
		t.Fatalf("rows = %+v", got.Rows)
	}
	for i, w := range want {
		g := got.Rows[i]
		if g.Title != w.Title || g.Detail != w.Detail || g.Badge != w.Badge || g.Height > 32 {
			t.Errorf("row %d = %+v, want %+v", i, g, w)
		}
	}
	if got.CopyButtons != 0 {
		t.Errorf("notice rows show %d copy buttons, want none", got.CopyButtons)
	}
	if got.RawBlocks != 1 || !got.MemoryShown {
		t.Errorf("raw notification blocks = %d (memory shown %v), want only the memory notice", got.RawBlocks, got.MemoryShown)
	}
	if got.Title != "Measure rate and block profile after fix" || got.Exit != "0" || !strings.HasPrefix(got.Output, "curl: (28) Operation timed out\nTraceback") {
		t.Errorf("inspector title=%q exit=%q output=%q", got.Title, got.Exit, got.Output)
	}

	if dir := os.Getenv("KODER_UI_SHOTS"); dir != "" {
		for _, width := range []int64{1440, 420} {
			var shot []byte
			if err := chromedp.Run(browserCtx,
				chromedp.Evaluate(`document.documentElement._x_dataStack[0].closeToolInspector()`, nil),
				chromedp.EmulateViewport(width, 900),
				chromedp.Sleep(200*time.Millisecond),
				chromedp.CaptureScreenshot(&shot),
			); err != nil {
				t.Fatal(err)
			}
			_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("notices-%d.png", width)), shot, 0o644)
		}
	}
}
