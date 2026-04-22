package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/lkarlslund/koder/internal/theme"
)

type ComposerProps struct {
	Palette       theme.Palette
	Width         int
	HalfBlocks    bool
	PromptGlyph   string
	Value         string
	Placeholder   string
	ContentBefore string
	ContentCursor string
	ContentAfter  string
	CursorVisible bool
}

type AttachmentItem struct {
	Label string
}

func RenderComposer(props ComposerProps) string {
	width := maxInt(1, props.Width)
	prompt := props.PromptGlyph + " "
	promptWidth := ansi.StringWidth(prompt)
	if promptWidth >= width {
		prompt = ansi.Truncate(prompt, maxInt(1, width-1), "")
		promptWidth = ansi.StringWidth(prompt)
	}
	contentWidth := maxInt(0, width-promptWidth)
	promptStyle := lipgloss.NewStyle().
		Background(props.Palette.UserTextBackground).
		Foreground(props.Palette.UserAccentBar)
	contentStyle := lipgloss.NewStyle().
		Background(props.Palette.UserTextBackground).
		Foreground(props.Palette.UserTextForeground)

	renderBlankLine := func() string {
		return renderComposerLine(prompt, promptStyle, "", "", "", contentWidth, false, props.Palette.UserTextForeground, props.Palette.UserTextBackground)
	}

	middle := renderComposerLine(
		prompt,
		promptStyle,
		props.ContentBefore,
		props.ContentCursor,
		props.ContentAfter,
		contentWidth,
		props.CursorVisible,
		props.Palette.UserTextForeground,
		props.Palette.UserTextBackground,
	)
	if strings.TrimSpace(props.Value) == "" {
		middle = RenderComposerPlaceholderLine(promptStyle, contentStyle, prompt, contentWidth, props.Placeholder, props.ContentCursor, props.CursorVisible, props.Palette)
	}

	if props.HalfBlocks {
		return lipgloss.JoinVertical(lipgloss.Left,
			RenderHalfBlockLine(width, "▄", props.Palette),
			middle,
			RenderHalfBlockLine(width, "▀", props.Palette),
		)
	}
	return lipgloss.JoinVertical(lipgloss.Left, renderBlankLine(), middle, renderBlankLine())
}

func RenderComposerPlaceholderLine(promptStyle, contentStyle lipgloss.Style, prompt string, contentWidth int, placeholder string, cursorChar string, cursorVisible bool, palette theme.Palette) string {
	placeholder = ansi.Truncate(placeholder, contentWidth, "")
	if placeholder == "" {
		return renderComposerPlaceholder(prompt, promptStyle, "", cursorChar, "", contentWidth, cursorVisible, palette.UserTextForeground, palette.UserTextBackground, palette.ComposerMutedText)
	}
	runes := []rune(placeholder)
	cursor := string(runes[0])
	if strings.TrimSpace(cursorChar) != "" {
		cursor = cursorChar
	}
	rest := ""
	if len(runes) > 1 {
		rest = string(runes[1:])
	}
	return renderComposerPlaceholder(prompt, promptStyle, "", cursor, rest, contentWidth, cursorVisible, palette.UserTextForeground, palette.UserTextBackground, palette.ComposerMutedText)
}

func renderComposerLine(prompt string, promptStyle lipgloss.Style, before, cursor, after string, contentWidth int, cursorVisible bool, textFG, textBG lipgloss.Color) string {
	line := promptStyle.Render(prompt)
	if contentWidth <= 0 {
		return line
	}
	before = ansi.Truncate(before, contentWidth, "")
	cursor = ansi.Truncate(cursor, maxInt(1, contentWidth-ansi.StringWidth(before)), "")
	remaining := maxInt(0, contentWidth-ansi.StringWidth(before)-ansi.StringWidth(cursor))
	after = ansi.Truncate(after, remaining, "")
	remaining = maxInt(0, contentWidth-ansi.StringWidth(before)-ansi.StringWidth(cursor)-ansi.StringWidth(after))

	line += renderButtonSegment(before, textFG, textBG, false)
	if cursorVisible {
		line += renderButtonSegment(cursor, textBG, textFG, false)
	} else {
		line += renderButtonSegment(cursor, textFG, textBG, false)
	}
	line += renderButtonSegment(after, textFG, textBG, false)
	if remaining > 0 {
		line += renderButtonSegment(strings.Repeat(" ", remaining), textFG, textBG, false)
	}
	return line
}

func renderComposerPlaceholder(prompt string, promptStyle lipgloss.Style, before, cursor, after string, contentWidth int, cursorVisible bool, textFG, textBG, muted lipgloss.Color) string {
	line := promptStyle.Render(prompt)
	if contentWidth <= 0 {
		return line
	}
	before = ansi.Truncate(before, contentWidth, "")
	cursor = ansi.Truncate(cursor, maxInt(1, contentWidth-ansi.StringWidth(before)), "")
	remaining := maxInt(0, contentWidth-ansi.StringWidth(before)-ansi.StringWidth(cursor))
	after = ansi.Truncate(after, remaining, "")
	remaining = maxInt(0, contentWidth-ansi.StringWidth(before)-ansi.StringWidth(cursor)-ansi.StringWidth(after))

	line += renderButtonSegment(before, textFG, textBG, false)
	if cursorVisible {
		line += renderButtonSegment(cursor, textBG, textFG, false)
	} else {
		line += renderButtonSegment(cursor, muted, textBG, false)
	}
	line += renderButtonSegment(after, muted, textBG, false)
	if remaining > 0 {
		line += renderButtonSegment(strings.Repeat(" ", remaining), textFG, textBG, false)
	}
	return line
}

func RenderHalfBlockLine(width int, char string, palette theme.Palette) string {
	if width <= 0 {
		return ""
	}
	bar := lipgloss.NewStyle().
		Foreground(palette.UserAccentBar).
		Render(char)
	fill := lipgloss.NewStyle().
		Width(maxInt(0, width-1)).
		Foreground(palette.UserTextBackground).
		Render(strings.Repeat(char, maxInt(1, width-1)))
	return bar + fill
}

func RenderAttachmentRows(items []AttachmentItem, width int, palette theme.Palette) string {
	if len(items) == 0 || width <= 0 {
		return ""
	}
	style := lipgloss.NewStyle().
		Width(width).
		Foreground(palette.MarkdownText).
		Background(palette.UserTextBackground).
		Padding(0, 1)
	rows := make([]string, 0, len(items))
	for _, item := range items {
		rows = append(rows, style.Render(ansi.Truncate(item.Label, maxInt(1, width-2), "")))
	}
	return strings.Join(rows, "\n")
}
