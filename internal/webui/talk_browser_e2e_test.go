package webui

import (
	"context"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// Any chat can be talked to from the Web UI, not only one created for voice.
func TestTalkWorksInAnOrdinaryChat(t *testing.T) {
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
		chromedp.Flag("use-fake-ui-for-media-stream", true),
		chromedp.Flag("use-fake-device-for-media-stream", true),
	)...)
	t.Cleanup(stopAllocator)
	browserCtx, stopBrowser := chromedp.NewContext(allocatorCtx)
	t.Cleanup(stopBrowser)
	browserCtx, cancel := context.WithTimeout(browserCtx, 60*time.Second)
	t.Cleanup(cancel)

	var before, after struct {
		Badge, Composer bool
		Talk            string
		Toast           string
	}
	read := `(() => {
		const app = document.documentElement._x_dataStack[0];
		const talk = document.querySelector('[data-talk]');
		return {
			Badge: app.chatCreatedForVoice(),
			Composer: !!document.querySelector('.voice-composer')?.offsetParent,
			Talk: talk && talk.offsetParent ? talk.getAttribute('title') : '',
			Toast: app.toast || '',
		};
	})()`
	if err := chromedp.Run(browserCtx,
		chromedp.Navigate(server.URL()+"/s/"+string(state.Session.ID)+"/c/"+string(state.ActiveChatID)),
		chromedp.WaitVisible(`[data-talk]`, chromedp.ByQuery),
		chromedp.Evaluate(read, &before),
		chromedp.Click(`[data-talk]`, chromedp.ByQuery),
		chromedp.Poll(`!!document.querySelector('.voice-composer')?.offsetParent`, nil),
		chromedp.Evaluate(read, &after),
	); err != nil {
		t.Fatalf("talk to an ordinary chat: %v", err)
	}
	if before.Badge || before.Composer || before.Talk != "Talk to this chat" {
		t.Fatalf("ordinary chat before talking = %+v", before)
	}
	if !after.Composer || after.Toast == "Select a voice chat first" {
		t.Fatalf("ordinary chat after Talk = %+v", after)
	}
}
