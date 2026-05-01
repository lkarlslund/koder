package app

import (
	"hash/fnv"
	"strconv"

	apptui "github.com/lkarlslund/koder/internal/tui"
	"github.com/lkarlslund/koder/internal/ui"
)

const composerBlinkTimerOwner = apptui.ComposerBlinkTimerOwner

func (m *Model) ensureMainScreenWidget() *apptui.MainScreenWidget {
	if m.mainScreen != nil {
		m.mainScreen.SetDelegate(m)
		return m.mainScreen
	}
	m.mainScreen = apptui.NewMainScreenWidget(m)
	return m.mainScreen
}

func (m *Model) SyncRetainedTranscript() *ui.RetainedTranscript {
	return m.syncRetainedTranscript()
}

func (m *Model) VisibleTranscriptSurface() ui.Surface {
	return m.viewport.VisibleSurface()
}

func (m *Model) RenderTranscriptViewportSurface(retained *ui.RetainedTranscript, width, height, offset int) (ui.Surface, int, int) {
	return m.renderTranscriptViewportSurface(retained, width, height, offset)
}

func (m *Model) ViewportSize() (int, int) {
	return m.viewport.Width, m.viewport.Height
}

func (m *Model) ViewportYOffset() int {
	return m.viewport.YOffset
}

func (m *Model) ScreenBackground() ui.CellColor {
	return m.palette.ScreenBackground
}

func (m *Model) ComposerCursorDirty() bool {
	return m.composerCursorDirty
}

func (m *Model) ComposerRevision() uint64 {
	return m.composer.Revision()
}

func (m *Model) ComposerShouldBlink() bool {
	return m.composerShouldBlink()
}

func (m *Model) ComposerFocused() bool {
	return m.composer.Focused()
}

func (m *Model) ToggleComposerBlink() bool {
	return m.composer.ToggleBlink()
}

func (m *Model) SetComposerCursorDirty(dirty bool) {
	m.composerCursorDirty = dirty
}

func (m *Model) ComposerAreaElement() ui.Node {
	return m.renderComposerAreaElement()
}

func (m *Model) ComposerElement() ui.Node {
	return m.renderComposerElement()
}

func (m *Model) ComposerCursorRect(bounds ui.Rect) (ui.Rect, bool) {
	if bounds.H < 2 || !m.shouldShowComposerArea() {
		return ui.Rect{}, false
	}
	composer, ok := m.renderComposerElement().(ui.Composer)
	if !ok {
		return ui.Rect{}, false
	}
	rect, ok := composer.CursorRect()
	if !ok {
		return ui.Rect{}, false
	}
	if rect.X >= bounds.W || rect.Y >= bounds.H {
		return ui.Rect{}, false
	}
	if rect.X+rect.W > bounds.W {
		rect.W = bounds.W - rect.X
	}
	if rect.W <= 0 {
		return ui.Rect{}, false
	}
	return rect, true
}

func (m *Model) SetComposerAreaCache(size ui.Size, surface ui.Surface) {
	cache := m.ensureRenderCache()
	cache.composerAreaHeight = size.H
	cache.composerAreaValid = size.H > 0
	if surface.SurfaceHeight() > 0 || surface.SurfaceWidth() > 0 {
		cache.renderedComposerAreaSurface = surface
	}
}

func (m *Model) EnsureUIRoot() *ui.Root {
	return m.ensureUIRoot()
}

func (m *Model) ShowSidebar() bool {
	return m.showSidebar
}

func (m *Model) SidebarWidth() int {
	return m.sidebarWidth()
}

func (m *Model) StatusPaneHeight() int {
	return m.statusPaneHeight()
}

func (m *Model) SidebarElement() ui.Node {
	return ui.AsNode(ui.TextPane{Content: m.renderSidebar()})
}

func (m *Model) StatusPaneElement() ui.Node {
	return m.renderStatusPaneElement()
}

func (m *Model) SidebarHash(bounds ui.Rect) uint64 {
	return hashStrings(
		strconv.Itoa(bounds.W),
		strconv.Itoa(bounds.H),
		strconv.FormatBool(m.showSidebar),
		m.renderSidebar(),
	)
}

func (m *Model) StatusPaneHash(bounds ui.Rect) uint64 {
	line := ""
	if m.busy.transcriptActive() {
		line = ui.WorkingIndicatorLine(m.workingIndicator(), m.busy.statusOrDefault("Working ..."))
	}
	return hashStrings(strconv.Itoa(bounds.W), strconv.Itoa(bounds.H), line)
}

func hashStrings(values ...string) uint64 {
	hasher := fnv.New64a()
	for _, value := range values {
		_, _ = hasher.Write([]byte(value))
		_, _ = hasher.Write([]byte{0})
	}
	return hasher.Sum64()
}
