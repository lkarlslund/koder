package webui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// The file browser marks what changed on disk since the page started
// tracking, and can follow edits in the open file or in any watched file.
func TestFileBrowserMarksChangesAndFollowsEdits(t *testing.T) {
	chromium := chromiumForTest(t)
	project := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(project, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	paragraphs := make([]string, 120)
	for i := range paragraphs {
		paragraphs[i] = fmt.Sprintf("Paragraph %d keeps the old words here.", i)
	}
	doc := func(edit func(p []string) []string) string {
		p := edit(append([]string(nil), paragraphs...))
		return "# Long\n\n" + strings.Join(p, "\n\n") + "\n"
	}
	write("docs/a.md", doc(func(p []string) []string { return p }))
	write("docs/b.md", "# B\n")
	write("docs/e.md", "# E\n\nOne two three.\n")

	ctrl := newTestControllerWithWorkdir(t, project)
	state := selectedTestState(t, ctrl)
	serverCtx, stopServer := context.WithCancel(context.Background())
	srv := startBrowserTestServer(t, serverCtx, ctrl)
	t.Cleanup(func() { stopServer(); _ = srv.server.Close() })
	opts := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(chromium), chromedp.Flag("no-sandbox", true), chromedp.Flag("disable-dev-shm-usage", true))
	allocCtx, stopAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	t.Cleanup(stopAlloc)
	ctx, stopBrowser := chromedp.NewContext(allocCtx)
	t.Cleanup(stopBrowser)
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	t.Cleanup(cancel)
	const app = `Alpine.$data(document.documentElement)`
	run := func(name string, actions ...chromedp.Action) {
		t.Helper()
		if err := chromedp.Run(ctx, actions...); err != nil {
			var ui string
			_ = chromedp.Run(ctx, chromedp.Evaluate(`JSON.stringify({path: `+app+`.file?.path, follow: `+app+`.followMode, marks: `+app+`.treeMarks, ins: [...document.querySelectorAll('.change-ins')].map(e => e.textContent), del: [...document.querySelectorAll('.change-del')].map(e => e.textContent), latest: document.querySelector('[data-latest-change]')?.textContent?.slice(0, 40)})`, &ui))
			t.Fatalf("%s: %v; UI: %s", name, err, ui)
		}
	}
	has := func(selector, text string) chromedp.Action {
		return chromedp.Poll(fmt.Sprintf(`[...document.querySelectorAll(%q)].some(el => el.textContent.includes(%q))`, selector, text), nil)
	}
	none := func(selector string) chromedp.Action {
		return chromedp.Poll(fmt.Sprintf(`!document.querySelector(%q)`, selector), nil)
	}
	setFollow := func(mode string) chromedp.Action {
		return chromedp.Evaluate(fmt.Sprintf(`(() => { const s = document.querySelector('#file-follow-mode'); s.value = %q; s.dispatchEvent(new Event('change')); })()`, mode), nil)
	}
	const latestVisible = `(() => {
		const box = document.querySelector('.file-browser-markdown').getBoundingClientRect();
		const el = document.querySelector('.file-browser-markdown [data-latest-change]');
		if (!el) return false;
		const r = el.getBoundingClientRect();
		return r.top >= box.top && r.bottom <= box.bottom;
	})()`

	run("open long document",
		chromedp.EmulateViewport(1440, 900),
		chromedp.Navigate(srv.URL()+"/s/"+string(state.Session.ID)+"/files?path=docs/a.md"),
		chromedp.Poll(`document.querySelector('.file-browser-markdown')?.textContent.includes('Paragraph 119')`, nil),
		chromedp.Poll(app+`.watchedPaths.includes('docs') && `+app+`.showChanges && `+app+`.followMode === 'off'`, nil),
	)

	write("docs/a.md", doc(func(p []string) []string {
		p[3] = "Paragraph 3 keeps the new words here."
		p = append(p[:5], p[6:]...)
		return append(p[:7], append([]string{"An inserted paragraph."}, p[7:]...)...)
	}))
	write("docs/c.txt", "new")
	if err := os.Remove(filepath.Join(project, "docs", "b.md")); err != nil {
		t.Fatal(err)
	}
	run("words and blocks are marked",
		has(".file-browser-markdown ins.change-ins", "new"),
		has(".file-browser-markdown del.change-del", "old"),
		has(".file-browser-markdown > .change-removed", "Paragraph 5 keeps"),
		has(".file-browser-markdown > .change-added", "An inserted paragraph."),
	)
	run("tree entries are marked",
		chromedp.WaitVisible(`[data-file-path="docs/c.txt"].change-added`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-file-removed="docs/b.md"].change-removed`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-file-path="docs/a.md"].change-modified`, chromedp.ByQuery),
	)
	run("changes switch off and on",
		chromedp.Click(`[data-file-changes]`, chromedp.ByQuery),
		none(`.change-ins, .change-del, .change-added, .change-removed, [data-file-removed]`),
		chromedp.Poll(`localStorage.getItem('koder.fileShowChanges') === 'false'`, nil),
		chromedp.Click(`[data-file-changes]`, chromedp.ByQuery),
		has(".file-browser-markdown ins.change-ins", "new"),
		chromedp.WaitVisible(`[data-file-removed="docs/b.md"]`, chromedp.ByQuery),
	)
	run("source view marks lines",
		chromedp.Evaluate(app+`.viewMode = 'source'`, nil),
		has(".code-line.change-added", "An inserted paragraph."),
		has(".code-line.change-removed", "Paragraph 5 keeps"),
		has(".code-line.change-removed", "Paragraph 3 keeps the old words"),
		chromedp.Evaluate(app+`.viewMode = 'preview'`, nil),
		chromedp.WaitVisible(`.file-browser-markdown`, chromedp.ByQuery),
	)

	run("follow this file", setFollow("file"), chromedp.Evaluate(`document.querySelector('.file-browser-markdown').scrollTop = 0`, nil))
	write("docs/a.md", doc(func(p []string) []string {
		p[3] = "Paragraph 3 keeps the new words here."
		p = append(p[:5], p[6:]...)
		p = append(p[:7], append([]string{"An inserted paragraph."}, p[7:]...)...)
		p[110] = "Paragraph 109 was edited far down."
		return p
	}))
	run("view moves to the edit",
		chromedp.Poll(`document.querySelector('.file-browser-markdown [data-latest-change]')?.textContent.includes('edited far down')`, nil),
		chromedp.Poll(latestVisible, nil),
	)

	var top float64
	run("follow off keeps the view", setFollow("off"),
		chromedp.Sleep(time.Second), // let the smooth scroll to the last edit finish
		chromedp.Evaluate(`document.querySelector('.file-browser-markdown').scrollTop = 0`, nil))
	write("docs/a.md", doc(func(p []string) []string {
		p[3] = "Paragraph 3 keeps the new words here."
		p = append(p[:5], p[6:]...)
		p = append(p[:7], append([]string{"An inserted paragraph."}, p[7:]...)...)
		p[110] = "Paragraph 109 was edited far down."
		p[100] = "Paragraph 99 changed while not following."
		return p
	}))
	run("off: content updates, view stays",
		chromedp.Poll(`document.querySelector('.file-browser-markdown').textContent.includes('changed while not following')`, nil),
		chromedp.Sleep(800*time.Millisecond),
		chromedp.Evaluate(`document.querySelector('.file-browser-markdown').scrollTop`, &top),
	)
	if top != 0 {
		t.Errorf("follow off moved the view to %v", top)
	}

	run("follow edited files", setFollow("files"))
	write("docs/e.md", "# E\n\nOne two four.\n")
	run("the edited file opens with its changes",
		chromedp.Poll(app+`.file?.path === 'docs/e.md' && new URLSearchParams(location.search).get('path') === 'docs/e.md'`, nil),
		has(".file-browser-markdown ins.change-ins", "four"),
		has(".file-browser-markdown del.change-del", "three"),
		chromedp.WaitVisible(`[data-file-path="docs/e.md"].active`, chromedp.ByQuery),
	)

	run("clear marks",
		chromedp.Click(`[data-file-clear-changes]`, chromedp.ByQuery),
		none(`.change-ins, .change-del, .change-added, .change-removed, .change-modified, [data-file-removed]`),
		chromedp.Poll(`document.querySelector('[data-file-clear-changes]').disabled`, nil),
	)
	write("docs/e.md", "# E\n\nOne two five.\n")
	run("marks start again from the clear",
		has(".file-browser-markdown ins.change-ins", "five"),
		has(".file-browser-markdown del.change-del", "four"),
		chromedp.Poll(`![...document.querySelectorAll('.change-del')].some(el => el.textContent.includes('three'))`, nil),
	)

	run("follow mode is remembered",
		chromedp.Reload(),
		chromedp.Poll(`document.querySelector('#file-follow-mode')?.value === 'files'`, nil),
	)

	if dir := os.Getenv("KODER_UI_SHOTS"); dir != "" {
		write("docs/a.md", doc(func(p []string) []string { return p }))
		run("show a",
			chromedp.Navigate(srv.URL()+"/s/"+string(state.Session.ID)+"/files?path=docs/a.md"),
			chromedp.Poll(app+`.watchedPaths.includes('docs')`, nil), setFollow("file"))
		write("docs/a.md", doc(func(p []string) []string {
			p[1] = "Paragraph 1 keeps the fresh words here, and more."
			p = append(p[:3], p[4:]...)
			return append(p[:5], append([]string{"An inserted paragraph."}, p[5:]...)...)
		}))
		write("docs/new.md", "x")
		run("marks shown", has(".change-ins", "fresh"), chromedp.WaitVisible(`[data-file-path="docs/new.md"]`, chromedp.ByQuery),
			chromedp.Evaluate(`document.querySelector('.file-browser-markdown').scrollTop = 0`, nil))
		for _, width := range []int64{1440, 820, 420} {
			var shot []byte
			run("shot", chromedp.EmulateViewport(width, 900), chromedp.Sleep(250*time.Millisecond), chromedp.CaptureScreenshot(&shot))
			_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("files-changes-%d.png", width)), shot, 0o644)
		}
	}
}
