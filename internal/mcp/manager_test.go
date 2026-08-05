package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/provider"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPStdioHelperProcess(t *testing.T) {
	if os.Getenv("KODER_MCP_STDIO_HELPER") != "1" {
		return
	}

	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "stdio-docs", Version: "v1.0.0"}, nil)
	server.AddTool(&sdkmcp.Tool{
		Name:        "echo",
		Description: "Echo text",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}, "additionalProperties": false},
	}, func(_ context.Context, req *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		var args struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
			return nil, err
		}
		return &sdkmcp.CallToolResult{
			Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "stdio " + args.Text}},
		}, nil
	})

	if err := server.Run(context.Background(), &sdkmcp.StdioTransport{}); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func TestManagerConnectsDiscoversAndExecutes(t *testing.T) {
	t.Helper()
	ctx := context.Background()

	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "remote-docs", Version: "v1.0.0"}, nil)
	server.AddTool(&sdkmcp.Tool{
		Name:        "greet",
		Description: "Say hi",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}, "additionalProperties": false},
	}, func(_ context.Context, req *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		return &sdkmcp.CallToolResult{
			Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "hello " + string(req.Params.Arguments)}},
		}, nil
	})
	server.AddResource(&sdkmcp.Resource{
		URI:         "file:///guide",
		Name:        "guide",
		Title:       "Guide",
		Description: "Docs guide",
		MIMEType:    "text/plain",
	}, func(_ context.Context, req *sdkmcp.ReadResourceRequest) (*sdkmcp.ReadResourceResult, error) {
		return &sdkmcp.ReadResourceResult{
			Contents: []*sdkmcp.ResourceContents{{
				URI:      req.Params.URI,
				MIMEType: "text/plain",
				Text:     "hello guide",
			}},
		}, nil
	})
	server.AddResourceTemplate(&sdkmcp.ResourceTemplate{
		URITemplate: "file:///guide/{slug}",
		Name:        "guide-template",
		Title:       "Guide Template",
		Description: "Template",
		MIMEType:    "text/plain",
	}, nil)
	server.AddPrompt(&sdkmcp.Prompt{
		Name:        "review",
		Title:       "Review Prompt",
		Description: "Review prompt",
		Arguments: []*sdkmcp.PromptArgument{{
			Name:        "topic",
			Description: "Topic",
			Required:    true,
		}},
	}, func(_ context.Context, req *sdkmcp.GetPromptRequest) (*sdkmcp.GetPromptResult, error) {
		return &sdkmcp.GetPromptResult{
			Description: "Prompt description",
			Messages: []*sdkmcp.PromptMessage{{
				Role:    "user",
				Content: &sdkmcp.TextContent{Text: "review " + req.Params.Arguments["topic"]},
			}},
		}, nil
	})

	handler := sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return server }, &sdkmcp.StreamableHTTPOptions{JSONResponse: true})
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if got := req.Header.Get("Authorization"); got != "Bearer secret" {
			t.Fatalf("expected Authorization header, got %q", got)
		}
		if got := req.Header.Get("X-Test"); got != "yes" {
			t.Fatalf("expected custom header, got %q", got)
		}
		handler.ServeHTTP(w, req)
	}))
	defer httpServer.Close()

	manager, err := NewManager(map[string]config.MCPServer{
		"docs": {
			Name:        "Docs",
			URL:         httpServer.URL,
			Headers:     map[string]string{"X-Test": "yes"},
			BearerToken: "secret",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := manager.DisconnectServer("docs"); err != nil {
			t.Errorf("disconnect docs server: %v", err)
		}
	}()
	if err := manager.ConnectAll(ctx); err != nil {
		t.Fatal(err)
	}

	states := manager.ListServers()
	if len(states) != 1 {
		t.Fatalf("expected 1 server state, got %d", len(states))
	}
	state := states[0]
	if state.Status != ServerStatusConnected {
		t.Fatalf("expected connected status, got %s", state.Status)
	}
	if state.ToolCount != 1 || state.ResourceCount != 1 || state.ResourceTemplateCount != 1 || state.PromptCount != 1 {
		t.Fatalf("unexpected discovery counts: %#v", state)
	}

	defs := manager.ToolDefinitions()
	if len(defs) != 1 {
		t.Fatalf("expected 1 dynamic tool definition, got %d", len(defs))
	}
	if defs[0].Function.Name != "greet" {
		t.Fatalf("unexpected tool definition name: %s", defs[0].Function.Name)
	}

	result, err := manager.ExecuteTool(ctx, "docs", "greet", map[string]any{"name": "Pat"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "\"name\":\"Pat\"") {
		t.Fatalf("unexpected tool output: %q", result.Output)
	}

	resource, err := manager.ReadResource(ctx, "docs", "file:///guide")
	if err != nil {
		t.Fatal(err)
	}
	if len(resource.Contents) != 1 || resource.Contents[0].Text != "hello guide" {
		t.Fatalf("unexpected resource contents: %#v", resource)
	}

	prompt, err := manager.GetPrompt(ctx, "docs", "review", map[string]string{"topic": "apis"})
	if err != nil {
		t.Fatal(err)
	}
	if len(prompt.Messages) != 1 || prompt.Messages[0].Text != "review apis" {
		t.Fatalf("unexpected prompt result: %#v", prompt)
	}
}

func TestManagerConnectsStdioServer(t *testing.T) {
	ctx := context.Background()
	values := url.Values{}
	values.Add("arg", "-test.run=TestMCPStdioHelperProcess")
	values.Add("env.KODER_MCP_STDIO_HELPER", "1")
	stdioURL := (&url.URL{Scheme: "stdio", Path: os.Args[0], RawQuery: values.Encode()}).String()

	manager, err := NewManager(map[string]config.MCPServer{
		"stdio": {
			Name: "Stdio",
			URL:  stdioURL,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := manager.DisconnectServer("stdio"); err != nil {
			t.Errorf("disconnect stdio server: %v", err)
		}
	}()
	if err := manager.ConnectAll(ctx); err != nil {
		t.Fatal(err)
	}

	states := manager.ListServers()
	if len(states) != 1 || states[0].Status != ServerStatusConnected || states[0].ToolCount != 1 {
		t.Fatalf("unexpected stdio server state: %#v", states)
	}
	result, err := manager.ExecuteTool(ctx, "stdio", "echo", map[string]any{"text": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "stdio hello") {
		t.Fatalf("unexpected stdio tool output: %q", result.Output)
	}
}

func TestParseStdioURL(t *testing.T) {
	t.Parallel()
	cfg, err := parseStdioURL("stdio://uvx?arg=mcp-server-git&arg=--repository&arg=/tmp/repo&cwd=/tmp&env.FOO=bar")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Command != "uvx" {
		t.Fatalf("unexpected command: %q", cfg.Command)
	}
	if !slices.Equal(cfg.Args, []string{"mcp-server-git", "--repository", "/tmp/repo"}) {
		t.Fatalf("unexpected args: %#v", cfg.Args)
	}
	if cfg.CWD != "/tmp" {
		t.Fatalf("unexpected cwd: %q", cfg.CWD)
	}
	if !slices.Equal(cfg.Env, []string{"FOO=bar"}) {
		t.Fatalf("unexpected env: %#v", cfg.Env)
	}
}

func TestValidateServerConfigAcceptsStdioURL(t *testing.T) {
	t.Parallel()
	if err := ValidateServerConfig("git", config.MCPServer{URL: "stdio://uvx?arg=mcp-server-git"}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateServerConfigRejectsStdioHTTPOptions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		cfg  config.MCPServer
	}{
		{name: "headers", cfg: config.MCPServer{URL: "stdio://uvx", Headers: map[string]string{"X-Test": "yes"}}},
		{name: "bearer token", cfg: config.MCPServer{URL: "stdio://uvx", BearerToken: "secret"}},
		{name: "bearer token env", cfg: config.MCPServer{URL: "stdio://uvx", BearerTokenEnv: "SECRET"}},
		{name: "standalone sse", cfg: config.MCPServer{URL: "stdio://uvx", DisableStandaloneSSE: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if err := ValidateServerConfig("stdio", tt.cfg); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestResolveToolNameUsesPlainNameWhenUnique(t *testing.T) {
	manager := &Manager{
		state: map[string]*serverState{
			"docs": {tools: []ToolDescriptor{{ServerID: "docs", Name: "search"}}},
		},
	}
	serverID, toolName, ok := manager.ResolveToolName("search", nil)
	if !ok {
		t.Fatal("expected resolve success")
	}
	if serverID != "docs" || toolName != "search" {
		t.Fatalf("unexpected resolved name: %q %q", serverID, toolName)
	}
}

func TestToolDefinitionsFallbackOnCollision(t *testing.T) {
	manager := &Manager{
		state: map[string]*serverState{
			"docs": {tools: []ToolDescriptor{{ServerID: "docs", Name: "search"}}},
			"exa":  {tools: []ToolDescriptor{{ServerID: "exa", Name: "search"}}},
		},
	}
	defs := manager.ToolDefinitions()
	got := []string{defs[0].Function.Name, defs[1].Function.Name}
	slices.Sort(got)
	want := []string{"_docs_search", "_exa_search"}
	if !slices.Equal(got, want) {
		t.Fatalf("unexpected fallback names: got=%v want=%v", got, want)
	}
}

func TestToolDefinitionsFallbackOnLocalCollision(t *testing.T) {
	manager := &Manager{
		state: map[string]*serverState{
			"exa": {tools: []ToolDescriptor{{ServerID: "exa", Name: "read"}}},
		},
	}
	defs := manager.ToolDefinitionsWithReserved([]provider.ToolDefinition{{
		Type: "function",
		Function: provider.FunctionDefinition{
			Name: "read",
		},
	}})
	if len(defs) != 1 {
		t.Fatalf("expected 1 dynamic tool definition, got %d", len(defs))
	}
	if defs[0].Function.Name != "_exa_read" {
		t.Fatalf("unexpected fallback name: %s", defs[0].Function.Name)
	}
	serverID, toolName, ok := manager.ResolveToolName("_exa_read", []provider.ToolDefinition{{
		Type:     "function",
		Function: provider.FunctionDefinition{Name: "read"},
	}})
	if !ok || serverID != "exa" || toolName != "read" {
		t.Fatalf("unexpected resolved fallback: ok=%v server=%q tool=%q", ok, serverID, toolName)
	}
}

func TestListToolsSortsDescriptorsDeterministically(t *testing.T) {
	manager := &Manager{
		state: map[string]*serverState{
			"b": {tools: []ToolDescriptor{
				{ServerID: "b", Name: "zeta", Title: "Zeta"},
				{ServerID: "b", Name: "alpha", Title: "Alpha"},
			}},
			"a": {tools: []ToolDescriptor{
				{ServerID: "a", Name: "beta", Title: "Beta"},
				{ServerID: "a", Name: "alpha", Title: "Alpha"},
			}},
		},
	}

	got := manager.ListTools()
	names := make([]string, 0, len(got))
	for _, item := range got {
		names = append(names, item.ServerID+"/"+item.Name)
	}
	want := []string{"a/alpha", "a/beta", "b/alpha", "b/zeta"}
	if !slices.Equal(names, want) {
		t.Fatalf("unexpected sorted tools: got=%v want=%v", names, want)
	}
}

func TestToolDefinitionsStableAcrossDiscoveryOrder(t *testing.T) {
	left := []ToolDescriptor{
		{ServerID: "docs", Name: "search"},
		{ServerID: "exa", Name: "search"},
		{ServerID: "docs", Name: "fetch"},
	}
	right := []ToolDescriptor{
		{ServerID: "exa", Name: "search"},
		{ServerID: "docs", Name: "fetch"},
		{ServerID: "docs", Name: "search"},
	}
	managerA := &Manager{
		state: map[string]*serverState{
			"docs": {tools: []ToolDescriptor{left[0], left[2]}},
			"exa":  {tools: []ToolDescriptor{left[1]}},
		},
	}
	managerB := &Manager{
		state: map[string]*serverState{
			"exa":  {tools: []ToolDescriptor{right[0]}},
			"docs": {tools: []ToolDescriptor{right[1], right[2]}},
		},
	}

	defsA := managerA.ToolDefinitions()
	defsB := managerB.ToolDefinitions()
	if len(defsA) != len(defsB) {
		t.Fatalf("tool definition count mismatch: %d vs %d", len(defsA), len(defsB))
	}
	namesA := make([]string, 0, len(defsA))
	namesB := make([]string, 0, len(defsB))
	for i := range defsA {
		namesA = append(namesA, defsA[i].Function.Name)
		namesB = append(namesB, defsB[i].Function.Name)
	}
	if !slices.Equal(namesA, namesB) {
		t.Fatalf("tool names should be stable across discovery order: %v vs %v", namesA, namesB)
	}
	serverA, toolA, okA := managerA.ResolveToolName("_docs_search", nil)
	serverB, toolB, okB := managerB.ResolveToolName("_docs_search", nil)
	if !okA || !okB || serverA != "docs" || serverB != "docs" || toolA != "search" || toolB != "search" {
		t.Fatalf("expected stable name resolution, got A=(%v,%q,%q) B=(%v,%q,%q)", okA, serverA, toolA, okB, serverB, toolB)
	}
}
