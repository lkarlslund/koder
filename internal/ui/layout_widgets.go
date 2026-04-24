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

func (s Split) WalkChildren(_ *Context, visit func(Element)) {
	if visit == nil {
		return
	}
	if s.First != nil {
		visit(s.First)
	}
	if s.Second != nil {
		visit(s.Second)
	}
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
	Title       string
	Child       Element
	Width       int
	Padding     Insets
	Background  lipgloss.Color
	Foreground  lipgloss.Color
	BorderColor lipgloss.Color
}

func (s Section) WalkChildren(ctx *Context, visit func(Element)) {
	if visit == nil {
		return
	}
	child := s.children(ctx)
	if child != nil {
		visit(child)
	}
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
	body := Border{
		Child:       s.Child,
		Width:       s.Width,
		Padding:     s.Padding,
		Background:  firstColor(s.Background, ctx.Palette.SidebarBackground),
		Foreground:  firstColor(s.Foreground, ctx.Palette.SidebarForeground),
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
	Items              []ListItem
	Width              int
	Selected           int
	Focused            bool
	OnSelectionChanged func(index int, item ListItem)
	OnActivate         func(index int, item ListItem)
}

func (l List) WalkChildren(ctx *Context, visit func(Element)) {
	if visit == nil {
		return
	}
	width := l.Width
	if width <= 0 && ctx != nil {
		width = 72
	}
	for idx, item := range l.Items {
		visit(SelectableRow{
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
		})
	}
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

func (l *List) Move(delta int) bool {
	if len(l.Items) == 0 {
		return false
	}
	return l.SetSelected(l.Selected + delta)
}

func (l *List) SetSelected(index int) bool {
	if len(l.Items) == 0 {
		if l.Selected != 0 {
			l.Selected = 0
			return true
		}
		return false
	}
	if index < 0 {
		index = 0
	}
	if index >= len(l.Items) {
		index = len(l.Items) - 1
	}
	if index == l.Selected {
		return false
	}
	l.Selected = index
	if l.OnSelectionChanged != nil {
		l.OnSelectionChanged(index, l.Items[index])
	}
	return true
}

func (l *List) ActivateSelected() bool {
	if len(l.Items) == 0 || l.Selected < 0 || l.Selected >= len(l.Items) || l.OnActivate == nil {
		return false
	}
	l.OnActivate(l.Selected, l.Items[l.Selected])
	return true
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
		children = append(children, Fixed(tableHeader{
			Palette: ctx.Palette,
			Columns: t.Columns,
			Width:   width,
		}))
	}
	for _, row := range t.Rows {
		children = append(children, Fixed(HitBox{
			ID: row.ControlID,
			Child: tableRow{
				Palette: ctx.Palette,
				Columns: t.Columns,
				Width:   width,
				Row:     row,
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

type tableHeader struct {
	Palette theme.Palette
	Columns []TableColumn
	Width   int
}

func (h tableHeader) Measure(_ *Context, constraints Constraints) Size {
	return constraints.Clamp(Size{W: h.Width, H: 1})
}

func (h tableHeader) Render(_ *Context, bounds Rect) Surface {
	width := h.Width
	if width <= 0 {
		width = bounds.W
	}
	s := BlankSurface(width, 1)
	style := CellStyle{FG: cellColor(h.Palette.AssistantTimestampText), Bold: true}
	colX := 0
	for idx, col := range h.Columns {
		text := truncateText(strings.TrimSpace(col.Title), col.Width)
		writeX := colX
		if col.AlignRight {
			writeX += max(0, col.Width-PlainWidth(text))
		}
		s.WriteText(writeX, 0, text, style)
		colX += col.Width
		if idx < len(h.Columns)-1 {
			colX += 2
		}
	}
	return s.normalize(bounds.W, bounds.H)
}

type tableRow struct {
	Palette theme.Palette
	Columns []TableColumn
	Width   int
	Row     TableRow
}

func (r tableRow) Measure(_ *Context, constraints Constraints) Size {
	return constraints.Clamp(Size{W: r.Width, H: 1})
}

func (r tableRow) Render(_ *Context, bounds Rect) Surface {
	width := r.Width
	if width <= 0 {
		width = bounds.W
	}
	selectionBackground := r.Palette.SelectionBackground
	selectionForeground := r.Palette.SelectionForeground
	if strings.TrimSpace(string(selectionBackground)) == "" {
		selectionBackground = r.Palette.UserTextBackground
	}
	if strings.TrimSpace(string(selectionForeground)) == "" {
		selectionForeground = r.Palette.UserTextForeground
	}
	rowStyle := CellStyle{}
	primaryStyle := CellStyle{Bold: true}
	cellStyle := CellStyle{FG: cellColor(r.Palette.AssistantTimestampText)}
	if r.Row.Selected {
		rowStyle = CellStyle{BG: cellColor(selectionBackground), FG: cellColor(selectionForeground)}
		primaryStyle = CellStyle{BG: cellColor(selectionBackground), FG: cellColor(selectionForeground), Bold: true}
		cellStyle = CellStyle{BG: cellColor(selectionBackground), FG: cellColor(selectionForeground)}
	}
	if r.Row.Focused {
		focusedBackground := deriveFocusedBackground(selectionBackground, firstNonEmptyColor(r.Palette.ScreenBackground, r.Palette.SidebarBackground, r.Palette.UserTextBackground))
		rowStyle = CellStyle{BG: cellColor(focusedBackground), FG: cellColor(selectionForeground)}
		primaryStyle = CellStyle{BG: cellColor(focusedBackground), FG: cellColor(selectionForeground), Bold: true}
		cellStyle = CellStyle{BG: cellColor(focusedBackground), FG: cellColor(selectionForeground)}
	}
	s := BlankSurface(width, 1)
	for x := 0; x < width; x++ {
		s.setCell(x, 0, Cell{Text: " ", Width: 1, Style: rowStyle})
	}
	colX := 0
	for idx, col := range r.Columns {
		value := ""
		if idx < len(r.Row.Cells) {
			value = compactInlineText(r.Row.Cells[idx])
		}
		value = truncateText(value, col.Width)
		writeX := colX
		if col.AlignRight {
			writeX += max(0, col.Width-PlainWidth(value))
		}
		style := cellStyle
		if idx == 0 {
			style = primaryStyle
		}
		s.WriteText(writeX, 0, value, style)
		colX += col.Width
		if idx < len(r.Columns)-1 {
			colX += 2
		}
	}
	return s.normalize(bounds.W, bounds.H)
}

func firstColor(values ...lipgloss.Color) lipgloss.Color {
	for _, value := range values {
		if strings.TrimSpace(string(value)) != "" {
			return value
		}
	}
	return ""
}

type ScrollFrame struct {
	Child   Element
	OffsetY int
	Width   int
	Height  int
}

func (s ScrollFrame) Measure(ctx *Context, constraints Constraints) Size {
	if s.Child == nil {
		return constraints.Clamp(Size{})
	}
	width := s.Width
	if width <= 0 {
		width = constraints.MaxW
	}
	height := s.Height
	if height <= 0 {
		height = constraints.MaxH
	}
	childSize := s.Child.Measure(ctx, Constraints{MaxW: width, MaxH: 0})
	if width <= 0 {
		width = childSize.W
	}
	if height <= 0 {
		height = childSize.H
	}
	return constraints.Clamp(Size{W: width, H: height})
}

func (s ScrollFrame) Render(ctx *Context, bounds Rect) Surface {
	base := BlankSurface(bounds.W, bounds.H)
	if s.Child == nil || bounds.W <= 0 || bounds.H <= 0 {
		return base
	}
	childSize := s.Child.Measure(ctx, Constraints{MaxW: bounds.W, MaxH: 0})
	childHeight := max(bounds.H, childSize.H)
	childSurface := s.Child.Render(ctx, Rect{
		X: bounds.X,
		Y: bounds.Y - max(0, s.OffsetY),
		W: bounds.W,
		H: childHeight,
	})
	return base.placeAt(0, -max(0, s.OffsetY), childSurface)
}
