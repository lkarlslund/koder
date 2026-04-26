package dialogs

import (
	"fmt"
	"strings"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/theme"
	"github.com/lkarlslund/koder/internal/ui"
	"github.com/lkarlslund/koder/internal/ui/textarea"
)

type ProviderConnectActionKind int

const (
	ProviderConnectActionNone ProviderConnectActionKind = iota
	ProviderConnectActionCancel
	ProviderConnectActionTest
	ProviderConnectActionSave
)

type ProviderConnectAction struct {
	Kind  ProviderConnectActionKind
	Draft provider.ConnectDraft
}

type connectStage int

const (
	connectStageProvider connectStage = iota
	connectStageAuth
	connectStageForm
)

type connectFocus int

const (
	connectFocusFields connectFocus = iota
	connectFocusButtons
)

type connectStatusKind int

const (
	connectStatusNone connectStatusKind = iota
	connectStatusInfo
	connectStatusSuccess
	connectStatusError
)

type ConnectDialog struct {
	stage      connectStage
	query      string
	index      int
	items      []provider.Descriptor
	view       []provider.Descriptor
	configured map[string]config.Provider
	selected   provider.Descriptor
	authIndex  int
	draft      provider.ConnectDraft
	status     string
	statusKind connectStatusKind
	focus      connectFocus
	fieldIndex int
	buttonIdx  int
	editors    map[string]textarea.Model
}

func NewConnectDialog(items []provider.Descriptor, configured map[string]config.Provider) ConnectDialog {
	dialog := ConnectDialog{
		stage:      connectStageProvider,
		items:      items,
		configured: configured,
		editors:    map[string]textarea.Model{},
	}
	dialog.refilter()
	return dialog
}

func (d *ConnectDialog) SetStatus(status string) {
	d.status = strings.TrimSpace(status)
	if d.status == "" {
		d.statusKind = connectStatusNone
		return
	}
	d.statusKind = connectStatusInfo
}

func (d *ConnectDialog) SetStatusSuccess(status string) {
	d.status = strings.TrimSpace(status)
	if d.status == "" {
		d.statusKind = connectStatusNone
		return
	}
	d.statusKind = connectStatusSuccess
}

func (d *ConnectDialog) SetStatusError(status string) {
	d.status = strings.TrimSpace(status)
	if d.status == "" {
		d.statusKind = connectStatusNone
		return
	}
	d.statusKind = connectStatusError
}

func (d *ConnectDialog) Update(msg ui.KeyMsg) ProviderConnectAction {
	switch d.stage {
	case connectStageProvider:
		return d.updateProviderList(msg)
	case connectStageAuth:
		return d.updateAuthPicker(msg)
	case connectStageForm:
		return d.updateForm(msg)
	default:
		return ProviderConnectAction{}
	}
}

func (d ConnectDialog) Measure(ctx *ui.Context, constraints ui.Constraints) ui.Size {
	width := constraints.MaxW
	if width <= 0 {
		width = 88
	}
	return constraints.Clamp(d.dialog(width, ctx.Palette).Measure(ctx, ui.Constraints{MaxW: width, MaxH: constraints.MaxH}))
}

func (d ConnectDialog) Render(ctx *ui.Context, bounds ui.Rect) ui.Surface {
	maxWidth := dialogRenderWidth(bounds, 88)
	element := d.dialog(maxWidth, ctx.Palette)
	size := element.Measure(ctx, ui.Constraints{MaxW: maxWidth, MaxH: bounds.H})
	return element.Render(ctx, ui.Rect{X: bounds.X, Y: bounds.Y, W: size.W, H: bounds.H})
}

func (d ConnectDialog) dialog(width int, palette theme.Palette) ui.Element {
	switch d.stage {
	case connectStageProvider:
		return d.providerListDialog(width, palette)
	case connectStageAuth:
		return d.authPickerDialog(width, palette)
	case connectStageForm:
		return d.formDialog(width, palette)
	default:
		return ui.Static{}
	}
}

func (d *ConnectDialog) updateProviderList(msg ui.KeyMsg) ProviderConnectAction {
	switch msg.String() {
	case "esc":
		return ProviderConnectAction{Kind: ProviderConnectActionCancel}
	case "up":
		d.move(-1)
	case "down":
		d.move(1)
	case "backspace":
		if d.query != "" {
			d.query = d.query[:len(d.query)-1]
			d.refilter()
		}
	case "enter":
		item, ok := d.currentProvider()
		if !ok {
			return ProviderConnectAction{Kind: ProviderConnectActionCancel}
		}
		d.selectProvider(item)
	default:
		if msg.Type == ui.KeyRunes {
			d.query += msg.String()
			d.refilter()
		}
	}
	return ProviderConnectAction{}
}

func (d *ConnectDialog) updateAuthPicker(msg ui.KeyMsg) ProviderConnectAction {
	switch msg.String() {
	case "esc":
		d.stage = connectStageProvider
	case "up":
		if d.authIndex > 0 {
			d.authIndex--
		}
	case "down":
		if d.authIndex < len(d.selected.AuthMethods)-1 {
			d.authIndex++
		}
	case "enter":
		d.chooseAuthMethod()
	}
	return ProviderConnectAction{}
}

func (d *ConnectDialog) updateForm(msg ui.KeyMsg) ProviderConnectAction {
	buttons := d.formButtons()
	buttons.Index = d.buttonIdx
	if idx, ok := buttons.HotkeyIndex(msg); ok {
		d.focus = connectFocusButtons
		d.buttonIdx = idx
		switch idx {
		case 0:
			return ProviderConnectAction{Kind: ProviderConnectActionTest, Draft: d.draft}
		case 1:
			return ProviderConnectAction{Kind: ProviderConnectActionSave, Draft: d.draft}
		default:
			return ProviderConnectAction{Kind: ProviderConnectActionCancel}
		}
	}
	switch msg.String() {
	case "esc":
		return ProviderConnectAction{Kind: ProviderConnectActionCancel}
	case "tab":
		d.advanceFocus(1)
	case "shift+tab":
		d.advanceFocus(-1)
	case "up":
		d.moveForm(-1)
	case "down":
		d.moveForm(1)
	case "left":
		if d.focus == connectFocusButtons {
			d.moveButtons(-1)
		} else {
			d.updateCurrentEditor(msg)
		}
	case "right":
		if d.focus == connectFocusButtons {
			d.moveButtons(1)
		} else {
			d.updateCurrentEditor(msg)
		}
	case "home", "ctrl+a":
		d.updateCurrentEditor(msg)
	case "end", "ctrl+e":
		d.updateCurrentEditor(msg)
	case "backspace":
		d.updateCurrentEditor(msg)
	case "ctrl+t":
		return ProviderConnectAction{Kind: ProviderConnectActionTest, Draft: d.draft}
	case "enter":
		if d.focus == connectFocusButtons {
			switch d.buttonIdx {
			case 0:
				return ProviderConnectAction{Kind: ProviderConnectActionTest, Draft: d.draft}
			case 1:
				return ProviderConnectAction{Kind: ProviderConnectActionSave, Draft: d.draft}
			default:
				return ProviderConnectAction{Kind: ProviderConnectActionCancel}
			}
		}
	default:
		if msg.Type == ui.KeyRunes {
			d.updateCurrentEditor(msg)
		}
	}
	return ProviderConnectAction{}
}

func (d *ConnectDialog) providerListDialog(width int, palette theme.Palette) ui.Element {
	dialogWidth := clampWidth(width, 72, 96)
	lines := []string{fmt.Sprintf("Filter: %s", d.query), ""}
	var list ui.Element = staticBlock("No providers match your filter.")
	if len(d.view) > 0 {
		start, end := windowBounds(d.index, len(d.view), 10)
		items := make([]ui.ListItem, 0, end-start)
		for idx := start; idx < end; idx++ {
			item := d.view[idx]
			tertiary := "remote"
			if _, ok := d.configured[item.ID]; ok {
				tertiary = "configured"
			} else if item.Local {
				tertiary = "local"
			}
			items = append(items, ui.ListItem{
				Primary:   item.Title,
				Secondary: item.Description,
				Tertiary:  tertiary,
			})
		}
		list = ui.Section{
			Width: dialogWidth - 8,
			Child: ui.List{
				Items:    items,
				Width:    dialogWidth - 8,
				Selected: d.index - start,
				Focused:  d.stage == connectStageProvider,
			},
		}
	}
	body := []ui.Child{
		ui.Fixed(linesBlock(lines...)),
		ui.Fixed(list),
	}
	if status := strings.TrimSpace(d.status); status != "" {
		body = append(body, ui.Fixed(ui.Spacer{H: 1}), ui.Fixed(ui.Label{Text: status}))
	}
	return ui.WindowFrame{
		Title: "Connect Provider",
		Width: dialogWidth,
		Content: ui.Column{
			Children: []ui.Child{
				ui.Fixed(ui.Column{Children: body, Spacing: 1}),
				ui.Fixed(ui.Static{Content: "Enter choose provider  Esc cancel"}),
			},
			Spacing: 2,
		},
		ShowClose: true,
	}
}

func (d *ConnectDialog) authPickerDialog(width int, palette theme.Palette) ui.Element {
	dialogWidth := clampWidth(width, 68, 88)
	lines := []string{
		d.selected.Title,
		d.selected.Description,
		"",
	}
	items := make([]ui.ListItem, 0, len(d.selected.AuthMethods))
	for _, method := range d.selected.AuthMethods {
		items = append(items, ui.ListItem{
			Primary:   method.Title,
			Secondary: method.Description,
		})
	}
	return ui.WindowFrame{
		Title: "Choose Auth Method",
		Width: dialogWidth,
		Content: ui.Column{
			Children: []ui.Child{
				ui.Fixed(ui.Column{
					Children: []ui.Child{
						ui.Fixed(linesBlock(lines...)),
						ui.Fixed(ui.Section{
							Width: dialogWidth - 8,
							Child: ui.List{
								Items:    items,
								Width:    dialogWidth - 8,
								Selected: d.authIndex,
								Focused:  d.stage == connectStageAuth,
							},
						}),
					},
					Spacing: 1,
				}),
				ui.Fixed(ui.Static{Content: "Enter continue  Esc back"}),
			},
			Spacing: 2,
		},
		ShowClose: true,
	}
}

func (d *ConnectDialog) formDialog(width int, palette theme.Palette) ui.Element {
	dialogWidth := clampWidth(width, 76, 100)
	fieldChildren := make([]ui.Child, 0, len(d.formFields()))
	for idx, field := range d.formFields() {
		active := d.focus == connectFocusFields && d.fieldIndex == idx
		fieldChildren = append(fieldChildren, ui.Fixed(d.renderFormField(field, dialogWidth-10, palette, active)))
	}

	bodyChildren := []ui.Child{
		ui.Fixed(ui.Section{
			Title:       "Provider",
			Padding:     ui.Insets{Left: 1, Right: 1, Bottom: 1},
			Background:  palette.SidebarBackground,
			Foreground:  palette.SidebarForeground,
			BorderColor: palette.SidebarBorder,
			Child: ui.Column{
				Children: []ui.Child{
					ui.Fixed(ui.Static{Content: d.selected.Title}),
					ui.Fixed(ui.Static{Content: compactInlineText(d.selected.Description)}),
				},
				Spacing: 0,
			},
		}),
		ui.Fixed(ui.Section{
			Title:       "Configuration",
			Padding:     ui.Insets{Left: 1, Right: 1, Bottom: 1},
			Background:  palette.SidebarBackground,
			Foreground:  palette.SidebarForeground,
			BorderColor: palette.SidebarBorder,
			Child: ui.Column{
				Children: fieldChildren,
				Spacing:  1,
			},
		}),
	}
	if strings.TrimSpace(d.status) != "" {
		auxChildren := make([]ui.Child, 0, 2)
		if status := strings.TrimSpace(d.status); status != "" {
			auxChildren = append(auxChildren, ui.Fixed(d.renderStatusElement(palette)))
		}
		bodyChildren = append(bodyChildren, ui.Fixed(ui.Section{
			Title:       "Connection",
			Padding:     ui.Insets{Left: 1, Right: 1, Bottom: 1},
			Background:  palette.SidebarBackground,
			Foreground:  palette.SidebarForeground,
			BorderColor: palette.SidebarBorder,
			Child: ui.Column{
				Children: auxChildren,
				Spacing:  1,
			},
		}))
	}
	buttons := d.formButtons()
	buttons.Index = d.buttonIdx
	buttons.Width = maxInt(0, dialogWidth-6)
	return ui.WindowFrame{
		Title: "Connect Provider",
		Width: dialogWidth,
		Content: ui.Column{
			Children: []ui.Child{
				ui.Fixed(ui.Column{Children: bodyChildren, Spacing: 1}),
				ui.Fixed(buttons),
				ui.Fixed(ui.Static{Content: "Type to edit  Ctrl+T test  Enter select  Esc cancel"}),
			},
			Spacing: 2,
		},
		ShowClose: true,
	}
}

func (d ConnectDialog) formButtons() ui.ButtonRow {
	return ui.ButtonRow{
		Buttons: []ui.Button{
			{ID: "test", Label: "Test", Hotkey: 't'},
			{ID: "save", Label: "Save", Hotkey: 's', Primary: true},
			{ID: "cancel", Label: "Cancel", Hotkey: 'c'},
		},
		Align: ui.HorizontalAlignRight,
	}
}

func (d ConnectDialog) renderFormField(field connectField, width int, palette theme.Palette, active bool) ui.Element {
	fieldWidth := maxInt(18, width)
	hintWidth := maxInt(16, width-ui.PlainWidth(field.Label)-3)
	hint := truncateText(field.Description, hintWidth)
	return ui.Column{
		Children: []ui.Child{
			ui.Fixed(ui.Row{
				Children: []ui.Child{
					ui.Fixed(ui.Static{Content: field.Label}),
					ui.Flex(ui.Spacer{}, 1),
					ui.Fixed(ui.Static{Content: hint}),
				},
			}),
			ui.Fixed(d.renderInputField(field.ID, fieldWidth, palette, active)),
		},
		Spacing: 1,
	}
}

func (d ConnectDialog) renderInputField(fieldID string, width int, palette theme.Palette, active bool) ui.Element {
	editor := d.editor(fieldID)
	line := editor.VisibleLine()
	before, cursor, after := line.Before(), line.Cursor(), line.After()
	value := editor.Value()
	if fieldID == "api_key" {
		before = maskVisible(before)
		cursor = maskVisible(cursor)
		after = maskVisible(after)
		if strings.TrimSpace(value) != "" {
			value = maskVisible(value)
		}
	}
	foreground := palette.MarkdownText
	background := palette.ScreenBackground
	borderColor := palette.SidebarBorder
	if active {
		foreground = palette.UserTextForeground
		background = palette.UserTextBackground
		borderColor = firstNonEmptyColor(palette.SelectionBackground, palette.ActivityText, palette.SidebarBorder)
	}
	return ui.InputField{
		Width:         width,
		Value:         value,
		Placeholder:   d.placeholderValue(fieldID),
		ContentBefore: before,
		ContentCursor: cursor,
		ContentAfter:  after,
		CursorVisible: active && editor.CursorVisible(),
		Foreground:    foreground,
		Background:    background,
		PlaceholderFG: palette.ComposerMutedText,
		BorderColor:   borderColor,
	}
}

func (d ConnectDialog) placeholderValue(fieldID string) string {
	switch fieldID {
	case "api_key":
		return "(required)"
	default:
		return ""
	}
}

func (d *ConnectDialog) currentProvider() (provider.Descriptor, bool) {
	if len(d.view) == 0 || d.index < 0 || d.index >= len(d.view) {
		return provider.Descriptor{}, false
	}
	return d.view[d.index], true
}

func (d *ConnectDialog) selectProvider(item provider.Descriptor) {
	d.selected = item
	d.authIndex = 0
	d.status = ""
	d.statusKind = connectStatusNone
	d.draft, _ = provider.BuildDraft(item.ID, d.configured)
	d.resetEditors()
	if len(item.AuthMethods) > 1 {
		d.stage = connectStageAuth
		return
	}
	d.draft = d.draft.WithAuthMethod(item.AuthMethods[0].ID, item)
	d.stage = connectStageForm
	d.focus = connectFocusFields
	d.fieldIndex = 0
	d.buttonIdx = 1
}

func (d *ConnectDialog) chooseAuthMethod() {
	if len(d.selected.AuthMethods) == 0 {
		return
	}
	method := d.selected.AuthMethods[d.authIndex].ID
	d.draft = d.draft.WithAuthMethod(method, d.selected)
	d.resetEditors()
	d.stage = connectStageForm
	d.focus = connectFocusFields
	d.fieldIndex = 0
	d.buttonIdx = 1
}

func (d *ConnectDialog) move(delta int) {
	if len(d.view) == 0 {
		d.index = 0
		return
	}
	d.index += delta
	if d.index < 0 {
		d.index = 0
	}
	if d.index >= len(d.view) {
		d.index = len(d.view) - 1
	}
}

func (d *ConnectDialog) refilter() {
	query := strings.ToLower(strings.TrimSpace(d.query))
	d.view = d.view[:0]
	for _, item := range d.items {
		haystack := strings.ToLower(item.Title + " " + item.Description + " " + item.ID)
		if query == "" || strings.Contains(haystack, query) {
			d.view = append(d.view, item)
		}
	}
	if d.index >= len(d.view) {
		d.index = maxInt(0, len(d.view)-1)
	}
}

type connectField struct {
	ID          string
	Label       string
	Description string
}

func (d ConnectDialog) formFields() []connectField {
	fields := []connectField{
		{ID: "name", Label: "Name", Description: "Stored label for this provider entry"},
		{ID: "base_url", Label: "Base URL", Description: "OpenAI-compatible API endpoint"},
	}
	if d.draft.AuthMethod == provider.AuthMethodAPIKey {
		fields = append(fields, connectField{ID: "api_key", Label: "API Key", Description: "Stored in config.toml for now"})
	}
	return fields
}

func (d ConnectDialog) currentFieldID() string {
	fields := d.formFields()
	if len(fields) == 0 || d.fieldIndex < 0 || d.fieldIndex >= len(fields) {
		return ""
	}
	return fields[d.fieldIndex].ID
}

func (d *ConnectDialog) moveForm(delta int) {
	if d.focus == connectFocusButtons {
		d.moveButtons(delta)
		return
	}
	fields := d.formFields()
	if len(fields) == 0 {
		return
	}
	d.fieldIndex += delta
	if d.fieldIndex < 0 {
		d.fieldIndex = 0
	}
	if d.fieldIndex >= len(fields) {
		d.fieldIndex = len(fields) - 1
		d.focus = connectFocusButtons
	}
}

func (d *ConnectDialog) moveButtons(delta int) {
	d.buttonIdx += delta
	if d.buttonIdx < 0 {
		d.buttonIdx = 0
	}
	if d.buttonIdx > 2 {
		d.buttonIdx = 2
	}
}

func (d *ConnectDialog) ActivateControl(controlID string) ProviderConnectAction {
	switch controlID {
	case "test":
		d.focus = connectFocusButtons
		d.buttonIdx = 0
		return ProviderConnectAction{Kind: ProviderConnectActionTest, Draft: d.draft}
	case "save":
		d.focus = connectFocusButtons
		d.buttonIdx = 1
		return ProviderConnectAction{Kind: ProviderConnectActionSave, Draft: d.draft}
	case "cancel":
		d.focus = connectFocusButtons
		d.buttonIdx = 2
		return ProviderConnectAction{Kind: ProviderConnectActionCancel}
	default:
		return ProviderConnectAction{}
	}
}

func (d *ConnectDialog) advanceFocus(delta int) {
	if delta > 0 {
		if d.focus == connectFocusFields {
			d.focus = connectFocusButtons
			return
		}
		d.focus = connectFocusFields
		return
	}
	if d.focus == connectFocusButtons {
		d.focus = connectFocusFields
		return
	}
	d.focus = connectFocusButtons
}

func (d ConnectDialog) fieldValue(id string) string {
	switch id {
	case "name":
		return d.draft.Name
	case "base_url":
		return d.draft.BaseURL
	case "api_key":
		return d.draft.APIKey
	default:
		return ""
	}
}

func (d *ConnectDialog) setFieldValue(id, value string) {
	switch id {
	case "name":
		d.draft.Name = value
	case "base_url":
		d.draft.BaseURL = value
	case "api_key":
		d.draft.APIKey = value
	}
}

func clampWidth(width, minWidth, maxWidth int) int {
	if width <= 0 {
		return maxWidth
	}
	if width < minWidth {
		return minWidth
	}
	if width > maxWidth {
		return maxWidth
	}
	return width
}

func windowBounds(index, total, visible int) (int, int) {
	if visible <= 0 || total <= 0 {
		return 0, 0
	}
	start := 0
	if index >= visible-1 {
		start = index - (visible - 2)
	}
	end := minInt(total, start+visible)
	if end == total && end-start < visible {
		start = maxInt(0, end-visible)
	}
	return start, end
}

func (d *ConnectDialog) updateCurrentEditor(msg ui.KeyMsg) {
	if d.focus != connectFocusFields {
		return
	}
	id := d.currentFieldID()
	if id == "" {
		return
	}
	editor := d.editor(id)
	updated, _ := editor.Update(msg)
	d.storeEditor(id, updated)
	d.setFieldValue(id, updated.Value())
}

func (d ConnectDialog) editor(id string) textarea.Model {
	if d.editors == nil {
		return d.newEditor(id)
	}
	editor, ok := d.editors[id]
	if !ok {
		return d.newEditor(id)
	}
	return editor
}

func (d *ConnectDialog) storeEditor(id string, editor textarea.Model) {
	if d.editors == nil {
		d.editors = map[string]textarea.Model{}
	}
	d.editors[id] = editor
}

func (d ConnectDialog) newEditor(id string) textarea.Model {
	editor := textarea.New()
	editor.BlinkEnabled = false
	editor.Focus()
	editor.SetHeight(1)
	editor.SetWidth(256)
	editor.SetValue(d.fieldValue(id))
	return editor
}

func (d *ConnectDialog) resetEditors() {
	d.editors = map[string]textarea.Model{}
	for _, field := range d.formFields() {
		d.editors[field.ID] = d.newEditor(field.ID)
	}
}

func (d *ConnectDialog) resetCursors() {
	d.resetEditors()
}

func (d *ConnectDialog) moveCursorTo(pos int) {
	id := d.currentFieldID()
	if id == "" {
		return
	}
	editor := d.editor(id)
	editor.SetCursor(pos)
	d.storeEditor(id, editor)
}

func maskVisible(input string) string {
	if strings.TrimSpace(input) == "" {
		return input
	}
	return strings.Repeat("•", len([]rune(input)))
}

func (d ConnectDialog) renderStatusElement(palette theme.Palette) ui.Element {
	status := strings.TrimSpace(d.status)
	if status == "" {
		return ui.Static{}
	}
	label := "WAIT"
	labelColor := palette.ActivityText
	switch d.statusKind {
	case connectStatusSuccess:
		label = "OK"
		labelColor = palette.DiffAddedText
	case connectStatusError:
		label = "ERROR"
		labelColor = palette.DiffDeletedText
	}
	_ = labelColor
	return ui.Static{Content: "[" + label + "] " + status}
}
