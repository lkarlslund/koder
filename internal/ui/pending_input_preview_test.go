package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/lkarlslund/koder/internal/theme"
)

func TestPendingInputPreviewShowsQueuedMessagesInMutedStyle(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)

	palette := theme.Palette{
		ComposerMutedText:  "#112233",
		UserTextBackground: "#445566",
	}

	got := PendingInputPreview{
		Width:          40,
		QueuedMessages: []string{"queued submission"},
	}.render(palette).String()

	if !strings.Contains(ansi.Strip(got), "Queued follow-up inputs") {
		t.Fatalf("expected queued preview header, got %q", got)
	}
	if !strings.Contains(ansi.Strip(got), "↳ queued submission") {
		t.Fatalf("expected queued preview row, got %q", got)
	}
	if !strings.Contains(got, "38;2;17;34;51") {
		t.Fatalf("expected muted foreground color in queued preview, got %q", got)
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
