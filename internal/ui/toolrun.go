package ui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/theme"
)

type ToolRunStatus string

const (
	ToolRunStatusRequested       ToolRunStatus = "requested"
	ToolRunStatusPendingApproval ToolRunStatus = "pending_approval"
	ToolRunStatusApproved        ToolRunStatus = "approved"
	ToolRunStatusCompleted       ToolRunStatus = "completed"
	ToolRunStatusDenied          ToolRunStatus = "denied"
	ToolRunStatusFailed          ToolRunStatus = "failed"
)

type ToolRun struct {
	ID         string
	Tool       domain.ToolKind
	ToolCallID string
	ApprovalID int64
	Title      string
	Subtitle   string
	Preview    string
	Status     ToolRunStatus
	Output     string
	Diff       string
	ErrorText  string
}

func (r ToolRun) PreviewText() string {
	return firstNonEmpty(strings.TrimSpace(r.ErrorText), strings.TrimSpace(r.Output), strings.TrimSpace(r.Diff), strings.TrimSpace(r.Preview))
}

func (r ToolRun) StatusLabel() string {
	switch r.Status {
	case ToolRunStatusPendingApproval:
		if r.ApprovalID > 0 {
			return "Needs approval #" + strconv.FormatInt(r.ApprovalID, 10)
		}
		return "Needs approval"
	case ToolRunStatusApproved:
		return "Approved"
	case ToolRunStatusCompleted:
		return "Completed"
	case ToolRunStatusDenied:
		return "Denied"
	case ToolRunStatusFailed:
		return "Failed"
	default:
		return "Requested"
	}
}

func (r ToolRun) CardSurface(palette theme.Palette, width int, expanded bool) Surface {
	return r.renderCard(palette, width, expanded)
}

func (r ToolRun) renderCard(palette theme.Palette, width int, expanded bool) Surface {
	headerWidth := innerCardWidth(width)
	headerParts := []string{r.Title}
	if hiddenLines := r.HiddenLineCount(headerWidth); hiddenLines > 0 {
		label := "Collapse"
		if !expanded && hiddenLines == 1 {
			label = "Expand (1 line more)"
		} else if !expanded {
			label = "Expand"
		}
		if hiddenLines > 1 && !expanded {
			label = "Expand (" + strconv.Itoa(hiddenLines) + " lines more)"
		}
		headerParts = append(headerParts, label)
	}
	lines := []string{strings.Join(headerParts, "  ")}
	if subtitle := strings.TrimSpace(r.Subtitle); subtitle != "" {
		lines = append(lines, subtitle)
	}
	if preview := r.PreviewText(); preview != "" {
		lines = append(lines, renderToolRunPreview(preview, r, lipgloss.NewStyle(), lipgloss.NewStyle(), lipgloss.NewStyle(), lipgloss.NewStyle(), headerWidth, expanded))
	}
	cardWidth := 0
	for _, line := range lines {
		for _, sub := range strings.Split(line, "\n") {
			cardWidth = maxInt(cardWidth, PlainWidth(sub))
		}
	}
	s := BlankSurface(cardWidth, strings.Count(strings.Join(lines, "\n"), "\n")+1)
	row := 0
	titleStyle := CellStyle{FG: cellColor(palette.MarkdownText), Bold: true, Italic: true}
	toggleStyle := CellStyle{FG: cellColor(palette.UserAccentBar), Bold: true}
	subtitleStyle := CellStyle{FG: cellColor(palette.ComposerMutedText)}
	bodyStyle := CellStyle{FG: cellColor(palette.MarkdownText)}
	addedStyle := CellStyle{FG: cellColor(palette.DiffAddedText)}
	deletedStyle := CellStyle{FG: cellColor(palette.DiffDeletedText)}
	metaStyle := CellStyle{FG: cellColor(palette.ComposerMutedText)}
	for _, header := range lines[:1] {
		headerCols := strings.Split(header, "  ")
		col := 0
		if len(headerCols) > 0 {
			s.WriteText(col, row, headerCols[0], titleStyle)
			col += PlainWidth(headerCols[0])
		}
		for _, extra := range headerCols[1:] {
			s.WriteText(col, row, "  ", bodyStyle)
			col += 2
			s.WriteText(col, row, extra, toggleStyle)
			col += PlainWidth(extra)
		}
		row++
	}
	if len(lines) > 1 {
		for _, line := range lines[1:] {
			for _, sub := range strings.Split(line, "\n") {
				style := bodyStyle
				trimmed := strings.TrimLeft(sub, " ")
				if strings.HasPrefix(trimmed, "+") {
					style = addedStyle
				} else if strings.HasPrefix(trimmed, "-") {
					style = deletedStyle
				} else if strings.HasPrefix(trimmed, "@@") {
					style = metaStyle
				} else if row == 1 && strings.TrimSpace(r.Subtitle) != "" {
					style = subtitleStyle
				}
				s.WriteText(0, row, sub, style)
				row++
			}
		}
	}
	return s
}

type ToolRunDock struct {
	Palette theme.Palette
	Run     ToolRun
	Buttons ButtonRow
	Hints   string
}

func (d ToolRunDock) render() Surface {
	return d.element().Render(&Context{Palette: d.Palette}, Rect{W: d.width()})
}

func (d ToolRunDock) Measure(_ *Context, constraints Constraints) Size {
	return constraints.Clamp(d.render().Size())
}

func (d ToolRunDock) Render(ctx *Context, bounds Rect) Surface {
	return d.element().Render(ctx, bounds)
}

func (d ToolRunDock) element() Element {
	children := []Child{
		Fixed(toolRunDockTitle{
			Palette: d.Palette,
			Title:   d.Run.Title,
			Status:  d.Run.StatusLabel(),
			Color:   toolRunStatusColor(d.Run.Status, d.Palette),
			Width:   d.contentWidth(),
		}),
	}
	if subtitle := strings.TrimSpace(d.Run.Subtitle); subtitle != "" {
		children = append(children, Fixed(Label{
			Text: subtitle,
			Style: lipgloss.NewStyle().
				Foreground(d.Palette.ComposerMutedText),
		}))
	}
	if preview := firstNonEmpty(strings.TrimSpace(d.Run.Preview), strings.TrimSpace(d.Run.Output), strings.TrimSpace(d.Run.ErrorText)); preview != "" {
		children = append(children, Fixed(toolRunDockPreview{
			Palette: d.Palette,
			Text:    preview,
			Width:   d.contentWidth(),
		}))
	}
	buttons := d.Buttons
	buttons.Align = HorizontalAlignRight
	buttons.Width = d.contentWidth()
	children = append(children,
		Fixed(buttons),
		Fixed(Label{
			Text:  d.Hints,
			Style: lipgloss.NewStyle().Foreground(d.Palette.AssistantTimestampText),
		}),
	)
	return Panel{
		Child:        Column{Children: children, Spacing: 1},
		Width:        d.width(),
		Padding:      SymmetricInsets(1, 0),
		BorderLeft:   true,
		BorderRight:  true,
		BorderTop:    true,
		BorderBottom: true,
		BorderColor:  toolRunStatusColor(d.Run.Status, d.Palette),
	}
}

func (d ToolRunDock) contentWidth() int {
	run := d.Run
	titleWidth := PlainWidth(run.Title) + 2 + PlainWidth(run.StatusLabel())
	contentWidth := maxInt(titleWidth, PlainWidth(d.Hints))
	buttons := d.Buttons
	buttons.Align = HorizontalAlignRight
	contentWidth = maxInt(contentWidth, PlainWidth(buttons.line(d.Palette)))
	if subtitle := strings.TrimSpace(run.Subtitle); subtitle != "" {
		contentWidth = maxInt(contentWidth, PlainWidth(subtitle))
	}
	if preview := firstNonEmpty(strings.TrimSpace(run.Preview), strings.TrimSpace(run.Output), strings.TrimSpace(run.ErrorText)); preview != "" {
		for _, line := range strings.Split(preview, "\n") {
			contentWidth = maxInt(contentWidth, PlainWidth(line))
		}
	}
	return contentWidth
}

func (d ToolRunDock) width() int {
	return d.contentWidth() + 4
}

func (r ToolRun) Expandable(width int) bool {
	return r.HiddenLineCount(width) > 0
}

func (r ToolRun) HiddenLineCount(width int) int {
	preview := strings.TrimSpace(r.PreviewText())
	if preview == "" {
		return 0
	}
	if strings.TrimSpace(r.Diff) != "" && strings.TrimSpace(r.Output) == "" && strings.TrimSpace(r.ErrorText) == "" {
		expandedLines := renderedLineCount(wrapPlain(preview, width))
		collapsedLines := renderedLineCount(wrapPlain(diffSummary(preview), width))
		if expandedLines <= collapsedLines {
			return 0
		}
		return expandedLines - collapsedLines
	}
	expandedLines := renderedLineCount(wrapPlain(preview, width))
	collapsedLines := renderedLineCount(wrapPlain(singleLineSummary(preview), width))
	if expandedLines <= collapsedLines {
		return 0
	}
	return expandedLines - collapsedLines
}

func renderToolRunPreview(preview string, run ToolRun, _ lipgloss.Style, _ lipgloss.Style, _ lipgloss.Style, _ lipgloss.Style, width int, expanded bool) string {
	preview = strings.TrimSpace(preview)
	if preview == "" {
		return ""
	}
	renderIndented := func(value string) string {
		var rendered string
		if expanded {
			rendered = wrapPlain(value, max(1, width-1))
		} else {
			rendered = firstWrappedLine(value, max(1, width-1))
		}
		lines := strings.Split(rendered, "\n")
		for idx, line := range lines {
			lines[idx] = " " + line
		}
		return strings.Join(lines, "\n")
	}
	renderStyledLines := func(value string) string {
		lines := strings.Split(value, "\n")
		if !expanded {
			lines = lines[:1]
		}
		rendered := make([]string, 0, len(lines))
		for _, line := range lines {
			wrapped := wrapPlain(line, max(1, width-1))
			for _, wrappedLine := range strings.Split(wrapped, "\n") {
				rendered = append(rendered, " "+wrappedLine)
			}
		}
		return strings.Join(rendered, "\n")
	}
	if strings.TrimSpace(run.Diff) != "" && strings.TrimSpace(run.Output) == "" && strings.TrimSpace(run.ErrorText) == "" {
		if expanded {
			return renderIndented(preview)
		}
		return renderIndented(diffSummary(preview))
	}
	if run.Tool == domain.ToolKindEdit && strings.Contains(preview, "@@") {
		if expanded {
			return renderStyledLines(preview)
		}
		return renderIndented(firstPreviewLine(preview))
	}
	if expanded {
		return renderIndented(preview)
	}
	return renderIndented(firstPreviewLine(preview))
}

func toolRunStatusColor(status ToolRunStatus, palette theme.Palette) lipgloss.Color {
	switch status {
	case ToolRunStatusPendingApproval, ToolRunStatusApproved:
		return palette.ActivityText
	case ToolRunStatusDenied, ToolRunStatusFailed:
		return palette.DiffDeletedText
	default:
		return palette.UserAccentBar
	}
}

type toolRunDockTitle struct {
	Palette theme.Palette
	Title   string
	Status  string
	Color   lipgloss.Color
	Width   int
}

func (t toolRunDockTitle) Measure(_ *Context, constraints Constraints) Size {
	return constraints.Clamp(Size{W: t.Width, H: 1})
}

func (t toolRunDockTitle) Render(_ *Context, bounds Rect) Surface {
	width := t.Width
	if width <= 0 {
		width = bounds.W
	}
	s := BlankSurface(width, 1)
	s.WriteText(0, 0, t.Title, CellStyle{FG: cellColor(t.Palette.MarkdownText), Bold: true})
	s.WriteText(PlainWidth(t.Title)+2, 0, t.Status, CellStyle{FG: cellColor(t.Color), Bold: true})
	return s.normalize(bounds.W, bounds.H)
}

type toolRunDockPreview struct {
	Palette theme.Palette
	Text    string
	Width   int
}

func (p toolRunDockPreview) Measure(_ *Context, constraints Constraints) Size {
	lines := strings.Split(strings.TrimRight(p.Text, "\n"), "\n")
	if len(lines) == 0 {
		lines = []string{""}
	}
	width := 0
	for _, line := range lines {
		width = max(width, PlainWidth(line))
	}
	if p.Width > 0 {
		width = min(width, p.Width)
	}
	return constraints.Clamp(Size{W: width, H: len(lines)})
}

func (p toolRunDockPreview) Render(_ *Context, bounds Rect) Surface {
	lines := strings.Split(strings.TrimRight(p.Text, "\n"), "\n")
	if len(lines) == 0 {
		lines = []string{""}
	}
	width := p.Width
	if width <= 0 {
		width = bounds.W
	}
	s := BlankSurface(width, len(lines))
	style := CellStyle{FG: cellColor(p.Palette.MarkdownText)}
	for y, line := range lines {
		s.WriteText(0, y, PlainTruncate(line, width, ""), style)
	}
	return s.normalize(bounds.W, bounds.H)
}

func diffSummary(diff string) string {
	lines := strings.Split(strings.TrimSpace(diff), "\n")
	if len(lines) == 0 {
		return "Diff generated"
	}
	return firstNonEmpty(strings.TrimSpace(lines[0]), "Diff generated")
}

func singleLineSummary(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}
	lines := strings.Fields(strings.ReplaceAll(input, "\n", " "))
	if len(lines) == 0 {
		return ""
	}
	summary := strings.Join(lines, " ")
	if lipgloss.Width(summary) <= 90 {
		return summary
	}
	return PlainTruncate(summary, 90, "…")
}

func firstPreviewLine(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}
	for _, line := range strings.Split(input, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func firstWrappedLine(input string, width int) string {
	wrapped := wrapPlain(input, width)
	if wrapped == "" {
		return ""
	}
	lines := strings.Split(wrapped, "\n")
	if len(lines) == 0 {
		return ""
	}
	return lines[0]
}

func wrapPlain(input string, width int) string {
	if width <= 0 {
		return input
	}
	var lines []string
	for _, line := range strings.Split(input, "\n") {
		if strings.TrimSpace(line) == "" {
			lines = append(lines, "")
			continue
		}
		lines = append(lines, strings.Split(PlainWordWrap(line, width), "\n")...)
	}
	return strings.Join(lines, "\n")
}

func innerCardWidth(width int) int {
	if width <= 0 {
		return 0
	}
	if width-6 < 1 {
		return 1
	}
	return width - 6
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func renderedLineCount(input string) int {
	input = strings.TrimRight(input, "\n")
	if input == "" {
		return 0
	}
	return len(strings.Split(input, "\n"))
}
