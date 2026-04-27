package ui

import (
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/theme"
)

type controlProbeElement struct {
	id     string
	width  int
	height int
}

func (e controlProbeElement) Measure(_ *Context, constraints Constraints) Size {
	return constraints.Clamp(Size{W: e.width, H: e.height})
}

func (e controlProbeElement) Render(ctx *Context, bounds Rect) Surface {
	if ctx != nil && ctx.Runtime != nil {
		ctx.Runtime.Register(Control{
			ID:      e.id,
			Rect:    Rect{X: bounds.X, Y: bounds.Y, W: max(1, bounds.W), H: max(1, bounds.H)},
			Enabled: true,
		})
	}
	return BlankSurface(bounds.W, bounds.H)
}

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

func TestUserMessageHalfBlocksKeepAccentTopAndBottomRows(t *testing.T) {
	palette := theme.Resolve("tokyonight").Palette
	surface := NewUserMessage(UserMessageProps{
		Palette:     palette,
		Body:        "hello",
		Width:       12,
		HalfBlocks:  true,
		PromptGlyph: "┃",
	}).Render(&Context{Palette: palette}, Rect{W: 12, H: 3})

	top := surface.Lines()[0]
	bottom := surface.Lines()[2]
	if !strings.Contains(top, "▄") {
		t.Fatalf("expected half-block accent on top row, got %q", top)
	}
	if !strings.Contains(bottom, "▀") {
		t.Fatalf("expected half-block accent on bottom row, got %q", bottom)
	}
}

func TestUserMessageHalfBlocksLeaveSeparatorRowsTransparent(t *testing.T) {
	palette := theme.Resolve("tokyonight").Palette
	surface := NewUserMessage(UserMessageProps{
		Palette:     palette,
		Body:        "hello",
		Width:       12,
		HalfBlocks:  true,
		PromptGlyph: "┃",
	}).Render(&Context{Palette: palette}, Rect{W: 12, H: 3})

	for _, pos := range [][2]int{{0, 0}, {5, 0}, {0, 2}, {5, 2}} {
		if _, _, _, ok := surface.SurfaceCellBG(pos[0], pos[1]); ok {
			t.Fatalf("expected transparent separator row background at (%d,%d)", pos[0], pos[1])
		}
	}
	if _, _, _, ok := surface.SurfaceCellBG(0, 1); !ok {
		t.Fatal("expected body row to keep bubble background")
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

func TestRetainedTranscriptOffsetsVisibleControls(t *testing.T) {
	transcript := NewRetainedTranscript()
	transcript.Add(TranscriptItem{Key: "one", Element: controlProbeElement{id: "first", width: 8, height: 2}})
	transcript.Add(TranscriptItem{Key: "two", GapBefore: 1, Element: controlProbeElement{id: "second", width: 8, height: 3}})

	runtime := &Runtime{}
	ctx := &Context{Palette: theme.Resolve("tokyonight").Palette, Runtime: runtime}
	_, _, _ = transcript.RenderVisible(ctx, 8, 4, 2)

	controls := runtime.Controls()
	if len(controls) != 1 {
		t.Fatalf("expected only the visible control to be registered, got %#v", controls)
	}
	if controls[0].ID != "second" {
		t.Fatalf("expected second control to remain visible, got %#v", controls[0])
	}
	if controls[0].Rect.Y != 1 {
		t.Fatalf("expected visible control to be offset into viewport row 1, got %#v", controls[0].Rect)
	}
}

func TestRetainedTranscriptDoesNotBottomAlignShortContent(t *testing.T) {
	transcript := NewRetainedTranscript()
	transcript.Add(TranscriptItem{
		Element: NewCachedElement(Paragraph{Text: "top\nnext"}, 2),
	})

	surface, totalHeight, offset := transcript.RenderBottom(nil, 8, 5)
	lines := surface.Lines()

	if totalHeight != 2 {
		t.Fatalf("expected total height 2, got %d", totalHeight)
	}
	if offset != 0 {
		t.Fatalf("expected offset 0 for short content, got %d", offset)
	}
	if len(lines) < 2 || strings.TrimSpace(lines[0]) != "top" || strings.TrimSpace(lines[1]) != "next" {
		t.Fatalf("expected short transcript to start at top, got %#v", lines)
	}
}

func TestRetainedTranscriptContentHeightTracksItemMutations(t *testing.T) {
	transcript := NewRetainedTranscript()
	transcript.Add(TranscriptItem{Element: NewCachedElement(Paragraph{Text: "one\ntwo"}, 2)})
	transcript.Add(TranscriptItem{Element: NewCachedElement(Paragraph{Text: "tail"}, 1), GapBefore: 1})

	if got := transcript.ContentHeight(12); got != 4 {
		t.Fatalf("expected initial total height 4, got %d", got)
	}

	transcript.Replace(1, TranscriptItem{Element: NewCachedElement(Paragraph{Text: "tail\nmore\nlast"}, 3), GapBefore: 1})
	if got := transcript.ContentHeight(12); got != 6 {
		t.Fatalf("expected replace to delta-update total height to 6, got %d", got)
	}

	transcript.Insert(1, TranscriptItem{Element: NewCachedElement(Paragraph{Text: "mid"}, 1), GapBefore: 2})
	if got := transcript.ContentHeight(12); got != 9 {
		t.Fatalf("expected insert to delta-update total height to 9, got %d", got)
	}

	transcript.Remove(1)
	if got := transcript.ContentHeight(12); got != 6 {
		t.Fatalf("expected remove to delta-update total height back to 6, got %d", got)
	}
}
