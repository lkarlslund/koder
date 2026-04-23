package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type Label struct {
	Text  string
	Style lipgloss.Style
}

func (l Label) Measure(_ *Context, constraints Constraints) Size {
	return constraints.Clamp(SurfaceFromString(l.render()).Size())
}

func (l Label) Render(_ *Context, bounds Rect) Surface {
	return SurfaceFromString(l.render()).normalize(bounds.W, bounds.H)
}

func (l Label) render() string {
	return l.Style.Render(l.Text)
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
	content := ""
	if p.Child != nil {
		content = p.Child.Render(ctx, childBounds).String()
	}
	style := lipgloss.NewStyle().
		Width(width).
		Padding(p.Padding.Top, p.Padding.Right, p.Padding.Bottom, p.Padding.Left).
		Background(p.Background).
		Foreground(p.Foreground).
		Border(lipgloss.Border{
			Top:         lipgloss.NormalBorder().Top,
			Bottom:      lipgloss.NormalBorder().Bottom,
			Left:        lipgloss.NormalBorder().Left,
			Right:       lipgloss.NormalBorder().Right,
			TopLeft:     lipgloss.NormalBorder().TopLeft,
			TopRight:    lipgloss.NormalBorder().TopRight,
			BottomLeft:  lipgloss.NormalBorder().BottomLeft,
			BottomRight: lipgloss.NormalBorder().BottomRight,
		}, p.BorderTop, p.BorderRight, p.BorderBottom, p.BorderLeft).
		BorderForeground(p.BorderColor)
	if height > 0 {
		style = style.Height(height)
	}
	return SurfaceFromString(style.Render(content)).normalize(width, height)
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
	return SurfaceFromString(d.Style.Render(text)).normalize(width, bounds.H)
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
	return SurfaceFromString(p.render(bounds.W)).normalize(bounds.W, bounds.H)
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
	bodyWidth := max(0, width-6)
	bodyHeight := 0
	if m.Body != nil {
		bodyHeight = m.Body.Measure(ctx, NewConstraints(bodyWidth, constraints.MaxH)).H
	}
	height := 2 // border + padding envelope approximation
	if strings.TrimSpace(m.Title) != "" {
		height++
	}
	if strings.TrimSpace(m.Subtitle) != "" {
		height++
	}
	if bodyHeight > 0 {
		height += 1 + bodyHeight
	}
	if strings.TrimSpace(m.Footer) != "" {
		height += 1 + 1
	}
	height += 2
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
	bodyWidth := max(0, bounds.W-6)
	parts := []string{}
	bodyY := bounds.Y + 2
	if title := strings.TrimSpace(m.Title); title != "" {
		parts = append(parts, lipgloss.NewStyle().Bold(true).Foreground(ctx.Palette.MarkdownText).Render(title))
		bodyY++
	}
	if subtitle := strings.TrimSpace(m.Subtitle); subtitle != "" {
		parts = append(parts, lipgloss.NewStyle().Foreground(ctx.Palette.AssistantTimestampText).Render(subtitle))
		bodyY++
	}
	if m.Body != nil {
		if len(parts) > 0 {
			bodyY++
		}
		bodyHeight := m.Body.Measure(ctx, NewConstraints(bodyWidth, max(0, bounds.H-bodyY))).H
		bodySurface := m.Body.Render(ctx, Rect{X: bounds.X + 3, Y: bodyY, W: bodyWidth, H: bodyHeight})
		parts = append(parts, bodySurface.String())
	}
	if footer := strings.TrimSpace(m.Footer); footer != "" {
		parts = append(parts, lipgloss.NewStyle().Foreground(ctx.Palette.AssistantTimestampText).Render(footer))
	}
	content := strings.Join(parts, "\n\n")
	rendered := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ctx.Palette.SidebarBorder).
		Background(ctx.Palette.SidebarBackground).
		Foreground(ctx.Palette.SidebarForeground).
		Padding(1, 2).
		Width(bounds.W).
		Render(content)
	return SurfaceFromString(rendered).normalize(bounds.W, bounds.H)
}
