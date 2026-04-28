package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type Border struct {
	BaseNode
	Child         Node
	Width         int
	Height        int
	Padding       Insets
	Background    lipgloss.Color
	Foreground    lipgloss.Color
	BorderColor   lipgloss.Color
	TopLabel      string
	EndLabel      string
	EndControlID  string
	TopLabelStyle CellStyle
	EndLabelStyle CellStyle
	Style         lipgloss.Border
	BorderLeft    bool
	BorderRight   bool
	BorderTop     bool
	BorderBottom  bool
}

func (b Border) Measure(ctx *Context, constraints Constraints) Size {
	width := constraints.MaxW
	if b.Width > 0 {
		width = b.Width
	}
	inset := b.insets()
	childSize := Size{}
	if b.Child != nil {
		childSize = b.Child.Measure(ctx, Constraints{
			MaxW: max(0, width-inset.Left-inset.Right),
			MaxH: max(0, constraints.MaxH-inset.Top-inset.Bottom),
		})
	}
	size := Size{
		W: childSize.W + inset.Left + inset.Right,
		H: childSize.H + inset.Top + inset.Bottom,
	}
	if b.Width > 0 {
		size.W = b.Width
	}
	if b.Height > 0 {
		size.H = b.Height
	}
	return constraints.Clamp(size)
}

func (b Border) Paint(ctx *Context, canvas Canvas) {
	borderPainter{
		border: b,
		ctx:    ctx,
		bounds: Rect{X: canvas.origin.X, Y: canvas.origin.Y, W: canvas.Width(), H: canvas.Height()},
	}.Paint(ctx, canvas)
}

type borderPainter struct {
	border Border
	ctx    *Context
	bounds Rect
}

func (p borderPainter) Paint(_ *Context, canvas Canvas) {
	b := p.border
	width := canvas.Width()
	if width <= 0 {
		width = b.Width
	}
	height := canvas.Height()
	if height <= 0 {
		height = b.Height
	}
	if width <= 0 || height <= 0 {
		return
	}
	inset := b.insets()
	fillStyle := CellStyle{FG: cellColor(b.Foreground), BG: cellColor(b.Background)}
	canvas.Fill(Rect{W: width, H: height}, fillStyle)
	border := b.borderStyle()
	borderStyle := CellStyle{FG: cellColor(b.BorderColor), BG: cellColor(b.Background)}
	if b.BorderTop && height > 0 {
		p.paintTopBorder(canvas, width, border, borderStyle)
	}
	if b.BorderBottom && height > 1 {
		p.paintBottomBorder(canvas, width, height, border, borderStyle)
	}
	if b.BorderLeft {
		for y := borderVerticalStart(b.BorderTop); y < borderVerticalEnd(height, b.BorderBottom); y++ {
			canvas.SetCell(0, y, newCell(GlyphFromString(border.Left), 1, borderStyle))
		}
	}
	if b.BorderRight && width > 1 {
		for y := borderVerticalStart(b.BorderTop); y < borderVerticalEnd(height, b.BorderBottom); y++ {
			canvas.SetCell(width-1, y, newCell(GlyphFromString(border.Right), 1, borderStyle))
		}
	}
	if b.Child != nil {
		paintNodeInto(p.ctx, b.Child, Rect{
			X: p.bounds.X + inset.Left,
			Y: p.bounds.Y + inset.Top,
			W: max(0, width-inset.Left-inset.Right),
			H: max(0, height-inset.Top-inset.Bottom),
		}, canvas.surface)
	}
}

func (p borderPainter) paintTopBorder(canvas Canvas, width int, border lipgloss.Border, borderStyle CellStyle) {
	top, endStart, endWidth := p.border.renderTopBorder(width, border, borderStyle)
	canvas.BlitSurface(0, 0, top)
	if p.ctx != nil && p.ctx.Runtime != nil && endWidth > 0 && p.border.EndControlID != "" {
		p.ctx.Runtime.Register(Control{
			ID:      p.border.EndControlID,
			Rect:    Rect{X: p.bounds.X + endStart, Y: p.bounds.Y, W: endWidth, H: 1},
			Enabled: true,
		})
	}
}

func (p borderPainter) paintBottomBorder(canvas Canvas, width, height int, border lipgloss.Border, borderStyle CellStyle) {
	canvas.BlitSurface(0, height-1, p.border.renderBottomBorder(width, border, borderStyle))
}

func (b Border) insets() Insets {
	inset := b.Padding
	if b.BorderLeft {
		inset.Left++
	}
	if b.BorderRight {
		inset.Right++
	}
	if b.BorderTop {
		inset.Top++
	}
	if b.BorderBottom {
		inset.Bottom++
	}
	return inset
}

func (b Border) borderStyle() lipgloss.Border {
	if b.Style.Top == "" && b.Style.Bottom == "" && b.Style.Left == "" && b.Style.Right == "" &&
		b.Style.TopLeft == "" && b.Style.TopRight == "" && b.Style.BottomLeft == "" && b.Style.BottomRight == "" {
		return lipgloss.NormalBorder()
	}
	return b.Style
}

func (b Border) renderTopBorder(width int, border lipgloss.Border, borderStyle CellStyle) (Surface, int, int) {
	s := BlankSurface(width, 1)
	for x := 0; x < width; x++ {
		s.setCell(x, 0, blankCell(borderStyle))
	}
	if width <= 0 {
		return s, 0, 0
	}
	if b.BorderLeft {
		s.WriteText(0, 0, border.TopLeft, borderStyle)
	}
	if b.BorderRight && width > 1 {
		s.WriteText(width-1, 0, border.TopRight, borderStyle)
	}
	start := 0
	if b.BorderLeft {
		start = 1
	}
	end := width
	if b.BorderRight {
		end--
	}
	innerWidth := max(0, end-start)
	title := PlainTruncate(b.TopLabel, innerWidth, "")
	endLabel := PlainTruncate(b.EndLabel, innerWidth, "")
	endWidth := PlainWidth(endLabel)
	titleBudget := max(0, innerWidth-endWidth)
	if PlainWidth(title) > titleBudget {
		title = PlainTruncate(title, titleBudget, "")
	}
	titleWidth := PlainWidth(title)
	fillerWidth := max(0, innerWidth-titleWidth-endWidth)
	offset := start
	if titleWidth > 0 {
		s.WriteText(offset, 0, title, borderStyle.Merge(b.TopLabelStyle))
		offset += titleWidth
	}
	if fillerWidth > 0 {
		s.WriteText(offset, 0, repeatBorder(border.Top, fillerWidth), borderStyle)
		offset += fillerWidth
	}
	endStart := offset
	if endWidth > 0 {
		s.WriteText(offset, 0, endLabel, borderStyle.Merge(b.EndLabelStyle))
		offset += endWidth
	}
	if remaining := end - offset; remaining > 0 {
		s.WriteText(offset, 0, repeatBorder(border.Top, remaining), borderStyle)
	}
	return s, endStart, endWidth
}

func (b Border) renderBottomBorder(width int, border lipgloss.Border, borderStyle CellStyle) Surface {
	s := BlankSurface(width, 1)
	for x := 0; x < width; x++ {
		s.setCell(x, 0, blankCell(borderStyle))
	}
	if width <= 0 {
		return s
	}
	if b.BorderLeft {
		s.WriteText(0, 0, border.BottomLeft, borderStyle)
	}
	if b.BorderRight && width > 1 {
		s.WriteText(width-1, 0, border.BottomRight, borderStyle)
	}
	start := 0
	if b.BorderLeft {
		start = 1
	}
	end := width
	if b.BorderRight {
		end--
	}
	if end > start {
		s.WriteText(start, 0, repeatBorder(border.Bottom, end-start), borderStyle)
	}
	return s
}

func borderVerticalStart(hasTop bool) int {
	if hasTop {
		return 1
	}
	return 0
}

func borderVerticalEnd(height int, hasBottom bool) int {
	if hasBottom {
		return max(0, height-1)
	}
	return max(0, height)
}

func repeatBorder(glyph string, width int) string {
	if width <= 0 {
		return ""
	}
	if glyph == "" {
		glyph = " "
	}
	return strings.Repeat(glyph, width)
}
