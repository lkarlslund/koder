package workspace

import "testing"

func TestParseStatus(t *testing.T) {
	raw := "## main...origin/main [ahead 1]\n M internal/tui/model.go\nA  internal/workspace/status.go\nD  old.txt\n?? new.txt\n"
	numstat := "12\t4\tinternal/tui/model.go\n7\t0\tinternal/workspace/status.go\n0\t9\told.txt\n"
	got := parseStatus(raw, numstat)

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
	if got.Files[0].Path != "internal/tui/model.go" {
		t.Fatalf("unexpected first file: %#v", got.Files[0])
	}
	if got.Files[0].Additions != 12 || got.Files[0].Deletions != 4 {
		t.Fatalf("unexpected diff stats: %#v", got.Files[0])
	}
	if got.Files[3].Additions != 0 || got.Files[3].Deletions != 0 {
		t.Fatalf("unexpected untracked diff stats: %#v", got.Files[3])
	}
}
