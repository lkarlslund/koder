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
	root.PrepareDirty(ctx)

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
	root.PrepareDirty(ctx)

	if !root.transcriptNode.NeedsPaint() {
		t.Fatal("expected transcript node to be paint-dirty after layout change")
	}
	if !root.composerNode.NeedsPaint() {
		t.Fatal("expected composer node to be paint-dirty after layout change")
	}
	if !root.statusNode.NeedsPaint() {
		t.Fatal("expected status node to be paint-dirty after layout change")
	}
}
