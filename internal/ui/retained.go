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

type MeasuredNode interface {
	Measure(ctx *Context, constraints Constraints) Size
	Paint(ctx *Context, canvas Canvas)
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

type LeafNode struct {
	BaseNode
	Content MeasuredNode
}

func (n *LeafNode) Measure(ctx *Context, constraints Constraints) Size {
	if n == nil || n.Content == nil {
		return constraints.Clamp(Size{})
	}
	return constraints.Clamp(n.Content.Measure(ctx, constraints))
}

func (n *LeafNode) Paint(ctx *Context, canvas Canvas) {
	if n == nil || n.Content == nil {
		return
	}
	n.Content.Paint(ctx, canvas)
}

func (n *LeafNode) ChildNodes() []Node {
	if n == nil || n.Content == nil {
		return nil
	}
	if children, ok := n.Content.(NodeChildren); ok {
		return children.ChildNodes()
	}
	return nil
}

func (n *LeafNode) Visible() bool {
	if n == nil || n.Content == nil {
		return false
	}
	if visible, ok := n.Content.(Visibility); ok {
		return visible.Visible()
	}
	return true
}

func (n *LeafNode) Box() BoxProps {
	if n == nil || n.Content == nil {
		return BoxProps{}
	}
	if box, ok := n.Content.(BoxModel); ok {
		return box.Box()
	}
	return BoxProps{Display: DisplayFlex, VisibleFlag: true}
}

func (n *LeafNode) InvalidateCache() {
	if n == nil || n.Content == nil {
		return
	}
	if invalidator, ok := n.Content.(CacheInvalidator); ok {
		invalidator.InvalidateCache()
	}
}

func (n *LeafNode) RenderVisibleInto(ctx *Context, width, height, offset int, dst *Surface) (int, int) {
	if n == nil || n.Content == nil {
		return 0, 0
	}
	if renderer, ok := n.Content.(interface {
		RenderVisibleInto(ctx *Context, width, height, offset int, dst *Surface) (int, int)
	}); ok {
		return renderer.RenderVisibleInto(ctx, width, height, offset, dst)
	}
	if width <= 0 || height <= 0 {
		return 0, 0
	}
	size := n.Content.Measure(ctx, Constraints{MaxW: width})
	totalHeight := size.H
	maxOffset := max(0, totalHeight-height)
	offset = min(max(0, offset), maxOffset)
	if dst == nil {
		return totalHeight, offset
	}
	childHeight := max(height, totalHeight)
	paintNodeInto(ctx, n, Rect{X: 0, Y: -offset, W: width, H: childHeight}, dst)
	return totalHeight, offset
}

func (n *LeafNode) RenderBottomInto(ctx *Context, width, height int, dst *Surface) (int, int) {
	if n == nil || n.Content == nil {
		return 0, 0
	}
	if renderer, ok := n.Content.(interface {
		RenderBottomInto(ctx *Context, width, height int, dst *Surface) (int, int)
	}); ok {
		return renderer.RenderBottomInto(ctx, width, height, dst)
	}
	size := n.Content.Measure(ctx, Constraints{MaxW: width})
	totalHeight := size.H
	offset := max(0, totalHeight-max(0, height))
	if dst == nil {
		return totalHeight, offset
	}
	childHeight := max(height, totalHeight)
	paintNodeInto(ctx, n, Rect{X: 0, Y: -offset, W: width, H: childHeight}, dst)
	return totalHeight, offset
}

func (n *LeafNode) ApproxHeight(width int) int {
	if n == nil || n.Content == nil {
		return 0
	}
	if cached, ok := n.Content.(interface{ ApproxHeight(int) int }); ok {
		return cached.ApproxHeight(width)
	}
	return n.Content.Measure(nil, NewConstraints(width, 0)).H
}

func (n *LeafNode) RenderCached(ctx *Context, width int) Surface {
	if n == nil || n.Content == nil {
		return Surface{}
	}
	if cached, ok := n.Content.(interface {
		RenderCached(ctx *Context, width int) Surface
	}); ok {
		return cached.RenderCached(ctx, width)
	}
	size := n.Content.Measure(ctx, NewConstraints(width, 0))
	return PaintNodeSurface(withoutRuntime(ctx), n, Rect{W: width, H: size.H})
}

func AsNode(value any) Node {
	switch typed := value.(type) {
	case nil:
		return nil
	case Node:
		return typed
	case MeasuredNode:
		return &LeafNode{Content: typed}
	default:
		return nil
	}
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
