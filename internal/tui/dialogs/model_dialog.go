package dialogs

import (
	"strconv"
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
	dialogWidth := dialogRenderWidth(Rect{W: width}, 84)
	return RenderElement(&Context{Palette: palette}, d.dialog(dialogWidth, palette), dialogWidth, 0)
}

func (d ModelDialog) Measure(ctx *Context, constraints Constraints) Size {
	return dialogMeasureElement(ctx, constraints, 84, d.dialog)
}

func (d ModelDialog) Render(ctx *Context, bounds Rect) Surface {
	return dialogRenderElement(ctx, bounds, 84, d.dialog)
}

func (d ModelDialog) dialog(width int, palette theme.Palette) Element {
	dialogWidth := width
	if dialogWidth <= 0 {
		dialogWidth = 84
	}
	dialogWidth = maxInt(72, dialogWidth)
	listWidth := maxInt(40, dialogWidth-6)

	listChildren := []Child{}
	if len(d.view) == 0 {
		listChildren = append(listChildren, Fixed(staticBlock("No matches")))
	} else {
		start, end := windowBounds(d.Index, len(d.view), 10)
		for idx := start; idx < end; idx++ {
			item := d.view[idx]
			listChildren = append(listChildren, Fixed(HitBox{
				ID:    "model-row-" + strconv.Itoa(idx),
				Child: TextPane{Content: d.renderRow(item, listWidth, idx == d.Index, idx == d.Index && d.focus == pickerDialogFocusList, palette)},
			}))
		}
	}

	return Dialog{
		Title: "Select Model",
		Body: Column{
			Children: []Child{
				Fixed(staticBlock("Filter: " + d.Query)),
				Fixed(Spacer{H: 1}),
				Fixed(Panel{Width: listWidth, Child: Column{Children: listChildren}}),
			},
		},
		Buttons: d.buttonRow(dialogWidth),
		Footer:  "Enter to select, Esc to cancel",
		Width:   dialogWidth,
	}
}

func (d ModelDialog) renderRow(item domain.Model, width int, selected bool, focused bool, palette theme.Palette) string {
	if width <= 0 {
		width = 72
	}
	primary := compactModelCell(item.ID)
	secondary := compactModelCell(firstNonEmptyModelValue(strings.TrimSpace(item.OwnedBy), strings.TrimSpace(d.ProviderID)))
	tertiary := compactModelCell(capabilityBadges(item))
	primaryWidth := minInt(42, maxInt(20, width/2))
	tertiaryWidth := 0
	if strings.TrimSpace(tertiary) != "" {
		tertiaryWidth = minInt(12, maxInt(6, width/8))
	}
	gapWidth := 2
	secondaryWidth := maxInt(8, width-primaryWidth-tertiaryWidth-gapWidth*2)
	if tertiaryWidth == 0 {
		secondaryWidth = maxInt(8, width-primaryWidth-gapWidth)
	}
	selectionBackground := palette.SelectionBackground
	selectionForeground := palette.SelectionForeground
	if strings.TrimSpace(string(selectionBackground)) == "" {
		selectionBackground = palette.UserTextBackground
	}
	if strings.TrimSpace(string(selectionForeground)) == "" {
		selectionForeground = palette.UserTextForeground
	}
	primaryStyle := lipgloss.NewStyle().Width(primaryWidth).Bold(true)
	gapStyle := lipgloss.NewStyle().Width(gapWidth)
	secondaryStyle := lipgloss.NewStyle().Width(secondaryWidth).Foreground(palette.AssistantTimestampText)
	tertiaryStyle := lipgloss.NewStyle().Width(tertiaryWidth).Align(lipgloss.Right).Foreground(palette.ActivityText)
	rowStyle := lipgloss.NewStyle().Width(width)
	if selected {
		rowStyle = rowStyle.Background(selectionBackground).Foreground(selectionForeground)
		primaryStyle = primaryStyle.Background(selectionBackground).Foreground(selectionForeground)
		gapStyle = gapStyle.Background(selectionBackground)
		secondaryStyle = secondaryStyle.Background(selectionBackground).Foreground(selectionForeground)
		tertiaryStyle = tertiaryStyle.Background(selectionBackground).Foreground(selectionForeground).Bold(true)
	}
	if focused {
		focusedBackground := selectionBackground
		if strings.TrimSpace(string(palette.UserTextBackground)) != "" {
			focusedBackground = palette.UserTextBackground
		}
		focusedForeground := selectionForeground
		rowStyle = rowStyle.Background(focusedBackground).Foreground(focusedForeground)
		primaryStyle = primaryStyle.Background(focusedBackground).Foreground(focusedForeground)
		gapStyle = gapStyle.Background(focusedBackground)
		secondaryStyle = secondaryStyle.Background(focusedBackground).Foreground(focusedForeground)
		tertiaryStyle = tertiaryStyle.Background(focusedBackground).Foreground(focusedForeground).Bold(true)
	}
	row := lipgloss.JoinHorizontal(
		lipgloss.Top,
		primaryStyle.Render(truncateModelCell(strings.TrimSpace(primary), primaryWidth)),
		gapStyle.Render(""),
		secondaryStyle.Render(truncateModelCell(strings.TrimSpace(secondary), secondaryWidth)),
	)
	if tertiaryWidth > 0 {
		row = lipgloss.JoinHorizontal(
			lipgloss.Top,
			row,
			gapStyle.Render(""),
			tertiaryStyle.Render(truncateModelCell(strings.TrimSpace(tertiary), tertiaryWidth)),
		)
	}
	return rowStyle.Render(row)
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
	if ansi.StringWidth(value) <= width {
		return value
	}
	return ansi.Truncate(value, width, "…")
}

func firstNonEmptyModelValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (d *ModelDialog) HandleMouse(localX, localY, width int, palette theme.Palette) ModelDialogAction {
	d.ensureButtons()
	var action ModelDialogAction
	d.buttons.Buttons[0].OnPress = func() { action = d.selectCurrent() }
	d.buttons.Buttons[1].OnPress = func() { action = ModelDialogAction{Kind: ModelDialogActionCancel} }
	controlID, ok := dialogHitControl(width, palette, d.dialog, localX, localY)
	if !ok {
		return ModelDialogAction{}
	}
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
