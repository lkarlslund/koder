package domain

import (
	"encoding/json"
	"testing"

	"github.com/lkarlslund/koder/internal/accesssettings"
)

func TestWorktreeMetadataRoundTripAndReadOnlyPolicy(t *testing.T) {
	original := Chat{ID: "child", Worktree: &ChatWorktree{Path: "/checkouts/child", WorkingDirectory: "/checkouts/child/pkg", Branch: "koder/child", BaseCommit: "abc", GitDir: "/repo/.git"}}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Chat
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Worktree == nil || *decoded.Worktree != *original.Worktree {
		t.Fatalf("worktree lost: %s", data)
	}
	session := Session{ProjectRoot: "/repo/pkg"}
	if got := decoded.WorkingDirectory(session); got != "/checkouts/child/pkg" {
		t.Fatalf("cwd = %s", got)
	}
	settings := accesssettings.LockedDown()
	settings.Project = accesssettings.ModeReadOnly
	got := decoded.WorktreeAccess(settings)
	for _, mount := range got.Mounts {
		if mount.Path == "/repo/.git" && mount.Mode != accesssettings.ModeReadOnly {
			t.Fatal("read-only policy broadened")
		}
	}
	if len(settings.Mounts) != 0 {
		t.Fatal("policy mutated")
	}
	if got := (Chat{}).WorkingDirectory(session); got != session.ProjectRoot {
		t.Fatalf("legacy cwd = %s", got)
	}
}

func TestWorktreeAccessPreservesExplicitGitRestriction(t *testing.T) {
	chat := Chat{Worktree: &ChatWorktree{Path: "/worker", GitDir: "/repo/.git"}}
	settings := accesssettings.Default()
	settings.Mounts = []accesssettings.Mount{{Path: "/repo/.git", Mode: accesssettings.ModeReadOnly}}
	got := chat.WorktreeAccess(settings)
	if err := accesssettings.Allows(got, accesssettings.Request{Kind: accesssettings.AccessWrite, Path: "/repo/.git/config", ProjectRoot: "/worker"}); err == nil {
		t.Fatal("explicit Git metadata restriction was overridden")
	}
}
