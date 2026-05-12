package app

import (
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/theme"
	"github.com/lkarlslund/koder/internal/ui"
	"github.com/lkarlslund/koder/internal/ui/textarea"
)

func TestMainScreenViewPrepareDirtyUsesNodeFlags(t *testing.T) {
	m := App{
		cfg:         testConfig(t),
		palette:     theme.Default().Palette,
		viewport:    newTranscriptViewport(80, 20),
		renderCache: &modelRenderCache{},
		composer:    textarea.New(),
		width:       80,
		height:      24,
	}
	m.composer.SetValue("draft text")
	w := m.ensureMainScreenView()
	ctx := &ui.Context{Palette: m.palette}
	bounds := ui.Rect{W: 80, H: 24}

	_ = w.Surface(ctx, bounds)

	w.InvalidateComposer()
	next := w.Surface(ctx, bounds)
	rects, _ := next.DirtyRects()
	if len(rects) == 0 {
		t.Fatal("expected composer invalidation to produce retained damage")
	}
	if len(rects) >= 1 && rects[0] == (ui.Rect{W: bounds.W, H: bounds.H}) {
		t.Fatal("expected composer invalidation to avoid a full-screen repaint")
	}
}

func TestMainScreenViewClearsSidebarWhenHidden(t *testing.T) {
	view := newMainScreenView()
	ctx := &ui.Context{Palette: theme.Default().Palette}
	bounds := ui.Rect{W: 40, H: 8}
	sidebar := ui.AsNode(ui.Sidebar{
		Child:  &ui.RetainedLabel{Text: "SIDEBAR"},
		Width:  12,
		Height: bounds.H,
	})

	view.SetSidebar(true, 12, sidebar, 1)
	first := view.Surface(ctx, bounds)
	if !strings.Contains(strings.Join(first.Lines(), "\n"), "SIDEBAR") {
		t.Fatalf("expected sidebar text in initial render, got %q", first.Lines())
	}

	view.SetSidebar(false, 0, nil, 2)
	next := view.Surface(ctx, bounds)
	if strings.Contains(strings.Join(next.Lines(), "\n"), "SIDEBAR") {
		t.Fatalf("expected hidden sidebar to clear retained pixels, got %q", next.Lines())
	}
}

func TestMainScreenViewLayoutChangeMarksNodesDirtyWithoutInvalidation(t *testing.T) {
	m := App{
		cfg:         testConfig(t),
		palette:     theme.Default().Palette,
		viewport:    newTranscriptViewport(80, 20),
		renderCache: &modelRenderCache{},
		composer:    textarea.New(),
		width:       80,
		height:      24,
	}
	m.composer.SetValue("draft text")
	w := m.ensureMainScreenView()
	ctx := &ui.Context{Palette: m.palette}

	_ = w.Surface(ctx, ui.Rect{W: 80, H: 24})

	next := w.Surface(ctx, ui.Rect{W: 90, H: 24})
	if next.SurfaceWidth() != 90 {
		t.Fatalf("expected resized surface width 90, got %d", next.SurfaceWidth())
	}
	rects, _ := next.DirtyRects()
	if len(rects) == 0 {
		t.Fatal("expected layout change to produce retained damage")
	}
}

func TestMainScreenViewRepaintsFullyAfterComposerHeightChange(t *testing.T) {
	m := App{
		cfg:         testConfig(t),
		palette:     theme.Default().Palette,
		viewport:    newTranscriptViewport(80, 20),
		renderCache: &modelRenderCache{},
		composer:    textarea.New(),
		width:       80,
		height:      24,
	}
	m.composer.SetValue("draft text")
	w := m.ensureMainScreenView()
	ctx := &ui.Context{Palette: m.palette}
	bounds := ui.Rect{W: 80, H: 24}

	base := w.Surface(ctx, bounds)
	if base.SurfaceHeight() != bounds.H {
		t.Fatalf("expected initial surface height %d, got %d", bounds.H, base.SurfaceHeight())
	}

	m.composer.SetValue("draft text\nsecond line\nthird line")
	m.invalidateFooterCache()
	if !w.Dirty() {
		t.Fatal("expected composer height change to invalidate main screen widget")
	}

	next := w.Surface(ctx, bounds)

	m.mainScreen = nil
	fullWidget := m.ensureMainScreenView()
	fullWidget.Invalidate()
	full := fullWidget.Surface(ctx, bounds)

	if diff := ui.DiffSurfaceDamage(next, full); len(diff) > 0 {
		t.Fatalf("widget repaint diverged from full repaint: %#v", diff)
	}
}
