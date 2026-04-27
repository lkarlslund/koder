package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/theme"
	"github.com/lkarlslund/koder/internal/ui"
)

type transcriptItemController interface {
	Key() string
	GapBefore() int
	SetGapBefore(int)
	UIItem() ui.TranscriptItem
	Refresh(*Model)
	Invalidate()
}

type transcriptItemBase struct {
	key   string
	gap   int
	cache *ui.CachedElement
}

func newTranscriptItemBase(key string, gap int) transcriptItemBase {
	return transcriptItemBase{
		key:   key,
		gap:   gap,
		cache: ui.NewCachedElement(nil, 1),
	}
}

func (b *transcriptItemBase) Key() string             { return b.key }
func (b *transcriptItemBase) GapBefore() int          { return b.gap }
func (b *transcriptItemBase) SetGapBefore(gap int)    { b.gap = max(0, gap) }
func (b *transcriptItemBase) Invalidate()             { b.cache.InvalidateCache() }
func (b *transcriptItemBase) setElement(e ui.Element) { b.cache.SetChild(e) }
func (b *transcriptItemBase) UIItem() ui.TranscriptItem {
	return ui.TranscriptItem{Key: b.key, Element: b.cache, GapBefore: b.gap}
}

type placeholderTranscriptItem struct {
	transcriptItemBase
	text string
}

func newPlaceholderTranscriptItem(key string, gap int, text string) *placeholderTranscriptItem {
	return &placeholderTranscriptItem{
		transcriptItemBase: newTranscriptItemBase(key, gap),
		text:               text,
	}
}

func (i *placeholderTranscriptItem) Refresh(_ *Model) {
	i.setElement(ui.Paragraph{Text: i.text})
}

type userMessageTranscriptItem struct {
	transcriptItemBase
	message domain.Message
	parts   []domain.Part
}

func newUserMessageTranscriptItem(key string, gap int, msg domain.Message, parts []domain.Part) *userMessageTranscriptItem {
	return &userMessageTranscriptItem{
		transcriptItemBase: newTranscriptItemBase(key, gap),
		message:            msg,
		parts:              cloneParts(parts),
	}
}

func (i *userMessageTranscriptItem) Update(msg domain.Message, parts []domain.Part) {
	i.message = msg
	i.parts = cloneParts(parts)
}

func (i *userMessageTranscriptItem) Refresh(m *Model) {
	body := m.renderUserMessageParts(i.parts)
	if strings.TrimSpace(body) == "" {
		body = strings.TrimSpace(i.message.Summary)
	}
	i.setElement(m.renderUserMessageElement(body, timestamp(i.message.CreatedAt, m.cfg.UI.ShowTimestamps)))
}

type assistantMessageTranscriptItem struct {
	transcriptItemBase
	message       domain.Message
	parts         []domain.Part
	showReasoning bool
	showSystem    bool
}

func newAssistantMessageTranscriptItem(key string, gap int, msg domain.Message, parts []domain.Part, showReasoning, showSystem bool) *assistantMessageTranscriptItem {
	return &assistantMessageTranscriptItem{
		transcriptItemBase: newTranscriptItemBase(key, gap),
		message:            msg,
		parts:              cloneParts(parts),
		showReasoning:      showReasoning,
		showSystem:         showSystem,
	}
}

func (i *assistantMessageTranscriptItem) Update(msg domain.Message, parts []domain.Part) {
	i.message = msg
	i.parts = cloneParts(parts)
}

func (i *assistantMessageTranscriptItem) SetReasoningVisible(v bool) { i.showReasoning = v }
func (i *assistantMessageTranscriptItem) SetSystemVisible(v bool)    { i.showSystem = v }

func (i *assistantMessageTranscriptItem) Refresh(m *Model) {
	origReasoning, origSystem := m.showReasoning, m.showSystem
	m.showReasoning, m.showSystem = i.showReasoning, i.showSystem
	defer func() {
		m.showReasoning, m.showSystem = origReasoning, origSystem
	}()
	i.setElement(m.renderTranscriptMessageElement(i.message, i.parts))
}

type pendingAssistantTranscriptItem struct {
	transcriptItemBase
	createdAt     time.Time
	text          string
	reasoning     string
	showReasoning bool
}

func newPendingAssistantTranscriptItem(gap int, createdAt time.Time, showReasoning bool) *pendingAssistantTranscriptItem {
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	return &pendingAssistantTranscriptItem{
		transcriptItemBase: newTranscriptItemBase("pending-assistant", gap),
		createdAt:          createdAt,
		showReasoning:      showReasoning,
	}
}

func (i *pendingAssistantTranscriptItem) SetReasoningVisible(v bool)  { i.showReasoning = v }
func (i *pendingAssistantTranscriptItem) AppendText(text string)      { i.text += text }
func (i *pendingAssistantTranscriptItem) AppendReasoning(text string) { i.reasoning += text }
func (i *pendingAssistantTranscriptItem) Reset(createdAt time.Time, text, reasoning string) {
	i.createdAt = createdAt
	i.text = text
	i.reasoning = reasoning
}

func (i *pendingAssistantTranscriptItem) Parts() []domain.Part {
	var parts []domain.Part
	if strings.TrimSpace(i.reasoning) != "" {
		parts = append(parts, domain.Part{ID: -1, Kind: domain.PartKindReasoning, Body: i.reasoning})
	}
	if strings.TrimSpace(i.text) != "" {
		parts = append(parts, domain.Part{ID: -2, Kind: domain.PartKindText, Body: i.text})
	}
	return parts
}

func (i *pendingAssistantTranscriptItem) Refresh(m *Model) {
	msg := domain.Message{Role: domain.MessageRoleAssistant, CreatedAt: i.createdAt}
	parts := i.Parts()
	origReasoning := m.showReasoning
	m.showReasoning = i.showReasoning
	defer func() { m.showReasoning = origReasoning }()
	i.setElement(m.renderTranscriptMessageElement(msg, parts))
}

type toolRunTranscriptItem interface {
	transcriptItemController
	RunID() string
	UpdateRun(ui.ToolRun)
	SetExpandedOutput(bool)
	SetExpandedCommand(bool)
	ToggleOutput()
	ToggleCommand()
}

type toolRunItemBase struct {
	transcriptItemBase
	run             ui.ToolRun
	expandedOutput  bool
	expandedCommand bool
}

func newToolRunItemBase(key string, gap int, run ui.ToolRun, expandedOutput, expandedCommand bool) toolRunItemBase {
	return toolRunItemBase{
		transcriptItemBase: newTranscriptItemBase(key, gap),
		run:                run,
		expandedOutput:     expandedOutput,
		expandedCommand:    expandedCommand,
	}
}

func (i *toolRunItemBase) RunID() string             { return i.run.ID }
func (i *toolRunItemBase) UpdateRun(run ui.ToolRun)  { i.run = run }
func (i *toolRunItemBase) SetExpandedOutput(v bool)  { i.expandedOutput = v }
func (i *toolRunItemBase) SetExpandedCommand(v bool) { i.expandedCommand = v }
func (i *toolRunItemBase) ToggleOutput()             { i.expandedOutput = !i.expandedOutput }
func (i *toolRunItemBase) ToggleCommand()            { i.expandedCommand = !i.expandedCommand }

type bashToolRunTranscriptItem struct{ toolRunItemBase }
type readToolRunTranscriptItem struct{ toolRunItemBase }
type writeToolRunTranscriptItem struct{ toolRunItemBase }
type editToolRunTranscriptItem struct{ toolRunItemBase }
type genericToolRunTranscriptItem struct{ toolRunItemBase }

func newToolRunTranscriptItem(gap int, run ui.ToolRun, expandedOutput, expandedCommand bool) toolRunTranscriptItem {
	key := "toolrun:" + firstNonEmptyToolRunKey(run)
	base := newToolRunItemBase(key, gap, run, expandedOutput, expandedCommand)
	switch run.Tool {
	case domain.ToolKindBash:
		return &bashToolRunTranscriptItem{toolRunItemBase: base}
	case domain.ToolKindRead:
		return &readToolRunTranscriptItem{toolRunItemBase: base}
	case domain.ToolKindWrite:
		return &writeToolRunTranscriptItem{toolRunItemBase: base}
	case domain.ToolKindEdit:
		return &editToolRunTranscriptItem{toolRunItemBase: base}
	default:
		return &genericToolRunTranscriptItem{toolRunItemBase: base}
	}
}

func firstNonEmptyToolRunKey(run ui.ToolRun) string {
	switch {
	case strings.TrimSpace(run.ID) != "":
		return run.ID
	case run.ApprovalID > 0:
		return fmt.Sprintf("approval:%d", run.ApprovalID)
	case strings.TrimSpace(run.ToolCallID) != "":
		return "call:" + run.ToolCallID
	default:
		return toolRunFallbackID(run.Tool, run.Preview)
	}
}

type bashToolRunCardElement struct {
	Run             ui.ToolRun
	Palette         theme.Palette
	Width           int
	ExpandedOutput  bool
	ExpandedCommand bool
}

type readToolRunCardElement struct {
	Run            ui.ToolRun
	Palette        theme.Palette
	Width          int
	ExpandedOutput bool
}

type writeToolRunCardElement struct {
	Run            ui.ToolRun
	Palette        theme.Palette
	Width          int
	ExpandedOutput bool
}

type editToolRunCardElement struct {
	Run            ui.ToolRun
	Palette        theme.Palette
	Width          int
	ExpandedOutput bool
}

type genericToolRunCardElement struct {
	Run             ui.ToolRun
	Palette         theme.Palette
	Width           int
	ExpandedOutput  bool
	ExpandedCommand bool
}

func (e bashToolRunCardElement) Measure(_ *ui.Context, c ui.Constraints) ui.Size {
	width := e.Width
	if width <= 0 {
		width = c.MaxW
	}
	return c.Clamp(e.Run.CardSurface(e.Palette, width, e.ExpandedOutput, e.ExpandedCommand).Size())
}
func (e bashToolRunCardElement) Render(ctx *ui.Context, bounds ui.Rect) ui.Surface {
	width := e.Width
	if width <= 0 {
		width = bounds.W
	}
	surface := e.Run.CardSurface(e.Palette, width, e.ExpandedOutput, e.ExpandedCommand)
	if ctx != nil && ctx.Runtime != nil {
		surface.RegisterControls(ctx.Runtime, bounds.X, bounds.Y)
	}
	return surface
}

func (e readToolRunCardElement) Measure(_ *ui.Context, c ui.Constraints) ui.Size {
	width := e.Width
	if width <= 0 {
		width = c.MaxW
	}
	return c.Clamp(e.Run.CardSurface(e.Palette, width, e.ExpandedOutput, false).Size())
}
func (e readToolRunCardElement) Render(ctx *ui.Context, bounds ui.Rect) ui.Surface {
	width := e.Width
	if width <= 0 {
		width = bounds.W
	}
	surface := e.Run.CardSurface(e.Palette, width, e.ExpandedOutput, false)
	if ctx != nil && ctx.Runtime != nil {
		surface.RegisterControls(ctx.Runtime, bounds.X, bounds.Y)
	}
	return surface
}

func (e writeToolRunCardElement) Measure(_ *ui.Context, c ui.Constraints) ui.Size {
	width := e.Width
	if width <= 0 {
		width = c.MaxW
	}
	return c.Clamp(e.Run.CardSurface(e.Palette, width, e.ExpandedOutput, false).Size())
}
func (e writeToolRunCardElement) Render(ctx *ui.Context, bounds ui.Rect) ui.Surface {
	width := e.Width
	if width <= 0 {
		width = bounds.W
	}
	surface := e.Run.CardSurface(e.Palette, width, e.ExpandedOutput, false)
	if ctx != nil && ctx.Runtime != nil {
		surface.RegisterControls(ctx.Runtime, bounds.X, bounds.Y)
	}
	return surface
}

func (e editToolRunCardElement) Measure(_ *ui.Context, c ui.Constraints) ui.Size {
	width := e.Width
	if width <= 0 {
		width = c.MaxW
	}
	return c.Clamp(e.Run.CardSurface(e.Palette, width, e.ExpandedOutput, false).Size())
}
func (e editToolRunCardElement) Render(ctx *ui.Context, bounds ui.Rect) ui.Surface {
	width := e.Width
	if width <= 0 {
		width = bounds.W
	}
	surface := e.Run.CardSurface(e.Palette, width, e.ExpandedOutput, false)
	if ctx != nil && ctx.Runtime != nil {
		surface.RegisterControls(ctx.Runtime, bounds.X, bounds.Y)
	}
	return surface
}

func (e genericToolRunCardElement) Measure(_ *ui.Context, c ui.Constraints) ui.Size {
	width := e.Width
	if width <= 0 {
		width = c.MaxW
	}
	return c.Clamp(e.Run.CardSurface(e.Palette, width, e.ExpandedOutput, e.ExpandedCommand).Size())
}
func (e genericToolRunCardElement) Render(ctx *ui.Context, bounds ui.Rect) ui.Surface {
	width := e.Width
	if width <= 0 {
		width = bounds.W
	}
	surface := e.Run.CardSurface(e.Palette, width, e.ExpandedOutput, e.ExpandedCommand)
	if ctx != nil && ctx.Runtime != nil {
		surface.RegisterControls(ctx.Runtime, bounds.X, bounds.Y)
	}
	return surface
}

func (i *bashToolRunTranscriptItem) Refresh(m *Model) {
	i.setElement(bashToolRunCardElement{Run: i.run, Palette: m.palette, Width: m.viewport.Width, ExpandedOutput: i.expandedOutput, ExpandedCommand: i.expandedCommand})
}
func (i *readToolRunTranscriptItem) Refresh(m *Model) {
	i.setElement(readToolRunCardElement{Run: i.run, Palette: m.palette, Width: m.viewport.Width, ExpandedOutput: i.expandedOutput})
}
func (i *writeToolRunTranscriptItem) Refresh(m *Model) {
	i.setElement(writeToolRunCardElement{Run: i.run, Palette: m.palette, Width: m.viewport.Width, ExpandedOutput: i.expandedOutput})
}
func (i *editToolRunTranscriptItem) Refresh(m *Model) {
	i.setElement(editToolRunCardElement{Run: i.run, Palette: m.palette, Width: m.viewport.Width, ExpandedOutput: i.expandedOutput})
}
func (i *genericToolRunTranscriptItem) Refresh(m *Model) {
	i.setElement(genericToolRunCardElement{Run: i.run, Palette: m.palette, Width: m.viewport.Width, ExpandedOutput: i.expandedOutput, ExpandedCommand: i.expandedCommand})
}

func cloneParts(parts []domain.Part) []domain.Part {
	if len(parts) == 0 {
		return nil
	}
	out := make([]domain.Part, len(parts))
	copy(out, parts)
	return out
}
