package tui

import (
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/theme"
	"github.com/lkarlslund/koder/internal/ui"
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

	frame := strings.Join(m.viewSurface().Lines(), "\n")
	if !strings.Contains(frame, "Help") || !strings.Contains(frame, "Hotkeys") {
		t.Fatalf("expected help modal content in frame, got %q", frame)
	}
	if !strings.Contains(frame, "Session 0") {
		t.Fatalf("expected main window content to remain in composed frame, got %q", frame)
	}
}

func TestResumeClosesSessionDialogAndRestoresTyping(t *testing.T) {
	m := newRuntimeTestModel(t)
	m.sessions = []domain.Session{{ID: 7, Title: "Session A"}}
	m.openSessionPicker()

	m = m.UpdateLoad(loadMsg{
		current:  domain.Session{ID: 7, Title: "Session A"},
		sessions: m.sessions,
		parts:    map[int64][]domain.Part{},
	})
	if m.hasSessionDialog() {
		t.Fatal("expected session dialog to close after loading")
	}
	if got := m.syncUIRoot().FocusedWindow(); got != mainWindowID {
		t.Fatalf("expected focus to return to main window, got %q", got)
	}

	updated, cmd := m.Update(ui.KeyMsg{Type: ui.KeyRunes, Runes: []rune("i")})
	next := asModelPtr(t, updated)
	if got := next.composer.Value(); got != "i" {
		t.Fatalf("expected typing to reach composer after resume, got %q", got)
	}
	if cmd == nil {
		t.Fatal("expected root timer command to continue after local typing")
	}
}

func TestMainWindowFocusSyncsComposerLifecycle(t *testing.T) {
	m := newRuntimeTestModel(t)
	m.sessions = []domain.Session{{ID: 7, Title: "Session A"}}
	m.openSessionPicker()
	m.composer.SetValue("draft")
	cache := m.ensureRenderCache()
	cache.bodyValid = true
	cache.renderedBodySurface = ui.BlankSurface(10, 3)

	root := m.syncUIRoot()
	if got := root.FocusedWindow(); got != sessionWindowID {
		t.Fatalf("expected session dialog focus, got %q", got)
	}
	if m.composer.Focused() {
		t.Fatal("expected composer to blur while session dialog is focused")
	}
	if timers := root.ActiveTimers(composerBlinkTimerOwner); len(timers) != 0 {
		t.Fatalf("expected composer blink timer to stop behind modal, got %v", timers)
	}

	m.closeSessionDialog()
	root = m.syncUIRoot()
	if got := root.FocusedWindow(); got != mainWindowID {
		t.Fatalf("expected focus to return to main window, got %q", got)
	}
	if !m.composer.Focused() {
		t.Fatal("expected main window focus to refocus the composer")
	}
	if timers := root.ActiveTimers(composerBlinkTimerOwner); len(timers) == 0 {
		t.Fatal("expected main window focus to restart the composer blink timer")
	}
	if !m.ensureRenderCache().bodyValid {
		t.Fatal("expected focus transition to keep the cached main screen surface for patching")
	}
	if m.ensureRenderCache().composerAreaValid {
		t.Fatal("expected focus transition to invalidate the composer area cache")
	}
}
