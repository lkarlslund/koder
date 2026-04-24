package dialogs

import (
	"strconv"
	"strings"

	. "github.com/lkarlslund/koder/internal/ui"
)

type ThemeDialogActionKind int

const (
	ThemeDialogActionNone ThemeDialogActionKind = iota
	ThemeDialogActionSelect
	ThemeDialogActionCancel
)

type ThemeDialogAction struct {
	Kind  ThemeDialogActionKind
	Theme string
}

type ThemeDialog struct {
	Query   string
	Index   int
	Themes  []string
	view    []string
	focus   pickerDialogFocus
	buttons ButtonRow
}

func NewThemeDialog(themes []string, current string) ThemeDialog {
	d := ThemeDialog{Themes: themes}
	d.buttons = ButtonRow{
		Buttons: []Button{
			{ID: "ok", Label: "OK", Hotkey: 'o', Primary: true},
			{ID: "cancel", Label: "Cancel", Hotkey: 'c'},
		},
		Align: HorizontalAlignRight,
	}
	d.refilter()
	d.SetCurrentValue(current)
	return d
}

func (d *ThemeDialog) Current() (string, bool) {
	if len(d.view) == 0 || d.Index < 0 || d.Index >= len(d.view) {
		return "", false
	}
	return d.view[d.Index], true
}

func (d *ThemeDialog) SetCurrentValue(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for idx, item := range d.view {
		if item != value {
			continue
		}
		d.Index = idx
		return true
	}
	return false
}

func (d *ThemeDialog) Update(msg KeyMsg) ThemeDialogAction {
	var action ThemeDialogAction
	d.buttons.Buttons[0].OnClick = func() { action = d.selectCurrent() }
	d.buttons.Buttons[1].OnClick = func() { action = ThemeDialogAction{Kind: ThemeDialogActionCancel} }
	if d.buttons.ActivateHotkey(msg) {
		return action
	}
	switch msg.String() {
	case "esc":
		return ThemeDialogAction{Kind: ThemeDialogActionCancel}
	case "tab":
		d.focus = (d.focus + 1) % 2
	case "shift+tab":
		d.focus--
		if d.focus < 0 {
			d.focus = pickerDialogFocusButtons
		}
	case "up":
		if d.focus == pickerDialogFocusList {
			d.moveGrid(0, -1)
		}
	case "down":
		if d.focus == pickerDialogFocusList {
			d.moveGrid(0, 1)
		}
	case "left":
		if d.focus == pickerDialogFocusButtons {
			d.buttons.Move(-1)
		} else if d.focus == pickerDialogFocusList {
			d.moveGrid(-1, 0)
		}
	case "right":
		if d.focus == pickerDialogFocusButtons {
			d.buttons.Move(1)
		} else if d.focus == pickerDialogFocusList {
			d.moveGrid(1, 0)
		}
	case "enter":
		if d.focus == pickerDialogFocusButtons {
			d.buttons.ActivateFocused()
			return action
		}
		return d.selectCurrent()
	case "backspace":
		if d.focus == pickerDialogFocusList && d.Query != "" {
			d.Query = d.Query[:len(d.Query)-1]
			d.refilter()
		}
	default:
		if d.focus == pickerDialogFocusList && msg.Type == KeyRunes {
			d.Query += msg.String()
			d.refilter()
		}
	}
	return action
}

func (d *ThemeDialog) ActivateControl(controlID string) ThemeDialogAction {
	var action ThemeDialogAction
	d.buttons.Buttons[0].OnClick = func() { action = d.selectCurrent() }
	d.buttons.Buttons[1].OnClick = func() { action = ThemeDialogAction{Kind: ThemeDialogActionCancel} }
	switch controlID {
	case "ok", "cancel":
		d.focus = pickerDialogFocusButtons
		for idx, button := range d.buttons.Buttons {
			if button.ID == controlID {
				d.buttons.Index = idx
				d.buttons.ActivateFocused()
				return action
			}
		}
	default:
		if strings.HasPrefix(controlID, "theme-item-") {
			idx, err := strconv.Atoi(strings.TrimPrefix(controlID, "theme-item-"))
			if err != nil || idx < 0 || idx >= len(d.view) {
				return ThemeDialogAction{}
			}
			d.Index = idx
			d.focus = pickerDialogFocusList
			return d.selectCurrent()
		}
	}
	return ThemeDialogAction{}
}

func (d ThemeDialog) Measure(ctx *Context, constraints Constraints) Size {
	width := constraints.MaxW
	if width <= 0 {
		width = 84
	}
	return constraints.Clamp(d.dialog(width).Measure(ctx, Constraints{MaxW: width, MaxH: constraints.MaxH}))
}

func (d ThemeDialog) Render(ctx *Context, bounds Rect) Surface {
	maxWidth := dialogRenderWidth(bounds, 84)
	element := d.dialog(maxWidth)
	size := element.Measure(ctx, Constraints{MaxW: maxWidth, MaxH: bounds.H})
	return element.Render(ctx, Rect{X: bounds.X, Y: bounds.Y, W: size.W, H: bounds.H})
}

func (d ThemeDialog) dialog(width int) Element {
	dialogWidth := minInt(84, maxInt(68, width))
	buttons := d.buttonRow(dialogWidth)
	buttons.Width = maxInt(0, dialogWidth-6)
	gridItems := make([]SelectionGridItem, 0, len(d.view))
	for idx, item := range d.view {
		gridItems = append(gridItems, SelectionGridItem{
			ControlID: "theme-item-" + strconv.Itoa(idx),
			Title:     item,
		})
	}
	var chooser Element
	if len(gridItems) == 0 {
		chooser = staticBlock("No matches")
	} else {
		chooser = SelectionGrid{
			Items:      gridItems,
			Width:      dialogWidth - 4,
			Columns:    4,
			Selected:   d.Index,
			Focused:    d.focus == pickerDialogFocusList,
			CellHeight: 1,
		}
	}
	return WindowFrame{
		Title: "Themes",
		Width: dialogWidth,
		Content: Column{
			Children: []Child{
				Fixed(staticBlock("Filter: " + d.Query)),
				Fixed(chooser),
				Fixed(buttons),
			},
			Spacing: 1,
		},
		ShowClose: true,
	}
}

func (d ThemeDialog) buttonRow(width int) ButtonRow {
	buttons := d.buttons
	buttons.Width = maxInt(0, width-4)
	buttons.Align = HorizontalAlignRight
	return buttons
}

func (d *ThemeDialog) moveGrid(deltaCol, deltaRow int) {
	if len(d.view) == 0 {
		d.Index = 0
		return
	}
	d.Index = themeGridMove(d.Index, len(d.view), 4, deltaCol, deltaRow)
}

func (d *ThemeDialog) refilter() {
	query := strings.ToLower(strings.TrimSpace(d.Query))
	currentValue := ""
	if current, ok := d.Current(); ok {
		currentValue = current
	}
	d.view = d.view[:0]
	for _, item := range d.Themes {
		if query == "" || strings.Contains(strings.ToLower(item), query) {
			d.view = append(d.view, item)
		}
	}
	if len(d.view) == 0 {
		d.Index = 0
		return
	}
	if currentValue != "" {
		for idx, item := range d.view {
			if item == currentValue {
				d.Index = idx
				return
			}
		}
	}
	if d.Index >= len(d.view) {
		d.Index = len(d.view) - 1
	}
	if d.Index < 0 {
		d.Index = 0
	}
}

func (d *ThemeDialog) selectCurrent() ThemeDialogAction {
	item, ok := d.Current()
	if !ok {
		return ThemeDialogAction{Kind: ThemeDialogActionCancel}
	}
	return ThemeDialogAction{Kind: ThemeDialogActionSelect, Theme: item}
}

func themeGridMove(index, total, columns, deltaCol, deltaRow int) int {
	if total <= 0 {
		return 0
	}
	if index < 0 {
		index = 0
	}
	if index >= total {
		index = total - 1
	}
	columns = max(1, columns)
	col := index % columns
	row := index / columns
	col += deltaCol
	row += deltaRow
	if col < 0 {
		col = 0
	}
	if col >= columns {
		col = columns - 1
	}
	if row < 0 {
		row = 0
	}
	maxRow := (total - 1) / columns
	if row > maxRow {
		row = maxRow
	}
	next := row*columns + col
	if next >= total {
		next = total - 1
	}
	return next
}
