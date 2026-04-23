package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/lkarlslund/koder/internal/theme"
)

func TestPendingInputPreviewShowsQueuedMessagesInMutedStyle(t *testing.T) {
	palette := theme.Palette{
		ComposerMutedText:  "#112233",
		UserTextBackground: "#445566",
	}

	got := PendingInputPreview{
		Width:          40,
		QueuedMessages: []string{"queued submission"},
	}.render(palette)

	plain := got.String()
	if !strings.Contains(ansi.Strip(plain), "Queued follow-up inputs") {
		t.Fatalf("expected queued preview header, got %q", plain)
	}
	if !strings.Contains(ansi.Strip(plain), "↳ queued submission") {
		t.Fatalf("expected queued preview row, got %q", plain)
	}
	r, g, b, ok := got.SurfaceCellFG(0, 0)
	if !ok || r != 0x11 || g != 0x22 || b != 0x33 {
		t.Fatalf("expected muted foreground color in queued preview, got (%d,%d,%d,%v)", r, g, b, ok)
	}
}

func TestPendingInputPreviewShowsPendingSteersBeforeQueuedMessages(t *testing.T) {
	palette := theme.Default().Palette
	got := PendingInputPreview{
		Width:          56,
		PendingSteers:  []string{"Please continue."},
		QueuedMessages: []string{"follow up later"},
	}.render(palette).String()
	plain := ansi.Strip(got)

	steerIdx := strings.Index(plain, "Messages to be submitted after next tool call")
	queueIdx := strings.Index(plain, "Queued follow-up inputs")
	if steerIdx == -1 || queueIdx == -1 {
		t.Fatalf("expected both preview sections, got %q", plain)
	}
	if steerIdx > queueIdx {
		t.Fatalf("expected pending steers before queued messages, got %q", plain)
	}
}
