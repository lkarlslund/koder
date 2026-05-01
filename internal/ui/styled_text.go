package ui

import (
	"strings"
	"unicode"
)

// StyledSpan is a run of text with one style and optional interactive control.
type StyledSpan struct {
	Text      string    // Text content for this run.
	Style     CellStyle // Style merged with the base style at paint time.
	ControlID string    // Optional control ID covering this span.
	Enabled   bool      // Whether the control ID should participate in hit-testing.
}

// AppendStyledSpan appends text while merging adjacent spans with identical style.
func AppendStyledSpan(dst []StyledSpan, text string, style CellStyle) []StyledSpan {
	if text == "" {
		return dst
	}
	if len(dst) > 0 && dst[len(dst)-1].Style.equal(style) && dst[len(dst)-1].ControlID == "" && !dst[len(dst)-1].Enabled {
		dst[len(dst)-1].Text += text
		return dst
	}
	return append(dst, StyledSpan{Text: text, Style: style})
}

// AppendInteractiveStyledSpan appends text with a hit-test control.
func AppendInteractiveStyledSpan(dst []StyledSpan, text string, style CellStyle, controlID string, enabled bool) []StyledSpan {
	if text == "" {
		return dst
	}
	span := StyledSpan{Text: text, Style: style, ControlID: controlID, Enabled: enabled}
	return appendStyledText(dst, span)
}

// PlainStyledText returns spans with style and control metadata removed.
func PlainStyledText(spans []StyledSpan) string {
	var b strings.Builder
	for _, span := range spans {
		b.WriteString(span.Text)
	}
	return b.String()
}

// StyledTextWidth returns the terminal display width of spans.
func StyledTextWidth(spans []StyledSpan) int {
	return PlainWidth(PlainStyledText(spans))
}

// LayoutStyledText wraps spans and paints them into a Surface.
func LayoutStyledText(spans []StyledSpan, width int, base CellStyle) Surface {
	lines := WrapStyledText(spans, width)
	maxWidth := 0
	for _, line := range lines {
		maxWidth = max(maxWidth, StyledTextWidth(line))
	}
	s := BlankSurface(maxWidth, len(lines))
	for y, line := range lines {
		s.WriteStyledSpans(0, y, line, base)
	}
	return s
}

// SplitStyledLines splits spans on newline characters while preserving style.
func SplitStyledLines(spans []StyledSpan) [][]StyledSpan {
	lines := make([][]StyledSpan, 1)
	for _, span := range spans {
		if span.Text == "" {
			continue
		}
		parts := strings.Split(span.Text, "\n")
		for idx, part := range parts {
			if part != "" {
				lines[len(lines)-1] = appendStyledText(lines[len(lines)-1], StyledSpan{
					Text:      part,
					Style:     span.Style,
					ControlID: span.ControlID,
					Enabled:   span.Enabled,
				})
			}
			if idx < len(parts)-1 {
				lines = append(lines, nil)
			}
		}
	}
	return lines
}

// WrapStyledText wraps spans to width without dropping style metadata.
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
			if len(current.spans) > 0 && (current.whitespace != isSpace || !styledSpanMetaEqual(current.spans[len(current.spans)-1], span)) {
				flush()
			}
			current.whitespace = isSpace
			current.spans = appendStyledText(current.spans, StyledSpan{
				Text:      string(r),
				Style:     span.Style,
				ControlID: span.ControlID,
				Enabled:   span.Enabled,
			})
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
			current = appendStyledText(current, StyledSpan{
				Text:      grapheme,
				Style:     span.Style,
				ControlID: span.ControlID,
				Enabled:   span.Enabled,
			})
			used += graphemeWidth
		}
	}
	flush()
	if len(parts) == 0 {
		return [][]StyledSpan{{}}
	}
	return parts
}

func appendStyledText(dst []StyledSpan, span StyledSpan) []StyledSpan {
	if span.Text == "" {
		return dst
	}
	if len(dst) > 0 && styledSpanMetaEqual(dst[len(dst)-1], span) {
		dst[len(dst)-1].Text += span.Text
		return dst
	}
	return append(dst, span)
}

func styledSpanMetaEqual(a, b StyledSpan) bool {
	return a.Style.equal(b.Style) && a.ControlID == b.ControlID && a.Enabled == b.Enabled
}
