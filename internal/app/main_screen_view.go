package app

import (
	"hash/fnv"
	"strconv"

	"github.com/lkarlslund/koder/internal/theme"
	"github.com/lkarlslund/koder/internal/tui"
	"github.com/lkarlslund/koder/internal/ui"
)

const composerBlinkTimerOwner = tui.ComposerBlinkTimerOwner

type mainScreenView struct {
	transcript       *tui.ChatTranscriptNode
	composer         *tui.ComposerNode
	sidebar          *ui.HashedNode
	sidebarContent   *ui.RetainedColumn
	sidebarSpinner   *ui.Spinner
	statusPane       *ui.HashedNode
	statusSpinner    *ui.Spinner
	leftMain         *ui.FlexNode
	body             *ui.FlexNode
	root             *ui.FlexNode
	surface          *ui.RetainedSurface
	bodyChildren     [2]ui.FlexNodeChild
	bodyWithoutSide  []ui.FlexNodeChild
	bodyWithSide     []ui.FlexNodeChild
	sidebarWidth     int
	showSidebar      bool
	statusPaneHeight int
}

func newMainScreenView() *mainScreenView {
	view := &mainScreenView{
		transcript:     tui.NewChatTranscriptNode(),
		composer:       tui.NewComposerNode(),
		sidebar:        ui.NewHashedNode(nil, 0),
		sidebarContent: ui.NewRetainedColumn(0),
		sidebarSpinner: &ui.Spinner{},
		statusPane:     ui.NewHashedNode(nil, 0),
		statusSpinner:  &ui.Spinner{},
	}
	view.leftMain = ui.NewFlexNode(ui.DirectionVertical, []ui.FlexNodeChild{
		{Node: view.transcript, Flex: 1},
		{Node: view.composer},
	}, 0)
	view.body = ui.NewFlexNode(ui.DirectionHorizontal, nil, 0)
	view.root = ui.NewFlexNode(ui.DirectionVertical, []ui.FlexNodeChild{
		{Node: view.body, Flex: 1},
		{Node: view.statusPane},
	}, 0)
	view.bodyChildren[0] = ui.FlexNodeChild{Node: view.leftMain, Flex: 1}
	view.bodyChildren[1] = ui.FlexNodeChild{Node: view.sidebar}
	view.bodyWithoutSide = view.bodyChildren[:1]
	view.bodyWithSide = view.bodyChildren[:2]
	view.syncBodyChildren()
	view.surface = ui.NewRetainedSurface(view.root)
	return view
}

func (m *App) ensureMainScreenView() *mainScreenView {
	if m.mainScreen == nil {
		m.mainScreen = newMainScreenView()
	}
	m.syncMainScreenViewState()
	return m.mainScreen
}

func (m *App) syncMainScreenViewState() {
	if m.mainScreen == nil {
		return
	}
	sidebarContent := m.sidebarContent()
	statusLine := ""
	if m.busy.transcriptActive() {
		statusLine = m.transcriptBusyStatus()
	}
	sidebarWidth := m.sidebarWidth()
	sidebarHash := hashStrings(
		strconv.Itoa(sidebarWidth),
		strconv.Itoa(m.viewport.Height),
		strconv.FormatBool(m.showSidebar),
		sidebarContent.hash(),
	)
	statusPaneHeight := m.statusPaneHeight()
	statusHash := hashStrings(strconv.Itoa(m.width), strconv.Itoa(statusPaneHeight), statusLine)
	m.mainScreen.SetTranscriptState(tui.ChatTranscriptState{
		Retained:   m.syncRetainedTranscript(),
		Width:      m.viewport.Width,
		Height:     m.viewport.Height,
		YOffset:    m.viewport.YOffset,
		Background: m.palette.ScreenBackground,
	})
	m.mainScreen.SetComposerState(tui.ComposerState{
		AreaElement:   m.renderComposerAreaElementWithCursor(true),
		Element:       m.renderComposerElementWithCursor(true),
		ElementHidden: m.renderComposerElementWithCursor(false),
		Revision:      m.composer.Revision(),
		CursorDirty:   m.composerCursorDirty,
		Focused:       m.composer.Focused(),
		BlinkEnabled:  m.composer.BlinkEnabled && !m.hasModalOverlay(),
	})
	sidebarNode := ui.Node(nil)
	if m.showSidebar {
		m.mainScreen.SetSidebarContent(sidebarContent, m.cfg.UI.Spinner, m.palette)
		sidebarNode = ui.AsNode(ui.Sidebar{
			Child:  m.mainScreen.sidebarContent,
			Height: m.viewport.Height,
			Width:  sidebarWidth,
		})
	}
	m.mainScreen.SetSidebar(m.showSidebar, sidebarWidth, sidebarNode, sidebarHash)
	m.mainScreen.statusSpinner.Set(m.cfg.UI.Spinner, statusLine, m.busy.transcriptActive(), m.palette)
	statusNode := ui.Node(ui.AsNode(ui.VisibleElement{}))
	if m.busy.transcriptActive() {
		statusNode = m.mainScreen.statusSpinner
	}
	m.mainScreen.SetStatusPane(statusPaneHeight, statusNode, statusHash)
	m.composerCursorDirty = false
}

func (v *mainScreenView) SetTranscriptState(state tui.ChatTranscriptState) {
	if v == nil {
		return
	}
	v.transcript.SetState(state)
}

func (v *mainScreenView) SetComposerState(state tui.ComposerState) {
	if v == nil {
		return
	}
	v.composer.SetState(state)
}

func (v *mainScreenView) SetSidebar(show bool, width int, node ui.Node, hash uint64) {
	if v == nil {
		return
	}
	width = max(0, width)
	if v.showSidebar != show || v.sidebarWidth != width {
		v.showSidebar = show
		v.sidebarWidth = width
		v.syncBodyChildren()
	}
	if show {
		v.sidebar.Set(node, hash)
		return
	}
	v.sidebar.Set(nil, hash)
}

func (v *mainScreenView) SetSidebarContent(content sidebarContent, spinnerStyle string, palette theme.Palette) {
	if v == nil || v.sidebarContent == nil {
		return
	}
	v.sidebarContent.Clear()
	for _, row := range content.rows {
		if row.Kind == sidebarRowStatus && row.Busy {
			v.sidebarSpinner.Set(spinnerStyle, row.Value, true, palette)
			v.sidebarContent.Add(ui.AsNode(ui.NewFlexBox(ui.DirectionHorizontal, []ui.Child{
				ui.Fixed(ui.Label{Text: row.Label}),
				ui.Fixed(v.sidebarSpinner),
			}, 1)))
			continue
		}
		v.sidebarContent.Add(ui.AsNode(ui.Label{Text: row.Text()}))
	}
	if !content.busy() {
		v.sidebarSpinner.Set(spinnerStyle, "", false, palette)
	}
}

func (v *mainScreenView) SetStatusPane(height int, node ui.Node, hash uint64) {
	if v == nil {
		return
	}
	v.statusPaneHeight = max(0, height)
	v.statusPane.Set(node, hash)
}

func (v *mainScreenView) syncBodyChildren() {
	if v == nil || v.body == nil {
		return
	}
	v.bodyChildren[1].Basis = max(0, v.sidebarWidth)
	if v.showSidebar {
		v.body.SetChildren(v.bodyWithSide)
		return
	}
	v.body.SetChildren(v.bodyWithoutSide)
	v.sidebar.Layout(nil, ui.Rect{})
}

func (v *mainScreenView) Surface(ctx *ui.Context, bounds ui.Rect) ui.Surface {
	if v == nil || v.surface == nil {
		return ui.Surface{}
	}
	return v.surface.Surface(ctx, bounds)
}

func (v *mainScreenView) PaintInto(ctx *ui.Context, bounds ui.Rect, dst *ui.Surface) []ui.Rect {
	if v == nil || v.surface == nil || dst == nil {
		return nil
	}
	return v.surface.PaintInto(ctx, bounds, dst)
}

func (v *mainScreenView) Dirty() bool {
	return v == nil || v.surface == nil || v.surface.Dirty()
}

func (v *mainScreenView) Invalidate() {
	if v == nil || v.surface == nil {
		return
	}
	v.surface.Invalidate()
}

func (v *mainScreenView) InvalidateTranscript() { v.transcript.Invalidate() }
func (v *mainScreenView) InvalidateComposer()   { v.composer.Invalidate() }
func (v *mainScreenView) InvalidateSidebar()    { v.sidebar.MarkLayoutDirty() }
func (v *mainScreenView) InvalidateStatusPane() { v.statusPane.MarkLayoutDirty() }
func (v *mainScreenView) SyncTimers(root *ui.Root) {
	if v == nil {
		return
	}
	v.composer.SyncBlinkTimer(root)
	ui.SyncNodeTimers(root, v.root)
}
func (v *mainScreenView) HandleTimer(event ui.TimerEvent) bool {
	if v == nil {
		return false
	}
	if v.composer.HandleTimer(event) {
		return true
	}
	return ui.HandleNodeTimer(v.root, event)
}
func (v *mainScreenView) ComposerDirty() bool { return v.composer.Dirty() }
func (v *mainScreenView) ComposerAreaHeight() int {
	if v == nil || v.composer == nil {
		return 0
	}
	return max(0, v.composer.Rect().H)
}
func (v *mainScreenView) FocusNext() bool {
	return v != nil && v.root.FocusNext()
}
func (v *mainScreenView) FocusPrev() bool {
	return v != nil && v.root.FocusPrev()
}
func (v *mainScreenView) ComposerFocused() bool {
	return v != nil && v.composer.Focused()
}
func (v *mainScreenView) TranscriptWantsWheel(point ui.Point) bool {
	if v == nil {
		return false
	}
	node, ok := v.root.WheelNodeAt(point)
	return ok && node == v.transcript
}
func (v *mainScreenView) TranscriptControlAt(point ui.Point) (ui.Control, bool) {
	return v.transcript.ControlAt(point)
}
func (v *mainScreenView) TranscriptControls() []ui.Control {
	return v.transcript.Controls()
}
func (v *mainScreenView) SidebarBasis() int {
	if v == nil {
		return 0
	}
	return v.sidebarWidth
}

func hashStrings(values ...string) uint64 {
	hasher := fnv.New64a()
	for _, value := range values {
		_, _ = hasher.Write([]byte(value))
		_, _ = hasher.Write([]byte{0})
	}
	return hasher.Sum64()
}
