package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/lkarlslund/koder/internal/theme"
)

type ComposerProps struct {
	Palette          theme.Palette
	Width            int
	HalfBlocks       bool
	PromptGlyph      string
	View             string
	Value            string
	Placeholder      string
	CursorView       string
	MutedCursorStyle lipgloss.Style
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
		Width(contentWidth).
		Background(props.Palette.UserTextBackground).
		Foreground(props.Palette.UserTextForeground)

	renderBlankLine := func() string {
		return promptStyle.Render(prompt) + contentStyle.Render("")
	}

	middle := lipgloss.NewStyle().
		Width(width).
		Background(props.Palette.UserTextBackground).
		Foreground(props.Palette.UserTextForeground).
		Render(props.View)
	if strings.TrimSpace(props.Value) == "" {
		middle = RenderComposerPlaceholderLine(promptStyle, contentStyle, prompt, contentWidth, props.Placeholder, props.CursorView, props.MutedCursorStyle, props.Palette)
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

func RenderComposerPlaceholderLine(promptStyle, contentStyle lipgloss.Style, prompt string, contentWidth int, placeholder string, cursorView string, muted lipgloss.Style, palette theme.Palette) string {
	placeholder = ansi.Truncate(placeholder, contentWidth, "")
	if placeholder == "" {
		return promptStyle.Render(prompt) + contentStyle.Render(cursorView)
	}
	runes := []rune(placeholder)
	rest := ""
	if len(runes) > 1 {
		rest = muted.Render(string(runes[1:]))
	}
	return promptStyle.Render(prompt) + contentStyle.Render(cursorView+rest)
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
