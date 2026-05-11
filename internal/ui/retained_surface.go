package ui

// RetainedSurface paints a retained node tree into a cached surface.
type RetainedSurface struct {
	node    Node
	bounds  Rect
	surface Surface
	valid   bool
}

// NewRetainedSurface constructs a retained surface for node.
func NewRetainedSurface(node Node) *RetainedSurface {
	return &RetainedSurface{node: node}
}

// SetNode replaces the retained root node.
func (r *RetainedSurface) SetNode(node Node) {
	if r == nil || r.node == node {
		return
	}
	r.node = node
	r.Invalidate()
}

// Invalidate forces the next paint to repaint the whole retained tree.
func (r *RetainedSurface) Invalidate() {
	if r == nil {
		return
	}
	r.valid = false
}

// Dirty reports whether the retained tree needs repainting.
func (r *RetainedSurface) Dirty() bool {
	if r == nil || !r.valid {
		return true
	}
	return NodePending(r.node)
}

// Surface returns the retained tree as a cached surface.
func (r *RetainedSurface) Surface(ctx *Context, bounds Rect) Surface {
	if r == nil || r.node == nil {
		return Surface{}
	}
	if r.valid && r.bounds == bounds && !r.Dirty() {
		return r.surface
	}
	surface := TransparentSurface(bounds.W, bounds.H)
	rects := r.PaintInto(ctx, bounds, &surface)
	if len(rects) > 0 {
		surface = surface.WithDirtyRects(rects...)
	}
	return surface
}

// PaintInto paints the retained tree into dst and returns local dirty rects.
func (r *RetainedSurface) PaintInto(ctx *Context, bounds Rect, dst *Surface) []Rect {
	if r == nil || r.node == nil || dst == nil {
		return nil
	}
	local := Rect{W: bounds.W, H: bounds.H}
	r.node.Layout(ctx, local)
	r.node.Prepare(ctx)
	fullPaint := !r.valid || r.bounds != local
	canvas := NewCanvas(dst, bounds)
	if !fullPaint && r.valid && r.bounds == local &&
		r.surface.SurfaceWidth() == local.W && r.surface.SurfaceHeight() == local.H {
		canvas.BlitSurface(0, 0, r.surface)
	}
	if fullPaint {
		r.node.Paint(ctx, canvas)
	} else {
		PaintDirtyNode(ctx, canvas, r.node)
	}
	rects := CollectNodeDamage(r.node)
	ClearNodeDirty(r.node)
	r.bounds = local
	if bounds.X == 0 && bounds.Y == 0 && dst.SurfaceWidth() == local.W && dst.SurfaceHeight() == local.H {
		r.surface = *dst
	} else {
		r.surface = canvas.Snapshot()
	}
	r.valid = true
	return rects
}

// NodePending reports whether node or any retained child has pending paint work.
func NodePending(node Node) bool {
	if node == nil {
		return true
	}
	if dirty, ok := node.(interface{ Dirty() bool }); ok && dirty.Dirty() {
		return true
	}
	if node.NeedsLayout() || node.NeedsPaint() {
		return true
	}
	for _, child := range NodeChildren(node) {
		if NodePending(child) {
			return true
		}
	}
	return false
}

// PaintDirtyNode paints only dirty leaf nodes in node's retained tree.
func PaintDirtyNode(ctx *Context, canvas Canvas, node Node) {
	if node == nil || node.Rect().Empty() {
		return
	}
	if node.NeedsPaint() {
		node.Paint(ctx, canvas.Subrect(node.Rect()))
		return
	}
	if children := NodeChildren(node); len(children) > 0 {
		for _, child := range children {
			PaintDirtyNode(ctx, canvas, child)
		}
		return
	}
}

// CollectNodeDamage returns dirty rectangles for node and retained descendants.
func CollectNodeDamage(node Node) []Rect {
	damage := DamageSet{}
	collectNodeDamage(&damage, node)
	return damage.Rects()
}

func collectNodeDamage(damage *DamageSet, node Node) {
	if damage == nil || node == nil {
		return
	}
	damage.AddAll(node.DirtyRects())
	for _, child := range NodeChildren(node) {
		collectNodeDamage(damage, child)
	}
}

// ClearNodeDirty clears dirty state from node and retained descendants.
func ClearNodeDirty(node Node) {
	if node == nil {
		return
	}
	node.ClearDirty()
	for _, child := range NodeChildren(node) {
		ClearNodeDirty(child)
	}
}

// NodeChildren returns retained child nodes when node is a container.
func NodeChildren(node Node) []Node {
	if node == nil {
		return nil
	}
	container, ok := node.(Container)
	if !ok {
		return nil
	}
	return container.Children()
}
