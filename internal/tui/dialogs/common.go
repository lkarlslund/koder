package dialogs

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/lkarlslund/koder/internal/ui"
)

func dialogRenderWidth(bounds ui.Rect, fallback int) int {
	width := bounds.W
	if width <= 0 {
		width = fallback
	}
	if width <= 0 {
		width = 80
	}
	return width
}

func staticBlock(text string) ui.Element {
	return ui.Static{Content: strings.TrimRight(text, "\n")}
}

func linesBlock(lines ...string) ui.Element {
	children := make([]ui.Child, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			children = append(children, ui.Fixed(ui.Spacer{H: 1}))
			continue
		}
		children = append(children, ui.Fixed(ui.Static{Content: line}))
	}
	return ui.Column{Children: children}
}

type pickerDialogFocus int

const (
	pickerDialogFocusList pickerDialogFocus = iota
	pickerDialogFocusButtons
)

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func truncateText(input string, width int) string {
	if width <= 0 {
		return ""
	}
	plain := compactInlineText(input)
	if ui.PlainWidth(plain) <= width {
		return plain
	}
	if width == 1 {
		return "…"
	}
	return ui.PlainTruncate(plain, width-1, "") + "…"
}

func compactInlineText(input string) string {
	fields := strings.Fields(strings.TrimSpace(input))
	return strings.Join(fields, " ")
}

func firstNonEmptyColor(values ...lipgloss.Color) lipgloss.Color {
	for _, value := range values {
		if strings.TrimSpace(string(value)) != "" {
			return value
		}
	}
	return ""
}

func wrapPlain(input string, width int) string {
	if width <= 0 {
		return strings.TrimSpace(input)
	}
	input = strings.ReplaceAll(strings.TrimSpace(input), "\r\n", "\n")
	if input == "" {
		return ""
	}
	var out []string
	for _, line := range strings.Split(input, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			if len(out) == 0 || out[len(out)-1] != "" {
				out = append(out, "")
			}
			continue
		}
		out = append(out, wrapWords(line, width)...)
	}
	return strings.Join(out, "\n")
}

func wrapWords(line string, width int) []string {
	if ui.PlainWidth(line) <= width {
		return []string{line}
	}
	var lines []string
	remaining := line
	for remaining != "" {
		if ui.PlainWidth(remaining) <= width {
			lines = append(lines, remaining)
			break
		}
		segment := ui.PlainTruncate(remaining, width, "")
		if idx := strings.LastIndex(segment, " "); idx > 0 {
			segment = strings.TrimRight(segment[:idx], " ")
		}
		if segment == "" {
			segment = ui.PlainTruncate(remaining, width, "")
		}
		lines = append(lines, segment)
		remaining = strings.TrimSpace(strings.TrimPrefix(remaining, segment))
	}
	return lines
}
