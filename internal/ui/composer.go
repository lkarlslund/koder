package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

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

func (c Composer) Paint(_ *Context, canvas Canvas) {
	width := canvas.Width()
	height := canvas.Height()
	if width <= 0 || height <= 0 {
		return
	}
	prompt := c.PromptGlyph + " "
	promptWidth := PlainWidth(prompt)
	if promptWidth >= width {
		prompt = PlainTruncate(prompt, maxInt(1, width-1), "")
		promptWidth = PlainWidth(prompt)
	}
	contentWidth := maxInt(0, width-promptWidth)
	promptStyle := CellStyle{BG: cellColor(c.Palette.UserTextBackground), FG: cellColor(c.Palette.UserAccentBar)}
	contentStyle := CellStyle{FG: cellColor(c.Palette.UserTextForeground), BG: cellColor(c.Palette.UserTextBackground)}
	mutedStyle := CellStyle{FG: cellColor(c.Palette.ComposerMutedText), BG: cellColor(c.Palette.UserTextBackground)}

	paintHalfBlock := func(y int, char string) {
		if y < 0 || y >= height || width <= 0 {
			return
		}
		canvas.SetCell(0, y, newCell(GlyphFromString(char), 1, CellStyle{FG: cellColor(c.Palette.UserAccentBar)}))
		for x := 1; x < width; x++ {
			canvas.SetCell(x, y, newCell(GlyphFromString(char), 1, CellStyle{FG: cellColor(c.Palette.UserTextBackground)}))
		}
	}
	paintBlankLine := func(y int) {
		if y < 0 || y >= height {
			return
		}
		canvas.Fill(Rect{Y: y, W: width, H: 1}, contentStyle)
		canvas.WriteText(0, y, PlainTruncate(prompt, width, ""), promptStyle)
	}
	paintLine := func(y int, before, cursor, after string, beforeStyle, afterStyle CellStyle) {
		if y < 0 || y >= height {
			return
		}
		canvas.Fill(Rect{Y: y, W: width, H: 1}, contentStyle)
		canvas.WriteText(0, y, PlainTruncate(prompt, width, ""), promptStyle)
		if contentWidth <= 0 {
			return
		}
		before = PlainTruncate(before, contentWidth, "")
		cursor = PlainTruncate(cursor, maxInt(1, contentWidth-PlainWidth(before)), "")
		remaining := maxInt(0, contentWidth-PlainWidth(before)-PlainWidth(cursor))
		after = PlainTruncate(after, remaining, "")
		offset := promptWidth
		cursorStyle := contentStyle
		if c.CursorVisible {
			cursorStyle = CellStyle{FG: cellColor(c.Palette.UserTextBackground), BG: cellColor(c.Palette.UserTextForeground)}
		}
		canvas.WriteText(offset, y, before, beforeStyle)
		canvas.WriteText(offset+PlainWidth(before), y, cursor, cursorStyle)
		canvas.WriteText(offset+PlainWidth(before)+PlainWidth(cursor), y, after, afterStyle)
	}

	middleY := 1
	if c.HalfBlocks {
		paintHalfBlock(0, "▄")
		paintHalfBlock(2, "▀")
	} else {
		paintBlankLine(0)
		paintBlankLine(2)
	}
	if strings.TrimSpace(c.Value) == "" {
		placeholder := PlainTruncate(c.Placeholder, contentWidth, "")
		cursor := ""
		after := ""
		if placeholder != "" {
			runes := []rune(placeholder)
			cursor = string(runes[0])
			if strings.TrimSpace(c.ContentCursor) != "" {
				cursor = c.ContentCursor
			}
			if len(runes) > 1 {
				after = string(runes[1:])
			}
		} else {
			cursor = c.ContentCursor
		}
		paintLine(middleY, "", cursor, after, contentStyle, mutedStyle)
		return
	}
	paintLine(middleY, c.ContentBefore, c.ContentCursor, c.ContentAfter, contentStyle, contentStyle)
}

func (c Composer) render() Surface {
	width := maxInt(1, c.Width)
	prompt := c.PromptGlyph + " "
	promptWidth := PlainWidth(prompt)
	if promptWidth >= width {
		prompt = PlainTruncate(prompt, maxInt(1, width-1), "")
		promptWidth = PlainWidth(prompt)
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
		s := BlankSurface(width, 3)
		s = s.placeAt(0, 0, renderHalfBlockSurface(width, "▄", c.Palette))
		s = s.placeAt(0, 1, middle)
		s = s.placeAt(0, 2, renderHalfBlockSurface(width, "▀", c.Palette))
		return s
	}
	s := BlankSurface(width, 3)
	s = s.placeAt(0, 0, renderBlankLine())
	s = s.placeAt(0, 1, middle)
	s = s.placeAt(0, 2, renderBlankLine())
	return s
}

func (c Composer) renderPlaceholderLine(promptStyle, contentStyle lipgloss.Style, prompt string, contentWidth int, placeholder string, cursorChar string) string {
	return strings.Join(c.renderPlaceholderSurface(promptStyle, contentStyle, prompt, contentWidth, placeholder, cursorChar).Lines(), "\n")
}

func (c Composer) renderPlaceholderSurface(promptStyle, contentStyle lipgloss.Style, prompt string, contentWidth int, placeholder string, cursorChar string) Surface {
	placeholder = PlainTruncate(placeholder, contentWidth, "")
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

func (c Composer) renderLineSurface(prompt string, promptStyle lipgloss.Style, before, cursor, after string, contentWidth int, cursorVisible bool, textFG, textBG lipgloss.Color) Surface {
	width := PlainWidth(prompt) + maxInt(0, contentWidth)
	if width <= 0 {
		width = PlainWidth(prompt)
	}
	s := BlankSurface(width, 1)
	promptCellStyle := CellStyle{BG: cellColor(c.Palette.UserTextBackground), FG: cellColor(c.Palette.UserAccentBar)}
	if contentWidth <= 0 {
		s.WriteText(0, 0, prompt, promptCellStyle)
		return s
	}
	before = PlainTruncate(before, contentWidth, "")
	cursor = PlainTruncate(cursor, maxInt(1, contentWidth-PlainWidth(before)), "")
	remaining := maxInt(0, contentWidth-PlainWidth(before)-PlainWidth(cursor))
	after = PlainTruncate(after, remaining, "")
	remaining = maxInt(0, contentWidth-PlainWidth(before)-PlainWidth(cursor)-PlainWidth(after))
	contentStyle := CellStyle{FG: cellColor(textFG), BG: cellColor(textBG)}
	cursorStyle := contentStyle
	if cursorVisible {
		cursorStyle = CellStyle{FG: cellColor(textBG), BG: cellColor(textFG)}
	}
	s.WriteText(0, 0, prompt, promptCellStyle)
	offset := PlainWidth(prompt)
	for x := offset; x < width; x++ {
		s.setCell(x, 0, blankCell(contentStyle))
	}
	s.WriteText(offset, 0, before, contentStyle)
	s.WriteText(offset+PlainWidth(before), 0, cursor, cursorStyle)
	s.WriteText(offset+PlainWidth(before)+PlainWidth(cursor), 0, after, contentStyle)
	if remaining > 0 {
		s.WriteText(offset+PlainWidth(before)+PlainWidth(cursor)+PlainWidth(after), 0, strings.Repeat(" ", remaining), contentStyle)
	}
	_ = promptStyle
	return s
}

func (c Composer) renderPlaceholder(prompt string, promptStyle lipgloss.Style, before, cursor, after string, contentWidth int, cursorVisible bool, textFG, textBG, muted lipgloss.Color) Surface {
	width := PlainWidth(prompt) + maxInt(0, contentWidth)
	if width <= 0 {
		width = PlainWidth(prompt)
	}
	s := BlankSurface(width, 1)
	promptCellStyle := CellStyle{BG: cellColor(c.Palette.UserTextBackground), FG: cellColor(c.Palette.UserAccentBar)}
	if contentWidth <= 0 {
		s.WriteText(0, 0, prompt, promptCellStyle)
		return s
	}
	before = PlainTruncate(before, contentWidth, "")
	cursor = PlainTruncate(cursor, maxInt(1, contentWidth-PlainWidth(before)), "")
	remaining := maxInt(0, contentWidth-PlainWidth(before)-PlainWidth(cursor))
	after = PlainTruncate(after, remaining, "")
	remaining = maxInt(0, contentWidth-PlainWidth(before)-PlainWidth(cursor)-PlainWidth(after))
	beforeStyle := CellStyle{FG: cellColor(textFG), BG: cellColor(textBG)}
	cursorStyle := CellStyle{FG: cellColor(muted), BG: cellColor(textBG)}
	if cursorVisible {
		cursorStyle = CellStyle{FG: cellColor(textBG), BG: cellColor(textFG)}
	}
	afterStyle := CellStyle{FG: cellColor(muted), BG: cellColor(textBG)}
	s.WriteText(0, 0, prompt, promptCellStyle)
	offset := PlainWidth(prompt)
	for x := offset; x < width; x++ {
		s.setCell(x, 0, blankCell(beforeStyle))
	}
	s.WriteText(offset, 0, before, beforeStyle)
	s.WriteText(offset+PlainWidth(before), 0, cursor, cursorStyle)
	s.WriteText(offset+PlainWidth(before)+PlainWidth(cursor), 0, after, afterStyle)
	if remaining > 0 {
		s.WriteText(offset+PlainWidth(before)+PlainWidth(cursor)+PlainWidth(after), 0, strings.Repeat(" ", remaining), beforeStyle)
	}
	_ = promptStyle
	return s
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

func (l AttachmentList) Paint(ctx *Context, canvas Canvas) {
	width := canvas.Width()
	if width <= 0 || canvas.Height() <= 0 || len(l.Items) == 0 {
		return
	}
	style := CellStyle{FG: cellColor(ctx.Palette.MarkdownText), BG: cellColor(ctx.Palette.UserTextBackground)}
	for y, item := range l.Items {
		if y >= canvas.Height() {
			break
		}
		canvas.Fill(Rect{Y: y, W: width, H: 1}, style)
		if width > 1 {
			canvas.WriteText(1, y, PlainTruncate(item.Label, maxInt(1, width-2), ""), style)
		}
	}
}

func (l AttachmentList) render(palette theme.Palette) Surface {
	if len(l.Items) == 0 || l.Width <= 0 {
		return Surface{}
	}
	s := BlankSurface(l.Width, len(l.Items))
	style := CellStyle{FG: cellColor(palette.MarkdownText), BG: cellColor(palette.UserTextBackground)}
	for y, item := range l.Items {
		for x := 0; x < l.Width; x++ {
			s.setCell(x, y, blankCell(style))
		}
		s.WriteText(1, y, PlainTruncate(item.Label, maxInt(1, l.Width-2), ""), style)
	}
	return s
}
