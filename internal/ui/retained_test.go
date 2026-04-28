package ui

import (
	"reflect"
	"testing"
)

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

func TestSurfaceNodePaintUsesSurfaceDirtyRects(t *testing.T) {
	node := &SurfaceNode{
		RenderFn: func(_ *Context, bounds Rect) Surface {
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

func TestManagedElementNodePrepareMarksLayoutAndPaint(t *testing.T) {
	called := false
	node := &ManagedElementNode{
		PrepareFn: func(_ *Context, rect Rect) {
			called = true
			if rect != (Rect{X: 10, Y: 3, W: 8, H: 2}) {
				t.Fatalf("prepare rect = %#v", rect)
			}
		},
		DirtyFn:         func() bool { return true },
		DirtyRectsFn:    func() []Rect { return []Rect{{X: 1, Y: 0, W: 2, H: 1}} },
		LayoutChangedFn: func() bool { return true },
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

func TestManagedElementNodeClearFrameDirtyClearsNodeAndOwnerState(t *testing.T) {
	cleared := false
	node := &ManagedElementNode{
		ClearFn: func() { cleared = true },
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
