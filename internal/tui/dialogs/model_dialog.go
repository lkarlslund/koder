package dialogs

import (
	"strconv"
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/theme"
	"github.com/lkarlslund/koder/internal/ui"
)

type ModelDialogActionKind int

const (
	ModelDialogActionNone ModelDialogActionKind = iota
	ModelDialogActionSelect
	ModelDialogActionCancel
)

type ModelDialogAction struct {
	Kind       ModelDialogActionKind
	ProviderID string
	ModelID    string
	PresetID   string
}

type ModelDialogEntry struct {
	ProviderID    string
	ProviderLabel string
	Model         domain.Model
}

type ModelDialog struct {
	ui.PassiveNode
	Query    string
	Index    int
	PresetID string
	Entries  []ModelDialogEntry
	view     []ModelDialogEntry
	presets  []provider.ModelPreset
	focus    modelDialogFocus
	buttons  ui.ButtonRow
}

type modelDialogFocus int

const (
	modelDialogFocusList modelDialogFocus = iota
	modelDialogFocusPreset
	modelDialogFocusButtons
)

func NewModelDialog(entries []ModelDialogEntry, currentProviderID, currentModelID string, presetID string) ModelDialog {
	d := ModelDialog{
		Entries:  entries,
		PresetID: provider.NormalizePresetSelection(presetID),
		presets:  provider.Presets(),
	}
	d.buttons = ui.ButtonRow{
		Buttons: []ui.Button{
			{ID: "ok", Label: "OK", Hotkey: 'o', Primary: true},
			{ID: "cancel", Label: "Cancel", Hotkey: 'c'},
		},
		Align: ui.HorizontalAlignRight,
	}
	d.refilter()
	for idx, item := range d.view {
		if item.ProviderID == strings.TrimSpace(currentProviderID) && item.Model.ID == strings.TrimSpace(currentModelID) {
			d.Index = idx
			break
		}
	}
	return d
}

func (d *ModelDialog) Update(msg ui.KeyMsg) ModelDialogAction {
	d.ensureButtons()
	var action ModelDialogAction
	d.buttons.Buttons[0].OnClick = func() { action = d.selectCurrent() }
	d.buttons.Buttons[1].OnClick = func() { action = ModelDialogAction{Kind: ModelDialogActionCancel} }
	if d.buttons.ActivateHotkey(msg) {
		return action
	}
	switch msg.String() {
	case "esc":
		return ModelDialogAction{Kind: ModelDialogActionCancel}
	case "tab":
		d.focus = (d.focus + 1) % 3
	case "shift+tab":
		d.focus--
		if d.focus < 0 {
			d.focus = modelDialogFocusButtons
		}
	case "enter":
		if d.focus == modelDialogFocusButtons {
			d.buttons.ActivateFocused()
			return action
		}
		if d.focus == modelDialogFocusPreset {
			d.movePreset(1)
			return ModelDialogAction{}
		}
		return d.selectCurrent()
	case "up":
		if d.focus == modelDialogFocusList {
			d.move(-1)
		}
	case "down":
		if d.focus == modelDialogFocusList {
			d.move(1)
		}
	case "left":
		if d.focus == modelDialogFocusButtons {
			d.buttons.Move(-1)
		} else if d.focus == modelDialogFocusPreset {
			d.movePreset(-1)
		}
	case "right":
		if d.focus == modelDialogFocusButtons {
			d.buttons.Move(1)
		} else if d.focus == modelDialogFocusPreset {
			d.movePreset(1)
		}
	case "backspace", "alt+backspace":
		if d.focus == modelDialogFocusList && d.Query != "" {
			d.Query, _ = ui.DeleteBeforeCursorString(d.Query, len([]rune(d.Query)), msg.Alt)
			d.refilter()
		}
	default:
		if d.focus == modelDialogFocusList && msg.Type == ui.KeyRunes {
			d.Query += msg.String()
			d.refilter()
		}
	}
	return ModelDialogAction{}
}

func (d ModelDialog) Measure(ctx *ui.Context, constraints ui.Constraints) ui.Size {
	width := constraints.MaxW
	if width <= 0 {
		width = 84
	}
	return constraints.Clamp(d.dialog(width, ctx.Palette).Measure(ctx, ui.Constraints{MaxW: width, MaxH: constraints.MaxH}))
}

func (d ModelDialog) Surface(ctx *ui.Context, bounds ui.Rect) ui.Surface {
	maxWidth := dialogRenderWidth(bounds, 84)
	node := d.dialog(maxWidth, ctx.Palette)
	size := node.Measure(ctx, ui.Constraints{MaxW: maxWidth, MaxH: bounds.H})
	return ui.PaintNodeSurface(ctx, node, ui.Rect{W: size.W, H: bounds.H})
}

func (d ModelDialog) Paint(ctx *ui.Context, canvas ui.Canvas) {
	if canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	canvas.BlitSurface(0, 0, d.Surface(ctx, ui.Rect{W: canvas.Width(), H: canvas.Height()}))
}

func (d ModelDialog) dialog(width int, palette theme.Palette) ui.Node {
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
	providerWidth := minInt(18, maxInt(10, listWidth/5))
	secondaryWidth := maxInt(6, listWidth-primaryWidth-providerWidth-tertiaryWidth-6)
	rows := []ui.TableRow{}
	start, end := windowBounds(d.Index, len(d.view), 10)
	for idx := start; idx < end; idx++ {
		item := d.view[idx]
		rows = append(rows, ui.TableRow{
			ControlID: "model-row-" + strconv.Itoa(idx),
			Cells: []string{
				item.Model.ID,
				firstNonEmptyModelValue(strings.TrimSpace(item.ProviderLabel), strings.TrimSpace(item.ProviderID)),
				firstNonEmptyModelValue(strings.TrimSpace(item.Model.OwnedBy), strings.TrimSpace(item.ProviderID)),
				capabilityBadges(item.Model),
			},
			Selected: idx == d.Index,
			Focused:  idx == d.Index && d.focus == modelDialogFocusList,
		})
	}

	var list ui.Node
	if len(rows) == 0 {
		list = staticBlock("No matches")
	} else {
		list = ui.AsNode(ui.Table{
			Width: listWidth,
			Columns: []ui.TableColumn{
				{Title: "Model", Width: primaryWidth},
				{Title: "Provider", Width: providerWidth},
				{Title: "Owner", Width: secondaryWidth},
				{Title: "Caps", Width: tertiaryWidth, AlignRight: tertiaryWidth > 0},
			},
			ShowHeader: true,
			Rows:       rows,
		})
	}

	buttons := d.buttonRow(dialogWidth)
	buttons.Width = maxInt(0, dialogWidth-6)
	return ui.AsNode(ui.WindowFrame{
		Title: "Select Model",
		Width: dialogWidth,
		Content: ui.AsNode(ui.NewFlexBox(
			ui.DirectionVertical,
			[]ui.Child{
				ui.Fixed(ui.AsNode(ui.NewFlexBox(
					ui.DirectionVertical,
					[]ui.Child{
						ui.Fixed(staticBlock("Providers: all configured")),
						ui.Fixed(ui.Spacer{H: 1}),
						ui.Fixed(staticBlock("Filter: " + d.Query)),
						ui.Fixed(ui.Spacer{H: 1}),
						ui.Fixed(ui.AsNode(ui.Section{Width: listWidth, Child: list})),
						ui.Fixed(ui.ChoiceRow{
							Label:       "Preset",
							Description: "Request options",
							Value:       d.presetValue(),
							Width:       listWidth,
							Focused:     d.focus == modelDialogFocusPreset,
						}),
					},
					0,
				))),
				ui.Fixed(buttons),
				ui.Fixed(ui.Static{Content: "Tab moves focus. Left/Right changes preset. Enter selects model."}),
			},
			2,
		)),
		ShowClose: true,
	})
}

func anyModelHasCapabilities(models []ModelDialogEntry) bool {
	for _, model := range models {
		if strings.TrimSpace(capabilityBadges(model.Model)) != "" {
			return true
		}
	}
	return false
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
	d.buttons.Buttons[0].OnClick = func() { action = d.selectCurrent() }
	d.buttons.Buttons[1].OnClick = func() { action = ModelDialogAction{Kind: ModelDialogActionCancel} }
	switch controlID {
	case "ok", "cancel":
		d.focus = modelDialogFocusButtons
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
			d.focus = modelDialogFocusList
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
	for _, item := range d.Entries {
		haystack := strings.ToLower(item.Model.ID + " " + item.Model.OwnedBy + " " + item.ProviderID + " " + item.ProviderLabel)
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

func (d ModelDialog) current() (ModelDialogEntry, bool) {
	if len(d.view) == 0 || d.Index < 0 || d.Index >= len(d.view) {
		return ModelDialogEntry{}, false
	}
	return d.view[d.Index], true
}

func (d ModelDialog) selectCurrent() ModelDialogAction {
	item, ok := d.current()
	if !ok {
		return ModelDialogAction{Kind: ModelDialogActionCancel}
	}
	return ModelDialogAction{
		Kind:       ModelDialogActionSelect,
		ProviderID: item.ProviderID,
		ModelID:    item.Model.ID,
		PresetID:   provider.NormalizePresetSelection(d.PresetID),
	}
}

func (d *ModelDialog) ensureButtons() {
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

func (d ModelDialog) buttonRow(width int) ui.ButtonRow {
	buttons := d.buttons
	buttons.Width = maxInt(0, width)
	buttons.Align = ui.HorizontalAlignRight
	return buttons
}

func (d *ModelDialog) movePreset(delta int) {
	if len(d.presets) == 0 {
		d.PresetID = provider.ModelPresetAuto
		return
	}
	index := 0
	for idx, preset := range d.presets {
		if preset.ID == provider.NormalizePresetSelection(d.PresetID) {
			index = idx
			break
		}
	}
	index += delta
	if index < 0 {
		index = len(d.presets) - 1
	}
	if index >= len(d.presets) {
		index = 0
	}
	d.PresetID = d.presets[index].ID
}

func (d ModelDialog) presetValue() string {
	selected := provider.NormalizePresetSelection(d.PresetID)
	current, _ := d.current()
	resolved := provider.ResolvePresetID(current.Model.ID, selected)
	selectedPreset, _ := provider.LookupPreset(selected)
	resolvedPreset, _ := provider.LookupPreset(resolved)
	if selected == provider.ModelPresetAuto {
		return selectedPreset.Title + " -> " + resolvedPreset.Title
	}
	return resolvedPreset.Title
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
