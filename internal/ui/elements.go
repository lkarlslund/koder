package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

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

type Context struct {
	Palette theme.Palette
	Runtime *Runtime
}

type Surface struct {
	lines []string
}

func BlankSurface(width, height int) Surface {
	if width < 0 {
		width = 0
	}
	if height < 0 {
		height = 0
	}
	lines := make([]string, height)
	for i := range lines {
		lines[i] = strings.Repeat(" ", width)
	}
	return Surface{lines: lines}
}

func SurfaceFromString(input string) Surface {
	if input == "" {
		return Surface{}
	}
	return Surface{lines: strings.Split(input, "\n")}
}

func (s Surface) Lines() []string {
	out := make([]string, len(s.lines))
	copy(out, s.lines)
	return out
}

func (s Surface) Size() Size {
	width := 0
	for _, line := range s.lines {
		width = max(width, ansi.StringWidth(line))
	}
	return Size{W: width, H: len(s.lines)}
}

func (s Surface) String() string {
	return strings.Join(s.lines, "\n")
}

func (s Surface) normalize(width, height int) Surface {
	if width < 0 {
		width = 0
	}
	if height < 0 {
		height = 0
	}
	lines := make([]string, 0, height)
	for i := 0; i < height; i++ {
		line := ""
		if i < len(s.lines) {
			line = ansi.Truncate(s.lines[i], width, "")
		}
		if delta := width - ansi.StringWidth(line); delta > 0 {
			line += strings.Repeat(" ", delta)
		}
		lines = append(lines, line)
	}
	return Surface{lines: lines}
}

func (s Surface) placeAt(x, y int, child Surface) Surface {
	if len(s.lines) == 0 || len(child.lines) == 0 {
		return s
	}
	base := s.Lines()
	for row, childLine := range child.lines {
		targetY := y + row
		if targetY < 0 || targetY >= len(base) {
			continue
		}
		baseWidth := ansi.StringWidth(base[targetY])
		if x >= baseWidth {
			continue
		}
		childLine = ansi.Truncate(childLine, max(0, baseWidth-x), "")
		if childLine == "" {
			continue
		}
		base[targetY] = overlayLine(base[targetY], childLine, x)
	}
	return Surface{lines: base}
}

func overlayLine(base, overlay string, offset int) string {
	if offset < 0 {
		offset = 0
	}
	baseWidth := ansi.StringWidth(base)
	if baseWidth == 0 {
		if offset > 0 {
			base = strings.Repeat(" ", offset)
		}
		return base + overlay
	}
	start := ansi.Truncate(base, offset, "")
	remaining := max(0, baseWidth-offset)
	overlay = ansi.Truncate(overlay, remaining, "")
	endStart := offset + ansi.StringWidth(overlay)
	end := ""
	if endStart < baseWidth {
		end = substringByWidth(base, endStart, baseWidth)
	}
	line := start + overlay + end
	if delta := baseWidth - ansi.StringWidth(line); delta > 0 {
		line += strings.Repeat(" ", delta)
	}
	return line
}

func substringByWidth(input string, start, end int) string {
	if end <= start {
		return ""
	}
	truncated := ansi.Truncate(input, end, "")
	if start <= 0 {
		return truncated
	}
	return strings.TrimPrefix(truncated, ansi.Truncate(input, start, ""))
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
		childSurface := child.Element.Render(ctx, Rect{W: bounds.W, H: max(0, height)})
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
		childSurface := child.Element.Render(ctx, Rect{W: max(0, width), H: bounds.H})
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
	childSurface := i.Child.Render(ctx, Rect{W: childBounds.W, H: childBounds.H})
	return base.placeAt(i.Padding.Left, i.Padding.Top, childSurface)
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
	childSurface := a.Child.Render(ctx, Rect{W: size.W, H: size.H})
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
	return element.Render(ctx, Rect{W: size.W, H: size.H}).String()
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
