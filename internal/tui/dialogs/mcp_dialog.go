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

type MCPDialog struct {
	servers   []kodermcp.ServerState
	configs   map[string]config.MCPServer
	list      EntityListDialog
	editor    *LabeledFormDialog
	editID    string
	status    string
	selected  string
}

func NewMCPDialog(servers []kodermcp.ServerState, current map[string]config.MCPServer) MCPDialog {
	d := MCPDialog{
		servers: slices.Clone(servers),
		configs: cloneMCPConfigs(current),
		list: EntityListDialog{
			Title:       "MCP Servers",
			FilterLabel: "Filter",
			EmptyText:   "No configured MCP servers",
			DetailTitle: "Details",
			FooterText:  "Enter selects. Alt+A add  Alt+E edit  Alt+R reconnect  Alt+X remove  Esc close.",
			Columns: []ui.TableColumn{
				{Title: "ID", Width: 22},
				{Title: "Status", Width: 12},
				{Title: "URL", Width: 60},
			},
			Buttons: ui.ButtonRow{
				Buttons: []ui.Button{
					{ID: "add", Label: "Add", Hotkey: 'a', Primary: true},
					{ID: "edit", Label: "Edit", Hotkey: 'e'},
					{ID: "reconnect", Label: "Reconnect", Hotkey: 'r'},
					{ID: "remove", Label: "Remove", Hotkey: 'x'},
					{ID: "close", Label: "Close", Hotkey: 'c'},
				},
				Align: ui.HorizontalAlignRight,
			},
		},
	}
	d.mergeConfiguredServers()
	d.refreshListItems()
	return d
}

func (d *MCPDialog) SetServers(servers []kodermcp.ServerState) {
	d.servers = slices.Clone(servers)
	d.mergeConfiguredServers()
	d.refreshListItems()
}

func (d *MCPDialog) SetStatus(status string) {
	d.status = strings.TrimSpace(status)
	if d.HasEditor() {
		d.editor.SetStatus(d.status)
		return
	}
	d.list.FooterText = firstNonBlank(d.status, "Enter selects. Alt+A add  Alt+E edit  Alt+R reconnect  Alt+X remove  Esc close.")
}

func (d MCPDialog) EditID() string {
	return strings.TrimSpace(d.editID)
}

func (d MCPDialog) HasEditor() bool {
	return d.editor != nil
}

func (d *MCPDialog) UpdateList(msg ui.KeyMsg) MCPDialogAction {
	event := d.list.Update(msg)
	return d.handleListEvent(event)
}

func (d *MCPDialog) UpdateEditor(msg ui.KeyMsg) MCPDialogAction {
	if d.editor == nil {
		return MCPDialogAction{}
	}
	event := d.editor.Update(msg)
	return d.handleEditorEvent(event)
}

func (d *MCPDialog) Update(msg ui.KeyMsg) MCPDialogAction {
	if d.HasEditor() {
		return d.UpdateEditor(msg)
	}
	return d.UpdateList(msg)
}

func (d *MCPDialog) ActivateListControl(controlID string) MCPDialogAction {
	return d.handleListEvent(d.list.ActivateControl(controlID))
}

func (d *MCPDialog) ActivateEditorControl(controlID string) MCPDialogAction {
	if d.editor == nil {
		return MCPDialogAction{}
	}
	return d.handleEditorEvent(d.editor.ActivateControl(controlID))
}

func (d *MCPDialog) ActivateControl(controlID string) MCPDialogAction {
	if d.HasEditor() {
		return d.ActivateEditorControl(controlID)
	}
	return d.ActivateListControl(controlID)
}

func (d MCPDialog) Measure(ctx *ui.Context, constraints ui.Constraints) ui.Size {
	width := constraints.MaxW
	if width <= 0 {
		width = 104
	}
	return constraints.Clamp(d.ListNode(width, ctx.Palette).Measure(ctx, ui.Constraints{MaxW: width, MaxH: constraints.MaxH}))
}

func (d MCPDialog) Surface(ctx *ui.Context, bounds ui.Rect) ui.Surface {
	maxWidth := dialogRenderWidth(bounds, 104)
	node := d.ListNode(maxWidth, ctx.Palette)
	size := node.Measure(ctx, ui.Constraints{MaxW: maxWidth, MaxH: bounds.H})
	return ui.PaintNodeSurface(ctx, node, ui.Rect{W: size.W, H: bounds.H})
}

func (d MCPDialog) Paint(ctx *ui.Context, canvas ui.Canvas) {
	if canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	canvas.BlitSurface(0, 0, d.Surface(ctx, ui.Rect{W: canvas.Width(), H: canvas.Height()}))
}

func (d MCPDialog) ListNode(width int, palette theme.Palette) ui.Node {
	return d.list.Node(width, palette)
}

func (d MCPDialog) EditorNode(width int, palette theme.Palette) ui.Node {
	if d.editor == nil {
		return nil
	}
	return d.editor.Node(width, palette)
}

func (d *MCPDialog) CloseEditor() {
	d.editor = nil
	d.editID = ""
	d.SetStatus("")
}

func (d *MCPDialog) handleListEvent(event EntityListDialogEvent) MCPDialogAction {
	if event.Cancel {
		return MCPDialogAction{Kind: MCPDialogActionCancel}
	}
	if strings.TrimSpace(event.OpenID) != "" {
		d.selected = event.OpenID
		d.openEditor(event.OpenID, d.currentConfig(event.OpenID))
		return MCPDialogAction{}
	}
	switch event.ButtonID {
	case "add":
		d.openEditor("", config.MCPServer{})
	case "edit":
		if item, ok := d.list.Current(); ok {
			d.selected = item.ID
			d.openEditor(item.ID, d.currentConfig(item.ID))
		}
	case "reconnect":
		if item, ok := d.list.Current(); ok {
			return MCPDialogAction{Kind: MCPDialogActionReconnect, ServerID: item.ID}
		}
	case "remove":
		if item, ok := d.list.Current(); ok {
			return MCPDialogAction{Kind: MCPDialogActionRemove, ServerID: item.ID}
		}
	case "close":
		return MCPDialogAction{Kind: MCPDialogActionCancel}
	}
	return MCPDialogAction{}
}

func (d *MCPDialog) handleEditorEvent(event LabeledFormEvent) MCPDialogAction {
	if event.Cancel {
		d.CloseEditor()
		return MCPDialogAction{}
	}
	switch event.ButtonID {
	case "save":
		return d.saveAction()
	case "remove":
		if strings.TrimSpace(d.editID) != "" {
			return MCPDialogAction{Kind: MCPDialogActionRemove, ServerID: d.editID}
		}
		d.CloseEditor()
	case "cancel":
		d.CloseEditor()
	}
	return MCPDialogAction{}
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

func (d *MCPDialog) refreshListItems() {
	items := make([]EntityListItem, 0, len(d.servers))
	for _, item := range d.servers {
		items = append(items, EntityListItem{
			ID: item.ID,
			Cells: []string{
				item.ID,
				string(item.Status),
				truncateText(item.URL, 60),
			},
			Search:  strings.Join([]string{item.ID, item.Name, item.URL, string(item.Status)}, " "),
			Details: serverDetailText(item, 80),
		})
	}
	d.list.SetItems(items)
	d.SetStatus(d.status)
}

func (d *MCPDialog) currentConfig(id string) config.MCPServer {
	if cfg, ok := d.configs[id]; ok {
		return cloneMCPConfig(cfg)
	}
	return config.MCPServer{}
}

func (d *MCPDialog) openEditor(id string, cfg config.MCPServer) {
	form := NewLabeledFormDialog("Edit MCP Server", []LabeledFormField{
		{ID: "id", Label: "ID", Description: "Stable config key used in tool names", Kind: LabeledFormFieldText},
		{ID: "name", Label: "Name", Description: "Human-readable display name", Kind: LabeledFormFieldText},
		{ID: "url", Label: "URL", Description: "Remote MCP endpoint URL", Kind: LabeledFormFieldText},
		{ID: "bearer_token", Label: "Bearer Token", Description: "Static bearer token if needed", Kind: LabeledFormFieldSecret},
		{ID: "bearer_token_env", Label: "Bearer Token Env", Description: "Environment variable for the bearer token", Kind: LabeledFormFieldText},
		{ID: "headers", Label: "Headers", Description: "Comma-separated key=value request headers", Kind: LabeledFormFieldText},
		{ID: "startup_timeout", Label: "Startup Timeout", Description: "Connect/initialize timeout, e.g. 10s", Kind: LabeledFormFieldText},
		{ID: "request_timeout", Label: "Request Timeout", Description: "HTTP request timeout, e.g. 30s", Kind: LabeledFormFieldText},
		{ID: "disable_sse", Label: "Disable SSE", Description: "Turn off standalone SSE for broken servers", Kind: LabeledFormFieldToggle},
		{ID: "disabled", Label: "Disabled", Description: "Keep config but do not connect", Kind: LabeledFormFieldToggle},
	}, []ui.Button{
		{ID: "save", Label: "Save", Hotkey: 's', Primary: true},
		{ID: "remove", Label: "Remove", Hotkey: 'x'},
		{ID: "cancel", Label: "Cancel", Hotkey: 'c'},
	})
	form.FooterText = "Tab moves between fields and buttons. Left/Right toggles yes/no fields."
	form.SetValue("id", id)
	form.SetValue("name", cfg.Name)
	form.SetValue("url", cfg.URL)
	form.SetValue("bearer_token", cfg.BearerToken)
	form.SetValue("bearer_token_env", cfg.BearerTokenEnv)
	form.SetValue("headers", formatHeaders(cfg.Headers))
	form.SetValue("startup_timeout", formatDuration(cfg.StartupTimeout))
	form.SetValue("request_timeout", formatDuration(cfg.RequestTimeout))
	form.SetToggle("disable_sse", cfg.DisableStandaloneSSE)
	form.SetToggle("disabled", cfg.Disabled)
	if strings.TrimSpace(d.status) != "" {
		form.SetStatus(d.status)
	}
	d.editor = &form
	d.editID = id
}

func (d *MCPDialog) saveAction() MCPDialogAction {
	if d.editor == nil {
		return MCPDialogAction{}
	}
	id := strings.TrimSpace(d.editor.Value("id"))
	startup, err := parseOptionalDuration(d.editor.Value("startup_timeout"))
	if err != nil {
		d.editor.SetStatus("Invalid startup timeout: " + err.Error())
		return MCPDialogAction{}
	}
	request, err := parseOptionalDuration(d.editor.Value("request_timeout"))
	if err != nil {
		d.editor.SetStatus("Invalid request timeout: " + err.Error())
		return MCPDialogAction{}
	}
	headers, err := parseHeaders(d.editor.Value("headers"))
	if err != nil {
		d.editor.SetStatus("Invalid headers: " + err.Error())
		return MCPDialogAction{}
	}
	cfg := config.MCPServer{
		Name:                 strings.TrimSpace(d.editor.Value("name")),
		URL:                  strings.TrimSpace(d.editor.Value("url")),
		BearerToken:          strings.TrimSpace(d.editor.Value("bearer_token")),
		BearerTokenEnv:       strings.TrimSpace(d.editor.Value("bearer_token_env")),
		Headers:              headers,
		StartupTimeout:       startup,
		RequestTimeout:       request,
		DisableStandaloneSSE: d.editor.Toggle("disable_sse"),
		Disabled:             d.editor.Toggle("disabled"),
	}
	return MCPDialogAction{Kind: MCPDialogActionSave, ServerID: id, Config: cfg}
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

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
