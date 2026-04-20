package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lkarlslund/koder/internal/theme"
)

func TestSessionDialogSelectsCurrentSession(t *testing.T) {
	dialog := NewSessionDialog([]SessionItem{
		{Title: "First", Value: "1"},
		{Title: "Second", Value: "2"},
	})
	dialog.Update(tea.KeyMsg{Type: tea.KeyDown})

	action := dialog.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if action.Kind != SessionDialogActionSelect {
		t.Fatalf("expected select action, got %#v", action)
	}
	if action.SessionID != 2 {
		t.Fatalf("expected session 2, got %d", action.SessionID)
	}
}

func TestSessionDialogFiltersItems(t *testing.T) {
	dialog := NewSessionDialog([]SessionItem{
		{Title: "Alpha", Value: "1"},
		{Title: "Beta", Value: "2"},
	})
	dialog.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})

	view := dialog.View(84, theme.Default().Palette)
	if strings.Contains(view, "Alpha") {
		t.Fatalf("expected filtered session list, got %q", view)
	}
	if !strings.Contains(view, "Beta") {
		t.Fatalf("expected Beta in filtered list, got %q", view)
	}
}
