package uitest

import (
	"strings"

	"github.com/lkarlslund/koder/internal/ui"
)

func SurfaceText(s ui.Surface) string {
	return strings.Join(s.Lines(), "\n")
}

func RenderElementText(ctx *ui.Context, element ui.Element, width, height int) string {
	return SurfaceText(ui.RenderSurface(ctx, element, width, height))
}
