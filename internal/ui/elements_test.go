package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

type fillBox struct {
	BoxProps
	mark string
}

func (f fillBox) Box() BoxProps {
	return f.BoxProps
}

func (f fillBox) Measure(_ *Context, constraints Constraints) Size {
	return constraints.Clamp(Size{W: 1, H: 1})
}

func (f fillBox) Render(_ *Context, bounds Rect) Surface {
	if f.mark == "" {
		f.mark = "x"
	}
	surface := BlankSurface(max(1, bounds.W), max(1, bounds.H))
	for y := 0; y < max(1, bounds.H); y++ {
		surface.WriteText(0, y, strings.Repeat(f.mark, max(1, bounds.W)), CellStyle{})
	}
	return surface.normalize(bounds.W, bounds.H)
}

func TestFlexBoxRenderPlacesChildrenHorizontally(t *testing.T) {
	got := RenderElement(nil, FlexBox{
		Direction: DirectionHorizontal,
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

func TestFlexBoxRenderPlacesChildrenVertically(t *testing.T) {
	got := RenderElement(nil, FlexBox{
		Direction: DirectionVertical,
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

func TestFlexBoxVerticalFlexChildFillsAllocatedHeight(t *testing.T) {
	got := RenderElement(nil, FlexBox{
		Direction: DirectionVertical,
		Children: []Child{
			Flex(fillBox{mark: "A"}, 1),
			Fixed(Static{Content: "B"}),
		},
		Spacing: 1,
	}, 4, 5)

	if got != "AAAA\nAAAA\nAAAA\n    \nB   " {
		t.Fatalf("expected flex child to fill remaining height, got %q", got)
	}
}

func TestFlexBoxHorizontalFlexChildFillsAllocatedWidth(t *testing.T) {
	got := RenderElement(nil, FlexBox{
		Direction: DirectionHorizontal,
		Children: []Child{
			Flex(fillBox{mark: "A"}, 1),
			Fixed(Static{Content: "B"}),
		},
		Spacing: 1,
	}, 5, 2)

	if got != "AAA B\nAAA  " {
		t.Fatalf("expected flex child to fill remaining width, got %q", got)
	}
}

func TestFlexBoxVerticalFlexChildrenShareHeightEquallyByDefault(t *testing.T) {
	got := RenderElement(nil, FlexBox{
		Direction: DirectionVertical,
		Children: []Child{
			Flex(fillBox{mark: "A"}, 1),
			Flex(fillBox{mark: "B"}, 1),
		},
	}, 2, 4)

	if got != "AA\nAA\nBB\nBB" {
		t.Fatalf("expected equal-height sharing for equal flex weights, got %q", got)
	}
}

func TestFlexBoxHorizontalFlexChildrenShareWidthEquallyByDefault(t *testing.T) {
	got := RenderElement(nil, FlexBox{
		Direction: DirectionHorizontal,
		Children: []Child{
			Flex(fillBox{mark: "A"}, 1),
			Flex(fillBox{mark: "B"}, 1),
		},
	}, 4, 1)

	if got != "AABB" {
		t.Fatalf("expected equal-width sharing for equal flex weights, got %q", got)
	}
}

func TestFlexBoxHorizontalFlexChildrenRespectShareWeights(t *testing.T) {
	got := RenderElement(nil, FlexBox{
		Direction: DirectionHorizontal,
		Children: []Child{
			Flex(fillBox{mark: "A"}, 1),
			Flex(fillBox{mark: "B"}, 2),
		},
	}, 6, 1)

	if got != "AABBBB" {
		t.Fatalf("expected weighted width sharing, got %q", got)
	}
}

func TestFlexBoxVerticalAlignmentCanOptOutOfFill(t *testing.T) {
	got := RenderElement(nil, FlexBox{
		Direction: DirectionVertical,
		Children: []Child{
			Flex(VisibleElement{
				BoxProps: BoxProps{VAlign: AlignCenter},
				Child:    Static{Content: "A"},
			}, 1),
		},
	}, 3, 3)

	if got != "   \nA  \n   " {
		t.Fatalf("expected aligned child to render smaller than slot, got %q", got)
	}
}

func TestFlexBoxHorizontalMaxWidthCanOptOutOfFill(t *testing.T) {
	got := RenderElement(nil, FlexBox{
		Direction: DirectionHorizontal,
		Children: []Child{
			Flex(fillBox{
				BoxProps: BoxProps{MaxW: 2},
				mark:     "A",
			}, 1),
		},
	}, 4, 1)

	if got != "AA  " {
		t.Fatalf("expected max width to cap child render width, got %q", got)
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

func TestStaticRenderMatchesRenderTo(t *testing.T) {
	assertRenderMatchesRenderTo(t, nil, Static{Content: "A\nB"}, Rect{W: 2, H: 2})
}

func TestLabelRenderMatchesRenderTo(t *testing.T) {
	element := Label{Text: "hello", Style: lipgloss.NewStyle().Bold(true)}
	assertRenderMatchesRenderTo(t, &Context{Runtime: &Runtime{}}, element, Rect{W: 8, H: 1})
}

func TestParagraphRenderMatchesRenderTo(t *testing.T) {
	element := Paragraph{Text: "wrapped paragraph text"}
	assertRenderMatchesRenderTo(t, nil, element, Rect{W: 8, H: 4})
}

func TestHitBoxRenderMatchesRenderTo(t *testing.T) {
	ctx := &Context{Runtime: &Runtime{}}
	element := HitBox{ID: "hit", Child: Static{Content: "X"}}
	assertRenderMatchesRenderTo(t, ctx, element, Rect{W: 2, H: 1})
}

func TestSimpleWidgetRenderToAvoidsOwnerSurfaceAllocation(t *testing.T) {
	element := Paragraph{Text: "alpha beta gamma"}

	ResetSurfaceAllocationStats()
	_ = element.Render(nil, Rect{W: 8, H: 3})
	renderStats := SurfaceAllocationStatsSnapshot()

	dst := TransparentSurface(8, 3)
	ResetSurfaceAllocationStats()
	element.RenderTo(nil, Rect{W: 8, H: 3}, &dst)
	renderToStats := SurfaceAllocationStatsSnapshot()

	if renderStats.Transparent <= renderToStats.Transparent {
		t.Fatalf("expected Render to allocate at least one additional transparent owner surface, got render=%#v renderTo=%#v", renderStats, renderToStats)
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

func TestNormalizeReturnsSameCellSurfaceWhenAlreadySized(t *testing.T) {
	surface := BlankSurface(4, 2)
	surface.WriteText(0, 0, "test", CellStyle{})
	got := surface.normalize(4, 2)

	if len(surface.cells) == 0 || len(got.cells) == 0 {
		t.Fatal("expected cell-backed surfaces")
	}
	if &got.cells[0] != &surface.cells[0] {
		t.Fatal("expected already-sized normalize to reuse the existing cell buffer")
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
		base.setCell(x, 0, blankCell(CellStyle{BG: bg}))
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
