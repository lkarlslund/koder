package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/store"
)

func TestMatchingSlashCommands(t *testing.T) {
	all := matchingSlashCommands("")
	if len(all) == 0 {
		t.Fatal("expected slash commands")
	}

	matches := matchingSlashCommands("n")
	if len(matches) != 1 {
		t.Fatalf("expected one match, got %d", len(matches))
	}
	if matches[0].Name != "/new" {
		t.Fatalf("expected /new, got %s", matches[0].Name)
	}

	matches = matchingSlashCommands("per")
	if len(matches) != 1 || matches[0].Name != "/perm" {
		t.Fatalf("expected /perm, got %#v", matches)
	}
}

func TestSlashQuery(t *testing.T) {
	query, ok := slashQuery("/")
	if !ok || query != "" {
		t.Fatalf("unexpected slash query for /: ok=%v query=%q", ok, query)
	}

	query, ok = slashQuery("/new")
	if !ok || query != "new" {
		t.Fatalf("unexpected slash query for /new: ok=%v query=%q", ok, query)
	}

	if _, ok := slashQuery("/read file.txt"); ok {
		t.Fatal("expected no autocomplete query after slash command arguments start")
	}
}

func TestEnterSendsNormalPrompt(t *testing.T) {
	m := Model{
		composer: textarea.New(),
		parts:    map[int64][]domain.Part{},
	}
	m.composer.SetValue("hello")

	updated, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected send command")
	}
	if !next.loading {
		t.Fatal("expected loading after enter")
	}
	if next.composer.Value() != "" {
		t.Fatalf("expected composer reset, got %q", next.composer.Value())
	}
	if len(next.messages) != 1 || next.messages[0].Summary != "hello" {
		t.Fatalf("expected optimistic user message, got %#v", next.messages)
	}
	if len(next.parts[next.messages[0].ID]) != 1 || next.parts[next.messages[0].ID][0].Body != "hello" {
		t.Fatalf("expected optimistic user part, got %#v", next.parts)
	}
}

func TestExactSlashCommandDoesNotConsumeEnterForAutocomplete(t *testing.T) {
	m := Model{
		composer: textarea.New(),
	}
	m.composer.SetValue("/read")
	m.updateSlashMenu()

	updated, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected enter to continue to normal command handling")
	}
	if !next.loading {
		t.Fatal("expected loading after slash command enter")
	}
}

func TestApprovalPromptConsumesEnter(t *testing.T) {
	m := Model{
		composer:  textarea.New(),
		approvals: []store.Approval{{ID: 7}},
	}

	updated, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected approval command")
	}
	if !next.loading {
		t.Fatal("expected loading after approval enter")
	}
}

func TestQuitCommandQuits(t *testing.T) {
	m := Model{
		composer: textarea.New(),
	}
	m.composer.SetValue("/quit")

	updated, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected quit command")
	}
	if next.loading {
		t.Fatal("expected quit to stop loading")
	}
}

func TestCtrlCUsesQuitPath(t *testing.T) {
	m := Model{
		composer: textarea.New(),
	}

	updated, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlC})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected quit command")
	}
	if next.status != "Quitting" {
		t.Fatalf("unexpected status: %q", next.status)
	}
}

func TestSessionPickerEscapeCreatesNewSession(t *testing.T) {
	m := Model{
		composer:      textarea.New(),
		pickerVisible: true,
		sessions:      []domain.Session{{ID: 1}},
	}

	updated, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected new session command")
	}
	if !next.loading {
		t.Fatal("expected loading after picker escape")
	}
}

func TestUpdateLoadHidesSessionPicker(t *testing.T) {
	m := Model{
		pickerVisible: true,
	}

	updated := m.UpdateLoad(loadMsg{
		current: domain.Session{ID: 4},
	})

	if updated.pickerVisible {
		t.Fatal("expected picker to close after loading a session")
	}
	if updated.currentSession.ID != 4 {
		t.Fatalf("unexpected current session: %#v", updated.currentSession)
	}
}

func TestWorkingIndicatorShownWhenLoading(t *testing.T) {
	m := Model{
		loading: true,
	}

	got := m.workingIndicator()
	if got == "" {
		t.Fatal("expected working indicator while loading")
	}
}

func TestRenderMessagePartsShowsReasoningBeforeText(t *testing.T) {
	m := Model{
		showReasoning: true,
	}

	got := m.renderMessageParts([]domain.Part{
		{Kind: domain.PartKindText, Body: "final answer"},
		{Kind: domain.PartKindReasoning, Body: "thinking first"},
	})

	if !strings.Contains(got, "thinking first") || !strings.Contains(got, "final answer") {
		t.Fatalf("expected both reasoning and text, got %q", got)
	}
	if strings.Index(got, "thinking first") > strings.Index(got, "final answer") {
		t.Fatalf("expected reasoning before text, got %q", got)
	}
}

func TestRenderReasoningBlockStartsWithBlankStyledLine(t *testing.T) {
	m := Model{}

	got := m.renderReasoningBlock("thinking first")
	lines := strings.Split(got, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected multiple lines, got %q", got)
	}
	if lines[0] != "" {
		t.Fatalf("expected blank first line, got %q", got)
	}
	if !strings.Contains(lines[1], "thinking first") {
		t.Fatalf("expected reasoning text on second line, got %q", got)
	}
}
