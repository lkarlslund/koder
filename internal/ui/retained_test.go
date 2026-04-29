package ui

import (
	"reflect"
	"testing"
)

type testDiffNode struct {
	BaseNode
	measureFn func(*Context, Constraints) Size
	paintFn   func(*Context, Rect) Surface
	surface   Surface
}

func (n *testDiffNode) Measure(ctx *Context, constraints Constraints) Size {
	if n.measureFn != nil {
		return n.measureFn(ctx, constraints)
	}
	return constraints.Clamp(Size{})
}

func (n *testDiffNode) Prepare(*Context) {}

func (n *testDiffNode) Paint(ctx *Context, canvas Canvas) {
	if n.paintFn == nil || n.Rect().Empty() || canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	next := n.paintFn(ctx, n.Rect()).Normalize(canvas.Width(), canvas.Height())
	if dirtyRects, ok := next.DirtyRects(); ok {
		for _, dirty := range dirtyRects {
			n.MarkDirtyAbsolute(dirty.Translate(n.Rect().X, n.Rect().Y))
		}
	} else if len(n.surface.cells) > 0 || n.surface.SurfaceWidth() > 0 || n.surface.SurfaceHeight() > 0 {
		n.MarkDirtyLocalRects(DiffSurfaceDamage(n.surface, next))
	} else {
		n.MarkDirtyLocal(Rect{W: n.Rect().W, H: n.Rect().H})
	}
	canvas.BlitSurface(0, 0, next)
	n.surface = next
}

type testManagedNode struct {
	BaseNode
	prepareFn    func(*Context, Rect)
	dirtyFn      func() bool
	dirtyRectsFn func() []Rect
	layoutFn     func() bool
	clearFn      func()
}

type testRectNode struct {
	BaseNode
	size Size
}

func (n *testRectNode) Measure(_ *Context, constraints Constraints) Size {
	return constraints.Clamp(n.size)
}

func (n *testRectNode) Prepare(_ *Context) {
}

func (n *testRectNode) Paint(_ *Context, _ Canvas) {
}

func (n *testManagedNode) Measure(_ *Context, constraints Constraints) Size {
	return constraints.Clamp(Size{W: n.Rect().W, H: n.Rect().H})
}

func (n *testManagedNode) Prepare(ctx *Context) {
	if n.prepareFn != nil {
		n.prepareFn(ctx, n.Rect())
	}
	if n.layoutFn != nil && n.layoutFn() {
		n.MarkLayoutDirty()
	}
	if n.dirtyFn != nil && n.dirtyFn() {
		if n.dirtyRectsFn != nil {
			rects := n.dirtyRectsFn()
			if len(rects) > 0 {
				n.MarkDirtyLocalRects(rects)
				return
			}
		}
		n.MarkDirtyLocal(Rect{})
	}
}

func (n *testManagedNode) Paint(_ *Context, _ Canvas) {}

func (n *testManagedNode) ClearFrameDirty() {
	n.ClearDirty()
	if n.clearFn != nil {
		n.clearFn()
	}
}

func TestBaseNodeLayoutMarksOldAndNewRectsDirty(t *testing.T) {
	var node BaseNode
	node.Layout(nil, Rect{X: 1, Y: 2, W: 3, H: 4})
	node.ClearDirty()
	node.Layout(nil, Rect{X: 2, Y: 3, W: 3, H: 4})

	got := node.DirtyRects()
	want := []Rect{
		{X: 1, Y: 2, W: 3, H: 4},
		{X: 2, Y: 3, W: 3, H: 4},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dirty rects mismatch:\n got %#v\nwant %#v", got, want)
	}
}

func TestBaseNodeMarkDirtyLocalTranslatesToAbsolute(t *testing.T) {
	var node BaseNode
	node.Layout(nil, Rect{X: 4, Y: 5, W: 10, H: 3})
	node.ClearDirty()
	node.MarkDirtyLocal(Rect{X: 2, Y: 1, W: 3, H: 1})

	got := node.DirtyRects()
	want := []Rect{{X: 6, Y: 6, W: 3, H: 1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dirty rects mismatch:\n got %#v\nwant %#v", got, want)
	}
	if node.NeedsLayout() {
		t.Fatal("expected paint-only dirty state")
	}
	if !node.NeedsPaint() {
		t.Fatal("expected paint dirty state after local invalidation")
	}
}

func TestBaseNodeMarkLayoutDirtyAlsoMarksPaintDirty(t *testing.T) {
	var node BaseNode
	node.Layout(nil, Rect{X: 1, Y: 1, W: 4, H: 2})
	node.ClearDirty()

	node.MarkLayoutDirty()

	if !node.NeedsLayout() {
		t.Fatal("expected layout dirty state")
	}
	if !node.NeedsPaint() {
		t.Fatal("expected paint dirty state with layout dirty")
	}
	got := node.DirtyRects()
	want := []Rect{{X: 1, Y: 1, W: 4, H: 2}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dirty rects mismatch:\n got %#v\nwant %#v", got, want)
	}
}

func TestBaseNodeClearDirtyResetsLayoutAndPaintFlags(t *testing.T) {
	var node BaseNode
	node.Layout(nil, Rect{X: 0, Y: 0, W: 2, H: 1})
	node.ClearDirty()
	node.MarkLayoutDirty()

	node.ClearDirty()

	if node.NeedsLayout() {
		t.Fatal("expected layout dirty flag to clear")
	}
	if node.NeedsPaint() {
		t.Fatal("expected paint dirty flag to clear")
	}
	if got := node.DirtyRects(); len(got) != 0 {
		t.Fatalf("expected dirty rects to clear, got %#v", got)
	}
}

func TestNativeDiffNodePaintUsesSurfaceDirtyRects(t *testing.T) {
	node := &testDiffNode{
		paintFn: func(_ *Context, bounds Rect) Surface {
			surface := BlankSurface(bounds.W, bounds.H)
			surface.WriteText(1, 0, "X", CellStyle{})
			return surface.WithDirtyRects(Rect{X: 1, Y: 0, W: 1, H: 1})
		},
	}
	node.Layout(nil, Rect{X: 3, Y: 4, W: 4, H: 1})
	node.ClearDirty()

	root := BlankSurface(10, 10)
	node.Paint(nil, NewCanvas(&root, node.Rect()))

	got := node.DirtyRects()
	want := []Rect{{X: 4, Y: 4, W: 1, H: 1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dirty rects mismatch:\n got %#v\nwant %#v", got, want)
	}
	if text := root.SurfaceCellText(4, 4); text != "X" {
		t.Fatalf("expected painted cell at 4,4, got %q", text)
	}
}

func TestNativeManagedNodePrepareMarksLayoutAndPaint(t *testing.T) {
	called := false
	node := &testManagedNode{
		prepareFn: func(_ *Context, rect Rect) {
			called = true
			if rect != (Rect{X: 10, Y: 3, W: 8, H: 2}) {
				t.Fatalf("prepare rect = %#v", rect)
			}
		},
		dirtyFn:      func() bool { return true },
		dirtyRectsFn: func() []Rect { return []Rect{{X: 1, Y: 0, W: 2, H: 1}} },
		layoutFn:     func() bool { return true },
	}
	node.Layout(nil, Rect{X: 10, Y: 3, W: 8, H: 2})
	node.ClearDirty()

	node.Prepare(nil)

	if !called {
		t.Fatal("expected prepare to be called")
	}
	if !node.NeedsLayout() {
		t.Fatal("expected layout dirty state")
	}
	if !node.NeedsPaint() {
		t.Fatal("expected paint dirty state")
	}
	got := node.DirtyRects()
	want := []Rect{
		{X: 10, Y: 3, W: 8, H: 2},
		{X: 11, Y: 3, W: 2, H: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dirty rects mismatch:\n got %#v\nwant %#v", got, want)
	}
}

func TestNativeManagedNodeClearFrameDirtyClearsNodeAndOwnerState(t *testing.T) {
	cleared := false
	node := &testManagedNode{
		clearFn: func() { cleared = true },
	}
	node.Layout(nil, Rect{X: 1, Y: 2, W: 3, H: 1})
	node.MarkLayoutDirty()

	node.ClearFrameDirty()

	if !cleared {
		t.Fatal("expected clear hook to run")
	}
	if node.NeedsLayout() {
		t.Fatal("expected layout dirty flag to clear")
	}
	if node.NeedsPaint() {
		t.Fatal("expected paint dirty flag to clear")
	}
	if got := node.DirtyRects(); len(got) != 0 {
		t.Fatalf("expected dirty rects to clear, got %#v", got)
	}
}

func TestFlexNodeLayoutVerticalAllocatesFixedAndFlexChildren(t *testing.T) {
	header := &testRectNode{size: Size{W: 20, H: 3}}
	body := &testRectNode{size: Size{W: 20, H: 8}}
	footer := &testRectNode{size: Size{W: 20, H: 2}}
	node := &FlexNode{
		Direction: DirectionVertical,
		Children: []FlexNodeChild{
			{Node: header},
			{Node: body, Flex: 1},
			{Node: footer},
		},
	}

	node.Layout(nil, Rect{W: 20, H: 12})

	if got := header.Rect(); got != (Rect{W: 20, H: 3}) {
		t.Fatalf("header rect = %#v", got)
	}
	if got := body.Rect(); got != (Rect{Y: 3, W: 20, H: 7}) {
		t.Fatalf("body rect = %#v", got)
	}
	if got := footer.Rect(); got != (Rect{Y: 10, W: 20, H: 2}) {
		t.Fatalf("footer rect = %#v", got)
	}
}

func TestFlexNodeLayoutHorizontalOmitsZeroWidthFixedChildren(t *testing.T) {
	main := &testRectNode{size: Size{W: 10, H: 4}}
	hidden := &testRectNode{size: Size{}}
	sidebar := &testRectNode{size: Size{W: 5, H: 4}}
	node := &FlexNode{
		Direction: DirectionHorizontal,
		Spacing:   1,
		Children: []FlexNodeChild{
			{Node: main, Flex: 1},
			{Node: hidden},
			{Node: sidebar},
		},
	}

	node.Layout(nil, Rect{W: 20, H: 4})

	if got := main.Rect(); got != (Rect{W: 14, H: 4}) {
		t.Fatalf("main rect = %#v", got)
	}
	if got := hidden.Rect(); !got.Empty() {
		t.Fatalf("hidden rect = %#v", got)
	}
	if got := sidebar.Rect(); got != (Rect{X: 15, W: 5, H: 4}) {
		t.Fatalf("sidebar rect = %#v", got)
	}
}

func TestFlexNodeLayoutHorizontalUsesChildBasis(t *testing.T) {
	main := &testRectNode{size: Size{W: 10, H: 4}}
	sidebar := &testRectNode{size: Size{W: 0, H: 4}}
	node := &FlexNode{
		Direction: DirectionHorizontal,
		Children: []FlexNodeChild{
			{Node: main, Flex: 1},
			{Node: sidebar, Basis: 8},
		},
	}

	node.Layout(nil, Rect{W: 20, H: 4})

	if got := main.Rect(); got != (Rect{W: 12, H: 4}) {
		t.Fatalf("main rect = %#v", got)
	}
	if got := sidebar.Rect(); got != (Rect{X: 12, W: 8, H: 4}) {
		t.Fatalf("sidebar rect = %#v", got)
	}
}
