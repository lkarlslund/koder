package tools_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
	_ "github.com/lkarlslund/koder/internal/tools/all"
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
			Name:      string(domain.ToolKindWrite),
			Arguments: `{"path":"notes.txt","content":"hello"}`,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "missing id") {
		t.Fatalf("expected missing id error, got %v", err)
	}
}

func TestRequestFromStoredUsesLegacyArgs(t *testing.T) {
	req, err := tools.RequestFromStored(domain.ToolKindWrite, "notes.txt")
	if err != nil {
		t.Fatal(err)
	}
	if req.Tool != domain.ToolKindWrite || req.Args["path"] != "notes.txt" {
		t.Fatalf("unexpected legacy request: %#v", req)
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
	session, err := st.CreateSession(context.Background(), "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}

	events, err := tools.PersistStandardResult(context.Background(), st, session.ID, tools.Request{
		Tool: domain.ToolKindWrite,
		Args: map[string]string{"path": "notes.txt"},
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

	messages, partsByMessage, err := st.PartsForSession(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 {
		t.Fatalf("expected one stored message, got %d", len(messages))
	}
	parts := partsByMessage[messages[0].ID]
	if len(parts) != 1 {
		t.Fatalf("expected tool output part, got %#v", parts)
	}
	if parts[0].Kind != domain.PartKindToolOutput {
		t.Fatalf("expected tool output part, got %s", parts[0].Kind)
	}
	payload, ok := parts[0].Payload.(domain.ToolOutputPayload)
	if !ok || payload.Tool != domain.ToolKindWrite || strings.TrimSpace(payload.Diff) == "" {
		t.Fatalf("expected typed write payload with diff, got %#v", parts[0].Payload)
	}
}
