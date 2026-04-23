package tui

import (
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/theme"
	"github.com/lkarlslund/koder/internal/ui/textarea"
)

func newRuntimeTestModel(t *testing.T) Model {
	t.Helper()
	composer := textarea.New()
	composer.Focus()
	return Model{
		cfg:         testConfig(t),
		palette:     theme.Default().Palette,
		viewport:    newTranscriptViewport(80, 18),
		renderCache: &modelRenderCache{},
		composer:    composer,
		width:       80,
		height:      24,
		showSidebar: true,
	}
}

func TestSyncUIRootFocusesTopmostModal(t *testing.T) {
	m := newRuntimeTestModel(t)
	m.openHelpModal()

	root := m.syncUIRoot()
	if got := root.FocusedWindow(); got != helpWindowID {
		t.Fatalf("expected help modal focus, got %q", got)
	}

	m.closeHelpModal()
	root = m.syncUIRoot()
	if got := root.FocusedWindow(); got != mainWindowID {
		t.Fatalf("expected focus to return to main window, got %q", got)
	}
}

func TestViewSurfaceComposesMainWindowBehindModal(t *testing.T) {
	m := newRuntimeTestModel(t)
	m.status = "Ready"
	m.openHelpModal()

	frame := m.viewSurface().String()
	if !strings.Contains(frame, "Help") || !strings.Contains(frame, "Hotkeys") {
		t.Fatalf("expected help modal content in frame, got %q", frame)
	}
	if !strings.Contains(frame, "Session 0") {
		t.Fatalf("expected main window content to remain in composed frame, got %q", frame)
	}
}
