package ui

import "strings"

func SurfaceText(s Surface) string {
	return strings.Join(s.Lines(), "\n")
}

func RenderElement(ctx *Context, element Element, width, height int) string {
	return SurfaceText(RenderSurface(ctx, AsNode(element), width, height))
}
