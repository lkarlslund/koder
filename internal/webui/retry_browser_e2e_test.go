package webui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/lkarlslund/koder/internal/app"
	"github.com/lkarlslund/koder/internal/chat"
)

// While a failed model request waits to be retried, the composer says why
// and when, and offers to try now or give up.
func TestRetryBannerTriesNowAndCancels(t *testing.T) {
	chromium := chromiumForTest(t)
	// Keep failures local and immediate. DNS/network delays can outlast the
	// retry banner's cancel window and make this test hang on shutdown.
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(provider.Close)
	ctrl, _, _ := newTestControllerAndEngine(t, t.TempDir(), provider.URL+"/v1")
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

	selection := app.Selection{SessionID: state.Session.ID, ChatID: state.ActiveChatID}
	if err := chromedp.Run(browserCtx,
		chromedp.EmulateViewport(1440, 1000),
		chromedp.Navigate(server.URL()+"/s/"+string(state.Session.ID)+"/c/"+string(state.ActiveChatID)),
		chromedp.WaitVisible(`.composer`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("open page: %v", err)
	}
	if err := ctrl.SendPromptWithKindSelection(context.Background(), selection, chat.QueueKindUser, "Hello", nil); err != nil {
		t.Fatal(err)
	}

	type banner struct {
		Headline, Error string
		TryNow, Cancel  bool
	}
	read := `(() => {
		const el = document.querySelector('[data-retry]');
		const enabled = label => { const b = Array.from(el.querySelectorAll('button')).find(b => b.textContent.trim() === label); return !!b && !!b.offsetParent && !b.disabled; };
		return {
			Headline: el.querySelector('.retry-banner-headline').textContent.trim(),
			Error: el.querySelector('.retry-banner-error').textContent.trim(),
			TryNow: enabled('Try now'),
			Cancel: enabled('Cancel'),
		};
	})()`
	var first, second banner
	if err := chromedp.Run(browserCtx,
		chromedp.Poll(`!!document.querySelector('[data-retry]')?.offsetParent && document.querySelector('.retry-banner-headline').textContent.includes('attempt 1 of 10')`, nil),
		chromedp.Evaluate(read, &first),
		chromedp.Evaluate(`document.querySelector("[data-retry] .btn-primary").click()`, nil),
		chromedp.Poll(`document.querySelector('.retry-banner-headline')?.textContent.includes('attempt 2 of 10')`, nil, chromedp.WithPollingTimeout(5*time.Second)),
		chromedp.Evaluate(read, &second),
	); err != nil {
		t.Fatalf("retry banner: %v", err)
	}
	if !strings.Contains(first.Headline, "retrying in ") || first.Error == "" || !first.TryNow || !first.Cancel {
		t.Fatalf("first wait = %+v", first)
	}
	if !strings.Contains(second.Headline, "retrying in ") {
		t.Fatalf("after try now = %+v", second)
	}

	var gone bool
	if err := chromedp.Run(browserCtx,
		chromedp.Evaluate(`document.querySelector("[data-retry] .btn-outline-secondary").click()`, nil),
		chromedp.Poll(`!document.querySelector('[data-retry]')?.offsetParent`, nil, chromedp.WithPollingTimeout(3*time.Second)),
		chromedp.Evaluate(`!document.querySelector('[data-retry]')?.offsetParent`, &gone),
	); err != nil {
		t.Fatalf("cancel retry: %v", err)
	}
	after, err := ctrl.StateForSelection(context.Background(), selection)
	if err != nil {
		t.Fatal(err)
	}
	if !gone || after.Snapshot.Active || after.Snapshot.Status != chat.StatusErrored {
		t.Fatalf("after cancel: gone=%v active=%v status=%v", gone, after.Snapshot.Active, after.Snapshot.Status)
	}
}
