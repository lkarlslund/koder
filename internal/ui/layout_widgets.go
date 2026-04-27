package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/lkarlslund/koder/internal/theme"
)

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
	return renderOwnedSurface(ctx, bounds, s.RenderTo)
}

func (s Section) RenderTo(ctx *Context, bounds Rect, dst *Surface) {
	if dst == nil {
		return
	}
	width := bounds.W
	if width <= 0 {
		width = s.Width
	}
	if width <= 0 {
		width = 40
	}
	renderElementInto(ctx, s.children(ctx), Rect{X: bounds.X, Y: bounds.Y, W: width, H: bounds.H}, dst)
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
	return FlexBox{
		Direction: DirectionVertical,
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
	return renderOwnedSurface(ctx, bounds, l.RenderTo)
}

func (l List) RenderTo(ctx *Context, bounds Rect, dst *Surface) {
	if dst == nil {
		return
	}
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
	renderElementInto(ctx, FlexBox{Direction: DirectionVertical, Children: children}, Rect{X: bounds.X, Y: bounds.Y, W: width, H: bounds.H}, dst)
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
	return renderOwnedSurface(ctx, bounds, t.RenderTo)
}

func (t Table) RenderTo(ctx *Context, bounds Rect, dst *Surface) {
	if dst == nil {
		return
	}
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
	renderElementInto(ctx, FlexBox{Direction: DirectionVertical, Children: children}, Rect{X: bounds.X, Y: bounds.Y, W: width, H: bounds.H}, dst)
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
	style := CellStyle{FG: cellColor(h.Palette.AssistantTimestampText)}.WithBold(true)
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
	primaryStyle := CellStyle{}.WithBold(true)
	cellStyle := CellStyle{FG: cellColor(r.Palette.AssistantTimestampText)}
	if r.Row.Selected {
		rowStyle = CellStyle{BG: cellColor(selectionBackground), FG: cellColor(selectionForeground)}
		primaryStyle = CellStyle{BG: cellColor(selectionBackground), FG: cellColor(selectionForeground)}.WithBold(true)
		cellStyle = CellStyle{BG: cellColor(selectionBackground), FG: cellColor(selectionForeground)}
	}
	if r.Row.Focused {
		focusedBackground := deriveFocusedBackground(selectionBackground, firstNonEmptyColor(r.Palette.ScreenBackground, r.Palette.SidebarBackground, r.Palette.UserTextBackground))
		rowStyle = CellStyle{BG: cellColor(focusedBackground), FG: cellColor(selectionForeground)}
		primaryStyle = CellStyle{BG: cellColor(focusedBackground), FG: cellColor(selectionForeground)}.WithBold(true)
		cellStyle = CellStyle{BG: cellColor(focusedBackground), FG: cellColor(selectionForeground)}
	}
	s := BlankSurface(width, 1)
	for x := 0; x < width; x++ {
		s.setCell(x, 0, blankCell(rowStyle))
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

type scrollWindowRenderer interface {
	RenderVisibleInto(ctx *Context, width, height, offset int, dst *Surface) (int, int)
	RenderBottomInto(ctx *Context, width, height int, dst *Surface) (int, int)
}

type ScrollBox struct {
	Child   Element
	OffsetY int
	Width   int
	Height  int
}

func (s ScrollBox) Measure(ctx *Context, constraints Constraints) Size {
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

func (s ScrollBox) Render(ctx *Context, bounds Rect) Surface {
	surface, _, _ := s.RenderVisible(ctx, bounds.W, bounds.H, s.OffsetY)
	return surface.normalize(bounds.W, bounds.H)
}

func (s ScrollBox) RenderVisible(ctx *Context, width, height, offset int) (Surface, int, int) {
	base := TransparentSurface(width, height)
	if s.Child == nil || width <= 0 || height <= 0 {
		return base, 0, 0
	}
	if child, ok := s.Child.(scrollWindowRenderer); ok {
		totalHeight, appliedOffset := child.RenderVisibleInto(ctx, width, height, offset, &base)
		return base, totalHeight, appliedOffset
	}
	childSize := s.Child.Measure(ctx, Constraints{MaxW: width, MaxH: 0})
	totalHeight := childSize.H
	maxOffset := max(0, totalHeight-height)
	offset = min(max(0, offset), maxOffset)
	childHeight := max(height, totalHeight)
	renderElementInto(ctx, s.Child, Rect{
		X: 0,
		Y: -offset,
		W: width,
		H: childHeight,
	}, &base)
	return base, totalHeight, offset
}

func (s ScrollBox) RenderBottom(ctx *Context, width, height int) (Surface, int, int) {
	base := TransparentSurface(width, height)
	if s.Child == nil || width <= 0 || height <= 0 {
		return base, 0, 0
	}
	if child, ok := s.Child.(scrollWindowRenderer); ok {
		totalHeight, appliedOffset := child.RenderBottomInto(ctx, width, height, &base)
		return base, totalHeight, appliedOffset
	}
	childSize := s.Child.Measure(ctx, Constraints{MaxW: width, MaxH: 0})
	totalHeight := childSize.H
	offset := max(0, totalHeight-height)
	childHeight := max(height, totalHeight)
	renderElementInto(ctx, s.Child, Rect{
		X: 0,
		Y: -offset,
		W: width,
		H: childHeight,
	}, &base)
	return base, totalHeight, offset
}

type ScrollFrame = ScrollBox
