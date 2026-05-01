package tui

import "github.com/lkarlslund/koder/internal/ui"

type transcriptViewport struct {
	Width         int
	Height        int
	WindowHeight  int
	YOffset       int
	contentHeight int
	visible       ui.Surface
}

func newTranscriptViewport(width, height int) transcriptViewport {
	v := transcriptViewport{Width: width, Height: height}
	v.SetContent("")
	return v
}

func (v *transcriptViewport) SetContent(content string) {
	v.visible = ui.SurfaceFromString(content)
	if content == "" {
		v.contentHeight = 0
		v.YOffset = 0
		return
	}
	v.contentHeight = v.visible.SurfaceHeight()
	v.SetYOffset(v.YOffset)
}

func (v transcriptViewport) VisibleSurface() ui.Surface {
	return v.visible
}

func (v *transcriptViewport) SetVisibleSurface(content ui.Surface) {
	v.visible = content
}

func (v *transcriptViewport) SetContentHeight(height int) {
	v.contentHeight = max(0, height)
	v.SetYOffset(v.YOffset)
}

func (v *transcriptViewport) SetWindowHeight(height int) {
	v.WindowHeight = max(0, height)
	v.SetYOffset(v.YOffset)
}

func (v *transcriptViewport) SetYOffset(n int) {
	v.YOffset = min(max(0, n), v.maxYOffset())
}

func (v *transcriptViewport) GotoBottom() {
	v.YOffset = v.maxYOffset()
}

func (v transcriptViewport) AtBottom() bool {
	return v.YOffset >= v.maxYOffset()
}

func (v transcriptViewport) TotalLineCount() int {
	return v.contentHeight
}

func (v transcriptViewport) VisibleLineCount() int {
	height := v.WindowHeight
	if height <= 0 {
		height = v.Height
	}
	if height <= 0 {
		return 0
	}
	if visibleHeight := v.visible.SurfaceHeight(); visibleHeight > 0 {
		return min(height, visibleHeight)
	}
	return min(height, v.contentHeight)
}

func (v transcriptViewport) maxYOffset() int {
	height := v.WindowHeight
	if height <= 0 {
		height = v.Height
	}
	return max(0, v.contentHeight-max(0, height))
}
