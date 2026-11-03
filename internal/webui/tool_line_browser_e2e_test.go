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

// Tool calls show as one line each. Clicking a line, or pressing Enter on
// it, opens the inspector with the whole command and output; only results
// meant for the user, such as images, stay in the transcript.
func TestToolCallsShowAsOneLineWithInspector(t *testing.T) {
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

	const timeline = `[{
		id: 'tool-line-item', kind: 'assistant', created_at: '2026-11-03T12:00:00Z',
		content: {text: 'Let me find out what the queue is configured to do.', tools: [
			{tool_call_id: 'c1', tool: 'exec_command', status: 'done',
				args: {cmd: 'grep -rn klaravik cmd/', description: 'Find klaravik queue wiring and flag defaults', workdir: '/work'},
				result: {data: {command: 'grep -rn klaravik cmd/', exit_code: 0, output: 'one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\nnine'}}},
			{tool_call_id: 'c2', tool: 'exec_command', status: 'done',
				args: {cmd: 'false', description: 'Inspect the queue journal'},
				result: {data: {command: 'false', exit_code: 1, output: 'no journal'}}},
			{tool_call_id: 'c3', tool: 'file_read', status: 'done',
				args: {path: '/home/lak/github-repos/car-scraper/cmd/serve.go', start_line: 20, end_line: 60},
				result: {text: 'package main'}},
			{tool_call_id: 'c4', tool: 'file_edit', status: 'done',
				args: {path: '/work/cmd/serve.go'},
				result: {data: {summary: 'Edited cmd/serve.go', diff: '@@ -1,2 +1,3 @@\n-old\n+new\n+added\n same'}}},
			{tool_call_id: 'c5', tool: 'exec_command', status: 'running',
				args: {cmd: 'sleep 100', description: 'Wait for the drain'}},
			{tool_call_id: 'c6', tool: 'show_image', status: 'done',
				args: {path: '/work/shot.png'},
				result: {data: {path: '/work/shot.png', url: 'data:image/gif;base64,R0lGODlhAQABAAAAACw=', mime_type: 'image/gif'}}}
		]}
	}]`

	type row struct {
		Title, Badge, Detail string
		Height               float64
		Inline               bool
	}
	var got struct {
		Rows                                  []row
		InlineResults                         int
		Title, Command, Output, Workdir, Exit string
		Open, KeyOpen, Closed                 bool
		EditResult, EditSummaryShown          string
		CancelInRow                           bool
	}
	setup := fmt.Sprintf(`(() => {
		const app = document.documentElement._x_dataStack[0];
		const items = %s;
		app.renderedTimeline = () => items;
		return true;
	})()`, timeline)
	if err := chromedp.Run(browserCtx,
		chromedp.EmulateViewport(1440, 900),
		chromedp.Navigate(server.URL()+"/s/"+string(state.Session.ID)+"/c/"+string(state.ActiveChatID)),
		chromedp.WaitReady(`.transcript`, chromedp.ByQuery),
		chromedp.Poll(`!!document.documentElement._x_dataStack?.[0]?.toolResultHTML`, nil),
		chromedp.Evaluate(setup, nil),
		chromedp.Poll(`[...document.querySelectorAll('.tool-line')].filter(el => el.offsetParent).length === 6`, nil),
		chromedp.Evaluate(`(async () => {
			const app = document.documentElement._x_dataStack[0];
			const visible = el => !!el && !!el.offsetParent;
			const lines = [...document.querySelectorAll('.tool-line')];
			const rows = lines.map(line => ({
				Title: line.querySelector('.tool-line-title').textContent.trim(),
				Badge: visible(line.querySelector('.badge')) ? line.querySelector('.badge').textContent.trim() + (line.querySelector('.badge').classList.contains('tool-status-badge-error') ? ' (error)' : '') : '',
				Detail: visible(line.querySelector('.tool-line-detail')) ? line.querySelector('.tool-line-detail').textContent.trim() : '',
				Height: line.getBoundingClientRect().height,
				Inline: !!line.closest('.tool').querySelector('.tool-result'),
			}));
			const tick = () => new Promise(r => setTimeout(r, 50));
			lines[0].click();
			await tick();
			const modal = document.querySelector('.tool-inspector');
			const meta = label => [...modal.querySelectorAll('.tool-command-meta-row')].find(r => r.children[0].textContent === label)?.children[1].textContent || '';
			const opened = {
				Open: visible(modal),
				Title: modal.querySelector('.tool-inspector-title').textContent.trim(),
				Command: modal.querySelector('.tool-command-full')?.textContent || '',
				Output: modal.querySelector('.tool-command-output-body')?.textContent || '',
				Workdir: meta('workdir'),
				Exit: meta('exit code'),
			};
			document.dispatchEvent(new KeyboardEvent('keydown', {key: 'Escape', bubbles: true}));
			window.dispatchEvent(new KeyboardEvent('keydown', {key: 'Escape'}));
			await tick();
			const closed = !app.toolInspector.open;
			lines[3].dispatchEvent(new KeyboardEvent('keydown', {key: 'Enter', bubbles: true}));
			await tick();
			await tick();
			const editResult = modal.querySelector('.tool-inspector-result')?.textContent || '';
			const summary = modal.querySelector('.tool-inspector-result summary');
			return {
				Rows: rows,
				InlineResults: document.querySelectorAll('.transcript .tool-result').length,
				...opened,
				Closed: closed,
				KeyOpen: app.toolInspector.open && app.toolInspector.title === 'Edit serve.go',
				EditResult: editResult,
				EditSummaryShown: visible(summary) ? summary.textContent : '',
				CancelInRow: visible(lines[4].querySelector('.tool-cancel-button')),
			};
		})()`, &got, func(p *cdpruntime.EvaluateParams) *cdpruntime.EvaluateParams { return p.WithAwaitPromise(true) }),
	); err != nil {
		t.Fatalf("tool lines: %v", err)
	}

	want := []row{
		{Title: "Find klaravik queue wiring and flag defaults"},
		{Title: "Inspect the queue journal", Badge: "exit 1 (error)"},
		{Title: "Read serve.go, lines 20-60"},
		{Title: "Edit serve.go", Detail: "+2 −1"},
		{Title: "Wait for the drain", Badge: "running"},
		{Title: "Show image /work/shot.png", Inline: true},
	}
	if len(got.Rows) != len(want) {
		t.Fatalf("rows = %+v", got.Rows)
	}
	for i, w := range want {
		g := got.Rows[i]
		if g.Title != w.Title || g.Badge != w.Badge || g.Detail != w.Detail || g.Inline != w.Inline {
			t.Errorf("row %d = %+v, want %+v", i, g, w)
		}
		if g.Height > 32 {
			t.Errorf("row %d is %.0fpx tall, want one line", i, g.Height)
		}
	}
	if got.InlineResults != 1 {
		t.Errorf("transcript shows %d inline results, want only the image", got.InlineResults)
	}
	if !got.Open || got.Title != "Find klaravik queue wiring and flag defaults" || got.Command != "grep -rn klaravik cmd/" ||
		got.Output != "one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\nnine" || got.Workdir != "/work" || got.Exit != "0" {
		t.Errorf("exec inspector = %+v", got)
	}
	if !got.Closed {
		t.Error("Escape did not close the inspector")
	}
	if !got.KeyOpen || got.EditSummaryShown != "" {
		t.Errorf("Enter on the edit row: open=%v folded summary=%q", got.KeyOpen, got.EditSummaryShown)
	}
	for _, line := range []string{"-old", "+new", "+added"} {
		if !strings.Contains(got.EditResult, line) {
			t.Errorf("edit inspector lacks %q: %q", line, got.EditResult)
		}
	}
	if !got.CancelInRow {
		t.Error("running tool row has no cancel button")
	}

	if dir := os.Getenv("KODER_UI_SHOTS"); dir != "" {
		for _, width := range []int64{1440, 820, 420} {
			var shot []byte
			if err := chromedp.Run(browserCtx,
				chromedp.Evaluate(`document.documentElement._x_dataStack[0].closeToolInspector()`, nil),
				chromedp.EmulateViewport(width, 900),
				chromedp.Sleep(200*time.Millisecond),
				chromedp.CaptureScreenshot(&shot),
				chromedp.Evaluate(`document.querySelectorAll('.tool-line')[0].click()`, nil),
				chromedp.Sleep(300*time.Millisecond),
			); err != nil {
				t.Fatal(err)
			}
			var modal []byte
			if err := chromedp.Run(browserCtx, chromedp.CaptureScreenshot(&modal)); err != nil {
				t.Fatal(err)
			}
			_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("tools-%d.png", width)), shot, 0o644)
			_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("inspector-%d.png", width)), modal, 0o644)
		}
	}
}
