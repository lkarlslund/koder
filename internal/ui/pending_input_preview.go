package ui

import (
	"strings"

	"github.com/lkarlslund/koder/internal/theme"
)

type PendingInputRow struct {
	Badge    string
	Text     string
	Held     bool
	Selected bool
}

type PendingInputPreview struct {
	PassiveNode
	Width       int
	Items       []PendingInputRow
	EditingMode bool
}

const pendingInputPreviewLineLimit = 3

func (p PendingInputPreview) render(palette theme.Palette) Surface {
	if p.Width <= 0 || len(p.Items) == 0 {
		return Surface{}
	}
	mutedFG := palette.ComposerMutedText
	selectedFG := palette.SelectionForeground
	bg := palette.UserTextBackground

	rows := make([]Surface, 0, len(p.Items)+1)
	header := "Queued inputs"
	if p.EditingMode {
		header = "Queued inputs • edit mode"
	}
	rows = append(rows, p.renderHeader(header, mutedFG, bg))
	for _, item := range p.Items {
		if item.Held && strings.TrimSpace(item.Badge) != "HELD" {
			item.Badge = "HELD " + strings.TrimSpace(item.Badge)
		}
		fg := mutedFG
		if item.Selected {
			fg = selectedFG
		}
		rows = append(rows, p.renderPreviewRows(item, fg, bg)...)
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
		Width:       width,
		Items:       p.Items,
		EditingMode: p.EditingMode,
	}.render(ctx.Palette).Size())
}

func (p PendingInputPreview) Paint(ctx *Context, canvas Canvas) {
	width := canvas.Width()
	height := canvas.Height()
	if width <= 0 || height <= 0 || len(p.Items) == 0 {
		return
	}
	mutedFG := ctx.Palette.ComposerMutedText
	selectedFG := ctx.Palette.SelectionForeground
	bg := ctx.Palette.UserTextBackground
	y := 0
	header := "Queued inputs"
	if p.EditingMode {
		header = "Queued inputs • edit mode"
	}
	paintLine := func(prefix, text string, fg CellColor, italic bool) {
		if y >= height {
			return
		}
		baseStyle := CellStyle{FG: cellColor(fg), BG: cellColor(bg)}
		textStyle := baseStyle
		if italic {
			textStyle = textStyle.WithItalic(true)
		}
		canvas.Fill(Rect{Y: y, W: width, H: 1}, baseStyle)
		prefix = PlainTruncate(prefix, width, "")
		canvas.WriteText(0, y, prefix, baseStyle)
		if available := maxInt(0, width-PlainWidth(prefix)); available > 0 {
			canvas.WriteText(PlainWidth(prefix), y, PlainTruncate(text, available, ""), textStyle)
		}
		y++
	}
	paintLine("• ", header, mutedFG, false)
	for _, item := range p.Items {
		if y >= height {
			break
		}
		if item.Held && strings.TrimSpace(item.Badge) != "HELD" {
			item.Badge = "HELD " + strings.TrimSpace(item.Badge)
		}
		fg := mutedFG
		if item.Selected {
			fg = selectedFG
		}
		label := strings.TrimSpace(item.Badge)
		if label == "" {
			label = "ITEM"
		}
		lines := strings.Split(strings.ReplaceAll(item.Text, "\r\n", "\n"), "\n")
		rendered := 0
		limitWidth := maxInt(1, width-8-PlainWidth(label))
		for _, line := range lines {
			for _, wrapped := range wrapPreviewLine(line, limitWidth) {
				prefix := "  " + label + " "
				if rendered > 0 {
					prefix = strings.Repeat(" ", maxInt(2, PlainWidth(prefix)))
				}
				paintLine(prefix, wrapped, fg, true)
				rendered++
				if rendered >= pendingInputPreviewLineLimit || y >= height {
					break
				}
			}
			if rendered >= pendingInputPreviewLineLimit || y >= height {
				break
			}
		}
		if y < height && countWrappedPreviewLines(lines, limitWidth) > pendingInputPreviewLineLimit {
			paintLine(strings.Repeat(" ", maxInt(2, PlainWidth("  "+label+" "))), "…", fg, true)
		}
	}
}

func (p PendingInputPreview) renderHeader(text string, fg, bg CellColor) Surface {
	width := maxInt(1, p.Width)
	prefix := "• "
	available := maxInt(1, width-PlainWidth(prefix))
	label := PlainTruncate(text, available, "")
	surface := BlankSurface(width, 1)
	style := CellStyle{FG: cellColor(fg), BG: cellColor(bg)}
	for x := 0; x < width; x++ {
		surface.setCell(x, 0, blankCell(style))
	}
	surface.WriteText(0, 0, prefix+label, style)
	return surface
}

func (p PendingInputPreview) renderPreviewRows(item PendingInputRow, fg, bg CellColor) []Surface {
	rows := make([]Surface, 0, pendingInputPreviewLineLimit)
	label := strings.TrimSpace(item.Badge)
	if label == "" {
		label = "ITEM"
	}
	lines := strings.Split(strings.ReplaceAll(item.Text, "\r\n", "\n"), "\n")
	rendered := 0
	for _, line := range lines {
		for _, wrapped := range wrapPreviewLine(line, maxInt(1, p.Width-8-PlainWidth(label))) {
			prefix := "  " + label + " "
			if rendered > 0 {
				prefix = strings.Repeat(" ", maxInt(2, PlainWidth(prefix)))
			}
			rows = append(rows, renderPendingPreviewLine(prefix, wrapped, p.Width, fg, bg, true))
			rendered++
			if rendered >= pendingInputPreviewLineLimit {
				break
			}
		}
		if rendered >= pendingInputPreviewLineLimit {
			break
		}
	}
	if countWrappedPreviewLines(lines, maxInt(1, p.Width-8-PlainWidth(label))) > pendingInputPreviewLineLimit {
		rows = append(rows, renderPendingPreviewLine(strings.Repeat(" ", maxInt(2, PlainWidth("  "+label+" "))), "…", p.Width, fg, bg, true))
	}
	return rows
}

func renderPendingPreviewLine(prefix, text string, width int, fg, bg CellColor, italic bool) Surface {
	width = maxInt(1, width)
	prefix = PlainTruncate(prefix, width, "")
	available := maxInt(0, width-PlainWidth(prefix))
	text = PlainTruncate(text, available, "")
	surface := BlankSurface(width, 1)
	baseStyle := CellStyle{FG: cellColor(fg), BG: cellColor(bg)}
	textStyle := baseStyle
	if italic {
		textStyle = textStyle.WithItalic(true)
	}
	for x := 0; x < width; x++ {
		surface.setCell(x, 0, blankCell(baseStyle))
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
