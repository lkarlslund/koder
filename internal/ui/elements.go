package ui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/lkarlslund/koder/internal/theme"
)

type Point struct {
	X int
	Y int
}

type Size struct {
	W int
	H int
}

type Rect struct {
	X int
	Y int
	W int
	H int
}

func (r Rect) Contains(p Point) bool {
	return p.X >= r.X && p.X < r.X+r.W && p.Y >= r.Y && p.Y < r.Y+r.H
}

func (r Rect) Inset(in Insets) Rect {
	x := r.X + in.Left
	y := r.Y + in.Top
	w := r.W - in.Left - in.Right
	h := r.H - in.Top - in.Bottom
	if w < 0 {
		w = 0
	}
	if h < 0 {
		h = 0
	}
	return Rect{X: x, Y: y, W: w, H: h}
}

type Insets struct {
	Top    int
	Right  int
	Bottom int
	Left   int
}

func UniformInsets(v int) Insets {
	return Insets{Top: v, Right: v, Bottom: v, Left: v}
}

func SymmetricInsets(horizontal, vertical int) Insets {
	return Insets{Top: vertical, Right: horizontal, Bottom: vertical, Left: horizontal}
}

type Constraints struct {
	MinW int
	MaxW int
	MinH int
	MaxH int
}

func NewConstraints(maxW, maxH int) Constraints {
	return Constraints{MaxW: maxW, MaxH: maxH}
}

func (c Constraints) Clamp(size Size) Size {
	size.W = clampInt(size.W, c.MinW, c.maxWidth())
	size.H = clampInt(size.H, c.MinH, c.maxHeight())
	return size
}

func (c Constraints) Tighten(size Size) Constraints {
	return Constraints{MinW: size.W, MaxW: size.W, MinH: size.H, MaxH: size.H}
}

func (c Constraints) Deflate(in Insets) Constraints {
	maxW := c.maxWidth()
	maxH := c.maxHeight()
	next := Constraints{
		MinW: max(0, c.MinW-in.Left-in.Right),
		MaxW: max(0, maxW-in.Left-in.Right),
		MinH: max(0, c.MinH-in.Top-in.Bottom),
		MaxH: max(0, maxH-in.Top-in.Bottom),
	}
	if c.MaxW == 0 {
		next.MaxW = 0
	}
	if c.MaxH == 0 {
		next.MaxH = 0
	}
	return next
}

func (c Constraints) maxWidth() int {
	if c.MaxW <= 0 {
		return int(^uint(0) >> 1)
	}
	return c.MaxW
}

func (c Constraints) maxHeight() int {
	if c.MaxH <= 0 {
		return int(^uint(0) >> 1)
	}
	return c.MaxH
}

type Control struct {
	ID      string
	Rect    Rect
	Enabled bool
}

type Runtime struct {
	controls []Control
}

func (r *Runtime) BeginFrame() {
	if r == nil {
		return
	}
	r.controls = r.controls[:0]
}

func (r *Runtime) Register(control Control) {
	if r == nil {
		return
	}
	r.controls = append(r.controls, control)
}

func (r *Runtime) Len() int {
	if r == nil {
		return 0
	}
	return len(r.controls)
}

func (r *Runtime) OffsetFrom(start, dx, dy int) {
	if r == nil {
		return
	}
	if start < 0 {
		start = 0
	}
	if start >= len(r.controls) {
		return
	}
	for idx := start; idx < len(r.controls); idx++ {
		r.controls[idx].Rect.X += dx
		r.controls[idx].Rect.Y += dy
	}
}

func (r *Runtime) Controls() []Control {
	if r == nil {
		return nil
	}
	out := make([]Control, len(r.controls))
	copy(out, r.controls)
	return out
}

func (r *Runtime) Hit(p Point) (Control, bool) {
	if r == nil {
		return Control{}, false
	}
	for i := len(r.controls) - 1; i >= 0; i-- {
		control := r.controls[i]
		if !control.Enabled {
			continue
		}
		if control.Rect.Contains(p) {
			return control, true
		}
	}
	return Control{}, false
}

type Context struct {
	Palette theme.Palette
	Runtime *Runtime
}

type CellColor struct {
	R     uint8
	G     uint8
	B     uint8
	Valid bool
}

func ParseCellColor(value string) CellColor {
	value = strings.TrimSpace(value)
	if len(value) != 7 || value[0] != '#' {
		return CellColor{}
	}
	r, err := strconv.ParseUint(value[1:3], 16, 8)
	if err != nil {
		return CellColor{}
	}
	g, err := strconv.ParseUint(value[3:5], 16, 8)
	if err != nil {
		return CellColor{}
	}
	b, err := strconv.ParseUint(value[5:7], 16, 8)
	if err != nil {
		return CellColor{}
	}
	return CellColor{R: uint8(r), G: uint8(g), B: uint8(b), Valid: true}
}

func CellColorFromLipgloss(value lipgloss.Color) CellColor {
	return ParseCellColor(string(value))
}

func cellColor(value lipgloss.Color) CellColor {
	return CellColorFromLipgloss(value)
}

type CellStyle struct {
	FG            CellColor
	BG            CellColor
	Bold          bool
	BoldSet       bool
	Italic        bool
	ItalicSet     bool
	Underline     bool
	UnderlineSet  bool
	Strikethrough bool
	StrikeSet     bool
}

func (s CellStyle) isZero() bool {
	return !s.FG.Valid && !s.BG.Valid &&
		!s.hasBold() &&
		!s.hasItalic() &&
		!s.hasUnderline() &&
		!s.hasStrikethrough()
}

func (s CellStyle) equal(other CellStyle) bool {
	return s.FG == other.FG &&
		s.BG == other.BG &&
		s.Bold == other.Bold &&
		s.BoldSet == other.BoldSet &&
		s.Italic == other.Italic &&
		s.ItalicSet == other.ItalicSet &&
		s.Underline == other.Underline &&
		s.UnderlineSet == other.UnderlineSet &&
		s.Strikethrough == other.Strikethrough &&
		s.StrikeSet == other.StrikeSet
}

func (s CellStyle) Merge(overlay CellStyle) CellStyle {
	out := s
	if overlay.FG.Valid {
		out.FG = overlay.FG
	}
	if overlay.BG.Valid {
		out.BG = overlay.BG
	}
	if overlay.hasBold() {
		out.Bold = overlay.Bold
		out.BoldSet = overlay.BoldSet || overlay.Bold
	}
	if overlay.hasItalic() {
		out.Italic = overlay.Italic
		out.ItalicSet = overlay.ItalicSet || overlay.Italic
	}
	if overlay.hasUnderline() {
		out.Underline = overlay.Underline
		out.UnderlineSet = overlay.UnderlineSet || overlay.Underline
	}
	if overlay.hasStrikethrough() {
		out.Strikethrough = overlay.Strikethrough
		out.StrikeSet = overlay.StrikeSet || overlay.Strikethrough
	}
	return out
}

func (s CellStyle) hasBold() bool {
	return s.BoldSet || s.Bold
}

func (s CellStyle) hasItalic() bool {
	return s.ItalicSet || s.Italic
}

func (s CellStyle) hasUnderline() bool {
	return s.UnderlineSet || s.Underline
}

func (s CellStyle) hasStrikethrough() bool {
	return s.StrikeSet || s.Strikethrough
}

type Cell struct {
	Text         string
	Width        int
	Style        CellStyle
	Continuation bool
	Painted      bool
}

type Surface struct {
	lines []string
	w     int
	h     int
	cells []Cell
}

func BlankSurface(width, height int) Surface {
	if width < 0 {
		width = 0
	}
	if height < 0 {
		height = 0
	}
	cells := make([]Cell, width*height)
	for i := range cells {
		cells[i] = Cell{Text: " ", Width: 1, Painted: true}
	}
	return Surface{w: width, h: height, cells: cells}
}

func TransparentSurface(width, height int) Surface {
	if width < 0 {
		width = 0
	}
	if height < 0 {
		height = 0
	}
	cells := make([]Cell, width*height)
	return Surface{w: width, h: height, cells: cells}
}

func SurfaceFromString(input string) Surface {
	if input == "" {
		return Surface{}
	}
	lines := strings.Split(input, "\n")
	width := 0
	for _, line := range lines {
		width = max(width, PlainWidth(line))
	}
	s := TransparentSurface(width, len(lines))
	for y, line := range lines {
		s.WriteText(0, y, line, CellStyle{})
	}
	return s
}

func (s Surface) Lines() []string {
	if s.isCellBuffer() {
		return s.cellLines()
	}
	out := make([]string, len(s.lines))
	copy(out, s.lines)
	return out
}

func (s Surface) Size() Size {
	if s.isCellBuffer() {
		return Size{W: s.w, H: s.h}
	}
	width := 0
	for _, line := range s.lines {
		width = max(width, PlainWidth(line))
	}
	return Size{W: width, H: len(s.lines)}
}

func (s Surface) Normalize(width, height int) Surface {
	return s.normalize(width, height)
}

func (s Surface) PlaceAt(x, y int, child Surface) Surface {
	return s.placeAt(x, y, child)
}

func (s Surface) SurfaceWidth() int {
	if s.isCellBuffer() {
		return s.w
	}
	return s.Size().W
}

func (s Surface) SurfaceHeight() int {
	if s.isCellBuffer() {
		return s.h
	}
	return s.Size().H
}

func (s Surface) SurfaceCellText(x, y int) string {
	return s.cellAt(x, y).Text
}

func (s Surface) SurfaceCellWidth(x, y int) int {
	return s.cellAt(x, y).Width
}

func (s Surface) SurfaceCellContinuation(x, y int) bool {
	return s.cellAt(x, y).Continuation
}

func (s Surface) SurfaceCellFG(x, y int) (uint8, uint8, uint8, bool) {
	cell := s.cellAt(x, y).Style.FG
	if !cell.Valid {
		return 0, 0, 0, false
	}
	return cell.R, cell.G, cell.B, true
}

func (s Surface) SurfaceCellBG(x, y int) (uint8, uint8, uint8, bool) {
	cell := s.cellAt(x, y).Style.BG
	if !cell.Valid {
		return 0, 0, 0, false
	}
	return cell.R, cell.G, cell.B, true
}

func (s Surface) SurfaceCellBold(x, y int) bool {
	return s.cellAt(x, y).Style.Bold
}

func (s Surface) SurfaceCellItalic(x, y int) bool {
	return s.cellAt(x, y).Style.Italic
}

func (s Surface) SurfaceCellUnderline(x, y int) bool {
	return s.cellAt(x, y).Style.Underline
}

func (s Surface) SurfaceCellStrikethrough(x, y int) bool {
	return s.cellAt(x, y).Style.Strikethrough
}

func (s Surface) normalize(width, height int) Surface {
	if s.isCellBuffer() {
		out := TransparentSurface(width, height)
		copyH := min(height, s.h)
		copyW := min(width, s.w)
		for y := 0; y < copyH; y++ {
			for x := 0; x < copyW; x++ {
				out.setCell(x, y, s.cellAt(x, y))
			}
		}
		return out
	}
	if width < 0 {
		width = 0
	}
	if height < 0 {
		height = 0
	}
	out := TransparentSurface(width, height)
	for i := 0; i < height; i++ {
		line := ""
		if i < len(s.lines) {
			line = PlainTruncate(s.lines[i], width, "")
		}
		if delta := width - PlainWidth(line); delta > 0 {
			line += strings.Repeat(" ", delta)
		}
		out.WriteText(0, i, line, CellStyle{})
	}
	return out
}

func (s Surface) placeAt(x, y int, child Surface) Surface {
	if s.isCellBuffer() {
		if !child.isCellBuffer() {
			child = child.normalize(child.Size().W, child.Size().H)
		}
		return s.blitAt(x, y, child)
	}
	baseLines := s.Lines()
	childLines := child.Lines()
	if len(baseLines) == 0 || len(childLines) == 0 {
		return s
	}
	base := baseLines
	for row, childLine := range childLines {
		targetY := y + row
		if targetY < 0 || targetY >= len(base) {
			continue
		}
		baseWidth := PlainWidth(base[targetY])
		if x >= baseWidth {
			continue
		}
		childLine = PlainTruncate(childLine, max(0, baseWidth-x), "")
		if childLine == "" {
			continue
		}
		base[targetY] = overlayLine(base[targetY], childLine, x)
	}
	return Surface{lines: base}
}

func (s Surface) isCellBuffer() bool {
	return len(s.cells) > 0 || (s.w == 0 && s.h == 0 && s.lines == nil)
}

func (s Surface) cellIndex(x, y int) int {
	return y*s.w + x
}

func (s Surface) cellAt(x, y int) Cell {
	if x < 0 || y < 0 || x >= s.w || y >= s.h || len(s.cells) == 0 {
		return Cell{}
	}
	return s.cells[s.cellIndex(x, y)]
}

func (s *Surface) setCell(x, y int, cell Cell) {
	if x < 0 || y < 0 || x >= s.w || y >= s.h || len(s.cells) == 0 {
		return
	}
	if !cell.Painted && (cell.Text != "" || cell.Width > 0 || cell.Continuation || !cell.Style.isZero()) {
		cell.Painted = true
	}
	s.cells[s.cellIndex(x, y)] = cell
}

func (s Surface) cellLines() []string {
	if !s.isCellBuffer() {
		return s.Lines()
	}
	lines := make([]string, s.h)
	for y := 0; y < s.h; y++ {
		var b strings.Builder
		for x := 0; x < s.w; x++ {
			cell := s.cellAt(x, y)
			if cell.Continuation {
				continue
			}
			if !cell.Painted {
				b.WriteString(" ")
				continue
			}
			text := cell.Text
			if text == "" {
				text = " "
			}
			b.WriteString(text)
		}
		lines[y] = b.String()
	}
	return lines
}

func (s Surface) blitAt(x, y int, child Surface) Surface {
	if !s.isCellBuffer() || !child.isCellBuffer() {
		return s
	}
	out := s
	for cy := 0; cy < child.h; cy++ {
		targetY := y + cy
		if targetY < 0 || targetY >= out.h {
			continue
		}
		for cx := 0; cx < child.w; cx++ {
			targetX := x + cx
			if targetX < 0 || targetX >= out.w {
				continue
			}
			out.setCell(targetX, targetY, compositeCell(out.cellAt(targetX, targetY), child.cellAt(cx, cy)))
		}
	}
	return out
}

func (s *Surface) WriteText(x, y int, text string, style CellStyle) {
	if !s.isCellBuffer() || y < 0 || y >= s.h {
		return
	}
	col := x
	for _, r := range text {
		grapheme := string(r)
		width := PlainWidth(grapheme)
		if width <= 0 {
			continue
		}
		if col >= s.w {
			break
		}
		if col >= 0 {
			s.setCell(col, y, Cell{Text: grapheme, Width: width, Style: style, Painted: true})
			for extra := 1; extra < width && col+extra < s.w; extra++ {
				s.setCell(col+extra, y, Cell{Continuation: true, Style: style, Painted: true})
			}
		}
		col += width
	}
}

func (s *Surface) WriteStyledSpans(x, y int, spans []StyledSpan, base CellStyle) {
	if !s.isCellBuffer() || y < 0 || y >= s.h {
		return
	}
	col := x
	for _, span := range spans {
		if span.Text == "" {
			continue
		}
		style := base.Merge(span.Style)
		for _, r := range span.Text {
			grapheme := string(r)
			width := PlainWidth(grapheme)
			if width <= 0 {
				continue
			}
			if col >= s.w {
				return
			}
			if col >= 0 {
				s.setCell(col, y, Cell{Text: grapheme, Width: width, Style: style, Painted: true})
				for extra := 1; extra < width && col+extra < s.w; extra++ {
					s.setCell(col+extra, y, Cell{Continuation: true, Style: style, Painted: true})
				}
			}
			col += width
		}
	}
}

func FilledLineSurface(width int, text string, fillStyle, textStyle CellStyle) Surface {
	s := BlankSurface(width, 1)
	for x := 0; x < width; x++ {
		s.setCell(x, 0, Cell{Text: " ", Width: 1, Style: fillStyle})
	}
	s.WriteText(0, 0, PlainTruncate(text, width, ""), textStyle)
	return s
}

func compositeCell(base, overlay Cell) Cell {
	if !overlay.Painted {
		return base
	}
	out := base
	if overlay.paintsGlyph() {
		out.Text = overlay.Text
		out.Width = overlay.Width
		out.Continuation = overlay.Continuation
	}
	out.Style = base.Style.Merge(overlay.Style)
	out.Painted = true
	return out
}

func (c Cell) paintsGlyph() bool {
	return c.Continuation || c.Width > 0 || c.Text != ""
}

func overlayLine(base, overlay string, offset int) string {
	if offset < 0 {
		offset = 0
	}
	baseWidth := PlainWidth(base)
	if baseWidth == 0 {
		if offset > 0 {
			base = strings.Repeat(" ", offset)
		}
		return base + overlay
	}
	start := PlainTruncate(base, offset, "")
	remaining := max(0, baseWidth-offset)
	overlay = PlainTruncate(overlay, remaining, "")
	endStart := offset + PlainWidth(overlay)
	end := ""
	if endStart < baseWidth {
		end = substringByWidth(base, endStart, baseWidth)
	}
	line := start + overlay + end
	if delta := baseWidth - PlainWidth(line); delta > 0 {
		line += strings.Repeat(" ", delta)
	}
	return line
}

func substringByWidth(input string, start, end int) string {
	if end <= start {
		return ""
	}
	truncated := PlainTruncate(input, end, "")
	if start <= 0 {
		return truncated
	}
	return strings.TrimPrefix(truncated, PlainTruncate(input, start, ""))
}

type Element interface {
	Measure(ctx *Context, constraints Constraints) Size
	Render(ctx *Context, bounds Rect) Surface
}

type TreeWalker interface {
	WalkChildren(ctx *Context, visit func(Element))
}

type CacheInvalidator interface {
	InvalidateCache()
}

func InvalidateElementCaches(ctx *Context, element Element) {
	if element == nil {
		return
	}
	invalidateElementCaches(ctx, element)
}

func invalidateElementCaches(ctx *Context, element Element) {
	if element == nil {
		return
	}
	if invalidator, ok := element.(CacheInvalidator); ok {
		invalidator.InvalidateCache()
	}
	if walker, ok := element.(TreeWalker); ok {
		walker.WalkChildren(ctx, func(child Element) {
			invalidateElementCaches(ctx, child)
		})
	}
}

type Visibility interface {
	Visible() bool
}

type BoxModel interface {
	Box() BoxProps
}

type Display int

const (
	DisplayFlex Display = iota
	DisplayNone
)

type BoxProps struct {
	Display     Display
	VisibleFlag bool
	Hidden      bool
	Grow        int
	Shrink      int
	Basis       int
	MinW        int
	MinH        int
	MaxW        int
	MaxH        int
	HAlign      Alignment
	VAlign      Alignment
}

func (p BoxProps) Visible() bool {
	if p.Display == DisplayNone {
		return false
	}
	if p.Hidden {
		return false
	}
	return true
}

func (p BoxProps) Box() BoxProps {
	return p
}

func (p BoxProps) constrained(axis Axis, size int) int {
	switch axis {
	case AxisHorizontal:
		return clampBoxDimension(size, p.MinW, p.MaxW)
	default:
		return clampBoxDimension(size, p.MinH, p.MaxH)
	}
}

func clampBoxDimension(size, minValue, maxValue int) int {
	if size < minValue {
		size = minValue
	}
	if maxValue > 0 && size > maxValue {
		size = maxValue
	}
	if size < 0 {
		size = 0
	}
	return size
}

func elementVisible(element Element) bool {
	if element == nil {
		return false
	}
	visible, ok := element.(Visibility)
	if !ok {
		return true
	}
	return visible.Visible()
}

func ElementVisible(element Element) bool {
	return elementVisible(element)
}

func elementBox(element Element) BoxProps {
	if element == nil {
		return BoxProps{}
	}
	box, ok := element.(BoxModel)
	if !ok {
		return BoxProps{Display: DisplayFlex, VisibleFlag: true}
	}
	props := box.Box()
	if props.Display == 0 {
		props.Display = DisplayFlex
	}
	return props
}

type Static struct {
	Content string
}

func (s Static) Measure(_ *Context, constraints Constraints) Size {
	return constraints.Clamp(SurfaceFromString(s.Content).Size())
}

func (s Static) Render(_ *Context, bounds Rect) Surface {
	return SurfaceFromString(s.Content).normalize(bounds.W, bounds.H)
}

type SurfaceBox struct {
	Surface Surface
}

func (b SurfaceBox) Measure(_ *Context, constraints Constraints) Size {
	return constraints.Clamp(b.Surface.Size())
}

func (b SurfaceBox) Render(_ *Context, bounds Rect) Surface {
	return b.Surface.normalize(bounds.W, bounds.H)
}

type VisibleElement struct {
	BoxProps
	Child Element
}

func (e VisibleElement) Visible() bool {
	return e.BoxProps.Visible() && e.Child != nil
}

func (e VisibleElement) Measure(ctx *Context, constraints Constraints) Size {
	if !e.Visible() {
		return Size{}
	}
	return constraints.Clamp(e.Child.Measure(ctx, constraints))
}

func (e VisibleElement) Render(ctx *Context, bounds Rect) Surface {
	if !e.Visible() {
		return Surface{}
	}
	if e.HAlign == AlignStart && e.VAlign == AlignStart {
		return e.Child.Render(ctx, bounds)
	}
	size := e.Child.Measure(ctx, NewConstraints(bounds.W, bounds.H))
	x := 0
	y := 0
	switch e.HAlign {
	case AlignCenter:
		x = max(0, (bounds.W-size.W)/2)
	case AlignEnd:
		x = max(0, bounds.W-size.W)
	}
	switch e.VAlign {
	case AlignCenter:
		y = max(0, (bounds.H-size.H)/2)
	case AlignEnd:
		y = max(0, bounds.H-size.H)
	}
	base := TransparentSurface(bounds.W, bounds.H)
	child := e.Child.Render(ctx, Rect{X: bounds.X + x, Y: bounds.Y + y, W: min(bounds.W, size.W), H: min(bounds.H, size.H)})
	return base.placeAt(x, y, child)
}

func (e VisibleElement) WalkChildren(_ *Context, visit func(Element)) {
	if e.Child == nil || visit == nil {
		return
	}
	visit(e.Child)
}

type Child struct {
	Element Element
	Flex    int
	Grow    int
	Shrink  int
	Basis   int
}

func Fixed(element Element) Child {
	return Child{Element: element}
}

func Flex(element Element, weight int) Child {
	if weight <= 0 {
		weight = 1
	}
	return Child{Element: element, Flex: weight, Grow: weight, Shrink: 1}
}

func (c Child) effectiveBox() BoxProps {
	props := elementBox(c.Element)
	if c.Basis > 0 {
		props.Basis = c.Basis
	}
	if c.Grow > 0 {
		props.Grow = c.Grow
	} else if c.Flex > 0 && props.Grow == 0 {
		props.Grow = c.Flex
	}
	if c.Shrink > 0 {
		props.Shrink = c.Shrink
	} else if c.Flex > 0 && props.Shrink == 0 {
		props.Shrink = 1
	}
	if props.Display == 0 {
		props.Display = DisplayFlex
	}
	return props
}

type Spacer struct {
	W int
	H int
}

func (s Spacer) Measure(_ *Context, constraints Constraints) Size {
	return constraints.Clamp(Size(s))
}

func (s Spacer) Render(_ *Context, bounds Rect) Surface {
	return TransparentSurface(bounds.W, bounds.H)
}

type FlexDirection int

const (
	DirectionHorizontal FlexDirection = iota
	DirectionVertical
)

type FlexBox struct {
	Direction FlexDirection
	Children  []Child
	Spacing   int
}

func (b FlexBox) Measure(ctx *Context, constraints Constraints) Size {
	axis := b.axis()
	targetMain := constraints.maxWidth()
	if axis == AxisVertical {
		targetMain = constraints.maxHeight()
	}
	plan := b.layoutPlan(ctx, constraints, targetMain)
	size := Size{W: plan.main, H: plan.cross}
	if axis == AxisVertical {
		size = Size{W: plan.cross, H: plan.main}
	}
	return constraints.Clamp(size)
}

func (b FlexBox) Render(ctx *Context, bounds Rect) Surface {
	if b.axis() == AxisVertical {
		return b.renderVertical(ctx, bounds)
	}
	return b.renderHorizontal(ctx, bounds)
}

func (b FlexBox) WalkChildren(_ *Context, visit func(Element)) {
	if visit == nil {
		return
	}
	for _, child := range b.Children {
		if child.Element != nil {
			visit(child.Element)
		}
	}
}

func (b FlexBox) axis() Axis {
	if b.Direction == DirectionVertical {
		return AxisVertical
	}
	return AxisHorizontal
}

func (b FlexBox) renderVertical(ctx *Context, bounds Rect) Surface {
	base := TransparentSurface(bounds.W, bounds.H)
	plan := b.layoutPlan(ctx, NewConstraints(bounds.W, bounds.H), bounds.H)
	y := 0
	for idx, item := range plan.items {
		if idx > 0 {
			y += b.Spacing
		}
		slotH := max(0, item.main)
		slotW := bounds.W
		childW := item.box.constrained(AxisHorizontal, slotW)
		childH := item.box.constrained(AxisVertical, slotH)
		x := alignedOffset(item.box.HAlign, slotW, childW)
		dy := alignedOffset(item.box.VAlign, slotH, childH)
		childSurface := item.child.Element.Render(ctx, Rect{
			X: bounds.X + x,
			Y: bounds.Y + y + dy,
			W: childW,
			H: childH,
		})
		base = base.placeAt(x, y+dy, childSurface)
		y += slotH
	}
	return base
}

func (b FlexBox) renderHorizontal(ctx *Context, bounds Rect) Surface {
	base := TransparentSurface(bounds.W, bounds.H)
	plan := b.layoutPlan(ctx, NewConstraints(bounds.W, bounds.H), bounds.W)
	x := 0
	for idx, item := range plan.items {
		if idx > 0 {
			x += b.Spacing
		}
		slotW := max(0, item.main)
		slotH := bounds.H
		childW := item.box.constrained(AxisHorizontal, slotW)
		childH := item.box.constrained(AxisVertical, slotH)
		dx := alignedOffset(item.box.HAlign, slotW, childW)
		y := alignedOffset(item.box.VAlign, slotH, childH)
		childSurface := item.child.Element.Render(ctx, Rect{
			X: bounds.X + x + dx,
			Y: bounds.Y + y,
			W: childW,
			H: childH,
		})
		base = base.placeAt(x+dx, y, childSurface)
		x += slotW
	}
	return base
}

type Inset struct {
	Padding Insets
	Child   Element
}

func (i Inset) Measure(ctx *Context, constraints Constraints) Size {
	if !elementVisible(i.Child) {
		return constraints.Clamp(Size{})
	}
	childSize := i.Child.Measure(ctx, constraints.Deflate(i.Padding))
	return constraints.Clamp(Size{
		W: childSize.W + i.Padding.Left + i.Padding.Right,
		H: childSize.H + i.Padding.Top + i.Padding.Bottom,
	})
}

func (i Inset) Render(ctx *Context, bounds Rect) Surface {
	base := TransparentSurface(bounds.W, bounds.H)
	if !elementVisible(i.Child) {
		return base
	}
	childBounds := bounds.Inset(i.Padding)
	childSurface := i.Child.Render(ctx, Rect{X: childBounds.X, Y: childBounds.Y, W: childBounds.W, H: childBounds.H})
	return base.placeAt(i.Padding.Left, i.Padding.Top, childSurface)
}

func (i Inset) WalkChildren(_ *Context, visit func(Element)) {
	if i.Child == nil || visit == nil {
		return
	}
	visit(i.Child)
}

type Constrained struct {
	Constraints Constraints
	Child       Element
}

func (c Constrained) Measure(ctx *Context, constraints Constraints) Size {
	if !elementVisible(c.Child) {
		return constraints.Clamp(Size{})
	}
	merged := Constraints{
		MinW: max(constraints.MinW, c.Constraints.MinW),
		MaxW: min(constraints.maxWidth(), c.Constraints.maxWidth()),
		MinH: max(constraints.MinH, c.Constraints.MinH),
		MaxH: min(constraints.maxHeight(), c.Constraints.maxHeight()),
	}
	return constraints.Clamp(c.Child.Measure(ctx, merged))
}

func (c Constrained) Render(ctx *Context, bounds Rect) Surface {
	if !elementVisible(c.Child) {
		return TransparentSurface(bounds.W, bounds.H)
	}
	size := c.Measure(ctx, NewConstraints(bounds.W, bounds.H))
	return c.Child.Render(ctx, Rect{X: bounds.X, Y: bounds.Y, W: size.W, H: size.H}).normalize(bounds.W, bounds.H)
}

func (c Constrained) WalkChildren(_ *Context, visit func(Element)) {
	if c.Child == nil || visit == nil {
		return
	}
	visit(c.Child)
}

type Stack struct {
	Children []Element
}

func (s Stack) Measure(ctx *Context, constraints Constraints) Size {
	size := Size{}
	for _, child := range s.Children {
		if !elementVisible(child) {
			continue
		}
		childSize := child.Measure(ctx, constraints)
		size.W = max(size.W, childSize.W)
		size.H = max(size.H, childSize.H)
	}
	return constraints.Clamp(size)
}

func (s Stack) Render(ctx *Context, bounds Rect) Surface {
	base := TransparentSurface(bounds.W, bounds.H)
	for _, child := range s.Children {
		if !elementVisible(child) {
			continue
		}
		base = base.placeAt(0, 0, child.Render(ctx, bounds))
	}
	return base
}

func (s Stack) WalkChildren(_ *Context, visit func(Element)) {
	if visit == nil {
		return
	}
	for _, child := range s.Children {
		if child != nil {
			visit(child)
		}
	}
}

type Alignment int

const (
	AlignStart Alignment = iota
	AlignCenter
	AlignEnd
)

type Align struct {
	Horizontal Alignment
	Vertical   Alignment
	Child      Element
}

func (a Align) Measure(ctx *Context, constraints Constraints) Size {
	if a.Child == nil {
		return constraints.Clamp(Size{})
	}
	return constraints.Clamp(a.Child.Measure(ctx, constraints))
}

func (a Align) Render(ctx *Context, bounds Rect) Surface {
	base := TransparentSurface(bounds.W, bounds.H)
	if a.Child == nil {
		return base
	}
	size := a.Child.Measure(ctx, NewConstraints(bounds.W, bounds.H))
	size = NewConstraints(bounds.W, bounds.H).Clamp(size)
	x := 0
	y := 0
	switch a.Horizontal {
	case AlignCenter:
		x = max(0, (bounds.W-size.W)/2)
	case AlignEnd:
		x = max(0, bounds.W-size.W)
	}
	switch a.Vertical {
	case AlignCenter:
		y = max(0, (bounds.H-size.H)/2)
	case AlignEnd:
		y = max(0, bounds.H-size.H)
	}
	childSurface := a.Child.Render(ctx, Rect{X: bounds.X + x, Y: bounds.Y + y, W: size.W, H: size.H})
	return base.placeAt(x, y, childSurface)
}

func clampInt(value, low, high int) int {
	if high < low {
		high = low
	}
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

type Axis int

const (
	AxisHorizontal Axis = iota
	AxisVertical
)

type flexItem struct {
	child Child
	box   BoxProps
	main  int
	cross int
}

type flexPlan struct {
	items []flexItem
	main  int
	cross int
}

func (b FlexBox) layoutPlan(ctx *Context, constraints Constraints, targetMain int) flexPlan {
	return computeFlexPlan(ctx, b.Children, b.Spacing, b.axis(), constraints, targetMain)
}

func computeFlexPlan(ctx *Context, children []Child, spacing int, axis Axis, constraints Constraints, targetMain int) flexPlan {
	items := make([]flexItem, 0, len(children))
	mainUsed := 0
	cross := 0
	for _, child := range children {
		if !elementVisible(child.Element) {
			continue
		}
		box := child.effectiveBox()
		size := child.Element.Measure(ctx, constraintsForAxis(axis, constraints, 0))
		mainSize := axisMain(axis, size)
		if box.Basis > 0 {
			mainSize = box.Basis
		}
		mainSize = box.constrained(axis, mainSize)
		cross = max(cross, axisCross(axis, size))
		items = append(items, flexItem{
			child: child,
			box:   box,
			main:  mainSize,
			cross: axisCross(axis, size),
		})
		mainUsed += mainSize
	}
	if len(items) > 1 {
		mainUsed += spacing * (len(items) - 1)
	}
	if targetMain <= 0 {
		targetMain = mainUsed
	}
	delta := targetMain - mainUsed
	if delta > 0 {
		totalGrow := 0
		for _, item := range items {
			totalGrow += max(0, item.box.Grow)
		}
		if totalGrow > 0 {
			remaining := delta
			for idx := range items {
				grow := max(0, items[idx].box.Grow)
				if grow == 0 {
					continue
				}
				add := delta * grow / totalGrow
				if idx == len(items)-1 {
					add = remaining
				}
				items[idx].main = items[idx].box.constrained(axis, items[idx].main+add)
				remaining -= add
			}
		}
	}
	if delta < 0 {
		deficit := -delta
		totalShrink := 0
		for _, item := range items {
			totalShrink += max(0, item.box.Shrink)
		}
		if totalShrink > 0 {
			remaining := deficit
			for idx := range items {
				shrink := max(0, items[idx].box.Shrink)
				if shrink == 0 {
					continue
				}
				reduce := deficit * shrink / totalShrink
				if idx == len(items)-1 {
					reduce = remaining
				}
				items[idx].main = items[idx].box.constrained(axis, items[idx].main-reduce)
				remaining -= reduce
			}
		}
	}
	main := 0
	for idx, item := range items {
		if idx > 0 {
			main += spacing
		}
		main += item.main
		cross = max(cross, item.cross)
	}
	return flexPlan{items: items, main: main, cross: cross}
}

func constraintsForAxis(axis Axis, constraints Constraints, main int) Constraints {
	switch axis {
	case AxisHorizontal:
		return Constraints{MaxW: chooseMain(main, constraints.MaxW), MaxH: constraints.MaxH}
	default:
		return Constraints{MaxW: constraints.MaxW, MaxH: chooseMain(main, constraints.MaxH)}
	}
}

func chooseMain(primary, fallback int) int {
	if primary > 0 {
		return primary
	}
	return fallback
}

func axisMain(axis Axis, size Size) int {
	if axis == AxisHorizontal {
		return size.W
	}
	return size.H
}

func axisCross(axis Axis, size Size) int {
	if axis == AxisHorizontal {
		return size.H
	}
	return size.W
}

func alignedOffset(alignment Alignment, outer, inner int) int {
	switch alignment {
	case AlignCenter:
		return max(0, (outer-inner)/2)
	case AlignEnd:
		return max(0, outer-inner)
	default:
		return 0
	}
}

func RenderSurface(ctx *Context, element Element, width, height int) Surface {
	if element == nil {
		return Surface{}
	}
	if ctx == nil {
		ctx = &Context{}
	}
	size := element.Measure(ctx, Constraints{MaxW: width, MaxH: height})
	if width > 0 {
		size.W = width
	}
	if height > 0 {
		size.H = height
	}
	return element.Render(ctx, Rect{W: size.W, H: size.H})
}
