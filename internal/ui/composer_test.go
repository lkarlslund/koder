package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/lkarlslund/koder/internal/theme"
)

func TestRenderComposerPlaceholderLineShowsCursorAndFirstPlaceholderCharacter(t *testing.T) {
	palette := theme.Default().Palette
	promptStyle := lipgloss.NewStyle()
	contentStyle := lipgloss.NewStyle()

	line := Composer{Palette: palette, CursorVisible: true}.renderPlaceholderLine(promptStyle, contentStyle, "> ", 24, "Ask koder", "A")
	if !strings.Contains(line, "Ask koder") {
		t.Fatalf("expected cursor and full placeholder text, got %q", line)
	}
}

func TestRenderComposerPlaceholderLineDoesNotAddExtraCursorCell(t *testing.T) {
	palette := theme.Default().Palette
	promptStyle := lipgloss.NewStyle()
	contentStyle := lipgloss.NewStyle()

	line := Composer{Palette: palette, CursorVisible: true}.renderPlaceholderLine(promptStyle, contentStyle, "> ", 12, "Hello", "H")
	if strings.Contains(line, "HHello") {
		t.Fatalf("expected first placeholder character to carry the cursor rather than duplicating, got %q", line)
	}
	if !strings.Contains(line, "Hello") {
		t.Fatalf("expected full placeholder text, got %q", line)
	}
}

func TestRenderComposerLineKeepsTypedTextAfterCursorAtNormalColor(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)

	promptStyle := lipgloss.NewStyle()
	line := Composer{}.renderLine("> ", promptStyle, "ab", "c", "def", 16, true, lipgloss.Color("#112233"), lipgloss.Color("#445566"))
	if !strings.Contains(line, "38;2;17;34;51;48;2;68;85;102mdef") {
		t.Fatalf("expected typed text after cursor to keep the normal text color, got %q", line)
	}
}
