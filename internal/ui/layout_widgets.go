package ui

import (
	"strings"

	"github.com/lkarlslund/koder/internal/theme"
)

type Section struct {
	BaseNode
	Title       string
	Child       Node
	Width       int
	Padding     Insets
	Background  CellColor
	Foreground  CellColor
	BorderColor CellColor
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

func (s Section) Paint(ctx *Context, canvas Canvas) {
	if canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	width := canvas.Width()
	if width <= 0 {
		width = s.Width
	}
	if width <= 0 {
		width = 40
	}
	paintNodeInto(ctx, s.children(ctx), Rect{X: canvas.origin.X, Y: canvas.origin.Y, W: width, H: canvas.Height()}, canvas.surface)
}

func (s Section) children(ctx *Context) Node {
	body := Border{
		Child:       s.Child,
		Width:       s.Width,
		Padding:     s.Padding,
		Background:  firstColor(s.Background, ctx.Palette.SidebarBackground),
		Foreground:  firstColor(s.Foreground, ctx.Palette.SidebarForeground),
		BorderColor: firstColor(s.BorderColor, ctx.Palette.SidebarBorder),
	}
	if strings.TrimSpace(s.Title) == "" {
		return AsNode(body)
	}
	return AsNode(NewFlexBox(
		DirectionVertical,
		[]Child{
			Fixed(Label{
				Text: s.Title,
				Style: NewStyle().
					Bold(true).
					Foreground(ctx.Palette.AssistantTimestampText),
			}),
			Fixed(body),
		},
		1,
	))
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

func (l List) Paint(ctx *Context, canvas Canvas) {
	if canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	width := canvas.Width()
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
	paintNodeInto(ctx, AsNode(NewFlexBox(DirectionVertical, children, 0)), Rect{X: canvas.origin.X, Y: canvas.origin.Y, W: width, H: canvas.Height()}, canvas.surface)
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

func (t Table) Paint(ctx *Context, canvas Canvas) {
	if canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	width := t.width(canvas.Width())
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
			Child: AsNode(tableRow{
				Palette: ctx.Palette,
				Columns: t.Columns,
				Width:   width,
				Row:     row,
			}),
		}))
	}
	paintNodeInto(ctx, AsNode(NewFlexBox(DirectionVertical, children, 0)), Rect{X: canvas.origin.X, Y: canvas.origin.Y, W: width, H: canvas.Height()}, canvas.surface)
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

func (h tableHeader) Paint(_ *Context, canvas Canvas) {
	width := h.Width
	if width <= 0 {
		width = canvas.Width()
	}
	style := CellStyle{FG: cellColor(h.Palette.AssistantTimestampText)}.WithBold(true)
	colX := 0
	for idx, col := range h.Columns {
		text := truncateText(strings.TrimSpace(col.Title), col.Width)
		writeX := colX
		if col.AlignRight {
			writeX += max(0, col.Width-PlainWidth(text))
		}
		canvas.WriteText(writeX, 0, text, style)
		colX += col.Width
		if idx < len(h.Columns)-1 {
			colX += 2
		}
	}
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

func (r tableRow) Paint(_ *Context, canvas Canvas) {
	width := r.Width
	if width <= 0 {
		width = canvas.Width()
	}
	selectionBackground := r.Palette.SelectionBackground
	selectionForeground := r.Palette.SelectionForeground
	if !selectionBackground.Valid() {
		selectionBackground = r.Palette.UserTextBackground
	}
	if !selectionForeground.Valid() {
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
	canvas.Fill(Rect{W: width, H: 1}, rowStyle)
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
		canvas.WriteText(writeX, 0, value, style)
		colX += col.Width
		if idx < len(r.Columns)-1 {
			colX += 2
		}
	}
}

func firstColor(values ...CellColor) CellColor {
	for _, value := range values {
		if value.Valid() {
			return value
		}
	}
	return CellColor{}
}

type scrollWindowRenderer interface {
	RenderVisibleInto(ctx *Context, width, height, offset int, dst *Surface) (int, int)
	RenderBottomInto(ctx *Context, width, height int, dst *Surface) (int, int)
}

type ScrollBox struct {
	BaseNode
	Child   Node
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

func (s ScrollBox) Paint(ctx *Context, canvas Canvas) {
	if canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	surface, _, _ := s.RenderVisible(ctx, canvas.Width(), canvas.Height(), s.OffsetY)
	canvas.BlitSurface(0, 0, surface)
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
	paintNodeInto(ctx, s.Child, Rect{
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
	paintNodeInto(ctx, s.Child, Rect{
		X: 0,
		Y: -offset,
		W: width,
		H: childHeight,
	}, &base)
	return base, totalHeight, offset
}

type ScrollFrame = ScrollBox
