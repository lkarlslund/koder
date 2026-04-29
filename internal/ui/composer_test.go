package ui

import (
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/theme"
)

func TestRenderComposerPlaceholderLineShowsCursorAndFirstPlaceholderCharacter(t *testing.T) {
	palette := theme.Default().Palette
	promptStyle := NewStyle()
	contentStyle := NewStyle()

	line := Composer{Palette: palette, CursorVisible: true}.renderPlaceholderLine(promptStyle, contentStyle, "> ", 24, "Ask koder", "A")
	if !strings.Contains(line, "Ask koder") {
		t.Fatalf("expected cursor and full placeholder text, got %q", line)
	}
}

func TestRenderComposerPlaceholderLineDoesNotAddExtraCursorCell(t *testing.T) {
	palette := theme.Default().Palette
	promptStyle := NewStyle()
	contentStyle := NewStyle()

	line := Composer{Palette: palette, CursorVisible: true}.renderPlaceholderLine(promptStyle, contentStyle, "> ", 12, "Hello", "H")
	if strings.Contains(line, "HHello") {
		t.Fatalf("expected first placeholder character to carry the cursor rather than duplicating, got %q", line)
	}
	if !strings.Contains(line, "Hello") {
		t.Fatalf("expected full placeholder text, got %q", line)
	}
}

func TestRenderComposerLineKeepsTypedTextAfterCursorAtNormalColor(t *testing.T) {
	promptStyle := NewStyle()
	surface := Composer{}.renderLineSurface("> ", promptStyle, "ab", "c", "def", nil, 16, true, ParseCellColor("#112233"), ParseCellColor("#445566"))
	rendered := SurfaceText(surface)
	start := strings.Index(rendered, "def")
	if start == -1 {
		t.Fatalf("expected rendered line to contain trailing text, got %q", rendered)
	}
	x := start
	r, g, b, ok := surface.SurfaceCellFG(x, 0)
	if !ok || r != 0x11 || g != 0x22 || b != 0x33 {
		t.Fatalf("expected typed text after cursor to keep the normal foreground color, got (%d,%d,%d,%v)", r, g, b, ok)
	}
	r, g, b, ok = surface.SurfaceCellBG(x, 0)
	if !ok || r != 0x44 || g != 0x55 || b != 0x66 {
		t.Fatalf("expected typed text after cursor to keep the normal background color, got (%d,%d,%d,%v)", r, g, b, ok)
	}
}

func TestRenderComposerLineHighlightsTokenRanges(t *testing.T) {
	palette := theme.Default().Palette
	promptStyle := NewStyle()
	surface := Composer{Palette: palette}.renderLineSurface(
		"> ",
		promptStyle,
		"",
		"@",
		"README.md rest",
		[]TokenRange{{Start: 0, End: len("@README.md")}},
		24,
		false,
		ParseCellColor("#112233"),
		ParseCellColor("#445566"),
	)
	x := strings.Index(SurfaceText(surface), "@README.md")
	if x == -1 {
		t.Fatalf("expected token text in rendered surface, got %q", SurfaceText(surface))
	}
	r, g, b, ok := surface.SurfaceCellBG(x, 0)
	want := palette.MarkdownMarkBackground
	if !ok || r != want.R() || g != want.G() || b != want.B() {
		t.Fatalf("expected token highlight background (%d,%d,%d), got (%d,%d,%d,%v)", want.R(), want.G(), want.B(), r, g, b, ok)
	}
}

func TestComposerAutoWrapsIntoMultipleLines(t *testing.T) {
	palette := theme.Default().Palette
	surface := NewComposer(ComposerProps{
		Palette:       palette,
		Width:         12,
		PromptGlyph:   ">",
		Value:         "draft text wraps",
		CursorIndex:   len("draft text wraps"),
		CursorVisible: true,
	}).render()

	if surface.SurfaceHeight() <= 3 {
		t.Fatalf("expected composer to grow beyond single-line height, got %d", surface.SurfaceHeight())
	}
	rendered := SurfaceText(surface)
	if !strings.Contains(rendered, "draft text") || !strings.Contains(rendered, "wraps") {
		t.Fatalf("expected wrapped composer text, got %q", rendered)
	}
}

func TestComposerCursorRectTracksWrappedLine(t *testing.T) {
	palette := theme.Default().Palette
	composer := NewComposer(ComposerProps{
		Palette:       palette,
		Width:         12,
		PromptGlyph:   ">",
		Value:         "draft text wraps",
		CursorIndex:   len("draft text wraps"),
		CursorVisible: true,
	})

	rect, ok := composer.CursorRect()
	if !ok {
		t.Fatal("expected cursor rect")
	}
	if rect.Y <= 1 {
		t.Fatalf("expected wrapped cursor to land on a later content row, got %#v", rect)
	}
}
