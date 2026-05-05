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
	return PreferencesValues{
		UI:                        cfg.UI,
		MaxToolLoopSteps:          cfg.MaxToolLoopSteps,
		AutoCompactAt:             cfg.AutoCompactAt,
		CompactionKeepToolBatches: cfg.CompactionKeepToolBatches,
	}
}

func TestPreferencesDialogThemeAndToggleEmitDraftChanges(t *testing.T) {
	dialog := NewPreferencesDialog(defaultPreferencesValues(), []string{"tokyonight", "gruvbox"}, []string{"github", "monokai"})

	action := dialog.Update(ui.KeyMsg{Type: ui.KeyRight})
	if action.Kind != PreferencesActionChanged {
		t.Fatalf("expected draft change action, got %#v", action)
	}
	if action.Values.MaxToolLoopSteps != config.Default().MaxToolLoopSteps+1 {
		t.Fatalf("expected tool turn limit to increment, got %#v", action.Values)
	}
	dialog.fieldIndex = 1
	action = dialog.Update(ui.KeyMsg{Type: ui.KeyLeft})
	if action.Kind != PreferencesActionChanged {
		t.Fatalf("expected compact threshold change action, got %#v", action)
	}
	if action.Values.AutoCompactAt != config.Default().AutoCompactAt-1 {
		t.Fatalf("expected compact threshold to decrement, got %#v", action.Values)
	}
	dialog.fieldIndex = 2
	action = dialog.Update(ui.KeyMsg{Type: ui.KeyLeft})
	if action.Kind != PreferencesActionChanged {
		t.Fatalf("expected kept batches change action, got %#v", action)
	}
	if action.Values.CompactionKeepToolBatches != config.Default().CompactionKeepToolBatches-1 {
		t.Fatalf("expected kept batches to decrement, got %#v", action.Values)
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
		t.Fatalf("expected code style change action, got %#v", action)
	}
	if action.Values.UI.CodeStyle != "monokai" {
		t.Fatalf("expected code style to advance, got %q", action.Values.UI.CodeStyle)
	}

	dialog.Update(ui.KeyMsg{Type: ui.KeyDown})
	action = dialog.Update(ui.KeyMsg{Type: ui.KeyRight})
	if action.Kind != PreferencesActionChanged {
		t.Fatalf("expected edit forgiveness change action, got %#v", action)
	}
	if action.Values.UI.EditForgiveness != 4 {
		t.Fatalf("expected edit forgiveness to advance, got %#v", action.Values)
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

	dialog.Update(ui.KeyMsg{Type: ui.KeyDown})
	dialog.Update(ui.KeyMsg{Type: ui.KeyDown})
	action = dialog.Update(ui.KeyMsg{Type: ui.KeyRight})
	if action.Kind != PreferencesActionChanged {
		t.Fatalf("expected sidebar width change action, got %#v", action)
	}
	if action.Values.UI.SidebarWidth != config.Default().UI.SidebarWidth+1 {
		t.Fatalf("expected sidebar width to increment, got %#v", action.Values)
	}

	dialog = NewPreferencesDialog(defaultPreferencesValues(), []string{"tokyonight", "gruvbox"}, []string{"github", "monokai"})
	dialog.tabList.Active = 2
	dialog.focus = preferencesFocusFields
	dialog.fieldIndex = 0
	action = dialog.Update(ui.KeyMsg{Type: ui.KeySpace})
	if action.Kind != PreferencesActionChanged {
		t.Fatalf("expected auto continue toggle change action, got %#v", action)
	}
	if action.Values.UI.AutoContinue {
		t.Fatalf("expected auto continue toggled off, got %#v", action.Values)
	}

	dialog.fieldIndex = 1
	action = dialog.Update(ui.KeyMsg{Type: ui.KeySpace})
	if action.Kind != PreferencesActionChanged {
		t.Fatalf("expected cursor blink toggle change action, got %#v", action)
	}
	if action.Values.UI.CursorBlink {
		t.Fatalf("expected cursor blink toggled off, got %#v", action.Values)
	}

	dialog.fieldIndex = 3
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
	dialog := NewPreferencesDialog(original, []string{"tokyonight", "gruvbox"}, []string{"github", "monokai"})
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
	dialog := NewPreferencesDialog(defaultPreferencesValues(), []string{"tokyonight", "gruvbox"}, []string{"github", "monokai"})

	view := renderPreferencesDialog(dialog, 84, theme.Default().Palette)
	for _, needle := range []string{"Preferences", "General", "Appearance", "Behavior", "Tool Turns", "Compact At %", "Keep Tool Batches", "OK", "Cancel"} {
		if !strings.Contains(view, needle) {
			t.Fatalf("expected %q in preferences dialog, got %q", needle, view)
		}
	}
	if strings.Contains(view, "┌") || strings.Contains(view, "┐") || strings.Contains(view, "└") || strings.Contains(view, "┘") {
		t.Fatalf("expected one-line integer fields without boxed input borders, got %q", view)
	}
	dialog.tabList.Active = 1
	view = renderPreferencesDialog(dialog, 84, theme.Default().Palette)
	if !strings.Contains(view, "Code Style") || !strings.Contains(view, "Edit Forgiveness") {
		t.Fatalf("expected appearance tab to show code style, got %q", view)
	}

	dialog.Update(ui.KeyMsg{Type: ui.KeyShiftTab})
	dialog.Update(ui.KeyMsg{Type: ui.KeyRight})
	dialog.Update(ui.KeyMsg{Type: ui.KeyRight})
	dialog.Update(ui.KeyMsg{Type: ui.KeyTab})
	view = renderPreferencesDialog(dialog, 84, theme.Default().Palette)
	for _, needle := range []string{"Auto Continue", "Cursor Blink", "System"} {
		if !strings.Contains(view, needle) {
			t.Fatalf("expected behavior tab to show %q, got %q", needle, view)
		}
	}
}

func TestPreferencesDialogSpinnerPreviewAnimates(t *testing.T) {
	dialog := NewPreferencesDialog(defaultPreferencesValues(), []string{"tokyonight", "gruvbox"}, []string{"github", "monokai"})
	dialog.tabList.Active = 1

	before := renderPreferencesDialog(dialog, 84, theme.Default().Palette)
	dialog.Tick()
	after := renderPreferencesDialog(dialog, 84, theme.Default().Palette)

	if before == after {
		t.Fatalf("expected animated spinner preview to change view")
	}
}

func TestPreferencesDialogToolTurnsEditorSupportsTyping(t *testing.T) {
	dialog := NewPreferencesDialog(defaultPreferencesValues(), []string{"tokyonight", "gruvbox"}, []string{"github", "monokai"})
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

	dialog.fieldIndex = 1
	dialog.Update(ui.KeyMsg{Type: ui.KeyUp})
	if dialog.fieldIndex != 0 {
		t.Fatalf("expected up to move to previous field, got %d", dialog.fieldIndex)
	}

	dialog.fieldIndex = 1
	editor = dialog.editors["auto_compact_at"]
	editor.SetValue("80")
	dialog.editors["auto_compact_at"] = editor

	action = dialog.Update(ui.KeyMsg{Type: ui.KeyBackspace})
	if action.Kind != PreferencesActionChanged {
		t.Fatalf("expected auto compact backspace edit change, got %#v", action)
	}
	action = dialog.Update(ui.KeyMsg{Type: ui.KeyRunes, Runes: []rune("5")})
	if action.Kind != PreferencesActionChanged {
		t.Fatalf("expected auto compact digit edit change, got %#v", action)
	}
	if action.Values.AutoCompactAt != 85 {
		t.Fatalf("expected typed auto compact value 85, got %#v", action.Values)
	}

	dialog.fieldIndex = 2
	editor = dialog.editors["compaction_keep_tool_batches"]
	editor.SetValue("2")
	dialog.editors["compaction_keep_tool_batches"] = editor

	action = dialog.Update(ui.KeyMsg{Type: ui.KeyBackspace})
	if action.Kind != PreferencesActionChanged {
		t.Fatalf("expected keep batches backspace edit change, got %#v", action)
	}
	action = dialog.Update(ui.KeyMsg{Type: ui.KeyRunes, Runes: []rune("0")})
	if action.Kind != PreferencesActionChanged {
		t.Fatalf("expected keep batches digit edit change, got %#v", action)
	}
	if action.Values.CompactionKeepToolBatches != 0 {
		t.Fatalf("expected typed kept batches value 0, got %#v", action.Values)
	}

	dialog.tabList.Active = 1
	dialog.fieldIndex = 6
	editor = dialog.editors["sidebar_width"]
	editor.SetValue("30")
	dialog.editors["sidebar_width"] = editor

	action = dialog.Update(ui.KeyMsg{Type: ui.KeyBackspace})
	if action.Kind != PreferencesActionChanged {
		t.Fatalf("expected sidebar width backspace edit change, got %#v", action)
	}
	action = dialog.Update(ui.KeyMsg{Type: ui.KeyRunes, Runes: []rune("5")})
	if action.Kind != PreferencesActionChanged {
		t.Fatalf("expected sidebar width digit edit change, got %#v", action)
	}
	if action.Values.UI.SidebarWidth != 35 {
		t.Fatalf("expected typed sidebar width 35, got %#v", action.Values)
	}
}

func TestPreferencesDialogButtonFocusUsesArrowKeys(t *testing.T) {
	dialog := NewPreferencesDialog(defaultPreferencesValues(), []string{"tokyonight", "gruvbox"}, []string{"github", "monokai"})
	dialog.focus = preferencesFocusButtons
	dialog.buttonIndex = 0

	dialog.Update(ui.KeyMsg{Type: ui.KeyRight})
	if dialog.buttonIndex != 1 {
		t.Fatalf("expected right arrow to move to cancel button, got %d", dialog.buttonIndex)
	}
	dialog.Update(ui.KeyMsg{Type: ui.KeyLeft})
	if dialog.buttonIndex != 0 {
		t.Fatalf("expected left arrow to move back to ok button, got %d", dialog.buttonIndex)
	}
	dialog.Update(ui.KeyMsg{Type: ui.KeyDown})
	if dialog.buttonIndex != 1 {
		t.Fatalf("expected down arrow to move across button bar, got %d", dialog.buttonIndex)
	}
	dialog.Update(ui.KeyMsg{Type: ui.KeyUp})
	if dialog.buttonIndex != 0 {
		t.Fatalf("expected up arrow to move back across button bar, got %d", dialog.buttonIndex)
	}
}
