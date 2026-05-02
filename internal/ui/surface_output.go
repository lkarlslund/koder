package ui

import "strings"

// This file is the ANSI output boundary for Surface serialization.
// Widgets and dialogs should operate on plain text and cells only.

func applyCellStyleWithProfile(profile ColorProfile, style CellStyle, text string) string {
	if style.isZero() || text == "" {
		return text
	}
	params := make([]string, 0, 10)
	if style.Bold() {
		params = append(params, "1")
	}
	if style.Italic() {
		params = append(params, "3")
	}
	if style.Underline() {
		params = append(params, "4")
	}
	if style.Strikethrough() {
		params = append(params, "9")
	}
	params = appendTerminalColorSGR(params, profile, true, style.FG.R(), style.FG.G(), style.FG.B(), style.FG.Valid())
	params = appendTerminalColorSGR(params, profile, false, style.BG.R(), style.BG.G(), style.BG.B(), style.BG.Valid())
	if len(params) == 0 {
		return text
	}
	return "\x1b[" + strings.Join(params, ";") + "m" + text + "\x1b[0m"
}

// RenderStyledTextANSI renders spans with true-color ANSI SGR sequences.
func RenderStyledTextANSI(spans []StyledSpan) string {
	return RenderStyledTextANSIWithProfile(spans, ColorProfileTrueColor)
}

// RenderStyledTextANSIWithProfile renders spans using profile's color depth.
func RenderStyledTextANSIWithProfile(spans []StyledSpan, profile ColorProfile) string {
	var b strings.Builder
	for _, span := range spans {
		b.WriteString(applyCellStyleWithProfile(profile, span.Style, span.Text))
	}
	return b.String()
}
