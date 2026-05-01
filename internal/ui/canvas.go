package ui

// Canvas writes styled cells into a clipped region of a Surface.
type Canvas struct {
	surface *Surface
	origin  Point
	clip    Rect
}

// NewCanvas creates a canvas clipped to bounds inside surface.
func NewCanvas(surface *Surface, bounds Rect) Canvas {
	if surface == nil {
		return Canvas{}
	}
	clip := bounds.Clip(Rect{W: surface.SurfaceWidth(), H: surface.SurfaceHeight()})
	return Canvas{
		surface: surface,
		origin:  Point{X: bounds.X, Y: bounds.Y},
		clip:    clip,
	}
}

// Width returns the canvas clipping width.
func (c Canvas) Width() int {
	return c.clip.W
}

// Height returns the canvas clipping height.
func (c Canvas) Height() int {
	return c.clip.H
}

// Bounds returns the canvas-local bounds.
func (c Canvas) Bounds() Rect {
	return Rect{W: c.clip.W, H: c.clip.H}
}

// Subrect returns a child canvas clipped to bounds relative to this canvas.
func (c Canvas) Subrect(bounds Rect) Canvas {
	if c.surface == nil {
		return Canvas{}
	}
	abs := Rect{
		X: c.origin.X + bounds.X,
		Y: c.origin.Y + bounds.Y,
		W: bounds.W,
		H: bounds.H,
	}
	clip := abs.Clip(c.clip)
	return Canvas{
		surface: c.surface,
		origin:  Point{X: abs.X, Y: abs.Y},
		clip:    clip,
	}
}

// WriteText writes styled text at a canvas-local position.
func (c Canvas) WriteText(x, y int, text string, style CellStyle) {
	if c.surface == nil {
		return
	}
	absY := c.origin.Y + y
	if absY < c.clip.Y || absY >= c.clip.Y+c.clip.H {
		return
	}
	col := c.origin.X + x
	for _, r := range text {
		grapheme := string(r)
		width := PlainWidth(grapheme)
		if width <= 0 {
			continue
		}
		if col >= c.clip.X+c.clip.W {
			break
		}
		if col >= c.clip.X {
			base := c.surface.cellAt(col, absY)
			c.surface.setCell(col, absY, base.composite(newCell(Glyph(r), width, style)))
			for extra := 1; extra < width && col+extra < c.clip.X+c.clip.W; extra++ {
				base := c.surface.cellAt(col+extra, absY)
				c.surface.setCell(col+extra, absY, base.composite(continuationCell(style)))
			}
		}
		col += width
	}
}

// Fill fills rect with blank cells using style.
func (c Canvas) Fill(rect Rect, style CellStyle) {
	if c.surface == nil {
		return
	}
	clip := Rect{X: c.origin.X + rect.X, Y: c.origin.Y + rect.Y, W: rect.W, H: rect.H}.Clip(c.clip)
	if clip.Empty() {
		return
	}
	for y := clip.Y; y < clip.Y+clip.H; y++ {
		for x := clip.X; x < clip.X+clip.W; x++ {
			c.surface.setCell(x, y, blankCell(style))
		}
	}
}

// SetCell writes a single cell at a canvas-local position.
func (c Canvas) SetCell(x, y int, cell Cell) {
	if c.surface == nil {
		return
	}
	absX := c.origin.X + x
	absY := c.origin.Y + y
	if absX < c.clip.X || absX >= c.clip.X+c.clip.W || absY < c.clip.Y || absY >= c.clip.Y+c.clip.H {
		return
	}
	c.surface.setCell(absX, absY, cell)
}

// BlitSurface composites child onto the canvas at a canvas-local position.
func (c Canvas) BlitSurface(x, y int, child Surface) {
	if c.surface == nil {
		return
	}
	*c.surface = c.surface.placeAt(c.origin.X+x, c.origin.Y+y, child)
}

// Snapshot copies the canvas clipping region into a new Surface.
func (c Canvas) Snapshot() Surface {
	if c.surface == nil || c.clip.Empty() {
		return Surface{}
	}
	out := TransparentSurface(c.clip.W, c.clip.H)
	for y := 0; y < c.clip.H; y++ {
		for x := 0; x < c.clip.W; x++ {
			out.setCell(x, y, c.surface.cellAt(c.clip.X+x, c.clip.Y+y))
		}
	}
	return out
}

// Painter is implemented by values that can paint into a Canvas.
type Painter interface {
	Paint(ctx *Context, canvas Canvas)
}
