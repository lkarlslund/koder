package ui

import (
	"fmt"
	"strings"

	"github.com/lkarlslund/koder/internal/theme"
)

type TranscriptItem struct {
	Key       string
	Node      Node
	GapBefore int
}

type Transcript struct {
	Items []TranscriptItem
}

type RetainedTranscript struct {
	items            []TranscriptItem
	layoutWidth      int
	itemHeights      []int
	totalHeight      int
	totalHeightValid bool
}

type transcriptViewportNode interface {
	ApproxHeight(width int) int
	RenderCached(ctx *Context, width int) Surface
}

type CachedElement struct {
	BaseNode
	Child      Node
	HeightHint int
	width      int
	surface    Surface
	cached     bool
}

func NewCachedElement(child Node, heightHint int) *CachedElement {
	return &CachedElement{Child: child, HeightHint: max(0, heightHint)}
}

func (e *CachedElement) ApproxHeight(width int) int {
	if e == nil {
		return 0
	}
	if e.cached && e.width == width {
		return e.surface.Size().H
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
	if e.cached && e.width == width {
		if ctx != nil && ctx.Runtime != nil {
			e.surface.RegisterControls(ctx.Runtime, 0, 0)
		}
		return e.surface
	}
	size := e.Child.Measure(ctx, NewConstraints(width, 0))
	surface := PaintNodeSurface(withoutRuntime(ctx), e.Child, Rect{W: width, H: size.H})
	if ctx != nil && ctx.Runtime != nil {
		surface.RegisterControls(ctx.Runtime, 0, 0)
	}
	e.width = width
	e.surface = surface
	e.cached = true
	return surface
}

func (e *CachedElement) Measure(ctx *Context, constraints Constraints) Size {
	surface := e.RenderCached(ctx, constraints.MaxW)
	return constraints.Clamp(surface.Size())
}

func (e *CachedElement) Paint(ctx *Context, canvas Canvas) {
	if e == nil || canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	canvas.BlitSurface(0, 0, e.RenderCached(ctx, canvas.Width()).normalize(canvas.Width(), canvas.Height()))
}

func (e *CachedElement) InvalidateCache() {
	if e == nil {
		return
	}
	e.width = 0
	e.surface = Surface{}
	e.cached = false
}

func (e *CachedElement) SetChild(child Node) {
	if e == nil {
		return
	}
	e.Child = child
	e.InvalidateCache()
}

func (e *CachedElement) ChildNodes() []Node {
	if e == nil || e.Child == nil {
		return nil
	}
	return []Node{e.Child}
}

func NewRetainedTranscript() *RetainedTranscript {
	return &RetainedTranscript{}
}

func (t *RetainedTranscript) SetItems(items []TranscriptItem) {
	t.items = append(t.items[:0], items...)
	t.invalidateLayout()
}

func (t *RetainedTranscript) Reconcile(items []TranscriptItem) {
	if t == nil {
		return
	}
	if len(items) == 0 {
		t.items = nil
		t.invalidateLayout()
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
			if item.Node == nil {
				item.Node = existing.Node
			}
			if item.GapBefore == 0 {
				item.GapBefore = existing.GapBefore
			}
		}
		next = append(next, item)
	}
	t.items = next
	t.invalidateLayout()
}

func (t *RetainedTranscript) Add(item TranscriptItem) {
	t.items = append(t.items, item)
	t.appendHeight(item)
}

func (t *RetainedTranscript) Insert(index int, item TranscriptItem) {
	index = max(0, min(index, len(t.items)))
	t.items = append(t.items[:index], append([]TranscriptItem{item}, t.items[index:]...)...)
	t.insertHeight(index, item)
}

func (t *RetainedTranscript) Remove(index int) {
	if index < 0 || index >= len(t.items) {
		return
	}
	t.removeHeight(index)
	t.items = append(t.items[:index], t.items[index+1:]...)
}

func (t *RetainedTranscript) Replace(index int, item TranscriptItem) {
	if index < 0 || index >= len(t.items) {
		return
	}
	t.replaceHeight(index, item)
	t.items[index] = item
}

func (t *RetainedTranscript) Clear() {
	t.items = nil
	t.invalidateLayout()
}

func (t *RetainedTranscript) Items() []TranscriptItem {
	out := make([]TranscriptItem, len(t.items))
	copy(out, t.items)
	return out
}

func (t *RetainedTranscript) Measure(ctx *Context, constraints Constraints) Size {
	width := constraints.MaxW
	if width <= 0 {
		return Transcript{Items: t.items}.Measure(ctx, constraints)
	}
	return constraints.Clamp(Size{W: width, H: t.ContentHeight(width)})
}

func (t *RetainedTranscript) Paint(ctx *Context, canvas Canvas) {
	if t == nil || canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	t.RenderVisibleInto(ctx, canvas.Width(), canvas.Height(), 0, canvas.surface)
}

func (t *RetainedTranscript) ContentHeight(width int) int {
	return t.exactContentHeight(nil, width)
}

func (t *RetainedTranscript) RenderVisible(ctx *Context, width, height, offset int) (Surface, int, int) {
	base := BlankSurface(width, height)
	totalHeight, appliedOffset := t.RenderVisibleInto(ctx, width, height, offset, &base)
	return base, totalHeight, appliedOffset
}

func (t *RetainedTranscript) RenderBottom(ctx *Context, width, height int) (Surface, int, int) {
	measureCtx := withoutRuntime(ctx)
	base := BlankSurface(width, height)
	totalHeight, offset := t.RenderBottomInto(measureCtx, width, height, &base)
	return base, totalHeight, offset
}

func (t *RetainedTranscript) RenderVisibleInto(ctx *Context, width, height, offset int, dst *Surface) (int, int) {
	measureCtx := withoutRuntime(ctx)
	totalHeight := t.exactContentHeight(measureCtx, width)
	maxOffset := max(0, totalHeight-max(0, height))
	offset = min(max(0, offset), maxOffset)
	if dst == nil {
		return totalHeight, offset
	}
	y := 0
	for idx, item := range t.items {
		gap := max(0, item.GapBefore)
		top := y + gap
		surface := t.itemSurfaceAt(measureCtx, idx, item, width)
		exactHeight := surface.Size().H
		bottom := top + exactHeight
		y = bottom
		if item.Node == nil || bottom <= offset || top >= offset+height || exactHeight <= 0 {
			continue
		}
		renderY := top - offset
		if ctx != nil && ctx.Runtime != nil {
			surface.RegisterControls(ctx.Runtime, 0, renderY)
		}
		*dst = dst.placeAt(0, renderY, surface)
	}
	return totalHeight, offset
}

func (t *RetainedTranscript) RenderBottomInto(ctx *Context, width, height int, dst *Surface) (int, int) {
	if len(t.items) == 0 {
		return 0, 0
	}
	measureCtx := withoutRuntime(ctx)
	totalHeight := t.exactContentHeight(measureCtx, width)
	offset := max(0, totalHeight-max(0, height))
	if dst == nil {
		return totalHeight, offset
	}
	y := 0
	for idx, item := range t.items {
		gap := max(0, item.GapBefore)
		top := y + gap
		surface := t.itemSurfaceAt(measureCtx, idx, item, width)
		exactHeight := surface.Size().H
		bottom := top + exactHeight
		y = bottom
		if item.Node == nil || exactHeight <= 0 || bottom <= offset || top >= offset+height {
			continue
		}
		renderY := top - offset
		if ctx != nil && ctx.Runtime != nil {
			surface.RegisterControls(ctx.Runtime, 0, renderY)
		}
		*dst = dst.placeAt(0, renderY, surface)
	}
	return totalHeight, offset
}

func (t *RetainedTranscript) exactContentHeight(ctx *Context, width int) int {
	t.ensureWidth(width)
	if t.totalHeightValid {
		return t.totalHeight
	}
	total := 0
	for idx, item := range t.items {
		total += max(0, item.GapBefore)
		total += t.itemHeightAt(ctx, idx, item, width)
	}
	t.totalHeight = total
	t.totalHeightValid = true
	return total
}

func withoutRuntime(ctx *Context) *Context {
	if ctx == nil {
		return nil
	}
	if ctx.Runtime == nil {
		return ctx
	}
	copy := *ctx
	copy.Runtime = nil
	return &copy
}

func (t *RetainedTranscript) itemApproxHeight(item TranscriptItem, width int) int {
	if item.Node == nil {
		return 0
	}
	if cached, ok := item.Node.(transcriptViewportNode); ok {
		return cached.ApproxHeight(width)
	}
	return item.Node.Measure(nil, NewConstraints(width, 0)).H
}

func (t *RetainedTranscript) itemSurfaceAt(ctx *Context, index int, item TranscriptItem, width int) Surface {
	t.ensureWidth(width)
	if item.Node == nil {
		return Surface{}
	}
	if cached, ok := item.Node.(transcriptViewportNode); ok {
		surface := cached.RenderCached(ctx, width)
		if index >= 0 && index < len(t.itemHeights) {
			t.itemHeights[index] = surface.Size().H
		}
		return surface
	}
	size := item.Node.Measure(ctx, NewConstraints(width, 0))
	surface := PaintNodeSurface(withoutRuntime(ctx), item.Node, Rect{W: width, H: size.H})
	if index >= 0 && index < len(t.itemHeights) {
		t.itemHeights[index] = surface.Size().H
	}
	return surface
}

func (t *RetainedTranscript) itemHeightAt(ctx *Context, index int, item TranscriptItem, width int) int {
	t.ensureWidth(width)
	if index >= 0 && index < len(t.itemHeights) && t.itemHeights[index] >= 0 {
		return t.itemHeights[index]
	}
	return t.itemSurfaceAt(ctx, index, item, width).Size().H
}

func (t *RetainedTranscript) ensureWidth(width int) {
	width = max(0, width)
	if t.layoutWidth == width && len(t.itemHeights) == len(t.items) {
		return
	}
	t.layoutWidth = width
	t.itemHeights = make([]int, len(t.items))
	for i := range t.itemHeights {
		t.itemHeights[i] = -1
	}
	t.totalHeight = 0
	t.totalHeightValid = false
	for _, item := range t.items {
		InvalidateNodeCaches(nil, item.Node)
	}
}

func (t *RetainedTranscript) invalidateLayout() {
	t.layoutWidth = 0
	t.itemHeights = nil
	t.totalHeight = 0
	t.totalHeightValid = false
}

func (t *RetainedTranscript) appendHeight(item TranscriptItem) {
	if !t.totalHeightValid || len(t.itemHeights) != len(t.items)-1 {
		t.invalidateLayout()
		return
	}
	t.itemHeights = append(t.itemHeights, -1)
	t.totalHeightValid = false
}

func (t *RetainedTranscript) insertHeight(index int, item TranscriptItem) {
	if !t.totalHeightValid || index < 0 || index > len(t.itemHeights) || len(t.itemHeights) != len(t.items)-1 {
		t.invalidateLayout()
		return
	}
	t.itemHeights = append(t.itemHeights, 0)
	copy(t.itemHeights[index+1:], t.itemHeights[index:])
	t.itemHeights[index] = -1
	t.totalHeightValid = false
}

func (t *RetainedTranscript) removeHeight(index int) {
	if !t.totalHeightValid || index < 0 || index >= len(t.items) || len(t.itemHeights) != len(t.items) {
		t.invalidateLayout()
		return
	}
	height := 0
	height = t.itemHeights[index]
	if height < 0 {
		height = t.itemApproxHeight(t.items[index], t.layoutWidth)
	}
	copy(t.itemHeights[index:], t.itemHeights[index+1:])
	t.itemHeights = t.itemHeights[:len(t.itemHeights)-1]
	t.totalHeight -= max(0, t.items[index].GapBefore) + max(0, height)
}

func (t *RetainedTranscript) replaceHeight(index int, item TranscriptItem) {
	if !t.totalHeightValid || index < 0 || index >= len(t.items) || index >= len(t.itemHeights) {
		t.invalidateLayout()
		return
	}
	InvalidateNodeCaches(nil, item.Node)
	t.itemHeights[index] = -1
	t.totalHeightValid = false
}

type TranscriptViewport struct {
	BaseNode
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

func (v TranscriptViewport) Paint(ctx *Context, canvas Canvas) {
	if canvas.Width() <= 0 || canvas.Height() <= 0 || v.Transcript == nil {
		return
	}
	surface, _, _ := v.Transcript.RenderVisible(ctx, canvas.Width(), canvas.Height(), v.OffsetY)
	canvas.BlitSurface(0, 0, surface.Normalize(canvas.Width(), canvas.Height()))
}

func (t Transcript) Measure(ctx *Context, constraints Constraints) Size {
	maxW := 0
	totalH := 0
	for _, item := range t.Items {
		if item.GapBefore > 0 {
			totalH += item.GapBefore
		}
		if item.Node == nil {
			continue
		}
		size := item.Node.Measure(ctx, constraints)
		if size.W > maxW {
			maxW = size.W
		}
		totalH += size.H
	}
	return constraints.Clamp(Size{W: maxW, H: totalH})
}

func (t Transcript) Paint(ctx *Context, canvas Canvas) {
	if canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	y := 0
	for _, item := range t.Items {
		y += max(0, item.GapBefore)
		if item.Node == nil || y >= canvas.Height() {
			continue
		}
		size := item.Node.Measure(ctx, NewConstraints(canvas.Width(), max(0, canvas.Height()-y)))
		if size.H <= 0 {
			continue
		}
		paintNodeInto(ctx, item.Node, Rect{
			X: canvas.origin.X,
			Y: canvas.origin.Y + y,
			W: canvas.Width(),
			H: size.H,
		}, canvas.surface)
		y += size.H
	}
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

func (i ActivityIndicator) Paint(_ *Context, canvas Canvas) {
	if canvas.Width() <= 0 || canvas.Height() <= 0 || strings.TrimSpace(i.Indicator) == "" {
		return
	}
	canvas.WriteText(0, 0, PlainTruncate(i.Indicator, canvas.Width(), ""), CellStyle{FG: cellColor(i.Palette.ActivityText)}.WithBold(true))
}

func (i ActivityIndicator) render() Surface {
	if strings.TrimSpace(i.Indicator) == "" {
		return Surface{}
	}
	line := BlankSurface(PlainWidth(i.Indicator), 1)
	line.WriteText(0, 0, i.Indicator, CellStyle{FG: cellColor(i.Palette.ActivityText)}.WithBold(true))
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

func (m UserMessage) Paint(_ *Context, canvas Canvas) {
	if canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	bar := m.PromptGlyph + " "
	bg := cellColor(m.Palette.UserTextBackground)
	barStyle := CellStyle{BG: bg, FG: cellColor(m.Palette.UserAccentBar)}
	textStyle := CellStyle{BG: bg, FG: cellColor(m.Palette.UserTextForeground)}
	timestampStyle := CellStyle{BG: bg, FG: cellColor(m.Palette.UserTimestampForeground)}
	content := strings.TrimSpace(m.Body)
	contentWidth := maxInt(1, canvas.Width()-TextWidth(bar))
	lines := []string{}
	if content != "" {
		for _, line := range strings.Split(content, "\n") {
			lines = append(lines, WrapUserMessageLine(line, contentWidth)...)
		}
	}
	stampStart := -1
	if m.Stamp != "" {
		stampLines := WrapUserMessageLine(m.Stamp, contentWidth)
		stampStart = len(lines)
		lines = append(lines, stampLines...)
	}
	paintBarLine := func(y int, half string) {
		if y < 0 || y >= canvas.Height() {
			return
		}
		if m.HalfBlocks {
			if half == "" {
				return
			}
			canvas.SetCell(0, y, newCell(GlyphFromString(half), 1, CellStyle{FG: cellColor(m.Palette.UserAccentBar)}))
			for x := 1; x < canvas.Width(); x++ {
				canvas.SetCell(x, y, newCell(GlyphFromString(half), 1, CellStyle{FG: cellColor(m.Palette.UserTextBackground)}))
			}
			return
		}
		canvas.Fill(Rect{Y: y, W: canvas.Width(), H: 1}, CellStyle{BG: bg})
		canvas.WriteText(0, y, PlainTruncate(bar, canvas.Width(), ""), barStyle)
	}
	startRow := 0
	endRow := canvas.Height()
	if m.HalfBlocks {
		startRow = 1
		endRow = max(startRow, canvas.Height()-1)
	}
	for y := startRow; y < endRow; y++ {
		canvas.Fill(Rect{Y: y, W: canvas.Width(), H: 1}, CellStyle{BG: bg})
	}
	paintBarLine(0, "▄")
	for idx, line := range lines {
		row := idx + 1
		if row >= canvas.Height() {
			break
		}
		canvas.WriteText(0, row, PlainTruncate(bar, canvas.Width(), ""), barStyle)
		style := textStyle
		if stampStart >= 0 && idx >= stampStart {
			style = timestampStyle
		}
		if TextWidth(bar) < canvas.Width() {
			canvas.WriteText(TextWidth(bar), row, PlainTruncate(line, canvas.Width()-TextWidth(bar), ""), style)
		}
	}
	paintBarLine(canvas.Height()-1, "▀")
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
	contentWidth := maxInt(1, width-TextWidth(bar))
	innerWidth := contentWidth

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
	fillStyle := CellStyle{BG: cellColor(m.Palette.UserTextBackground)}
	startRow := 0
	endRow := height
	if m.HalfBlocks {
		startRow = 1
		endRow = max(startRow, height-1)
	}
	for y := startRow; y < endRow; y++ {
		for x := 0; x < width; x++ {
			rendered.setCell(x, y, blankCell(fillStyle))
		}
	}
	if m.HalfBlocks {
		rendered = appendSurfaceRows(rendered, 0, renderHalfBlockSurface(width, "▄", m.Palette))
	} else {
		rendered = appendSurfaceRows(rendered, 0, FilledLineSurface(width, bar, CellStyle{BG: cellColor(m.Palette.UserTextBackground), FG: cellColor(m.Palette.UserAccentBar)}, CellStyle{BG: cellColor(m.Palette.UserTextBackground)}))
	}
	for idx, line := range lines {
		row := idx + 1
		rendered.WriteText(0, row, bar, CellStyle{BG: cellColor(m.Palette.UserTextBackground), FG: cellColor(m.Palette.UserAccentBar)})
		if stampStart >= 0 && idx >= stampStart {
			rendered.WriteText(TextWidth(bar), row, line, CellStyle{BG: cellColor(m.Palette.UserTextBackground), FG: cellColor(m.Palette.UserTimestampForeground)})
			continue
		}
		rendered.WriteText(TextWidth(bar), row, line, CellStyle{BG: cellColor(m.Palette.UserTextBackground), FG: cellColor(m.Palette.UserTextForeground)})
	}
	if m.HalfBlocks {
		rendered = appendSurfaceRows(rendered, height-1, renderHalfBlockSurface(width, "▀", m.Palette))
	} else {
		rendered = appendSurfaceRows(rendered, height-1, FilledLineSurface(width, bar, CellStyle{BG: cellColor(m.Palette.UserTextBackground), FG: cellColor(m.Palette.UserAccentBar)}, CellStyle{BG: cellColor(m.Palette.UserTextBackground)}))
	}
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
	width := TextWidth("┃ ") + 2
	for _, line := range lines {
		width = maxInt(width, TextWidth(line)+TextWidth("┃ ")+2)
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

func (m AssistantMessage) Paint(ctx *Context, canvas Canvas) {
	if canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
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
	lines = append(lines, WrapStyledText(body, m.Width)...)
	for y, line := range lines {
		if y >= canvas.Height() {
			break
		}
		col := 0
		for _, span := range line {
			if span.Text == "" {
				continue
			}
			style := baseStyle.Merge(span.Style)
			startCol := col
			canvas.WriteText(col, y, span.Text, style)
			col += PlainWidth(span.Text)
			if ctx == nil || ctx.Runtime == nil || strings.TrimSpace(span.ControlID) == "" || !span.Enabled {
				continue
			}
			left := max(0, startCol)
			right := min(canvas.Width(), col)
			if right <= left {
				continue
			}
			ctx.Runtime.Register(Control{
				ID:      span.ControlID,
				Rect:    Rect{X: canvas.origin.X + left, Y: canvas.origin.Y + y, W: right - left, H: 1},
				Enabled: true,
			})
		}
	}
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
	lines = append(lines, WrapStyledText(body, m.Width)...)
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

func (b ReasoningBlock) Paint(_ *Context, canvas Canvas) {
	if canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	content := strings.TrimSpace(b.Body)
	if content == "" {
		return
	}
	style := CellStyle{BG: cellColor(b.Palette.ReasoningBackground), FG: cellColor(b.Palette.ReasoningText)}.WithItalic(true)
	lines := wrapStyledLines(content, b.Width)
	for y, line := range lines {
		if y >= canvas.Height() {
			break
		}
		canvas.Fill(Rect{Y: y, W: canvas.Width(), H: 1}, style)
		canvas.WriteText(0, y, PlainTruncate(line, canvas.Width(), ""), style)
	}
}

func (b ReasoningBlock) render() Surface {
	content := strings.TrimSpace(b.Body)
	if content == "" {
		return Surface{}
	}
	lines := wrapStyledLines(content, b.Width)
	width := 0
	for _, line := range lines {
		width = maxInt(width, PlainWidth(line))
	}
	s := BlankSurface(width, len(lines))
	style := CellStyle{BG: cellColor(b.Palette.ReasoningBackground), FG: cellColor(b.Palette.ReasoningText)}.WithItalic(true)
	for y, line := range lines {
		for x := 0; x < width; x++ {
			s.setCell(x, y, blankCell(style))
		}
		s.WriteText(0, y, line, style)
	}
	return s
}

func WorkingIndicatorLine(indicator, status string) string {
	if strings.TrimSpace(indicator) == "" {
		return ""
	}
	status = strings.TrimSpace(status)
	if status == "" {
		status = "Working ..."
	}
	return fmt.Sprintf("%s  %s", indicator, status)
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
			s.setCell(x, 0, newCell(GlyphFromString(char), 1, fillStyle))
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
