package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/lkarlslund/koder/internal/theme"
)

type Label struct {
	Text  string
	Style lipgloss.Style
}

func (l Label) Measure(_ *Context, constraints Constraints) Size {
	return constraints.Clamp(Size{W: lipgloss.Width(l.Text), H: 1})
}

func (l Label) Render(ctx *Context, bounds Rect) Surface {
	return renderOwnedSurface(ctx, bounds, l.RenderTo)
}

func (l Label) RenderTo(_ *Context, bounds Rect, dst *Surface) {
	if dst == nil || bounds.W <= 0 || bounds.H <= 0 {
		return
	}
	width := max(1, bounds.W)
	surface := TransparentSurface(width, max(1, bounds.H))
	surface.WriteText(0, 0, PlainTruncate(l.Text, width, ""), lipglossToCellStyle(l.Style))
	*dst = dst.placeAt(bounds.X, bounds.Y, surface.normalize(bounds.W, bounds.H))
}

type TextPane struct {
	Content string
}

func (t TextPane) Measure(_ *Context, constraints Constraints) Size {
	return constraints.Clamp(SurfaceFromString(t.Content).Size())
}

func (t TextPane) Render(ctx *Context, bounds Rect) Surface {
	return renderOwnedSurface(ctx, bounds, t.RenderTo)
}

func (t TextPane) RenderTo(_ *Context, bounds Rect, dst *Surface) {
	if dst == nil || bounds.W <= 0 || bounds.H <= 0 {
		return
	}
	*dst = dst.placeAt(bounds.X, bounds.Y, SurfaceFromString(t.Content).normalize(bounds.W, bounds.H))
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
	return renderOwnedSurface(ctx, bounds, h.RenderTo)
}

func (h HitBox) RenderTo(ctx *Context, bounds Rect, dst *Surface) {
	if dst == nil || bounds.W <= 0 || bounds.H <= 0 {
		return
	}
	if ctx != nil && ctx.Runtime != nil && strings.TrimSpace(h.ID) != "" {
		ctx.Runtime.Register(Control{
			ID:      h.ID,
			Rect:    Rect{X: bounds.X, Y: bounds.Y, W: bounds.W, H: max(1, bounds.H)},
			Enabled: true,
		})
	}
	if h.Child == nil {
		return
	}
	renderElementInto(ctx, h.Child, bounds, dst)
}

func (h HitBox) WalkChildren(_ *Context, visit func(Element)) {
	if h.Child == nil || visit == nil {
		return
	}
	visit(h.Child)
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

func (d Divider) Render(ctx *Context, bounds Rect) Surface {
	return renderOwnedSurface(ctx, bounds, d.RenderTo)
}

func (d Divider) RenderTo(_ *Context, bounds Rect, dst *Surface) {
	if dst == nil || bounds.W <= 0 || bounds.H <= 0 {
		return
	}
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
	surface := TransparentSurface(width, max(1, bounds.H))
	surface.WriteText(0, 0, PlainTruncate(text, width, ""), lipglossToCellStyle(d.Style))
	*dst = dst.placeAt(bounds.X, bounds.Y, surface.normalize(width, bounds.H))
}

type Paragraph struct {
	Text  string
	Style lipgloss.Style
}

func (p Paragraph) Measure(_ *Context, constraints Constraints) Size {
	lines := p.lines(constraints.maxWidth())
	width := 0
	for _, line := range lines {
		width = max(width, PlainWidth(line))
	}
	return constraints.Clamp(Size{W: width, H: len(lines)})
}

func (p Paragraph) Render(ctx *Context, bounds Rect) Surface {
	return renderOwnedSurface(ctx, bounds, p.RenderTo)
}

func (p Paragraph) RenderTo(_ *Context, bounds Rect, dst *Surface) {
	if dst == nil || bounds.W <= 0 || bounds.H <= 0 {
		return
	}
	text := strings.TrimSpace(p.Text)
	if text == "" {
		return
	}
	width := bounds.W
	if width <= 0 {
		width = lipgloss.Width(text)
	}
	lines := p.lines(width)
	surface := TransparentSurface(width, len(lines))
	style := lipglossToCellStyle(p.Style)
	for y, line := range lines {
		surface.WriteText(0, y, PlainTruncate(line, width, ""), style)
	}
	*dst = dst.placeAt(bounds.X, bounds.Y, surface.normalize(bounds.W, bounds.H))
}

func (p Paragraph) lines(width int) []string {
	text := strings.TrimSpace(p.Text)
	if text == "" {
		return nil
	}
	if width > 0 {
		var lines []string
		for _, line := range strings.Split(text, "\n") {
			if strings.TrimSpace(line) == "" {
				lines = append(lines, "")
				continue
			}
			lines = append(lines, strings.Split(PlainWordWrap(line, width), "\n")...)
		}
		text = strings.Join(lines, "\n")
	}
	return strings.Split(text, "\n")
}

type ModalFrame struct {
	Title    string
	Subtitle string
	Body     Element
	Footer   string
	Width    int
}

func (m ModalFrame) WalkChildren(ctx *Context, visit func(Element)) {
	if visit == nil {
		return
	}
	visit(m.window(ctx))
}

func (m ModalFrame) Measure(ctx *Context, constraints Constraints) Size {
	return m.window(ctx).Measure(ctx, constraints)
}

func (m ModalFrame) Render(ctx *Context, bounds Rect) Surface {
	return renderOwnedSurface(ctx, bounds, m.RenderTo)
}

func (m ModalFrame) RenderTo(ctx *Context, bounds Rect, dst *Surface) {
	if dst == nil || bounds.W <= 0 || bounds.H <= 0 {
		return
	}
	renderElementInto(ctx, m.window(ctx), bounds, dst)
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
	return FlexBox{Direction: DirectionVertical, Children: children}
}

func (m ModalFrame) window(ctx *Context) WindowFrame {
	return WindowFrame{
		Title:       m.Title,
		Content:     m.contentElement(themePalette(ctx)),
		Width:       m.Width,
		Background:  themePalette(ctx).SidebarBackground,
		Foreground:  themePalette(ctx).SidebarForeground,
		BorderColor: themePalette(ctx).SidebarBorder,
		ShowClose:   true,
	}
}

func lipglossToCellStyle(style lipgloss.Style) CellStyle {
	cell := CellStyle{}.
		WithBold(style.GetBold()).
		WithItalic(style.GetItalic()).
		WithUnderline(style.GetUnderline())
	if fg := style.GetForeground(); fg != nil {
		cell.FG = ParseCellColor(fmt.Sprint(fg))
	}
	if bg := style.GetBackground(); bg != nil {
		cell.BG = ParseCellColor(fmt.Sprint(bg))
	}
	return cell
}
