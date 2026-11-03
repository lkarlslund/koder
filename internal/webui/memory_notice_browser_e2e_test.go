package webui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/lkarlslund/koder/internal/domain"
)

// A memory notification shows as a message from Koder, labelled memory,
// with its text kept as written.
func TestMemoryNotificationRendersAsKoderNotice(t *testing.T) {
	chromium := chromiumForTest(t)
	ctrl, engine, _ := newTestControllerAndEngine(t, t.TempDir())
	state := selectedTestState(t, ctrl)
	ctx := context.Background()
	owner, err := engine.LoadSession(ctx, state.Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	rt, err := owner.Chat(ctx, state.ActiveChatID)
	if err != nil {
		t.Fatal(err)
	}
	notice := "Memory changed since the memory index in your instructions was written, by another chat or by hand.\n- updated global memory time-zone, now: Europe/Copenhagen time zone"
	if _, err := rt.AppendUserMessage(ctx, domain.UserMessage{Text: notice, Source: domain.UserMessageSourceMemory}); err != nil {
		t.Fatal(err)
	}

	serverCtx, stopServer := context.WithCancel(ctx)
	server := startBrowserTestServer(t, serverCtx, ctrl)
	t.Cleanup(func() {
		stopServer()
		_ = server.server.Close()
	})
	allocatorCtx, stopAllocator := chromedp.NewExecAllocator(ctx, append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(chromium),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
	)...)
	t.Cleanup(stopAllocator)
	browserCtx, stopBrowser := chromedp.NewContext(allocatorCtx)
	t.Cleanup(stopBrowser)
	browserCtx, cancel := context.WithTimeout(browserCtx, 60*time.Second)
	t.Cleanup(cancel)

	var got struct {
		Who, Qualifier, Text string
		Icon                 bool
	}
	if err := chromedp.Run(browserCtx,
		chromedp.EmulateViewport(1440, 1000),
		chromedp.Navigate(server.URL()+"/s/"+string(state.Session.ID)+"/c/"+string(state.ActiveChatID)),
		chromedp.Poll(`!!document.querySelector('.user-turn .notification-message-text')?.offsetParent`, nil),
		chromedp.Evaluate(`(() => {
			const turn = document.querySelector('.user-turn .notification-message-text').closest('.user-turn');
			const visible = el => el && el.offsetParent ? el.textContent.trim() : '';
			return {
				Who: visible(turn.querySelector('.turn-header [x-text*="koder"]')),
				Qualifier: visible(turn.querySelector('.turn-source-qualifier')),
				Text: visible(turn.querySelector('.notification-message-text')),
				Icon: !!turn.querySelector('.bi-journal-bookmark'),
			};
		})()`, &got),
	); err != nil {
		t.Fatalf("memory notification: %v", err)
	}
	if got.Who != "koder" || got.Qualifier != "memory" || !got.Icon || !strings.Contains(got.Text, "- updated global memory time-zone, now: Europe/Copenhagen time zone") {
		t.Fatalf("memory notification = %+v", got)
	}
}
