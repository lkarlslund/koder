package dialogs

import (
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/theme"
	"github.com/lkarlslund/koder/internal/ui"
	"github.com/lkarlslund/koder/internal/ui/uitest"
)

func renderConnectDialog(dialog ConnectDialog, width int, palette theme.Palette) string {
	return uitest.RenderElementText(&ui.Context{Palette: palette}, dialog, width, 0)
}
func TestConnectDialogSelectsProviderAndSavesDraft(t *testing.T) {
	dialog := NewConnectDialog(provider.Catalog(), map[string]config.Provider{})

	if action := dialog.Update(ui.KeyMsg{Type: ui.KeyEnter}); action.Kind != ProviderConnectActionNone {
		t.Fatalf("unexpected action on provider select: %#v", action)
	}
	if action := dialog.Update(ui.KeyMsg{Type: ui.KeyTab}); action.Kind != ProviderConnectActionNone {
		t.Fatalf("unexpected action on tab: %#v", action)
	}
	action := dialog.Update(ui.KeyMsg{Type: ui.KeyEnter})
	if action.Kind != ProviderConnectActionSave {
		t.Fatalf("expected save action, got %#v", action)
	}
	if action.Draft.ProviderID == "" || action.Draft.BaseURL == "" {
		t.Fatalf("expected populated draft, got %#v", action.Draft)
	}
}

func TestConnectDialogCanFilterProviderList(t *testing.T) {
	dialog := NewConnectDialog(provider.Catalog(), map[string]config.Provider{})
	dialog.Update(ui.KeyMsg{Type: ui.KeyRunes, Runes: []rune("o")})
	dialog.Update(ui.KeyMsg{Type: ui.KeyRunes, Runes: []rune("l")})
	dialog.Update(ui.KeyMsg{Type: ui.KeyRunes, Runes: []rune("l")})

	if len(dialog.view) == 0 || dialog.view[0].ID != "ollama" {
		t.Fatalf("expected ollama filtered to top, got %#v", dialog.view)
	}
}

func TestConnectDialogProviderListRendersSingleLineRows(t *testing.T) {
	dialog := NewConnectDialog(provider.Catalog(), map[string]config.Provider{
		"openai": {},
	})

	got := renderConnectDialog(dialog, 88, theme.Resolve("tokyonight").Palette)
	if !strings.Contains(got, "OpenAI") || !strings.Contains(got, "configured") {
		t.Fatalf("expected compact provider row, got %q", got)
	}
	if strings.Contains(got, "OpenAI\n") {
		t.Fatalf("expected description to stay on the same row, got %q", got)
	}
}

func TestConnectDialogTestActionEmitsDraft(t *testing.T) {
	dialog := NewConnectDialog(provider.Catalog(), map[string]config.Provider{})
	dialog.selectProvider(provider.Catalog()[0])
	dialog.advanceFocus(1)
	dialog.moveButtons(-1)
	action := dialog.Update(ui.KeyMsg{Type: ui.KeyEnter})
	if action.Kind != ProviderConnectActionTest {
		t.Fatalf("expected test action, got %#v", action)
	}
}

func TestConnectDialogAltHotkeysTriggerActions(t *testing.T) {
	dialog := NewConnectDialog(provider.Catalog(), map[string]config.Provider{})
	dialog.selectProvider(provider.Catalog()[0])

	if action := dialog.Update(ui.KeyMsg{Type: ui.KeyRunes, Alt: true, Runes: []rune("t")}); action.Kind != ProviderConnectActionTest {
		t.Fatalf("expected alt+t to trigger test, got %#v", action)
	}
	if action := dialog.Update(ui.KeyMsg{Type: ui.KeyRunes, Alt: true, Runes: []rune("s")}); action.Kind != ProviderConnectActionSave {
		t.Fatalf("expected alt+s to trigger save, got %#v", action)
	}
}

func TestConnectDialogCyclesDiscoveredModels(t *testing.T) {
	dialog := NewConnectDialog(provider.Catalog(), map[string]config.Provider{})
	dialog.selectProvider(provider.Catalog()[0])
	dialog.SetModels([]string{"model-a", "model-b"})
	dialog.fieldIndex = len(dialog.formFields()) - 1
	dialog.Update(ui.KeyMsg{Type: ui.KeyEnter})

	if dialog.draft.Model != "model-b" {
		t.Fatalf("expected next discovered model, got %q", dialog.draft.Model)
	}
}

func TestConnectDialogFillsBlankModelFromFirstDiscoveredModel(t *testing.T) {
	dialog := NewConnectDialog(provider.Catalog(), map[string]config.Provider{})
	dialog.selectProvider(provider.Catalog()[0])
	dialog.draft.Model = ""

	dialog.SetModels([]string{"model-a", "model-b"})

	if dialog.draft.Model != "model-a" {
		t.Fatalf("expected blank model to adopt first discovered model, got %q", dialog.draft.Model)
	}
}

func TestConnectDialogPreservesExistingModelWhenModelsDiscovered(t *testing.T) {
	dialog := NewConnectDialog(provider.Catalog(), map[string]config.Provider{})
	dialog.selectProvider(provider.Catalog()[0])
	dialog.draft.Model = "custom-model"

	dialog.SetModels([]string{"model-a", "model-b"})

	if dialog.draft.Model != "custom-model" {
		t.Fatalf("expected existing model to be preserved, got %q", dialog.draft.Model)
	}
}

func TestConnectDialogViewShowsSuccessStatus(t *testing.T) {
	dialog := NewConnectDialog(provider.Catalog(), map[string]config.Provider{})
	dialog.selectProvider(provider.Catalog()[0])
	dialog.SetStatusSuccess("Connected: discovered 2 models")

	got := renderConnectDialog(dialog, 90, theme.Resolve("tokyonight").Palette)
	if !strings.Contains(got, "OK") || !strings.Contains(got, "Connected: discovered 2 models") {
		t.Fatalf("expected success status in view, got %q", got)
	}
}

func TestConnectDialogViewShowsErrorStatus(t *testing.T) {
	dialog := NewConnectDialog(provider.Catalog(), map[string]config.Provider{})
	dialog.selectProvider(provider.Catalog()[0])
	dialog.SetStatusError("Connection test failed: boom")

	got := renderConnectDialog(dialog, 90, theme.Resolve("tokyonight").Palette)
	if !strings.Contains(got, "ERROR") || !strings.Contains(got, "Connection test failed: boom") {
		t.Fatalf("expected error status in view, got %q", got)
	}
}

func TestConnectDialogViewShowsEditorCursorAndTail(t *testing.T) {
	dialog := NewConnectDialog(provider.Catalog(), map[string]config.Provider{})
	dialog.selectProvider(provider.Catalog()[0])
	dialog.draft.BaseURL = "https://example.com/very/long/path/that/should/show/the/end"
	dialog.resetCursors()
	dialog.fieldIndex = 1

	got := renderConnectDialog(dialog, 90, theme.Resolve("tokyonight").Palette)
	if !strings.Contains(got, "█") {
		t.Fatalf("expected editor cursor in view, got %q", got)
	}
	if !strings.Contains(got, "┌") || !strings.Contains(got, "└") {
		t.Fatalf("expected composed input box in view, got %q", got)
	}
	if !strings.Contains(got, "https://example.com/very/long/path/that/should/show/the/end█") {
		t.Fatalf("expected editor to show current value inside the input box, got %q", got)
	}
}

func TestConnectDialogFormSeparatesLabelsDescriptionsAndInputs(t *testing.T) {
	dialog := NewConnectDialog(provider.Catalog(), map[string]config.Provider{})
	dialog.selectProvider(provider.Catalog()[0])

	got := renderConnectDialog(dialog, 90, theme.Resolve("tokyonight").Palette)
	if !strings.Contains(got, "Configuration") || !strings.Contains(got, "Base URL") {
		t.Fatalf("expected composed configuration section, got %q", got)
	}
	if !strings.Contains(got, "OpenAI-compatible API endpoint") {
		t.Fatalf("expected field description line, got %q", got)
	}
	if !strings.Contains(got, "│ │ https://api.openai.com/v1") {
		t.Fatalf("expected bordered value row separate from label/description, got %q", got)
	}
}

func TestConnectDialogMovesCursorAndInsertsAtCursor(t *testing.T) {
	dialog := NewConnectDialog(provider.Catalog(), map[string]config.Provider{})
	dialog.selectProvider(provider.Catalog()[0])
	dialog.fieldIndex = 1
	dialog.draft.BaseURL = "abcd"
	dialog.resetCursors()
	dialog.moveCursorTo(2)
	dialog.Update(ui.KeyMsg{Type: ui.KeyRunes, Runes: []rune("X")})
	if dialog.draft.BaseURL != "abXcd" {
		t.Fatalf("expected insertion at cursor, got %q", dialog.draft.BaseURL)
	}
	dialog.Update(ui.KeyMsg{Type: ui.KeyLeft})
	dialog.Update(ui.KeyMsg{Type: ui.KeyBackspace})
	if dialog.draft.BaseURL != "aXcd" {
		t.Fatalf("expected backspace before cursor, got %q", dialog.draft.BaseURL)
	}
}
