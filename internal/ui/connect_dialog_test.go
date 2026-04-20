package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/provider"
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
