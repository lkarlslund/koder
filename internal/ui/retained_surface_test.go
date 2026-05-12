package ui

import (
	"testing"

	"github.com/lkarlslund/koder/internal/theme"
)

type retainedSurfaceTestNode struct {
	BaseNode
	text string
}

func (n *retainedSurfaceTestNode) Measure(_ *Context, constraints Constraints) Size {
	return constraints.Clamp(Size{W: max(1, len(n.text)), H: 1})
}

func (n *retainedSurfaceTestNode) Prepare(_ *Context) {
}

func (n *retainedSurfaceTestNode) Paint(_ *Context, canvas Canvas) {
	canvas.WriteText(0, 0, n.text, CellStyle{})
}

type retainedFocusTestNode struct {
	retainedSurfaceTestNode
	focused bool
}

func (n *retainedFocusTestNode) Focus() {
	n.focused = true
	n.MarkDirtyLocal(Rect{W: n.Rect().W, H: n.Rect().H})
}

func (n *retainedFocusTestNode) Blur() {
	n.focused = false
	n.MarkDirtyLocal(Rect{W: n.Rect().W, H: n.Rect().H})
}

func (n *retainedFocusTestNode) Focused() bool { return n.focused }

func (n *retainedFocusTestNode) HandleKey(KeyMsg) (bool, Cmd) { return false, nil }

type retainedWheelTestNode struct {
	retainedSurfaceTestNode
	wants bool
}

func (n *retainedWheelTestNode) WantsWheel(Point) bool { return n.wants }

func TestRetainedSurfaceReusesCleanSurface(t *testing.T) {
	node := &retainedSurfaceTestNode{text: "first"}
	retained := NewRetainedSurface(node)
	bounds := Rect{W: 8, H: 1}

	first := retained.Surface(&Context{}, bounds)
	if got := first.Lines()[0]; got != "first   " {
		t.Fatalf("expected first paint, got %q", got)
	}

	node.text = "second"
	second := retained.Surface(&Context{}, bounds)
	if got := second.Lines()[0]; got != "first   " {
		t.Fatalf("expected clean retained surface to be reused, got %q", got)
	}
}

func TestRetainedSurfaceReportsFullDamageAfterInvalidate(t *testing.T) {
	node := &retainedSurfaceTestNode{text: "first"}
	retained := NewRetainedSurface(node)
	bounds := Rect{W: 8, H: 2}
	_ = retained.Surface(&Context{}, bounds)

	node.text = "later"
	retained.Invalidate()
	next := retained.Surface(&Context{}, bounds)

	if got := next.Lines()[0]; got != "later   " {
		t.Fatalf("expected invalidated surface to repaint, got %q", got)
	}
	rects, ok := next.DirtyRects()
	if !ok || len(rects) != 1 || rects[0] != bounds {
		t.Fatalf("expected invalidated retained surface to report full damage, got %#v ok=%v", rects, ok)
	}
}

func TestFlexNodeFocusTraversalSkipsNonFocusableChildren(t *testing.T) {
	first := &retainedFocusTestNode{retainedSurfaceTestNode: retainedSurfaceTestNode{text: "a"}}
	passive := &retainedSurfaceTestNode{text: "b"}
	second := &retainedFocusTestNode{retainedSurfaceTestNode: retainedSurfaceTestNode{text: "c"}}
	flex := NewFlexNode(DirectionVertical, []FlexNodeChild{
		{Node: first},
		{Node: passive},
		{Node: second},
	}, 0)

	if !flex.FocusFirst() || !first.Focused() {
		t.Fatal("expected first focusable child to receive focus")
	}
	if !flex.FocusNext() || first.Focused() || !second.Focused() {
		t.Fatal("expected focus to advance to second focusable child")
	}
	if !flex.FocusPrev() || !first.Focused() || second.Focused() {
		t.Fatal("expected focus to move back to first focusable child")
	}
}

func TestFlexNodeWheelNodeAtUsesHitTestAndInterest(t *testing.T) {
	uninterested := &retainedWheelTestNode{retainedSurfaceTestNode: retainedSurfaceTestNode{text: "a"}, wants: false}
	interested := &retainedWheelTestNode{retainedSurfaceTestNode: retainedSurfaceTestNode{text: "b"}, wants: true}
	flex := NewFlexNode(DirectionVertical, []FlexNodeChild{
		{Node: uninterested},
		{Node: interested},
	}, 0)
	flex.Layout(&Context{}, Rect{W: 5, H: 2})

	if _, ok := flex.WheelNodeAt(Point{X: 1, Y: 0}); ok {
		t.Fatal("expected uninterested child not to receive wheel")
	}
	got, ok := flex.WheelNodeAt(Point{X: 1, Y: 1})
	if !ok || got != interested {
		t.Fatalf("expected interested child under pointer, got %#v ok=%v", got, ok)
	}
}

func TestRetainedSurfacePaintsDirtyNode(t *testing.T) {
	node := &retainedSurfaceTestNode{text: "first"}
	retained := NewRetainedSurface(node)
	bounds := Rect{W: 8, H: 1}
	_ = retained.Surface(&Context{}, bounds)

	node.text = "later"
	node.MarkDirtyLocal(Rect{W: 8, H: 1})
	next := retained.Surface(&Context{}, bounds)
	if got := next.Lines()[0]; got != "later   " {
		t.Fatalf("expected dirty retained node repaint, got %q", got)
	}
	rects, ok := next.DirtyRects()
	if !ok || len(rects) == 0 || rects[0] != (Rect{W: 8, H: 1}) {
		t.Fatalf("expected dirty rect for repaint, got %#v ok=%v", rects, ok)
	}
}

func TestRetainedSurfacePaintsDirtyContainerNode(t *testing.T) {
	child := &retainedSurfaceTestNode{text: "first"}
	node := NewHashedNode(child, 0)
	retained := NewRetainedSurface(node)
	bounds := Rect{W: 8, H: 1}
	first := retained.Surface(&Context{}, bounds)
	if got := first.Lines()[0]; got != "first   " {
		t.Fatalf("expected first paint, got %q", got)
	}

	child.text = "later"
	node.SetHash(1)
	next := retained.Surface(&Context{}, bounds)
	if got := next.Lines()[0]; got != "later   " {
		t.Fatalf("expected dirty container to repaint child subtree, got %q", got)
	}
}

func TestRetainedSurfacePaintsDirtyNodeUnderPassiveContainer(t *testing.T) {
	spinner := &Spinner{}
	spinner.Set("dots", "Working", true, theme.Palette{})
	column := NewRetainedColumn(0)
	column.Add(NewFlexNode(DirectionHorizontal, []FlexNodeChild{
		{Node: &RetainedLabel{Text: "Status"}},
		{Node: spinner},
	}, 1))
	node := NewHashedNode(AsNode(Sidebar{Child: column, Width: 20, Height: 1}), 0)
	retained := NewRetainedSurface(node)
	bounds := Rect{W: 20, H: 1}

	first := retained.Surface(&Context{}, bounds)
	if got := first.Lines()[0]; got != " Status ⠋  Working  " {
		t.Fatalf("expected first sidebar spinner frame, got %q", got)
	}

	if !HandleSpinnerTimer(node, TimerEvent{Owner: SpinnerTimerOwner}) {
		t.Fatal("expected spinner timer to be handled")
	}
	next := retained.Surface(&Context{}, bounds)
	if got := next.Lines()[0]; got != " Status ⠙  Working  " {
		t.Fatalf("expected dirty sidebar spinner to advance, got %q", got)
	}
}
