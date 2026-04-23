package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/lkarlslund/koder/internal/theme"
)

type SplitDirection int

const (
	SplitHorizontal SplitDirection = iota
	SplitVertical
)

type Split struct {
	Direction   SplitDirection
	First       Element
	Second      Element
	FirstFixed  int
	SecondFixed int
	Gap         int
}

func (s Split) Measure(ctx *Context, constraints Constraints) Size {
	switch s.Direction {
	case SplitVertical:
		return s.measureVertical(ctx, constraints)
	default:
		return s.measureHorizontal(ctx, constraints)
	}
}

func (s Split) Render(ctx *Context, bounds Rect) Surface {
	switch s.Direction {
	case SplitVertical:
		return s.renderVertical(ctx, bounds)
	default:
		return s.renderHorizontal(ctx, bounds)
	}
}

func (s Split) measureHorizontal(ctx *Context, constraints Constraints) Size {
	firstW, secondW := s.horizontalWidths(ctx, constraints.maxWidth())
	firstSize := Size{}
	secondSize := Size{}
	if s.First != nil {
		firstSize = s.First.Measure(ctx, Constraints{MaxW: firstW, MaxH: constraints.MaxH})
	}
	if s.Second != nil {
		secondSize = s.Second.Measure(ctx, Constraints{MaxW: secondW, MaxH: constraints.MaxH})
	}
	width := firstSize.W + secondSize.W
	if s.First != nil && s.Second != nil {
		width += s.Gap
	}
	return constraints.Clamp(Size{W: width, H: max(firstSize.H, secondSize.H)})
}

func (s Split) measureVertical(ctx *Context, constraints Constraints) Size {
	firstH, secondH := s.verticalHeights(ctx, constraints.maxHeight())
	firstSize := Size{}
	secondSize := Size{}
	if s.First != nil {
		firstSize = s.First.Measure(ctx, Constraints{MaxW: constraints.MaxW, MaxH: firstH})
	}
	if s.Second != nil {
		secondSize = s.Second.Measure(ctx, Constraints{MaxW: constraints.MaxW, MaxH: secondH})
	}
	height := firstSize.H + secondSize.H
	if s.First != nil && s.Second != nil {
		height += s.Gap
	}
	return constraints.Clamp(Size{W: max(firstSize.W, secondSize.W), H: height})
}

func (s Split) renderHorizontal(ctx *Context, bounds Rect) Surface {
	base := BlankSurface(bounds.W, bounds.H)
	firstW, secondW := s.horizontalWidths(ctx, bounds.W)
	if s.First != nil {
		base = base.placeAt(0, 0, s.First.Render(ctx, Rect{X: bounds.X, Y: bounds.Y, W: firstW, H: bounds.H}))
	}
	if s.Second != nil {
		secondX := firstW
		if s.First != nil {
			secondX += s.Gap
		}
		base = base.placeAt(secondX, 0, s.Second.Render(ctx, Rect{X: bounds.X + secondX, Y: bounds.Y, W: secondW, H: bounds.H}))
	}
	return base
}

func (s Split) renderVertical(ctx *Context, bounds Rect) Surface {
	base := BlankSurface(bounds.W, bounds.H)
	firstH, secondH := s.verticalHeights(ctx, bounds.H)
	if s.First != nil {
		base = base.placeAt(0, 0, s.First.Render(ctx, Rect{X: bounds.X, Y: bounds.Y, W: bounds.W, H: firstH}))
	}
	if s.Second != nil {
		secondY := firstH
		if s.First != nil {
			secondY += s.Gap
		}
		base = base.placeAt(0, secondY, s.Second.Render(ctx, Rect{X: bounds.X, Y: bounds.Y + secondY, W: bounds.W, H: secondH}))
	}
	return base
}

func (s Split) horizontalWidths(ctx *Context, width int) (int, int) {
	width = max(0, width)
	if s.First == nil {
		return 0, width
	}
	if s.Second == nil {
		return width, 0
	}
	gap := max(0, s.Gap)
	available := max(0, width-gap)
	switch {
	case s.FirstFixed > 0:
		return min(s.FirstFixed, available), max(0, available-min(s.FirstFixed, available))
	case s.SecondFixed > 0:
		second := min(s.SecondFixed, available)
		return max(0, available-second), second
	default:
		first := s.First.Measure(ctx, Constraints{MaxW: available, MaxH: 0}).W
		if first <= 0 {
			first = available / 2
		}
		first = min(first, available)
		return first, max(0, available-first)
	}
}

func (s Split) verticalHeights(ctx *Context, height int) (int, int) {
	height = max(0, height)
	if s.First == nil {
		return 0, height
	}
	if s.Second == nil {
		return height, 0
	}
	gap := max(0, s.Gap)
	available := max(0, height-gap)
	switch {
	case s.FirstFixed > 0:
		return min(s.FirstFixed, available), max(0, available-min(s.FirstFixed, available))
	case s.SecondFixed > 0:
		second := min(s.SecondFixed, available)
		return max(0, available-second), second
	default:
		first := s.First.Measure(ctx, Constraints{MaxW: 0, MaxH: available}).H
		if first <= 0 {
			first = available / 2
		}
		first = min(first, available)
		return first, max(0, available-first)
	}
}

type Section struct {
	Title      string
	Child      Element
	Width      int
	Padding    Insets
	Background lipgloss.Color
	Foreground lipgloss.Color
	BorderColor lipgloss.Color
}

func (s Section) Measure(ctx *Context, constraints Constraints) Size {
	width := s.Width
	if width <= 0 {
		width = constraints.MaxW
	}
	if width <= 0 {
		width = 40
	}
	children := s.children(ctx)
	return constraints.Clamp(children.Measure(ctx, Constraints{MaxW: width, MaxH: constraints.MaxH}))
}

func (s Section) Render(ctx *Context, bounds Rect) Surface {
	width := bounds.W
	if width <= 0 {
		width = s.Width
	}
	if width <= 0 {
		width = 40
	}
	return s.children(ctx).Render(ctx, Rect{X: bounds.X, Y: bounds.Y, W: width, H: bounds.H})
}

func (s Section) children(ctx *Context) Element {
	body := Panel{
		Child:      s.Child,
		Width:      s.Width,
		Padding:    s.Padding,
		Background: firstColor(s.Background, ctx.Palette.SidebarBackground),
		Foreground: firstColor(s.Foreground, ctx.Palette.SidebarForeground),
		BorderColor: firstColor(s.BorderColor, ctx.Palette.SidebarBorder),
	}
	if strings.TrimSpace(s.Title) == "" {
		return body
	}
	return Column{
		Children: []Child{
			Fixed(Label{
				Text: s.Title,
				Style: lipgloss.NewStyle().
					Bold(true).
					Foreground(ctx.Palette.AssistantTimestampText),
			}),
			Fixed(body),
		},
		Spacing: 1,
	}
}

type ListItem struct {
	ControlID      string
	Primary        string
	Secondary      string
	Tertiary       string
	PrimaryWidth   int
	SecondaryWidth int
	TertiaryWidth  int
}

type List struct {
	Items          []ListItem
	Width          int
	Selected       int
	Focused        bool
}

func (l List) Measure(ctx *Context, constraints Constraints) Size {
	width := l.Width
	if width <= 0 {
		width = constraints.MaxW
	}
	if width <= 0 {
		width = 72
	}
	return constraints.Clamp(Size{W: width, H: len(l.Items)})
}

func (l List) Render(ctx *Context, bounds Rect) Surface {
	width := bounds.W
	if width <= 0 {
		width = l.Width
	}
	if width <= 0 {
		width = 72
	}
	children := make([]Child, 0, len(l.Items))
	for idx, item := range l.Items {
		children = append(children, Fixed(SelectableRow{
			ControlID:      item.ControlID,
			Primary:        item.Primary,
			Secondary:      item.Secondary,
			Tertiary:       item.Tertiary,
			Width:          width,
			PrimaryWidth:   item.PrimaryWidth,
			SecondaryWidth: item.SecondaryWidth,
			TertiaryWidth:  item.TertiaryWidth,
			Selected:       idx == l.Selected,
			Focused:        l.Focused && idx == l.Selected,
		}))
	}
	return Column{Children: children}.Render(ctx, Rect{X: bounds.X, Y: bounds.Y, W: width, H: bounds.H})
}

type TableColumn struct {
	Title      string
	Width      int
	AlignRight bool
}

type TableRow struct {
	ControlID string
	Cells     []string
	Selected  bool
	Focused   bool
}

type Table struct {
	Columns    []TableColumn
	Rows       []TableRow
	Width      int
	ShowHeader bool
}

func (t Table) Measure(ctx *Context, constraints Constraints) Size {
	width := t.width(constraints.MaxW)
	height := len(t.Rows)
	if t.ShowHeader {
		height++
	}
	return constraints.Clamp(Size{W: width, H: height})
}

func (t Table) Render(ctx *Context, bounds Rect) Surface {
	width := t.width(bounds.W)
	children := make([]Child, 0, len(t.Rows)+1)
	if t.ShowHeader {
		children = append(children, Fixed(TextPane{Content: t.header(ctx.Palette, width)}))
	}
	for _, row := range t.Rows {
		children = append(children, Fixed(HitBox{
			ID: row.ControlID,
			Child: TextPane{
				Content: t.renderRow(ctx.Palette, width, row),
			},
		}))
	}
	return Column{Children: children}.Render(ctx, Rect{X: bounds.X, Y: bounds.Y, W: width, H: bounds.H})
}

func (t Table) width(fallback int) int {
	width := t.Width
	if width <= 0 {
		width = fallback
	}
	if width <= 0 {
		width = 72
	}
	return width
}

func (t Table) header(palette theme.Palette, width int) string {
	parts := make([]string, 0, len(t.Columns))
	for idx, col := range t.Columns {
		style := lipgloss.NewStyle().
			Width(col.Width).
			Bold(true).
			Foreground(palette.AssistantTimestampText)
		if col.AlignRight {
			style = style.Align(lipgloss.Right)
		}
		parts = append(parts, style.Render(truncateText(strings.TrimSpace(col.Title), col.Width)))
		if idx < len(t.Columns)-1 {
			parts = append(parts, lipgloss.NewStyle().Width(2).Render(""))
		}
	}
	return lipgloss.NewStyle().Width(width).Render(lipgloss.JoinHorizontal(lipgloss.Top, parts...))
}

func (t Table) renderRow(palette theme.Palette, width int, row TableRow) string {
	selectionBackground := palette.SelectionBackground
	selectionForeground := palette.SelectionForeground
	if strings.TrimSpace(string(selectionBackground)) == "" {
		selectionBackground = palette.UserTextBackground
	}
	if strings.TrimSpace(string(selectionForeground)) == "" {
		selectionForeground = palette.UserTextForeground
	}
	background := lipgloss.Color("")
	foreground := lipgloss.Color("")
	if row.Selected {
		background = selectionBackground
		foreground = selectionForeground
	}
	if row.Focused {
		background = deriveFocusedBackground(selectionBackground, firstNonEmptyColor(palette.ScreenBackground, palette.SidebarBackground, palette.UserTextBackground))
		foreground = selectionForeground
	}
	parts := make([]string, 0, len(t.Columns)*2)
	for idx, col := range t.Columns {
		value := ""
		if idx < len(row.Cells) {
			value = compactInlineText(row.Cells[idx])
		}
		style := lipgloss.NewStyle().Width(col.Width)
		if idx == 0 {
			style = style.Bold(true)
		} else {
			style = style.Foreground(palette.AssistantTimestampText)
		}
		if col.AlignRight {
			style = style.Align(lipgloss.Right)
		}
		if strings.TrimSpace(string(background)) != "" {
			style = style.Background(background).Foreground(foreground)
		}
		parts = append(parts, style.Render(truncateText(value, col.Width)))
		if idx < len(t.Columns)-1 {
			gap := lipgloss.NewStyle().Width(2)
			if strings.TrimSpace(string(background)) != "" {
				gap = gap.Background(background)
			}
			parts = append(parts, gap.Render(""))
		}
	}
	rowStyle := lipgloss.NewStyle().Width(width)
	if strings.TrimSpace(string(background)) != "" {
		rowStyle = rowStyle.Background(background).Foreground(foreground)
	}
	return rowStyle.Render(lipgloss.JoinHorizontal(lipgloss.Top, parts...))
}

func firstColor(values ...lipgloss.Color) lipgloss.Color {
	for _, value := range values {
		if strings.TrimSpace(string(value)) != "" {
			return value
		}
	}
	return ""
}
