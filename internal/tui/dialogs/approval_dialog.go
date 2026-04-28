package dialogs

import (
	"strings"

	"github.com/lkarlslund/koder/internal/theme"
	"github.com/lkarlslund/koder/internal/ui"
)

type ApprovalDialogActionKind int

const (
	ApprovalDialogActionNone ApprovalDialogActionKind = iota
	ApprovalDialogActionApproveOnce
	ApprovalDialogActionApproveAllTool
	ApprovalDialogActionApproveMatching
	ApprovalDialogActionDeny
	ApprovalDialogActionPermissions
)

type ApprovalDialogAction struct {
	Kind ApprovalDialogActionKind
}

type ApprovalDialog struct {
	run           ui.ToolRun
	toolLabel     string
	matchingLabel string
	buttons       ui.ButtonRow
}

func NewApprovalDialog(run ui.ToolRun, toolLabel, matchingLabel string) *ApprovalDialog {
	buttons := []ui.Button{
		{ID: "approve_once", Label: "Approve this time", Hotkey: 't', Primary: true},
		{ID: "approve_all_tool", Label: "Approve all " + strings.TrimSpace(toolLabel) + " commands", Hotkey: 'a'},
		{ID: "approve_matching", Label: "Approve all " + strings.TrimSpace(toolLabel) + " " + strings.TrimSpace(matchingLabel) + " commands", Hotkey: 'm'},
		{ID: "deny", Label: "Deny", Hotkey: 'd'},
		{ID: "permissions", Label: "Switch permissions model", Hotkey: 's'},
	}
	if strings.TrimSpace(toolLabel) == "" {
		buttons[1].Label = "Approve all tool commands"
	}
	if strings.TrimSpace(toolLabel) == "" || strings.TrimSpace(matchingLabel) == "" {
		buttons[2].Label = "Approve matching tool commands"
	}
	return &ApprovalDialog{
		run:           run,
		toolLabel:     toolLabel,
		matchingLabel: matchingLabel,
		buttons: ui.ButtonRow{
			Buttons: buttons,
			Align:   ui.HorizontalAlignCenter,
		},
	}
}

func (d *ApprovalDialog) ButtonIndex() int {
	if d == nil {
		return 0
	}
	return d.buttons.Index
}

func (d *ApprovalDialog) SetButtonIndex(index int) {
	if d == nil {
		return
	}
	if index < 0 {
		index = 0
	}
	if index >= len(d.buttons.Buttons) {
		index = len(d.buttons.Buttons) - 1
	}
	if index < 0 {
		index = 0
	}
	d.buttons.Index = index
}

func (d *ApprovalDialog) Update(msg ui.KeyMsg) ApprovalDialogAction {
	if d == nil {
		return ApprovalDialogAction{}
	}
	if idx, ok := d.buttons.HotkeyIndex(msg); ok {
		return d.activateButton(idx)
	}
	switch msg.String() {
	case "esc":
		return ApprovalDialogAction{Kind: ApprovalDialogActionDeny}
	case "left", "up", "shift+tab":
		d.buttons.Move(-1)
		return ApprovalDialogAction{}
	case "right", "down", "tab":
		d.buttons.Move(1)
		return ApprovalDialogAction{}
	case "enter", " ":
		return d.activateButton(d.buttons.Index)
	default:
		return ApprovalDialogAction{}
	}
}

func (d *ApprovalDialog) ActivateControl(id string) ApprovalDialogAction {
	if d == nil {
		return ApprovalDialogAction{}
	}
	switch id {
	case "window-close":
		return ApprovalDialogAction{Kind: ApprovalDialogActionDeny}
	case "approve_once":
		return ApprovalDialogAction{Kind: ApprovalDialogActionApproveOnce}
	case "approve_all_tool":
		return ApprovalDialogAction{Kind: ApprovalDialogActionApproveAllTool}
	case "approve_matching":
		return ApprovalDialogAction{Kind: ApprovalDialogActionApproveMatching}
	case "deny":
		return ApprovalDialogAction{Kind: ApprovalDialogActionDeny}
	case "permissions":
		return ApprovalDialogAction{Kind: ApprovalDialogActionPermissions}
	default:
		return ApprovalDialogAction{}
	}
}

func (d *ApprovalDialog) activateButton(index int) ApprovalDialogAction {
	if d == nil || index < 0 || index >= len(d.buttons.Buttons) {
		return ApprovalDialogAction{}
	}
	return d.ActivateControl(d.buttons.Buttons[index].ID)
}

func (d *ApprovalDialog) Element(palette theme.Palette, bounds ui.Rect) ui.Element {
	if d == nil {
		return nil
	}
	width := dialogRenderWidth(bounds, 112)
	cardWidth := maxInt(48, width-8)
	card := strings.Join(d.run.CardSurface(palette, cardWidth, true, true).Lines(), "\n")
	buttons := d.buttons
	buttons.Width = maxInt(buttons.Width, ui.PlainWidth(card))
	body := ui.FlexBox{
		Direction: ui.DirectionVertical,
		Children: []ui.Child{
			ui.Fixed(ui.Static{Content: card}),
			ui.Fixed(ui.Spacer{H: 1}),
			ui.Fixed(buttons),
		},
	}
	return ui.ModalFrame{
		Title:    "Approval required",
		Subtitle: "Choose how to handle this tool request",
		Body:     body,
		Footer:   "enter select  tab/arrow switch  esc deny  alt+hotkey shortcut",
		Width:    maxInt(88, minInt(width, 132)),
	}
}
