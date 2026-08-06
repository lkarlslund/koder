package browser

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/browserapi"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/id"
)

// TestAuthenticatedLocatorIntegration exercises semantic locators against a
// real authenticated page without putting credentials or site data in source.
func TestAuthenticatedLocatorIntegration(t *testing.T) {
	stateDir := os.Getenv("KODER_BROWSER_AUTH_TEST_STATE_DIR")
	url := os.Getenv("KODER_BROWSER_AUTH_TEST_URL")
	target := os.Getenv("KODER_BROWSER_AUTH_TEST_TARGET")
	within := os.Getenv("KODER_BROWSER_AUTH_TEST_WITHIN")
	expectedURL := os.Getenv("KODER_BROWSER_AUTH_TEST_EXPECTED_URL")
	if stateDir == "" || url == "" || target == "" || within == "" || expectedURL == "" {
		t.Skip("set KODER_BROWSER_AUTH_TEST_STATE_DIR, URL, TARGET, WITHIN, and EXPECTED_URL")
	}

	m := NewManager(config.Browser{
		Enabled:          true,
		Headed:           false,
		OperationTimeout: 30 * time.Second,
		MaxTabsPerChat:   2,
		MaxTabsGlobal:    2,
	}, stateDir)
	t.Cleanup(func() { _ = m.Stop(t.Context()) })
	chat := browserapi.Chat{SessionID: id.ID("authenticated-session"), ChatID: id.ID("authenticated-chat")}

	if _, err := m.NewTab(t.Context(), chat, url); err != nil {
		t.Fatalf("open authenticated test page: %v", err)
	}
	waitForBrowserTarget(t, m, chat, target)

	if err := m.Interact(t.Context(), chat, "click", browserapi.Locator{Target: target}, ""); err != nil {
		t.Fatalf("execute the target-only locator emitted by the model: %v", err)
	}
	assertSelectedBrowserURL(t, m, chat, expectedURL)
	waitForBrowserText(t, m, chat, within)

	if _, err := m.Navigate(t.Context(), chat, url, "load"); err != nil {
		t.Fatalf("return to authenticated test page: %v", err)
	}
	waitForBrowserTarget(t, m, chat, target)
	if err := m.Interact(t.Context(), chat, "click", browserapi.Locator{
		Target: target,
		Role:   "link",
		Within: within,
		Exact:  true,
	}, ""); err != nil {
		t.Fatalf("click real target scoped by structural content: %v", err)
	}
	assertSelectedBrowserURL(t, m, chat, expectedURL)
	waitForBrowserText(t, m, chat, within)
}

func waitForBrowserTarget(t *testing.T, m *Manager, chat browserapi.Chat, target string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		found, err := m.Find(t.Context(), chat, target, "link", 32*1024)
		if err == nil && strings.Contains(found.Text, target) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("wait for browser target %q: last result %q, error %v", target, found.Text, err)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func waitForBrowserText(t *testing.T, m *Manager, chat browserapi.Chat, text string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		snapshot, err := m.Snapshot(t.Context(), chat, text, -1, 32*1024)
		if err == nil && strings.Contains(snapshot.Text, text) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("wait for browser text %q: last result %q, error %v", text, snapshot.Text, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func assertSelectedBrowserURL(t *testing.T, m *Manager, chat browserapi.Chat, expected string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		tabs, err := m.Tabs(t.Context(), chat)
		if err == nil {
			for _, tab := range tabs {
				if tab.Selected && strings.Contains(tab.URL, expected) {
					return
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("selected browser URL did not contain %q: tabs=%#v, error=%v", expected, tabs, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
