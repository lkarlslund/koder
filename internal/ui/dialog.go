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
	Sections []string
	Buttons  ButtonRow
	Footer   string
	Width    int
}

func (d Dialog) View(palette theme.Palette) string {
	parts := make([]string, 0, len(d.Sections)+1)
	maxContentWidth := 0
	var measuredButtons ButtonRow
	buttonLineWidth := 0
	for _, section := range d.Sections {
		if strings.TrimSpace(section) == "" {
			continue
		}
		trimmed := strings.TrimRight(section, "\n")
		parts = append(parts, trimmed)
		maxContentWidth = maxDialogContentWidth(maxContentWidth, trimmed)
	}
	if len(d.Buttons.Buttons) > 0 {
		measuredButtons = d.Buttons
		measuredButtons.Width = 0
		buttonLineWidth = ansi.StringWidth(measuredButtons.line(palette))
		maxContentWidth = max(maxContentWidth, buttonLineWidth)
	}
	maxContentWidth = max(maxContentWidth, ansi.StringWidth(strings.TrimSpace(d.Title)))
	maxContentWidth = max(maxContentWidth, ansi.StringWidth(strings.TrimSpace(d.Subtitle)))
	maxContentWidth = max(maxContentWidth, ansi.StringWidth(strings.TrimSpace(d.Footer)))
	width := d.Width
	if required := maxContentWidth + 6; required > width {
		width = required
	}
	if len(d.Buttons.Buttons) > 0 {
		buttons := d.Buttons
		buttons.Width = max(maxContentWidth, buttonLineWidth)
		parts = append(parts, buttons.View(palette))
	}
	return Modal{
		Title:    d.Title,
		Subtitle: d.Subtitle,
		Body:     strings.Join(parts, "\n\n"),
		Footer:   d.Footer,
		Width:    width,
	}.View(palette)
}

func (d Dialog) Measure(ctx *Context, constraints Constraints) Size {
	return constraints.Clamp(SurfaceFromString(d.View(ctx.Palette)).Size())
}

func (d Dialog) Render(ctx *Context, bounds Rect) Surface {
	return SurfaceFromString(d.View(ctx.Palette)).normalize(bounds.W, bounds.H)
}

func maxDialogContentWidth(current int, block string) int {
	for _, line := range strings.Split(block, "\n") {
		current = max(current, ansi.StringWidth(line))
	}
	return current
}
