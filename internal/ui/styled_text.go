package ui

import (
	"strings"
	"unicode"
)

type StyledSpan struct {
	Text  string
	Style CellStyle
}

func AppendStyledSpan(dst []StyledSpan, text string, style CellStyle) []StyledSpan {
	if text == "" {
		return dst
	}
	if len(dst) > 0 && dst[len(dst)-1].Style.equal(style) {
		dst[len(dst)-1].Text += text
		return dst
	}
	return append(dst, StyledSpan{Text: text, Style: style})
}

func PlainStyledText(spans []StyledSpan) string {
	var b strings.Builder
	for _, span := range spans {
		b.WriteString(span.Text)
	}
	return b.String()
}

func StyledTextWidth(spans []StyledSpan) int {
	return PlainWidth(PlainStyledText(spans))
}

func SplitStyledLines(spans []StyledSpan) [][]StyledSpan {
	lines := make([][]StyledSpan, 1)
	for _, span := range spans {
		if span.Text == "" {
			continue
		}
		parts := strings.Split(span.Text, "\n")
		for idx, part := range parts {
			if part != "" {
				lines[len(lines)-1] = AppendStyledSpan(lines[len(lines)-1], part, span.Style)
			}
			if idx < len(parts)-1 {
				lines = append(lines, nil)
			}
		}
	}
	return lines
}

func WrapStyledText(spans []StyledSpan, width int) [][]StyledSpan {
	if width <= 0 {
		return SplitStyledLines(spans)
	}
	rawLines := SplitStyledLines(spans)
	wrapped := make([][]StyledSpan, 0, len(rawLines))
	for _, line := range rawLines {
		if StyledTextWidth(line) == 0 {
			wrapped = append(wrapped, nil)
			continue
		}
		wrapped = append(wrapped, wrapStyledLine(line, width)...)
	}
	return wrapped
}

type styledToken struct {
	spans      []StyledSpan
	width      int
	whitespace bool
}

func wrapStyledLine(spans []StyledSpan, width int) [][]StyledSpan {
	if width <= 0 || StyledTextWidth(spans) <= width {
		return [][]StyledSpan{spans}
	}
	tokens := tokenizeStyledLine(spans)
	lines := make([][]StyledSpan, 0, len(tokens))
	var current []StyledSpan
	currentWidth := 0
	appendWord := func(word styledToken) {
		if currentWidth == 0 {
			current = append(current, word.spans...)
			currentWidth = word.width
			return
		}
		current = AppendStyledSpan(current, " ", CellStyle{})
		current = append(current, word.spans...)
		currentWidth += 1 + word.width
	}
	for _, token := range tokens {
		if token.whitespace {
			continue
		}
		if token.width > width {
			if currentWidth > 0 {
				lines = append(lines, current)
				current = nil
				currentWidth = 0
			}
			parts := splitStyledToken(token, width)
			lines = append(lines, parts[:len(parts)-1]...)
			current = parts[len(parts)-1]
			currentWidth = StyledTextWidth(current)
			continue
		}
		if currentWidth == 0 {
			appendWord(token)
			continue
		}
		if currentWidth+1+token.width <= width {
			appendWord(token)
			continue
		}
		lines = append(lines, current)
		current = nil
		currentWidth = 0
		appendWord(token)
	}
	if len(current) > 0 {
		lines = append(lines, current)
	}
	if len(lines) == 0 {
		return [][]StyledSpan{{}}
	}
	return lines
}

func tokenizeStyledLine(spans []StyledSpan) []styledToken {
	var tokens []styledToken
	var current styledToken
	flush := func() {
		if len(current.spans) == 0 {
			return
		}
		tokens = append(tokens, current)
		current = styledToken{}
	}
	for _, span := range spans {
		for _, r := range span.Text {
			isSpace := unicode.IsSpace(r)
			width := PlainWidth(string(r))
			if width <= 0 {
				continue
			}
			if len(current.spans) > 0 && current.whitespace != isSpace {
				flush()
			}
			current.whitespace = isSpace
			current.spans = AppendStyledSpan(current.spans, string(r), span.Style)
			current.width += width
		}
	}
	flush()
	return tokens
}

func splitStyledToken(token styledToken, width int) [][]StyledSpan {
	if width <= 0 || token.width <= width {
		return [][]StyledSpan{token.spans}
	}
	var (
		parts   [][]StyledSpan
		current []StyledSpan
		used    int
	)
	flush := func() {
		if len(current) == 0 {
			return
		}
		parts = append(parts, current)
		current = nil
		used = 0
	}
	for _, span := range token.spans {
		for _, r := range span.Text {
			grapheme := string(r)
			graphemeWidth := PlainWidth(grapheme)
			if graphemeWidth <= 0 {
				continue
			}
			if used > 0 && used+graphemeWidth > width {
				flush()
			}
			current = AppendStyledSpan(current, grapheme, span.Style)
			used += graphemeWidth
		}
	}
	flush()
	if len(parts) == 0 {
		return [][]StyledSpan{{}}
	}
	return parts
}
