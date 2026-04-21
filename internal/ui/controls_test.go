package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/lkarlslund/koder/internal/theme"
)

func TestRenderSelectableRowSelectedUsesDistinctHighlightColors(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)

	palette := theme.Palette{
		ActivityText:           "#0a0b0c",
		AssistantTimestampText: "#010203",
		UserAccentBar:          "#0d0e0f",
		UserTextBackground:     "#040506",
		UserTextForeground:     "#070809",
	}

	unselected := RenderSelectableRow("write", "Allow writes after approval", "active", 48, palette, false)
	selected := RenderSelectableRow("write", "Allow writes after approval", "active", 48, palette, true)

	if selected == unselected {
		t.Fatal("expected selected row styling to differ from unselected row")
	}
	if !strings.Contains(selected, "48;2;4;5;6") {
		t.Fatalf("expected selected row to use the selected row background, got %q", selected)
	}
	if !strings.Contains(selected, "38;2;7;8;9") {
		t.Fatalf("expected selected row to use the selected row foreground, got %q", selected)
	}
	if !strings.Contains(selected, "38;2;13;14;15") {
		t.Fatalf("expected selected row tertiary text to use accent color, got %q", selected)
	}
}
