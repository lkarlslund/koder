package ui

import (
	"testing"

	"github.com/lkarlslund/koder/internal/theme"
)

func TestPickerDialogAltOCSelectsCurrentItem(t *testing.T) {
	dialog := NewPickerDialog("Themes", "", []PickerItem{{Title: "Tokyo Night", Value: "tokyonight"}})

	action := dialog.Update(KeyMsg{Type: KeyRunes, Alt: true, Runes: []rune("o")})
	if action.Kind != PickerDialogActionSelect || action.Value != "tokyonight" {
		t.Fatalf("unexpected action: %#v", action)
	}
}

func TestPickerDialogActivateCancelButton(t *testing.T) {
	dialog := NewPickerDialog("Themes", "", []PickerItem{{Title: "Tokyo Night", Value: "tokyonight"}})
	action := dialog.ActivateControl("cancel")
	if action.Kind != PickerDialogActionCancel {
		t.Fatalf("expected cancel action, got %#v", action)
	}
}

func TestPickerDialogRenderMatchesInnerElement(t *testing.T) {
	palette := theme.Default().Palette
	dialog := NewPickerDialog("Themes", "Choose one", []PickerItem{
		{Title: "Tokyo Night", Description: "Dark", Value: "tokyonight"},
		{Title: "Gruvbox", Description: "Warm", Value: "gruvbox"},
	})
	assertNodeRenderMatchesWrapper(t, &Context{Palette: palette, Runtime: &Runtime{}}, AsNode(dialog), dialog.node(80, palette), Rect{W: 80, H: 10})
}

func TestPickerDialogAltBackspaceDeletesPreviousWord(t *testing.T) {
	dialog := NewPickerDialog("Themes", "", []PickerItem{{Title: "Tokyo Night", Value: "tokyonight"}})
	dialog.Query = "tokyo night"
	dialog.Update(KeyMsg{Type: KeyBackspace, Alt: true})
	if got := dialog.Query; got != "tokyo " {
		t.Fatalf("expected alt+backspace to delete previous word, got %q", got)
	}
}
