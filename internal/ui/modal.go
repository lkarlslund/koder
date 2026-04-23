package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/lkarlslund/koder/internal/theme"
)

type Modal struct {
	Title       string
	Subtitle    string
	Body        string
	BodyElement Element
	Footer      string
	Width       int
}

func (m Modal) View(palette theme.Palette) string {
	ctx := &Context{Palette: palette}
	width := m.Width
	if width <= 0 {
		width = 80
	}
	return RenderElement(ctx, m, width, 0)
}

func (m Modal) bodyElement() Element {
	if m.BodyElement != nil {
		return m.BodyElement
	}
	if strings.TrimSpace(m.Body) == "" {
		return nil
	}
	return TextPane{Content: m.Body}
}

func (m Modal) Measure(ctx *Context, constraints Constraints) Size {
	width := constraints.maxWidth()
	if m.Width > 0 {
		width = min(width, m.Width)
	}
	if width <= 0 || width == int(^uint(0)>>1) {
		width = max(80, m.Width)
	}
	height := 4
	maxContentWidth := 0
	if title := strings.TrimSpace(m.Title); title != "" {
		height++
		maxContentWidth = max(maxContentWidth, lipgloss.Width(title))
	}
	if subtitle := strings.TrimSpace(m.Subtitle); subtitle != "" {
		height++
		maxContentWidth = max(maxContentWidth, lipgloss.Width(subtitle))
	}
	if body := m.bodyElement(); body != nil {
		bodySize := body.Measure(ctx, NewConstraints(max(0, width-6), constraints.MaxH))
		height += bodySize.H
		maxContentWidth = max(maxContentWidth, bodySize.W)
	}
	if footer := strings.TrimSpace(m.Footer); footer != "" {
		height += 2
		maxContentWidth = max(maxContentWidth, lipgloss.Width(footer))
	}
	if required := maxContentWidth + 6; required > width {
		width = required
	}
	return constraints.Clamp(Size{W: width, H: height})
}

func (m Modal) Render(ctx *Context, bounds Rect) Surface {
	width := bounds.W
	if width <= 0 {
		width = m.Width
	}
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
	if body := m.bodyElement(); body != nil {
		bodyWidth := max(0, width-6)
		bodyHeight := body.Measure(ctx, NewConstraints(bodyWidth, max(0, bounds.H-bodyY))).H
		parts = append(parts, body.Render(ctx, Rect{X: bounds.X + 3, Y: bodyY, W: bodyWidth, H: bodyHeight}).String())
	}
	if footer := strings.TrimSpace(m.Footer); footer != "" {
		parts = append(parts, "", lipgloss.NewStyle().Foreground(ctx.Palette.AssistantTimestampText).Render(footer))
	}
	return SurfaceFromString(lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ctx.Palette.SidebarBorder).
		Background(ctx.Palette.SidebarBackground).
		Foreground(ctx.Palette.SidebarForeground).
		Padding(1, 2).
		Width(width).
		Render(strings.Join(parts, "\n"))).normalize(bounds.W, bounds.H)
}
