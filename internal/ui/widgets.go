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

func (l Label) Paint(_ *Context, canvas Canvas) {
	if canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	canvas.WriteText(0, 0, PlainTruncate(l.Text, max(1, canvas.Width()), ""), lipglossToCellStyle(l.Style))
}

type TextPane struct {
	Content string
}

func (t TextPane) Measure(_ *Context, constraints Constraints) Size {
	return constraints.Clamp(SurfaceFromString(t.Content).Size())
}

func (t TextPane) Paint(_ *Context, canvas Canvas) {
	if canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	lines := strings.Split(t.Content, "\n")
	for y, line := range lines {
		canvas.WriteText(0, y, PlainTruncate(line, canvas.Width(), ""), CellStyle{})
	}
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

func (h HitBox) WalkChildren(_ *Context, visit func(Element)) {
	if h.Child == nil || visit == nil {
		return
	}
	visit(h.Child)
}

func (h HitBox) Paint(ctx *Context, canvas Canvas) {
	if canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	if ctx != nil && ctx.Runtime != nil && strings.TrimSpace(h.ID) != "" {
		ctx.Runtime.Register(Control{
			ID:      h.ID,
			Rect:    Rect{X: canvas.origin.X, Y: canvas.origin.Y, W: canvas.Width(), H: max(1, canvas.Height())},
			Enabled: true,
		})
	}
	if h.Child == nil {
		return
	}
	renderElementInto(ctx, h.Child, Rect{
		X: canvas.origin.X,
		Y: canvas.origin.Y,
		W: canvas.Width(),
		H: canvas.Height(),
	}, canvas.surface)
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

func (d Divider) Paint(_ *Context, canvas Canvas) {
	if canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	width := canvas.Width()
	text := strings.TrimSpace(d.Text)
	if text == "" {
		text = strings.Repeat("─", width)
	} else if lipgloss.Width(text) < width {
		text += strings.Repeat("─", width-lipgloss.Width(text))
	}
	canvas.WriteText(0, 0, PlainTruncate(text, width, ""), lipglossToCellStyle(d.Style))
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

func (p Paragraph) Paint(_ *Context, canvas Canvas) {
	if canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	lines := p.lines(canvas.Width())
	style := lipglossToCellStyle(p.Style)
	for y, line := range lines {
		canvas.WriteText(0, y, PlainTruncate(line, canvas.Width(), ""), style)
	}
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

func (m ModalFrame) Paint(ctx *Context, canvas Canvas) {
	if canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	renderElementInto(ctx, m.window(ctx), Rect{
		X: canvas.origin.X,
		Y: canvas.origin.Y,
		W: canvas.Width(),
		H: canvas.Height(),
	}, canvas.surface)
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
