package dialogs

import (
	"strconv"
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/theme"
	"github.com/lkarlslund/koder/internal/ui"
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
	buttons  ui.ButtonRow
}

func NewToolsDialog(items []ToolToggleItem) ToolsDialog {
	dialog := ToolsDialog{
		items:    append([]ToolToggleItem(nil), items...),
		original: map[domain.ToolKind]bool{},
		buttons: ui.ButtonRow{
			Buttons: []ui.Button{
				{ID: "ok", Label: "OK", Hotkey: 'o', Primary: true},
				{ID: "cancel", Label: "Cancel", Hotkey: 'c'},
			},
			Align: ui.HorizontalAlignRight,
		},
	}
	for _, item := range dialog.items {
		dialog.original[item.Tool] = item.Enabled
	}
	return dialog
}

func (d *ToolsDialog) Update(msg ui.KeyMsg) ToolsDialogAction {
	var action ToolsDialogAction
	d.buttons.Buttons[0].OnClick = func() {
		action = ToolsDialogAction{Kind: ToolsDialogActionApply, States: d.States()}
	}
	d.buttons.Buttons[1].OnClick = func() {
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

func (d ToolsDialog) Measure(ctx *ui.Context, constraints ui.Constraints) ui.Size {
	width := constraints.MaxW
	if width <= 0 {
		width = 88
	}
	return constraints.Clamp(d.dialog(width, ctx.Palette).Measure(ctx, ui.Constraints{MaxW: width, MaxH: constraints.MaxH}))
}

func (d ToolsDialog) Surface(ctx *ui.Context, bounds ui.Rect) ui.Surface {
	maxWidth := dialogRenderWidth(bounds, 88)
	element := d.dialog(maxWidth, ctx.Palette)
	size := element.Measure(ctx, ui.Constraints{MaxW: maxWidth, MaxH: bounds.H})
	return ui.PaintElementSurface(ctx, element, ui.Rect{W: size.W, H: bounds.H})
}

func (d ToolsDialog) Paint(ctx *ui.Context, canvas ui.Canvas) {
	if canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	canvas.BlitSurface(0, 0, d.Surface(ctx, ui.Rect{W: canvas.Width(), H: canvas.Height()}))
}

func (d ToolsDialog) dialog(width int, palette theme.Palette) ui.Element {
	dialogWidth := width
	if dialogWidth <= 0 {
		dialogWidth = 88
	}
	dialogWidth = maxInt(72, dialogWidth)
	rowWidth := maxInt(56, dialogWidth-6)
	rows := make([]ui.Child, 0, len(d.items))
	for idx, item := range d.items {
		rows = append(rows, ui.Fixed(ui.HitBox{
			ID: "tool-row-" + strconv.Itoa(idx),
			Child: ui.CheckboxRow{
				Label:       item.Label,
				Description: item.Description,
				Checked:     item.Enabled,
				OnLabel:     "Enabled",
				OffLabel:    "Disabled",
				Width:       rowWidth,
				Focused:     d.focus == toolsDialogFocusList && idx == d.index,
			},
		}))
	}
	buttons := d.buttonRow(dialogWidth)
	buttons.Width = maxInt(0, dialogWidth-6)
	return ui.WindowFrame{
		Title: "Tools",
		Width: dialogWidth,
		Content: ui.FlexBox{
			Direction: ui.DirectionVertical,
			Children: []ui.Child{
				ui.Fixed(ui.Static{Content: "Per-session tool access. Space toggles the current tool."}),
				ui.Fixed(ui.FlexBox{Direction: ui.DirectionVertical, Children: rows}),
				ui.Fixed(buttons),
				ui.Fixed(ui.Static{Content: "Enter toggles a tool or activates the focused button. Esc cancels."}),
			},
			Spacing: 2,
		},
		ShowClose: true,
	}
}

func (d *ToolsDialog) ActivateControl(controlID string) ToolsDialogAction {
	var action ToolsDialogAction
	d.buttons.Buttons[0].OnClick = func() {
		action = ToolsDialogAction{Kind: ToolsDialogActionApply, States: d.States()}
	}
	d.buttons.Buttons[1].OnClick = func() {
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

func (d ToolsDialog) buttonRow(width int) ui.ButtonRow {
	buttons := d.buttons
	buttons.Width = maxInt(0, width-4)
	buttons.Align = ui.HorizontalAlignRight
	return buttons
}
