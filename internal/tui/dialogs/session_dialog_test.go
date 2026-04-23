package dialogs

import (
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	tea "github.com/lkarlslund/koder/internal/ui/tea"

	"github.com/lkarlslund/koder/internal/theme"
	"github.com/lkarlslund/koder/internal/ui"
)

func renderSessionDialog(dialog SessionDialog, width int, palette theme.Palette) string {
	return ui.RenderElement(&ui.Context{Palette: palette}, dialog, width, 0)
}

func TestSessionDialogSelectsCurrentSession(t *testing.T) {
	dialog := NewSessionDialog([]SessionItem{
		{Title: "First", Value: "1"},
		{Title: "Second", Value: "2"},
	}, false)
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
	}, false)
	dialog.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})

	view := renderSessionDialog(dialog, 84, theme.Default().Palette)
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
	}}, false)

	got := renderSessionDialog(dialog, 84, theme.Default().Palette)
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
	stripped := strings.Split(ansi.Strip(got), "\n")
	lineThreeRow := -1
	for i, line := range stripped {
		if strings.Contains(line, "line three") {
			lineThreeRow = i
			break
		}
	}
	trimDialogLine := func(line string) string {
		return strings.TrimSpace(strings.Trim(line, "│ "))
	}
	if lineThreeRow < 1 || trimDialogLine(stripped[lineThreeRow-1]) != "" {
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
	}}, false)

	got := renderSessionDialog(dialog, 84, theme.Default().Palette)
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
	}}, false)

	got := renderSessionDialog(dialog, 84, theme.Default().Palette)
	if strings.Contains(got, "line 11") || strings.Contains(got, "line 12") {
		t.Fatalf("expected preview to clamp at ten lines, got %q", got)
	}
	if !strings.Contains(got, "line 10 …") {
		t.Fatalf("expected clamped preview marker on tenth line, got %q", got)
	}
}

func TestSessionDialogViewShowsCWDColumnWhenEnabled(t *testing.T) {
	dialog := NewSessionDialog([]SessionItem{{
		SessionID:    "#1",
		CreatedAt:    "10h ago",
		ModifiedAt:   "3m ago",
		TokenSummary: "123/456",
		Title:        "Session A",
		CWD:          "/tmp/worktree",
		Value:        "1",
	}}, true)

	got := renderSessionDialog(dialog, 96, theme.Default().Palette)
	if !strings.Contains(got, "CWD") || !strings.Contains(got, "/tmp/worktree") {
		t.Fatalf("expected cwd column in session dialog, got %q", got)
	}
}

func TestSessionDialogAltCCancels(t *testing.T) {
	dialog := NewSessionDialog([]SessionItem{{Title: "First", Value: "1"}}, false)
	action := dialog.Update(tea.KeyMsg{Type: tea.KeyRunes, Alt: true, Runes: []rune("c")})
	if action.Kind != SessionDialogActionCancel {
		t.Fatalf("expected alt+c to cancel, got %#v", action)
	}
}

func TestSessionDialogActivateCancelButton(t *testing.T) {
	dialog := NewSessionDialog([]SessionItem{{Title: "First", Value: "1"}}, false)
	action := dialog.ActivateControl("cancel")
	if action.Kind != SessionDialogActionCancel {
		t.Fatalf("expected cancel action, got %#v", action)
	}
}

func TestSessionDialogAdaptsToNarrowWidth(t *testing.T) {
	dialog := NewSessionDialog([]SessionItem{{
		SessionID:    "#1",
		CreatedAt:    "10h ago",
		ModifiedAt:   "3m ago",
		TokenSummary: "123/456",
		Title:        "Very Long Session Title",
		Value:        "1",
	}}, false)

	got := renderSessionDialog(dialog, 72, theme.Default().Palette)
	for _, line := range strings.Split(ansi.Strip(got), "\n") {
		if w := ansi.StringWidth(line); w > 72 {
			t.Fatalf("expected dialog to fit requested width, got line width %d in %q", w, line)
		}
	}
}
