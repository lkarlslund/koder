package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/lkarlslund/koder/internal/theme"
)

type TranscriptItem struct {
	Element   Element
	GapBefore int
}

type Transcript struct {
	Items []TranscriptItem
}

func (t Transcript) Measure(ctx *Context, constraints Constraints) Size {
	maxW := 0
	totalH := 0
	for _, item := range t.Items {
		if item.GapBefore > 0 {
			totalH += item.GapBefore
		}
		if item.Element == nil {
			continue
		}
		size := item.Element.Measure(ctx, constraints)
		if size.W > maxW {
			maxW = size.W
		}
		totalH += size.H
	}
	return constraints.Clamp(Size{W: maxW, H: totalH})
}

func (t Transcript) Render(ctx *Context, bounds Rect) Surface {
	base := BlankSurface(bounds.W, bounds.H)
	y := 0
	for _, item := range t.Items {
		y += max(0, item.GapBefore)
		if item.Element == nil || y >= bounds.H {
			continue
		}
		size := item.Element.Measure(ctx, NewConstraints(bounds.W, max(0, bounds.H-y)))
		if size.H <= 0 {
			continue
		}
		child := item.Element.Render(ctx, Rect{X: bounds.X, Y: bounds.Y + y, W: bounds.W, H: size.H})
		base = base.placeAt(0, y, child)
		y += size.H
	}
	return base
}

type UserMessageProps struct {
	Palette     theme.Palette
	Body        string
	Stamp       string
	Width       int
	HalfBlocks  bool
	PromptGlyph string
}

type ActivityIndicator struct {
	Indicator string
	Palette   theme.Palette
}

func (i ActivityIndicator) Measure(_ *Context, constraints Constraints) Size {
	return constraints.Clamp(i.render().Size())
}

func (i ActivityIndicator) Render(_ *Context, bounds Rect) Surface {
	return i.render().normalize(bounds.W, bounds.H)
}

func (i ActivityIndicator) render() Surface {
	if strings.TrimSpace(i.Indicator) == "" {
		return Surface{}
	}
	line := BlankSurface(PlainWidth(i.Indicator), 1)
	line.WriteText(0, 0, i.Indicator, CellStyle{FG: i.Palette.ActivityText, Bold: true})
	return line
}

type UserMessage struct {
	Palette     theme.Palette
	Body        string
	Stamp       string
	Width       int
	HalfBlocks  bool
	PromptGlyph string
}

func NewUserMessage(props UserMessageProps) UserMessage {
	return UserMessage(props)
}

func (m UserMessage) Measure(_ *Context, constraints Constraints) Size {
	return constraints.Clamp(m.render().Size())
}

func (m UserMessage) Render(_ *Context, bounds Rect) Surface {
	return m.render().normalize(bounds.W, bounds.H)
}

func (m UserMessage) render() Surface {
	baseLines := []string{""}
	content := strings.TrimSpace(m.Body)
	if content != "" {
		baseLines = append(baseLines, strings.Split(content, "\n")...)
	}
	if m.Stamp != "" {
		baseLines = append(baseLines, m.Stamp)
	}
	baseLines = append(baseLines, "")

	width := m.Width
	if width <= 0 {
		width = UserMessageWidth(baseLines)
	}
	bar := m.PromptGlyph + " "
	contentWidth := maxInt(1, width-lipgloss.Width(bar))
	innerWidth := contentWidth
	barStyle := lipgloss.NewStyle().
		Background(m.Palette.UserTextBackground).
		Foreground(m.Palette.UserAccentBar)
	contentStyle := lipgloss.NewStyle().
		Background(m.Palette.UserTextBackground).
		Foreground(m.Palette.UserTextForeground).
		Width(contentWidth)
	timestampStyle := contentStyle.Foreground(m.Palette.UserTimestampForeground)

	lines := []string{}
	if content != "" {
		for _, line := range strings.Split(content, "\n") {
			lines = append(lines, WrapUserMessageLine(line, innerWidth)...)
		}
	}
	if m.Stamp != "" {
		lines = append(lines, WrapUserMessageLine(m.Stamp, innerWidth)...)
	}

	stampStart := -1
	if m.Stamp != "" {
		stampStart = len(lines) - len(WrapUserMessageLine(m.Stamp, innerWidth))
	}
	height := len(lines) + 2
	rendered := BlankSurface(width, height)
	if m.HalfBlocks {
		rendered = appendSurfaceRows(rendered, 0, renderHalfBlockSurface(width, "▄", m.Palette))
	} else {
		rendered = appendSurfaceRows(rendered, 0, FilledLineSurface(width, bar, CellStyle{BG: m.Palette.UserTextBackground, FG: m.Palette.UserAccentBar}, CellStyle{BG: m.Palette.UserTextBackground}))
	}
	for idx, line := range lines {
		row := idx + 1
		rendered.WriteText(0, row, bar, CellStyle{BG: m.Palette.UserTextBackground, FG: m.Palette.UserAccentBar})
		if stampStart >= 0 && idx >= stampStart {
			rendered.WriteText(lipgloss.Width(bar), row, line, CellStyle{BG: m.Palette.UserTextBackground, FG: m.Palette.UserTimestampForeground})
			continue
		}
		rendered.WriteText(lipgloss.Width(bar), row, line, CellStyle{BG: m.Palette.UserTextBackground, FG: m.Palette.UserTextForeground})
	}
	if m.HalfBlocks {
		rendered = appendSurfaceRows(rendered, height-1, renderHalfBlockSurface(width, "▀", m.Palette))
	} else {
		rendered = appendSurfaceRows(rendered, height-1, FilledLineSurface(width, bar, CellStyle{BG: m.Palette.UserTextBackground, FG: m.Palette.UserAccentBar}, CellStyle{BG: m.Palette.UserTextBackground}))
	}
	_ = barStyle
	_ = contentStyle
	_ = timestampStyle
	return rendered
}

func WrapUserMessageLine(line string, width int) []string {
	if width <= 0 {
		return []string{""}
	}
	if strings.TrimSpace(line) == "" {
		return []string{""}
	}
	wrapped := PlainWordWrap(line, width)
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

type AssistantMessage struct {
	Body    string
	Stamp   string
	Width   int
	Palette theme.Palette
}

func (m AssistantMessage) Measure(_ *Context, constraints Constraints) Size {
	return constraints.Clamp(m.render().Size())
}

func (m AssistantMessage) Render(_ *Context, bounds Rect) Surface {
	return m.render().normalize(bounds.W, bounds.H)
}

func (m AssistantMessage) render() Surface {
	lines := make([]struct {
		text  string
		style CellStyle
	}, 0, 1)
	if m.Stamp != "" {
		lines = append(lines, struct {
			text  string
			style CellStyle
		}{text: m.Stamp, style: CellStyle{FG: m.Palette.AssistantTimestampText}})
	}
	bodyStyle := CellStyle{FG: m.Palette.MarkdownText}
	for _, line := range wrapStyledLines(strings.TrimSpace(m.Body), m.Width) {
		lines = append(lines, struct {
			text  string
			style CellStyle
		}{text: line, style: bodyStyle})
	}
	width := 0
	for _, line := range lines {
		width = maxInt(width, PlainWidth(line.text))
	}
	s := BlankSurface(width, len(lines))
	for y, line := range lines {
		s.WriteText(0, y, line.text, line.style)
	}
	return s
}

type ReasoningBlock struct {
	Body    string
	Width   int
	Palette theme.Palette
}

func (b ReasoningBlock) Measure(_ *Context, constraints Constraints) Size {
	return constraints.Clamp(b.render().Size())
}

func (b ReasoningBlock) Render(_ *Context, bounds Rect) Surface {
	return b.render().normalize(bounds.W, bounds.H)
}

func (b ReasoningBlock) render() Surface {
	content := strings.TrimSpace(b.Body)
	if content == "" {
		return Surface{}
	}
	lines := []string{""}
	for _, line := range wrapStyledLines(content, b.Width) {
		lines = append(lines, line)
	}
	width := 0
	for _, line := range lines {
		width = maxInt(width, PlainWidth(line))
	}
	s := BlankSurface(width, len(lines))
	style := CellStyle{BG: b.Palette.ReasoningBackground, FG: b.Palette.ReasoningText}
	for y, line := range lines {
		for x := 0; x < width; x++ {
			s.setCell(x, y, Cell{Text: " ", Width: 1, Style: style})
		}
		s.WriteText(0, y, line, style)
	}
	return s
}

func WorkingIndicatorLine(indicator string) string {
	if strings.TrimSpace(indicator) == "" {
		return ""
	}
	return fmt.Sprintf("%s  Working ...", indicator)
}

func renderHalfBlockSurface(width int, char string, palette theme.Palette) Surface {
	if width <= 0 {
		return Surface{}
	}
	s := BlankSurface(width, 1)
	s.WriteText(0, 0, char, CellStyle{FG: palette.UserAccentBar})
	if width > 1 {
		fillStyle := CellStyle{FG: palette.UserTextBackground}
		for x := 1; x < width; x++ {
			s.setCell(x, 0, Cell{Text: char, Width: 1, Style: fillStyle})
		}
	}
	return s
}

func appendSurfaceRows(dst Surface, y int, src Surface) Surface {
	return dst.placeAt(0, y, src)
}

func WrapStyledBlock(input string, width int) string {
	return strings.Join(wrapStyledLines(input, width), "\n")
}

func wrapStyledLines(input string, width int) []string {
	if width <= 0 {
		if strings.TrimSpace(input) == "" {
			return nil
		}
		return strings.Split(input, "\n")
	}
	var wrapped []string
	for _, line := range strings.Split(input, "\n") {
		if strings.TrimSpace(line) == "" {
			wrapped = append(wrapped, "")
			continue
		}
		chunks := strings.Split(PlainWordWrap(line, width), "\n")
		wrapped = append(wrapped, chunks...)
	}
	return wrapped
}
