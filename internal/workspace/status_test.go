package workspace

import (
	"context"
	"os"
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
