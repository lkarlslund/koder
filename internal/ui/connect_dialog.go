package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/theme"
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
	models     []string
	status     string
	focus      connectFocus
	fieldIndex int
	buttonIdx  int
}

func NewConnectDialog(items []provider.Descriptor, configured map[string]config.Provider) ConnectDialog {
	dialog := ConnectDialog{
		stage:      connectStageProvider,
		items:      items,
		configured: configured,
	}
	dialog.refilter()
	return dialog
}

func (d *ConnectDialog) SetStatus(status string) {
	d.status = strings.TrimSpace(status)
}

func (d *ConnectDialog) SetModels(models []string) {
	d.models = append(d.models[:0], models...)
	if strings.TrimSpace(d.draft.Model) == "" && len(models) > 0 {
		d.draft.Model = models[0]
	}
}

func (d *ConnectDialog) Update(msg tea.KeyMsg) ProviderConnectAction {
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

func (d ConnectDialog) View(width int, palette theme.Palette) string {
	switch d.stage {
	case connectStageProvider:
		return d.providerListView(width, palette)
	case connectStageAuth:
		return d.authPickerView(width, palette)
	case connectStageForm:
		return d.formView(width, palette)
	default:
		return ""
	}
}

func (d *ConnectDialog) updateProviderList(msg tea.KeyMsg) ProviderConnectAction {
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
		if msg.Type == tea.KeyRunes {
			d.query += msg.String()
			d.refilter()
		}
	}
	return ProviderConnectAction{}
}

func (d *ConnectDialog) updateAuthPicker(msg tea.KeyMsg) ProviderConnectAction {
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

func (d *ConnectDialog) updateForm(msg tea.KeyMsg) ProviderConnectAction {
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
			d.adjustModel(-1)
		}
	case "right":
		if d.focus == connectFocusButtons {
			d.moveButtons(1)
		} else {
			d.adjustModel(1)
		}
	case "backspace":
		d.deleteRune()
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
		if d.currentFieldID() == "model" {
			d.adjustModel(1)
		}
	default:
		if msg.Type == tea.KeyRunes {
			d.appendText(msg.String())
		}
	}
	return ProviderConnectAction{}
}

func (d *ConnectDialog) providerListView(width int, palette theme.Palette) string {
	dialogWidth := clampWidth(width, 72, 96)
	lines := []string{fmt.Sprintf("Filter: %s", d.query), ""}
	if len(d.view) == 0 {
		lines = append(lines, "No providers match your filter.")
	} else {
		start, end := windowBounds(d.index, len(d.view), 10)
		for idx := start; idx < end; idx++ {
			item := d.view[idx]
			prefix := "  "
			if _, ok := d.configured[item.ID]; ok {
				prefix = "✓ "
			}
			if item.Local {
				prefix = "⌂ "
			}
			line := fmt.Sprintf("%s%s", prefix, item.Title)
			desc := lipgloss.NewStyle().Foreground(palette.AssistantTimestampText).Render(item.Description)
			row := strings.Join([]string{line, "  " + desc}, "\n")
			if idx == d.index {
				row = lipgloss.NewStyle().Background(palette.UserTextBackground).Foreground(palette.UserTextForeground).Render(row)
			}
			lines = append(lines, row, "")
		}
	}
	if status := strings.TrimSpace(d.status); status != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(palette.AssistantTimestampText).Render(status))
	}
	return Modal{
		Title:  "Connect Provider",
		Body:   strings.TrimRight(strings.Join(lines, "\n"), "\n"),
		Footer: "Enter choose provider  Esc cancel",
		Width:  dialogWidth,
	}.View(palette)
}

func (d *ConnectDialog) authPickerView(width int, palette theme.Palette) string {
	dialogWidth := clampWidth(width, 68, 88)
	lines := []string{
		lipgloss.NewStyle().Bold(true).Render(d.selected.Title),
		lipgloss.NewStyle().Foreground(palette.AssistantTimestampText).Render(d.selected.Description),
		"",
	}
	for idx, method := range d.selected.AuthMethods {
		row := lipgloss.JoinVertical(lipgloss.Left,
			method.Title,
			lipgloss.NewStyle().Foreground(palette.AssistantTimestampText).Render(method.Description),
		)
		if idx == d.authIndex {
			row = lipgloss.NewStyle().Background(palette.UserTextBackground).Foreground(palette.UserTextForeground).Render(row)
		}
		lines = append(lines, row, "")
	}
	return Modal{
		Title:  "Choose Auth Method",
		Body:   strings.TrimRight(strings.Join(lines, "\n"), "\n"),
		Footer: "Enter continue  Esc back",
		Width:  dialogWidth,
	}.View(palette)
}

func (d *ConnectDialog) formView(width int, palette theme.Palette) string {
	dialogWidth := clampWidth(width, 76, 100)
	lines := []string{
		lipgloss.NewStyle().Bold(true).Render(d.selected.Title),
		lipgloss.NewStyle().Foreground(palette.AssistantTimestampText).Render(d.selected.Description),
		"",
	}
	for idx, field := range d.formFields() {
		active := d.focus == connectFocusFields && d.fieldIndex == idx
		row := d.renderFormField(field, dialogWidth-8, palette, active)
		lines = append(lines, row, "")
	}
	if len(d.models) > 0 {
		lines = append(lines, lipgloss.NewStyle().Foreground(palette.AssistantTimestampText).Render("Discovered models: "+strings.Join(d.models[:minInt(4, len(d.models))], ", ")))
		lines = append(lines, "")
	}
	if status := strings.TrimSpace(d.status); status != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(palette.AssistantTimestampText).Render(status))
		lines = append(lines, "")
	}
	buttons := []string{
		Button{Label: "Test", Focused: d.focus == connectFocusButtons && d.buttonIdx == 0}.View(palette),
		Button{Label: "Save", Focused: d.focus == connectFocusButtons && d.buttonIdx == 1, Primary: true}.View(palette),
		Button{Label: "Cancel", Focused: d.focus == connectFocusButtons && d.buttonIdx == 2}.View(palette),
	}
	lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Left, buttons...))
	return Modal{
		Title:  "Connect Provider",
		Body:   strings.TrimRight(strings.Join(lines, "\n"), "\n"),
		Footer: "Type to edit  Ctrl+T test  Enter select  Esc cancel",
		Width:  dialogWidth,
	}.View(palette)
}

func (d ConnectDialog) renderFormField(field connectField, width int, palette theme.Palette, active bool) string {
	label := lipgloss.NewStyle().Bold(true).Render(field.Label)
	desc := lipgloss.NewStyle().
		Width(width).
		Foreground(palette.AssistantTimestampText).
		Render(truncateText(field.Description, width))

	value := d.fieldValue(field.ID)
	if active {
		return strings.Join([]string{
			label,
			desc,
			d.renderEditorValue(field.ID, width, palette),
		}, "\n")
	}

	display := d.displayValue(field.ID)
	row := lipgloss.JoinHorizontal(
		lipgloss.Top,
		lipgloss.NewStyle().Width(maxInt(12, width-18)).Bold(true).Render(truncateText(field.Label, maxInt(12, width-18))),
		lipgloss.NewStyle().Foreground(palette.ActivityText).Render(truncateText(display, 16)),
	)
	if strings.TrimSpace(value) == "" {
		row = lipgloss.JoinHorizontal(
			lipgloss.Top,
			lipgloss.NewStyle().Width(maxInt(12, width-18)).Bold(true).Render(truncateText(field.Label, maxInt(12, width-18))),
			lipgloss.NewStyle().Foreground(palette.AssistantTimestampText).Render(truncateText(display, 16)),
		)
	}
	return strings.Join([]string{row, desc}, "\n")
}

func (d ConnectDialog) renderEditorValue(fieldID string, width int, palette theme.Palette) string {
	value := d.fieldValue(fieldID)
	if fieldID == "api_key" {
		value = strings.Repeat("•", len([]rune(value)))
	}
	placeholder := d.placeholderValue(fieldID)
	editorWidth := maxInt(12, width)
	content := fitEditorTail(value, placeholder, editorWidth-3)
	line := " " + content + " "
	style := lipgloss.NewStyle().
		Width(editorWidth).
		Background(palette.UserTextBackground).
		Foreground(palette.UserTextForeground)
	if strings.TrimSpace(value) == "" {
		style = style.Foreground(palette.ComposerMutedText)
	}
	return style.Render(line)
}

func (d ConnectDialog) displayValue(fieldID string) string {
	value := d.fieldValue(fieldID)
	if fieldID == "api_key" {
		if value == "" {
			return "(required)"
		}
		return strings.Repeat("•", minVisibleRunes(len([]rune(value)), 12))
	}
	if fieldID == "model" && strings.TrimSpace(value) == "" {
		return "(set a model)"
	}
	if strings.TrimSpace(value) == "" {
		return "(empty)"
	}
	return value
}

func (d ConnectDialog) placeholderValue(fieldID string) string {
	switch fieldID {
	case "api_key":
		return "(required)"
	case "model":
		return "(set a model)"
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
	d.models = nil
	d.status = ""
	d.draft, _ = provider.BuildDraft(item.ID, d.configured)
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
	fields = append(fields, connectField{ID: "model", Label: "Model", Description: "Default model used for new sessions"})
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

func (d *ConnectDialog) deleteRune() {
	if d.focus != connectFocusFields {
		return
	}
	value := []rune(d.fieldValue(d.currentFieldID()))
	if len(value) == 0 {
		return
	}
	d.setFieldValue(d.currentFieldID(), string(value[:len(value)-1]))
}

func (d *ConnectDialog) appendText(input string) {
	if d.focus != connectFocusFields {
		return
	}
	id := d.currentFieldID()
	if id == "" {
		return
	}
	d.setFieldValue(id, d.fieldValue(id)+input)
}

func (d *ConnectDialog) adjustModel(delta int) {
	if d.currentFieldID() != "model" || len(d.models) == 0 {
		return
	}
	current := strings.TrimSpace(d.draft.Model)
	idx := 0
	for i, item := range d.models {
		if item == current {
			idx = i
			break
		}
	}
	idx += delta
	if idx < 0 {
		idx = len(d.models) - 1
	}
	if idx >= len(d.models) {
		idx = 0
	}
	d.draft.Model = d.models[idx]
}

func (d ConnectDialog) fieldValue(id string) string {
	switch id {
	case "name":
		return d.draft.Name
	case "base_url":
		return d.draft.BaseURL
	case "api_key":
		return d.draft.APIKey
	case "model":
		return d.draft.Model
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
	case "model":
		d.draft.Model = value
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
	start := 0
	if index >= visible-1 {
		start = index - (visible - 2)
	}
	end := minInt(total, start+visible)
	return start, end
}

func minVisibleRunes(value, max int) int {
	if value < max {
		return value
	}
	return max
}

func fitEditorTail(value, placeholder string, width int) string {
	if width <= 0 {
		return ""
	}
	if strings.TrimSpace(value) == "" {
		if lipgloss.Width(placeholder) <= width {
			return placeholder
		}
		return truncateText(placeholder, width)
	}
	runes := []rune(value)
	if len(runes) >= width {
		return "…" + string(runes[len(runes)-maxInt(1, width-1):]) + "█"
	}
	return value + "█"
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
