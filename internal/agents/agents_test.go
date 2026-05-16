package agents

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestFindProjectRootPrefersNearestMarker(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "repo")
	nested := filepath.Join(project, "a", "b")
	if err := os.MkdirAll(filepath.Join(project, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(project, "a", ".koder"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := FindProjectRoot(nested); got != filepath.Join(project, "a") {
		t.Fatalf("expected nearest marker root, got %q", got)
	}
}

func TestFindProjectRootFallsBackToCWD(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("HOME", cwd)
	if got := FindProjectRoot(cwd); got != cwd {
		t.Fatalf("expected cwd fallback, got %q", got)
	}
}

func TestDiscoverIncludesAgentsAndRecursiveReferences(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "app", "feature")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	globalDir := t.TempDir()
	globalPath := filepath.Join(globalDir, "AGENTS.md")
	files := map[string]string{
		globalPath:                               "global rule",
		filepath.Join(root, "AGENTS.md"):         "see docs/shared.md",
		filepath.Join(root, "docs", "shared.md"): "shared rule mentions extra.txt",
		filepath.Join(root, "docs", "extra.txt"): "deep reference",
		filepath.Join(root, "app", "AGENTS.md"):  "local rule",
	}
	for path, body := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mgr := NewManager(t.TempDir(), globalPath)
	snap, err := mgr.Discover(context.Background(), sub)
	if err != nil {
		t.Fatal(err)
	}
	if snap.ProjectRoot != root {
		t.Fatalf("unexpected project root: %q", snap.ProjectRoot)
	}
	if len(snap.Files) != 5 {
		t.Fatalf("expected 5 discovered files, got %d", len(snap.Files))
	}
	if snap.Files[0].Path != filepath.Join(root, "app", "AGENTS.md") {
		t.Fatalf("expected closest AGENTS first, got %#v", snap.Files[0])
	}
	if snap.Checksum == "" {
		t.Fatal("expected checksum")
	}
}

func TestParseResolverResponse(t *testing.T) {
	got, err := parseResolverResponse("```json\n{\"resolved_agents_md\":\"a\",\"conflict_summary\":\"No conflicts\"}\n```")
	if err != nil {
		t.Fatal(err)
	}
	if got.ResolvedAgentsMD != "a" || got.ConflictSummary != "No conflicts" {
		t.Fatalf("unexpected parse result: %#v", got)
	}
}
