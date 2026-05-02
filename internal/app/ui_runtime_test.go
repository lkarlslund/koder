package app

import (
	"strconv"
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
	if !strings.Contains(frame, "Session #0") {
		t.Fatalf("expected main window content to remain in composed frame, got %q", frame)
	}
}

func TestHelpModalContentUsesWindowBackground(t *testing.T) {
	m := newRuntimeTestModel(t)
	m.openHelpModal()

	surface := m.viewSurface()
	lines := surface.Lines()
	var foundX, foundY int
	found := false
	for y, line := range lines {
		if x := strings.Index(line, "Hotkeys"); x >= 0 {
			foundX, foundY, found = x, y, true
			break
		}
	}
	if !found {
		t.Fatalf("expected help modal text in surface, got %q", strings.Join(lines, "\n"))
	}
	r, g, b, ok := surface.SurfaceCellBG(foundX, foundY)
	if !ok {
		t.Fatal("expected help modal text to inherit a background")
	}
	want := m.palette.SidebarBackground
	if !want.Valid() || r != want.R() || g != want.G() || b != want.B() {
		t.Fatalf("expected inherited help background %v, got %d %d %d", want, r, g, b)
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

func TestSessionDialogScrollMarksWindowDirty(t *testing.T) {
	m := newRuntimeTestModel(t)
	m.width = 100
	m.height = 32
	for i := int64(1); i <= 14; i++ {
		m.sessions = append(m.sessions, domain.Session{
			ID:    i,
			Title: "Session " + strconv.FormatInt(i, 10),
		})
	}
	m.openSessionPicker()

	root := m.syncUIRoot()
	_ = root.RenderFrame()
	handled, _ := root.HandleEvent(ui.KeyEvent{Type: ui.KeyDown})
	if !handled {
		t.Fatal("expected session dialog to handle down key")
	}
	frame := root.RenderFrame()
	rects, ok := frame.DirtyRects()
	if !ok || len(rects) == 0 {
		t.Fatalf("expected session dialog scroll to report dirty rects, got ok=%v rects=%v", ok, rects)
	}
	if !strings.Contains(strings.Join(frame.Lines(), "\n"), "Session 2") {
		t.Fatalf("expected scrolled dialog to repaint selected session, got %q", strings.Join(frame.Lines(), "\n"))
	}
}

func TestSyncUIRootNoopKeepsRootClean(t *testing.T) {
	m := newRuntimeTestModel(t)
	_ = m.viewSurface()

	root := m.syncUIRoot()
	if root.NeedsRedraw() {
		t.Fatal("expected synced root to stay clean when nothing changed")
	}

	_ = m.syncUIRoot()
	if root.NeedsRedraw() {
		t.Fatal("expected repeated sync to avoid marking the root dirty")
	}
}

func TestViewSurfaceSidebarToggleDirtyRectsCoverDiff(t *testing.T) {
	m := newRuntimeTestModel(t)
	m.status = "Ready"
	m.resize()
	m.refreshViewport()

	before := m.viewSurface()
	m.showSidebar = false
	m.resize()
	m.refreshViewportPreserve()
	after := m.viewSurface()

	assertDirtyRectsCoverSurfaceDiff(t, before, after)
}

func TestViewSurfaceResizeDirtyRectsCoverDiff(t *testing.T) {
	m := newRuntimeTestModel(t)
	m.status = "Ready"
	m.resize()
	m.refreshViewport()

	before := m.viewSurface()
	m.width = 96
	m.height = 28
	m.resize()
	m.refreshViewportPreserve()
	after := m.viewSurface()

	assertDirtyRectsCoverSurfaceDiff(t, before, after)
}

func TestViewSurfaceModalOpenCloseDirtyRectsCoverDiff(t *testing.T) {
	m := newRuntimeTestModel(t)
	m.resize()
	m.refreshViewport()

	before := m.viewSurface()
	m.openHelpModal()
	withModal := m.viewSurface()
	assertDirtyRectsCoverSurfaceDiff(t, before, withModal)

	m.closeHelpModal()
	afterClose := m.viewSurface()
	assertDirtyRectsCoverSurfaceDiff(t, withModal, afterClose)
}

func TestViewSurfaceThemeChangeDirtyRectsCoverDiff(t *testing.T) {
	m := newRuntimeTestModel(t)
	m.status = "Ready"
	m.resize()
	m.refreshViewport()

	before := m.viewSurface()
	if err := m.setTheme("gruvbox", false); err != nil {
		t.Fatalf("set theme: %v", err)
	}
	after := m.viewSurface()

	assertDirtyRectsCoverSurfaceDiff(t, before, after)
}

func TestBouncyBallsOverlayTintsFrameAndTracksDamage(t *testing.T) {
	m := newRuntimeTestModel(t)
	m.resize()
	m.refreshViewport()

	before := m.viewSurface()
	m.bouncyBalls.Enable(m.width, m.height)
	withOverlay := m.viewSurface()
	assertDirtyRectsCoverSurfaceDiff(t, before, withOverlay)

	centerX := int(m.bouncyBalls.balls[0].x)
	centerY := int(m.bouncyBalls.balls[0].y)
	fgR, fgG, fgB, fgOK := withOverlay.SurfaceCellFG(centerX, centerY)
	bgR, bgG, bgB, bgOK := withOverlay.SurfaceCellBG(centerX, centerY)
	if !fgOK || !bgOK {
		t.Fatal("expected overlay to tint both foreground and background at ball center")
	}
	if beforeR, beforeG, beforeB, beforeOK := before.SurfaceCellFG(centerX, centerY); beforeOK && beforeR == fgR && beforeG == fgG && beforeB == fgB {
		t.Fatalf("expected overlay to change foreground color, before=(%d,%d,%d,%v) after=(%d,%d,%d,%v)", beforeR, beforeG, beforeB, beforeOK, fgR, fgG, fgB, fgOK)
	}
	if beforeR, beforeG, beforeB, beforeOK := before.SurfaceCellBG(centerX, centerY); beforeOK && beforeR == bgR && beforeG == bgG && beforeB == bgB {
		t.Fatalf("expected overlay to change background color, before=(%d,%d,%d,%v) after=(%d,%d,%d,%v)", beforeR, beforeG, beforeB, beforeOK, bgR, bgG, bgB, bgOK)
	}

	movedBefore := withOverlay
	m.bouncyBalls.Step(m.width, m.height)
	moved := m.viewSurface()
	assertDirtyRectsCoverSurfaceDiff(t, movedBefore, moved)

	m.bouncyBalls.Disable()
	afterDisable := m.viewSurface()
	assertDirtyRectsCoverSurfaceDiff(t, moved, afterDisable)
}

func TestViewSurfaceTranscriptAppendWithSidebarDirtyRectsCoverDiff(t *testing.T) {
	m := newRuntimeTestModel(t)
	m.currentSession = domain.Session{ID: 1, Title: "Session 1"}
	m.appendLocalUserPrompt("first message", nil, nil)
	m.resize()
	m.refreshViewport()

	before := m.viewSurface()
	m.appendLocalUserPrompt("second message that should append below the existing content", nil, nil)
	after := m.viewSurface()

	assertDirtyRectsCoverSurfaceDiff(t, before, after)
}

func assertDirtyRectsCoverSurfaceDiff(t *testing.T, before, after ui.Surface) {
	t.Helper()
	rects, ok := after.DirtyRects()
	if !ok || len(rects) == 0 {
		t.Fatal("expected dirty rects on updated frame")
	}
	width := max(before.SurfaceWidth(), after.SurfaceWidth())
	height := max(before.SurfaceHeight(), after.SurfaceHeight())
	before = before.Normalize(width, height)
	after = after.Normalize(width, height)
	diff := ui.DiffSurfaceDamage(before, after)
	if len(diff) == 0 {
		t.Fatal("expected frame diff after runtime update")
	}
	for _, rect := range rects {
		if rect.X < 0 || rect.Y < 0 || rect.X+rect.W > width || rect.Y+rect.H > height {
			t.Fatalf("dirty rect %v exceeds normalized frame %dx%d", rect, width, height)
		}
	}
	if !rectsCoverDamage(rects, diff, width, height) {
		t.Fatalf("dirty rects %v did not cover frame diff %v", rects, diff)
	}
}

func rectsCoverDamage(rects, diff []ui.Rect, width, height int) bool {
	if len(diff) == 0 {
		return true
	}
	covered := make([]bool, width*height)
	mark := func(rect ui.Rect) {
		startX := max(0, rect.X)
		startY := max(0, rect.Y)
		endX := min(width, rect.X+rect.W)
		endY := min(height, rect.Y+rect.H)
		for y := startY; y < endY; y++ {
			row := y * width
			for x := startX; x < endX; x++ {
				covered[row+x] = true
			}
		}
	}
	for _, rect := range rects {
		mark(rect)
	}
	for _, rect := range diff {
		startX := max(0, rect.X)
		startY := max(0, rect.Y)
		endX := min(width, rect.X+rect.W)
		endY := min(height, rect.Y+rect.H)
		for y := startY; y < endY; y++ {
			row := y * width
			for x := startX; x < endX; x++ {
				if !covered[row+x] {
					return false
				}
			}
		}
	}
	return true
}
