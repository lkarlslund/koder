package ui

import (
	"strings"
)

type InputField struct {
	Width         int
	Value         string
	Placeholder   string
	ContentBefore string
	ContentCursor string
	ContentAfter  string
	CursorVisible bool
	Foreground    CellColor
	Background    CellColor
	PlaceholderFG CellColor
	BorderColor   CellColor
}

func (i InputField) Measure(_ *Context, constraints Constraints) Size {
	width := i.Width
	if width <= 0 {
		width = constraints.MaxW
	}
	if width <= 0 {
		width = 1
	}
	return constraints.Clamp(Size{W: width, H: 3})
}

func (i InputField) render(width int) Surface {
	if width <= 0 {
		width = maxInt(1, i.Width)
	}
	s := BlankSurface(width, 3)
	bg := cellColor(i.Background)
	fg := cellColor(i.Foreground)
	borderStyle := CellStyle{FG: cellColor(i.BorderColor), BG: bg}
	contentStyle := CellStyle{FG: fg, BG: bg}
	placeholderStyle := CellStyle{FG: cellColor(i.PlaceholderFG), BG: bg}.WithItalic(true)
	cursorStyle := CellStyle{FG: bg, BG: fg}

	border := NormalBorder()
	s.WriteText(0, 0, border.TopLeft, borderStyle)
	s.WriteText(width-1, 0, border.TopRight, borderStyle)
	s.WriteText(0, 2, border.BottomLeft, borderStyle)
	s.WriteText(width-1, 2, border.BottomRight, borderStyle)
	for x := 1; x < width-1; x++ {
		s.setCell(x, 0, newCell(GlyphFromString(border.Top), 1, borderStyle))
		s.setCell(x, 2, newCell(GlyphFromString(border.Bottom), 1, borderStyle))
	}
	s.setCell(0, 1, newCell(GlyphFromString(border.Left), 1, borderStyle))
	s.setCell(width-1, 1, newCell(GlyphFromString(border.Right), 1, borderStyle))
	for x := 1; x < width-1; x++ {
		s.setCell(x, 1, blankCell(contentStyle))
	}

	innerWidth := maxInt(1, width-2)
	if strings.TrimSpace(i.Value) == "" && strings.TrimSpace(i.Placeholder) != "" {
		placeholder := PlainTruncate(i.Placeholder, innerWidth, "")
		if i.CursorVisible {
			runes := []rune(placeholder)
			cursor := "█"
			after := ""
			if len(runes) > 0 {
				cursor = string(runes[0])
				if len(runes) > 1 {
					after = string(runes[1:])
				}
			}
			s.WriteText(1, 1, "", placeholderStyle)
			s.WriteText(1, 1, cursor, cursorStyle)
			s.WriteText(1+PlainWidth(cursor), 1, after, placeholderStyle)
			return s
		}
		s.WriteText(1, 1, placeholder, placeholderStyle)
		return s
	}

	before := PlainTruncate(i.ContentBefore, innerWidth, "")
	cursor := PlainTruncate(i.ContentCursor, maxInt(1, innerWidth-PlainWidth(before)), "")
	cursorDisplay := cursorDisplay(cursor, i.CursorVisible)
	remaining := maxInt(0, innerWidth-PlainWidth(before)-PlainWidth(cursorDisplay))
	after := PlainTruncate(i.ContentAfter, remaining, "")
	s.WriteText(1, 1, before, contentStyle)
	if i.CursorVisible {
		if cursorDisplay == "█" {
			s.WriteText(1+PlainWidth(before), 1, cursorDisplay, contentStyle)
		} else {
			s.WriteText(1+PlainWidth(before), 1, cursorDisplay, cursorStyle)
		}
	} else {
		s.WriteText(1+PlainWidth(before), 1, cursorDisplay, contentStyle)
	}
	s.WriteText(1+PlainWidth(before)+PlainWidth(cursorDisplay), 1, after, contentStyle)
	return s
}

func cursorDisplay(input string, visible bool) string {
	if input == "" || input == " " {
		if visible {
			return "█"
		}
		return " "
	}
	return input
}

func (i InputField) Paint(_ *Context, canvas Canvas) {
	if canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	canvas.BlitSurface(0, 0, i.render(maxInt(1, canvas.Width())).normalize(canvas.Width(), canvas.Height()))
}
