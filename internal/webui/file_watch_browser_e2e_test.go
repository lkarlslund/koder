package webui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// The file browser keeps its tree and the open file current as files change
// on disk, through folder watches over the websocket.
func TestFileBrowserFollowsDiskChanges(t *testing.T) {
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
	write("docs/note.md", "# First version\n")
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
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	t.Cleanup(cancel)
	const app = `Alpine.$data(document.documentElement)`
	run := func(name string, actions ...chromedp.Action) {
		t.Helper()
		if err := chromedp.Run(ctx, actions...); err != nil {
			var ui string
			_ = chromedp.Run(ctx, chromedp.Evaluate(`JSON.stringify({watched: `+app+`.watchedPaths, sent: `+app+`.watchSentKey, expanded: `+app+`.expanded, nodes: `+app+`.visibleNodes().map(n => n.path), error: `+app+`.fileError, changes: window.__changes})`, &ui))
			t.Fatalf("%s: %v; UI: %s", name, err, ui)
		}
	}
	watching := func(paths ...string) chromedp.Action {
		cond := "true"
		for _, p := range paths {
			cond += fmt.Sprintf(" && %s.watchedPaths.includes(%q)", app, p)
		}
		return chromedp.Poll(cond, nil)
	}
	shown := func(path string) chromedp.Action {
		return chromedp.Poll(fmt.Sprintf(`!!document.querySelector('[data-file-path=%q]')`, path), nil)
	}
	gone := func(path string) chromedp.Action {
		return chromedp.Poll(fmt.Sprintf(`!document.querySelector('[data-file-path=%q]')`, path), nil)
	}

	// With no folder open, the project root alone is watched.
	run("root only", chromedp.Navigate(srv.URL()+"/s/"+string(state.Session.ID)+"/files"), watching(""))
	write("root-only.txt", "x")
	run("root change shows", shown("root-only.txt"))

	run("open note",
		chromedp.Navigate(srv.URL()+"/s/"+string(state.Session.ID)+"/files?path=docs/note.md"),
		chromedp.Poll(`document.querySelector('.file-browser-markdown')?.textContent.includes('First version')`, nil),
		watching("", "docs"),
		chromedp.Evaluate(`(() => {
			const data = `+app+`;
			window.__changes = 0;
			const apply = data.applyFilesChanged.bind(data);
			data.applyFilesChanged = change => { window.__changes++; return apply(change); };
		})()`, nil),
	)

	var escape string
	run("watch outside the project is refused", chromedp.Evaluate(fmt.Sprintf(`new Promise(resolve => {
		const socket = new WebSocket('ws://' + location.host + '/ws');
		socket.onopen = () => socket.send(JSON.stringify({id: 'watch-x', method: 'watch_files', params: {session_id: %q, paths: ['../']}}));
		socket.onmessage = event => { const m = JSON.parse(event.data); if (m.id === 'watch-x') { socket.close(); resolve(m.ok ? 'ok' : m.error); } };
	})`, string(state.Session.ID)), &escape, func(p *cdpruntime.EvaluateParams) *cdpruntime.EvaluateParams { return p.WithAwaitPromise(true) }))
	if escape != "path escapes project root" {
		t.Errorf("watching ../ answered %q, want the escape error", escape)
	}

	write("new.txt", "hello")
	run("new file in root appears", shown("new.txt"))
	if err := os.Mkdir(filepath.Join(project, "docs", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	run("new folder in open folder appears", shown("docs/sub"))

	write("docs/note.md", "# Second version\n")
	run("open file reloads in place",
		chromedp.Poll(`document.querySelector('.file-browser-markdown')?.textContent.includes('Second version')`, nil),
		chromedp.Poll(app+`.viewMode === 'preview' && !`+app+`.fileLoading`, nil),
	)

	if err := os.Rename(filepath.Join(project, "docs", "sub"), filepath.Join(project, "docs", "renamed")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(project, "new.txt")); err != nil {
		t.Fatal(err)
	}
	run("rename and delete show", shown("docs/renamed"), gone("docs/sub"), gone("new.txt"))

	// A burst of writes arrives as a few messages, not one per file.
	var before int
	run("count before burst", chromedp.Evaluate(`window.__changes`, &before))
	for i := range 40 {
		write(fmt.Sprintf("docs/burst-%02d.txt", i), "x")
	}
	run("burst appears", shown("docs/burst-39.txt"), chromedp.Sleep(1500*time.Millisecond))
	var after int
	run("count after burst", chromedp.Evaluate(`window.__changes`, &after))
	if after-before > 3 {
		t.Errorf("40 new files caused %d change messages, want at most 3", after-before)
	}

	// A collapsed folder is not watched; opening it again shows its changes.
	run("collapse renamed folder", chromedp.Click(`[data-file-path="docs/renamed"]`, chromedp.ByQuery), shown("docs/renamed"),
		chromedp.Click(`[data-file-path="docs/renamed"]`, chromedp.ByQuery))
	write("docs/renamed/later.txt", "later")
	run("reopen renamed folder", chromedp.Click(`[data-file-path="docs/renamed"]`, chromedp.ByQuery), shown("docs/renamed/later.txt"))

	if err := os.Remove(filepath.Join(project, "docs", "note.md")); err != nil {
		t.Fatal(err)
	}
	run("deleted open file is reported", gone("docs/note.md"), chromedp.Poll(app+`.fileError === 'This file was deleted or moved.'`, nil))

	if err := os.RemoveAll(filepath.Join(project, "docs")); err != nil {
		t.Fatal(err)
	}
	run("deleted folder leaves tree", gone("docs"), chromedp.Poll(`!(`+app+`.expanded.docs) && `+app+`.currentDir === ''`, nil))

	// The open file's folder comes back with the file in it.
	write("docs/note.md", "# Note\n\nBack again.\n")
	run("recreated file shows again", shown("docs"),
		chromedp.Poll(`document.querySelector('.file-browser-markdown')?.textContent.includes('Back again') && `+app+`.fileError === ''`, nil))
	write("docs/note.md", "# Note\n\nChanged after coming back.\n")
	run("recreated folder is watched again",
		chromedp.Poll(`document.querySelector('.file-browser-markdown')?.textContent.includes('Changed after coming back')`, nil))

	if dir := os.Getenv("KODER_UI_SHOTS"); dir != "" {
		for _, width := range []int64{1440, 820, 420} {
			var shot []byte
			run("shot",
				chromedp.EmulateViewport(width, 900),
				chromedp.Sleep(200*time.Millisecond),
				chromedp.CaptureScreenshot(&shot),
			)
			_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("files-watch-%d.png", width)), shot, 0o644)
		}
	}
}
