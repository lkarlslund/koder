package tui

import (
	"hash/fnv"
	"strconv"

	"github.com/lkarlslund/koder/internal/ui"
	"github.com/lkarlslund/koder/internal/ui/textarea"
)

const mainScreenVerticalInset = 0
const composerBlinkTimerOwner = "composer"

type transcriptWidget struct {
	model       *Model
	invalidated bool
}

func (w *transcriptWidget) Dirty() bool {
	return w == nil || w.invalidated
}

func (w *transcriptWidget) Invalidate() {
	if w == nil {
		return
	}
	w.invalidated = true
}

func (w *transcriptWidget) ClearDirty() {
	if w == nil {
		return
	}
	w.invalidated = false
}

func (w *transcriptWidget) Surface(bounds ui.Rect) ui.Surface {
	if w == nil || w.model == nil {
		return ui.Surface{}
	}
	retained := w.model.syncRetainedTranscript()
	raw := w.model.viewport.VisibleSurface()
	surface := raw.Normalize(max(0, bounds.W), max(0, bounds.H))
	if retained == nil {
		return surface
	}
	scroll := ui.ScrollBox{
		Child:   ui.AsNode(retained),
		OffsetY: max(0, w.model.viewport.YOffset),
		Width:   max(0, bounds.W),
		Height:  max(0, bounds.H),
	}
	if raw.SurfaceWidth() != max(0, bounds.W) || raw.SurfaceHeight() != max(0, bounds.H) {
		rendered, _, _ := scroll.RenderVisible(&ui.Context{Palette: w.model.palette}, max(0, bounds.W), max(0, bounds.H), max(0, w.model.viewport.YOffset))
		return rendered
	}
	_, _, _ = scroll.RenderVisible(&ui.Context{Palette: w.model.palette}, max(0, bounds.W), max(0, bounds.H), max(0, w.model.viewport.YOffset))
	return surface
}

type composerAreaWidget struct {
	model        *Model
	measureWidth int
	measureSize  ui.Size
	measureValid bool
	measureRev   uint64
	invalidated  bool
	lastRevision uint64
	blinkActive  bool
}

type measuredPainter interface {
	Measure(*ui.Context, ui.Constraints) ui.Size
	Paint(*ui.Context, ui.Canvas)
}

func (w *composerAreaWidget) Dirty() bool {
	return w == nil || w.invalidated || w.revisionChanged() || w.model.composerCursorDirty
}

func (w *composerAreaWidget) Invalidate() {
	if w == nil {
		return
	}
	w.invalidated = true
	w.measureValid = false
}

func (w *composerAreaWidget) measure(ctx *ui.Context, width int) ui.Size {
	if w != nil && w.measureValid && w.measureWidth == width && w.measureRev == w.currentRevision() {
		return w.measureSize
	}
	if w == nil {
		return ui.Size{}
	}
	content := w.content()
	if content == nil {
		return ui.Size{}
	}
	size := content.Measure(ctx, ui.NewConstraints(width, 0))
	w.measureWidth = width
	w.measureSize = size
	w.measureValid = true
	w.measureRev = w.currentRevision()
	cache := w.model.ensureRenderCache()
	cache.composerAreaHeight = size.H
	return size
}

func (w *composerAreaWidget) ClearDirty() {
	if w == nil {
		return
	}
	w.invalidated = false
	w.lastRevision = w.currentRevision()
}

func (w *composerAreaWidget) currentRevision() uint64 {
	if w == nil || w.model == nil {
		return 0
	}
	return w.model.composer.Revision()
}

func (w *composerAreaWidget) revisionChanged() bool {
	if w == nil {
		return true
	}
	return w.lastRevision != w.currentRevision()
}

func (w *composerAreaWidget) shouldBlink() bool {
	if w == nil || w.model == nil {
		return false
	}
	return w.model.composerShouldBlink() && w.model.composer.Focused()
}

func (w *composerAreaWidget) syncBlinkTimer(root *ui.Root) {
	if w == nil || root == nil {
		return
	}
	if !w.shouldBlink() {
		if w.blinkActive {
			root.StopOwnerTimers(composerBlinkTimerOwner)
			w.blinkActive = false
		}
		return
	}
	if w.blinkActive {
		return
	}
	root.StartTimer(composerBlinkTimerOwner, ui.TimerSpec{
		Interval: textarea.BlinkInterval(),
		Repeat:   true,
	})
	w.blinkActive = true
}

func (w *composerAreaWidget) handleTimer(event ui.TimerEvent) bool {
	if w == nil || event.Owner != composerBlinkTimerOwner {
		return false
	}
	if !w.model.composer.ToggleBlink() {
		return false
	}
	w.model.composerCursorDirty = true
	return true
}

func (w *composerAreaWidget) content() measuredPainter {
	if w == nil || w.model == nil {
		return nil
	}
	return measuredPainterFromElement(w.model.renderComposerAreaElement())
}

func (w *composerAreaWidget) Surface(ctx *ui.Context, bounds ui.Rect) ui.Surface {
	if w == nil {
		return ui.Surface{}
	}
	width := max(0, bounds.W)
	size := w.measure(ctx, width)
	if width <= 0 {
		width = size.W
	}
	rect := ui.Rect{W: width, H: size.H}
	surface := paintMeasuredSurface(ctx, w.content(), rect)
	cache := w.model.ensureRenderCache()
	cache.composerAreaValid = rect.H > 0
	cache.renderedComposerAreaSurface = surface
	return surface
}

type hashedElementWidget struct {
	model       *Model
	build       func(*Model) measuredPainter
	hash        func(*Model, ui.Rect) uint64
	invalidated bool
}

func (w *hashedElementWidget) Dirty() bool {
	return w == nil || w.invalidated
}

func (w *hashedElementWidget) Invalidate() {
	if w == nil {
		return
	}
	w.invalidated = true
}

func (w *hashedElementWidget) ClearDirty() {
	if w == nil {
		return
	}
	w.invalidated = false
}

func (w *hashedElementWidget) content() measuredPainter {
	if w == nil || w.build == nil {
		return nil
	}
	return w.build(w.model)
}

type mainScreenWidget struct {
	model      *Model
	transcript *transcriptWidget
	composer   *composerAreaWidget
	sidebar    *hashedElementWidget
	statusPane *hashedElementWidget
	retained   *mainScreenRetainedRoot
	bounds     ui.Rect
	surface    ui.Surface
	valid      bool
}

func (w *mainScreenWidget) DirtyRects() []ui.Rect {
	if w == nil || w.retained == nil {
		return nil
	}
	return copyRects(w.retained.DirtyRects())
}

func (w *mainScreenWidget) Invalidate() {
	if w == nil {
		return
	}
	w.valid = false
}

func (w *mainScreenWidget) dirty() bool {
	if w == nil || !w.valid {
		return true
	}
	if w.retained != nil {
		return w.retained.Pending()
	}
	return w.transcript.Dirty() || w.composer.Dirty() || w.sidebar.Dirty() || w.statusPane.Dirty()
}

func (w *mainScreenWidget) Surface(ctx *ui.Context, bounds ui.Rect) ui.Surface {
	if w == nil {
		return ui.Surface{}
	}
	if w.valid && w.bounds == bounds && !w.dirty() {
		return w.surface
	}
	root := w.ensureRetainedRoot()
	root.model = w.model
	root.Layout(ctx, bounds)
	root.Prepare(ctx)
	fullPaint := !w.valid || w.bounds != bounds
	surface := ui.TransparentSurface(bounds.W, bounds.H)
	if !fullPaint && w.valid && w.bounds == bounds &&
		w.surface.SurfaceWidth() == bounds.W && w.surface.SurfaceHeight() == bounds.H {
		surface = surface.PlaceAt(0, 0, w.surface)
	}
	canvas := ui.NewCanvas(&surface, bounds)
	if fullPaint {
		root.PaintAll(ctx, canvas)
	} else {
		root.PaintDirty(ctx, canvas)
	}
	rects := root.DirtyRects()
	if len(rects) > 0 {
		surface = surface.WithDirtyRects(rects...)
	}
	root.ClearDirty()
	w.transcript.ClearDirty()
	w.composer.ClearDirty()
	w.sidebar.ClearDirty()
	w.statusPane.ClearDirty()
	w.bounds = bounds
	w.surface = surface
	w.valid = true
	cache := w.model.ensureRenderCache()
	cache.renderedBodySurface = surface
	cache.bodyValid = true
	return surface
}

func (w *mainScreenWidget) PaintInto(ctx *ui.Context, bounds ui.Rect, dst *ui.Surface) []ui.Rect {
	if w == nil || dst == nil {
		return nil
	}
	return w.paintIntoCanvas(ctx, ui.Rect{W: bounds.W, H: bounds.H}, ui.NewCanvas(dst, bounds))
}

func (w *mainScreenWidget) paintIntoCanvas(ctx *ui.Context, bounds ui.Rect, canvas ui.Canvas) []ui.Rect {
	if w == nil {
		return nil
	}
	root := w.ensureRetainedRoot()
	root.model = w.model
	root.Layout(ctx, bounds)
	root.Prepare(ctx)
	fullPaint := !w.valid || w.bounds != bounds
	if !fullPaint && w.valid && w.bounds == bounds &&
		w.surface.SurfaceWidth() == bounds.W && w.surface.SurfaceHeight() == bounds.H {
		canvas.BlitSurface(0, 0, w.surface)
	}
	if fullPaint {
		root.PaintAll(ctx, canvas)
	} else {
		root.PaintDirty(ctx, canvas)
	}
	rects := root.DirtyRects()
	root.ClearDirty()
	w.transcript.ClearDirty()
	w.composer.ClearDirty()
	w.sidebar.ClearDirty()
	w.statusPane.ClearDirty()
	w.bounds = bounds
	w.surface = canvas.Snapshot()
	w.valid = true
	cache := w.model.ensureRenderCache()
	cache.renderedBodySurface = w.surface
	cache.bodyValid = true
	return rects
}

type mainScreenRetainedRoot struct {
	ui.BaseNode
	model          *Model
	transcriptNode *transcriptRetainedNode
	composerNode   *composerRetainedNode
	sidebarNode    *hashedElementRetainedNode
	statusNode     *hashedElementRetainedNode
	mainColumnNode *ui.FlexNode
	bodyNode       *ui.FlexNode
	layoutRootNode *ui.FlexNode
	bodyChildren   [2]ui.FlexNodeChild
	bodySlices     [2][]ui.FlexNodeChild
}

type transcriptRetainedNode struct {
	ui.BaseNode
	widget  *transcriptWidget
	surface ui.Surface
}

func (n *transcriptRetainedNode) Pending() bool {
	return n == nil || n.widget == nil || n.widget.invalidated
}

func (n *transcriptRetainedNode) Measure(_ *ui.Context, constraints ui.Constraints) ui.Size {
	if n == nil || n.widget == nil || n.widget.model == nil {
		return constraints.Clamp(ui.Size{})
	}
	m := n.widget.model
	return constraints.Clamp(ui.Size{W: max(0, m.viewport.Width), H: max(0, m.viewport.Height)})
}

func (n *transcriptRetainedNode) Prepare(ctx *ui.Context) {
	if n == nil || n.widget == nil || n.widget.model == nil {
		return
	}
	rect := n.Rect()
	if rect.Empty() {
		n.widget.ClearDirty()
		return
	}
	if !n.widget.invalidated && !n.NeedsPaint() && !n.NeedsLayout() {
		return
	}
	next := n.widget.Surface(rect)
	if n.widget.invalidated {
		diff := ui.DiffSurfaceDamage(n.surface, next)
		if len(diff) == 0 && n.NeedsLayout() {
			n.MarkDirtyLocal(ui.Rect{W: rect.W, H: rect.H})
		} else {
			n.MarkDirtyLocalRects(diff)
		}
	}
	n.surface = next
	n.widget.ClearDirty()
}

func (n *transcriptRetainedNode) Paint(_ *ui.Context, canvas ui.Canvas) {
	if n == nil || canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	if n.widget != nil && n.widget.model != nil {
		canvas.Fill(ui.Rect{W: canvas.Width(), H: canvas.Height()}, ui.CellStyle{
			BG: n.widget.model.palette.ScreenBackground,
		})
	}
	canvas.BlitSurface(0, 0, n.surface.Normalize(canvas.Width(), canvas.Height()))
}

type composerRetainedNode struct {
	ui.BaseNode
	widget      *composerAreaWidget
	surface     ui.Surface
	cursorRect  ui.Rect
	cursorValid bool
}

func (n *composerRetainedNode) Pending() bool {
	return n == nil || n.widget == nil || n.widget.Dirty()
}

func (n *composerRetainedNode) Measure(ctx *ui.Context, constraints ui.Constraints) ui.Size {
	if n == nil || n.widget == nil {
		return constraints.Clamp(ui.Size{})
	}
	return n.widget.measure(ctx, constraints.MaxW)
}

func (n *composerRetainedNode) Prepare(ctx *ui.Context) {
	if n == nil || n.widget == nil || n.widget.model == nil {
		return
	}
	rect := n.Rect()
	if rect.Empty() {
		n.widget.ClearDirty()
		return
	}
	m := n.widget.model
	n.widget.syncBlinkTimer(m.ensureUIRoot())
	needsSync := n.widget.Dirty() || n.NeedsPaint()
	if !needsSync {
		return
	}
	element := m.renderComposerAreaElement()
	next := paintMeasuredSurface(ctx, measuredPainterFromElement(element), rect)
	nextCursorRect, nextCursorOK := composerCursorRectForBounds(m, rect)
	switch {
	case !n.NeedsLayout() && m.composerCursorDirty && n.cursorValid && nextCursorOK:
		damage := ui.DamageSet{}
		damage.Add(n.cursorRect)
		damage.Add(nextCursorRect)
		n.MarkDirtyLocalRects(damage.Rects())
	default:
		diff := ui.DiffSurfaceDamage(n.surface, next)
		if len(diff) == 0 && (n.widget.invalidated || n.NeedsLayout()) {
			n.MarkDirtyLocal(ui.Rect{W: rect.W, H: rect.H})
		} else {
			n.MarkDirtyLocalRects(diff)
		}
	}
	n.surface = next
	n.cursorRect = nextCursorRect
	n.cursorValid = nextCursorOK
	cache := m.ensureRenderCache()
	cache.composerAreaValid = rect.H > 0
	cache.renderedComposerAreaSurface = next
	m.composerCursorDirty = false
	n.widget.ClearDirty()
}

func (n *composerRetainedNode) Paint(_ *ui.Context, canvas ui.Canvas) {
	if n == nil || canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	canvas.BlitSurface(0, 0, n.surface.Normalize(canvas.Width(), canvas.Height()))
}

type hashedElementRetainedNode struct {
	ui.BaseNode
	widget   *hashedElementWidget
	measure  func(*ui.Context, ui.Constraints) ui.Size
	surface  ui.Surface
	lastHash uint64
}

func (n *hashedElementRetainedNode) Pending() bool {
	if n == nil || n.widget == nil {
		return true
	}
	if n.widget.invalidated {
		return true
	}
	if n.Rect().Empty() {
		return false
	}
	if n.widget.hash == nil {
		return false
	}
	return n.widget.hash(n.widget.model, n.Rect()) != n.lastHash
}

func (n *hashedElementRetainedNode) Measure(ctx *ui.Context, constraints ui.Constraints) ui.Size {
	if n == nil {
		return constraints.Clamp(ui.Size{})
	}
	if n.measure != nil {
		return n.measure(ctx, constraints)
	}
	return constraints.Clamp(ui.Size{})
}

func (n *hashedElementRetainedNode) Prepare(ctx *ui.Context) {
	if n == nil || n.widget == nil {
		return
	}
	rect := n.Rect()
	if rect.Empty() {
		n.widget.ClearDirty()
		return
	}
	currentHash := uint64(0)
	if n.widget.hash != nil {
		currentHash = n.widget.hash(n.widget.model, rect)
	}
	needsSync := n.widget.invalidated || currentHash != n.lastHash || n.NeedsPaint()
	if !needsSync {
		return
	}
	next := paintMeasuredSurface(ctx, n.widget.content(), rect)
	if !n.NeedsLayout() && (n.widget.invalidated || currentHash != n.lastHash) {
		n.MarkDirtyLocalRects(ui.DiffSurfaceDamage(n.surface, next))
	}
	n.surface = next
	n.lastHash = currentHash
	n.widget.ClearDirty()
}

func (n *hashedElementRetainedNode) Paint(_ *ui.Context, canvas ui.Canvas) {
	if n == nil || canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	canvas.BlitSurface(0, 0, n.surface.Normalize(canvas.Width(), canvas.Height()))
}

func (r *mainScreenRetainedRoot) Measure(_ *ui.Context, constraints ui.Constraints) ui.Size {
	if r == nil {
		return ui.Size{}
	}
	return constraints.Clamp(ui.Size{W: constraints.MaxW, H: constraints.MaxH})
}

func (r *mainScreenRetainedRoot) Pending() bool {
	if r == nil {
		return true
	}
	return r.transcriptNode.Pending() || r.composerNode.Pending() || r.sidebarNode.Pending() || r.statusNode.Pending()
}

func newMainScreenRetainedRoot(m *Model, transcript *transcriptWidget, composer *composerAreaWidget, sidebar, status *hashedElementWidget) *mainScreenRetainedRoot {
	root := &mainScreenRetainedRoot{
		model: m,
	}
	root.transcriptNode = &transcriptRetainedNode{widget: transcript}
	root.composerNode = &composerRetainedNode{widget: composer}
	root.sidebarNode = &hashedElementRetainedNode{
		widget: sidebar,
		measure: func(_ *ui.Context, constraints ui.Constraints) ui.Size {
			return constraints.Clamp(ui.Size{W: max(0, m.sidebarWidth()), H: max(0, m.viewport.Height)})
		},
	}
	root.statusNode = &hashedElementRetainedNode{
		widget: status,
		measure: func(_ *ui.Context, constraints ui.Constraints) ui.Size {
			return constraints.Clamp(ui.Size{W: constraints.MaxW, H: max(0, m.statusPaneHeight())})
		},
	}
	root.mainColumnNode = ui.NewFlexNode(ui.DirectionVertical, []ui.FlexNodeChild{
		{Node: root.transcriptNode, Flex: 1},
		{Node: root.composerNode},
	}, 0)
	root.bodyNode = ui.NewFlexNode(ui.DirectionHorizontal, nil, 0)
	root.layoutRootNode = ui.NewFlexNode(ui.DirectionVertical, []ui.FlexNodeChild{
		{Node: root.bodyNode, Flex: 1},
		{Node: root.statusNode},
	}, 0)
	root.bodyChildren[0] = ui.FlexNodeChild{Node: root.mainColumnNode, Flex: 1}
	root.bodyChildren[1] = ui.FlexNodeChild{Node: root.sidebarNode, Basis: max(0, m.sidebarWidth())}
	root.bodySlices[0] = root.bodyChildren[:1]
	root.bodySlices[1] = root.bodyChildren[:2]
	return root
}

func (r *mainScreenRetainedRoot) Layout(ctx *ui.Context, rect ui.Rect) {
	r.BaseNode.Layout(ctx, rect)
	r.syncLayoutTree()
	r.layoutRootNode.Layout(ctx, rect)
}

func (r *mainScreenRetainedRoot) Paint(ctx *ui.Context, canvas ui.Canvas) {
	r.PaintAll(ctx, canvas)
}

func (r *mainScreenRetainedRoot) Prepare(ctx *ui.Context) {
	r.syncLayoutTree()
	r.layoutRootNode.Prepare(ctx)
}

func (r *mainScreenRetainedRoot) PaintAll(ctx *ui.Context, canvas ui.Canvas) {
	r.syncLayoutTree()
	r.layoutRootNode.Paint(ctx, canvas)
}

func (r *mainScreenRetainedRoot) PaintDirty(ctx *ui.Context, canvas ui.Canvas) {
	r.syncLayoutTree()
	paintDirtyNode(ctx, canvas, r.layoutRootNode)
}

func (r *mainScreenRetainedRoot) DirtyRects() []ui.Rect {
	damage := ui.DamageSet{}
	damage.AddAll(r.BaseNode.DirtyRects())
	collectNodeDamage(&damage, r.layoutRootNode)
	return damage.Rects()
}

func (r *mainScreenRetainedRoot) ClearDirty() {
	r.BaseNode.ClearDirty()
	clearNodeDirty(r.layoutRootNode)
}

func (r *mainScreenRetainedRoot) forEachNode(visit func(ui.Node)) {
	if r == nil || visit == nil {
		return
	}
	visit(r.transcriptNode)
	visit(r.composerNode)
	visit(r.sidebarNode)
	visit(r.statusNode)
}

func (r *mainScreenRetainedRoot) syncLayoutTree() {
	if r == nil || r.bodyNode == nil || r.layoutRootNode == nil {
		return
	}
	if r.model != nil && r.model.showSidebar {
		r.bodyChildren[1].Basis = max(0, r.model.sidebarWidth())
		r.bodyNode.SetChildren(r.bodySlices[1])
		return
	}
	r.bodyNode.SetChildren(r.bodySlices[0])
	if r.sidebarNode != nil {
		r.sidebarNode.Layout(nil, ui.Rect{})
	}
}

func paintDirtyNode(ctx *ui.Context, canvas ui.Canvas, node ui.Node) {
	if node == nil || node.Rect().Empty() {
		return
	}
	if walkChildNodes(node, func(child ui.Node) {
		paintDirtyNode(ctx, canvas, child)
	}) {
		return
	}
	if !node.NeedsPaint() {
		return
	}
	node.Paint(ctx, canvas.Subrect(node.Rect()))
}

func collectNodeDamage(damage *ui.DamageSet, node ui.Node) {
	if damage == nil || node == nil {
		return
	}
	damage.AddAll(node.DirtyRects())
	walkChildNodes(node, func(child ui.Node) {
		collectNodeDamage(damage, child)
	})
}

func clearNodeDirty(node ui.Node) {
	if node == nil {
		return
	}
	node.ClearDirty()
	walkChildNodes(node, func(child ui.Node) {
		clearNodeDirty(child)
	})
}

func walkChildNodes(node ui.Node, visit func(ui.Node)) bool {
	if node == nil || visit == nil {
		return false
	}
	if typed, ok := node.(ui.Container); ok {
		for _, child := range typed.Children() {
			if child != nil {
				visit(child)
			}
		}
		return true
	}
	return false
}

func fullSurfaceDamage(surface ui.Surface) []ui.Rect {
	if surface.SurfaceWidth() <= 0 || surface.SurfaceHeight() <= 0 {
		return nil
	}
	return []ui.Rect{{W: surface.SurfaceWidth(), H: surface.SurfaceHeight()}}
}

func fullRectDamage(bounds ui.Rect) []ui.Rect {
	if bounds.W <= 0 || bounds.H <= 0 {
		return nil
	}
	return []ui.Rect{{W: bounds.W, H: bounds.H}}
}

func copyRects(rects []ui.Rect) []ui.Rect {
	if len(rects) == 0 {
		return nil
	}
	out := make([]ui.Rect, len(rects))
	copy(out, rects)
	return out
}

func translateRects(rects []ui.Rect, dx, dy int) []ui.Rect {
	if len(rects) == 0 {
		return nil
	}
	out := make([]ui.Rect, 0, len(rects))
	for _, rect := range rects {
		if rect.Empty() {
			continue
		}
		out = append(out, rect.Translate(dx, dy))
	}
	return out
}

func collectMainScreenDamage(
	transcriptDirty bool, transcriptSurface ui.Surface, transcriptRect ui.Rect, transcriptRects []ui.Rect,
	composerDirty bool, composerSurface ui.Surface, composerRect ui.Rect, composerRects []ui.Rect,
	sidebarDirty bool, sidebarSurface ui.Surface, sidebarRect ui.Rect, sidebarRects []ui.Rect,
	statusDirty bool, statusSurface ui.Surface, statusRect ui.Rect, statusRects []ui.Rect,
) []ui.Rect {
	damage := ui.DamageSet{}
	addWidgetDamage := func(widgetDirty bool, surface ui.Surface, rect ui.Rect, rects []ui.Rect) {
		if !widgetDirty {
			return
		}
		if len(rects) == 0 {
			rects = fullSurfaceDamage(surface)
		}
		damage.AddAll(translateRects(rects, rect.X, rect.Y))
	}
	addWidgetDamage(transcriptDirty, transcriptSurface, transcriptRect, transcriptRects)
	addWidgetDamage(composerDirty, composerSurface, composerRect, composerRects)
	addWidgetDamage(sidebarDirty, sidebarSurface, sidebarRect, sidebarRects)
	addWidgetDamage(statusDirty, statusSurface, statusRect, statusRects)
	return damage.Rects()
}

func composerCursorRect(m *Model, surface ui.Surface) (ui.Rect, bool) {
	return composerCursorRectForBounds(m, ui.Rect{W: surface.SurfaceWidth(), H: surface.SurfaceHeight()})
}

func composerCursorRectForBounds(m *Model, bounds ui.Rect) (ui.Rect, bool) {
	if m == nil || bounds.H < 2 || !m.shouldShowComposerArea() {
		return ui.Rect{}, false
	}
	element, ok := m.renderComposerElement().(*ui.LeafNode)
	if !ok {
		return ui.Rect{}, false
	}
	composer, ok := element.Content.(ui.Composer)
	if !ok {
		return ui.Rect{}, false
	}
	rect, ok := composer.CursorRect()
	if !ok {
		return ui.Rect{}, false
	}
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

func sidebarWidgetHash(m *Model, bounds ui.Rect) uint64 {
	return hashStrings(
		strconv.Itoa(bounds.W),
		strconv.Itoa(bounds.H),
		strconv.FormatBool(m.showSidebar),
		m.renderSidebar(),
	)
}

func statusPaneWidgetHash(m *Model, bounds ui.Rect) uint64 {
	line := ""
	if m.busy.transcriptActive() {
		line = ui.WorkingIndicatorLine(m.workingIndicator(), m.busy.statusOrDefault("Working ..."))
	}
	return hashStrings(
		strconv.Itoa(bounds.W),
		strconv.Itoa(bounds.H),
		line,
	)
}

func hashStrings(values ...string) uint64 {
	hasher := fnv.New64a()
	for _, value := range values {
		_, _ = hasher.Write([]byte(value))
		_, _ = hasher.Write([]byte{0})
	}
	return hasher.Sum64()
}

func (m *Model) ensureMainScreenWidget() *mainScreenWidget {
	if m.mainScreen != nil {
		m.mainScreen.model = m
		m.mainScreen.transcript.model = m
		m.mainScreen.composer.model = m
		m.mainScreen.sidebar.model = m
		m.mainScreen.statusPane.model = m
		if m.mainScreen.retained != nil {
			m.mainScreen.retained.model = m
		}
		return m.mainScreen
	}
	m.mainScreen = &mainScreenWidget{
		model:      m,
		transcript: &transcriptWidget{model: m, invalidated: true},
		composer:   &composerAreaWidget{model: m, invalidated: true},
		sidebar: &hashedElementWidget{
			model: m,
			build: func(m *Model) measuredPainter {
				if !m.showSidebar {
					return nil
				}
				return ui.Sidebar{
					Child:  ui.AsNode(ui.TextPane{Content: m.renderSidebar()}),
					Height: m.viewport.Height,
					Width:  m.sidebarWidth(),
				}
			},
			hash:        sidebarWidgetHash,
			invalidated: true,
		},
		statusPane: &hashedElementWidget{
			model:       m,
			build:       func(m *Model) measuredPainter { return measuredPainterFromElement(m.renderStatusPaneElement()) },
			hash:        statusPaneWidgetHash,
			invalidated: true,
		},
	}
	m.mainScreen.retained = newMainScreenRetainedRoot(m, m.mainScreen.transcript, m.mainScreen.composer, m.mainScreen.sidebar, m.mainScreen.statusPane)
	return m.mainScreen
}

func (w *mainScreenWidget) ensureRetainedRoot() *mainScreenRetainedRoot {
	if w.retained == nil {
		w.retained = newMainScreenRetainedRoot(w.model, w.transcript, w.composer, w.sidebar, w.statusPane)
	}
	return w.retained
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
