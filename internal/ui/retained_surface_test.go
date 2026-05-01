package ui

import "testing"

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
