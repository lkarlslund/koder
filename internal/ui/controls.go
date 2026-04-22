package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

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
	selectionBackground := palette.SelectionBackground
	selectionForeground := palette.SelectionForeground
	if strings.TrimSpace(string(selectionBackground)) == "" {
		selectionBackground = palette.UserTextBackground
	}
	if strings.TrimSpace(string(selectionForeground)) == "" {
		selectionForeground = palette.UserTextForeground
	}
	primaryStyle := lipgloss.NewStyle().Width(primaryWidth).Bold(true)
	gapStyle := lipgloss.NewStyle().Width(gapWidth)
	secondaryStyle := lipgloss.NewStyle().Width(secondaryWidth).Foreground(palette.AssistantTimestampText)
	tertiaryStyle := lipgloss.NewStyle().Width(tertiaryWidth).Align(lipgloss.Right).Foreground(palette.ActivityText)
	rowStyle := lipgloss.NewStyle().Width(width)
	if selected {
		rowStyle = rowStyle.Background(selectionBackground).Foreground(selectionForeground)
		primaryStyle = primaryStyle.Background(selectionBackground).Foreground(selectionForeground)
		gapStyle = gapStyle.Background(selectionBackground)
		secondaryStyle = secondaryStyle.Background(selectionBackground).Foreground(selectionForeground)
		tertiaryStyle = tertiaryStyle.Background(selectionBackground).Foreground(selectionForeground).Bold(true)
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
		Background(palette.SelectionBackground).
		Foreground(palette.SelectionForeground).
		Bold(true)
	if focused {
		activeStyle = activeStyle.
			Background(firstNonEmptyColor(palette.FocusBackground, palette.SelectionBackground)).
			Foreground(firstNonEmptyColor(palette.FocusForeground, palette.SelectionForeground))
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

type CheckboxRow struct {
	Label       string
	Description string
	Checked     bool
	OnLabel     string
	OffLabel    string
}

func (r CheckboxRow) View(width int, palette theme.Palette, focused bool) string {
	label := strings.TrimSpace(r.OffLabel)
	valueColor := palette.AssistantTimestampText
	glyph := "☐"
	if r.Checked {
		label = strings.TrimSpace(r.OnLabel)
		valueColor = palette.ActivityText
		glyph = "☑"
	}
	if label == "" {
		if r.Checked {
			label = "On"
		} else {
			label = "Off"
		}
	}
	row := RenderSelectableRow(r.Label, r.Description, glyph+" "+label, width, palette, focused)
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
	ID       string
	Label    string
	Hotkey   rune
	Focused  bool
	Primary  bool
	Selected bool
	OnPress  func()
}

func (b Button) View(palette theme.Palette) string {
	style := lipgloss.NewStyle().Padding(0, 2)
	if b.Primary {
		style = style.Background(palette.UserTextBackground).Foreground(palette.UserAccentBar).Bold(true)
	}
	if b.Selected {
		style = style.
			Background(firstNonEmptyColor(palette.SelectionBackground, palette.UserTextBackground)).
			Foreground(firstNonEmptyColor(palette.SelectionForeground, palette.UserTextForeground)).
			Bold(true)
	}
	if b.Focused {
		style = style.
			Background(firstNonEmptyColor(palette.FocusBackground, palette.SelectionBackground, palette.UserTextBackground)).
			Foreground(firstNonEmptyColor(palette.FocusForeground, palette.SelectionForeground, palette.UserTextForeground)).
			Bold(true)
	}
	label := b.Label
	if b.Hotkey != 0 {
		label = renderButtonLabel(b.Label, b.Hotkey, palette)
	}
	return style.Render(label)
}

func RenderDialogButtons(palette theme.Palette, okLabel, cancelLabel string) string {
	return ButtonRow{
		Buttons: []Button{
			{Label: okLabel, Primary: true},
			{Label: cancelLabel},
		},
	}.View(palette)
}

type HorizontalAlign int

const (
	HorizontalAlignLeft HorizontalAlign = iota
	HorizontalAlignCenter
	HorizontalAlignRight
)

type ButtonRow struct {
	Buttons []Button
	Index   int
	Gap     int
	Width   int
	Align   HorizontalAlign
}

func (r *ButtonRow) Move(delta int) {
	if len(r.Buttons) == 0 {
		r.Index = 0
		return
	}
	r.Index += delta
	if r.Index < 0 {
		r.Index = 0
	}
	if r.Index >= len(r.Buttons) {
		r.Index = len(r.Buttons) - 1
	}
}

func (r *ButtonRow) ActivateFocused() bool {
	if len(r.Buttons) == 0 || r.Index < 0 || r.Index >= len(r.Buttons) {
		return false
	}
	button := r.Buttons[r.Index]
	if button.OnPress == nil {
		return false
	}
	button.OnPress()
	return true
}

func (r *ButtonRow) ActivateHotkey(msg tea.KeyMsg) bool {
	if len(r.Buttons) == 0 {
		return false
	}
	if !msg.Alt {
		return false
	}
	key := strings.TrimSpace(strings.ToLower(msg.String()))
	key = strings.TrimPrefix(key, "alt+")
	if key == "" {
		return false
	}
	target := []rune(key)[0]
	for idx, button := range r.Buttons {
		if !strings.EqualFold(string(button.Hotkey), string(target)) {
			continue
		}
		r.Index = idx
		if button.OnPress != nil {
			button.OnPress()
			return true
		}
	}
	return false
}

func (r *ButtonRow) HotkeyIndex(msg tea.KeyMsg) (int, bool) {
	if len(r.Buttons) == 0 || !msg.Alt {
		return 0, false
	}
	key := strings.TrimSpace(strings.ToLower(msg.String()))
	key = strings.TrimPrefix(key, "alt+")
	if key == "" {
		return 0, false
	}
	target := []rune(key)[0]
	for idx, button := range r.Buttons {
		if !strings.EqualFold(string(button.Hotkey), string(target)) {
			continue
		}
		r.Index = idx
		return idx, true
	}
	return 0, false
}

func (r ButtonRow) View(palette theme.Palette) string {
	line := r.line(palette)
	if r.Width <= ansi.StringWidth(line) {
		return line
	}
	align := lipgloss.Left
	switch r.Align {
	case HorizontalAlignCenter:
		align = lipgloss.Center
	case HorizontalAlignRight:
		align = lipgloss.Right
	}
	return lipgloss.NewStyle().Width(r.Width).Align(align).Render(line)
}

func (r ButtonRow) line(palette theme.Palette) string {
	parts := make([]string, 0, len(r.Buttons))
	for idx, button := range r.Buttons {
		button.Focused = idx == r.Index
		parts = append(parts, button.View(palette))
	}
	return strings.Join(parts, strings.Repeat(" ", r.gap()))
}

func (r ButtonRow) ActivateAtX(x int, palette theme.Palette) bool {
	if len(r.Buttons) == 0 {
		return false
	}
	idx, ok := r.IndexAtX(x, palette)
	if !ok {
		return false
	}
	button := r.Buttons[idx]
	if button.OnPress != nil {
		button.OnPress()
		return true
	}
	return false
}

func (r ButtonRow) IndexAtX(x int, palette theme.Palette) (int, bool) {
	if len(r.Buttons) == 0 {
		return 0, false
	}
	offset := 0
	for idx, button := range r.Buttons {
		rendered := button.View(palette)
		width := ansi.StringWidth(rendered)
		if x >= offset && x < offset+width {
			return idx, true
		}
		offset += width + r.gap()
	}
	return 0, false
}

func buttonRowOffset(line string, row ButtonRow, palette theme.Palette) (int, bool) {
	return row.OffsetIn(line, palette)
}

func (r ButtonRow) OffsetIn(line string, palette theme.Palette) (int, bool) {
	start := strings.Index(line, ansi.Strip(r.line(palette)))
	if start < 0 {
		return 0, false
	}
	return start, true
}

func (r ButtonRow) gap() int {
	if r.Gap <= 0 {
		return 2
	}
	return r.Gap
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

func firstNonEmptyColor(values ...lipgloss.Color) lipgloss.Color {
	for _, value := range values {
		if strings.TrimSpace(string(value)) != "" {
			return value
		}
	}
	return lipgloss.Color("")
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
