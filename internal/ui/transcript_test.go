package ui

import (
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/theme"
)

func TestUserMessageClassicViewDoesNotAddLeadingSpaceBeforeBody(t *testing.T) {
	palette := theme.Resolve("tokyonight").Palette
	got := NewUserMessage(UserMessageProps{
		Palette:     palette,
		Body:        "hello",
		Width:       12,
		HalfBlocks:  false,
		PromptGlyph: "┃",
	}).View()

	lines := strings.Split(got, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected classic message body row, got %q", got)
	}
	bodyLine := lines[1]
	if strings.Contains(bodyLine, "┃  hello") {
		t.Fatalf("expected no extra leading space before user text, got %q", bodyLine)
	}
	if !strings.Contains(bodyLine, "┃ hello") {
		t.Fatalf("expected text flush after prompt glyph separator, got %q", bodyLine)
	}
}
