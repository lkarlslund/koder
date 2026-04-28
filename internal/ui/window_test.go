package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/lkarlslund/koder/internal/theme"
)

func TestBorderWrapsChildWithoutNestedFrameArtifacts(t *testing.T) {
	got := RenderElement(nil, Border{
		Child:        Static{Content: "Body"},
		Padding:      Insets{Left: 1, Right: 1},
		BorderLeft:   true,
		BorderRight:  true,
		BorderTop:    true,
		BorderBottom: true,
	}, 8, 3)

	lines := strings.Split(ansi.Strip(got), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d in %q", len(lines), got)
	}
	if !strings.Contains(lines[0], "┌") || !strings.Contains(lines[2], "└") {
		t.Fatalf("expected single composed border, got %q", got)
	}
	if strings.Count(got, "┌") != 1 || strings.Count(got, "└") != 1 {
		t.Fatalf("expected one outer border, got %q", got)
	}
}

func TestWindowFrameRendersTitleAndCloseInBorder(t *testing.T) {
	palette := theme.Default().Palette
	got := RenderElement(&Context{Palette: palette}, WindowFrame{
		Title:     "Connect Provider",
		Content:   Static{Content: "Body"},
		Width:     32,
		ShowClose: true,
	}, 32, 4)

	top := strings.Split(ansi.Strip(got), "\n")[0]
	if !strings.Contains(top, "[Connect Provider]") {
		t.Fatalf("expected bracketed title in window border, got %q", top)
	}
	if !strings.Contains(top, "[X]") {
		t.Fatalf("expected close indicator in window border, got %q", top)
	}
}

func TestWindowFrameContentInheritsFrameBackground(t *testing.T) {
	palette := theme.Default().Palette
	surface := WindowFrame{
		Title:   "Help",
		Content: TextPane{Content: "Hotkeys"},
		Width:   24,
	}.Render(&Context{Palette: palette}, Rect{W: 24, H: 5})

	x := strings.Index(surface.Lines()[2], "Hotkeys")
	if x < 0 {
		t.Fatalf("expected help text in rendered window, got %q", strings.Join(surface.Lines(), "\n"))
	}
	r, g, b, ok := surface.SurfaceCellBG(x, 2)
	if !ok {
		t.Fatal("expected text cell to inherit window background")
	}
	want := ParseCellColor(string(palette.SidebarBackground))
	if !want.Valid() || r != want.R() || g != want.G() || b != want.B() {
		t.Fatalf("expected inherited background %v, got %d %d %d", want, r, g, b)
	}
}

func TestWindowFrameRenderMatchesInnerBorder(t *testing.T) {
	palette := theme.Default().Palette
	ctx := &Context{Palette: palette, Runtime: &Runtime{}}
	element := WindowFrame{
		Title:     "Help",
		Content:   HitBox{ID: "body", Child: TextPane{Content: "Hotkeys"}},
		Width:     24,
		ShowClose: true,
	}
	got := element.Render(ctx, Rect{W: 24, H: 5})
	want := element.border(ctx).Render(&Context{Palette: palette}, Rect{W: 24, H: 5})
	if got.Size() != want.Size() {
		t.Fatalf("size mismatch: got %#v want %#v", got.Size(), want.Size())
	}
	if gotText, wantText := got.Lines(), want.Lines(); len(gotText) != len(wantText) || strings.Join(gotText, "\n") != strings.Join(wantText, "\n") {
		t.Fatalf("line mismatch:\ngot=%q\nwant=%q", gotText, wantText)
	}
	if len(got.Controls()) == 0 {
		t.Fatal("expected window frame render to preserve controls")
	}
}
