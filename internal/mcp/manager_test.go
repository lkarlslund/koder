package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/provider"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

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
