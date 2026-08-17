package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseStatusSeparatesMetadataAndDiffPreview(t *testing.T) {
	raw := "## main...origin/main [ahead 1]\n M internal/webui/server.go\nA  internal/workspace/status.go\nD  old.txt\n?? new.txt\n"
	numstat := "12\t4\tinternal/webui/server.go\n7\t0\tinternal/workspace/status.go\n0\t9\told.txt\n"
	got := parseStatus(raw, numstat)

	if got.Status.Branch != "main" {
		t.Fatalf("unexpected branch: %q", got.Status.Branch)
	}
	if got.Status.Upstream != "origin/main" {
		t.Fatalf("unexpected upstream: %q", got.Status.Upstream)
	}
	if got.Status.Summary != "ahead 1" {
		t.Fatalf("unexpected summary: %q", got.Status.Summary)
	}
	if got.Status.Modified != 1 || got.Status.Added != 1 || got.Status.Deleted != 1 || got.Status.Untracked != 1 {
		t.Fatalf("unexpected counts: %#v", got.Status)
	}
	if len(got.Diff.Files) != 4 {
		t.Fatalf("unexpected files: %#v", got.Diff.Files)
	}
	if got.Diff.Files[0].Path != "internal/webui/server.go" {
		t.Fatalf("unexpected first file: %#v", got.Diff.Files[0])
	}
	if got.Diff.Files[0].Additions != 12 || got.Diff.Files[0].Deletions != 4 {
		t.Fatalf("unexpected diff stats: %#v", got.Diff.Files[0])
	}
	if got.Diff.Files[3].Additions != 0 || got.Diff.Files[3].Deletions != 0 {
		t.Fatalf("untracked files should not be line-counted, got %#v", got.Diff.Files[3])
	}
}

func TestParseStatusDoesNotExpandUntrackedDirectories(t *testing.T) {
	got := parseStatus("## main\n?? pkg/e2e/\n", "")
	if got.Status.Untracked != 1 {
		t.Fatalf("expected one untracked directory row, got %#v", got.Status)
	}
	if len(got.Diff.Files) != 1 {
		t.Fatalf("unexpected files: %#v", got.Diff.Files)
	}
	if got.Diff.Files[0].Path != "pkg/e2e/" || got.Diff.Files[0].Code != "??" {
		t.Fatalf("expected unexpanded directory row, got %#v", got.Diff.Files[0])
	}
}

func TestParseStatusCapsDetailedDiffPreview(t *testing.T) {
	var raw strings.Builder
	raw.WriteString("## main\n")
	for i := range maxDiffFiles + 5 {
		_, _ = fmt.Fprintf(&raw, " M file-%04d.go\n", i)
	}

	got := parseStatus(raw.String(), "")
	if got.Status.Modified != maxDiffFiles+5 {
		t.Fatalf("expected aggregate modified count to include all rows, got %#v", got.Status)
	}
	if len(got.Diff.Files) != maxDiffFiles {
		t.Fatalf("expected capped file list, got %d", len(got.Diff.Files))
	}
	if !got.Diff.FilesTruncated || got.Diff.FileLimit != maxDiffFiles {
		t.Fatalf("expected truncation metadata, got %#v", got.Diff)
	}
}

func TestSnapshotUsesNormalUntrackedMode(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "test@example.invalid")
	runGit(t, root, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("tracked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "tracked.txt")
	runGit(t, root, "commit", "-m", "init")

	for _, path := range []string{
		filepath.Join(root, "nested", "visible-a.txt"),
		filepath.Join(root, "nested", "visible-b.txt"),
		filepath.Join(root, "nested", ".git", "objects", "pack", "pack-test.pack"),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("one\ntwo\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got, err := Snapshot(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status.Untracked != 1 {
		t.Fatalf("expected one untracked directory row, got %#v", got.Status)
	}
	if len(got.Diff.Files) != 1 || got.Diff.Files[0].Path != "nested/" {
		t.Fatalf("expected normal untracked directory preview, got %#v", got.Diff.Files)
	}
}

func TestWatcherReportsFileChanges(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	watcher, err := Watch(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := watcher.Close(); err != nil {
			t.Errorf("close watcher: %v", err)
		}
	})

	if err := os.WriteFile(filepath.Join(root, "changed.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	select {
	case <-watcher.Events():
	case <-time.After(2 * time.Second):
		t.Fatal("expected file watcher event")
	}
}

func TestWatcherRejectsTooManyDirectories(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "b"), 0o755); err != nil {
		t.Fatal(err)
	}

	watcher, err := watchWithLimits(context.Background(), root, watchLimits{MaxDirs: 10, MaxEntries: 2})
	if watcher != nil {
		_ = watcher.Close()
	}
	if !errors.Is(err, ErrWatchTreeTooLarge) {
		t.Fatalf("expected too-large watcher error, got %v", err)
	}
}

func TestWatcherRejectsBroadRoots(t *testing.T) {
	if !isBroadWatchRoot(string(filepath.Separator)) {
		t.Fatal("expected filesystem root to be too broad for watching")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip(err)
	}
	if !isBroadWatchRoot(home) {
		t.Fatal("expected user home directory to be too broad for watching")
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, output)
	}
}
