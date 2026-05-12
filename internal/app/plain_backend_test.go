package app

import (
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/ui"
)

func (m App) View() string {
	return strings.Join(m.viewSurface().Lines(), "\n")
}

func (v transcriptViewport) View() string {
	return strings.Join(v.VisibleSurface().Lines(), "\n")
}

func (m *App) renderBody() string {
	return strings.Join(m.renderBodySurface().Lines(), "\n")
}

func (m *App) renderFooter() string {
	return strings.Join(m.renderComposerAreaSurface().Lines(), "\n")
}

func (m *App) renderComposer() string {
	element := m.renderComposerElement()
	ctx := &ui.Context{Palette: m.palette}
	size := element.Measure(ctx, ui.NewConstraints(m.composerWidth(), 0))
	return strings.Join(ui.PaintNodeSurface(ctx, element, ui.Rect{W: m.composerWidth(), H: size.H}).Lines(), "\n")
}

func (m *App) renderTranscriptActivity() string {
	element := m.renderTranscriptActivityElement()
	if element == nil {
		return ""
	}
	width := max(40, m.viewport.Width)
	ctx := &ui.Context{Palette: m.palette}
	size := element.Measure(ctx, ui.NewConstraints(width, 0))
	return strings.Join(ui.PaintNodeSurface(ctx, element, ui.Rect{W: width, H: size.H}).Lines(), "\n")
}

func (m *App) renderTranscriptMessage(msg domain.Message) string {
	element := newTranscriptRenderer(m).renderTranscriptMessageElement(msg, m.activeParts()[msg.ID])
	if element == nil {
		return ""
	}
	width := max(0, m.viewport.Width)
	size := element.Measure(&ui.Context{Palette: m.palette}, ui.NewConstraints(width, 0))
	return strings.Join(ui.PaintNodeSurface(&ui.Context{Palette: m.palette}, element, ui.Rect{W: max(width, size.W), H: size.H}).Lines(), "\n")
}
