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
