package ui

import (
	"fmt"
	"strings"

	"github.com/lkarlslund/koder/internal/theme"
)

type SelectableRow struct {
	PassiveNode
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
	PassiveNode
	Primary        string
	Secondary      string
	Tertiary       string
	Width          int
	PrimaryWidth   int
	SecondaryWidth int
	TertiaryWidth  int
}

func (h SelectableHeader) View(palette theme.Palette) string {
	return strings.Join(h.render(palette).Lines(), "\n")
}

func (h SelectableHeader) render(palette theme.Palette) Surface {
	primaryWidth, secondaryWidth, tertiaryWidth := selectableColumnWidths(h.Width, h.Primary, h.Secondary, h.Tertiary, h.PrimaryWidth, h.SecondaryWidth, h.TertiaryWidth)
	gapWidth := 2
	width := maxInt(h.Width, primaryWidth+secondaryWidth+tertiaryWidth+gapWidth)
	if tertiaryWidth > 0 {
		width += gapWidth
	}
	s := TransparentSurface(width, 1)
	style := CellStyle{FG: cellColor(palette.AssistantTimestampText)}.WithBold(true)
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
	return strings.Join(r.render(palette).Lines(), "\n")
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
	if !selectionBackground.Valid() {
		selectionBackground = palette.UserTextBackground
	}
	if !selectionForeground.Valid() {
		selectionForeground = palette.UserTextForeground
	}
	rowStyle := CellStyle{}
	primaryStyle := CellStyle{}.WithBold(true)
	secondaryStyle := CellStyle{FG: cellColor(palette.AssistantTimestampText)}
	tertiaryStyle := CellStyle{FG: cellColor(palette.ActivityText)}
	if selected {
		rowStyle = CellStyle{BG: cellColor(selectionBackground), FG: cellColor(selectionForeground)}
		primaryStyle = CellStyle{BG: cellColor(selectionBackground), FG: cellColor(selectionForeground)}.WithBold(true)
		secondaryStyle = CellStyle{BG: cellColor(selectionBackground), FG: cellColor(selectionForeground)}
		tertiaryStyle = CellStyle{BG: cellColor(selectionBackground), FG: cellColor(selectionForeground)}.WithBold(true)
	}
	if focused {
		focusedBackground := deriveFocusedBackground(selectionBackground, firstNonEmptyColor(palette.ScreenBackground, palette.SidebarBackground, palette.UserTextBackground))
		focusedForeground := selectionForeground
		rowStyle = CellStyle{BG: cellColor(focusedBackground), FG: cellColor(focusedForeground)}
		primaryStyle = CellStyle{BG: cellColor(focusedBackground), FG: cellColor(focusedForeground)}.WithBold(true)
		secondaryStyle = CellStyle{BG: cellColor(focusedBackground), FG: cellColor(focusedForeground)}
		tertiaryStyle = CellStyle{BG: cellColor(focusedBackground), FG: cellColor(focusedForeground)}.WithBold(true)
	}
	s := BlankSurface(width, 1)
	fillStyle := rowStyle
	if fillStyle.isZero() {
		fillStyle = CellStyle{}
	}
	for x := 0; x < width; x++ {
		s.setCell(x, 0, blankCell(fillStyle))
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

func (r SelectableRow) Paint(ctx *Context, canvas Canvas) {
	selectableRowPainter{row: r}.Paint(ctx, canvas)
}

type selectableRowPainter struct {
	row SelectableRow
}

func (p selectableRowPainter) Paint(ctx *Context, canvas Canvas) {
	if canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	r := p.row
	if ctx != nil && ctx.Runtime != nil && strings.TrimSpace(r.ControlID) != "" {
		ctx.Runtime.Register(Control{
			ID:      r.ControlID,
			Rect:    Rect{X: canvas.origin.X, Y: canvas.origin.Y, W: canvas.Width(), H: max(1, canvas.Height())},
			Enabled: true,
		})
	}
	rendered := r.render(ctx.Palette)
	canvas.BlitSurface(0, 0, rendered.normalize(canvas.Width(), canvas.Height()))
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
	PassiveNode
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
	return strings.Join(v.render(width, palette, focused).Lines(), "\n")
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

func (v VerticalTabs) Paint(ctx *Context, canvas Canvas) {
	verticalTabsPainter{tabs: v}.Paint(ctx, canvas)
}

type verticalTabsPainter struct {
	tabs VerticalTabs
}

func (p verticalTabsPainter) Paint(ctx *Context, canvas Canvas) {
	width := p.tabs.Width
	if width <= 0 {
		width = canvas.Width()
	}
	if width <= 0 {
		width = 1
	}
	canvas.BlitSurface(0, 0, p.tabs.render(width, ctx.Palette, p.tabs.Focused).normalize(canvas.Width(), canvas.Height()))
}

func (v VerticalTabs) render(width int, palette theme.Palette, focused bool) Surface {
	if width <= 0 {
		width = 1
	}
	s := BlankSurface(width, len(v.Tabs))
	baseStyle := CellStyle{FG: cellColor(palette.SidebarForeground)}
	activeStyle := CellStyle{BG: cellColor(palette.SelectionBackground), FG: cellColor(palette.SelectionForeground)}.WithBold(true)
	if focused {
		activeStyle = CellStyle{
			BG: cellColor(deriveFocusedBackground(firstNonEmptyColor(palette.SelectionBackground, palette.UserTextBackground), firstNonEmptyColor(palette.ScreenBackground, palette.SidebarBackground, palette.UserTextBackground))),
			FG: cellColor(firstNonEmptyColor(palette.SelectionForeground, palette.UserTextForeground)),
		}.WithBold(true)
	}
	for idx, tab := range v.Tabs {
		label := fmt.Sprintf(" %s ", strings.TrimSpace(tab))
		style := baseStyle
		if idx == v.Current() {
			style = activeStyle
		}
		for x := 0; x < width; x++ {
			s.setCell(x, idx, blankCell(style))
		}
		s.WriteText(0, idx, PlainTruncate(label, width, ""), style)
	}
	return s
}

type CheckboxRow struct {
	PassiveNode
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

func (r CheckboxRow) Paint(ctx *Context, canvas Canvas) {
	checkboxRowPainter{row: r}.Paint(ctx, canvas)
}

type checkboxRowPainter struct {
	row CheckboxRow
}

func (p checkboxRowPainter) Paint(ctx *Context, canvas Canvas) {
	r := p.row
	width := r.Width
	if width <= 0 {
		width = canvas.Width()
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
	canvas.BlitSurface(0, 0, SelectableRow{
		Primary:   r.Label,
		Secondary: r.Description,
		Tertiary:  glyph + " " + label,
		Width:     width,
		Selected:  r.Focused,
		Focused:   r.Focused,
	}.render(ctx.Palette).normalize(canvas.Width(), canvas.Height()))
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
	return strings.Join(row.Lines(), "\n")
}

type ChoiceRow struct {
	PassiveNode
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

func (r ChoiceRow) Paint(ctx *Context, canvas Canvas) {
	choiceRowPainter{row: r}.Paint(ctx, canvas)
}

type choiceRowPainter struct {
	row ChoiceRow
}

func (p choiceRowPainter) Paint(ctx *Context, canvas Canvas) {
	r := p.row
	width := r.Width
	if width <= 0 {
		width = canvas.Width()
	}
	if width <= 0 {
		width = 1
	}
	canvas.BlitSurface(0, 0, SelectableRow{
		Primary:   r.Label,
		Secondary: r.Description,
		Tertiary:  r.Value,
		Width:     width,
		Selected:  r.Focused,
		Focused:   r.Focused,
	}.render(ctx.Palette).normalize(canvas.Width(), canvas.Height()))
}

func (r ChoiceRow) render(width int, palette theme.Palette, focused bool) string {
	return strings.Join(SelectableRow{
		Primary:   r.Label,
		Secondary: r.Description,
		Tertiary:  r.Value,
		Width:     width,
		Selected:  focused,
		Focused:   focused,
	}.render(palette).Lines(), "\n")
}

type Button struct {
	ID       string
	Label    string
	Hotkey   rune
	Focused  bool
	Primary  bool
	Selected bool
	OnClick  func()
	OnPress  func()
}

func (b Button) View(palette theme.Palette) string {
	return strings.Join(b.renderSurface(palette).Lines(), "\n")
}

func (b Button) renderSurface(palette theme.Palette) Surface {
	background := CellColor{}
	foreground := CellColor{}
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
	style := CellStyle{FG: cellColor(foreground), BG: cellColor(background)}.WithBold(bold)
	hotStyle := style
	hotStyle.FG = cellColor(palette.ActivityText)
	parts := buttonLabelParts(b.Label, b.Hotkey)
	width := 4
	for _, part := range parts {
		width += PlainWidth(part.text)
	}
	s := BlankSurface(width, 1)
	for x := 0; x < width; x++ {
		s.setCell(x, 0, blankCell(style))
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
	return strings.Join(ButtonRow{
		Buttons: []Button{
			{Label: okLabel, Primary: true},
			{Label: cancelLabel},
		},
	}.render(palette).Lines(), "\n")
}

type HorizontalAlign int

const (
	HorizontalAlignLeft HorizontalAlign = iota
	HorizontalAlignCenter
	HorizontalAlignRight
)

type ButtonRow struct {
	PassiveNode
	Buttons []Button
	Index   int
	Gap     int
	Width   int
	Align   HorizontalAlign
}

func (r ButtonRow) View(palette theme.Palette) string {
	return strings.Join(r.render(palette).Lines(), "\n")
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
	return triggerButton(r.Buttons[r.Index])
}

func (r *ButtonRow) ActivateHotkey(msg KeyMsg) bool {
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
		if triggerButton(button) {
			return true
		}
	}
	return false
}

func triggerButton(button Button) bool {
	if button.OnClick != nil {
		button.OnClick()
		return true
	}
	if button.OnPress != nil {
		button.OnPress()
		return true
	}
	return false
}

func (r *ButtonRow) HotkeyIndex(msg KeyMsg) (int, bool) {
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
	lineWidth := r.lineWidth(palette)
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
	for idx, button := range r.Buttons {
		button.Focused = idx == r.Index
		buttonSurface := button.renderSurface(palette)
		s = s.placeAt(offset, 0, buttonSurface)
		offset += buttonSurface.Size().W
		if idx < len(r.Buttons)-1 {
			offset += r.gap()
		}
	}
	return s
}

func (r ButtonRow) Measure(ctx *Context, constraints Constraints) Size {
	return constraints.Clamp(r.render(ctx.Palette).Size())
}

func (r ButtonRow) Paint(ctx *Context, canvas Canvas) {
	buttonRowPainter{row: r}.Paint(ctx, canvas)
}

type buttonRowPainter struct {
	row ButtonRow
}

func (p buttonRowPainter) Paint(ctx *Context, canvas Canvas) {
	r := p.row
	rendered := r.render(ctx.Palette)
	rowWidth := rendered.Size().W
	lineWidth := r.lineWidth(ctx.Palette)
	startX := 0
	if canvas.Width() > lineWidth {
		switch r.Align {
		case HorizontalAlignCenter:
			startX = max(0, (canvas.Width()-lineWidth)/2)
		case HorizontalAlignRight:
			startX = max(0, canvas.Width()-lineWidth)
		}
	}
	offset := 0
	for _, button := range r.Buttons {
		if ctx != nil && ctx.Runtime != nil && strings.TrimSpace(button.ID) != "" {
			buttonWidth := button.renderSurface(ctx.Palette).Size().W
			ctx.Runtime.Register(Control{
				ID:      button.ID,
				Rect:    Rect{X: canvas.origin.X + startX + offset, Y: canvas.origin.Y, W: buttonWidth, H: 1},
				Enabled: true,
			})
			offset += buttonWidth + r.gap()
		}
	}
	canvas.BlitSurface(0, 0, rendered.normalize(max(canvas.Width(), rowWidth), canvas.Height()))
}

func (r ButtonRow) line(palette theme.Palette) string {
	parts := make([]string, 0, len(r.Buttons))
	for idx, button := range r.Buttons {
		button.Focused = idx == r.Index
		parts = append(parts, strings.Join(button.renderSurface(palette).Lines(), "\n"))
	}
	return strings.Join(parts, strings.Repeat(" ", r.gap()))
}

func (r ButtonRow) lineWidth(palette theme.Palette) int {
	width := 0
	for idx, button := range r.Buttons {
		button.Focused = idx == r.Index
		width += button.renderSurface(palette).Size().W
		if idx < len(r.Buttons)-1 {
			width += r.gap()
		}
	}
	return width
}

func (r ButtonRow) gap() int {
	if r.Gap <= 0 {
		return 2
	}
	return r.Gap
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

func firstNonEmptyColor(values ...CellColor) CellColor {
	for _, value := range values {
		if value.Valid() {
			return value
		}
	}
	return CellColor{}
}

func deriveFocusedBackground(base CellColor, screen CellColor) CellColor {
	if !base.Valid() {
		return base
	}
	screenLuminance := 255.0
	if screen.Valid() {
		screenLuminance = 0.2126*float64(screen.R()) + 0.7152*float64(screen.G()) + 0.0722*float64(screen.B())
	}
	adjust := func(v uint8) uint8 {
		if screenLuminance < 140 {
			return uint8(minInt(255, int(v)+28))
		}
		return uint8(max(0, int(v)-28))
	}
	return NewCellColorRGBA(adjust(base.R()), adjust(base.G()), adjust(base.B()), base.A())
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
