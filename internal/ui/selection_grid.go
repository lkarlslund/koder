package ui

import (
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

func (g SelectionGrid) Paint(ctx *Context, canvas Canvas) {
	width, cellWidth, rows, cellHeight, rowGap := g.layout(canvas.Width())
	_ = rows
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
			ctx.Runtime.Register(Control{ID: item.ControlID, Rect: cardBounds.Translate(canvas.origin.X, canvas.origin.Y), Enabled: true})
		}
		card.Paint(ctx, canvas.Subrect(cardBounds))
	}
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

func (c selectionGridCard) Paint(ctx *Context, canvas Canvas) {
	selectionGridCardPainter{card: c}.Paint(ctx, canvas)
}

type selectionGridCardPainter struct {
	card selectionGridCard
}

func (p selectionGridCardPainter) Paint(ctx *Context, canvas Canvas) {
	c := p.card
	palette := theme.Palette{}
	if ctx != nil {
		palette = ctx.Palette
	}
	baseBackground := firstNonEmptyColor(palette.SidebarBackground, palette.ScreenBackground)
	selectedBackground := firstNonEmptyColor(palette.SelectionBackground, palette.UserTextBackground, palette.UserAccentBar, baseBackground)
	borderColor := palette.SidebarBorder
	background := baseBackground
	foreground := firstNonEmptyColor(palette.SidebarForeground, palette.MarkdownText)
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(foreground)
	descriptionStyle := lipgloss.NewStyle().Foreground(firstNonEmptyColor(palette.AssistantTimestampText, palette.ComposerMutedText))
	if c.Selected {
		background = selectedBackground
		foreground = firstNonEmptyColor(palette.SelectionForeground, palette.UserTextForeground, foreground)
		borderColor = firstNonEmptyColor(selectedBackground, palette.ActivityText, borderColor)
		titleStyle = lipgloss.NewStyle().Bold(true).Foreground(foreground)
		descriptionStyle = lipgloss.NewStyle().Foreground(foreground)
	}
	if c.Focused {
		background = deriveFocusedBackground(
			selectedBackground,
			firstNonEmptyColor(palette.ScreenBackground, palette.SidebarBackground, palette.UserTextBackground),
		)
		if strings.TrimSpace(string(background)) == "" || background == baseBackground {
			background = selectedBackground
		}
		foreground = firstNonEmptyColor(palette.SelectionForeground, palette.UserTextForeground, foreground)
		borderColor = firstNonEmptyColor(palette.SelectionForeground, palette.UserTextForeground, borderColor)
		titleStyle = lipgloss.NewStyle().Bold(true).Foreground(foreground)
		descriptionStyle = lipgloss.NewStyle().Foreground(foreground)
	}
	if canvas.Height() <= 1 {
		fillStyle := CellStyle{}
		if background != "" {
			fillStyle.BG = cellColor(background)
		}
		if foreground != "" {
			fillStyle.FG = cellColor(foreground)
		}
		textStyle := lipglossToCellStyle(titleStyle)
		if background != "" {
			textStyle.BG = cellColor(background)
		}
		text := truncateText(strings.TrimSpace(c.Item.Title), max(1, canvas.Width()))
		if pad := max(0, canvas.Width()-PlainWidth(text)); pad > 0 {
			left := pad / 2
			right := pad - left
			text = strings.Repeat(" ", left) + text + strings.Repeat(" ", right)
		}
		canvas.BlitSurface(0, 0, FilledLineSurface(canvas.Width(), text, fillStyle, textStyle).normalize(canvas.Width(), canvas.Height()))
		return
	}
	content := FlexBox{
		Direction: DirectionVertical,
		Children: []Child{
			Fixed(Label{Text: truncateText(strings.TrimSpace(c.Item.Title), max(1, canvas.Width()-4)), Style: titleStyle}),
			Fixed(Paragraph{Text: truncateText(strings.TrimSpace(c.Item.Description), max(1, (canvas.Width()-4)*2)), Style: descriptionStyle}),
		},
		Spacing: 1,
	}
	renderElementInto(ctx, Border{
		Child:        content,
		Width:        canvas.Width(),
		Height:       canvas.Height(),
		Padding:      UniformInsets(1),
		Background:   background,
		Foreground:   foreground,
		BorderLeft:   true,
		BorderRight:  true,
		BorderTop:    true,
		BorderBottom: true,
		BorderColor:  borderColor,
	}, Rect{
		X: canvas.origin.X,
		Y: canvas.origin.Y,
		W: canvas.Width(),
		H: canvas.Height(),
	}, canvas.surface)
}
