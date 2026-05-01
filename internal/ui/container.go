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
}

// Insert inserts child at index, clamped to the current child range.
func (c *RetainedColumn) Insert(index int, child Node) {
	index = max(0, min(index, len(c.children)))
	c.children = append(c.children[:index], append([]Node{child}, c.children[index:]...)...)
}

// Remove removes the child at index when present.
func (c *RetainedColumn) Remove(index int) {
	if index < 0 || index >= len(c.children) {
		return
	}
	c.children = append(c.children[:index], c.children[index+1:]...)
}

// Replace replaces the child at index when present.
func (c *RetainedColumn) Replace(index int, child Node) {
	if index < 0 || index >= len(c.children) {
		return
	}
	c.children[index] = child
}

// Clear removes all children.
func (c *RetainedColumn) Clear() {
	c.children = nil
}

// Children returns a copy of the column's child nodes.
func (c *RetainedColumn) Children() []Node {
	out := make([]Node, len(c.children))
	copy(out, c.children)
	return out
}

// Measure measures the column using vertical flex layout.
func (c *RetainedColumn) Measure(ctx *Context, constraints Constraints) Size {
	items := make([]Child, 0, len(c.children))
	for _, child := range c.children {
		if child != nil {
			items = append(items, Fixed(child))
		}
	}
	return NewFlexBox(DirectionVertical, items, c.spacing).Measure(ctx, constraints)
}

// Paint paints children using vertical flex layout.
func (c *RetainedColumn) Paint(ctx *Context, canvas Canvas) {
	if c == nil || canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	items := make([]Child, 0, len(c.children))
	for _, child := range c.children {
		if child != nil {
			items = append(items, Fixed(child))
		}
	}
	paintNodeInto(ctx, AsNode(NewFlexBox(DirectionVertical, items, c.spacing)), Rect{
		X: canvas.origin.X,
		Y: canvas.origin.Y,
		W: canvas.Width(),
		H: canvas.Height(),
	}, canvas.surface)
}
