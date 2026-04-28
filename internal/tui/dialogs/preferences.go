package dialogs

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/theme"
	"github.com/lkarlslund/koder/internal/ui"
	"github.com/lkarlslund/koder/internal/ui/textarea"
)

type PreferencesActionKind int

const (
	PreferencesActionNone PreferencesActionKind = iota
	PreferencesActionChanged
	PreferencesActionApply
	PreferencesActionCancel
)

type PreferencesAction struct {
	Kind   PreferencesActionKind
	Values PreferencesValues
}

type PreferencesValues struct {
	UI               config.UI
	MaxToolLoopSteps int
}

type preferencesFocus int

const (
	preferencesFocusTabs preferencesFocus = iota
	preferencesFocusFields
	preferencesFocusButtons
)

type preferencesFieldKind int

const (
	preferencesFieldTheme preferencesFieldKind = iota
	preferencesFieldSpinner
	preferencesFieldToggle
	preferencesFieldInteger
)

type preferencesField struct {
	Kind        preferencesFieldKind
	ID          string
	Label       string
	Description string
}

type preferencesTab struct {
	Title  string
	Fields []preferencesField
}

type PreferencesDialog struct {
	original     PreferencesValues
	draft        PreferencesValues
	themeNames   []string
	spinnerFrame int
	tabs         []preferencesTab
	tabList      ui.VerticalTabs
	focus        preferencesFocus
	fieldIndex   int
	buttonIndex  int
	editors      map[string]textarea.Model
}

func NewPreferencesDialog(current PreferencesValues, themeNames []string) PreferencesDialog {
	if len(themeNames) == 0 {
		themeNames = []string{theme.Default().Name}
	}
	tabs := []preferencesTab{
		{
			Title: "General",
			Fields: []preferencesField{
				{Kind: preferencesFieldInteger, ID: "max_tool_loop_steps", Label: "Tool Turns", Description: "Maximum model tool turns before continuation pauses"},
			},
		},
		{
			Title: "Appearance",
			Fields: []preferencesField{
				{Kind: preferencesFieldTheme, ID: "theme", Label: "Theme", Description: "Choose the active color theme"},
				{Kind: preferencesFieldSpinner, ID: "spinner", Label: "Spinner", Description: "Choose the activity spinner style"},
				{Kind: preferencesFieldToggle, ID: "half_blocks", Label: "Half Blocks", Description: "Use half-block separators for boxed user text"},
				{Kind: preferencesFieldToggle, ID: "show_sidebar", Label: "Sidebar", Description: "Show the session sidebar"},
				{Kind: preferencesFieldToggle, ID: "show_timestamps", Label: "Timestamps", Description: "Show message timestamps in the transcript"},
			},
		},
		{
			Title: "Behavior",
			Fields: []preferencesField{
				{Kind: preferencesFieldToggle, ID: "cursor_blink", Label: "Cursor Blink", Description: "Blink the composer cursor while the input is focused"},
				{Kind: preferencesFieldToggle, ID: "show_reasoning", Label: "Reasoning", Description: "Render model reasoning blocks in the transcript"},
				{Kind: preferencesFieldToggle, ID: "show_system", Label: "System", Description: "Render system prompts, internal notices, and skill output in the transcript"},
				{Kind: preferencesFieldToggle, ID: "mouse", Label: "Mouse", Description: "Enable terminal mouse capture"},
			},
		},
	}
	tabNames := make([]string, 0, len(tabs))
	for _, tab := range tabs {
		tabNames = append(tabNames, tab.Title)
	}
	return PreferencesDialog{
		original:   current,
		draft:      current,
		themeNames: themeNames,
		tabs:       tabs,
		tabList: ui.VerticalTabs{
			Tabs: tabNames,
		},
		focus: preferencesFocusFields,
		editors: map[string]textarea.Model{
			"max_tool_loop_steps": newPreferencesEditor(strconv.Itoa(current.MaxToolLoopSteps)),
		},
	}
}

func (d *PreferencesDialog) Tick() {
	d.spinnerFrame++
}

func (d PreferencesDialog) Draft() config.UI {
	return d.draft.UI
}

func (d PreferencesDialog) Original() config.UI {
	return d.original.UI
}

func (d PreferencesDialog) DraftValues() PreferencesValues {
	return d.draft
}

func (d PreferencesDialog) OriginalValues() PreferencesValues {
	return d.original
}

func (d *PreferencesDialog) Update(msg ui.KeyMsg) PreferencesAction {
	switch msg.String() {
	case "esc":
		return PreferencesAction{Kind: PreferencesActionCancel, Values: d.original}
	case "tab":
		d.focus = (d.focus + 1) % 3
		return PreferencesAction{}
	case "shift+tab":
		d.focus--
		if d.focus < 0 {
			d.focus = preferencesFocusButtons
		}
		return PreferencesAction{}
	case "up":
		if action, ok := d.handleFieldStep(1); ok {
			return action
		}
		return d.moveVertical(-1)
	case "down":
		if action, ok := d.handleFieldStep(-1); ok {
			return action
		}
		return d.moveVertical(1)
	case "left":
		return d.moveHorizontal(-1)
	case "right":
		return d.moveHorizontal(1)
	case " ", "enter":
		return d.activate()
	default:
		if d.focus == preferencesFocusFields && d.currentFieldKind() == preferencesFieldInteger {
			if action, ok := d.updateCurrentIntegerEditor(msg); ok {
				return action
			}
		}
		return PreferencesAction{}
	}
}

func (d *PreferencesDialog) moveVertical(delta int) PreferencesAction {
	switch d.focus {
	case preferencesFocusTabs:
		d.tabList.Move(delta)
		d.fieldIndex = 0
	case preferencesFocusFields:
		fields := d.currentFields()
		if len(fields) == 0 {
			d.fieldIndex = 0
			return PreferencesAction{}
		}
		d.fieldIndex += delta
		if d.fieldIndex < 0 {
			d.fieldIndex = 0
		}
		if d.fieldIndex >= len(fields) {
			d.fieldIndex = len(fields) - 1
		}
	case preferencesFocusButtons:
		d.buttonIndex += delta
		if d.buttonIndex < 0 {
			d.buttonIndex = 0
		}
		if d.buttonIndex > 1 {
			d.buttonIndex = 1
		}
	}
	return PreferencesAction{}
}

func (d *PreferencesDialog) moveHorizontal(delta int) PreferencesAction {
	switch d.focus {
	case preferencesFocusTabs:
		d.tabList.Move(delta)
		d.fieldIndex = 0
		return PreferencesAction{}
	case preferencesFocusFields:
		return d.adjustField(delta)
	case preferencesFocusButtons:
		d.buttonIndex += delta
		if d.buttonIndex < 0 {
			d.buttonIndex = 0
		}
		if d.buttonIndex > 1 {
			d.buttonIndex = 1
		}
		return PreferencesAction{}
	default:
		return PreferencesAction{}
	}
}

func (d *PreferencesDialog) activate() PreferencesAction {
	switch d.focus {
	case preferencesFocusTabs:
		d.focus = preferencesFocusFields
		return PreferencesAction{}
	case preferencesFocusFields:
		return d.adjustField(1)
	case preferencesFocusButtons:
		if d.buttonIndex == 0 {
			return PreferencesAction{Kind: PreferencesActionApply, Values: d.draft}
		}
		return PreferencesAction{Kind: PreferencesActionCancel, Values: d.original}
	default:
		return PreferencesAction{}
	}
}

func (d *PreferencesDialog) adjustField(delta int) PreferencesAction {
	fields := d.currentFields()
	if len(fields) == 0 || d.fieldIndex < 0 || d.fieldIndex >= len(fields) {
		return PreferencesAction{}
	}
	field := fields[d.fieldIndex]
	switch field.Kind {
	case preferencesFieldTheme:
		idx := 0
		current := strings.TrimSpace(d.draft.UI.Theme)
		for i, name := range d.themeNames {
			if name == current {
				idx = i
				break
			}
		}
		idx += delta
		if idx < 0 {
			idx = len(d.themeNames) - 1
		}
		if idx >= len(d.themeNames) {
			idx = 0
		}
		d.draft.UI.Theme = d.themeNames[idx]
	case preferencesFieldSpinner:
		names := ui.SpinnerNames()
		idx := ui.SpinnerIndex(d.draft.UI.Spinner)
		if idx < 0 {
			idx = 0
		}
		idx += delta
		if idx < 0 {
			idx = len(names) - 1
		}
		if idx >= len(names) {
			idx = 0
		}
		d.draft.UI.Spinner = names[idx]
	case preferencesFieldToggle:
		d.setToggle(field.ID, !d.toggleValue(field.ID))
	case preferencesFieldInteger:
		switch field.ID {
		case "max_tool_loop_steps":
			d.draft.MaxToolLoopSteps += delta
			if d.draft.MaxToolLoopSteps < 1 {
				d.draft.MaxToolLoopSteps = 1
			}
			d.setIntegerEditorValue(field.ID, d.draft.MaxToolLoopSteps)
		default:
			return PreferencesAction{}
		}
	default:
		return PreferencesAction{}
	}
	return PreferencesAction{Kind: PreferencesActionChanged, Values: d.draft}
}

func (d PreferencesDialog) Measure(ctx *ui.Context, constraints ui.Constraints) ui.Size {
	width := constraints.MaxW
	if width <= 0 {
		width = 84
	}
	return constraints.Clamp(d.dialog(width, ctx.Palette).Measure(ctx, ui.Constraints{MaxW: width, MaxH: constraints.MaxH}))
}

func (d PreferencesDialog) Surface(ctx *ui.Context, bounds ui.Rect) ui.Surface {
	maxWidth := dialogRenderWidth(bounds, 84)
	node := d.dialog(maxWidth, ctx.Palette)
	size := node.Measure(ctx, ui.Constraints{MaxW: maxWidth, MaxH: bounds.H})
	return ui.PaintNodeSurface(ctx, node, ui.Rect{W: size.W, H: bounds.H})
}

func (d PreferencesDialog) Paint(ctx *ui.Context, canvas ui.Canvas) {
	if canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	canvas.BlitSurface(0, 0, d.Surface(ctx, ui.Rect{W: canvas.Width(), H: canvas.Height()}))
}

func (d PreferencesDialog) dialog(width int, palette theme.Palette) ui.Node {
	dialogWidth := width
	if dialogWidth <= 0 {
		dialogWidth = 84
	}
	dialogWidth = maxInt(68, dialogWidth)
	tabWidth := 18
	fieldWidth := maxInt(40, dialogWidth-tabWidth-9)

	fieldRows := make([]ui.Child, 0, len(d.currentFields()))
	for idx, field := range d.currentFields() {
		focused := d.focus == preferencesFocusFields && idx == d.fieldIndex
		switch field.Kind {
		case preferencesFieldInteger:
			fieldRows = append(fieldRows, ui.Fixed(d.renderIntegerField(field, fieldWidth, palette, focused)))
		case preferencesFieldTheme:
			fieldRows = append(fieldRows, ui.Fixed(ui.ChoiceRow{
				Label:       field.Label,
				Description: field.Description,
				Value:       d.draft.UI.Theme,
				Width:       fieldWidth,
				Focused:     focused,
			}))
		case preferencesFieldSpinner:
			style := ui.SpinnerStyleByID(d.draft.UI.Spinner)
			value := ui.SpinnerFrame(d.draft.UI.Spinner, d.spinnerFrame) + " " + style.Label
			fieldRows = append(fieldRows, ui.Fixed(ui.ChoiceRow{
				Label:       field.Label,
				Description: field.Description,
				Value:       value,
				Width:       fieldWidth,
				Focused:     focused,
			}))
		case preferencesFieldToggle:
			fieldRows = append(fieldRows, ui.Fixed(ui.CheckboxRow{
				Label:       field.Label,
				Description: field.Description,
				Checked:     d.toggleValue(field.ID),
				OnLabel:     "Enabled",
				OffLabel:    "Disabled",
				Width:       fieldWidth,
				Focused:     focused,
			}))
		}
	}
	fields := ui.AsNode(ui.Inset{
		Padding: ui.Insets{Left: 1},
		Child:   ui.AsNode(ui.FlexBox{Direction: ui.DirectionVertical, Children: fieldRows}),
	})

	buttons := ui.ButtonRow{
		Buttons: []ui.Button{
			{Label: "OK", Primary: true, Focused: d.focus == preferencesFocusButtons && d.buttonIndex == 0},
			{Label: "Cancel", Focused: d.focus == preferencesFocusButtons && d.buttonIndex == 1},
		},
		Align: ui.HorizontalAlignRight,
		Width: dialogWidth - 4,
	}

	buttons.Width = maxInt(0, dialogWidth-6)
	return ui.AsNode(ui.WindowFrame{
		Title: "Preferences",
		Width: dialogWidth,
		Content: ui.AsNode(ui.FlexBox{
			Direction: ui.DirectionVertical,
			Children: []ui.Child{
				ui.Fixed(ui.Static{Content: "Tab/Shift+Tab moves focus. Enter or arrows change values."}),
				ui.Fixed(ui.AsNode(ui.FlexBox{
					Direction: ui.DirectionHorizontal,
					Spacing:   1,
					Children: []ui.Child{
						{
							Node: ui.AsNode(ui.Section{
								Title: "Tabs",
								Width: tabWidth,
								Child: ui.AsNode(ui.VerticalTabs{
									Tabs:    d.tabList.Tabs,
									Active:  d.tabList.Active,
									Width:   tabWidth - 2,
									Focused: d.focus == preferencesFocusTabs,
								}),
							}),
							Basis: tabWidth,
						},
						ui.Flex(ui.AsNode(ui.Section{
							Title:   "Options",
							Width:   fieldWidth + 1,
							Padding: ui.Insets{Left: 1},
							Child:   fields,
						}), 1),
					},
				})),
				ui.Fixed(buttons),
				ui.Fixed(ui.Static{Content: fmt.Sprintf("Theme: %s  Spinner: %s  Tool Turns: %d", strings.TrimSpace(d.draft.UI.Theme), ui.SpinnerStyleByID(d.draft.UI.Spinner).Label, d.draft.MaxToolLoopSteps)}),
			},
			Spacing: 2,
		}),
		ShowClose: true,
	})
}

func (d PreferencesDialog) currentFields() []preferencesField {
	if len(d.tabs) == 0 {
		return nil
	}
	idx := d.tabList.Current()
	if idx < 0 || idx >= len(d.tabs) {
		return nil
	}
	return d.tabs[idx].Fields
}

func (d PreferencesDialog) toggleValue(id string) bool {
	switch id {
	case "cursor_blink":
		return d.draft.UI.CursorBlink
	case "half_blocks":
		return d.draft.UI.HalfBlocks
	case "show_sidebar":
		return d.draft.UI.ShowSidebar
	case "show_timestamps":
		return d.draft.UI.ShowTimestamps
	case "show_reasoning":
		return d.draft.UI.ShowReasoning
	case "show_system":
		return d.draft.UI.ShowSystem
	case "mouse":
		return d.draft.UI.Mouse
	default:
		return false
	}
}

func (d *PreferencesDialog) setToggle(id string, value bool) {
	switch id {
	case "cursor_blink":
		d.draft.UI.CursorBlink = value
	case "half_blocks":
		d.draft.UI.HalfBlocks = value
	case "show_sidebar":
		d.draft.UI.ShowSidebar = value
	case "show_timestamps":
		d.draft.UI.ShowTimestamps = value
	case "show_reasoning":
		d.draft.UI.ShowReasoning = value
	case "show_system":
		d.draft.UI.ShowSystem = value
	case "mouse":
		d.draft.UI.Mouse = value
	}
}

func (d PreferencesDialog) currentFieldKind() preferencesFieldKind {
	fields := d.currentFields()
	if len(fields) == 0 || d.fieldIndex < 0 || d.fieldIndex >= len(fields) {
		return preferencesFieldToggle
	}
	return fields[d.fieldIndex].Kind
}

func (d *PreferencesDialog) handleFieldStep(delta int) (PreferencesAction, bool) {
	if d.focus != preferencesFocusFields || d.currentFieldKind() != preferencesFieldInteger {
		return PreferencesAction{}, false
	}
	fields := d.currentFields()
	if d.fieldIndex < 0 || d.fieldIndex >= len(fields) {
		return PreferencesAction{}, false
	}
	editor := d.integerEditor(fields[d.fieldIndex].ID)
	value, err := strconv.Atoi(strings.TrimSpace(editor.Value()))
	if err != nil {
		return PreferencesAction{}, false
	}
	value += delta
	if value < 1 {
		value = 1
	}
	d.draft.MaxToolLoopSteps = value
	d.setIntegerEditorValue(fields[d.fieldIndex].ID, value)
	return PreferencesAction{Kind: PreferencesActionChanged, Values: d.draft}, true
}

func (d *PreferencesDialog) updateCurrentIntegerEditor(msg ui.KeyMsg) (PreferencesAction, bool) {
	fields := d.currentFields()
	if len(fields) == 0 || d.fieldIndex < 0 || d.fieldIndex >= len(fields) {
		return PreferencesAction{}, false
	}
	field := fields[d.fieldIndex]
	if field.Kind != preferencesFieldInteger {
		return PreferencesAction{}, false
	}
	if !preferencesIntegerKeyAllowed(msg) {
		return PreferencesAction{}, false
	}
	editor := d.integerEditor(field.ID)
	updated, _ := editor.Update(msg)
	d.storeIntegerEditor(field.ID, updated)
	if value, err := strconv.Atoi(strings.TrimSpace(updated.Value())); err == nil && value > 0 {
		d.draft.MaxToolLoopSteps = value
	}
	return PreferencesAction{Kind: PreferencesActionChanged, Values: d.draft}, true
}

func preferencesIntegerKeyAllowed(msg ui.KeyMsg) bool {
	switch msg.Type {
	case ui.KeyBackspace, ui.KeyDelete, ui.KeyLeft, ui.KeyRight, ui.KeyHome, ui.KeyEnd:
		return true
	case ui.KeyRunes:
		for _, r := range msg.Runes {
			if r < '0' || r > '9' {
				return false
			}
		}
		return len(msg.Runes) > 0
	default:
		switch msg.String() {
		case "ctrl+a", "ctrl+e":
			return true
		default:
			return false
		}
	}
}

func newPreferencesEditor(value string) textarea.Model {
	editor := textarea.New()
	editor.BlinkEnabled = false
	editor.Focus()
	editor.SetHeight(1)
	editor.SetWidth(32)
	editor.SetValue(value)
	return editor
}

func (d PreferencesDialog) integerEditor(id string) textarea.Model {
	if d.editors == nil {
		return newPreferencesEditor("")
	}
	editor, ok := d.editors[id]
	if !ok {
		return newPreferencesEditor("")
	}
	return editor
}

func (d *PreferencesDialog) storeIntegerEditor(id string, editor textarea.Model) {
	if d.editors == nil {
		d.editors = map[string]textarea.Model{}
	}
	d.editors[id] = editor
}

func (d *PreferencesDialog) setIntegerEditorValue(id string, value int) {
	editor := d.integerEditor(id)
	editor.SetValue(strconv.Itoa(value))
	d.storeIntegerEditor(id, editor)
}

func (d PreferencesDialog) renderIntegerField(field preferencesField, width int, palette theme.Palette, active bool) ui.Node {
	editor := d.integerEditor(field.ID)
	line := editor.VisibleLine()
	foreground := palette.MarkdownText
	background := palette.ScreenBackground
	borderColor := palette.SidebarBorder
	if active {
		foreground = palette.UserTextForeground
		background = palette.UserTextBackground
		borderColor = firstNonEmptyColor(palette.SelectionBackground, palette.ActivityText, palette.SidebarBorder)
	}
	return ui.AsNode(ui.FlexBox{
		Direction: ui.DirectionVertical,
		Children: []ui.Child{
			ui.Fixed(ui.AsNode(ui.FlexBox{
				Direction: ui.DirectionHorizontal,
				Children: []ui.Child{
					ui.Fixed(ui.Static{Content: field.Label}),
					ui.Flex(ui.Spacer{}, 1),
					ui.Fixed(ui.Static{Content: truncateText(field.Description, maxInt(16, width-ui.PlainWidth(field.Label)-3))}),
				},
			})),
			ui.Fixed(ui.InputField{
				Width:         maxInt(18, width),
				Value:         editor.Value(),
				ContentBefore: line.Before(),
				ContentCursor: line.Cursor(),
				ContentAfter:  line.After(),
				CursorVisible: active && editor.CursorVisible(),
				Foreground:    foreground,
				Background:    background,
				PlaceholderFG: palette.ComposerMutedText,
				BorderColor:   borderColor,
			}),
		},
		Spacing: 1,
	})
}
