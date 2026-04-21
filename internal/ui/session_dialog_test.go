package ui

import (
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/lkarlslund/koder/internal/theme"
)

func TestSessionDialogSelectsCurrentSession(t *testing.T) {
	dialog := NewSessionDialog([]SessionItem{
		{Title: "First", Value: "1"},
		{Title: "Second", Value: "2"},
	})
	dialog.Update(tea.KeyMsg{Type: tea.KeyDown})

	action := dialog.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if action.Kind != SessionDialogActionSelect {
		t.Fatalf("expected select action, got %#v", action)
	}
	if action.SessionID != 2 {
		t.Fatalf("expected session 2, got %d", action.SessionID)
	}
}

func TestSessionDialogFiltersItems(t *testing.T) {
	dialog := NewSessionDialog([]SessionItem{
		{Title: "Alpha", Value: "1"},
		{Title: "Beta", Value: "2"},
	})
	dialog.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})

	view := dialog.View(84, theme.Default().Palette)
	if strings.Contains(view, "Alpha") {
		t.Fatalf("expected filtered session list, got %q", view)
	}
	if !strings.Contains(view, "Beta") {
		t.Fatalf("expected Beta in filtered list, got %q", view)
	}
}

func TestSessionDialogViewPreservesPreviewLineBreaks(t *testing.T) {
	dialog := NewSessionDialog([]SessionItem{{
		SessionID:    "#1",
		CreatedAt:    "10h ago",
		ModifiedAt:   "3m ago",
		TokenSummary: "123/456",
		Title:        "Session A",
		Description:  "line one\nline two\n\nline three",
		Preview:      "line one\nline two\n\nline three",
		Value:        "1",
	}})

	got := dialog.View(84, theme.Default().Palette)
	lineOne := strings.Index(got, "line one")
	lineTwo := strings.Index(got, "line two")
	lineThree := strings.Index(got, "line three")
	if lineOne < 0 || lineTwo < 0 || lineThree < 0 {
		t.Fatalf("expected preview lines in view, got %q", got)
	}
	if !(lineOne < lineTwo && lineTwo < lineThree) {
		t.Fatalf("expected preview lines to remain ordered, got %q", got)
	}
	if !strings.Contains(got, "line two") || !strings.Contains(got, "line three") {
		t.Fatalf("expected preview lines in detail pane, got %q", got)
	}
	if !strings.Contains(got, "│                                                                                                │\n│   line three") {
		t.Fatalf("expected blank line before line three, got %q", got)
	}
	if !strings.Contains(got, "ID") || !strings.Contains(got, "Created") || !strings.Contains(got, "Modified") || !strings.Contains(got, "Tokens") {
		t.Fatalf("expected table headers in session dialog, got %q", got)
	}
	if !strings.Contains(got, "10h ago") || !strings.Contains(got, "3m ago") {
		t.Fatalf("expected relative times in table row, got %q", got)
	}
	if strings.Contains(got, "Session ID: 1") {
		t.Fatalf("expected session details to stay in the table only, got %q", got)
	}
	if !strings.Contains(got, "OK") || !strings.Contains(got, "Cancel") {
		t.Fatalf("expected dialog buttons in session dialog, got %q", got)
	}
}

func TestSessionDialogViewPrefersPreviewOverDescription(t *testing.T) {
	dialog := NewSessionDialog([]SessionItem{{
		Title:       "Session A",
		Description: "plain fallback",
		Preview:     "rendered\npreview",
		Value:       "1",
	}})

	got := dialog.View(84, theme.Default().Palette)
	if !strings.Contains(got, "rendered") || !strings.Contains(got, "preview") {
		t.Fatalf("expected rendered preview in detail pane, got %q", got)
	}
	if strings.Contains(got, "plain fallback") {
		t.Fatalf("expected description fallback to be ignored when preview exists, got %q", got)
	}
}

func TestSessionDialogViewClampsPreviewToTenLines(t *testing.T) {
	lines := make([]string, 12)
	for i := range lines {
		lines[i] = "line " + strconv.Itoa(i+1)
	}
	dialog := NewSessionDialog([]SessionItem{{
		Title:   "Session A",
		Preview: strings.Join(lines, "\n"),
		Value:   "1",
	}})

	got := dialog.View(84, theme.Default().Palette)
	if strings.Contains(got, "line 11") || strings.Contains(got, "line 12") {
		t.Fatalf("expected preview to clamp at ten lines, got %q", got)
	}
	if !strings.Contains(got, "line 10 …") {
		t.Fatalf("expected clamped preview marker on tenth line, got %q", got)
	}
}

func TestSessionDialogAltCCancels(t *testing.T) {
	dialog := NewSessionDialog([]SessionItem{{Title: "First", Value: "1"}})
	action := dialog.Update(tea.KeyMsg{Type: tea.KeyRunes, Alt: true, Runes: []rune("c")})
	if action.Kind != SessionDialogActionCancel {
		t.Fatalf("expected alt+c to cancel, got %#v", action)
	}
}

func TestSessionDialogMouseCancelButton(t *testing.T) {
	palette := theme.Default().Palette
	dialog := NewSessionDialog([]SessionItem{{Title: "First", Value: "1"}})
	lines := strings.Split(dialog.View(96, palette), "\n")

	buttonLine := -1
	cancelX := -1
	for idx, line := range lines {
		stripped := ansi.Strip(line)
		if !strings.Contains(stripped, "OK") || !strings.Contains(stripped, "Cancel") {
			continue
		}
		buttonLine = idx
		cancelX = strings.Index(stripped, "Cancel") + 1
		break
	}
	if buttonLine < 0 || cancelX < 0 {
		t.Fatalf("failed to find cancel button in view: %q", dialog.View(96, palette))
	}

	action := dialog.HandleMouse(cancelX, buttonLine, 96, palette)
	if action.Kind != SessionDialogActionCancel {
		t.Fatalf("expected mouse click to cancel, got %#v", action)
	}
}
