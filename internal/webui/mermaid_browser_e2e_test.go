package webui

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

func TestTranscriptClientBatchesAndReconcilesEnhancements(t *testing.T) {
	var scrollAnchorResult struct {
		ItemID      string  `json:"itemID"`
		Top         float64 `json:"top"`
		FallbackTop float64 `json:"fallbackTop"`
		BottomGap   float64 `json:"bottomGap"`
	}
	var mermaidLightboxResult struct {
		HTMLLength int     `json:"htmlLength"`
		Width      float64 `json:"width"`
		Height     float64 `json:"height"`
	}
	var interruptedBottomScrollResult struct {
		Top          float64 `json:"top"`
		BottomGap    float64 `json:"bottomGap"`
		Stick        bool    `json:"stick"`
		LoadingShift float64 `json:"loadingShift"`
		ScrollHeight float64 `json:"scrollHeight"`
		ClientHeight float64 `json:"clientHeight"`
	}
	var paginationAnchorResult struct {
		ItemID       string  `json:"itemID"`
		BeforeOffset float64 `json:"beforeOffset"`
		AfterOffset  float64 `json:"afterOffset"`
		Count        int     `json:"count"`
		LatestShown  bool    `json:"latestShown"`
	}
	chromium := memoryBrowserChromium(t)
	ctrl := newTestController(t)
	state := selectedTestState(t, ctrl)
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

	if err := chromedp.Run(browserCtx,
		chromedp.Navigate(server.URL()+"/s/"+string(state.Session.ID)+"/c/"+string(state.ActiveChatID)),
		chromedp.WaitReady(`.transcript`, chromedp.ByQuery),
		chromedp.Poll(`document.documentElement._x_dataStack?.[0]?.transcriptEnhancementObserver instanceof MutationObserver`, nil),
		chromedp.Evaluate(`new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))`, nil),
		chromedp.Evaluate(`(() => {
			const app = document.documentElement._x_dataStack[0];
			window.__transcriptCallbackCount = 0;
			for (let index = 0; index < 3; index++) {
				app.afterTranscriptDOMUpdate(() => { window.__transcriptCallbackCount++; });
			}
		})()`, nil),
		chromedp.Poll(`window.__transcriptCallbackCount === 3`, nil),
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
			const app = document.documentElement._x_dataStack[0];
			const chatID = 'freeze-chat';
			app.state.active_chat_id = chatID;
			app.state.ActiveChatID = chatID;
			const existing = {id: 'freeze-existing', chat_id: chatID, kind: 'user', content: {text: 'reading this'}};
			app.storeTimeline(chatID, [existing]);
			const snapshot = {...app.activeSnapshot(), TimelineHasNewer: false, timeline_has_newer: false};
			app.state.snapshots = {...(app.state.snapshots || {}), [chatID]: snapshot};
			app.state.Snapshots = app.state.snapshots;
			app.state.snapshot = snapshot;
			app.state.Snapshot = snapshot;
			app.transcriptStickToBottom = false;
			app.applyChatDelta({chat_id: chatID, item: {id: 'freeze-new', chat_id: chatID, kind: 'assistant', content: {text: 'new tail'}}});
			window.__scrolledBackTimelineFrozen = app.timeline().length === 1 &&
				app.timeline()[0].id === 'freeze-existing' && app.timelineHasNewer();
		})()`, nil),
		chromedp.Poll(`window.__scrolledBackTimelineFrozen === true`, nil),
		chromedp.Evaluate(`(() => {
			window.__mermaidRenderCalls = [];
			window.mermaid.render = (id, source) => {
				window.__mermaidRenderCalls.push(source);
				const result = {svg: '<svg xmlns="http://www.w3.org/2000/svg" width="100%" viewBox="0 0 640 320"><text x="20" y="40">' + source + '</text></svg>'};
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
		chromedp.Evaluate(`(() => {
			const app = document.documentElement._x_dataStack[0];
			const originalTranscriptElement = app.transcriptElement;
			const originalTimelineHasNewer = app.timelineHasNewer;
			const transcript = document.createElement('div');
			transcript.style.cssText = 'position:fixed;top:0;left:0;width:400px;height:180px;overflow:auto';
			document.body.append(transcript);
			app.transcriptElement = () => transcript;
			app.timelineHasNewer = () => false;
			for (let index = 0; index < 4; index++) {
				const row = document.createElement('section');
				row.className = 'transcript-turn';
				row.dataset.timelineItemId = 'anchor-' + index;
				row.style.cssText = 'display:block;height:120px;min-height:120px';
				row.textContent = 'row ' + index;
				transcript.append(row);
			}
			transcript.scrollTop = 90;
			app.transcriptStickToBottom = false;
			const anchor = app.transcriptScrollState();
			const anchoredItemID = anchor.itemID;
			const first = transcript.querySelector('[data-timeline-item-id="anchor-0"]');
			first.style.height = '320px';
			app.restoreTranscriptScroll(anchor);
			const anchoredTop = transcript.scrollTop;
			anchor.itemID = 'missing-anchor';
			anchor.top = 75;
			app.restoreTranscriptScroll(anchor);
			const fallbackTop = transcript.scrollTop;
			app.transcriptStickToBottom = true;
			transcript.scrollTop = transcript.scrollHeight;
			const sticky = app.transcriptScrollState();
			const tail = document.createElement('section');
			tail.className = 'transcript-turn';
			tail.dataset.timelineItemId = 'anchor-tail';
			tail.style.cssText = 'display:block;height:120px;min-height:120px';
			transcript.append(tail);
			app.restoreTranscriptScroll(sticky);
			const result = {
				itemID: anchoredItemID,
				top: anchoredTop,
				fallbackTop,
				bottomGap: app.transcriptBottomDistance(transcript),
			};
			app.transcriptElement = originalTranscriptElement;
			app.timelineHasNewer = originalTimelineHasNewer;
			transcript.remove();
			return result;
		})()`, &scrollAnchorResult),
		chromedp.Evaluate(`(() => {
			const app = document.documentElement._x_dataStack[0];
			const originalRequestAnimationFrame = window.requestAnimationFrame;
			const originalTimelineHasNewer = app.timelineHasNewer;
			const callbacks = [];
			window.requestAnimationFrame = callback => { callbacks.push(callback); return callbacks.length; };
			const originalTranscriptElement = app.transcriptElement;
			const transcript = document.createElement('div');
			transcript.style.cssText = 'position:fixed;top:0;left:0;width:400px;height:180px;overflow:auto';
			document.body.append(transcript);
			app.transcriptElement = () => transcript;
			app.timelineHasNewer = () => false;
			for (let index = 0; index < 4; index++) {
				const row = document.createElement('section');
				row.style.cssText = 'display:block;height:120px;min-height:120px';
				row.textContent = 'row ' + index;
				transcript.append(row);
			}
			app.transcriptStickToBottom = true;
			app.scrollTranscriptToBottom();
			app.markTranscriptUserScrollIntent();
			app.setTranscriptStickToBottom(false);
			transcript.scrollTop = 80;
			const tail = document.createElement('div');
			tail.style.cssText = 'display:block;height:120px;min-height:120px;overflow-anchor:none';
			transcript.append(tail);
			while (callbacks.length) callbacks.shift()();
			const loadingAnchor = document.querySelector('.timeline-load-indicator-anchor');
			const result = {
				top: transcript.scrollTop,
				bottomGap: app.transcriptBottomDistance(transcript),
				stick: app.transcriptStickToBottom,
				loadingShift: loadingAnchor?.getBoundingClientRect().height || 0,
				scrollHeight: transcript.scrollHeight,
				clientHeight: transcript.clientHeight,
			};
			if (app.transcriptUserScrollTimer) clearTimeout(app.transcriptUserScrollTimer);
			app.transcriptUserScrollTimer = null;
			app.transcriptUserScrollActive = false;
			app.transcriptElement = originalTranscriptElement;
			app.timelineHasNewer = originalTimelineHasNewer;
			app.transcriptStickToBottom = true;
			window.requestAnimationFrame = originalRequestAnimationFrame;
			transcript.remove();
			return result;
		})()`, &interruptedBottomScrollResult),
		chromedp.Evaluate(`document.querySelector('.transcript').insertAdjacentHTML('beforeend', '<div id="media-observer-test" class="markdown-body"><img alt="preview" src="data:image/gif;base64,R0lGODlhAQABAAAAACw="></div>')`, nil),
		chromedp.Poll(`document.querySelector('#media-observer-test img')?.closest('.markdown-media-preview')?.querySelector('.media-expand-button') !== null`, nil),
		chromedp.Evaluate(`document.querySelector('#mermaid-observer-test .media-expand-button').click()`, nil),
		chromedp.Poll(`document.querySelector('.image-lightbox-svg')?.offsetParent !== null`, nil),
		chromedp.Evaluate(`(() => {
			const app = document.documentElement._x_dataStack[0];
			const svg = document.querySelector('.image-lightbox-svg svg');
			const rect = svg?.getBoundingClientRect();
			return {htmlLength: app.imageLightbox.html.length, width: rect?.width || 0, height: rect?.height || 0};
		})()`, &mermaidLightboxResult),
		chromedp.Evaluate(`(() => {
			const app = document.documentElement._x_dataStack[0];
			window.__paginationAnchorResult = null;
			const chatID = String(app.activeChatID());
			const makeItem = (prefix, index) => ({
				id: prefix + '-' + index,
				chat_id: chatID,
				kind: 'user',
				content: {text: prefix + ' row ' + index + '\n\n' + ('content '.repeat(20))},
			});
			const current = Array.from({length: 20}, (_, index) => makeItem('current', index));
			app.storeTimeline(chatID, current);
			const snapshot = {...app.activeSnapshot(), TimelineHasMore: true, TimelineHasNewer: false};
			app.state.snapshots = {...(app.state.snapshots || {}), [chatID]: snapshot};
			app.state.Snapshots = app.state.snapshots;
			app.state.snapshot = snapshot;
			app.state.Snapshot = snapshot;
			app.$nextTick(() => requestAnimationFrame(async () => {
				const transcript = app.transcriptElement();
				const row = transcript.querySelector('[data-timeline-item-id="current-5"]');
				transcript.scrollTop += row.getBoundingClientRect().top - transcript.getBoundingClientRect().top;
				app.setTranscriptStickToBottom(false);
				const scroll = app.transcriptScrollState();
				const beforeOffset = row.getBoundingClientRect().top - transcript.getBoundingClientRect().top;
				const older = Array.from({length: 10}, (_, index) => makeItem('older', index));
				await app.mergeTimelinePage({chat_id: chatID, items: older, has_more: false, has_newer: true}, {prepend: true, scroll});
				const anchored = transcript.querySelector('[data-timeline-item-id="current-5"]');
				window.__paginationAnchorResult = {
					itemID: scroll.itemID,
					beforeOffset,
					afterOffset: anchored.getBoundingClientRect().top - transcript.getBoundingClientRect().top,
					count: app.timeline().length,
					latestShown: document.querySelector('.timeline-latest-button')?.offsetParent !== null,
				};
			}));
		})()`, nil),
		chromedp.Poll(`window.__paginationAnchorResult !== null`, nil),
		chromedp.Evaluate(`window.__paginationAnchorResult`, &paginationAnchorResult),
	); err != nil {
		t.Fatalf("enhance replacement Markdown media: %v", err)
	}
	if scrollAnchorResult.ItemID != "anchor-1" || scrollAnchorResult.Top != 290 || scrollAnchorResult.FallbackTop != 75 || scrollAnchorResult.BottomGap != 0 {
		t.Fatalf("restore transcript position = %+v, want anchored top 290, fallback top 75, and sticky bottom gap 0", scrollAnchorResult)
	}
	if interruptedBottomScrollResult.Top != 80 || interruptedBottomScrollResult.BottomGap <= 0 || interruptedBottomScrollResult.Stick || interruptedBottomScrollResult.LoadingShift != 0 {
		t.Fatalf("interrupted bottom scroll = %+v, want top 80, detached scroll, and zero-height loading overlay", interruptedBottomScrollResult)
	}
	if mermaidLightboxResult.HTMLLength == 0 || mermaidLightboxResult.Width <= 0 || mermaidLightboxResult.Height <= 0 {
		t.Fatalf("expanded Mermaid dimensions = %+v, want retained SVG with positive width and height", mermaidLightboxResult)
	}
	if paginationAnchorResult.ItemID != "current-5" || paginationAnchorResult.Count != 30 || !paginationAnchorResult.LatestShown || math.Abs(paginationAnchorResult.BeforeOffset-paginationAnchorResult.AfterOffset) > 0.5 {
		t.Fatalf("pagination anchor = %+v, want current-5 at an unchanged offset across a 30-item prepended window", paginationAnchorResult)
	}
}
