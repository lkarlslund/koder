package dialogs

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lkarlslund/koder/internal/theme"
	"github.com/lkarlslund/koder/internal/ui"
)


func renderDisconnectDialog(dialog DisconnectDialog, width int, palette theme.Palette) string {
	return ui.RenderElement(&ui.Context{Palette: palette}, dialog, width, 0)
}
func TestDisconnectDialogSelectsProvider(t *testing.T) {
	dialog := NewDisconnectDialog([]ProviderItem{{ID: "openai", Title: "OpenAI"}})
	action := dialog.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if action.Kind != DisconnectDialogActionSelect || action.ProviderID != "openai" {
		t.Fatalf("unexpected action: %#v", action)
	}
}

func TestDisconnectDialogFiltersItems(t *testing.T) {
	dialog := NewDisconnectDialog([]ProviderItem{
		{ID: "openai", Title: "OpenAI"},
		{ID: "ollama", Title: "Ollama"},
	})
	dialog.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o")})
	dialog.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})

	if len(dialog.view) == 0 || dialog.view[0].ID != "ollama" {
		t.Fatalf("expected ollama match, got %#v", dialog.view)
	}
}

func TestDisconnectDialogRenderShowsHelper(t *testing.T) {
	dialog := NewDisconnectDialog([]ProviderItem{{ID: "openai", Title: "OpenAI", Description: "Direct API", Details: []string{"Default: yes"}}})
	got := renderDisconnectDialog(dialog, 84, theme.Resolve("tokyonight").Palette)
	if !strings.Contains(got, "Disconnect Provider") || !strings.Contains(got, "Enter to disconnect") {
		t.Fatalf("unexpected render: %q", got)
	}
}
