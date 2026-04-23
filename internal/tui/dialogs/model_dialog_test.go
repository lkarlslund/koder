package dialogs

import (
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/theme"
	"github.com/lkarlslund/koder/internal/ui"
	"github.com/lkarlslund/koder/internal/ui/uitest"
)

func renderModelDialog(dialog ModelDialog, width int, palette theme.Palette) string {
	return uitest.RenderElementText(&ui.Context{Palette: palette}, dialog, width, 0)
}

func TestModelDialogSelectsModel(t *testing.T) {
	dialog := NewModelDialog("openai", []domain.Model{{ID: "gpt-5.4"}}, "gpt-5.4")
	action := dialog.Update(ui.KeyMsg{Type: ui.KeyEnter})
	if action.Kind != ModelDialogActionSelect || action.ModelID != "gpt-5.4" {
		t.Fatalf("unexpected action: %#v", action)
	}
}

func TestModelDialogFiltersModels(t *testing.T) {
	dialog := NewModelDialog("openai", []domain.Model{{ID: "gpt-5.4"}, {ID: "gpt-4.1-mini"}}, "")
	dialog.Update(ui.KeyMsg{Type: ui.KeyRunes, Runes: []rune("m")})
	dialog.Update(ui.KeyMsg{Type: ui.KeyRunes, Runes: []rune("i")})
	dialog.Update(ui.KeyMsg{Type: ui.KeyRunes, Runes: []rune("n")})

	if len(dialog.view) == 0 || dialog.view[0].ID != "gpt-4.1-mini" {
		t.Fatalf("unexpected filtered models: %#v", dialog.view)
	}
}

func TestModelDialogRenderShowsProvider(t *testing.T) {
	dialog := NewModelDialog("openai", []domain.Model{{ID: "gpt-5.4", OwnedBy: "openai", SupportsImages: true, CapabilitiesKnown: true}}, "gpt-5.4")
	got := renderModelDialog(dialog, 84, theme.Resolve("tokyonight").Palette)
	if !strings.Contains(got, "Select Model") || !strings.Contains(got, "gpt-5.4") || !strings.Contains(got, "openai") || !strings.Contains(got, "image") {
		t.Fatalf("unexpected render: %q", got)
	}
}

func TestModelDialogRenderUsesSingleLineTableRows(t *testing.T) {
	dialog := NewModelDialog("openai", []domain.Model{
		{ID: "gpt-5.4", OwnedBy: "openai", SupportsImages: true, CapabilitiesKnown: true},
		{ID: "gpt-4.1-mini", OwnedBy: "openai"},
	}, "gpt-5.4")
	got := renderModelDialog(dialog, 84, theme.Resolve("tokyonight").Palette)
	if !strings.Contains(got, "Model") || !strings.Contains(got, "Owner") {
		t.Fatalf("expected table header row, got %q", got)
	}
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

func TestModelDialogRenderFallsBackToProviderWhenOwnerBlank(t *testing.T) {
	dialog := NewModelDialog("ollama", []domain.Model{
		{ID: "qwen2.5-coder:32b", OwnedBy: "", SupportsImages: true, CapabilitiesKnown: true},
	}, "qwen2.5-coder:32b")
	got := renderModelDialog(dialog, 84, theme.Resolve("tokyonight").Palette)
	if !strings.Contains(got, "ollama") {
		t.Fatalf("expected provider fallback in owner column, got %q", got)
	}
}

func TestModelDialogRenderPreservesLongModelNames(t *testing.T) {
	dialog := NewModelDialog("openrouter", []domain.Model{
		{ID: "anthropic/claude-sonnet-4-20250514", OwnedBy: "openrouter", SupportsImages: true, CapabilitiesKnown: true},
	}, "anthropic/claude-sonnet-4-20250514")
	got := renderModelDialog(dialog, 96, theme.Resolve("tokyonight").Palette)
	if !strings.Contains(got, "anthropic/claude-sonnet-4-20250514") {
		t.Fatalf("expected long model id to stay visible at reasonable widths, got %q", got)
	}
}

func TestModelDialogTabThenEnterCancels(t *testing.T) {
	dialog := NewModelDialog("openai", []domain.Model{{ID: "gpt-5.4"}}, "gpt-5.4")
	dialog.Update(ui.KeyMsg{Type: ui.KeyTab})
	dialog.Update(ui.KeyMsg{Type: ui.KeyRight})
	action := dialog.Update(ui.KeyMsg{Type: ui.KeyEnter})
	if action.Kind != ModelDialogActionCancel {
		t.Fatalf("expected button focus cancel, got %#v", action)
	}
}

func TestModelDialogKeepsFullWindowNearListEnd(t *testing.T) {
	models := make([]domain.Model, 12)
	for i := range models {
		models[i] = domain.Model{ID: "model-" + strconv.Itoa(i)}
	}
	dialog := NewModelDialog("openai", models, "")
	for range 11 {
		dialog.Update(ui.KeyMsg{Type: ui.KeyDown})
	}

	got := renderModelDialog(dialog, 84, theme.Resolve("tokyonight").Palette)
	lines := strings.Split(got, "\n")
	hasModelLine := func(id string) bool {
		for _, line := range lines {
			if strings.Contains(line, id+" ") {
				return true
			}
		}
		return false
	}
	for i := 2; i <= 11; i++ {
		id := "model-" + strconv.Itoa(i)
		if !hasModelLine(id) {
			t.Fatalf("expected %s in full tail window, got %q", id, got)
		}
	}
	if hasModelLine("model-1") {
		t.Fatalf("expected only the last ten rows near list end, got %q", got)
	}
}

func TestModelDialogUsesTighterWidthBudget(t *testing.T) {
	dialog := NewModelDialog("ollama", []domain.Model{{ID: "qwen2.5-coder:32b", OwnedBy: "", SupportsImages: true, CapabilitiesKnown: true}}, "")
	got := renderModelDialog(dialog, 120, theme.Resolve("tokyonight").Palette)
	for _, line := range strings.Split(got, "\n") {
		if ansi.StringWidth(line) > 76 {
			t.Fatalf("expected model dialog to stay compact, got width %d in %q", ansi.StringWidth(line), line)
		}
	}
}
