package agents

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeProjectRootDoesNotSearchParentMarkers(t *testing.T) {
	home := t.TempDir()
	project := filepath.Join(home, "project")
	if err := os.MkdirAll(filepath.Join(home, ".koder"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := NormalizeProjectRoot(project); got != project {
		t.Fatalf("expected project root %q, got %q", project, got)
	}
}

func TestDiscoverProjectKeepsAuthoritativeRoot(t *testing.T) {
	home := t.TempDir()
	project := filepath.Join(home, "project")
	if err := os.MkdirAll(filepath.Join(home, ".koder"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, agentsFileName), []byte("project rules"), 0o644); err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(t.TempDir(), "")
	snap, err := mgr.DiscoverProject(context.Background(), project, project)
	if err != nil {
		t.Fatal(err)
	}
	if snap.ProjectRoot != project {
		t.Fatalf("expected authoritative root %q, got %q", project, snap.ProjectRoot)
	}
	if len(snap.Files) != 1 || snap.Files[0].Path != filepath.Join(project, agentsFileName) {
		t.Fatalf("unexpected discovered files: %#v", snap.Files)
	}
}

func TestResolveCandidatesHonorsCancellation(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := resolveCandidates(ctx, "missing.md", root, root)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}

func TestNormalizeProjectRootUsesProvidedDirectory(t *testing.T) {
	cwd := t.TempDir()
	if got := NormalizeProjectRoot(cwd); got != cwd {
		t.Fatalf("expected provided project root, got %q", got)
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
	snap, err := mgr.DiscoverProject(context.Background(), root, sub)
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
