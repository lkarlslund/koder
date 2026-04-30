package ui

type Measurer interface {
	Measure(ctx *Context, constraints Constraints) Size
}

type Layouter interface {
	Layout(ctx *Context, rect Rect)
	Rect() Rect
}

type Preparer interface {
	Prepare(ctx *Context)
}

type CanvasPainter interface {
	Paint(ctx *Context, canvas Canvas)
}

type DirtyTracker interface {
	NeedsLayout() bool
	NeedsPaint() bool
	DirtyRects() []Rect
	ClearDirty()
}

type Node interface {
	Measurer
	Layouter
	Preparer
	CanvasPainter
	DirtyTracker
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

type PassiveNode struct{}

func (PassiveNode) Layout(_ *Context, _ Rect) {}
func (PassiveNode) Prepare(_ *Context)        {}
func (PassiveNode) Rect() Rect                { return Rect{} }
func (PassiveNode) NeedsLayout() bool         { return false }
func (PassiveNode) NeedsPaint() bool          { return false }
func (PassiveNode) DirtyRects() []Rect        { return nil }
func (PassiveNode) ClearDirty()               {}

func RenderVisibleIntoLeaf(ctx *Context, node Node, width, height, offset int, dst *Surface) (int, int) {
	if node == nil {
		return 0, 0
	}
	if renderer, ok := node.(interface {
		RenderVisibleInto(ctx *Context, width, height, offset int, dst *Surface) (int, int)
	}); ok {
		return renderer.RenderVisibleInto(ctx, width, height, offset, dst)
	}
	if width <= 0 || height <= 0 {
		return 0, 0
	}
	size := node.Measure(ctx, Constraints{MaxW: width})
	totalHeight := size.H
	maxOffset := max(0, totalHeight-height)
	offset = min(max(0, offset), maxOffset)
	if dst == nil {
		return totalHeight, offset
	}
	childHeight := max(height, totalHeight)
	paintNodeInto(ctx, node, Rect{X: 0, Y: -offset, W: width, H: childHeight}, dst)
	return totalHeight, offset
}

func RenderBottomIntoLeaf(ctx *Context, node Node, width, height int, dst *Surface) (int, int) {
	if node == nil {
		return 0, 0
	}
	if renderer, ok := node.(interface {
		RenderBottomInto(ctx *Context, width, height int, dst *Surface) (int, int)
	}); ok {
		return renderer.RenderBottomInto(ctx, width, height, dst)
	}
	size := node.Measure(ctx, Constraints{MaxW: width})
	totalHeight := size.H
	offset := max(0, totalHeight-max(0, height))
	if dst == nil {
		return totalHeight, offset
	}
	childHeight := max(height, totalHeight)
	paintNodeInto(ctx, node, Rect{X: 0, Y: -offset, W: width, H: childHeight}, dst)
	return totalHeight, offset
}

func ApproxHeightLeaf(node Node, width int) int {
	if node == nil {
		return 0
	}
	if cached, ok := node.(interface{ ApproxHeight(int) int }); ok {
		return cached.ApproxHeight(width)
	}
	return node.Measure(nil, NewConstraints(width, 0)).H
}

func RenderCachedLeaf(ctx *Context, node Node, width int) Surface {
	if node == nil {
		return Surface{}
	}
	if cached, ok := node.(interface {
		RenderCached(ctx *Context, width int) Surface
	}); ok {
		return cached.RenderCached(ctx, width)
	}
	size := node.Measure(ctx, NewConstraints(width, 0))
	return PaintNodeSurface(withoutRuntime(ctx), node, Rect{W: width, H: size.H})
}

func AsNode(node Node) Node {
	return node
}

func PaintNodeSurface(ctx *Context, node Node, bounds Rect) Surface {
	if node == nil || bounds.W <= 0 || bounds.H <= 0 {
		return Surface{}
	}
	base := TransparentSurface(bounds.W, bounds.H)
	shadow := &Runtime{}
	localCtx := &Context{Runtime: shadow}
	if ctx != nil {
		copyCtx := *ctx
		copyCtx.Runtime = shadow
		localCtx = &copyCtx
	}
	node.Layout(localCtx, Rect{W: bounds.W, H: bounds.H})
	node.Prepare(localCtx)
	node.Paint(localCtx, NewCanvas(&base, Rect{W: bounds.W, H: bounds.H}))
	if controls := shadow.Controls(); len(controls) > 0 {
		base.ctrls = append(base.ctrls[:0], controls...)
		if ctx != nil && ctx.Runtime != nil {
			base.RegisterControls(ctx.Runtime, bounds.X, bounds.Y)
		}
	}
	return base
}

func paintNodeInto(ctx *Context, node Node, bounds Rect, dst *Surface) {
	if node == nil || dst == nil || bounds.W <= 0 || bounds.H <= 0 {
		return
	}
	node.Layout(ctx, bounds)
	node.Prepare(ctx)
	node.Paint(ctx, NewCanvas(dst, bounds))
}
