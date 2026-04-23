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

type Composer struct {
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

func NewComposer(props ComposerProps) Composer {
	return Composer(props)
}

func (c Composer) Measure(_ *Context, constraints Constraints) Size {
	return constraints.Clamp(c.render().Size())
}

func (c Composer) Render(_ *Context, bounds Rect) Surface {
	return c.render().normalize(bounds.W, bounds.H)
}

func (c Composer) render() Surface {
	width := maxInt(1, c.Width)
	prompt := c.PromptGlyph + " "
	promptWidth := ansi.StringWidth(prompt)
	if promptWidth >= width {
		prompt = ansi.Truncate(prompt, maxInt(1, width-1), "")
		promptWidth = ansi.StringWidth(prompt)
	}
	contentWidth := maxInt(0, width-promptWidth)
	promptStyle := lipgloss.NewStyle().
		Background(c.Palette.UserTextBackground).
		Foreground(c.Palette.UserAccentBar)
	contentStyle := lipgloss.NewStyle().
		Background(c.Palette.UserTextBackground).
		Foreground(c.Palette.UserTextForeground)

	renderBlankLine := func() Surface {
		return c.renderLineSurface(prompt, promptStyle, "", "", "", contentWidth, false, c.Palette.UserTextForeground, c.Palette.UserTextBackground)
	}

	middle := c.renderLineSurface(
		prompt,
		promptStyle,
		c.ContentBefore,
		c.ContentCursor,
		c.ContentAfter,
		contentWidth,
		c.CursorVisible,
		c.Palette.UserTextForeground,
		c.Palette.UserTextBackground,
	)
	if strings.TrimSpace(c.Value) == "" {
		middle = c.renderPlaceholderSurface(promptStyle, contentStyle, prompt, contentWidth, c.Placeholder, c.ContentCursor)
	}

	if c.HalfBlocks {
		return Surface{lines: []string{
			c.HalfBlockLine("▄"),
			middle.String(),
			c.HalfBlockLine("▀"),
		}}
	}
	return Surface{lines: []string{
		renderBlankLine().String(),
		middle.String(),
		renderBlankLine().String(),
	}}
}

func (c Composer) renderPlaceholderLine(promptStyle, contentStyle lipgloss.Style, prompt string, contentWidth int, placeholder string, cursorChar string) string {
	return c.renderPlaceholderSurface(promptStyle, contentStyle, prompt, contentWidth, placeholder, cursorChar).String()
}

func (c Composer) renderPlaceholderSurface(promptStyle, contentStyle lipgloss.Style, prompt string, contentWidth int, placeholder string, cursorChar string) Surface {
	placeholder = ansi.Truncate(placeholder, contentWidth, "")
	if placeholder == "" {
		return c.renderPlaceholder(prompt, promptStyle, "", cursorChar, "", contentWidth, c.CursorVisible, c.Palette.UserTextForeground, c.Palette.UserTextBackground, c.Palette.ComposerMutedText)
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
	return c.renderPlaceholder(prompt, promptStyle, "", cursor, rest, contentWidth, c.CursorVisible, c.Palette.UserTextForeground, c.Palette.UserTextBackground, c.Palette.ComposerMutedText)
}

func (c Composer) renderLine(prompt string, promptStyle lipgloss.Style, before, cursor, after string, contentWidth int, cursorVisible bool, textFG, textBG lipgloss.Color) string {
	return c.renderLineSurface(prompt, promptStyle, before, cursor, after, contentWidth, cursorVisible, textFG, textBG).String()
}

func (c Composer) renderLineSurface(prompt string, promptStyle lipgloss.Style, before, cursor, after string, contentWidth int, cursorVisible bool, textFG, textBG lipgloss.Color) Surface {
	line := promptStyle.Render(prompt)
	if contentWidth <= 0 {
		return Surface{lines: []string{line}}
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
	return Surface{lines: []string{line}}
}

func (c Composer) renderPlaceholder(prompt string, promptStyle lipgloss.Style, before, cursor, after string, contentWidth int, cursorVisible bool, textFG, textBG, muted lipgloss.Color) Surface {
	line := promptStyle.Render(prompt)
	if contentWidth <= 0 {
		return Surface{lines: []string{line}}
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
	return Surface{lines: []string{line}}
}

func (c Composer) HalfBlockLine(char string) string {
	return renderHalfBlockLine(c.Width, char, c.Palette)
}

func renderHalfBlockLine(width int, char string, palette theme.Palette) string {
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

type AttachmentList struct {
	Items []AttachmentItem
	Width int
}

func (l AttachmentList) Measure(ctx *Context, constraints Constraints) Size {
	width := l.Width
	if width <= 0 {
		width = constraints.maxWidth()
	}
	return constraints.Clamp(AttachmentList{Items: l.Items, Width: width}.render(ctx.Palette).Size())
}

func (l AttachmentList) Render(ctx *Context, bounds Rect) Surface {
	width := l.Width
	if width <= 0 {
		width = bounds.W
	}
	return AttachmentList{Items: l.Items, Width: width}.render(ctx.Palette).normalize(bounds.W, bounds.H)
}

func (l AttachmentList) render(palette theme.Palette) Surface {
	if len(l.Items) == 0 || l.Width <= 0 {
		return Surface{}
	}
	style := lipgloss.NewStyle().
		Width(l.Width).
		Foreground(palette.MarkdownText).
		Background(palette.UserTextBackground).
		Padding(0, 1)
	rows := make([]string, 0, len(l.Items))
	for _, item := range l.Items {
		rows = append(rows, style.Render(ansi.Truncate(item.Label, maxInt(1, l.Width-2), "")))
	}
	return Surface{lines: rows}
}
