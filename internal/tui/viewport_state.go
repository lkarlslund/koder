package tui

import "strings"

type transcriptViewport struct {
	Width   int
	Height  int
	YOffset int
	contentHeight int
	visible       string
	lines         []string
}

func newTranscriptViewport(width, height int) transcriptViewport {
	v := transcriptViewport{Width: width, Height: height}
	v.SetContent("")
	return v
}

func (v *transcriptViewport) SetContent(content string) {
	v.visible = content
	if content == "" {
		v.lines = nil
		v.contentHeight = 0
		v.YOffset = 0
		return
	}
	v.lines = strings.Split(content, "\n")
	v.contentHeight = len(v.lines)
	v.SetYOffset(v.YOffset)
}

func (v transcriptViewport) View() string {
	return v.visible
}

func (v *transcriptViewport) SetVisible(content string) {
	v.visible = content
	if content == "" {
		v.lines = nil
		return
	}
	v.lines = strings.Split(content, "\n")
}

func (v *transcriptViewport) SetContentHeight(height int) {
	v.contentHeight = max(0, height)
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
	if v.Height <= 0 || len(v.lines) == 0 {
		return 0
	}
	return min(v.Height, len(v.lines))
}

func (v transcriptViewport) maxYOffset() int {
	return max(0, v.contentHeight-max(0, v.Height))
}
