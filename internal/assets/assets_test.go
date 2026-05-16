package assets

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSyncInstallsUpdatesAndSkipsUserModifiedAssets(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	initial := Asset{ID: "example", Target: "skills/example/SKILL.md", Content: []byte("v1\n"), Mode: 0o644}

	results, err := Sync(ctx, root, []Asset{initial})
	if err != nil {
		t.Fatal(err)
	}
	assertResult(t, results, StatusInstalled)
	assertFile(t, filepath.Join(root, initial.Target), "v1\n")

	results, err = Sync(ctx, root, []Asset{initial})
	if err != nil {
		t.Fatal(err)
	}
	assertResult(t, results, StatusUnchanged)

	updated := initial
	updated.Content = []byte("v2\n")
	results, err = Sync(ctx, root, []Asset{updated})
	if err != nil {
		t.Fatal(err)
	}
	assertResult(t, results, StatusUpdated)
	assertFile(t, filepath.Join(root, initial.Target), "v2\n")

	if err := os.WriteFile(filepath.Join(root, initial.Target), []byte("user edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	newDefault := initial
	newDefault.Content = []byte("v3\n")
	results, err = Sync(ctx, root, []Asset{newDefault})
	if err != nil {
		t.Fatal(err)
	}
	assertResult(t, results, StatusModified)
	assertFile(t, filepath.Join(root, initial.Target), "user edit\n")
}

func TestSyncSkipsExistingUnmanagedFile(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "skills/example/SKILL.md")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("preexisting\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	results, err := Sync(context.Background(), root, []Asset{
		{ID: "example", Target: "skills/example/SKILL.md", Content: []byte("default\n"), Mode: 0o644},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertResult(t, results, StatusUnmanaged)
	assertFile(t, target, "preexisting\n")
}

func TestSyncReinstallsMissingManifestFile(t *testing.T) {
	root := t.TempDir()
	item := Asset{ID: "example", Target: "skills/example/SKILL.md", Content: []byte("v1\n"), Mode: 0o644}
	if _, err := Sync(context.Background(), root, []Asset{item}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, item.Target)); err != nil {
		t.Fatal(err)
	}

	results, err := Sync(context.Background(), root, []Asset{item})
	if err != nil {
		t.Fatal(err)
	}
	assertResult(t, results, StatusInstalled)
	assertFile(t, filepath.Join(root, item.Target), "v1\n")
}

func TestSyncRejectsEscapingTargets(t *testing.T) {
	_, err := Sync(context.Background(), t.TempDir(), []Asset{
		{ID: "bad", Target: "../bad", Content: []byte("bad")},
	})
	if err == nil {
		t.Fatal("expected escaping target error")
	}
}

func TestUserDefaultsIncludesSkillCreator(t *testing.T) {
	items, err := UserDefaults()
	if err != nil {
		t.Fatal(err)
	}
	foundPrompt := false
	foundCompactionPrompt := false
	foundSkill := false
	for _, item := range items {
		if item.Target == "skills/skill-creator/SKILL.md" && len(item.Content) > 0 {
			foundSkill = true
		}
		if item.Target == "system-prompt.md" && len(item.Content) > 0 {
			foundPrompt = true
		}
		if item.Target == "compaction-prompt.md" && len(item.Content) > 0 {
			foundCompactionPrompt = true
		}
	}
	if !foundSkill || !foundPrompt || !foundCompactionPrompt {
		t.Fatalf("expected embedded skill creator and prompt defaults, got %#v", items)
	}
}

func TestDefaultContentReadsEmbeddedDefault(t *testing.T) {
	content, err := DefaultContent("system-prompt.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "You are koder") {
		t.Fatalf("unexpected system prompt content: %q", string(content))
	}
}

func TestDefaultContentReadsEmbeddedCompactionDefault(t *testing.T) {
	content, err := DefaultContent("compaction-prompt.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "Summarize this coding session") {
		t.Fatalf("unexpected compaction prompt content: %q", string(content))
	}
}

func TestManifestRecordsLastWrittenHash(t *testing.T) {
	root := t.TempDir()
	item := Asset{ID: "example", Target: "skills/example/SKILL.md", Content: []byte("v1\n"), Mode: 0o644}
	if _, err := Sync(context.Background(), root, []Asset{item}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, manifestName))
	if err != nil {
		t.Fatal(err)
	}
	var got manifest
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	entry := got.Files["skills/example/SKILL.md"]
	if entry.AssetID != "example" || entry.SHA256 == "" || entry.Mode != "0644" {
		t.Fatalf("unexpected manifest entry: %#v", entry)
	}
}

func assertResult(t *testing.T, results []Result, want Status) {
	t.Helper()
	if len(results) != 1 {
		t.Fatalf("expected one result, got %#v", results)
	}
	if results[0].Status != want {
		t.Fatalf("expected status %s, got %#v", want, results[0])
	}
}

func assertFile(t *testing.T, path string, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("expected %q, got %q", want, data)
	}
}
