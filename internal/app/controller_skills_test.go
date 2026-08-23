package app

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/config"
)

func TestSkillsPreferencesAndInspectionExposePortablePresentation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := t.TempDir()
	dir := filepath.Join(project, ".agents", "skills", "visual-review")
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `---
name: visual-review
description: Review visual output when a screenshot or UI needs inspection.
license: Apache-2.0
metadata:
  display-name: Visual Review
  logo: assets/logo.png
  brand-color: "#123ABC"
---

# Visual Review

Inspect the supplied screenshot.
`
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	png, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "logo.png"), png, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default().WithManagedAssetsDir(filepath.Join(t.TempDir(), "managed"))
	controller := &Controller{cfg: cfg, projectRoot: project}
	state, err := controller.SkillsForSelection(context.Background(), Selection{})
	if err != nil {
		t.Fatal(err)
	}
	var item *SkillPreference
	for idx := range state.Items {
		if state.Items[idx].Name == "visual-review" {
			item = &state.Items[idx]
			break
		}
	}
	if item == nil || item.DisplayName != "Visual Review" || item.BrandColor != "#123ABC" || !strings.HasPrefix(item.LogoDataURL, "data:image/png;base64,") {
		t.Fatalf("unexpected skill preference: %#v", item)
	}
	inspection, err := controller.InspectSkill(context.Background(), Selection{}, item.CanonicalPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(inspection.Content, "Inspect the supplied screenshot") || len(inspection.Files) != 2 {
		t.Fatalf("unexpected skill inspection: %#v", inspection)
	}
}

func TestApplySkillsPreferencesPreservesAbsentLegacyPayload(t *testing.T) {
	cfg := config.Default()
	cfg.Skills.Disabled = []string{"/tmp/disabled/SKILL.md"}
	if err := applySkillsPreferences(&cfg, SkillsPreferences{}); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Skills.Disabled) != 1 {
		t.Fatalf("legacy payload cleared skill settings: %#v", cfg.Skills)
	}
}

func TestApplySkillsPreferencesValidatesPolicy(t *testing.T) {
	cfg := config.Default()
	if err := applySkillsPreferences(&cfg, SkillsPreferences{CatalogMaxChars: 8_192, DisabledPaths: []string{"/tmp/a/SKILL.md", "/tmp/a/SKILL.md"}}); err != nil {
		t.Fatal(err)
	}
	if cfg.Skills.CatalogMaxChars != 8_192 || len(cfg.Skills.Disabled) != 1 {
		t.Fatalf("skills policy not applied: %#v", cfg.Skills)
	}
	if err := applySkillsPreferences(&cfg, SkillsPreferences{CatalogMaxChars: 100, DisabledPaths: []string{}}); err == nil {
		t.Fatal("expected invalid prompt budget to fail")
	}
	if err := applySkillsPreferences(&cfg, SkillsPreferences{CatalogMaxChars: 1_000, DisabledPaths: []string{"relative/SKILL.md"}}); err == nil {
		t.Fatal("expected relative disabled path to fail")
	}
}
