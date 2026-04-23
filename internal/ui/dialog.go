package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Dialog is a declarative modal shell for app-specific dialog state that lives
// outside the ui package.
type Dialog struct {
	Title    string
	Subtitle string
	Body     Element
	Buttons  ButtonRow
	Footer   string
	Width    int
}

func (d Dialog) Measure(ctx *Context, constraints Constraints) Size {
	width := d.frameWidth(ctx, constraints)
	return constraints.Clamp(ModalFrame{
		Title:    d.Title,
		Subtitle: d.Subtitle,
		Body:     d.bodyElement(width),
		Footer:   d.Footer,
		Width:    width,
	}.Measure(ctx, constraints))
}

func (d Dialog) Render(ctx *Context, bounds Rect) Surface {
	width := d.frameWidth(ctx, Constraints{MaxW: bounds.W, MaxH: bounds.H})
	return ModalFrame{
		Title:    d.Title,
		Subtitle: d.Subtitle,
		Body:     d.bodyElement(width),
		Footer:   d.Footer,
		Width:    width,
	}.Render(ctx, Rect{X: bounds.X, Y: bounds.Y, W: width, H: bounds.H})
}

func (d Dialog) bodyElement(width int) Element {
	children := make([]Child, 0, 2)
	if d.Body != nil {
		children = append(children, Fixed(d.Body))
	}
	if len(d.Buttons.Buttons) > 0 {
		buttons := d.Buttons
		buttons.Width = max(0, width-6)
		children = append(children, Fixed(buttons))
	}
	if len(children) == 0 {
		return nil
	}
	return Column{Children: children, Spacing: 2}
}

func (d Dialog) frameWidth(ctx *Context, constraints Constraints) int {
	maxContentWidth := 0
	if d.Body != nil {
		size := d.Body.Measure(ctx, constraints.Deflate(Insets{Left: 3, Right: 3, Top: 0, Bottom: 0}))
		maxContentWidth = max(maxContentWidth, size.W)
	}
	if len(d.Buttons.Buttons) > 0 {
		buttons := d.Buttons
		buttons.Width = 0
		maxContentWidth = max(maxContentWidth, ansi.StringWidth(buttons.line(ctx.Palette)))
	}
	maxContentWidth = max(maxContentWidth, ansi.StringWidth(strings.TrimSpace(d.Title)))
	maxContentWidth = max(maxContentWidth, ansi.StringWidth(strings.TrimSpace(d.Subtitle)))
	maxContentWidth = max(maxContentWidth, ansi.StringWidth(strings.TrimSpace(d.Footer)))
	width := d.Width
	if required := maxContentWidth + 6; required > width {
		width = required
	}
	if constraints.MaxW > 0 && width > constraints.MaxW {
		width = constraints.MaxW
	}
	return width
}
