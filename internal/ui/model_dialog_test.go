package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/theme"
)

func TestModelDialogSelectsModel(t *testing.T) {
	dialog := NewModelDialog("openai", []domain.Model{{ID: "gpt-5.4"}}, "gpt-5.4")
	action := dialog.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if action.Kind != ModelDialogActionSelect || action.ModelID != "gpt-5.4" {
		t.Fatalf("unexpected action: %#v", action)
	}
}

func TestModelDialogFiltersModels(t *testing.T) {
	dialog := NewModelDialog("openai", []domain.Model{{ID: "gpt-5.4"}, {ID: "gpt-4.1-mini"}}, "")
	dialog.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})
	dialog.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	dialog.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})

	if len(dialog.view) == 0 || dialog.view[0].ID != "gpt-4.1-mini" {
		t.Fatalf("unexpected filtered models: %#v", dialog.view)
	}
}

func TestModelDialogRenderShowsProvider(t *testing.T) {
	dialog := NewModelDialog("openai", []domain.Model{{ID: "gpt-5.4", OwnedBy: "openai", SupportsImages: true, CapabilitiesKnown: true}}, "gpt-5.4")
	got := dialog.View(84, theme.Resolve("tokyonight").Palette)
	if !strings.Contains(got, "Select Model") || !strings.Contains(got, "Provider: openai") || !strings.Contains(got, "image") {
		t.Fatalf("unexpected render: %q", got)
	}
}
