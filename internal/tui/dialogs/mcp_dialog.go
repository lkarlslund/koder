package dialogs

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/config"
	kodermcp "github.com/lkarlslund/koder/internal/mcp"
	"github.com/lkarlslund/koder/internal/theme"
	"github.com/lkarlslund/koder/internal/ui"
	"github.com/lkarlslund/koder/internal/ui/textarea"
)

type MCPDialogActionKind int

const (
	MCPDialogActionNone MCPDialogActionKind = iota
	MCPDialogActionCancel
	MCPDialogActionSave
	MCPDialogActionRemove
	MCPDialogActionReconnect
)

type MCPDialogAction struct {
	Kind     MCPDialogActionKind
	ServerID string
	Config   config.MCPServer
}

type mcpDialogMode int

const (
	mcpDialogModeList mcpDialogMode = iota
	mcpDialogModeEdit
	mcpDialogModeDetail
)

type mcpDialogFocus int

const (
	mcpDialogFocusList mcpDialogFocus = iota
	mcpDialogFocusFields
	mcpDialogFocusButtons
)

type MCPDialog struct {
	mode       mcpDialogMode
	focus      mcpDialogFocus
	query      string
	index      int
	servers     []kodermcp.ServerState
	configs     map[string]config.MCPServer
	view        []kodermcp.ServerState
	selectedID  string
	status      string
	editID      string
	editDraft   config.MCPServer
	editors     map[string]textarea.Model
	fieldIndex  int
	buttonIdx   int
	listButtons ui.ButtonRow
	detailBtns  ui.ButtonRow
	editBtns    ui.ButtonRow
}

func NewMCPDialog(servers []kodermcp.ServerState, current map[string]config.MCPServer) MCPDialog {
	d := MCPDialog{
		mode:    mcpDialogModeList,
		focus:   mcpDialogFocusList,
		servers: slices.Clone(servers),
		configs: cloneMCPConfigs(current),
		editors: map[string]textarea.Model{},
		listButtons: ui.ButtonRow{
			Buttons: []ui.Button{
				{ID: "add", Label: "Add", Hotkey: 'a', Primary: true},
				{ID: "edit", Label: "Edit", Hotkey: 'e'},
				{ID: "reconnect", Label: "Reconnect", Hotkey: 'r'},
				{ID: "remove", Label: "Remove", Hotkey: 'x'},
				{ID: "close", Label: "Close", Hotkey: 'c'},
			},
			Align: ui.HorizontalAlignRight,
		},
		detailBtns: ui.ButtonRow{
			Buttons: []ui.Button{
				{ID: "edit", Label: "Edit", Hotkey: 'e', Primary: true},
				{ID: "reconnect", Label: "Reconnect", Hotkey: 'r'},
				{ID: "back", Label: "Back", Hotkey: 'b'},
			},
			Align: ui.HorizontalAlignRight,
		},
		editBtns: ui.ButtonRow{
			Buttons: []ui.Button{
				{ID: "save", Label: "Save", Hotkey: 's', Primary: true},
				{ID: "remove", Label: "Remove", Hotkey: 'x'},
				{ID: "cancel", Label: "Cancel", Hotkey: 'c'},
			},
			Align: ui.HorizontalAlignRight,
		},
	}
	d.mergeConfiguredServers()
	d.refilter()
	return d
}

func (d *MCPDialog) SetServers(servers []kodermcp.ServerState) {
	d.servers = slices.Clone(servers)
	d.mergeConfiguredServers()
	d.refilter()
}

func (d *MCPDialog) SetStatus(status string) {
	d.status = strings.TrimSpace(status)
}

func (d MCPDialog) EditID() string {
	return strings.TrimSpace(d.editID)
}

func (d *MCPDialog) Update(msg ui.KeyMsg) MCPDialogAction {
	switch d.mode {
	case mcpDialogModeList:
		return d.updateList(msg)
	case mcpDialogModeEdit:
		return d.updateEdit(msg)
	case mcpDialogModeDetail:
		return d.updateDetail(msg)
	default:
		return MCPDialogAction{}
	}
}

func (d *MCPDialog) ActivateControl(controlID string) MCPDialogAction {
	switch d.mode {
	case mcpDialogModeList:
		return d.activateListControl(controlID)
	case mcpDialogModeEdit:
		return d.activateEditControl(controlID)
	case mcpDialogModeDetail:
		return d.activateDetailControl(controlID)
	default:
		return MCPDialogAction{}
	}
}

func (d MCPDialog) Measure(ctx *ui.Context, constraints ui.Constraints) ui.Size {
	width := constraints.MaxW
	if width <= 0 {
		width = 104
	}
	return constraints.Clamp(d.dialog(width, ctx.Palette).Measure(ctx, ui.Constraints{MaxW: width, MaxH: constraints.MaxH}))
}

func (d MCPDialog) Surface(ctx *ui.Context, bounds ui.Rect) ui.Surface {
	maxWidth := dialogRenderWidth(bounds, 104)
	node := d.dialog(maxWidth, ctx.Palette)
	size := node.Measure(ctx, ui.Constraints{MaxW: maxWidth, MaxH: bounds.H})
	return ui.PaintNodeSurface(ctx, node, ui.Rect{W: size.W, H: bounds.H})
}

func (d MCPDialog) Paint(ctx *ui.Context, canvas ui.Canvas) {
	if canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	canvas.BlitSurface(0, 0, d.Surface(ctx, ui.Rect{W: canvas.Width(), H: canvas.Height()}))
}

func (d MCPDialog) dialog(width int, palette theme.Palette) ui.Node {
	switch d.mode {
	case mcpDialogModeEdit:
		return d.editDialog(width)
	case mcpDialogModeDetail:
		return d.detailDialog(width, palette)
	default:
		return d.listDialog(width, palette)
	}
}

func (d *MCPDialog) updateList(msg ui.KeyMsg) MCPDialogAction {
	d.bindListButtons()
	if d.listButtons.ActivateHotkey(msg) {
		return d.activateListControl(d.listButtons.Buttons[d.listButtons.Index].ID)
	}
	switch msg.String() {
	case "esc":
		return MCPDialogAction{Kind: MCPDialogActionCancel}
	case "tab":
		d.focus = (d.focus + 1) % 2
	case "shift+tab":
		d.focus--
		if d.focus < 0 {
			d.focus = mcpDialogFocusButtons
		}
	case "enter":
		if d.focus == mcpDialogFocusButtons {
			d.listButtons.ActivateFocused()
			return d.activateListControl(d.listButtons.Buttons[d.listButtons.Index].ID)
		}
		if item, ok := d.current(); ok {
			d.selectedID = item.ID
			d.mode = mcpDialogModeDetail
			d.focus = mcpDialogFocusButtons
			d.buttonIdx = 0
		}
	case "up":
		if d.focus == mcpDialogFocusList {
			d.move(-1)
		}
	case "down":
		if d.focus == mcpDialogFocusList {
			d.move(1)
		}
	case "left":
		if d.focus == mcpDialogFocusButtons {
			d.listButtons.Move(-1)
		}
	case "right":
		if d.focus == mcpDialogFocusButtons {
			d.listButtons.Move(1)
		}
	case "backspace":
		if d.focus == mcpDialogFocusList && d.query != "" {
			d.query = d.query[:len(d.query)-1]
			d.refilter()
		}
	default:
		if d.focus == mcpDialogFocusList && msg.Type == ui.KeyRunes {
			d.query += msg.String()
			d.refilter()
		}
	}
	return MCPDialogAction{}
}

func (d *MCPDialog) updateDetail(msg ui.KeyMsg) MCPDialogAction {
	d.bindDetailButtons()
	if d.detailBtns.ActivateHotkey(msg) {
		return d.activateDetailControl(d.detailBtns.Buttons[d.detailBtns.Index].ID)
	}
	switch msg.String() {
	case "esc":
		d.mode = mcpDialogModeList
		d.focus = mcpDialogFocusList
	case "left":
		d.detailBtns.Move(-1)
	case "right", "tab":
		d.detailBtns.Move(1)
	case "shift+tab":
		d.detailBtns.Move(-1)
	case "enter":
		d.detailBtns.ActivateFocused()
		return d.activateDetailControl(d.detailBtns.Buttons[d.detailBtns.Index].ID)
	}
	return MCPDialogAction{}
}

func (d *MCPDialog) updateEdit(msg ui.KeyMsg) MCPDialogAction {
	d.bindEditButtons()
	if d.editBtns.ActivateHotkey(msg) {
		return d.activateEditControl(d.editBtns.Buttons[d.editBtns.Index].ID)
	}
	switch msg.String() {
	case "esc":
		d.mode = mcpDialogModeList
		d.focus = mcpDialogFocusList
		return MCPDialogAction{}
	case "tab":
		if d.focus == mcpDialogFocusFields {
			d.focus = mcpDialogFocusButtons
		} else {
			d.focus = mcpDialogFocusFields
		}
	case "shift+tab":
		if d.focus == mcpDialogFocusButtons {
			d.focus = mcpDialogFocusFields
		} else {
			d.focus = mcpDialogFocusButtons
		}
	case "up":
		if d.focus == mcpDialogFocusFields && d.fieldIndex > 0 {
			d.fieldIndex--
		}
	case "down":
		if d.focus == mcpDialogFocusFields && d.fieldIndex < len(d.fieldOrder())-1 {
			d.fieldIndex++
		}
	case "left":
		if d.focus == mcpDialogFocusButtons {
			d.editBtns.Move(-1)
		} else {
			d.toggleCurrentBool()
		}
	case "right":
		if d.focus == mcpDialogFocusButtons {
			d.editBtns.Move(1)
		} else {
			d.toggleCurrentBool()
		}
	case "enter":
		if d.focus == mcpDialogFocusButtons {
			d.editBtns.ActivateFocused()
			return d.activateEditControl(d.editBtns.Buttons[d.editBtns.Index].ID)
		}
		d.toggleCurrentBool()
	default:
		if d.focus == mcpDialogFocusFields {
			d.updateCurrentEditor(msg)
		}
	}
	return MCPDialogAction{}
}

func (d *MCPDialog) activateListControl(controlID string) MCPDialogAction {
	switch controlID {
	case "add":
		d.startEdit("", config.MCPServer{})
	case "edit":
		if item, ok := d.current(); ok {
			d.startEdit(item.ID, d.currentConfig(item.ID))
		}
	case "reconnect":
		if item, ok := d.current(); ok {
			return MCPDialogAction{Kind: MCPDialogActionReconnect, ServerID: item.ID}
		}
	case "remove":
		if item, ok := d.current(); ok {
			return MCPDialogAction{Kind: MCPDialogActionRemove, ServerID: item.ID}
		}
	case "close":
		return MCPDialogAction{Kind: MCPDialogActionCancel}
	default:
		if strings.HasPrefix(controlID, "server-row-") {
			idxText := strings.TrimPrefix(controlID, "server-row-")
			for idx, item := range d.view {
				if fmt.Sprintf("%d", idx) == idxText {
					d.index = idx
					d.selectedID = item.ID
					d.mode = mcpDialogModeDetail
					d.focus = mcpDialogFocusButtons
					return MCPDialogAction{}
				}
			}
		}
	}
	return MCPDialogAction{}
}

func (d *MCPDialog) activateDetailControl(controlID string) MCPDialogAction {
	item, ok := d.currentSelected()
	switch controlID {
	case "edit":
		if ok {
			d.startEdit(item.ID, d.currentConfig(item.ID))
		}
	case "reconnect":
		if ok {
			return MCPDialogAction{Kind: MCPDialogActionReconnect, ServerID: item.ID}
		}
	case "back":
		d.mode = mcpDialogModeList
		d.focus = mcpDialogFocusList
	case "window-close":
		return MCPDialogAction{Kind: MCPDialogActionCancel}
	}
	return MCPDialogAction{}
}

func (d *MCPDialog) activateEditControl(controlID string) MCPDialogAction {
	switch controlID {
	case "save":
		return d.saveAction()
	case "remove":
		if strings.TrimSpace(d.editID) != "" {
			return MCPDialogAction{Kind: MCPDialogActionRemove, ServerID: d.editID}
		}
		d.startEdit("", config.MCPServer{})
		d.mode = mcpDialogModeList
		d.focus = mcpDialogFocusList
	case "cancel", "window-close":
		d.mode = mcpDialogModeList
		d.focus = mcpDialogFocusList
	}
	return MCPDialogAction{}
}

func (d MCPDialog) listDialog(width int, palette theme.Palette) ui.Node {
	dialogWidth := maxInt(90, minInt(width, 118))
	contentWidth := dialogWidth - 6
	rows := make([]ui.TableRow, 0, len(d.view))
	for idx, item := range d.view {
		rows = append(rows, ui.TableRow{
			ControlID: "server-row-" + fmt.Sprintf("%d", idx),
			Cells: []string{
				item.ID,
				string(item.Status),
				truncateText(item.URL, maxInt(20, contentWidth-28)),
			},
			Selected: idx == d.index,
			Focused:  idx == d.index && d.focus == mcpDialogFocusList,
		})
	}
	var list ui.Node = staticBlock("No configured MCP servers")
	if len(rows) > 0 {
		list = ui.AsNode(ui.Table{
			Width: contentWidth,
			Columns: []ui.TableColumn{
				{Title: "ID", Width: minInt(24, maxInt(14, contentWidth/4))},
				{Title: "Status", Width: 12},
				{Title: "URL", Width: maxInt(20, contentWidth-minInt(24, maxInt(14, contentWidth/4))-14)},
			},
			Rows:       rows,
			ShowHeader: true,
		})
	}
	buttons := d.listButtons
	buttons.Index = minInt(buttons.Index, len(buttons.Buttons)-1)
	buttons.Width = contentWidth
	details := d.detailText(contentWidth)
	status := "Enter opens the selected server. Tab moves between list and buttons."
	if strings.TrimSpace(d.status) != "" {
		status = d.status
	}
	return ui.AsNode(ui.WindowFrame{
		Title: "MCP Servers",
		Width: dialogWidth,
		Content: ui.AsNode(ui.FlexBox{
			Direction: ui.DirectionVertical,
			Children: []ui.Child{
				ui.Fixed(staticBlock("Filter: " + d.query)),
				ui.Fixed(ui.AsNode(ui.Section{Title: "Servers", Width: contentWidth, Child: list})),
				ui.Fixed(ui.AsNode(ui.Section{Title: "Details", Width: contentWidth, Child: ui.AsNode(ui.TextPane{Content: details})})),
				ui.Fixed(ui.AsNode(buttons)),
				ui.Fixed(staticBlock(status)),
			},
			Spacing: 1,
		}),
		ShowClose: true,
	})
}

func (d MCPDialog) detailDialog(width int, _ theme.Palette) ui.Node {
	dialogWidth := maxInt(90, minInt(width, 120))
	contentWidth := dialogWidth - 6
	buttons := d.detailBtns
	buttons.Width = contentWidth
	return ui.AsNode(ui.WindowFrame{
		Title: "MCP Server",
		Width: dialogWidth,
		Content: ui.AsNode(ui.FlexBox{
			Direction: ui.DirectionVertical,
			Children: []ui.Child{
				ui.Fixed(ui.AsNode(ui.TextPane{Content: d.selectedDetailText(contentWidth)})),
				ui.Fixed(ui.AsNode(buttons)),
			},
			Spacing: 1,
		}),
		ShowClose: true,
	})
}

func (d MCPDialog) editDialog(width int) ui.Node {
	dialogWidth := maxInt(88, minInt(width, 112))
	contentWidth := dialogWidth - 6
	buttons := d.editBtns
	buttons.Width = contentWidth
	status := d.status
	if status == "" {
		status = "Headers use key=value pairs separated by commas."
	}
	return ui.AsNode(ui.WindowFrame{
		Title: "Edit MCP Server",
		Width: dialogWidth,
		Content: ui.AsNode(ui.FlexBox{
			Direction: ui.DirectionVertical,
			Children: []ui.Child{
				ui.Fixed(d.editFields(contentWidth)),
				ui.Fixed(ui.AsNode(buttons)),
				ui.Fixed(staticBlock(status)),
			},
			Spacing: 1,
		}),
		ShowClose: true,
	})
}

func (d *MCPDialog) mergeConfiguredServers() {
	seen := make(map[string]struct{}, len(d.servers))
	for _, item := range d.servers {
		seen[item.ID] = struct{}{}
	}
	for id, cfg := range d.configs {
		if _, ok := seen[id]; ok {
			continue
		}
		status := kodermcp.ServerStatusDisconnected
		if cfg.Disabled {
			status = kodermcp.ServerStatusDisabled
		}
		d.servers = append(d.servers, kodermcp.ServerState{
			ID:       id,
			Name:     cfg.Name,
			URL:      cfg.URL,
			Status:   status,
			Disabled: cfg.Disabled,
		})
	}
	slices.SortFunc(d.servers, func(a, b kodermcp.ServerState) int {
		return strings.Compare(a.ID, b.ID)
	})
}

func (d *MCPDialog) refilter() {
	query := strings.ToLower(strings.TrimSpace(d.query))
	d.view = d.view[:0]
	for _, item := range d.servers {
		hay := strings.ToLower(strings.Join([]string{item.ID, item.Name, item.URL, string(item.Status)}, " "))
		if query == "" || strings.Contains(hay, query) {
			d.view = append(d.view, item)
		}
	}
	if d.index >= len(d.view) {
		d.index = maxInt(0, len(d.view)-1)
	}
}

func (d *MCPDialog) move(delta int) {
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

func (d *MCPDialog) current() (kodermcp.ServerState, bool) {
	if d.index < 0 || d.index >= len(d.view) {
		return kodermcp.ServerState{}, false
	}
	return d.view[d.index], true
}

func (d *MCPDialog) currentSelected() (kodermcp.ServerState, bool) {
	for _, item := range d.servers {
		if item.ID == d.selectedID {
			return item, true
		}
	}
	return kodermcp.ServerState{}, false
}

func (d *MCPDialog) currentConfig(id string) config.MCPServer {
	if cfg, ok := d.configs[id]; ok {
		return cloneMCPConfig(cfg)
	}
	return config.MCPServer{}
}

func (d *MCPDialog) startEdit(id string, cfg config.MCPServer) {
	d.mode = mcpDialogModeEdit
	d.focus = mcpDialogFocusFields
	d.editID = id
	d.editDraft = cloneMCPConfig(cfg)
	d.buttonIdx = 0
	d.fieldIndex = 0
	d.status = ""
	d.editors = map[string]textarea.Model{
		"id":               newMCPDialogEditor(id),
		"name":             newMCPDialogEditor(cfg.Name),
		"url":              newMCPDialogEditor(cfg.URL),
		"bearer_token":     newMCPDialogEditor(cfg.BearerToken),
		"bearer_token_env": newMCPDialogEditor(cfg.BearerTokenEnv),
		"headers":          newMCPDialogEditor(formatHeaders(cfg.Headers)),
		"startup_timeout":  newMCPDialogEditor(formatDuration(cfg.StartupTimeout)),
		"request_timeout":  newMCPDialogEditor(formatDuration(cfg.RequestTimeout)),
	}
}

func (d *MCPDialog) saveAction() MCPDialogAction {
	id := strings.TrimSpace(d.editorValue("id"))
	startup, err := parseOptionalDuration(d.editorValue("startup_timeout"))
	if err != nil {
		d.status = "Invalid startup timeout: " + err.Error()
		return MCPDialogAction{}
	}
	request, err := parseOptionalDuration(d.editorValue("request_timeout"))
	if err != nil {
		d.status = "Invalid request timeout: " + err.Error()
		return MCPDialogAction{}
	}
	headers, err := parseHeaders(d.editorValue("headers"))
	if err != nil {
		d.status = "Invalid headers: " + err.Error()
		return MCPDialogAction{}
	}
	cfg := config.MCPServer{
		Name:                 strings.TrimSpace(d.editorValue("name")),
		URL:                  strings.TrimSpace(d.editorValue("url")),
		BearerToken:          strings.TrimSpace(d.editorValue("bearer_token")),
		BearerTokenEnv:       strings.TrimSpace(d.editorValue("bearer_token_env")),
		Headers:              headers,
		StartupTimeout:       startup,
		RequestTimeout:       request,
		DisableStandaloneSSE: d.editDraft.DisableStandaloneSSE,
		Disabled:             d.editDraft.Disabled,
	}
	return MCPDialogAction{Kind: MCPDialogActionSave, ServerID: id, Config: cfg}
}

func (d *MCPDialog) toggleCurrentBool() {
	if d.focus != mcpDialogFocusFields {
		return
	}
	switch d.fieldOrder()[d.fieldIndex] {
	case "disable_sse":
		d.editDraft.DisableStandaloneSSE = !d.editDraft.DisableStandaloneSSE
	case "disabled":
		d.editDraft.Disabled = !d.editDraft.Disabled
	}
}

func (d *MCPDialog) updateCurrentEditor(msg ui.KeyMsg) {
	key := d.fieldOrder()[d.fieldIndex]
	editor, ok := d.editors[key]
	if !ok {
		return
	}
	var cmd ui.Cmd
	editor, cmd = editor.Update(msg)
	_ = cmd
	d.editors[key] = editor
}

func (d *MCPDialog) bindListButtons() {
	d.listButtons.Buttons[0].OnClick = func() {}
	d.listButtons.Buttons[1].OnClick = func() {}
	d.listButtons.Buttons[2].OnClick = func() {}
	d.listButtons.Buttons[3].OnClick = func() {}
	d.listButtons.Buttons[4].OnClick = func() {}
}

func (d *MCPDialog) bindDetailButtons() {
	d.detailBtns.Buttons[0].OnClick = func() {}
	d.detailBtns.Buttons[1].OnClick = func() {}
	d.detailBtns.Buttons[2].OnClick = func() {}
}

func (d *MCPDialog) bindEditButtons() {
	d.editBtns.Buttons[0].OnClick = func() {}
	d.editBtns.Buttons[1].OnClick = func() {}
	d.editBtns.Buttons[2].OnClick = func() {}
}

func (d *MCPDialog) editorValue(key string) string {
	editor, ok := d.editors[key]
	if !ok {
		return ""
	}
	return editor.Value()
}

func (d *MCPDialog) fieldOrder() []string {
	return []string{
		"id",
		"name",
		"url",
		"bearer_token",
		"bearer_token_env",
		"headers",
		"startup_timeout",
		"request_timeout",
		"disable_sse",
		"disabled",
	}
}

func (d MCPDialog) editFields(width int) ui.Node {
	order := d.fieldOrder()
	children := make([]ui.Child, 0, len(order))
	for idx, key := range order {
		children = append(children, ui.Fixed(staticBlock(d.fieldLabel(key)+": "+blankAsDash(d.fieldValue(key)))))
		if idx == d.fieldIndex && d.focus == mcpDialogFocusFields {
			children = append(children, ui.Fixed(staticBlock("  ^ focused")))
		}
	}
	return ui.AsNode(ui.Section{Width: width, Child: ui.AsNode(ui.FlexBox{Direction: ui.DirectionVertical, Children: children, Spacing: 0})})
}

func (d MCPDialog) fieldLabel(key string) string {
	switch key {
	case "id":
		return "ID"
	case "name":
		return "Name"
	case "url":
		return "URL"
	case "bearer_token":
		return "Bearer Token"
	case "bearer_token_env":
		return "Bearer Token Env"
	case "headers":
		return "Headers"
	case "startup_timeout":
		return "Startup Timeout"
	case "request_timeout":
		return "Request Timeout"
	case "disable_sse":
		return "Disable SSE"
	case "disabled":
		return "Disabled"
	default:
		return key
	}
}

func (d MCPDialog) fieldValue(key string) string {
	switch key {
	case "disable_sse":
		return yesNo(d.editDraft.DisableStandaloneSSE)
	case "disabled":
		return yesNo(d.editDraft.Disabled)
	default:
		return d.editorValue(key)
	}
}

func (d MCPDialog) detailText(width int) string {
	item, ok := d.current()
	if !ok {
		return "No server selected"
	}
	return serverDetailText(item, width)
}

func (d MCPDialog) selectedDetailText(width int) string {
	item, ok := d.currentSelected()
	if !ok {
		return "No server selected"
	}
	return serverDetailText(item, width)
}

func serverDetailText(item kodermcp.ServerState, width int) string {
	lines := []string{
		fmt.Sprintf("ID: %s", item.ID),
		fmt.Sprintf("Name: %s", blankAsDash(item.Name)),
		fmt.Sprintf("URL: %s", blankAsDash(item.URL)),
		fmt.Sprintf("Status: %s", item.Status),
		fmt.Sprintf("Session: %s", blankAsDash(item.SessionID)),
		fmt.Sprintf("Tools: %d  Resources: %d  Templates: %d  Prompts: %d", item.ToolCount, item.ResourceCount, item.ResourceTemplateCount, item.PromptCount),
	}
	if strings.TrimSpace(item.LastError) != "" {
		lines = append(lines, "Error: "+item.LastError)
	}
	if strings.TrimSpace(item.ServerInstructions) != "" {
		lines = append(lines, "", "Instructions", wrapPlain(item.ServerInstructions, maxInt(20, width-4)))
	}
	if len(item.Tools) > 0 {
		lines = append(lines, "", "Tools")
		for _, tool := range item.Tools {
			lines = append(lines, "- "+blankAsDash(coalesce(tool.Title, tool.Name)))
		}
	}
	if len(item.Resources) > 0 {
		lines = append(lines, "", "Resources")
		for _, resource := range item.Resources {
			lines = append(lines, "- "+blankAsDash(coalesce(resource.Title, resource.URI)))
		}
	}
	if len(item.ResourceTemplates) > 0 {
		lines = append(lines, "", "Resource Templates")
		for _, resource := range item.ResourceTemplates {
			lines = append(lines, "- "+blankAsDash(coalesce(resource.Title, resource.URITemplate)))
		}
	}
	if len(item.Prompts) > 0 {
		lines = append(lines, "", "Prompts")
		for _, prompt := range item.Prompts {
			lines = append(lines, "- "+blankAsDash(coalesce(prompt.Title, prompt.Name)))
		}
	}
	return strings.Join(lines, "\n")
}

func newMCPDialogEditor(value string) textarea.Model {
	editor := textarea.New()
	editor.BlinkEnabled = false
	editor.Focus()
	editor.SetHeight(1)
	editor.SetWidth(32)
	editor.SetValue(value)
	return editor
}

func blankAsDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return strings.TrimSpace(value)
}

func formatHeaders(headers map[string]string) string {
	if len(headers) == 0 {
		return ""
	}
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+headers[key])
	}
	return strings.Join(parts, ", ")
}

func parseHeaders(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]string{}, nil
	}
	out := map[string]string{}
	for _, item := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == '\n' }) {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		key, value, ok := strings.Cut(item, "=")
		if !ok {
			return nil, fmt.Errorf("expected key=value, got %q", item)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" {
			return nil, fmt.Errorf("header key is empty in %q", item)
		}
		out[key] = value
	}
	return out, nil
}

func parseOptionalDuration(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	return time.ParseDuration(raw)
}

func formatDuration(value time.Duration) string {
	if value <= 0 {
		return ""
	}
	return value.String()
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func coalesce(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func cloneMCPConfigs(src map[string]config.MCPServer) map[string]config.MCPServer {
	if len(src) == 0 {
		return map[string]config.MCPServer{}
	}
	dst := make(map[string]config.MCPServer, len(src))
	for id, cfg := range src {
		dst[id] = cloneMCPConfig(cfg)
	}
	return dst
}

func cloneMCPConfig(cfg config.MCPServer) config.MCPServer {
	cfg.Headers = cloneStringMap(cfg.Headers)
	return cfg
}

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return map[string]string{}
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}
