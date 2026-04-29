package ui

import (
	"testing"

	"github.com/charmbracelet/x/input"
)

func TestConvertEventSequenceKeepsEscThenPrintableRuneSeparate(t *testing.T) {
	events := []input.Event{
		input.KeyPressEvent(input.Key{Code: input.KeyEsc}),
		input.KeyPressEvent(input.Key{Code: 'h', Text: "h"}),
	}

	msgs := convertEventSequence(events)
	if len(msgs) != 2 {
		t.Fatalf("expected two messages, got %#v", msgs)
	}
	first, ok := msgs[0].(KeyMsg)
	if !ok || first.Type != KeyEsc || first.Alt {
		t.Fatalf("expected first message to stay plain esc, got %#v", msgs[0])
	}
	second, ok := msgs[1].(KeyMsg)
	if !ok || second.Type != KeyRunes || second.Alt || string(second.Runes) != "h" {
		t.Fatalf("expected second message to stay plain rune, got %#v", msgs[1])
	}
}

func TestConvertEventSequenceSynthesizesAltForEditorNavigationKeys(t *testing.T) {
	events := []input.Event{
		input.KeyPressEvent(input.Key{Code: input.KeyEsc}),
		input.KeyPressEvent(input.Key{Code: input.KeyLeft}),
	}

	msgs := convertEventSequence(events)
	if len(msgs) != 1 {
		t.Fatalf("expected one synthesized alt message, got %#v", msgs)
	}
	got, ok := msgs[0].(KeyMsg)
	if !ok || got.Type != KeyLeft || !got.Alt {
		t.Fatalf("expected synthesized alt+left, got %#v", msgs[0])
	}
}
