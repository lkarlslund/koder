package dialogs

import (
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/theme"
	"github.com/lkarlslund/koder/internal/ui"
)

func renderDisconnectDialog(dialog DisconnectDialog, width int, palette theme.Palette) string {
	size := dialog.Measure(&ui.Context{Palette: palette}, ui.Constraints{MaxW: width})
	return strings.Join(dialog.Render(&ui.Context{Palette: palette}, ui.Rect{W: maxInt(width, size.W), H: size.H}).Lines(), "\n")
}
func TestDisconnectDialogSelectsProvider(t *testing.T) {
	dialog := NewDisconnectDialog([]ProviderItem{{ID: "openai", Title: "OpenAI"}})
	action := dialog.Update(ui.KeyMsg{Type: ui.KeyEnter})
	if action.Kind != DisconnectDialogActionSelect || action.ProviderID != "openai" {
		t.Fatalf("unexpected action: %#v", action)
	}
}

func TestDisconnectDialogFiltersItems(t *testing.T) {
	dialog := NewDisconnectDialog([]ProviderItem{
		{ID: "openai", Title: "OpenAI"},
		{ID: "ollama", Title: "Ollama"},
	})
	dialog.Update(ui.KeyMsg{Type: ui.KeyRunes, Runes: []rune("o")})
	dialog.Update(ui.KeyMsg{Type: ui.KeyRunes, Runes: []rune("l")})

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
