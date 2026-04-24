package ui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/lkarlslund/koder/internal/theme"
)

type SelectionGridItem struct {
	ControlID   string
	Title       string
	Description string
}

type SelectionGrid struct {
	Items      []SelectionGridItem
	Width      int
	Columns    int
	Selected   int
	Focused    bool
	CellHeight int
}

func (g SelectionGrid) Measure(ctx *Context, constraints Constraints) Size {
	width, _, rows, cellHeight, rowGap := g.layout(constraints.MaxW)
	return constraints.Clamp(Size{W: width, H: rows*cellHeight + max(0, rows-1)*rowGap})
}

func (g SelectionGrid) Render(ctx *Context, bounds Rect) Surface {
	width, cellWidth, rows, cellHeight, rowGap := g.layout(bounds.W)
	s := TransparentSurface(width, rows*cellHeight+max(0, rows-1)*rowGap)
	for idx, item := range g.Items {
		col := idx % g.columns(width)
		row := idx / g.columns(width)
		x := col * (cellWidth + 2)
		y := row * (cellHeight + rowGap)
		cardBounds := Rect{X: x, Y: y, W: cellWidth, H: cellHeight}
		card := selectionGridCard{
			Item:     item,
			Selected: idx == g.Selected,
			Focused:  g.Focused && idx == g.Selected,
		}
		if ctx != nil && ctx.Runtime != nil && strings.TrimSpace(item.ControlID) != "" {
			ctx.Runtime.Register(Control{ID: item.ControlID, Rect: cardBounds, Enabled: true})
		}
		s = s.PlaceAt(x, y, card.Render(ctx, Rect{W: cellWidth, H: cellHeight}))
	}
	return s.normalize(bounds.W, bounds.H)
}

func (g SelectionGrid) layout(fallbackWidth int) (width, cellWidth, rows, cellHeight, rowGap int) {
	width = g.Width
	if width <= 0 {
		width = fallbackWidth
	}
	if width <= 0 {
		width = 72
	}
	cols := g.columns(width)
	gap := 2
	cellWidth = max(18, (width-gap*(cols-1))/cols)
	width = cellWidth*cols + gap*(cols-1)
	rows = 0
	if len(g.Items) > 0 {
		rows = (len(g.Items) + cols - 1) / cols
	}
	cellHeight = g.CellHeight
	if cellHeight <= 0 {
		cellHeight = 4
	}
	rowGap = 1
	if cellHeight <= 1 {
		rowGap = 0
	}
	return width, cellWidth, rows, cellHeight, rowGap
}

func (g SelectionGrid) columns(width int) int {
	cols := g.Columns
	if cols <= 0 {
		cols = 2
	}
	if width <= 0 {
		return cols
	}
	for cols > 1 {
		cellWidth := (width - 2*(cols-1)) / cols
		if cellWidth >= 18 {
			break
		}
		cols--
	}
	return max(1, cols)
}

type selectionGridCard struct {
	Item     SelectionGridItem
	Selected bool
	Focused  bool
}

func (c selectionGridCard) Render(ctx *Context, bounds Rect) Surface {
	palette := theme.Palette{}
	if ctx != nil {
		palette = ctx.Palette
	}
	borderColor := palette.SidebarBorder
	background := firstNonEmptyColor(palette.SidebarBackground, palette.ScreenBackground)
	foreground := firstNonEmptyColor(palette.SidebarForeground, palette.MarkdownText)
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(foreground)
	descriptionStyle := lipgloss.NewStyle().Foreground(firstNonEmptyColor(palette.AssistantTimestampText, palette.ComposerMutedText))
	if c.Selected {
		background = firstNonEmptyColor(palette.SelectionBackground, palette.UserTextBackground, background)
		foreground = firstNonEmptyColor(palette.SelectionForeground, palette.UserTextForeground, foreground)
		borderColor = firstNonEmptyColor(palette.SelectionBackground, palette.ActivityText, borderColor)
		titleStyle = lipgloss.NewStyle().Bold(true).Foreground(foreground)
		descriptionStyle = lipgloss.NewStyle().Foreground(foreground)
	}
	if c.Focused {
		background = deriveFocusedBackground(
			firstNonEmptyColor(palette.SelectionBackground, palette.UserTextBackground, background),
			firstNonEmptyColor(palette.ScreenBackground, palette.SidebarBackground, palette.UserTextBackground),
		)
		foreground = firstNonEmptyColor(palette.SelectionForeground, palette.UserTextForeground, foreground)
		borderColor = firstNonEmptyColor(palette.SelectionForeground, palette.UserTextForeground, borderColor)
		titleStyle = lipgloss.NewStyle().Bold(true).Foreground(foreground)
		descriptionStyle = lipgloss.NewStyle().Foreground(foreground)
	}
	if bounds.H <= 1 {
		fillStyle := CellStyle{}
		if background != "" {
			fillStyle.BG = cellColor(background)
		}
		if foreground != "" {
			fillStyle.FG = cellColor(foreground)
		}
		text := truncateText(strings.TrimSpace(c.Item.Title), max(1, bounds.W))
		if pad := max(0, bounds.W-PlainWidth(text)); pad > 0 {
			left := pad / 2
			right := pad - left
			text = strings.Repeat(" ", left) + text + strings.Repeat(" ", right)
		}
		return FilledLineSurface(bounds.W, text, fillStyle, lipglossToCellStyle(titleStyle)).normalize(bounds.W, bounds.H)
	}
	content := Column{
		Children: []Child{
			Fixed(Label{Text: truncateText(strings.TrimSpace(c.Item.Title), max(1, bounds.W-4)), Style: titleStyle}),
			Fixed(Paragraph{Text: truncateText(strings.TrimSpace(c.Item.Description), max(1, (bounds.W-4)*2)), Style: descriptionStyle}),
		},
		Spacing: 1,
	}
	return Border{
		Child:        content,
		Width:        bounds.W,
		Height:       bounds.H,
		Padding:      UniformInsets(1),
		Background:   background,
		Foreground:   foreground,
		BorderLeft:   true,
		BorderRight:  true,
		BorderTop:    true,
		BorderBottom: true,
		BorderColor:  borderColor,
	}.Render(ctx, bounds)
}

func selectionGridMove(index, total, columns, deltaCol, deltaRow int) int {
	if total <= 0 {
		return 0
	}
	if index < 0 {
		index = 0
	}
	if index >= total {
		index = total - 1
	}
	columns = max(1, columns)
	col := index % columns
	row := index / columns
	col += deltaCol
	row += deltaRow
	if col < 0 {
		col = 0
	}
	if col >= columns {
		col = columns - 1
	}
	if row < 0 {
		row = 0
	}
	maxRow := (total - 1) / columns
	if row > maxRow {
		row = maxRow
	}
	next := row*columns + col
	if next >= total {
		next = total - 1
	}
	return next
}

func selectionGridControlID(index int) string {
	return "picker-item-" + strconv.Itoa(index)
}
