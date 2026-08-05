package workspace

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestParseStatus(t *testing.T) {
	raw := "## main...origin/main [ahead 1]\n M internal/webui/server.go\nA  internal/workspace/status.go\nD  old.txt\n?? new.txt\n"
	numstat := "12\t4\tinternal/webui/server.go\n7\t0\tinternal/workspace/status.go\n0\t9\told.txt\n"
	got := parseStatus(raw, numstat, map[string]FileStatus{
		"new.txt": {Path: "new.txt", Additions: 3, Files: 1},
	})

	if got.Branch != "main" {
		t.Fatalf("unexpected branch: %q", got.Branch)
	}
	if got.Upstream != "origin/main" {
		t.Fatalf("unexpected upstream: %q", got.Upstream)
	}
	if got.Summary != "ahead 1" {
		t.Fatalf("unexpected summary: %q", got.Summary)
	}
	if got.Modified != 1 || got.Added != 1 || got.Deleted != 1 || got.Untracked != 1 {
		t.Fatalf("unexpected counts: %#v", got)
	}
	if len(got.Files) != 4 {
		t.Fatalf("unexpected files: %#v", got.Files)
	}
	if got.Files[0].Path != "internal/webui/server.go" {
		t.Fatalf("unexpected first file: %#v", got.Files[0])
	}
	if got.Files[0].Additions != 12 || got.Files[0].Deletions != 4 {
		t.Fatalf("unexpected diff stats: %#v", got.Files[0])
	}
	if got.Files[3].Additions != 3 || got.Files[3].Deletions != 0 || got.Files[3].Files != 1 {
		t.Fatalf("unexpected untracked diff stats: %#v", got.Files[3])
	}
}

func TestParseStatusExpandsUntrackedDirectoryStats(t *testing.T) {
	raw := "## main\n?? pkg/e2e/\n"
	got := parseStatus(raw, "", map[string]FileStatus{
		"pkg/e2e/a.go": {Path: "pkg/e2e/a.go", Additions: 10, Files: 1},
		"pkg/e2e/b.go": {Path: "pkg/e2e/b.go", Additions: 20, Files: 1},
	})
	if len(got.Files) != 2 {
		t.Fatalf("unexpected files: %#v", got.Files)
	}
	if got.Files[0].Path != "pkg/e2e/a.go" || got.Files[0].Code != "??" || got.Files[0].Additions != 10 || got.Files[0].Files != 1 {
		t.Fatalf("expected first untracked directory file, got %#v", got.Files[0])
	}
	if got.Files[1].Path != "pkg/e2e/b.go" || got.Files[1].Code != "??" || got.Files[1].Additions != 20 || got.Files[1].Files != 1 {
		t.Fatalf("expected second untracked directory file, got %#v", got.Files[1])
	}
	if got.Untracked != 2 {
		t.Fatalf("expected untracked file count, got %#v", got)
	}
}

func TestUntrackedFileStatsSkipsNestedGitInternals(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "test@example.invalid")
	runGit(t, root, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("tracked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "tracked.txt")
	runGit(t, root, "commit", "-m", "init")

	nestedPack := filepath.Join(root, "nested", ".git", "objects", "pack", "pack-test.pack")
	if err := os.MkdirAll(filepath.Dir(nestedPack), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nestedPack, []byte("not text\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	visible := filepath.Join(root, "nested", "visible.txt")
	if err := os.WriteFile(visible, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	stats := untrackedFileStats(context.Background(), root)
	if _, ok := stats["nested/.git/objects/pack/pack-test.pack"]; ok {
		t.Fatalf("expected nested git internal path to be skipped, got %#v", stats)
	}
	if got := stats["nested/visible.txt"].Additions; got != 2 {
		t.Fatalf("expected visible file line count, got %#v", stats["nested/visible.txt"])
	}
}

func TestCountFileLinesDoesNotReadHugeFiles(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "huge.txt")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(maxUntrackedLineCountBytes, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if got := countFileLines(path); got != 0 {
		t.Fatalf("expected huge file line count to be skipped, got %d", got)
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
	defer watcher.Close()

	if err := os.WriteFile(filepath.Join(root, "changed.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case <-watcher.Events():
	case <-time.After(2 * time.Second):
		t.Fatal("expected file watcher event")
	}
}

func TestWatcherSkipsGitDirectories(t *testing.T) {
	if !shouldSkipWatchDir(filepath.Join("repo", ".git"), ".git") {
		t.Fatal("expected watcher to skip .git directories")
	}
	if !shouldSkipWatchDir(filepath.Join("repo", ".git", "objects"), "objects") {
		t.Fatal("expected watcher to skip .git objects directories")
	}
}

func TestWatcherReturnsTooLargeWhenDirectoryLimitExceeded(t *testing.T) {
	root := t.TempDir()
	for idx := range 3 {
		if err := os.Mkdir(filepath.Join(root, string(rune('a'+idx))), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	watcher, err := watchWithLimits(context.Background(), root, watchLimits{MaxDirs: 2, MaxEntries: 100})
	if watcher != nil {
		_ = watcher.Close()
	}
	if !errors.Is(err, ErrWatchTreeTooLarge) {
		t.Fatalf("expected too-large watcher error, got %v", err)
	}
}

func TestWatcherReturnsTooLargeWhenEntryLimitExceeded(t *testing.T) {
	root := t.TempDir()
	for idx := range 3 {
		if err := os.WriteFile(filepath.Join(root, string(rune('a'+idx))+".txt"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
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
