package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/attachment"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/permission"
	"github.com/lkarlslund/koder/internal/reference"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
)

func TestParseToolCall(t *testing.T) {
	call, plain := parseToolCall("I will inspect the repo.\n<koder_tool>\n{\"tool\":\"bash\",\"command\":\"pwd\"}\n</koder_tool>")
	if call == nil {
		t.Fatal("expected tool call")
	}
	if call.Tool != domain.ToolKindBash || call.Args["command"] != "pwd" {
		t.Fatalf("unexpected tool call: %#v", call)
	}
	if plain != "I will inspect the repo." {
		t.Fatalf("unexpected plain text: %q", plain)
	}
}

func testConfig(t *testing.T) config.Config {
	t.Helper()
	return config.Default().WithStateDir(t.TempDir())
}

func TestSystemPromptDoesNotMentionInternalSlashCommands(t *testing.T) {
	prompt := systemPrompt()
	for _, command := range []string{"/new", "/quit", "/permissions", "/mouse", "/approve", "/deny"} {
		if strings.Contains(prompt, command) {
			t.Fatalf("expected system prompt to exclude internal slash command %q", command)
		}
	}
}

func TestMaxToolLoopStepsDefaultsToTwenty(t *testing.T) {
	engine := New(testConfig(t), nil, nil, nil, t.TempDir())
	if got := engine.maxToolLoopSteps(); got != 20 {
		t.Fatalf("expected default max tool loop steps 20, got %d", got)
	}
}

func TestMaxToolLoopStepsUsesConfiguredValue(t *testing.T) {
	cfg := testConfig(t)
	cfg.MaxToolLoopSteps = 7

	engine := New(cfg, nil, nil, nil, t.TempDir())
	if got := engine.maxToolLoopSteps(); got != 7 {
		t.Fatalf("expected configured max tool loop steps 7, got %d", got)
	}
}

func TestApprovalSerializationRoundTrip(t *testing.T) {
	req := tools.Request{
		Tool: domain.ToolKindApplyPatch,
		Args: map[string]string{
			"patch": "--- a/file.txt\n+++ b/file.txt\n@@ -1 +1 @@\n-before\n+after\n",
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
	if got.Args["patch"] == "" {
		t.Fatalf("unexpected round trip args: %#v", got.Args)
	}
}

func TestHandleModelToolCallDeniesDisabledSessionTool(t *testing.T) {
	cfg := testConfig(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	engine := New(cfg, st, tools.NewRegistry(t.TempDir()), nil, t.TempDir())
	session, err := st.CreateSession(context.Background(), "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSessionToolStates(context.Background(), session.ID, map[domain.ToolKind]bool{
		domain.ToolKindRead: false,
	}); err != nil {
		t.Fatal(err)
	}

	evt, err := engine.handleModelToolCall(context.Background(), session, tools.Request{
		Tool: domain.ToolKindRead,
		Args: map[string]string{"path": "README.md"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if evt.Kind != domain.EventKindToolResult {
		t.Fatalf("expected tool result event, got %#v", evt)
	}
	if !strings.Contains(evt.Text, "disabled for this session") {
		t.Fatalf("expected disabled tool message, got %#v", evt)
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

func TestPersistAssistantToolCallsStoresNarrationAsText(t *testing.T) {
	cfg := testConfig(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	engine := New(cfg, st, tools.NewRegistry(t.TempDir()), nil, t.TempDir())
	session, err := st.CreateSession(context.Background(), "test", cfg.DefaultProvider, "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}

	call := tools.Request{
		Tool:       domain.ToolKindRead,
		ToolCallID: "call_1",
		Args:       map[string]string{"path": "README.md"},
	}
	if err := engine.persistAssistantToolCalls(context.Background(), session.ID, []tools.Request{call}, "Let me inspect that file first.", domain.Usage{TotalTokens: 10}); err != nil {
		t.Fatal(err)
	}

	messages, parts, err := st.PartsForSession(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 {
		t.Fatalf("expected one assistant message, got %d", len(messages))
	}
	got := parts[messages[0].ID]
	if len(got) < 3 {
		t.Fatalf("expected text, tool call, and usage parts, got %#v", got)
	}
	if got[0].Kind != domain.PartKindToolCall {
		t.Fatalf("expected first part to be tool call, got %#v", got[0])
	}
	if got[1].Kind != domain.PartKindText || !strings.Contains(got[1].Body, "inspect that file") {
		t.Fatalf("expected narration to be stored as text, got %#v", got[1])
	}
	if got[2].Kind != domain.PartKindSystemNotice || got[2].Body != "usage" {
		t.Fatalf("expected usage to remain a system notice, got %#v", got[2])
	}
}

func TestBuildConversationIncludesAssistantNarrationAlongsideToolCalls(t *testing.T) {
	cfg := testConfig(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	engine := New(cfg, st, tools.NewRegistry(t.TempDir()), nil, t.TempDir())
	session, err := st.CreateSession(context.Background(), "test", cfg.DefaultProvider, "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}

	assistantMsg, err := st.AddMessage(context.Background(), session.ID, domain.MessageRoleAssistant, "tool:read")
	if err != nil {
		t.Fatal(err)
	}
	req := tools.Request{
		Tool:       domain.ToolKindRead,
		ToolCallID: "call_1",
		Args:       map[string]string{"path": "README.md"},
	}
	meta, _ := json.Marshal(req)
	if _, err := st.AddPart(context.Background(), assistantMsg.ID, domain.PartKindToolCall, req.ContextString(), string(meta)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddPart(context.Background(), assistantMsg.ID, domain.PartKindText, "Let me inspect that file first.", ""); err != nil {
		t.Fatal(err)
	}

	conversation, err := engine.buildConversation(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(conversation) == 0 {
		t.Fatal("expected conversation entries")
	}
	got := conversation[len(conversation)-1]
	if got.Role != domain.MessageRoleAssistant {
		t.Fatalf("expected assistant conversation entry, got %#v", got)
	}
	if !strings.Contains(got.Content, "inspect that file") {
		t.Fatalf("expected assistant narration in structured assistant message, got %#v", got)
	}
	if len(got.ToolCalls) != 1 || got.ToolCalls[0].ID != "call_1" {
		t.Fatalf("expected structured tool call to remain attached, got %#v", got)
	}
}

func TestBuildConversationResetsAtCompactionBoundary(t *testing.T) {
	cfg := testConfig(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	engine := New(cfg, st, tools.NewRegistry(t.TempDir()), nil, t.TempDir())
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
	if len(conversation) < 3 {
		t.Fatalf("expected compact summary and later message, got %#v", conversation)
	}
	if !strings.Contains(conversation[len(conversation)-2].Content, "summary block") {
		t.Fatalf("expected compact summary in context, got %#v", conversation[len(conversation)-2])
	}
	if strings.Contains(conversation[len(conversation)-1].Content, "old question") || !strings.Contains(conversation[len(conversation)-1].Content, "new question") {
		t.Fatalf("expected only post-compact history, got %#v", conversation[len(conversation)-1])
	}
}

func TestBuildConversationIncludesSkillPromptContext(t *testing.T) {
	cfg := testConfig(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	home := t.TempDir()
	t.Setenv("HOME", home)

	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	skillPath := filepath.Join(repo, ".agents", "skills", "review", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skillPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillPath, []byte("---\nname: review\ndescription: Review code carefully\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	engine := New(cfg, st, tools.NewRegistry(repo), nil, repo)
	session, err := st.CreateSession(context.Background(), "test", cfg.DefaultProvider, "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}

	conversation, err := engine.buildConversation(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(conversation) < 2 {
		t.Fatalf("expected system prompt and skill prompt, got %#v", conversation)
	}
	if !strings.Contains(conversation[1].Content, "$skill-name") || !strings.Contains(conversation[1].Content, "<name>review</name>") {
		t.Fatalf("expected skill prompt context in conversation, got %#v", conversation[1])
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
	cfg := testConfig(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	engine := New(cfg, st, tools.NewRegistry(t.TempDir()), nil, t.TempDir())
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
	meta := tools.MetaWithStoredResult(map[string]string{
		"tool":         "bash",
		"tool_call_id": "call_1",
	}, domain.PartKindToolOutput, domain.ToolKindBash, tools.StoredResultStatusOK, tools.BashStoredResult{
		Command:   "pwd",
		Workdir:   ".",
		TimeoutMS: 1000,
		ExitCode:  0,
		Output:    "/typed/output",
	})
	if _, err := st.AddPart(context.Background(), toolMsg.ID, domain.PartKindToolOutput, "/stale/body", tools.JSONMeta(meta)); err != nil {
		t.Fatal(err)
	}

	conversation, err := engine.buildConversation(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(conversation) < 3 {
		t.Fatalf("expected assistant tool call and tool output, got %#v", conversation)
	}
	if len(conversation[len(conversation)-2].ToolCalls) != 1 || conversation[len(conversation)-2].ToolCalls[0].ID != "call_1" {
		t.Fatalf("expected structured assistant tool call, got %#v", conversation[len(conversation)-2])
	}
	if conversation[len(conversation)-1].Role != domain.MessageRoleTool || conversation[len(conversation)-1].ToolCallID != "call_1" || conversation[len(conversation)-1].Content != "/typed/output" {
		t.Fatalf("expected structured tool message, got %#v", conversation[len(conversation)-1])
	}
}

func TestStringifyPartsFormatsStoredTaskAndPlanUpdates(t *testing.T) {
	taskMeta := tools.MetaWithStoredResult(map[string]string{
		"status": "pending",
	}, domain.PartKindTaskUpdate, domain.ToolKindTask, tools.StoredResultStatusOK, tools.TaskStoredResult{
		Body:   "write docs",
		Status: domain.TaskStatusPending,
	})
	planMeta := tools.MetaWithStoredResult(nil, domain.PartKindPlanUpdate, domain.ToolKindUpdatePlan, tools.StoredResultStatusOK, tools.UpdatePlanStoredResult{
		Explanation: "updated plan",
		Steps: []tools.PlanStoredStep{
			{Step: "inspect repo", Status: "completed"},
			{Step: "wire persistence", Status: "in_progress"},
		},
	})

	got := stringifyParts([]domain.Part{
		{Kind: domain.PartKindTaskUpdate, Body: "stale task body", MetaJSON: tools.JSONMeta(taskMeta)},
		{Kind: domain.PartKindPlanUpdate, Body: "stale plan body", MetaJSON: tools.JSONMeta(planMeta)},
	})
	if !strings.Contains(got, "Task update:\nwrite docs") {
		t.Fatalf("expected task update from stored result, got %q", got)
	}
	if !strings.Contains(got, "Plan update:\nupdated plan\n[completed] inspect repo\n[in_progress] wire persistence") {
		t.Fatalf("expected plan update from stored result, got %q", got)
	}
}

func TestBuildConversationIncludesImageAndTextAttachments(t *testing.T) {
	cfg := testConfig(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	engine := New(cfg, st, tools.NewRegistry(t.TempDir()), nil, t.TempDir())
	session, err := st.CreateSession(context.Background(), "test", "openai", "gpt-5.4", nil)
	if err != nil {
		t.Fatal(err)
	}

	msg, err := st.AddMessage(context.Background(), session.ID, domain.MessageRoleUser, "inspect these")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddPart(context.Background(), msg.ID, domain.PartKindText, "inspect these", ""); err != nil {
		t.Fatal(err)
	}
	imageDraft, err := engine.files.ImportClipboardImage([]byte("\x89PNG\r\n\x1a\nfake"))
	if err != nil {
		t.Fatal(err)
	}
	imageMeta, err := engine.files.AdoptDraft(imageDraft, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	imageRaw, err := attachment.EncodeMeta(imageMeta)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddPart(context.Background(), msg.ID, domain.PartKindAttachment, imageMeta.Name, imageRaw); err != nil {
		t.Fatal(err)
	}
	textPath := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(textPath, []byte("remember this"), 0o644); err != nil {
		t.Fatal(err)
	}
	textDraft, err := engine.files.ImportFile(textPath)
	if err != nil {
		t.Fatal(err)
	}
	textMeta, err := engine.files.AdoptDraft(textDraft, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	textRaw, err := attachment.EncodeMeta(textMeta)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddPart(context.Background(), msg.ID, domain.PartKindAttachment, textMeta.Name, textRaw); err != nil {
		t.Fatal(err)
	}

	conversation, err := engine.buildConversation(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(conversation) < 2 {
		t.Fatalf("expected user message, got %#v", conversation)
	}
	userMsg := conversation[len(conversation)-1]
	if got := len(userMsg.ContentParts); got != 3 {
		t.Fatalf("expected text + image + attached text content parts, got %#v", userMsg.ContentParts)
	}
	if userMsg.ContentParts[1].Type != "image_url" {
		t.Fatalf("expected image attachment content part, got %#v", userMsg.ContentParts)
	}
	if userMsg.ContentParts[2].Type != "text" || !strings.Contains(userMsg.ContentParts[2].Text, "remember this") {
		t.Fatalf("expected attached text file content, got %#v", userMsg.ContentParts[2])
	}
}

func TestPreviewNextRequestIncludesUnsentDraftMessage(t *testing.T) {
	cfg := testConfig(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	workdir := t.TempDir()
	engine := New(cfg, st, tools.NewRegistry(workdir), nil, workdir)
	session, err := st.CreateSession(context.Background(), "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := st.AddMessage(context.Background(), session.ID, domain.MessageRoleUser, "saved prompt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddPart(context.Background(), msg.ID, domain.PartKindText, "saved prompt", ""); err != nil {
		t.Fatal(err)
	}

	req, err := engine.PreviewNextRequest(context.Background(), session, "unsent draft", nil, nil, "Permission mode changed")
	if err != nil {
		t.Fatal(err)
	}
	if req.Model != "test-model" {
		t.Fatalf("expected model in preview request, got %#v", req)
	}
	if len(req.Messages) < 4 {
		t.Fatalf("expected system, note, saved prompt, and unsent draft, got %#v", req.Messages)
	}
	last := req.Messages[len(req.Messages)-1]
	if last.Role != domain.MessageRoleUser || last.Content != "unsent draft" {
		t.Fatalf("expected unsent draft as final user message, got %#v", last)
	}
	if req.Messages[len(req.Messages)-2].Content != "saved prompt" {
		t.Fatalf("expected stored conversation before draft, got %#v", req.Messages)
	}
	if !strings.Contains(req.Messages[len(req.Messages)-3].Content, "Permission mode changed") {
		t.Fatalf("expected transient note before draft, got %#v", req.Messages)
	}
}

func TestRunPromptWithUnsupportedPDFAttachmentFailsBeforeProviderCall(t *testing.T) {
	cfg := testConfig(t)
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

	engine := New(cfg, st, tools.NewRegistry(t.TempDir()), nil, t.TempDir())
	session, err := st.CreateSession(context.Background(), "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}

	pdfPath := filepath.Join(t.TempDir(), "doc.pdf")
	if err := os.WriteFile(pdfPath, []byte("%PDF-1.7\nfake"), 0o644); err != nil {
		t.Fatal(err)
	}
	draft, err := engine.files.ImportFile(pdfPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.RunPromptWithAttachments(context.Background(), session, "summarize", []attachment.Draft{draft}, ""); err == nil {
		t.Fatal("expected unsupported pdf attachment to be rejected")
	}
}

func TestPreviewNextRequestIncludesStructuredFileReference(t *testing.T) {
	cfg := testConfig(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	workdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workdir, "README.md"), []byte("hello refs"), 0o644); err != nil {
		t.Fatal(err)
	}
	engine := New(cfg, st, tools.NewRegistry(workdir), nil, workdir)
	session, err := st.CreateSession(context.Background(), "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}

	prompt := "check @README.md"
	refs := []reference.Draft{{
		Kind:    reference.KindFile,
		Path:    "README.md",
		Display: "@README.md",
		Start:   len("check "),
		End:     len(prompt),
	}}
	req, err := engine.PreviewNextRequest(context.Background(), session, prompt, nil, refs, "")
	if err != nil {
		t.Fatal(err)
	}
	userMsg := req.Messages[len(req.Messages)-1]
	if len(userMsg.ContentParts) != 2 {
		t.Fatalf("expected prompt text plus resolved reference, got %#v", userMsg.ContentParts)
	}
	if userMsg.ContentParts[0].Text != "check " {
		t.Fatalf("unexpected leading text part: %#v", userMsg.ContentParts)
	}
	if !strings.Contains(userMsg.ContentParts[1].Text, "Referenced file README.md") || !strings.Contains(userMsg.ContentParts[1].Text, "hello refs") {
		t.Fatalf("expected resolved file reference content, got %#v", userMsg.ContentParts[1])
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

	cfg := testConfig(t)
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
	engine := New(cfg, st, tools.NewRegistry(workdir), nil, workdir)
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

func TestPermissionProfileChangeReevaluatesPendingApproval(t *testing.T) {
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
		switch len(requests) {
		case 1:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"tool_calls":[{"id":"call_1","type":"function","function":{"name":"bash","arguments":"{\"command\":\"printf hello\"}"}}]}}],"usage":{"total_tokens":1}}`))
		case 2:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"done"}}],"usage":{"total_tokens":1}}`))
		default:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ignored title"}}],"usage":{"total_tokens":1}}`))
		}
	}))
	defer server.Close()

	cfg := testConfig(t)
	cfg.Providers = map[string]config.Provider{
		"test": {
			BaseURL: server.URL + "/v1",
			Timeout: time.Second,
		},
	}
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "test-model"
	cfg.Permissions.Profile = permission.ProfileAsk

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	workdir := t.TempDir()
	engine := New(cfg, st, tools.NewRegistry(workdir), nil, workdir)
	session, err := st.CreateSession(context.Background(), "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSessionPermissionProfile(context.Background(), session.ID, permission.ProfileAsk); err != nil {
		t.Fatal(err)
	}

	events, err := engine.RunPrompt(context.Background(), session, "say hello")
	if err != nil {
		t.Fatal(err)
	}
	var approvalID int64
	for evt := range events {
		if evt.Kind == domain.EventKindApprovalAsk {
			approvalID, err = parseApprovalID(evt.Meta["approval_id"])
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if approvalID == 0 {
		t.Fatal("expected approval request")
	}

	reeval, err := engine.SetPermissionProfileAndReevaluateApproval(context.Background(), session.ID, approvalID, permission.ProfileFullAccess)
	if err != nil {
		t.Fatal(err)
	}
	var sawProfileChange bool
	var sawToolResult bool
	var sawFinalAnswer bool
	for evt := range reeval {
		if evt.Kind == domain.EventKindStatus && evt.Meta["permission_profile"] == permission.ProfileFullAccess {
			sawProfileChange = true
		}
		if evt.Kind == domain.EventKindToolResult && strings.Contains(evt.Text, "hello") {
			sawToolResult = true
		}
		if evt.Kind == domain.EventKindMessageDelta && evt.Text == "done" {
			sawFinalAnswer = true
		}
	}
	if !sawProfileChange {
		t.Fatal("expected permission profile status event")
	}
	if !sawToolResult {
		t.Fatal("expected tool result after re-evaluation")
	}
	if !sawFinalAnswer {
		t.Fatal("expected final assistant answer after re-evaluation")
	}

	updated, err := st.GetSession(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.PermissionProfile != permission.ProfileFullAccess {
		t.Fatalf("expected permission profile %q, got %q", permission.ProfileFullAccess, updated.PermissionProfile)
	}

	approval, err := st.GetApproval(context.Background(), approvalID)
	if err != nil {
		t.Fatal(err)
	}
	if approval.Status != domain.ApprovalStatusApproved {
		t.Fatalf("expected approval to be approved, got %s", approval.Status)
	}
	if len(requests) < 2 {
		t.Fatalf("expected approval re-evaluation to continue the model, got %d requests", len(requests))
	}
}

func TestRunPromptExecutesMultipleToolCallsInParallel(t *testing.T) {
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
		switch len(requests) {
		case 1:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"tool_calls":[{"id":"call_slow","type":"function","function":{"name":"bash","arguments":"{\"command\":\"sleep 0.2; printf slow\"}"}},{"id":"call_fast","type":"function","function":{"name":"bash","arguments":"{\"command\":\"printf fast\"}"}}]}}],"usage":{"total_tokens":1}}`))
		case 2:
			if !strings.Contains(string(body), `"tool_call_id":"call_slow"`) || !strings.Contains(string(body), `"tool_call_id":"call_fast"`) {
				t.Fatalf("expected second request to include both tool outputs, got %s", string(body))
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"done"}}],"usage":{"total_tokens":1}}`))
		default:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ignored title"}}],"usage":{"total_tokens":1}}`))
		}
	}))
	defer server.Close()

	cfg := testConfig(t)
	cfg.Providers = map[string]config.Provider{
		"test": {
			BaseURL: server.URL + "/v1",
			Timeout: time.Second,
		},
	}
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "test-model"
	cfg.Permissions.Profile = permission.ProfileFullAccess

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	workdir := t.TempDir()
	engine := New(cfg, st, tools.NewRegistry(workdir), nil, workdir)
	session, err := st.CreateSession(context.Background(), "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSessionPermissionProfile(context.Background(), session.ID, permission.ProfileFullAccess); err != nil {
		t.Fatal(err)
	}

	events, err := engine.RunPrompt(context.Background(), session, "say hello")
	if err != nil {
		t.Fatal(err)
	}
	var toolResults []string
	var sawFinalAnswer bool
	for evt := range events {
		if evt.Kind == domain.EventKindToolResult {
			toolResults = append(toolResults, evt.Text)
		}
		if evt.Kind == domain.EventKindMessageDelta && evt.Text == "done" {
			sawFinalAnswer = true
		}
	}
	if len(toolResults) != 2 {
		t.Fatalf("expected two tool results, got %#v", toolResults)
	}
	if !strings.Contains(toolResults[0], "fast") {
		t.Fatalf("expected faster tool result first, got %#v", toolResults)
	}
	if !strings.Contains(toolResults[1], "slow") {
		t.Fatalf("expected slower tool result second, got %#v", toolResults)
	}
	if !sawFinalAnswer {
		t.Fatal("expected final assistant answer after tool batch")
	}
	if len(requests) < 2 {
		t.Fatalf("expected at least two provider requests, got %d", len(requests))
	}
}

func TestRunPromptPersistsAssistantErrorOnBackendFailure(t *testing.T) {
	cfg := testConfig(t)
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

	engine := New(cfg, st, tools.NewRegistry(t.TempDir()), nil, t.TempDir())
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
	if len(errorParts) != 1 || errorParts[0].Kind != domain.PartKindEventNotice {
		t.Fatalf("expected one assistant event notice part, got %#v", errorParts)
	}
	if !strings.Contains(errorParts[0].Body, "Error:") {
		t.Fatalf("expected stored error prefix, got %q", errorParts[0].Body)
	}
}

func TestHandleModelToolCallAsksForOutsideProjectRead(t *testing.T) {
	cfg := testConfig(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	workdir := t.TempDir()
	engine := New(cfg, st, tools.NewRegistry(workdir), nil, workdir)
	session, err := st.CreateSession(context.Background(), "test", cfg.DefaultProvider, "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	session.PermissionProfile = permission.ProfileReadAsk
	session.ProjectRoot = workdir

	outsideDir := t.TempDir()
	outsidePath := filepath.Join(outsideDir, "outside.txt")
	if err := os.WriteFile(outsidePath, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	evt, err := engine.handleModelToolCall(context.Background(), session, tools.Request{
		Tool: domain.ToolKindRead,
		Args: map[string]string{"path": outsidePath},
	})
	if err != nil {
		t.Fatal(err)
	}
	if evt.Kind != domain.EventKindApprovalAsk {
		t.Fatalf("expected approval ask, got %#v", evt)
	}
	if !strings.Contains(evt.Text, "outside the current project folder") {
		t.Fatalf("expected outside-project reason, got %q", evt.Text)
	}
}

func TestHandleModelToolCallAllowsProjectReadInReadAskMode(t *testing.T) {
	cfg := testConfig(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	workdir := t.TempDir()
	engine := New(cfg, st, tools.NewRegistry(workdir), nil, workdir)
	session, err := st.CreateSession(context.Background(), "test", cfg.DefaultProvider, "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	session.PermissionProfile = permission.ProfileReadAsk
	session.ProjectRoot = workdir

	targetPath := filepath.Join(workdir, "inside.txt")
	if err := os.WriteFile(targetPath, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	evt, err := engine.handleModelToolCall(context.Background(), session, tools.Request{
		Tool: domain.ToolKindRead,
		Args: map[string]string{"path": "inside.txt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if evt.Kind != domain.EventKindToolResult {
		t.Fatalf("expected tool result, got %#v", evt)
	}
	if strings.Contains(strings.ToLower(evt.Text), "requires approval") {
		t.Fatalf("expected read to avoid approval in read-ask mode, got %q", evt.Text)
	}
}

func TestApproveContinuesAfterOutsideWorkspaceRead(t *testing.T) {
	t.Parallel()

	outsideDir := t.TempDir()
	outsidePath := filepath.Join(outsideDir, "rules.md")
	if err := os.WriteFile(outsidePath, []byte("# Rules\nhello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

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
		args, err := json.Marshal(map[string]string{"path": outsidePath})
		if err != nil {
			t.Fatalf("marshal args: %v", err)
		}
		switch len(requests) {
		case 1:
			_, _ = w.Write([]byte(fmt.Sprintf(`{"choices":[{"message":{"tool_calls":[{"id":"call_1","type":"function","function":{"name":"read","arguments":%q}}]}}],"usage":{"total_tokens":1}}`, string(args))))
		case 2:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"done"}}],"usage":{"total_tokens":1}}`))
		default:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ignored"}}],"usage":{"total_tokens":1}}`))
		}
	}))
	defer server.Close()

	cfg := testConfig(t)
	cfg.Providers = map[string]config.Provider{
		"test": {
			BaseURL: server.URL + "/v1",
			Timeout: time.Second,
		},
	}
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "test-model"

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	workdir := t.TempDir()
	engine := New(cfg, st, tools.NewRegistry(workdir), nil, workdir)
	session, err := st.CreateSession(context.Background(), "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	session.PermissionProfile = permission.ProfileReadAsk
	session.ProjectRoot = workdir
	if err := st.SetSessionPermissionProfile(context.Background(), session.ID, permission.ProfileReadAsk); err != nil {
		t.Fatal(err)
	}

	events, err := engine.RunPrompt(context.Background(), session, "continue")
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
		t.Fatal("expected approved outside-workspace read to emit tool result")
	}
	if !sawFinalAnswer {
		t.Fatal("expected final assistant answer after approved outside-workspace read")
	}
}

func TestApproveContinuesAfterApprovedToolFailure(t *testing.T) {
	t.Parallel()

	missingPath := filepath.Join(t.TempDir(), "missing.md")

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
		args, err := json.Marshal(map[string]string{"path": missingPath})
		if err != nil {
			t.Fatalf("marshal args: %v", err)
		}
		switch len(requests) {
		case 1:
			_, _ = w.Write([]byte(fmt.Sprintf(`{"choices":[{"message":{"tool_calls":[{"id":"call_1","type":"function","function":{"name":"read","arguments":%q}}]}}],"usage":{"total_tokens":1}}`, string(args))))
		case 2:
			if !strings.Contains(requests[1], "read failed:") {
				t.Fatalf("expected second request to include tool failure, got %s", requests[1])
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"handled failure"}}],"usage":{"total_tokens":1}}`))
		default:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ignored"}}],"usage":{"total_tokens":1}}`))
		}
	}))
	defer server.Close()

	cfg := testConfig(t)
	cfg.Providers = map[string]config.Provider{
		"test": {
			BaseURL: server.URL + "/v1",
			Timeout: time.Second,
		},
	}
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "test-model"

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	workdir := t.TempDir()
	engine := New(cfg, st, tools.NewRegistry(workdir), nil, workdir)
	session, err := st.CreateSession(context.Background(), "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	session.PermissionProfile = permission.ProfileReadAsk
	session.ProjectRoot = workdir
	if err := st.SetSessionPermissionProfile(context.Background(), session.ID, permission.ProfileReadAsk); err != nil {
		t.Fatal(err)
	}

	events, err := engine.RunPrompt(context.Background(), session, "continue")
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
	var sawToolFailure bool
	var sawFinalAnswer bool
	for evt := range approvedEvents {
		if evt.Kind == domain.EventKindToolResult && strings.Contains(evt.Text, "read failed:") {
			sawToolFailure = true
		}
		if evt.Kind == domain.EventKindMessageDelta && evt.Text == "handled failure" {
			sawFinalAnswer = true
		}
		if evt.Kind == domain.EventKindError {
			t.Fatalf("expected failure to be shipped as tool result, got %#v", evt)
		}
	}
	if !sawToolFailure {
		t.Fatal("expected approved tool failure to emit tool result")
	}
	if !sawFinalAnswer {
		t.Fatal("expected final assistant answer after approved tool failure")
	}
}

func TestHandleModelToolCallBypassesApprovalForSkill(t *testing.T) {
	cfg := testConfig(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	home := t.TempDir()
	t.Setenv("HOME", home)

	workdir := t.TempDir()
	skillPath := filepath.Join(home, ".agents", "skills", "review", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skillPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillPath, []byte("---\nname: review\ndescription: Review code carefully\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	engine := New(cfg, st, tools.NewRegistry(workdir), nil, workdir)
	session, err := st.CreateSession(context.Background(), "test", cfg.DefaultProvider, "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	session.PermissionProfile = permission.ProfileAsk
	session.ProjectRoot = workdir

	evt, err := engine.handleModelToolCall(context.Background(), session, tools.Request{
		Tool: domain.ToolKindSkill,
		Args: map[string]string{"name": "review"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if evt.Kind != domain.EventKindToolResult {
		t.Fatalf("expected tool result, got %#v", evt)
	}
	if strings.Contains(strings.ToLower(evt.Text), "requires approval") {
		t.Fatalf("expected skill load to bypass approval, got %q", evt.Text)
	}
}

func TestHandleModelToolCallAllowsProjectWriteInWriteAskMode(t *testing.T) {
	cfg := testConfig(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	workdir := t.TempDir()
	engine := New(cfg, st, tools.NewRegistry(workdir), nil, workdir)
	session, err := st.CreateSession(context.Background(), "test", cfg.DefaultProvider, "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	session.PermissionProfile = permission.ProfileWriteAsk
	session.ProjectRoot = workdir

	evt, err := engine.handleModelToolCall(context.Background(), session, tools.Request{
		Tool: domain.ToolKindWrite,
		Args: map[string]string{"path": "note.txt", "content": "hello"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if evt.Kind != domain.EventKindToolResult {
		t.Fatalf("expected tool result, got %#v", evt)
	}
	if _, err := os.Stat(filepath.Join(workdir, "note.txt")); err != nil {
		t.Fatalf("expected write to succeed: %v", err)
	}
}

func TestHandleModelToolCallAsksForBashInWriteAskMode(t *testing.T) {
	cfg := testConfig(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	workdir := t.TempDir()
	engine := New(cfg, st, tools.NewRegistry(workdir), nil, workdir)
	session, err := st.CreateSession(context.Background(), "test", cfg.DefaultProvider, "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	session.PermissionProfile = permission.ProfileWriteAsk
	session.ProjectRoot = workdir

	evt, err := engine.handleModelToolCall(context.Background(), session, tools.Request{
		Tool: domain.ToolKindBash,
		Args: map[string]string{"command": "pwd"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if evt.Kind != domain.EventKindApprovalAsk {
		t.Fatalf("expected approval ask, got %#v", evt)
	}
	if !strings.Contains(evt.Text, "shell commands require approval in this mode") {
		t.Fatalf("unexpected approval text: %q", evt.Text)
	}
}

func TestRunPromptIncludesTransientSessionNote(t *testing.T) {
	t.Parallel()

	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		requests = append(requests, string(body))
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"done"}}],"usage":{"total_tokens":1}}`))
	}))
	defer server.Close()

	cfg := testConfig(t)
	cfg.Providers = map[string]config.Provider{
		"test": {BaseURL: server.URL + "/v1", Timeout: time.Second},
	}
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "test-model"
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	engine := New(cfg, st, tools.NewRegistry(t.TempDir()), nil, t.TempDir())
	session, err := st.CreateSession(context.Background(), "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	events, err := engine.RunPromptWithAttachments(context.Background(), session, "hello", nil, "Permission mode changed to ask.")
	if err != nil {
		t.Fatal(err)
	}
	for range events {
	}
	if len(requests) == 0 || !strings.Contains(requests[0], `Session update:\nPermission mode changed to ask.`) {
		t.Fatalf("expected transient session note in request, got %v", requests)
	}
	messages, _, err := st.PartsForSession(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) == 0 || messages[0].Summary != "hello" {
		t.Fatalf("expected persisted user prompt only, got %#v", messages)
	}
}

func TestRunContinueSendsContinueInstruction(t *testing.T) {
	t.Parallel()

	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		requests = append(requests, string(body))
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"continued"}}],"usage":{"total_tokens":1}}`))
	}))
	defer server.Close()

	cfg := testConfig(t)
	cfg.Providers = map[string]config.Provider{
		"test": {BaseURL: server.URL + "/v1", Timeout: time.Second},
	}
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "test-model"
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	engine := New(cfg, st, tools.NewRegistry(t.TempDir()), nil, t.TempDir())
	session, err := st.CreateSession(context.Background(), "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	events, err := engine.RunContinue(context.Background(), session, "Permission mode changed to write / ask.")
	if err != nil {
		t.Fatal(err)
	}
	for range events {
	}
	if len(requests) == 0 || !strings.Contains(requests[0], "Continue from where you left off.") {
		t.Fatalf("expected continue instruction in request, got %v", requests)
	}
	if !strings.Contains(requests[0], "Permission mode changed to write / ask.") {
		t.Fatalf("expected transient note in continue request, got %v", requests)
	}
}

func TestRunPromptCancellationDoesNotPersistAssistantError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()

	cfg := testConfig(t)
	cfg.Providers = map[string]config.Provider{
		"test": {
			BaseURL: server.URL + "/v1",
			Timeout: time.Second,
		},
	}
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "test-model"

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	engine := New(cfg, st, tools.NewRegistry(t.TempDir()), nil, t.TempDir())
	session, err := st.CreateSession(context.Background(), "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	events, err := engine.RunPrompt(ctx, session, "hello")
	if err != nil {
		t.Fatal(err)
	}
	cancel()

	var sawInterrupted bool
	for evt := range events {
		if evt.Kind == domain.EventKindStatus && evt.Text == "Interrupted" {
			sawInterrupted = true
		}
		if evt.Kind == domain.EventKindError {
			t.Fatalf("expected interruption status instead of error, got %#v", evt)
		}
	}
	if !sawInterrupted {
		t.Fatal("expected interrupted status event")
	}

	messages, parts, err := st.PartsForSession(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) > 0 {
		last := messages[len(messages)-1]
		if last.Role == domain.MessageRoleAssistant {
			t.Fatalf("did not expect assistant error message after cancellation, got %#v", last)
		}
	}
	for _, byMessage := range parts {
		for _, part := range byMessage {
			if strings.Contains(part.Body, "context canceled") {
				t.Fatalf("unexpected persisted cancellation error: %#v", part)
			}
		}
	}
}

func TestModelTaskPersistsTranscriptUpdate(t *testing.T) {
	t.Parallel()

	cfg := testConfig(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	engine := New(cfg, st, tools.NewRegistry(t.TempDir()), nil, t.TempDir())
	session, err := st.CreateSession(context.Background(), "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}

	evt, err := engine.handleModelToolCall(context.Background(), session, tools.Request{
		Tool: domain.ToolKindTask,
		Args: map[string]string{"body": "write docs"},
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

func TestRunPromptRetriesRateLimitAndCompletes(t *testing.T) {
	t.Parallel()

	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		switch requests {
		case 1:
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"rate limit"}}`))
		case 2:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"done"}}],"usage":{"total_tokens":1}}`))
		default:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ignored title"}}],"usage":{"total_tokens":1}}`))
		}
	}))
	defer server.Close()

	cfg := testConfig(t)
	cfg.Providers = map[string]config.Provider{
		"test": {
			BaseURL: server.URL + "/v1",
			Timeout: time.Second,
		},
	}
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "test-model"

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	engine := New(cfg, st, tools.NewRegistry(t.TempDir()), nil, t.TempDir())
	var waited []time.Duration
	engine.retryPause = func(_ context.Context, delay time.Duration) error {
		waited = append(waited, delay)
		return nil
	}
	session, err := st.CreateSession(context.Background(), "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}

	events, err := engine.RunPrompt(context.Background(), session, "hello")
	if err != nil {
		t.Fatal(err)
	}

	var sawRetryStatus bool
	for evt := range events {
		if evt.Kind == domain.EventKindStatus && strings.Contains(evt.Text, "rate limit hit") {
			sawRetryStatus = true
		}
		if evt.Kind == domain.EventKindError {
			t.Fatalf("expected retry to succeed, got %#v", evt)
		}
	}
	if !sawRetryStatus {
		t.Fatal("expected retry status event")
	}
	if len(waited) != 1 || waited[0] != 2*time.Second {
		t.Fatalf("expected single retry wait of 2s, got %#v", waited)
	}

	messages, parts, err := st.PartsForSession(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	last := messages[len(messages)-1]
	if last.Role != domain.MessageRoleAssistant {
		t.Fatalf("expected assistant message, got %#v", last)
	}
	got := parts[last.ID]
	if len(got) == 0 || got[0].Kind != domain.PartKindText || !strings.Contains(got[0].Body, "done") {
		t.Fatalf("expected final assistant text after retry, got %#v", got)
	}
}

func TestRunPromptPersistsEventNoticeWhenRetriesExhausted(t *testing.T) {
	t.Parallel()

	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limit"}}`))
	}))
	defer server.Close()

	cfg := testConfig(t)
	cfg.Providers = map[string]config.Provider{
		"test": {
			BaseURL: server.URL + "/v1",
			Timeout: time.Second,
		},
	}
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "test-model"

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	engine := New(cfg, st, tools.NewRegistry(t.TempDir()), nil, t.TempDir())
	engine.retryPause = func(_ context.Context, _ time.Duration) error { return nil }
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
		t.Fatal("expected terminal error event")
	}
	if requests != maxRateLimitRetries+1 {
		t.Fatalf("expected %d attempts, got %d", maxRateLimitRetries+1, requests)
	}

	messages, parts, err := st.PartsForSession(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	last := messages[len(messages)-1]
	got := parts[last.ID]
	if len(got) != 1 || got[0].Kind != domain.PartKindEventNotice {
		t.Fatalf("expected persisted event notice, got %#v", got)
	}
	if !strings.Contains(got[0].Body, "Error: chat status 429") {
		t.Fatalf("expected persisted provider failure, got %#v", got[0])
	}
}

func TestRunPromptPersistsInterruptedEventNoticeDuringRetryWait(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "9")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limit"}}`))
	}))
	defer server.Close()

	cfg := testConfig(t)
	cfg.Providers = map[string]config.Provider{
		"test": {
			BaseURL: server.URL + "/v1",
			Timeout: time.Second,
		},
	}
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "test-model"

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	engine := New(cfg, st, tools.NewRegistry(t.TempDir()), nil, t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	engine.retryPause = func(ctx context.Context, _ time.Duration) error {
		cancel()
		<-ctx.Done()
		return ctx.Err()
	}
	session, err := st.CreateSession(context.Background(), "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}

	events, err := engine.RunPrompt(ctx, session, "hello")
	if err != nil {
		t.Fatal(err)
	}

	var sawInterrupted bool
	for evt := range events {
		if evt.Kind == domain.EventKindStatus && evt.Text == "Interrupted" {
			sawInterrupted = true
		}
		if evt.Kind == domain.EventKindError {
			t.Fatalf("expected interruption status instead of error, got %#v", evt)
		}
	}
	if !sawInterrupted {
		t.Fatal("expected interrupted status event")
	}

	messages, parts, err := st.PartsForSession(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	last := messages[len(messages)-1]
	got := parts[last.ID]
	if len(got) != 1 || got[0].Kind != domain.PartKindEventNotice || got[0].Body != "Interrupted" {
		t.Fatalf("expected persisted interruption notice, got %#v", got)
	}
}

func TestPersistToolResultSynthesizesVisibleOutputWhenToolReturnsNothing(t *testing.T) {
	t.Parallel()

	cfg := testConfig(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	engine := New(cfg, st, tools.NewRegistry(t.TempDir()), nil, t.TempDir())
	session, err := st.CreateSession(context.Background(), "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}

	events, err := engine.persistToolResult(context.Background(), session.ID, tools.Request{Tool: domain.ToolKindBash}, tools.Result{})
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
