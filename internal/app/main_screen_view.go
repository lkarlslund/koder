package app

import (
	"hash/fnv"
	"strconv"

	"github.com/lkarlslund/koder/internal/tui"
	"github.com/lkarlslund/koder/internal/ui"
)

const composerBlinkTimerOwner = tui.ComposerBlinkTimerOwner

type mainScreenView struct {
	transcript       *tui.ChatTranscriptNode
	composer         *tui.ComposerNode
	sidebar          *ui.HashedNode
	statusPane       *ui.HashedNode
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
		transcript: tui.NewChatTranscriptNode(),
		composer:   tui.NewComposerNode(),
		sidebar:    ui.NewHashedNode(nil, 0),
		statusPane: ui.NewHashedNode(nil, 0),
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

func (m *Model) ensureMainScreenView() *mainScreenView {
	if m.mainScreen == nil {
		m.mainScreen = newMainScreenView()
	}
	m.syncMainScreenViewState()
	return m.mainScreen
}

func (m *Model) syncMainScreenViewState() {
	if m.mainScreen == nil {
		return
	}
	sidebarContent := m.renderSidebar()
	statusLine := ""
	if m.busy.transcriptActive() {
		statusLine = ui.WorkingIndicatorLine(m.workingIndicator(), m.busy.statusOrDefault("Working ..."))
	}
	sidebarWidth := m.sidebarWidth()
	sidebarHash := hashStrings(
		strconv.Itoa(sidebarWidth),
		strconv.Itoa(m.viewport.Height),
		strconv.FormatBool(m.showSidebar),
		sidebarContent,
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
		AreaElement:       m.renderComposerAreaElementWithCursor(true),
		AreaElementHidden: m.renderComposerAreaElementWithCursor(false),
		Element:           m.renderComposerElementWithCursor(true),
		ElementHidden:     m.renderComposerElementWithCursor(false),
		Revision:          m.composer.Revision(),
		CursorDirty:       m.composerCursorDirty,
		Focused:           m.composer.Focused(),
		BlinkEnabled:      m.composer.BlinkEnabled && !m.hasModalOverlay(),
	})
	sidebarNode := ui.Node(nil)
	if m.showSidebar {
		sidebarNode = ui.AsNode(ui.Sidebar{
			Child:  ui.AsNode(ui.TextPane{Content: sidebarContent}),
			Height: m.viewport.Height,
			Width:  sidebarWidth,
		})
	}
	m.mainScreen.SetSidebar(m.showSidebar, sidebarWidth, sidebarNode, sidebarHash)
	m.mainScreen.SetStatusPane(statusPaneHeight, m.renderStatusPaneElement(), statusHash)
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
func (v *mainScreenView) SyncComposerBlinkTimer(root *ui.Root) {
	v.composer.SyncBlinkTimer(root)
}
func (v *mainScreenView) HandleComposerTimer(event ui.TimerEvent) bool {
	return v.composer.HandleTimer(event)
}
func (v *mainScreenView) ComposerDirty() bool { return v.composer.Dirty() }
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
