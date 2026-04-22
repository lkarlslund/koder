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
	if title := strings.TrimSpace(m.Title); title != "" {
		parts = append(parts, lipgloss.NewStyle().Bold(true).Foreground(ctx.Palette.MarkdownText).Render(title))
	}
	if subtitle := strings.TrimSpace(m.Subtitle); subtitle != "" {
		parts = append(parts, lipgloss.NewStyle().Foreground(ctx.Palette.AssistantTimestampText).Render(subtitle))
	}
	if m.Body != nil {
		bodyHeight := max(0, bounds.H-6)
		parts = append(parts, RenderElement(ctx, m.Body, bodyWidth, bodyHeight))
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
