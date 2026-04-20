package agent

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
)

func TestParseToolCall(t *testing.T) {
	call, plain := parseToolCall("I will inspect the repo.\n<koder_tool>\n{\"tool\":\"bash\",\"command\":\"pwd\"}\n</koder_tool>")
	if call == nil {
		t.Fatal("expected tool call")
	}
	if call.Tool != domain.ToolKindBash || call.Command != "pwd" {
		t.Fatalf("unexpected tool call: %#v", call)
	}
	if plain != "I will inspect the repo." {
		t.Fatalf("unexpected plain text: %q", plain)
	}
}

func TestSystemPromptDoesNotMentionInternalSlashCommands(t *testing.T) {
	prompt := systemPrompt()
	for _, command := range []string{"/new", "/quit", "/perm", "/mouse", "/approve", "/deny"} {
		if strings.Contains(prompt, command) {
			t.Fatalf("expected system prompt to exclude internal slash command %q", command)
		}
	}
}

func TestApprovalSerializationRoundTrip(t *testing.T) {
	req := tools.Request{
		Tool: domain.ToolKindApplyPatch,
		Args: map[string]string{
			"path":    "file.txt",
			"content": "hello",
		},
	}
	raw, err := serializeRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	got, err := requestFromStoredApproval(domain.ToolKindApplyPatch, raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Args["path"] != "file.txt" || got.Args["content"] != "hello" {
		t.Fatalf("unexpected round trip args: %#v", got.Args)
	}
}

func TestStringifyPartsExcludesSystemNotice(t *testing.T) {
	got := stringifyParts([]domain.Part{
		{Kind: domain.PartKindText, Body: "answer"},
		{Kind: domain.PartKindSystemNotice, Body: "usage", MetaJSON: `{"PromptTokens":1}`},
	})
	if strings.Contains(got, "PromptTokens") || strings.Contains(got, "usage") {
		t.Fatalf("expected system notices to stay out of model context, got %q", got)
	}
	if !strings.Contains(got, "answer") {
		t.Fatalf("expected text to remain in model context, got %q", got)
	}
}

func TestBuildConversationResetsAtCompactionBoundary(t *testing.T) {
	cfg := config.Default()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	engine := New(cfg, st, tools.NewRegistry(t.TempDir()), nil)
	session, err := st.CreateSession(context.Background(), "test", cfg.DefaultProvider, "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	before, err := st.AddMessage(context.Background(), session.ID, domain.MessageRoleUser, "before")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddPart(context.Background(), before.ID, domain.PartKindText, "old question", ""); err != nil {
		t.Fatal(err)
	}
	compactMsg, err := st.AddMessage(context.Background(), session.ID, domain.MessageRoleAssistant, "compact")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddPart(context.Background(), compactMsg.ID, domain.PartKindCompaction, "summary block", ""); err != nil {
		t.Fatal(err)
	}
	after, err := st.AddMessage(context.Background(), session.ID, domain.MessageRoleUser, "after")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddPart(context.Background(), after.ID, domain.PartKindText, "new question", ""); err != nil {
		t.Fatal(err)
	}

	conversation, err := engine.buildConversation(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(conversation) != 3 {
		t.Fatalf("expected system + compact summary + later message, got %#v", conversation)
	}
	if !strings.Contains(conversation[1].Content, "summary block") {
		t.Fatalf("expected compact summary in context, got %#v", conversation[1])
	}
	if strings.Contains(conversation[2].Content, "old question") || !strings.Contains(conversation[2].Content, "new question") {
		t.Fatalf("expected only post-compact history, got %#v", conversation[2])
	}
}

func TestStringifyPartsNormalizesToolCallFromMetadata(t *testing.T) {
	got := stringifyParts([]domain.Part{{
		Kind:     domain.PartKindToolCall,
		Body:     "Tool call:\n<koder_tool>\n{\"tool\":\"read\",\"path\":\"README.md\"}\n</koder_tool>",
		MetaJSON: `{"tool":"read","path":"README.md"}`,
	}})
	if !strings.Contains(got, `"tool":"read"`) || !strings.Contains(got, `"path":"README.md"`) {
		t.Fatalf("expected structured tool call in model context, got %q", got)
	}
	if strings.Contains(got, "<koder_tool>") || strings.Contains(got, "Tool call:\nTool call:") {
		t.Fatalf("expected raw wrapper text to be removed, got %q", got)
	}
}

func TestBuildConversationUsesStructuredToolMessages(t *testing.T) {
	cfg := config.Default()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	engine := New(cfg, st, tools.NewRegistry(t.TempDir()), nil)
	session, err := st.CreateSession(context.Background(), "test", cfg.DefaultProvider, "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	assistantMsg, err := st.AddMessage(context.Background(), session.ID, domain.MessageRoleAssistant, "tool:bash")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddPart(context.Background(), assistantMsg.ID, domain.PartKindToolCall, `{"tool":"bash","command":"pwd"}`, `{"tool_call_id":"call_1","tool":"bash","command":"pwd"}`); err != nil {
		t.Fatal(err)
	}
	toolMsg, err := st.AddMessage(context.Background(), session.ID, domain.MessageRoleTool, "bash")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddPart(context.Background(), toolMsg.ID, domain.PartKindToolOutput, "/tmp/workspace", `{"tool":"bash","tool_call_id":"call_1"}`); err != nil {
		t.Fatal(err)
	}

	conversation, err := engine.buildConversation(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(conversation) != 3 {
		t.Fatalf("expected system + assistant tool call + tool output, got %#v", conversation)
	}
	if len(conversation[1].ToolCalls) != 1 || conversation[1].ToolCalls[0].ID != "call_1" {
		t.Fatalf("expected structured assistant tool call, got %#v", conversation[1])
	}
	if conversation[2].Role != domain.MessageRoleTool || conversation[2].ToolCallID != "call_1" || conversation[2].Content != "/tmp/workspace" {
		t.Fatalf("expected structured tool message, got %#v", conversation[2])
	}
}

func TestApproveContinuesModelWithToolOutput(t *testing.T) {
	t.Parallel()

	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		requests = append(requests, string(body))
		callIndex := len(requests)
		switch callIndex {
		case 1:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"tool_calls":[{"id":"call_1","type":"function","function":{"name":"bash","arguments":"{\"command\":\"printf hello\"}"}}]}}],"usage":{"total_tokens":1}}`))
		case 2:
			if !strings.Contains(string(body), `"tool_call_id":"call_1"`) {
				t.Fatalf("expected second request to include tool call id, got %s", string(body))
			}
			if !strings.Contains(string(body), `"tool_calls":[{"id":"call_1","type":"function","function":{"name":"bash","arguments":"{\"command\":\"printf hello\"}"}}]`) {
				t.Fatalf("expected second request to include assistant tool call, got %s", string(body))
			}
			if !strings.Contains(string(body), `"role":"tool","content":"hello","tool_call_id":"call_1"`) {
				t.Fatalf("expected second request to include tool output, got %s", string(body))
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"done"}}],"usage":{"total_tokens":1}}`))
		default:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ignored title"}}],"usage":{"total_tokens":1}}`))
		}
	}))
	defer server.Close()

	cfg := config.Default()
	cfg.Providers = map[string]config.Provider{
		"test": {
			BaseURL: server.URL + "/v1",
			Timeout: time.Second,
		},
	}
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "test-model"
	cfg.Permissions.Profiles["default"] = config.PermissionProfile{
		Rules: []config.PermissionRule{
			{Tool: domain.ToolKindRead, Pattern: "*", Action: domain.PermissionModeAllow},
			{Tool: domain.ToolKindGlob, Pattern: "*", Action: domain.PermissionModeAllow},
			{Tool: domain.ToolKindGrep, Pattern: "*", Action: domain.PermissionModeAllow},
			{Tool: domain.ToolKindBash, Pattern: "*", Action: domain.PermissionModeAsk},
		},
	}

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	workdir := t.TempDir()
	engine := New(cfg, st, tools.NewRegistry(workdir), nil)
	session, err := st.CreateSession(context.Background(), "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSessionPermissionProfile(context.Background(), session.ID, "default"); err != nil {
		t.Fatal(err)
	}

	events, err := engine.RunPrompt(context.Background(), session, "say hello")
	if err != nil {
		t.Fatal(err)
	}
	var approvalID int64
	for evt := range events {
		if evt.Kind == domain.EventKindApprovalAsk {
			id, convErr := parseApprovalID(evt.Meta["approval_id"])
			if convErr != nil {
				t.Fatal(convErr)
			}
			approvalID = id
		}
	}
	if approvalID == 0 {
		t.Fatal("expected approval request")
	}

	approvedEvents, err := engine.approve(context.Background(), session.ID, strconv.FormatInt(approvalID, 10))
	if err != nil {
		t.Fatal(err)
	}
	var sawToolResult bool
	var sawFinalAnswer bool
	for evt := range approvedEvents {
		if evt.Kind == domain.EventKindToolResult && strings.Contains(evt.Text, "hello") {
			sawToolResult = true
		}
		if evt.Kind == domain.EventKindMessageDelta && evt.Text == "done" {
			sawFinalAnswer = true
		}
	}
	if !sawToolResult {
		t.Fatal("expected tool result event")
	}
	if !sawFinalAnswer {
		t.Fatal("expected final assistant answer after approval")
	}
	if len(requests) < 2 {
		t.Fatalf("expected at least two provider requests, got %d", len(requests))
	}
}

func TestRunPromptPersistsAssistantErrorOnBackendFailure(t *testing.T) {
	cfg := config.Default()
	cfg.Providers = map[string]config.Provider{
		"test": {
			BaseURL: "http://127.0.0.1:1/v1",
			Timeout: 50 * time.Millisecond,
		},
	}
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "test-model"

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	engine := New(cfg, st, tools.NewRegistry(t.TempDir()), nil)
	session, err := st.CreateSession(context.Background(), "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}

	events, err := engine.RunPrompt(context.Background(), session, "hello")
	if err != nil {
		t.Fatal(err)
	}

	var sawError bool
	for evt := range events {
		if evt.Kind == domain.EventKindError {
			sawError = true
		}
	}
	if !sawError {
		t.Fatal("expected backend failure event")
	}

	messages, parts, err := st.PartsForSession(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) < 2 {
		t.Fatalf("expected persisted user and assistant error messages, got %d", len(messages))
	}
	last := messages[len(messages)-1]
	if last.Role != domain.MessageRoleAssistant {
		t.Fatalf("expected assistant error message, got %s", last.Role)
	}
	errorParts := parts[last.ID]
	if len(errorParts) != 1 || errorParts[0].Kind != domain.PartKindText {
		t.Fatalf("expected one assistant text part, got %#v", errorParts)
	}
	if !strings.Contains(errorParts[0].Body, "Error:") {
		t.Fatalf("expected stored error prefix, got %q", errorParts[0].Body)
	}
}

func TestModelTaskPersistsTranscriptUpdate(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	engine := New(cfg, st, tools.NewRegistry(t.TempDir()), nil)
	session, err := st.CreateSession(context.Background(), "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}

	evt, err := engine.handleModelToolCall(context.Background(), session, toolCall{
		Tool: domain.ToolKindTask,
		Body: "write docs",
	})
	if err != nil {
		t.Fatal(err)
	}
	if evt.Kind != domain.EventKindTaskUpdate || evt.Text != "write docs" {
		t.Fatalf("unexpected task update event: %#v", evt)
	}

	messages, parts, err := st.PartsForSession(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 {
		t.Fatalf("expected one persisted message, got %d", len(messages))
	}
	if messages[0].Role != domain.MessageRoleTool {
		t.Fatalf("expected tool role, got %s", messages[0].Role)
	}
	if got := parts[messages[0].ID][0].Kind; got != domain.PartKindTaskUpdate {
		t.Fatalf("expected task update part, got %s", got)
	}
	if got := parts[messages[0].ID][0].Body; got != "write docs" {
		t.Fatalf("unexpected task update body: %q", got)
	}
}

func TestPersistToolResultSynthesizesVisibleOutputWhenToolReturnsNothing(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	engine := New(cfg, st, tools.NewRegistry(t.TempDir()), nil)
	session, err := st.CreateSession(context.Background(), "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}

	events, err := engine.persistToolResult(context.Background(), session.ID, domain.ToolKindBash, "", tools.Result{})
	if err != nil {
		t.Fatal(err)
	}
	evt := <-events
	if evt.Kind != domain.EventKindToolResult || !strings.Contains(evt.Text, "completed with no output") {
		t.Fatalf("unexpected tool result event: %#v", evt)
	}

	messages, parts, err := st.PartsForSession(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 {
		t.Fatalf("expected one tool message, got %d", len(messages))
	}
	if got := parts[messages[0].ID][0].Body; !strings.Contains(got, "completed with no output") {
		t.Fatalf("expected synthesized visible tool output, got %q", got)
	}
}

func parseApprovalID(raw string) (int64, error) {
	return strconv.ParseInt(raw, 10, 64)
}

func TestErrorSummaryPrefixesMessage(t *testing.T) {
	got := errorSummary(errors.New("connection refused"))
	if got != "Error: connection refused" {
		t.Fatalf("unexpected error summary: %q", got)
	}
}
