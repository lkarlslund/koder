package toolruntime

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lkarlslund/koder/internal/accesssettings"
	"github.com/lkarlslund/koder/internal/skills"
)

func writeSkill(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: Test workflow\n---\n"
	if err := os.WriteFile(filepath.Join(dir, name, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWithSkillMountsMakesSkillFoldersReadable(t *testing.T) {
	// Root is always readable, so put the skills in a home the session
	// cannot read.
	base := t.TempDir()
	t.Setenv("HOME", base)
	managed := filepath.Join(base, "managed")
	elsewhere := filepath.Join(base, "elsewhere")
	writeSkill(t, managed, "stock")
	writeSkill(t, elsewhere, "linked")
	if err := os.Symlink(filepath.Join(elsewhere, "linked"), filepath.Join(managed, "linked")); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(base, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	locked := accesssettings.LockedDown()
	got := withSkillMounts(locked, project, skills.InspectWithOptions(project, skills.DiscoverOptions{ManagedRoots: []string{managed}}))
	for _, path := range []string{filepath.Join(managed, "stock", "SKILL.md"), filepath.Join(elsewhere, "linked", "SKILL.md")} {
		if err := accesssettings.Allows(got, accesssettings.Request{Kind: accesssettings.AccessRead, Path: path, ProjectRoot: project}); err != nil {
			t.Errorf("read %s: %v", path, err)
		}
		if err := accesssettings.Allows(got, accesssettings.Request{Kind: accesssettings.AccessWrite, Path: path, ProjectRoot: project}); err == nil {
			t.Errorf("write %s allowed", path)
		}
	}
	if len(locked.Mounts) != 0 {
		t.Fatalf("input settings changed: %#v", locked.Mounts)
	}
}

func TestWithSkillMountsKeepsProjectSkillsWritable(t *testing.T) {
	project := t.TempDir()
	writeSkill(t, filepath.Join(project, ".agents", "skills"), "local")
	got := withSkillMounts(accesssettings.Default(), project, skills.InspectWithOptions(project, skills.DiscoverOptions{}))
	path := filepath.Join(project, ".agents", "skills", "local", "SKILL.md")
	if err := accesssettings.Allows(got, accesssettings.Request{Kind: accesssettings.AccessWrite, Path: path, ProjectRoot: project}); err != nil {
		t.Fatalf("project skill became read-only: %v", err)
	}
}
