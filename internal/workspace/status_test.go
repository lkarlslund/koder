package workspace

import "testing"

func TestParseStatus(t *testing.T) {
	raw := "## main...origin/main [ahead 1]\n M internal/tui/model.go\nA  internal/workspace/status.go\nD  old.txt\n?? new.txt\n"
	got := parseStatus(raw)

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
}
