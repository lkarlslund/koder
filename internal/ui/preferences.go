package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/theme"
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
	original    config.UI
	draft       config.UI
	themeNames  []string
	spinnerFrame int
	tabs        []preferencesTab
	tabList     VerticalTabs
	focus       preferencesFocus
	fieldIndex  int
	buttonIndex int
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
				{Kind: preferencesFieldToggle, ID: "show_reasoning", Label: "Reasoning", Description: "Render model reasoning blocks in the transcript"},
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
		tabList: VerticalTabs{
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

func (d *PreferencesDialog) Update(msg tea.KeyMsg) PreferencesAction {
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
		names := SpinnerNames()
		idx := SpinnerIndex(d.draft.Spinner)
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

func (d PreferencesDialog) View(width int, palette theme.Palette) string {
	dialogWidth := width
	if dialogWidth <= 0 {
		dialogWidth = 84
	}
	dialogWidth = maxInt(68, dialogWidth)
	tabWidth := 18
	fieldWidth := maxInt(40, dialogWidth-tabWidth-9)

	tabRail := lipgloss.NewStyle().
		Width(tabWidth).
		BorderRight(true).
		BorderForeground(palette.SidebarBorder).
		Render(d.tabList.View(tabWidth-2, palette, d.focus == preferencesFocusTabs))

	fieldLines := make([]string, 0, len(d.currentFields())*2)
	for idx, field := range d.currentFields() {
		focused := d.focus == preferencesFocusFields && idx == d.fieldIndex
		switch field.Kind {
		case preferencesFieldTheme:
			fieldLines = append(fieldLines, ChoiceRow{
				Label:       field.Label,
				Description: field.Description,
				Value:       d.draft.Theme,
			}.View(fieldWidth, palette, focused))
		case preferencesFieldSpinner:
			style := SpinnerStyleByID(d.draft.Spinner)
			value := SpinnerFrame(d.draft.Spinner, d.spinnerFrame) + " " + style.Label
			fieldLines = append(fieldLines, ChoiceRow{
				Label:       field.Label,
				Description: field.Description,
				Value:       value,
			}.View(fieldWidth, palette, focused))
		case preferencesFieldToggle:
			fieldLines = append(fieldLines, ToggleRow{
				Label:       field.Label,
				Description: field.Description,
				Value:       d.toggleValue(field.ID),
			}.View(fieldWidth, palette, focused))
		}
	}
	fields := lipgloss.NewStyle().
		Width(fieldWidth).
		PaddingLeft(1).
		Render(strings.Join(fieldLines, "\n\n"))

	buttons := lipgloss.JoinHorizontal(
		lipgloss.Left,
		Button{Label: "OK", Primary: true, Focused: d.focus == preferencesFocusButtons && d.buttonIndex == 0}.View(palette),
		"  ",
		Button{Label: "Cancel", Focused: d.focus == preferencesFocusButtons && d.buttonIndex == 1}.View(palette),
	)

	body := lipgloss.JoinVertical(
		lipgloss.Left,
		lipgloss.JoinHorizontal(lipgloss.Top, tabRail, " ", fields),
		"",
		buttons,
	)

	return Modal{
		Title:    "Preferences",
		Subtitle: "Tab/Shift+Tab moves focus. Enter or arrows change values.",
		Body:     body,
		Footer:   fmt.Sprintf("Theme: %s  Spinner: %s", strings.TrimSpace(d.draft.Theme), SpinnerStyleByID(d.draft.Spinner).Label),
		Width:    dialogWidth,
	}.View(palette)
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
	case "half_blocks":
		return d.draft.HalfBlocks
	case "show_sidebar":
		return d.draft.ShowSidebar
	case "show_timestamps":
		return d.draft.ShowTimestamps
	case "show_reasoning":
		return d.draft.ShowReasoning
	case "mouse":
		return d.draft.Mouse
	default:
		return false
	}
}

func (d *PreferencesDialog) setToggle(id string, value bool) {
	switch id {
	case "half_blocks":
		d.draft.HalfBlocks = value
	case "show_sidebar":
		d.draft.ShowSidebar = value
	case "show_timestamps":
		d.draft.ShowTimestamps = value
	case "show_reasoning":
		d.draft.ShowReasoning = value
	case "mouse":
		d.draft.Mouse = value
	}
}
