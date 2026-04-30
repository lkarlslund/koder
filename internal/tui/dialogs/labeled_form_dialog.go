package dialogs

import (
	"strings"

	"github.com/lkarlslund/koder/internal/theme"
	"github.com/lkarlslund/koder/internal/ui"
	"github.com/lkarlslund/koder/internal/ui/textarea"
)

type LabeledFormFieldKind int

const (
	LabeledFormFieldText LabeledFormFieldKind = iota
	LabeledFormFieldSecret
	LabeledFormFieldToggle
)

type LabeledFormField struct {
	ID          string
	Label       string
	Description string
	Placeholder string
	Kind        LabeledFormFieldKind
}

type LabeledFormEvent struct {
	ButtonID string
	Cancel   bool
}

type labeledFormFocus int

const (
	labeledFormFocusFields labeledFormFocus = iota
	labeledFormFocusButtons
)

type LabeledFormDialog struct {
	Title      string
	FooterText string
	Status     string
	Fields     []LabeledFormField
	Buttons    ui.ButtonRow
	focus      labeledFormFocus
	fieldIndex int
	editors    map[string]textarea.Model
	toggles    map[string]bool
}

func NewLabeledFormDialog(title string, fields []LabeledFormField, buttons []ui.Button) LabeledFormDialog {
	btns := ui.ButtonRow{Buttons: buttons, Align: ui.HorizontalAlignRight}
	return LabeledFormDialog{
		Title:   title,
		Fields:  append([]LabeledFormField(nil), fields...),
		Buttons: btns,
		editors: map[string]textarea.Model{},
		toggles: map[string]bool{},
	}
}

func (d *LabeledFormDialog) SetStatus(status string) {
	d.Status = strings.TrimSpace(status)
}

func (d *LabeledFormDialog) SetValue(id, value string) {
	editor := d.editor(id)
	editor.SetValue(value)
	d.storeEditor(id, editor)
}

func (d *LabeledFormDialog) SetToggle(id string, value bool) {
	if d.toggles == nil {
		d.toggles = map[string]bool{}
	}
	d.toggles[id] = value
}

func (d LabeledFormDialog) Value(id string) string {
	editor := d.editor(id)
	return editor.Value()
}

func (d LabeledFormDialog) Toggle(id string) bool {
	return d.toggles[id]
}

func (d *LabeledFormDialog) Update(msg ui.KeyMsg) LabeledFormEvent {
	if d.Buttons.ActivateHotkey(msg) {
		return LabeledFormEvent{ButtonID: d.currentButtonID()}
	}
	switch msg.String() {
	case "esc":
		return LabeledFormEvent{Cancel: true}
	case "tab":
		if d.focus == labeledFormFocusFields {
			d.focus = labeledFormFocusButtons
		} else {
			d.focus = labeledFormFocusFields
		}
	case "shift+tab":
		if d.focus == labeledFormFocusButtons {
			d.focus = labeledFormFocusFields
		} else {
			d.focus = labeledFormFocusButtons
		}
	case "up":
		if d.focus == labeledFormFocusFields && d.fieldIndex > 0 {
			d.fieldIndex--
		}
	case "down":
		if d.focus == labeledFormFocusFields && d.fieldIndex < len(d.Fields)-1 {
			d.fieldIndex++
		}
	case "left":
		if d.focus == labeledFormFocusButtons {
			d.Buttons.Move(-1)
		} else {
			d.toggleCurrentField()
		}
	case "right":
		if d.focus == labeledFormFocusButtons {
			d.Buttons.Move(1)
		} else {
			d.toggleCurrentField()
		}
	case "enter":
		if d.focus == labeledFormFocusButtons {
			d.Buttons.ActivateFocused()
			return LabeledFormEvent{ButtonID: d.currentButtonID()}
		}
		d.toggleCurrentField()
	default:
		if d.focus == labeledFormFocusFields {
			d.updateCurrentEditor(msg)
		}
	}
	return LabeledFormEvent{}
}

func (d *LabeledFormDialog) ActivateControl(controlID string) LabeledFormEvent {
	if controlID == "window-close" {
		return LabeledFormEvent{Cancel: true}
	}
	for idx, button := range d.Buttons.Buttons {
		if button.ID == controlID {
			d.focus = labeledFormFocusButtons
			d.Buttons.Index = idx
			return LabeledFormEvent{ButtonID: controlID}
		}
	}
	return LabeledFormEvent{}
}

func (d LabeledFormDialog) Node(width int, palette theme.Palette) ui.Node {
	dialogWidth := maxInt(78, minInt(width, 112))
	contentWidth := dialogWidth - 6
	buttons := d.Buttons
	buttons.Width = contentWidth
	status := d.Status
	if status == "" {
		status = d.FooterText
	}
	return ui.AsNode(ui.WindowFrame{
		Title: d.Title,
		Width: dialogWidth,
		Content: ui.AsNode(ui.NewFlexBox(
			ui.DirectionVertical,
			[]ui.Child{
				ui.Fixed(d.renderFields(contentWidth, palette)),
				ui.Fixed(ui.AsNode(buttons)),
				ui.Fixed(staticBlock(blankAsDash(status))),
			},
			1,
		)),
		ShowClose: true,
	})
}

func (d LabeledFormDialog) renderFields(width int, palette theme.Palette) ui.Node {
	labelWidth := 0
	for _, field := range d.Fields {
		labelWidth = maxInt(labelWidth, ui.PlainWidth(field.Label))
	}
	labelWidth = maxInt(12, minInt(22, labelWidth))
	rows := make([]ui.Child, 0, len(d.Fields)*2)
	for idx, field := range d.Fields {
		active := d.focus == labeledFormFocusFields && idx == d.fieldIndex
		rows = append(rows, ui.Fixed(d.renderField(field, labelWidth, width, palette, active)))
		if strings.TrimSpace(field.Description) != "" {
			rows = append(rows, ui.Fixed(ui.Static{Content: strings.Repeat(" ", labelWidth+2) + truncateText(field.Description, maxInt(16, width-labelWidth-2))}))
		}
	}
	return ui.AsNode(ui.Section{Width: width, Child: ui.AsNode(ui.NewFlexBox(ui.DirectionVertical, rows, 1))})
}

func (d LabeledFormDialog) renderField(field LabeledFormField, labelWidth int, width int, palette theme.Palette, active bool) ui.Node {
	rightWidth := maxInt(18, width-labelWidth-2)
	label := ui.Fixed(ui.Static{Content: padRight(field.Label, labelWidth)})
	switch field.Kind {
	case LabeledFormFieldToggle:
		value := "disabled"
		if d.Toggle(field.ID) {
			value = "enabled"
		}
		if strings.EqualFold(field.ID, "disable_sse") || strings.EqualFold(field.ID, "disabled") {
			value = yesNo(d.Toggle(field.ID))
		}
		valueNode := ui.AsNode(ui.InputField{
			Width:         rightWidth,
			Value:         value,
			CursorVisible: false,
			Foreground:    palette.MarkdownText,
			Background:    palette.ScreenBackground,
			PlaceholderFG: palette.ComposerMutedText,
			BorderColor:   chooseBorderColor(palette, active),
		})
		return ui.AsNode(ui.NewFlexBox(ui.DirectionHorizontal, []ui.Child{label, ui.Fixed(valueNode)}, 1))
	default:
		editor := d.editor(field.ID)
		line := editor.VisibleLine()
		before, cursor, after := line.Before(), line.Cursor(), line.After()
		value := editor.Value()
		if field.Kind == LabeledFormFieldSecret {
			before = maskFormVisible(before)
			cursor = maskFormVisible(cursor)
			after = maskFormVisible(after)
			if strings.TrimSpace(value) != "" {
				value = maskFormVisible(value)
			}
		}
		input := ui.AsNode(ui.InputField{
			Width:         rightWidth,
			Value:         value,
			Placeholder:   field.Placeholder,
			ContentBefore: before,
			ContentCursor: cursor,
			ContentAfter:  after,
			CursorVisible: active && editor.CursorVisible(),
			Foreground:    chooseForegroundColor(palette, active),
			Background:    chooseBackgroundColor(palette, active),
			PlaceholderFG: palette.ComposerMutedText,
			BorderColor:   chooseBorderColor(palette, active),
		})
		return ui.AsNode(ui.NewFlexBox(ui.DirectionHorizontal, []ui.Child{label, ui.Fixed(input)}, 1))
	}
}

func (d *LabeledFormDialog) updateCurrentEditor(msg ui.KeyMsg) {
	if d.focus != labeledFormFocusFields || d.fieldIndex < 0 || d.fieldIndex >= len(d.Fields) {
		return
	}
	field := d.Fields[d.fieldIndex]
	if field.Kind == LabeledFormFieldToggle {
		return
	}
	editor := d.editor(field.ID)
	updated, _ := editor.Update(msg)
	d.storeEditor(field.ID, updated)
}

func (d *LabeledFormDialog) toggleCurrentField() {
	if d.focus != labeledFormFocusFields || d.fieldIndex < 0 || d.fieldIndex >= len(d.Fields) {
		return
	}
	field := d.Fields[d.fieldIndex]
	if field.Kind != LabeledFormFieldToggle {
		return
	}
	d.SetToggle(field.ID, !d.Toggle(field.ID))
}

func (d LabeledFormDialog) editor(id string) textarea.Model {
	if d.editors == nil {
		return d.newEditor("")
	}
	editor, ok := d.editors[id]
	if !ok {
		return d.newEditor("")
	}
	return editor
}

func (d *LabeledFormDialog) storeEditor(id string, editor textarea.Model) {
	if d.editors == nil {
		d.editors = map[string]textarea.Model{}
	}
	d.editors[id] = editor
}

func (d LabeledFormDialog) newEditor(value string) textarea.Model {
	editor := textarea.New()
	editor.BlinkEnabled = false
	editor.Focus()
	editor.SetHeight(1)
	editor.SetWidth(256)
	editor.SetValue(value)
	return editor
}

func (d LabeledFormDialog) currentButtonID() string {
	if len(d.Buttons.Buttons) == 0 || d.Buttons.Index < 0 || d.Buttons.Index >= len(d.Buttons.Buttons) {
		return ""
	}
	return d.Buttons.Buttons[d.Buttons.Index].ID
}

func chooseForegroundColor(palette theme.Palette, active bool) ui.CellColor {
	if active {
		return palette.UserTextForeground
	}
	return palette.MarkdownText
}

func chooseBackgroundColor(palette theme.Palette, active bool) ui.CellColor {
	if active {
		return palette.UserTextBackground
	}
	return palette.ScreenBackground
}

func chooseBorderColor(palette theme.Palette, active bool) ui.CellColor {
	if active {
		return firstNonEmptyColor(palette.SelectionBackground, palette.ActivityText, palette.SidebarBorder)
	}
	return palette.SidebarBorder
}

func padRight(input string, width int) string {
	padding := width - ui.PlainWidth(input)
	if padding <= 0 {
		return input
	}
	return input + strings.Repeat(" ", padding)
}

func maskFormVisible(input string) string {
	if strings.TrimSpace(input) == "" {
		return input
	}
	return strings.Repeat("•", len([]rune(input)))
}
