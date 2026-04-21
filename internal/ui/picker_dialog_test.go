package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/lkarlslund/koder/internal/theme"
)

func TestPickerDialogAltOCSelectsCurrentItem(t *testing.T) {
	dialog := NewPickerDialog("Themes", "", []PickerItem{{Title: "Tokyo Night", Value: "tokyonight"}})

	action := dialog.Update(tea.KeyMsg{Type: tea.KeyRunes, Alt: true, Runes: []rune("o")})
	if action.Kind != PickerDialogActionSelect || action.Value != "tokyonight" {
		t.Fatalf("unexpected action: %#v", action)
	}
}

func TestPickerDialogMouseCancelButton(t *testing.T) {
	palette := theme.Default().Palette
	dialog := NewPickerDialog("Themes", "", []PickerItem{{Title: "Tokyo Night", Value: "tokyonight"}})
	lines := strings.Split(dialog.View(80, palette), "\n")

	buttonLine := -1
	cancelX := -1
	for idx, line := range lines {
		stripped := ansi.Strip(line)
		if !strings.Contains(stripped, "OK") || !strings.Contains(stripped, "Cancel") {
			continue
		}
		buttonLine = idx
		cancelX = strings.Index(stripped, "Cancel") + 1
		break
	}
	if buttonLine < 0 || cancelX < 0 {
		t.Fatalf("failed to find cancel button in view: %q", dialog.View(80, palette))
	}

	action := dialog.HandleMouse(cancelX, buttonLine, 80, palette)
	if action.Kind != PickerDialogActionCancel {
		t.Fatalf("expected cancel action from mouse click, got %#v", action)
	}
}
