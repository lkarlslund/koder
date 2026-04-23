package ui

import (
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/theme"
)

func TestUserMessageClassicViewDoesNotAddLeadingSpaceBeforeBody(t *testing.T) {
	palette := theme.Resolve("tokyonight").Palette
	got := RenderElement(&Context{Palette: palette}, NewUserMessage(UserMessageProps{
		Palette:     palette,
		Body:        "hello",
		Width:       12,
		HalfBlocks:  false,
		PromptGlyph: "┃",
	}), 12, 0)

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

func TestActivityIndicatorViewDoesNotAddLeadingSpace(t *testing.T) {
	palette := theme.Resolve("tokyonight").Palette
	got := RenderElement(&Context{Palette: palette}, ActivityIndicator{
		Indicator: "x Working ...",
		Palette:   palette,
	}, 0, 0)

	if strings.HasPrefix(got, " ") {
		t.Fatalf("expected activity indicator to start without a leading space, got %q", got)
	}
}
