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
		SelectionBackground:    "#040506",
		SelectionForeground:    "#070809",
		UserAccentBar:          "#0d0e0f",
		UserTextBackground:     "#040506",
		UserTextForeground:     "#070809",
	}

	base := SelectableRow{Primary: "write", Secondary: "Allow writes after approval", Tertiary: "active", Width: 48}
	unselected := base.render(palette).String()
	base.Selected = true
	selected := base.render(palette).String()
	base.Focused = true
	focused := base.render(palette).String()

	if selected == unselected {
		t.Fatal("expected selected row styling to differ from unselected row")
	}
	if !strings.Contains(selected, "48;2;4;5;6") {
		t.Fatalf("expected selected row to use the selected row background, got %q", selected)
	}
	if !strings.Contains(selected, "38;2;7;8;9") {
		t.Fatalf("expected selected row to use the selected row foreground, got %q", selected)
	}
	if strings.Contains(selected, "38;2;13;14;15") {
		t.Fatalf("expected selected row tertiary text to use the shared selection foreground, got %q", selected)
	}
	if focused == selected {
		t.Fatal("expected focused row styling to differ from merely selected row")
	}
}

func TestFocusedButtonUsesFocusColors(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)

	palette := theme.Palette{
		SelectionBackground: "#101112",
		SelectionForeground: "#f1f2f3",
		UserTextBackground:  "#040506",
		UserTextForeground:  "#070809",
		UserAccentBar:       "#0d0e0f",
	}

	view := Button{Label: "Approve", Focused: true}.render(palette)
	if !strings.Contains(view, "48;2;44;44;46") {
		t.Fatalf("expected focused button to use focus background, got %q", view)
	}
	if !strings.Contains(view, "38;2;241;242;243") {
		t.Fatalf("expected focused button to use focus foreground, got %q", view)
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

	got := ansi.Strip(row.render(palette).String())
	raw := ansi.Strip(ButtonRow{Buttons: row.Buttons}.render(palette).String())
	if !strings.HasSuffix(got, raw) {
		t.Fatalf("expected right-aligned row to end with raw button line, got %q", got)
	}
	if len(got) == len(raw) || got[0] != ' ' {
		t.Fatalf("expected left padding from right alignment, got %q", got)
	}
}
