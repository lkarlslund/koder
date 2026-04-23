package dialogs

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/theme"
	. "github.com/lkarlslund/koder/internal/ui"
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
	models     []string
	status     string
	statusKind connectStatusKind
	focus      connectFocus
	fieldIndex int
	buttonIdx  int
	cursors    map[string]int
}

func NewConnectDialog(items []provider.Descriptor, configured map[string]config.Provider) ConnectDialog {
	dialog := ConnectDialog{
		stage:      connectStageProvider,
		items:      items,
		configured: configured,
		cursors:    map[string]int{},
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

func (d *ConnectDialog) SetModels(models []string) {
	d.models = append(d.models[:0], models...)
	if strings.TrimSpace(d.draft.Model) == "" && len(models) > 0 {
		d.draft.Model = models[0]
	}
}

func (d ConnectDialog) Models() []string {
	out := make([]string, len(d.models))
	copy(out, d.models)
	return out
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
	dialogWidth := dialogRenderWidth(Rect{W: width}, 88)
	return RenderElement(&Context{Palette: palette}, d.dialog(dialogWidth, palette), dialogWidth, 0)
}

func (d ConnectDialog) Measure(ctx *Context, constraints Constraints) Size {
	return dialogMeasureElement(ctx, constraints, 88, d.dialog)
}

func (d ConnectDialog) Render(ctx *Context, bounds Rect) Surface {
	return dialogRenderElement(ctx, bounds, 88, d.dialog)
}

func (d ConnectDialog) dialog(width int, palette theme.Palette) Element {
	switch d.stage {
	case connectStageProvider:
		return d.providerListDialog(width, palette)
	case connectStageAuth:
		return d.authPickerDialog(width, palette)
	case connectStageForm:
		return d.formDialog(width, palette)
	default:
		return Static{}
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
			d.moveCursor(-1)
		}
	case "right":
		if d.focus == connectFocusButtons {
			d.moveButtons(1)
		} else {
			d.moveCursor(1)
		}
	case "home", "ctrl+a":
		d.moveCursorTo(0)
	case "end", "ctrl+e":
		d.moveCursorTo(len([]rune(d.fieldValue(d.currentFieldID()))))
	case "backspace":
		d.deleteRune()
	case "ctrl+t":
		return ProviderConnectAction{Kind: ProviderConnectActionTest, Draft: d.draft}
	case "alt+t":
		return ProviderConnectAction{Kind: ProviderConnectActionTest, Draft: d.draft}
	case "alt+s":
		return ProviderConnectAction{Kind: ProviderConnectActionSave, Draft: d.draft}
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

func (d *ConnectDialog) providerListDialog(width int, palette theme.Palette) Element {
	dialogWidth := clampWidth(width, 72, 96)
	lines := []string{fmt.Sprintf("Filter: %s", d.query), ""}
	var list Element = staticBlock("No providers match your filter.")
	if len(d.view) > 0 {
		start, end := windowBounds(d.index, len(d.view), 10)
		items := make([]ListItem, 0, end-start)
		for idx := start; idx < end; idx++ {
			item := d.view[idx]
			tertiary := "remote"
			if _, ok := d.configured[item.ID]; ok {
				tertiary = "configured"
			} else if item.Local {
				tertiary = "local"
			}
			items = append(items, ListItem{
				Primary:   item.Title,
				Secondary: item.Description,
				Tertiary:  tertiary,
			})
		}
		list = Section{
			Width: dialogWidth - 8,
			Child: List{
				Items:    items,
				Width:    dialogWidth - 8,
				Selected: d.index - start,
				Focused:  d.stage == connectStageProvider,
			},
		}
	}
	body := []Child{
		Fixed(linesBlock(lines...)),
		Fixed(list),
	}
	if status := strings.TrimSpace(d.status); status != "" {
		body = append(body, Fixed(Spacer{H: 1}), Fixed(staticBlock(lipgloss.NewStyle().Foreground(palette.AssistantTimestampText).Render(status))))
	}
	return Dialog{
		Title:  "Connect Provider",
		Body:   Column{Children: body, Spacing: 1},
		Footer: "Enter choose provider  Esc cancel",
		Width:  dialogWidth,
	}
}

func (d *ConnectDialog) authPickerDialog(width int, palette theme.Palette) Element {
	dialogWidth := clampWidth(width, 68, 88)
	lines := []string{
		lipgloss.NewStyle().Bold(true).Render(d.selected.Title),
		lipgloss.NewStyle().Foreground(palette.AssistantTimestampText).Render(d.selected.Description),
		"",
	}
	items := make([]ListItem, 0, len(d.selected.AuthMethods))
	for _, method := range d.selected.AuthMethods {
		items = append(items, ListItem{
			Primary:   method.Title,
			Secondary: method.Description,
		})
	}
	return Dialog{
		Title:  "Choose Auth Method",
		Body: Column{
			Children: []Child{
				Fixed(linesBlock(lines...)),
				Fixed(Section{
					Width: dialogWidth - 8,
					Child: List{
						Items:    items,
						Width:    dialogWidth - 8,
						Selected: d.authIndex,
						Focused:  d.stage == connectStageAuth,
					},
				}),
			},
			Spacing: 1,
		},
		Footer: "Enter continue  Esc back",
		Width:  dialogWidth,
	}
}

func (d *ConnectDialog) formDialog(width int, palette theme.Palette) Element {
	dialogWidth := clampWidth(width, 76, 100)
	lines := []string{
		lipgloss.NewStyle().Bold(true).Render(d.selected.Title),
		lipgloss.NewStyle().Foreground(palette.AssistantTimestampText).Render(d.selected.Description),
		"",
	}
	for idx, field := range d.formFields() {
		active := d.focus == connectFocusFields && d.fieldIndex == idx
		row := d.renderFormField(field, dialogWidth-8, palette, active)
		lines = append(lines, row)
	}
	if len(d.models) > 0 {
		lines = append(lines, lipgloss.NewStyle().Foreground(palette.AssistantTimestampText).Render("Discovered models: "+strings.Join(d.models[:minInt(4, len(d.models))], ", ")))
	}
	if status := strings.TrimSpace(d.status); status != "" {
		lines = append(lines, d.renderStatus(palette))
	}
	buttons := ButtonRow{
		Buttons: []Button{
			{Label: "Test", Hotkey: 't', Focused: d.focus == connectFocusButtons && d.buttonIdx == 0},
			{Label: "Save", Hotkey: 's', Focused: d.focus == connectFocusButtons && d.buttonIdx == 1, Primary: true},
			{Label: "Cancel", Focused: d.focus == connectFocusButtons && d.buttonIdx == 2},
		},
		Align: HorizontalAlignRight,
		Width: dialogWidth - 4,
	}
	return Dialog{
		Title:   "Connect Provider",
		Body:    linesBlock(lines...),
		Buttons: buttons,
		Footer:  "Type to edit  Ctrl+T test  Enter select  Esc cancel",
		Width:   dialogWidth,
	}
}

func (d ConnectDialog) renderFormField(field connectField, width int, palette theme.Palette, active bool) string {
	if active {
		return d.renderEditorValue(field.ID, field.Label, field.Description, width, palette)
	}
	return SelectableRow{
		Primary:   field.Label,
		Secondary: field.Description,
		Tertiary:  d.displayValue(field.ID),
		Width:     width,
	}.View(palette)
}

func (d ConnectDialog) renderEditorValue(fieldID string, label string, description string, width int, palette theme.Palette) string {
	value := d.fieldValue(fieldID)
	placeholder := d.placeholderValue(fieldID)
	labelWidth := minInt(20, maxInt(10, width/4))
	valueWidth := minInt(22, maxInt(10, width/4))
	descWidth := maxInt(8, width-labelWidth-valueWidth-4)
	content := d.renderEditorContent(fieldID, value, placeholder, valueWidth)
	line := lipgloss.JoinHorizontal(
		lipgloss.Top,
		lipgloss.NewStyle().Width(labelWidth).Bold(true).Render(truncateText(label, labelWidth)),
		lipgloss.NewStyle().Width(2).Render(""),
		lipgloss.NewStyle().Width(descWidth).Foreground(palette.AssistantTimestampText).Render(truncateText(description, descWidth)),
		lipgloss.NewStyle().Width(2).Render(""),
		lipgloss.NewStyle().Width(valueWidth).Render(content),
	)
	style := lipgloss.NewStyle().
		Width(width).
		Background(palette.UserTextBackground).
		Foreground(palette.UserTextForeground)
	if strings.TrimSpace(value) == "" && placeholder != "" {
		style = style.Foreground(palette.ComposerMutedText)
	}
	return style.Render(" " + line)
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
	d.statusKind = connectStatusNone
	d.draft, _ = provider.BuildDraft(item.ID, d.configured)
	d.resetCursors()
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
	d.resetCursors()
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
	id := d.currentFieldID()
	value := []rune(d.fieldValue(id))
	cursor := d.cursorPosition(id)
	if len(value) == 0 || cursor <= 0 {
		return
	}
	next := append([]rune{}, value[:cursor-1]...)
	next = append(next, value[cursor:]...)
	d.setFieldValue(id, string(next))
	d.moveCursorTo(cursor - 1)
}

func (d *ConnectDialog) appendText(input string) {
	if d.focus != connectFocusFields {
		return
	}
	id := d.currentFieldID()
	if id == "" {
		return
	}
	current := []rune(d.fieldValue(id))
	insert := []rune(input)
	cursor := d.cursorPosition(id)
	if cursor > len(current) {
		cursor = len(current)
	}
	next := append([]rune{}, current[:cursor]...)
	next = append(next, insert...)
	next = append(next, current[cursor:]...)
	d.setFieldValue(id, string(next))
	d.moveCursorTo(cursor + len(insert))
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
	d.clampCursor(id)
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

func (d *ConnectDialog) moveCursor(delta int) {
	id := d.currentFieldID()
	if id == "" {
		return
	}
	d.moveCursorTo(d.cursorPosition(id) + delta)
}

func (d *ConnectDialog) moveCursorTo(pos int) {
	id := d.currentFieldID()
	if id == "" {
		return
	}
	if d.cursors == nil {
		d.cursors = map[string]int{}
	}
	if pos < 0 {
		pos = 0
	}
	maxPos := len([]rune(d.fieldValue(id)))
	if pos > maxPos {
		pos = maxPos
	}
	d.cursors[id] = pos
}

func (d ConnectDialog) cursorPosition(id string) int {
	if d.cursors == nil {
		return len([]rune(d.fieldValue(id)))
	}
	pos, ok := d.cursors[id]
	if !ok {
		return len([]rune(d.fieldValue(id)))
	}
	maxPos := len([]rune(d.fieldValue(id)))
	if pos > maxPos {
		return maxPos
	}
	if pos < 0 {
		return 0
	}
	return pos
}

func (d *ConnectDialog) clampCursor(id string) {
	if d.cursors == nil {
		return
	}
	maxPos := len([]rune(d.fieldValue(id)))
	if d.cursors[id] > maxPos {
		d.cursors[id] = maxPos
	}
	if d.cursors[id] < 0 {
		d.cursors[id] = 0
	}
}

func (d *ConnectDialog) resetCursors() {
	d.cursors = map[string]int{}
	for _, field := range d.formFields() {
		d.cursors[field.ID] = len([]rune(d.fieldValue(field.ID)))
	}
}

func (d ConnectDialog) renderEditorContent(fieldID, value, placeholder string, width int) string {
	if width <= 0 {
		return ""
	}
	displayRunes := []rune(value)
	if fieldID == "api_key" {
		displayRunes = []rune(strings.Repeat("•", len([]rune(value))))
	}
	cursor := d.cursorPosition(fieldID)
	if strings.TrimSpace(value) == "" && placeholder != "" {
		text := truncateText(placeholder, maxInt(1, width-1))
		return padRight(text, width-1)
	}
	if cursor > len(displayRunes) {
		cursor = len(displayRunes)
	}
	available := maxInt(1, width-1)
	start := 0
	if cursor > available {
		start = cursor - available
	}
	if start > 0 {
		start--
	}
	end := minInt(len(displayRunes), start+available)
	segment := string(displayRunes[start:end])
	cursorCol := cursor - start
	if start > 0 {
		segment = "…" + string(displayRunes[start+1:end])
		cursorCol = maxInt(0, cursorCol-1)
	}
	segmentRunes := []rune(segment)
	if cursorCol > len(segmentRunes) {
		cursorCol = len(segmentRunes)
	}
	before := string(segmentRunes[:cursorCol])
	after := string(segmentRunes[cursorCol:])
	content := before + "█" + after
	return padRight(content, width)
}

func (d ConnectDialog) renderStatus(palette theme.Palette) string {
	status := strings.TrimSpace(d.status)
	if status == "" {
		return ""
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
	tag := lipgloss.NewStyle().
		Bold(true).
		Foreground(labelColor).
		Background(palette.UserTextBackground).
		Padding(0, 1).
		Render(label)
	body := lipgloss.NewStyle().
		Foreground(palette.SidebarForeground).
		Background(palette.UserTextBackground).
		Padding(0, 1).
		Render(status)
	return lipgloss.JoinHorizontal(lipgloss.Left, tag, " ", body)
}

func padRight(input string, width int) string {
	got := lipgloss.Width(input)
	if got >= width {
		return truncateText(input, width)
	}
	return input + strings.Repeat(" ", width-got)
}
