package dialogs

import (
	"fmt"
	"strings"

	"github.com/lkarlslund/koder/internal/theme"
	"github.com/lkarlslund/koder/internal/ui"
)

type EntityListDialogEvent struct {
	ButtonID string
	OpenID   string
	Cancel   bool
}

type EntityListItem struct {
	ID      string
	Cells   []string
	Search  string
	Details string
}

type EntityListDialog struct {
	Title       string
	FilterLabel string
	EmptyText   string
	DetailTitle string
	FooterText  string
	Columns     []ui.TableColumn
	Buttons     ui.ButtonRow
	Query       string
	Index       int
	Items       []EntityListItem
	view        []EntityListItem
	focus       pickerDialogFocus
}

func (d *EntityListDialog) SetItems(items []EntityListItem) {
	d.Items = append(d.Items[:0], items...)
	d.refilter()
}

func (d *EntityListDialog) Current() (EntityListItem, bool) {
	if d.Index < 0 || d.Index >= len(d.view) {
		return EntityListItem{}, false
	}
	return d.view[d.Index], true
}

func (d *EntityListDialog) Update(msg ui.KeyMsg) EntityListDialogEvent {
	if d.Buttons.ActivateHotkey(msg) {
		return EntityListDialogEvent{ButtonID: d.currentButtonID()}
	}
	switch msg.String() {
	case "esc":
		return EntityListDialogEvent{Cancel: true}
	case "tab":
		d.focus = (d.focus + 1) % 2
	case "shift+tab":
		d.focus--
		if d.focus < 0 {
			d.focus = pickerDialogFocusButtons
		}
	case "enter":
		if d.focus == pickerDialogFocusButtons {
			d.Buttons.ActivateFocused()
			return EntityListDialogEvent{ButtonID: d.currentButtonID()}
		}
		if item, ok := d.Current(); ok {
			return EntityListDialogEvent{OpenID: item.ID}
		}
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
			d.Buttons.Move(-1)
		}
	case "right":
		if d.focus == pickerDialogFocusButtons {
			d.Buttons.Move(1)
		}
	case "backspace":
		if d.focus == pickerDialogFocusList && d.Query != "" {
			d.Query = d.Query[:len(d.Query)-1]
			d.refilter()
		}
	default:
		if d.focus == pickerDialogFocusList && msg.Type == ui.KeyRunes {
			d.Query += msg.String()
			d.refilter()
		}
	}
	return EntityListDialogEvent{}
}

func (d *EntityListDialog) ActivateControl(controlID string) EntityListDialogEvent {
	if controlID == "window-close" {
		return EntityListDialogEvent{Cancel: true}
	}
	for idx, button := range d.Buttons.Buttons {
		if button.ID == controlID {
			d.focus = pickerDialogFocusButtons
			d.Buttons.Index = idx
			return EntityListDialogEvent{ButtonID: controlID}
		}
	}
	if strings.HasPrefix(controlID, "entity-row-") {
		idxText := strings.TrimPrefix(controlID, "entity-row-")
		for idx, item := range d.view {
			if fmt.Sprintf("%d", idx) == idxText {
				d.Index = idx
				return EntityListDialogEvent{OpenID: item.ID}
			}
		}
	}
	return EntityListDialogEvent{}
}

func (d EntityListDialog) Node(width int, _ theme.Palette) ui.Node {
	dialogWidth := maxInt(90, minInt(width, 118))
	contentWidth := dialogWidth - 6
	var list ui.Node = staticBlock(blankAsDash(d.EmptyText))
	if len(d.view) > 0 {
		rows := make([]ui.TableRow, 0, len(d.view))
		for idx, item := range d.view {
			rows = append(rows, ui.TableRow{
				ControlID: "entity-row-" + fmt.Sprintf("%d", idx),
				Cells:     item.Cells,
				Selected:  idx == d.Index,
				Focused:   idx == d.Index && d.focus == pickerDialogFocusList,
			})
		}
		list = ui.AsNode(ui.Table{
			Width:      contentWidth,
			Columns:    d.Columns,
			Rows:       rows,
			ShowHeader: true,
		})
	}
	details := "No item selected"
	if item, ok := d.Current(); ok {
		details = blankAsDash(item.Details)
	}
	buttons := d.Buttons
	buttons.Width = contentWidth
	footer := d.FooterText
	if strings.TrimSpace(footer) == "" {
		footer = "Enter opens the selected item. Tab moves between list and buttons."
	}
	return ui.AsNode(ui.WindowFrame{
		Title: d.Title,
		Width: dialogWidth,
		Content: ui.AsNode(ui.FlexBox{
			Direction: ui.DirectionVertical,
			Children: []ui.Child{
				ui.Fixed(staticBlock(d.filterText())),
				ui.Fixed(ui.AsNode(ui.Section{Title: "Items", Width: contentWidth, Child: list})),
				ui.Fixed(ui.AsNode(ui.Section{Title: blankAsDash(d.DetailTitle), Width: contentWidth, Child: ui.AsNode(ui.TextPane{Content: details})})),
				ui.Fixed(ui.AsNode(buttons)),
				ui.Fixed(staticBlock(footer)),
			},
			Spacing: 1,
		}),
		ShowClose: true,
	})
}

func (d *EntityListDialog) move(delta int) {
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

func (d *EntityListDialog) refilter() {
	query := strings.ToLower(strings.TrimSpace(d.Query))
	d.view = d.view[:0]
	for _, item := range d.Items {
		search := strings.TrimSpace(item.Search)
		if search == "" {
			search = strings.Join(append([]string{item.ID}, item.Cells...), " ")
		}
		if query == "" || strings.Contains(strings.ToLower(search), query) {
			d.view = append(d.view, item)
		}
	}
	if d.Index >= len(d.view) {
		d.Index = maxInt(0, len(d.view)-1)
	}
}

func (d EntityListDialog) currentButtonID() string {
	if len(d.Buttons.Buttons) == 0 || d.Buttons.Index < 0 || d.Buttons.Index >= len(d.Buttons.Buttons) {
		return ""
	}
	return d.Buttons.Buttons[d.Buttons.Index].ID
}

func (d EntityListDialog) filterText() string {
	label := strings.TrimSpace(d.FilterLabel)
	if label == "" {
		label = "Filter"
	}
	return label + ": " + d.Query
}
