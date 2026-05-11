package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/appstate"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/theme"
	"github.com/lkarlslund/koder/internal/ui"
)

type transcriptItemController interface {
	Key() string
	GapBefore() int
	SetGapBefore(int)
	UIItem() ui.TranscriptItem
	Refresh(*App)
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

func (b *transcriptItemBase) Key() string          { return b.key }
func (b *transcriptItemBase) GapBefore() int       { return b.gap }
func (b *transcriptItemBase) SetGapBefore(gap int) { b.gap = max(0, gap) }
func (b *transcriptItemBase) Invalidate()          { b.cache.InvalidateCache() }
func (b *transcriptItemBase) setElement(e ui.Node) { b.cache.SetChild(e) }
func (b *transcriptItemBase) UIItem() ui.TranscriptItem {
	return ui.TranscriptItem{Key: b.key, Node: b.cache, GapBefore: b.gap}
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

func (i *placeholderTranscriptItem) Refresh(_ *App) {
	i.setElement(ui.AsNode(ui.Paragraph{Text: i.text}))
}

type userMessageTranscriptItem struct {
	transcriptItemBase
	record *appstate.MessageRecord
	msg    domain.Message
	parts  []domain.Part
}

func newUserMessageTranscriptItem(key string, gap int, record *appstate.MessageRecord) *userMessageTranscriptItem {
	item := &userMessageTranscriptItem{
		transcriptItemBase: newTranscriptItemBase(key, gap),
		record:             record,
	}
	if record != nil {
		item.Bind(record)
	}
	return item
}

func newUserMessageTranscriptItemValue(key string, gap int, msg domain.Message, parts []domain.Part) *userMessageTranscriptItem {
	return &userMessageTranscriptItem{
		transcriptItemBase: newTranscriptItemBase(key, gap),
		msg:                msg,
		parts:              slicesCloneParts(parts),
	}
}

func (i *userMessageTranscriptItem) Bind(record *appstate.MessageRecord) {
	i.record = record
	if record != nil {
		i.msg = record.MessageValue()
		i.parts = record.PartSnapshots()
	}
}

func (i *userMessageTranscriptItem) BindValue(msg domain.Message, parts []domain.Part) {
	i.record = nil
	i.msg = msg
	i.parts = slicesCloneParts(parts)
}

func (i *userMessageTranscriptItem) Refresh(m *App) {
	if i.msg.ID == 0 && strings.TrimSpace(i.msg.Summary) == "" && len(i.parts) == 0 {
		i.setElement(ui.AsNode(ui.Paragraph{Text: ""}))
		return
	}
	renderer := newTranscriptRenderer(m)
	body := renderer.renderUserMessageParts(i.parts)
	if strings.TrimSpace(body) == "" {
		body = strings.TrimSpace(i.msg.Summary)
	}
	i.setElement(renderer.renderUserMessageElement(body, timestamp(i.msg.CreatedAt, m.cfg.UI.ShowTimestamps)))
}

type assistantMessageTranscriptItem struct {
	transcriptItemBase
	record        *appstate.MessageRecord
	msg           domain.Message
	parts         []domain.Part
	showReasoning bool
	showSystem    bool
}

func newAssistantMessageTranscriptItem(key string, gap int, record *appstate.MessageRecord, showReasoning, showSystem bool) *assistantMessageTranscriptItem {
	item := &assistantMessageTranscriptItem{
		transcriptItemBase: newTranscriptItemBase(key, gap),
		record:             record,
		showReasoning:      showReasoning,
		showSystem:         showSystem,
	}
	if record != nil {
		item.Bind(record)
	}
	return item
}

func newAssistantMessageTranscriptItemValue(key string, gap int, msg domain.Message, parts []domain.Part, showReasoning, showSystem bool) *assistantMessageTranscriptItem {
	return &assistantMessageTranscriptItem{
		transcriptItemBase: newTranscriptItemBase(key, gap),
		msg:                msg,
		parts:              slicesCloneParts(parts),
		showReasoning:      showReasoning,
		showSystem:         showSystem,
	}
}

func (i *assistantMessageTranscriptItem) Bind(record *appstate.MessageRecord) {
	i.record = record
	if record != nil {
		i.msg = record.MessageValue()
		i.parts = record.PartSnapshots()
	}
}

func (i *assistantMessageTranscriptItem) BindValue(msg domain.Message, parts []domain.Part) {
	i.record = nil
	i.msg = msg
	i.parts = slicesCloneParts(parts)
}

func (i *assistantMessageTranscriptItem) SetReasoningVisible(v bool) { i.showReasoning = v }
func (i *assistantMessageTranscriptItem) SetSystemVisible(v bool)    { i.showSystem = v }

func (i *assistantMessageTranscriptItem) Refresh(m *App) {
	if i.msg.ID == 0 && strings.TrimSpace(i.msg.Summary) == "" && len(i.parts) == 0 {
		i.setElement(ui.AsNode(ui.Paragraph{Text: ""}))
		return
	}
	renderer := newTranscriptRenderer(m)
	renderer.showReasoning = i.showReasoning
	renderer.showSystem = i.showSystem
	i.setElement(renderer.renderTranscriptMessageElement(i.msg, i.parts))
}

type pendingAssistantTranscriptItem struct {
	transcriptItemBase
	createdAt     time.Time
	text          string
	reasoning     string
	showReasoning bool
	pendingLine   string
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
func (i *pendingAssistantTranscriptItem) Reset(createdAt time.Time, text, reasoning, pendingLine string) {
	i.createdAt = createdAt
	i.text = text
	i.reasoning = reasoning
	i.pendingLine = pendingLine
}

func (i *pendingAssistantTranscriptItem) Parts() []domain.Part {
	var parts []domain.Part
	if strings.TrimSpace(i.reasoning) != "" {
		parts = append(parts, domain.Part{ID: -1, Kind: domain.PartKindReasoning, Payload: domain.ReasoningPayload{Text: i.reasoning}, Body: i.reasoning})
	}
	if strings.TrimSpace(i.text) != "" {
		parts = append(parts, domain.Part{ID: -2, Kind: domain.PartKindText, Payload: domain.TextPayload{Text: i.text}, Body: i.text})
	}
	return parts
}

func (i *pendingAssistantTranscriptItem) Refresh(m *App) {
	msg := domain.Message{Role: domain.MessageRoleAssistant, CreatedAt: i.createdAt}
	parts := i.Parts()
	renderer := newTranscriptRenderer(m)
	renderer.showReasoning = i.showReasoning
	renderer.pendingReasoningLine = i.pendingLine
	i.setElement(renderer.renderTranscriptMessageElement(msg, parts))
}

type toolRunTranscriptItem interface {
	transcriptItemController
	RunID() string
	Bind(*appstate.ToolRunRecord)
	UpdateRun(ui.ToolRun)
	SetExpandedOutput(bool)
	SetExpandedCommand(bool)
	ToggleOutput()
	ToggleCommand()
}

type toolRunItemBase struct {
	transcriptItemBase
	record          *appstate.ToolRunRecord
	run             ui.ToolRun
	expandedOutput  bool
	expandedCommand bool
}

func newToolRunItemBase(key string, gap int, record *appstate.ToolRunRecord, run ui.ToolRun, expandedOutput, expandedCommand bool) toolRunItemBase {
	return toolRunItemBase{
		transcriptItemBase: newTranscriptItemBase(key, gap),
		record:             record,
		run:                run,
		expandedOutput:     expandedOutput,
		expandedCommand:    expandedCommand,
	}
}

func (i *toolRunItemBase) RunID() string { return i.run.ID }
func (i *toolRunItemBase) Bind(record *appstate.ToolRunRecord) {
	i.record = record
	if record != nil {
		i.run = record.RunValue()
	}
}
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

func newToolRunTranscriptItem(gap int, record *appstate.ToolRunRecord, run ui.ToolRun, expandedOutput, expandedCommand bool) toolRunTranscriptItem {
	key := "toolrun:" + firstNonEmptyToolRunKey(run)
	base := newToolRunItemBase(key, gap, record, run, expandedOutput, expandedCommand)
	switch run.Tool {
	case domain.ToolKindBash, domain.ToolKindExecCommand:
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

func partValues(records []*appstate.PartRecord) []domain.Part {
	if len(records) == 0 {
		return nil
	}
	out := make([]domain.Part, 0, len(records))
	for _, record := range records {
		if record == nil {
			continue
		}
		out = append(out, record.Part)
	}
	return out
}

func slicesCloneParts(parts []domain.Part) []domain.Part {
	if len(parts) == 0 {
		return nil
	}
	out := make([]domain.Part, len(parts))
	copy(out, parts)
	return out
}

type bashToolRunCardElement struct {
	ui.PassiveNode
	Run             ui.ToolRun
	Palette         theme.Palette
	Width           int
	ExpandedOutput  bool
	ExpandedCommand bool
}

type readToolRunCardElement struct {
	ui.PassiveNode
	Run            ui.ToolRun
	Palette        theme.Palette
	Width          int
	ExpandedOutput bool
}

type writeToolRunCardElement struct {
	ui.PassiveNode
	Run            ui.ToolRun
	Palette        theme.Palette
	Width          int
	ExpandedOutput bool
}

type editToolRunCardElement struct {
	ui.PassiveNode
	Run            ui.ToolRun
	Palette        theme.Palette
	Width          int
	ExpandedOutput bool
}

type genericToolRunCardElement struct {
	ui.PassiveNode
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
func (e bashToolRunCardElement) Paint(_ *ui.Context, canvas ui.Canvas) {
	if canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	width := e.Width
	if width <= 0 {
		width = canvas.Width()
	}
	canvas.BlitSurface(0, 0, e.Run.CardSurface(e.Palette, width, e.ExpandedOutput, e.ExpandedCommand).Normalize(canvas.Width(), canvas.Height()))
}

func (e readToolRunCardElement) Measure(_ *ui.Context, c ui.Constraints) ui.Size {
	width := e.Width
	if width <= 0 {
		width = c.MaxW
	}
	return c.Clamp(e.Run.CardSurface(e.Palette, width, e.ExpandedOutput, false).Size())
}
func (e readToolRunCardElement) Paint(_ *ui.Context, canvas ui.Canvas) {
	if canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	width := e.Width
	if width <= 0 {
		width = canvas.Width()
	}
	canvas.BlitSurface(0, 0, e.Run.CardSurface(e.Palette, width, e.ExpandedOutput, false).Normalize(canvas.Width(), canvas.Height()))
}

func (e writeToolRunCardElement) Measure(_ *ui.Context, c ui.Constraints) ui.Size {
	width := e.Width
	if width <= 0 {
		width = c.MaxW
	}
	return c.Clamp(e.Run.CardSurface(e.Palette, width, e.ExpandedOutput, false).Size())
}
func (e writeToolRunCardElement) Paint(_ *ui.Context, canvas ui.Canvas) {
	if canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	width := e.Width
	if width <= 0 {
		width = canvas.Width()
	}
	canvas.BlitSurface(0, 0, e.Run.CardSurface(e.Palette, width, e.ExpandedOutput, false).Normalize(canvas.Width(), canvas.Height()))
}

func (e editToolRunCardElement) Measure(_ *ui.Context, c ui.Constraints) ui.Size {
	width := e.Width
	if width <= 0 {
		width = c.MaxW
	}
	return c.Clamp(e.Run.CardSurface(e.Palette, width, e.ExpandedOutput, false).Size())
}
func (e editToolRunCardElement) Paint(_ *ui.Context, canvas ui.Canvas) {
	if canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	width := e.Width
	if width <= 0 {
		width = canvas.Width()
	}
	canvas.BlitSurface(0, 0, e.Run.CardSurface(e.Palette, width, e.ExpandedOutput, false).Normalize(canvas.Width(), canvas.Height()))
}

func (e genericToolRunCardElement) Measure(_ *ui.Context, c ui.Constraints) ui.Size {
	width := e.Width
	if width <= 0 {
		width = c.MaxW
	}
	return c.Clamp(e.Run.CardSurface(e.Palette, width, e.ExpandedOutput, e.ExpandedCommand).Size())
}
func (e genericToolRunCardElement) Paint(_ *ui.Context, canvas ui.Canvas) {
	if canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	width := e.Width
	if width <= 0 {
		width = canvas.Width()
	}
	canvas.BlitSurface(0, 0, e.Run.CardSurface(e.Palette, width, e.ExpandedOutput, e.ExpandedCommand).Normalize(canvas.Width(), canvas.Height()))
}

func (i *bashToolRunTranscriptItem) Refresh(m *App) {
	if i.record != nil {
		i.run = i.record.RunValue()
	}
	i.setElement(ui.AsNode(bashToolRunCardElement{Run: i.run, Palette: m.palette, Width: m.viewport.Width, ExpandedOutput: i.expandedOutput, ExpandedCommand: i.expandedCommand}))
}
func (i *readToolRunTranscriptItem) Refresh(m *App) {
	if i.record != nil {
		i.run = i.record.RunValue()
	}
	i.setElement(ui.AsNode(readToolRunCardElement{Run: i.run, Palette: m.palette, Width: m.viewport.Width, ExpandedOutput: i.expandedOutput}))
}
func (i *writeToolRunTranscriptItem) Refresh(m *App) {
	if i.record != nil {
		i.run = i.record.RunValue()
	}
	i.setElement(ui.AsNode(writeToolRunCardElement{Run: i.run, Palette: m.palette, Width: m.viewport.Width, ExpandedOutput: i.expandedOutput}))
}
func (i *editToolRunTranscriptItem) Refresh(m *App) {
	if i.record != nil {
		i.run = i.record.RunValue()
	}
	i.setElement(ui.AsNode(editToolRunCardElement{Run: i.run, Palette: m.palette, Width: m.viewport.Width, ExpandedOutput: i.expandedOutput}))
}
func (i *genericToolRunTranscriptItem) Refresh(m *App) {
	if i.record != nil {
		i.run = i.record.RunValue()
	}
	i.setElement(ui.AsNode(genericToolRunCardElement{Run: i.run, Palette: m.palette, Width: m.viewport.Width, ExpandedOutput: i.expandedOutput, ExpandedCommand: i.expandedCommand}))
}
