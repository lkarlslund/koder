package webui

import (
	"context"
	"strings"
	"testing"
	"time"

	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// While an agent turn streams tool calls, a transcript scrolled to the bottom
// stays at the bottom: rows arriving, growing and trimming the oldest item
// must not move the view or unpin it.
func TestStreamingToolCallsKeepTranscriptAtBottom(t *testing.T) {
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

	var got struct {
		Log      []string
		Unpinned int
		MaxGap   float64
	}
	if err := chromedp.Run(browserCtx,
		chromedp.EmulateViewport(1440, 900),
		chromedp.Navigate(server.URL()+"/s/"+string(state.Session.ID)+"/c/"+string(state.ActiveChatID)),
		chromedp.WaitReady(`.transcript`, chromedp.ByQuery),
		chromedp.Poll(`document.documentElement._x_dataStack?.[0]?.connected === true && String(document.documentElement._x_dataStack[0].activeChatID() || '').length > 10`, nil),
		chromedp.Evaluate(`(async () => {
			const app = document.documentElement._x_dataStack[0];
			const chatID = String(app.activeChatID());
			const frame = () => new Promise(r => requestAnimationFrame(() => r()));
			const tr = document.querySelector('.transcript');
			let n = 0;
			const id = () => '01a1a1c9-0000-7000-8000-' + String(++n).padStart(12, '0');
			const at = () => new Date(Date.UTC(2026, 10, 3, 13, 0, n)).toISOString();
			const tool = (status, desc, result) => ({tool_call_id: 'tc' + n, tool: 'exec_command', status, args: desc ? {cmd: 'true', description: desc} : {}, ...(result ? {result} : {})});
			const send = item => app.applyChatDelta({chat_id: chatID, item, active: true, status: 'running_tools'});
			// Fill past the memory window so new items trim the oldest one.
			const seed = [{id: id(), kind: 'user', created_at: at(), content: {text: 'start', source: 'user'}}];
			for (let i = 0; i < 140; i++) {
				seed.push({id: id(), kind: 'assistant', created_at: at(), content: i % 4 === 0
					? {text: 'Step ' + i + ' explains what the agent is doing next in one sentence.', tools: [tool('done', 'Earlier step ' + i, {data: {exit_code: 0}})]}
					: {tools: [tool('done', 'Earlier step ' + i, {data: {exit_code: 0}})]}});
			}
			await app.mergeTimelinePage({chat_id: chatID, items: seed, has_more: false}, {replace: true, scrollToBottom: true});
			for (let i = 0; i < 10; i++) await frame();
			const log = [];
			let unpinned = 0, maxGap = 0;
			const sample = label => {
				const gap = tr.scrollHeight - tr.scrollTop - tr.clientHeight;
				if (gap > maxGap) maxGap = gap;
				if (!app.transcriptStickToBottom) unpinned++;
				log.push(label + ' gap=' + Math.round(gap) + ' stick=' + app.transcriptStickToBottom + ' h=' + tr.scrollHeight + ' items=' + app.timeline().length + ' rows=' + tr.querySelectorAll('.transcript-turn').length + ' ch=' + tr.clientHeight);
			};
			sample('start');
			for (let step = 0; step < 6; step++) {
				const itemID = id();
				const base = {id: itemID, kind: 'assistant', created_at: at()};
				const phases = [
					{...base, content: {}},
					{...base, content: {reasoning: {text: 'thinking', tokens: 12}}},
					{...base, content: {reasoning: {text: 'thinking', tokens: 12}, tools: [tool('pending', '')]}},
					{...base, content: {reasoning: {text: 'thinking', tokens: 12}, tools: [tool('pending', 'Run step ' + step)]}},
					{...base, content: {reasoning: {text: 'thinking', tokens: 12}, tools: [tool('running', 'Run step ' + step)]}},
					{...base, content: {reasoning: {text: 'thinking', tokens: 12}, tools: [tool('done', 'Run step ' + step, {data: {exit_code: 0}})]}},
				];
				for (const [p, item] of phases.entries()) {
					send(item);
					for (let f = 0; f < 3; f++) { await frame(); sample('step' + step + '.' + p + '.f' + f); }
				}
			}
			return {Log: log, Unpinned: unpinned, MaxGap: maxGap};
		})()`, &got, func(p *cdpruntime.EvaluateParams) *cdpruntime.EvaluateParams { return p.WithAwaitPromise(true) }),
	); err != nil {
		t.Fatalf("stream: %v", err)
	}
	if got.Unpinned > 0 || got.MaxGap > 2 {
		t.Errorf("transcript left the bottom while tools streamed: unpinned frames=%d max gap=%.0fpx\n%s", got.Unpinned, got.MaxGap, strings.Join(got.Log, "\n"))
	}
}
