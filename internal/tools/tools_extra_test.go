package tools_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/chatstore"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/sessionstore"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
	_ "github.com/lkarlslund/koder/internal/tools/all"
	"github.com/lkarlslund/koder/internal/tools/tooltest"
)

func openToolsTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.OpenWithOptions(t.TempDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})
	return st
}

func TestRequestJSONRoundTrip(t *testing.T) {
	original := tools.Request{
		Tool:       domain.ToolKindWrite,
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
			Name:      domain.ToolKindWrite.String(),
			Arguments: `{"path":"notes.txt","content":"hello"}`,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "missing id") {
		t.Fatalf("expected missing id error, got %v", err)
	}
}

func TestParseProviderCallStoresNormalizedArguments(t *testing.T) {
	req, err := tools.ParseProviderCall(provider.ToolCall{
		ID: "call_1",
		Function: provider.FunctionCall{
			Name:      domain.ToolKindRead.String(),
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

func TestDefinitionsDoNotExposeApplyPatch(t *testing.T) {
	for _, def := range tools.Definitions(tools.Runtime{}) {
		if def.Function.Name == "apply_patch" {
			t.Fatal("apply_patch should not be exposed")
		}
	}
}

func TestWriteDefinitionForceOverwriteOptional(t *testing.T) {
	def, enabled := tools.DefinitionFor(domain.ToolKindWrite, tools.Runtime{})
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
	_, err := tools.RequestFromStored(domain.ToolKindWrite, "notes.txt")
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

func TestPersistStandardResultPersistsMessagePartAndDiff(t *testing.T) {
	st := openToolsTestStore(t)
	session, err := sessionstore.CreateSession(context.Background(), st, "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	chat, err := sessionstore.DefaultChat(context.Background(), st, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = chatstore.AppendAssistantToolCalls(context.Background(), st, chat.ID, []domain.ToolCall{{
		ToolCallID: "call_write",
		Tool:       domain.ToolKindWrite,
		Args:       map[string]string{"path": "notes.txt"},
		Status:     domain.ToolStatusPending,
	}}, "", domain.Usage{})
	if err != nil {
		t.Fatal(err)
	}

	events, err := tools.PersistStandardResult(tools.WithChatID(context.Background(), chat.ID), tools.Runtime{Store: st, SessionID: session.ID, SessionControl: tooltest.NewSessionControl(st)}, tools.Request{
		Tool:       domain.ToolKindWrite,
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
	evt := <-events
	if evt.Kind != domain.EventKindToolResult || evt.Tool != domain.ToolKindWrite {
		t.Fatalf("unexpected event: %#v", evt)
	}

	items, err := chatstore.TimelineForChat(context.Background(), st, chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one stored item, got %d", len(items))
	}
	assistant, ok := items[0].Content.(domain.AssistantMessage)
	if !ok || len(assistant.Tools) != 1 || assistant.Tools[0].Result == nil {
		t.Fatalf("expected tool result child, got %#v", items[0].Content)
	}
	if strings.TrimSpace(assistant.Tools[0].Result.Diff) == "" {
		t.Fatalf("expected diff on tool result, got %#v", assistant.Tools[0].Result)
	}
	payload, ok := assistant.Tools[0].Result.Data.(domain.WriteStoredResult)
	if !ok || payload.Path != "notes.txt" {
		t.Fatalf("expected typed write payload, got %#v", assistant.Tools[0].Result.Data)
	}
}
