package dialogs

import (
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/theme"
	"github.com/lkarlslund/koder/internal/ui"
)

func renderThemeDialog(dialog ThemeDialog, width int, palette theme.Palette) string {
	size := dialog.Measure(&ui.Context{Palette: palette}, ui.Constraints{MaxW: width})
	return strings.Join(dialog.Render(&ui.Context{Palette: palette}, ui.Rect{W: maxInt(width, size.W), H: size.H}).Lines(), "\n")
}

func TestThemeDialogSelectsCurrentTheme(t *testing.T) {
	dialog := NewThemeDialog([]string{"tokyonight", "gruvbox"}, "tokyonight")
	action := dialog.Update(ui.KeyMsg{Type: ui.KeyEnter})
	if action.Kind != ThemeDialogActionSelect || action.Theme != "tokyonight" {
		t.Fatalf("unexpected action: %#v", action)
	}
}

func TestThemeDialogGridMovesAcrossRowsAndColumns(t *testing.T) {
	dialog := NewThemeDialog([]string{"one", "two", "three", "four", "five"}, "one")
	dialog.Update(ui.KeyMsg{Type: ui.KeyRight})
	current, _ := dialog.Current()
	if current != "two" {
		t.Fatalf("expected move right to select two, got %q", current)
	}
	dialog.Update(ui.KeyMsg{Type: ui.KeyDown})
	current, _ = dialog.Current()
	if current != "five" {
		t.Fatalf("expected move down to select five, got %q", current)
	}
}

func TestThemeDialogActivateControlSelectsTheme(t *testing.T) {
	dialog := NewThemeDialog([]string{"tokyonight", "gruvbox"}, "tokyonight")
	action := dialog.ActivateControl("theme-item-1")
	if action.Kind != ThemeDialogActionSelect || action.Theme != "gruvbox" {
		t.Fatalf("unexpected action: %#v", action)
	}
}

func TestThemeDialogRenderShowsGridThemes(t *testing.T) {
	dialog := NewThemeDialog([]string{"tokyonight", "gruvbox", "flexoki", "catppuccin"}, "tokyonight")
	got := renderThemeDialog(dialog, 84, theme.Resolve("tokyonight").Palette)
	for _, name := range []string{"tokyonight", "gruvbox", "flexoki", "catppuccin"} {
		if !strings.Contains(got, name) {
			t.Fatalf("expected %q in render, got %q", name, got)
		}
	}
}
