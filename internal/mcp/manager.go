package mcp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/tools"
	"github.com/lkarlslund/koder/internal/version"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/santhosh-tekuri/jsonschema/v6"
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
	ServerID        string
	ServerName      string
	Name            string
	Title           string
	Description     string
	InputSchema     any
	OutputSchema    any
	ReadOnlyHint    bool
	DestructiveHint *bool
	IdempotentHint  bool
	OpenWorldHint   *bool
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
	mu           sync.RWMutex
	connectMu    sync.Mutex
	refreshMu    sync.Mutex
	refreshing   map[refreshRequest]bool
	refreshDirty map[refreshRequest]bool
	config       map[string]config.MCPServer
	state        map[string]*serverState
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
	watchStop          chan struct{}
}

const (
	mcpKeepAliveInterval = 15 * time.Second
	mcpReconnectInitial  = 250 * time.Millisecond
	mcpReconnectMaximum  = 30 * time.Second
)

func NewManager(cfgs map[string]config.MCPServer) (*Manager, error) {
	m := &Manager{}
	if err := m.LoadConfig(cfgs); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Manager) LoadConfig(cfgs map[string]config.MCPServer) error {
	m.connectMu.Lock()
	defer m.connectMu.Unlock()

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
		stopSessionWatch(state)
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
	var errs []error
	for _, id := range m.serverIDs() {
		if err := m.ConnectServer(ctx, id); err != nil {
			errs = append(errs, fmt.Errorf("connect mcp server %q: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

func (m *Manager) ConnectServer(ctx context.Context, id string) error {
	m.connectMu.Lock()
	defer m.connectMu.Unlock()
	return m.connectServer(ctx, id, false)
}

func (m *Manager) connectServer(ctx context.Context, id string, preserveWatch bool) error {
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
		stopSessionWatch(state)
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
	if !preserveWatch {
		stopSessionWatch(state)
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
		Capabilities:              &sdkmcp.ClientCapabilities{},
		KeepAlive:                 mcpKeepAliveInterval,
		KeepAliveFailureThreshold: 2,
		ToolListChangedHandler: func(context.Context, *sdkmcp.ToolListChangedRequest) {
			m.refreshServerAsync(id, refreshTools)
		},
		PromptListChangedHandler: func(context.Context, *sdkmcp.PromptListChangedRequest) {
			m.refreshServerAsync(id, refreshPrompts)
		},
		ResourceListChangedHandler: func(context.Context, *sdkmcp.ResourceListChangedRequest) {
			m.refreshServerAsync(id, refreshResources)
		},
	})

	transport, err := newClientTransport(cfg)
	if err != nil {
		m.setServerError(id, err)
		return err
	}
	session, err := client.Connect(connectCtx, transport, nil)
	if err != nil {
		m.setServerError(id, err)
		return err
	}

	next, err := m.discoverState(connectCtx, id, cfg, client, session)
	if err != nil {
		_ = session.Close()
		m.setServerError(id, err)
		return err
	}

	m.mu.Lock()
	current, ok := m.state[id]
	if !ok {
		m.mu.Unlock()
		_ = session.Close()
		return fmt.Errorf("mcp server %q removed during connect", id)
	}
	stopSessionWatch(current)
	if current.session != nil && current.session != session {
		_ = current.session.Close()
	}
	next.watchStop = make(chan struct{})
	*current = *next
	watchStop := current.watchStop
	m.mu.Unlock()
	go m.watchSession(id, session, watchStop)
	return nil
}

func (m *Manager) DisconnectServer(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("mcp server id is empty")
	}
	m.connectMu.Lock()
	defer m.connectMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	state, ok := m.state[id]
	if !ok {
		return fmt.Errorf("mcp server %q not configured", id)
	}
	stopSessionWatch(state)
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

// Close disconnects every configured MCP server and releases persistent transports.
func (m *Manager) Close() error {
	m.connectMu.Lock()
	defer m.connectMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	var errs []error
	for id, state := range m.state {
		stopSessionWatch(state)
		if state.session != nil {
			if err := state.session.Close(); err != nil {
				errs = append(errs, fmt.Errorf("close mcp server %q: %w", id, err))
			}
		}
		state.session = nil
		state.client = nil
		if state.config.Disabled {
			state.status = ServerStatusDisabled
		} else {
			state.status = ServerStatusDisconnected
		}
	}
	return errors.Join(errs...)
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
		out = append(out, provider.ToolDefinition{
			Type: "function",
			Function: provider.FunctionDefinition{
				Name:        name,
				Description: desc.Description,
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
	supervised := state.watchStop != nil
	serverName := strings.TrimSpace(state.config.Name)
	cfg := cloneServerConfig(state.config)
	readOnly := toolIsReadOnly(state.tools, toolName)
	outputSchema := toolOutputSchema(state.tools, toolName)
	m.mu.RUnlock()

	if session == nil {
		if !supervised {
			return tools.Result{}, fmt.Errorf("mcp server %q is not connected", serverID)
		}
		var err error
		session, err = m.recoverSession(ctx, serverID, nil)
		if err != nil {
			return tools.Result{}, fmt.Errorf("reconnect mcp server %q: %w", serverID, err)
		}
		m.mu.RLock()
		state = m.state[serverID]
		if state == nil || state.session != session {
			m.mu.RUnlock()
			return tools.Result{}, fmt.Errorf("mcp server %q changed during reconnect", serverID)
		}
		serverName = strings.TrimSpace(state.config.Name)
		cfg = cloneServerConfig(state.config)
		readOnly = toolIsReadOnly(state.tools, toolName)
		outputSchema = toolOutputSchema(state.tools, toolName)
		m.mu.RUnlock()
	}
	if args == nil {
		args = map[string]any{}
	}
	res, err := callToolWithTimeout(ctx, cfg, session, toolName, args)
	if err != nil && !isRecoverableSessionError(err) {
		return tools.Result{}, err
	}
	if err != nil {
		recovered, recoverErr := m.recoverSession(ctx, serverID, session)
		if recoverErr != nil {
			return tools.Result{}, fmt.Errorf("mcp server %q session failed and reconnect failed: %w", serverID, recoverErr)
		}
		if !readOnly {
			return tools.Result{}, fmt.Errorf("%w; mcp server %q reconnected, but tool %q was not retried because it is not declared read-only", err, serverID, toolName)
		}
		res, err = callToolWithTimeout(ctx, cfg, recovered, toolName, args)
		if err != nil {
			return tools.Result{}, fmt.Errorf("mcp tool %s/%s failed after reconnect: %w", serverID, toolName, err)
		}
	}
	return convertCallToolResult(serverID, serverName, toolName, outputSchema, res)
}

func (m *Manager) ReadResource(ctx context.Context, serverID, uri string) (ResourceReadResult, error) {
	serverID = strings.TrimSpace(serverID)
	session, cfg, err := m.sessionAndConfigForServer(serverID)
	if err != nil {
		return ResourceReadResult{}, err
	}
	res, err := readResourceWithTimeout(ctx, cfg, session, uri)
	if err != nil && isRecoverableSessionError(err) {
		session, err = m.recoverSession(ctx, serverID, session)
		if err == nil {
			res, err = readResourceWithTimeout(ctx, cfg, session, uri)
		}
	}
	if err != nil {
		return ResourceReadResult{}, err
	}
	out := ResourceReadResult{Contents: make([]tools.MCPStoredContentItem, 0, len(res.Contents))}
	retainedBinaryBytes := int64(0)
	for _, item := range res.Contents {
		out.Contents = append(out.Contents, contentItemFromResourceContents(item, &retainedBinaryBytes))
	}
	return out, nil
}

func (m *Manager) GetPrompt(ctx context.Context, serverID, name string, args map[string]string) (PromptResult, error) {
	serverID = strings.TrimSpace(serverID)
	session, cfg, err := m.sessionAndConfigForServer(serverID)
	if err != nil {
		return PromptResult{}, err
	}
	res, err := getPromptWithTimeout(ctx, cfg, session, name, args)
	if err != nil && isRecoverableSessionError(err) {
		session, err = m.recoverSession(ctx, serverID, session)
		if err == nil {
			res, err = getPromptWithTimeout(ctx, cfg, session, name, args)
		}
	}
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

func toolIsReadOnly(tools []ToolDescriptor, name string) bool {
	for _, tool := range tools {
		if tool.Name == name {
			return tool.ReadOnlyHint
		}
	}
	return false
}

func toolOutputSchema(tools []ToolDescriptor, name string) any {
	for _, tool := range tools {
		if tool.Name == name {
			return tool.OutputSchema
		}
	}
	return nil
}

func isRecoverableSessionError(err error) bool {
	return errors.Is(err, sdkmcp.ErrConnectionClosed) || errors.Is(err, sdkmcp.ErrSessionMissing)
}

func (m *Manager) recoverSession(ctx context.Context, serverID string, failed *sdkmcp.ClientSession) (*sdkmcp.ClientSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.connectMu.Lock()
	defer m.connectMu.Unlock()

	m.mu.RLock()
	state, ok := m.state[serverID]
	if !ok {
		m.mu.RUnlock()
		return nil, fmt.Errorf("mcp server %q not configured", serverID)
	}
	current := state.session
	status := state.status
	preserveWatch := state.watchStop != nil
	m.mu.RUnlock()
	if current != nil && current != failed && status == ServerStatusConnected {
		return current, nil
	}
	if err := m.connectServer(ctx, serverID, preserveWatch); err != nil {
		return nil, err
	}
	return m.sessionForServer(serverID)
}

func stopSessionWatch(state *serverState) {
	if state == nil || state.watchStop == nil {
		return
	}
	close(state.watchStop)
	state.watchStop = nil
}

func (m *Manager) watchSession(id string, session *sdkmcp.ClientSession, stop <-chan struct{}) {
	err := session.Wait()
	select {
	case <-stop:
		return
	default:
	}

	m.mu.Lock()
	state := m.state[id]
	if state == nil || state.session != session || state.watchStop != stop || state.config.Disabled {
		m.mu.Unlock()
		return
	}
	state.session = nil
	state.client = nil
	state.status = ServerStatusDisconnected
	if err != nil {
		state.lastErr = fmt.Sprintf("MCP session closed: %v", err)
	} else {
		state.lastErr = "MCP session closed"
	}
	m.mu.Unlock()

	for delay := mcpReconnectInitial; ; delay = min(delay*2, mcpReconnectMaximum) {
		select {
		case <-stop:
			return
		default:
		}

		m.connectMu.Lock()
		m.mu.RLock()
		state = m.state[id]
		current := state != nil && state.watchStop == stop && !state.config.Disabled
		connected := current && state.session != nil
		m.mu.RUnlock()
		if !current || connected {
			m.connectMu.Unlock()
			return
		}
		err = m.connectServer(context.Background(), id, true)
		m.connectMu.Unlock()
		if err == nil {
			return
		}

		timer := time.NewTimer(delay)
		select {
		case <-stop:
			timer.Stop()
			return
		case <-timer.C:
		}
	}
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
			fallback = "_mcp_tool"
		}
		candidate := fallback
		if _, collision := reserved[candidate]; collision {
			digest := sha256.Sum256([]byte(key))
			candidate = fmt.Sprintf("%s_%x", fallback, digest[:4])
			for suffix := 2; ; suffix++ {
				if _, exists := reserved[candidate]; !exists {
					break
				}
				candidate = fmt.Sprintf("%s_%x_%d", fallback, digest[:4], suffix)
			}
		}
		resolved[key] = candidate
		reserved[candidate] = struct{}{}
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
	if strings.TrimSpace(cfg.BearerToken) != "" && strings.TrimSpace(cfg.BearerTokenEnv) != "" {
		return fmt.Errorf("mcp server %q cannot set both bearer_token and bearer_token_env", id)
	}
	switch parsed.Scheme {
	case "http", "https":
	case "stdio":
		if _, err := parseStdioURL(rawURL); err != nil {
			return fmt.Errorf("mcp server %q url: %w", id, err)
		}
		if len(cfg.Headers) > 0 {
			return fmt.Errorf("mcp server %q stdio transport cannot set HTTP headers", id)
		}
		if strings.TrimSpace(cfg.BearerToken) != "" || strings.TrimSpace(cfg.BearerTokenEnv) != "" {
			return fmt.Errorf("mcp server %q stdio transport cannot set bearer token auth", id)
		}
		if cfg.DisableStandaloneSSE {
			return fmt.Errorf("mcp server %q stdio transport cannot disable standalone SSE", id)
		}
	default:
		return fmt.Errorf("mcp server %q url must use http, https, or stdio", id)
	}
	return nil
}

type stdioCommandConfig struct {
	Command string
	Args    []string
	CWD     string
	Env     []string
}

func newClientTransport(cfg config.MCPServer) (sdkmcp.Transport, error) {
	rawURL := strings.TrimSpace(cfg.URL)
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	switch parsed.Scheme {
	case "http", "https":
		return &sdkmcp.StreamableClientTransport{
			Endpoint:             rawURL,
			HTTPClient:           newHTTPClient(cfg),
			DisableStandaloneSSE: cfg.DisableStandaloneSSE,
		}, nil
	case "stdio":
		commandConfig, err := parseStdioURL(rawURL)
		if err != nil {
			return nil, err
		}
		cmd := exec.Command(commandConfig.Command, commandConfig.Args...)
		if commandConfig.CWD != "" {
			cmd.Dir = commandConfig.CWD
		}
		if len(commandConfig.Env) > 0 {
			cmd.Env = append(os.Environ(), commandConfig.Env...)
		}
		return &sdkmcp.CommandTransport{Command: cmd}, nil
	default:
		return nil, fmt.Errorf("unsupported MCP transport scheme %q", parsed.Scheme)
	}
}

func parseStdioURL(rawURL string) (stdioCommandConfig, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return stdioCommandConfig{}, err
	}
	if parsed.Scheme != "stdio" {
		return stdioCommandConfig{}, fmt.Errorf("scheme must be stdio")
	}

	command := strings.TrimSpace(parsed.Host)
	if command != "" {
		if path := strings.Trim(parsed.EscapedPath(), "/"); path != "" {
			return stdioCommandConfig{}, fmt.Errorf("path is not supported when the command is in the host; use repeated arg query parameters")
		}
	} else {
		command = strings.TrimSpace(parsed.Path)
	}
	if command == "" {
		return stdioCommandConfig{}, fmt.Errorf("command is empty")
	}

	query := parsed.Query()
	out := stdioCommandConfig{
		Command: command,
		Args:    slices.Clone(query["arg"]),
	}
	if values := query["cwd"]; len(values) > 1 {
		return stdioCommandConfig{}, fmt.Errorf("cwd can only be set once")
	} else if len(values) == 1 {
		out.CWD = values[0]
	}

	var unknown []string
	for key, values := range query {
		switch {
		case key == "arg", key == "cwd":
			continue
		case strings.HasPrefix(key, "env."):
			name := strings.TrimPrefix(key, "env.")
			if name == "" || strings.Contains(name, "=") {
				return stdioCommandConfig{}, fmt.Errorf("invalid environment variable name %q", name)
			}
			for _, value := range values {
				out.Env = append(out.Env, name+"="+value)
			}
		default:
			unknown = append(unknown, key)
		}
	}
	if len(unknown) > 0 {
		slices.Sort(unknown)
		return stdioCommandConfig{}, fmt.Errorf("unsupported query parameter %q", unknown[0])
	}
	return out, nil
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

type refreshKind uint8

const (
	refreshTools refreshKind = iota
	refreshResources
	refreshPrompts
)

type refreshRequest struct {
	serverID string
	kind     refreshKind
}

func (m *Manager) refreshServerAsync(id string, kind refreshKind) {
	request := refreshRequest{serverID: id, kind: kind}
	m.refreshMu.Lock()
	if m.refreshing == nil {
		m.refreshing = make(map[refreshRequest]bool)
		m.refreshDirty = make(map[refreshRequest]bool)
	}
	if m.refreshing[request] {
		m.refreshDirty[request] = true
		m.refreshMu.Unlock()
		return
	}
	m.refreshing[request] = true
	m.refreshMu.Unlock()
	go func() {
		for {
			m.refreshServer(id, kind)
			m.refreshMu.Lock()
			if m.refreshDirty[request] {
				m.refreshDirty[request] = false
				m.refreshMu.Unlock()
				continue
			}
			delete(m.refreshing, request)
			delete(m.refreshDirty, request)
			m.refreshMu.Unlock()
			return
		}
	}()
}

func (m *Manager) refreshServer(id string, kind refreshKind) {
	session, cfg, err := m.sessionAndConfigForServer(id)
	if err != nil {
		return
	}
	ctx, cancel := requestContext(context.Background(), cfg)
	defer cancel()
	var toolsList []ToolDescriptor
	var resources []ResourceDescriptor
	var templates []ResourceTemplateDescriptor
	var prompts []PromptDescriptor
	switch kind {
	case refreshTools:
		toolsList, err = collectTools(ctx, id, cfg, session)
	case refreshResources:
		resources, err = collectResources(ctx, id, cfg, session)
		if err == nil {
			templates, err = collectResourceTemplates(ctx, id, cfg, session)
		}
	case refreshPrompts:
		prompts, err = collectPrompts(ctx, id, cfg, session)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.state[id]
	if state == nil || state.session != session {
		return
	}
	if err != nil {
		state.lastErr = fmt.Sprintf("refresh MCP metadata: %v", err)
		return
	}
	switch kind {
	case refreshTools:
		state.tools = toolsList
	case refreshResources:
		state.resources = resources
		state.resourceTemplates = templates
	case refreshPrompts:
		state.prompts = prompts
	}
	state.lastErr = ""
}

func (m *Manager) sessionForServer(serverID string) (*sdkmcp.ClientSession, error) {
	session, _, err := m.sessionAndConfigForServer(serverID)
	return session, err
}

func (m *Manager) sessionAndConfigForServer(serverID string) (*sdkmcp.ClientSession, config.MCPServer, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state, ok := m.state[serverID]
	if !ok {
		return nil, config.MCPServer{}, fmt.Errorf("mcp server %q not configured", serverID)
	}
	if state.session == nil {
		return nil, config.MCPServer{}, fmt.Errorf("mcp server %q is not connected", serverID)
	}
	return state.session, cloneServerConfig(state.config), nil
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
			ServerID:        serverID,
			ServerName:      strings.TrimSpace(cfg.Name),
			Name:            strings.TrimSpace(tool.Name),
			Title:           title,
			Description:     tool.Description,
			InputSchema:     tool.InputSchema,
			OutputSchema:    tool.OutputSchema,
			ReadOnlyHint:    tool.Annotations != nil && tool.Annotations.ReadOnlyHint,
			DestructiveHint: cloneBoolPointer(toolAnnotationDestructive(tool)),
			IdempotentHint:  tool.Annotations != nil && tool.Annotations.IdempotentHint,
			OpenWorldHint:   cloneBoolPointer(toolAnnotationOpenWorld(tool)),
		})
	}
	return out, nil
}

func toolAnnotationDestructive(tool *sdkmcp.Tool) *bool {
	if tool == nil || tool.Annotations == nil {
		return nil
	}
	return tool.Annotations.DestructiveHint
}

func toolAnnotationOpenWorld(tool *sdkmcp.Tool) *bool {
	if tool == nil || tool.Annotations == nil {
		return nil
	}
	return tool.Annotations.OpenWorldHint
}

func cloneBoolPointer(value *bool) *bool {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
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

const (
	maximumMCPBinaryItemBytes = 10 << 20
	maximumMCPBinaryCallBytes = 25 << 20
)

func convertCallToolResult(serverID, serverName, toolName string, outputSchema any, res *sdkmcp.CallToolResult) (tools.Result, error) {
	if res == nil {
		return tools.Result{}, errors.New("nil mcp tool result")
	}
	contentItems := make([]tools.MCPStoredContentItem, 0, len(res.Content))
	lines := make([]string, 0, len(res.Content)+1)
	retainedBinaryBytes := int64(0)
	for _, item := range res.Content {
		rendered := renderContent(item)
		switch typed := item.(type) {
		case *sdkmcp.TextContent:
			contentItems = append(contentItems, tools.MCPStoredContentItem{Type: "text", Text: typed.Text})
		case *sdkmcp.ResourceLink:
			contentItems = append(contentItems, tools.MCPStoredContentItem{
				Type:        "resource_link",
				Text:        coalesce(typed.Title, typed.Name),
				Description: typed.Description,
				URI:         typed.URI,
				MIMEType:    typed.MIMEType,
			})
		case *sdkmcp.EmbeddedResource:
			contentItems = append(contentItems, contentItemFromResourceContents(typed.Resource, &retainedBinaryBytes))
		case *sdkmcp.ImageContent:
			contentItems = append(contentItems, binaryContentItem("image", "", typed.MIMEType, typed.Data, &retainedBinaryBytes))
		case *sdkmcp.AudioContent:
			contentItems = append(contentItems, binaryContentItem("audio", "", typed.MIMEType, typed.Data, &retainedBinaryBytes))
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
	// Typed MCP servers commonly return the same JSON once as text content and
	// once as structured content. Keep structured content in Stored, but only
	// render it into model-visible output when no normal content was supplied.
	if strings.TrimSpace(structured) != "" && len(lines) == 0 {
		lines = append(lines, structured)
	}
	validationError := validateStructuredContent(outputSchema, res.StructuredContent)
	needsInput := res.NeedsInput()
	inputRequests := ""
	if needsInput {
		if body, err := json.MarshalIndent(res.InputRequests, "", "  "); err == nil {
			inputRequests = string(body)
		}
		lines = append(lines, "MCP tool requires additional client input before it can complete.")
	}
	if validationError != nil {
		lines = append(lines, "MCP structured output failed its declared schema: "+validationError.Error())
	}
	output := strings.TrimSpace(strings.Join(lines, "\n\n"))
	if output == "" {
		if res.IsError {
			output = fmt.Sprintf("MCP tool %s/%s returned an error with no content", serverID, toolName)
		} else {
			output = fmt.Sprintf("MCP tool %s/%s completed with no content", serverID, toolName)
		}
	}
	status := domain.ToolResultStatusOK
	if res.IsError || needsInput || validationError != nil {
		status = domain.ToolResultStatusError
	}
	return tools.Result{
		Output: output,
		Status: status,
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
			NeedsInput:        needsInput,
			InputRequests:     inputRequests,
			RequestState:      res.RequestState,
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

func contentItemFromResourceContents(item *sdkmcp.ResourceContents, retained *int64) tools.MCPStoredContentItem {
	if item == nil {
		return tools.MCPStoredContentItem{Type: "resource"}
	}
	if len(item.Blob) > 0 {
		return binaryContentItem("resource", item.URI, item.MIMEType, item.Blob, retained)
	}
	return tools.MCPStoredContentItem{
		Type:     "resource",
		Text:     strings.TrimSpace(item.Text),
		URI:      strings.TrimSpace(item.URI),
		MIMEType: strings.TrimSpace(item.MIMEType),
	}
}

func binaryContentItem(kind, uri, mimeType string, data []byte, retained *int64) tools.MCPStoredContentItem {
	digest := sha256.Sum256(data)
	limit := int64(maximumMCPBinaryItemBytes)
	if remaining := int64(maximumMCPBinaryCallBytes) - *retained; remaining < limit {
		limit = max(0, remaining)
	}
	keep := min(int64(len(data)), limit)
	stored := slices.Clone(data[:keep])
	*retained += keep
	return tools.MCPStoredContentItem{
		Type: kind, URI: strings.TrimSpace(uri), MIMEType: strings.TrimSpace(mimeType),
		Data: stored, Size: int64(len(data)), SHA256: fmt.Sprintf("%x", digest[:]), Truncated: keep < int64(len(data)),
	}
}

func validateStructuredContent(schema, content any) error {
	if schema == nil || content == nil {
		return nil
	}
	schemaData, err := json.Marshal(schema)
	if err != nil {
		return fmt.Errorf("encode schema: %w", err)
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaData))
	if err != nil {
		return fmt.Errorf("parse schema: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("mcp-output.json", document); err != nil {
		return fmt.Errorf("load schema: %w", err)
	}
	compiled, err := compiler.Compile("mcp-output.json")
	if err != nil {
		return fmt.Errorf("compile schema: %w", err)
	}
	contentData, err := json.Marshal(content)
	if err != nil {
		return fmt.Errorf("encode output: %w", err)
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(contentData))
	if err != nil {
		return fmt.Errorf("parse output: %w", err)
	}
	return compiled.Validate(value)
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
	return &http.Client{
		Transport: headerRoundTripper{
			base:        http.DefaultTransport,
			headers:     cloneStringMap(cfg.Headers),
			bearerToken: strings.TrimSpace(cfg.BearerToken),
		},
	}
}

func requestContext(ctx context.Context, cfg config.MCPServer) (context.Context, context.CancelFunc) {
	timeout := cfg.RequestTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return context.WithTimeout(ctx, timeout)
}

func callToolWithTimeout(ctx context.Context, cfg config.MCPServer, session *sdkmcp.ClientSession, toolName string, args map[string]any) (*sdkmcp.CallToolResult, error) {
	callCtx, cancel := requestContext(ctx, cfg)
	defer cancel()
	return session.CallTool(callCtx, &sdkmcp.CallToolParams{Name: toolName, Arguments: args})
}

func readResourceWithTimeout(ctx context.Context, cfg config.MCPServer, session *sdkmcp.ClientSession, uri string) (*sdkmcp.ReadResourceResult, error) {
	callCtx, cancel := requestContext(ctx, cfg)
	defer cancel()
	return session.ReadResource(callCtx, &sdkmcp.ReadResourceParams{URI: strings.TrimSpace(uri)})
}

func getPromptWithTimeout(ctx context.Context, cfg config.MCPServer, session *sdkmcp.ClientSession, name string, args map[string]string) (*sdkmcp.GetPromptResult, error) {
	callCtx, cancel := requestContext(ctx, cfg)
	defer cancel()
	return session.GetPrompt(callCtx, &sdkmcp.GetPromptParams{Name: strings.TrimSpace(name), Arguments: args})
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
