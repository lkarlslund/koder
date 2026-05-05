package dialogs

import (
	"strings"

	"github.com/lkarlslund/koder/internal/theme"
	"github.com/lkarlslund/koder/internal/ui"
)

type ProviderDialogActionKind int

const (
	ProviderDialogActionNone ProviderDialogActionKind = iota
	ProviderDialogActionAdd
	ProviderDialogActionEdit
	ProviderDialogActionDelete
	ProviderDialogActionCancel
)

type ProviderDialogAction struct {
	Kind       ProviderDialogActionKind
	ProviderID string
}

type ProviderDialog struct {
	ui.PassiveNode
	list EntityListDialog
}

func NewProviderDialog(items []EntityListItem) ProviderDialog {
	dialog := ProviderDialog{
		list: EntityListDialog{
			Title:       "Providers",
			FilterLabel: "Filter",
			EmptyText:   "No providers configured.",
			DetailTitle: "Details",
			FooterText:  "Enter edits the selected provider. Tab moves to the button row.",
			Columns: []ui.TableColumn{
				{Title: "Name", Width: 18},
				{Title: "Status", Width: 10},
				{Title: "Models", Width: 8, AlignRight: true},
				{Title: "Type", Width: 18},
				{Title: "Remote", Width: 26},
			},
			Buttons: ui.ButtonRow{
				Buttons: []ui.Button{
					{ID: "add", Label: "Add", Hotkey: 'a', Primary: true},
					{ID: "edit", Label: "Edit", Hotkey: 'e'},
					{ID: "delete", Label: "Delete", Hotkey: 'd'},
					{ID: "cancel", Label: "Close", Hotkey: 'c'},
				},
				Align: ui.HorizontalAlignRight,
			},
		},
	}
	dialog.list.SetItems(items)
	return dialog
}

func (d *ProviderDialog) SetItems(items []EntityListItem) {
	d.list.SetItems(items)
}

func (d *ProviderDialog) Update(msg ui.KeyMsg) ProviderDialogAction {
	event := d.list.Update(msg)
	return d.eventToAction(event)
}

func (d *ProviderDialog) ActivateControl(controlID string) ProviderDialogAction {
	return d.eventToAction(d.list.ActivateControl(controlID))
}

func (d ProviderDialog) Measure(ctx *ui.Context, constraints ui.Constraints) ui.Size {
	width := constraints.MaxW
	if width <= 0 {
		width = 110
	}
	return constraints.Clamp(d.Node(width, ctx.Palette).Measure(ctx, ui.Constraints{MaxW: width, MaxH: constraints.MaxH}))
}

func (d ProviderDialog) Surface(ctx *ui.Context, bounds ui.Rect) ui.Surface {
	maxWidth := dialogRenderWidth(bounds, 110)
	node := d.Node(maxWidth, ctx.Palette)
	size := node.Measure(ctx, ui.Constraints{MaxW: maxWidth, MaxH: bounds.H})
	return ui.PaintNodeSurface(ctx, node, ui.Rect{W: size.W, H: bounds.H})
}

func (d ProviderDialog) Paint(ctx *ui.Context, canvas ui.Canvas) {
	if canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	canvas.BlitSurface(0, 0, d.Surface(ctx, ui.Rect{W: canvas.Width(), H: canvas.Height()}))
}

func (d ProviderDialog) Node(width int, palette theme.Palette) ui.Node {
	return d.list.Node(width, palette)
}

func (d ProviderDialog) eventToAction(event EntityListDialogEvent) ProviderDialogAction {
	if event.Cancel {
		return ProviderDialogAction{Kind: ProviderDialogActionCancel}
	}
	if strings.TrimSpace(event.OpenID) != "" {
		return ProviderDialogAction{Kind: ProviderDialogActionEdit, ProviderID: event.OpenID}
	}
	switch event.ButtonID {
	case "add":
		return ProviderDialogAction{Kind: ProviderDialogActionAdd}
	case "edit":
		item, ok := d.list.Current()
		if !ok {
			return ProviderDialogAction{}
		}
		return ProviderDialogAction{Kind: ProviderDialogActionEdit, ProviderID: item.ID}
	case "delete":
		item, ok := d.list.Current()
		if !ok {
			return ProviderDialogAction{}
		}
		return ProviderDialogAction{Kind: ProviderDialogActionDelete, ProviderID: item.ID}
	case "cancel":
		return ProviderDialogAction{Kind: ProviderDialogActionCancel}
	default:
		return ProviderDialogAction{}
	}
}
