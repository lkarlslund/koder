package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/lkarlslund/koder/internal/theme"
)

// Dialog is a declarative modal shell for app-specific dialog state that lives
// outside the ui package.
type Dialog struct {
	Title    string
	Subtitle string
	Body     Element
	Sections []string
	Buttons  ButtonRow
	Footer   string
	Width    int
}

func (d Dialog) View(palette theme.Palette) string {
	ctx := &Context{Palette: palette}
	width := d.frameWidth(ctx, NewConstraints(0, 0))
	return RenderElement(&Context{Palette: palette}, d, width, 0)
}

func (d Dialog) Measure(ctx *Context, constraints Constraints) Size {
	width := d.frameWidth(ctx, constraints)
	return constraints.Clamp(ModalFrame{
		Title:    d.Title,
		Subtitle: d.Subtitle,
		Body:     d.bodyElement(width, ctx.Palette),
		Footer:   d.Footer,
		Width:    width,
	}.Measure(ctx, constraints))
}

func (d Dialog) Render(ctx *Context, bounds Rect) Surface {
	width := bounds.W
	if width <= 0 {
		width = d.frameWidth(ctx, NewConstraints(bounds.W, bounds.H))
	}
	return ModalFrame{
		Title:    d.Title,
		Subtitle: d.Subtitle,
		Body:     d.bodyElement(width, ctx.Palette),
		Footer:   d.Footer,
		Width:    width,
	}.Render(ctx, Rect{X: bounds.X, Y: bounds.Y, W: width, H: bounds.H})
}

func (d Dialog) content(palette theme.Palette) string {
	ctx := &Context{Palette: palette}
	width := d.frameWidth(ctx, NewConstraints(0, 0))
	return ModalFrame{
		Title:    d.Title,
		Subtitle: d.Subtitle,
		Body:     d.bodyElement(width, palette),
		Footer:   d.Footer,
		Width:    width,
	}.Render(ctx, Rect{W: width, H: 0}).String()
}

func (d Dialog) bodyElement(width int, palette theme.Palette) Element {
	children := make([]Child, 0, len(d.Sections)+1)
	if d.Body != nil {
		children = append(children, Fixed(d.Body))
	} else {
		for _, section := range d.Sections {
			if strings.TrimSpace(section) == "" {
				continue
			}
			children = append(children, Fixed(Static{Content: strings.TrimRight(section, "\n")}))
		}
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
	} else {
		for _, section := range d.Sections {
			maxContentWidth = maxDialogContentWidth(maxContentWidth, section)
		}
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
	return width
}

func maxDialogContentWidth(current int, block string) int {
	for _, line := range strings.Split(block, "\n") {
		current = max(current, ansi.StringWidth(line))
	}
	return current
}
