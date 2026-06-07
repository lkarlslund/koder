package tools_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/tools"
	_ "github.com/lkarlslund/koder/internal/tools/all"
)

func TestRequestJSONRoundTrip(t *testing.T) {
	original := tools.Request{
		Tool:       domain.ToolKindFileWrite,
		ToolCallID: "call_1",
		Args: map[string]string{
			"path":    "notes.txt",
			"content": "hello",
		},
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var decoded tools.Request
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Tool != original.Tool || decoded.ToolCallID != original.ToolCallID || decoded.Args["path"] != "notes.txt" || decoded.Args["content"] != "hello" {
		t.Fatalf("unexpected decoded request: %#v", decoded)
	}
}

func TestParseProviderCallRejectsMissingToolCallID(t *testing.T) {
	_, err := tools.ParseProviderCall(provider.ToolCall{
		Function: provider.FunctionCall{
			Name:      domain.ToolKindFileWrite.String(),
			Arguments: `{"path":"notes.txt","content":"hello"}`,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "missing id") {
		t.Fatalf("expected missing id error, got %v", err)
	}
}

func TestParseProviderCallRejectsOversizedArguments(t *testing.T) {
	_, err := tools.ParseProviderCall(provider.ToolCall{
		ID: "call_1",
		Function: provider.FunctionCall{
			Name:      domain.ToolKindBash.String(),
			Arguments: `{"command":"` + strings.Repeat("x", 9*1024) + `"}`,
		},
	})
	var callErr tools.ProviderCallError
	if !errors.As(err, &callErr) {
		t.Fatalf("expected ProviderCallError, got %T %v", err, err)
	}
	if callErr.Request.Tool != domain.ToolKindBash || callErr.Request.ToolCallID != "call_1" {
		t.Fatalf("expected partial bash request identity, got %#v", callErr.Request)
	}
	if !strings.Contains(err.Error(), "bash tool arguments exceeded 8 KiB") {
		t.Fatalf("expected byte limit error, got %v", err)
	}
}

func TestParseProviderCallStoresNormalizedArguments(t *testing.T) {
	req, err := tools.ParseProviderCall(provider.ToolCall{
		ID: "call_1",
		Function: provider.FunctionCall{
			Name:      domain.ToolKindFileRead.String(),
			Arguments: `{"path":"README.md","start_line":"150.0000","end_line":"175.0000"}`,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := req.Args["start_line"]; got != "150" {
		t.Fatalf("expected normalized start_line, got %q", got)
	}
	if got := req.Args["end_line"]; got != "175" {
		t.Fatalf("expected normalized end_line, got %q", got)
	}
}

func TestParseProviderCallReturnsPartialRequestOnNormalizeError(t *testing.T) {
	_, err := tools.ParseProviderCall(provider.ToolCall{
		ID: "call_1",
		Function: provider.FunctionCall{
			Name:      domain.ToolKindTasksUpdate.String(),
			Arguments: `{"id":"019aa000-0000-7000-8000-000000000001","status":"InProgress"}`,
		},
	})
	var callErr tools.ProviderCallError
	if !errors.As(err, &callErr) {
		t.Fatalf("expected ProviderCallError, got %T %v", err, err)
	}
	if callErr.Request.Tool != domain.ToolKindTasksUpdate || callErr.Request.ToolCallID != "call_1" {
		t.Fatalf("expected partial task request identity, got %#v", callErr.Request)
	}
	if callErr.Request.Args["status"] != "InProgress" {
		t.Fatalf("expected raw status in partial request, got %#v", callErr.Request.Args)
	}
}

func TestWriteDefinitionForceOverwriteOptional(t *testing.T) {
	def, enabled := tools.DefinitionFor(domain.ToolKindFileWrite, tools.Runtime{})
	if !enabled {
		t.Fatal("expected write definition")
	}
	var schema struct {
		Properties map[string]any `json:"properties"`
		Required   []string       `json:"required"`
	}
	if err := json.Unmarshal(def.Function.Parameters, &schema); err != nil {
		t.Fatal(err)
	}
	if _, ok := schema.Properties["force_overwrite"]; !ok {
		t.Fatalf("expected force_overwrite property in %#v", schema.Properties)
	}
	for _, required := range schema.Required {
		if required == "force_overwrite" {
			t.Fatalf("force_overwrite must be optional, required fields: %#v", schema.Required)
		}
	}
}

func TestRequestFromStoredRejectsUnstructuredArgs(t *testing.T) {
	_, err := tools.RequestFromStored(domain.ToolKindFileWrite, "notes.txt")
	if err == nil || !strings.Contains(err.Error(), "decode stored tool arguments") {
		t.Fatalf("expected structured stored arguments error, got %v", err)
	}
}

func TestRequestFromMetaRejectsEmpty(t *testing.T) {
	_, err := tools.RequestFromMeta("")
	if err == nil || !strings.Contains(err.Error(), "empty request metadata") {
		t.Fatalf("expected empty metadata error, got %v", err)
	}
}

func TestRequireChatControlRequiresActiveChat(t *testing.T) {
	_, err := tools.RequireChatControl(tools.Runtime{})
	if err == nil || !strings.Contains(err.Error(), "active persisted chat") {
		t.Fatalf("expected missing chat control error, got %v", err)
	}
}

func TestBuildToolResultPreservesPayloadAndDiff(t *testing.T) {
	result, _, err := tools.BuildToolResult(tools.Request{
		Tool:       domain.ToolKindFileWrite,
		ToolCallID: "call_write",
		Args:       map[string]string{"path": "notes.txt"},
	}, tools.Result{
		Output:   "Created notes.txt",
		DiffText: "diff --git a/notes.txt b/notes.txt",
		Meta:     map[string]string{"path": "notes.txt"},
		Stored: tools.WriteStoredResult{
			Path:    "notes.txt",
			Action:  "created",
			Summary: "Created notes.txt",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(result.Diff) == "" {
		t.Fatalf("expected diff on tool result, got %#v", result)
	}
	payload, ok := result.Data.(domain.WriteStoredResult)
	if !ok || payload.Path != "notes.txt" {
		t.Fatalf("expected typed write payload, got %#v", result.Data)
	}
}
