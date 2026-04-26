package ui

type Container interface {
	Add(child Element)
	Insert(index int, child Element)
	Remove(index int)
	Replace(index int, child Element)
	Clear()
	Children() []Element
}

type RetainedColumn struct {
	children []Element
	spacing  int
}

func NewRetainedColumn(spacing int) *RetainedColumn {
	return &RetainedColumn{spacing: max(0, spacing)}
}

func (c *RetainedColumn) Add(child Element) {
	c.children = append(c.children, child)
}

func (c *RetainedColumn) Insert(index int, child Element) {
	index = max(0, min(index, len(c.children)))
	c.children = append(c.children[:index], append([]Element{child}, c.children[index:]...)...)
}

func (c *RetainedColumn) Remove(index int) {
	if index < 0 || index >= len(c.children) {
		return
	}
	c.children = append(c.children[:index], c.children[index+1:]...)
}

func (c *RetainedColumn) Replace(index int, child Element) {
	if index < 0 || index >= len(c.children) {
		return
	}
	c.children[index] = child
}

func (c *RetainedColumn) Clear() {
	c.children = nil
}

func (c *RetainedColumn) Children() []Element {
	out := make([]Element, len(c.children))
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

func (c *RetainedColumn) Render(ctx *Context, bounds Rect) Surface {
	items := make([]Child, 0, len(c.children))
	for _, child := range c.children {
		if child != nil {
			items = append(items, Fixed(child))
		}
	}
	return FlexBox{Direction: DirectionVertical, Children: items, Spacing: c.spacing}.Render(ctx, bounds)
}
