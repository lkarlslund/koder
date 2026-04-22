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

type ModelDialogActionKind int

const (
	ModelDialogActionNone ModelDialogActionKind = iota
	ModelDialogActionSelect
	ModelDialogActionCancel
)

type ModelDialogAction struct {
	Kind    ModelDialogActionKind
	ModelID string
}

type ModelDialog struct {
	ProviderID string
	Query      string
	Index      int
	Models     []domain.Model
	view       []domain.Model
	focus      pickerDialogFocus
	buttons    ButtonRow
}

func NewModelDialog(providerID string, models []domain.Model, current string) ModelDialog {
	d := ModelDialog{
		ProviderID: providerID,
		Models:     models,
	}
	d.buttons = ButtonRow{
		Buttons: []Button{
			{ID: "ok", Label: "OK", Hotkey: 'o', Primary: true},
			{ID: "cancel", Label: "Cancel", Hotkey: 'c'},
		},
		Align: HorizontalAlignRight,
	}
	d.refilter()
	for idx, item := range d.view {
		if item.ID == strings.TrimSpace(current) {
			d.Index = idx
			break
		}
	}
	return d
}

func (d *ModelDialog) Update(msg tea.KeyMsg) ModelDialogAction {
	d.ensureButtons()
	var action ModelDialogAction
	d.buttons.Buttons[0].OnPress = func() { action = d.selectCurrent() }
	d.buttons.Buttons[1].OnPress = func() { action = ModelDialogAction{Kind: ModelDialogActionCancel} }
	if d.buttons.ActivateHotkey(msg) {
		return action
	}
	switch msg.String() {
	case "esc":
		return ModelDialogAction{Kind: ModelDialogActionCancel}
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
	return ModelDialogAction{}
}

func (d ModelDialog) View(width int, palette theme.Palette) string {
	dialogWidth := width
	if dialogWidth <= 0 {
		dialogWidth = 84
	}
	dialogWidth = maxInt(72, dialogWidth)
	listWidth := maxInt(40, dialogWidth-4)

	listLines := []string{}
	if len(d.view) == 0 {
		listLines = append(listLines, "No matches")
	} else {
		start := 0
		if d.Index >= 5 {
			start = d.Index - 4
		}
		end := len(d.view)
		if end > start+10 {
			end = start + 10
		}
		for idx := start; idx < end; idx++ {
			item := d.view[idx]
			listLines = append(listLines, SelectableRow{
				Primary:   item.ID,
				Secondary: item.OwnedBy,
				Tertiary:  capabilityBadges(item),
				Width:     listWidth,
				Selected:  idx == d.Index,
				Focused:   idx == d.Index && d.focus == pickerDialogFocusList,
			}.View(palette))
		}
	}

	body := lipgloss.JoinVertical(
		lipgloss.Left,
		"Filter: "+d.Query,
		"",
		lipgloss.NewStyle().Width(listWidth).Render(strings.Join(listLines, "\n")),
	)

	return Dialog{
		Title: "Select Model",
		Sections: []string{body},
		Buttons: d.buttonRow(dialogWidth),
		Footer: "Enter to select, Esc to cancel",
		Width:  dialogWidth,
	}.View(palette)
}

func (d *ModelDialog) HandleMouse(localX, localY, width int, palette theme.Palette) ModelDialogAction {
	d.ensureButtons()
	var action ModelDialogAction
	d.buttons.Buttons[0].OnPress = func() { action = d.selectCurrent() }
	d.buttons.Buttons[1].OnPress = func() { action = ModelDialogAction{Kind: ModelDialogActionCancel} }
	lines := strings.Split(d.View(width, palette), "\n")
	if localY < 0 || localY >= len(lines) {
		return ModelDialogAction{}
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
		if strings.TrimSpace(item.ID) == "" {
			continue
		}
		if !strings.Contains(line, item.ID) {
			continue
		}
		d.Index = idx
		d.focus = pickerDialogFocusList
		return d.selectCurrent()
	}
	return ModelDialogAction{}
}

func (d *ModelDialog) move(delta int) {
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

func (d *ModelDialog) refilter() {
	query := strings.ToLower(strings.TrimSpace(d.Query))
	d.view = d.view[:0]
	for _, item := range d.Models {
		haystack := strings.ToLower(item.ID + " " + item.OwnedBy)
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

func (d ModelDialog) current() (domain.Model, bool) {
	if len(d.view) == 0 || d.Index < 0 || d.Index >= len(d.view) {
		return domain.Model{}, false
	}
	return d.view[d.Index], true
}

func (d ModelDialog) selectCurrent() ModelDialogAction {
	item, ok := d.current()
	if !ok {
		return ModelDialogAction{Kind: ModelDialogActionCancel}
	}
	return ModelDialogAction{Kind: ModelDialogActionSelect, ModelID: item.ID}
}

func (d *ModelDialog) ensureButtons() {
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

func (d ModelDialog) buttonRow(width int) ButtonRow {
	buttons := d.buttons
	buttons.Width = maxInt(0, width)
	buttons.Align = HorizontalAlignRight
	return buttons
}

func capabilityBadges(model domain.Model) string {
	var badges []string
	if model.SupportsImages {
		badges = append(badges, "image")
	}
	if model.SupportsPDFs {
		badges = append(badges, "pdf")
	}
	if len(badges) == 0 && model.CapabilitiesKnown {
		badges = append(badges, "text")
	}
	return strings.Join(badges, ", ")
}
