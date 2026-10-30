package webui

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

func TestSessionPickerListsEverythingByLastUse(t *testing.T) {
	chromium := chromiumForTest(t)
	ctrl := newTestController(t)
	ctx := context.Background()
	older, err := ctrl.CreateSession(ctx, "Older session", t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	quick, _, err := ctrl.CreateQuickChat(ctx)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	newer, err := ctrl.CreateSession(ctx, "Newer session", t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{string(newer.ID), string(quick.ID), string(older.ID)}

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

	var rows []string
	if err := chromedp.Run(browserCtx,
		chromedp.Navigate(server.URL()),
		chromedp.Poll(`document.querySelectorAll('.session-list .session-selector-row').length >= 3`, nil),
		chromedp.Evaluate(`(() => {
			const app = document.documentElement._x_dataStack[0];
			return app.sessionPickerRows().map(s => app.sessionID(s));
		})()`, &rows),
	); err != nil {
		t.Fatalf("read session picker: %v", err)
	}
	var got []string
	for _, id := range rows {
		if slices.Contains(want, id) {
			got = append(got, id)
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("picker order = %v, want newest session, then quick chat, then older session %v", got, want)
	}
	var rendered int
	if err := chromedp.Run(browserCtx, chromedp.Evaluate(`document.querySelectorAll('.session-list > .session-selector-row').length`, &rendered)); err != nil {
		t.Fatal(err)
	}
	if rendered != len(rows) {
		t.Fatalf("rendered %d rows directly in the list, want %d", rendered, len(rows))
	}
}
