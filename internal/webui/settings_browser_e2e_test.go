package webui

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

func TestSettingsBrowserNavigationGroupsConfigurationByDomain(t *testing.T) {
	chromium := knowledgeBrowserChromium(t)
	ctrl := newTestController(t)
	serverCtx, stopServer := context.WithCancel(context.Background())
	server := startKnowledgeBrowserTestServer(t, serverCtx, ctrl)
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
		chromedp.Poll(`Number(document.querySelector('[data-settings-open] .badge').textContent) > 0`, nil),
		chromedp.Click(`[data-settings-open]`, chromedp.ByQuery),
		chromedp.WaitVisible(`.settings-dialog`, chromedp.ByQuery),
		chromedp.Poll(`document.querySelectorAll('[data-settings-tab]').length === 8`, nil),
		chromedp.Poll(`document.querySelector('.settings-form').textContent.includes('Theme')`, nil),
	); err != nil {
		t.Fatalf("open settings: %v", err)
	}

	var labels []string
	if err := chromedp.Run(browserCtx, chromedp.Evaluate(`Array.from(document.querySelectorAll('[data-settings-tab]')).map(node => node.textContent.trim().replace(/\s+/g, ' '))`, &labels)); err != nil {
		t.Fatalf("read settings navigation: %v", err)
	}
	wantLabels := []string{"Overview", "Models", "Backends", "Tools", "Voice & devices", "Conversation", "Access", "Prompts"}
	if len(labels) != len(wantLabels) {
		t.Fatalf("settings tabs = %#v, want %#v", labels, wantLabels)
	}
	for idx, want := range wantLabels {
		if !strings.HasPrefix(labels[idx], want) {
			t.Fatalf("settings tab %d = %q, want prefix %q", idx, labels[idx], want)
		}
	}

	assertSettingsPageContains(t, browserCtx, "models", "Model providers", "Models", "Default model")
	assertSettingsPageContains(t, browserCtx, "backends", "Koder", "Codex backend")
	assertSettingsPageContains(t, browserCtx, "tools", "Managed browser", "MCP tool sources", "Native tools")
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
	ready := `document.querySelector('.settings-form').innerText.includes(` + strconv.Quote(expected[0]) + `)`
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
