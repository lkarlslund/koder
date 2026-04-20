package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/lkarlslund/koder/internal/theme"
)

type UserMessageProps struct {
	Palette     theme.Palette
	Body        string
	Stamp       string
	Width       int
	HalfBlocks  bool
	PromptGlyph string
}

func RenderActivityIndicator(indicator string, palette theme.Palette) string {
	if strings.TrimSpace(indicator) == "" {
		return ""
	}
	return lipgloss.NewStyle().
		Foreground(palette.ActivityText).
		Bold(true).
		Padding(0, 1).
		Render(indicator)
}

func RenderUserMessage(props UserMessageProps) string {
	baseLines := []string{""}
	content := strings.TrimSpace(props.Body)
	if content != "" {
		baseLines = append(baseLines, strings.Split(content, "\n")...)
	}
	if props.Stamp != "" {
		baseLines = append(baseLines, props.Stamp)
	}
	baseLines = append(baseLines, "")

	width := props.Width
	if width <= 0 {
		width = UserMessageWidth(baseLines)
	}
	bar := props.PromptGlyph + " "
	contentWidth := maxInt(1, width-lipgloss.Width(bar))
	innerWidth := maxInt(1, contentWidth-2)
	barStyle := lipgloss.NewStyle().
		Background(props.Palette.UserTextBackground).
		Foreground(props.Palette.UserAccentBar)
	contentStyle := lipgloss.NewStyle().
		Background(props.Palette.UserTextBackground).
		Foreground(props.Palette.UserTextForeground).
		Width(contentWidth).
		Padding(0, 1)
	timestampStyle := contentStyle.Foreground(props.Palette.UserTimestampForeground)

	lines := []string{}
	if content != "" {
		for _, line := range strings.Split(content, "\n") {
			lines = append(lines, WrapUserMessageLine(line, innerWidth)...)
		}
	}
	if props.Stamp != "" {
		lines = append(lines, WrapUserMessageLine(props.Stamp, innerWidth)...)
	}

	rendered := make([]string, 0, len(lines))
	stampStart := -1
	if props.Stamp != "" {
		stampStart = len(lines) - len(WrapUserMessageLine(props.Stamp, innerWidth))
	}
	if props.HalfBlocks {
		rendered = append(rendered, RenderHalfBlockLine(width, "▄", props.Palette))
	} else {
		rendered = append(rendered, barStyle.Render(bar)+contentStyle.Render(""))
	}
	for idx, line := range lines {
		prefix := barStyle.Render(bar)
		if stampStart >= 0 && idx >= stampStart {
			rendered = append(rendered, prefix+timestampStyle.Render(line))
			continue
		}
		rendered = append(rendered, prefix+contentStyle.Render(line))
	}
	if props.HalfBlocks {
		rendered = append(rendered, RenderHalfBlockLine(width, "▀", props.Palette))
	} else {
		rendered = append(rendered, barStyle.Render(bar)+contentStyle.Render(""))
	}
	return strings.Join(rendered, "\n")
}

func WrapUserMessageLine(line string, width int) []string {
	if width <= 0 {
		return []string{""}
	}
	if strings.TrimSpace(line) == "" {
		return []string{""}
	}
	wrapped := ansi.Wordwrap(line, width, "")
	lines := strings.Split(wrapped, "\n")
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func UserMessageWidth(lines []string) int {
	width := lipgloss.Width("┃ ") + 2
	for _, line := range lines {
		width = maxInt(width, lipgloss.Width(line)+lipgloss.Width("┃ ")+2)
	}
	return width
}

func RenderAssistantMessage(body, stamp string, palette theme.Palette) string {
	return RenderAssistantMessageWidth(body, stamp, 0, palette)
}

func RenderAssistantMessageWidth(body, stamp string, width int, palette theme.Palette) string {
	body = strings.TrimSpace(body)
	body = WrapStyledBlock(body, width)
	bodyStyle := lipgloss.NewStyle().Foreground(palette.MarkdownText)
	if body != "" {
		lines := strings.Split(body, "\n")
		rendered := make([]string, 0, len(lines))
		for _, line := range lines {
			rendered = append(rendered, bodyStyle.Render(line))
		}
		body = strings.Join(rendered, "\n")
	}
	if stamp == "" {
		return body
	}
	header := lipgloss.NewStyle().
		Foreground(palette.AssistantTimestampText).
		Render(stamp)
	return header + "\n" + body
}

func RenderReasoningBlock(input string, palette theme.Palette) string {
	return RenderReasoningBlockWidth(input, 0, palette)
}

func RenderReasoningBlockWidth(input string, width int, palette theme.Palette) string {
	content := strings.TrimSpace(input)
	if content == "" {
		return ""
	}
	content = WrapStyledBlock(content, width)
	lineStyle := lipgloss.NewStyle().
		Background(palette.ReasoningBackground).
		Foreground(palette.ReasoningText)
	lines := append([]string{""}, strings.Split(content, "\n")...)
	rendered := make([]string, 0, len(lines))
	for _, line := range lines {
		rendered = append(rendered, lineStyle.Render(line))
	}
	return strings.Join(rendered, "\n")
}

func WorkingIndicatorLine(indicator string) string {
	if strings.TrimSpace(indicator) == "" {
		return ""
	}
	return fmt.Sprintf("%s  Working ...", indicator)
}

func WrapStyledBlock(input string, width int) string {
	if width <= 0 {
		return input
	}
	var wrapped []string
	for _, line := range strings.Split(input, "\n") {
		if strings.TrimSpace(line) == "" {
			wrapped = append(wrapped, "")
			continue
		}
		chunks := strings.Split(ansi.Wordwrap(line, width, ""), "\n")
		wrapped = append(wrapped, chunks...)
	}
	return strings.Join(wrapped, "\n")
}
