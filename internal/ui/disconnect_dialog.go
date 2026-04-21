package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/lkarlslund/koder/internal/theme"
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
	buttons ButtonRow
}

func NewDisconnectDialog(items []ProviderItem) DisconnectDialog {
	d := DisconnectDialog{Items: items}
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

func (d *DisconnectDialog) Update(msg tea.KeyMsg) DisconnectDialogAction {
	d.ensureButtons()
	var action DisconnectDialogAction
	d.buttons.Buttons[0].OnPress = func() { action = d.selectCurrent() }
	d.buttons.Buttons[1].OnPress = func() { action = DisconnectDialogAction{Kind: DisconnectDialogActionCancel} }
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
	case "backspace":
		if d.focus == pickerDialogFocusList && d.Query != "" {
			d.Query = d.Query[:len(d.Query)-1]
			d.refilter()
		}
	default:
		if d.focus == pickerDialogFocusList && msg.Type == tea.KeyRunes {
			d.Query += msg.String()
			d.refilter()
		}
	}
	return DisconnectDialogAction{}
}

func (d DisconnectDialog) View(width int, palette theme.Palette) string {
	dialogWidth := width
	if dialogWidth <= 0 {
		dialogWidth = 84
	}
	dialogWidth = maxInt(72, dialogWidth)
	listWidth := 28
	detailWidth := maxInt(36, dialogWidth-listWidth-9)

	listLines := []string{}
	if len(d.view) == 0 {
		listLines = append(listLines, "No matches")
	} else {
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
			listLines = append(listLines, RenderSelectableRow(item.Title, item.Description, item.ID, listWidth, palette, idx == d.Index))
		}
	}

	details := "No provider selected"
	if item, ok := d.current(); ok {
		blocks := []string{
			lipgloss.NewStyle().Bold(true).Render(item.Title),
		}
		blocks = append(blocks, item.Details...)
		if desc := strings.TrimSpace(item.Description); desc != "" {
			blocks = append(blocks, "", truncateText(desc, detailWidth))
		}
		details = strings.Join(blocks, "\n")
	}

	body := lipgloss.JoinVertical(
		lipgloss.Left,
		fmt.Sprintf("Filter: %s", d.Query),
		"",
		lipgloss.JoinHorizontal(
			lipgloss.Top,
			lipgloss.NewStyle().Width(listWidth).BorderRight(true).BorderForeground(palette.SidebarBorder).PaddingRight(1).Render(strings.Join(listLines, "\n")),
			" ",
			lipgloss.NewStyle().Width(detailWidth).PaddingLeft(1).Render(details),
		),
		"",
		d.buttonRow(dialogWidth).View(palette),
	)

	return Modal{
		Title:  "Disconnect Provider",
		Body:   body,
		Footer: "Enter to disconnect, Esc to cancel",
		Width:  dialogWidth,
	}.View(palette)
}

func (d *DisconnectDialog) HandleMouse(localX, localY, width int, palette theme.Palette) DisconnectDialogAction {
	d.ensureButtons()
	var action DisconnectDialogAction
	d.buttons.Buttons[0].OnPress = func() { action = d.selectCurrent() }
	d.buttons.Buttons[1].OnPress = func() { action = DisconnectDialogAction{Kind: DisconnectDialogActionCancel} }
	lines := strings.Split(d.View(width, palette), "\n")
	if localY < 0 || localY >= len(lines) {
		return DisconnectDialogAction{}
	}
	line := ansi.Strip(lines[localY])
	buttons := d.buttonRow(width)
	if strings.Contains(line, "OK") && strings.Contains(line, "Cancel") {
		if start, ok := buttonRowOffset(line, buttons, palette); ok {
			d.focus = pickerDialogFocusButtons
			if idx, hit := buttons.IndexAtX(localX-start, palette); hit {
				d.buttons.Index = idx
				d.buttons.ActivateFocused()
				return action
			}
		}
	}
	for idx, item := range d.view {
		if strings.TrimSpace(item.Title) == "" {
			continue
		}
		if !strings.Contains(line, item.Title) {
			continue
		}
		d.Index = idx
		d.focus = pickerDialogFocusList
		return d.selectCurrent()
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
	d.buttons = ButtonRow{
		Buttons: []Button{
			{ID: "ok", Label: "OK", Hotkey: 'o', Primary: true},
			{ID: "cancel", Label: "Cancel", Hotkey: 'c'},
		},
		Align: HorizontalAlignRight,
	}
}

func (d DisconnectDialog) buttonRow(width int) ButtonRow {
	buttons := d.buttons
	buttons.Width = maxInt(0, width)
	buttons.Align = HorizontalAlignRight
	return buttons
}
