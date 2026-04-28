package dialogs

import (
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/theme"
	"github.com/lkarlslund/koder/internal/ui"
)

func renderToolsDialog(dialog ToolsDialog, width int, palette theme.Palette) string {
	size := dialog.Measure(&ui.Context{Palette: palette}, ui.Constraints{MaxW: width})
	return strings.Join(dialog.Surface(&ui.Context{Palette: palette}, ui.Rect{W: maxInt(width, size.W), H: size.H}).Lines(), "\n")
}
func TestToolsDialogTogglesAndAppliesStates(t *testing.T) {
	dialog := NewToolsDialog([]ToolToggleItem{
		{Tool: domain.ToolKindRead, Label: "Read", Enabled: true},
		{Tool: domain.ToolKindBash, Label: "Bash", Enabled: true},
	})

	dialog.Update(ui.KeyMsg{Type: ui.KeyDown})
	dialog.Update(ui.KeyMsg{Type: ui.KeySpace})
	action := dialog.Update(ui.KeyMsg{Type: ui.KeyTab})
	if action.Kind != ToolsDialogActionNone {
		t.Fatalf("unexpected action on tab: %#v", action)
	}
	action = dialog.Update(ui.KeyMsg{Type: ui.KeyEnter})
	if action.Kind != ToolsDialogActionApply {
		t.Fatalf("expected apply action, got %#v", action)
	}
	if !action.States[domain.ToolKindRead] || action.States[domain.ToolKindBash] {
		t.Fatalf("unexpected tool states: %#v", action.States)
	}
}

func TestToolsDialogRenderShowsCheckboxes(t *testing.T) {
	dialog := NewToolsDialog([]ToolToggleItem{
		{Tool: domain.ToolKindRead, Label: "Read", Description: "Read files", Enabled: true},
		{Tool: domain.ToolKindBash, Label: "Bash", Description: "Run shell commands", Enabled: false},
	})

	view := renderToolsDialog(dialog, 88, theme.Default().Palette)
	for _, needle := range []string{"Tools", "Read", "Bash", "☑ Enabled", "☐ Disabled", "OK", "Cancel"} {
		if !strings.Contains(view, needle) {
			t.Fatalf("expected %q in tools dialog, got %q", needle, view)
		}
	}
}
