package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

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
	return Surface{lines: []string{
		lipgloss.NewStyle().
			Foreground(i.Palette.ActivityText).
			Bold(true).
			Render(i.Indicator),
	}}
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

	rendered := make([]string, 0, len(lines))
	stampStart := -1
	if m.Stamp != "" {
		stampStart = len(lines) - len(WrapUserMessageLine(m.Stamp, innerWidth))
	}
	if m.HalfBlocks {
		rendered = append(rendered, renderHalfBlockLine(width, "▄", m.Palette))
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
	if m.HalfBlocks {
		rendered = append(rendered, renderHalfBlockLine(width, "▀", m.Palette))
	} else {
		rendered = append(rendered, barStyle.Render(bar)+contentStyle.Render(""))
	}
	return Surface{lines: rendered}
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
	lines := make([]string, 0, 1)
	if m.Stamp != "" {
		lines = append(lines, lipgloss.NewStyle().
			Foreground(m.Palette.AssistantTimestampText).
			Render(m.Stamp))
	}
	bodyStyle := lipgloss.NewStyle().Foreground(m.Palette.MarkdownText)
	for _, line := range wrapStyledLines(strings.TrimSpace(m.Body), m.Width) {
		lines = append(lines, bodyStyle.Render(line))
	}
	return Surface{lines: lines}
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
	lineStyle := lipgloss.NewStyle().
		Background(b.Palette.ReasoningBackground).
		Foreground(b.Palette.ReasoningText)
	lines := []string{lineStyle.Render("")}
	for _, line := range wrapStyledLines(content, b.Width) {
		lines = append(lines, lineStyle.Render(line))
	}
	return Surface{lines: lines}
}

func WorkingIndicatorLine(indicator string) string {
	if strings.TrimSpace(indicator) == "" {
		return ""
	}
	return fmt.Sprintf("%s  Working ...", indicator)
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
		chunks := strings.Split(ansi.Wordwrap(line, width, ""), "\n")
		wrapped = append(wrapped, chunks...)
	}
	return wrapped
}
