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

func (w *transcriptWidget) Surface(_ *ui.Context, bounds ui.Rect) ui.Surface {
	if w == nil {
		return ui.Surface{}
	}
	if w.valid && !w.dirty && w.bounds == bounds {
		return w.surface
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
	return surface
}

func (w *transcriptWidget) DirtyRects() []ui.Rect {
	return copyRects(w.damage)
}

func (w *transcriptWidget) LayoutChanged() bool {
	return w != nil && w.layout
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
	return w == nil || w.dirty || !w.valid
}

func (w *composerAreaWidget) Invalidate() {
	if w == nil {
		return
	}
	w.dirty = true
	w.valid = false
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

func (w *composerAreaWidget) Surface(ctx *ui.Context, bounds ui.Rect) ui.Surface {
	if w == nil {
		return ui.Surface{}
	}
	if w.valid && !w.dirty && w.bounds == bounds {
		return w.surface
	}
	width := max(0, bounds.W)
	size := w.measure(ctx, width)
	if width <= 0 {
		width = size.W
	}
	element := w.model.renderComposerAreaElement()
	surface := element.Render(ctx, ui.Rect{W: width, H: size.H})
	nextBounds := ui.Rect{W: width, H: size.H}
	nextCursorRect, nextCursorOK := composerCursorRect(w.model, surface)
	w.layout = !w.valid || w.bounds != nextBounds
	if w.model.composerCursorDirty && !w.layout && w.cursorValid && nextCursorOK {
		damage := ui.DamageSet{}
		damage.Add(w.cursorRect)
		damage.Add(nextCursorRect)
		w.damage = damage.Rects()
	} else if !w.layout {
		w.damage = ui.DiffSurfaceDamage(w.surface, surface)
	} else {
		w.damage = fullSurfaceDamage(surface)
	}
	w.bounds = nextBounds
	w.surface = surface
	w.cursorRect = nextCursorRect
	w.cursorValid = nextCursorOK
	w.valid = true
	w.dirty = false
	cache := w.model.ensureRenderCache()
	cache.composerAreaValid = true
	cache.renderedComposerAreaSurface = surface
	w.model.composerCursorDirty = false
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
	if w == nil || w.dirty || !w.valid {
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
	w.valid = false
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
	element := ui.VisibleElement{}
	if w.build != nil {
		if built := w.build(w.model); built != nil {
			element = ui.VisibleElement{Child: built}
		}
	}
	surface := element.Render(ctx, bounds)
	w.layout = !w.valid || w.bounds != bounds
	if !w.layout {
		w.damage = ui.DiffSurfaceDamage(w.surface, surface)
	} else {
		w.damage = fullSurfaceDamage(surface)
	}
	w.bounds = bounds
	w.surface = surface
	w.lastHash = currentHash
	w.valid = true
	w.dirty = false
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
	surface := ui.TransparentSurface(bounds.W, bounds.H)
	root.Layout(ctx, bounds)
	root.Paint(ctx, ui.NewCanvas(&surface, bounds))
	if rects := root.DirtyRects(); len(rects) > 0 {
		surface = surface.WithDirtyRects(rects...)
	}
	root.ClearDirty()
	w.bounds = bounds
	w.surface = surface
	w.valid = true
	cache := w.model.ensureRenderCache()
	cache.renderedBodySurface = surface
	cache.bodyValid = true
	return surface
}

type mainScreenRetainedRoot struct {
	ui.BaseNode
	model              *Model
	transcriptWidget   *transcriptWidget
	composerWidget     *composerAreaWidget
	sidebarWidget      *hashedElementWidget
	statusWidget       *hashedElementWidget
	transcriptNode     *ui.SurfaceNode
	composerNode       *ui.SurfaceNode
	sidebarNode        *ui.SurfaceNode
	statusNode         *ui.SurfaceNode
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
	root.transcriptNode = &ui.SurfaceNode{
		MeasureFn: func(_ *ui.Context, constraints ui.Constraints) ui.Size {
			return constraints.Clamp(ui.Size{W: max(0, m.viewport.Width), H: max(0, m.transcriptViewportHeight())})
		},
		RenderFn: func(ctx *ui.Context, bounds ui.Rect) ui.Surface {
			return transcript.Surface(ctx, ui.Rect{W: bounds.W, H: bounds.H})
		},
	}
	root.composerNode = &ui.SurfaceNode{
		MeasureFn: func(ctx *ui.Context, constraints ui.Constraints) ui.Size {
			return composer.measure(ctx, constraints.MaxW)
		},
		RenderFn: func(ctx *ui.Context, bounds ui.Rect) ui.Surface {
			return composer.Surface(ctx, ui.Rect{W: bounds.W})
		},
	}
	root.sidebarNode = &ui.SurfaceNode{
		MeasureFn: func(_ *ui.Context, constraints ui.Constraints) ui.Size {
			return constraints.Clamp(ui.Size{W: max(0, m.sidebarWidth()), H: max(0, m.viewport.Height)})
		},
		RenderFn: func(ctx *ui.Context, bounds ui.Rect) ui.Surface {
			return sidebar.Surface(ctx, bounds)
		},
	}
	root.statusNode = &ui.SurfaceNode{
		MeasureFn: func(_ *ui.Context, constraints ui.Constraints) ui.Size {
			return constraints.Clamp(ui.Size{W: constraints.MaxW, H: max(0, m.statusPaneHeight())})
		},
		RenderFn: func(ctx *ui.Context, bounds ui.Rect) ui.Surface {
			return status.Surface(ctx, bounds)
		},
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
	palette := r.model.palette
	canvas.Fill(r.transcriptPaneRect, ui.CellStyle{BG: ui.CellColorFromLipgloss(palette.ScreenBackground)})
	for _, node := range []*ui.SurfaceNode{r.transcriptNode, r.composerNode, r.sidebarNode, r.statusNode} {
		rect := node.Rect()
		if rect.Empty() {
			continue
		}
		node.Paint(ctx, canvas.Subrect(rect))
	}
}

func (r *mainScreenRetainedRoot) DirtyRects() []ui.Rect {
	damage := ui.DamageSet{}
	damage.AddAll(r.BaseNode.DirtyRects())
	for _, node := range []*ui.SurfaceNode{r.transcriptNode, r.composerNode, r.sidebarNode, r.statusNode} {
		damage.AddAll(node.DirtyRects())
	}
	return damage.Rects()
}

func (r *mainScreenRetainedRoot) ClearDirty() {
	r.BaseNode.ClearDirty()
	for _, node := range []*ui.SurfaceNode{r.transcriptNode, r.composerNode, r.sidebarNode, r.statusNode} {
		node.ClearDirty()
	}
}

func fullSurfaceDamage(surface ui.Surface) []ui.Rect {
	if surface.SurfaceWidth() <= 0 || surface.SurfaceHeight() <= 0 {
		return nil
	}
	return []ui.Rect{{W: surface.SurfaceWidth(), H: surface.SurfaceHeight()}}
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
	if m == nil || surface.SurfaceHeight() < 2 || !m.shouldShowComposerArea() {
		return ui.Rect{}, false
	}
	line := m.composer.VisibleLine()
	promptWidth := ui.PlainWidth(m.promptGlyph() + " ")
	x := promptWidth + ui.PlainWidth(line.Before())
	width := ui.PlainWidth(line.Cursor())
	if width <= 0 {
		width = 1
	}
	y := max(0, surface.SurfaceHeight()-2)
	if x >= surface.SurfaceWidth() || y >= surface.SurfaceHeight() {
		return ui.Rect{}, false
	}
	if x+width > surface.SurfaceWidth() {
		width = surface.SurfaceWidth() - x
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
