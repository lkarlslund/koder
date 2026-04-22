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

func RenderSlashMenu(title string, items []MenuItem, selected int) string {
	if len(items) == 0 {
		return ""
	}
	lines := []string{lipgloss.NewStyle().Bold(true).Render(title)}
	for idx, item := range items {
		line := fmt.Sprintf("%-12s %s", item.Title, item.Description)
		if idx == selected {
			line = lipgloss.NewStyle().Reverse(true).Render(line)
		}
		lines = append(lines, line)
	}
	return lipgloss.NewStyle().Border(lipgloss.NormalBorder()).Padding(0, 1).Render(strings.Join(lines, "\n"))
}

func RenderApprovalPrompt(props ApprovalPromptProps) string {
	approve := lipgloss.NewStyle().Padding(0, 1)
	if props.ApproveFocus {
		approve = approve.Reverse(true).Bold(true)
	}
	deny := lipgloss.NewStyle().Padding(0, 1)
	if props.DenyFocus {
		deny = deny.Reverse(true).Bold(true)
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1).
		Render(strings.Join([]string{
			lipgloss.NewStyle().Bold(true).Render(props.Title),
			props.Body,
			lipgloss.JoinHorizontal(lipgloss.Left, approve.Render(props.ApproveLabel), "  ", deny.Render(props.DenyLabel)),
			props.Hints,
		}, "\n"))
}

func RenderPickerDialog(props PickerDialogProps) string {
	lines := []string{}
	if hint := strings.TrimSpace(props.Hint); hint != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(props.Palette.AssistantTimestampText).Render(hint))
	}
	lines = append(lines, "", fmt.Sprintf("filter: %s", props.Query), "")
	if len(props.Items) == 0 {
		lines = append(lines, "  no matches")
	} else {
		for idx, item := range props.Items {
			lines = append(lines, RenderSelectableRow(item.Title, item.Description, "", 72, props.Palette, idx == props.Index, idx == props.Index))
		}
	}
	lines = append(lines, "", RenderDialogButtons(props.Palette, "OK", "Cancel"))
	return Modal{
		Title:  props.Title,
		Body:   strings.Join(lines, "\n"),
		Footer: "Enter applies the highlighted row. Esc cancels.",
		Width:  80,
	}.View(props.Palette)
}
