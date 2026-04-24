package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/lkarlslund/koder/internal/theme"
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
	return constraints.Clamp(d.window(ctx, width).Measure(ctx, constraints))
}

func (d Dialog) Render(ctx *Context, bounds Rect) Surface {
	width := d.frameWidth(ctx, Constraints{MaxW: bounds.W, MaxH: bounds.H})
	return d.window(ctx, width).Render(ctx, Rect{X: bounds.X, Y: bounds.Y, W: width, H: bounds.H})
}

func (d Dialog) bodyElement(width int, palette theme.Palette) Element {
	children := make([]Child, 0, 4)
	if subtitle := strings.TrimSpace(d.Subtitle); subtitle != "" {
		children = append(children, Fixed(Label{
			Text:  subtitle,
			Style: lipgloss.NewStyle().Foreground(palette.AssistantTimestampText),
		}))
	}
	if d.Body != nil {
		children = append(children, Fixed(d.Body))
	}
	if len(d.Buttons.Buttons) > 0 {
		buttons := d.Buttons
		buttons.Width = max(0, width-6)
		children = append(children, Fixed(buttons))
	}
	if footer := strings.TrimSpace(d.Footer); footer != "" {
		children = append(children, Fixed(Label{
			Text:  footer,
			Style: lipgloss.NewStyle().Foreground(palette.AssistantTimestampText),
		}))
	}
	if len(children) == 0 {
		return nil
	}
	return Column{Children: children, Spacing: 2}
}

func (d Dialog) window(ctx *Context, width int) WindowFrame {
	palette := themePalette(ctx)
	return WindowFrame{
		Title:       d.Title,
		Content:     d.bodyElement(width, palette),
		Width:       width,
		Background:  palette.SidebarBackground,
		Foreground:  palette.SidebarForeground,
		BorderColor: palette.SidebarBorder,
		ShowClose:   true,
	}
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
		maxContentWidth = max(maxContentWidth, buttons.Measure(ctx, Constraints{}).W)
	}
	maxContentWidth = max(maxContentWidth, PlainWidth(strings.TrimSpace(d.Title)))
	maxContentWidth = max(maxContentWidth, PlainWidth(strings.TrimSpace(d.Subtitle)))
	maxContentWidth = max(maxContentWidth, PlainWidth(strings.TrimSpace(d.Footer)))
	width := d.Width
	if required := maxContentWidth + 6; required > width {
		width = required
	}
	if constraints.MaxW > 0 && width > constraints.MaxW {
		width = constraints.MaxW
	}
	return width
}
