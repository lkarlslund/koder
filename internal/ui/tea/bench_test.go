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

func BenchmarkRenderFrameLinesLarge(b *testing.B) {
	lines := make([]string, 0, 120)
	for i := 0; i < 120; i++ {
		lines = append(lines, "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = renderFrameLines(lines)
	}
}

func BenchmarkDiffFrameNoChanges(b *testing.B) {
	lines := make([]string, 0, 80)
	for i := 0; i < 80; i++ {
		lines = append(lines, "steady line content")
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = diffFrameLines(lines, lines)
	}
}

func BenchmarkDiffFrameSingleRowChanged(b *testing.B) {
	previous := make([]string, 0, 80)
	current := make([]string, 0, 80)
	for i := 0; i < 80; i++ {
		line := "steady line content"
		previous = append(previous, line)
		current = append(current, line)
	}
	current[40] = "changed line content"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = diffFrameLines(previous, current)
	}
}

func BenchmarkDiffFrameFewRowsChanged(b *testing.B) {
	previous := make([]string, 0, 80)
	current := make([]string, 0, 80)
	for i := 0; i < 80; i++ {
		line := "steady line content"
		previous = append(previous, line)
		current = append(current, line)
	}
	for _, row := range []int{5, 20, 41, 79} {
		current[row] = "changed line content"
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = diffFrameLines(previous, current)
	}
}
