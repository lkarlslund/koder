package ui

import (
	"testing"
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
