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

type SurfaceNode struct {
	BaseNode
	MeasureFn func(ctx *Context, constraints Constraints) Size
	RenderFn  func(ctx *Context, bounds Rect) Surface
	surface   Surface
}

type ElementNode struct {
	BaseNode
	MeasureFn func(ctx *Context, constraints Constraints) Size
	ElementFn func(ctx *Context) Element
}

type ManagedElementNode struct {
	ElementNode
	PrepareFn       func(*Context, Rect)
	DirtyFn         func() bool
	DirtyRectsFn    func() []Rect
	LayoutChangedFn func() bool
	ClearFn         func()
}

func (n *SurfaceNode) Measure(ctx *Context, constraints Constraints) Size {
	if n == nil {
		return Size{}
	}
	if n.MeasureFn != nil {
		return n.MeasureFn(ctx, constraints)
	}
	if n.RenderFn == nil {
		return constraints.Clamp(Size{})
	}
	surface := n.RenderFn(ctx, Rect{W: constraints.MaxW, H: constraints.MaxH})
	return constraints.Clamp(surface.Size())
}

func (n *SurfaceNode) Prepare(_ *Context) {
}

func (n *SurfaceNode) Paint(ctx *Context, canvas Canvas) {
	if n == nil || n.RenderFn == nil {
		return
	}
	rect := n.Rect()
	if rect.Empty() || canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	next := n.RenderFn(ctx, Rect{W: rect.W, H: rect.H}).Normalize(rect.W, rect.H)
	if dirtyRects, ok := next.DirtyRects(); ok {
		for _, dirty := range dirtyRects {
			n.MarkDirtyAbsolute(dirty.Translate(rect.X, rect.Y))
		}
	} else if len(n.surface.cells) > 0 || n.surface.SurfaceWidth() > 0 || n.surface.SurfaceHeight() > 0 {
		for _, dirty := range DiffSurfaceDamage(n.surface, next) {
			n.MarkDirtyAbsolute(dirty.Translate(rect.X, rect.Y))
		}
	} else {
		n.MarkDirtyAbsolute(rect)
	}
	canvas.BlitSurface(0, 0, next)
	n.surface = next
}

func (n *ElementNode) Measure(ctx *Context, constraints Constraints) Size {
	if n == nil {
		return Size{}
	}
	if n.MeasureFn != nil {
		return n.MeasureFn(ctx, constraints)
	}
	if n.ElementFn == nil {
		return constraints.Clamp(Size{})
	}
	element := n.ElementFn(ctx)
	if element == nil {
		return constraints.Clamp(Size{})
	}
	return constraints.Clamp(element.Measure(ctx, constraints))
}

func (n *ElementNode) Prepare(_ *Context) {
}

func (n *ElementNode) Paint(ctx *Context, canvas Canvas) {
	if n == nil || n.ElementFn == nil {
		return
	}
	element := n.ElementFn(ctx)
	if element == nil {
		return
	}
	renderElementInto(ctx, element, Rect{
		X: canvas.origin.X,
		Y: canvas.origin.Y,
		W: canvas.Width(),
		H: canvas.Height(),
	}, canvas.surface)
}

func (n *ManagedElementNode) Prepare(ctx *Context) {
	if n == nil || n.PrepareFn == nil {
		return
	}
	n.PrepareFn(ctx, n.Rect())
	if n.LayoutChangedFn != nil && n.LayoutChangedFn() {
		n.MarkLayoutDirty()
	}
	if n.DirtyFn != nil && n.DirtyFn() {
		if n.DirtyRectsFn != nil {
			if rects := n.DirtyRectsFn(); len(rects) > 0 {
				for _, rect := range rects {
					n.MarkDirtyLocal(rect)
				}
				return
			}
		}
		n.MarkDirtyLocal(Rect{})
	}
}

func (n *ManagedElementNode) ClearFrameDirty() {
	if n == nil {
		return
	}
	n.ClearDirty()
	if n.ClearFn != nil {
		n.ClearFn()
	}
}
