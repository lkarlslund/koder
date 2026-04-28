package ui

import "strings"

func SurfaceText(s Surface) string {
	return strings.Join(s.Lines(), "\n")
}

func RenderNode(ctx *Context, node any, width, height int) string {
	return SurfaceText(RenderSurface(ctx, AsNode(node), width, height))
}
