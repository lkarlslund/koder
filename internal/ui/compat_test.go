package ui

type Element = any

func PaintElementSurface(ctx *Context, element Element, bounds Rect) Surface {
	return PaintNodeSurface(ctx, AsNode(element), bounds)
}

func InvalidateElementCaches(ctx *Context, element Element) {
	InvalidateNodeCaches(ctx, AsNode(element))
}

func renderElementInto(ctx *Context, element Element, bounds Rect, dst *Surface) {
	paintNodeInto(ctx, AsNode(element), bounds, dst)
}

func ElementVisible(element Element) bool {
	return NodeVisible(AsNode(element))
}
