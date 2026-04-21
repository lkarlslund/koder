package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverPrefersNearestProjectSkill(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	repo := filepath.Join(t.TempDir(), "repo")
	cwd := filepath.Join(repo, "pkg", "feature")
	mustMkdirAll(t, filepath.Join(repo, ".git"))
	mustMkdirAll(t, cwd)

	writeSkill(t, filepath.Join(repo, ".agents", "skills", "dup", fileName), "dup", "repo")
	writeSkill(t, filepath.Join(repo, "pkg", ".agents", "skills", "dup", fileName), "dup", "nearest")
	writeSkill(t, filepath.Join(home, ".agents", "skills", "dup", fileName), "dup", "user")
	writeSkill(t, filepath.Join(home, ".agents", "skills", "global-only", fileName), "global-only", "global")

	items := Discover(cwd)
	if len(items) != 2 {
		t.Fatalf("expected 2 skills, got %d: %s", len(items), DebugString(items))
	}
	if items[0].Name != "dup" || !strings.Contains(items[0].Path, filepath.Join("pkg", ".agents", "skills", "dup", fileName)) {
		t.Fatalf("expected nearest project dup skill first, got %#v", items[0])
	}
	if items[1].Name != "global-only" || items[1].Scope != ScopeUser {
		t.Fatalf("expected user-global skill second, got %#v", items[1])
	}
}

func TestToolDescriptionIncludesAvailableSkills(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	repo := filepath.Join(t.TempDir(), "repo")
	mustMkdirAll(t, filepath.Join(repo, ".git"))
	writeSkill(t, filepath.Join(repo, ".agents", "skills", "formatter", fileName), "formatter", "Format output consistently")

	description := ToolDescription("Load a reusable local skill by name", repo)
	if !strings.Contains(description, "<available_skills>") {
		t.Fatalf("expected available skills block, got %q", description)
	}
	if !strings.Contains(description, "<name>formatter</name>") {
		t.Fatalf("expected formatter skill in description, got %q", description)
	}
}

func TestPromptContextMentionsDollarSyntax(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	repo := filepath.Join(t.TempDir(), "repo")
	mustMkdirAll(t, filepath.Join(repo, ".git"))
	writeSkill(t, filepath.Join(repo, ".agents", "skills", "review", fileName), "review", "Review code carefully")

	context := PromptContext(repo)
	if !strings.Contains(context, "$skill-name") {
		t.Fatalf("expected dollar skill hint, got %q", context)
	}
	if !strings.Contains(context, "<name>review</name>") {
		t.Fatalf("expected review skill in prompt context, got %q", context)
	}
}

func writeSkill(t *testing.T, path string, name string, description string) {
	t.Helper()
	mustMkdirAll(t, filepath.Dir(path))
	body := "---\nname: " + name + "\ndescription: " + description + "\n---\n\n# " + name + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}
