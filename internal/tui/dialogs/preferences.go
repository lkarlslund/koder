package dialogs

import (
	"fmt"
	"strings"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/theme"
	"github.com/lkarlslund/koder/internal/ui"
)

type PreferencesActionKind int

const (
	PreferencesActionNone PreferencesActionKind = iota
	PreferencesActionChanged
	PreferencesActionApply
	PreferencesActionCancel
)

type PreferencesAction struct {
	Kind PreferencesActionKind
	UI   config.UI
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
	original     config.UI
	draft        config.UI
	themeNames   []string
	spinnerFrame int
	tabs         []preferencesTab
	tabList      ui.VerticalTabs
	focus        preferencesFocus
	fieldIndex   int
	buttonIndex  int
}

func NewPreferencesDialog(current config.UI, themeNames []string) PreferencesDialog {
	if len(themeNames) == 0 {
		themeNames = []string{theme.Default().Name}
	}
	tabs := []preferencesTab{
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
	}
}

func (d *PreferencesDialog) Tick() {
	d.spinnerFrame++
}

func (d PreferencesDialog) Draft() config.UI {
	return d.draft
}

func (d PreferencesDialog) Original() config.UI {
	return d.original
}

func (d *PreferencesDialog) Update(msg ui.KeyMsg) PreferencesAction {
	switch msg.String() {
	case "esc":
		return PreferencesAction{Kind: PreferencesActionCancel, UI: d.original}
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
		return d.moveVertical(-1)
	case "down":
		return d.moveVertical(1)
	case "left":
		return d.moveHorizontal(-1)
	case "right":
		return d.moveHorizontal(1)
	case " ", "enter":
		return d.activate()
	default:
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
			return PreferencesAction{Kind: PreferencesActionApply, UI: d.draft}
		}
		return PreferencesAction{Kind: PreferencesActionCancel, UI: d.original}
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
		current := strings.TrimSpace(d.draft.Theme)
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
		d.draft.Theme = d.themeNames[idx]
	case preferencesFieldSpinner:
		names := ui.SpinnerNames()
		idx := ui.SpinnerIndex(d.draft.Spinner)
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
		d.draft.Spinner = names[idx]
	case preferencesFieldToggle:
		d.setToggle(field.ID, !d.toggleValue(field.ID))
	default:
		return PreferencesAction{}
	}
	return PreferencesAction{Kind: PreferencesActionChanged, UI: d.draft}
}

func (d PreferencesDialog) Measure(ctx *ui.Context, constraints ui.Constraints) ui.Size {
	width := constraints.MaxW
	if width <= 0 {
		width = 84
	}
	return constraints.Clamp(d.dialog(width, ctx.Palette).Measure(ctx, ui.Constraints{MaxW: width, MaxH: constraints.MaxH}))
}

func (d PreferencesDialog) Render(ctx *ui.Context, bounds ui.Rect) ui.Surface {
	maxWidth := dialogRenderWidth(bounds, 84)
	element := d.dialog(maxWidth, ctx.Palette)
	size := element.Measure(ctx, ui.Constraints{MaxW: maxWidth, MaxH: bounds.H})
	return element.Render(ctx, ui.Rect{X: bounds.X, Y: bounds.Y, W: size.W, H: bounds.H})
}

func (d PreferencesDialog) dialog(width int, palette theme.Palette) ui.Element {
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
		case preferencesFieldTheme:
			fieldRows = append(fieldRows, ui.Fixed(ui.ChoiceRow{
				Label:       field.Label,
				Description: field.Description,
				Value:       d.draft.Theme,
				Width:       fieldWidth,
				Focused:     focused,
			}))
		case preferencesFieldSpinner:
			style := ui.SpinnerStyleByID(d.draft.Spinner)
			value := ui.SpinnerFrame(d.draft.Spinner, d.spinnerFrame) + " " + style.Label
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
	fields := ui.Inset{
		Padding: ui.Insets{Left: 1},
		Child:   ui.FlexBox{Direction: ui.DirectionVertical, Children: fieldRows},
	}

	buttons := ui.ButtonRow{
		Buttons: []ui.Button{
			{Label: "OK", Primary: true, Focused: d.focus == preferencesFocusButtons && d.buttonIndex == 0},
			{Label: "Cancel", Focused: d.focus == preferencesFocusButtons && d.buttonIndex == 1},
		},
		Align: ui.HorizontalAlignRight,
		Width: dialogWidth - 4,
	}

	buttons.Width = maxInt(0, dialogWidth-6)
	return ui.WindowFrame{
		Title: "Preferences",
		Width: dialogWidth,
		Content: ui.FlexBox{
			Direction: ui.DirectionVertical,
			Children: []ui.Child{
				ui.Fixed(ui.Static{Content: "Tab/Shift+Tab moves focus. Enter or arrows change values."}),
				ui.Fixed(ui.FlexBox{
					Direction: ui.DirectionHorizontal,
					Spacing:   1,
					Children: []ui.Child{
						{
							Element: ui.Section{
								Title: "Tabs",
								Width: tabWidth,
								Child: ui.VerticalTabs{
									Tabs:    d.tabList.Tabs,
									Active:  d.tabList.Active,
									Width:   tabWidth - 2,
									Focused: d.focus == preferencesFocusTabs,
								},
							},
							Basis: tabWidth,
						},
						ui.Flex(ui.Section{
							Title:   "Options",
							Width:   fieldWidth + 1,
							Padding: ui.Insets{Left: 1},
							Child:   fields,
						}, 1),
					},
				}),
				ui.Fixed(buttons),
				ui.Fixed(ui.Static{Content: fmt.Sprintf("Theme: %s  Spinner: %s", strings.TrimSpace(d.draft.Theme), ui.SpinnerStyleByID(d.draft.Spinner).Label)}),
			},
			Spacing: 2,
		},
		ShowClose: true,
	}
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
		return d.draft.CursorBlink
	case "half_blocks":
		return d.draft.HalfBlocks
	case "show_sidebar":
		return d.draft.ShowSidebar
	case "show_timestamps":
		return d.draft.ShowTimestamps
	case "show_reasoning":
		return d.draft.ShowReasoning
	case "show_system":
		return d.draft.ShowSystem
	case "mouse":
		return d.draft.Mouse
	default:
		return false
	}
}

func (d *PreferencesDialog) setToggle(id string, value bool) {
	switch id {
	case "cursor_blink":
		d.draft.CursorBlink = value
	case "half_blocks":
		d.draft.HalfBlocks = value
	case "show_sidebar":
		d.draft.ShowSidebar = value
	case "show_timestamps":
		d.draft.ShowTimestamps = value
	case "show_reasoning":
		d.draft.ShowReasoning = value
	case "show_system":
		d.draft.ShowSystem = value
	case "mouse":
		d.draft.Mouse = value
	}
}
