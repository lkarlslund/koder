package skilltool

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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

	def, enabled := tool{}.Definition(tools.Runtime{Workdir: repo})
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
