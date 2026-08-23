package skills

import (
	"encoding/base64"
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
	writeSkill(t, filepath.Join(home, ".koder", "skills", "managed-only", fileName), "managed-only", "managed")

	items := DiscoverWithOptions(cwd, DiscoverOptions{ProjectRoot: repo})
	if len(items) != 3 {
		t.Fatalf("expected 3 skills, got %d: %s", len(items), DebugString(items))
	}
	if items[0].Name != "dup" || !strings.Contains(items[0].Path, filepath.Join("pkg", ".agents", "skills", "dup", fileName)) {
		t.Fatalf("expected nearest project dup skill first, got %#v", items[0])
	}
	if items[1].Name != "global-only" || items[1].Scope != ScopeUser {
		t.Fatalf("expected user-global skill second, got %#v", items[1])
	}
	if items[2].Name != "managed-only" || items[2].Scope != ScopeManaged {
		t.Fatalf("expected managed user skill third, got %#v", items[2])
	}
}

func TestInspectParsesPortableMetadataAndLogo(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := filepath.Join(t.TempDir(), "repo")
	mustMkdirAll(t, filepath.Join(repo, ".git"))
	dir := filepath.Join(repo, ".agents", "skills", "visual-review")
	mustMkdirAll(t, filepath.Join(dir, "assets"))
	body := `---
name: visual-review
description: >-
  Review visual output carefully across
  multiple screen sizes.
license: Apache-2.0
compatibility: Requires a browser screenshot capability.
metadata:
  display-name: Visual Review
  short-description: Inspect screenshots and layouts
  logo: assets/logo.png
  brand-color: "#12AADD"
---

# Visual Review
`
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	// A valid one-pixel PNG is sufficient; catalog code only resolves the asset.
	png, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "logo.png"), png, 0o644); err != nil {
		t.Fatal(err)
	}

	catalog := InspectWithOptions(repo, DiscoverOptions{ProjectRoot: repo})
	if len(catalog.Items) != 1 {
		t.Fatalf("expected one skill, got %#v", catalog.Items)
	}
	got := catalog.Items[0]
	if !got.Valid || !got.Effective || got.Description != "Review visual output carefully across multiple screen sizes." {
		t.Fatalf("unexpected parsed skill: %#v", got)
	}
	if got.License != "Apache-2.0" || got.Presentation.DisplayName != "Visual Review" || got.Presentation.BrandColor != "#12AADD" {
		t.Fatalf("portable metadata was not preserved: %#v", got)
	}
	if got.Presentation.Logo != "assets/logo.png" || got.Presentation.LogoPath == "" {
		t.Fatalf("logo was not resolved: %#v", got.Presentation)
	}
}

func TestInspectRetainsInvalidDisabledAndShadowedSkills(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := filepath.Join(t.TempDir(), "repo")
	mustMkdirAll(t, filepath.Join(repo, ".git"))
	projectPath := filepath.Join(repo, ".agents", "skills", "review", fileName)
	userPath := filepath.Join(home, ".agents", "skills", "review", fileName)
	writeSkill(t, projectPath, "review", "project review")
	writeSkill(t, userPath, "review", "shared review")
	writeSkill(t, filepath.Join(home, ".agents", "skills", "broken", fileName), "wrong-name", "broken")

	catalog := InspectWithOptions(repo, DiscoverOptions{ProjectRoot: repo})
	if len(catalog.Items) != 3 {
		t.Fatalf("expected three catalog entries, got %#v", catalog.Items)
	}
	project, projectOK := catalogSkill(catalog, ScopeProject, "review")
	shared, sharedOK := catalogSkill(catalog, ScopeUser, "review")
	broken, brokenOK := catalogSkill(catalog, ScopeUser, "wrong-name")
	if !projectOK || !sharedOK || !brokenOK || !project.Effective || shared.ShadowedBy == "" || broken.Valid || broken.Error == "" {
		t.Fatalf("expected effective, shadowed, and invalid diagnostics: %#v", catalog.Items)
	}

	catalog = InspectWithOptions(repo, DiscoverOptions{ProjectRoot: repo, DisabledPaths: []string{projectPath}})
	project, _ = catalogSkill(catalog, ScopeProject, "review")
	shared, _ = catalogSkill(catalog, ScopeUser, "review")
	if project.Enabled || !shared.Effective {
		t.Fatalf("expected disabling project skill to expose shared skill: %#v", catalog.Items)
	}
}

func TestDiscoverFollowsSymlinkedSkillDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := filepath.Join(t.TempDir(), "repo")
	mustMkdirAll(t, filepath.Join(repo, ".git"))
	target := filepath.Join(t.TempDir(), "portable")
	writeSkill(t, filepath.Join(target, fileName), "portable", "Shared through a symlink")
	root := filepath.Join(home, ".agents", "skills")
	mustMkdirAll(t, root)
	if err := os.Symlink(target, filepath.Join(root, "portable")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	items := DiscoverWithOptions(repo, DiscoverOptions{ProjectRoot: repo})
	if len(items) != 1 || items[0].Name != "portable" || items[0].CanonicalDirectory != canonicalPath(target) {
		t.Fatalf("expected symlinked shared skill, got %#v", items)
	}
}

func TestCatalogEscapesValuesAndHonorsBudget(t *testing.T) {
	items := []Skill{
		{Name: "one", Description: "<unsafe & text>"},
		{Name: "two", Description: strings.Repeat("long ", 200)},
	}
	got := catalogXML(items, 512)
	if strings.Contains(got, "<unsafe & text>") || !strings.Contains(got, "&lt;unsafe &amp; text&gt;") {
		t.Fatalf("catalog values were not escaped: %q", got)
	}
	if len(got) > 512 {
		t.Fatalf("catalog exceeded budget: %d", len(got))
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

func catalogSkill(catalog Catalog, scope Scope, name string) (Skill, bool) {
	for _, skill := range catalog.Items {
		if skill.Scope == scope && skill.Name == name {
			return skill, true
		}
	}
	return Skill{}, false
}
