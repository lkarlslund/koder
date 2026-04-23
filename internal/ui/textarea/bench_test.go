package textarea

import (
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/ui"
)

func BenchmarkUpdateRunes(b *testing.B) {
	m := New()
	msg := ui.KeyMsg{Type: ui.KeyRunes, Runes: []rune("benchmark")}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		next, _ := m.Update(msg)
		m = next
	}
}

func BenchmarkVisibleLineLongBuffer(b *testing.B) {
	m := New()
	m.SetValue(strings.Repeat("0123456789abcdef", 32))
	m.SetWidth(80)
	m.SetCursor(len(m.Value()))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = m.VisibleLine()
	}
}

func BenchmarkViewPlaceholder(b *testing.B) {
	m := New()
	m.Placeholder = "Ask koder or type / for commands"
	m.SetWidth(80)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = m.View()
	}
}
