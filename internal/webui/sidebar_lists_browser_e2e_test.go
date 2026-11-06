package webui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/lkarlslund/koder/internal/cron"
	"github.com/lkarlslund/koder/internal/execruntime"
)

// The chat list's status filter is a dropdown beside its heading, chats and
// sessions with cron jobs or running commands carry a badge, and the git section stays out of a
// session that is not in a git repository.
func TestSidebarListsFilterAndMarkScheduledChats(t *testing.T) {
	chromium := chromiumForTest(t)
	workdir := t.TempDir()
	ctrl, engine, st := newTestControllerAndEngine(t, workdir)
	state := selectedTestState(t, ctrl)
	if _, err := cron.NewScheduler(st, nil).Create(context.Background(), state.Session.ID, state.ActiveChatID, "0 9 * * *", "Check the build", "Check the build every morning", true); err != nil {
		t.Fatalf("create cron job: %v", err)
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

	var page struct {
		HeaderLines  int
		ChatBadge    string
		GitShown     bool
		ChipRows     int
		Summary      string
		MenuOptions  int
		FilteredRows int
		AfterReset   string
	}
	if err := chromedp.Run(browserCtx,
		chromedp.EmulateViewport(1440, 1000),
		chromedp.Navigate(server.URL()+"/s/"+string(state.Session.ID)+"/c/"+string(state.ActiveChatID)),
		chromedp.WaitVisible(`[data-list-filter="chats"]`, chromedp.ByQuery),
		chromedp.Poll(`!!document.querySelector('[data-scheduled-chat]')?.offsetParent`, nil),
		chromedp.Evaluate(`(() => {
			const toggle = document.querySelector('[data-list-filter="chats"]');
			const row = toggle.closest('.sidebar-info-row');
			const tops = new Set(Array.from(row.children).filter(el => el.offsetParent).map(el => Math.round(el.getBoundingClientRect().top + el.getBoundingClientRect().height / 2)));
			const git = Array.from(document.querySelectorAll('.sidebar-heading')).find(el => el.textContent.trim() === 'Git');
			return {
				HeaderLines: tops.size,
				ChatBadge: document.querySelector('[data-scheduled-chat]').textContent.trim(),
				GitShown: !!git?.offsetParent,
				ChipRows: document.querySelectorAll('.status-filter-row').length,
				Summary: toggle.textContent.trim(),
			};
		})()`, &page),
		chromedp.Click(`[data-list-filter="chats"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`.list-filter-menu.show`, chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelectorAll('.list-filter-menu.show .list-filter-option').length`, &page.MenuOptions),
		// Hiding idle chats hides the only chat and says so on the toggle.
		chromedp.Evaluate(`Array.from(document.querySelectorAll('.list-filter-menu.show .list-filter-option')).find(el => el.textContent.includes('Idle')).querySelector('input').click()`, nil),
		chromedp.Poll(`document.querySelector('[data-list-filter="chats"]').textContent.trim() === 'No idle, archived'`, nil),
		chromedp.Poll(`document.querySelectorAll('.chat-list-item').length === 0`, nil, chromedp.WithPollingTimeout(2*time.Second)),
		chromedp.Evaluate(`document.querySelectorAll('.chat-list-item').length`, &page.FilteredRows),
		chromedp.Evaluate(`Array.from(document.querySelectorAll('.list-filter-menu.show .dropdown-item')).find(el => el.textContent.trim() === 'Show all').click()`, nil),
		chromedp.Poll(`document.querySelector('[data-list-filter="chats"]').textContent.trim() === 'All'`, nil),
		chromedp.Evaluate(`document.querySelector('[data-list-filter="chats"]').textContent.trim()`, &page.AfterReset),
	); err != nil {
		t.Fatalf("sidebar lists: %v", err)
	}
	if page.HeaderLines != 1 || page.ChatBadge != "1" || page.GitShown || page.ChipRows != 0 || page.Summary != "No archived" || page.MenuOptions < 2 || page.FilteredRows != 0 || page.AfterReset != "All" {
		t.Fatalf("sidebar = %+v", page)
	}

	var sessionBadge string
	if err := chromedp.Run(browserCtx,
		chromedp.Evaluate(`document.documentElement._x_dataStack[0].openSessionDialog?.()`, nil),
		chromedp.Poll(`!!document.querySelector('[data-scheduled-session]')?.offsetParent`, nil),
		chromedp.Evaluate(`document.querySelector('[data-scheduled-session]').textContent.trim()`, &sessionBadge),
	); err != nil {
		t.Fatalf("session picker badge: %v", err)
	}
	if sessionBadge != "1" {
		t.Fatalf("session badge = %q", sessionBadge)
	}

	// A running command marks its chat and its session in the picker, and
	// the marks go away when it ends.
	started, err := engine.ExecManager().Start(context.Background(), execruntime.StartRequest{
		SessionID: state.Session.ID, ChatID: state.ActiveChatID, Command: "sleep 30", Label: "Serve the site",
		Workdir: workdir, YieldTime: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	var running struct{ Session, Chat, SessionTitle string }
	if err := chromedp.Run(browserCtx,
		chromedp.Poll(`!!document.querySelector('[data-running-session]')?.offsetParent && !!document.querySelector('[data-running-chat]')`, nil, chromedp.WithPollingTimeout(10*time.Second)),
		chromedp.Evaluate(`({
			Session: document.querySelector('[data-running-session]').textContent.trim(),
			Chat: document.querySelector('[data-running-chat]').textContent.trim(),
			SessionTitle: document.querySelector('[data-running-session]').title,
		})`, &running),
	); err != nil {
		t.Fatalf("running badges: %v", err)
	}
	if running.Session != "1" || running.Chat != "1" || running.SessionTitle != "A command is running in this session" {
		t.Fatalf("running badges = %+v", running)
	}
	if _, err := engine.ExecManager().Terminate(context.Background(), execruntime.TerminateRequest{SessionID: state.Session.ID, ChatID: state.ActiveChatID, ProcessID: started.ProcessID}); err != nil {
		t.Fatal(err)
	}
	if err := chromedp.Run(browserCtx,
		chromedp.Poll(`!document.querySelector('[data-running-session]')?.offsetParent && !document.querySelector('[data-running-chat]')?.offsetParent`, nil, chromedp.WithPollingTimeout(10*time.Second)),
	); err != nil {
		t.Fatalf("running badges stayed after the command ended: %v", err)
	}
	if dir := os.Getenv("KODER_UI_SHOTS"); dir != "" {
		if _, err := engine.ExecManager().Start(context.Background(), execruntime.StartRequest{
			SessionID: state.Session.ID, ChatID: state.ActiveChatID, Command: "sleep 30", Label: "Serve again",
			Workdir: workdir, YieldTime: time.Millisecond, AllowDuplicate: true,
		}); err != nil {
			t.Fatal(err)
		}
		for _, width := range []int64{1440, 820, 420} {
			var shot []byte
			if err := chromedp.Run(browserCtx,
				chromedp.EmulateViewport(width, 900),
				chromedp.Poll(`!!document.querySelector('[data-running-session]')?.offsetParent`, nil, chromedp.WithPollingTimeout(10*time.Second)),
				chromedp.Sleep(200*time.Millisecond),
				chromedp.CaptureScreenshot(&shot),
			); err != nil {
				t.Fatal(err)
			}
			_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("picker-running-%d.png", width)), shot, 0o644)
		}
	}
}
