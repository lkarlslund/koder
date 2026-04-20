package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/theme"
)

func TestConnectDialogSelectsProviderAndSavesDraft(t *testing.T) {
	dialog := NewConnectDialog(provider.Catalog(), map[string]config.Provider{})

	if action := dialog.Update(tea.KeyMsg{Type: tea.KeyEnter}); action.Kind != ProviderConnectActionNone {
		t.Fatalf("unexpected action on provider select: %#v", action)
	}
	if action := dialog.Update(tea.KeyMsg{Type: tea.KeyTab}); action.Kind != ProviderConnectActionNone {
		t.Fatalf("unexpected action on tab: %#v", action)
	}
	action := dialog.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if action.Kind != ProviderConnectActionSave {
		t.Fatalf("expected save action, got %#v", action)
	}
	if action.Draft.ProviderID == "" || action.Draft.BaseURL == "" {
		t.Fatalf("expected populated draft, got %#v", action.Draft)
	}
}

func TestConnectDialogCanFilterProviderList(t *testing.T) {
	dialog := NewConnectDialog(provider.Catalog(), map[string]config.Provider{})
	dialog.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o")})
	dialog.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	dialog.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})

	if len(dialog.view) == 0 || dialog.view[0].ID != "ollama" {
		t.Fatalf("expected ollama filtered to top, got %#v", dialog.view)
	}
}

func TestConnectDialogProviderListRendersSingleLineRows(t *testing.T) {
	dialog := NewConnectDialog(provider.Catalog(), map[string]config.Provider{
		"openai": {},
	})

	got := dialog.View(88, theme.Resolve("tokyonight").Palette)
	if !strings.Contains(got, "✓ OpenAI") {
		t.Fatalf("expected compact provider row, got %q", got)
	}
	if strings.Contains(got, "Direct OpenAI API access\n") {
		t.Fatalf("expected description to stay on the same row, got %q", got)
	}
}

func TestConnectDialogTestActionEmitsDraft(t *testing.T) {
	dialog := NewConnectDialog(provider.Catalog(), map[string]config.Provider{})
	dialog.selectProvider(provider.Catalog()[0])
	dialog.advanceFocus(1)
	dialog.moveButtons(-1)
	action := dialog.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if action.Kind != ProviderConnectActionTest {
		t.Fatalf("expected test action, got %#v", action)
	}
}

func TestConnectDialogCyclesDiscoveredModels(t *testing.T) {
	dialog := NewConnectDialog(provider.Catalog(), map[string]config.Provider{})
	dialog.selectProvider(provider.Catalog()[0])
	dialog.SetModels([]string{"model-a", "model-b"})
	dialog.fieldIndex = len(dialog.formFields()) - 1
	dialog.Update(tea.KeyMsg{Type: tea.KeyRight})

	if dialog.draft.Model != "model-b" {
		t.Fatalf("expected next discovered model, got %q", dialog.draft.Model)
	}
}

func TestConnectDialogViewShowsEditorCursorAndTail(t *testing.T) {
	dialog := NewConnectDialog(provider.Catalog(), map[string]config.Provider{})
	dialog.selectProvider(provider.Catalog()[0])
	dialog.draft.BaseURL = "https://example.com/very/long/path/that/should/show/the/end"
	dialog.fieldIndex = 1

	got := dialog.View(90, theme.Resolve("tokyonight").Palette)
	if !strings.Contains(got, "█") {
		t.Fatalf("expected editor cursor in view, got %q", got)
	}
	if !strings.Contains(got, "show/the/end") {
		t.Fatalf("expected editor to keep tail of typed value visible, got %q", got)
	}
}
