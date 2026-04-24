package ui

import (
	"strings"
	"testing"
)

func TestRowRenderPlacesChildrenHorizontally(t *testing.T) {
	got := RenderElement(nil, Row{
		Children: []Child{
			Fixed(Static{Content: "A"}),
			Fixed(Static{Content: "B"}),
		},
		Spacing: 1,
	}, 4, 1)

	if got != "A B " {
		t.Fatalf("unexpected row render: %q", got)
	}
}

func TestColumnRenderPlacesChildrenVertically(t *testing.T) {
	got := RenderElement(nil, Column{
		Children: []Child{
			Fixed(Static{Content: "A"}),
			Fixed(Static{Content: "B"}),
		},
		Spacing: 1,
	}, 1, 3)

	if got != "A\n \nB" {
		t.Fatalf("unexpected column render: %q", got)
	}
}

func TestAlignCentersChildWithinBounds(t *testing.T) {
	got := RenderElement(nil, Align{
		Horizontal: AlignCenter,
		Vertical:   AlignCenter,
		Child:      Static{Content: "X"},
	}, 3, 3)

	lines := strings.Split(got, "\n")
	if len(lines) != 3 || lines[1] != " X " {
		t.Fatalf("unexpected centered render: %q", got)
	}
}

func TestInsetAddsPadding(t *testing.T) {
	got := RenderElement(nil, Inset{
		Padding: UniformInsets(1),
		Child:   Static{Content: "X"},
	}, 3, 3)

	lines := strings.Split(got, "\n")
	if len(lines) != 3 || lines[1] != " X " {
		t.Fatalf("unexpected inset render: %q", got)
	}
}

func TestStackOverlaysLaterChildren(t *testing.T) {
	got := RenderElement(nil, Stack{
		Children: []Element{
			Static{Content: "AAAA"},
			Static{Content: " BB "},
		},
	}, 4, 1)

	if got != " BB " {
		t.Fatalf("unexpected stack render: %q", got)
	}
}

func TestConstrainedClampsChildSize(t *testing.T) {
	got := RenderElement(nil, Constrained{
		Constraints: Constraints{MaxW: 2, MaxH: 1},
		Child:       Static{Content: "WIDE"},
	}, 4, 1)

	if got != "WI  " {
		t.Fatalf("unexpected constrained render: %q", got)
	}
}

func TestNormalizeConvertsPlainStringSurfaceToCells(t *testing.T) {
	got := SurfaceFromString("abc\ndef").normalize(4, 2)

	if got.SurfaceWidth() != 4 || got.SurfaceHeight() != 2 {
		t.Fatalf("expected normalized surface size 4x2, got %dx%d", got.SurfaceWidth(), got.SurfaceHeight())
	}
	if text := got.SurfaceCellText(0, 0); text != "a" {
		t.Fatalf("expected first cell text to survive normalization, got %q", text)
	}
	if text := got.SurfaceCellText(2, 1); text != "f" {
		t.Fatalf("expected second row text to survive normalization, got %q", text)
	}
}

func TestPlaceAtBlitsPlainStringChildOntoCellSurface(t *testing.T) {
	base := BlankSurface(6, 2)
	got := base.placeAt(1, 0, SurfaceFromString("abc"))

	if text := got.SurfaceCellText(1, 0); text != "a" {
		t.Fatalf("expected plain string child to blit into cell surface, got %q", text)
	}
	if text := got.SurfaceCellText(3, 0); text != "c" {
		t.Fatalf("expected plain string child tail to blit into cell surface, got %q", text)
	}
}

func TestPlaceAtInheritsParentBackgroundForSparseChild(t *testing.T) {
	base := BlankSurface(4, 1)
	bg := cellColor("#112233")
	for x := 0; x < 4; x++ {
		base.setCell(x, 0, Cell{Text: " ", Width: 1, Style: CellStyle{BG: bg}})
	}

	child := TransparentSurface(4, 1)
	child.WriteText(1, 0, "x", CellStyle{FG: cellColor("#ffffff")})
	got := base.placeAt(0, 0, child)

	if text := got.SurfaceCellText(1, 0); text != "x" {
		t.Fatalf("expected child text to render, got %q", text)
	}
	r, g, b, ok := got.SurfaceCellBG(1, 0)
	if !ok || r != 0x11 || g != 0x22 || b != 0x33 {
		t.Fatalf("expected parent background to shine through, got %v %d %d %d", ok, r, g, b)
	}
}
