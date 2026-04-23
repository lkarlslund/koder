package dialogs

import (
	"strconv"
	"strings"

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

func (d *ModelDialog) Update(msg KeyMsg) ModelDialogAction {
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
		if d.focus == pickerDialogFocusList && msg.Type == KeyRunes {
			d.Query += msg.String()
			d.refilter()
		}
	}
	return ModelDialogAction{}
}

func (d ModelDialog) Measure(ctx *Context, constraints Constraints) Size {
	width := constraints.MaxW
	if width <= 0 {
		width = 84
	}
	return constraints.Clamp(d.dialog(width, ctx.Palette).Measure(ctx, Constraints{MaxW: width, MaxH: constraints.MaxH}))
}

func (d ModelDialog) Render(ctx *Context, bounds Rect) Surface {
	maxWidth := dialogRenderWidth(bounds, 84)
	element := d.dialog(maxWidth, ctx.Palette)
	size := element.Measure(ctx, Constraints{MaxW: maxWidth, MaxH: bounds.H})
	return element.Render(ctx, Rect{X: bounds.X, Y: bounds.Y, W: size.W, H: bounds.H})
}

func (d ModelDialog) dialog(width int, palette theme.Palette) Element {
	dialogWidth := width
	if dialogWidth <= 0 {
		dialogWidth = 72
	}
	dialogWidth = minInt(76, maxInt(64, dialogWidth))
	listWidth := maxInt(34, dialogWidth-6)
	primaryWidth := minInt(40, maxInt(18, listWidth/2))
	tertiaryWidth := 0
	if anyModelHasCapabilities(d.view) {
		tertiaryWidth = minInt(10, maxInt(5, listWidth/8))
	}
	secondaryWidth := maxInt(6, listWidth-primaryWidth-tertiaryWidth-4)
	rows := []TableRow{}
	start, end := windowBounds(d.Index, len(d.view), 10)
	for idx := start; idx < end; idx++ {
		item := d.view[idx]
		rows = append(rows, TableRow{
			ControlID: "model-row-" + strconv.Itoa(idx),
			Cells: []string{
				item.ID,
				firstNonEmptyModelValue(strings.TrimSpace(item.OwnedBy), strings.TrimSpace(d.ProviderID)),
				capabilityBadges(item),
			},
			Selected: idx == d.Index,
			Focused:  idx == d.Index && d.focus == pickerDialogFocusList,
		})
	}

	var list Element
	if len(rows) == 0 {
		list = staticBlock("No matches")
	} else {
		list = Table{
			Width: listWidth,
			Columns: []TableColumn{
				{Title: "Model", Width: primaryWidth},
				{Title: "Owner", Width: secondaryWidth},
				{Title: "Caps", Width: tertiaryWidth, AlignRight: tertiaryWidth > 0},
			},
			ShowHeader: true,
			Rows:       rows,
		}
	}

	return Dialog{
		Title: "Select Model",
		Body: Column{
			Children: []Child{
				Fixed(staticBlock("Filter: " + d.Query)),
				Fixed(Spacer{H: 1}),
				Fixed(Section{Width: listWidth, Child: list}),
			},
		},
		Buttons: d.buttonRow(dialogWidth),
		Footer:  "Enter to select, Esc to cancel",
		Width:   dialogWidth,
	}
}

func anyModelHasCapabilities(models []domain.Model) bool {
	for _, model := range models {
		if strings.TrimSpace(capabilityBadges(model)) != "" {
			return true
		}
	}
	return false
}

func compactModelCell(value string) string {
	value = strings.ReplaceAll(value, "\n", " ")
	return strings.Join(strings.Fields(value), " ")
}

func truncateModelCell(value string, width int) string {
	value = strings.TrimSpace(value)
	if width <= 0 {
		return ""
	}
	if PlainWidth(value) <= width {
		return value
	}
	return PlainTruncate(value, width, "…")
}

func firstNonEmptyModelValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (d *ModelDialog) ActivateControl(controlID string) ModelDialogAction {
	d.ensureButtons()
	var action ModelDialogAction
	d.buttons.Buttons[0].OnPress = func() { action = d.selectCurrent() }
	d.buttons.Buttons[1].OnPress = func() { action = ModelDialogAction{Kind: ModelDialogActionCancel} }
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
		if strings.HasPrefix(controlID, "model-row-") {
			idx, err := strconv.Atoi(strings.TrimPrefix(controlID, "model-row-"))
			if err != nil || idx < 0 || idx >= len(d.view) {
				return ModelDialogAction{}
			}
			d.Index = idx
			d.focus = pickerDialogFocusList
			return d.selectCurrent()
		}
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
