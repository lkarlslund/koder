package ui

import (
	"strings"
	"sync/atomic"

	"github.com/lkarlslund/koder/internal/colorx"
	"github.com/lkarlslund/koder/internal/theme"
)

// Point is a terminal-cell coordinate.
type Point struct {
	X int
	Y int
}

// Size is a width and height in terminal cells.
type Size struct {
	W int
	H int
}

// Rect is an axis-aligned rectangle in terminal-cell coordinates.
type Rect struct {
	X int
	Y int
	W int
	H int
}

// Empty reports whether r has no drawable area.
func (r Rect) Empty() bool {
	return r.W <= 0 || r.H <= 0
}

// Translate returns r moved by dx and dy.
func (r Rect) Translate(dx, dy int) Rect {
	if r.Empty() {
		return Rect{}
	}
	return Rect{X: r.X + dx, Y: r.Y + dy, W: r.W, H: r.H}
}

// Contains reports whether p is inside r.
func (r Rect) Contains(p Point) bool {
	return p.X >= r.X && p.X < r.X+r.W && p.Y >= r.Y && p.Y < r.Y+r.H
}

// Inset returns r reduced by insets on each side.
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

// Clip returns the intersection of r and bounds.
func (r Rect) Clip(bounds Rect) Rect {
	if r.W <= 0 || r.H <= 0 || bounds.W <= 0 || bounds.H <= 0 {
		return Rect{}
	}
	left := max(r.X, bounds.X)
	top := max(r.Y, bounds.Y)
	right := min(r.X+r.W, bounds.X+bounds.W)
	bottom := min(r.Y+r.H, bounds.Y+bounds.H)
	if right <= left || bottom <= top {
		return Rect{}
	}
	return Rect{X: left, Y: top, W: right - left, H: bottom - top}
}

// Insets describes per-edge spacing in terminal cells.
type Insets struct {
	Top    int
	Right  int
	Bottom int
	Left   int
}

// UniformInsets returns equal insets on every edge.
func UniformInsets(v int) Insets {
	return Insets{Top: v, Right: v, Bottom: v, Left: v}
}

// SymmetricInsets returns horizontal and vertical inset pairs.
func SymmetricInsets(horizontal, vertical int) Insets {
	return Insets{Top: vertical, Right: horizontal, Bottom: vertical, Left: horizontal}
}

// Constraints bounds the size a node may choose during measurement.
type Constraints struct {
	MinW int
	MaxW int
	MinH int
	MaxH int
}

// NewConstraints returns constraints with only maximum width and height set.
func NewConstraints(maxW, maxH int) Constraints {
	return Constraints{MaxW: maxW, MaxH: maxH}
}

// Clamp confines size to the constraint range.
func (c Constraints) Clamp(size Size) Size {
	size.W = clampInt(size.W, c.MinW, c.maxWidth())
	size.H = clampInt(size.H, c.MinH, c.maxHeight())
	return size
}

// Tighten returns constraints that force exactly size.
func (c Constraints) Tighten(size Size) Constraints {
	return Constraints{MinW: size.W, MaxW: size.W, MinH: size.H, MaxH: size.H}
}

// Deflate subtracts insets from the available constraint area.
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

// Control is a clickable or otherwise addressable rectangle registered at paint time.
type Control struct {
	ID      string
	Rect    Rect
	Enabled bool
}

// Runtime stores per-frame UI metadata such as hit-test controls.
type Runtime struct {
	controls []Control
}

// BeginFrame clears frame-local runtime metadata.
func (r *Runtime) BeginFrame() {
	if r == nil {
		return
	}
	r.controls = r.controls[:0]
}

// Register adds a control to the current frame.
func (r *Runtime) Register(control Control) {
	if r == nil {
		return
	}
	r.controls = append(r.controls, control)
}

// Len returns the number of registered controls.
func (r *Runtime) Len() int {
	if r == nil {
		return 0
	}
	return len(r.controls)
}

// OffsetFrom translates controls starting at index start.
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

// Controls returns a copy of the registered controls.
func (r *Runtime) Controls() []Control {
	if r == nil {
		return nil
	}
	out := make([]Control, len(r.controls))
	copy(out, r.controls)
	return out
}

// Hit returns the topmost enabled control containing p.
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

// Context carries theme and frame runtime data through layout and paint.
type Context struct {
	Palette theme.Palette
	Runtime *Runtime
}

// CellColor is the color representation used by UI cells.
type CellColor = colorx.Color

// NewCellColorRGB creates an opaque RGB cell color.
func NewCellColorRGB(r, g, b uint8) CellColor {
	return colorx.RGB(r, g, b)
}

// NewCellColorRGBA creates an RGBA cell color.
func NewCellColorRGBA(r, g, b, a uint8) CellColor {
	return colorx.RGBA(r, g, b, a)
}

// ParseCellColor parses a CSS-style color string.
func ParseCellColor(value string) CellColor {
	return colorx.ParseCSSColor(value)
}

func cellColor(value CellColor) CellColor {
	return value
}

// CellStyle stores the colors and text attributes for one terminal cell.
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

// Merge overlays style attributes on top of s.
func (s CellStyle) Merge(overlay CellStyle) CellStyle {
	out := s
	if overlay.FG.Valid() {
		out.FG = compositeColor(out.FG, overlay.FG)
	}
	if overlay.BG.Valid() {
		out.BG = compositeColor(out.BG, overlay.BG)
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

// Bold reports whether bold text is enabled.
func (s CellStyle) Bold() bool {
	return s.flags&cellFlagBold != 0
}

// Italic reports whether italic text is enabled.
func (s CellStyle) Italic() bool {
	return s.flags&cellFlagItalic != 0
}

// Underline reports whether underline text is enabled.
func (s CellStyle) Underline() bool {
	return s.flags&cellFlagUnderline != 0
}

// Strikethrough reports whether strikethrough text is enabled.
func (s CellStyle) Strikethrough() bool {
	return s.flags&cellFlagStrikethrough != 0
}

// WithBold returns s with bold set to enabled.
func (s CellStyle) WithBold(enabled bool) CellStyle {
	return s.withFlag(cellFlagBold, enabled)
}

// WithItalic returns s with italic set to enabled.
func (s CellStyle) WithItalic(enabled bool) CellStyle {
	return s.withFlag(cellFlagItalic, enabled)
}

// WithUnderline returns s with underline set to enabled.
func (s CellStyle) WithUnderline(enabled bool) CellStyle {
	return s.withFlag(cellFlagUnderline, enabled)
}

// WithStrikethrough returns s with strikethrough set to enabled.
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

// Glyph is a single terminal glyph stored in a cell.
type Glyph rune

// SpaceGlyph is the standard painted blank glyph.
const SpaceGlyph = Glyph(' ')

// GlyphFromString returns the first rune in value as a Glyph.
func GlyphFromString(value string) Glyph {
	for _, r := range value {
		return Glyph(r)
	}
	return 0
}

// String returns g as a string, or empty for the zero glyph.
func (g Glyph) String() string {
	if g == 0 {
		return ""
	}
	return string(rune(g))
}

// Cell is the styled terminal-cell primitive stored by Surface.
type Cell struct {
	Glyph Glyph
	FG    CellColor
	BG    CellColor
	flags uint8
}

// Width returns the display width recorded for c.
func (c Cell) Width() int {
	return int(c.flags & cellFlagWidthMask)
}

// SetWidth records a display width clamped to the supported cell range.
func (c *Cell) SetWidth(width int) {
	if width < 0 {
		width = 0
	}
	if width > 2 {
		width = 2
	}
	c.flags = (c.flags &^ cellFlagWidthMask) | uint8(width)
}

// Painted reports whether c should visually overwrite lower layers.
func (c Cell) Painted() bool {
	return c.flags&cellFlagPainted != 0
}

// SetPainted toggles whether c visually overwrites lower layers.
func (c *Cell) SetPainted(enabled bool) {
	if enabled {
		c.flags |= cellFlagPainted
	} else {
		c.flags &^= cellFlagPainted
	}
}

// Bold reports whether c is bold.
func (c Cell) Bold() bool {
	return c.flags&cellFlagBold != 0
}

// Italic reports whether c is italic.
func (c Cell) Italic() bool {
	return c.flags&cellFlagItalic != 0
}

// Underline reports whether c is underlined.
func (c Cell) Underline() bool {
	return c.flags&cellFlagUnderline != 0
}

// Strikethrough reports whether c is struck through.
func (c Cell) Strikethrough() bool {
	return c.flags&cellFlagStrikethrough != 0
}

// SetStyle replaces c's visual style while preserving glyph metadata.
func (c *Cell) SetStyle(style CellStyle) {
	c.FG = style.FG
	c.BG = style.BG
	c.flags = (c.flags &^ cellStyleFlagMask) | (style.flags & cellStyleFlagMask)
}

// Style returns c's color and text attributes.
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

// SurfaceAllocationStats reports allocation counts for surface constructors.
type SurfaceAllocationStats struct {
	Blank       uint64
	Transparent uint64
}

var surfaceAllocationCounters struct {
	blank       atomic.Uint64
	transparent atomic.Uint64
}

// ResetSurfaceAllocationStats clears surface allocation counters.
func ResetSurfaceAllocationStats() {
	surfaceAllocationCounters.blank.Store(0)
	surfaceAllocationCounters.transparent.Store(0)
}

// SurfaceAllocationStatsSnapshot returns current surface allocation counters.
func SurfaceAllocationStatsSnapshot() SurfaceAllocationStats {
	return SurfaceAllocationStats{
		Blank:       surfaceAllocationCounters.blank.Load(),
		Transparent: surfaceAllocationCounters.transparent.Load(),
	}
}

// Surface is a drawable terminal-cell buffer plus frame metadata.
//
// Surfaces can represent either legacy line-oriented content or the newer cell
// buffer. Rendering and diffing code consume Surface through SurfaceView.
type Surface struct {
	lines []string
	w     int
	h     int
	cells []Cell
	ctrls []Control
	dirty []Rect
}

// BlankSurface creates an opaque surface initialized with painted spaces.
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

// TransparentSurface creates a surface whose cells do not paint until written.
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

// SurfaceFromString creates a transparent surface from newline-delimited text.
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

// Lines returns a plain-text view of the surface rows.
func (s Surface) Lines() []string {
	if s.isCellBuffer() {
		return s.cellLines()
	}
	out := make([]string, len(s.lines))
	copy(out, s.lines)
	return out
}

// Size returns the surface dimensions.
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

// Clone returns a deep copy of s.
func (s Surface) Clone() Surface {
	out := Surface{
		w: s.w,
		h: s.h,
	}
	if len(s.lines) > 0 {
		out.lines = append([]string(nil), s.lines...)
	}
	if len(s.cells) > 0 {
		out.cells = append([]Cell(nil), s.cells...)
	}
	if len(s.ctrls) > 0 {
		out.ctrls = append([]Control(nil), s.ctrls...)
	}
	if len(s.dirty) > 0 {
		out.dirty = append([]Rect(nil), s.dirty...)
	}
	return out
}

// Normalize returns s converted to an exact cell-buffer size.
func (s Surface) Normalize(width, height int) Surface {
	return s.normalize(width, height)
}

// PlaceAt composites child onto s at x,y and returns the result.
func (s Surface) PlaceAt(x, y int, child Surface) Surface {
	return s.placeAt(x, y, child)
}

// Controls returns a copy of controls embedded in the surface.
func (s Surface) Controls() []Control {
	if len(s.ctrls) == 0 {
		return nil
	}
	out := make([]Control, len(s.ctrls))
	copy(out, s.ctrls)
	return out
}

// WithDirtyRows returns s with a contiguous dirty row range attached.
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

// DirtyRowRange returns the dirty row span attached to s.
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

// WithDirtyRects returns s with normalized dirty rectangles attached.
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

// DirtyRects returns a copy of the dirty rectangles attached to s.
func (s Surface) DirtyRects() ([]Rect, bool) {
	if len(s.dirty) == 0 {
		return nil, false
	}
	out := make([]Rect, len(s.dirty))
	copy(out, s.dirty)
	return out, true
}

// RegisterControls registers surface controls into runtime with an offset.
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

// SurfaceWidth returns the width required by SurfaceView.
func (s Surface) SurfaceWidth() int {
	if s.isCellBuffer() {
		return s.w
	}
	return s.Size().W
}

// SurfaceHeight returns the height required by SurfaceView.
func (s Surface) SurfaceHeight() int {
	if s.isCellBuffer() {
		return s.h
	}
	return s.Size().H
}

// SurfaceCellText returns the visible text at a cell.
func (s Surface) SurfaceCellText(x, y int) string {
	return s.cellAt(x, y).Glyph.String()
}

// SurfaceCellWidth returns the display width of a cell.
func (s Surface) SurfaceCellWidth(x, y int) int {
	return s.cellAt(x, y).Width()
}

// SurfaceCellContinuation reports whether a cell continues a wide glyph.
func (s Surface) SurfaceCellContinuation(x, y int) bool {
	cell := s.cellAt(x, y)
	return cell.Painted() && cell.Width() == 0
}

// SurfaceCellFG returns the cell foreground RGB color when present.
func (s Surface) SurfaceCellFG(x, y int) (uint8, uint8, uint8, bool) {
	cell := s.cellAt(x, y).FG
	if !cell.Valid() {
		return 0, 0, 0, false
	}
	return cell.R(), cell.G(), cell.B(), true
}

// SurfaceCellBG returns the cell background RGB color when present.
func (s Surface) SurfaceCellBG(x, y int) (uint8, uint8, uint8, bool) {
	cell := s.cellAt(x, y).BG
	if !cell.Valid() {
		return 0, 0, 0, false
	}
	return cell.R(), cell.G(), cell.B(), true
}

// SurfaceCellBold reports whether a cell is bold.
func (s Surface) SurfaceCellBold(x, y int) bool {
	return s.cellAt(x, y).Bold()
}

// SurfaceCellItalic reports whether a cell is italic.
func (s Surface) SurfaceCellItalic(x, y int) bool {
	return s.cellAt(x, y).Italic()
}

// SurfaceCellUnderline reports whether a cell is underlined.
func (s Surface) SurfaceCellUnderline(x, y int) bool {
	return s.cellAt(x, y).Underline()
}

// SurfaceCellStrikethrough reports whether a cell is struck through.
func (s Surface) SurfaceCellStrikethrough(x, y int) bool {
	return s.cellAt(x, y).Strikethrough()
}

// BlendStyleAt merges overlay into the style at x,y.
func (s *Surface) BlendStyleAt(x, y int, overlay CellStyle) {
	if s == nil || !s.isCellBuffer() {
		return
	}
	if x < 0 || y < 0 || x >= s.w || y >= s.h {
		return
	}
	base := s.cellAt(x, y)
	base.SetStyle(base.Style().Merge(overlay))
	if !base.Painted() && !base.Style().isZero() {
		base.SetPainted(true)
	}
	s.setCell(x, y, base)
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
			dstRow[cx] = dstRow[cx].composite(srcRow[cx])
			cx++
		}
	}
	if len(child.ctrls) > 0 {
		for _, control := range child.ctrls {
			control.Rect.X += x
			control.Rect.Y += y
			if clipped, ok := control.clipToSurface(out.w, out.h); ok {
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

// WriteText writes styled text into a cell-buffer surface.
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

// WriteStyledSpans writes styled spans and registers span controls when present.
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
		if clipped, ok := control.clipToSurface(width, height); ok {
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
		if _, ok := control.clipToSurface(width, height); !ok {
			return false
		}
		if control.Rect.X < 0 || control.Rect.Y < 0 || control.Rect.X+control.Rect.W > width || control.Rect.Y+control.Rect.H > height {
			return false
		}
	}
	return true
}

func (control Control) clipToSurface(width, height int) (Control, bool) {
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

// FilledLineSurface creates a one-row surface with a filled background and text.
func FilledLineSurface(width int, text string, fillStyle, textStyle CellStyle) Surface {
	s := BlankSurface(width, 1)
	for x := 0; x < width; x++ {
		s.setCell(x, 0, blankCell(fillStyle))
	}
	s.WriteText(0, 0, PlainTruncate(text, width, ""), textStyle)
	return s
}

func (base Cell) composite(overlay Cell) Cell {
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

func compositeColor(base, overlay CellColor) CellColor {
	if !overlay.Valid() {
		return base
	}
	if !base.Valid() || overlay.A() == 0xff {
		return overlay
	}
	if overlay.A() == 0 {
		return base
	}
	alpha := float64(overlay.A()) / 255.0
	invAlpha := 1 - alpha
	return NewCellColorRGBA(
		uint8(float64(overlay.R())*alpha+float64(base.R())*invAlpha+0.5),
		uint8(float64(overlay.G())*alpha+float64(base.G())*invAlpha+0.5),
		uint8(float64(overlay.B())*alpha+float64(base.B())*invAlpha+0.5),
		uint8(float64(overlay.A())+float64(base.A())*invAlpha+0.5),
	)
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

type CacheInvalidator interface {
	InvalidateCache()
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

func InvalidateNodeCaches(ctx *Context, node Node) {
	if node == nil {
		return
	}
	if invalidator, ok := node.(CacheInvalidator); ok {
		invalidator.InvalidateCache()
	}
	if walker, ok := node.(Container); ok {
		for _, child := range walker.Children() {
			InvalidateNodeCaches(ctx, child)
		}
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

func NodeVisible(node Node) bool {
	if node == nil {
		return false
	}
	visible, ok := node.(Visibility)
	if !ok {
		return true
	}
	return visible.Visible()
}

func nodeBox(node Node) BoxProps {
	if node == nil {
		return BoxProps{}
	}
	box, ok := node.(BoxModel)
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
	PassiveNode
	Content string
}

func (s Static) Measure(_ *Context, constraints Constraints) Size {
	return constraints.Clamp(measurePlainTextBlock(s.Content))
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
	PassiveNode
	Surface Surface
}

func (b SurfaceBox) Measure(_ *Context, constraints Constraints) Size {
	return constraints.Clamp(b.Surface.Size())
}

func (b SurfaceBox) Paint(_ *Context, canvas Canvas) {
	if canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	canvas.BlitSurface(0, 0, b.Surface.normalize(canvas.Width(), canvas.Height()))
}

type VisibleElement struct {
	PassiveNode
	BoxProps
	Child Node
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

func (e VisibleElement) Children() []Node {
	if e.Child == nil {
		return nil
	}
	return []Node{e.Child}
}

func (e VisibleElement) Paint(ctx *Context, canvas Canvas) {
	if !e.Visible() || canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	if e.HAlign == AlignStart && e.VAlign == AlignStart {
		paintNodeInto(ctx, e.Child, Rect{X: canvas.origin.X, Y: canvas.origin.Y, W: canvas.Width(), H: canvas.Height()}, canvas.surface)
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
	paintNodeInto(ctx, e.Child, Rect{
		X: canvas.origin.X + x,
		Y: canvas.origin.Y + y,
		W: min(canvas.Width(), size.W),
		H: min(canvas.Height(), size.H),
	}, canvas.surface)
}

type Child struct {
	Node   Node
	Flex   int
	Grow   int
	Shrink int
	Basis  int
}

func Fixed(node Node) Child {
	return Child{Node: node}
}

func Flex(node Node, weight int) Child {
	if weight <= 0 {
		weight = 1
	}
	return Child{Node: node, Flex: weight, Grow: weight, Shrink: 1}
}

func (c Child) effectiveBox() BoxProps {
	props := nodeBox(c.Node)
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
	PassiveNode
	W int
	H int
}

func (s Spacer) Measure(_ *Context, constraints Constraints) Size {
	return constraints.Clamp(Size{W: s.W, H: s.H})
}

func (s Spacer) Paint(_ *Context, canvas Canvas) {
}

type FlexDirection int

const (
	DirectionHorizontal FlexDirection = iota
	DirectionVertical
)

type FlexBox struct {
	PassiveNode
	Direction FlexDirection
	children  []Child
	Spacing   int
}

func NewFlexBox(direction FlexDirection, children []Child, spacing int) FlexBox {
	out := make([]Child, len(children))
	copy(out, children)
	return FlexBox{Direction: direction, children: out, Spacing: spacing}
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

func (b FlexBox) Children() []Node {
	out := make([]Node, 0, len(b.children))
	for _, child := range b.children {
		if child.Node != nil {
			out = append(out, child.Node)
		}
	}
	return out
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
		paintNodeInto(ctx, item.child.Node, Rect{
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
		paintNodeInto(ctx, item.child.Node, Rect{
			X: bounds.X + x + dx,
			Y: bounds.Y + y,
			W: childW,
			H: childH,
		}, dst)
		x += slotW
	}
}

type Inset struct {
	PassiveNode
	Padding Insets
	Child   Node
}

func (i Inset) Measure(ctx *Context, constraints Constraints) Size {
	if !NodeVisible(i.Child) {
		return constraints.Clamp(Size{})
	}
	childSize := i.Child.Measure(ctx, constraints.Deflate(i.Padding))
	return constraints.Clamp(Size{
		W: childSize.W + i.Padding.Left + i.Padding.Right,
		H: childSize.H + i.Padding.Top + i.Padding.Bottom,
	})
}

func (i Inset) Children() []Node {
	if i.Child == nil {
		return nil
	}
	return []Node{i.Child}
}

func (i Inset) Paint(ctx *Context, canvas Canvas) {
	if !NodeVisible(i.Child) || canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	paintNodeInto(ctx, i.Child, Rect{
		X: canvas.origin.X + i.Padding.Left,
		Y: canvas.origin.Y + i.Padding.Top,
		W: max(0, canvas.Width()-i.Padding.Left-i.Padding.Right),
		H: max(0, canvas.Height()-i.Padding.Top-i.Padding.Bottom),
	}, canvas.surface)
}

type Constrained struct {
	PassiveNode
	Constraints Constraints
	Child       Node
}

func (c Constrained) Measure(ctx *Context, constraints Constraints) Size {
	if !NodeVisible(c.Child) {
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

func (c Constrained) Children() []Node {
	if c.Child == nil {
		return nil
	}
	return []Node{c.Child}
}

func (c Constrained) Paint(ctx *Context, canvas Canvas) {
	if !NodeVisible(c.Child) || canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	size := c.Measure(ctx, NewConstraints(canvas.Width(), canvas.Height()))
	paintNodeInto(ctx, c.Child, Rect{
		X: canvas.origin.X,
		Y: canvas.origin.Y,
		W: size.W,
		H: size.H,
	}, canvas.surface)
}

type Stack struct {
	PassiveNode
	children []Node
}

func NewStack(children ...Node) Stack {
	out := make([]Node, len(children))
	copy(out, children)
	return Stack{children: out}
}

func NewStackFrom(children []Node) Stack {
	out := make([]Node, len(children))
	copy(out, children)
	return Stack{children: out}
}

func (s Stack) Measure(ctx *Context, constraints Constraints) Size {
	size := Size{}
	for _, child := range s.children {
		if !NodeVisible(child) {
			continue
		}
		childSize := child.Measure(ctx, constraints)
		size.W = max(size.W, childSize.W)
		size.H = max(size.H, childSize.H)
	}
	return constraints.Clamp(size)
}

func (s Stack) Children() []Node {
	return append([]Node(nil), s.children...)
}

func (s Stack) Paint(ctx *Context, canvas Canvas) {
	if canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	bounds := Rect{X: canvas.origin.X, Y: canvas.origin.Y, W: canvas.Width(), H: canvas.Height()}
	for _, child := range s.children {
		if !NodeVisible(child) {
			continue
		}
		paintNodeInto(ctx, child, bounds, canvas.surface)
	}
}

type Alignment int

const (
	AlignStart Alignment = iota
	AlignCenter
	AlignEnd
)

type Align struct {
	PassiveNode
	Horizontal Alignment
	Vertical   Alignment
	Child      Node
}

func (a Align) Measure(ctx *Context, constraints Constraints) Size {
	if a.Child == nil {
		return constraints.Clamp(Size{})
	}
	return constraints.Clamp(a.Child.Measure(ctx, constraints))
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
	paintNodeInto(ctx, a.Child, Rect{
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
	return computeFlexPlan(ctx, b.children, b.Spacing, b.axis(), constraints, targetMain)
}

func computeFlexPlan(ctx *Context, children []Child, spacing int, axis Axis, constraints Constraints, targetMain int) flexPlan {
	items := make([]flexItem, 0, len(children))
	mainUsed := 0
	cross := 0
	for _, child := range children {
		if !NodeVisible(child.Node) {
			continue
		}
		box := child.effectiveBox()
		size := child.Node.Measure(ctx, constraintsForAxis(axis, constraints, 0))
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

func RenderSurface(ctx *Context, node Node, width, height int) Surface {
	if node == nil {
		return Surface{}
	}
	if ctx == nil {
		ctx = &Context{}
	}
	size := node.Measure(ctx, Constraints{MaxW: width, MaxH: height})
	if width > 0 {
		size.W = width
	}
	if height > 0 {
		size.H = height
	}
	return PaintNodeSurface(ctx, node, Rect{W: size.W, H: size.H})
}
