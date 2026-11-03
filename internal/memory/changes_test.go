package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A chat hears what others changed in memory, but not what it changed
// itself with the memory tool.
func TestChangeNoticeReportsOthersChangesOnly(t *testing.T) {
	configDir := t.TempDir()
	store := NewStore(configDir, t.TempDir())
	root := "/work/project"
	for _, m := range []Memory{
		{Scope: ScopeGlobal, Name: "time-zone", Description: "Danish local time", Content: "CET"},
		{Scope: ScopeGlobal, Name: "old-calendar", Description: "Pharma Nord calendar", Content: "Mon/Wed"},
		{Scope: ScopeGlobal, Name: "terse", Description: "Terse answers", Content: "No summaries"},
		{Scope: ScopeProject, Name: "deploy", Description: "Deploy steps", Content: "build, restart"},
	} {
		if _, err := store.Create(m, root); err != nil {
			t.Fatal(err)
		}
	}
	before := store.Snapshot(root)

	// Another chat renames a memory and changes a description.
	if _, err := store.Create(Memory{Scope: ScopeGlobal, Name: "calendar-time-tracking", Description: "Time tracking in the calendar", Content: "Pharma Nord, Danish Crown"}, root); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ScopeGlobal, root, "old-calendar"); err != nil {
		t.Fatal(err)
	}
	updated, err := store.Update(Memory{Scope: ScopeGlobal, Name: "time-zone", Description: "Europe/Copenhagen time zone", Content: "CET"}, root)
	if err != nil {
		t.Fatal(err)
	}
	store.RecordWriter("chat-b", updated, root, false)
	// The user edits a memory's content by hand.
	if err := os.WriteFile(filepath.Join(configDir, "memory", "terse.md"), []byte("---\ndescription: Terse answers\n---\nNo summaries, no headings\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// This chat updates the project memory itself.
	mine, err := store.Update(Memory{Scope: ScopeProject, Name: "deploy", Description: "Deploy steps", Content: "check idle, build, restart"}, root)
	if err != nil {
		t.Fatal(err)
	}
	store.RecordWriter("chat-a", mine, root, false)
	after := store.Snapshot(root)

	notice := store.ChangeNotice(before, after, "chat-a")
	for _, want := range []string{
		"- added global memory calendar-time-tracking: Time tracking in the calendar",
		"- deleted global memory old-calendar",
		"- updated global memory time-zone, now: Europe/Copenhagen time zone",
		"- updated the content of global memory terse",
	} {
		if !strings.Contains(notice, want) {
			t.Errorf("notice lacks %q:\n%s", want, notice)
		}
	}
	if strings.Contains(notice, "deploy") {
		t.Errorf("notice reports the chat's own change:\n%s", notice)
	}
	// The chat that changed time-zone is not told about it, but hears the rest.
	if other := store.ChangeNotice(before, after, "chat-b"); strings.Contains(other, "time-zone") || !strings.Contains(other, "deploy") {
		t.Errorf("notice for the writing chat:\n%s", other)
	}
	if store.ChangeNotice(after, after, "chat-a") != "" {
		t.Error("notice for an unchanged snapshot is not empty")
	}
}

// Watch counts changes to the memory folders, including hand edits in a
// project folder created after the watch started.
func TestWatchCountsHandEdits(t *testing.T) {
	configDir := t.TempDir()
	store := NewStore(configDir, t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := store.Watch(ctx); err != nil {
		t.Fatal(err)
	}
	if !store.Watching() {
		t.Fatal("store is not watching")
	}
	waitForChange := func(what string, change func()) {
		t.Helper()
		before := store.Generation()
		change()
		for deadline := time.Now().Add(5 * time.Second); store.Generation() == before; time.Sleep(10 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("generation did not change after %s", what)
			}
		}
	}
	waitForChange("a hand edit", func() {
		if err := os.WriteFile(filepath.Join(configDir, "memory", "by-hand.md"), []byte("edited by hand\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	})
	root := "/work/new-project"
	dir, _ := store.Dir(ScopeProject, root)
	// The watcher adds a new project folder before it counts the change.
	waitForChange("a new project folder", func() {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	})
	waitForChange("a hand edit in a new project folder", func() {
		if err := os.WriteFile(filepath.Join(dir, "second.md"), []byte("edited by hand\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	})
	cancel()
	for deadline := time.Now().Add(5 * time.Second); store.Watching(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("store still watching after the context ended")
		}
	}
}
