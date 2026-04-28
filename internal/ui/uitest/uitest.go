package uitest

import (
	"strings"

	"github.com/lkarlslund/koder/internal/ui"
)

func SurfaceText(s ui.Surface) string {
	return strings.Join(s.Lines(), "\n")
}

func RenderNodeText(ctx *ui.Context, node ui.Node, width, height int) string {
	return SurfaceText(ui.RenderSurface(ctx, node, width, height))
}
