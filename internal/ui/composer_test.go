package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/lkarlslund/koder/internal/theme"
)

func TestRenderComposerPlaceholderLineShowsCursorAndFirstPlaceholderCharacter(t *testing.T) {
	palette := theme.Default().Palette
	promptStyle := lipgloss.NewStyle()
	contentStyle := lipgloss.NewStyle()
	muted := lipgloss.NewStyle()

	line := RenderComposerPlaceholderLine(promptStyle, contentStyle, "> ", 24, "Ask koder", "A", muted, palette)
	if !strings.Contains(line, "Ask koder") {
		t.Fatalf("expected cursor and full placeholder text, got %q", line)
	}
}

func TestRenderComposerPlaceholderLineDoesNotAddExtraCursorCell(t *testing.T) {
	palette := theme.Default().Palette
	promptStyle := lipgloss.NewStyle()
	contentStyle := lipgloss.NewStyle()
	muted := lipgloss.NewStyle()

	line := RenderComposerPlaceholderLine(promptStyle, contentStyle, "> ", 12, "Hello", "H", muted, palette)
	if strings.Contains(line, "HHello") {
		t.Fatalf("expected first placeholder character to carry the cursor rather than duplicating, got %q", line)
	}
	if !strings.Contains(line, "Hello") {
		t.Fatalf("expected full placeholder text, got %q", line)
	}
}
