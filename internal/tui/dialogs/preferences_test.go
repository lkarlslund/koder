package dialogs

import (
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/theme"
	"github.com/lkarlslund/koder/internal/ui"
	"github.com/lkarlslund/koder/internal/ui/uitest"
)

func renderPreferencesDialog(dialog PreferencesDialog, width int, palette theme.Palette) string {
	return uitest.RenderElementText(&ui.Context{Palette: palette}, dialog, width, 0)
}
func TestPreferencesDialogThemeAndToggleEmitDraftChanges(t *testing.T) {
	dialog := NewPreferencesDialog(config.Default().UI, []string{"tokyonight", "gruvbox"})

	action := dialog.Update(ui.KeyMsg{Type: ui.KeyRight})
	if action.Kind != PreferencesActionChanged {
		t.Fatalf("expected draft change action, got %#v", action)
	}
	if action.UI.Theme != "gruvbox" {
		t.Fatalf("expected theme to advance, got %q", action.UI.Theme)
	}

	dialog.Update(ui.KeyMsg{Type: ui.KeyDown})
	action = dialog.Update(ui.KeyMsg{Type: ui.KeyRight})
	if action.Kind != PreferencesActionChanged {
		t.Fatalf("expected spinner change action, got %#v", action)
	}
	if action.UI.Spinner == "dots" {
		t.Fatalf("expected spinner to advance, got %#v", action.UI)
	}

	dialog.Update(ui.KeyMsg{Type: ui.KeyDown})
	action = dialog.Update(ui.KeyMsg{Type: ui.KeySpace})
	if action.Kind != PreferencesActionChanged {
		t.Fatalf("expected toggle change action, got %#v", action)
	}
	if action.UI.HalfBlocks {
		t.Fatalf("expected half blocks toggled off, got %#v", action.UI)
	}

	dialog = NewPreferencesDialog(config.Default().UI, []string{"tokyonight", "gruvbox"})
	dialog.tabList.Active = 1
	dialog.focus = preferencesFocusFields
	dialog.fieldIndex = 0
	action = dialog.Update(ui.KeyMsg{Type: ui.KeySpace})
	if action.Kind != PreferencesActionChanged {
		t.Fatalf("expected cursor blink toggle change action, got %#v", action)
	}
	if action.UI.CursorBlink {
		t.Fatalf("expected cursor blink toggled off, got %#v", action.UI)
	}

	dialog.fieldIndex = 2
	action = dialog.Update(ui.KeyMsg{Type: ui.KeySpace})
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
	dialog.Update(ui.KeyMsg{Type: ui.KeyRight})

	action := dialog.Update(ui.KeyMsg{Type: ui.KeyEsc})
	if action.Kind != PreferencesActionCancel {
		t.Fatalf("expected cancel action, got %#v", action)
	}
	if action.UI != original {
		t.Fatalf("expected original UI restored, got %#v", action.UI)
	}
}

func TestPreferencesDialogRenderShowsTabsAndButtons(t *testing.T) {
	dialog := NewPreferencesDialog(config.Default().UI, []string{"tokyonight", "gruvbox"})

	view := renderPreferencesDialog(dialog, 84, theme.Default().Palette)
	for _, needle := range []string{"Preferences", "Appearance", "Behavior", "Theme", "Spinner", "OK", "Cancel"} {
		if !strings.Contains(view, needle) {
			t.Fatalf("expected %q in preferences dialog, got %q", needle, view)
		}
	}

	dialog.Update(ui.KeyMsg{Type: ui.KeyShiftTab})
	dialog.Update(ui.KeyMsg{Type: ui.KeyDown})
	view = renderPreferencesDialog(dialog, 84, theme.Default().Palette)
	for _, needle := range []string{"Cursor Blink", "System"} {
		if !strings.Contains(view, needle) {
			t.Fatalf("expected behavior tab to show %q, got %q", needle, view)
		}
	}
}

func TestPreferencesDialogSpinnerPreviewAnimates(t *testing.T) {
	dialog := NewPreferencesDialog(config.Default().UI, []string{"tokyonight", "gruvbox"})

	before := renderPreferencesDialog(dialog, 84, theme.Default().Palette)
	dialog.Tick()
	after := renderPreferencesDialog(dialog, 84, theme.Default().Palette)

	if before == after {
		t.Fatalf("expected animated spinner preview to change view")
	}
}
