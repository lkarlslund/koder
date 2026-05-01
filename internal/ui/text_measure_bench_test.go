package ui

import "testing"

func BenchmarkStyledTextWidth(b *testing.B) {
	style := CellStyle{}.WithBold(true)
	spans := make([]StyledSpan, 0, 256)
	for i := 0; i < 256; i++ {
		spans = append(spans,
			StyledSpan{Text: "alpha beta gamma delta ", Style: style},
			StyledSpan{Text: "表情 symbols and words ", Style: CellStyle{}},
		)
	}

	b.ReportAllocs()
	for b.Loop() {
		_ = StyledTextWidth(spans)
	}
}

func BenchmarkPlainWidthASCII(b *testing.B) {
	text := "alpha beta gamma delta epsilon zeta eta theta"

	b.ReportAllocs()
	for b.Loop() {
		_ = PlainWidth(text)
	}
}

func BenchmarkPlainWidthWideRunes(b *testing.B) {
	text := "alpha 表情 beta gamma 表cd efghij"

	b.ReportAllocs()
	for b.Loop() {
		_ = PlainWidth(text)
	}
}

func BenchmarkWrapStyledTextWidthOnly(b *testing.B) {
	style := CellStyle{}.WithItalic(true)
	spans := make([]StyledSpan, 0, 128)
	for i := 0; i < 128; i++ {
		spans = append(spans,
			StyledSpan{Text: "alpha beta gamma ", Style: style},
			StyledSpan{Text: "表cd efghij ", Style: CellStyle{}},
		)
	}

	b.ReportAllocs()
	for b.Loop() {
		_ = WrapStyledText(spans, 48)
	}
}
