package ui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

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

func (r ToolRun) CardView(palette theme.Palette, width int, expanded bool) string {
	return r.renderCard(palette, width, expanded).String()
}

func (r ToolRun) CardSurface(palette theme.Palette, width int, expanded bool) Surface {
	return r.renderCard(palette, width, expanded)
}

func (r ToolRun) renderCard(palette theme.Palette, width int, expanded bool) Surface {
	titleStyle := lipgloss.NewStyle().Foreground(palette.MarkdownText).Bold(true).Italic(true)
	subtitleStyle := lipgloss.NewStyle().Foreground(palette.ComposerMutedText)
	bodyStyle := lipgloss.NewStyle().Foreground(palette.MarkdownText)
	addedStyle := lipgloss.NewStyle().Foreground(palette.DiffAddedText)
	deletedStyle := lipgloss.NewStyle().Foreground(palette.DiffDeletedText)
	metaStyle := lipgloss.NewStyle().Foreground(palette.ComposerMutedText)
	toggleStyle := lipgloss.NewStyle().Foreground(palette.UserAccentBar).Bold(true)
	headerWidth := innerCardWidth(width)
	headerParts := []string{titleStyle.Render(r.Title)}
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
		headerParts = append(headerParts, toggleStyle.Render(label))
	}
	lines := []string{strings.Join(headerParts, "  ")}
	if subtitle := strings.TrimSpace(r.Subtitle); subtitle != "" {
		lines = append(lines, subtitleStyle.Render(subtitle))
	}
	if preview := r.PreviewText(); preview != "" {
		lines = append(lines, renderToolRunPreview(preview, r, bodyStyle, addedStyle, deletedStyle, metaStyle, headerWidth, expanded))
	}
	return SurfaceFromString(strings.Join(lines, "\n"))
}

type ToolRunDock struct {
	Palette theme.Palette
	Run     ToolRun
	Buttons ButtonRow
	Hints   string
}

func (d ToolRunDock) View() string {
	return d.render().String()
}

func (d ToolRunDock) render() Surface {
	run := d.Run
	statusStyle := lipgloss.NewStyle().Foreground(toolRunStatusColor(run.Status, d.Palette)).Bold(true)
	title := lipgloss.JoinHorizontal(lipgloss.Left,
		lipgloss.NewStyle().Bold(true).Render(run.Title),
		"  ",
		statusStyle.Render(run.StatusLabel()),
	)
	lines := []string{title}
	if subtitle := strings.TrimSpace(run.Subtitle); subtitle != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(d.Palette.ComposerMutedText).Render(subtitle))
	}
	if preview := firstNonEmpty(strings.TrimSpace(run.Preview), strings.TrimSpace(run.Output), strings.TrimSpace(run.ErrorText)); preview != "" {
		lines = append(lines, preview)
	}
	buttons := d.Buttons
	buttons.Align = HorizontalAlignRight
	contentWidth := maxInt(ansi.StringWidth(title), ansi.StringWidth(d.Hints))
	contentWidth = maxInt(contentWidth, ansi.StringWidth(buttons.line(d.Palette)))
	if subtitle := strings.TrimSpace(run.Subtitle); subtitle != "" {
		contentWidth = maxInt(contentWidth, ansi.StringWidth(subtitle))
	}
	if preview := firstNonEmpty(strings.TrimSpace(run.Preview), strings.TrimSpace(run.Output), strings.TrimSpace(run.ErrorText)); preview != "" {
		for _, line := range strings.Split(preview, "\n") {
			contentWidth = maxInt(contentWidth, ansi.StringWidth(line))
		}
	}
	buttons.Width = contentWidth

	lines = append(lines,
		buttons.View(d.Palette),
		d.Hints,
	)
	return SurfaceFromString(lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(toolRunStatusColor(run.Status, d.Palette)).
		Padding(0, 1).
		Render(strings.Join(lines, "\n")))
}

func (d ToolRunDock) Measure(_ *Context, constraints Constraints) Size {
	return constraints.Clamp(d.render().Size())
}

func (d ToolRunDock) Render(ctx *Context, bounds Rect) Surface {
	if ctx != nil && ctx.Runtime != nil {
		run := d.Run
		title := run.Title + "  " + run.StatusLabel()
		contentWidth := maxInt(ansi.StringWidth(title), ansi.StringWidth(d.Hints))
		if subtitle := strings.TrimSpace(run.Subtitle); subtitle != "" {
			contentWidth = maxInt(contentWidth, ansi.StringWidth(subtitle))
		}
		if preview := firstNonEmpty(strings.TrimSpace(run.Preview), strings.TrimSpace(run.Output), strings.TrimSpace(run.ErrorText)); preview != "" {
			for _, line := range strings.Split(preview, "\n") {
				contentWidth = maxInt(contentWidth, ansi.StringWidth(line))
			}
		}
		buttons := d.Buttons
		buttons.Align = HorizontalAlignRight
		contentWidth = maxInt(contentWidth, ansi.StringWidth(buttons.line(d.Palette)))
		buttons.Width = contentWidth
		lineOffset := 2
		if strings.TrimSpace(run.Subtitle) != "" {
			lineOffset++
		}
		if preview := firstNonEmpty(strings.TrimSpace(run.Preview), strings.TrimSpace(run.Output), strings.TrimSpace(run.ErrorText)); preview != "" {
			lineOffset += len(strings.Split(preview, "\n"))
		}
		buttons.Render(ctx, Rect{X: bounds.X + 2, Y: bounds.Y + lineOffset, W: contentWidth, H: 1})
	}
	return d.render().normalize(bounds.W, bounds.H)
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

func renderToolRunPreview(preview string, run ToolRun, bodyStyle, addedStyle, deletedStyle, metaStyle lipgloss.Style, width int, expanded bool) string {
	preview = strings.TrimSpace(preview)
	if preview == "" {
		return ""
	}
	renderIndented := func(style lipgloss.Style, value string) string {
		var rendered string
		if expanded {
			rendered = wrapPlain(value, max(1, width-1))
		} else {
			rendered = firstWrappedLine(value, max(1, width-1))
		}
		lines := strings.Split(rendered, "\n")
		for idx, line := range lines {
			lines[idx] = " " + style.Render(line)
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
			style := bodyStyle
			switch {
			case strings.HasPrefix(line, "+"):
				style = addedStyle
			case strings.HasPrefix(line, "-"):
				style = deletedStyle
			case strings.HasPrefix(line, "@@"):
				style = metaStyle
			}
			wrapped := wrapPlain(line, max(1, width-1))
			for _, wrappedLine := range strings.Split(wrapped, "\n") {
				rendered = append(rendered, " "+style.Render(wrappedLine))
			}
		}
		return strings.Join(rendered, "\n")
	}
	if strings.TrimSpace(run.Diff) != "" && strings.TrimSpace(run.Output) == "" && strings.TrimSpace(run.ErrorText) == "" {
		if expanded {
			return renderIndented(addedStyle, preview)
		}
		return renderIndented(addedStyle, diffSummary(preview))
	}
	if run.Tool == domain.ToolKindEdit && strings.Contains(preview, "@@") {
		if expanded {
			return renderStyledLines(preview)
		}
		return renderIndented(bodyStyle, firstPreviewLine(preview))
	}
	if expanded {
		return renderIndented(bodyStyle, preview)
	}
	return renderIndented(bodyStyle, firstPreviewLine(preview))
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
	return ansi.Truncate(summary, 90, "…")
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
		lines = append(lines, strings.Split(ansi.Wordwrap(line, width, ""), "\n")...)
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
