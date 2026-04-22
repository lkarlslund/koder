package dialogs

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
	if !strings.Contains(got, "Select Model") || !strings.Contains(got, "gpt-5.4") || !strings.Contains(got, "openai") || !strings.Contains(got, "image") {
		t.Fatalf("unexpected render: %q", got)
	}
}

func TestModelDialogRenderUsesSingleLineTableRows(t *testing.T) {
	dialog := NewModelDialog("openai", []domain.Model{
		{ID: "gpt-5.4", OwnedBy: "openai", SupportsImages: true, CapabilitiesKnown: true},
		{ID: "gpt-4.1-mini", OwnedBy: "openai"},
	}, "gpt-5.4")
	got := dialog.View(84, theme.Resolve("tokyonight").Palette)
	lines := strings.Split(got, "\n")
	var modelLine string
	for _, line := range lines {
		if strings.Contains(line, "gpt-5.4") {
			modelLine = line
			break
		}
	}
	if modelLine == "" {
		t.Fatalf("expected model row in render, got %q", got)
	}
	if strings.Contains(modelLine, "Provider:") {
		t.Fatalf("expected compact single-line row instead of detail block, got %q", modelLine)
	}
	if !strings.Contains(modelLine, "openai") || !strings.Contains(modelLine, "image") {
		t.Fatalf("expected owner and capability columns in row, got %q", modelLine)
	}
}

func TestModelDialogTabThenEnterCancels(t *testing.T) {
	dialog := NewModelDialog("openai", []domain.Model{{ID: "gpt-5.4"}}, "gpt-5.4")
	dialog.Update(tea.KeyMsg{Type: tea.KeyTab})
	dialog.Update(tea.KeyMsg{Type: tea.KeyRight})
	action := dialog.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if action.Kind != ModelDialogActionCancel {
		t.Fatalf("expected button focus cancel, got %#v", action)
	}
}
