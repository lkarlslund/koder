package ui

import "testing"

func TestSelectionGridSelectedCellUsesDistinctBackground(t *testing.T) {
	palette := benchmarkPalette()
	grid := SelectionGrid{
		Items: []SelectionGridItem{
			{Title: "tokyonight"},
			{Title: "gruvbox"},
		},
		Width:      40,
		Columns:    2,
		Selected:   1,
		Focused:    true,
		CellHeight: 1,
	}

	surface := grid.Render(&Context{Palette: palette}, Rect{W: 40, H: 1})
	unselectedX := 1
	selectedX := 22

	ur, ug, ub, uok := surface.SurfaceCellBG(unselectedX, 0)
	sr, sg, sb, sok := surface.SurfaceCellBG(selectedX, 0)
	if !uok || !sok {
		t.Fatalf("expected both grid cells to paint a background, got unselected=%v selected=%v", uok, sok)
	}
	if ur == sr && ug == sg && ub == sb {
		t.Fatalf("expected selected grid cell background to differ from unselected cell, got (%d,%d,%d)", sr, sg, sb)
	}
}
