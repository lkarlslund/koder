package ui

import (
	"strconv"
	"strings"
	"sync/atomic"

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

func (r Rect) Empty() bool {
	return r.W <= 0 || r.H <= 0
}

func (r Rect) Translate(dx, dy int) Rect {
	if r.Empty() {
		return Rect{}
	}
	return Rect{X: r.X + dx, Y: r.Y + dy, W: r.W, H: r.H}
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

type CellColor uint32

func NewCellColorRGB(r, g, b uint8) CellColor {
	return NewCellColorRGBA(r, g, b, 0xff)
}

func NewCellColorRGBA(r, g, b, a uint8) CellColor {
	return CellColor(uint32(a)<<24 | uint32(r)<<16 | uint32(g)<<8 | uint32(b))
}

func (c CellColor) Valid() bool {
	return c.A() != 0
}

func (c CellColor) A() uint8 {
	return uint8(uint32(c) >> 24)
}

func (c CellColor) R() uint8 {
	return uint8(uint32(c) >> 16)
}

func (c CellColor) G() uint8 {
	return uint8(uint32(c) >> 8)
}

func (c CellColor) B() uint8 {
	return uint8(c)
}

func ParseCellColor(value string) CellColor {
	value = strings.TrimSpace(value)
	if len(value) != 7 && len(value) != 9 || value[0] != '#' {
		return 0
	}
	r, err := strconv.ParseUint(value[1:3], 16, 8)
	if err != nil {
		return 0
	}
	g, err := strconv.ParseUint(value[3:5], 16, 8)
	if err != nil {
		return 0
	}
	b, err := strconv.ParseUint(value[5:7], 16, 8)
	if err != nil {
		return 0
	}
	a := uint8(0xff)
	if len(value) == 9 {
		parsedAlpha, err := strconv.ParseUint(value[7:9], 16, 8)
		if err != nil {
			return 0
		}
		a = uint8(parsedAlpha)
	}
	return NewCellColorRGBA(uint8(r), uint8(g), uint8(b), a)
}

func CellColorFromLipgloss(value lipgloss.Color) CellColor {
	return ParseCellColor(string(value))
}

func cellColor(value lipgloss.Color) CellColor {
	return CellColorFromLipgloss(value)
}

type CellStyle struct {
	FG    CellColor
	BG    CellColor
	flags uint8
}

func (s CellStyle) isZero() bool {
	return !s.FG.Valid() && !s.BG.Valid() && s.flags == 0
}

func (s CellStyle) equal(other CellStyle) bool {
	return s.FG == other.FG &&
		s.BG == other.BG &&
		s.flags == other.flags
}

func (s CellStyle) Merge(overlay CellStyle) CellStyle {
	out := s
	if overlay.FG.Valid() {
		out.FG = overlay.FG
	}
	if overlay.BG.Valid() {
		out.BG = overlay.BG
	}
	if overlay.Bold() {
		out = out.WithBold(true)
	}
	if overlay.Italic() {
		out = out.WithItalic(true)
	}
	if overlay.Underline() {
		out = out.WithUnderline(true)
	}
	if overlay.Strikethrough() {
		out = out.WithStrikethrough(true)
	}
	return out
}

const (
	cellFlagWidthMask     uint8 = 0b00000011
	cellFlagBold          uint8 = 1 << 2
	cellFlagItalic        uint8 = 1 << 3
	cellFlagUnderline     uint8 = 1 << 4
	cellFlagStrikethrough uint8 = 1 << 5
	cellFlagPainted       uint8 = 1 << 6
	cellStyleFlagMask           = cellFlagBold | cellFlagItalic | cellFlagUnderline | cellFlagStrikethrough
)

func (s CellStyle) Bold() bool {
	return s.flags&cellFlagBold != 0
}

func (s CellStyle) Italic() bool {
	return s.flags&cellFlagItalic != 0
}

func (s CellStyle) Underline() bool {
	return s.flags&cellFlagUnderline != 0
}

func (s CellStyle) Strikethrough() bool {
	return s.flags&cellFlagStrikethrough != 0
}

func (s CellStyle) WithBold(enabled bool) CellStyle {
	return s.withFlag(cellFlagBold, enabled)
}

func (s CellStyle) WithItalic(enabled bool) CellStyle {
	return s.withFlag(cellFlagItalic, enabled)
}

func (s CellStyle) WithUnderline(enabled bool) CellStyle {
	return s.withFlag(cellFlagUnderline, enabled)
}

func (s CellStyle) WithStrikethrough(enabled bool) CellStyle {
	return s.withFlag(cellFlagStrikethrough, enabled)
}

func (s CellStyle) withFlag(flag uint8, enabled bool) CellStyle {
	if enabled {
		s.flags |= flag
	} else {
		s.flags &^= flag
	}
	return s
}

type Glyph rune

const SpaceGlyph = Glyph(' ')

func GlyphFromString(value string) Glyph {
	for _, r := range value {
		return Glyph(r)
	}
	return 0
}

func (g Glyph) String() string {
	if g == 0 {
		return ""
	}
	return string(rune(g))
}

type Cell struct {
	Glyph Glyph
	FG    CellColor
	BG    CellColor
	flags uint8
}

func (c Cell) Width() int {
	return int(c.flags & cellFlagWidthMask)
}

func (c *Cell) SetWidth(width int) {
	if width < 0 {
		width = 0
	}
	if width > 2 {
		width = 2
	}
	c.flags = (c.flags &^ cellFlagWidthMask) | uint8(width)
}

func (c Cell) Painted() bool {
	return c.flags&cellFlagPainted != 0
}

func (c *Cell) SetPainted(enabled bool) {
	if enabled {
		c.flags |= cellFlagPainted
	} else {
		c.flags &^= cellFlagPainted
	}
}

func (c Cell) Bold() bool {
	return c.flags&cellFlagBold != 0
}

func (c Cell) Italic() bool {
	return c.flags&cellFlagItalic != 0
}

func (c Cell) Underline() bool {
	return c.flags&cellFlagUnderline != 0
}

func (c Cell) Strikethrough() bool {
	return c.flags&cellFlagStrikethrough != 0
}

func (c *Cell) SetStyle(style CellStyle) {
	c.FG = style.FG
	c.BG = style.BG
	c.flags = (c.flags &^ cellStyleFlagMask) | (style.flags & cellStyleFlagMask)
}

func (c Cell) Style() CellStyle {
	return CellStyle{
		FG:    c.FG,
		BG:    c.BG,
		flags: c.flags & cellStyleFlagMask,
	}
}

func newCell(glyph Glyph, width int, style CellStyle) Cell {
	cell := Cell{Glyph: glyph}
	cell.SetWidth(width)
	cell.SetStyle(style)
	cell.SetPainted(true)
	return cell
}

func blankCell(style CellStyle) Cell {
	return newCell(SpaceGlyph, 1, style)
}

func continuationCell(style CellStyle) Cell {
	cell := Cell{}
	cell.SetStyle(style)
	cell.SetPainted(true)
	return cell
}

type SurfaceAllocationStats struct {
	Blank       uint64
	Transparent uint64
}

var surfaceAllocationCounters struct {
	blank       atomic.Uint64
	transparent atomic.Uint64
}

func ResetSurfaceAllocationStats() {
	surfaceAllocationCounters.blank.Store(0)
	surfaceAllocationCounters.transparent.Store(0)
}

func SurfaceAllocationStatsSnapshot() SurfaceAllocationStats {
	return SurfaceAllocationStats{
		Blank:       surfaceAllocationCounters.blank.Load(),
		Transparent: surfaceAllocationCounters.transparent.Load(),
	}
}

type Surface struct {
	lines []string
	w     int
	h     int
	cells []Cell
	ctrls []Control
	dirty []Rect
}

func BlankSurface(width, height int) Surface {
	surfaceAllocationCounters.blank.Add(1)
	if width < 0 {
		width = 0
	}
	if height < 0 {
		height = 0
	}
	cells := make([]Cell, width*height)
	for i := range cells {
		cells[i] = blankCell(CellStyle{})
	}
	return Surface{w: width, h: height, cells: cells}
}

func TransparentSurface(width, height int) Surface {
	surfaceAllocationCounters.transparent.Add(1)
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

func (s Surface) Controls() []Control {
	if len(s.ctrls) == 0 {
		return nil
	}
	out := make([]Control, len(s.ctrls))
	copy(out, s.ctrls)
	return out
}

func (s Surface) WithDirtyRows(start, end int) Surface {
	if start < 0 || end < start {
		s.dirty = nil
		return s
	}
	width := s.SurfaceWidth()
	height := s.SurfaceHeight()
	if width <= 0 || height <= 0 || start >= height {
		s.dirty = nil
		return s
	}
	if end >= height {
		end = height - 1
	}
	s.dirty = []Rect{{X: 0, Y: start, W: width, H: end - start + 1}}
	return s
}

func (s Surface) DirtyRowRange() (start int, end int, ok bool) {
	rects, ok := s.DirtyRects()
	if !ok {
		return 0, 0, false
	}
	start = rects[0].Y
	end = rects[0].Y + rects[0].H - 1
	for _, rect := range rects[1:] {
		start = min(start, rect.Y)
		end = max(end, rect.Y+rect.H-1)
	}
	return start, end, true
}

func (s Surface) WithDirtyRects(rects ...Rect) Surface {
	if len(rects) == 0 {
		s.dirty = nil
		return s
	}
	damage := DamageSet{}
	damage.AddAll(rects)
	s.dirty = damage.Normalized(Rect{W: s.SurfaceWidth(), H: s.SurfaceHeight()})
	return s
}

func (s Surface) DirtyRects() ([]Rect, bool) {
	if len(s.dirty) == 0 {
		return nil, false
	}
	out := make([]Rect, len(s.dirty))
	copy(out, s.dirty)
	return out, true
}

func (s Surface) RegisterControls(runtime *Runtime, dx, dy int) {
	if runtime == nil || len(s.ctrls) == 0 {
		return
	}
	for _, control := range s.ctrls {
		control.Rect.X += dx
		control.Rect.Y += dy
		runtime.Register(control)
	}
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
	return s.cellAt(x, y).Glyph.String()
}

func (s Surface) SurfaceCellWidth(x, y int) int {
	return s.cellAt(x, y).Width()
}

func (s Surface) SurfaceCellContinuation(x, y int) bool {
	cell := s.cellAt(x, y)
	return cell.Painted() && cell.Width() == 0
}

func (s Surface) SurfaceCellFG(x, y int) (uint8, uint8, uint8, bool) {
	cell := s.cellAt(x, y).FG
	if !cell.Valid() {
		return 0, 0, 0, false
	}
	return cell.R(), cell.G(), cell.B(), true
}

func (s Surface) SurfaceCellBG(x, y int) (uint8, uint8, uint8, bool) {
	cell := s.cellAt(x, y).BG
	if !cell.Valid() {
		return 0, 0, 0, false
	}
	return cell.R(), cell.G(), cell.B(), true
}

func (s Surface) SurfaceCellBold(x, y int) bool {
	return s.cellAt(x, y).Bold()
}

func (s Surface) SurfaceCellItalic(x, y int) bool {
	return s.cellAt(x, y).Italic()
}

func (s Surface) SurfaceCellUnderline(x, y int) bool {
	return s.cellAt(x, y).Underline()
}

func (s Surface) SurfaceCellStrikethrough(x, y int) bool {
	return s.cellAt(x, y).Strikethrough()
}

func (s Surface) normalize(width, height int) Surface {
	if s.isCellBuffer() {
		if width == s.w && height == s.h {
			if controlsFitSurface(s.ctrls, width, height) {
				return s
			}
		}
		out := TransparentSurface(width, height)
		copyH := min(height, s.h)
		copyW := min(width, s.w)
		for y := 0; y < copyH; y++ {
			srcStart := y * s.w
			dstStart := y * out.w
			copy(out.cells[dstStart:dstStart+copyW], s.cells[srcStart:srcStart+copyW])
		}
		out.ctrls = clipControlsToSurface(s.ctrls, width, height)
		out.dirty = append(out.dirty[:0], s.dirty...)
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
	out := Surface{lines: base}
	if rects, ok := child.DirtyRects(); ok {
		damage := DamageSet{}
		for _, rect := range rects {
			damage.Add(rect.Translate(x, y))
		}
		out.dirty = damage.Normalized(Rect{W: out.SurfaceWidth(), H: out.SurfaceHeight()})
	}
	return out
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
	if !cell.Painted() && (cell.Glyph != 0 || cell.Width() > 0 || !cell.Style().isZero()) {
		cell.SetPainted(true)
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
			if cell.Painted() && cell.Width() == 0 {
				continue
			}
			if !cell.Painted() {
				b.WriteString(" ")
				continue
			}
			text := cell.Glyph.String()
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
	defaultBlank := blankCell(CellStyle{})
	for cy := 0; cy < child.h; cy++ {
		targetY := y + cy
		if targetY < 0 || targetY >= out.h {
			continue
		}
		srcStartX := 0
		dstStartX := x
		if dstStartX < 0 {
			srcStartX = -dstStartX
			dstStartX = 0
		}
		spanWidth := min(child.w-srcStartX, out.w-dstStartX)
		if spanWidth <= 0 {
			continue
		}
		srcBase := cy*child.w + srcStartX
		dstBase := targetY*out.w + dstStartX
		srcRow := child.cells[srcBase : srcBase+spanWidth]
		dstRow := out.cells[dstBase : dstBase+spanWidth]
		for cx := 0; cx < spanWidth; {
			if !srcRow[cx].Painted() {
				cx++
				continue
			}
			if dstRow[cx] == defaultBlank {
				end := cx + 1
				for end < spanWidth && srcRow[end].Painted() && dstRow[end] == defaultBlank {
					end++
				}
				copy(dstRow[cx:end], srcRow[cx:end])
				cx = end
				continue
			}
			dstRow[cx] = compositeCell(dstRow[cx], srcRow[cx])
			cx++
		}
	}
	if len(child.ctrls) > 0 {
		for _, control := range child.ctrls {
			control.Rect.X += x
			control.Rect.Y += y
			if clipped, ok := clipControlRect(control, out.w, out.h); ok {
				out.ctrls = append(out.ctrls, clipped)
			}
		}
	}
	if rects, ok := child.DirtyRects(); ok {
		damage := DamageSet{}
		if existing, ok := out.DirtyRects(); ok {
			damage.AddAll(existing)
		}
		for _, rect := range rects {
			damage.Add(rect.Translate(x, y))
		}
		out.dirty = damage.Normalized(Rect{W: out.w, H: out.h})
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
			s.setCell(col, y, newCell(Glyph(r), width, style))
			for extra := 1; extra < width && col+extra < s.w; extra++ {
				s.setCell(col+extra, y, continuationCell(style))
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
		startCol := col
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
				s.setCell(col, y, newCell(Glyph(r), width, style))
				for extra := 1; extra < width && col+extra < s.w; extra++ {
					s.setCell(col+extra, y, continuationCell(style))
				}
			}
			col += width
		}
		if strings.TrimSpace(span.ControlID) == "" || !span.Enabled {
			continue
		}
		left := max(0, startCol)
		right := min(s.w, col)
		if right <= left {
			continue
		}
		s.ctrls = append(s.ctrls, Control{
			ID:      span.ControlID,
			Rect:    Rect{X: left, Y: y, W: right - left, H: 1},
			Enabled: true,
		})
	}
}

func clipControlsToSurface(controls []Control, width, height int) []Control {
	if len(controls) == 0 {
		return nil
	}
	out := make([]Control, 0, len(controls))
	for _, control := range controls {
		if clipped, ok := clipControlRect(control, width, height); ok {
			out = append(out, clipped)
		}
	}
	return out
}

func controlsFitSurface(controls []Control, width, height int) bool {
	if len(controls) == 0 {
		return true
	}
	for _, control := range controls {
		if _, ok := clipControlRect(control, width, height); !ok {
			return false
		}
		if control.Rect.X < 0 || control.Rect.Y < 0 || control.Rect.X+control.Rect.W > width || control.Rect.Y+control.Rect.H > height {
			return false
		}
	}
	return true
}

func clipControlRect(control Control, width, height int) (Control, bool) {
	x1 := max(0, control.Rect.X)
	y1 := max(0, control.Rect.Y)
	x2 := min(width, control.Rect.X+control.Rect.W)
	y2 := min(height, control.Rect.Y+control.Rect.H)
	if x2 <= x1 || y2 <= y1 {
		return Control{}, false
	}
	control.Rect = Rect{X: x1, Y: y1, W: x2 - x1, H: y2 - y1}
	return control, true
}

func FilledLineSurface(width int, text string, fillStyle, textStyle CellStyle) Surface {
	s := BlankSurface(width, 1)
	for x := 0; x < width; x++ {
		s.setCell(x, 0, blankCell(fillStyle))
	}
	s.WriteText(0, 0, PlainTruncate(text, width, ""), textStyle)
	return s
}

func compositeCell(base, overlay Cell) Cell {
	if !overlay.Painted() {
		return base
	}
	out := base
	if overlay.paintsGlyph() {
		out.Glyph = overlay.Glyph
		out.SetWidth(overlay.Width())
	}
	out.SetStyle(base.Style().Merge(overlay.Style()))
	out.SetPainted(true)
	return out
}

func (c Cell) paintsGlyph() bool {
	return c.Glyph != 0 || c.Width() > 0 || (c.Painted() && c.Width() == 0)
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

func renderElementCapturedSurface(ctx *Context, element Element, bounds Rect) Surface {
	if element == nil || bounds.W <= 0 || bounds.H <= 0 {
		return Surface{}
	}
	shadow := &Runtime{}
	copyCtx := Context{}
	if ctx != nil {
		copyCtx = *ctx
	}
	copyCtx.Runtime = shadow
	surface := element.Render(&copyCtx, Rect{W: bounds.W, H: bounds.H})
	if controls := shadow.Controls(); len(controls) > 0 {
		surface.ctrls = append(surface.ctrls[:0], controls...)
	}
	return surface
}

func renderElementInto(ctx *Context, element Element, bounds Rect, dst *Surface) {
	if element == nil || dst == nil || bounds.W <= 0 || bounds.H <= 0 {
		return
	}
	if painter, ok := element.(Painter); ok {
		painter.Paint(ctx, NewCanvas(dst, bounds))
		return
	}
	surface := renderElementCapturedSurface(ctx, element, bounds)
	*dst = dst.placeAt(bounds.X, bounds.Y, surface)
	if ctx != nil && ctx.Runtime != nil {
		surface.RegisterControls(ctx.Runtime, bounds.X, bounds.Y)
	}
}

func PaintElementSurface(ctx *Context, element Element, bounds Rect) Surface {
	if element == nil || bounds.W <= 0 || bounds.H <= 0 {
		return Surface{}
	}
	if painter, ok := element.(Painter); ok {
		return renderOwnedCanvas(ctx, bounds, painter)
	}
	return renderElementCapturedSurface(ctx, element, bounds)
}

func renderOwnedCanvas(ctx *Context, bounds Rect, painter Painter) Surface {
	base := TransparentSurface(bounds.W, bounds.H)
	if painter == nil {
		return base
	}
	localBounds := Rect{W: bounds.W, H: bounds.H}
	shadow := &Runtime{}
	if ctx == nil {
		canvas := NewCanvas(&base, localBounds)
		painter.Paint(&Context{Runtime: shadow}, canvas)
		if controls := shadow.Controls(); len(controls) > 0 {
			base.ctrls = append(base.ctrls[:0], controls...)
		}
		return base
	}
	copyCtx := *ctx
	copyCtx.Runtime = shadow
	canvas := NewCanvas(&base, localBounds)
	painter.Paint(&copyCtx, canvas)
	if controls := shadow.Controls(); len(controls) > 0 {
		base.ctrls = append(base.ctrls[:0], controls...)
		if ctx.Runtime != nil {
			base.RegisterControls(ctx.Runtime, bounds.X, bounds.Y)
		}
	}
	return base
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
	return renderOwnedCanvas(nil, bounds, s)
}

func (s Static) Paint(_ *Context, canvas Canvas) {
	if canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	lines := strings.Split(s.Content, "\n")
	for y, line := range lines {
		canvas.WriteText(0, y, PlainTruncate(line, canvas.Width(), ""), CellStyle{})
	}
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

func (b SurfaceBox) Paint(_ *Context, canvas Canvas) {
	if canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	canvas.BlitSurface(0, 0, b.Surface.normalize(canvas.Width(), canvas.Height()))
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
	return renderOwnedCanvas(ctx, bounds, e)
}

func (e VisibleElement) WalkChildren(_ *Context, visit func(Element)) {
	if e.Child == nil || visit == nil {
		return
	}
	visit(e.Child)
}

func (e VisibleElement) Paint(ctx *Context, canvas Canvas) {
	if !e.Visible() || canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	if e.HAlign == AlignStart && e.VAlign == AlignStart {
		renderElementInto(ctx, e.Child, Rect{X: canvas.origin.X, Y: canvas.origin.Y, W: canvas.Width(), H: canvas.Height()}, canvas.surface)
		return
	}
	size := e.Child.Measure(ctx, NewConstraints(canvas.Width(), canvas.Height()))
	x := 0
	y := 0
	switch e.HAlign {
	case AlignCenter:
		x = max(0, (canvas.Width()-size.W)/2)
	case AlignEnd:
		x = max(0, canvas.Width()-size.W)
	}
	switch e.VAlign {
	case AlignCenter:
		y = max(0, (canvas.Height()-size.H)/2)
	case AlignEnd:
		y = max(0, canvas.Height()-size.H)
	}
	renderElementInto(ctx, e.Child, Rect{
		X: canvas.origin.X + x,
		Y: canvas.origin.Y + y,
		W: min(canvas.Width(), size.W),
		H: min(canvas.Height(), size.H),
	}, canvas.surface)
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
	return renderOwnedCanvas(ctx, bounds, b)
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

func (b FlexBox) Paint(ctx *Context, canvas Canvas) {
	if canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	bounds := Rect{X: canvas.origin.X, Y: canvas.origin.Y, W: canvas.Width(), H: canvas.Height()}
	if b.axis() == AxisVertical {
		b.renderVerticalTo(ctx, bounds, canvas.surface)
		return
	}
	b.renderHorizontalTo(ctx, bounds, canvas.surface)
}

func (b FlexBox) axis() Axis {
	if b.Direction == DirectionVertical {
		return AxisVertical
	}
	return AxisHorizontal
}

func (b FlexBox) renderVerticalTo(ctx *Context, bounds Rect, dst *Surface) {
	if dst == nil {
		return
	}
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
		renderElementInto(ctx, item.child.Element, Rect{
			X: bounds.X + x,
			Y: bounds.Y + y + dy,
			W: childW,
			H: childH,
		}, dst)
		y += slotH
	}
}

func (b FlexBox) renderHorizontalTo(ctx *Context, bounds Rect, dst *Surface) {
	if dst == nil {
		return
	}
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
		renderElementInto(ctx, item.child.Element, Rect{
			X: bounds.X + x + dx,
			Y: bounds.Y + y,
			W: childW,
			H: childH,
		}, dst)
		x += slotW
	}
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
	return renderOwnedCanvas(ctx, bounds, i)
}

func (i Inset) WalkChildren(_ *Context, visit func(Element)) {
	if i.Child == nil || visit == nil {
		return
	}
	visit(i.Child)
}

func (i Inset) Paint(ctx *Context, canvas Canvas) {
	if !elementVisible(i.Child) || canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	renderElementInto(ctx, i.Child, Rect{
		X: canvas.origin.X + i.Padding.Left,
		Y: canvas.origin.Y + i.Padding.Top,
		W: max(0, canvas.Width()-i.Padding.Left-i.Padding.Right),
		H: max(0, canvas.Height()-i.Padding.Top-i.Padding.Bottom),
	}, canvas.surface)
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
	return renderOwnedCanvas(ctx, bounds, c)
}

func (c Constrained) WalkChildren(_ *Context, visit func(Element)) {
	if c.Child == nil || visit == nil {
		return
	}
	visit(c.Child)
}

func (c Constrained) Paint(ctx *Context, canvas Canvas) {
	if !elementVisible(c.Child) || canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	size := c.Measure(ctx, NewConstraints(canvas.Width(), canvas.Height()))
	renderElementInto(ctx, c.Child, Rect{
		X: canvas.origin.X,
		Y: canvas.origin.Y,
		W: size.W,
		H: size.H,
	}, canvas.surface)
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
	return renderOwnedCanvas(ctx, bounds, s)
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

func (s Stack) Paint(ctx *Context, canvas Canvas) {
	if canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	bounds := Rect{X: canvas.origin.X, Y: canvas.origin.Y, W: canvas.Width(), H: canvas.Height()}
	for _, child := range s.Children {
		if !elementVisible(child) {
			continue
		}
		renderElementInto(ctx, child, bounds, canvas.surface)
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
	return renderOwnedCanvas(ctx, bounds, a)
}

func (a Align) Paint(ctx *Context, canvas Canvas) {
	if a.Child == nil || canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	size := a.Child.Measure(ctx, NewConstraints(canvas.Width(), canvas.Height()))
	size = NewConstraints(canvas.Width(), canvas.Height()).Clamp(size)
	x := 0
	y := 0
	switch a.Horizontal {
	case AlignCenter:
		x = max(0, (canvas.Width()-size.W)/2)
	case AlignEnd:
		x = max(0, canvas.Width()-size.W)
	}
	switch a.Vertical {
	case AlignCenter:
		y = max(0, (canvas.Height()-size.H)/2)
	case AlignEnd:
		y = max(0, canvas.Height()-size.H)
	}
	renderElementInto(ctx, a.Child, Rect{
		X: canvas.origin.X + x,
		Y: canvas.origin.Y + y,
		W: size.W,
		H: size.H,
	}, canvas.surface)
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
	return PaintElementSurface(ctx, element, Rect{W: size.W, H: size.H})
}
