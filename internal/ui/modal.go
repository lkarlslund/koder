package ui

import (
	"strings"

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
	return ModalFrame{
		Title:    m.Title,
		Subtitle: m.Subtitle,
		Body:     m.bodyElement(),
		Footer:   m.Footer,
		Width:    m.Width,
	}.Measure(ctx, constraints)
}

func (m Modal) Render(ctx *Context, bounds Rect) Surface {
	return ModalFrame{
		Title:    m.Title,
		Subtitle: m.Subtitle,
		Body:     m.bodyElement(),
		Footer:   m.Footer,
		Width:    m.Width,
	}.Render(ctx, bounds)
}
