package browser

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
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

func TestOperationContextHonorsParentCancellation(t *testing.T) {
	m := NewManager(config.Browser{OperationTimeout: time.Minute}, t.TempDir())
	parent, cancelParent := context.WithCancel(context.Background())
	ctx, cancel := m.operationContext(parent, context.Background())
	defer cancel()
	cancelParent()
	select {
	case <-ctx.Done():
		if ctx.Err() != context.Canceled {
			t.Fatalf("operation error = %v, want context canceled", ctx.Err())
		}
	case <-time.After(time.Second):
		t.Fatal("operation context ignored parent cancellation")
	}
}

func TestUnresponsiveBrowserIsInvalidated(t *testing.T) {
	m := NewManager(config.Browser{}, t.TempDir())
	browserCtx, cancelBrowser := context.WithCancel(context.Background())
	m.state = stateRunning
	m.browserCtx = browserCtx
	m.stop = cancelBrowser
	m.tabs["tab"] = &ownedTab{id: "tab", cancel: func() {}}
	m.invalidateUnhealthyBrowser(context.Background(), browserCtx, context.DeadlineExceeded)
	if m.state != stateError || m.browserCtx != nil || len(m.tabs) != 0 || !strings.Contains(m.lastErr, "was reset") {
		t.Fatalf("browser was not invalidated: state=%s context=%v tabs=%d error=%q", m.state, m.browserCtx, len(m.tabs), m.lastErr)
	}
}

func TestCallerCancellationDoesNotInvalidateBrowser(t *testing.T) {
	m := NewManager(config.Browser{}, t.TempDir())
	browserCtx := context.Background()
	m.state = stateRunning
	m.browserCtx = browserCtx
	parent, cancelParent := context.WithCancel(context.Background())
	cancelParent()
	m.invalidateUnhealthyBrowser(parent, browserCtx, context.DeadlineExceeded)
	if m.state != stateRunning || m.browserCtx != browserCtx {
		t.Fatalf("caller cancellation invalidated browser: state=%s context=%v", m.state, m.browserCtx)
	}
}

func TestProfilePreferencesDisableRestoreAndPasswordStorage(t *testing.T) {
	m := NewManager(config.Browser{}, t.TempDir())
	path := filepath.Join(m.profileDir, "Default", "Preferences")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data := []byte("{\"profile\":{\"exit_type\":\"Crashed\",\"keep\":\"value\"},\"session\":{\"restore_on_startup\":1},\"unrelated\":{\"value\":42}}")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.enforceProfilePreferences(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var preferences map[string]any
	if err := json.Unmarshal(data, &preferences); err != nil {
		t.Fatal(err)
	}
	profile := preferences["profile"].(map[string]any)
	session := preferences["session"].(map[string]any)
	translate := preferences["translate"].(map[string]any)
	if profile["exit_type"] != "Normal" || profile["exited_cleanly"] != true ||
		profile["password_manager_enabled"] != false || profile["password_manager_leak_detection"] != false ||
		preferences["credentials_enable_service"] != false || preferences["credentials_enable_autosignin"] != false ||
		session["restore_on_startup"] != float64(5) || translate["enabled"] != false {
		t.Fatalf("unexpected managed preferences: %#v", preferences)
	}
	if profile["keep"] != "value" || preferences["unrelated"].(map[string]any)["value"] != float64(42) {
		t.Fatalf("unrelated preferences were not preserved: %#v", preferences)
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

func TestPageChanges(t *testing.T) {
	before := pageObservation{URL: "https://example.test/one", Title: "One", Document: "doc-1", Generation: 2, ScrollY: 10}
	after := pageObservation{URL: "https://example.test/two", Title: "Two", Document: "doc-2", Generation: 3, ScrollY: 20}
	if got := strings.Join(pageChanges(before, after), ","); got != "document,url,title,dom,scroll" {
		t.Fatalf("pageChanges() = %q", got)
	}
}

func TestTargetStateMatches(t *testing.T) {
	checked := true
	state := browserapi.ElementState{Found: true, Enabled: true, Checked: &checked}
	for _, wanted := range []string{"present", "visible", "enabled", "checked"} {
		if !targetStateMatches(state, wanted) {
			t.Errorf("state should match %q", wanted)
		}
	}
	for _, wanted := range []string{"absent", "hidden", "disabled", "unchecked"} {
		if targetStateMatches(state, wanted) {
			t.Errorf("state should not match %q", wanted)
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
