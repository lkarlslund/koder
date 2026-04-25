package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/lkarlslund/koder/internal/theme"
)

func TestSplitRendersFixedSidebarOnRight(t *testing.T) {
	got := RenderElement(nil, Split{
		Direction:   SplitHorizontal,
		First:       Static{Content: "MAIN"},
		Second:      Static{Content: "SIDE"},
		SecondFixed: 4,
	}, 8, 1)

	if got != "MAINSIDE" {
		t.Fatalf("unexpected split render: %q", got)
	}
}

func TestTableRendersHeaderAndRows(t *testing.T) {
	palette := theme.Default().Palette
	got := RenderElement(&Context{Palette: palette}, Table{
		Width: 20,
		Columns: []TableColumn{
			{Title: "Name", Width: 10},
			{Title: "Kind", Width: 8},
		},
		ShowHeader: true,
		Rows: []TableRow{{
			Cells: []string{"README.md", "file"},
		}},
	}, 20, 2)

	lines := strings.Split(ansi.Strip(got), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected header and row, got %q", got)
	}
	if !strings.Contains(lines[0], "Name") || !strings.Contains(lines[0], "Kind") {
		t.Fatalf("expected table header, got %q", lines[0])
	}
	if !strings.Contains(lines[1], "README.md") || !strings.Contains(lines[1], "file") {
		t.Fatalf("expected table row, got %q", lines[1])
	}
}

func TestSectionRendersTitleAbovePanel(t *testing.T) {
	palette := theme.Default().Palette
	got := RenderElement(&Context{Palette: palette}, Section{
		Title: "Preview",
		Width: 18,
		Child: Static{Content: "Body"},
	}, 18, 3)

	lines := strings.Split(ansi.Strip(got), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected titled section, got %q", got)
	}
	if !strings.Contains(lines[0], "Preview") {
		t.Fatalf("expected section title, got %q", lines[0])
	}
	if !strings.Contains(got, "Body") {
		t.Fatalf("expected section body, got %q", got)
	}
}

func TestListSelectionChangedCallback(t *testing.T) {
	list := List{
		Items: []ListItem{
			{Primary: "A"},
			{Primary: "B"},
		},
	}
	var gotIndex int
	var gotItem ListItem
	list.OnSelectionChanged = func(index int, item ListItem) {
		gotIndex = index
		gotItem = item
	}
	if !list.Move(1) {
		t.Fatal("expected selection to move")
	}
	if gotIndex != 1 || gotItem.Primary != "B" {
		t.Fatalf("unexpected callback payload: index=%d item=%+v", gotIndex, gotItem)
	}
}

func TestScrollFrameRendersVisibleWindowAtOffset(t *testing.T) {
	got := RenderElement(nil, ScrollFrame{
		Child:   Static{Content: "line1\nline2\nline3\nline4"},
		OffsetY: 1,
		Width:   5,
		Height:  2,
	}, 5, 2)

	lines := strings.Split(ansi.Strip(got), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 visible lines, got %d in %q", len(lines), got)
	}
	if strings.TrimSpace(lines[0]) != "line2" || strings.TrimSpace(lines[1]) != "line3" {
		t.Fatalf("expected scrolled window to show line2/line3, got %#v", lines)
	}
}

func TestScrollBoxClampsOffsetToContentBottom(t *testing.T) {
	box := ScrollBox{
		Child:   Static{Content: "line1\nline2\nline3"},
		OffsetY: 99,
		Width:   5,
		Height:  2,
	}

	surface, totalHeight, offset := box.RenderVisible(nil, 5, 2, box.OffsetY)
	lines := strings.Split(ansi.Strip(strings.Join(surface.Lines(), "\n")), "\n")

	if totalHeight != 3 {
		t.Fatalf("expected content height 3, got %d", totalHeight)
	}
	if offset != 1 {
		t.Fatalf("expected offset to clamp to 1, got %d", offset)
	}
	if len(lines) != 2 || strings.TrimSpace(lines[0]) != "line2" || strings.TrimSpace(lines[1]) != "line3" {
		t.Fatalf("expected bottom window to show line2/line3, got %#v", lines)
	}
}
