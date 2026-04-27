package ui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/lkarlslund/koder/internal/theme"
)

func renderViaRenderToForTest(ctx *Context, element Element, bounds Rect) Surface {
	surface := TransparentSurface(bounds.W, bounds.H)
	renderer, ok := element.(SurfaceRenderer)
	if !ok {
		panic("element does not implement SurfaceRenderer")
	}
	if ctx != nil && ctx.Runtime != nil {
		shadow := &Runtime{}
		copyCtx := *ctx
		copyCtx.Runtime = shadow
		renderer.RenderTo(&copyCtx, bounds, &surface)
		if controls := shadow.Controls(); len(controls) > 0 {
			surface.ctrls = append(surface.ctrls[:0], controls...)
			surface.RegisterControls(ctx.Runtime, bounds.X, bounds.Y)
		}
		return surface
	}
	renderer.RenderTo(ctx, bounds, &surface)
	return surface
}

func assertRenderMatchesRenderTo(t *testing.T, ctx *Context, element Element, bounds Rect) {
	t.Helper()
	gotRender := element.Render(ctx, bounds)
	gotRenderTo := renderViaRenderToForTest(ctx, element, bounds)
	if gotRender.Size() != gotRenderTo.Size() {
		t.Fatalf("render/renderTo size mismatch: %#v vs %#v", gotRender.Size(), gotRenderTo.Size())
	}
	if !reflect.DeepEqual(gotRender.Lines(), gotRenderTo.Lines()) {
		t.Fatalf("render/renderTo lines mismatch:\nrender=%q\nrenderTo=%q", gotRender.Lines(), gotRenderTo.Lines())
	}
	if !reflect.DeepEqual(gotRender.Controls(), gotRenderTo.Controls()) {
		t.Fatalf("render/renderTo controls mismatch:\nrender=%#v\nrenderTo=%#v", gotRender.Controls(), gotRenderTo.Controls())
	}
}

func TestFlexBoxRendersFixedSidebarOnRight(t *testing.T) {
	got := RenderElement(nil, FlexBox{
		Direction: DirectionHorizontal,
		Children: []Child{
			Flex(Static{Content: "MAIN"}, 1),
			{Element: Static{Content: "SIDE"}, Basis: 4},
		},
	}, 8, 1)

	if got != "MAINSIDE" {
		t.Fatalf("unexpected split render: %q", got)
	}
}

func TestTableRendersHeaderAndRows(t *testing.T) {
	palette := theme.Default().Palette
	got := RenderElement(&Context{Palette: palette}, Table{
		Width: 20,
		Columns: []TableColumn{
			{Title: "Name", Width: 10},
			{Title: "Kind", Width: 8},
		},
		ShowHeader: true,
		Rows: []TableRow{{
			Cells: []string{"README.md", "file"},
		}},
	}, 20, 2)

	lines := strings.Split(ansi.Strip(got), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected header and row, got %q", got)
	}
	if !strings.Contains(lines[0], "Name") || !strings.Contains(lines[0], "Kind") {
		t.Fatalf("expected table header, got %q", lines[0])
	}
	if !strings.Contains(lines[1], "README.md") || !strings.Contains(lines[1], "file") {
		t.Fatalf("expected table row, got %q", lines[1])
	}
}

func TestSectionRendersTitleAbovePanel(t *testing.T) {
	palette := theme.Default().Palette
	got := RenderElement(&Context{Palette: palette}, Section{
		Title: "Preview",
		Width: 18,
		Child: Static{Content: "Body"},
	}, 18, 3)

	lines := strings.Split(ansi.Strip(got), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected titled section, got %q", got)
	}
	if !strings.Contains(lines[0], "Preview") {
		t.Fatalf("expected section title, got %q", lines[0])
	}
	if !strings.Contains(got, "Body") {
		t.Fatalf("expected section body, got %q", got)
	}
}

func TestSectionRenderMatchesRenderTo(t *testing.T) {
	palette := theme.Default().Palette
	ctx := &Context{Palette: palette, Runtime: &Runtime{}}
	element := Section{
		Title: "Preview",
		Width: 24,
		Child: HitBox{ID: "body", Child: Static{Content: "Body"}},
	}
	assertRenderMatchesRenderTo(t, ctx, element, Rect{W: 24, H: 4})
}

func TestListSelectionChangedCallback(t *testing.T) {
	list := List{
		Items: []ListItem{
			{Primary: "A"},
			{Primary: "B"},
		},
	}
	var gotIndex int
	var gotItem ListItem
	list.OnSelectionChanged = func(index int, item ListItem) {
		gotIndex = index
		gotItem = item
	}
	if !list.Move(1) {
		t.Fatal("expected selection to move")
	}
	if gotIndex != 1 || gotItem.Primary != "B" {
		t.Fatalf("unexpected callback payload: index=%d item=%+v", gotIndex, gotItem)
	}
}

func TestListRenderMatchesRenderTo(t *testing.T) {
	palette := theme.Default().Palette
	ctx := &Context{Palette: palette, Runtime: &Runtime{}}
	element := List{
		Width:    24,
		Selected: 1,
		Focused:  true,
		Items: []ListItem{
			{ControlID: "first", Primary: "A", Secondary: "alpha"},
			{ControlID: "second", Primary: "B", Secondary: "beta"},
		},
	}
	assertRenderMatchesRenderTo(t, ctx, element, Rect{W: 24, H: 2})
}

func TestTableRenderMatchesRenderTo(t *testing.T) {
	palette := theme.Default().Palette
	ctx := &Context{Palette: palette, Runtime: &Runtime{}}
	element := Table{
		Width: 20,
		Columns: []TableColumn{
			{Title: "Name", Width: 10},
			{Title: "Kind", Width: 8},
		},
		ShowHeader: true,
		Rows: []TableRow{{
			ControlID: "readme",
			Cells:     []string{"README.md", "file"},
			Selected:  true,
			Focused:   true,
		}},
	}
	assertRenderMatchesRenderTo(t, ctx, element, Rect{W: 20, H: 2})
}

func TestModalFrameRenderMatchesRenderTo(t *testing.T) {
	palette := theme.Default().Palette
	ctx := &Context{Palette: palette, Runtime: &Runtime{}}
	element := ModalFrame{
		Title:    "Connect",
		Subtitle: "Configure provider",
		Body:     HitBox{ID: "body", Child: Static{Content: "Fields"}},
		Footer:   "Enter to submit",
		Width:    28,
	}
	assertRenderMatchesRenderTo(t, ctx, element, Rect{W: 28, H: 8})
}

func TestContainerRenderToAvoidsOwnerSurfaceAllocation(t *testing.T) {
	palette := theme.Default().Palette
	ctx := &Context{Palette: palette}
	element := List{
		Width:    24,
		Selected: 0,
		Focused:  true,
		Items: []ListItem{
			{Primary: "A", Secondary: "alpha"},
			{Primary: "B", Secondary: "beta"},
		},
	}

	ResetSurfaceAllocationStats()
	_ = element.Render(ctx, Rect{W: 24, H: 2})
	renderStats := SurfaceAllocationStatsSnapshot()

	dst := TransparentSurface(24, 2)
	ResetSurfaceAllocationStats()
	element.RenderTo(ctx, Rect{W: 24, H: 2}, &dst)
	renderToStats := SurfaceAllocationStatsSnapshot()

	if renderStats.Transparent <= renderToStats.Transparent {
		t.Fatalf("expected Render to allocate at least one additional transparent owner surface, got render=%#v renderTo=%#v", renderStats, renderToStats)
	}
}

func TestScrollFrameRendersVisibleWindowAtOffset(t *testing.T) {
	got := RenderElement(nil, ScrollFrame{
		Child:   Static{Content: "line1\nline2\nline3\nline4"},
		OffsetY: 1,
		Width:   5,
		Height:  2,
	}, 5, 2)

	lines := strings.Split(ansi.Strip(got), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 visible lines, got %d in %q", len(lines), got)
	}
	if strings.TrimSpace(lines[0]) != "line2" || strings.TrimSpace(lines[1]) != "line3" {
		t.Fatalf("expected scrolled window to show line2/line3, got %#v", lines)
	}
}

func TestScrollBoxClampsOffsetToContentBottom(t *testing.T) {
	box := ScrollBox{
		Child:   Static{Content: "line1\nline2\nline3"},
		OffsetY: 99,
		Width:   5,
		Height:  2,
	}

	surface, totalHeight, offset := box.RenderVisible(nil, 5, 2, box.OffsetY)
	lines := strings.Split(ansi.Strip(strings.Join(surface.Lines(), "\n")), "\n")

	if totalHeight != 3 {
		t.Fatalf("expected content height 3, got %d", totalHeight)
	}
	if offset != 1 {
		t.Fatalf("expected offset to clamp to 1, got %d", offset)
	}
	if len(lines) != 2 || strings.TrimSpace(lines[0]) != "line2" || strings.TrimSpace(lines[1]) != "line3" {
		t.Fatalf("expected bottom window to show line2/line3, got %#v", lines)
	}
}
