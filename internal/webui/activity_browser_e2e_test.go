package webui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/lkarlslund/koder/internal/execruntime"
)

// The sidebar's activity list appears only while the chat has something
// live, shows each item as icon, description, runtime and status, and opens
// its details on click rather than on hover.
func TestSidebarActivityListsProcessesAndOpensDetails(t *testing.T) {
	chromium := chromiumForTest(t)
	workdir := t.TempDir()
	ctrl, engine, _ := newTestControllerAndEngine(t, workdir)
	state := selectedTestState(t, ctrl)
	manager := engine.ExecManager()
	if manager == nil {
		t.Skip("engine has no exec manager")
	}

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

	var shown bool
	if err := chromedp.Run(browserCtx,
		chromedp.EmulateViewport(1440, 1000),
		chromedp.Navigate(server.URL()+"/s/"+string(state.Session.ID)+"/c/"+string(state.ActiveChatID)),
		chromedp.WaitVisible(`.sidebar`, chromedp.ByQuery),
		chromedp.Sleep(300*time.Millisecond),
		chromedp.Evaluate(`!!document.querySelector('.activity-list')?.offsetParent`, &shown),
	); err != nil {
		t.Fatalf("open page: %v", err)
	}
	if shown {
		t.Fatal("the activity list showed with nothing in it")
	}

	process, err := manager.Start(context.Background(), execruntime.StartRequest{
		SessionID: state.Session.ID,
		ChatID:    state.ActiveChatID,
		Command:   "sleep 30",
		Workdir:   workdir,
		Label:     "Wait for the build server",
		YieldTime: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("start process: %v", err)
	}
	t.Cleanup(func() {
		_, _ = manager.Terminate(context.Background(), execruntime.TerminateRequest{SessionID: state.Session.ID, ChatID: state.ActiveChatID, ProcessID: process.ProcessID})
	})

	var row struct {
		Title, Subtitle, Time string
		Spinning, Tooltip     bool
	}
	if err := chromedp.Run(browserCtx,
		chromedp.Poll(`!!document.querySelector('[data-activity="process"] .activity-time')?.textContent.trim()`, nil),
		chromedp.Evaluate(`(() => {
			const row = document.querySelector('[data-activity="process"]');
			return {
				Title: row.querySelector('.activity-title').textContent.trim(),
				Subtitle: row.querySelector('.activity-subtitle').textContent.trim(),
				Time: row.querySelector('.activity-time').textContent.trim(),
				Spinning: !!row.querySelector('.activity-status .spinner-border')?.offsetParent,
				Tooltip: !!row.getAttribute('title') || !!document.querySelector('.exec-process-tooltip'),
			};
		})()`, &row),
	); err != nil {
		t.Fatalf("read process row: %v", err)
	}
	if row.Title != "Wait for the build server" || row.Subtitle != "sleep 30" || !strings.HasSuffix(row.Time, "s") || !row.Spinning || row.Tooltip {
		t.Fatalf("process row = %+v", row)
	}

	var modalTitle string
	if err := chromedp.Run(browserCtx,
		chromedp.Click(`[data-activity="process"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[title="Stop process"]`, chromedp.ByQuery),
		chromedp.Evaluate(`document.documentElement._x_dataStack[0].execProcessModalSubtitle()`, &modalTitle),
		chromedp.Click(`[title="Stop process"]`, chromedp.ByQuery),
		// A stopped process stays listed for a while, with its outcome.
		chromedp.Poll(`!!document.querySelector('[data-activity="process"] .activity-status.text-warning')`, nil),
	); err != nil {
		t.Fatalf("open and stop process %s: %v", process.ProcessID, err)
	}
	if !strings.HasPrefix(modalTitle, "Wait for the build server") {
		t.Fatalf("modal subtitle = %q", modalTitle)
	}

	// A finished process says how long it took, and leaves the list half a
	// minute after it ended.
	var took string
	var listedLater int
	if err := chromedp.Run(browserCtx,
		chromedp.Evaluate(`document.querySelector('[data-activity="process"] .activity-time').textContent.trim()`, &took),
		chromedp.Evaluate(`(() => {
			const app = document.documentElement._x_dataStack[0];
			const later = app.activityNow + 31000;
			return app.allExecProcesses().filter(p => app.execProcessRecent(p, later)).length;
		})()`, &listedLater),
	); err != nil {
		t.Fatalf("read finished process: %v", err)
	}
	if !strings.HasPrefix(took, "took ") || listedLater != 0 {
		t.Fatalf("finished process time = %q, still listed 31s later: %d", took, listedLater)
	}
}
