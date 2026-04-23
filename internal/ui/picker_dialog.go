package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	tea "github.com/lkarlslund/koder/internal/ui/tea"

	"github.com/lkarlslund/koder/internal/theme"
)

type PickerItem struct {
	Title       string
	Description string
	Value       string
}

type PickerDialogActionKind int

const (
	PickerDialogActionNone PickerDialogActionKind = iota
	PickerDialogActionSelect
	PickerDialogActionCancel
)

type PickerDialogAction struct {
	Kind  PickerDialogActionKind
	Value string
}

type pickerDialogFocus int

const (
	pickerDialogFocusList pickerDialogFocus = iota
	pickerDialogFocusButtons
)

type PickerDialog struct {
	Title   string
	Hint    string
	Query   string
	Index   int
	Items   []PickerItem
	view    []PickerItem
	Focus   pickerDialogFocus
	buttons ButtonRow
}

func NewPickerDialog(title, hint string, items []PickerItem) PickerDialog {
	d := PickerDialog{
		Title: title,
		Hint:  hint,
		Items: items,
	}
	d.buttons = ButtonRow{
		Buttons: []Button{
			{ID: "ok", Label: "OK", Hotkey: 'o', Primary: true},
			{ID: "cancel", Label: "Cancel", Hotkey: 'c'},
		},
		Align: HorizontalAlignRight,
	}
	d.refilter()
	return d
}

func (d *PickerDialog) Current() (PickerItem, bool) {
	if len(d.view) == 0 || d.Index < 0 || d.Index >= len(d.view) {
		return PickerItem{}, false
	}
	return d.view[d.Index], true
}

func (d *PickerDialog) Move(delta int) {
	d.move(delta)
}

func (d *PickerDialog) SetCurrentValue(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for idx, item := range d.view {
		if item.Value != value {
			continue
		}
		d.Index = idx
		return true
	}
	return false
}

func (d *PickerDialog) Update(msg tea.KeyMsg) PickerDialogAction {
	var action PickerDialogAction
	d.buttons.Buttons[0].OnPress = func() { action = d.selectCurrent() }
	d.buttons.Buttons[1].OnPress = func() { action = PickerDialogAction{Kind: PickerDialogActionCancel} }

	if d.buttons.ActivateHotkey(msg) {
		return action
	}
	switch msg.String() {
	case "esc":
		return PickerDialogAction{Kind: PickerDialogActionCancel}
	case "tab":
		d.Focus = (d.Focus + 1) % 2
	case "shift+tab":
		d.Focus--
		if d.Focus < 0 {
			d.Focus = pickerDialogFocusButtons
		}
	case "up":
		if d.Focus == pickerDialogFocusList {
			d.move(-1)
		}
	case "down":
		if d.Focus == pickerDialogFocusList {
			d.move(1)
		}
	case "left":
		if d.Focus == pickerDialogFocusButtons {
			d.buttons.Move(-1)
		}
	case "right":
		if d.Focus == pickerDialogFocusButtons {
			d.buttons.Move(1)
		}
	case "enter":
		if d.Focus == pickerDialogFocusButtons {
			d.buttons.ActivateFocused()
			return action
		}
		return d.selectCurrent()
	case "backspace":
		if d.Focus == pickerDialogFocusList && d.Query != "" {
			d.Query = d.Query[:len(d.Query)-1]
			d.refilter()
		}
	default:
		if d.Focus == pickerDialogFocusList && msg.Type == tea.KeyRunes {
			d.Query += msg.String()
			d.refilter()
		}
	}
	return action
}

func (d *PickerDialog) ActivateControl(controlID string) PickerDialogAction {
	var action PickerDialogAction
	d.buttons.Buttons[0].OnPress = func() { action = d.selectCurrent() }
	d.buttons.Buttons[1].OnPress = func() { action = PickerDialogAction{Kind: PickerDialogActionCancel} }
	switch controlID {
	case "ok", "cancel":
		d.Focus = pickerDialogFocusButtons
		for idx, button := range d.buttons.Buttons {
			if button.ID == controlID {
				d.buttons.Index = idx
				d.buttons.ActivateFocused()
				return action
			}
		}
	default:
		if strings.HasPrefix(controlID, "picker-row-") {
			idx, err := strconv.Atoi(strings.TrimPrefix(controlID, "picker-row-"))
			if err != nil || idx < 0 || idx >= len(d.view) {
				return PickerDialogAction{}
			}
			d.Index = idx
			d.Focus = pickerDialogFocusList
			return d.selectCurrent()
		}
	}
	return PickerDialogAction{}
}

func (d PickerDialog) Measure(ctx *Context, constraints Constraints) Size {
	width := constraints.maxWidth()
	if width == int(^uint(0)>>1) || width <= 0 {
		width = 80
	}
	return constraints.Clamp(d.element(width, ctx.Palette).Measure(ctx, Constraints{MaxW: width, MaxH: constraints.MaxH}))
}

func (d PickerDialog) Render(ctx *Context, bounds Rect) Surface {
	width := bounds.W
	if width <= 0 {
		width = 80
	}
	return d.element(width, ctx.Palette).Render(ctx, Rect{W: width, H: bounds.H})
}

func (d PickerDialog) element(width int, palette theme.Palette) Element {
	children := []Child{}
	if hint := strings.TrimSpace(d.Hint); hint != "" {
		children = append(children, Fixed(Label{
			Text:  hint,
			Style: lipgloss.NewStyle().Foreground(palette.AssistantTimestampText),
		}))
		children = append(children, Fixed(Spacer{H: 1}))
	}
	children = append(children, Fixed(Label{Text: fmt.Sprintf("filter: %s", d.Query)}))
	children = append(children, Fixed(Spacer{H: 1}))
	if len(d.view) == 0 {
		children = append(children, Fixed(Label{Text: "  no matches"}))
	} else {
		items := make([]ListItem, 0, len(d.view))
		for idx, item := range d.view {
			items = append(items, ListItem{
				ControlID: "picker-row-" + strconv.Itoa(idx),
				Primary:   item.Title,
				Secondary: item.Description,
			})
		}
		children = append(children, Fixed(Section{
			Width: 72,
			Child: List{
				Items:    items,
				Width:    72,
				Selected: d.Index,
				Focused:  d.Focus == pickerDialogFocusList,
			},
		}))
	}
	children = append(children, Fixed(Spacer{H: 1}))
	return Modal{
		Title:       d.Title,
		BodyElement: Column{Children: append(children, Fixed(d.buttonRow(width)))},
		Footer:      "Enter selects. Tab switches focus. Esc cancels.",
		Width:       80,
	}
}

func (d PickerDialog) buttonRow(width int) ButtonRow {
	buttons := d.buttons
	buttons.Width = maxInt(0, width-4)
	buttons.Align = HorizontalAlignRight
	return buttons
}

func (d *PickerDialog) move(delta int) {
	if len(d.view) == 0 {
		d.Index = 0
		return
	}
	d.Index += delta
	if d.Index < 0 {
		d.Index = 0
	}
	if d.Index >= len(d.view) {
		d.Index = len(d.view) - 1
	}
}

func (d *PickerDialog) refilter() {
	query := strings.ToLower(strings.TrimSpace(d.Query))
	currentValue := ""
	if current, ok := d.Current(); ok {
		currentValue = current.Value
	}
	d.view = d.view[:0]
	for _, item := range d.Items {
		haystack := strings.ToLower(item.Title + " " + item.Description + " " + item.Value)
		if query == "" || strings.Contains(haystack, query) {
			d.view = append(d.view, item)
		}
	}
	if len(d.view) == 0 {
		d.Index = 0
		return
	}
	if currentValue != "" {
		for idx, item := range d.view {
			if item.Value == currentValue {
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

func (d *PickerDialog) selectCurrent() PickerDialogAction {
	item, ok := d.Current()
	if !ok {
		return PickerDialogAction{Kind: PickerDialogActionCancel}
	}
	return PickerDialogAction{Kind: PickerDialogActionSelect, Value: item.Value}
}
