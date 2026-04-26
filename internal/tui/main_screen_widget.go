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
	w.bounds = bounds
	w.surface = surface
	w.valid = true
	w.dirty = false
	return surface
}

type composerAreaWidget struct {
	model        *Model
	bounds       ui.Rect
	measureWidth int
	measureSize  ui.Size
	measureValid bool
	surface      ui.Surface
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
	w.bounds = ui.Rect{W: width, H: size.H}
	w.surface = surface
	w.valid = true
	w.dirty = false
	cache := w.model.ensureRenderCache()
	cache.composerAreaValid = true
	cache.renderedComposerAreaSurface = surface
	return surface
}

type hashedElementWidget struct {
	model    *Model
	build    func(*Model) ui.Element
	hash     func(*Model, ui.Rect) uint64
	bounds   ui.Rect
	surface  ui.Surface
	lastHash uint64
	dirty    bool
	valid    bool
}

func (w *hashedElementWidget) Dirty() bool {
	return w == nil || w.dirty || !w.valid
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
	w.bounds = bounds
	w.surface = surface
	w.lastHash = currentHash
	w.valid = true
	w.dirty = false
	return surface
}

type mainScreenWidget struct {
	model      *Model
	transcript *transcriptWidget
	composer   *composerAreaWidget
	sidebar    *hashedElementWidget
	statusPane *hashedElementWidget
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
	transcriptBounds := ui.Rect{W: max(0, w.model.viewport.Width), H: max(0, w.model.transcriptViewportHeight())}
	composerBounds := ui.Rect{W: max(0, w.model.composerWidth())}
	composerSurface := w.composer.Surface(ctx, composerBounds)
	composerBounds.H = composerSurface.Size().H
	sidebarBounds := ui.Rect{W: max(0, w.model.sidebarWidth()), H: max(0, w.model.viewport.Height)}
	statusBounds := ui.Rect{W: max(0, bounds.W), H: max(0, w.model.statusPaneHeight())}

	transcriptSurface := w.transcript.Surface(ctx, transcriptBounds)
	sidebarSurface := w.sidebar.Surface(ctx, sidebarBounds)
	statusSurface := w.statusPane.Surface(ctx, statusBounds)

	var transcriptElement ui.Element = w.model.renderTranscriptPaneElement(ui.Align{
		Vertical: ui.AlignEnd,
		Child:    ui.SurfaceBox{Surface: transcriptSurface},
	})
	mainChildren := []ui.Child{
		ui.Flex(transcriptElement, 1),
		ui.Fixed(ui.VisibleElement{
			Child: ui.SurfaceBox{Surface: composerSurface},
			BoxProps: ui.BoxProps{
				Hidden: composerSurface.Size().H == 0,
			},
		}),
	}
	mainColumn := ui.FlexBox{Direction: ui.DirectionVertical, Children: mainChildren}
	sidebarElement := ui.VisibleElement{
		BoxProps: ui.BoxProps{
			Hidden: !w.model.showSidebar || sidebarSurface.Size().W == 0,
		},
		Child: ui.SurfaceBox{Surface: sidebarSurface},
	}
	rootChildren := []ui.Child{
		ui.Flex(ui.FlexBox{
			Direction: ui.DirectionHorizontal,
			Children: []ui.Child{
				ui.Flex(ui.Inset{Padding: ui.SymmetricInsets(mainScreenVerticalInset, 0), Child: mainColumn}, 1),
				ui.Fixed(ui.Spacer{W: 1}),
				ui.Fixed(sidebarElement),
			},
		}, 1),
		ui.Fixed(ui.VisibleElement{
			Child: ui.SurfaceBox{Surface: statusSurface},
			BoxProps: ui.BoxProps{
				Hidden: statusSurface.Size().H == 0,
			},
		}),
	}
	surface := ui.FlexBox{Direction: ui.DirectionVertical, Children: rootChildren}.Render(ctx, bounds)
	w.bounds = bounds
	w.surface = surface
	w.valid = true
	cache := w.model.ensureRenderCache()
	cache.renderedBodySurface = surface
	cache.bodyValid = true
	return surface
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
	return m.mainScreen
}
