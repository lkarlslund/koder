package webui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

func TestFileManagerBrowserUploadMoveDelete(t *testing.T) {
	chromium := chromiumForTest(t)
	project := t.TempDir()
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
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	t.Cleanup(cancel)
	run := func(name string, actions ...chromedp.Action) {
		t.Helper()
		if err := chromedp.Run(ctx, actions...); err != nil {
			var state string
			_ = chromedp.Run(ctx, chromedp.Evaluate(`JSON.stringify({status: Alpine.$data(document.documentElement).mutationStatus, error: Alpine.$data(document.documentElement).mutationError, dialog: Alpine.$data(document.documentElement).fileDialog, selected: Alpine.$data(document.documentElement).selectedNode, url: location.href})`, &state))
			t.Fatalf("%s: %v; UI: %s", name, err, state)
		}
	}
	local := t.TempDir()
	for _, name := range []string{"one.txt", "two.txt"} {
		if err := os.WriteFile(filepath.Join(local, name), []byte("content of "+name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run("open file manager",
		chromedp.Navigate(srv.URL()+"/s/"+string(state.Session.ID)+"/files"),
		chromedp.WaitVisible(`[data-file-root]`, chromedp.ByQuery),
		chromedp.Poll(`Alpine.$data(document.documentElement).projectRoot !== ''`, nil),
	)
	run("create destination",
		chromedp.Click(`[data-file-mkdir]`, chromedp.ByQuery),
		chromedp.WaitVisible(`#file-dialog-path`, chromedp.ByQuery),
		chromedp.SetValue(`#file-dialog-path`, "destination", chromedp.ByQuery),
		chromedp.Click(`[data-file-confirm]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-file-path="destination"]`, chromedp.ByQuery),
		chromedp.Poll(`!Alpine.$data(document.documentElement).mutationBusy && !Alpine.$data(document.documentElement).fileDialog`, nil),
	)
	run("upload multiple files",
		chromedp.SetUploadFiles(`[data-file-upload]`, []string{filepath.Join(local, "one.txt"), filepath.Join(local, "two.txt")}, chromedp.ByQuery),
		chromedp.Poll(`Alpine.$data(document.documentElement).mutationStatus === 'Uploaded 2/2 files to project root'`, nil),
		chromedp.Click(`[data-file-path="one.txt"]`, chromedp.ByQuery),
		chromedp.Poll(`document.querySelector('.file-browser-code')?.textContent.includes('content of one.txt')`, nil),
	)
	run("drag file into folder", chromedp.Evaluate(`(() => {
		const source = document.querySelector('[data-file-path="one.txt"]');
		const target = document.querySelector('[data-file-path="destination"]');
		const dataTransfer = new DataTransfer();
		source.dispatchEvent(new DragEvent('dragstart', {bubbles:true, dataTransfer}));
		target.dispatchEvent(new DragEvent('dragover', {bubbles:true, cancelable:true, dataTransfer}));
		target.dispatchEvent(new DragEvent('drop', {bubbles:true, cancelable:true, dataTransfer}));
		source.dispatchEvent(new DragEvent('dragend', {bubbles:true, dataTransfer}));
	})()`, nil),
		chromedp.Poll(`new URLSearchParams(location.search).get('path') === 'destination/one.txt' && !Alpine.$data(document.documentElement).mutationBusy`, nil),
		chromedp.WaitVisible(`[data-file-path="destination/one.txt"]`, chromedp.ByQuery),
	)
	run("rename moved file",
		chromedp.Click(`[data-file-move]`, chromedp.ByQuery),
		chromedp.WaitVisible(`#file-dialog-path`, chromedp.ByQuery),
		chromedp.SetValue(`#file-dialog-path`, "destination/renamed.txt", chromedp.ByQuery),
		chromedp.Click(`[data-file-confirm]`, chromedp.ByQuery),
		chromedp.Poll(`new URLSearchParams(location.search).get('path') === 'destination/renamed.txt' && !Alpine.$data(document.documentElement).fileDialog`, nil),
	)
	run("drop local file on folder", chromedp.Evaluate(`(() => {
		const dataTransfer = new DataTransfer();
		dataTransfer.items.add(new File(['dropped content'], 'dropped.txt', {type:'text/plain'}));
		document.querySelector('[data-file-path="destination"]').dispatchEvent(new DragEvent('drop', {bubbles:true, cancelable:true, dataTransfer}));
	})()`, nil),
		chromedp.Poll(`Alpine.$data(document.documentElement).mutationStatus === 'Uploaded 1/1 files to destination'`, nil),
		chromedp.WaitVisible(`[data-file-path="destination/dropped.txt"]`, chromedp.ByQuery),
	)
	run("cancel deletion",
		chromedp.Click(`[data-file-delete]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-file-confirm]`, chromedp.ByQuery),
		chromedp.Click(`[x-ref="fileDialogCancel"]`, chromedp.ByQuery),
		chromedp.WaitNotVisible(`.file-browser-dialog`, chromedp.ByQuery),
	)
	if _, err := os.Stat(filepath.Join(project, "destination", "renamed.txt")); err != nil {
		t.Fatal(err)
	}
	run("confirm deletion",
		chromedp.Click(`[data-file-delete]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-file-confirm]`, chromedp.ByQuery),
		chromedp.Click(`[data-file-confirm]`, chromedp.ByQuery),
		chromedp.Poll(`Alpine.$data(document.documentElement).mutationStatus === 'Deleted destination/renamed.txt' && !Alpine.$data(document.documentElement).fileDialog`, nil),
		chromedp.Poll(`!document.querySelector('.file-browser-code') && !new URLSearchParams(location.search).has('path')`, nil),
	)
	run("delete nonempty folder",
		chromedp.Click(`[data-file-path="destination"]`, chromedp.ByQuery),
		chromedp.Click(`[data-file-delete]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-file-confirm]`, chromedp.ByQuery),
		chromedp.Poll(`document.querySelector('.file-browser-dialog').textContent.includes('everything inside')`, nil),
		chromedp.Click(`[data-file-confirm]`, chromedp.ByQuery),
		chromedp.Poll(`Alpine.$data(document.documentElement).mutationStatus === 'Deleted destination' && !Alpine.$data(document.documentElement).fileDialog`, nil),
	)
	// Verify the operations changed the server's files, not just browser state.
	entries, err := os.ReadDir(project)
	if err != nil || len(entries) != 1 || entries[0].Name() != "two.txt" {
		t.Fatalf("project entries: %v %v", entries, err)
	}
	run("collision leaves original file", chromedp.Evaluate(`(() => {
		const dataTransfer = new DataTransfer();
		dataTransfer.items.add(new File(['replacement'], 'two.txt'));
		document.querySelector('[data-file-root]').dispatchEvent(new DragEvent('drop', {bubbles:true, cancelable:true, dataTransfer}));
	})()`, nil),
		chromedp.Poll(`Alpine.$data(document.documentElement).mutationError.includes('exist') && !Alpine.$data(document.documentElement).mutationBusy`, nil),
	)
	data, err := os.ReadFile(filepath.Join(project, "two.txt"))
	if err != nil || string(data) != "content of two.txt" {
		t.Fatalf("original overwritten: %q %v", data, err)
	}
	run("refresh persists files", chromedp.Reload(), chromedp.WaitVisible(`[data-file-path="two.txt"]`, chromedp.ByQuery))
	var errorText string
	run("check no UI errors", chromedp.Evaluate(`Array.from(document.querySelectorAll('.alert-danger')).map(e=>e.textContent).join('\n')`, &errorText))
	if strings.TrimSpace(errorText) != "" {
		t.Fatalf("file manager error: %s", errorText)
	}
}
