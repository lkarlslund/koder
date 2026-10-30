package webui

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"
)

func TestSettingsBrowserNavigationGroupsConfigurationByDomain(t *testing.T) {
	chromium := chromiumForTest(t)
	ctrl := newTestController(t)
	serverCtx, stopServer := context.WithCancel(context.Background())
	server := startBrowserTestServer(t, serverCtx, ctrl)
	t.Cleanup(func() {
		stopServer()
		_ = server.server.Close()
	})

	allocatorOptions := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(chromium),
		chromedp.WSURLReadTimeout(60*time.Second),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
	)
	allocatorCtx, stopAllocator := chromedp.NewExecAllocator(context.Background(), allocatorOptions...)
	t.Cleanup(stopAllocator)
	browserCtx, stopBrowser := chromedp.NewContext(allocatorCtx)
	t.Cleanup(stopBrowser)
	browserCtx, cancelDeadline := context.WithTimeout(browserCtx, 60*time.Second)
	t.Cleanup(cancelDeadline)

	if err := chromedp.Run(browserCtx,
		chromedp.EmulateViewport(1440, 1000),
		chromedp.Navigate(server.URL()),
		chromedp.WaitVisible(`[data-settings-open]`, chromedp.ByQuery),
		chromedp.Click(`[data-settings-open]`, chromedp.ByQuery),
		chromedp.WaitVisible(`.settings-dialog`, chromedp.ByQuery),
		chromedp.Poll(`document.querySelectorAll('[data-settings-tab]').length === 12`, nil),
		chromedp.Poll(`document.querySelector('.settings-form').textContent.includes('Theme')`, nil),
		chromedp.Poll(`Number(document.querySelector('[data-settings-open] .badge').textContent) > 0`, nil),
		chromedp.Poll(`getComputedStyle(document.querySelector('.settings-setup-label')).display === 'none'`, nil),
	); err != nil {
		t.Fatalf("open settings: %v", err)
	}

	var labels []string
	if err := chromedp.Run(browserCtx, chromedp.Evaluate(`Array.from(document.querySelectorAll('[data-settings-tab]')).map(node => node.textContent.trim().replace(/\s+/g, ' '))`, &labels)); err != nil {
		t.Fatalf("read settings navigation: %v", err)
	}
	wantLabels := []string{"Overview", "Models", "Integrations", "Backends", "Tools", "Search", "Skills", "Memory", "Voice & devices", "Conversation", "Access", "Prompts"}
	if len(labels) != len(wantLabels) {
		t.Fatalf("settings tabs = %#v, want %#v", labels, wantLabels)
	}
	for idx, want := range wantLabels {
		if !strings.HasPrefix(labels[idx], want) {
			t.Fatalf("settings tab %d = %q, want prefix %q", idx, labels[idx], want)
		}
	}

	assertSettingsPageContains(t, browserCtx, "models", "Models", "Default model")
	assertSettingsPageContains(t, browserCtx, "integrations", "Model providers", "MCP tool sources")
	assertSettingsPageContains(t, browserCtx, "backends", "Koder", "Codex", "Configure")
	assertSettingsPageContains(t, browserCtx, "tools", "Browser", "Configure", "Native tools")
	assertSettingsPageContains(t, browserCtx, "search", "Web search providers", "Exa", "Tavily", "SearXNG", "DuckDuckGo")
	// A search provider's settings survive a save from the browser.
	if err := chromedp.Run(browserCtx,
		chromedp.Click(`[data-settings-tab="search"]`, chromedp.ByQuery),
		chromedp.Evaluate(`(() => {
			const app = document.documentElement._x_dataStack[0];
			const item = app.searchProviders().find(p => p.id === 'searxng');
			item.base_url = 'https://searx.example.invalid/';
			item.enabled = true;
			app.saveSettings();
		})()`, nil),
		chromedp.Poll(`(() => {
			const app = document.documentElement._x_dataStack[0];
			const item = app.searchProviders().find(p => p.id === 'searxng');
			return !app.settingsSaving && !app.settingsDirty() && item.enabled && item.base_url === 'https://searx.example.invalid';
		})()`, nil),
	); err != nil {
		t.Fatalf("save search settings: %v", err)
	}
	saved, err := ctrl.Preferences(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range saved.Search.Providers {
		if got.ID == "searxng" && (got.BaseURL != "https://searx.example.invalid" || !got.Enabled) {
			t.Fatalf("saved searxng settings = %+v", got)
		}
	}
	// Test is usable for a configured provider and not for one that lacks a key.
	var testDisabled map[string]bool
	if err := chromedp.Run(browserCtx, chromedp.Evaluate(`Object.fromEntries(Array.from(document.querySelectorAll('[data-search-provider]')).map(row => [row.dataset.searchProvider, row.querySelector('[data-search-test]').disabled]))`, &testDisabled)); err != nil {
		t.Fatalf("read search test buttons: %v", err)
	}
	if testDisabled["searxng"] || !testDisabled["exa"] {
		t.Fatalf("search Test buttons disabled = %v, want searxng enabled and exa disabled", testDisabled)
	}
	// Dragging a provider's grip onto another row moves it there.
	var points []float64
	if err := chromedp.Run(browserCtx, chromedp.Evaluate(`(() => {
		const grip = document.querySelector('[data-search-provider="exa"] .settings-search-grip').getBoundingClientRect();
		const target = document.querySelector('[data-search-provider="tavily"]').getBoundingClientRect();
		return [grip.x + grip.width / 2, grip.y + grip.height / 2, target.x + 40, target.bottom - 4];
	})()`, &points)); err != nil || len(points) != 4 {
		t.Fatalf("locate search rows: %v %v", points, err)
	}
	var order []string
	if err := chromedp.Run(browserCtx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			steps := []*input.DispatchMouseEventParams{{Type: input.MousePressed, X: points[0], Y: points[1], Button: input.Left, ClickCount: 1}}
			for i := 1; i <= 8; i++ {
				f := float64(i) / 8
				steps = append(steps, &input.DispatchMouseEventParams{Type: input.MouseMoved, X: points[0] + (points[2]-points[0])*f, Y: points[1] + (points[3]-points[1])*f, Button: input.Left})
			}
			steps = append(steps, &input.DispatchMouseEventParams{Type: input.MouseReleased, X: points[2], Y: points[3], Button: input.Left, ClickCount: 1})
			for _, step := range steps {
				if err := step.Do(ctx); err != nil {
					return err
				}
				// Chrome merges pointer moves within a frame.
				time.Sleep(50 * time.Millisecond)
			}
			return nil
		}),
		chromedp.Evaluate(`document.documentElement._x_dataStack[0].searchProviders().map(p => p.id)`, &order),
	); err != nil {
		t.Fatalf("drag search provider: %v", err)
	}
	if exa, tavily := slices.Index(order, "exa"), slices.Index(order, "tavily"); exa != tavily+1 {
		t.Fatalf("order after dragging exa onto tavily = %v", order)
	}
	assertSettingsPageContains(t, browserCtx, "skills", "Agent Skills")
	assertSettingsPageContains(t, browserCtx, "memory", "Global memory", "No memories yet")
	assertSettingsPageContains(t, browserCtx, "voice", "Android devices", "Speech output")
	assertSettingsPageContains(t, browserCtx, "conversation", "Turn execution", "Compaction", "Thinking helper")

	var toolGroups []string
	if err := chromedp.Run(browserCtx,
		chromedp.Click(`[data-settings-tab="tools"]`, chromedp.ByQuery),
		chromedp.Evaluate(`Array.from(document.querySelectorAll('.settings-tool-group .settings-tool-group-header .toggle-switch-label')).map(node => node.textContent.trim())`, &toolGroups),
	); err != nil {
		t.Fatalf("read native tool groups: %v", err)
	}
	for _, group := range toolGroups {
		if strings.EqualFold(group, "browser") {
			t.Fatalf("browser enablement was duplicated in native tool groups: %#v", toolGroups)
		}
	}
}

func assertSettingsPageContains(t *testing.T, browserCtx context.Context, tab string, expected ...string) {
	t.Helper()
	var text string
	checks := make([]string, 0, len(expected))
	for _, text := range expected {
		checks = append(checks, `document.querySelector('.settings-form').innerText.includes(`+strconv.Quote(text)+`)`)
	}
	ready := `document.querySelector('[data-settings-tab="` + tab + `"]').classList.contains('active') && ` + strings.Join(checks, " && ")
	if err := chromedp.Run(browserCtx,
		chromedp.Click(`[data-settings-tab="`+tab+`"]`, chromedp.ByQuery),
		chromedp.Poll(ready, nil),
		chromedp.Evaluate(`document.querySelector('.settings-form').innerText`, &text),
	); err != nil {
		t.Fatalf("open %s settings: %v", tab, err)
	}
	for _, want := range expected {
		if !strings.Contains(text, want) {
			t.Fatalf("%s settings omitted %q; visible text: %s", tab, want, text)
		}
	}
}
