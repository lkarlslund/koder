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
	return renderOwnedCanvas(ctx, bounds, m)
}

func (m Modal) Paint(ctx *Context, canvas Canvas) {
	if canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	renderElementInto(ctx, ModalFrame{
		Title:    m.Title,
		Subtitle: m.Subtitle,
		Body:     m.BodyElement,
		Footer:   m.Footer,
		Width:    m.Width,
	}, Rect{
		X: canvas.origin.X,
		Y: canvas.origin.Y,
		W: canvas.Width(),
		H: canvas.Height(),
	}, canvas.surface)
}
