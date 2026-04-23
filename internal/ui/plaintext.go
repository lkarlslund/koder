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
	words := strings.Fields(line)
	if len(words) == 0 {
		return []string{""}
	}
	lines := make([]string, 0, len(words))
	current := words[0]
	currentWidth := PlainWidth(current)
	if currentWidth > width {
		parts := splitLongWordPlain(current, width)
		lines = append(lines, parts[:len(parts)-1]...)
		current = parts[len(parts)-1]
		currentWidth = PlainWidth(current)
	}
	for _, word := range words[1:] {
		wordWidth := PlainWidth(word)
		if wordWidth > width {
			if current != "" {
				lines = append(lines, current)
				current = ""
				currentWidth = 0
			}
			parts := splitLongWordPlain(word, width)
			lines = append(lines, parts[:len(parts)-1]...)
			current = parts[len(parts)-1]
			currentWidth = PlainWidth(current)
			continue
		}
		if current == "" {
			current = word
			currentWidth = wordWidth
			continue
		}
		if currentWidth+1+wordWidth <= width {
			current += " " + word
			currentWidth += 1 + wordWidth
			continue
		}
		lines = append(lines, current)
		current = word
		currentWidth = wordWidth
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}

func splitLongWordPlain(word string, width int) []string {
	if width <= 0 || word == "" {
		return []string{word}
	}
	parts := make([]string, 0, (len(word)/max(1, width))+1)
	var (
		b    strings.Builder
		used int
	)
	for _, r := range word {
		rw := runewidth.RuneWidth(r)
		if rw <= 0 {
			continue
		}
		if used > 0 && used+rw > width {
			parts = append(parts, b.String())
			b.Reset()
			used = 0
		}
		b.WriteRune(r)
		used += rw
	}
	if b.Len() > 0 {
		parts = append(parts, b.String())
	}
	if len(parts) == 0 {
		return []string{""}
	}
	return parts
}
