package ui

type FlexNodeChild struct {
	Node Node
	Flex int
}

type FlexNode struct {
	BaseNode
	Direction FlexDirection
	Spacing   int
	Children  []FlexNodeChild
}

func (n *FlexNode) ChildNodes() []Node {
	if n == nil || len(n.Children) == 0 {
		return nil
	}
	out := make([]Node, 0, len(n.Children))
	for _, child := range n.Children {
		if child.Node != nil {
			out = append(out, child.Node)
		}
	}
	return out
}

func (n *FlexNode) Measure(ctx *Context, constraints Constraints) Size {
	if n == nil {
		return constraints.Clamp(Size{})
	}
	main := 0
	cross := 0
	visible := 0
	for _, child := range n.Children {
		if child.Node == nil {
			continue
		}
		size := n.measureChild(ctx, child.Node, constraints)
		if size.W <= 0 && size.H <= 0 && child.Flex <= 0 {
			continue
		}
		if visible > 0 {
			main += n.Spacing
		}
		visible++
		if n.Direction == DirectionVertical {
			if child.Flex <= 0 {
				main += size.H
			}
			cross = max(cross, size.W)
			continue
		}
		if child.Flex <= 0 {
			main += size.W
		}
		cross = max(cross, size.H)
	}
	if n.Direction == DirectionVertical {
		return constraints.Clamp(Size{W: cross, H: main})
	}
	return constraints.Clamp(Size{W: main, H: cross})
}

func (n *FlexNode) Layout(ctx *Context, rect Rect) {
	n.BaseNode.Layout(ctx, rect)
	if n == nil {
		return
	}
	active := n.activeChildren(ctx, rect)
	if len(active) == 0 {
		return
	}
	availableMain := rect.W
	if n.Direction == DirectionVertical {
		availableMain = rect.H
	}
	availableMain = max(0, availableMain)
	spacingTotal := max(0, len(active)-1) * max(0, n.Spacing)
	remaining := max(0, availableMain-spacingTotal)
	flexWeight := 0
	for idx := range active {
		if active[idx].flex > 0 {
			flexWeight += active[idx].flex
			continue
		}
		remaining = max(0, remaining-active[idx].main)
	}

	offset := 0
	for idx := range active {
		mainSize := active[idx].main
		if active[idx].flex > 0 {
			if flexWeight <= 0 {
				mainSize = 0
			} else if idx == len(active)-1 {
				mainSize = remaining
			} else {
				mainSize = remaining * active[idx].flex / flexWeight
			}
			remaining -= mainSize
			flexWeight -= active[idx].flex
		}
		childRect := Rect{X: rect.X, Y: rect.Y, W: rect.W, H: rect.H}
		if n.Direction == DirectionVertical {
			childRect.Y += offset
			childRect.H = mainSize
		} else {
			childRect.X += offset
			childRect.W = mainSize
		}
		active[idx].node.Layout(ctx, childRect)
		offset += mainSize + n.Spacing
	}
}

func (n *FlexNode) Prepare(ctx *Context) {
	if n == nil {
		return
	}
	for _, child := range n.Children {
		if child.Node != nil {
			child.Node.Prepare(ctx)
		}
	}
}

func (n *FlexNode) Paint(ctx *Context, canvas Canvas) {
	if n == nil {
		return
	}
	for _, child := range n.Children {
		if child.Node == nil || child.Node.Rect().Empty() {
			continue
		}
		child.Node.Paint(ctx, canvas.Subrect(child.Node.Rect()))
	}
}

type flexNodeLayoutChild struct {
	node Node
	flex int
	main int
}

func (n *FlexNode) activeChildren(ctx *Context, rect Rect) []flexNodeLayoutChild {
	active := make([]flexNodeLayoutChild, 0, len(n.Children))
	for _, child := range n.Children {
		if child.Node == nil {
			continue
		}
		size := n.measureChild(ctx, child.Node, NewConstraints(rect.W, rect.H))
		if child.Flex <= 0 {
			if n.Direction == DirectionVertical && size.H <= 0 {
				continue
			}
			if n.Direction == DirectionHorizontal && size.W <= 0 {
				continue
			}
		}
		main := size.W
		if n.Direction == DirectionVertical {
			main = size.H
		}
		if main < 0 {
			main = 0
		}
		active = append(active, flexNodeLayoutChild{
			node: child.Node,
			flex: max(0, child.Flex),
			main: main,
		})
	}
	return active
}

func (n *FlexNode) measureChild(ctx *Context, child Node, constraints Constraints) Size {
	if child == nil {
		return Size{}
	}
	if n.Direction == DirectionVertical {
		return child.Measure(ctx, NewConstraints(constraints.MaxW, 0))
	}
	return child.Measure(ctx, NewConstraints(0, constraints.MaxH))
}
