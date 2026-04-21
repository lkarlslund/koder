package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
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

func TestButtonRowRightAlignsWithinWidth(t *testing.T) {
	palette := theme.Default().Palette
	row := ButtonRow{
		Buttons: []Button{
			{Label: "OK", Primary: true},
			{Label: "Cancel"},
		},
		Width: 32,
		Align: HorizontalAlignRight,
	}

	got := ansi.Strip(row.View(palette))
	raw := ansi.Strip(ButtonRow{Buttons: row.Buttons}.View(palette))
	if !strings.HasSuffix(got, raw) {
		t.Fatalf("expected right-aligned row to end with raw button line, got %q", got)
	}
	if len(got) == len(raw) || got[0] != ' ' {
		t.Fatalf("expected left padding from right alignment, got %q", got)
	}
}
