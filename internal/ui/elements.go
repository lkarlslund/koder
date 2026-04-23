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
	FG        CellColor
	BG        CellColor
	Bold      bool
	Italic    bool
	Underline bool
}

func (s CellStyle) isZero() bool {
	return !s.FG.Valid && !s.BG.Valid && !s.Bold && !s.Italic && !s.Underline
}

func (s CellStyle) equal(other CellStyle) bool {
	return s.FG == other.FG &&
		s.BG == other.BG &&
		s.Bold == other.Bold &&
		s.Italic == other.Italic &&
		s.Underline == other.Underline
}

type Cell struct {
	Text         string
	Width        int
	Style        CellStyle
	Continuation bool
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
		cells[i] = Cell{Text: " ", Width: 1}
	}
	return Surface{w: width, h: height, cells: cells}
}

func SurfaceFromString(input string) Surface {
	if input == "" {
		return Surface{}
	}
	return Surface{lines: strings.Split(input, "\n")}
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

func (s Surface) normalize(width, height int) Surface {
	if s.isCellBuffer() {
		out := BlankSurface(width, height)
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
	out := BlankSurface(width, height)
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
		return Cell{Text: " ", Width: 1}
	}
	return s.cells[s.cellIndex(x, y)]
}

func (s *Surface) setCell(x, y int, cell Cell) {
	if x < 0 || y < 0 || x >= s.w || y >= s.h || len(s.cells) == 0 {
		return
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
			out.setCell(targetX, targetY, child.cellAt(cx, cy))
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
			s.setCell(col, y, Cell{Text: grapheme, Width: width, Style: style})
			for extra := 1; extra < width && col+extra < s.w; extra++ {
				s.setCell(col+extra, y, Cell{Continuation: true, Style: style})
			}
		}
		col += width
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

type Child struct {
	Element Element
	Flex    int
}

func Fixed(element Element) Child {
	return Child{Element: element}
}

func Flex(element Element, weight int) Child {
	return Child{Element: element, Flex: weight}
}

type Spacer struct {
	W int
	H int
}

func (s Spacer) Measure(_ *Context, constraints Constraints) Size {
	return constraints.Clamp(Size{W: s.W, H: s.H})
}

func (s Spacer) Render(_ *Context, bounds Rect) Surface {
	return BlankSurface(bounds.W, bounds.H)
}

type Column struct {
	Children []Child
	Spacing  int
}

func (c Column) Measure(ctx *Context, constraints Constraints) Size {
	available := constraints.maxHeight()
	totalH := 0
	maxW := 0
	totalFlex := 0
	count := 0
	for _, child := range c.Children {
		if child.Element == nil {
			continue
		}
		if count > 0 {
			totalH += c.Spacing
		}
		count++
		if child.Flex > 0 {
			totalFlex += child.Flex
			continue
		}
		size := child.Element.Measure(ctx, Constraints{MaxW: constraints.MaxW, MaxH: max(0, available-totalH)})
		totalH += size.H
		maxW = max(maxW, size.W)
	}
	if totalFlex > 0 {
		remaining := max(0, available-totalH)
		totalH += remaining
		for _, child := range c.Children {
			if child.Element == nil || child.Flex <= 0 {
				continue
			}
			height := remaining * child.Flex / totalFlex
			size := child.Element.Measure(ctx, Constraints{MaxW: constraints.MaxW, MaxH: height})
			maxW = max(maxW, size.W)
		}
	}
	return constraints.Clamp(Size{W: maxW, H: totalH})
}

func (c Column) Render(ctx *Context, bounds Rect) Surface {
	base := BlankSurface(bounds.W, bounds.H)
	y := 0
	totalFlex := 0
	fixedHeight := 0
	count := 0
	for _, child := range c.Children {
		if child.Element == nil {
			continue
		}
		if count > 0 {
			fixedHeight += c.Spacing
		}
		count++
		if child.Flex > 0 {
			totalFlex += child.Flex
			continue
		}
		fixedHeight += child.Element.Measure(ctx, Constraints{MaxW: bounds.W, MaxH: bounds.H}).H
	}
	remaining := max(0, bounds.H-fixedHeight)
	index := 0
	for _, child := range c.Children {
		if child.Element == nil {
			continue
		}
		if index > 0 {
			y += c.Spacing
		}
		index++
		height := child.Element.Measure(ctx, Constraints{MaxW: bounds.W, MaxH: bounds.H}).H
		if child.Flex > 0 && totalFlex > 0 {
			height = remaining * child.Flex / totalFlex
		}
		childSurface := child.Element.Render(ctx, Rect{X: bounds.X, Y: bounds.Y + y, W: bounds.W, H: max(0, height)})
		base = base.placeAt(0, y, childSurface)
		y += height
	}
	return base
}

type Row struct {
	Children []Child
	Spacing  int
}

func (r Row) Measure(ctx *Context, constraints Constraints) Size {
	available := constraints.maxWidth()
	totalW := 0
	maxH := 0
	totalFlex := 0
	count := 0
	for _, child := range r.Children {
		if child.Element == nil {
			continue
		}
		if count > 0 {
			totalW += r.Spacing
		}
		count++
		if child.Flex > 0 {
			totalFlex += child.Flex
			continue
		}
		size := child.Element.Measure(ctx, Constraints{MaxW: max(0, available-totalW), MaxH: constraints.MaxH})
		totalW += size.W
		maxH = max(maxH, size.H)
	}
	if totalFlex > 0 {
		remaining := max(0, available-totalW)
		totalW += remaining
		for _, child := range r.Children {
			if child.Element == nil || child.Flex <= 0 {
				continue
			}
			width := remaining * child.Flex / totalFlex
			size := child.Element.Measure(ctx, Constraints{MaxW: width, MaxH: constraints.MaxH})
			maxH = max(maxH, size.H)
		}
	}
	return constraints.Clamp(Size{W: totalW, H: maxH})
}

func (r Row) Render(ctx *Context, bounds Rect) Surface {
	base := BlankSurface(bounds.W, bounds.H)
	x := 0
	totalFlex := 0
	fixedWidth := 0
	count := 0
	for _, child := range r.Children {
		if child.Element == nil {
			continue
		}
		if count > 0 {
			fixedWidth += r.Spacing
		}
		count++
		if child.Flex > 0 {
			totalFlex += child.Flex
			continue
		}
		fixedWidth += child.Element.Measure(ctx, Constraints{MaxW: bounds.W, MaxH: bounds.H}).W
	}
	remaining := max(0, bounds.W-fixedWidth)
	index := 0
	for _, child := range r.Children {
		if child.Element == nil {
			continue
		}
		if index > 0 {
			x += r.Spacing
		}
		index++
		width := child.Element.Measure(ctx, Constraints{MaxW: bounds.W, MaxH: bounds.H}).W
		if child.Flex > 0 && totalFlex > 0 {
			width = remaining * child.Flex / totalFlex
		}
		childSurface := child.Element.Render(ctx, Rect{X: bounds.X + x, Y: bounds.Y, W: max(0, width), H: bounds.H})
		base = base.placeAt(x, 0, childSurface)
		x += width
	}
	return base
}

type Inset struct {
	Padding Insets
	Child   Element
}

func (i Inset) Measure(ctx *Context, constraints Constraints) Size {
	if i.Child == nil {
		return constraints.Clamp(Size{})
	}
	childSize := i.Child.Measure(ctx, constraints.Deflate(i.Padding))
	return constraints.Clamp(Size{
		W: childSize.W + i.Padding.Left + i.Padding.Right,
		H: childSize.H + i.Padding.Top + i.Padding.Bottom,
	})
}

func (i Inset) Render(ctx *Context, bounds Rect) Surface {
	base := BlankSurface(bounds.W, bounds.H)
	if i.Child == nil {
		return base
	}
	childBounds := bounds.Inset(i.Padding)
	childSurface := i.Child.Render(ctx, Rect{X: childBounds.X, Y: childBounds.Y, W: childBounds.W, H: childBounds.H})
	return base.placeAt(i.Padding.Left, i.Padding.Top, childSurface)
}

type Constrained struct {
	Constraints Constraints
	Child       Element
}

func (c Constrained) Measure(ctx *Context, constraints Constraints) Size {
	if c.Child == nil {
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
	if c.Child == nil {
		return BlankSurface(bounds.W, bounds.H)
	}
	size := c.Measure(ctx, NewConstraints(bounds.W, bounds.H))
	return c.Child.Render(ctx, Rect{X: bounds.X, Y: bounds.Y, W: size.W, H: size.H}).normalize(bounds.W, bounds.H)
}

type Stack struct {
	Children []Element
}

func (s Stack) Measure(ctx *Context, constraints Constraints) Size {
	size := Size{}
	for _, child := range s.Children {
		if child == nil {
			continue
		}
		childSize := child.Measure(ctx, constraints)
		size.W = max(size.W, childSize.W)
		size.H = max(size.H, childSize.H)
	}
	return constraints.Clamp(size)
}

func (s Stack) Render(ctx *Context, bounds Rect) Surface {
	base := BlankSurface(bounds.W, bounds.H)
	for _, child := range s.Children {
		if child == nil {
			continue
		}
		base = base.placeAt(0, 0, child.Render(ctx, bounds))
	}
	return base
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
	base := BlankSurface(bounds.W, bounds.H)
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

func RenderElement(ctx *Context, element Element, width, height int) string {
	if element == nil {
		return ""
	}
	if ctx == nil {
		ctx = &Context{}
	}
	size := element.Measure(ctx, Constraints{MaxW: width, MaxH: height})
	if width > 0 {
		size.W = width
	} else if size.W == 0 {
		size.W = 0
	}
	if height > 0 {
		size.H = height
	} else if size.H == 0 {
		size.H = 0
	}
	return strings.Join(element.Render(ctx, Rect{W: size.W, H: size.H}).Lines(), "\n")
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
