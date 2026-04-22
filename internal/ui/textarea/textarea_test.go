package textarea

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestUpdateInsertsRunesAndSpaces(t *testing.T) {
	m := newTestModel()

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hi")})
	if cmd == nil {
		t.Fatal("expected blink command after rune input")
	}
	if got := next.Value(); got != "hi" {
		t.Fatalf("expected rune input to be inserted, got %q", got)
	}

	next, _ = next.Update(tea.KeyMsg{Type: tea.KeySpace})
	if got := next.Value(); got != "hi " {
		t.Fatalf("expected space key to insert a space, got %q", got)
	}

	next, _ = next.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("there")})
	if got := next.Value(); got != "hi there" {
		t.Fatalf("expected text after space insertion, got %q", got)
	}
}

func TestUpdateMovesCursorAndEditsAtCursor(t *testing.T) {
	m := newTestModel()
	m.SetValue("ac")
	m.SetCursor(len("a"))

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	if got := next.Value(); got != "abc" {
		t.Fatalf("expected insertion at cursor, got %q", got)
	}

	next, _ = next.Update(tea.KeyMsg{Type: tea.KeyLeft})
	next, _ = next.Update(tea.KeyMsg{Type: tea.KeyDelete})
	if got := next.Value(); got != "ac" {
		t.Fatalf("expected delete at cursor to remove the following rune, got %q", got)
	}

	next, _ = next.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if got := next.Value(); got != "c" {
		t.Fatalf("expected backspace to remove the previous rune, got %q", got)
	}
}

func TestUpdateHomeEndAndControlAliases(t *testing.T) {
	m := newTestModel()
	m.SetValue("hello")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyHome})
	next, _ = next.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("!")})
	if got := next.Value(); got != "!hello" {
		t.Fatalf("expected home to move to the beginning, got %q", got)
	}

	next, _ = next.Update(tea.KeyMsg{Type: tea.KeyEnd})
	next, _ = next.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	if got := next.Value(); got != "!hello?" {
		t.Fatalf("expected end to move to the end, got %q", got)
	}

	next, _ = next.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" more")})
	next, _ = next.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	next, _ = next.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(">")})
	if got := next.Value(); got[0] != '>' {
		t.Fatalf("expected ctrl+a to move cursor to start, got %q", got)
	}

	next, _ = next.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	next, _ = next.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("<")})
	if got := next.Value(); got[len(got)-1] != '<' {
		t.Fatalf("expected ctrl+e to move cursor to end, got %q", got)
	}
}

func TestSetCursorUsesByteOffsets(t *testing.T) {
	m := newTestModel()
	m.SetValue("hø")
	m.SetCursor(len("h"))
	m.InsertRune('!')
	if got := m.Value(); got != "h!ø" {
		t.Fatalf("expected byte-based cursor offset to land after the first rune, got %q", got)
	}
}

func TestVisibleLineTracksCurrentLine(t *testing.T) {
	m := newTestModel()
	m.SetValue("first line\nsecond line\nthird line")
	m.SetCursor(len("first line\nsecond"))
	line := m.visibleLine()
	if line.plain != "second line" {
		t.Fatalf("expected current line to be selected, got %q", line.plain)
	}
}

func TestViewScrollsLongLinesAroundCursor(t *testing.T) {
	m := newTestModel()
	m.Prompt = "> "
	m.SetWidth(8)
	m.SetValue("abcdefghijk")
	m.SetCursor(len("abcdefghijk"))
	view := m.View()
	if !containsAll(view, "ghijk") {
		t.Fatalf("expected visible window to include the tail near the cursor, got %q", view)
	}
	if containsAll(view, "abcde") {
		t.Fatalf("expected long line view to scroll, got %q", view)
	}
}

func TestBlinkTogglesCursorVisibility(t *testing.T) {
	m := newTestModel()
	m.Prompt = "> "
	m.SetValue("abc")
	m.SetCursor(len("abc"))

	visible := m.View()
	if !strings.Contains(visible, "abc") {
		t.Fatalf("expected visible cursor rendering in view, got %q", visible)
	}

	next, cmd := m.Update(blinkTickMsg{generation: m.blinkGeneration})
	if cmd == nil {
		t.Fatal("expected blink tick to schedule the next blink")
	}
	hidden := next.View()
	if !containsAll(hidden, "abc") {
		t.Fatalf("expected hidden cursor state to still render plain text, got %q", hidden)
	}
	if hidden == visible {
		t.Fatalf("expected blink tick to change the rendered cursor state, before=%q after=%q", visible, hidden)
	}
}

func TestStaleBlinkTickIsIgnored(t *testing.T) {
	m := newTestModel()
	m.SetValue("abc")
	m.SetCursor(len("abc"))

	currentGen := m.blinkGeneration
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	if next.blinkGeneration == currentGen {
		t.Fatal("expected key input to advance blink generation")
	}

	before := next.View()
	after, cmd := next.Update(blinkTickMsg{generation: currentGen})
	if cmd != nil {
		t.Fatal("expected stale blink tick to be ignored without scheduling a new command")
	}
	if after.View() != before {
		t.Fatalf("expected stale blink tick to leave view unchanged, before=%q after=%q", before, after.View())
	}
}

func TestBlinkCanBeDisabled(t *testing.T) {
	m := newTestModel()
	m.BlinkEnabled = false
	m.SetValue("abc")
	m.SetCursor(len("abc"))

	if cmd := m.BlinkCmd(); cmd != nil {
		t.Fatal("expected no blink command when blinking is disabled")
	}

	visible := m.View()
	next, cmd := m.Update(blinkTickMsg{generation: m.blinkGeneration})
	if cmd != nil {
		t.Fatal("expected disabled blinking to ignore blink ticks")
	}
	if next.View() != visible {
		t.Fatalf("expected cursor to stay visible when blinking is disabled, before=%q after=%q", visible, next.View())
	}
}

func TestBlurStopsBlinkAndInput(t *testing.T) {
	m := newTestModel()
	m.SetValue("abc")
	m.Blur()

	next, cmd := m.Update(blinkTickMsg{generation: m.blinkGeneration})
	if cmd != nil {
		t.Fatal("expected no blink command while blurred")
	}
	if got := next.View(); got != m.View() {
		t.Fatalf("expected blurred view to remain unchanged, before=%q after=%q", m.View(), got)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if got := next.Value(); got != "abc" {
		t.Fatalf("expected blurred model to ignore text input, got %q", got)
	}
}

func TestResetClearsValueAndCursor(t *testing.T) {
	m := newTestModel()
	m.SetValue("hello")
	m.Reset()
	if got := m.Value(); got != "" {
		t.Fatalf("expected reset to clear the value, got %q", got)
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if got := next.Value(); got != "a" {
		t.Fatalf("expected input after reset to start from an empty state, got %q", got)
	}
}

func newTestModel() Model {
	m := New()
	m.Prompt = "> "
	m.SetWidth(20)
	m.Cursor.TextStyle = lipgloss.NewStyle().Reverse(true)
	return m
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
