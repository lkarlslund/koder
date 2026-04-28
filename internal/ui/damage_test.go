package ui

import (
	"reflect"
	"testing"
)

func TestDamageSetNormalizedClipsAndMerges(t *testing.T) {
	var damage DamageSet
	damage.Add(Rect{X: -2, Y: 1, W: 4, H: 1})
	damage.Add(Rect{X: 2, Y: 1, W: 3, H: 1})
	damage.Add(Rect{X: 2, Y: 2, W: 3, H: 1})

	got := damage.Normalized(Rect{W: 4, H: 4})
	want := []Rect{
		{X: 0, Y: 1, W: 4, H: 1},
		{X: 2, Y: 2, W: 2, H: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalized damage mismatch:\n got %#v\nwant %#v", got, want)
	}
}

func TestDamageRowsUsesLeftmostDirtyCellPerRow(t *testing.T) {
	rows := DamageRows([]Rect{
		{X: 4, Y: 2, W: 2, H: 1},
		{X: 1, Y: 2, W: 1, H: 1},
		{X: 3, Y: 3, W: 2, H: 2},
	})
	want := []RowDamage{
		{Y: 2, StartX: 1},
		{Y: 3, StartX: 3},
		{Y: 4, StartX: 3},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("row damage mismatch:\n got %#v\nwant %#v", rows, want)
	}
}

func TestSurfaceWithDirtyRectsClipsToSurface(t *testing.T) {
	surface := BlankSurface(5, 3).WithDirtyRects(
		Rect{X: -2, Y: 1, W: 4, H: 1},
		Rect{X: 4, Y: 2, W: 3, H: 1},
	)
	got, ok := surface.DirtyRects()
	if !ok {
		t.Fatal("expected dirty rects")
	}
	want := []Rect{
		{X: 0, Y: 1, W: 2, H: 1},
		{X: 4, Y: 2, W: 1, H: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dirty rects mismatch:\n got %#v\nwant %#v", got, want)
	}
}

func TestDiffSurfaceDamageTracksTightChangedSpan(t *testing.T) {
	previous := BlankSurface(6, 2)
	previous.WriteText(0, 0, "abcdef", CellStyle{})
	previous.WriteText(0, 1, "ghijkl", CellStyle{})

	current := BlankSurface(previous.SurfaceWidth(), previous.SurfaceHeight()).PlaceAt(0, 0, previous)
	current.WriteText(2, 0, "Z", CellStyle{})
	current.WriteText(3, 1, "XY", CellStyle{})

	got := DiffSurfaceDamage(previous, current)
	want := []Rect{
		{X: 2, Y: 0, W: 1, H: 1},
		{X: 3, Y: 1, W: 2, H: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("surface damage mismatch:\n got %#v\nwant %#v", got, want)
	}
}
