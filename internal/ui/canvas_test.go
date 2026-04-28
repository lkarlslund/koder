package ui

import "testing"

func TestCanvasWriteTextPreservesExistingBackground(t *testing.T) {
	surface := BlankSurface(6, 1)
	bg := CellStyle{BG: NewCellColorRGB(10, 20, 30)}
	for x := 0; x < surface.SurfaceWidth(); x++ {
		surface.setCell(x, 0, blankCell(bg))
	}

	canvas := NewCanvas(&surface, Rect{W: 6, H: 1})
	canvas.WriteText(1, 0, "abc", CellStyle{FG: NewCellColorRGB(200, 210, 220)})

	r, g, b, ok := surface.SurfaceCellBG(1, 0)
	if !ok {
		t.Fatal("expected background on painted text cell")
	}
	if r != 10 || g != 20 || b != 30 {
		t.Fatalf("expected inherited background 10/20/30, got %d/%d/%d", r, g, b)
	}
}

func TestCanvasSupportsNegativeOriginClipping(t *testing.T) {
	surface := BlankSurface(5, 2)
	canvas := NewCanvas(&surface, Rect{X: 0, Y: -1, W: 5, H: 3})
	canvas.WriteText(0, 0, "line1", CellStyle{})
	canvas.WriteText(0, 1, "line2", CellStyle{})
	canvas.WriteText(0, 2, "line3", CellStyle{})

	if got := SurfaceText(surface); got != "line2\nline3" {
		t.Fatalf("expected clipped negative-origin paint to keep lower lines, got %q", got)
	}
}
