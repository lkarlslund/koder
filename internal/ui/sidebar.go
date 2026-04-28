package ui

import "strings"

type Sidebar struct {
	Child  Element
	Height int
	Width  int
}

func (s Sidebar) render(ctx *Context, width int) Surface {
	height := s.Height
	contentHeight := 0
	var content Surface
	if s.Child != nil {
		contentBounds := Rect{W: max(0, width-1), H: height}
		if height <= 0 {
			contentHeight = s.Child.Measure(ctx, NewConstraints(contentBounds.W, 0)).H
			contentBounds.H = contentHeight
		}
		content = PaintElementSurface(ctx, s.Child, contentBounds)
	}
	if height <= 0 {
		height = max(1, contentHeight)
	}
	surface := BlankSurface(width, height)
	fillStyle := CellStyle{FG: cellColor(ctx.Palette.SidebarForeground), BG: cellColor(ctx.Palette.SidebarBackground)}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			surface.setCell(x, y, blankCell(fillStyle))
		}
	}
	if s.Child != nil && width > 0 {
		surface = surface.placeAt(1, 0, content)
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

func (s Sidebar) Paint(ctx *Context, canvas Canvas) {
	width := canvas.Width()
	height := canvas.Height()
	if width <= 0 || height <= 0 {
		return
	}
	fillStyle := CellStyle{FG: cellColor(ctx.Palette.SidebarForeground), BG: cellColor(ctx.Palette.SidebarBackground)}
	canvas.Fill(Rect{W: width, H: height}, fillStyle)
	if s.Child == nil || width <= 1 {
		return
	}
	renderElementInto(ctx, s.Child, Rect{
		X: canvas.origin.X + 1,
		Y: canvas.origin.Y,
		W: max(0, width-1),
		H: height,
	}, canvas.surface)
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
	return renderOwnedCanvas(ctx, bounds, l)
}

func (l BodyLayout) element() Element {
	main := Inset{Padding: SymmetricInsets(1, 0), Child: l.MainElement}
	if !l.ShowSidebar || l.SidebarElement == nil {
		return main
	}
	return FlexBox{
		Direction: DirectionHorizontal,
		Children: []Child{
			Flex(main, 1),
			{
				Element: l.SidebarElement,
				Basis:   l.sidebarWidth(),
			},
		},
	}
}

func (l BodyLayout) sidebarWidth() int {
	sidebar, ok := l.SidebarElement.(Sidebar)
	if !ok {
		return 0
	}
	return sidebar.Width
}

func (l BodyLayout) Paint(ctx *Context, canvas Canvas) {
	if canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	renderElementInto(ctx, l.element(), Rect{
		X: canvas.origin.X,
		Y: canvas.origin.Y,
		W: canvas.Width(),
		H: canvas.Height(),
	}, canvas.surface)
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
	return f.renderContent(&Context{}, FlexBox{Direction: DirectionVertical, Children: children})
}

func (f Footer) Measure(ctx *Context, constraints Constraints) Size {
	if len(f.Elements) == 0 {
		return constraints.Clamp(f.render().Size())
	}
	content := FlexBox{Direction: DirectionVertical, Children: f.children()}
	size := content.Measure(ctx, constraints)
	return constraints.Clamp(Size{W: size.W + 2, H: size.H + 1})
}

func (f Footer) Render(ctx *Context, bounds Rect) Surface {
	return renderOwnedCanvas(ctx, bounds, f)
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
		rendered := PaintElementSurface(ctx, content, Rect{W: max(0, width-2), H: max(0, height-1)})
		surface = surface.placeAt(1, 1, rendered)
	}
	return surface
}

func (f Footer) Paint(ctx *Context, canvas Canvas) {
	if canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	content := FlexBox{Direction: DirectionVertical, Children: f.children()}
	if len(f.Elements) == 0 {
		content = FlexBox{Direction: DirectionVertical, Children: make([]Child, 0, len(f.Parts))}
		for _, part := range f.Parts {
			content.Children = append(content.Children, Fixed(Label{Text: part}))
		}
	}
	canvas.BlitSurface(0, 0, f.renderContent(ctx, content).normalize(canvas.Width(), canvas.Height()))
}
