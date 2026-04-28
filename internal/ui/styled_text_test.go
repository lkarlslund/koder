package ui

import (
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/theme"
)

func TestWrapStyledTextPreservesInlineStyles(t *testing.T) {
	spans := []StyledSpan{
		{Text: "alpha "},
		{Text: "beta", Style: CellStyle{}.WithBold(true)},
		{Text: " gamma", Style: CellStyle{}.WithItalic(true)},
	}

	lines := WrapStyledText(spans, 10)
	if len(lines) != 2 {
		t.Fatalf("expected 2 wrapped lines, got %d", len(lines))
	}
	if got := PlainStyledText(lines[0]); got != "alpha beta" {
		t.Fatalf("unexpected first line %q", got)
	}
	if got := PlainStyledText(lines[1]); got != "gamma" {
		t.Fatalf("unexpected second line %q", got)
	}
	foundBold := false
	for _, span := range lines[0] {
		if strings.Contains(span.Text, "beta") && span.Style.Bold() {
			foundBold = true
		}
	}
	if !foundBold {
		t.Fatalf("expected bold span to survive wrapping, got %#v", lines[0])
	}
	foundItalic := false
	for _, span := range lines[1] {
		if strings.Contains(span.Text, "gamma") && span.Style.Italic() {
			foundItalic = true
		}
	}
	if !foundItalic {
		t.Fatalf("expected italic span to survive wrapping, got %#v", lines[1])
	}
}

func TestRenderStyledTextANSIIncludesStrikethrough(t *testing.T) {
	got := RenderStyledTextANSI([]StyledSpan{{
		Text:  "gone",
		Style: CellStyle{}.WithStrikethrough(true),
	}})
	if !strings.Contains(got, "\x1b[9m") {
		t.Fatalf("expected strikethrough SGR, got %q", got)
	}
}

func TestLayoutStyledTextRegistersWrappedInteractiveFragments(t *testing.T) {
	surface := LayoutStyledText([]StyledSpan{
		{Text: "Edited file game/sim_test.go", Style: CellStyle{}.WithBold(true)},
		{Text: "  ", Style: CellStyle{}},
		{Text: "Expand (14 lines)", Style: CellStyle{}.WithItalic(true), ControlID: "toolrun:edit:output", Enabled: true},
	}, 18, CellStyle{})

	controls := surface.Controls()
	if len(controls) < 2 {
		t.Fatalf("expected wrapped interactive fragments, got %#v", controls)
	}
	for _, control := range controls {
		if control.ID != "toolrun:edit:output" {
			t.Fatalf("unexpected control id %#v", controls)
		}
		if control.Rect.W <= 0 || control.Rect.H != 1 {
			t.Fatalf("unexpected control rect %#v", control.Rect)
		}
	}
}

func TestAssistantMessageStyledBodyUsesBaseAndSpanStyles(t *testing.T) {
	palette := theme.Default().Palette
	msg := AssistantMessage{
		Width:     40,
		Palette:   palette,
		BaseStyle: CellStyle{FG: cellColor(palette.MarkdownText)},
		StyledBody: []StyledSpan{
			{Text: "plain "},
			{
				Text: "code",
				Style: CellStyle{
					FG: cellColor(palette.MarkdownInlineCodeText),
					BG: cellColor(palette.MarkdownInlineCodeBackground),
				},
			},
		},
	}

	surface := PaintElementSurface(&Context{Palette: palette}, msg, Rect{W: 40, H: 1})
	if line := strings.TrimSpace(strings.Join(surface.Lines(), "\n")); !strings.Contains(line, "plain code") {
		t.Fatalf("unexpected rendered assistant message %q", line)
	}
	if _, _, _, ok := surface.SurfaceCellFG(0, 0); !ok {
		t.Fatal("expected base foreground color on unstyled text")
	}
	codeX := strings.Index(surface.Lines()[0], "code")
	if codeX < 0 {
		t.Fatal("expected code span in rendered line")
	}
	if _, _, _, ok := surface.SurfaceCellBG(codeX, 0); !ok {
		t.Fatal("expected background color on styled code span")
	}
}
