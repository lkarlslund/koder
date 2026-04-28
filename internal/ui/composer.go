package ui

import (
	"strings"

	"github.com/lkarlslund/koder/internal/theme"
)

type ComposerProps struct {
	Palette       theme.Palette
	Width         int
	Attachments   []AttachmentItem
	TokenRanges   []TokenRange
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
	Attachments   []AttachmentItem
	TokenRanges   []TokenRange
	HalfBlocks    bool
	PromptGlyph   string
	Value         string
	Placeholder   string
	ContentBefore string
	ContentCursor string
	ContentAfter  string
	CursorVisible bool
}

type TokenRange struct {
	Start int
	End   int
}

func NewComposer(props ComposerProps) Composer {
	return Composer(props)
}

func (c Composer) Measure(_ *Context, constraints Constraints) Size {
	return constraints.Clamp(c.render().Size())
}

func (c Composer) Paint(_ *Context, canvas Canvas) {
	if canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	canvas.BlitSurface(0, 0, c.render().Normalize(canvas.Width(), canvas.Height()))
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
	promptStyle := NewStyle().
		Background(c.Palette.UserTextBackground).
		Foreground(c.Palette.UserAccentBar)
	contentStyle := NewStyle().
		Background(c.Palette.UserTextBackground).
		Foreground(c.Palette.UserTextForeground)
	attachmentRows := c.renderAttachmentRows()
	attachmentHeight := attachmentRows.SurfaceHeight()

	renderBlankLine := func() Surface {
		return c.renderLineSurface(prompt, promptStyle, "", "", "", nil, contentWidth, false, c.Palette.UserTextForeground, c.Palette.UserTextBackground)
	}

	middle := c.renderLineSurface(
		prompt,
		promptStyle,
		c.ContentBefore,
		c.ContentCursor,
		c.ContentAfter,
		c.TokenRanges,
		contentWidth,
		c.CursorVisible,
		c.Palette.UserTextForeground,
		c.Palette.UserTextBackground,
	)
	if strings.TrimSpace(c.Value) == "" {
		middle = c.renderPlaceholderSurface(promptStyle, contentStyle, prompt, contentWidth, c.Placeholder, c.ContentCursor)
	}

	if c.HalfBlocks {
		s := BlankSurface(width, attachmentHeight+3)
		s = s.placeAt(0, 0, renderHalfBlockSurface(width, "▄", c.Palette))
		if attachmentHeight > 0 {
			s = s.placeAt(0, 1, attachmentRows)
		}
		s = s.placeAt(0, attachmentHeight+1, middle)
		s = s.placeAt(0, attachmentHeight+2, renderHalfBlockSurface(width, "▀", c.Palette))
		return s
	}
	s := BlankSurface(width, attachmentHeight+3)
	s = s.placeAt(0, 0, renderBlankLine())
	if attachmentHeight > 0 {
		s = s.placeAt(0, 1, attachmentRows)
	}
	s = s.placeAt(0, attachmentHeight+1, middle)
	s = s.placeAt(0, attachmentHeight+2, renderBlankLine())
	return s
}

func (c Composer) renderAttachmentRows() Surface {
	if len(c.Attachments) == 0 {
		return Surface{}
	}
	return AttachmentList{Items: c.Attachments, Width: maxInt(1, c.Width)}.render(c.Palette)
}

func (c Composer) renderPlaceholderLine(promptStyle, contentStyle Style, prompt string, contentWidth int, placeholder string, cursorChar string) string {
	return strings.Join(c.renderPlaceholderSurface(promptStyle, contentStyle, prompt, contentWidth, placeholder, cursorChar).Lines(), "\n")
}

func (c Composer) renderPlaceholderSurface(promptStyle, contentStyle Style, prompt string, contentWidth int, placeholder string, cursorChar string) Surface {
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

func (c Composer) renderLineSurface(prompt string, promptStyle Style, before, cursor, after string, tokenRanges []TokenRange, contentWidth int, cursorVisible bool, textFG, textBG CellColor) Surface {
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
	s.WriteText(0, 0, prompt, promptCellStyle)
	offset := PlainWidth(prompt)
	for x := offset; x < width; x++ {
		s.setCell(x, 0, blankCell(contentStyle))
	}
	tokenStyle := CellStyle{
		FG: firstNonEmptyColor(cellColor(c.Palette.MarkdownStrongText), cellColor(textFG)),
		BG: firstNonEmptyColor(cellColor(c.Palette.MarkdownMarkBackground), cellColor(textBG)),
	}
	contentRunes := []rune(before + cursor + after)
	cursorPos := len([]rune(before))
	x := offset
	for i, r := range contentRunes {
		style := contentStyle
		if rangeContainsToken(tokenRanges, i) {
			style = tokenStyle
		}
		if cursorVisible && i == cursorPos {
			style = CellStyle{FG: style.BG, BG: style.FG}
		}
		char := string(r)
		s.WriteText(x, 0, char, style)
		x += PlainWidth(char)
	}
	if remaining > 0 {
		s.WriteText(offset+PlainWidth(before)+PlainWidth(cursor)+PlainWidth(after), 0, strings.Repeat(" ", remaining), contentStyle)
	}
	_ = promptStyle
	return s
}

func rangeContainsToken(ranges []TokenRange, pos int) bool {
	for _, rng := range ranges {
		if pos >= rng.Start && pos < rng.End {
			return true
		}
	}
	return false
}

func (c Composer) renderPlaceholder(prompt string, promptStyle Style, before, cursor, after string, contentWidth int, cursorVisible bool, textFG, textBG, muted CellColor) Surface {
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
	if width <= 1 {
		return PlainTruncate(char, width, "")
	}
	return char + strings.Repeat(char, maxInt(1, width-1))
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
