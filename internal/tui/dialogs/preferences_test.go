package dialogs

import (
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/theme"
	"github.com/lkarlslund/koder/internal/ui"
)

func renderPreferencesDialog(dialog PreferencesDialog, width int, palette theme.Palette) string {
	size := dialog.Measure(&ui.Context{Palette: palette}, ui.Constraints{MaxW: width})
	return strings.Join(dialog.Surface(&ui.Context{Palette: palette}, ui.Rect{W: maxInt(width, size.W), H: size.H}).Lines(), "\n")
}

func defaultPreferencesValues() PreferencesValues {
	cfg := config.Default()
	return PreferencesValues{UI: cfg.UI, MaxToolLoopSteps: cfg.MaxToolLoopSteps}
}

func TestPreferencesDialogThemeAndToggleEmitDraftChanges(t *testing.T) {
	dialog := NewPreferencesDialog(defaultPreferencesValues(), []string{"tokyonight", "gruvbox"})

	action := dialog.Update(ui.KeyMsg{Type: ui.KeyRight})
	if action.Kind != PreferencesActionChanged {
		t.Fatalf("expected draft change action, got %#v", action)
	}
	if action.Values.MaxToolLoopSteps != config.Default().MaxToolLoopSteps+1 {
		t.Fatalf("expected tool turn limit to increment, got %#v", action.Values)
	}

	dialog.Update(ui.KeyMsg{Type: ui.KeyShiftTab})
	dialog.Update(ui.KeyMsg{Type: ui.KeyRight})
	dialog.Update(ui.KeyMsg{Type: ui.KeyTab})
	action = dialog.Update(ui.KeyMsg{Type: ui.KeyRight})
	if action.Kind != PreferencesActionChanged {
		t.Fatalf("expected draft change action, got %#v", action)
	}
	if action.Values.UI.Theme != "gruvbox" {
		t.Fatalf("expected theme to advance, got %q", action.Values.UI.Theme)
	}

	dialog.Update(ui.KeyMsg{Type: ui.KeyDown})
	action = dialog.Update(ui.KeyMsg{Type: ui.KeyRight})
	if action.Kind != PreferencesActionChanged {
		t.Fatalf("expected spinner change action, got %#v", action)
	}
	if action.Values.UI.Spinner == "dots" {
		t.Fatalf("expected spinner to advance, got %#v", action.Values)
	}

	dialog.Update(ui.KeyMsg{Type: ui.KeyDown})
	action = dialog.Update(ui.KeyMsg{Type: ui.KeySpace})
	if action.Kind != PreferencesActionChanged {
		t.Fatalf("expected toggle change action, got %#v", action)
	}
	if action.Values.UI.HalfBlocks {
		t.Fatalf("expected half blocks toggled off, got %#v", action.Values)
	}

	dialog = NewPreferencesDialog(defaultPreferencesValues(), []string{"tokyonight", "gruvbox"})
	dialog.tabList.Active = 2
	dialog.focus = preferencesFocusFields
	dialog.fieldIndex = 0
	action = dialog.Update(ui.KeyMsg{Type: ui.KeySpace})
	if action.Kind != PreferencesActionChanged {
		t.Fatalf("expected cursor blink toggle change action, got %#v", action)
	}
	if action.Values.UI.CursorBlink {
		t.Fatalf("expected cursor blink toggled off, got %#v", action.Values)
	}

	dialog.fieldIndex = 2
	action = dialog.Update(ui.KeyMsg{Type: ui.KeySpace})
	if action.Kind != PreferencesActionChanged {
		t.Fatalf("expected system toggle change action, got %#v", action)
	}
	if !action.Values.UI.ShowSystem {
		t.Fatalf("expected show system toggled on, got %#v", action.Values)
	}
}

func TestPreferencesDialogCancelReturnsOriginalUI(t *testing.T) {
	original := defaultPreferencesValues()
	dialog := NewPreferencesDialog(original, []string{"tokyonight", "gruvbox"})
	dialog.Update(ui.KeyMsg{Type: ui.KeyRight})

	action := dialog.Update(ui.KeyMsg{Type: ui.KeyEsc})
	if action.Kind != PreferencesActionCancel {
		t.Fatalf("expected cancel action, got %#v", action)
	}
	if action.Values != original {
		t.Fatalf("expected original preferences restored, got %#v", action.Values)
	}
}

func TestPreferencesDialogRenderShowsTabsAndButtons(t *testing.T) {
	dialog := NewPreferencesDialog(defaultPreferencesValues(), []string{"tokyonight", "gruvbox"})

	view := renderPreferencesDialog(dialog, 84, theme.Default().Palette)
	for _, needle := range []string{"Preferences", "General", "Appearance", "Behavior", "Tool Turns", "OK", "Cancel"} {
		if !strings.Contains(view, needle) {
			t.Fatalf("expected %q in preferences dialog, got %q", needle, view)
		}
	}

	dialog.Update(ui.KeyMsg{Type: ui.KeyShiftTab})
	dialog.Update(ui.KeyMsg{Type: ui.KeyRight})
	dialog.Update(ui.KeyMsg{Type: ui.KeyRight})
	dialog.Update(ui.KeyMsg{Type: ui.KeyTab})
	view = renderPreferencesDialog(dialog, 84, theme.Default().Palette)
	for _, needle := range []string{"Cursor Blink", "System"} {
		if !strings.Contains(view, needle) {
			t.Fatalf("expected behavior tab to show %q, got %q", needle, view)
		}
	}
}

func TestPreferencesDialogSpinnerPreviewAnimates(t *testing.T) {
	dialog := NewPreferencesDialog(defaultPreferencesValues(), []string{"tokyonight", "gruvbox"})
	dialog.tabList.Active = 1

	before := renderPreferencesDialog(dialog, 84, theme.Default().Palette)
	dialog.Tick()
	after := renderPreferencesDialog(dialog, 84, theme.Default().Palette)

	if before == after {
		t.Fatalf("expected animated spinner preview to change view")
	}
}

func TestPreferencesDialogToolTurnsEditorSupportsTypingAndStepping(t *testing.T) {
	dialog := NewPreferencesDialog(defaultPreferencesValues(), []string{"tokyonight", "gruvbox"})
	editor := dialog.editors["max_tool_loop_steps"]
	editor.SetValue("20")
	dialog.editors["max_tool_loop_steps"] = editor

	action := dialog.Update(ui.KeyMsg{Type: ui.KeyBackspace})
	if action.Kind != PreferencesActionChanged {
		t.Fatalf("expected backspace edit change, got %#v", action)
	}
	action = dialog.Update(ui.KeyMsg{Type: ui.KeyRunes, Runes: []rune("5")})
	if action.Kind != PreferencesActionChanged {
		t.Fatalf("expected digit edit change, got %#v", action)
	}
	if action.Values.MaxToolLoopSteps != 25 {
		t.Fatalf("expected typed value 25, got %#v", action.Values)
	}

	action = dialog.Update(ui.KeyMsg{Type: ui.KeyDown})
	if action.Values.MaxToolLoopSteps != 24 {
		t.Fatalf("expected down to decrement to 24, got %#v", action.Values)
	}
	action = dialog.Update(ui.KeyMsg{Type: ui.KeyUp})
	if action.Values.MaxToolLoopSteps != 25 {
		t.Fatalf("expected up to increment to 25, got %#v", action.Values)
	}
}
