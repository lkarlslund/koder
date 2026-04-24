package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/lkarlslund/koder/internal/theme"
)

func TestBorderWrapsChildWithoutNestedFrameArtifacts(t *testing.T) {
	got := RenderElement(nil, Border{
		Child:        Static{Content: "Body"},
		Padding:      Insets{Left: 1, Right: 1},
		BorderLeft:   true,
		BorderRight:  true,
		BorderTop:    true,
		BorderBottom: true,
	}, 8, 3)

	lines := strings.Split(ansi.Strip(got), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d in %q", len(lines), got)
	}
	if !strings.Contains(lines[0], "┌") || !strings.Contains(lines[2], "└") {
		t.Fatalf("expected single composed border, got %q", got)
	}
	if strings.Count(got, "┌") != 1 || strings.Count(got, "└") != 1 {
		t.Fatalf("expected one outer border, got %q", got)
	}
}

func TestWindowFrameRendersTitleAndCloseInBorder(t *testing.T) {
	palette := theme.Default().Palette
	got := RenderElement(&Context{Palette: palette}, WindowFrame{
		Title:     "Connect Provider",
		Content:   Static{Content: "Body"},
		Width:     32,
		ShowClose: true,
	}, 32, 4)

	top := strings.Split(ansi.Strip(got), "\n")[0]
	if !strings.Contains(top, "[Connect Provider]") {
		t.Fatalf("expected bracketed title in window border, got %q", top)
	}
	if !strings.Contains(top, "[X]") {
		t.Fatalf("expected close indicator in window border, got %q", top)
	}
}
