package webui

import (
	"context"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

func TestExecToolOutputExpansionAndWaitInspection(t *testing.T) {
	chromium := memoryBrowserChromium(t)
	ctrl := newTestController(t)
	serverCtx, stopServer := context.WithCancel(context.Background())
	server := startMemoryBrowserTestServer(t, serverCtx, ctrl)
	t.Cleanup(func() {
		stopServer()
		_ = server.server.Close()
	})

	allocatorOptions := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(chromium),
		chromedp.WSURLReadTimeout(60*time.Second),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
	)
	allocatorCtx, stopAllocator := chromedp.NewExecAllocator(context.Background(), allocatorOptions...)
	t.Cleanup(stopAllocator)
	browserCtx, stopBrowser := chromedp.NewContext(allocatorCtx)
	t.Cleanup(stopBrowser)
	browserCtx, cancelDeadline := context.WithTimeout(browserCtx, 60*time.Second)
	t.Cleanup(cancelDeadline)

	var result struct {
		Summary          string `json:"summary"`
		Expanded         bool   `json:"expanded"`
		ExpandedText     string `json:"expandedText"`
		WaitSummary      string `json:"waitSummary"`
		WaitInspectable  bool   `json:"waitInspectable"`
		InspectedCommand string `json:"inspectedCommand"`
		InspectedProcess string `json:"inspectedProcess"`
	}
	if err := chromedp.Run(browserCtx,
		chromedp.Navigate(server.URL()),
		chromedp.WaitReady(`.transcript`, chromedp.ByQuery),
		chromedp.Poll(`document.documentElement._x_dataStack?.[0]?.toolResultHTML`, nil),
		chromedp.Evaluate(`(() => {
			const app = document.documentElement._x_dataStack[0];
			const host = document.createElement('div');
			host.innerHTML = app.toolResultHTML({
				tool: 'exec_command',
				result: {data: {output: 'first line\nsecond line\nthird line\nfourth line'}}
			});
			document.body.append(host);
			const details = host.querySelector('.tool-result-omitted-details');
			const summary = details?.querySelector('summary');
			summary?.click();

			const waitTool = {
				tool: 'exec_session',
				args: {action: 'wait', process_id: 'exec_7', yield_time_ms: 30000},
				result: {data: {
					process_id: 'exec_7', state: 'running', command: 'serve --foreground',
					output: 'wait one\nwait two\nwait three\nwait four\nwait five\nwait six'
				}}
			};
			const waitHost = document.createElement('div');
			waitHost.innerHTML = app.toolResultHTML(waitTool);
			const waitInspectable = app.toolCommandInspectable(waitTool);
			app.openToolCommandModal(waitTool);
			return {
				summary: summary?.textContent || '',
				expanded: details?.open === true,
				expandedText: details?.querySelector('.tool-result-expanded')?.textContent || '',
				waitSummary: waitHost.querySelector('.tool-result-omitted-details summary')?.textContent || '',
				waitInspectable,
				inspectedCommand: app.toolCommandModal.command,
				inspectedProcess: app.toolCommandModal.meta.find(row => row.label === 'process id')?.value || ''
			};
		})()`, &result),
	); err != nil {
		t.Fatalf("exercise exec tool rendering: %v", err)
	}

	if result.Summary != "... 3 lines omitted ..." || !result.Expanded {
		t.Fatalf("omitted output summary=%q expanded=%v", result.Summary, result.Expanded)
	}
	if result.ExpandedText != "second linethird linefourth line" {
		t.Fatalf("expanded output = %q", result.ExpandedText)
	}
	if result.WaitSummary != "... 2 lines omitted ..." {
		t.Fatalf("wait output summary = %q", result.WaitSummary)
	}
	if !result.WaitInspectable || result.InspectedCommand != "serve --foreground" || result.InspectedProcess != "exec_7" {
		t.Fatalf("wait inspection = %#v", result)
	}
}
