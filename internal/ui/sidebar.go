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
	Main           string
	MainElement    Element
	Sidebar        string
	SidebarElement Element
	ShowSidebar    bool
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
	mainChild := Element(Static{Content: l.Main})
	if l.MainElement != nil {
		mainChild = l.MainElement
	}
	children := []Child{
		Flex(Inset{Padding: SymmetricInsets(1, 0), Child: mainChild}, 1),
	}
	if l.ShowSidebar {
		sidebarChild := Element(Static{Content: l.Sidebar})
		if l.SidebarElement != nil {
			sidebarChild = l.SidebarElement
		}
		children = append(children, Fixed(sidebarChild))
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
