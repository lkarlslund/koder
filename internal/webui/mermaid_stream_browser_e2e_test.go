package webui

import (
	"context"
	"testing"
	"time"

	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// A Mermaid diagram in a streaming message draws while it arrives, keeps its
// last drawing between renders, and settles on the final diagram.
func TestStreamingMermaidDiagramDrawsWhileArriving(t *testing.T) {
	chromium := chromiumForTest(t)
	ctrl := newTestController(t)
	serverCtx, stopServer := context.WithCancel(context.Background())
	server := startBrowserTestServer(t, serverCtx, ctrl)
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
	browserCtx, cancel := context.WithTimeout(browserCtx, 2*time.Minute)
	t.Cleanup(cancel)

	var result struct {
		Steps          int    `json:"steps"`
		DrawnSteps     int    `json:"drawnSteps"`
		DrawnBeforeEnd bool   `json:"drawnBeforeEnd"`
		Final          string `json:"final"`
		FinalSVG       bool   `json:"finalSVG"`
		Errors         int    `json:"errors"`
	}
	// Arrows ("-->") matter: the sanitizer drops attributes that hold them.
	script := "(async () => {\n" +
		"const app = document.documentElement._x_dataStack[0];\n" +
		"const el = document.createElement('div');\n" +
		"el.className = 'markdown-body';\n" +
		"app.transcriptElement().append(el);\n" +
		"const diagram = ['flowchart TD', '    A[Request] --> B{Valid?}', '    B -- yes --> C[Load]', '', '    subgraph Work', '        C --> D[Parse]', '        D --> E[Store]', '        E --> F[Index]', '    end', '    B -- no --> G[Reject]', '    F --> H([Done])', '    G --> H'].join('\\n');\n" +
		"const full = 'Here is the flow:\\n\\n```mermaid\\n' + diagram + '\\n```\\n\\nThat is all.';\n" +
		"const item = {id: 'streaming-diagram'};\n" +
		"const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));\n" +
		"const fenceEnd = full.indexOf('```', full.indexOf('```mermaid') + 3);\n" +
		"let steps = 0, drawnSteps = 0, drawnBeforeEnd = false;\n" +
		"for (let n = 70; n < full.length + 20; n += 20) {\n" +
		"  app.renderTimelineMarkdownElement(el, item, full.slice(0, n), {deferDiagrams: true, incremental: true});\n" +
		"  await app.enhanceTranscript();\n" +
		"  const drawn = !!el.querySelector('.mermaid-diagram svg');\n" +
		"  steps++;\n" +
		"  if (drawn) drawnSteps++;\n" +
		"  if (drawn && n < fenceEnd) drawnBeforeEnd = true;\n" +
		"  await sleep(120);\n" +
		"}\n" +
		"app.renderTimelineMarkdownElement(el, {...item, sealed_at: 'now'}, full, {deferDiagrams: false, incremental: false});\n" +
		"await app.enhanceTranscript();\n" +
		"const slot = el.querySelector('.mermaid-diagram');\n" +
		"const out = {steps, drawnSteps, drawnBeforeEnd, final: slot?.dataset.mermaidState || '', finalSVG: !!slot?.querySelector('svg'), errors: el.querySelectorAll('.mermaid-error').length};\n" +
		"el.remove();\n" +
		"return out;\n" +
		"})()"
	if err := chromedp.Run(browserCtx,
		chromedp.Navigate(server.URL()),
		chromedp.WaitReady(`.transcript`, chromedp.ByQuery),
		chromedp.Poll(`!!window.mermaid && !!document.documentElement._x_dataStack?.[0]?.renderTimelineMarkdownElement`, nil),
		chromedp.Evaluate(script, &result, func(p *cdpruntime.EvaluateParams) *cdpruntime.EvaluateParams { return p.WithAwaitPromise(true) }),
	); err != nil {
		t.Fatal(err)
	}
	if !result.DrawnBeforeEnd {
		t.Fatalf("diagram was not drawn before its fence closed: %+v", result)
	}
	if result.DrawnSteps*2 < result.Steps {
		t.Fatalf("diagram was drawn in only %d of %d streaming steps", result.DrawnSteps, result.Steps)
	}
	if result.Final != "done" || !result.FinalSVG || result.Errors != 0 {
		t.Fatalf("final diagram = %+v", result)
	}
}
