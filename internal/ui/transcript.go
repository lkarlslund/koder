package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/lkarlslund/koder/internal/theme"
)

type TranscriptItem struct {
	Key       string
	Element   Element
	GapBefore int
}

type Transcript struct {
	Items []TranscriptItem
}

type RetainedTranscript struct {
	items []TranscriptItem
}

type transcriptViewportElement interface {
	ApproxHeight(width int) int
	RenderCached(ctx *Context, width int) Surface
}

type CachedElement struct {
	Child      Element
	HeightHint int
	surfaces   map[int]Surface
}

func NewCachedElement(child Element, heightHint int) *CachedElement {
	return &CachedElement{Child: child, HeightHint: max(0, heightHint)}
}

func (e *CachedElement) ApproxHeight(width int) int {
	if e == nil {
		return 0
	}
	if cached, ok := e.surfaces[width]; ok {
		return cached.Size().H
	}
	if e.HeightHint > 0 {
		return e.HeightHint
	}
	return 1
}

func (e *CachedElement) RenderCached(ctx *Context, width int) Surface {
	if e == nil || e.Child == nil {
		return Surface{}
	}
	if width <= 0 {
		size := e.Child.Measure(ctx, Constraints{})
		width = size.W
	}
	if cached, ok := e.surfaces[width]; ok {
		return cached
	}
	size := e.Child.Measure(ctx, NewConstraints(width, 0))
	surface := e.Child.Render(ctx, Rect{W: width, H: size.H})
	if e.surfaces == nil {
		e.surfaces = make(map[int]Surface)
	}
	e.surfaces[width] = surface
	return surface
}

func (e *CachedElement) Measure(ctx *Context, constraints Constraints) Size {
	surface := e.RenderCached(ctx, constraints.MaxW)
	return constraints.Clamp(surface.Size())
}

func (e *CachedElement) Render(ctx *Context, bounds Rect) Surface {
	return e.RenderCached(ctx, bounds.W).normalize(bounds.W, bounds.H)
}

func NewRetainedTranscript() *RetainedTranscript {
	return &RetainedTranscript{}
}

func (t *RetainedTranscript) SetItems(items []TranscriptItem) {
	t.items = append(t.items[:0], items...)
}

func (t *RetainedTranscript) Reconcile(items []TranscriptItem) {
	if t == nil {
		return
	}
	if len(items) == 0 {
		t.items = nil
		return
	}
	if len(t.items) == 0 {
		t.SetItems(items)
		return
	}
	prevByKey := make(map[string]TranscriptItem, len(t.items))
	for _, item := range t.items {
		if strings.TrimSpace(item.Key) == "" {
			continue
		}
		prevByKey[item.Key] = item
	}
	next := make([]TranscriptItem, 0, len(items))
	for _, item := range items {
		if existing, ok := prevByKey[item.Key]; ok {
			if item.Element == nil {
				item.Element = existing.Element
			}
			if item.GapBefore == 0 {
				item.GapBefore = existing.GapBefore
			}
		}
		next = append(next, item)
	}
	t.items = next
}

func (t *RetainedTranscript) Add(item TranscriptItem) {
	t.items = append(t.items, item)
}

func (t *RetainedTranscript) Insert(index int, item TranscriptItem) {
	index = max(0, min(index, len(t.items)))
	t.items = append(t.items[:index], append([]TranscriptItem{item}, t.items[index:]...)...)
}

func (t *RetainedTranscript) Remove(index int) {
	if index < 0 || index >= len(t.items) {
		return
	}
	t.items = append(t.items[:index], t.items[index+1:]...)
}

func (t *RetainedTranscript) Replace(index int, item TranscriptItem) {
	if index < 0 || index >= len(t.items) {
		return
	}
	t.items[index] = item
}

func (t *RetainedTranscript) Clear() {
	t.items = nil
}

func (t *RetainedTranscript) Items() []TranscriptItem {
	out := make([]TranscriptItem, len(t.items))
	copy(out, t.items)
	return out
}

func (t *RetainedTranscript) Measure(ctx *Context, constraints Constraints) Size {
	return Transcript{Items: t.items}.Measure(ctx, constraints)
}

func (t *RetainedTranscript) Render(ctx *Context, bounds Rect) Surface {
	return Transcript{Items: t.items}.Render(ctx, bounds)
}

func (t *RetainedTranscript) ContentHeight(width int) int {
	total := 0
	for _, item := range t.items {
		total += max(0, item.GapBefore)
		total += t.itemApproxHeight(item, width)
	}
	return total
}

func (t *RetainedTranscript) RenderVisible(ctx *Context, width, height, offset int) (Surface, int, int) {
	totalHeight := t.ContentHeight(width)
	maxOffset := max(0, totalHeight-max(0, height))
	offset = min(max(0, offset), maxOffset)
	base := BlankSurface(width, height)
	y := 0
	for _, item := range t.items {
		gap := max(0, item.GapBefore)
		top := y + gap
		approxHeight := t.itemApproxHeight(item, width)
		bottom := top + approxHeight
		y = bottom
		if item.Element == nil || bottom <= offset || top >= offset+height {
			continue
		}
		surface := t.itemSurface(ctx, item, width)
		exactHeight := surface.Size().H
		if exactHeight <= 0 {
			continue
		}
		renderY := top - offset
		base = base.placeAt(0, renderY, surface)
	}
	return base, totalHeight, offset
}

func (t *RetainedTranscript) RenderBottom(ctx *Context, width, height int) (Surface, int, int) {
	base := BlankSurface(width, height)
	if len(t.items) == 0 {
		return base, 0, 0
	}
	indices := make([]int, 0, min(len(t.items), max(1, height)))
	usedHeight := 0
	for idx := len(t.items) - 1; idx >= 0 && usedHeight < height; idx-- {
		surface := t.itemSurface(ctx, t.items[idx], width)
		blockHeight := surface.Size().H
		if blockHeight <= 0 {
			continue
		}
		usedHeight += blockHeight
		if len(indices) > 0 {
			usedHeight += max(0, t.items[indices[len(indices)-1]].GapBefore)
		}
		indices = append(indices, idx)
	}
	totalHeight := t.ContentHeight(width)
	startY := max(0, height-usedHeight)
	cursorY := startY
	for i := len(indices) - 1; i >= 0; i-- {
		idx := indices[i]
		if i < len(indices)-1 {
			cursorY += max(0, t.items[idx].GapBefore)
		}
		surface := t.itemSurface(ctx, t.items[idx], width)
		base = base.placeAt(0, cursorY, surface)
		cursorY += surface.Size().H
	}
	return base, totalHeight, max(0, totalHeight-height)
}

func (t *RetainedTranscript) itemApproxHeight(item TranscriptItem, width int) int {
	if item.Element == nil {
		return 0
	}
	if cached, ok := item.Element.(transcriptViewportElement); ok {
		return cached.ApproxHeight(width)
	}
	return item.Element.Measure(nil, NewConstraints(width, 0)).H
}

func (t *RetainedTranscript) itemSurface(ctx *Context, item TranscriptItem, width int) Surface {
	if item.Element == nil {
		return Surface{}
	}
	if cached, ok := item.Element.(transcriptViewportElement); ok {
		return cached.RenderCached(ctx, width)
	}
	size := item.Element.Measure(ctx, NewConstraints(width, 0))
	return item.Element.Render(ctx, Rect{W: width, H: size.H})
}

type TranscriptViewport struct {
	Transcript *RetainedTranscript
	OffsetY    int
	Width      int
	Height     int
}

func (v TranscriptViewport) Measure(_ *Context, constraints Constraints) Size {
	width := v.Width
	if width <= 0 {
		width = constraints.MaxW
	}
	height := v.Height
	if height <= 0 {
		height = constraints.MaxH
	}
	return constraints.Clamp(Size{W: width, H: height})
}

func (v TranscriptViewport) Render(ctx *Context, bounds Rect) Surface {
	if v.Transcript == nil {
		return BlankSurface(bounds.W, bounds.H)
	}
	surface, _, _ := v.Transcript.RenderVisible(ctx, bounds.W, bounds.H, v.OffsetY)
	return surface.normalize(bounds.W, bounds.H)
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
	line.WriteText(0, 0, i.Indicator, CellStyle{FG: cellColor(i.Palette.ActivityText), Bold: true})
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
		rendered = appendSurfaceRows(rendered, 0, FilledLineSurface(width, bar, CellStyle{BG: cellColor(m.Palette.UserTextBackground), FG: cellColor(m.Palette.UserAccentBar)}, CellStyle{BG: cellColor(m.Palette.UserTextBackground)}))
	}
	for idx, line := range lines {
		row := idx + 1
		rendered.WriteText(0, row, bar, CellStyle{BG: cellColor(m.Palette.UserTextBackground), FG: cellColor(m.Palette.UserAccentBar)})
		if stampStart >= 0 && idx >= stampStart {
			rendered.WriteText(lipgloss.Width(bar), row, line, CellStyle{BG: cellColor(m.Palette.UserTextBackground), FG: cellColor(m.Palette.UserTimestampForeground)})
			continue
		}
		rendered.WriteText(lipgloss.Width(bar), row, line, CellStyle{BG: cellColor(m.Palette.UserTextBackground), FG: cellColor(m.Palette.UserTextForeground)})
	}
	if m.HalfBlocks {
		rendered = appendSurfaceRows(rendered, height-1, renderHalfBlockSurface(width, "▀", m.Palette))
	} else {
		rendered = appendSurfaceRows(rendered, height-1, FilledLineSurface(width, bar, CellStyle{BG: cellColor(m.Palette.UserTextBackground), FG: cellColor(m.Palette.UserAccentBar)}, CellStyle{BG: cellColor(m.Palette.UserTextBackground)}))
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
	Body       string
	StyledBody []StyledSpan
	BaseStyle  CellStyle
	Stamp      string
	Width      int
	Palette    theme.Palette
}

func (m AssistantMessage) Measure(_ *Context, constraints Constraints) Size {
	return constraints.Clamp(m.render().Size())
}

func (m AssistantMessage) Render(_ *Context, bounds Rect) Surface {
	return m.render().normalize(bounds.W, bounds.H)
}

func (m AssistantMessage) render() Surface {
	lines := make([][]StyledSpan, 0, 1)
	if m.Stamp != "" {
		lines = append(lines, []StyledSpan{{
			Text:  m.Stamp,
			Style: CellStyle{FG: cellColor(m.Palette.AssistantTimestampText)},
		}})
	}
	body := m.StyledBody
	if len(body) == 0 {
		body = []StyledSpan{{Text: strings.TrimSpace(m.Body)}}
	}
	baseStyle := m.BaseStyle
	if baseStyle.isZero() {
		baseStyle = CellStyle{FG: cellColor(m.Palette.MarkdownText)}
	}
	for _, line := range WrapStyledText(body, m.Width) {
		lines = append(lines, line)
	}
	width := 0
	for _, line := range lines {
		width = maxInt(width, StyledTextWidth(line))
	}
	s := BlankSurface(width, len(lines))
	for y, line := range lines {
		s.WriteStyledSpans(0, y, line, baseStyle)
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
	style := CellStyle{BG: cellColor(b.Palette.ReasoningBackground), FG: cellColor(b.Palette.ReasoningText)}
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
	s.WriteText(0, 0, char, CellStyle{FG: cellColor(palette.UserAccentBar)})
	if width > 1 {
		fillStyle := CellStyle{FG: cellColor(palette.UserTextBackground)}
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
