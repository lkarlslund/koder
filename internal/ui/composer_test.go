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
	surface := Composer{}.renderLineSurface("> ", promptStyle, "ab", "c", "def", 16, true, ParseCellColor("#112233"), ParseCellColor("#445566"))
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
