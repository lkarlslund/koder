package tea

import (
	"testing"

	"github.com/charmbracelet/x/input"
)

func BenchmarkKeyMsgString(b *testing.B) {
	msg := KeyMsg{Type: KeyRunes, Runes: []rune("p"), Alt: true}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = msg.String()
	}
}

func BenchmarkConvertKeyPressRune(b *testing.B) {
	ev := input.KeyPressEvent(input.Key{Text: "x", Code: 'x'})
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = convertKeyPress(ev)
	}
}

func BenchmarkConvertKeyPressCtrl(b *testing.B) {
	ev := input.KeyPressEvent(input.Key{Text: "r", Code: 'r', Mod: input.ModCtrl})
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = convertKeyPress(ev)
	}
}

func BenchmarkBatchConstruction(b *testing.B) {
	cmdA := func() Msg { return KeyMsg{Type: KeyEnter} }
	cmdB := func() Msg { return KeyMsg{Type: KeyEsc} }
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		cmd := Batch(cmdA, cmdB)
		_ = cmd()
	}
}
