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

type ToolRunDockProps struct {
	Palette theme.Palette
	Run     ToolRun
	Buttons ButtonRow
	Hints   string
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

func RenderToolRunCard(run ToolRun, palette theme.Palette, width int, expanded bool) string {
	titleStyle := lipgloss.NewStyle().Foreground(palette.MarkdownText).Bold(true).Italic(true)
	subtitleStyle := lipgloss.NewStyle().Foreground(palette.ComposerMutedText)
	bodyStyle := lipgloss.NewStyle().Foreground(palette.MarkdownText)
	diffStyle := lipgloss.NewStyle().Foreground(palette.DiffAddedText)
	toggleStyle := lipgloss.NewStyle().Foreground(palette.UserAccentBar).Bold(true)
	headerWidth := innerCardWidth(width)
	headerParts := []string{titleStyle.Render(run.Title)}
	if subtitle := strings.TrimSpace(run.Subtitle); subtitle != "" {
		headerParts = append(headerParts, subtitleStyle.Render(subtitle))
	}
	if hiddenLines := ToolRunHiddenLineCount(run, headerWidth); hiddenLines > 0 {
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
	if preview := run.PreviewText(); preview != "" {
		lines = append(lines, renderToolRunPreview(preview, run, bodyStyle, diffStyle, headerWidth, expanded))
	}
	return strings.Join(lines, "\n")
}

func RenderToolRunDock(props ToolRunDockProps) string {
	run := props.Run
	statusStyle := lipgloss.NewStyle().Foreground(toolRunStatusColor(run.Status, props.Palette)).Bold(true)
	title := lipgloss.JoinHorizontal(lipgloss.Left,
		lipgloss.NewStyle().Bold(true).Render(run.Title),
		"  ",
		statusStyle.Render(run.StatusLabel()),
	)
	lines := []string{title}
	if subtitle := strings.TrimSpace(run.Subtitle); subtitle != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(props.Palette.ComposerMutedText).Render(subtitle))
	}
	if preview := firstNonEmpty(strings.TrimSpace(run.Preview), strings.TrimSpace(run.Output), strings.TrimSpace(run.ErrorText)); preview != "" {
		lines = append(lines, preview)
	}

	lines = append(lines,
		props.Buttons.View(props.Palette),
		props.Hints,
	)
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(toolRunStatusColor(run.Status, props.Palette)).
		Padding(0, 1).
		Render(strings.Join(lines, "\n"))
}

func ToolRunExpandable(run ToolRun, width int) bool {
	return ToolRunHiddenLineCount(run, width) > 0
}

func ToolRunHiddenLineCount(run ToolRun, width int) int {
	preview := strings.TrimSpace(run.PreviewText())
	if preview == "" {
		return 0
	}
	if strings.TrimSpace(run.Diff) != "" && strings.TrimSpace(run.Output) == "" && strings.TrimSpace(run.ErrorText) == "" {
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

func renderToolRunPreview(preview string, run ToolRun, bodyStyle, diffStyle lipgloss.Style, width int, expanded bool) string {
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
	if strings.TrimSpace(run.Diff) != "" && strings.TrimSpace(run.Output) == "" && strings.TrimSpace(run.ErrorText) == "" {
		if expanded {
			return renderIndented(diffStyle, preview)
		}
		return renderIndented(diffStyle, diffSummary(preview))
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
