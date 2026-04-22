package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/lkarlslund/koder/internal/theme"
)

func TestDialogExpandsToFitButtonRowOnSingleLine(t *testing.T) {
	palette := theme.Default().Palette
	got := Dialog{
		Title:    "Resume Session",
		Sections: []string{"Body"},
		Buttons: ButtonRow{
			Buttons: []Button{
				{Label: "OK", Primary: true},
				{Label: "Cancel"},
			},
			Align: HorizontalAlignRight,
		},
		Footer: "Enter selects. Esc cancels.",
		Width:  12,
	}.View(palette)

	lines := strings.Split(ansi.Strip(got), "\n")
	var buttonLine string
	for _, line := range lines {
		if strings.Contains(line, "OK") && strings.Contains(line, "Cancel") {
			buttonLine = line
			break
		}
	}
	if buttonLine == "" {
		t.Fatalf("expected single button row in dialog, got %q", got)
	}
	if strings.Contains(buttonLine, "\n") {
		t.Fatalf("expected buttons on one line, got %q", buttonLine)
	}
	okIndex := strings.Index(buttonLine, "OK")
	cancelIndex := strings.Index(buttonLine, "Cancel")
	if okIndex < 0 || cancelIndex < 0 || cancelIndex <= okIndex {
		t.Fatalf("expected right-aligned OK/Cancel row, got %q", buttonLine)
	}
	if okIndex == 0 {
		t.Fatalf("expected button row to include left padding for right alignment, got %q", buttonLine)
	}
}
