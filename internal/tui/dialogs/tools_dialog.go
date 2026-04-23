package dialogs

import (
	"strconv"
	"strings"

	tea "github.com/lkarlslund/koder/internal/ui/tea"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/theme"
	. "github.com/lkarlslund/koder/internal/ui"
)

type ToolToggleItem struct {
	Tool        domain.ToolKind
	Label       string
	Description string
	Enabled     bool
}

type ToolsDialogActionKind int

const (
	ToolsDialogActionNone ToolsDialogActionKind = iota
	ToolsDialogActionApply
	ToolsDialogActionCancel
)

type ToolsDialogAction struct {
	Kind   ToolsDialogActionKind
	States map[domain.ToolKind]bool
}

type toolsDialogFocus int

const (
	toolsDialogFocusList toolsDialogFocus = iota
	toolsDialogFocusButtons
)

type ToolsDialog struct {
	items    []ToolToggleItem
	original map[domain.ToolKind]bool
	index    int
	focus    toolsDialogFocus
	buttons  ButtonRow
}

func NewToolsDialog(items []ToolToggleItem) ToolsDialog {
	dialog := ToolsDialog{
		items:    append([]ToolToggleItem(nil), items...),
		original: map[domain.ToolKind]bool{},
		buttons: ButtonRow{
			Buttons: []Button{
				{ID: "ok", Label: "OK", Hotkey: 'o', Primary: true},
				{ID: "cancel", Label: "Cancel", Hotkey: 'c'},
			},
			Align: HorizontalAlignRight,
		},
	}
	for _, item := range dialog.items {
		dialog.original[item.Tool] = item.Enabled
	}
	return dialog
}

func (d *ToolsDialog) Update(msg tea.KeyMsg) ToolsDialogAction {
	var action ToolsDialogAction
	d.buttons.Buttons[0].OnPress = func() {
		action = ToolsDialogAction{Kind: ToolsDialogActionApply, States: d.States()}
	}
	d.buttons.Buttons[1].OnPress = func() {
		action = ToolsDialogAction{Kind: ToolsDialogActionCancel, States: d.originalStates()}
	}
	if d.buttons.ActivateHotkey(msg) {
		return action
	}
	switch msg.String() {
	case "esc":
		return ToolsDialogAction{Kind: ToolsDialogActionCancel, States: d.originalStates()}
	case "tab":
		d.focus = (d.focus + 1) % 2
	case "shift+tab":
		d.focus--
		if d.focus < 0 {
			d.focus = toolsDialogFocusButtons
		}
	case "up":
		if d.focus == toolsDialogFocusList {
			d.move(-1)
		}
	case "down":
		if d.focus == toolsDialogFocusList {
			d.move(1)
		}
	case "left":
		if d.focus == toolsDialogFocusButtons {
			d.buttons.Move(-1)
		}
	case "right":
		if d.focus == toolsDialogFocusButtons {
			d.buttons.Move(1)
		}
	case " ", "enter":
		if d.focus == toolsDialogFocusButtons {
			d.buttons.ActivateFocused()
			return action
		}
		d.toggleCurrent()
	case "x":
		if d.focus == toolsDialogFocusList {
			d.toggleCurrent()
		}
	}
	return action
}

func (d ToolsDialog) Measure(ctx *Context, constraints Constraints) Size {
	width := constraints.MaxW
	if width <= 0 {
		width = 88
	}
	return constraints.Clamp(d.dialog(width, ctx.Palette).Measure(ctx, Constraints{MaxW: width, MaxH: constraints.MaxH}))
}

func (d ToolsDialog) Render(ctx *Context, bounds Rect) Surface {
	maxWidth := dialogRenderWidth(bounds, 88)
	element := d.dialog(maxWidth, ctx.Palette)
	size := element.Measure(ctx, Constraints{MaxW: maxWidth, MaxH: bounds.H})
	return element.Render(ctx, Rect{X: bounds.X, Y: bounds.Y, W: size.W, H: bounds.H})
}

func (d ToolsDialog) dialog(width int, palette theme.Palette) Element {
	dialogWidth := width
	if dialogWidth <= 0 {
		dialogWidth = 88
	}
	dialogWidth = maxInt(72, dialogWidth)
	rowWidth := maxInt(56, dialogWidth-6)
	rows := make([]Child, 0, len(d.items))
	for idx, item := range d.items {
		rows = append(rows, Fixed(HitBox{
			ID: "tool-row-" + strconv.Itoa(idx),
			Child: TextPane{Content: CheckboxRow{
				Label:       item.Label,
				Description: item.Description,
				Checked:     item.Enabled,
				OnLabel:     "Enabled",
				OffLabel:    "Disabled",
			}.View(rowWidth, palette, d.focus == toolsDialogFocusList && idx == d.index)},
		}))
	}
	return Dialog{
		Title:    "Tools",
		Subtitle: "Per-session tool access. Space toggles the current tool.",
		Body:     Column{Children: rows},
		Buttons:  d.buttonRow(dialogWidth),
		Footer:   "Enter toggles a tool or activates the focused button. Esc cancels.",
		Width:    dialogWidth,
	}
}

func (d *ToolsDialog) ActivateControl(controlID string) ToolsDialogAction {
	var action ToolsDialogAction
	d.buttons.Buttons[0].OnPress = func() {
		action = ToolsDialogAction{Kind: ToolsDialogActionApply, States: d.States()}
	}
	d.buttons.Buttons[1].OnPress = func() {
		action = ToolsDialogAction{Kind: ToolsDialogActionCancel, States: d.originalStates()}
	}
	switch controlID {
	case "ok", "cancel":
		d.focus = toolsDialogFocusButtons
		for idx, button := range d.buttons.Buttons {
			if button.ID == controlID {
				d.buttons.Index = idx
				d.buttons.ActivateFocused()
				return action
			}
		}
	default:
		if strings.HasPrefix(controlID, "tool-row-") {
			idx, err := strconv.Atoi(strings.TrimPrefix(controlID, "tool-row-"))
			if err != nil || idx < 0 || idx >= len(d.items) {
				return ToolsDialogAction{}
			}
			d.index = idx
			d.focus = toolsDialogFocusList
			d.toggleCurrent()
			return ToolsDialogAction{}
		}
	}
	return ToolsDialogAction{}
}

func (d ToolsDialog) States() map[domain.ToolKind]bool {
	states := make(map[domain.ToolKind]bool, len(d.items))
	for _, item := range d.items {
		states[item.Tool] = item.Enabled
	}
	return states
}

func (d ToolsDialog) originalStates() map[domain.ToolKind]bool {
	states := make(map[domain.ToolKind]bool, len(d.original))
	for kind, enabled := range d.original {
		states[kind] = enabled
	}
	return states
}

func (d *ToolsDialog) move(delta int) {
	if len(d.items) == 0 {
		d.index = 0
		return
	}
	d.index += delta
	if d.index < 0 {
		d.index = 0
	}
	if d.index >= len(d.items) {
		d.index = len(d.items) - 1
	}
}

func (d *ToolsDialog) toggleCurrent() {
	if len(d.items) == 0 || d.index < 0 || d.index >= len(d.items) {
		return
	}
	d.items[d.index].Enabled = !d.items[d.index].Enabled
}

func (d ToolsDialog) buttonRow(width int) ButtonRow {
	buttons := d.buttons
	buttons.Width = maxInt(0, width-4)
	buttons.Align = HorizontalAlignRight
	return buttons
}
