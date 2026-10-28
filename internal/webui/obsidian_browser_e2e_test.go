package webui

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// An Obsidian note previews with its properties, resolved wikilinks,
// callouts, highlights and tags, and a wikilink opens the linked note.
func TestFileBrowserPreviewsObsidianNotes(t *testing.T) {
	chromium := chromiumForTest(t)
	project := t.TempDir()
	files := map[string]string{
		"vault/.obsidian/app.json": "{}",
		"vault/hub.md": "---\ntype: view\nconfidence: high\nparts: [2, 3, 4]\n---\n# Hub\n\n" +
			"Bus: [[CAN-ECAN|the E-CAN]], wiring in [[CAN-ECAN#Pinout]], gone: [[Nowhere]].\n\n" +
			"> [!warning] Mind the gap\n> Body text\n\n" +
			"This is ==important== and tagged #can/ecan.\n\n%%hidden note%%\n\n" +
			"```\nfirst line\n[[not-a-link]] #not-a-tag\n```\n",
		"vault/buses/CAN-ECAN.md": "# CAN-ECAN\n\nIntro.\n\n## Pinout\n\nPins.\n",
	}
	for name, content := range files {
		full := filepath.Join(project, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
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
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	t.Cleanup(cancel)

	var preview struct {
		Properties  []string `json:"properties"`
		LinkPath    string   `json:"linkPath"`
		LinkText    string   `json:"linkText"`
		Heading     string   `json:"heading"`
		Missing     bool     `json:"missing"`
		Callout     string   `json:"callout"`
		CalloutType string   `json:"calloutType"`
		Mark        string   `json:"mark"`
		Tag         string   `json:"tag"`
		Comment     bool     `json:"comment"`
		Code        string   `json:"code"`
	}
	var opened string
	if err := chromedp.Run(ctx,
		chromedp.Navigate(srv.URL()+"/s/"+string(state.Session.ID)+"/files?path=vault/hub.md"),
		chromedp.Poll(`!!document.querySelector('.file-browser-markdown .obsidian-callout')`, nil),
		chromedp.Evaluate(`(() => {
			const md = document.querySelector('.file-browser-markdown');
			const links = md.querySelectorAll('a.wikilink');
			return {
				properties: Array.from(md.querySelectorAll('.obsidian-properties tr')).map(tr => tr.cells[0].textContent + '=' + tr.cells[1].textContent.replace(/\s+/g, ' ').trim()),
				linkPath: links[0]?.dataset.wikilinkPath || '',
				linkText: links[0]?.textContent || '',
				heading: links[1]?.dataset.wikilinkHeading || '',
				missing: !!md.querySelector('a.wikilink-missing'),
				callout: md.querySelector('.obsidian-callout-title')?.textContent || '',
				calloutType: md.querySelector('.obsidian-callout')?.dataset.callout || '',
				mark: md.querySelector('mark')?.textContent || '',
				tag: md.querySelector('.obsidian-tag')?.textContent || '',
				comment: md.textContent.includes('hidden note'),
				code: md.querySelector('pre')?.textContent || '',
			};
		})()`, &preview),
		chromedp.Click(`.file-browser-markdown a.wikilink`, chromedp.ByQuery),
		chromedp.Poll(`Alpine.$data(document.documentElement).file?.path === 'vault/buses/CAN-ECAN.md'`, nil),
		chromedp.Evaluate(`Alpine.$data(document.documentElement).file.path`, &opened),
	); err != nil {
		t.Fatal(err)
	}
	if len(preview.Properties) != 3 || preview.Properties[0] != "type=view" || preview.Properties[2] != "parts=2 3 4" {
		t.Fatalf("properties = %q", preview.Properties)
	}
	if preview.LinkPath != "vault/buses/CAN-ECAN.md" || preview.LinkText != "the E-CAN" || preview.Heading != "Pinout" || !preview.Missing {
		t.Fatalf("wikilinks = %+v", preview)
	}
	if preview.Callout != "Mind the gap" || preview.CalloutType != "warning" || preview.Mark != "important" || preview.Tag != "#can/ecan" || preview.Comment {
		t.Fatalf("obsidian syntax = %+v", preview)
	}
	if preview.Code != "first line\n[[not-a-link]] #not-a-tag\n" {
		t.Fatalf("code block changed: %q", preview.Code)
	}
	if opened != "vault/buses/CAN-ECAN.md" {
		t.Fatalf("opened %q", opened)
	}
}
