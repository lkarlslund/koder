package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/lkarlslund/koder/internal/theme"
)

func RenderSelectableRow(primary, secondary, tertiary string, width int, palette theme.Palette, selected bool) string {
	if width <= 0 {
		width = 72
	}
	primary = compactInlineText(primary)
	secondary = compactInlineText(secondary)
	tertiary = compactInlineText(tertiary)
	primaryWidth := minInt(28, maxInt(12, width/3))
	tertiaryWidth := 0
	if strings.TrimSpace(tertiary) != "" {
		tertiaryWidth = minInt(18, maxInt(8, width/5))
	}
	gapWidth := 2
	secondaryWidth := maxInt(8, width-primaryWidth-tertiaryWidth-gapWidth*2)
	if tertiaryWidth == 0 {
		secondaryWidth = maxInt(8, width-primaryWidth-gapWidth)
	}
	selectionBackground := palette.UserTextBackground
	primaryStyle := lipgloss.NewStyle().Width(primaryWidth).Bold(true)
	gapStyle := lipgloss.NewStyle().Width(gapWidth)
	secondaryStyle := lipgloss.NewStyle().Width(secondaryWidth).Foreground(palette.AssistantTimestampText)
	tertiaryStyle := lipgloss.NewStyle().Width(tertiaryWidth).Align(lipgloss.Right).Foreground(palette.ActivityText)
	rowStyle := lipgloss.NewStyle().Width(width)
	if selected {
		rowStyle = rowStyle.Background(selectionBackground).Foreground(palette.UserTextForeground)
		primaryStyle = primaryStyle.Background(selectionBackground).Foreground(palette.UserTextForeground)
		gapStyle = gapStyle.Background(selectionBackground)
		secondaryStyle = secondaryStyle.Background(selectionBackground).Foreground(palette.UserTextForeground)
		tertiaryStyle = tertiaryStyle.Background(selectionBackground).Foreground(palette.UserAccentBar).Bold(true)
	}
	row := lipgloss.JoinHorizontal(
		lipgloss.Top,
		primaryStyle.Render(truncateText(strings.TrimSpace(primary), primaryWidth)),
		gapStyle.Render(""),
		secondaryStyle.Render(truncateText(strings.TrimSpace(secondary), secondaryWidth)),
	)
	if tertiaryWidth > 0 {
		row = lipgloss.JoinHorizontal(
			lipgloss.Top,
			row,
			gapStyle.Render(""),
			tertiaryStyle.Render(truncateText(strings.TrimSpace(tertiary), tertiaryWidth)),
		)
	}
	return rowStyle.Render(row)
}

type VerticalTabs struct {
	Tabs   []string
	Active int
}

func (v *VerticalTabs) Move(delta int) {
	if len(v.Tabs) == 0 {
		v.Active = 0
		return
	}
	v.Active += delta
	if v.Active < 0 {
		v.Active = 0
	}
	if v.Active >= len(v.Tabs) {
		v.Active = len(v.Tabs) - 1
	}
}

func (v VerticalTabs) Current() int {
	if len(v.Tabs) == 0 {
		return 0
	}
	if v.Active < 0 {
		return 0
	}
	if v.Active >= len(v.Tabs) {
		return len(v.Tabs) - 1
	}
	return v.Active
}

func (v VerticalTabs) View(width int, palette theme.Palette, focused bool) string {
	lines := make([]string, 0, len(v.Tabs))
	base := lipgloss.NewStyle().Width(width)
	activeStyle := base.
		Background(palette.UserTextBackground).
		Foreground(palette.UserAccentBar).
		Bold(true)
	if focused {
		activeStyle = activeStyle.Reverse(true)
	}
	for idx, tab := range v.Tabs {
		label := fmt.Sprintf(" %s ", strings.TrimSpace(tab))
		if idx == v.Current() {
			lines = append(lines, activeStyle.Render(label))
			continue
		}
		lines = append(lines, base.Foreground(palette.SidebarForeground).Render(label))
	}
	return strings.Join(lines, "\n")
}

type ToggleRow struct {
	Label       string
	Description string
	Value       bool
}

func (r ToggleRow) View(width int, palette theme.Palette, focused bool) string {
	value := "Off"
	valueColor := palette.AssistantTimestampText
	if r.Value {
		value = "On"
		valueColor = palette.ActivityText
	}
	row := RenderSelectableRow(r.Label, r.Description, value, width, palette, focused)
	if focused {
		return lipgloss.NewStyle().Foreground(valueColor).Background(palette.UserTextBackground).Render(row)
	}
	return row
}

type ChoiceRow struct {
	Label       string
	Description string
	Value       string
}

func (r ChoiceRow) View(width int, palette theme.Palette, focused bool) string {
	return RenderSelectableRow(r.Label, r.Description, r.Value, width, palette, focused)
}

type Button struct {
	Label    string
	Hotkey   rune
	Focused  bool
	Primary  bool
	Selected bool
}

func (b Button) View(palette theme.Palette) string {
	style := lipgloss.NewStyle().Padding(0, 2)
	if b.Primary {
		style = style.Background(palette.UserTextBackground).Foreground(palette.UserAccentBar).Bold(true)
	}
	if b.Focused || b.Selected {
		style = style.Reverse(true)
	}
	label := b.Label
	if b.Hotkey != 0 {
		label = renderButtonLabel(b.Label, b.Hotkey, palette)
	}
	return style.Render(label)
}

func RenderDialogButtons(palette theme.Palette, okLabel, cancelLabel string) string {
	return lipgloss.JoinHorizontal(
		lipgloss.Left,
		Button{Label: okLabel, Primary: true}.View(palette),
		"  ",
		Button{Label: cancelLabel}.View(palette),
	)
}

func renderButtonLabel(label string, hotkey rune, palette theme.Palette) string {
	labelRunes := []rune(label)
	target := []rune(strings.ToLower(string(hotkey)))
	if len(target) == 0 {
		return label
	}
	idx := -1
	for i, r := range labelRunes {
		if strings.ToLower(string(r)) == string(target) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return label
	}
	hot := lipgloss.NewStyle().Foreground(palette.ActivityText).Bold(true).Render(string(labelRunes[idx]))
	return string(labelRunes[:idx]) + hot + string(labelRunes[idx+1:])
}

func truncateText(input string, width int) string {
	if width <= 0 {
		return ""
	}
	input = compactInlineText(input)
	if lipgloss.Width(input) <= width {
		return input
	}
	if width == 1 {
		return "…"
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(input)
}

func compactInlineText(input string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(input)), " ")
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
