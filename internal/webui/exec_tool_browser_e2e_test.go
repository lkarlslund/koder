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
		MCPSummary       string `json:"mcpSummary"`
		MCPExpanded      bool   `json:"mcpExpanded"`
		MCPCopied        string `json:"mcpCopied"`
		PreCopyButton    bool   `json:"preCopyButton"`
		PreCopied        string `json:"preCopied"`
	}
	if err := chromedp.Run(browserCtx,
		chromedp.Navigate(server.URL()),
		chromedp.WaitReady(`.transcript`, chromedp.ByQuery),
		chromedp.Poll(`document.documentElement._x_dataStack?.[0]?.toolResultHTML`, nil),
		chromedp.Evaluate(`(() => {
			const app = document.documentElement._x_dataStack[0];
			Object.defineProperty(navigator, 'clipboard', {configurable: true, value: {writeText: async text => { window.__copiedOutput = text; }}});
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

			const mcpHost = document.createElement('div');
			mcpHost.innerHTML = app.toolResultHTML({
				tool: 'mcp',
				args: {server: 'redok', tool: 'search'},
				result: {text: 'mcp one\nmcp two\nmcp three\nmcp four\nmcp five\nmcp six\nmcp seven'}
			});
			document.body.append(mcpHost);
			const mcpDetails = mcpHost.querySelector('.tool-result-omitted-details');
			mcpDetails?.querySelector('summary')?.click();
			mcpHost.querySelector('.copy-output-button')?.click();
			const mcpCopied = window.__copiedOutput || '';

			const markdownHost = document.createElement('div');
			markdownHost.className = 'markdown-body';
			markdownHost.innerHTML = '<pre><code>alpha\nbeta</code></pre>';
			document.body.append(markdownHost);
			app.enhanceDisplayedMedia(markdownHost);
			const preCopyButton = markdownHost.querySelector('.copyable-mono > .copy-output-button');
			preCopyButton?.click();
			return {
				summary: summary?.textContent || '',
				expanded: details?.open === true,
				expandedText: details?.querySelector('.tool-result-expanded')?.textContent || '',
				waitSummary: waitHost.querySelector('.tool-result-omitted-details summary')?.textContent || '',
				waitInspectable,
				inspectedCommand: app.toolCommandModal.command,
				inspectedProcess: app.toolCommandModal.meta.find(row => row.label === 'process id')?.value || '',
				mcpSummary: mcpDetails?.querySelector('summary')?.textContent || '',
				mcpExpanded: mcpDetails?.open === true,
				mcpCopied,
				preCopyButton: !!preCopyButton,
				preCopied: window.__copiedOutput || ''
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
	if result.MCPSummary != "... 3 lines omitted ..." || !result.MCPExpanded {
		t.Fatalf("MCP omitted output summary=%q expanded=%v", result.MCPSummary, result.MCPExpanded)
	}
	if result.MCPCopied != "mcp one\nmcp two\nmcp three\nmcp four\nmcp five\nmcp six\nmcp seven" {
		t.Fatalf("copied MCP output = %q", result.MCPCopied)
	}
	if !result.PreCopyButton || result.PreCopied != "alpha\nbeta" {
		t.Fatalf("pre copy button=%v copied=%q", result.PreCopyButton, result.PreCopied)
	}
}
