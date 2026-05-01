package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/tools"
	"github.com/lkarlslund/koder/internal/version"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type ServerStatus string

const (
	ServerStatusDisconnected ServerStatus = "disconnected"
	ServerStatusConnecting   ServerStatus = "connecting"
	ServerStatusConnected    ServerStatus = "connected"
	ServerStatusError        ServerStatus = "error"
	ServerStatusDisabled     ServerStatus = "disabled"
)

type ToolDescriptor struct {
	ServerID     string
	ServerName   string
	Name         string
	Title        string
	Description  string
	InputSchema  any
	ReadOnlyHint bool
}

type ResourceDescriptor struct {
	ServerID    string
	ServerName  string
	URI         string
	Name        string
	Title       string
	Description string
	MIMEType    string
	Size        int64
}

type ResourceTemplateDescriptor struct {
	ServerID    string
	ServerName  string
	URITemplate string
	Name        string
	Title       string
	Description string
	MIMEType    string
}

type PromptArgumentDescriptor struct {
	Name        string
	Title       string
	Description string
	Required    bool
}

type PromptDescriptor struct {
	ServerID    string
	ServerName  string
	Name        string
	Title       string
	Description string
	Arguments   []PromptArgumentDescriptor
}

type PromptMessage struct {
	Role string
	Text string
}

type PromptResult struct {
	Description string
	Messages    []PromptMessage
}

type ResourceReadResult struct {
	Contents []tools.MCPStoredContentItem
}

type ServerState struct {
	ID                    string
	Name                  string
	URL                   string
	Status                ServerStatus
	Disabled              bool
	LastError             string
	SessionID             string
	ServerInstructions    string
	ToolCount             int
	ResourceCount         int
	ResourceTemplateCount int
	PromptCount           int
	Tools                 []ToolDescriptor
	Resources             []ResourceDescriptor
	ResourceTemplates     []ResourceTemplateDescriptor
	Prompts               []PromptDescriptor
}

type Manager struct {
	mu     sync.RWMutex
	config map[string]config.MCPServer
	state  map[string]*serverState
}

type serverState struct {
	id                 string
	config             config.MCPServer
	status             ServerStatus
	lastErr            string
	session            *sdkmcp.ClientSession
	client             *sdkmcp.Client
	serverInstructions string
	tools              []ToolDescriptor
	resources          []ResourceDescriptor
	resourceTemplates  []ResourceTemplateDescriptor
	prompts            []PromptDescriptor
}

func NewManager(cfgs map[string]config.MCPServer) (*Manager, error) {
	m := &Manager{}
	if err := m.LoadConfig(cfgs); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Manager) LoadConfig(cfgs map[string]config.MCPServer) error {
	next := make(map[string]config.MCPServer, len(cfgs))
	for id, cfg := range cfgs {
		if err := ValidateServerConfig(id, cfg); err != nil {
			return err
		}
		next[strings.TrimSpace(id)] = cloneServerConfig(cfg)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, state := range m.state {
		if state.session != nil {
			_ = state.session.Close()
		}
	}
	m.config = next
	m.state = make(map[string]*serverState, len(next))
	for id, cfg := range next {
		status := ServerStatusDisconnected
		if cfg.Disabled {
			status = ServerStatusDisabled
		}
		m.state[id] = &serverState{
			id:     id,
			config: cfg,
			status: status,
		}
	}
	return nil
}

func (m *Manager) ConnectAll(ctx context.Context) error {
	for _, id := range m.serverIDs() {
		if err := m.ConnectServer(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) ConnectServer(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("mcp server id is empty")
	}

	m.mu.Lock()
	state, ok := m.state[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("mcp server %q not configured", id)
	}
	cfg := cloneServerConfig(state.config)
	if cfg.Disabled {
		if state.session != nil {
			_ = state.session.Close()
			state.session = nil
			state.client = nil
		}
		state.status = ServerStatusDisabled
		state.lastErr = ""
		m.mu.Unlock()
		return nil
	}
	if state.session != nil {
		_ = state.session.Close()
		state.session = nil
		state.client = nil
	}
	state.status = ServerStatusConnecting
	state.lastErr = ""
	m.mu.Unlock()

	timeout := cfg.StartupTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	connectCtx := ctx
	var cancel context.CancelFunc
	if timeout > 0 {
		connectCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	client := sdkmcp.NewClient(&sdkmcp.Implementation{
		Name:    "koder",
		Version: version.Current().Version,
	}, &sdkmcp.ClientOptions{
		ToolListChangedHandler: func(context.Context, *sdkmcp.ToolListChangedRequest) {
			m.refreshServerAsync(id)
		},
		PromptListChangedHandler: func(context.Context, *sdkmcp.PromptListChangedRequest) {
			m.refreshServerAsync(id)
		},
		ResourceListChangedHandler: func(context.Context, *sdkmcp.ResourceListChangedRequest) {
			m.refreshServerAsync(id)
		},
	})

	transport := &sdkmcp.StreamableClientTransport{
		Endpoint:             cfg.URL,
		HTTPClient:           newHTTPClient(cfg),
		DisableStandaloneSSE: cfg.DisableStandaloneSSE,
	}
	session, err := client.Connect(connectCtx, transport, nil)
	if err != nil {
		m.setServerError(id, err)
		return err
	}

	next, err := m.discoverState(ctx, id, cfg, client, session)
	if err != nil {
		_ = session.Close()
		m.setServerError(id, err)
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.state[id]
	if !ok {
		_ = session.Close()
		return fmt.Errorf("mcp server %q removed during connect", id)
	}
	if current.session != nil && current.session != session {
		_ = current.session.Close()
	}
	*current = *next
	return nil
}

func (m *Manager) DisconnectServer(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("mcp server id is empty")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	state, ok := m.state[id]
	if !ok {
		return fmt.Errorf("mcp server %q not configured", id)
	}
	if state.session != nil {
		_ = state.session.Close()
	}
	state.session = nil
	state.client = nil
	state.tools = nil
	state.resources = nil
	state.resourceTemplates = nil
	state.prompts = nil
	state.serverInstructions = ""
	state.lastErr = ""
	if state.config.Disabled {
		state.status = ServerStatusDisabled
	} else {
		state.status = ServerStatusDisconnected
	}
	return nil
}

func (m *Manager) ListServers() []ServerState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ServerState, 0, len(m.state))
	for _, id := range m.serverIDsLocked() {
		state := m.state[id]
		out = append(out, snapshotState(state))
	}
	return out
}

func (m *Manager) ListTools() []ToolDescriptor {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []ToolDescriptor
	for _, id := range m.serverIDsLocked() {
		out = append(out, slices.Clone(m.state[id].tools)...)
	}
	sortToolDescriptors(out)
	return out
}

func (m *Manager) ListResources(serverID string) []ResourceDescriptor {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if state, ok := m.state[strings.TrimSpace(serverID)]; ok {
		return slices.Clone(state.resources)
	}
	return nil
}

func (m *Manager) ListResourceTemplates(serverID string) []ResourceTemplateDescriptor {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if state, ok := m.state[strings.TrimSpace(serverID)]; ok {
		return slices.Clone(state.resourceTemplates)
	}
	return nil
}

func (m *Manager) ListPrompts(serverID string) []PromptDescriptor {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if state, ok := m.state[strings.TrimSpace(serverID)]; ok {
		return slices.Clone(state.prompts)
	}
	return nil
}

func (m *Manager) ToolDefinitions() []provider.ToolDefinition {
	return m.ToolDefinitionsWithReserved(nil)
}

func (m *Manager) ToolDefinitionsWithReserved(reserved []provider.ToolDefinition) []provider.ToolDefinition {
	descriptors := m.ListTools()
	nameMap := buildToolNameMap(descriptors, reservedToolNames(reserved))
	out := make([]provider.ToolDefinition, 0, len(descriptors))
	for _, desc := range descriptors {
		schema := normalizeSchema(desc.InputSchema)
		name := nameMap[toolKey(desc.ServerID, desc.Name)]
		description := strings.TrimSpace(desc.Description)
		if description == "" {
			description = strings.TrimSpace(desc.Title)
		}
		if description == "" {
			description = fmt.Sprintf("MCP tool %s/%s", desc.ServerID, desc.Name)
		}
		out = append(out, provider.ToolDefinition{
			Type: "function",
			Function: provider.FunctionDefinition{
				Name:        name,
				Description: description,
				Parameters:  schema,
			},
		})
	}
	return out
}

func (m *Manager) ResolveToolName(name string, reserved []provider.ToolDefinition) (serverID, toolName string, ok bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", "", false
	}
	descriptors := m.ListTools()
	nameMap := buildToolNameMap(descriptors, reservedToolNames(reserved))
	for _, desc := range descriptors {
		if nameMap[toolKey(desc.ServerID, desc.Name)] == name {
			return desc.ServerID, desc.Name, true
		}
	}
	return "", "", false
}

func (m *Manager) ExecuteTool(ctx context.Context, serverID, toolName string, args map[string]any) (tools.Result, error) {
	serverID = strings.TrimSpace(serverID)
	toolName = strings.TrimSpace(toolName)
	if serverID == "" || toolName == "" {
		return tools.Result{}, errors.New("mcp server and tool are required")
	}

	m.mu.RLock()
	state, ok := m.state[serverID]
	if !ok {
		m.mu.RUnlock()
		return tools.Result{}, fmt.Errorf("mcp server %q not configured", serverID)
	}
	session := state.session
	serverName := strings.TrimSpace(state.config.Name)
	m.mu.RUnlock()

	if session == nil {
		return tools.Result{}, fmt.Errorf("mcp server %q is not connected", serverID)
	}
	if args == nil {
		args = map[string]any{}
	}
	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      toolName,
		Arguments: args,
	})
	if err != nil {
		return tools.Result{}, err
	}
	return convertCallToolResult(serverID, serverName, toolName, res)
}

func (m *Manager) ReadResource(ctx context.Context, serverID, uri string) (ResourceReadResult, error) {
	session, err := m.sessionForServer(strings.TrimSpace(serverID))
	if err != nil {
		return ResourceReadResult{}, err
	}
	res, err := session.ReadResource(ctx, &sdkmcp.ReadResourceParams{URI: strings.TrimSpace(uri)})
	if err != nil {
		return ResourceReadResult{}, err
	}
	out := ResourceReadResult{Contents: make([]tools.MCPStoredContentItem, 0, len(res.Contents))}
	for _, item := range res.Contents {
		out.Contents = append(out.Contents, contentItemFromResourceContents(item))
	}
	return out, nil
}

func (m *Manager) GetPrompt(ctx context.Context, serverID, name string, args map[string]string) (PromptResult, error) {
	session, err := m.sessionForServer(strings.TrimSpace(serverID))
	if err != nil {
		return PromptResult{}, err
	}
	res, err := session.GetPrompt(ctx, &sdkmcp.GetPromptParams{
		Name:      strings.TrimSpace(name),
		Arguments: args,
	})
	if err != nil {
		return PromptResult{}, err
	}
	out := PromptResult{
		Description: strings.TrimSpace(res.Description),
		Messages:    make([]PromptMessage, 0, len(res.Messages)),
	}
	for _, msg := range res.Messages {
		out.Messages = append(out.Messages, PromptMessage{
			Role: string(msg.Role),
			Text: renderContent(msg.Content),
		})
	}
	return out, nil
}

func ToolName(serverID, toolName string) string {
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		return ""
	}
	serverID = sanitizeToolSegment(serverID)
	if serverID == "" {
		return toolName
	}
	return "_" + serverID + "_" + sanitizeToolSegment(toolName)
}

func reservedToolNames(defs []provider.ToolDefinition) map[string]struct{} {
	reserved := make(map[string]struct{}, len(defs))
	for _, def := range defs {
		name := strings.TrimSpace(def.Function.Name)
		if name == "" {
			continue
		}
		reserved[name] = struct{}{}
	}
	return reserved
}

func buildToolNameMap(descriptors []ToolDescriptor, reserved map[string]struct{}) map[string]string {
	reserved = cloneStringSet(reserved)
	counts := make(map[string]int, len(descriptors))
	for _, desc := range descriptors {
		name := strings.TrimSpace(desc.Name)
		if name == "" {
			continue
		}
		counts[name]++
	}
	resolved := make(map[string]string, len(descriptors))
	for _, desc := range descriptors {
		name := strings.TrimSpace(desc.Name)
		if name == "" {
			continue
		}
		key := toolKey(desc.ServerID, desc.Name)
		if counts[name] == 1 {
			if _, collision := reserved[name]; !collision {
				resolved[key] = name
				reserved[name] = struct{}{}
				continue
			}
		}
		fallback := ToolName(desc.ServerID, desc.Name)
		if fallback == "" {
			continue
		}
		resolved[key] = fallback
		reserved[fallback] = struct{}{}
	}
	return resolved
}

func sortToolDescriptors(items []ToolDescriptor) {
	slices.SortFunc(items, func(a, b ToolDescriptor) int {
		if cmp := strings.Compare(strings.TrimSpace(a.ServerID), strings.TrimSpace(b.ServerID)); cmp != 0 {
			return cmp
		}
		if cmp := strings.Compare(strings.TrimSpace(a.Name), strings.TrimSpace(b.Name)); cmp != 0 {
			return cmp
		}
		if cmp := strings.Compare(strings.TrimSpace(a.Title), strings.TrimSpace(b.Title)); cmp != 0 {
			return cmp
		}
		if cmp := strings.Compare(strings.TrimSpace(a.Description), strings.TrimSpace(b.Description)); cmp != 0 {
			return cmp
		}
		switch {
		case a.ReadOnlyHint && !b.ReadOnlyHint:
			return -1
		case !a.ReadOnlyHint && b.ReadOnlyHint:
			return 1
		default:
			return 0
		}
	})
}

func toolKey(serverID, toolName string) string {
	return strings.TrimSpace(serverID) + "\x00" + strings.TrimSpace(toolName)
}

func cloneStringSet(in map[string]struct{}) map[string]struct{} {
	if len(in) == 0 {
		return map[string]struct{}{}
	}
	out := make(map[string]struct{}, len(in))
	for key := range in {
		out[key] = struct{}{}
	}
	return out
}

func sanitizeToolSegment(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(value))
	lastUnderscore := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastUnderscore = false
		case r == '_':
			if !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		default:
			if !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	return strings.Trim(b.String(), "_")
}

func ValidateServerConfig(id string, cfg config.MCPServer) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("mcp server id is empty")
	}
	rawURL := strings.TrimSpace(cfg.URL)
	if rawURL == "" {
		return fmt.Errorf("mcp server %q url is empty", id)
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("mcp server %q url: %w", id, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("mcp server %q url must use http or https", id)
	}
	if strings.TrimSpace(cfg.BearerToken) != "" && strings.TrimSpace(cfg.BearerTokenEnv) != "" {
		return fmt.Errorf("mcp server %q cannot set both bearer_token and bearer_token_env", id)
	}
	return nil
}

func cloneServerConfig(cfg config.MCPServer) config.MCPServer {
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

func (m *Manager) serverIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.serverIDsLocked()
}

func (m *Manager) serverIDsLocked() []string {
	ids := make([]string, 0, len(m.state))
	for id := range m.state {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

func snapshotState(state *serverState) ServerState {
	if state == nil {
		return ServerState{}
	}
	sessionID := ""
	if state.session != nil {
		sessionID = state.session.ID()
	}
	return ServerState{
		ID:                    state.id,
		Name:                  strings.TrimSpace(state.config.Name),
		URL:                   strings.TrimSpace(state.config.URL),
		Status:                state.status,
		Disabled:              state.config.Disabled,
		LastError:             state.lastErr,
		SessionID:             sessionID,
		ServerInstructions:    state.serverInstructions,
		ToolCount:             len(state.tools),
		ResourceCount:         len(state.resources),
		ResourceTemplateCount: len(state.resourceTemplates),
		PromptCount:           len(state.prompts),
		Tools:                 slices.Clone(state.tools),
		Resources:             slices.Clone(state.resources),
		ResourceTemplates:     slices.Clone(state.resourceTemplates),
		Prompts:               slices.Clone(state.prompts),
	}
}

func (m *Manager) setServerError(id string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, ok := m.state[id]
	if !ok {
		return
	}
	state.status = ServerStatusError
	state.lastErr = strings.TrimSpace(err.Error())
	state.tools = nil
	state.resources = nil
	state.resourceTemplates = nil
	state.prompts = nil
	state.serverInstructions = ""
	if state.session != nil {
		_ = state.session.Close()
	}
	state.session = nil
	state.client = nil
}

func (m *Manager) refreshServerAsync(id string) {
	go func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		_ = m.ConnectServer(ctx, id)
	}()
}

func (m *Manager) sessionForServer(serverID string) (*sdkmcp.ClientSession, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state, ok := m.state[serverID]
	if !ok {
		return nil, fmt.Errorf("mcp server %q not configured", serverID)
	}
	if state.session == nil {
		return nil, fmt.Errorf("mcp server %q is not connected", serverID)
	}
	return state.session, nil
}

func (m *Manager) discoverState(ctx context.Context, id string, cfg config.MCPServer, client *sdkmcp.Client, session *sdkmcp.ClientSession) (*serverState, error) {
	toolsList, err := collectTools(ctx, id, cfg, session)
	if err != nil {
		return nil, err
	}
	resources, err := collectResources(ctx, id, cfg, session)
	if err != nil {
		return nil, err
	}
	resourceTemplates, err := collectResourceTemplates(ctx, id, cfg, session)
	if err != nil {
		return nil, err
	}
	prompts, err := collectPrompts(ctx, id, cfg, session)
	if err != nil {
		return nil, err
	}
	instructions := ""
	if init := session.InitializeResult(); init != nil {
		instructions = strings.TrimSpace(init.Instructions)
	}
	return &serverState{
		id:                 id,
		config:             cfg,
		status:             ServerStatusConnected,
		session:            session,
		client:             client,
		serverInstructions: instructions,
		tools:              toolsList,
		resources:          resources,
		resourceTemplates:  resourceTemplates,
		prompts:            prompts,
	}, nil
}

func collectTools(ctx context.Context, serverID string, cfg config.MCPServer, session *sdkmcp.ClientSession) ([]ToolDescriptor, error) {
	var out []ToolDescriptor
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			return nil, err
		}
		if tool == nil {
			continue
		}
		title := strings.TrimSpace(tool.Title)
		if title == "" && tool.Annotations != nil {
			title = strings.TrimSpace(tool.Annotations.Title)
		}
		out = append(out, ToolDescriptor{
			ServerID:     serverID,
			ServerName:   strings.TrimSpace(cfg.Name),
			Name:         strings.TrimSpace(tool.Name),
			Title:        title,
			Description:  strings.TrimSpace(tool.Description),
			InputSchema:  tool.InputSchema,
			ReadOnlyHint: tool.Annotations != nil && tool.Annotations.ReadOnlyHint,
		})
	}
	return out, nil
}

func collectResources(ctx context.Context, serverID string, cfg config.MCPServer, session *sdkmcp.ClientSession) ([]ResourceDescriptor, error) {
	var out []ResourceDescriptor
	for item, err := range session.Resources(ctx, nil) {
		if err != nil {
			return nil, err
		}
		if item == nil {
			continue
		}
		out = append(out, ResourceDescriptor{
			ServerID:    serverID,
			ServerName:  strings.TrimSpace(cfg.Name),
			URI:         strings.TrimSpace(item.URI),
			Name:        strings.TrimSpace(item.Name),
			Title:       strings.TrimSpace(item.Title),
			Description: strings.TrimSpace(item.Description),
			MIMEType:    strings.TrimSpace(item.MIMEType),
			Size:        item.Size,
		})
	}
	return out, nil
}

func collectResourceTemplates(ctx context.Context, serverID string, cfg config.MCPServer, session *sdkmcp.ClientSession) ([]ResourceTemplateDescriptor, error) {
	var out []ResourceTemplateDescriptor
	for item, err := range session.ResourceTemplates(ctx, nil) {
		if err != nil {
			return nil, err
		}
		if item == nil {
			continue
		}
		out = append(out, ResourceTemplateDescriptor{
			ServerID:    serverID,
			ServerName:  strings.TrimSpace(cfg.Name),
			URITemplate: strings.TrimSpace(item.URITemplate),
			Name:        strings.TrimSpace(item.Name),
			Title:       strings.TrimSpace(item.Title),
			Description: strings.TrimSpace(item.Description),
			MIMEType:    strings.TrimSpace(item.MIMEType),
		})
	}
	return out, nil
}

func collectPrompts(ctx context.Context, serverID string, cfg config.MCPServer, session *sdkmcp.ClientSession) ([]PromptDescriptor, error) {
	var out []PromptDescriptor
	for item, err := range session.Prompts(ctx, nil) {
		if err != nil {
			return nil, err
		}
		if item == nil {
			continue
		}
		desc := PromptDescriptor{
			ServerID:    serverID,
			ServerName:  strings.TrimSpace(cfg.Name),
			Name:        strings.TrimSpace(item.Name),
			Title:       strings.TrimSpace(item.Title),
			Description: strings.TrimSpace(item.Description),
			Arguments:   make([]PromptArgumentDescriptor, 0, len(item.Arguments)),
		}
		for _, arg := range item.Arguments {
			if arg == nil {
				continue
			}
			desc.Arguments = append(desc.Arguments, PromptArgumentDescriptor{
				Name:        strings.TrimSpace(arg.Name),
				Title:       strings.TrimSpace(arg.Title),
				Description: strings.TrimSpace(arg.Description),
				Required:    arg.Required,
			})
		}
		out = append(out, desc)
	}
	return out, nil
}

func convertCallToolResult(serverID, serverName, toolName string, res *sdkmcp.CallToolResult) (tools.Result, error) {
	if res == nil {
		return tools.Result{}, errors.New("nil mcp tool result")
	}
	contentItems := make([]tools.MCPStoredContentItem, 0, len(res.Content))
	lines := make([]string, 0, len(res.Content)+1)
	for _, item := range res.Content {
		rendered := renderContent(item)
		switch typed := item.(type) {
		case *sdkmcp.TextContent:
			contentItems = append(contentItems, tools.MCPStoredContentItem{Type: "text", Text: typed.Text})
		case *sdkmcp.ResourceLink:
			contentItems = append(contentItems, tools.MCPStoredContentItem{
				Type:     "resource_link",
				Text:     coalesce(typed.Title, typed.Name),
				URI:      typed.URI,
				MIMEType: typed.MIMEType,
			})
		case *sdkmcp.EmbeddedResource:
			contentItems = append(contentItems, contentItemFromResourceContents(typed.Resource))
		case *sdkmcp.ImageContent:
			contentItems = append(contentItems, tools.MCPStoredContentItem{Type: "image", MIMEType: typed.MIMEType})
		case *sdkmcp.AudioContent:
			contentItems = append(contentItems, tools.MCPStoredContentItem{Type: "audio", MIMEType: typed.MIMEType})
		default:
			contentItems = append(contentItems, tools.MCPStoredContentItem{Type: fmt.Sprintf("%T", item), Text: rendered})
		}
		if strings.TrimSpace(rendered) != "" {
			lines = append(lines, rendered)
		}
	}
	structured := ""
	if res.StructuredContent != nil {
		if body, err := json.MarshalIndent(res.StructuredContent, "", "  "); err == nil {
			structured = string(body)
		}
	}
	if strings.TrimSpace(structured) != "" {
		lines = append(lines, "Structured content:\n"+structured)
	}
	output := strings.TrimSpace(strings.Join(lines, "\n\n"))
	if output == "" {
		if res.IsError {
			output = fmt.Sprintf("MCP tool %s/%s returned an error with no content", serverID, toolName)
		} else {
			output = fmt.Sprintf("MCP tool %s/%s completed with no content", serverID, toolName)
		}
	}
	return tools.Result{
		Output: output,
		Meta: map[string]string{
			"server_id":   serverID,
			"server_name": strings.TrimSpace(serverName),
			"tool_name":   toolName,
			"is_error":    boolString(res.IsError),
		},
		Stored: tools.MCPStoredResult{
			ServerID:          serverID,
			ServerName:        strings.TrimSpace(serverName),
			ToolName:          toolName,
			StructuredContent: structured,
			IsError:           res.IsError,
			Content:           contentItems,
		},
	}, nil
}

func renderContent(item sdkmcp.Content) string {
	switch typed := item.(type) {
	case *sdkmcp.TextContent:
		return strings.TrimSpace(typed.Text)
	case *sdkmcp.ResourceLink:
		title := coalesce(typed.Title, typed.Name, typed.URI)
		if typed.URI == "" || title == typed.URI {
			return title
		}
		return title + "\n" + typed.URI
	case *sdkmcp.EmbeddedResource:
		return renderResourceContents(typed.Resource)
	case *sdkmcp.ImageContent:
		return fmt.Sprintf("[image content %s, %d bytes]", strings.TrimSpace(typed.MIMEType), len(typed.Data))
	case *sdkmcp.AudioContent:
		return fmt.Sprintf("[audio content %s, %d bytes]", strings.TrimSpace(typed.MIMEType), len(typed.Data))
	default:
		body, err := json.Marshal(item)
		if err != nil {
			return ""
		}
		return string(body)
	}
}

func renderResourceContents(item *sdkmcp.ResourceContents) string {
	if item == nil {
		return ""
	}
	switch {
	case strings.TrimSpace(item.Text) != "":
		if strings.TrimSpace(item.URI) != "" {
			return item.URI + "\n" + strings.TrimSpace(item.Text)
		}
		return strings.TrimSpace(item.Text)
	case len(item.Blob) > 0:
		return fmt.Sprintf("[resource %s %s, %d bytes]", strings.TrimSpace(item.URI), strings.TrimSpace(item.MIMEType), len(item.Blob))
	default:
		return strings.TrimSpace(item.URI)
	}
}

func contentItemFromResourceContents(item *sdkmcp.ResourceContents) tools.MCPStoredContentItem {
	if item == nil {
		return tools.MCPStoredContentItem{Type: "resource"}
	}
	text := strings.TrimSpace(item.Text)
	if text == "" && len(item.Blob) > 0 {
		text = fmt.Sprintf("[%d bytes]", len(item.Blob))
	}
	return tools.MCPStoredContentItem{
		Type:     "resource",
		Text:     text,
		URI:      strings.TrimSpace(item.URI),
		MIMEType: strings.TrimSpace(item.MIMEType),
	}
}

func normalizeSchema(schema any) json.RawMessage {
	if schema == nil {
		return json.RawMessage(`{"type":"object","additionalProperties":true}`)
	}
	body, err := json.Marshal(schema)
	if err != nil || len(body) == 0 || string(body) == "null" {
		return json.RawMessage(`{"type":"object","additionalProperties":true}`)
	}
	return json.RawMessage(body)
}

func newHTTPClient(cfg config.MCPServer) *http.Client {
	timeout := cfg.RequestTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &http.Client{
		Timeout: timeout,
		Transport: headerRoundTripper{
			base:        http.DefaultTransport,
			headers:     cloneStringMap(cfg.Headers),
			bearerToken: strings.TrimSpace(cfg.BearerToken),
		},
	}
}

type headerRoundTripper struct {
	base        http.RoundTripper
	headers     map[string]string
	bearerToken string
}

func (rt headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	base := rt.base
	if base == nil {
		base = http.DefaultTransport
	}
	next := req.Clone(req.Context())
	next.Header = req.Header.Clone()
	for key, value := range rt.headers {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		next.Header.Set(key, value)
	}
	if strings.TrimSpace(rt.bearerToken) != "" && strings.TrimSpace(next.Header.Get("Authorization")) == "" {
		next.Header.Set("Authorization", "Bearer "+strings.TrimSpace(rt.bearerToken))
	}
	return base.RoundTrip(next)
}

func boolString(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func coalesce(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
