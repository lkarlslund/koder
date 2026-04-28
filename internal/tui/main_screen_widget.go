package tui

import (
	"hash/fnv"
	"strconv"

	"github.com/lkarlslund/koder/internal/ui"
)

const mainScreenVerticalInset = 0

type transcriptWidget struct {
	model   *Model
	bounds  ui.Rect
	surface ui.Surface
	damage  []ui.Rect
	layout  bool
	dirty   bool
	valid   bool
}

func (w *transcriptWidget) Dirty() bool {
	return w == nil || w.dirty || !w.valid
}

func (w *transcriptWidget) Invalidate() {
	if w == nil {
		return
	}
	w.dirty = true
	w.valid = false
}

func (w *transcriptWidget) Prepare(bounds ui.Rect) {
	if w == nil {
		return
	}
	if w.valid && !w.dirty && w.bounds == bounds {
		w.layout = false
		w.damage = nil
		return
	}
	retained := w.model.syncRetainedTranscript()
	raw := w.model.viewport.VisibleSurface()
	surface := raw.Normalize(max(0, bounds.W), max(0, bounds.H))
	if retained != nil {
		scroll := ui.ScrollBox{
			Child:   retained,
			OffsetY: max(0, w.model.viewport.YOffset),
			Width:   max(0, bounds.W),
			Height:  max(0, bounds.H),
		}
		if raw.SurfaceWidth() != max(0, bounds.W) || raw.SurfaceHeight() != max(0, bounds.H) {
			rendered, _, _ := scroll.RenderVisible(&ui.Context{Palette: w.model.palette}, max(0, bounds.W), max(0, bounds.H), max(0, w.model.viewport.YOffset))
			surface = rendered
		} else {
			// Keep the already-computed viewport surface from refreshViewport* so
			// the cached widget cannot drift from the model's chosen alignment.
			// Re-render retained content only to refresh runtime controls for this frame.
			_, _, _ = scroll.RenderVisible(&ui.Context{Palette: w.model.palette}, max(0, bounds.W), max(0, bounds.H), max(0, w.model.viewport.YOffset))
		}
	}
	w.layout = !w.valid || w.bounds != bounds
	if !w.layout {
		w.damage = ui.DiffSurfaceDamage(w.surface, surface)
	} else {
		w.damage = fullSurfaceDamage(surface)
	}
	w.bounds = bounds
	w.surface = surface
	w.valid = true
	w.dirty = false
}

func (w *transcriptWidget) Element() ui.Element {
	if w == nil {
		return ui.VisibleElement{}
	}
	return ui.SurfaceBox{Surface: w.surface}
}

func (w *transcriptWidget) Surface(_ *ui.Context, bounds ui.Rect) ui.Surface {
	if w == nil {
		return ui.Surface{}
	}
	w.Prepare(bounds)
	return w.surface
}

func (w *transcriptWidget) DirtyRects() []ui.Rect {
	return copyRects(w.damage)
}

func (w *transcriptWidget) LayoutChanged() bool {
	return w != nil && w.layout
}

func (w *transcriptWidget) ClearDirty() {
	if w == nil {
		return
	}
	w.damage = nil
	w.layout = false
}

type composerAreaWidget struct {
	model        *Model
	bounds       ui.Rect
	measureWidth int
	measureSize  ui.Size
	measureValid bool
	surface      ui.Surface
	damage       []ui.Rect
	layout       bool
	cursorRect   ui.Rect
	cursorValid  bool
	dirty        bool
	valid        bool
}

func (w *composerAreaWidget) Dirty() bool {
	return w == nil || w.dirty || !w.valid || len(w.damage) > 0
}

func (w *composerAreaWidget) Invalidate() {
	if w == nil {
		return
	}
	w.dirty = true
	w.measureValid = false
}

func (w *composerAreaWidget) measure(ctx *ui.Context, width int) ui.Size {
	if w != nil && w.measureValid && w.measureWidth == width {
		return w.measureSize
	}
	if w == nil {
		return ui.Size{}
	}
	element := w.model.renderComposerAreaElement()
	size := element.Measure(ctx, ui.NewConstraints(width, 0))
	w.measureWidth = width
	w.measureSize = size
	w.measureValid = true
	cache := w.model.ensureRenderCache()
	cache.composerAreaHeight = size.H
	return size
}

func (w *composerAreaWidget) Prepare(ctx *ui.Context, bounds ui.Rect) {
	if w == nil {
		return
	}
	width := max(0, bounds.W)
	size := w.measure(ctx, width)
	if width <= 0 {
		width = size.W
	}
	nextBounds := ui.Rect{W: width, H: size.H}
	if w.valid && !w.dirty && !w.model.composerCursorDirty && w.bounds == nextBounds {
		w.layout = false
		w.damage = nil
		return
	}
	nextCursorRect, nextCursorOK := composerCursorRectForBounds(w.model, nextBounds)
	w.layout = !w.valid || w.bounds != nextBounds
	switch {
	case w.model.composerCursorDirty && !w.layout && w.cursorValid && nextCursorOK:
		damage := ui.DamageSet{}
		damage.Add(w.cursorRect)
		damage.Add(nextCursorRect)
		w.damage = damage.Rects()
	case !w.layout:
		w.damage = []ui.Rect{{W: nextBounds.W, H: nextBounds.H}}
	default:
		w.damage = fullRectDamage(nextBounds)
	}
	w.bounds = nextBounds
	w.cursorRect = nextCursorRect
	w.cursorValid = nextCursorOK
	w.valid = true
	w.dirty = false
	cache := w.model.ensureRenderCache()
	cache.composerAreaValid = nextBounds.H > 0
	cache.renderedComposerAreaSurface = ui.Surface{}
	w.model.composerCursorDirty = false
}

func (w *composerAreaWidget) Element() ui.Element {
	if w == nil {
		return ui.VisibleElement{}
	}
	return w.model.renderComposerAreaElement()
}

func (w *composerAreaWidget) ClearDirty() {
	if w == nil {
		return
	}
	w.damage = nil
	w.layout = false
}

func (w *composerAreaWidget) Surface(ctx *ui.Context, bounds ui.Rect) ui.Surface {
	if w == nil {
		return ui.Surface{}
	}
	if w.valid && !w.dirty && w.bounds == bounds {
		return w.surface
	}
	w.Prepare(ctx, bounds)
	surface := w.Element().Render(ctx, w.bounds)
	w.surface = surface
	cache := w.model.ensureRenderCache()
	cache.composerAreaValid = true
	cache.renderedComposerAreaSurface = surface
	return surface
}

func (w *composerAreaWidget) DirtyRects() []ui.Rect {
	return copyRects(w.damage)
}

func (w *composerAreaWidget) LayoutChanged() bool {
	return w != nil && w.layout
}

type hashedElementWidget struct {
	model    *Model
	build    func(*Model) ui.Element
	hash     func(*Model, ui.Rect) uint64
	bounds   ui.Rect
	surface  ui.Surface
	damage   []ui.Rect
	layout   bool
	lastHash uint64
	dirty    bool
	valid    bool
}

func (w *hashedElementWidget) Dirty() bool {
	if w == nil || w.dirty || !w.valid || len(w.damage) > 0 {
		return true
	}
	if w.hash == nil {
		return false
	}
	return w.hash(w.model, w.bounds) != w.lastHash
}

func (w *hashedElementWidget) Invalidate() {
	if w == nil {
		return
	}
	w.dirty = true
}

func (w *hashedElementWidget) Prepare(bounds ui.Rect) {
	if w == nil {
		return
	}
	currentHash := uint64(0)
	if w.hash != nil {
		currentHash = w.hash(w.model, bounds)
	}
	if w.valid && !w.dirty && w.bounds == bounds && currentHash == w.lastHash {
		w.layout = false
		w.damage = nil
		return
	}
	w.layout = !w.valid || w.bounds != bounds
	if !w.layout {
		w.damage = fullRectDamage(bounds)
	} else {
		w.damage = fullRectDamage(bounds)
	}
	w.bounds = bounds
	w.lastHash = currentHash
	w.valid = true
	w.dirty = false
}

func (w *hashedElementWidget) Element() ui.Element {
	if w == nil || w.build == nil {
		return ui.VisibleElement{}
	}
	if built := w.build(w.model); built != nil {
		return ui.VisibleElement{Child: built}
	}
	return ui.VisibleElement{}
}

func (w *hashedElementWidget) ClearDirty() {
	if w == nil {
		return
	}
	w.damage = nil
	w.layout = false
}

func (w *hashedElementWidget) Surface(ctx *ui.Context, bounds ui.Rect) ui.Surface {
	if w == nil {
		return ui.Surface{}
	}
	currentHash := uint64(0)
	if w.hash != nil {
		currentHash = w.hash(w.model, bounds)
	}
	if w.valid && !w.dirty && w.bounds == bounds && currentHash == w.lastHash {
		return w.surface
	}
	w.Prepare(bounds)
	element := w.Element()
	surface := element.Render(ctx, bounds)
	w.surface = surface
	return surface
}

func (w *hashedElementWidget) DirtyRects() []ui.Rect {
	return copyRects(w.damage)
}

func (w *hashedElementWidget) LayoutChanged() bool {
	return w != nil && w.layout
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
	root.PrepareDirty(ctx)
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
	root.PrepareDirty(ctx)
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
	model              *Model
	transcriptWidget   *transcriptWidget
	composerWidget     *composerAreaWidget
	sidebarWidget      *hashedElementWidget
	statusWidget       *hashedElementWidget
	transcriptNode     *ui.ManagedElementNode
	composerNode       *ui.ManagedElementNode
	sidebarNode        *ui.ManagedElementNode
	statusNode         *ui.ManagedElementNode
	transcriptPaneRect ui.Rect
}

func newMainScreenRetainedRoot(m *Model, transcript *transcriptWidget, composer *composerAreaWidget, sidebar, status *hashedElementWidget) *mainScreenRetainedRoot {
	root := &mainScreenRetainedRoot{
		model:            m,
		transcriptWidget: transcript,
		composerWidget:   composer,
		sidebarWidget:    sidebar,
		statusWidget:     status,
	}
	root.transcriptNode = &ui.ManagedElementNode{
		ElementNode: ui.ElementNode{
			MeasureFn: func(_ *ui.Context, constraints ui.Constraints) ui.Size {
				return constraints.Clamp(ui.Size{W: max(0, m.viewport.Width), H: max(0, m.viewport.Height)})
			},
			ElementFn: func(ctx *ui.Context) ui.Element {
				return transcript.Element()
			},
		},
		PrepareFn: func(_ *ui.Context, rect ui.Rect) {
			transcript.Prepare(rect)
		},
		DirtyFn:         transcript.Dirty,
		DirtyRectsFn:    transcript.DirtyRects,
		LayoutChangedFn: transcript.LayoutChanged,
		ClearFn:         transcript.ClearDirty,
	}
	root.composerNode = &ui.ManagedElementNode{
		ElementNode: ui.ElementNode{
			MeasureFn: func(ctx *ui.Context, constraints ui.Constraints) ui.Size {
				return composer.measure(ctx, constraints.MaxW)
			},
			ElementFn: func(ctx *ui.Context) ui.Element {
				return composer.Element()
			},
		},
		PrepareFn:       composer.Prepare,
		DirtyFn:         composer.Dirty,
		DirtyRectsFn:    composer.DirtyRects,
		LayoutChangedFn: composer.LayoutChanged,
		ClearFn:         composer.ClearDirty,
	}
	root.sidebarNode = &ui.ManagedElementNode{
		ElementNode: ui.ElementNode{
			MeasureFn: func(_ *ui.Context, constraints ui.Constraints) ui.Size {
				return constraints.Clamp(ui.Size{W: max(0, m.sidebarWidth()), H: max(0, m.viewport.Height)})
			},
			ElementFn: func(ctx *ui.Context) ui.Element {
				return sidebar.Element()
			},
		},
		PrepareFn: func(_ *ui.Context, rect ui.Rect) {
			sidebar.Prepare(rect)
		},
		DirtyFn:         sidebar.Dirty,
		DirtyRectsFn:    sidebar.DirtyRects,
		LayoutChangedFn: sidebar.LayoutChanged,
		ClearFn:         sidebar.ClearDirty,
	}
	root.statusNode = &ui.ManagedElementNode{
		ElementNode: ui.ElementNode{
			MeasureFn: func(_ *ui.Context, constraints ui.Constraints) ui.Size {
				return constraints.Clamp(ui.Size{W: constraints.MaxW, H: max(0, m.statusPaneHeight())})
			},
			ElementFn: func(ctx *ui.Context) ui.Element {
				return status.Element()
			},
		},
		PrepareFn: func(_ *ui.Context, rect ui.Rect) {
			status.Prepare(rect)
		},
		DirtyFn:         status.Dirty,
		DirtyRectsFn:    status.DirtyRects,
		LayoutChangedFn: status.LayoutChanged,
		ClearFn:         status.ClearDirty,
	}
	return root
}

func (r *mainScreenRetainedRoot) Layout(ctx *ui.Context, rect ui.Rect) {
	r.BaseNode.Layout(ctx, rect)
	statusSize := r.statusNode.Measure(ctx, ui.NewConstraints(rect.W, 0))
	statusH := max(0, min(rect.H, statusSize.H))
	bodyH := max(0, rect.H-statusH)
	sidebarW := 0
	if r.model.showSidebar {
		sidebarW = max(0, r.model.sidebarWidth())
	}
	gap := 0
	if sidebarW > 0 {
		gap = 1
	}
	mainW := max(0, rect.W-sidebarW-gap)
	composerSize := r.composerNode.Measure(ctx, ui.NewConstraints(mainW, 0))
	composerH := max(0, min(bodyH, composerSize.H))
	transcriptPaneH := max(0, bodyH-composerH)
	transcriptSize := r.transcriptNode.Measure(ctx, ui.NewConstraints(mainW, transcriptPaneH))
	transcriptH := max(0, min(transcriptPaneH, transcriptSize.H))
	transcriptY := max(0, transcriptPaneH-transcriptH)
	r.transcriptPaneRect = ui.Rect{W: mainW, H: transcriptPaneH}
	r.transcriptNode.Layout(ctx, ui.Rect{X: 0, Y: transcriptY, W: mainW, H: transcriptH})
	r.composerNode.Layout(ctx, ui.Rect{X: 0, Y: transcriptPaneH, W: mainW, H: composerH})
	if sidebarW > 0 {
		r.sidebarNode.Layout(ctx, ui.Rect{X: mainW + gap, Y: 0, W: sidebarW, H: bodyH})
	} else {
		r.sidebarNode.Layout(ctx, ui.Rect{})
	}
	r.statusNode.Layout(ctx, ui.Rect{X: 0, Y: bodyH, W: rect.W, H: statusH})
}

func (r *mainScreenRetainedRoot) Paint(ctx *ui.Context, canvas ui.Canvas) {
	r.PaintAll(ctx, canvas)
}

func (r *mainScreenRetainedRoot) PrepareDirty(ctx *ui.Context) {
	r.forEachNode(func(node *ui.ManagedElementNode) {
		node.PrepareDirty(ctx)
	})
}

func (r *mainScreenRetainedRoot) PaintAll(ctx *ui.Context, canvas ui.Canvas) {
	palette := r.model.palette
	canvas.Fill(r.transcriptPaneRect, ui.CellStyle{BG: ui.CellColorFromLipgloss(palette.ScreenBackground)})
	r.paintNode(ctx, canvas, r.transcriptNode)
	r.paintNode(ctx, canvas, r.composerNode)
	r.paintNode(ctx, canvas, r.sidebarNode)
	r.paintNode(ctx, canvas, r.statusNode)
}

func (r *mainScreenRetainedRoot) PaintDirty(ctx *ui.Context, canvas ui.Canvas) {
	palette := r.model.palette
	if r.transcriptNode.NeedsPaint() {
		canvas.Fill(r.transcriptPaneRect, ui.CellStyle{BG: ui.CellColorFromLipgloss(palette.ScreenBackground)})
	}
	r.forEachNode(func(node *ui.ManagedElementNode) {
		if !node.NeedsPaint() || node.Rect().Empty() {
			return
		}
		node.Paint(ctx, canvas.Subrect(node.Rect()))
	})
}

func (r *mainScreenRetainedRoot) paintNode(ctx *ui.Context, canvas ui.Canvas, node ui.Node) {
	rect := node.Rect()
	if rect.Empty() {
		return
	}
	node.Paint(ctx, canvas.Subrect(rect))
}

func (r *mainScreenRetainedRoot) DirtyRects() []ui.Rect {
	damage := ui.DamageSet{}
	damage.AddAll(r.BaseNode.DirtyRects())
	r.forEachNode(func(node *ui.ManagedElementNode) {
		damage.AddAll(node.DirtyRects())
	})
	return damage.Rects()
}

func (r *mainScreenRetainedRoot) ClearDirty() {
	r.BaseNode.ClearDirty()
	r.forEachNode(func(node *ui.ManagedElementNode) {
		node.ClearFrameDirty()
	})
}

func (r *mainScreenRetainedRoot) forEachNode(visit func(*ui.ManagedElementNode)) {
	if r == nil || visit == nil {
		return
	}
	visit(r.transcriptNode)
	visit(r.composerNode)
	visit(r.sidebarNode)
	visit(r.statusNode)
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
	line := m.composer.VisibleLine()
	promptWidth := ui.PlainWidth(m.promptGlyph() + " ")
	x := promptWidth + ui.PlainWidth(line.Before())
	width := ui.PlainWidth(line.Cursor())
	if width <= 0 {
		width = 1
	}
	y := max(0, bounds.H-2)
	if x >= bounds.W || y >= bounds.H {
		return ui.Rect{}, false
	}
	if x+width > bounds.W {
		width = bounds.W - x
	}
	if width <= 0 {
		return ui.Rect{}, false
	}
	return ui.Rect{X: x, Y: y, W: width, H: 1}, true
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
		transcript: &transcriptWidget{model: m, dirty: true},
		composer:   &composerAreaWidget{model: m, dirty: true},
		sidebar: &hashedElementWidget{
			model: m,
			build: func(m *Model) ui.Element {
				if !m.showSidebar {
					return nil
				}
				return ui.Sidebar{
					Child:  ui.TextPane{Content: m.renderSidebar()},
					Height: m.viewport.Height,
					Width:  m.sidebarWidth(),
				}
			},
			hash:  sidebarWidgetHash,
			dirty: true,
		},
		statusPane: &hashedElementWidget{
			model: m,
			build: func(m *Model) ui.Element { return m.renderStatusPaneElement() },
			hash:  statusPaneWidgetHash,
			dirty: true,
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
