package tea

import (
	"strings"
	"testing"
)

func TestRenderFrameAddressesRowsWithoutNewlines(t *testing.T) {
	got := renderFrame("alpha\nbeta\ngamma")
	if strings.Contains(got, "alpha\nbeta") {
		t.Fatalf("frame should not stream raw newlines between rows: %q", got)
	}
	wantParts := []string{
		"\x1b[H\x1b[2J",
		"\x1b[1;1Halpha",
		"\x1b[2;1Hbeta",
		"\x1b[3;1Hgamma",
	}
	for _, want := range wantParts {
		if !strings.Contains(got, want) {
			t.Fatalf("frame missing %q in %q", want, got)
		}
	}
}

func TestRenderFrameLinesAddressesRows(t *testing.T) {
	got := renderFrameLines([]string{"alpha", "beta", "gamma"})
	wantParts := []string{
		"\x1b[H\x1b[2J",
		"\x1b[1;1Halpha",
		"\x1b[2;1Hbeta",
		"\x1b[3;1Hgamma",
	}
	for _, want := range wantParts {
		if !strings.Contains(got, want) {
			t.Fatalf("frame missing %q in %q", want, got)
		}
	}
}

func TestDiffFrameLinesSkipsUnchangedRows(t *testing.T) {
	got := diffFrameLines([]string{"alpha", "beta"}, []string{"alpha", "beta"})
	if got != "" {
		t.Fatalf("expected no output for unchanged frame, got %q", got)
	}
}

func TestDiffFrameLinesUpdatesOnlyChangedRows(t *testing.T) {
	got := diffFrameLines([]string{"alpha", "beta", "gamma"}, []string{"alpha", "BETA", "gamma"})
	if strings.Contains(got, "\x1b[1;1Halpha") || strings.Contains(got, "\x1b[3;1Hgamma") {
		t.Fatalf("expected unchanged rows to be skipped, got %q", got)
	}
	if !strings.Contains(got, "\x1b[2;1HBETA\x1b[K") {
		t.Fatalf("expected changed row to be rewritten with clear, got %q", got)
	}
}

func TestDiffFrameLinesClearsRemovedRows(t *testing.T) {
	got := diffFrameLines([]string{"alpha", "beta"}, []string{"alpha"})
	if !strings.Contains(got, "\x1b[2;1H\x1b[K") {
		t.Fatalf("expected removed row to be cleared, got %q", got)
	}
}

func TestRenderFrameSurfaceEmitsRealSGRSequences(t *testing.T) {
	s := fakeSurface{
		w: 5,
		h: 1,
		cells: []fakeCell{
			{text: "H", fg: "#c8d3f5", bg: "#1e2030", bold: true},
			{text: "e", fg: "#c8d3f5", bg: "#1e2030", bold: true},
			{text: "l", fg: "#c8d3f5", bg: "#1e2030", bold: true},
			{text: "l", fg: "#c8d3f5", bg: "#1e2030", bold: true},
			{text: "o", fg: "#c8d3f5", bg: "#1e2030", bold: true},
		},
	}
	got := renderFrameSurface(s)
	if !strings.Contains(got, "\x1b[1;38;2;200;211;245") {
		t.Fatalf("expected real SGR foreground sequence, got %q", got)
	}
	if strings.Contains(got, "[38;2;200;211;245") && !strings.Contains(got, "\x1b[1;38;2;200;211;245") {
		t.Fatalf("expected no bare CSI tail without ESC, got %q", got)
	}
}

type fakeCell struct {
	text         string
	width        int
	continuation bool
	fg           string
	bg           string
	bold         bool
	italic       bool
}

type fakeSurface struct {
	w     int
	h     int
	cells []fakeCell
}

func (f fakeSurface) SurfaceWidth() int                     { return f.w }
func (f fakeSurface) SurfaceHeight() int                    { return f.h }
func (f fakeSurface) SurfaceCellText(x, y int) string       { return f.cells[y*f.w+x].text }
func (f fakeSurface) SurfaceCellWidth(x, y int) int {
	width := f.cells[y*f.w+x].width
	if width <= 0 {
		return 1
	}
	return width
}
func (f fakeSurface) SurfaceCellContinuation(x, y int) bool { return f.cells[y*f.w+x].continuation }
func (f fakeSurface) SurfaceCellFG(x, y int) string         { return f.cells[y*f.w+x].fg }
func (f fakeSurface) SurfaceCellBG(x, y int) string         { return f.cells[y*f.w+x].bg }
func (f fakeSurface) SurfaceCellBold(x, y int) bool         { return f.cells[y*f.w+x].bold }
func (f fakeSurface) SurfaceCellItalic(x, y int) bool       { return f.cells[y*f.w+x].italic }
