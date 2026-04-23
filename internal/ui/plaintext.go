package ui

import (
	"strings"

	"github.com/mattn/go-runewidth"
)

func PlainWidth(input string) int {
	return runewidth.StringWidth(input)
}

func PlainTruncate(input string, width int, tail string) string {
	if width <= 0 {
		return ""
	}
	if PlainWidth(input) <= width {
		return input
	}
	tailWidth := PlainWidth(tail)
	if tailWidth >= width {
		return truncateRunes(tail, width)
	}
	remaining := width - tailWidth
	return truncateRunes(input, remaining) + tail
}

func PlainWordWrap(input string, width int) string {
	if width <= 0 {
		return strings.TrimSpace(input)
	}
	input = strings.ReplaceAll(strings.TrimSpace(input), "\r\n", "\n")
	if input == "" {
		return ""
	}
	var lines []string
	for _, line := range strings.Split(input, "\n") {
		if strings.TrimSpace(line) == "" {
			lines = append(lines, "")
			continue
		}
		lines = append(lines, wrapWordsPlain(line, width)...)
	}
	return strings.Join(lines, "\n")
}

func truncateRunes(input string, width int) string {
	if width <= 0 {
		return ""
	}
	var b strings.Builder
	current := 0
	for _, r := range input {
		rw := runewidth.RuneWidth(r)
		if rw <= 0 {
			continue
		}
		if current+rw > width {
			break
		}
		b.WriteRune(r)
		current += rw
	}
	return b.String()
}

func wrapWordsPlain(line string, width int) []string {
	if width <= 0 || PlainWidth(line) <= width {
		return []string{line}
	}
	var lines []string
	remaining := line
	for remaining != "" {
		if PlainWidth(remaining) <= width {
			lines = append(lines, remaining)
			break
		}
		segment := truncateRunes(remaining, width)
		if idx := strings.LastIndex(segment, " "); idx > 0 {
			segment = strings.TrimRight(segment[:idx], " ")
		}
		if segment == "" {
			segment = truncateRunes(remaining, width)
		}
		lines = append(lines, segment)
		remaining = strings.TrimSpace(strings.TrimPrefix(remaining, segment))
	}
	return lines
}
