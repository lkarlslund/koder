package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

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

	rows := make([]string, 0, 8)
	if len(p.PendingSteers) > 0 {
		rows = append(rows, p.renderHeader("Messages to be submitted after next tool call", mutedFG, bg))
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
	return SurfaceFromString(strings.Join(rows, "\n"))
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

func (p PendingInputPreview) renderHeader(text string, fg, bg lipgloss.Color) string {
	width := maxInt(1, p.Width)
	prefix := "• "
	available := maxInt(1, width-ansi.StringWidth(prefix))
	label := ansi.Truncate(text, available, "")
	return renderButtonSegment(prefix, fg, bg, false) +
		renderButtonSegment(label, fg, bg, false) +
		renderButtonSegment(strings.Repeat(" ", maxInt(0, width-ansi.StringWidth(prefix)-ansi.StringWidth(label))), fg, bg, false)
}

func (p PendingInputPreview) renderPreviewRows(messages []string, fg, bg lipgloss.Color, italic bool) []string {
	rows := make([]string, 0, len(messages))
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

func (p PendingInputPreview) renderBlank(bg lipgloss.Color) string {
	return renderButtonSegment(strings.Repeat(" ", maxInt(1, p.Width)), bg, bg, false)
}

func renderPendingPreviewLine(prefix, text string, width int, fg, bg lipgloss.Color, italic bool) string {
	width = maxInt(1, width)
	prefix = ansi.Truncate(prefix, width, "")
	available := maxInt(0, width-ansi.StringWidth(prefix))
	text = ansi.Truncate(text, available, "")
	line := renderButtonSegment(prefix, fg, bg, false)
	if italic {
		line += lipgloss.NewStyle().Foreground(fg).Background(bg).Italic(true).Render(text)
	} else {
		line += renderButtonSegment(text, fg, bg, false)
	}
	remaining := maxInt(0, width-ansi.StringWidth(prefix)-ansi.StringWidth(text))
	if remaining > 0 {
		line += renderButtonSegment(strings.Repeat(" ", remaining), fg, bg, false)
	}
	return line
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
		if ansi.StringWidth(remaining) <= width {
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
