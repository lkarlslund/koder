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

	line := RenderComposerPlaceholderLine(promptStyle, contentStyle, "> ", 24, "Ask koder", "_", muted, palette)
	if !strings.Contains(line, "_Ask koder") {
		t.Fatalf("expected cursor and full placeholder text, got %q", line)
	}
}
