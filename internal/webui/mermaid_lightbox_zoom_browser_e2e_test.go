package webui

import (
	"bytes"
	"context"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// edgeSharpness is the largest brightness step between two neighbouring
// pixels. Vector drawing keeps edges about one pixel wide at any zoom; an
// enlarged bitmap spreads each edge over several pixels.
func edgeSharpness(t *testing.T, shot []byte) int {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(shot))
	if err != nil {
		t.Fatal(err)
	}
	luma := func(x, y int) int {
		r, g, b, _ := img.At(x, y).RGBA()
		return int(299*r+587*g+114*b) / 1000 >> 8
	}
	best := 0
	bounds := img.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X + 1; x < bounds.Max.X; x++ {
			d := luma(x, y) - luma(x-1, y)
			if d < 0 {
				d = -d
			}
			best = max(best, d)
		}
	}
	return best
}

// An expanded Mermaid diagram stays sharp when zoomed in: it is drawn at the
// zoomed size, not enlarged from a picture taken at 100%.
func TestMermaidLightboxZoomStaysSharp(t *testing.T) {
	chromium := chromiumForTest(t)
	project := t.TempDir()
	md := "# Diagram\n\n```mermaid\ngraph TD\n  Alpha[Alpha node] --> Beta[Beta node]\n```\n"
	if err := os.WriteFile(filepath.Join(project, "doc.md"), []byte(md), 0o600); err != nil {
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

	// Size, and offset of the diagram's centre from the view's centre.
	const svgSize = `(() => {
		const r = document.querySelector('.image-lightbox-svg svg').getBoundingClientRect();
		const v = document.querySelector('.image-lightbox-body').getBoundingClientRect();
		return [r.width, r.height, r.x + r.width / 2 - (v.x + v.width / 2), r.y + r.height / 2 - (v.y + v.height / 2)];
	})()`
	clip := func(shot *[]byte) chromedp.Action {
		return chromedp.ActionFunc(func(ctx context.Context) error {
			var box []float64
			if err := chromedp.Evaluate(`(() => { const r = document.querySelector('.image-lightbox-body').getBoundingClientRect(); return [r.x, r.y, r.width, r.height]; })()`, &box).Do(ctx); err != nil {
				return err
			}
			var err error
			*shot, err = page.CaptureScreenshot().WithClip(&page.Viewport{X: box[0], Y: box[1], Width: box[2], Height: box[3], Scale: 1}).Do(ctx)
			return err
		})
	}
	setZoom := func(zoom float64) chromedp.Action {
		return chromedp.Evaluate(fmt.Sprintf(`(() => { const app = Alpine.$data(document.documentElement); app.zoomLightbox(%v - app.imageLightbox.zoom); })()`, zoom), nil)
	}

	var base, zoomed []float64
	var sharpShot, zoomShot []byte
	if err := chromedp.Run(ctx,
		chromedp.EmulateViewport(1440, 900),
		chromedp.Navigate(srv.URL()+"/s/"+string(state.Session.ID)+"/files?path=doc.md"),
		chromedp.Poll(`!!document.querySelector('.file-browser-markdown .mermaid-diagram svg')`, nil, chromedp.WithPollingTimeout(15*time.Second)),
		chromedp.Sleep(300*time.Millisecond),
		chromedp.Click(`.file-browser-markdown .mermaid-diagram .media-expand-button`, chromedp.ByQuery),
		chromedp.Poll(`document.querySelector('.image-lightbox-svg svg')?.getBoundingClientRect().width > 0`, nil),
		chromedp.Sleep(400*time.Millisecond),
		chromedp.Evaluate(svgSize, &base),
		clip(&sharpShot),
		setZoom(5),
		chromedp.Sleep(400*time.Millisecond),
		chromedp.Evaluate(svgSize, &zoomed),
		clip(&zoomShot),
	); err != nil {
		t.Fatal(err)
	}
	// The diagram is laid out at five times its size, so the browser draws
	// it from the vectors at that size.
	if zoomed[0] < base[0]*5-2 || zoomed[0] > base[0]*5+2 || zoomed[1] < base[1]*5-2 || zoomed[1] > base[1]*5+2 {
		t.Errorf("SVG at 500%% is %vx%v, want 5 × %vx%v", zoomed[0], zoomed[1], base[0], base[1])
	}
	for i, at := range [][]float64{base, zoomed} {
		if at[2] < -2 || at[2] > 2 || at[3] < -2 || at[3] > 2 {
			t.Errorf("diagram at %d%% is %v,%v px off the view's centre", []int{100, 500}[i], at[2], at[3])
		}
	}
	at100, at500 := edgeSharpness(t, sharpShot), edgeSharpness(t, zoomShot)
	if at500 < at100*3/4 {
		t.Errorf("edges at 500%% step by at most %d, at 100%% by %d: the zoomed diagram is blurred", at500, at100)
	}

	// Images zoom the same way, from their own pixels.
	writeTestPNG(t, filepath.Join(project, "photo.png"), 400)
	var img []float64 // natural width, shown width
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`Alpine.$data(document.documentElement).closeImageLightbox()`, nil),
		chromedp.Evaluate(`Alpine.$data(document.documentElement).openImageLightbox(location.origin + '/api/sessions/' + Alpine.$data(document.documentElement).sessionID + '/files/raw?path=photo.png', 'photo')`, nil),
		chromedp.Poll(`document.querySelector('.image-lightbox-img')?.complete && document.querySelector('.image-lightbox-img').naturalWidth === 400 && document.querySelector('.image-lightbox-img').getBoundingClientRect().width > 0`, nil),
		chromedp.Sleep(300*time.Millisecond),
		setZoom(2),
		chromedp.Evaluate(`(() => { const img = document.querySelector('.image-lightbox-img'); return [img.naturalWidth, img.getBoundingClientRect().width]; })()`, &img),
	); err != nil {
		t.Fatalf("image lightbox: %v", err)
	}
	if img[1] < 798 || img[1] > 802 {
		t.Errorf("400 px image at 200%% is %v px wide, want 800", img[1])
	}
	if dir := os.Getenv("KODER_UI_SHOTS"); dir != "" {
		_ = os.WriteFile(filepath.Join(dir, "mermaid-zoom-100.png"), sharpShot, 0o644)
		_ = os.WriteFile(filepath.Join(dir, "mermaid-zoom-500.png"), zoomShot, 0o644)
		for _, width := range []int64{1440, 820, 420} {
			var shot []byte
			if err := chromedp.Run(ctx,
				chromedp.Evaluate(`Alpine.$data(document.documentElement).closeImageLightbox()`, nil),
				chromedp.EmulateViewport(width, 900),
				chromedp.Sleep(300*time.Millisecond),
				chromedp.Click(`.file-browser-markdown .mermaid-diagram .media-expand-button`, chromedp.ByQuery),
				chromedp.Sleep(400*time.Millisecond),
				setZoom(3),
				chromedp.Sleep(300*time.Millisecond),
				// The header keeps its buttons on screen.
				chromedp.Poll(`(() => { const r = document.querySelector('.image-lightbox [title="Close preview"]').getBoundingClientRect(); return r.width > 0 && r.right <= innerWidth; })()`, nil, chromedp.WithPollingTimeout(2*time.Second)),
				chromedp.CaptureScreenshot(&shot),
			); err != nil {
				t.Fatal(err)
			}
			_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("mermaid-zoom-%d.png", width)), shot, 0o644)
		}
	}
}
