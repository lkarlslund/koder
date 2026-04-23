package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/lkarlslund/koder/internal/theme"
)

type Label struct {
	Text  string
	Style lipgloss.Style
}

func (l Label) Measure(_ *Context, constraints Constraints) Size {
	return constraints.Clamp(Size{W: lipgloss.Width(l.Text), H: 1})
}

func (l Label) Render(_ *Context, bounds Rect) Surface {
	width := max(1, bounds.W)
	s := BlankSurface(width, max(1, bounds.H))
	s.WriteText(0, 0, ansi.Truncate(l.Text, width, ""), lipglossToCellStyle(l.Style))
	return s.normalize(bounds.W, bounds.H)
}

type TextPane struct {
	Content string
}

func (t TextPane) Measure(_ *Context, constraints Constraints) Size {
	return constraints.Clamp(SurfaceFromString(t.Content).Size())
}

func (t TextPane) Render(_ *Context, bounds Rect) Surface {
	return SurfaceFromString(t.Content).normalize(bounds.W, bounds.H)
}

type HitBox struct {
	ID    string
	Child Element
}

func (h HitBox) Measure(ctx *Context, constraints Constraints) Size {
	if h.Child == nil {
		return constraints.Clamp(Size{})
	}
	return constraints.Clamp(h.Child.Measure(ctx, constraints))
}

func (h HitBox) Render(ctx *Context, bounds Rect) Surface {
	if ctx != nil && ctx.Runtime != nil && strings.TrimSpace(h.ID) != "" {
		ctx.Runtime.Register(Control{
			ID:      h.ID,
			Rect:    Rect{X: bounds.X, Y: bounds.Y, W: bounds.W, H: max(1, bounds.H)},
			Enabled: true,
		})
	}
	if h.Child == nil {
		return BlankSurface(bounds.W, bounds.H)
	}
	return h.Child.Render(ctx, bounds)
}

type Panel struct {
	Child        Element
	Width        int
	Height       int
	Padding      Insets
	Background   lipgloss.Color
	Foreground   lipgloss.Color
	BorderLeft   bool
	BorderRight  bool
	BorderTop    bool
	BorderBottom bool
	BorderColor  lipgloss.Color
}

func (p Panel) Measure(ctx *Context, constraints Constraints) Size {
	width := constraints.MaxW
	if p.Width > 0 {
		width = p.Width
	}
	inset := p.panelInsets()
	childSize := Size{}
	if p.Child != nil {
		childSize = p.Child.Measure(ctx, Constraints{MaxW: max(0, width-inset.Left-inset.Right), MaxH: max(0, constraints.MaxH-inset.Top-inset.Bottom)})
	}
	size := Size{
		W: childSize.W + inset.Left + inset.Right,
		H: childSize.H + inset.Top + inset.Bottom,
	}
	if p.Width > 0 {
		size.W = p.Width
	}
	if p.Height > 0 {
		size.H = p.Height
	}
	return constraints.Clamp(size)
}

func (p Panel) Render(ctx *Context, bounds Rect) Surface {
	width := bounds.W
	if width <= 0 {
		width = p.Width
	}
	if width <= 0 {
		width = p.Measure(ctx, NewConstraints(bounds.W, bounds.H)).W
	}
	height := bounds.H
	if height <= 0 {
		height = p.Height
	}
	if height <= 0 {
		height = p.Measure(ctx, NewConstraints(width, bounds.H)).H
	}
	inset := p.panelInsets()
	childBounds := Rect{
		X: bounds.X + inset.Left,
		Y: bounds.Y + inset.Top,
		W: max(0, width-inset.Left-inset.Right),
		H: max(0, height-inset.Top-inset.Bottom),
	}
	s := BlankSurface(width, height)
	fillStyle := CellStyle{BG: p.Background, FG: p.Foreground}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			s.setCell(x, y, Cell{Text: " ", Width: 1, Style: fillStyle})
		}
	}
	border := lipgloss.NormalBorder()
	borderStyle := CellStyle{FG: p.BorderColor, BG: p.Background}
	if p.BorderTop && width >= 2 {
		s.WriteText(0, 0, border.TopLeft, borderStyle)
		s.WriteText(width-1, 0, border.TopRight, borderStyle)
		for x := 1; x < width-1; x++ {
			s.setCell(x, 0, Cell{Text: border.Top, Width: 1, Style: borderStyle})
		}
	}
	if p.BorderBottom && height >= 2 && width >= 2 {
		s.WriteText(0, height-1, border.BottomLeft, borderStyle)
		s.WriteText(width-1, height-1, border.BottomRight, borderStyle)
		for x := 1; x < width-1; x++ {
			s.setCell(x, height-1, Cell{Text: border.Bottom, Width: 1, Style: borderStyle})
		}
	}
	if p.BorderLeft {
		for y := 1; y < height-1; y++ {
			s.setCell(0, y, Cell{Text: border.Left, Width: 1, Style: borderStyle})
		}
	}
	if p.BorderRight && width > 1 {
		for y := 1; y < height-1; y++ {
			s.setCell(width-1, y, Cell{Text: border.Right, Width: 1, Style: borderStyle})
		}
	}
	if p.Child != nil {
		s = s.placeAt(inset.Left, inset.Top, p.Child.Render(ctx, childBounds))
	}
	return s.normalize(width, height)
}

func (p Panel) panelInsets() Insets {
	inset := p.Padding
	if p.BorderLeft {
		inset.Left++
	}
	if p.BorderRight {
		inset.Right++
	}
	if p.BorderTop {
		inset.Top++
	}
	if p.BorderBottom {
		inset.Bottom++
	}
	return inset
}

type Divider struct {
	Text  string
	Style lipgloss.Style
}

func (d Divider) Measure(_ *Context, constraints Constraints) Size {
	width := constraints.MaxW
	if width <= 0 {
		width = lipgloss.Width(d.Text)
	}
	if width <= 0 {
		width = 1
	}
	return constraints.Clamp(Size{W: width, H: 1})
}

func (d Divider) Render(_ *Context, bounds Rect) Surface {
	width := bounds.W
	if width <= 0 {
		width = max(1, lipgloss.Width(d.Text))
	}
	text := strings.TrimSpace(d.Text)
	if text == "" {
		text = strings.Repeat("─", width)
	} else if lipgloss.Width(text) < width {
		text += strings.Repeat("─", width-lipgloss.Width(text))
	}
	s := BlankSurface(width, max(1, bounds.H))
	s.WriteText(0, 0, ansi.Truncate(text, width, ""), lipglossToCellStyle(d.Style))
	return s.normalize(width, bounds.H)
}

type Paragraph struct {
	Text  string
	Style lipgloss.Style
}

func (p Paragraph) Measure(_ *Context, constraints Constraints) Size {
	rendered := p.render(constraints.maxWidth())
	return constraints.Clamp(SurfaceFromString(rendered).Size())
}

func (p Paragraph) Render(_ *Context, bounds Rect) Surface {
	text := strings.TrimSpace(p.Text)
	if text == "" {
		return BlankSurface(max(0, bounds.W), max(0, bounds.H))
	}
	width := bounds.W
	if width <= 0 {
		width = lipgloss.Width(text)
	}
	rendered := p.render(width)
	lines := strings.Split(rendered, "\n")
	s := BlankSurface(width, len(lines))
	style := lipglossToCellStyle(p.Style)
	for y, line := range lines {
		s.WriteText(0, y, ansi.Truncate(line, width, ""), style)
	}
	return s.normalize(bounds.W, bounds.H)
}

func (p Paragraph) render(width int) string {
	text := strings.TrimSpace(p.Text)
	if text == "" {
		return ""
	}
	if width > 0 {
		var lines []string
		for _, line := range strings.Split(text, "\n") {
			if strings.TrimSpace(line) == "" {
				lines = append(lines, "")
				continue
			}
			lines = append(lines, strings.Split(ansi.Wordwrap(line, width, ""), "\n")...)
		}
		text = strings.Join(lines, "\n")
	}
	var rendered []string
	for _, line := range strings.Split(text, "\n") {
		rendered = append(rendered, p.Style.Render(line))
	}
	return strings.Join(rendered, "\n")
}

type ModalFrame struct {
	Title    string
	Subtitle string
	Body     Element
	Footer   string
	Width    int
}

func (m ModalFrame) Measure(ctx *Context, constraints Constraints) Size {
	width := constraints.maxWidth()
	if m.Width > 0 {
		width = min(width, m.Width)
	}
	if width <= 0 || width == int(^uint(0)>>1) {
		width = m.Width
	}
	if width <= 0 {
		width = 80
	}
	contentWidth := max(0, width-6)
	content := m.contentElement(ctx.Palette)
	contentSize := Size{}
	if content != nil {
		contentSize = content.Measure(ctx, NewConstraints(contentWidth, constraints.MaxH))
	}
	width = max(width, m.minimumFrameWidth(contentSize.W))
	height := contentSize.H + 4
	return constraints.Clamp(Size{W: width, H: height})
}

func (m ModalFrame) Render(ctx *Context, bounds Rect) Surface {
	if bounds.W <= 0 || bounds.H <= 0 {
		size := m.Measure(ctx, NewConstraints(bounds.W, bounds.H))
		if bounds.W <= 0 {
			bounds.W = size.W
		}
		if bounds.H <= 0 {
			bounds.H = size.H
		}
	}
	contentWidth := max(0, bounds.W-6)
	content := m.contentElement(ctx.Palette)
	contentHeight := 0
	var contentSurface Surface
	if content != nil {
		contentHeight = content.Measure(ctx, NewConstraints(contentWidth, max(0, bounds.H-4))).H
		contentSurface = content.Render(ctx, Rect{X: bounds.X + 3, Y: bounds.Y + 2, W: contentWidth, H: contentHeight})
	}
	base := BlankSurface(bounds.W, bounds.H)
	top, closeStart, closeWidth := m.topBorder(ctx.Palette, bounds.W)
	base = base.placeAt(0, 0, top)
	if ctx != nil && ctx.Runtime != nil && closeWidth > 0 {
		ctx.Runtime.Register(Control{
			ID:      "window-close",
			Rect:    Rect{X: bounds.X + closeStart, Y: bounds.Y, W: closeWidth, H: 1},
			Enabled: true,
		})
	}
	base = base.placeAt(0, 1, m.frameLine(ctx.Palette, bounds.W, ""))
	for row := 0; row < contentHeight; row++ {
		line := ""
		lines := contentSurface.Lines()
		if row < len(lines) {
			line = lines[row]
		}
		base = base.placeAt(0, row+2, m.frameLine(ctx.Palette, bounds.W, line))
	}
	base = base.placeAt(0, contentHeight+2, m.frameLine(ctx.Palette, bounds.W, ""))
	base = base.placeAt(0, bounds.H-1, m.bottomBorder(ctx.Palette, bounds.W))
	return base.normalize(bounds.W, bounds.H)
}

func (m ModalFrame) contentElement(palette theme.Palette) Element {
	children := []Child{}
	if subtitle := strings.TrimSpace(m.Subtitle); subtitle != "" {
		children = append(children, Fixed(Label{
			Text: subtitle,
			Style: lipgloss.NewStyle().
				Foreground(palette.AssistantTimestampText),
		}))
	}
	if m.Body != nil {
		if len(children) > 0 {
			children = append(children, Fixed(Spacer{H: 1}))
		}
		children = append(children, Fixed(m.Body))
	}
	if footer := strings.TrimSpace(m.Footer); footer != "" {
		if len(children) > 0 {
			children = append(children, Fixed(Spacer{H: 1}))
		}
		children = append(children, Fixed(Label{
			Text: footer,
			Style: lipgloss.NewStyle().
				Foreground(palette.AssistantTimestampText),
		}))
	}
	if len(children) == 0 {
		return nil
	}
	return Column{Children: children}
}

func (m ModalFrame) minimumFrameWidth(contentWidth int) int {
	titleWidth := ansi.StringWidth(m.titleLabel())
	closeWidth := ansi.StringWidth(m.closeLabel())
	return max(contentWidth+6, titleWidth+closeWidth+2)
}

func (m ModalFrame) titleLabel() string {
	title := strings.TrimSpace(m.Title)
	if title == "" {
		return ""
	}
	return "[" + title + "]"
}

func (m ModalFrame) closeLabel() string {
	return "[X]"
}

func (m ModalFrame) topBorder(palette theme.Palette, width int) (Surface, int, int) {
	border := lipgloss.RoundedBorder()
	borderStyle := CellStyle{FG: palette.SidebarBorder, BG: palette.SidebarBackground}
	titleStyle := CellStyle{FG: palette.MarkdownText, BG: palette.SidebarBackground, Bold: true}
	closeStyle := CellStyle{FG: palette.AssistantTimestampText, BG: palette.SidebarBackground, Bold: true}
	innerWidth := max(0, width-2)
	title := m.titleLabel()
	close := m.closeLabel()
	closeWidth := min(innerWidth, ansi.StringWidth(close))
	titleBudget := max(0, innerWidth-closeWidth)
	if titleBudget > 0 && ansi.StringWidth(title) > titleBudget {
		title = "[" + ansi.Truncate(strings.TrimSpace(m.Title), max(0, titleBudget-2), "") + "]"
	}
	titleWidth := min(innerWidth-closeWidth, ansi.StringWidth(title))
	fillerWidth := max(0, innerWidth-titleWidth-closeWidth)
	closeStart := width - 1 - closeWidth
	s := BlankSurface(width, 1)
	for x := 0; x < width; x++ {
		s.setCell(x, 0, Cell{Text: " ", Width: 1, Style: borderStyle})
	}
	s.WriteText(0, 0, border.TopLeft, borderStyle)
	s.WriteText(1, 0, title, titleStyle)
	s.WriteText(1+titleWidth, 0, strings.Repeat(border.Top, fillerWidth), borderStyle)
	s.WriteText(closeStart, 0, close, closeStyle)
	s.WriteText(width-1, 0, border.TopRight, borderStyle)
	return s, max(0, closeStart), closeWidth
}

func (m ModalFrame) bottomBorder(palette theme.Palette, width int) Surface {
	border := lipgloss.RoundedBorder()
	borderStyle := CellStyle{FG: palette.SidebarBorder, BG: palette.SidebarBackground}
	s := BlankSurface(width, 1)
	for x := 0; x < width; x++ {
		s.setCell(x, 0, Cell{Text: " ", Width: 1, Style: borderStyle})
	}
	s.WriteText(0, 0, border.BottomLeft, borderStyle)
	if width > 2 {
		s.WriteText(1, 0, strings.Repeat(border.Bottom, width-2), borderStyle)
	}
	if width > 1 {
		s.WriteText(width-1, 0, border.BottomRight, borderStyle)
	}
	return s
}

func (m ModalFrame) frameLine(palette theme.Palette, width int, content string) Surface {
	border := lipgloss.RoundedBorder()
	borderStyle := CellStyle{FG: palette.SidebarBorder, BG: palette.SidebarBackground}
	fillStyle := CellStyle{FG: palette.SidebarForeground, BG: palette.SidebarBackground}
	contentWidth := max(0, width-6)
	line := ansi.Truncate(content, contentWidth, "")
	if delta := contentWidth - ansi.StringWidth(line); delta > 0 {
		line += strings.Repeat(" ", delta)
	}
	s := BlankSurface(width, 1)
	for x := 0; x < width; x++ {
		s.setCell(x, 0, Cell{Text: " ", Width: 1, Style: fillStyle})
	}
	s.WriteText(0, 0, border.Left, borderStyle)
	s.WriteText(1, 0, "  "+line+"  ", fillStyle)
	s.WriteText(width-1, 0, border.Right, borderStyle)
	return s
}

func lipglossToCellStyle(style lipgloss.Style) CellStyle {
	cell := CellStyle{
		Bold:   style.GetBold(),
		Italic: style.GetItalic(),
	}
	if fg := style.GetForeground(); fg != nil {
		cell.FG = lipgloss.Color(fmt.Sprint(fg))
	}
	if bg := style.GetBackground(); bg != nil {
		cell.BG = lipgloss.Color(fmt.Sprint(bg))
	}
	return cell
}
