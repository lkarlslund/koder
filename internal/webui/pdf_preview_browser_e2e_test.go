package webui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// writeTestPDF writes an A4 PDF whose pages each carry "Koder PDF page N"
// in Helvetica, which PDF.js draws from its standard font files, above a
// black bar.
func writeTestPDF(t *testing.T, path string, pages int) {
	t.Helper()
	var objects []string
	add := func(body string) int {
		objects = append(objects, body)
		return len(objects)
	}
	add("<< /Type /Catalog /Pages 2 0 R >>")
	pagesIndex := add("") // filled in once the pages exist
	font := add("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	var kids []string
	for n := 1; n <= pages; n++ {
		stream := fmt.Sprintf("BT /F1 36 Tf 72 720 Td (Koder PDF page %d) Tj ET\n0 0 0 rg 72 600 300 60 re f\n", n)
		content := add(fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(stream), stream))
		page := add(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Resources << /Font << /F1 %d 0 R >> >> /Contents %d 0 R >>", font, content))
		kids = append(kids, fmt.Sprintf("%d 0 R", page))
	}
	objects[pagesIndex-1] = fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), pages)

	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects))
	for i, body := range objects {
		offsets[i] = out.Len()
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", i+1, body)
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, offset := range offsets {
		fmt.Fprintf(&out, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	if err := os.WriteFile(path, out.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The file browser shows a PDF's pages, fitted to the view's width, with
// selectable text, zoom, and a page counter that follows scrolling. An
// edited PDF is shown again; a broken one says so.
func TestFileBrowserPreviewsPDF(t *testing.T) {
	chromium := chromiumForTest(t)
	project := t.TempDir()
	writeTestPDF(t, filepath.Join(project, "doc.pdf"), 3)
	if err := os.WriteFile(filepath.Join(project, "broken.pdf"), append([]byte{0, 1, 2, 3, 0xff}, bytes.Repeat([]byte{0x9c}, 600)...), 0o600); err != nil {
		t.Fatal(err)
	}
	ctrl := newTestControllerWithWorkdir(t, project)
	state := selectedTestState(t, ctrl)
	serverCtx, stopServer := context.WithCancel(context.Background())
	srv := startBrowserTestServer(t, serverCtx, ctrl)
	t.Cleanup(func() { stopServer(); _ = srv.server.Close() })

	// The file's metadata marks it as a PDF, and the raw endpoint serves it.
	api := srv.URL() + "/api/sessions/" + string(state.Session.ID) + "/files/"
	resp, err := http.Get(api + "read?path=doc.pdf")
	if err != nil {
		t.Fatal(err)
	}
	var meta fileContentResponse
	err = json.NewDecoder(resp.Body).Decode(&meta)
	_ = resp.Body.Close()
	if err != nil || !meta.PDF || meta.Binary || meta.Content != "" || meta.MIME != "application/pdf" {
		t.Fatalf("PDF metadata = %+v, %v", meta, err)
	}
	resp, err = http.Get(api + "raw?path=doc.pdf")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/pdf" || resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("raw PDF: %d, %v", resp.StatusCode, resp.Header)
	}

	opts := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(chromium), chromedp.Flag("no-sandbox", true), chromedp.Flag("disable-dev-shm-usage", true))
	allocCtx, stopAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	t.Cleanup(stopAlloc)
	ctx, stopBrowser := chromedp.NewContext(allocCtx)
	t.Cleanup(stopBrowser)
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	t.Cleanup(cancel)

	// drawn reports whether page n has a canvas with dark pixels: the black
	// bar and the text.
	drawn := func(n int) string {
		return fmt.Sprintf(`(() => {
			const canvas = document.querySelector('.file-browser-pdf-page[data-page="%d"] canvas');
			if (!canvas || !canvas.width) return false;
			const data = canvas.getContext('2d').getImageData(0, 0, canvas.width, canvas.height).data;
			let dark = 0;
			for (let i = 0; i < data.length; i += 4) if (data[i] < 60 && data[i + 1] < 60 && data[i + 2] < 60) dark++;
			return dark > 1000;
		})()`, n)
	}
	const layout = `(() => {
		const scroller = document.querySelector('.file-browser-pdf-scroll');
		const page = document.querySelector('.file-browser-pdf-page[data-page="1"]');
		return [page.getBoundingClientRect().width, scroller.clientWidth];
	})()`
	const status = `(document.querySelector('[data-pdf-status]')?.textContent ?? '')`
	var size []float64
	var text, selected, zoomLabel string
	var codeShown bool
	if err := chromedp.Run(ctx,
		chromedp.EmulateViewport(1440, 900),
		chromedp.Navigate(srv.URL()+"/s/"+string(state.Session.ID)+"/files?path=doc.pdf"),
		chromedp.Poll(status+` === 'Page 1 of 3'`, nil, chromedp.WithPollingTimeout(20*time.Second)),
		chromedp.Poll(drawn(1), nil, chromedp.WithPollingTimeout(20*time.Second)),
		chromedp.Poll(`document.querySelector('.file-browser-pdf-page[data-page="1"] .textLayer').textContent.includes('Koder PDF page 1')`, nil),
		chromedp.Evaluate(layout, &size),
		chromedp.Evaluate(`document.querySelector('.file-browser-pdf-page[data-page="1"] .textLayer').textContent`, &text),
		chromedp.Evaluate(`!!document.querySelector('.file-browser-code')`, &codeShown),
		// Select the first page's text as a reader would, and read it the
		// way "Send selected text to chat" does.
		chromedp.Evaluate(`(() => {
			const span = Array.from(document.querySelectorAll('.file-browser-pdf-page[data-page="1"] .textLayer span')).find(s => s.textContent.includes('Koder'));
			const range = document.createRange();
			range.selectNodeContents(span);
			getSelection().removeAllRanges();
			getSelection().addRange(range);
			return Alpine.$data(document.documentElement).selectedText();
		})()`, &selected),
	); err != nil {
		t.Fatalf("open the PDF: %v", err)
	}
	if want := size[1] - 32; size[0] < want-2 || size[0] > want+2 {
		t.Errorf("page 1 is %v px wide in a %v px view, want it fitted to %v", size[0], size[1], want)
	}
	if codeShown {
		t.Error("the PDF is also shown as source code")
	}
	if selected != "Koder PDF page 1" {
		t.Errorf("selected text = %q (layer %q), want the page's text", selected, text)
	}

	// Zooming in draws the pages larger; the middle button fits them again.
	var zoomed []float64
	if err := chromedp.Run(ctx,
		chromedp.Click(`[data-pdf-zoom-in]`, chromedp.ByQuery),
		chromedp.Poll(drawn(1), nil),
		chromedp.Evaluate(layout, &zoomed),
		chromedp.Evaluate(`document.querySelector('[data-pdf-zoom-fit]').textContent`, &zoomLabel),
	); err != nil {
		t.Fatalf("zoom in: %v", err)
	}
	if zoomed[0] < size[0]*1.25-2 || zoomed[0] > size[0]*1.25+2 || zoomLabel != "125%" {
		t.Errorf("zoomed in: page %v px wide, label %q; want %v and 125%%", zoomed[0], zoomLabel, size[0]*1.25)
	}
	if err := chromedp.Run(ctx,
		chromedp.Click(`[data-pdf-zoom-fit]`, chromedp.ByQuery),
		chromedp.Poll(drawn(1), nil),
		chromedp.Evaluate(layout, &zoomed),
	); err != nil {
		t.Fatalf("fit: %v", err)
	}
	if zoomed[0] < size[0]-2 || zoomed[0] > size[0]+2 {
		t.Errorf("fitted again: page %v px wide, want %v", zoomed[0], size[0])
	}

	// Scrolling to the end draws the last page and counts it.
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`(() => { const s = document.querySelector('.file-browser-pdf-scroll'); s.scrollTop = s.scrollHeight; })()`, nil),
		chromedp.Poll(status+` === 'Page 3 of 3'`, nil),
		chromedp.Poll(drawn(3), nil),
	); err != nil {
		t.Fatalf("scroll to the last page: %v", err)
	}

	if dir := os.Getenv("KODER_UI_SHOTS"); dir != "" {
		for _, width := range []int64{1440, 820, 420} {
			var shot []byte
			var onScreen bool
			if err := chromedp.Run(ctx,
				chromedp.EmulateViewport(width, 900),
				chromedp.Evaluate(`document.querySelector('.file-browser-pdf-scroll').scrollTop = 0`, nil),
				chromedp.Poll(drawn(1), nil),
				chromedp.Sleep(300*time.Millisecond),
				chromedp.Evaluate(`(() => { const r = document.querySelector('[data-pdf-zoom-in]').getBoundingClientRect(); return r.width > 0 && r.right <= innerWidth; })()`, &onScreen),
				chromedp.CaptureScreenshot(&shot),
			); err != nil {
				t.Fatal(err)
			}
			if !onScreen {
				t.Errorf("at %d px the zoom buttons are off screen", width)
			}
			_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("pdf-preview-%d.png", width)), shot, 0o644)
		}
		if err := chromedp.Run(ctx, chromedp.EmulateViewport(1440, 900)); err != nil {
			t.Fatal(err)
		}
	}

	// The PDF is rewritten with two pages: the preview follows.
	writeTestPDF(t, filepath.Join(project, "doc.pdf"), 2)
	later := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(filepath.Join(project, "doc.pdf"), later, later)
	if err := chromedp.Run(ctx,
		chromedp.Poll(status+`.endsWith('of 2')`, nil, chromedp.WithPollingTimeout(20*time.Second)),
	); err != nil {
		t.Fatalf("the edited PDF was not shown again: %v", err)
	}

	// A damaged file named .pdf says it cannot be read.
	var errorText string
	if err := chromedp.Run(ctx,
		chromedp.Navigate(srv.URL()+"/s/"+string(state.Session.ID)+"/files?path=broken.pdf"),
		chromedp.Poll(`!!document.querySelector('.file-browser-pdf .alert-danger')?.offsetParent`, nil, chromedp.WithPollingTimeout(20*time.Second)),
		chromedp.Evaluate(`document.querySelector('.file-browser-pdf .alert-danger').textContent`, &errorText),
	); err != nil {
		t.Fatalf("broken PDF: %v", err)
	}
	if !strings.Contains(errorText, "not a readable PDF") {
		t.Errorf("broken PDF message = %q", errorText)
	}
}
