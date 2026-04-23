package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	tea "github.com/lkarlslund/koder/internal/ui/tea"

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
	return h.render(palette).String()
}

func (h SelectableHeader) render(palette theme.Palette) Surface {
	primaryWidth, secondaryWidth, tertiaryWidth := selectableColumnWidths(h.Width, h.Primary, h.Secondary, h.Tertiary, h.PrimaryWidth, h.SecondaryWidth, h.TertiaryWidth)
	gapWidth := 2
	width := maxInt(h.Width, primaryWidth+secondaryWidth+tertiaryWidth+gapWidth)
	if tertiaryWidth > 0 {
		width += gapWidth
	}
	s := BlankSurface(width, 1)
	style := CellStyle{FG: palette.AssistantTimestampText, Bold: true}
	col := 0
	s.WriteText(col, 0, truncateText(strings.TrimSpace(h.Primary), primaryWidth), style)
	col += primaryWidth + gapWidth
	s.WriteText(col, 0, truncateText(strings.TrimSpace(h.Secondary), secondaryWidth), style)
	if tertiaryWidth > 0 {
		col += secondaryWidth + gapWidth
		text := truncateText(strings.TrimSpace(h.Tertiary), tertiaryWidth)
		s.WriteText(col+maxInt(0, tertiaryWidth-PlainWidth(text)), 0, text, style)
	}
	return s
}

func (r SelectableRow) View(palette theme.Palette) string {
	return r.render(palette).String()
}

func (r SelectableRow) render(palette theme.Palette) Surface {
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
	rowStyle := CellStyle{}
	primaryStyle := CellStyle{Bold: true}
	secondaryStyle := CellStyle{FG: palette.AssistantTimestampText}
	tertiaryStyle := CellStyle{FG: palette.ActivityText}
	if selected {
		rowStyle = CellStyle{BG: selectionBackground, FG: selectionForeground}
		primaryStyle = CellStyle{BG: selectionBackground, FG: selectionForeground, Bold: true}
		secondaryStyle = CellStyle{BG: selectionBackground, FG: selectionForeground}
		tertiaryStyle = CellStyle{BG: selectionBackground, FG: selectionForeground, Bold: true}
	}
	if focused {
		focusedBackground := deriveFocusedBackground(selectionBackground, firstNonEmptyColor(palette.ScreenBackground, palette.SidebarBackground, palette.UserTextBackground))
		focusedForeground := selectionForeground
		rowStyle = CellStyle{BG: focusedBackground, FG: focusedForeground}
		primaryStyle = CellStyle{BG: focusedBackground, FG: focusedForeground, Bold: true}
		secondaryStyle = CellStyle{BG: focusedBackground, FG: focusedForeground}
		tertiaryStyle = CellStyle{BG: focusedBackground, FG: focusedForeground, Bold: true}
	}
	s := BlankSurface(width, 1)
	fillStyle := rowStyle
	if fillStyle.isZero() {
		fillStyle = CellStyle{}
	}
	for x := 0; x < width; x++ {
		s.setCell(x, 0, Cell{Text: " ", Width: 1, Style: fillStyle})
	}
	col := 0
	s.WriteText(col, 0, truncateText(strings.TrimSpace(primary), primaryWidth), primaryStyle)
	col += primaryWidth + gapWidth
	s.WriteText(col, 0, truncateText(strings.TrimSpace(secondary), secondaryWidth), secondaryStyle)
	if tertiaryWidth > 0 {
		col += secondaryWidth + gapWidth
		text := truncateText(strings.TrimSpace(tertiary), tertiaryWidth)
		s.WriteText(col+maxInt(0, tertiaryWidth-PlainWidth(text)), 0, text, tertiaryStyle)
	}
	return s
}

func (r SelectableRow) Measure(ctx *Context, constraints Constraints) Size {
	return constraints.Clamp(r.render(ctx.Palette).Size())
}

func (r SelectableRow) Render(ctx *Context, bounds Rect) Surface {
	if ctx != nil && ctx.Runtime != nil && strings.TrimSpace(r.ControlID) != "" {
		ctx.Runtime.Register(Control{
			ID:      r.ControlID,
			Rect:    Rect{X: bounds.X, Y: bounds.Y, W: bounds.W, H: max(1, bounds.H)},
			Enabled: true,
		})
	}
	return r.render(ctx.Palette).normalize(bounds.W, bounds.H)
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
	Tabs    []string
	Active  int
	Width   int
	Focused bool
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
	return v.render(width, palette, focused).String()
}

func (v VerticalTabs) Measure(ctx *Context, constraints Constraints) Size {
	width := v.Width
	if width <= 0 {
		width = constraints.MaxW
	}
	if width <= 0 {
		width = 1
	}
	return constraints.Clamp(Size{W: width, H: len(v.Tabs)})
}

func (v VerticalTabs) Render(ctx *Context, bounds Rect) Surface {
	width := v.Width
	if width <= 0 {
		width = bounds.W
	}
	if width <= 0 {
		width = 1
	}
	return v.render(width, ctx.Palette, v.Focused).normalize(bounds.W, bounds.H)
}

func (v VerticalTabs) render(width int, palette theme.Palette, focused bool) Surface {
	if width <= 0 {
		width = 1
	}
	s := BlankSurface(width, len(v.Tabs))
	baseStyle := CellStyle{FG: palette.SidebarForeground}
	activeStyle := CellStyle{BG: palette.SelectionBackground, FG: palette.SelectionForeground, Bold: true}
	if focused {
		activeStyle = CellStyle{
			BG:   deriveFocusedBackground(firstNonEmptyColor(palette.SelectionBackground, palette.UserTextBackground), firstNonEmptyColor(palette.ScreenBackground, palette.SidebarBackground, palette.UserTextBackground)),
			FG:   firstNonEmptyColor(palette.SelectionForeground, palette.UserTextForeground),
			Bold: true,
		}
	}
	for idx, tab := range v.Tabs {
		label := fmt.Sprintf(" %s ", strings.TrimSpace(tab))
		style := baseStyle
		if idx == v.Current() {
			style = activeStyle
		}
		for x := 0; x < width; x++ {
			s.setCell(x, idx, Cell{Text: " ", Width: 1, Style: style})
		}
		s.WriteText(0, idx, PlainTruncate(label, width, ""), style)
	}
	return s
}

type CheckboxRow struct {
	Label       string
	Description string
	Checked     bool
	OnLabel     string
	OffLabel    string
	Width       int
	Focused     bool
}

func (r CheckboxRow) View(width int, palette theme.Palette, focused bool) string {
	return r.render(width, palette, focused)
}

func (r CheckboxRow) Measure(ctx *Context, constraints Constraints) Size {
	width := r.Width
	if width <= 0 {
		width = constraints.MaxW
	}
	if width <= 0 {
		width = 1
	}
	return constraints.Clamp(Size{W: width, H: 1})
}

func (r CheckboxRow) Render(ctx *Context, bounds Rect) Surface {
	width := r.Width
	if width <= 0 {
		width = bounds.W
	}
	if width <= 0 {
		width = 1
	}
	label := strings.TrimSpace(r.OffLabel)
	glyph := "☐"
	if r.Checked {
		label = strings.TrimSpace(r.OnLabel)
		glyph = "☑"
	}
	if label == "" {
		if r.Checked {
			label = "On"
		} else {
			label = "Off"
		}
	}
	return SelectableRow{
		Primary:   r.Label,
		Secondary: r.Description,
		Tertiary:  glyph + " " + label,
		Width:     width,
		Selected:  r.Focused,
		Focused:   r.Focused,
	}.render(ctx.Palette).normalize(bounds.W, bounds.H)
}

func (r CheckboxRow) render(width int, palette theme.Palette, focused bool) string {
	label := strings.TrimSpace(r.OffLabel)
	glyph := "☐"
	if r.Checked {
		label = strings.TrimSpace(r.OnLabel)
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
	}.render(palette)
	if focused {
		line := row.Lines()
		if len(line) > 0 {
			return line[0]
		}
	}
	return row.String()
}

type ChoiceRow struct {
	Label       string
	Description string
	Value       string
	Width       int
	Focused     bool
}

func (r ChoiceRow) View(width int, palette theme.Palette, focused bool) string {
	return r.render(width, palette, focused)
}

func (r ChoiceRow) Measure(ctx *Context, constraints Constraints) Size {
	width := r.Width
	if width <= 0 {
		width = constraints.MaxW
	}
	if width <= 0 {
		width = 1
	}
	return constraints.Clamp(Size{W: width, H: 1})
}

func (r ChoiceRow) Render(ctx *Context, bounds Rect) Surface {
	width := r.Width
	if width <= 0 {
		width = bounds.W
	}
	if width <= 0 {
		width = 1
	}
	return SelectableRow{
		Primary:   r.Label,
		Secondary: r.Description,
		Tertiary:  r.Value,
		Width:     width,
		Selected:  r.Focused,
		Focused:   r.Focused,
	}.render(ctx.Palette).normalize(bounds.W, bounds.H)
}

func (r ChoiceRow) render(width int, palette theme.Palette, focused bool) string {
	return SelectableRow{
		Primary:   r.Label,
		Secondary: r.Description,
		Tertiary:  r.Value,
		Width:     width,
		Selected:  focused,
		Focused:   focused,
	}.render(palette).String()
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
	return b.renderSurface(palette).String()
}

func (b Button) render(palette theme.Palette) string {
	return b.renderSurface(palette).String()
}

func (b Button) renderSurface(palette theme.Palette) Surface {
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
	style := CellStyle{FG: foreground, BG: background, Bold: bold}
	hotStyle := style
	hotStyle.FG = palette.ActivityText
	parts := buttonLabelParts(b.Label, b.Hotkey)
	width := 4
	for _, part := range parts {
		width += PlainWidth(part.text)
	}
	s := BlankSurface(width, 1)
	for x := 0; x < width; x++ {
		s.setCell(x, 0, Cell{Text: " ", Width: 1, Style: style})
	}
	col := 2
	for _, part := range parts {
		partStyle := style
		if part.hot {
			partStyle = hotStyle
		}
		s.WriteText(col, 0, part.text, partStyle)
		col += PlainWidth(part.text)
	}
	return s
}

func RenderDialogButtons(palette theme.Palette, okLabel, cancelLabel string) string {
	return ButtonRow{
		Buttons: []Button{
			{Label: okLabel, Primary: true},
			{Label: cancelLabel},
		},
	}.render(palette).String()
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

func (r ButtonRow) View(palette theme.Palette) string {
	return r.render(palette).String()
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

func (r ButtonRow) render(palette theme.Palette) Surface {
	line := r.line(palette)
	lineWidth := PlainWidth(line)
	width := maxInt(lineWidth, r.Width)
	if width <= 0 {
		width = lineWidth
	}
	s := BlankSurface(width, 1)
	startX := 0
	if width > lineWidth {
		switch r.Align {
		case HorizontalAlignCenter:
			startX = max(0, (width-lineWidth)/2)
		case HorizontalAlignRight:
			startX = max(0, width-lineWidth)
		}
	}
	offset := startX
	gap := strings.Repeat(" ", r.gap())
	for idx, button := range r.Buttons {
		button.Focused = idx == r.Index
		buttonSurface := button.renderSurface(palette)
		s = s.placeAt(offset, 0, buttonSurface)
		offset += buttonSurface.Size().W
		if idx < len(r.Buttons)-1 {
			s.WriteText(offset, 0, gap, CellStyle{})
			offset += PlainWidth(gap)
		}
	}
	return s
}

func (r ButtonRow) Measure(ctx *Context, constraints Constraints) Size {
	return constraints.Clamp(r.render(ctx.Palette).Size())
}

func (r ButtonRow) Render(ctx *Context, bounds Rect) Surface {
	rendered := r.render(ctx.Palette)
	line := r.line(ctx.Palette)
	rowWidth := rendered.Size().W
	lineWidth := PlainWidth(line)
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
			buttonWidth := PlainWidth(button.render(ctx.Palette))
			ctx.Runtime.Register(Control{
				ID:      button.ID,
				Rect:    Rect{X: bounds.X + startX + offset, Y: bounds.Y, W: buttonWidth, H: 1},
				Enabled: true,
			})
			offset += buttonWidth + r.gap()
		}
	}
	return rendered.normalize(max(bounds.W, rowWidth), bounds.H)
}

func (r ButtonRow) line(palette theme.Palette) string {
	parts := make([]string, 0, len(r.Buttons))
	for idx, button := range r.Buttons {
		button.Focused = idx == r.Index
		parts = append(parts, button.renderSurface(palette).String())
	}
	return strings.Join(parts, strings.Repeat(" ", r.gap()))
}

func (r ButtonRow) gap() int {
	if r.Gap <= 0 {
		return 2
	}
	return r.Gap
}

func renderButtonLabel(label string, hotkey rune, palette theme.Palette, foreground, background lipgloss.Color, bold bool) string {
	return buttonLabelSurface(label, hotkey, palette, foreground, background, bold).String()
}

type buttonLabelPart struct {
	text string
	hot  bool
}

func buttonLabelParts(label string, hotkey rune) []buttonLabelPart {
	labelRunes := []rune(label)
	target := []rune(strings.ToLower(string(hotkey)))
	if len(target) == 0 {
		return []buttonLabelPart{{text: label}}
	}
	idx := -1
	for i, r := range labelRunes {
		if strings.ToLower(string(r)) == string(target) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return []buttonLabelPart{{text: label}}
	}
	parts := make([]buttonLabelPart, 0, 3)
	if idx > 0 {
		parts = append(parts, buttonLabelPart{text: string(labelRunes[:idx])})
	}
	parts = append(parts, buttonLabelPart{text: string(labelRunes[idx]), hot: true})
	if idx+1 < len(labelRunes) {
		parts = append(parts, buttonLabelPart{text: string(labelRunes[idx+1:])})
	}
	return parts
}

func buttonLabelSurface(label string, hotkey rune, palette theme.Palette, foreground, background lipgloss.Color, bold bool) Surface {
	style := CellStyle{FG: foreground, BG: background, Bold: bold}
	hotStyle := style
	hotStyle.FG = palette.ActivityText
	parts := buttonLabelParts(label, hotkey)
	width := 0
	for _, part := range parts {
		width += PlainWidth(part.text)
	}
	s := BlankSurface(width, 1)
	col := 0
	for _, part := range parts {
		partStyle := style
		if part.hot {
			partStyle = hotStyle
		}
		s.WriteText(col, 0, part.text, partStyle)
		col += PlainWidth(part.text)
	}
	return s
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
	if PlainWidth(input) <= width {
		return input
	}
	if width == 1 {
		return "…"
	}
	return PlainTruncate(input, width, "…")
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
