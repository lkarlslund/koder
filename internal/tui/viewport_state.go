package tui

import "strings"

type transcriptViewport struct {
	Width   int
	Height  int
	YOffset int
	content string
	lines   []string
}

func newTranscriptViewport(width, height int) transcriptViewport {
	v := transcriptViewport{Width: width, Height: height}
	v.SetContent("")
	return v
}

func (v *transcriptViewport) SetContent(content string) {
	v.content = content
	if content == "" {
		v.lines = nil
		v.YOffset = 0
		return
	}
	v.lines = strings.Split(content, "\n")
	v.SetYOffset(v.YOffset)
}

func (v transcriptViewport) View() string {
	if len(v.lines) == 0 || v.Height <= 0 {
		return ""
	}
	top := max(0, min(v.YOffset, v.maxYOffset()))
	bottom := min(len(v.lines), top+v.Height)
	return strings.Join(v.lines[top:bottom], "\n")
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
	return len(v.lines)
}

func (v transcriptViewport) VisibleLineCount() int {
	if v.Height <= 0 {
		return 0
	}
	return min(v.Height, max(0, len(v.lines)-v.YOffset))
}

func (v transcriptViewport) maxYOffset() int {
	return max(0, len(v.lines)-max(0, v.Height))
}
