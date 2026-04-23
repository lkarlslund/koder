package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/lkarlslund/koder/internal/theme"
)

type Sidebar struct {
	Child  Element
	Height int
	Width  int
}

func (s Sidebar) content(ctx *Context, width int) string {
	return s.render(ctx, width).String()
}

func (s Sidebar) render(ctx *Context, width int) Surface {
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
	return SurfaceFromString(style.Render(strings.TrimRight(content, "\n")))
}

func (s Sidebar) View(palette theme.Palette) string {
	width := s.Width
	if width <= 0 {
		width = 30
	}
	return s.content(&Context{Palette: palette}, width)
}

func (s Sidebar) Measure(ctx *Context, constraints Constraints) Size {
	width := s.Width
	if width <= 0 {
		width = constraints.MaxW
	}
	if width <= 0 {
		width = 30
	}
	return constraints.Clamp(s.render(ctx, width).Size())
}

func (s Sidebar) Render(ctx *Context, bounds Rect) Surface {
	width := s.Width
	if width <= 0 {
		width = bounds.W
	}
	if width <= 0 {
		width = 30
	}
	return s.render(ctx, width).normalize(bounds.W, bounds.H)
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
	return constraints.Clamp(l.element().Measure(ctx, constraints))
}

func (l BodyLayout) Render(ctx *Context, bounds Rect) Surface {
	return l.element().Render(ctx, bounds)
}

func (l BodyLayout) element() Element {
	main := Inset{Padding: SymmetricInsets(1, 0), Child: l.MainElement}
	if !l.ShowSidebar || l.SidebarElement == nil {
		return main
	}
	return Split{
		Direction:  SplitHorizontal,
		First:      main,
		Second:     l.SidebarElement,
		SecondFixed: l.sidebarWidth(),
	}
}

func (l BodyLayout) sidebarWidth() int {
	sidebar, ok := l.SidebarElement.(Sidebar)
	if !ok {
		return 0
	}
	return sidebar.Width
}

type Footer struct {
	Parts    []string
	Elements []Element
}

func (f Footer) View() string {
	return f.render().String()
}

func (f Footer) render() Surface {
	return SurfaceFromString(lipgloss.NewStyle().
		BorderTop(true).
		Padding(0, 1).
		Render(lipgloss.JoinVertical(lipgloss.Left, f.Parts...)))
}

func (f Footer) Measure(ctx *Context, constraints Constraints) Size {
	if len(f.Elements) == 0 {
		return constraints.Clamp(f.render().Size())
	}
	content := Column{Children: f.children()}
	size := content.Measure(ctx, constraints)
	return constraints.Clamp(Size{W: size.W + 2, H: size.H + 1})
}

func (f Footer) Render(ctx *Context, bounds Rect) Surface {
	if len(f.Elements) == 0 {
		return f.render().normalize(bounds.W, bounds.H)
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
