package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/lkarlslund/koder/internal/theme"
)

type Sidebar struct {
	Child  Element
	Height int
}

func (s Sidebar) content(ctx *Context, width int) string {
	style := lipgloss.NewStyle().
		Width(width).
		Padding(0, 1).
		Background(ctx.Palette.SidebarBackground).
		Foreground(ctx.Palette.SidebarForeground).
		BorderLeft(true).
		BorderForeground(ctx.Palette.SidebarBorder)
	if s.Height > 0 {
		style = style.Height(s.Height).MaxHeight(s.Height)
	}
	content := ""
	if s.Child != nil {
		content = RenderElement(ctx, s.Child, max(0, width-3), s.Height)
	}
	return style.Render(strings.TrimRight(content, "\n"))
}

func (s Sidebar) View(palette theme.Palette) string {
	return s.content(&Context{Palette: palette}, 30)
}

func (s Sidebar) Measure(ctx *Context, constraints Constraints) Size {
	width := constraints.MaxW
	if width <= 0 {
		width = 30
	}
	return constraints.Clamp(SurfaceFromString(s.content(ctx, width)).Size())
}

func (s Sidebar) Render(ctx *Context, bounds Rect) Surface {
	width := bounds.W
	if width <= 0 {
		width = 30
	}
	return SurfaceFromString(s.content(ctx, width)).normalize(bounds.W, bounds.H)
}

type BodyLayout struct {
	MainElement    Element
	SidebarElement Element
	ShowSidebar    bool
}

func (l BodyLayout) View() string {
	main := ""
	if l.MainElement != nil {
		main = RenderElement(&Context{}, Inset{Padding: SymmetricInsets(1, 0), Child: l.MainElement}, 0, 0)
	}
	if !l.ShowSidebar {
		return main
	}
	sidebar := ""
	if l.SidebarElement != nil {
		sidebar = RenderElement(&Context{}, l.SidebarElement, 0, 0)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, main, sidebar)
}

func (l BodyLayout) Measure(ctx *Context, constraints Constraints) Size {
	return constraints.Clamp(SurfaceFromString(l.View()).Size())
}

func (l BodyLayout) Render(ctx *Context, bounds Rect) Surface {
	children := []Child{
		Flex(Inset{Padding: SymmetricInsets(1, 0), Child: l.MainElement}, 1),
	}
	if l.ShowSidebar {
		children = append(children, Fixed(l.SidebarElement))
	}
	return Row{Children: children}.Render(ctx, bounds)
}

type Footer struct {
	Parts    []string
	Elements []Element
}

func (f Footer) View() string {
	return lipgloss.NewStyle().
		BorderTop(true).
		Padding(0, 1).
		Render(lipgloss.JoinVertical(lipgloss.Left, f.Parts...))
}

func (f Footer) Measure(ctx *Context, constraints Constraints) Size {
	if len(f.Elements) == 0 {
		return constraints.Clamp(SurfaceFromString(f.View()).Size())
	}
	content := Column{Children: f.children()}
	size := content.Measure(ctx, constraints)
	return constraints.Clamp(Size{W: size.W + 2, H: size.H + 1})
}

func (f Footer) Render(ctx *Context, bounds Rect) Surface {
	if len(f.Elements) == 0 {
		return SurfaceFromString(f.View()).normalize(bounds.W, bounds.H)
	}
	content := Column{Children: f.children()}
	width := bounds.W
	if width <= 0 {
		width = content.Measure(ctx, NewConstraints(0, bounds.H)).W + 2
	}
	rendered := lipgloss.NewStyle().
		BorderTop(true).
		Padding(0, 1).
		Width(width).
		Render(RenderElement(ctx, content, max(0, width-2), 0))
	return SurfaceFromString(rendered).normalize(bounds.W, bounds.H)
}

func (f Footer) children() []Child {
	children := make([]Child, 0, len(f.Elements))
	for _, child := range f.Elements {
		if child == nil {
			continue
		}
		children = append(children, Fixed(child))
	}
	return children
}
