package browser

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp/kb"

	"github.com/lkarlslund/koder/internal/browserapi"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/id"
)

func TestTabOwnershipAndCaps(t *testing.T) {
	m := NewManager(config.Browser{MaxTabsPerChat: 1, MaxTabsGlobal: 2, OperationTimeout: time.Second}, t.TempDir())
	first := browserapi.Chat{SessionID: id.ID("s1"), ChatID: id.ID("c1")}
	second := browserapi.Chat{SessionID: id.ID("s1"), ChatID: id.ID("c2")}
	m.tabs["owned"] = &ownedTab{id: "owned", owner: first}
	m.tabs["manual"] = &ownedTab{
		id:       "manual",
		console:  []browserapi.ConsoleRecord{{Text: "before claim"}},
		requests: map[string]*requestState{"request": {}},
		order:    []string{"request"},
	}
	m.downloads["download"] = &downloadState{tabID: "manual"}
	if err := m.checkTabCapsLocked(first); err == nil || !strings.Contains(err.Error(), "chat browser tab limit") {
		t.Fatalf("expected per-chat cap, got %v", err)
	}
	claimed, err := m.ClaimTab(t.Context(), second, "manual")
	if err != nil {
		t.Fatalf("claim tab: %v", err)
	}
	if !claimed.Owned || m.tabs["manual"].owner.ChatID != second.ChatID {
		t.Fatalf("tab was not assigned to second chat: %#v", claimed)
	}
	if len(m.tabs["manual"].console) != 0 || len(m.tabs["manual"].requests) != 0 || len(m.downloads) != 0 {
		t.Fatal("claim retained diagnostics collected before ownership")
	}
	if _, err := m.ClaimTab(t.Context(), first, "manual"); err == nil {
		t.Fatal("expected a second claim to fail")
	}
}

func TestListingTabsDoesNotStartBrowser(t *testing.T) {
	m := NewManager(config.Browser{Enabled: true}, t.TempDir())
	tabs, err := m.Tabs(t.Context(), browserapi.Chat{SessionID: "session", ChatID: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	if len(tabs) != 0 || m.state != stateStopped || m.browserCtx != nil {
		t.Fatalf("tab listing started browser: tabs=%#v state=%s", tabs, m.state)
	}
}

func TestSnapshotScriptIsInformational(t *testing.T) {
	script := snapshotScript("Submit", "button", 3)
	for _, want := range []string{`"Submit"`, `"button"`, "maxDepth=3"} {
		if !strings.Contains(script, want) {
			t.Fatalf("snapshot script missing %q", want)
		}
	}
	for _, unwanted := range []string{"data-koder-ref", "generation"} {
		if strings.Contains(script, unwanted) {
			t.Fatalf("snapshot script contains interaction state %q", unwanted)
		}
	}
}

func TestLocatorExpressionContainsSemanticInputs(t *testing.T) {
	script := locatorExpression(browserapi.Locator{Target: "Submit order", Role: "button", Within: "Checkout", Exact: true, Occurrence: 2}, "click")
	for _, want := range []string{"Submit order", "button", "Checkout", `"occurrence":2`, "ambiguous"} {
		if !strings.Contains(script, want) {
			t.Fatalf("locator expression missing %q", want)
		}
	}
}

func TestBrowserKeyNames(t *testing.T) {
	for input, want := range map[string]string{"Enter": kb.Enter, "Esc": kb.Escape, "ArrowDown": kb.ArrowDown, "Space": " ", "F11": kb.F11, "x": "x"} {
		if got := browserKey(input); got != want {
			t.Fatalf("browserKey(%q) = %q, want %q", input, got, want)
		}
	}
	key, modifiers := browserKeyChord("Control+Shift+a")
	if key != "a" || len(modifiers) != 2 || modifiers[0] != input.ModifierCtrl || modifiers[1] != input.ModifierShift {
		t.Fatalf("browserKeyChord() = %q, %#v", key, modifiers)
	}
}

func TestTruncateSnapshotHonorsCharacterLimit(t *testing.T) {
	for _, limit := range []int{1, 20, 50} {
		got, truncated := truncateSnapshot(strings.Repeat("x", 100), limit)
		if !truncated || len([]rune(got)) != limit {
			t.Fatalf("truncateSnapshot(_, %d) returned %d chars, truncated=%v", limit, len([]rune(got)), truncated)
		}
	}
}

func TestSandboxCommandHidesHomeAndUsesPrivateProfile(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap unavailable")
	}
	cmd := exec.Command("/usr/bin/chromium", "--user-data-dir=/tmp/koder/profile")
	sandboxCommand(cmd, "/state/profile", "/state/run")
	joined := strings.Join(cmd.Args, " ")
	for _, want := range []string{"--tmpfs /home", "--bind /state/profile /tmp/koder/profile", "-- /usr/bin/chromium"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("sandbox command missing %q: %s", want, joined)
		}
	}
}
