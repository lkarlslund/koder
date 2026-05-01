package ui

// Measurer computes the size a node wants within constraints.
type Measurer interface {
	Measure(ctx *Context, constraints Constraints) Size
}

// Layouter assigns an absolute rectangle to a node.
type Layouter interface {
	Layout(ctx *Context, rect Rect)
	Rect() Rect
}

// Preparer lets a node update cached state after layout and before paint.
type Preparer interface {
	Prepare(ctx *Context)
}

// CanvasPainter paints a node into a clipped Canvas.
type CanvasPainter interface {
	Paint(ctx *Context, canvas Canvas)
}

// DirtyTracker reports whether a node needs layout or paint work.
type DirtyTracker interface {
	NeedsLayout() bool
	NeedsPaint() bool
	DirtyRects() []Rect
	ClearDirty()
}

// Node is the retained UI primitive understood by the layout and paint system.
type Node interface {
	Measurer
	Layouter
	Preparer
	CanvasPainter
	DirtyTracker
}

// FocusableNode is an opt-in retained node that can own keyboard focus.
type FocusableNode interface {
	Node
	Focus()
	Blur()
	Focused() bool
	HandleKey(KeyMsg) (bool, Cmd)
}

// FocusScope is an opt-in retained node that manages focus among descendants.
type FocusScope interface {
	Node
	FocusFirst() bool
	FocusNext() bool
	FocusPrev() bool
	FocusedNode() FocusableNode
}

// WheelNode is an opt-in retained node that wants mouse wheel events.
type WheelNode interface {
	Node
	WantsWheel(Point) bool
}

// BaseNode provides common rectangle and dirty-region bookkeeping for nodes.
type BaseNode struct {
	rect        Rect
	damage      DamageSet
	layoutDirty bool
	paintDirty  bool
}

// Rect returns the node's last assigned layout rectangle.
func (n *BaseNode) Rect() Rect {
	if n == nil {
		return Rect{}
	}
	return n.rect
}

// Layout records rect and marks old and new bounds as damaged when it changes.
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

// Prepare is a no-op default for nodes with no pre-paint work.
func (n *BaseNode) Prepare(_ *Context) {
}

// MarkDirtyLocal marks a rectangle in the node's local coordinate space dirty.
func (n *BaseNode) MarkDirtyLocal(rect Rect) {
	if n == nil || n.rect.Empty() {
		return
	}
	n.paintDirty = true
	if rect.Empty() {
		n.damage.Add(n.rect)
		return
	}
	n.damage.Add(rect.Translate(n.rect.X, n.rect.Y).Clip(n.rect))
}

// MarkDirtyLocalRects marks multiple local rectangles dirty.
func (n *BaseNode) MarkDirtyLocalRects(rects []Rect) {
	if n == nil {
		return
	}
	for _, rect := range rects {
		n.MarkDirtyLocal(rect)
	}
}

// MarkDirtyAbsolute marks an already-root-relative rectangle dirty.
func (n *BaseNode) MarkDirtyAbsolute(rect Rect) {
	if n == nil || rect.Empty() {
		return
	}
	n.paintDirty = true
	n.damage.Add(rect)
}

// MarkLayoutDirty marks the node for re-layout and repaints its current bounds.
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

// NeedsLayout reports whether the node needs another layout pass.
func (n *BaseNode) NeedsLayout() bool {
	return n != nil && n.layoutDirty
}

// NeedsPaint reports whether the node has dirty paint regions.
func (n *BaseNode) NeedsPaint() bool {
	return n != nil && n.paintDirty
}

// DirtyRects returns a copy of the accumulated dirty rectangles.
func (n *BaseNode) DirtyRects() []Rect {
	if n == nil {
		return nil
	}
	return n.damage.Rects()
}

// ClearDirty clears layout and paint dirty state.
func (n *BaseNode) ClearDirty() {
	if n == nil {
		return
	}
	n.damage.Reset()
	n.layoutDirty = false
	n.paintDirty = false
}

// PassiveNode implements the retained node lifecycle for stateless leaf nodes.
type PassiveNode struct{}

func (PassiveNode) Layout(_ *Context, _ Rect) {}
func (PassiveNode) Prepare(_ *Context)        {}
func (PassiveNode) Rect() Rect                { return Rect{} }
func (PassiveNode) NeedsLayout() bool         { return false }
func (PassiveNode) NeedsPaint() bool          { return false }
func (PassiveNode) DirtyRects() []Rect        { return nil }
func (PassiveNode) ClearDirty()               {}

// RenderVisibleIntoLeaf renders a vertical viewport of node into dst.
//
// It returns the node's total height and the clamped offset used. Nodes with a
// custom RenderVisibleInto method can avoid rendering off-screen content.
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

// RenderBottomIntoLeaf renders the bottom-aligned viewport of node into dst.
//
// It returns the node's total height and the offset of the visible window.
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

// ApproxHeightLeaf returns a cheap height estimate when a node provides one.
//
// Nodes without an approximate-height hook are measured normally.
func ApproxHeightLeaf(node Node, width int) int {
	if node == nil {
		return 0
	}
	if cached, ok := node.(interface{ ApproxHeight(int) int }); ok {
		return cached.ApproxHeight(width)
	}
	return node.Measure(nil, NewConstraints(width, 0)).H
}

// RenderCachedLeaf renders node to a surface, using a node cache when present.
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

// AsNode returns node unchanged while documenting declarative node intent.
//
// It is useful at construction sites where the concrete value should be treated
// as a retained Node without hiding any of the node's optional interfaces.
func AsNode(node Node) Node {
	return node
}

// PaintNodeSurface lays out and paints node into a new Surface.
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
