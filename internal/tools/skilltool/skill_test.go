package skilltool

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/tools"
)

func TestDefinitionIncludesAvailableSkills(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repo, ".agents", "skills", "formatter", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("---\nname: formatter\ndescription: Format output\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	def, enabled := tools.DefinitionFor(domain.ToolKindSkill, tools.Runtime{Workdir: repo})
	if !enabled {
		t.Fatal("expected skill tool definition to be enabled")
	}
	if !strings.Contains(def.Function.Description, "<available_skills>") {
		t.Fatalf("expected available skills block, got %q", def.Function.Description)
	}
	if !strings.Contains(def.Function.Description, "<name>formatter</name>") {
		t.Fatalf("expected formatter skill in definition, got %q", def.Function.Description)
	}
}

func TestCallIncludesExactSkillResourceRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	skillDir := filepath.Join(repo, ".agents", "skills", "portable")
	path := filepath.Join(skillDir, "SKILL.md")
	body := "---\nname: portable\ndescription: Portable workflow\n---\nRun `scripts/helper`.\n"
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := (tool{}).Call(t.Context(), tools.Options{
		Runtime: tools.Runtime{Workdir: repo},
		Request: tools.Request{Args: map[string]string{"name": "portable"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "resource_root: "+skillDir) {
		t.Fatalf("missing exact resource root in %q", result.Output)
	}
	if !strings.Contains(result.Output, "Relative paths such as scripts/... and references/... resolve from resource_root.") {
		t.Fatalf("missing path resolution guidance in %q", result.Output)
	}
	stored, ok := result.Stored.(tools.SkillStoredResult)
	if !ok {
		t.Fatalf("stored result type = %T", result.Stored)
	}
	if stored.Content != body {
		t.Fatalf("stored content changed: %q", stored.Content)
	}
}
