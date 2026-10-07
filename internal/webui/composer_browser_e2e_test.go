package webui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

func TestComposerBrowserScrollsKeyboardSelection(t *testing.T) {
	chromium := chromiumForTest(t)
	workdir := t.TempDir()
	for i := range 60 {
		if err := os.WriteFile(filepath.Join(workdir, fmt.Sprintf("example-%02d.txt", i)), []byte("example"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 20 {
		name := fmt.Sprintf("example-%02d", i)
		dir := filepath.Join(workdir, ".agents", "skills", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+name+"\ndescription: Example skill\n---\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ctrl := newTestControllerWithWorkdir(t, workdir)
	state := selectedTestState(t, ctrl)
	serverCtx, stopServer := context.WithCancel(context.Background())
	server := startBrowserTestServer(t, serverCtx, ctrl)
	t.Cleanup(func() { stopServer(); _ = server.server.Close() })
	options := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(chromium), chromedp.Flag("no-sandbox", true), chromedp.Flag("disable-dev-shm-usage", true))
	allocator, stopAllocator := chromedp.NewExecAllocator(context.Background(), options...)
	defer stopAllocator()
	browser, stopBrowser := chromedp.NewContext(allocator)
	defer stopBrowser()
	ctx, cancel := context.WithTimeout(browser, 45*time.Second)
	defer cancel()
	if err := chromedp.Run(ctx,
		chromedp.EmulateViewport(1440, 1000),
		chromedp.Navigate(server.URL()+"/s/"+string(state.Session.ID)+"/c/"+string(state.ActiveChatID)),
		chromedp.Poll(`!!document.documentElement._x_dataStack?.[0]?.$refs.composerInput`, nil),
	); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		text  string
		count int
	}{{"@example", 50}, {"@./example", 50}, {"$example", 20}} {
		t.Run(tc.text, func(t *testing.T) {
			if err := chromedp.Run(ctx,
				chromedp.Evaluate(fmt.Sprintf(`(() => {
					const app = document.documentElement._x_dataStack[0];
					app.clearCompletions(); app.draft = %q;
					app.$nextTick(() => { const input = app.$refs.composerInput; input.focus(); input.setSelectionRange(app.draft.length, app.draft.length); app.updateCompletions(); });
				})()`, tc.text), nil),
				chromedp.Poll(fmt.Sprintf(`document.querySelectorAll('.composer-menu button').length === %d`, tc.count), nil),
				chromedp.Evaluate(`(() => { const input = document.documentElement._x_dataStack[0].$refs.composerInput; for (let i=0; i<18; i++) input.dispatchEvent(new KeyboardEvent('keydown', {key:'ArrowDown', bubbles:true, cancelable:true})); })()`, nil),
				chromedp.Poll(`(() => { const menu = document.querySelector('.composer-menu'); const row = menu.querySelector('.active'); const a = menu.getBoundingClientRect(), b = row.getBoundingClientRect(); return document.documentElement._x_dataStack[0].completion.selected === 18 && menu.scrollTop > 0 && b.top >= a.top && b.bottom <= a.bottom + 1; })()`, nil),
				chromedp.Evaluate(`(() => { const input = document.documentElement._x_dataStack[0].$refs.composerInput; for (let i=0; i<18; i++) input.dispatchEvent(new KeyboardEvent('keydown', {key:'ArrowUp', bubbles:true, cancelable:true})); })()`, nil),
				chromedp.Poll(`document.documentElement._x_dataStack[0].completion.selected === 0 && document.querySelector('.composer-menu').scrollTop <= 1`, nil),
			); err != nil {
				t.Fatal(err)
			}
		})
	}
}
