package ui

type Modal struct {
	Title       string
	Subtitle    string
	BodyElement Element
	Footer      string
	Width       int
}

func (m Modal) Measure(ctx *Context, constraints Constraints) Size {
	return ModalFrame{
		Title:    m.Title,
		Subtitle: m.Subtitle,
		Body:     m.BodyElement,
		Footer:   m.Footer,
		Width:    m.Width,
	}.Measure(ctx, constraints)
}

func (m Modal) Render(ctx *Context, bounds Rect) Surface {
	return ModalFrame{
		Title:    m.Title,
		Subtitle: m.Subtitle,
		Body:     m.BodyElement,
		Footer:   m.Footer,
		Width:    m.Width,
	}.Render(ctx, bounds)
}
