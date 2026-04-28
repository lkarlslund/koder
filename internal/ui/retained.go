package ui

type Node interface {
	Measure(ctx *Context, constraints Constraints) Size
	Layout(ctx *Context, rect Rect)
	Prepare(ctx *Context)
	Paint(ctx *Context, canvas Canvas)
	Rect() Rect
	NeedsLayout() bool
	NeedsPaint() bool
	DirtyRects() []Rect
	ClearDirty()
}

type NodeChildren interface {
	ChildNodes() []Node
}

type BaseNode struct {
	rect        Rect
	damage      DamageSet
	layoutDirty bool
	paintDirty  bool
}

func (n *BaseNode) Rect() Rect {
	if n == nil {
		return Rect{}
	}
	return n.rect
}

func (n *BaseNode) Layout(_ *Context, rect Rect) {
	if n == nil {
		return
	}
	if n.rect == rect {
		n.layoutDirty = false
		return
	}
	if !n.rect.Empty() {
		n.damage.Add(n.rect)
	}
	n.rect = rect
	if !n.rect.Empty() {
		n.damage.Add(n.rect)
	}
	n.layoutDirty = false
	n.paintDirty = true
}

func (n *BaseNode) Prepare(_ *Context) {
}

func (n *BaseNode) MarkDirtyLocal(rect Rect) {
	if n == nil || n.rect.Empty() {
		return
	}
	n.paintDirty = true
	if rect.Empty() {
		n.damage.Add(n.rect)
		return
	}
	n.damage.Add(clipRect(rect.Translate(n.rect.X, n.rect.Y), n.rect))
}

func (n *BaseNode) MarkDirtyLocalRects(rects []Rect) {
	if n == nil {
		return
	}
	for _, rect := range rects {
		n.MarkDirtyLocal(rect)
	}
}

func (n *BaseNode) MarkDirtyAbsolute(rect Rect) {
	if n == nil || rect.Empty() {
		return
	}
	n.paintDirty = true
	n.damage.Add(rect)
}

func (n *BaseNode) MarkLayoutDirty() {
	if n == nil {
		return
	}
	n.layoutDirty = true
	n.paintDirty = true
	if !n.rect.Empty() {
		n.damage.Add(n.rect)
	}
}

func (n *BaseNode) NeedsLayout() bool {
	return n != nil && n.layoutDirty
}

func (n *BaseNode) NeedsPaint() bool {
	return n != nil && n.paintDirty
}

func (n *BaseNode) DirtyRects() []Rect {
	if n == nil {
		return nil
	}
	return n.damage.Rects()
}

func (n *BaseNode) ClearDirty() {
	if n == nil {
		return
	}
	n.damage.Reset()
	n.layoutDirty = false
	n.paintDirty = false
}
