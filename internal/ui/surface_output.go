package ui

import (
	"strconv"
	"strings"
)

// This file is the ANSI output boundary for Surface serialization.
// Widgets and dialogs should operate on plain text and cells only.

func applyCellStyle(style CellStyle, text string) string {
	if style.isZero() || text == "" {
		return text
	}
	params := make([]string, 0, 10)
	if style.Bold {
		params = append(params, "1")
	}
	if style.Italic {
		params = append(params, "3")
	}
	if style.Underline {
		params = append(params, "4")
	}
	if style.Strikethrough {
		params = append(params, "9")
	}
	if style.FG.Valid {
		params = append(params, "38", "2",
			strconv.Itoa(int(style.FG.R)),
			strconv.Itoa(int(style.FG.G)),
			strconv.Itoa(int(style.FG.B)),
		)
	}
	if style.BG.Valid {
		params = append(params, "48", "2",
			strconv.Itoa(int(style.BG.R)),
			strconv.Itoa(int(style.BG.G)),
			strconv.Itoa(int(style.BG.B)),
		)
	}
	if len(params) == 0 {
		return text
	}
	return "\x1b[" + strings.Join(params, ";") + "m" + text + "\x1b[0m"
}

func RenderStyledTextANSI(spans []StyledSpan) string {
	var b strings.Builder
	for _, span := range spans {
		b.WriteString(applyCellStyle(span.Style, span.Text))
	}
	return b.String()
}

func serializeSurfaceRow(s Surface, y int) string {
	if y < 0 || y >= s.h || len(s.cells) == 0 {
		return ""
	}
	var b strings.Builder
	var currentStyle CellStyle
	var segment strings.Builder
	flush := func() {
		if segment.Len() == 0 {
			return
		}
		b.WriteString(applyCellStyle(currentStyle, segment.String()))
		segment.Reset()
	}
	for x := 0; x < s.w; x++ {
		cell := s.cellAt(x, y)
		if cell.Continuation {
			continue
		}
		text := cell.Text
		if text == "" {
			text = " "
		}
		if segment.Len() > 0 && !currentStyle.equal(cell.Style) {
			flush()
		}
		currentStyle = cell.Style
		segment.WriteString(text)
	}
	flush()
	return b.String()
}
