package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/lkarlslund/koder/internal/theme"
)

type PendingInputPreview struct {
	Width          int
	PendingSteers  []string
	RejectedSteers []string
	QueuedMessages []string
}

const pendingInputPreviewLineLimit = 3

func (p PendingInputPreview) render(palette theme.Palette) Surface {
	if p.Width <= 0 || (len(p.PendingSteers) == 0 && len(p.RejectedSteers) == 0 && len(p.QueuedMessages) == 0) {
		return Surface{}
	}
	mutedFG := palette.ComposerMutedText
	bg := palette.UserTextBackground

	rows := make([]Surface, 0, 8)
	if len(p.PendingSteers) > 0 {
		rows = append(rows, p.renderHeader("Messages to be submitted when current run finishes", mutedFG, bg))
		rows = append(rows, p.renderPreviewRows(p.PendingSteers, mutedFG, bg, true)...)
	}
	if len(p.RejectedSteers) > 0 {
		if len(rows) > 0 {
			rows = append(rows, p.renderBlank(bg))
		}
		rows = append(rows, p.renderHeader("Messages to be submitted at end of turn", mutedFG, bg))
		rows = append(rows, p.renderPreviewRows(p.RejectedSteers, mutedFG, bg, true)...)
	}
	if len(p.QueuedMessages) > 0 {
		if len(rows) > 0 {
			rows = append(rows, p.renderBlank(bg))
		}
		rows = append(rows, p.renderHeader("Queued follow-up inputs", mutedFG, bg))
		rows = append(rows, p.renderPreviewRows(p.QueuedMessages, mutedFG, bg, true)...)
	}
	surface := BlankSurface(p.Width, len(rows))
	for y, row := range rows {
		surface = surface.placeAt(0, y, row)
	}
	return surface
}

func (p PendingInputPreview) Measure(ctx *Context, constraints Constraints) Size {
	width := p.Width
	if width <= 0 {
		width = constraints.maxWidth()
	}
	return constraints.Clamp(PendingInputPreview{
		Width:          width,
		PendingSteers:  p.PendingSteers,
		RejectedSteers: p.RejectedSteers,
		QueuedMessages: p.QueuedMessages,
	}.render(ctx.Palette).Size())
}

func (p PendingInputPreview) Render(ctx *Context, bounds Rect) Surface {
	width := p.Width
	if width <= 0 {
		width = bounds.W
	}
	return PendingInputPreview{
		Width:          width,
		PendingSteers:  p.PendingSteers,
		RejectedSteers: p.RejectedSteers,
		QueuedMessages: p.QueuedMessages,
	}.render(ctx.Palette).normalize(bounds.W, bounds.H)
}

func (p PendingInputPreview) renderHeader(text string, fg, bg lipgloss.Color) Surface {
	width := maxInt(1, p.Width)
	prefix := "• "
	available := maxInt(1, width-PlainWidth(prefix))
	label := PlainTruncate(text, available, "")
	surface := BlankSurface(width, 1)
	style := CellStyle{FG: cellColor(fg), BG: cellColor(bg)}
	for x := 0; x < width; x++ {
		surface.setCell(x, 0, Cell{Text: " ", Width: 1, Style: style})
	}
	surface.WriteText(0, 0, prefix+label, style)
	return surface
}

func (p PendingInputPreview) renderPreviewRows(messages []string, fg, bg lipgloss.Color, italic bool) []Surface {
	rows := make([]Surface, 0, len(messages))
	for _, message := range messages {
		lines := strings.Split(strings.ReplaceAll(message, "\r\n", "\n"), "\n")
		rendered := 0
		for _, line := range lines {
			for _, wrapped := range wrapPreviewLine(line, maxInt(1, p.Width-4)) {
				prefix := "  ↳ "
				if rendered > 0 {
					prefix = "    "
				}
				rows = append(rows, renderPendingPreviewLine(prefix, wrapped, p.Width, fg, bg, italic))
				rendered++
				if rendered >= pendingInputPreviewLineLimit {
					break
				}
			}
			if rendered >= pendingInputPreviewLineLimit {
				break
			}
		}
		if countWrappedPreviewLines(lines, maxInt(1, p.Width-4)) > pendingInputPreviewLineLimit {
			rows = append(rows, renderPendingPreviewLine("    ", "…", p.Width, fg, bg, italic))
		}
	}
	return rows
}

func (p PendingInputPreview) renderBlank(bg lipgloss.Color) Surface {
	width := maxInt(1, p.Width)
	surface := BlankSurface(width, 1)
	style := CellStyle{FG: cellColor(bg), BG: cellColor(bg)}
	for x := 0; x < width; x++ {
		surface.setCell(x, 0, Cell{Text: " ", Width: 1, Style: style})
	}
	return surface
}

func renderPendingPreviewLine(prefix, text string, width int, fg, bg lipgloss.Color, italic bool) Surface {
	width = maxInt(1, width)
	prefix = PlainTruncate(prefix, width, "")
	available := maxInt(0, width-PlainWidth(prefix))
	text = PlainTruncate(text, available, "")
	surface := BlankSurface(width, 1)
	baseStyle := CellStyle{FG: cellColor(fg), BG: cellColor(bg)}
	textStyle := baseStyle
	if italic {
		textStyle.Italic = true
	}
	for x := 0; x < width; x++ {
		surface.setCell(x, 0, Cell{Text: " ", Width: 1, Style: baseStyle})
	}
	surface.WriteText(0, 0, prefix, baseStyle)
	surface.WriteText(PlainWidth(prefix), 0, text, textStyle)
	return surface
}

func wrapPreviewLine(text string, width int) []string {
	width = maxInt(1, width)
	text = strings.TrimSpace(text)
	if text == "" {
		return []string{""}
	}
	var lines []string
	remaining := text
	for remaining != "" {
		if PlainWidth(remaining) <= width {
			lines = append(lines, remaining)
			break
		}
		cut := width
		runes := []rune(remaining)
		if cut > len(runes) {
			cut = len(runes)
		}
		segment := string(runes[:cut])
		if idx := strings.LastIndex(segment, " "); idx > 0 {
			segment = strings.TrimRight(segment[:idx], " ")
		}
		if segment == "" {
			segment = string(runes[:cut])
		}
		lines = append(lines, segment)
		remaining = strings.TrimSpace(strings.TrimPrefix(remaining, segment))
	}
	return lines
}

func countWrappedPreviewLines(lines []string, width int) int {
	count := 0
	for _, line := range lines {
		count += len(wrapPreviewLine(line, width))
	}
	return count
}
