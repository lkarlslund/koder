package ui

import "strings"

type Sidebar struct {
	Child  Element
	Height int
	Width  int
}

func (s Sidebar) content(ctx *Context, width int) string {
	return s.render(ctx, width).String()
}

func (s Sidebar) render(ctx *Context, width int) Surface {
	height := s.Height
	contentHeight := 0
	var content Surface
	if s.Child != nil {
		contentBounds := Rect{W: max(0, width-3), H: height}
		if height <= 0 {
			contentHeight = s.Child.Measure(ctx, NewConstraints(contentBounds.W, 0)).H
			contentBounds.H = contentHeight
		}
		content = s.Child.Render(ctx, contentBounds)
	}
	if height <= 0 {
		height = max(1, contentHeight)
	}
	surface := BlankSurface(width, height)
	fillStyle := CellStyle{FG: ctx.Palette.SidebarForeground, BG: ctx.Palette.SidebarBackground}
	borderStyle := CellStyle{FG: ctx.Palette.SidebarBorder, BG: ctx.Palette.SidebarBackground}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			surface.setCell(x, y, Cell{Text: " ", Width: 1, Style: fillStyle})
		}
		if width > 0 {
			surface.setCell(0, y, Cell{Text: "│", Width: 1, Style: borderStyle})
		}
	}
	if s.Child != nil && width > 2 {
		surface = surface.placeAt(2, 0, content)
	}
	return surface
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
		Direction:   SplitHorizontal,
		First:       main,
		Second:      l.SidebarElement,
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

func (f Footer) render() Surface {
	children := make([]Child, 0, len(f.Parts))
	for _, part := range f.Parts {
		children = append(children, Fixed(Label{Text: part}))
	}
	return f.renderContent(&Context{}, Column{Children: children})
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
	content := Column{Children: f.children()}
	if len(f.Elements) == 0 {
		content = Column{Children: make([]Child, 0, len(f.Parts))}
		for _, part := range f.Parts {
			content.Children = append(content.Children, Fixed(Label{Text: part}))
		}
	}
	return f.renderContent(ctx, content).normalize(bounds.W, bounds.H)
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

func (f Footer) renderContent(ctx *Context, content Element) Surface {
	width := 0
	height := 1
	if content != nil {
		size := content.Measure(ctx, NewConstraints(0, 0))
		width = size.W + 2
		height += size.H
	}
	if width <= 0 {
		width = 2
	}
	surface := BlankSurface(width, height)
	borderStyle := CellStyle{}
	if width > 0 {
		surface.WriteText(0, 0, strings.Repeat("─", width), borderStyle)
	}
	if content != nil {
		rendered := content.Render(ctx, Rect{W: max(0, width-2), H: max(0, height-1)})
		surface = surface.placeAt(1, 1, rendered)
	}
	return surface
}
