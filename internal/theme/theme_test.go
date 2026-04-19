package theme

import "testing"

func TestNamesIncludeOpenCodeAndClaudeThemes(t *testing.T) {
	names := Names()
	required := []string{
		"aura",
		"catppuccin",
		"catppuccin-frappe",
		"catppuccin-macchiato",
		"dracula",
		"everforest",
		"kanagawa",
		"nord",
		"one-dark",
		"tokyonight",
		"vercel",
		"zenburn",
		"claude-dark",
		"claude-light",
		"claude-dark-daltonized",
		"claude-light-daltonized",
	}
	for _, name := range required {
		found := false
		for _, candidate := range names {
			if candidate == name {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected theme %q in Names(), got %#v", name, names)
		}
	}
}

func TestResolveFallsBackToDefault(t *testing.T) {
	got := Resolve("does-not-exist")
	if got.Name != Default().Name {
		t.Fatalf("expected default fallback %q, got %q", Default().Name, got.Name)
	}
}

func TestResolveThemeProducesUsablePalette(t *testing.T) {
	got := Resolve("dracula")
	if got.Name != "dracula" {
		t.Fatalf("expected dracula theme, got %q", got.Name)
	}
	if got.Palette.MarkdownText == "" || got.Palette.SidebarBackground == "" || got.Palette.UserTextBackground == "" {
		t.Fatalf("expected populated palette, got %#v", got.Palette)
	}
}
