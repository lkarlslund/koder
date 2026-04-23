package dialogs

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

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

func (d ToolsDialog) View(width int, palette theme.Palette) string {
	dialogWidth := width
	if dialogWidth <= 0 {
		dialogWidth = 88
	}
	dialogWidth = maxInt(72, dialogWidth)
	rowWidth := maxInt(56, dialogWidth-6)
	lines := make([]string, 0, len(d.items)+3)
	for idx, item := range d.items {
		lines = append(lines, CheckboxRow{
			Label:       item.Label,
			Description: item.Description,
			Checked:     item.Enabled,
			OnLabel:     "Enabled",
			OffLabel:    "Disabled",
		}.View(rowWidth, palette, d.focus == toolsDialogFocusList && idx == d.index))
	}
	body := lipgloss.JoinVertical(
		lipgloss.Left,
		strings.Join(lines, "\n"),
	)
	return Dialog{
		Title:    "Tools",
		Subtitle: "Per-session tool access. Space toggles the current tool.",
		Sections: []string{body},
		Buttons:  d.buttonRow(dialogWidth),
		Footer:   "Enter toggles a tool or activates the focused button. Esc cancels.",
		Width:    dialogWidth,
	}.View(palette)
}

func (d ToolsDialog) Measure(ctx *Context, constraints Constraints) Size {
	return dialogMeasure(ctx, constraints, 88, d.View)
}

func (d ToolsDialog) Render(ctx *Context, bounds Rect) Surface {
	return dialogRender(ctx, bounds, 88, d.View)
}

func (d *ToolsDialog) HandleMouse(localX, localY, width int, palette theme.Palette) ToolsDialogAction {
	var action ToolsDialogAction
	d.buttons.Buttons[0].OnPress = func() {
		action = ToolsDialogAction{Kind: ToolsDialogActionApply, States: d.States()}
	}
	d.buttons.Buttons[1].OnPress = func() {
		action = ToolsDialogAction{Kind: ToolsDialogActionCancel, States: d.originalStates()}
	}
	lines := strings.Split(d.View(width, palette), "\n")
	if localY < 0 || localY >= len(lines) {
		return ToolsDialogAction{}
	}
	line := ansi.Strip(lines[localY])
	buttons := d.buttonRow(width)
	if strings.Contains(line, "OK") && strings.Contains(line, "Cancel") {
		if start, ok := buttonRowOffset(line, buttons, palette); ok {
			d.focus = toolsDialogFocusButtons
			if idx, hit := buttons.IndexAtX(localX-start, palette); hit {
				d.buttons.Index = idx
				d.buttons.ActivateFocused()
				return action
			}
		}
	}
	for idx, item := range d.items {
		if !strings.Contains(line, item.Label) {
			continue
		}
		d.index = idx
		d.focus = toolsDialogFocusList
		d.toggleCurrent()
		return ToolsDialogAction{}
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
