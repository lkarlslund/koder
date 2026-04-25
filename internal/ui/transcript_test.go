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

func TestUserMessageFillsEntireRowBackground(t *testing.T) {
	palette := theme.Resolve("tokyonight").Palette
	surface := NewUserMessage(UserMessageProps{
		Palette:     palette,
		Body:        "hello",
		Width:       12,
		HalfBlocks:  false,
		PromptGlyph: "┃",
	}).Render(&Context{Palette: palette}, Rect{W: 12, H: 3})

	want := ParseCellColor(string(palette.UserTextBackground))
	for x := 0; x < 12; x++ {
		r, g, b, ok := surface.SurfaceCellBG(x, 1)
		if !ok {
			t.Fatalf("expected background color at x=%d", x)
		}
		if !want.Valid || r != want.R || g != want.G || b != want.B {
			t.Fatalf("expected row background %v at x=%d, got %d %d %d", want, x, r, g, b)
		}
	}
}

func TestRetainedTranscriptMaintainsChildItems(t *testing.T) {
	transcript := NewRetainedTranscript()
	transcript.Add(TranscriptItem{Element: Paragraph{Text: "first"}})
	transcript.Add(TranscriptItem{Element: Paragraph{Text: "second"}, GapBefore: 1})

	got := RenderElement(nil, transcript, 0, 0)
	if !strings.Contains(got, "first") || !strings.Contains(got, "second") {
		t.Fatalf("expected retained transcript to render added items, got %q", got)
	}

	transcript.Replace(1, TranscriptItem{Element: Paragraph{Text: "updated"}, GapBefore: 1})
	got = RenderElement(nil, transcript, 0, 0)
	if strings.Contains(got, "second") || !strings.Contains(got, "updated") {
		t.Fatalf("expected retained transcript replace to update content, got %q", got)
	}

	transcript.Clear()
	if size := transcript.Measure(nil, Constraints{}); size.H != 0 || size.W != 0 {
		t.Fatalf("expected cleared transcript to measure empty, got %#v", size)
	}
}

func TestRetainedTranscriptRenderBottomUsesExactCachedHeights(t *testing.T) {
	transcript := NewRetainedTranscript()
	transcript.Add(TranscriptItem{
		Element: NewCachedElement(Paragraph{Text: "one\ntwo\nthree\nfour"}, 1),
	})
	transcript.Add(TranscriptItem{
		Element: NewCachedElement(Paragraph{Text: "tail"}, 1),
	})

	surface, totalHeight, offset := transcript.RenderBottom(nil, 10, 2)
	got := strings.Join(surface.Lines(), "\n")

	if totalHeight != 5 {
		t.Fatalf("expected exact total height 5, got %d", totalHeight)
	}
	if offset != 3 {
		t.Fatalf("expected exact bottom offset 3, got %d", offset)
	}
	if got != "four      \ntail      " {
		t.Fatalf("expected exact transcript tail, got %q", got)
	}
}
