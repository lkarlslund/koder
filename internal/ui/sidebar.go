package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/lkarlslund/koder/internal/theme"
)

type Sidebar struct {
	Content string
	Height  int
}

func (s Sidebar) content(palette theme.Palette) string {
	style := lipgloss.NewStyle().
		Width(30).
		Padding(0, 1).
		Background(palette.SidebarBackground).
		Foreground(palette.SidebarForeground).
		BorderLeft(true).
		BorderForeground(palette.SidebarBorder)
	if s.Height > 0 {
		style = style.Height(s.Height).MaxHeight(s.Height)
	}
	return style.Render(strings.TrimRight(s.Content, "\n"))
}

func (s Sidebar) View(palette theme.Palette) string {
	return s.content(palette)
}

func (s Sidebar) Measure(ctx *Context, constraints Constraints) Size {
	return constraints.Clamp(SurfaceFromString(s.content(ctx.Palette)).Size())
}

func (s Sidebar) Render(ctx *Context, bounds Rect) Surface {
	return SurfaceFromString(s.content(ctx.Palette)).normalize(bounds.W, bounds.H)
}

type BodyLayout struct {
	Main        string
	Sidebar     string
	ShowSidebar bool
}

func (l BodyLayout) View() string {
	main := lipgloss.NewStyle().Padding(0, 1).Render(l.Main)
	if !l.ShowSidebar {
		return main
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, main, l.Sidebar)
}

func (l BodyLayout) Measure(ctx *Context, constraints Constraints) Size {
	return constraints.Clamp(SurfaceFromString(l.View()).Size())
}

func (l BodyLayout) Render(ctx *Context, bounds Rect) Surface {
	children := []Child{
		Flex(Inset{Padding: SymmetricInsets(1, 0), Child: Static{Content: l.Main}}, 1),
	}
	if l.ShowSidebar {
		children = append(children, Fixed(Static{Content: l.Sidebar}))
	}
	return Row{Children: children}.Render(ctx, bounds)
}

type Footer struct {
	Parts []string
}

func (f Footer) View() string {
	return lipgloss.NewStyle().
		BorderTop(true).
		Padding(0, 1).
		Render(lipgloss.JoinVertical(lipgloss.Left, f.Parts...))
}

func (f Footer) Measure(ctx *Context, constraints Constraints) Size {
	return constraints.Clamp(SurfaceFromString(f.View()).Size())
}

func (f Footer) Render(ctx *Context, bounds Rect) Surface {
	return SurfaceFromString(f.View()).normalize(bounds.W, bounds.H)
}
