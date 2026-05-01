package ui

import (
	"strings"
	"unicode"

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
	CursorIndex   int
	Placeholder   string
	CursorVisible bool
}

type AttachmentItem struct {
	Label string
}

type Composer struct {
	PassiveNode
	Palette       theme.Palette
	Width         int
	Attachments   []AttachmentItem
	TokenRanges   []TokenRange
	HalfBlocks    bool
	PromptGlyph   string
	Value         string
	CursorIndex   int
	Placeholder   string
	CursorVisible bool
}

type TokenRange struct {
	Start int
	End   int
}

func NewComposer(props ComposerProps) Composer {
	return Composer{
		Palette:       props.Palette,
		Width:         props.Width,
		Attachments:   props.Attachments,
		TokenRanges:   props.TokenRanges,
		HalfBlocks:    props.HalfBlocks,
		PromptGlyph:   props.PromptGlyph,
		Value:         props.Value,
		CursorIndex:   props.CursorIndex,
		Placeholder:   props.Placeholder,
		CursorVisible: props.CursorVisible,
	}
}

func (c Composer) Measure(_ *Context, constraints Constraints) Size {
	return constraints.Clamp(c.size())
}

func (c Composer) Paint(_ *Context, canvas Canvas) {
	if canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	c.paint(canvas)
}

func (c Composer) render() Surface {
	size := c.size()
	if size.W <= 0 || size.H <= 0 {
		return Surface{}
	}
	return PaintNodeSurface(nil, c, Rect{W: size.W, H: size.H})
}

func (c Composer) size() Size {
	layout := c.layout()
	return Size{
		W: maxInt(1, c.Width),
		H: len(c.Attachments) + len(layout.lines) + 2,
	}
}

func (c Composer) paint(canvas Canvas) {
	layout := c.layout()
	width := maxInt(1, c.Width)
	if canvas.Width() > 0 {
		width = min(width, canvas.Width())
	}
	prompt := c.PromptGlyph + " "
	promptWidth := PlainWidth(prompt)
	if promptWidth >= width {
		prompt = PlainTruncate(prompt, maxInt(1, width-1), "")
		promptWidth = PlainWidth(prompt)
	}
	contentWidth := maxInt(0, width-promptWidth)
	attachmentHeight := len(c.Attachments)

	y := 0
	if c.HalfBlocks {
		c.paintHalfBlockLine(canvas, y, "▄", width)
	} else {
		c.paintLine(canvas, y, prompt, "", "", "", nil, contentWidth, false, c.Palette.UserTextForeground, c.Palette.UserTextBackground)
	}
	y++
	if attachmentHeight > 0 {
		AttachmentList{Items: c.Attachments, Width: width}.Paint(&Context{Palette: c.Palette}, canvas.Subrect(Rect{Y: y, W: width, H: min(attachmentHeight, maxInt(0, canvas.Height()-y))}))
		y += attachmentHeight
	}
	for idx, line := range layout.lines {
		linePrompt := prompt
		if idx > 0 {
			linePrompt = strings.Repeat(" ", promptWidth)
		}
		if line.placeholder {
			c.paintPlaceholder(canvas, y, linePrompt, line.before, line.cursor, line.after, contentWidth, line.cursorVisible, c.Palette.UserTextForeground, c.Palette.UserTextBackground, c.Palette.ComposerMutedText)
		} else {
			c.paintLine(canvas, y, linePrompt, line.before, line.cursor, line.after, line.tokens, contentWidth, line.cursorVisible, c.Palette.UserTextForeground, c.Palette.UserTextBackground)
		}
		y++
	}
	if c.HalfBlocks {
		c.paintHalfBlockLine(canvas, y, "▀", width)
	} else {
		c.paintLine(canvas, y, prompt, "", "", "", nil, contentWidth, false, c.Palette.UserTextForeground, c.Palette.UserTextBackground)
	}
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
	c.paintLine(NewCanvas(&s, Rect{W: width, H: 1}), 0, prompt, before, cursor, after, tokenRanges, contentWidth, cursorVisible, textFG, textBG)
	_ = promptStyle
	return s
}

func (c Composer) paintLine(canvas Canvas, y int, prompt, before, cursor, after string, tokenRanges []TokenRange, contentWidth int, cursorVisible bool, textFG, textBG CellColor) {
	if y < 0 || y >= canvas.Height() {
		return
	}
	width := min(canvas.Width(), PlainWidth(prompt)+maxInt(0, contentWidth))
	if width <= 0 {
		return
	}
	promptCellStyle := CellStyle{BG: cellColor(c.Palette.UserTextBackground), FG: cellColor(c.Palette.UserAccentBar)}
	paintComposerText(canvas, 0, y, prompt, promptCellStyle)
	if contentWidth <= 0 {
		return
	}
	before = PlainTruncate(before, contentWidth, "")
	cursor = PlainTruncate(cursor, maxInt(1, contentWidth-PlainWidth(before)), "")
	remaining := maxInt(0, contentWidth-PlainWidth(before)-PlainWidth(cursor))
	after = PlainTruncate(after, remaining, "")
	remaining = maxInt(0, contentWidth-PlainWidth(before)-PlainWidth(cursor)-PlainWidth(after))
	contentStyle := CellStyle{FG: cellColor(textFG), BG: cellColor(textBG)}
	offset := PlainWidth(prompt)
	if offset < width {
		canvas.Fill(Rect{X: offset, Y: y, W: width - offset, H: 1}, contentStyle)
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
		paintComposerText(canvas, x, y, char, style)
		x += PlainWidth(char)
	}
	if remaining > 0 {
		paintComposerText(canvas, offset+PlainWidth(before)+PlainWidth(cursor)+PlainWidth(after), y, strings.Repeat(" ", remaining), contentStyle)
	}
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
	c.paintPlaceholder(NewCanvas(&s, Rect{W: width, H: 1}), 0, prompt, before, cursor, after, contentWidth, cursorVisible, textFG, textBG, muted)
	_ = promptStyle
	return s
}

func (c Composer) paintPlaceholder(canvas Canvas, y int, prompt, before, cursor, after string, contentWidth int, cursorVisible bool, textFG, textBG, muted CellColor) {
	if y < 0 || y >= canvas.Height() {
		return
	}
	width := min(canvas.Width(), PlainWidth(prompt)+maxInt(0, contentWidth))
	if width <= 0 {
		return
	}
	promptCellStyle := CellStyle{BG: cellColor(c.Palette.UserTextBackground), FG: cellColor(c.Palette.UserAccentBar)}
	paintComposerText(canvas, 0, y, prompt, promptCellStyle)
	if contentWidth <= 0 {
		return
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
	offset := PlainWidth(prompt)
	if offset < width {
		canvas.Fill(Rect{X: offset, Y: y, W: width - offset, H: 1}, beforeStyle)
	}
	paintComposerText(canvas, offset, y, before, beforeStyle)
	paintComposerText(canvas, offset+PlainWidth(before), y, cursor, cursorStyle)
	paintComposerText(canvas, offset+PlainWidth(before)+PlainWidth(cursor), y, after, afterStyle)
	if remaining > 0 {
		paintComposerText(canvas, offset+PlainWidth(before)+PlainWidth(cursor)+PlainWidth(after), y, strings.Repeat(" ", remaining), beforeStyle)
	}
}

func (c Composer) paintHalfBlockLine(canvas Canvas, y int, char string, width int) {
	if y < 0 || y >= canvas.Height() || width <= 0 {
		return
	}
	style := CellStyle{FG: cellColor(c.Palette.UserTextBackground)}
	paintComposerText(canvas, 0, y, renderHalfBlockLine(width, char, c.Palette), style)
}

func paintComposerText(canvas Canvas, x, y int, text string, style CellStyle) {
	col := x
	for _, r := range text {
		grapheme := string(r)
		width := PlainWidth(grapheme)
		if width <= 0 {
			continue
		}
		if col >= canvas.Width() {
			break
		}
		if col >= 0 {
			canvas.SetCell(col, y, newCell(Glyph(r), width, style))
			for extra := 1; extra < width && col+extra < canvas.Width(); extra++ {
				canvas.SetCell(col+extra, y, continuationCell(style))
			}
		}
		col += width
	}
}

func (c Composer) CursorRect() (Rect, bool) {
	layout := c.layout()
	if !layout.cursorVisible {
		return Rect{}, false
	}
	width := layout.cursorWidth
	if width <= 0 {
		width = 1
	}
	return Rect{X: layout.cursorX, Y: layout.cursorY, W: width, H: 1}, true
}

type composerRenderLine struct {
	before        string
	cursor        string
	after         string
	tokens        []TokenRange
	cursorVisible bool
	placeholder   bool
}

type composerLayout struct {
	lines         []composerRenderLine
	cursorVisible bool
	cursorX       int
	cursorY       int
	cursorWidth   int
}

func (c Composer) layout() composerLayout {
	width := maxInt(1, c.Width)
	prompt := c.PromptGlyph + " "
	promptWidth := PlainWidth(prompt)
	if promptWidth >= width {
		prompt = PlainTruncate(prompt, maxInt(1, width-1), "")
		promptWidth = PlainWidth(prompt)
	}
	contentWidth := maxInt(1, width-promptWidth)
	attachmentHeight := len(c.Attachments)
	layout := composerLayout{}
	if strings.TrimSpace(c.Value) == "" {
		layout.lines = placeholderComposerLines(c.Placeholder, contentWidth, c.CursorVisible)
		if len(layout.lines) == 0 {
			layout.lines = []composerRenderLine{{cursor: " ", cursorVisible: c.CursorVisible, placeholder: true}}
		}
		layout.cursorVisible = c.CursorVisible
		layout.cursorX = promptWidth
		layout.cursorY = attachmentHeight + 1
		layout.cursorWidth = PlainWidth(layout.lines[0].cursor)
		return layout
	}
	lines, cursorLine, cursorCol, cursorWidth := layoutComposerValue([]rune(c.Value), c.CursorIndex, c.TokenRanges, contentWidth)
	layout.lines = lines
	if cursorLine >= 0 {
		layout.cursorVisible = c.CursorVisible
		layout.cursorX = promptWidth + cursorCol
		layout.cursorY = attachmentHeight + 1 + cursorLine
		layout.cursorWidth = cursorWidth
	}
	return layout
}

func placeholderComposerLines(placeholder string, width int, cursorVisible bool) []composerRenderLine {
	runes := []rune(placeholder)
	if len(runes) == 0 {
		return []composerRenderLine{{cursor: " ", cursorVisible: cursorVisible, placeholder: true}}
	}
	chunks := wrapComposerRunes(runes, width)
	lines := make([]composerRenderLine, 0, len(chunks))
	cursorPlaced := false
	for _, chunk := range chunks {
		line := composerRenderLine{placeholder: true}
		if !cursorPlaced {
			line.cursorVisible = cursorVisible
			if len(chunk) > 0 {
				line.cursor = string(chunk[0])
				line.after = string(chunk[1:])
			} else {
				line.cursor = " "
			}
			cursorPlaced = true
		} else {
			line.before = string(chunk)
		}
		lines = append(lines, line)
	}
	return lines
}

func layoutComposerValue(value []rune, cursor int, tokenRanges []TokenRange, width int) ([]composerRenderLine, int, int, int) {
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(value) {
		cursor = len(value)
	}
	var (
		lines      []composerRenderLine
		cursorLine = -1
		cursorCol  int
		cursorW    = 1
		lineStart  int
	)
	appendLogicalLine := func(end int, includeTrailingCursor bool) {
		runes := value[lineStart:end]
		chunks := wrapComposerRunes(runes, width)
		if len(chunks) == 0 {
			chunks = [][]rune{{}}
		}
		globalChunkStart := lineStart
		for idx, chunk := range chunks {
			chunkEnd := globalChunkStart + len(chunk)
			hasCursor := false
			if cursorLine < 0 {
				switch {
				case cursor >= globalChunkStart && cursor < chunkEnd:
					hasCursor = true
				case cursor == chunkEnd && (idx == len(chunks)-1 || includeTrailingCursor):
					hasCursor = true
				}
			}
			line := composerRenderLine{tokens: localTokenRanges(tokenRanges, globalChunkStart, chunkEnd)}
			if hasCursor {
				local := cursor - globalChunkStart
				if local < 0 {
					local = 0
				}
				if local > len(chunk) {
					local = len(chunk)
				}
				line.before = string(chunk[:local])
				line.cursorVisible = true
				if local < len(chunk) {
					line.cursor = string(chunk[local])
					line.after = string(chunk[local+1:])
				} else {
					line.cursor = " "
				}
				cursorLine = len(lines)
				cursorCol = PlainWidth(line.before)
				if width := PlainWidth(line.cursor); width > 0 {
					cursorW = width
				}
			} else {
				line.before = string(chunk)
			}
			lines = append(lines, line)
			globalChunkStart = chunkEnd
		}
	}
	for idx := 0; idx <= len(value); idx++ {
		if idx == len(value) || value[idx] == '\n' {
			appendLogicalLine(idx, idx == len(value))
			lineStart = idx + 1
		}
	}
	if len(lines) == 0 {
		lines = []composerRenderLine{{cursor: " ", cursorVisible: true}}
		cursorLine = 0
		cursorCol = 0
	}
	return lines, cursorLine, cursorCol, cursorW
}

func wrapComposerRunes(runes []rune, width int) [][]rune {
	width = maxInt(1, width)
	if len(runes) == 0 {
		return [][]rune{{}}
	}
	type token struct {
		runes []rune
		width int
		space bool
	}
	tokenWidth := func(chunk []rune) int {
		total := 0
		for _, r := range chunk {
			total += PlainWidth(string(r))
		}
		return total
	}
	splitChunk := func(chunk []rune) [][]rune {
		if len(chunk) == 0 {
			return [][]rune{{}}
		}
		var (
			out  [][]rune
			part []rune
			used int
		)
		flush := func() {
			out = append(out, append([]rune(nil), part...))
			part = nil
			used = 0
		}
		for _, r := range chunk {
			rw := PlainWidth(string(r))
			if rw <= 0 {
				continue
			}
			if used > 0 && used+rw > width {
				flush()
			}
			part = append(part, r)
			used += rw
		}
		if len(part) > 0 {
			flush()
		}
		if len(out) == 0 {
			return [][]rune{{}}
		}
		return out
	}
	var tokens []token
	start := 0
	for start < len(runes) {
		space := unicode.IsSpace(runes[start])
		end := start + 1
		for end < len(runes) && unicode.IsSpace(runes[end]) == space {
			end++
		}
		chunk := append([]rune(nil), runes[start:end]...)
		tokens = append(tokens, token{runes: chunk, width: tokenWidth(chunk), space: space})
		start = end
	}
	var (
		lines [][]rune
		line  []rune
		used  int
	)
	flush := func() {
		lines = append(lines, append([]rune(nil), line...))
		line = nil
		used = 0
	}
	appendChunk := func(chunk []rune, chunkWidth int) {
		line = append(line, chunk...)
		used += chunkWidth
	}
	for idx := 0; idx < len(tokens); idx++ {
		tok := tokens[idx]
		if tok.width > width {
			if used > 0 {
				flush()
			}
			parts := splitChunk(tok.runes)
			for partIdx, part := range parts {
				partWidth := tokenWidth(part)
				if partIdx == len(parts)-1 {
					appendChunk(part, partWidth)
				} else {
					lines = append(lines, append([]rune(nil), part...))
				}
			}
			continue
		}
		if used+tok.width <= width {
			appendChunk(tok.runes, tok.width)
			continue
		}
		if tok.space {
			flush()
			appendChunk(tok.runes, tok.width)
			continue
		}
		flush()
		appendChunk(tok.runes, tok.width)
	}
	if len(line) > 0 {
		flush()
	}
	if len(lines) == 0 {
		return [][]rune{{}}
	}
	return lines
}

func localTokenRanges(ranges []TokenRange, start, end int) []TokenRange {
	if len(ranges) == 0 || end < start {
		return nil
	}
	out := make([]TokenRange, 0, len(ranges))
	for _, token := range ranges {
		if token.End <= start || token.Start >= end {
			continue
		}
		local := TokenRange{
			Start: maxInt(start, token.Start) - start,
			End:   min(end, token.End) - start,
		}
		if local.End > local.Start {
			out = append(out, local)
		}
	}
	return out
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
