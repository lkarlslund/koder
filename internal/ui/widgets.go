package ui

import (
	"strings"

	"github.com/lkarlslund/koder/internal/theme"
)

type Label struct {
	PassiveNode
	Text  string
	Style Style
}

func (l Label) Measure(_ *Context, constraints Constraints) Size {
	return constraints.Clamp(Size{W: TextWidth(l.Text), H: 1})
}

func (l Label) Paint(_ *Context, canvas Canvas) {
	if canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	canvas.WriteText(0, 0, PlainTruncate(l.Text, max(1, canvas.Width()), ""), l.Style.CellStyle())
}

type RetainedLabel struct {
	BaseNode
	Text  string
	Style Style
}

func (l *RetainedLabel) Set(text string, style Style) {
	if l == nil {
		return
	}
	if l.Text == text && l.Style == style {
		return
	}
	needsLayout := l.Text != text
	l.Text = text
	l.Style = style
	if needsLayout {
		l.MarkLayoutDirty()
		return
	}
	l.MarkDirtyLocal(Rect{W: l.Rect().W, H: l.Rect().H})
}

func (l *RetainedLabel) Measure(_ *Context, constraints Constraints) Size {
	if l == nil {
		return constraints.Clamp(Size{})
	}
	return constraints.Clamp(Size{W: TextWidth(l.Text), H: 1})
}

func (l *RetainedLabel) Paint(_ *Context, canvas Canvas) {
	if l == nil || canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	canvas.WriteText(0, 0, PlainTruncate(l.Text, max(1, canvas.Width()), ""), l.Style.CellStyle())
}

type TextPane struct {
	PassiveNode
	Content string
}

func (t TextPane) Measure(_ *Context, constraints Constraints) Size {
	return constraints.Clamp(measurePlainTextBlock(t.Content))
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

func measurePlainTextBlock(content string) Size {
	if content == "" {
		return Size{}
	}
	width := 0
	height := 1
	start := 0
	for idx, r := range content {
		if r != '\n' {
			continue
		}
		width = max(width, PlainWidth(content[start:idx]))
		height++
		start = idx + 1
	}
	width = max(width, PlainWidth(content[start:]))
	return Size{W: width, H: height}
}

type HitBox struct {
	PassiveNode
	ID    string
	Child Node
}

func (h HitBox) Children() []Node {
	if h.Child == nil {
		return nil
	}
	return []Node{h.Child}
}

func (h HitBox) Measure(ctx *Context, constraints Constraints) Size {
	if h.Child == nil {
		return constraints.Clamp(Size{})
	}
	return constraints.Clamp(h.Child.Measure(ctx, constraints))
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
	paintNodeInto(ctx, h.Child, Rect{
		X: canvas.origin.X,
		Y: canvas.origin.Y,
		W: canvas.Width(),
		H: canvas.Height(),
	}, canvas.surface)
}

type Divider struct {
	PassiveNode
	Text  string
	Style Style
}

func (d Divider) Measure(_ *Context, constraints Constraints) Size {
	width := constraints.MaxW
	if width <= 0 {
		width = TextWidth(d.Text)
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
	} else if TextWidth(text) < width {
		text += strings.Repeat("─", width-TextWidth(text))
	}
	canvas.WriteText(0, 0, PlainTruncate(text, width, ""), d.Style.CellStyle())
}

type Paragraph struct {
	PassiveNode
	Text  string
	Style Style
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
	style := p.Style.CellStyle()
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
	PassiveNode
	Title    string
	Subtitle string
	Body     Node
	Footer   string
	Width    int
}

func (m ModalFrame) Measure(ctx *Context, constraints Constraints) Size {
	return m.window(ctx).Measure(ctx, constraints)
}

func (m ModalFrame) Paint(ctx *Context, canvas Canvas) {
	if canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	paintNodeInto(ctx, m.window(ctx), Rect{
		X: canvas.origin.X,
		Y: canvas.origin.Y,
		W: canvas.Width(),
		H: canvas.Height(),
	}, canvas.surface)
}

func (m ModalFrame) contentNode(palette theme.Palette) Node {
	children := []Child{}
	if subtitle := strings.TrimSpace(m.Subtitle); subtitle != "" {
		children = append(children, Fixed(Label{
			Text:  subtitle,
			Style: NewStyle().Foreground(palette.AssistantTimestampText),
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
			Text:  footer,
			Style: NewStyle().Foreground(palette.AssistantTimestampText),
		}))
	}
	if len(children) == 0 {
		return nil
	}
	return NewFlexBox(DirectionVertical, children, 0)
}

func (m ModalFrame) window(ctx *Context) WindowFrame {
	return WindowFrame{
		Title:       m.Title,
		Content:     m.contentNode(themePalette(ctx)),
		Width:       m.Width,
		Background:  themePalette(ctx).SidebarBackground,
		Foreground:  themePalette(ctx).SidebarForeground,
		BorderColor: themePalette(ctx).SidebarBorder,
		ShowClose:   true,
	}
}

func (m ModalFrame) Children() []Node {
	if m.Body == nil {
		return nil
	}
	return []Node{m.Body}
}
