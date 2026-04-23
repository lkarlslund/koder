package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// This file is the ANSI output boundary for Surface serialization.
// Widgets and dialogs should operate on plain text and cells only.

func applyCellStyle(style CellStyle, text string) string {
	if style.isZero() || text == "" {
		return text
	}
	render := lipgloss.NewStyle()
	if style.FG != "" {
		render = render.Foreground(style.FG)
	}
	if style.BG != "" {
		render = render.Background(style.BG)
	}
	if style.Bold {
		render = render.Bold(true)
	}
	if style.Italic {
		render = render.Italic(true)
	}
	return render.Render(text)
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
