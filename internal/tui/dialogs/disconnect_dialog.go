package dialogs

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/lkarlslund/koder/internal/theme"
	"github.com/lkarlslund/koder/internal/ui"
)

type ProviderItem struct {
	ID          string
	Title       string
	Description string
	Details     []string
}

type DisconnectDialogActionKind int

const (
	DisconnectDialogActionNone DisconnectDialogActionKind = iota
	DisconnectDialogActionSelect
	DisconnectDialogActionCancel
)

type DisconnectDialogAction struct {
	Kind       DisconnectDialogActionKind
	ProviderID string
}

type DisconnectDialog struct {
	Query   string
	Index   int
	Items   []ProviderItem
	view    []ProviderItem
	focus   pickerDialogFocus
	buttons ui.ButtonRow
}

func NewDisconnectDialog(items []ProviderItem) DisconnectDialog {
	d := DisconnectDialog{Items: items}
	d.buttons = ui.ButtonRow{
		Buttons: []ui.Button{
			{ID: "ok", Label: "OK", Hotkey: 'o', Primary: true},
			{ID: "cancel", Label: "Cancel", Hotkey: 'c'},
		},
		Align: ui.HorizontalAlignRight,
	}
	d.refilter()
	return d
}

func (d *DisconnectDialog) Update(msg ui.KeyMsg) DisconnectDialogAction {
	d.ensureButtons()
	var action DisconnectDialogAction
	d.buttons.Buttons[0].OnClick = func() { action = d.selectCurrent() }
	d.buttons.Buttons[1].OnClick = func() { action = DisconnectDialogAction{Kind: DisconnectDialogActionCancel} }
	if d.buttons.ActivateHotkey(msg) {
		return action
	}
	switch msg.String() {
	case "esc":
		return DisconnectDialogAction{Kind: DisconnectDialogActionCancel}
	case "tab":
		d.focus = (d.focus + 1) % 2
	case "shift+tab":
		d.focus--
		if d.focus < 0 {
			d.focus = pickerDialogFocusButtons
		}
	case "enter":
		if d.focus == pickerDialogFocusButtons {
			d.buttons.ActivateFocused()
			return action
		}
		return d.selectCurrent()
	case "up":
		if d.focus == pickerDialogFocusList {
			d.move(-1)
		}
	case "down":
		if d.focus == pickerDialogFocusList {
			d.move(1)
		}
	case "left":
		if d.focus == pickerDialogFocusButtons {
			d.buttons.Move(-1)
		}
	case "right":
		if d.focus == pickerDialogFocusButtons {
			d.buttons.Move(1)
		}
	case "backspace", "alt+backspace":
		if d.focus == pickerDialogFocusList && d.Query != "" {
			d.Query, _ = ui.DeleteBeforeCursorString(d.Query, len([]rune(d.Query)), msg.Alt)
			d.refilter()
		}
	default:
		if d.focus == pickerDialogFocusList && msg.Type == ui.KeyRunes {
			d.Query += msg.String()
			d.refilter()
		}
	}
	return DisconnectDialogAction{}
}

func (d DisconnectDialog) Measure(ctx *ui.Context, constraints ui.Constraints) ui.Size {
	width := constraints.MaxW
	if width <= 0 {
		width = 84
	}
	return constraints.Clamp(d.dialog(width, ctx.Palette).Measure(ctx, ui.Constraints{MaxW: width, MaxH: constraints.MaxH}))
}

func (d DisconnectDialog) Surface(ctx *ui.Context, bounds ui.Rect) ui.Surface {
	maxWidth := dialogRenderWidth(bounds, 84)
	node := d.dialog(maxWidth, ctx.Palette)
	size := node.Measure(ctx, ui.Constraints{MaxW: maxWidth, MaxH: bounds.H})
	return ui.PaintNodeSurface(ctx, node, ui.Rect{W: size.W, H: bounds.H})
}

func (d DisconnectDialog) Paint(ctx *ui.Context, canvas ui.Canvas) {
	if canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	canvas.BlitSurface(0, 0, d.Surface(ctx, ui.Rect{W: canvas.Width(), H: canvas.Height()}))
}

func (d DisconnectDialog) dialog(width int, palette theme.Palette) ui.Node {
	dialogWidth := width
	if dialogWidth <= 0 {
		dialogWidth = 84
	}
	dialogWidth = maxInt(72, dialogWidth)
	listWidth := 28
	detailWidth := maxInt(36, dialogWidth-listWidth-9)
	items := []ui.ListItem{}
	start := 0
	if d.Index >= 5 {
		start = d.Index - 4
	}
	end := len(d.view)
	if end > start+9 {
		end = start + 9
	}
	for idx := start; idx < end; idx++ {
		item := d.view[idx]
		items = append(items, ui.ListItem{
			ControlID: "disconnect-row-" + strconv.Itoa(idx),
			Primary:   item.Title,
			Secondary: item.Description,
			Tertiary:  item.ID,
		})
	}

	var detailsElement ui.Node = staticBlock("No provider selected")
	if item, ok := d.current(); ok {
		blocks := []ui.Child{
			ui.Fixed(ui.Label{Text: item.Title}),
		}
		for _, line := range item.Details {
			if strings.TrimSpace(line) == "" {
				blocks = append(blocks, ui.Fixed(ui.Spacer{H: 1}))
				continue
			}
			blocks = append(blocks, ui.Fixed(ui.Label{Text: line}))
		}
		if desc := strings.TrimSpace(item.Description); desc != "" {
			blocks = append(blocks, ui.Fixed(ui.Spacer{H: 1}), ui.Fixed(ui.Paragraph{Text: truncateText(desc, detailWidth)}))
		}
		detailsElement = ui.AsNode(ui.FlexBox{Direction: ui.DirectionVertical, Children: blocks})
	}

	buttons := d.buttonRow(dialogWidth)
	buttons.Width = maxInt(0, dialogWidth-6)
	return ui.AsNode(ui.WindowFrame{
		Title: "Disconnect Provider",
		Width: dialogWidth,
		Content: ui.AsNode(ui.FlexBox{
			Direction: ui.DirectionVertical,
			Children: []ui.Child{
				ui.Fixed(ui.AsNode(ui.FlexBox{
					Direction: ui.DirectionVertical,
					Children: []ui.Child{
						ui.Fixed(staticBlock(fmt.Sprintf("Filter: %s", d.Query))),
						ui.Fixed(ui.Spacer{H: 1}),
						ui.Fixed(ui.AsNode(ui.FlexBox{
							Direction: ui.DirectionHorizontal,
							Spacing:   1,
							Children: []ui.Child{
								{
									Node: ui.AsNode(ui.Section{
										Title:   "Providers",
										Width:   listWidth + 2,
										Padding: ui.Insets{Right: 1},
										Child: func() ui.Node {
											if len(items) == 0 {
												return staticBlock("No matches")
											}
											return ui.AsNode(ui.List{
												Items:    items,
												Width:    listWidth,
												Selected: d.Index - start,
												Focused:  d.focus == pickerDialogFocusList,
											})
										}(),
									}),
									Basis: listWidth + 2,
								},
								ui.Flex(ui.AsNode(ui.Section{
									Title:       "Details",
									Width:       detailWidth + 1,
									Padding:     ui.Insets{Left: 1},
									Background:  palette.SidebarBackground,
									Foreground:  palette.SidebarForeground,
									BorderColor: palette.SidebarBorder,
									Child:       detailsElement,
								}), 1),
							},
						})),
					},
				})),
				ui.Fixed(buttons),
				ui.Fixed(ui.Static{Content: "Enter to disconnect, Esc to cancel"}),
			},
			Spacing: 2,
		}),
		ShowClose: true,
	})
}

func (d *DisconnectDialog) ActivateControl(controlID string) DisconnectDialogAction {
	d.ensureButtons()
	var action DisconnectDialogAction
	d.buttons.Buttons[0].OnClick = func() { action = d.selectCurrent() }
	d.buttons.Buttons[1].OnClick = func() { action = DisconnectDialogAction{Kind: DisconnectDialogActionCancel} }
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
		if strings.HasPrefix(controlID, "disconnect-row-") {
			idx, err := strconv.Atoi(strings.TrimPrefix(controlID, "disconnect-row-"))
			if err != nil || idx < 0 || idx >= len(d.view) {
				return DisconnectDialogAction{}
			}
			d.Index = idx
			d.focus = pickerDialogFocusList
			return d.selectCurrent()
		}
	}
	return DisconnectDialogAction{}
}

func (d *DisconnectDialog) move(delta int) {
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

func (d *DisconnectDialog) refilter() {
	query := strings.ToLower(strings.TrimSpace(d.Query))
	d.view = d.view[:0]
	for _, item := range d.Items {
		haystack := strings.ToLower(item.Title + " " + item.Description + " " + item.ID)
		if query == "" || strings.Contains(haystack, query) {
			d.view = append(d.view, item)
		}
	}
	if len(d.view) == 0 {
		d.Index = 0
		return
	}
	if d.Index >= len(d.view) {
		d.Index = len(d.view) - 1
	}
	if d.Index < 0 {
		d.Index = 0
	}
}

func (d DisconnectDialog) current() (ProviderItem, bool) {
	if len(d.view) == 0 || d.Index < 0 || d.Index >= len(d.view) {
		return ProviderItem{}, false
	}
	return d.view[d.Index], true
}

func (d DisconnectDialog) selectCurrent() DisconnectDialogAction {
	item, ok := d.current()
	if !ok {
		return DisconnectDialogAction{Kind: DisconnectDialogActionCancel}
	}
	return DisconnectDialogAction{Kind: DisconnectDialogActionSelect, ProviderID: item.ID}
}

func (d *DisconnectDialog) ensureButtons() {
	if len(d.buttons.Buttons) != 0 {
		return
	}
	d.buttons = ui.ButtonRow{
		Buttons: []ui.Button{
			{ID: "ok", Label: "OK", Hotkey: 'o', Primary: true},
			{ID: "cancel", Label: "Cancel", Hotkey: 'c'},
		},
		Align: ui.HorizontalAlignRight,
	}
}

func (d DisconnectDialog) buttonRow(width int) ui.ButtonRow {
	buttons := d.buttons
	buttons.Width = maxInt(0, width)
	buttons.Align = ui.HorizontalAlignRight
	return buttons
}
