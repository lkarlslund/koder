package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/lkarlslund/koder/internal/theme"
)

type MenuItem struct {
	Title       string
	Description string
}

type HistoryMenu struct {
	Palette  theme.Palette
	Query    string
	Items    []MenuItem
	Selected int
	Width    int
}

type ApprovalPromptProps struct {
	Palette      theme.Palette
	Title        string
	Body         string
	ApproveLabel string
	DenyLabel    string
	ApproveFocus bool
	DenyFocus    bool
	Hints        string
}

type PickerDialogProps struct {
	Palette theme.Palette
	Title   string
	Hint    string
	Query   string
	Items   []MenuItem
	Index   int
}

type SlashMenu struct {
	Title    string
	Items    []MenuItem
	Selected int
}

func (m SlashMenu) View() string {
	if len(m.Items) == 0 {
		return ""
	}
	lines := []string{lipgloss.NewStyle().Bold(true).Render(m.Title)}
	for idx, item := range m.Items {
		line := fmt.Sprintf("%-12s %s", item.Title, item.Description)
		if idx == m.Selected {
			line = lipgloss.NewStyle().Reverse(true).Render(line)
		}
		lines = append(lines, line)
	}
	return lipgloss.NewStyle().Border(lipgloss.NormalBorder()).Padding(0, 1).Render(strings.Join(lines, "\n"))
}

func (m SlashMenu) Measure(_ *Context, constraints Constraints) Size {
	return constraints.Clamp(SurfaceFromString(m.View()).Size())
}

func (m SlashMenu) Render(_ *Context, bounds Rect) Surface {
	return SurfaceFromString(m.View()).normalize(bounds.W, bounds.H)
}

func (m HistoryMenu) View() string {
	width := m.Width
	if width <= 0 {
		width = 72
	}
	lines := []string{
		lipgloss.NewStyle().Bold(true).Render("History"),
		lipgloss.NewStyle().Foreground(m.Palette.AssistantTimestampText).Render("filter: " + m.Query),
	}
	if len(m.Items) == 0 {
		lines = append(lines, "", "  no matches")
	} else {
		lines = append(lines, "")
		for idx, item := range m.Items {
			lines = append(lines, SelectableRow{
				Primary:   item.Title,
				Secondary: item.Description,
				Width:     width - 4,
				Selected:  idx == m.Selected,
				Focused:   idx == m.Selected,
			}.View(m.Palette))
		}
	}
	lines = append(lines, "", lipgloss.NewStyle().Foreground(m.Palette.AssistantTimestampText).Render("enter accept  esc cancel  ctrl-r/down older  ctrl-s/up newer"))
	return lipgloss.NewStyle().Border(lipgloss.NormalBorder()).Padding(0, 1).Width(width).Render(strings.Join(lines, "\n"))
}

func (m HistoryMenu) Measure(_ *Context, constraints Constraints) Size {
	return constraints.Clamp(SurfaceFromString(m.View()).Size())
}

func (m HistoryMenu) Render(_ *Context, bounds Rect) Surface {
	return SurfaceFromString(m.View()).normalize(bounds.W, bounds.H)
}

type ApprovalPrompt struct {
	Palette      theme.Palette
	Title        string
	Body         string
	ApproveLabel string
	DenyLabel    string
	ApproveFocus bool
	DenyFocus    bool
	Hints        string
}

func NewApprovalPrompt(props ApprovalPromptProps) ApprovalPrompt {
	return ApprovalPrompt(props)
}

func (p ApprovalPrompt) View() string {
	approve := lipgloss.NewStyle().Padding(0, 1)
	if p.ApproveFocus {
		approve = approve.Reverse(true).Bold(true)
	}
	deny := lipgloss.NewStyle().Padding(0, 1)
	if p.DenyFocus {
		deny = deny.Reverse(true).Bold(true)
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1).
		Render(strings.Join([]string{
			lipgloss.NewStyle().Bold(true).Render(p.Title),
			p.Body,
			lipgloss.JoinHorizontal(lipgloss.Left, approve.Render(p.ApproveLabel), "  ", deny.Render(p.DenyLabel)),
			p.Hints,
		}, "\n"))
}

func (p ApprovalPrompt) Measure(_ *Context, constraints Constraints) Size {
	return constraints.Clamp(SurfaceFromString(p.View()).Size())
}

func (p ApprovalPrompt) Render(_ *Context, bounds Rect) Surface {
	return SurfaceFromString(p.View()).normalize(bounds.W, bounds.H)
}

type MenuPickerDialog struct {
	Palette theme.Palette
	Title   string
	Hint    string
	Query   string
	Items   []MenuItem
	Index   int
}

func NewMenuPickerDialog(props PickerDialogProps) MenuPickerDialog {
	return MenuPickerDialog(props)
}

func (d MenuPickerDialog) View() string {
	lines := []string{}
	if hint := strings.TrimSpace(d.Hint); hint != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(d.Palette.AssistantTimestampText).Render(hint))
	}
	lines = append(lines, "", fmt.Sprintf("filter: %s", d.Query), "")
	if len(d.Items) == 0 {
		lines = append(lines, "  no matches")
	} else {
		for idx, item := range d.Items {
			lines = append(lines, SelectableRow{
				Primary:   item.Title,
				Secondary: item.Description,
				Width:     72,
				Selected:  idx == d.Index,
				Focused:   idx == d.Index,
			}.View(d.Palette))
		}
	}
	lines = append(lines, "", RenderDialogButtons(d.Palette, "OK", "Cancel"))
	return Modal{
		Title:  d.Title,
		Body:   strings.Join(lines, "\n"),
		Footer: "Enter applies the highlighted row. Esc cancels.",
		Width:  80,
	}.View(d.Palette)
}

func (d MenuPickerDialog) Measure(_ *Context, constraints Constraints) Size {
	return constraints.Clamp(SurfaceFromString(d.View()).Size())
}

func (d MenuPickerDialog) Render(_ *Context, bounds Rect) Surface {
	return SurfaceFromString(d.View()).normalize(bounds.W, bounds.H)
}
