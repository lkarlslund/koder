package tui

import (
	"github.com/lkarlslund/koder/internal/ui"
	"github.com/lkarlslund/koder/internal/ui/textarea"
)

const mainScreenVerticalInset = 0
const ComposerBlinkTimerOwner = "composer"
const composerBlinkTimerOwner = ComposerBlinkTimerOwner

type MainScreenDelegate interface {
	SyncRetainedTranscript() *ui.RetainedTranscript
	VisibleTranscriptSurface() ui.Surface
	RenderTranscriptViewportSurface(*ui.RetainedTranscript, int, int, int) (ui.Surface, int, int)
	ViewportSize() (int, int)
	ViewportYOffset() int
	ScreenBackground() ui.CellColor
	ComposerCursorDirty() bool
	ComposerRevision() uint64
	ComposerShouldBlink() bool
	ComposerFocused() bool
	ToggleComposerBlink() bool
	SetComposerCursorDirty(bool)
	ComposerAreaElement() ui.Node
	ComposerElement() ui.Node
	ComposerCursorRect(ui.Rect) (ui.Rect, bool)
	SetComposerAreaCache(ui.Size, ui.Surface)
	EnsureUIRoot() *ui.Root
	ShowSidebar() bool
	SidebarWidth() int
	StatusPaneHeight() int
	SidebarElement() ui.Node
	StatusPaneElement() ui.Node
	SidebarHash(ui.Rect) uint64
	StatusPaneHash(ui.Rect) uint64
}

type transcriptWidget struct {
	delegate    MainScreenDelegate
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
	if w == nil || w.delegate == nil {
		return ui.Surface{}
	}
	retained := w.delegate.SyncRetainedTranscript()
	if retained == nil {
		return ui.Surface{}
	}
	raw := w.delegate.VisibleTranscriptSurface()
	if raw.SurfaceWidth() == max(0, bounds.W) && raw.SurfaceHeight() == max(0, bounds.H) {
		return raw
	}
	surface, _, _ := w.delegate.RenderTranscriptViewportSurface(retained, max(0, bounds.W), max(0, bounds.H), max(0, w.delegate.ViewportYOffset()))
	return surface
}

type composerAreaWidget struct {
	delegate     MainScreenDelegate
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
	return w == nil || w.delegate == nil || w.invalidated || w.revisionChanged() || w.delegate.ComposerCursorDirty()
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
	w.delegate.SetComposerAreaCache(size, ui.Surface{})
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
	if w == nil || w.delegate == nil {
		return 0
	}
	return w.delegate.ComposerRevision()
}

func (w *composerAreaWidget) revisionChanged() bool {
	if w == nil {
		return true
	}
	return w.lastRevision != w.currentRevision()
}

func (w *composerAreaWidget) shouldBlink() bool {
	if w == nil || w.delegate == nil {
		return false
	}
	return w.delegate.ComposerShouldBlink() && w.delegate.ComposerFocused()
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
	if !w.delegate.ToggleComposerBlink() {
		return false
	}
	w.delegate.SetComposerCursorDirty(true)
	return true
}

func (w *composerAreaWidget) content() measuredPainter {
	if w == nil || w.delegate == nil {
		return nil
	}
	return measuredPainterFromElement(w.delegate.ComposerAreaElement())
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
	w.delegate.SetComposerAreaCache(size, surface)
	return surface
}

type hashedElementWidget struct {
	delegate    MainScreenDelegate
	build       func(MainScreenDelegate) measuredPainter
	hash        func(MainScreenDelegate, ui.Rect) uint64
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
	return w.build(w.delegate)
}

type MainScreenWidget struct {
	delegate   MainScreenDelegate
	transcript *transcriptWidget
	composer   *composerAreaWidget
	sidebar    *hashedElementWidget
	statusPane *hashedElementWidget
	retained   *mainScreenRetainedRoot
	bounds     ui.Rect
	surface    ui.Surface
	valid      bool
}

func (w *MainScreenWidget) DirtyRects() []ui.Rect {
	if w == nil || w.retained == nil {
		return nil
	}
	return copyRects(w.retained.DirtyRects())
}

func (w *MainScreenWidget) Invalidate() {
	if w == nil {
		return
	}
	w.valid = false
}

func (w *MainScreenWidget) Dirty() bool {
	if w == nil || !w.valid {
		return true
	}
	if w.retained != nil {
		return w.retained.Pending()
	}
	return w.transcript.Dirty() || w.composer.Dirty() || w.sidebar.Dirty() || w.statusPane.Dirty()
}

func (w *MainScreenWidget) Surface(ctx *ui.Context, bounds ui.Rect) ui.Surface {
	if w == nil {
		return ui.Surface{}
	}
	if w.valid && w.bounds == bounds && !w.Dirty() {
		return w.surface
	}
	root := w.ensureRetainedRoot()
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
	return surface
}

func (w *MainScreenWidget) PaintInto(ctx *ui.Context, bounds ui.Rect, dst *ui.Surface) []ui.Rect {
	if w == nil || dst == nil {
		return nil
	}
	return w.paintIntoCanvas(ctx, ui.Rect{W: bounds.W, H: bounds.H}, ui.NewCanvas(dst, bounds))
}

func (w *MainScreenWidget) paintIntoCanvas(ctx *ui.Context, bounds ui.Rect, canvas ui.Canvas) []ui.Rect {
	if w == nil {
		return nil
	}
	root := w.ensureRetainedRoot()
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
	return rects
}

type mainScreenRetainedRoot struct {
	ui.BaseNode
	delegate       MainScreenDelegate
	transcriptNode *chatViewportNode
	composerNode   *composerRetainedNode
	sidebarNode    *hashedElementRetainedNode
	statusNode     *hashedElementRetainedNode
	mainColumnNode *ui.FlexNode
	bodyNode       *ui.FlexNode
	layoutRootNode *ui.FlexNode
	bodyChildren   [2]ui.FlexNodeChild
	bodySlices     [2][]ui.FlexNodeChild
}

type chatViewportNode struct {
	ui.BaseNode
	widget  *transcriptWidget
	surface ui.Surface
}

func (n *chatViewportNode) Pending() bool {
	return n == nil || n.widget == nil || n.widget.invalidated
}

func (n *chatViewportNode) Measure(_ *ui.Context, constraints ui.Constraints) ui.Size {
	if n == nil || n.widget == nil || n.widget.delegate == nil {
		return constraints.Clamp(ui.Size{})
	}
	width, height := n.widget.delegate.ViewportSize()
	return constraints.Clamp(ui.Size{W: max(0, width), H: max(0, height)})
}

func (n *chatViewportNode) Prepare(_ *ui.Context) {
	if n == nil || n.widget == nil || n.widget.delegate == nil {
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

func (n *chatViewportNode) Paint(_ *ui.Context, canvas ui.Canvas) {
	if n == nil || canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	if n.widget != nil && n.widget.delegate != nil {
		canvas.Fill(ui.Rect{W: canvas.Width(), H: canvas.Height()}, ui.CellStyle{
			BG: n.widget.delegate.ScreenBackground(),
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
	if n == nil || n.widget == nil || n.widget.delegate == nil {
		return
	}
	rect := n.Rect()
	if rect.Empty() {
		n.widget.ClearDirty()
		return
	}
	delegate := n.widget.delegate
	n.widget.syncBlinkTimer(delegate.EnsureUIRoot())
	needsSync := n.widget.Dirty() || n.NeedsPaint()
	if !needsSync {
		return
	}
	element := delegate.ComposerAreaElement()
	next := paintMeasuredSurface(ctx, measuredPainterFromElement(element), rect)
	nextCursorRect, nextCursorOK := delegate.ComposerCursorRect(rect)
	switch {
	case !n.NeedsLayout() && delegate.ComposerCursorDirty() && n.cursorValid && nextCursorOK:
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
	delegate.SetComposerAreaCache(ui.Size{W: rect.W, H: rect.H}, next)
	delegate.SetComposerCursorDirty(false)
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
	return n.widget.hash(n.widget.delegate, n.Rect()) != n.lastHash
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
		currentHash = n.widget.hash(n.widget.delegate, rect)
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

func newMainScreenRetainedRoot(delegate MainScreenDelegate, transcript *transcriptWidget, composer *composerAreaWidget, sidebar, status *hashedElementWidget) *mainScreenRetainedRoot {
	root := &mainScreenRetainedRoot{
		delegate: delegate,
	}
	root.transcriptNode = &chatViewportNode{widget: transcript}
	root.composerNode = &composerRetainedNode{widget: composer}
	root.sidebarNode = &hashedElementRetainedNode{
		widget: sidebar,
		measure: func(_ *ui.Context, constraints ui.Constraints) ui.Size {
			_, height := delegate.ViewportSize()
			return constraints.Clamp(ui.Size{W: max(0, delegate.SidebarWidth()), H: max(0, height)})
		},
	}
	root.statusNode = &hashedElementRetainedNode{
		widget: status,
		measure: func(_ *ui.Context, constraints ui.Constraints) ui.Size {
			return constraints.Clamp(ui.Size{W: constraints.MaxW, H: max(0, delegate.StatusPaneHeight())})
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
	root.bodyChildren[1] = ui.FlexNodeChild{Node: root.sidebarNode, Basis: max(0, delegate.SidebarWidth())}
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
	if r.delegate != nil && r.delegate.ShowSidebar() {
		r.bodyChildren[1].Basis = max(0, r.delegate.SidebarWidth())
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

func sidebarWidgetHash(delegate MainScreenDelegate, bounds ui.Rect) uint64 {
	return delegate.SidebarHash(bounds)
}

func statusPaneWidgetHash(delegate MainScreenDelegate, bounds ui.Rect) uint64 {
	return delegate.StatusPaneHash(bounds)
}

func NewMainScreenWidget(delegate MainScreenDelegate) *MainScreenWidget {
	widget := &MainScreenWidget{
		delegate:   delegate,
		transcript: &transcriptWidget{delegate: delegate, invalidated: true},
		composer:   &composerAreaWidget{delegate: delegate, invalidated: true},
		sidebar: &hashedElementWidget{
			delegate: delegate,
			build: func(delegate MainScreenDelegate) measuredPainter {
				if !delegate.ShowSidebar() {
					return nil
				}
				return ui.Sidebar{
					Child:  delegate.SidebarElement(),
					Height: viewportHeight(delegate),
					Width:  delegate.SidebarWidth(),
				}
			},
			hash:        sidebarWidgetHash,
			invalidated: true,
		},
		statusPane: &hashedElementWidget{
			delegate: delegate,
			build: func(delegate MainScreenDelegate) measuredPainter {
				return measuredPainterFromElement(delegate.StatusPaneElement())
			},
			hash:        statusPaneWidgetHash,
			invalidated: true,
		},
	}
	widget.retained = newMainScreenRetainedRoot(delegate, widget.transcript, widget.composer, widget.sidebar, widget.statusPane)
	return widget
}

func (w *MainScreenWidget) SetDelegate(delegate MainScreenDelegate) {
	if w == nil {
		return
	}
	w.delegate = delegate
	w.transcript.delegate = delegate
	w.composer.delegate = delegate
	w.sidebar.delegate = delegate
	w.statusPane.delegate = delegate
	if w.retained != nil {
		w.retained.delegate = delegate
	}
}

func (w *MainScreenWidget) InvalidateTranscript() { w.transcript.Invalidate() }
func (w *MainScreenWidget) InvalidateComposer()   { w.composer.Invalidate() }
func (w *MainScreenWidget) InvalidateSidebar()    { w.sidebar.Invalidate() }
func (w *MainScreenWidget) InvalidateStatusPane() { w.statusPane.Invalidate() }
func (w *MainScreenWidget) SyncComposerBlinkTimer(root *ui.Root) {
	w.composer.syncBlinkTimer(root)
}
func (w *MainScreenWidget) HandleComposerTimer(event ui.TimerEvent) bool {
	return w.composer.handleTimer(event)
}
func (w *MainScreenWidget) ComposerDirty() bool { return w.composer.Dirty() }
func (w *MainScreenWidget) ComposerSurface(ctx *ui.Context, bounds ui.Rect) ui.Surface {
	if w == nil || w.composer == nil {
		return ui.Surface{}
	}
	return w.composer.Surface(ctx, bounds)
}
func (w *MainScreenWidget) ClearStatusPaneDirty() {
	w.statusPane.ClearDirty()
}
func (w *MainScreenWidget) SidebarBasis() int {
	if w == nil || w.retained == nil {
		return 0
	}
	return w.retained.bodyChildren[1].Basis
}

func (w *MainScreenWidget) ensureRetainedRoot() *mainScreenRetainedRoot {
	if w.retained == nil {
		w.retained = newMainScreenRetainedRoot(w.delegate, w.transcript, w.composer, w.sidebar, w.statusPane)
	}
	return w.retained
}

func viewportHeight(delegate MainScreenDelegate) int {
	_, height := delegate.ViewportSize()
	return height
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
