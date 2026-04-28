package ui

import (
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/theme"
)

func TestPendingInputPreviewShowsQueueRowsInMutedStyle(t *testing.T) {
	palette := theme.Palette{
		ComposerMutedText:  ParseCellColor("#112233"),
		UserTextBackground: ParseCellColor("#445566"),
	}

	got := PendingInputPreview{
		Width: 40,
		Items: []PendingInputRow{{
			Badge: "QUEUED",
			Text:  "queued submission",
		}},
	}.render(palette)

	plain := SurfaceText(got)
	if !strings.Contains(plain, "Queued inputs") {
		t.Fatalf("expected queued preview header, got %q", plain)
	}
	if !strings.Contains(plain, "QUEUED queued submission") {
		t.Fatalf("expected queued preview row, got %q", plain)
	}
	r, g, b, ok := got.SurfaceCellFG(0, 0)
	if !ok || r != 0x11 || g != 0x22 || b != 0x33 {
		t.Fatalf("expected muted foreground color in queued preview, got (%d,%d,%d,%v)", r, g, b, ok)
	}
}

func TestPendingInputPreviewShowsEditModeAndSelectedRow(t *testing.T) {
	palette := theme.Default().Palette
	got := SurfaceText(PendingInputPreview{
		Width: 56,
		Items: []PendingInputRow{
			{Badge: "STEER", Text: "Please continue."},
			{Badge: "QUEUED", Text: "follow up later", Selected: true},
		},
		EditingMode: true,
	}.render(palette))
	plain := got

	if !strings.Contains(plain, "Queued inputs • edit mode") {
		t.Fatalf("expected edit mode header, got %q", plain)
	}
	if strings.Index(plain, "STEER Please continue.") > strings.Index(plain, "QUEUED follow up later") {
		t.Fatalf("expected rows to render in queue order, got %q", plain)
	}
}
