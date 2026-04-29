package tui

import (
	"testing"

	"github.com/lkarlslund/koder/internal/theme"
	"github.com/lkarlslund/koder/internal/ui"
	"github.com/lkarlslund/koder/internal/ui/textarea"
)

func TestMainScreenRetainedRootPrepareDirtyUsesNodeFlags(t *testing.T) {
	m := Model{
		cfg:         testConfig(t),
		palette:     theme.Default().Palette,
		viewport:    newTranscriptViewport(80, 20),
		renderCache: &modelRenderCache{},
		composer:    textarea.New(),
		width:       80,
		height:      24,
	}
	m.composer.SetValue("draft text")
	w := m.ensureMainScreenWidget()
	ctx := &ui.Context{Palette: m.palette}
	bounds := ui.Rect{W: 80, H: 24}

	_ = w.Surface(ctx, bounds)

	w.composer.Invalidate()
	root := w.ensureRetainedRoot()
	root.model = &m
	root.Layout(ctx, bounds)
	root.Prepare(ctx)

	if !root.composerNode.NeedsPaint() {
		t.Fatal("expected composer node to be paint-dirty")
	}
	if root.transcriptNode.NeedsPaint() {
		t.Fatal("expected transcript node to stay clean")
	}
	if root.sidebarNode.NeedsPaint() {
		t.Fatal("expected sidebar node to stay clean")
	}
	if root.statusNode.NeedsPaint() {
		t.Fatal("expected status node to stay clean")
	}
}

func TestMainScreenRetainedRootLayoutChangeMarksNodesDirtyWithoutWidgetInvalidation(t *testing.T) {
	m := Model{
		cfg:         testConfig(t),
		palette:     theme.Default().Palette,
		viewport:    newTranscriptViewport(80, 20),
		renderCache: &modelRenderCache{},
		composer:    textarea.New(),
		width:       80,
		height:      24,
	}
	m.composer.SetValue("draft text")
	w := m.ensureMainScreenWidget()
	ctx := &ui.Context{Palette: m.palette}

	_ = w.Surface(ctx, ui.Rect{W: 80, H: 24})

	root := w.ensureRetainedRoot()
	root.model = &m
	root.Layout(ctx, ui.Rect{W: 90, H: 24})
	root.Prepare(ctx)

	if !root.transcriptNode.NeedsPaint() {
		t.Fatal("expected transcript node to be paint-dirty after layout change")
	}
	if !root.composerNode.NeedsPaint() {
		t.Fatal("expected composer node to be paint-dirty after layout change")
	}
}

func TestMainScreenWidgetRepaintsFullyAfterComposerHeightChange(t *testing.T) {
	m := Model{
		cfg:         testConfig(t),
		palette:     theme.Default().Palette,
		viewport:    newTranscriptViewport(80, 20),
		renderCache: &modelRenderCache{},
		composer:    textarea.New(),
		width:       80,
		height:      24,
	}
	m.composer.SetValue("draft text")
	w := m.ensureMainScreenWidget()
	ctx := &ui.Context{Palette: m.palette}
	bounds := ui.Rect{W: 80, H: 24}

	base := w.Surface(ctx, bounds)
	if base.SurfaceHeight() != bounds.H {
		t.Fatalf("expected initial surface height %d, got %d", bounds.H, base.SurfaceHeight())
	}

	m.composer.SetValue("draft text\nsecond line\nthird line")
	m.invalidateFooterCache()
	if !w.dirty() {
		t.Fatal("expected composer height change to invalidate main screen widget")
	}

	next := w.Surface(ctx, bounds)

	root := w.ensureRetainedRoot()
	root.model = &m
	root.Layout(ctx, bounds)
	full := ui.TransparentSurface(bounds.W, bounds.H)
	root.PaintAll(ctx, ui.NewCanvas(&full, bounds))

	if diff := ui.DiffSurfaceDamage(next, full); len(diff) > 0 {
		t.Fatalf("widget repaint diverged from full repaint: %#v", diff)
	}
}
