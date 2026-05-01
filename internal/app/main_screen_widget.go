package app

import (
	"hash/fnv"
	"strconv"

	apptui "github.com/lkarlslund/koder/internal/tui"
	"github.com/lkarlslund/koder/internal/ui"
)

const composerBlinkTimerOwner = apptui.ComposerBlinkTimerOwner

func (m *Model) ensureMainScreenWidget() *apptui.MainScreenWidget {
	if m.mainScreen == nil {
		m.mainScreen = apptui.NewMainScreenWidget()
	}
	m.syncMainScreenWidgetState()
	return m.mainScreen
}

func (m *Model) syncMainScreenWidgetState() {
	if m.mainScreen == nil {
		return
	}
	sidebarContent := m.renderSidebar()
	statusLine := ""
	if m.busy.transcriptActive() {
		statusLine = ui.WorkingIndicatorLine(m.workingIndicator(), m.busy.statusOrDefault("Working ..."))
	}
	m.mainScreen.SetState(apptui.MainScreenState{
		Transcript: apptui.TranscriptState{
			Retained:   m.syncRetainedTranscript(),
			Width:      m.viewport.Width,
			Height:     m.viewport.Height,
			YOffset:    m.viewport.YOffset,
			Background: m.palette.ScreenBackground,
		},
		Composer: apptui.ComposerState{
			AreaElement: m.renderComposerAreaElement(),
			Element:     m.renderComposerElement(),
			Revision:    m.composer.Revision(),
			CursorDirty: m.composerCursorDirty,
			Focused:     m.composer.Focused(),
			ShouldBlink: m.composerShouldBlink(),
		},
		Sidebar: apptui.SidebarState{
			Element: ui.AsNode(ui.TextPane{Content: sidebarContent}),
			Show:    m.showSidebar,
			Width:   m.sidebarWidth(),
			Height:  m.viewport.Height,
			Hash: hashStrings(
				strconv.Itoa(m.sidebarWidth()),
				strconv.Itoa(m.viewport.Height),
				strconv.FormatBool(m.showSidebar),
				sidebarContent,
			),
		},
		StatusPane: apptui.StatusPaneState{
			Element: m.renderStatusPaneElement(),
			Height:  m.statusPaneHeight(),
			Hash: hashStrings(
				strconv.Itoa(m.width),
				strconv.Itoa(m.statusPaneHeight()),
				statusLine,
			),
		},
	})
	m.composerCursorDirty = false
}

func hashStrings(values ...string) uint64 {
	hasher := fnv.New64a()
	for _, value := range values {
		_, _ = hasher.Write([]byte(value))
		_, _ = hasher.Write([]byte{0})
	}
	return hasher.Sum64()
}
