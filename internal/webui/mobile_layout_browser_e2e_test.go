package webui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"
)

// mobileBrowser starts a browser for phone-sized tests and returns its
// context.
func mobileBrowser(t *testing.T) context.Context {
	t.Helper()
	chromium := chromiumForTest(t)
	opts := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(chromium), chromedp.Flag("no-sandbox", true), chromedp.Flag("disable-dev-shm-usage", true))
	allocCtx, stopAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	t.Cleanup(stopAlloc)
	ctx, stopBrowser := chromedp.NewContext(allocCtx)
	t.Cleanup(stopBrowser)
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// swipe drags one finger across the page from (x0, y) to (x1, y).
func swipe(x0, x1, y float64) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		if err := emulation.SetTouchEmulationEnabled(true).Do(ctx); err != nil {
			return err
		}
		point := func(x float64) []*input.TouchPoint { return []*input.TouchPoint{{X: x, Y: y, ID: 1}} }
		if err := input.DispatchTouchEvent(input.TouchStart, point(x0)).Do(ctx); err != nil {
			return err
		}
		for i := 1; i <= 8; i++ {
			if err := input.DispatchTouchEvent(input.TouchMove, point(x0+(x1-x0)*float64(i)/8)).Do(ctx); err != nil {
				return err
			}
		}
		return input.DispatchTouchEvent(input.TouchEnd, []*input.TouchPoint{}).Do(ctx)
	})
}

// On a phone the top bar keeps a few buttons and moves the rest into a
// menu, the sidebar slides in from a top bar button and closes with a
// swipe, and the large dialogs fill the screen. On a desktop nothing moves
// into the menu.
func TestMobileLayoutCollapsesTopBarAndSlidesSidebar(t *testing.T) {
	ctrl := newTestControllerWithWorkdir(t, t.TempDir())
	state := selectedTestState(t, ctrl)
	serverCtx, stopServer := context.WithCancel(context.Background())
	srv := startBrowserTestServer(t, serverCtx, ctrl)
	t.Cleanup(func() { stopServer(); _ = srv.server.Close() })
	ctx := mobileBrowser(t)
	sessionURL := srv.URL() + "/s/" + string(state.Session.ID)

	// shown reports whether the element is displayed and wholly on screen.
	shown := func(selector string) string {
		return fmt.Sprintf(`(() => {
			const el = document.querySelector(%q);
			if (!el || !el.getClientRects().length || getComputedStyle(el).visibility === 'hidden') return false;
			const r = el.getBoundingClientRect();
			return r.width > 0 && r.left >= 0 && r.right <= innerWidth + 0.5 && r.top >= 0 && r.bottom <= innerHeight + 0.5;
		})()`, selector)
	}
	const app = `Alpine.$data(document.documentElement)`
	const loaded = app + `.sessionLoadedMode() && ` + app + `.connected`
	const topBarFits = `document.documentElement.scrollWidth <= innerWidth && [...document.querySelectorAll('.topbar .btn, .topbar .badge')].filter(b => b.getClientRects().length && !b.closest('.topbar-menu')).every(b => b.getBoundingClientRect().right <= innerWidth + 0.5)`
	var fits, browseShown, moreShown, toggleShown, menuOpen, sidebarOpen, sidebarShown bool
	var opened string
	if err := chromedp.Run(ctx,
		chromedp.EmulateViewport(390, 844, chromedp.EmulateMobile),
		chromedp.Navigate(sessionURL),
		chromedp.Poll(loaded, nil, chromedp.WithPollingTimeout(15*time.Second)),
		chromedp.Sleep(300*time.Millisecond),
		chromedp.Evaluate(topBarFits, &fits),
		chromedp.Evaluate(shown(`[data-browse-files]`), &browseShown),
		chromedp.Evaluate(shown(`[data-topbar-more]`), &moreShown),
		chromedp.Evaluate(shown(`[data-sidebar-toggle]`), &toggleShown),
		chromedp.Evaluate(shown(`[data-sidebar]`), &sidebarShown),
	); err != nil {
		t.Fatalf("open the session on a phone: %v", err)
	}
	if !fits || browseShown || !moreShown || !toggleShown || sidebarShown {
		t.Fatalf("phone top bar: fits=%v browse button=%v more=%v sidebar toggle=%v sidebar shown=%v; want the menu and sidebar buttons, no browse button, sidebar hidden", fits, browseShown, moreShown, toggleShown, sidebarShown)
	}

	// The menu opens, offers the moved actions, and runs one.
	if err := chromedp.Run(ctx,
		chromedp.Click(`[data-topbar-more]`, chromedp.ByQuery),
		chromedp.Poll(shown(`[data-menu-browse-files]`), nil, chromedp.WithPollingTimeout(3*time.Second)),
		chromedp.Evaluate(`(() => { `+app+`.openURLInNewTab = url => { window.__opened = url; }; })()`, nil),
		chromedp.Click(`[data-menu-browse-files]`, chromedp.ByQuery),
		chromedp.Poll(`!!window.__opened`, nil),
		chromedp.Evaluate(`window.__opened`, &opened),
		chromedp.Evaluate(app+`.topbarMenuOpen`, &menuOpen),
	); err != nil {
		t.Fatalf("browse files from the menu: %v", err)
	}
	if !strings.HasPrefix(opened, "/s/"+string(state.Session.ID)+"/files") || menuOpen {
		t.Fatalf("menu action opened %q, menu still open %v", opened, menuOpen)
	}

	// Escape and a tap outside close the menu.
	if err := chromedp.Run(ctx,
		chromedp.Click(`[data-topbar-more]`, chromedp.ByQuery),
		chromedp.Poll(app+`.topbarMenuOpen`, nil),
		chromedp.KeyEvent("\x1b"),
		chromedp.Poll(`!`+app+`.topbarMenuOpen && !`+app+`.chatInterruptible()`, nil, chromedp.WithPollingTimeout(3*time.Second)),
		chromedp.Click(`[data-topbar-more]`, chromedp.ByQuery),
		chromedp.Poll(app+`.topbarMenuOpen`, nil),
		chromedp.MouseClickXY(200, 500),
		chromedp.Poll(`!`+app+`.topbarMenuOpen`, nil, chromedp.WithPollingTimeout(3*time.Second)),
	); err != nil {
		t.Fatalf("close the menu: %v", err)
	}

	// The sidebar slides in from its button and a swipe right closes it.
	if err := chromedp.Run(ctx,
		chromedp.Click(`[data-sidebar-toggle]`, chromedp.ByQuery),
		chromedp.Poll(shown(`[data-sidebar]`), nil, chromedp.WithPollingTimeout(3*time.Second)),
		swipe(150, 360, 600),
		chromedp.Poll(`!`+app+`.mobileSidebarOpen`, nil, chromedp.WithPollingTimeout(3*time.Second)),
		chromedp.Sleep(400*time.Millisecond),
		chromedp.Evaluate(shown(`[data-sidebar]`), &sidebarShown),
	); err != nil {
		t.Fatalf("open and swipe away the sidebar: %v", err)
	}
	if sidebarShown {
		t.Fatal("the sidebar is still on screen after the swipe")
	}
	// A short or vertical drag leaves it open.
	if err := chromedp.Run(ctx,
		chromedp.Click(`[data-sidebar-toggle]`, chromedp.ByQuery),
		chromedp.Poll(shown(`[data-sidebar]`), nil, chromedp.WithPollingTimeout(3*time.Second)),
		swipe(150, 180, 600),
		chromedp.Sleep(300*time.Millisecond),
		chromedp.Evaluate(app+`.mobileSidebarOpen`, &sidebarOpen),
	); err != nil {
		t.Fatalf("short drag on the sidebar: %v", err)
	}
	if !sidebarOpen {
		t.Fatal("a short drag closed the sidebar")
	}

	// The Sessions dialog fills the screen with its buttons on it.
	var dialogWidth float64
	var closeShown bool
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(app+`.closeMobileSidebar()`, nil),
		chromedp.Poll(`!document.querySelector('.mobile-sidebar-backdrop').getClientRects().length`, nil, chromedp.WithPollingTimeout(3*time.Second)),
		chromedp.Click(`.topbar [title="Sessions & Chats"]`, chromedp.ByQuery),
		chromedp.Poll(shown(`.session-dialog [title="Close"]`), nil, chromedp.WithPollingTimeout(5*time.Second)),
		// The dialog grows in from 95%; wait for it to settle.
		chromedp.Poll(`document.querySelector('.session-dialog').getBoundingClientRect().width === innerWidth`, nil, chromedp.WithPollingTimeout(3*time.Second)),
		chromedp.Evaluate(`document.querySelector('.session-dialog').getBoundingClientRect().width`, &dialogWidth),
		chromedp.Evaluate(`[...document.querySelectorAll('.session-selector-row .btn')].every(b => b.getBoundingClientRect().right <= innerWidth + 0.5)`, &closeShown),
	); err != nil {
		t.Fatalf("sessions dialog: %v", err)
	}
	if dialogWidth != 390 || !closeShown {
		t.Fatalf("sessions dialog is %v px wide (want 390), row buttons on screen %v", dialogWidth, closeShown)
	}

	// On a desktop the buttons stay in the bar and the menu is not offered.
	if err := chromedp.Run(ctx,
		chromedp.EmulateViewport(1440, 900),
		chromedp.Navigate(sessionURL),
		chromedp.Poll(loaded, nil, chromedp.WithPollingTimeout(15*time.Second)),
		chromedp.Sleep(300*time.Millisecond),
		chromedp.Evaluate(shown(`[data-browse-files]`), &browseShown),
		chromedp.Evaluate(shown(`[data-topbar-more]`), &moreShown),
		chromedp.Evaluate(shown(`[data-sidebar-toggle]`), &toggleShown),
		chromedp.Evaluate(shown(`[data-sidebar]`), &sidebarShown),
	); err != nil {
		t.Fatalf("desktop: %v", err)
	}
	if !browseShown || moreShown || toggleShown || !sidebarShown {
		t.Fatalf("desktop: browse=%v more=%v sidebar toggle=%v sidebar=%v; want browse and sidebar only", browseShown, moreShown, toggleShown, sidebarShown)
	}

	if dir := os.Getenv("KODER_UI_SHOTS"); dir != "" {
		for _, width := range []int64{1440, 820, 420} {
			var shot []byte
			var fits bool
			actions := []chromedp.Action{
				chromedp.EmulateViewport(width, 900),
				chromedp.Navigate(sessionURL),
				chromedp.Poll(loaded, nil, chromedp.WithPollingTimeout(15*time.Second)),
				chromedp.Sleep(400 * time.Millisecond),
				chromedp.Evaluate(topBarFits, &fits),
			}
			if width < 900 {
				actions = append(actions, chromedp.Click(`[data-topbar-more]`, chromedp.ByQuery), chromedp.Sleep(200*time.Millisecond))
			}
			actions = append(actions, chromedp.CaptureScreenshot(&shot))
			if err := chromedp.Run(ctx, actions...); err != nil {
				t.Fatal(err)
			}
			if !fits {
				t.Errorf("at %d px the top bar runs off the screen", width)
			}
			_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("mobile-topbar-%d.png", width)), shot, 0o644)
		}
	}
}

// On a phone the file browser shows the tree or the open file, not both:
// opening a file shows it, the back button shows the tree again.
func TestFileBrowserTakesTurnsOnPhone(t *testing.T) {
	project := t.TempDir()
	for name, body := range map[string]string{"README.md": "# Hello\n", "notes.txt": "notes\n"} {
		if err := os.WriteFile(filepath.Join(project, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctrl := newTestControllerWithWorkdir(t, project)
	state := selectedTestState(t, ctrl)
	serverCtx, stopServer := context.WithCancel(context.Background())
	srv := startBrowserTestServer(t, serverCtx, ctrl)
	t.Cleanup(func() { stopServer(); _ = srv.server.Close() })
	ctx := mobileBrowser(t)

	const view = `(() => {
		const visible = s => { const el = document.querySelector(s); return !!el && el.getClientRects().length > 0; };
		return (visible('.file-browser-tree-pane') ? 'tree' : '') + (visible('.file-browser-main') ? 'file' : '');
	})()`
	const fileShown = `document.querySelector('.file-browser-file-header .fw-semibold')?.textContent`
	var atStart, opened, back, picked, wide, name string
	if err := chromedp.Run(ctx,
		chromedp.EmulateViewport(390, 844, chromedp.EmulateMobile),
		chromedp.Navigate(srv.URL()+"/s/"+string(state.Session.ID)+"/files"),
		chromedp.Poll(`!!document.querySelector('[data-file-path="notes.txt"]')`, nil, chromedp.WithPollingTimeout(15*time.Second)),
		chromedp.Evaluate(view, &atStart),
		chromedp.Click(`[data-file-path="README.md"]`, chromedp.ByQuery),
		chromedp.Poll(fileShown+` === 'README.md'`, nil, chromedp.WithPollingTimeout(5*time.Second)),
		chromedp.Evaluate(view, &opened),
		chromedp.Poll(`!!document.querySelector('[data-file-back]')?.getClientRects().length`, nil, chromedp.WithPollingTimeout(3*time.Second)),
		chromedp.Click(`[data-file-back]`, chromedp.ByQuery),
		chromedp.Evaluate(view, &back),
		chromedp.Click(`[data-file-path="notes.txt"]`, chromedp.ByQuery),
		chromedp.Poll(fileShown+` === 'notes.txt'`, nil, chromedp.WithPollingTimeout(5*time.Second)),
		chromedp.Evaluate(view, &picked),
		chromedp.Evaluate(fileShown, &name),
		chromedp.EmulateViewport(1440, 900),
		chromedp.Sleep(200*time.Millisecond),
		chromedp.Evaluate(view, &wide),
	); err != nil {
		t.Fatalf("browse files on a phone: %v", err)
	}
	if atStart != "tree" || opened != "file" || back != "tree" || picked != "file" || wide != "treefile" {
		t.Fatalf("views: start %q, opened %q, back %q, picked %q (%s), desktop %q; want tree, file, tree, file, treefile", atStart, opened, back, picked, name, wide)
	}
}
