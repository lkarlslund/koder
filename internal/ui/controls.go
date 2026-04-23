package ui

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/lkarlslund/koder/internal/theme"
)

type SelectableRow struct {
	ControlID      string
	Primary        string
	Secondary      string
	Tertiary       string
	Width          int
	PrimaryWidth   int
	SecondaryWidth int
	TertiaryWidth  int
	Selected       bool
	Focused        bool
}

type SelectableHeader struct {
	Primary        string
	Secondary      string
	Tertiary       string
	Width          int
	PrimaryWidth   int
	SecondaryWidth int
	TertiaryWidth  int
}

func (h SelectableHeader) View(palette theme.Palette) string {
	primaryWidth, secondaryWidth, tertiaryWidth := selectableColumnWidths(h.Width, h.Primary, h.Secondary, h.Tertiary, h.PrimaryWidth, h.SecondaryWidth, h.TertiaryWidth)
	gapWidth := 2
	row := lipgloss.JoinHorizontal(
		lipgloss.Top,
		lipgloss.NewStyle().Width(primaryWidth).Bold(true).Foreground(palette.AssistantTimestampText).Render(truncateText(strings.TrimSpace(h.Primary), primaryWidth)),
		lipgloss.NewStyle().Width(gapWidth).Render(""),
		lipgloss.NewStyle().Width(secondaryWidth).Bold(true).Foreground(palette.AssistantTimestampText).Render(truncateText(strings.TrimSpace(h.Secondary), secondaryWidth)),
	)
	if tertiaryWidth > 0 {
		row = lipgloss.JoinHorizontal(
			lipgloss.Top,
			row,
			lipgloss.NewStyle().Width(gapWidth).Render(""),
			lipgloss.NewStyle().Width(tertiaryWidth).Bold(true).Align(lipgloss.Right).Foreground(palette.AssistantTimestampText).Render(truncateText(strings.TrimSpace(h.Tertiary), tertiaryWidth)),
		)
	}
	return row
}

func (r SelectableRow) View(palette theme.Palette) string {
	primary := r.Primary
	secondary := r.Secondary
	tertiary := r.Tertiary
	width := r.Width
	selected := r.Selected
	focused := r.Focused
	if width <= 0 {
		width = 72
	}
	primary = compactInlineText(primary)
	secondary = compactInlineText(secondary)
	tertiary = compactInlineText(tertiary)
	primaryWidth, secondaryWidth, tertiaryWidth := selectableColumnWidths(width, primary, secondary, tertiary, r.PrimaryWidth, r.SecondaryWidth, r.TertiaryWidth)
	gapWidth := 2
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
	if focused {
		focusedBackground := deriveFocusedBackground(selectionBackground, firstNonEmptyColor(palette.ScreenBackground, palette.SidebarBackground, palette.UserTextBackground))
		focusedForeground := selectionForeground
		rowStyle = rowStyle.Background(focusedBackground).Foreground(focusedForeground)
		primaryStyle = primaryStyle.Background(focusedBackground).Foreground(focusedForeground)
		gapStyle = gapStyle.Background(focusedBackground)
		secondaryStyle = secondaryStyle.Background(focusedBackground).Foreground(focusedForeground)
		tertiaryStyle = tertiaryStyle.Background(focusedBackground).Foreground(focusedForeground).Bold(true)
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

func (r SelectableRow) Measure(ctx *Context, constraints Constraints) Size {
	return constraints.Clamp(SurfaceFromString(r.View(ctx.Palette)).Size())
}

func (r SelectableRow) Render(ctx *Context, bounds Rect) Surface {
	if ctx != nil && ctx.Runtime != nil && strings.TrimSpace(r.ControlID) != "" {
		ctx.Runtime.Register(Control{
			ID:      r.ControlID,
			Rect:    Rect{X: bounds.X, Y: bounds.Y, W: bounds.W, H: max(1, bounds.H)},
			Enabled: true,
		})
	}
	return SurfaceFromString(r.View(ctx.Palette)).normalize(bounds.W, bounds.H)
}

func selectableColumnWidths(width int, primary, secondary, tertiary string, primaryWidth, secondaryWidth, tertiaryWidth int) (int, int, int) {
	if width <= 0 {
		width = 72
	}
	gapWidth := 2
	if primaryWidth <= 0 {
		primaryWidth = minInt(28, maxInt(12, width/3))
	}
	if tertiaryWidth <= 0 && strings.TrimSpace(tertiary) != "" {
		tertiaryWidth = minInt(18, maxInt(8, width/5))
	}
	if secondaryWidth <= 0 {
		secondaryWidth = maxInt(8, width-primaryWidth-tertiaryWidth-gapWidth*2)
		if tertiaryWidth == 0 {
			secondaryWidth = maxInt(8, width-primaryWidth-gapWidth)
		}
	}
	return primaryWidth, secondaryWidth, tertiaryWidth
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
			Background(deriveFocusedBackground(firstNonEmptyColor(palette.SelectionBackground, palette.UserTextBackground), firstNonEmptyColor(palette.ScreenBackground, palette.SidebarBackground, palette.UserTextBackground))).
			Foreground(firstNonEmptyColor(palette.SelectionForeground, palette.UserTextForeground))
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
	row := SelectableRow{
		Primary:   r.Label,
		Secondary: r.Description,
		Tertiary:  glyph + " " + label,
		Width:     width,
		Selected:  focused,
		Focused:   focused,
	}.View(palette)
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
	return SelectableRow{
		Primary:   r.Label,
		Secondary: r.Description,
		Tertiary:  r.Value,
		Width:     width,
		Selected:  focused,
		Focused:   focused,
	}.View(palette)
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
	background := lipgloss.Color("")
	foreground := lipgloss.Color("")
	bold := false
	if b.Primary {
		background = firstNonEmptyColor(palette.SelectionBackground, palette.UserTextBackground)
		foreground = firstNonEmptyColor(palette.SelectionForeground, palette.UserTextForeground)
		bold = true
	}
	if b.Selected {
		background = firstNonEmptyColor(palette.SelectionBackground, palette.UserTextBackground)
		foreground = firstNonEmptyColor(palette.SelectionForeground, palette.UserTextForeground)
		bold = true
	}
	if b.Focused {
		background = deriveFocusedBackground(firstNonEmptyColor(palette.SelectionBackground, palette.UserTextBackground), firstNonEmptyColor(palette.ScreenBackground, palette.SidebarBackground, palette.UserTextBackground))
		foreground = firstNonEmptyColor(palette.SelectionForeground, palette.UserTextForeground)
		bold = true
	}
	label := b.Label
	if b.Hotkey != 0 {
		label = renderButtonLabel(b.Label, b.Hotkey, palette, foreground, background, bold)
	} else {
		label = renderButtonSegment(b.Label, foreground, background, bold)
	}
	leftPad := renderButtonSegment("  ", foreground, background, bold)
	rightPad := renderButtonSegment("  ", foreground, background, bold)
	return leftPad + label + rightPad
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

func (r ButtonRow) Measure(ctx *Context, constraints Constraints) Size {
	return constraints.Clamp(SurfaceFromString(r.View(ctx.Palette)).Size())
}

func (r ButtonRow) Render(ctx *Context, bounds Rect) Surface {
	rendered := r.View(ctx.Palette)
	line := r.line(ctx.Palette)
	rowWidth := ansi.StringWidth(rendered)
	lineWidth := ansi.StringWidth(line)
	startX := 0
	if bounds.W > lineWidth {
		switch r.Align {
		case HorizontalAlignCenter:
			startX = max(0, (bounds.W-lineWidth)/2)
		case HorizontalAlignRight:
			startX = max(0, bounds.W-lineWidth)
		}
	}
	offset := 0
	for _, button := range r.Buttons {
		if ctx != nil && ctx.Runtime != nil && strings.TrimSpace(button.ID) != "" {
			buttonWidth := ansi.StringWidth(button.View(ctx.Palette))
			ctx.Runtime.Register(Control{
				ID:      button.ID,
				Rect:    Rect{X: bounds.X + startX + offset, Y: bounds.Y, W: buttonWidth, H: 1},
				Enabled: true,
			})
			offset += buttonWidth + r.gap()
		}
	}
	return SurfaceFromString(rendered).normalize(max(bounds.W, rowWidth), bounds.H)
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

func renderButtonLabel(label string, hotkey rune, palette theme.Palette, foreground, background lipgloss.Color, bold bool) string {
	labelRunes := []rune(label)
	target := []rune(strings.ToLower(string(hotkey)))
	if len(target) == 0 {
		return renderButtonSegment(label, foreground, background, bold)
	}
	idx := -1
	for i, r := range labelRunes {
		if strings.ToLower(string(r)) == string(target) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return renderButtonSegment(label, foreground, background, bold)
	}
	before := renderButtonSegment(string(labelRunes[:idx]), foreground, background, bold)
	hot := renderButtonSegment(string(labelRunes[idx]), palette.ActivityText, background, true)
	after := renderButtonSegment(string(labelRunes[idx+1:]), foreground, background, bold)
	return before + hot + after
}

func renderButtonSegment(text string, foreground, background lipgloss.Color, bold bool) string {
	style := lipgloss.NewStyle()
	if strings.TrimSpace(string(foreground)) != "" {
		style = style.Foreground(foreground)
	}
	if strings.TrimSpace(string(background)) != "" {
		style = style.Background(background)
	}
	if bold {
		style = style.Bold(true)
	}
	return style.Render(text)
}

func firstNonEmptyColor(values ...lipgloss.Color) lipgloss.Color {
	for _, value := range values {
		if strings.TrimSpace(string(value)) != "" {
			return value
		}
	}
	return lipgloss.Color("")
}

func deriveFocusedBackground(base lipgloss.Color, screen lipgloss.Color) lipgloss.Color {
	rawBase := strings.TrimSpace(string(base))
	rawScreen := strings.TrimSpace(string(screen))
	if len(rawBase) != 7 || rawBase[0] != '#' {
		return base
	}
	r, errR := strconv.ParseInt(rawBase[1:3], 16, 64)
	g, errG := strconv.ParseInt(rawBase[3:5], 16, 64)
	b, errB := strconv.ParseInt(rawBase[5:7], 16, 64)
	if errR != nil || errG != nil || errB != nil {
		return base
	}
	screenLuminance := 255.0
	if len(rawScreen) == 7 && rawScreen[0] == '#' {
		sr, errSR := strconv.ParseInt(rawScreen[1:3], 16, 64)
		sg, errSG := strconv.ParseInt(rawScreen[3:5], 16, 64)
		sb, errSB := strconv.ParseInt(rawScreen[5:7], 16, 64)
		if errSR == nil && errSG == nil && errSB == nil {
			screenLuminance = 0.2126*float64(sr) + 0.7152*float64(sg) + 0.0722*float64(sb)
		}
	}
	adjust := func(v int64) int64 {
		if screenLuminance < 140 {
			return minInt64(255, v+28)
		}
		return maxInt64(0, v-28)
	}
	return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", adjust(r), adjust(g), adjust(b)))
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
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

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
