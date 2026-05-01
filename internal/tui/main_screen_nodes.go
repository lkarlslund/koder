package tui

import (
	"github.com/lkarlslund/koder/internal/ui"
	"github.com/lkarlslund/koder/internal/ui/textarea"
)

// ComposerBlinkTimerOwner identifies the composer cursor blink timer.
const ComposerBlinkTimerOwner = "composer"

// ChatTranscriptState describes the current transcript viewport.
type ChatTranscriptState struct {
	Retained   *ui.RetainedTranscript
	Width      int
	Height     int
	YOffset    int
	Background ui.CellColor
}

// ChatTranscriptNode renders a retained transcript viewport.
type ChatTranscriptNode struct {
	ui.BaseNode
	retained   *ui.RetainedTranscript
	width      int
	height     int
	yOffset    int
	background ui.CellColor
	controls   []ui.Control
}

// NewChatTranscriptNode constructs a transcript viewport node.
func NewChatTranscriptNode() *ChatTranscriptNode {
	node := &ChatTranscriptNode{}
	node.MarkLayoutDirty()
	return node
}

// SetState replaces the transcript display state.
func (n *ChatTranscriptNode) SetState(state ChatTranscriptState) {
	if n == nil {
		return
	}
	if n.retained == state.Retained &&
		n.width == max(0, state.Width) &&
		n.height == max(0, state.Height) &&
		n.yOffset == max(0, state.YOffset) &&
		n.background == state.Background {
		return
	}
	n.retained = state.Retained
	n.width = max(0, state.Width)
	n.height = max(0, state.Height)
	n.yOffset = max(0, state.YOffset)
	n.background = state.Background
	n.MarkLayoutDirty()
}

// Invalidate marks the transcript for repaint.
func (n *ChatTranscriptNode) Invalidate() {
	if n == nil {
		return
	}
	n.MarkDirtyLocal(ui.Rect{W: n.Rect().W, H: n.Rect().H})
}

// Measure returns the viewport size requested by app state.
func (n *ChatTranscriptNode) Measure(_ *ui.Context, constraints ui.Constraints) ui.Size {
	if n == nil {
		return constraints.Clamp(ui.Size{})
	}
	return constraints.Clamp(ui.Size{W: n.width, H: n.height})
}

// Prepare records transcript damage and refreshes local controls.
func (n *ChatTranscriptNode) Prepare(ctx *ui.Context) {
	if n == nil {
		return
	}
	rect := n.Rect()
	if rect.Empty() {
		n.controls = nil
		return
	}
	if !n.NeedsPaint() && !n.NeedsLayout() {
		return
	}
	n.refreshControls(ctx, rect)
	n.MarkDirtyLocal(ui.Rect{W: rect.W, H: rect.H})
}

// Paint paints the visible transcript directly into canvas.
func (n *ChatTranscriptNode) Paint(ctx *ui.Context, canvas ui.Canvas) {
	if n == nil || canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	canvas.Fill(ui.Rect{W: canvas.Width(), H: canvas.Height()}, ui.CellStyle{BG: n.background})
	if n.retained == nil {
		n.controls = nil
		return
	}
	runtime := ui.Runtime{}
	renderCtx := ui.Context{}
	if ctx != nil {
		renderCtx = *ctx
	}
	renderCtx.Runtime = &runtime
	n.retained.PaintVisible(&renderCtx, canvas, max(0, n.yOffset))
	n.controls = runtime.Controls()
}

// ControlAt returns the topmost transcript control at point.
func (n *ChatTranscriptNode) ControlAt(point ui.Point) (ui.Control, bool) {
	if n == nil {
		return ui.Control{}, false
	}
	for idx := len(n.controls) - 1; idx >= 0; idx-- {
		control := n.controls[idx]
		if control.Enabled && control.Rect.Contains(point) {
			return control, true
		}
	}
	return ui.Control{}, false
}

// Controls returns the controls registered during the latest render.
func (n *ChatTranscriptNode) Controls() []ui.Control {
	if n == nil || len(n.controls) == 0 {
		return nil
	}
	out := make([]ui.Control, len(n.controls))
	copy(out, n.controls)
	return out
}

// WantsWheel reports whether the transcript should receive wheel input.
func (n *ChatTranscriptNode) WantsWheel(point ui.Point) bool {
	return n != nil && n.Rect().Contains(point)
}

func (n *ChatTranscriptNode) refreshControls(ctx *ui.Context, bounds ui.Rect) {
	if n == nil || n.retained == nil || bounds.W <= 0 || bounds.H <= 0 {
		n.controls = nil
		return
	}
	runtime := ui.Runtime{}
	renderCtx := ui.Context{}
	if ctx != nil {
		renderCtx = *ctx
	}
	renderCtx.Runtime = &runtime
	n.retained.RenderVisibleInto(&renderCtx, max(0, bounds.W), max(0, bounds.H), max(0, n.yOffset), nil)
	n.controls = runtime.Controls()
}

// ComposerState describes the current composer display.
type ComposerState struct {
	AreaElement   ui.Node
	Element       ui.Node
	ElementHidden ui.Node
	Revision      uint64
	CursorDirty   bool
	Focused       bool
	BlinkEnabled  bool
}

// ComposerNode renders the composer area and tracks cursor damage.
type ComposerNode struct {
	ui.BaseNode
	areaElement   ui.Node
	element       ui.Node
	elementHidden ui.Node
	revision      uint64
	cursorDirty   bool
	focused       bool
	blinkEnabled  bool
	blinkVisible  bool
	blinkActive   bool
	measureWidth  int
	measureSize   ui.Size
	measureValid  bool
	measureRev    uint64
	lastRevision  uint64
	surface       ui.Surface
	cursorRect    ui.Rect
	cursorValid   bool
}

// NewComposerNode constructs a composer display node.
func NewComposerNode() *ComposerNode {
	node := &ComposerNode{blinkVisible: true}
	node.MarkLayoutDirty()
	return node
}

// SetState replaces the composer display state.
func (n *ComposerNode) SetState(state ComposerState) {
	if n == nil {
		return
	}
	revisionChanged := n.revision != state.Revision
	focusChanged := n.focused != state.Focused
	n.areaElement = state.AreaElement
	n.element = state.Element
	n.elementHidden = state.ElementHidden
	n.revision = state.Revision
	n.cursorDirty = state.CursorDirty
	n.focused = state.Focused
	n.blinkEnabled = state.BlinkEnabled
	if revisionChanged || focusChanged {
		n.blinkVisible = true
	}
	if revisionChanged {
		n.measureValid = false
	}
	if revisionChanged {
		n.MarkDirtyLocal(ui.Rect{W: n.Rect().W, H: n.Rect().H})
	}
}

// Focus marks the composer node focused for retained UI traversal.
func (n *ComposerNode) Focus() {
	if n == nil || n.focused {
		return
	}
	n.focused = true
	n.blinkVisible = true
	n.MarkDirtyLocal(ui.Rect{W: n.Rect().W, H: n.Rect().H})
}

// Blur marks the composer node unfocused for retained UI traversal.
func (n *ComposerNode) Blur() {
	if n == nil || !n.focused {
		return
	}
	n.focused = false
	n.blinkVisible = true
	n.MarkDirtyLocal(ui.Rect{W: n.Rect().W, H: n.Rect().H})
}

// Focused reports whether the composer node currently holds focus.
func (n *ComposerNode) Focused() bool {
	return n != nil && n.focused
}

// HandleKey reports unhandled input; the app textarea owns editing state.
func (n *ComposerNode) HandleKey(ui.KeyMsg) (bool, ui.Cmd) {
	return false, nil
}

// Invalidate marks the composer for remeasure and repaint.
func (n *ComposerNode) Invalidate() {
	if n == nil {
		return
	}
	n.measureValid = false
	n.MarkLayoutDirty()
}

// Dirty reports whether the composer has pending state changes.
func (n *ComposerNode) Dirty() bool {
	return n == nil || n.NeedsLayout() || n.NeedsPaint() || n.lastRevision != n.revision || n.cursorDirty
}

// Measure returns the measured composer area size.
func (n *ComposerNode) Measure(ctx *ui.Context, constraints ui.Constraints) ui.Size {
	if n == nil {
		return constraints.Clamp(ui.Size{})
	}
	if n.measureValid && n.measureWidth == constraints.MaxW && n.measureRev == n.revision {
		return constraints.Clamp(n.measureSize)
	}
	painter := measuredPainterFromElement(n.areaElement)
	if painter == nil {
		n.measureSize = ui.Size{}
	} else {
		n.measureSize = painter.Measure(ctx, ui.NewConstraints(constraints.MaxW, 0))
	}
	n.measureWidth = constraints.MaxW
	n.measureRev = n.revision
	n.measureValid = true
	return constraints.Clamp(n.measureSize)
}

// Prepare renders the composer area and records cursor-local damage.
func (n *ComposerNode) Prepare(ctx *ui.Context) {
	if n == nil {
		return
	}
	rect := n.Rect()
	if rect.Empty() {
		return
	}
	if !n.Dirty() {
		return
	}
	next := paintMeasuredSurface(ctx, measuredPainterFromElement(n.areaElement), rect)
	if !n.blinkVisible {
		n.paintHiddenComposerCursor(ctx, &next, rect)
	}
	nextCursorRect, nextCursorOK := n.cursorRectForBounds(rect)
	switch {
	case !n.NeedsLayout() && n.cursorDirty && n.cursorValid && nextCursorOK:
		damage := ui.DamageSet{}
		damage.Add(n.cursorRect)
		damage.Add(nextCursorRect)
		n.MarkDirtyLocalRects(damage.Rects())
	default:
		diff := ui.DiffSurfaceDamage(n.surface, next)
		if len(diff) == 0 && n.NeedsLayout() {
			n.MarkDirtyLocal(ui.Rect{W: rect.W, H: rect.H})
		} else {
			n.MarkDirtyLocalRects(diff)
		}
	}
	n.surface = next
	n.cursorRect = nextCursorRect
	n.cursorValid = nextCursorOK
	n.cursorDirty = false
	n.lastRevision = n.revision
}

// Paint paints the latest composer surface.
func (n *ComposerNode) Paint(_ *ui.Context, canvas ui.Canvas) {
	if n == nil || canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	canvas.BlitSurface(0, 0, n.surface.Normalize(canvas.Width(), canvas.Height()))
}

// SyncBlinkTimer synchronizes the composer-owned cursor blink timer.
func (n *ComposerNode) SyncBlinkTimer(root *ui.Root) {
	if n == nil || root == nil {
		return
	}
	if !n.shouldBlink() {
		if n.blinkActive {
			root.StopOwnerTimers(ComposerBlinkTimerOwner)
			n.blinkActive = false
		}
		n.blinkVisible = true
		return
	}
	if n.blinkActive {
		return
	}
	root.StartTimer(ComposerBlinkTimerOwner, ui.TimerSpec{
		Interval: textarea.BlinkInterval(),
		Repeat:   true,
	})
	n.blinkActive = true
}

// HandleTimer advances the composer cursor blink state and invalidates it.
func (n *ComposerNode) HandleTimer(event ui.TimerEvent) bool {
	if n == nil || event.Owner != ComposerBlinkTimerOwner || !n.shouldBlink() {
		return false
	}
	n.blinkVisible = !n.blinkVisible
	if n.cursorValid {
		n.MarkDirtyLocal(n.cursorRect)
	} else {
		n.MarkDirtyLocal(ui.Rect{W: n.Rect().W, H: n.Rect().H})
	}
	return true
}

// Surface renders the composer area to a standalone surface.
func (n *ComposerNode) Surface(ctx *ui.Context, bounds ui.Rect) ui.Surface {
	if n == nil {
		return ui.Surface{}
	}
	surface := paintMeasuredSurface(ctx, measuredPainterFromElement(n.areaElement), bounds)
	if !n.blinkVisible {
		n.paintHiddenComposerCursor(ctx, &surface, bounds)
	}
	return surface
}

func (n *ComposerNode) shouldBlink() bool {
	return n != nil && n.focused && n.blinkEnabled
}

func (n *ComposerNode) currentElement() ui.Node {
	if n == nil {
		return nil
	}
	if n.blinkVisible || n.elementHidden == nil {
		return n.element
	}
	return n.elementHidden
}

func (n *ComposerNode) paintHiddenComposerCursor(ctx *ui.Context, surface *ui.Surface, bounds ui.Rect) {
	if n == nil || surface == nil || n.elementHidden == nil || bounds.W <= 0 || bounds.H <= 0 {
		return
	}
	painter := measuredPainterFromElement(n.elementHidden)
	if painter == nil {
		return
	}
	composerRect := n.composerRect(ctx, bounds)
	if composerRect.Empty() {
		return
	}
	painter.Paint(ctx, ui.NewCanvas(surface, composerRect))
}

func (n *ComposerNode) cursorRectForBounds(bounds ui.Rect) (ui.Rect, bool) {
	if n == nil || bounds.H < 2 {
		return ui.Rect{}, false
	}
	composer, ok := n.currentElement().(ui.Composer)
	if !ok {
		return ui.Rect{}, false
	}
	rect, ok := composer.CursorRect()
	if !ok {
		return ui.Rect{}, false
	}
	composerRect := n.composerRect(nil, bounds)
	if composerRect.Empty() {
		return ui.Rect{}, false
	}
	rect.X += composerRect.X
	rect.Y += composerRect.Y
	if rect.X >= bounds.W || rect.Y >= bounds.H {
		return ui.Rect{}, false
	}
	if rect.X+rect.W > bounds.W {
		rect.W = bounds.W - rect.X
	}
	if rect.W <= 0 {
		return ui.Rect{}, false
	}
	return rect, true
}

func (n *ComposerNode) composerRect(ctx *ui.Context, bounds ui.Rect) ui.Rect {
	if n == nil || n.element == nil || bounds.W <= 0 || bounds.H <= 0 {
		return ui.Rect{}
	}
	painter := measuredPainterFromElement(n.element)
	if painter == nil {
		return ui.Rect{}
	}
	size := painter.Measure(ctx, ui.NewConstraints(bounds.W, 0))
	height := min(bounds.H, max(0, size.H))
	if height <= 0 {
		return ui.Rect{}
	}
	return ui.Rect{Y: bounds.H - height, W: bounds.W, H: height}
}

type measuredPainter interface {
	Measure(*ui.Context, ui.Constraints) ui.Size
	Paint(*ui.Context, ui.Canvas)
}

func measuredPainterFromElement(node ui.Node) measuredPainter {
	if node == nil {
		return nil
	}
	painter, ok := node.(measuredPainter)
	if !ok {
		return nil
	}
	return painter
}

func paintMeasuredSurface(ctx *ui.Context, painter measuredPainter, bounds ui.Rect) ui.Surface {
	width := max(0, bounds.W)
	height := max(0, bounds.H)
	if painter == nil || width <= 0 || height <= 0 {
		return ui.TransparentSurface(width, height)
	}
	surface := ui.TransparentSurface(width, height)
	painter.Paint(ctx, ui.NewCanvas(&surface, ui.Rect{W: width, H: height}))
	return surface
}
