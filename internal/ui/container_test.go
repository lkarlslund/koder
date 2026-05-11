package ui

import "testing"

func TestRetainedColumnPaintsChildrenAtTheirLayoutRows(t *testing.T) {
	column := NewRetainedColumn(0)
	column.Add(&RetainedLabel{Text: "first"})
	column.Add(&RetainedLabel{Text: "second"})
	column.Add(&RetainedLabel{Text: "third"})

	got := RenderNode(nil, column, 12, 0)
	want := "first       \nsecond      \nthird       "
	if got != want {
		t.Fatalf("expected column rows, got %q", got)
	}
}
