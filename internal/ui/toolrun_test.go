package ui

import (
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/theme"
)

func TestToolRunCardViewPlacesSubtitleOnSeparateLine(t *testing.T) {
	palette := theme.Resolve("tokyonight").Palette
	run := ToolRun{
		Tool:     domain.ToolKindRead,
		Title:    "Read file",
		Subtitle: "README.md",
		Output:   "line 1\nline 2",
	}

	got := SurfaceText(run.CardSurface(palette, 80, false))
	lines := strings.Split(got, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected multi-line card, got %q", got)
	}
	if strings.Contains(lines[0], "Read file  README.md") {
		t.Fatalf("expected subtitle on separate line, got %q", got)
	}
	if !strings.Contains(lines[0], "Read file") {
		t.Fatalf("expected title on first line, got %q", got)
	}
	if !strings.Contains(lines[1], "README.md") {
		t.Fatalf("expected subtitle on second line, got %q", got)
	}
}

func TestToolRunCardViewPlacesGrepSubtitleOnSeparateLine(t *testing.T) {
	palette := theme.Resolve("tokyonight").Palette
	run := ToolRun{
		Tool:     domain.ToolKindGrep,
		Title:    "Search text",
		Subtitle: "needle in internal (*.go)",
		Output:   "internal/a.go:1:needle",
	}

	got := SurfaceText(run.CardSurface(palette, 80, false))
	lines := strings.Split(got, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected multi-line card, got %q", got)
	}
	if strings.Contains(lines[0], "Search text  needle in internal (*.go)") {
		t.Fatalf("expected subtitle on separate line, got %q", got)
	}
	if !strings.Contains(lines[1], "needle in internal (*.go)") {
		t.Fatalf("expected grep subtitle on second line, got %q", got)
	}
}
