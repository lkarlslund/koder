package webui

import (
	"context"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

func TestTranscriptClientBatchesAndReconcilesEnhancements(t *testing.T) {
	chromium := knowledgeBrowserChromium(t)
	ctrl := newTestController(t)
	serverCtx, stopServer := context.WithCancel(context.Background())
	server := startKnowledgeBrowserTestServer(t, serverCtx, ctrl)
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

	if err := chromedp.Run(browserCtx,
		chromedp.Navigate(server.URL()),
		chromedp.WaitReady(`.transcript`, chromedp.ByQuery),
		chromedp.Poll(`document.documentElement._x_dataStack?.[0]?.transcriptEnhancementObserver instanceof MutationObserver`, nil),
		chromedp.Evaluate(`new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))`, nil),
		chromedp.Evaluate(`(() => {
			const app = document.documentElement._x_dataStack[0];
			window.__transcriptMeasurementCount = 0;
			window.__transcriptCallbackCount = 0;
			window.__originalMeasureRenderedTimelineItems = app.measureRenderedTimelineItems;
			app.measureRenderedTimelineItems = () => { window.__transcriptMeasurementCount++; };
			for (let index = 0; index < 3; index++) {
				app.afterTranscriptDOMUpdate(() => { window.__transcriptCallbackCount++; });
			}
		})()`, nil),
		chromedp.Poll(`window.__transcriptMeasurementCount === 1 && window.__transcriptCallbackCount === 3`, nil),
		chromedp.Evaluate(`document.documentElement._x_dataStack[0].measureRenderedTimelineItems = window.__originalMeasureRenderedTimelineItems`, nil),
		chromedp.Evaluate(`(() => {
			const app = document.documentElement._x_dataStack[0];
			let timeline = app.patchTimelineItemAppend([], 'chat-1', {item_id: 'item-1', seq: 2, text: 'hello'});
			timeline = app.patchTimelineItemAppend(timeline, 'chat-1', {item_id: 'item-1', reasoning: 'think'});
			timeline = app.patchTimelineItemAppend(timeline, 'chat-1', {item_id: 'item-1', text: ' world'});
			timeline = app.patchTimelineItem(timeline, {
				id: 'item-1', kind: 'assistant', sealed_at: '2026-08-23T12:00:00Z',
				content: {text: 'authoritative final', reasoning: {text: 'final thought'}}
			});
			window.__timelineAppendResult = timeline[0];
			window.__timelineStreamingOptions = app.itemMarkdownOptions({id: 'item-2'});
			window.__timelineFinalOptions = app.itemMarkdownOptions(timeline[0]);
		})()`, nil),
		chromedp.Poll(`window.__timelineAppendResult?.content?.text === 'authoritative final' &&
			window.__timelineAppendResult?.content?.reasoning?.text === 'final thought' &&
			window.__timelineStreamingOptions?.incremental === true &&
			window.__timelineFinalOptions?.incremental === false`, nil),
		chromedp.Evaluate(`(() => {
			window.__mermaidRenderCalls = [];
			window.mermaid.render = (id, source) => {
				window.__mermaidRenderCalls.push(source);
				const result = {svg: '<svg xmlns="http://www.w3.org/2000/svg"><text>' + source + '</text></svg>'};
				if (window.__mermaidRenderCalls.length > 1) return Promise.resolve(result);
				return new Promise(resolve => { window.__resolveFirstMermaidRender = () => resolve(result); });
			};
			const host = document.createElement('div');
			host.id = 'mermaid-observer-test';
			document.querySelector('.transcript').append(host);
			host.innerHTML = '<div class="mermaid-diagram" data-mermaid-state="pending"><pre>graph TD; A--&gt;B</pre></div>';
		})()`, nil),
		chromedp.Poll(`window.__mermaidRenderCalls?.length === 1`, nil),
		chromedp.Evaluate(`(() => {
			const host = document.querySelector('#mermaid-observer-test');
			host.innerHTML = '<div class="mermaid-diagram" data-mermaid-state="pending"><pre>graph TD; C--&gt;D</pre></div>';
			window.__resolveFirstMermaidRender();
		})()`, nil),
		chromedp.Poll(`(() => {
			const diagram = document.querySelector('#mermaid-observer-test .mermaid-diagram');
			return window.__mermaidRenderCalls?.length === 2 &&
				diagram?.dataset.mermaidState === 'done' &&
					diagram.textContent.includes('graph TD; C-->D');
		})()`, nil),
		chromedp.Evaluate(`document.querySelector('#mermaid-observer-test').insertAdjacentHTML('beforeend', '<div class="markdown-body"><img alt="preview" src="data:image/gif;base64,R0lGODlhAQABAAAAACw="></div>')`, nil),
		chromedp.Poll(`document.querySelector('#mermaid-observer-test img')?.closest('.markdown-media-preview')?.querySelector('.media-expand-button') !== null`, nil),
	); err != nil {
		t.Fatalf("enhance replacement Markdown media: %v", err)
	}
}
