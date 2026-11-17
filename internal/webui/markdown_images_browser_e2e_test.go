package webui

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

func writeTestPNG(t *testing.T, path string, size int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for i := range img.Pix {
		img.Pix[i] = 0xff
	}
	img.Set(0, 0, color.RGBA{R: 0xff, A: 0xff})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

type renderedImage struct {
	Alt    string
	Src    string
	Loaded bool
	Width  float64
	Height float64
}

// Markdown images load from relative paths, absolute paths inside and
// outside the project, and root-relative paths, with sizes from raw HTML,
// =WxH and {width=}, in both the file browser and the chat.
func TestMarkdownImagesLoadAndTakeTheirSize(t *testing.T) {
	chromium := chromiumForTest(t)
	project := t.TempDir()
	outside := t.TempDir()
	writeTestPNG(t, filepath.Join(project, "img", "x.png"), 100)
	writeTestPNG(t, filepath.Join(outside, "away.png"), 100)
	inside := filepath.Join(project, "img", "x.png")
	away := filepath.Join(outside, "away.png")
	md := strings.Join([]string{
		"# Images",
		`<img src="../img/x.png" width="86" alt="html-relative">`,
		`<img src="` + inside + `" width="30" alt="html-absolute">`,
		"![typora](../img/x.png =40x40)",
		"![pandoc](../img/x.png){width=50}",
		"![outside](" + away + " =x24)",
		"![rooted](/img/x.png){width=20}",
		"| Photo | Name |\n|---|---|\n| ![cell](../img/x.png =32x) | A |",
		"![plain](../img/x.png)",
		"```mermaid\ngraph TD\n  A --> B\n```",
	}, "\n\n") + "\n"
	if err := os.MkdirAll(filepath.Join(project, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "notes", "doc.md"), []byte(md), 0o600); err != nil {
		t.Fatal(err)
	}

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
	await := func(p *cdpruntime.EvaluateParams) *cdpruntime.EvaluateParams { return p.WithAwaitPromise(true) }
	const measure = `(async root => {
		const imgs = [...root.querySelectorAll('img')];
		await Promise.all(imgs.map(img => img.complete ? null : new Promise(r => { img.onload = img.onerror = r; img.loading = 'eager'; })));
		return imgs.map(img => { const r = img.getBoundingClientRect(); return {Alt: img.alt, Src: img.getAttribute('src'), Loaded: img.naturalWidth > 0, Width: Math.round(r.width), Height: Math.round(r.height)}; });
	})`
	want := map[string][2]float64{ // alt -> width, height (0: not checked)
		"html-relative": {86, 86},
		"html-absolute": {30, 30},
		"typora":        {40, 40},
		"pandoc":        {50, 50},
		"outside":       {24, 24},
		"rooted":        {20, 20},
		"cell":          {32, 32},
		"plain":         {0, 0},
	}
	check := func(where string, got []renderedImage, skip string) {
		t.Helper()
		var kept []renderedImage
		for _, img := range got {
			if img.Alt != skip {
				kept = append(kept, img)
			}
		}
		got = kept
		if skip != "" {
			delete(want, skip)
		}
		if len(got) != len(want) {
			t.Fatalf("%s: %d images, want %d: %+v", where, len(got), len(want), got)
		}
		for _, img := range got {
			w, ok := want[img.Alt]
			if !ok || !img.Loaded {
				t.Errorf("%s: image %q did not load from %s", where, img.Alt, img.Src)
				continue
			}
			if w[0] > 0 && (img.Width < w[0]-1 || img.Width > w[0]+1 || img.Height < w[1]-1 || img.Height > w[1]+1) {
				t.Errorf("%s: image %q is %vx%v, want %vx%v", where, img.Alt, img.Width, img.Height, w[0], w[1])
			}
		}
	}

	var files []renderedImage
	if err := chromedp.Run(ctx,
		chromedp.EmulateViewport(1440, 900),
		chromedp.Navigate(srv.URL()+"/s/"+string(state.Session.ID)+"/files?path=notes/doc.md"),
		chromedp.Poll(`Alpine.$data(document.documentElement).watchedPaths.includes('notes')`, nil),
		// The diagram must survive the preview updating once the page has
		// fetched the file's starting version.
		chromedp.Poll(`!!document.querySelector('.file-browser-markdown .mermaid-diagram svg')`, nil, chromedp.WithPollingTimeout(15*time.Second)),
		chromedp.Sleep(500*time.Millisecond),
		chromedp.Evaluate(`!!document.querySelector('.file-browser-markdown .mermaid-diagram svg')`, nil),
		chromedp.Evaluate(measure+`(document.querySelector('.file-browser-markdown'))`, &files, await),
	); err != nil {
		t.Fatalf("file browser: %v", err)
	}
	check("file browser", files, "")
	if dir := os.Getenv("KODER_UI_SHOTS"); dir != "" {
		for _, width := range []int64{1440, 820, 420} {
			var shot []byte
			if err := chromedp.Run(ctx, chromedp.EmulateViewport(width, 900), chromedp.Sleep(300*time.Millisecond), chromedp.CaptureScreenshot(&shot)); err != nil {
				t.Fatal(err)
			}
			_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("markdown-images-%d.png", width)), shot, 0o644)
		}
		if err := chromedp.Run(ctx, chromedp.EmulateViewport(1440, 900)); err != nil {
			t.Fatal(err)
		}
	}

	// The chat renders the same Markdown with paths relative to the project.
	chatMD := strings.ReplaceAll(md, "../img/", "img/")
	var chat []renderedImage
	if err := chromedp.Run(ctx,
		chromedp.Navigate(srv.URL()+"/s/"+string(state.Session.ID)+"/c/"+string(state.ActiveChatID)),
		chromedp.Poll(`document.documentElement._x_dataStack?.[0]?.connected === true && String(document.documentElement._x_dataStack[0].activeChatID() || '').length > 10`, nil),
		chromedp.Evaluate(fmt.Sprintf(`(() => {
			const box = document.createElement('div');
			box.className = 'markdown-body';
			box.id = 'probe';
			box.style.cssText = 'position:fixed;top:0;left:0;width:900px;background:#000;z-index:9999';
			box.innerHTML = document.documentElement._x_dataStack[0].markdownHTML(%q);
			document.body.append(box);
		})()`, chatMD), nil),
		chromedp.Evaluate(measure+`(document.getElementById('probe'))`, &chat, await),
	); err != nil {
		t.Fatalf("chat: %v", err)
	}
	// The chat has no project listing to tell /img/x.png from a path on
	// disk; agents write absolute paths, so it is read as one there.
	check("chat", chat, "rooted")
}
