package ui

type Container interface {
	Add(child Node)
	Insert(index int, child Node)
	Remove(index int)
	Replace(index int, child Node)
	Clear()
	Children() []Node
}

type RetainedColumn struct {
	BaseNode
	children []Node
	spacing  int
}

func NewRetainedColumn(spacing int) *RetainedColumn {
	return &RetainedColumn{spacing: max(0, spacing)}
}

func (c *RetainedColumn) Add(child Node) {
	c.children = append(c.children, child)
}

func (c *RetainedColumn) Insert(index int, child Node) {
	index = max(0, min(index, len(c.children)))
	c.children = append(c.children[:index], append([]Node{child}, c.children[index:]...)...)
}

func (c *RetainedColumn) Remove(index int) {
	if index < 0 || index >= len(c.children) {
		return
	}
	c.children = append(c.children[:index], c.children[index+1:]...)
}

func (c *RetainedColumn) Replace(index int, child Node) {
	if index < 0 || index >= len(c.children) {
		return
	}
	c.children[index] = child
}

func (c *RetainedColumn) Clear() {
	c.children = nil
}

func (c *RetainedColumn) Children() []Node {
	out := make([]Node, len(c.children))
	copy(out, c.children)
	return out
}

func (c *RetainedColumn) Measure(ctx *Context, constraints Constraints) Size {
	items := make([]Child, 0, len(c.children))
	for _, child := range c.children {
		if child != nil {
			items = append(items, Fixed(child))
		}
	}
	return FlexBox{Direction: DirectionVertical, Children: items, Spacing: c.spacing}.Measure(ctx, constraints)
}

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
	paintNodeInto(ctx, AsNode(FlexBox{Direction: DirectionVertical, Children: items, Spacing: c.spacing}), Rect{
		X: canvas.origin.X,
		Y: canvas.origin.Y,
		W: canvas.Width(),
		H: canvas.Height(),
	}, canvas.surface)
}
