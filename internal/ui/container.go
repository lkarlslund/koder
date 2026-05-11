package ui

// Container exposes a node's children for traversal.
type Container interface {
	Children() []Node
}

// MutableContainer supports declarative and imperative child mutation.
type MutableContainer interface {
	Container
	Add(child Node)
	Insert(index int, child Node)
	Remove(index int)
	Replace(index int, child Node)
	Clear()
}

// RetainedColumn is a mutable vertical container for retained nodes.
type RetainedColumn struct {
	BaseNode
	children []Node
	spacing  int
}

// NewRetainedColumn creates an empty vertical retained container.
func NewRetainedColumn(spacing int) *RetainedColumn {
	return &RetainedColumn{spacing: max(0, spacing)}
}

// Add appends child to the column.
func (c *RetainedColumn) Add(child Node) {
	c.children = append(c.children, child)
	c.MarkLayoutDirty()
}

// Insert inserts child at index, clamped to the current child range.
func (c *RetainedColumn) Insert(index int, child Node) {
	index = max(0, min(index, len(c.children)))
	c.children = append(c.children[:index], append([]Node{child}, c.children[index:]...)...)
	c.MarkLayoutDirty()
}

// Remove removes the child at index when present.
func (c *RetainedColumn) Remove(index int) {
	if index < 0 || index >= len(c.children) {
		return
	}
	c.children = append(c.children[:index], c.children[index+1:]...)
	c.MarkLayoutDirty()
}

// Replace replaces the child at index when present.
func (c *RetainedColumn) Replace(index int, child Node) {
	if index < 0 || index >= len(c.children) {
		return
	}
	c.children[index] = child
	c.MarkLayoutDirty()
}

// Clear removes all children.
func (c *RetainedColumn) Clear() {
	c.children = nil
	c.MarkLayoutDirty()
}

// Children returns a copy of the column's child nodes.
func (c *RetainedColumn) Children() []Node {
	out := make([]Node, len(c.children))
	copy(out, c.children)
	return out
}

// Measure measures the column using vertical flex layout.
func (c *RetainedColumn) Measure(ctx *Context, constraints Constraints) Size {
	if c == nil {
		return constraints.Clamp(Size{})
	}
	main := 0
	cross := 0
	visible := 0
	for _, child := range c.children {
		if child == nil {
			continue
		}
		size := child.Measure(ctx, NewConstraints(constraints.MaxW, 0))
		if size.W <= 0 && size.H <= 0 {
			continue
		}
		if visible > 0 {
			main += c.spacing
		}
		visible++
		main += size.H
		cross = max(cross, size.W)
	}
	return constraints.Clamp(Size{W: cross, H: main})
}

// Layout assigns child rectangles in column order.
func (c *RetainedColumn) Layout(ctx *Context, rect Rect) {
	if c == nil {
		return
	}
	c.BaseNode.Layout(ctx, rect)
	y := rect.Y
	for _, child := range c.children {
		if child == nil {
			continue
		}
		size := child.Measure(ctx, NewConstraints(rect.W, 0))
		if size.W <= 0 && size.H <= 0 {
			child.Layout(ctx, Rect{})
			continue
		}
		child.Layout(ctx, Rect{X: rect.X, Y: y, W: rect.W, H: size.H})
		y += size.H + c.spacing
	}
}

// Prepare forwards preparation to child nodes.
func (c *RetainedColumn) Prepare(ctx *Context) {
	if c == nil {
		return
	}
	for _, child := range c.children {
		if child != nil {
			child.Prepare(ctx)
		}
	}
}

// Paint paints children using vertical flex layout.
func (c *RetainedColumn) Paint(ctx *Context, canvas Canvas) {
	if c == nil || canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	y := canvas.origin.Y
	for _, child := range c.children {
		if child == nil {
			continue
		}
		size := child.Measure(ctx, NewConstraints(canvas.Width(), 0))
		if size.W <= 0 && size.H <= 0 {
			continue
		}
		child.Layout(ctx, Rect{X: canvas.origin.X, Y: y, W: canvas.Width(), H: size.H})
		child.Prepare(ctx)
		child.Paint(ctx, canvas)
		y += size.H + c.spacing
	}
}
