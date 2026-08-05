package browser

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/browserapi"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/id"
)

func TestTabOwnershipAndCaps(t *testing.T) {
	m := NewManager(config.Browser{MaxTabsPerChat: 1, MaxTabsGlobal: 2, OperationTimeout: time.Second}, t.TempDir())
	first := browserapi.Chat{SessionID: id.ID("s1"), ChatID: id.ID("c1")}
	second := browserapi.Chat{SessionID: id.ID("s1"), ChatID: id.ID("c2")}
	m.tabs["owned"] = &ownedTab{id: "owned", owner: first}
	m.tabs["manual"] = &ownedTab{id: "manual"}
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
	if _, err := m.ClaimTab(t.Context(), first, "manual"); err == nil {
		t.Fatal("expected a second claim to fail")
	}
}

func TestSnapshotScriptUsesGenerationAndQuery(t *testing.T) {
	script := snapshotScript(7, "Submit", 3)
	for _, want := range []string{"7-e", `"Submit"`, "maxDepth=3", "data-koder-ref"} {
		if !strings.Contains(script, want) {
			t.Fatalf("snapshot script missing %q", want)
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
