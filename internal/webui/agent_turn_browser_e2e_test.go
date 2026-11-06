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

// The steps of one agent turn share a single agent header. Thinking, timing
// and the rollback and fork actions are in a popup opened from that header.
func TestAgentTurnStepsShareOneHeaderWithDetailsPopup(t *testing.T) {
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
		{id: 'u1', kind: 'user', created_at: '2026-11-03T13:14:00Z', content: {text: 'deploy ver 19', source: 'user'}},
		{id: 'a1', kind: 'assistant', created_at: '2026-11-03T13:15:16Z', content: {
			text: 'Baseline saved. Deploying the new version.',
			reasoning: {text: 'Write the baseline first.', tokens: 60},
			performance: {time_to_first_response_ms: 213, cached_prompt_tokens: 970, processed_prompt_tokens: 30, prompt_tokens_per_second: 8000, generation_tokens_per_second: 414},
			tools: [{tool_call_id: 't1', tool: 'exec_command', status: 'done', args: {cmd: 'deploy', description: 'Deploy the new version and start the service'}, result: {data: {exit_code: 0}}}]}},
		{id: 'a2', kind: 'assistant', created_at: '2026-11-03T13:15:23Z', content: {
			reasoning: {text: 'Now reload.', tokens: 28},
			tools: [{tool_call_id: 't2', tool: 'exec_command', status: 'done', args: {cmd: 'reload', description: 'Trigger reload and report deploy state'}, result: {data: {exit_code: 0}}}]}},
		{id: 'a3', kind: 'assistant', created_at: '2026-11-03T13:15:47Z', content: {
			reasoning: {text: 'Wait for it.', tokens: 35},
			tools: [{tool_call_id: 't3', tool: 'exec_session', status: 'done', args: {action: 'wait', process_id: 'exec_1'}, result: {data: {state: 'exited'}}}]}},
		{id: 'a4', kind: 'assistant', created_at: '2026-11-03T13:15:50Z', content: {text: 'The new version is deployed and the drain is queued.', reasoning: {text: 'Report.', tokens: 68}}},
		{id: 'u2', kind: 'user', created_at: '2026-11-03T13:16:00Z', content: {text: 'thanks', source: 'user'}},
		{id: 'a5', kind: 'assistant', created_at: '2026-11-03T13:16:05Z', content: {text: 'You are welcome.'}}
	]`
	setup := fmt.Sprintf(`(() => {
		const app = document.documentElement._x_dataStack[0];
		const items = %s;
		app.renderedTimeline = () => items;
		return true;
	})()`, timeline)

	var got struct {
		Headers, ThinkingInTranscript, PerfInTranscript int
		Gaps                                            []float64
		Steps                                           []string
		Thinking                                        []string
		Perf                                            string
		RollbackItem                                    string
		PopupClosed                                     bool
	}
	if err := chromedp.Run(browserCtx,
		chromedp.EmulateViewport(1440, 900),
		chromedp.Navigate(server.URL()+"/s/"+string(state.Session.ID)+"/c/"+string(state.ActiveChatID)),
		chromedp.WaitReady(`.transcript`, chromedp.ByQuery),
		chromedp.Poll(`!!document.documentElement._x_dataStack?.[0]?.toolResultHTML`, nil),
		chromedp.Evaluate(setup, nil),
		chromedp.Poll(`[...document.querySelectorAll('.tool-line')].filter(el => el.offsetParent).length === 3`, nil),
		chromedp.Evaluate(`(async () => {
			const app = document.documentElement._x_dataStack[0];
			const visible = el => !!el && !!el.offsetParent;
			const tick = () => new Promise(r => setTimeout(r, 80));
			const transcript = document.querySelector('.transcript');
			const lines = [...transcript.querySelectorAll('.tool-line')].filter(visible);
			const gaps = lines.slice(1).map((line, i) => line.getBoundingClientRect().top - lines[i].getBoundingClientRect().bottom);
			const result = {
				Headers: [...transcript.querySelectorAll('.assistant-header')].filter(visible).length,
				ThinkingInTranscript: [...transcript.querySelectorAll('.assistant-reasoning')].filter(visible).length,
				PerfInTranscript: [...transcript.querySelectorAll('.turn-performance')].filter(visible).length,
				Gaps: gaps,
			};
			[...transcript.querySelectorAll('.turn-details-button')].find(visible).click();
			await tick();
			const modal = document.querySelector('.turn-details-modal');
			result.Steps = [...modal.querySelectorAll('.turn-detail-summary')].filter(visible).map(el => el.textContent.trim());
			result.Thinking = [...modal.querySelectorAll('.assistant-reasoning > summary')].filter(visible).map(el => el.textContent.trim());
			result.Perf = [...modal.querySelectorAll('.turn-performance-detail')].filter(visible).map(el => el.textContent).join('|');
			modal.querySelectorAll('.turn-detail-step')[1].querySelector('[title^="Rollback"]').click();
			await tick();
			result.PopupClosed = !app.turnDetails.open;
			result.RollbackItem = app.timelineAction.open && app.timelineAction.mode === 'rollback' ? app.timelineAction.itemID : '';
			app.timelineAction = {...app.timelineAction, open: false};
			return result;
		})()`, &got, func(p *cdpruntime.EvaluateParams) *cdpruntime.EvaluateParams { return p.WithAwaitPromise(true) }),
	); err != nil {
		t.Fatalf("agent turn: %v", err)
	}

	if got.Headers != 2 {
		t.Errorf("visible agent headers = %d, want one per turn (2)", got.Headers)
	}
	if got.ThinkingInTranscript != 0 || got.PerfInTranscript != 0 {
		t.Errorf("transcript shows %d thinking and %d timing labels, want none", got.ThinkingInTranscript, got.PerfInTranscript)
	}
	for i, gap := range got.Gaps {
		if gap > 12 {
			t.Errorf("gap %d between tool rows of one turn is %.0fpx", i, gap)
		}
	}
	wantSteps := []string{
		"Baseline saved. Deploying the new version.",
		"Trigger reload and report deploy state",
		"Wait for exec_1",
		"The new version is deployed and the drain is queued.",
	}
	if strings.Join(got.Steps, "\n") != strings.Join(wantSteps, "\n") {
		t.Errorf("popup steps = %q", got.Steps)
	}
	if strings.Join(got.Thinking, ",") != "thinking (60 tokens),thinking (28 tokens),thinking (35 tokens),thinking (68 tokens)" {
		t.Errorf("popup thinking = %q", got.Thinking)
	}
	if !strings.Contains(got.Perf, "Time to first response: 213ms") || !strings.Contains(got.Perf, "Prompt cache: 97") {
		t.Errorf("popup timing = %q", got.Perf)
	}
	if !got.PopupClosed || got.RollbackItem != "a2" {
		t.Errorf("rollback from popup: closed=%v item=%q", got.PopupClosed, got.RollbackItem)
	}

	if dir := os.Getenv("KODER_UI_SHOTS"); dir != "" {
		for _, width := range []int64{1440, 820, 420} {
			var turn, popup []byte
			if err := chromedp.Run(browserCtx,
				chromedp.Evaluate(`document.documentElement._x_dataStack[0].closeTurnDetails()`, nil),
				chromedp.EmulateViewport(width, 900),
				chromedp.Sleep(200*time.Millisecond),
				chromedp.CaptureScreenshot(&turn),
				chromedp.Evaluate(`[...document.querySelectorAll('.turn-details-button')].find(el => el.offsetParent).click()`, nil),
				chromedp.Sleep(300*time.Millisecond),
				chromedp.Evaluate(`document.querySelector('.turn-details-modal details')?.setAttribute('open', '')`, nil),
				chromedp.Sleep(100*time.Millisecond),
				chromedp.CaptureScreenshot(&popup),
			); err != nil {
				t.Fatal(err)
			}
			_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("turn-%d.png", width)), turn, 0o644)
			_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("turn-popup-%d.png", width)), popup, 0o644)
		}
	}
}
