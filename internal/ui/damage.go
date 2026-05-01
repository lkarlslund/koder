package ui

import "slices"

// DirtyRectsProvider reports exact damaged rectangles for incremental painting.
type DirtyRectsProvider interface {
	DirtyRects() ([]Rect, bool)
}

// DamageSet accumulates dirty rectangles before normalizing them for repaint.
type DamageSet struct {
	rects []Rect
}

// Add records rect as damaged when it is non-empty.
func (d *DamageSet) Add(rect Rect) {
	if rect.Empty() {
		return
	}
	d.rects = append(d.rects, rect)
}

// AddAll records every non-empty rectangle in rects as damaged.
func (d *DamageSet) AddAll(rects []Rect) {
	for _, rect := range rects {
		d.Add(rect)
	}
}

// Reset clears all accumulated damage.
func (d *DamageSet) Reset() {
	d.rects = d.rects[:0]
}

// Empty reports whether no damage has been recorded.
func (d DamageSet) Empty() bool {
	return len(d.rects) == 0
}

// Rects returns a copy of the raw damage rectangles.
func (d DamageSet) Rects() []Rect {
	if len(d.rects) == 0 {
		return nil
	}
	out := make([]Rect, len(d.rects))
	copy(out, d.rects)
	return out
}

// Normalized clips, sorts, and merges damage rectangles within bounds.
func (d DamageSet) Normalized(bounds Rect) []Rect {
	if len(d.rects) == 0 {
		return nil
	}
	rects := make([]Rect, 0, len(d.rects))
	for _, rect := range d.rects {
		clipped := rect.Clip(bounds)
		if clipped.Empty() {
			continue
		}
		rects = append(rects, clipped)
	}
	if len(rects) == 0 {
		return nil
	}
	slices.SortFunc(rects, func(a, b Rect) int {
		if a.Y != b.Y {
			return a.Y - b.Y
		}
		if a.X != b.X {
			return a.X - b.X
		}
		if a.H != b.H {
			return a.H - b.H
		}
		return a.W - b.W
	})
	out := make([]Rect, 0, len(rects))
	for _, rect := range rects {
		if len(out) == 0 {
			out = append(out, rect)
			continue
		}
		last := out[len(out)-1]
		if merged, ok := last.mergeDamage(rect); ok {
			out[len(out)-1] = merged
			continue
		}
		out = append(out, rect)
	}
	return out
}

func (r Rect) mergeDamage(other Rect) (Rect, bool) {
	if r.Empty() {
		return other, !other.Empty()
	}
	if other.Empty() {
		return r, true
	}
	if r.Y == other.Y && r.H == other.H && r.X+r.W >= other.X {
		right := max(r.X+r.W, other.X+other.W)
		return Rect{X: min(r.X, other.X), Y: r.Y, W: right - min(r.X, other.X), H: r.H}, true
	}
	if r.X == other.X && r.W == other.W && r.Y+r.H >= other.Y {
		bottom := max(r.Y+r.H, other.Y+other.H)
		return Rect{X: r.X, Y: min(r.Y, other.Y), W: r.W, H: bottom - min(r.Y, other.Y)}, true
	}
	return Rect{}, false
}

// RowDamage identifies a damaged terminal row and first changed column.
type RowDamage struct {
	Y      int
	StartX int
}

// DamageRows converts rectangles into sorted row damage entries.
func DamageRows(rects []Rect) []RowDamage {
	if len(rects) == 0 {
		return nil
	}
	starts := make(map[int]int, len(rects))
	for _, rect := range rects {
		for y := rect.Y; y < rect.Y+rect.H; y++ {
			start, ok := starts[y]
			if !ok || rect.X < start {
				starts[y] = rect.X
			}
		}
	}
	rows := make([]RowDamage, 0, len(starts))
	for y, startX := range starts {
		rows = append(rows, RowDamage{Y: y, StartX: startX})
	}
	slices.SortFunc(rows, func(a, b RowDamage) int {
		if a.Y != b.Y {
			return a.Y - b.Y
		}
		return a.StartX - b.StartX
	})
	return rows
}

// DiffSurfaceDamage returns exact damage between two surface snapshots.
func DiffSurfaceDamage(previous, current SurfaceView) []Rect {
	prevRows := surfaceHeight(previous)
	currRows := surfaceHeight(current)
	maxRows := max(prevRows, currRows)
	if maxRows <= 0 {
		return nil
	}
	rects := make([]Rect, 0, maxRows)
	for y := 0; y < maxRows; y++ {
		prevWidth := 0
		currWidth := 0
		if previous != nil && y < previous.SurfaceHeight() {
			prevWidth = previous.SurfaceWidth()
		}
		if current != nil && y < current.SurfaceHeight() {
			currWidth = current.SurfaceWidth()
		}
		maxWidth := max(prevWidth, currWidth)
		if maxWidth <= 0 {
			continue
		}
		start := -1
		end := -1
		for x := 0; x < maxWidth; x++ {
			if surfaceCellsEqual(previous, current, x, y) {
				continue
			}
			if start < 0 {
				start = x
			}
			end = x
		}
		if start < 0 {
			continue
		}
		rect := Rect{X: start, Y: y, W: end - start + 1, H: 1}
		if len(rects) > 0 {
			last := rects[len(rects)-1]
			if last.X == rect.X && last.W == rect.W && last.Y+last.H == rect.Y {
				rects[len(rects)-1].H++
				continue
			}
		}
		rects = append(rects, rect)
	}
	return rects
}
