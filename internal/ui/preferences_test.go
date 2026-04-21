package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/theme"
)

func TestPreferencesDialogThemeAndToggleEmitDraftChanges(t *testing.T) {
	dialog := NewPreferencesDialog(config.Default().UI, []string{"tokyonight", "gruvbox"})

	action := dialog.Update(tea.KeyMsg{Type: tea.KeyRight})
	if action.Kind != PreferencesActionChanged {
		t.Fatalf("expected draft change action, got %#v", action)
	}
	if action.UI.Theme != "gruvbox" {
		t.Fatalf("expected theme to advance, got %q", action.UI.Theme)
	}

	dialog.Update(tea.KeyMsg{Type: tea.KeyDown})
	action = dialog.Update(tea.KeyMsg{Type: tea.KeyRight})
	if action.Kind != PreferencesActionChanged {
		t.Fatalf("expected spinner change action, got %#v", action)
	}
	if action.UI.Spinner == "dots" {
		t.Fatalf("expected spinner to advance, got %#v", action.UI)
	}

	dialog.Update(tea.KeyMsg{Type: tea.KeyDown})
	action = dialog.Update(tea.KeyMsg{Type: tea.KeySpace})
	if action.Kind != PreferencesActionChanged {
		t.Fatalf("expected toggle change action, got %#v", action)
	}
	if action.UI.HalfBlocks {
		t.Fatalf("expected half blocks toggled off, got %#v", action.UI)
	}

	dialog.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	dialog.Update(tea.KeyMsg{Type: tea.KeyDown})
	dialog.Update(tea.KeyMsg{Type: tea.KeyTab})
	dialog.Update(tea.KeyMsg{Type: tea.KeyDown})
	action = dialog.Update(tea.KeyMsg{Type: tea.KeySpace})
	if action.Kind != PreferencesActionChanged {
		t.Fatalf("expected system toggle change action, got %#v", action)
	}
	if !action.UI.ShowSystem {
		t.Fatalf("expected show system toggled on, got %#v", action.UI)
	}
}

func TestPreferencesDialogCancelReturnsOriginalUI(t *testing.T) {
	original := config.Default().UI
	dialog := NewPreferencesDialog(original, []string{"tokyonight", "gruvbox"})
	dialog.Update(tea.KeyMsg{Type: tea.KeyRight})

	action := dialog.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if action.Kind != PreferencesActionCancel {
		t.Fatalf("expected cancel action, got %#v", action)
	}
	if action.UI != original {
		t.Fatalf("expected original UI restored, got %#v", action.UI)
	}
}

func TestPreferencesDialogRenderShowsTabsAndButtons(t *testing.T) {
	dialog := NewPreferencesDialog(config.Default().UI, []string{"tokyonight", "gruvbox"})

	view := dialog.View(84, theme.Default().Palette)
	for _, needle := range []string{"Preferences", "Appearance", "Behavior", "Theme", "Spinner", "OK", "Cancel"} {
		if !strings.Contains(view, needle) {
			t.Fatalf("expected %q in preferences dialog, got %q", needle, view)
		}
	}

	dialog.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	dialog.Update(tea.KeyMsg{Type: tea.KeyDown})
	view = dialog.View(84, theme.Default().Palette)
	if !strings.Contains(view, "System") {
		t.Fatalf("expected behavior tab to show system toggle, got %q", view)
	}
}

func TestPreferencesDialogSpinnerPreviewAnimates(t *testing.T) {
	dialog := NewPreferencesDialog(config.Default().UI, []string{"tokyonight", "gruvbox"})

	before := dialog.View(84, theme.Default().Palette)
	dialog.Tick()
	after := dialog.View(84, theme.Default().Palette)

	if before == after {
		t.Fatalf("expected animated spinner preview to change view")
	}
}
