package ui

import (
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/theme"
)

func TestRenderSelectableRowSelectedUsesDistinctHighlightColors(t *testing.T) {
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
	unselected := base.render(palette)
	base.Selected = true
	selected := base.render(palette)
	base.Focused = true
	focused := base.render(palette)

	if SurfaceText(selected) == SurfaceText(unselected) && selected.cellAt(0, 0).Style.equal(unselected.cellAt(0, 0).Style) {
		t.Fatal("expected selected row styling to differ from unselected row")
	}
	r, g, b, ok := selected.SurfaceCellBG(0, 0)
	if !ok || r != 0x04 || g != 0x05 || b != 0x06 {
		t.Fatalf("expected selected row to use the selected row background, got (%d,%d,%d,%v)", r, g, b, ok)
	}
	r, g, b, ok = selected.SurfaceCellFG(0, 0)
	if !ok || r != 0x07 || g != 0x08 || b != 0x09 {
		t.Fatalf("expected selected row to use the selected row foreground, got (%d,%d,%d,%v)", r, g, b, ok)
	}
	selectedText := SurfaceText(selected)
	activeOffset := strings.Index(selectedText, "active")
	if activeOffset == -1 {
		t.Fatalf("expected row to contain tertiary text, got %q", selectedText)
	}
	r, g, b, ok = selected.SurfaceCellFG(activeOffset, 0)
	if !ok || r != 0x07 || g != 0x08 || b != 0x09 {
		t.Fatalf("expected selected row tertiary text to use the shared selection foreground, got (%d,%d,%d,%v)", r, g, b, ok)
	}
	if focused.cellAt(0, 0).Style.equal(selected.cellAt(0, 0).Style) {
		t.Fatal("expected focused row styling to differ from merely selected row")
	}
}

func TestFocusedButtonUsesFocusColors(t *testing.T) {
	palette := theme.Palette{
		SelectionBackground: "#101112",
		SelectionForeground: "#f1f2f3",
		UserTextBackground:  "#040506",
		UserTextForeground:  "#070809",
		UserAccentBar:       "#0d0e0f",
	}

	view := Button{Label: "Approve", Focused: true}.renderSurface(palette)
	r, g, b, ok := view.SurfaceCellBG(0, 0)
	if !ok || r != 0x2c || g != 0x2d || b != 0x2e {
		t.Fatalf("expected focused button to use focus background, got (%d,%d,%d,%v)", r, g, b, ok)
	}
	r, g, b, ok = view.SurfaceCellFG(0, 0)
	if !ok || r != 0xf1 || g != 0xf2 || b != 0xf3 {
		t.Fatalf("expected focused button to use focus foreground, got (%d,%d,%d,%v)", r, g, b, ok)
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

	got := SurfaceText(row.render(palette))
	raw := SurfaceText(ButtonRow{Buttons: row.Buttons}.render(palette))
	if !strings.HasSuffix(got, raw) {
		t.Fatalf("expected right-aligned row to end with raw button line, got %q", got)
	}
	if len(got) == len(raw) || got[0] != ' ' {
		t.Fatalf("expected left padding from right alignment, got %q", got)
	}
}

func TestButtonRowHotkeyTriggersOnClick(t *testing.T) {
	var clicked bool
	row := ButtonRow{
		Buttons: []Button{
			{Label: "Save", Hotkey: 's', OnClick: func() { clicked = true }},
		},
	}
	if !row.ActivateHotkey(KeyMsg{Type: KeyRunes, Runes: []rune("s"), Alt: true}) {
		t.Fatal("expected hotkey activation to succeed")
	}
	if !clicked {
		t.Fatal("expected on-click callback to run")
	}
}
