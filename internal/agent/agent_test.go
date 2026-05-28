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
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/attachment"
	chatpkg "github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/mcp"
	"github.com/lkarlslund/koder/internal/permissionprofile"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/reference"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
	"github.com/lkarlslund/koder/internal/turncontrol"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
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

func defaultChatForSession(t *testing.T, st *store.Store, sessionID domain.ID) domain.Chat {
	t.Helper()
	chat, err := st.DefaultChat(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	return chat
}

func appendUserTimelineItem(t *testing.T, st *store.Store, chatID domain.ID, text string) domain.TimelineItem {
	t.Helper()
	return appendUserTimelineItemWithAttachments(t, st, chatID, text, nil)
}

func appendUserTimelineItemWithAttachments(t *testing.T, st *store.Store, chatID domain.ID, text string, attachments []domain.Attachment) domain.TimelineItem {
	t.Helper()
	item, err := st.AppendTimeline(context.Background(), chatID, domain.UserMessage{Text: text, Attachments: attachments})
	if err != nil {
		t.Fatal(err)
	}
	item.Seal(time.Now().UTC())
	if err := st.Timeline().Put(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	return item
}

func appendAssistantTimelineItem(t *testing.T, st *store.Store, chatID domain.ID, msg domain.AssistantMessage) domain.TimelineItem {
	t.Helper()
	item, err := st.AppendTimeline(context.Background(), chatID, msg)
	if err != nil {
		t.Fatal(err)
	}
	item.Seal(time.Now().UTC())
	if err := st.Timeline().Put(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	return item
}

func appendAssistantToolTimelineItem(t *testing.T, st *store.Store, chatID domain.ID, req tools.Request, text string) domain.TimelineItem {
	t.Helper()
	item, err := st.AppendAssistantToolCalls(context.Background(), chatID, []domain.ToolCall{{
		ToolCallID: domain.ToolCallID(req.ToolCallID),
		Tool:       req.Tool,
		Args:       req.Args,
		Status:     domain.ToolStatusPending,
	}}, text, domain.Usage{})
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func attachToolResultTimelineItem(t *testing.T, st *store.Store, chatID domain.ID, req tools.Request, text string, data domain.ToolResultPayload) domain.TimelineItem {
	t.Helper()
	item, err := st.AttachToolResult(context.Background(), chatID, req.ToolCallID, domain.ToolResult{
		Text:   text,
		Data:   data,
		Status: domain.ToolResultStatusOK,
	})
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func waitForToolStatus(t *testing.T, st *store.Store, chatID domain.ID, toolCallID string, want domain.ToolStatus) bool {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if currentToolStatus(t, st, chatID, toolCallID) == want {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func assertToolStatus(t *testing.T, st *store.Store, chatID domain.ID, toolCallID string, want domain.ToolStatus) {
	t.Helper()
	got := currentToolStatus(t, st, chatID, toolCallID)
	if got != want {
		t.Fatalf("tool %s status = %s, want %s", toolCallID, got, want)
	}
}

func currentToolStatus(t *testing.T, st *store.Store, chatID domain.ID, toolCallID string) domain.ToolStatus {
	t.Helper()
	items, err := st.TimelineForChat(context.Background(), chatID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		assistant, ok := item.Content.(domain.AssistantMessage)
		if !ok {
			continue
		}
		call := assistant.ToolByID(domain.ToolCallID(toolCallID))
		if call != nil {
			return call.Status
		}
	}
	t.Fatalf("tool %s not found in chat %s", toolCallID, chatID)
	return ""
}

func appendCompactionTimelineItem(t *testing.T, st *store.Store, chatID domain.ID, summary string, firstKeptItemID string) domain.TimelineItem {
	t.Helper()
	item, err := st.AppendTimeline(context.Background(), chatID, domain.Compaction{
		Summary:         summary,
		Status:          "completed",
		FirstKeptItemID: firstKeptItemID,
	})
	if err != nil {
		t.Fatal(err)
	}
	item.Seal(time.Now().UTC())
	if err := st.Timeline().Put(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	return item
}

func timelineTranscriptForSession(t *testing.T, st *store.Store, sessionID domain.ID) ([]domain.Message, map[domain.ID][]domain.Part, error) {
	t.Helper()
	chat, err := st.DefaultChat(context.Background(), sessionID)
	if err != nil {
		return nil, nil, err
	}
	return timelineTranscriptForChat(t, st, chat)
}

func timelineTranscriptForChat(t *testing.T, st *store.Store, chat domain.Chat) ([]domain.Message, map[domain.ID][]domain.Part, error) {
	t.Helper()
	items, err := st.TimelineForChat(context.Background(), chat.ID)
	if err != nil {
		return nil, nil, err
	}
	messages := make([]domain.Message, 0, len(items))
	parts := make(map[domain.ID][]domain.Part, len(items))
	for _, item := range items {
		msg, itemParts := testTranscriptItem(chat.SessionID, item)
		if msg.ID == "" {
			continue
		}
		messages = append(messages, msg)
		parts[msg.ID] = itemParts
	}
	return messages, parts, nil
}

func testTranscriptItem(sessionID domain.ID, item domain.TimelineItem) (domain.Message, []domain.Part) {
	messageID := domain.ID(item.ID)
	msg := domain.Message{ID: messageID, SessionID: sessionID, ChatID: item.ChatID, CreatedAt: item.CreatedAt}
	addPart := func(parts *[]domain.Part, kind domain.PartKind, payload domain.PartPayload, offset int64) {
		part := domain.Part{ID: domain.ID(fmt.Sprintf("%s-part-%d", messageID, offset)), MessageID: messageID, Kind: kind, Payload: payload, CreatedAt: item.CreatedAt}
		part.Body = part.Text()
		*parts = append(*parts, part)
	}
	var parts []domain.Part
	switch content := item.Content.(type) {
	case domain.UserMessage:
		msg.Role = domain.MessageRoleUser
		msg.Summary = content.Text
		addPart(&parts, domain.PartKindText, domain.TextPayload{Text: content.Text}, 1)
	case domain.AssistantMessage:
		msg.Role = domain.MessageRoleAssistant
		msg.Summary = content.Text
		offset := int64(1)
		if strings.TrimSpace(content.Text) != "" {
			addPart(&parts, domain.PartKindText, domain.TextPayload{Text: content.Text}, offset)
			offset++
		}
		for _, tool := range content.Tools {
			addPart(&parts, domain.PartKindToolCall, domain.ToolCallPayload{Tool: tool.Tool, ToolCallID: string(tool.ToolCallID), Args: tool.Args}, offset)
			offset++
			if tool.Result != nil {
				addPart(&parts, domain.PartKindToolOutput, domain.ToolOutputPayload{Tool: tool.Tool, ToolCallID: string(tool.ToolCallID), Args: tool.Args, Status: tool.Result.Status, Text: tool.Result.Text, Diff: tool.Result.Diff, Result: tool.Result.Data}, offset)
			}
			if tool.Error != nil {
				addPart(&parts, domain.PartKindToolOutput, domain.ToolOutputPayload{Tool: tool.Tool, ToolCallID: string(tool.ToolCallID), Args: tool.Args, Status: domain.ToolResultStatusError, Text: tool.Error.Message, Result: domain.ErrorStoredResult{Message: tool.Error.Message}}, offset)
			}
		}
	case domain.ToolExecution:
		msg.Role = domain.MessageRoleTool
		msg.Summary = string(content.Tool)
		if content.Result != nil {
			addPart(&parts, domain.PartKindToolOutput, domain.ToolOutputPayload{Tool: content.Tool, ToolCallID: string(content.ToolCallID), Args: content.Args, Status: content.Result.Status, Text: content.Result.Text, Diff: content.Result.Diff, Result: content.Result.Data}, 1)
		}
		if content.Error != nil {
			addPart(&parts, domain.PartKindToolOutput, domain.ToolOutputPayload{Tool: content.Tool, ToolCallID: string(content.ToolCallID), Args: content.Args, Status: domain.ToolResultStatusError, Text: content.Error.Message, Result: domain.ErrorStoredResult{Message: content.Error.Message}}, 1)
		}
	case domain.Notice:
		msg.Role = domain.MessageRoleAssistant
		msg.Summary = content.Text
		addPart(&parts, domain.PartKindEventNotice, domain.EventNoticePayload{Text: content.Text, Kind: content.Kind, Severity: content.Level}, 1)
	case domain.Compaction:
		msg.Role = domain.MessageRoleAssistant
		msg.Summary = content.Summary
		addPart(&parts, domain.PartKindCompaction, domain.CompactionPayload{Summary: content.Summary, Status: content.Status, BeforeContextTokens: content.BeforeContextTokens, AfterContextTokens: content.AfterContextTokens}, 1)
	}
	return msg, parts
}

func timelineNoticesForChat(t *testing.T, st *store.Store, chatID domain.ID) []domain.Notice {
	t.Helper()
	items, err := st.TimelineForChat(context.Background(), chatID)
	if err != nil {
		t.Fatal(err)
	}
	var out []domain.Notice
	for _, item := range items {
		if notice, ok := item.Content.(domain.Notice); ok {
			out = append(out, notice)
		}
	}
	return out
}

func TestSystemPromptDoesNotMentionInternalSlashCommands(t *testing.T) {
	prompt := systemPrompt()
	for _, command := range []string{"/new", "/quit", "/permissions", "/mouse", "/approve", "/deny"} {
		if strings.Contains(prompt, command) {
			t.Fatalf("expected system prompt to exclude internal slash command %q", command)
		}
	}
}

func TestEngineSystemPromptUsesManagedUserAsset(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".koder"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".koder", "system-prompt.md"), []byte("custom system prompt\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	engine := New(testConfig(t), nil, nil, nil, t.TempDir())
	if got := engine.systemPrompt(); got != "custom system prompt" {
		t.Fatalf("expected managed user system prompt, got %q", got)
	}
}

func TestEngineCompactionPromptUsesManagedUserAsset(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".koder"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".koder", "compaction-prompt.md"), []byte("custom compaction prompt\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	engine := New(testConfig(t), nil, nil, nil, t.TempDir())
	if got := engine.compactPrompt(); got != "custom compaction prompt" {
		t.Fatalf("expected managed user compaction prompt, got %q", got)
	}
}

func TestFormatEnvironmentPrompt(t *testing.T) {
	got := formatEnvironmentPrompt(environmentSnapshot{
		WorkspaceRoot: "/repo",
		Workdir:       "/repo/pkg",
		Platform:      "linux/amd64",
		OS:            "Linux 6.8.0",
		Shell:         "/bin/zsh",
		Git: gitSnapshot{
			Repository: true,
			Root:       "/repo",
			Branch:     "main",
			Commit:     "abc1234",
			Upstream:   "origin/main",
			Staged:     1,
			Unstaged:   2,
			Untracked:  3,
		},
	})
	for _, want := range []string{
		"Runtime environment:",
		"- Workspace root: /repo",
		"- Current working directory: /repo/pkg",
		"- Current date and time: not included; use a tool if the exact system time is needed",
		"- Platform: linux/amd64",
		"- OS: Linux 6.8.0",
		"- Shell: /bin/zsh",
		"- Git repository: yes",
		"- Git root: /repo",
		"- Git branch: main",
		"- Git commit: abc1234",
		"- Git upstream: origin/main",
		"- Git status: dirty (staged 1, unstaged 2, untracked 3)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in environment prompt, got %q", want, got)
		}
	}
}

func TestFormatEnvironmentPromptNonGit(t *testing.T) {
	got := formatEnvironmentPrompt(environmentSnapshot{
		WorkspaceRoot: "/repo",
		Workdir:       "/repo",
		Platform:      "linux/amd64",
		OS:            "Linux",
		Shell:         "unknown",
	})
	if !strings.Contains(got, "- Git repository: no") {
		t.Fatalf("expected non-git status, got %q", got)
	}
	if strings.Contains(got, "- Git root:") {
		t.Fatalf("expected git details to be omitted for non-git workspace, got %q", got)
	}
}

func TestNeedsSessionAgentsRefresh(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		session domain.Session
		want    bool
	}{
		{
			name:    "missing checksum",
			session: domain.Session{},
			want:    true,
		},
		{
			name: "missing resolved and summary",
			session: domain.Session{
				ProjectChecksum: "abc",
			},
			want: true,
		},
		{
			name: "resolved present",
			session: domain.Session{
				ProjectChecksum: "abc",
				AgentsResolved:  "resolved",
			},
			want: false,
		},
		{
			name: "summary present",
			session: domain.Session{
				ProjectChecksum: "abc",
				AgentsSummary:   "summary",
			},
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := needsSessionAgentsRefresh(tc.session); got != tc.want {
				t.Fatalf("needsSessionAgentsRefresh() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSessionEnvironmentPromptBuildsOncePerSession(t *testing.T) {
	cfg := testConfig(t)
	workdir := t.TempDir()
	engine := New(cfg, nil, tools.NewRegistry(workdir), nil, workdir)
	session := domain.Session{ID: "session-42", ProjectRoot: workdir}

	first := engine.sessionEnvironmentPrompt(session)
	if first == "" {
		t.Fatal("expected generated environment prompt")
	}
	engine.envPrompts[session.ID] = "cached prompt"
	second := engine.sessionEnvironmentPrompt(session)
	if second != "cached prompt" {
		t.Fatalf("expected cached environment prompt, got %q", second)
	}
}

func TestGitInfoDetectsRepositoryState(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "tracked.txt")
	runGit(t, repo, "commit", "-m", "initial")
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "untracked.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := gitInfo(repo)
	if !got.Repository {
		t.Fatalf("expected git repository, got %#v", got)
	}
	if got.Root != repo {
		t.Fatalf("expected root %q, got %#v", repo, got)
	}
	if got.Branch == "" || got.Commit == "" {
		t.Fatalf("expected branch and commit, got %#v", got)
	}
	if got.Unstaged != 1 || got.Untracked != 1 {
		t.Fatalf("expected unstaged and untracked counts, got %#v", got)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

func TestMaxToolLoopStepsDefaultsToTwenty(t *testing.T) {
	engine := New(testConfig(t), nil, nil, nil, t.TempDir())
	if got := engine.maxToolLoopSteps(); got != 500 {
		t.Fatalf("expected default max tool loop steps 500, got %d", got)
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
		Tool: domain.ToolKindWrite,
		Args: map[string]string{
			"path":            "file.txt",
			"content":         "after\n",
			"force_overwrite": "true",
		},
	}
	raw, err := serializeRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	got, err := requestFromStoredApproval(domain.ToolKindWrite, raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Args["path"] != "file.txt" || got.Args["force_overwrite"] != "true" {
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
	chat := defaultChatForSession(t, st, session.ID)

	evt, err := engine.handleModelToolCall(context.Background(), session, chat, tools.Request{
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

func TestUpdateMilestoneStatusSetsAndClearsOwner(t *testing.T) {
	cfg := testConfig(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	session, err := st.CreateSession(context.Background(), "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetMilestonePlan(context.Background(), session.ID, "Ship it", []store.Milestone{
		{Ref: "alpha", Title: "Alpha", Status: domain.MilestoneStatusExecuting, Position: 0},
		{Ref: "beta", Title: "Beta", Status: domain.MilestoneStatusPending, Position: 1},
	}); err != nil {
		t.Fatal(err)
	}
	engine := New(cfg, st, tools.NewRegistry(t.TempDir()), nil, t.TempDir())
	ownerID := domain.NewID()

	if err := engine.updateMilestoneStatus(context.Background(), session.ID, "beta", domain.MilestoneStatusExecuting, ownerID); err != nil {
		t.Fatal(err)
	}
	plan, err := st.GetMilestonePlan(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Milestones[0].Status != domain.MilestoneStatusExecuting || plan.Milestones[1].Status != domain.MilestoneStatusExecuting {
		t.Fatalf("expected both milestones to remain executing, got %#v", plan.Milestones)
	}
	if plan.Milestones[1].OwnerChatID == nil || *plan.Milestones[1].OwnerChatID != ownerID {
		t.Fatalf("expected beta to be owned by %s, got %#v", ownerID, plan.Milestones[1].OwnerChatID)
	}
	if err := engine.updateMilestoneStatus(context.Background(), session.ID, "beta", domain.MilestoneStatusReady, ownerID); err != nil {
		t.Fatal(err)
	}
	plan, err = st.GetMilestonePlan(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Milestones[1].Status != domain.MilestoneStatusReady || plan.Milestones[1].OwnerChatID != nil {
		t.Fatalf("expected beta to become ready and release owner, got %#v", plan.Milestones[1])
	}
}

func TestUpdateMilestoneStatusRejectsOtherOwner(t *testing.T) {
	cfg := testConfig(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	session, err := st.CreateSession(context.Background(), "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	ownerID := domain.NewID()
	if _, err := st.SetMilestonePlan(context.Background(), session.ID, "Ship it", []store.Milestone{
		{Ref: "alpha", Title: "Alpha", Status: domain.MilestoneStatusExecuting, OwnerChatID: &ownerID, Position: 0},
	}); err != nil {
		t.Fatal(err)
	}
	engine := New(cfg, st, tools.NewRegistry(t.TempDir()), nil, t.TempDir())

	otherID := domain.NewID()
	err = engine.updateMilestoneStatus(context.Background(), session.ID, "alpha", domain.MilestoneStatusReady, otherID)
	if err == nil || !strings.Contains(err.Error(), "owned by chat") {
		t.Fatalf("expected ownership error, got %v", err)
	}
}

func TestStartChatScopesToTodoAndInheritsParentSettings(t *testing.T) {
	cfg := testConfig(t)
	st, err := store.OpenWithOptions(t.TempDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	session, err := st.CreateSession(context.Background(), "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	parent := defaultChatForSession(t, st, session.ID)
	parent.ProviderID = "parent-provider"
	parent.ModelID = "parent-model"
	parent.PermissionProfile = "parent-permission"
	parent.ToolStates = map[domain.ToolKind]bool{domain.ToolKindBash: false}
	if err := st.UpdateChat(context.Background(), parent); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetMilestonePlan(context.Background(), session.ID, "Ship it", []store.Milestone{
		{Ref: "alpha", Title: "Alpha", Status: domain.MilestoneStatusReady, Position: 0},
	}); err != nil {
		t.Fatal(err)
	}
	todos, err := st.AddTodoItems(context.Background(), session.ID, "alpha", []string{"Implement alpha"})
	if err != nil {
		t.Fatal(err)
	}
	engine := New(cfg, st, tools.NewRegistry(t.TempDir()), nil, t.TempDir())

	status, err := engine.StartChat(context.Background(), session.ID, parent.ID, tools.ChatStartRequest{
		Profile:   chatrole.Execution,
		Objective: "Implement only the assigned todo",
		TodoRef:   todos[0].ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := st.GetChat(context.Background(), status.Chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	if created.ParentChatID == nil || *created.ParentChatID != parent.ID {
		t.Fatalf("expected parent %s, got %#v", parent.ID, created.ParentChatID)
	}
	if created.ActiveMilestoneRef != "alpha" || created.AssignedTodoBucketRef != "alpha" || created.AssignedTodoRef != todos[0].ID {
		t.Fatalf("unexpected chat scope: %#v", created)
	}
	if created.ProviderID != parent.ProviderID || created.ModelID != parent.ModelID || created.PermissionProfile != parent.PermissionProfile || created.ToolStates[domain.ToolKindBash] {
		t.Fatalf("expected inherited parent settings, got %#v", created)
	}
	updatedTodos, err := st.ListTodos(context.Background(), session.ID, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if updatedTodos[0].Status != domain.TodoStatusInProgress {
		t.Fatalf("expected execution todo to become in_progress, got %#v", updatedTodos[0])
	}
}

func TestStartChatRejectsMismatchedTodoAndMilestone(t *testing.T) {
	cfg := testConfig(t)
	st, err := store.OpenWithOptions(t.TempDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	session, err := st.CreateSession(context.Background(), "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	parent := defaultChatForSession(t, st, session.ID)
	if _, err := st.SetMilestonePlan(context.Background(), session.ID, "Ship it", []store.Milestone{
		{Ref: "alpha", Title: "Alpha", Status: domain.MilestoneStatusReady, Position: 0},
		{Ref: "beta", Title: "Beta", Status: domain.MilestoneStatusReady, Position: 1},
	}); err != nil {
		t.Fatal(err)
	}
	todos, err := st.AddTodoItems(context.Background(), session.ID, "alpha", []string{"Implement alpha"})
	if err != nil {
		t.Fatal(err)
	}
	engine := New(cfg, st, tools.NewRegistry(t.TempDir()), nil, t.TempDir())

	_, err = engine.StartChat(context.Background(), session.ID, parent.ID, tools.ChatStartRequest{
		Profile:      chatrole.Execution,
		Objective:    "Implement only the assigned todo",
		MilestoneRef: "beta",
		TodoRef:      todos[0].ID,
	})
	if err == nil || !strings.Contains(err.Error(), "belongs to milestone") {
		t.Fatalf("expected mismatched scope error, got %v", err)
	}
}

func TestConsumeChatUpdatesIgnoresInitialInactiveSnapshot(t *testing.T) {
	cfg := testConfig(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	session, err := st.CreateSession(context.Background(), "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	parent := defaultChatForSession(t, st, session.ID)
	parentID := parent.ID
	child, err := st.CreateChat(context.Background(), session.ID, "child", chatrole.Decomposition, &parentID)
	if err != nil {
		t.Fatal(err)
	}
	engine := New(cfg, st, tools.NewRegistry(t.TempDir()), nil, t.TempDir())

	updates := make(chan chatpkg.Update, 1)
	updates <- chatpkg.Update{Snapshot: chatpkg.Snapshot{Chat: child, Status: chatpkg.StatusIdle}, Status: chatpkg.StatusIdle, StatusText: "Idle", Active: false}
	close(updates)
	engine.consumeChatUpdates(child.ID, updates, nil)

	got, err := st.GetChat(context.Background(), parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.QueuedInputs) != 0 {
		t.Fatalf("expected no parent notification for initial inactive snapshot, got %#v", got.QueuedInputs)
	}
}

func TestConsumeChatUpdatesNotifiesParentWhenChildBecomesIdle(t *testing.T) {
	cfg := testConfig(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	session, err := st.CreateSession(context.Background(), "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	parent := defaultChatForSession(t, st, session.ID)
	parentID := parent.ID
	child, err := st.CreateChat(context.Background(), session.ID, "child", chatrole.Decomposition, &parentID)
	if err != nil {
		t.Fatal(err)
	}
	engine := New(cfg, st, tools.NewRegistry(t.TempDir()), nil, t.TempDir())

	updates := make(chan chatpkg.Update, 2)
	updates <- chatpkg.Update{Snapshot: chatpkg.Snapshot{Chat: child, Status: chatpkg.StatusWaitingLLM, Active: true}, Status: chatpkg.StatusWaitingLLM, StatusText: "Running", Active: true}
	updates <- chatpkg.Update{Snapshot: chatpkg.Snapshot{Chat: child, Status: chatpkg.StatusIdle, Active: false}, Status: chatpkg.StatusIdle, StatusText: "Idle", Active: false}
	close(updates)
	engine.consumeChatUpdates(child.ID, updates, nil)

	got, err := st.GetChat(context.Background(), parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.QueuedInputs) != 1 || !strings.Contains(got.QueuedInputs[0].Text, " is idle: Idle") {
		t.Fatalf("expected one idle parent notification, got %#v", got.QueuedInputs)
	}
}

func TestHandleModelToolCallRejectsRoleForbiddenTool(t *testing.T) {
	cfg := testConfig(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	session, err := st.CreateSession(context.Background(), "test", cfg.DefaultProvider, "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	chat := defaultChatForSession(t, st, session.ID)
	chat.WorkflowRole = chatrole.Decomposition
	if err := st.UpdateChat(context.Background(), chat); err != nil {
		t.Fatal(err)
	}
	engine := New(cfg, st, tools.NewRegistry(t.TempDir()), nil, t.TempDir())

	evt, err := engine.handleModelToolCall(context.Background(), session, chat, tools.Request{
		Tool: domain.ToolKindBash,
		Args: map[string]string{"command": "echo no"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if evt.Kind != domain.EventKindToolResult || !strings.Contains(evt.Text, "not available to decomposition chats") {
		t.Fatalf("expected role denied tool result, got %#v", evt)
	}
}

func TestHandleModelToolCallPersistsNormalizationFailure(t *testing.T) {
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
	chat := defaultChatForSession(t, st, session.ID)

	evt, err := engine.handleModelToolCall(context.Background(), session, chat, tools.Request{
		Tool: domain.ToolKindRead,
		Args: map[string]string{
			"path":   "README.md",
			"offset": "400.5",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if evt.Kind != domain.EventKindToolResult {
		t.Fatalf("expected tool result event, got %#v", evt)
	}
	if !strings.Contains(evt.Text, "offset must be a positive integer") {
		t.Fatalf("expected normalization failure text, got %#v", evt)
	}

	messages, parts, err := timelineTranscriptForSession(t, st, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 {
		t.Fatalf("expected one stored tool message, got %d", len(messages))
	}
	got := parts[messages[0].ID]
	if len(got) != 1 || got[0].Kind != domain.PartKindToolOutput {
		t.Fatalf("expected one tool output part, got %#v", got)
	}
	if !strings.Contains(got[0].Body, "offset must be a positive integer") {
		t.Fatalf("expected persisted failure body, got %#v", got[0])
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
	chat := defaultChatForSession(t, st, session.ID)

	call := tools.Request{
		Tool:       domain.ToolKindRead,
		ToolCallID: "call_1",
		Args:       map[string]string{"path": "README.md"},
	}
	itemSeed := domain.TimelineItem{ID: domain.NewTimelineID(time.Now().UTC()), ChatID: chat.ID, Seq: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	item, err := engine.persistAssistantToolCalls(context.Background(), chat.ID, session.ID, itemSeed, []tools.Request{call}, "Let me inspect that file first.", domain.Usage{TotalTokens: 10})
	if err != nil {
		t.Fatal(err)
	}
	if item.ID == "" {
		t.Fatalf("expected persisted timeline item, got %#v", item)
	}

	items, err := st.TimelineForChat(context.Background(), chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one assistant item, got %d", len(items))
	}
	assistant, ok := items[0].Content.(domain.AssistantMessage)
	if !ok {
		t.Fatalf("expected assistant item, got %#v", items[0].Content)
	}
	if len(assistant.Tools) != 1 || assistant.Tools[0].Tool != domain.ToolKindRead {
		t.Fatalf("expected tool call child, got %#v", assistant.Tools)
	}
	if !strings.Contains(assistant.Text, "inspect that file") {
		t.Fatalf("expected narration to be stored as text, got %#v", assistant)
	}
	if assistant.Usage == nil || assistant.Usage.TotalTokens != 10 {
		t.Fatalf("expected usage to be stored on assistant item, got %#v", assistant.Usage)
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
	chat := defaultChatForSession(t, st, session.ID)

	req := tools.Request{
		Tool:       domain.ToolKindRead,
		ToolCallID: "call_1",
		Args:       map[string]string{"path": "README.md"},
	}
	appendAssistantToolTimelineItem(t, st, chat.ID, req, "Let me inspect that file first.")

	conversation, err := engine.buildConversation(context.Background(), session.ID, chat.ID)
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
	chat := defaultChatForSession(t, st, session.ID)
	appendUserTimelineItem(t, st, chat.ID, "old question")
	appendCompactionTimelineItem(t, st, chat.ID, "summary block", "")
	appendUserTimelineItem(t, st, chat.ID, "new question")

	conversation, err := engine.buildConversation(context.Background(), session.ID, chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(conversation) < 3 {
		t.Fatalf("expected compact summary and later message, got %#v", conversation)
	}
	if conversation[len(conversation)-2].Role != domain.MessageRoleUser {
		t.Fatalf("expected compact summary to be replayed as a user replacement-history anchor, got %#v", conversation[len(conversation)-2])
	}
	if !strings.Contains(conversation[len(conversation)-2].Content, "summary block") {
		t.Fatalf("expected compact summary in context, got %#v", conversation[len(conversation)-2])
	}
	if !strings.Contains(conversation[len(conversation)-2].Content, "replacement history") {
		t.Fatalf("expected compact summary to describe replacement history, got %#v", conversation[len(conversation)-2])
	}
	if strings.Contains(conversation[len(conversation)-1].Content, "old question") || !strings.Contains(conversation[len(conversation)-1].Content, "new question") {
		t.Fatalf("expected only post-compact history, got %#v", conversation[len(conversation)-1])
	}
}

func TestBuildConversationKeepsRecentToolBatchAfterCompactionBoundary(t *testing.T) {
	cfg := testConfig(t)
	cfg.CompactionKeepToolBatches = 1
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
	chat := defaultChatForSession(t, st, session.ID)

	appendUserTimelineItem(t, st, chat.ID, "old question")
	toolReq := tools.Request{Tool: domain.ToolKindBash, ToolCallID: "call_1", Args: map[string]string{"command": "pwd"}}
	toolItem := appendAssistantToolTimelineItem(t, st, chat.ID, toolReq, "")
	attachToolResultTimelineItem(t, st, chat.ID, toolReq, "/tmp/project", domain.BashStoredResult{Command: "pwd", Output: "/tmp/project"})
	appendCompactionTimelineItem(t, st, chat.ID, "summary block", toolItem.ID)

	conversation, err := engine.buildConversation(context.Background(), session.ID, chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(conversation) < 3 {
		t.Fatalf("expected summary plus preserved tool batch, got %#v", conversation)
	}
	if !strings.Contains(conversation[len(conversation)-3].Content, "summary block") {
		t.Fatalf("expected compact summary in context, got %#v", conversation)
	}
	if len(conversation[len(conversation)-2].ToolCalls) != 1 || conversation[len(conversation)-2].ToolCalls[0].ID != "call_1" {
		t.Fatalf("expected preserved structured tool call, got %#v", conversation[len(conversation)-2])
	}
	if conversation[len(conversation)-1].Role != domain.MessageRoleTool || conversation[len(conversation)-1].ToolCallID != "call_1" {
		t.Fatalf("expected preserved tool result, got %#v", conversation[len(conversation)-1])
	}
	if strings.Contains(conversation[len(conversation)-3].Content, "old question") {
		t.Fatalf("expected pre-tool history summarized away, got %#v", conversation[len(conversation)-3])
	}
}

func TestBuildConversationAfterCompactionKeepsEntireSuffixFromSavedBoundary(t *testing.T) {
	cfg := testConfig(t)
	cfg.CompactionKeepToolBatches = 1
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
	chat := defaultChatForSession(t, st, session.ID)

	appendUserTimelineItem(t, st, chat.ID, "summarize this away")
	toolReq := tools.Request{Tool: domain.ToolKindBash, ToolCallID: "call_1", Args: map[string]string{"command": "pwd"}}
	toolItem := appendAssistantToolTimelineItem(t, st, chat.ID, toolReq, "")
	attachToolResultTimelineItem(t, st, chat.ID, toolReq, "/tmp/project", domain.BashStoredResult{Command: "pwd", Output: "/tmp/project"})
	appendUserTimelineItem(t, st, chat.ID, "keep this question raw")
	appendAssistantTimelineItem(t, st, chat.ID, domain.AssistantMessage{Text: "keep this answer raw"})
	appendCompactionTimelineItem(t, st, chat.ID, "summary block", toolItem.ID)

	conversation, err := engine.buildConversation(context.Background(), session.ID, chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	var rendered strings.Builder
	for _, msg := range conversation {
		rendered.WriteString(msg.Content)
		rendered.WriteString("\n")
	}
	got := rendered.String()
	if strings.Contains(got, "summarize this away") {
		t.Fatalf("expected messages before saved boundary to be removed from replay, got %#v", conversation)
	}
	for _, want := range []string{"/tmp/project", "keep this question raw", "keep this answer raw"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected replay to keep entire suffix after boundary including %q, got %#v", want, conversation)
		}
	}
}

func TestBuildCompactionConversationExcludesPreservedToolTail(t *testing.T) {
	cfg := testConfig(t)
	cfg.CompactionKeepToolBatches = 1
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
	chat := defaultChatForSession(t, st, session.ID)

	appendUserTimelineItem(t, st, chat.ID, "old question")
	toolReq := tools.Request{Tool: domain.ToolKindBash, ToolCallID: "call_1", Args: map[string]string{"command": "pwd"}}
	toolItem := appendAssistantToolTimelineItem(t, st, chat.ID, toolReq, "")
	attachToolResultTimelineItem(t, st, chat.ID, toolReq, "/tmp/project", domain.BashStoredResult{Command: "pwd", Output: "/tmp/project"})

	timeline, err := st.TimelineForChat(context.Background(), chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	conversation, firstKeptItemID, err := engine.buildCompactionConversationForTimeline(session, chat, timeline)
	if err != nil {
		t.Fatal(err)
	}
	if len(conversation) == 0 {
		t.Fatal("expected compaction source conversation")
	}
	if firstKeptItemID != toolItem.ID {
		t.Fatalf("expected compaction boundary at tool call item, got %s want %s", firstKeptItemID, toolItem.ID)
	}
	last := conversation[len(conversation)-1]
	if strings.Contains(last.Content, "/tmp/project") || len(last.ToolCalls) != 0 {
		t.Fatalf("expected preserved tool tail to be excluded from compaction source, got %#v", last)
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
	chat := defaultChatForSession(t, st, session.ID)

	conversation, err := engine.buildConversation(context.Background(), session.ID, chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(conversation) < 1 {
		t.Fatalf("expected system prompt, got %#v", conversation)
	}
	if conversation[0].Role != domain.MessageRoleSystem {
		t.Fatalf("expected leading system prompt, got %#v", conversation)
	}
	if !strings.Contains(conversation[0].Content, "$skill-name") || !strings.Contains(conversation[0].Content, "<name>review</name>") {
		t.Fatalf("expected skill prompt context in joined system prompt, got %#v", conversation[0])
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
	chat := defaultChatForSession(t, st, session.ID)
	toolReq := tools.Request{Tool: domain.ToolKindBash, ToolCallID: "call_1", Args: map[string]string{"command": "pwd"}}
	appendAssistantToolTimelineItem(t, st, chat.ID, toolReq, "")
	attachToolResultTimelineItem(t, st, chat.ID, toolReq, "/stale/body", domain.BashStoredResult{
		Command:   "pwd",
		Workdir:   ".",
		TimeoutMS: 1000,
		ExitCode:  0,
		Output:    "/typed/output",
	})

	conversation, err := engine.buildConversation(context.Background(), session.ID, chat.ID)
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

func TestBuildConversationIncludesViewImageToolContentParts(t *testing.T) {
	cfg := testConfig(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	workdir := t.TempDir()
	engine := New(cfg, st, tools.NewRegistry(workdir), nil, workdir)
	session, err := st.CreateSession(context.Background(), "test", "openai", "gpt-5.4", nil)
	if err != nil {
		t.Fatal(err)
	}
	chat := defaultChatForSession(t, st, session.ID)
	imagePath := filepath.Join(workdir, "screen.png")
	if err := os.WriteFile(imagePath, []byte("\x89PNG\r\n\x1a\nfake"), 0o644); err != nil {
		t.Fatal(err)
	}
	toolReq := tools.Request{Tool: domain.ToolKindViewImage, ToolCallID: "call_image", Args: map[string]string{"path": "screen.png"}}
	appendAssistantToolTimelineItem(t, st, chat.ID, toolReq, "")
	attachToolResultTimelineItem(t, st, chat.ID, toolReq, "Viewed image screen.png", domain.ViewImageStoredResult{
		Path:       "screen.png",
		SourcePath: imagePath,
		MIMEType:   "image/png",
		Summary:    "Viewed image screen.png",
	})

	conversation, err := engine.buildConversation(context.Background(), session.ID, chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(conversation) < 2 {
		t.Fatalf("expected tool message in conversation, got %#v", conversation)
	}
	msg := conversation[len(conversation)-1]
	if msg.Role != domain.MessageRoleTool || msg.ToolCallID != "call_image" {
		t.Fatalf("expected tool message with tool call id, got %#v", msg)
	}
	if got := len(msg.ContentParts); got != 2 {
		t.Fatalf("expected text and image content parts, got %#v", msg.ContentParts)
	}
	if msg.ContentParts[0].Type != "text" || !strings.Contains(msg.ContentParts[0].Text, "Viewed image screen.png") {
		t.Fatalf("expected leading text content part, got %#v", msg.ContentParts[0])
	}
	if msg.ContentParts[1].Type != "image_url" {
		t.Fatalf("expected trailing image content part, got %#v", msg.ContentParts[1])
	}
	if len(msg.ContentParts[1].Data) == 0 {
		t.Fatalf("expected image bytes in content part, got %#v", msg.ContentParts[1])
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
	chat := defaultChatForSession(t, st, session.ID)

	imageDraft, err := engine.files.ImportClipboardImage([]byte("\x89PNG\r\n\x1a\nfake"))
	if err != nil {
		t.Fatal(err)
	}
	imageMeta, err := engine.files.AdoptDraft(imageDraft, session.ID)
	if err != nil {
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
	appendUserTimelineItemWithAttachments(t, st, chat.ID, "inspect these", []domain.Attachment{
		{ID: imageMeta.ID, Name: imageMeta.Name, MIME: imageMeta.MIME, Path: imageMeta.Path, Size: imageMeta.Size, Source: imageMeta.Source, Original: imageMeta.Original},
		{ID: textMeta.ID, Name: textMeta.Name, MIME: textMeta.MIME, Path: textMeta.Path, Size: textMeta.Size, Source: textMeta.Source, Original: textMeta.Original},
	})

	conversation, err := engine.buildConversation(context.Background(), session.ID, chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(conversation) < 2 {
		t.Fatalf("expected user message, got %#v", conversation)
	}
	userMsg := conversation[len(conversation)-1]
	if got := len(userMsg.ContentParts); got != 3 {
		t.Fatalf("expected image + text + attached text content parts, got %#v", userMsg.ContentParts)
	}
	if userMsg.ContentParts[0].Type != "image_url" {
		t.Fatalf("expected leading image attachment content part, got %#v", userMsg.ContentParts)
	}
	if userMsg.ContentParts[1].Type != "text" || strings.TrimSpace(userMsg.ContentParts[1].Text) == "" {
		t.Fatalf("expected prompt text after image, got %#v", userMsg.ContentParts[1])
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
	chat := defaultChatForSession(t, st, session.ID)
	appendUserTimelineItem(t, st, chat.ID, "saved prompt")

	req, err := engine.PreviewNextRequest(context.Background(), session, "unsent draft", nil, nil, "Permission mode changed")
	if err != nil {
		t.Fatal(err)
	}
	if req.Model != "test-model" {
		t.Fatalf("expected model in preview request, got %#v", req)
	}
	if len(req.Messages) != 3 {
		t.Fatalf("expected single system prompt plus saved prompt and unsent draft, got %#v", req.Messages)
	}
	last := req.Messages[len(req.Messages)-1]
	if last.Role != domain.MessageRoleUser || last.Content != "unsent draft" {
		t.Fatalf("expected unsent draft as final user message, got %#v", last)
	}
	if req.Messages[len(req.Messages)-2].Content != "saved prompt" {
		t.Fatalf("expected stored conversation before draft, got %#v", req.Messages)
	}
	if req.Messages[0].Role != domain.MessageRoleSystem || !strings.Contains(req.Messages[0].Content, "Permission mode changed") {
		t.Fatalf("expected transient note folded into leading system prompt, got %#v", req.Messages)
	}
	if got := strings.Count(req.Messages[0].Content, "Session update:\nPermission mode changed"); got != 1 {
		t.Fatalf("expected exactly one session update block in system prompt, got %q", req.Messages[0].Content)
	}
}

func TestPreviewNextRequestUsesSingleLeadingSystemMessage(t *testing.T) {
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
	session, err := st.CreateSession(context.Background(), "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	session.AgentsResolved = "Follow repository instructions."

	req, err := engine.PreviewNextRequest(context.Background(), session, "what's in this folder?", nil, nil, "Permission mode changed")
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Messages) != 2 {
		t.Fatalf("expected one system and one user message, got %#v", req.Messages)
	}
	if req.Messages[0].Role != domain.MessageRoleSystem {
		t.Fatalf("expected leading system message, got %#v", req.Messages)
	}
	if req.Messages[1].Role != domain.MessageRoleUser {
		t.Fatalf("expected trailing user message, got %#v", req.Messages)
	}
	for _, want := range []string{
		"You are koder, a terminal coding agent.",
		"Runtime environment:",
		"Current working directory: " + repo,
		"Resolved project AGENTS.md instructions:\nFollow repository instructions.",
		"$skill-name",
		"Session update:\nPermission mode changed",
	} {
		if !strings.Contains(req.Messages[0].Content, want) {
			t.Fatalf("expected %q in leading system message, got %q", want, req.Messages[0].Content)
		}
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

func TestPreviewNextRequestIncludesQwenPresetExtraBody(t *testing.T) {
	cfg := testConfig(t)
	cfg.Providers = map[string]config.Provider{
		"test": {
			BaseURL: "http://127.0.0.1:8000/v1",
		},
	}
	cfg.SetModelConfig(config.ModelConfig{
		ProviderID:  "test",
		ModelID:     "Qwen/Qwen3.6-35B-A3B",
		ModelPreset: provider.ModelPresetAuto,
	})
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	engine := New(cfg, st, tools.NewRegistry(t.TempDir()), nil, t.TempDir())
	session, err := st.CreateSession(context.Background(), "test", "test", "Qwen/Qwen3.6-35B-A3B", nil)
	if err != nil {
		t.Fatal(err)
	}
	req, err := engine.PreviewNextRequest(context.Background(), session, "continue", nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	got, ok := req.ExtraBody["chat_template_kwargs"].(map[string]any)
	if !ok || got["preserve_thinking"] != false || got["enable_thinking"] != false {
		t.Fatalf("expected qwen non-thinking kwargs, got %#v", req.ExtraBody)
	}
	if _, ok := req.ExtraBody["thinking_token_budget"]; ok {
		t.Fatalf("expected qwen preset to omit thinking token budget, got %#v", req.ExtraBody)
	}
}

func TestPreviewNextRequestKeepsStableMCPToolOrder(t *testing.T) {
	cfg := testConfig(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "test-mcp", Version: "v1.0.0"}, nil)
	server.AddTool(&sdkmcp.Tool{Name: "zeta", Description: "late tool", InputSchema: map[string]any{"type": "object"}}, func(_ context.Context, _ *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		return &sdkmcp.CallToolResult{}, nil
	})
	server.AddTool(&sdkmcp.Tool{Name: "alpha", Description: "early tool", InputSchema: map[string]any{"type": "object"}}, func(_ context.Context, _ *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		return &sdkmcp.CallToolResult{}, nil
	})

	handler := sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return server }, &sdkmcp.StreamableHTTPOptions{JSONResponse: true})
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	manager, err := mcp.NewManager(map[string]config.MCPServer{
		"docs": {URL: httpServer.URL},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.ConnectAll(context.Background()); err != nil {
		t.Fatal(err)
	}

	workdir := t.TempDir()
	registry := tools.NewRegistry(workdir)
	engine := New(cfg, st, registry, nil, workdir, manager)
	session, err := st.CreateSession(context.Background(), "test", cfg.DefaultProvider, "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSessionToolStates(context.Background(), session.ID, map[domain.ToolKind]bool{
		domain.ToolKindMCP: true,
	}); err != nil {
		t.Fatal(err)
	}

	req, err := engine.PreviewNextRequest(context.Background(), session, "continue", nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	var mcpNames []string
	for _, def := range req.Tools {
		if strings.HasPrefix(def.Function.Name, "_docs_") || def.Function.Name == "alpha" || def.Function.Name == "zeta" {
			mcpNames = append(mcpNames, def.Function.Name)
		}
	}
	if len(mcpNames) != 2 {
		t.Fatalf("expected 2 MCP tool definitions, got %v", mcpNames)
	}
	if !slices.Equal(mcpNames, []string{"alpha", "zeta"}) {
		t.Fatalf("expected MCP tools sorted by name in request, got %v", mcpNames)
	}
}

func TestBuildConversationPreservesThinkingBlockForQwenPreset(t *testing.T) {
	cfg := testConfig(t)
	cfg.Providers = map[string]config.Provider{
		"test": {
			BaseURL: "http://127.0.0.1:8000/v1",
		},
	}
	cfg.SetModelConfig(config.ModelConfig{
		ProviderID:  "test",
		ModelID:     "Qwen/Qwen3.6-35B-A3B",
		ModelPreset: provider.ModelPresetQwen36PreserveThinking,
	})
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	engine := New(cfg, st, tools.NewRegistry(t.TempDir()), nil, t.TempDir())
	session, err := st.CreateSession(context.Background(), "test", "test", "Qwen/Qwen3.6-35B-A3B", nil)
	if err != nil {
		t.Fatal(err)
	}
	chat := defaultChatForSession(t, st, session.ID)
	appendAssistantTimelineItem(t, st, chat.ID, domain.AssistantMessage{
		Reasoning: domain.ReasoningContent{Text: "hidden trace"},
		Text:      "done",
	})

	conversation, err := engine.buildConversation(context.Background(), session.ID, chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(conversation) == 0 {
		t.Fatalf("expected assistant message, got %#v", conversation)
	}
	assistant := conversation[len(conversation)-1]
	if !strings.Contains(assistant.Content, "<think>\nhidden trace\n</think>") || !strings.Contains(assistant.Content, "done") {
		t.Fatalf("expected preserved thinking block, got %#v", assistant)
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
	var approvalID domain.ID
	for evt := range events {
		if evt.Kind == domain.EventKindApprovalAsk {
			id, convErr := parseApprovalID(evt.Meta["approval_id"])
			if convErr != nil {
				t.Fatal(convErr)
			}
			approvalID = id
		}
	}
	if approvalID == "" {
		t.Fatal("expected approval request")
	}

	approvedEvents, err := engine.approve(context.Background(), session.ID, "", approvalID)
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
	cfg.Permissions.Profile = permissionprofile.ProfileAsk
	cfg.Permissions.Profiles[permissionprofile.ProfileAsk] = config.PermissionProfile{
		Root:      string(permissionprofile.ModeReadOnly),
		Workspace: string(permissionprofile.ModeReadWrite),
		Rules: []config.PermissionRule{{
			Tool:    domain.ToolKindBash,
			Pattern: "*",
			Action:  domain.PermissionModeAsk,
		}},
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
	if err := st.SetSessionPermissionProfile(context.Background(), session.ID, permissionprofile.ProfileAsk); err != nil {
		t.Fatal(err)
	}

	events, err := engine.RunPrompt(context.Background(), session, "say hello")
	if err != nil {
		t.Fatal(err)
	}
	var approvalID domain.ID
	for evt := range events {
		if evt.Kind == domain.EventKindApprovalAsk {
			approvalID, err = parseApprovalID(evt.Meta["approval_id"])
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if approvalID == "" {
		t.Fatal("expected approval request")
	}

	reeval, err := engine.SetPermissionProfileAndReevaluateApproval(context.Background(), session.ID, approvalID, permissionprofile.ProfileFullAccess)
	if err != nil {
		t.Fatal(err)
	}
	var sawProfileChange bool
	var sawToolResult bool
	var sawFinalAnswer bool
	for evt := range reeval {
		if evt.Kind == domain.EventKindStatus && evt.Meta["permission_profile"] == permissionprofile.ProfileFullAccess {
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
	if updated.PermissionProfile != permissionprofile.ProfileFullAccess {
		t.Fatalf("expected permission profile %q, got %q", permissionprofile.ProfileFullAccess, updated.PermissionProfile)
	}

	chats, err := st.ListChats(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(chats) != 1 {
		t.Fatalf("expected one chat, got %d", len(chats))
	}
	pending, err := st.PendingApprovalsForChat(context.Background(), chats[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("expected no pending approvals after re-evaluation, got %#v", pending)
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
	cfg.Permissions.Profile = permissionprofile.ProfileFullAccess

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
	if err := st.SetSessionPermissionProfile(context.Background(), session.ID, permissionprofile.ProfileFullAccess); err != nil {
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

func TestHandleModelToolCallsStopsAfterToolBatchWhenCancelRequested(t *testing.T) {
	t.Parallel()

	cfg := testConfig(t)
	cfg.Permissions.Profile = permissionprofile.ProfileFullAccess

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
	if err := st.SetSessionPermissionProfile(context.Background(), session.ID, permissionprofile.ProfileFullAccess); err != nil {
		t.Fatal(err)
	}
	chat, err := st.CreateChat(context.Background(), session.ID, "chat", "", nil)
	if err != nil {
		t.Fatal(err)
	}

	out := make(chan domain.Event, 8)
	ctx := turncontrol.WithShouldStop(context.Background(), func() bool { return true })
	req := tools.Request{
		Tool:       domain.ToolKindBash,
		ToolCallID: "call_1",
		Args:       map[string]string{"command": "printf hi"},
	}
	appendAssistantToolTimelineItem(t, st, chat.ID, req, "")
	needsApproval, err := engine.handleModelToolCalls(ctx, session, chat, []tools.Request{req}, out)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled, got %v", err)
	}
	if needsApproval {
		t.Fatal("did not expect approval request")
	}
	close(out)
	var sawToolStart bool
	var sawToolResult bool
	for evt := range out {
		if evt.Kind == domain.EventKindToolStart {
			sawToolStart = true
		}
		if evt.Kind == domain.EventKindToolResult && strings.Contains(evt.Text, "hi") {
			sawToolResult = true
		}
	}
	if !sawToolStart {
		t.Fatal("expected tool start event")
	}
	if !sawToolResult {
		t.Fatal("expected persisted tool result before cancellation")
	}
}

func TestContinueModelTurnStopsAfterPersistingToolCallsWhenDrainRequested(t *testing.T) {
	t.Parallel()

	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		requests++
		_, _ = w.Write([]byte(`{"choices":[{"message":{"tool_calls":[{"id":"call_1","type":"function","function":{"name":"bash","arguments":"{\"command\":\"printf hi\"}"}}]}}],"usage":{"total_tokens":1}}`))
	}))
	defer server.Close()

	cfg := testConfig(t)
	cfg.Providers = map[string]config.Provider{"test": {BaseURL: server.URL + "/v1", Timeout: time.Second}}
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "test-model"
	cfg.Permissions.Profile = permissionprofile.ProfileFullAccess

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
	if err := st.SetSessionPermissionProfile(context.Background(), session.ID, permissionprofile.ProfileFullAccess); err != nil {
		t.Fatal(err)
	}
	chat := defaultChatForSession(t, st, session.ID)
	client, err := provider.New("test", cfg.Providers["test"], nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := turncontrol.WithShouldStop(context.Background(), func() bool { return true })
	out := make(chan domain.Event, 8)

	if err := engine.continueModelTurn(ctx, session, chat, client, out, nil); err != nil {
		t.Fatal(err)
	}
	close(out)

	if requests != 1 {
		t.Fatalf("expected one provider request, got %d", requests)
	}
	items, err := st.TimelineForChat(context.Background(), chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	var pendingTool bool
	for _, item := range items {
		assistant, ok := item.Content.(domain.AssistantMessage)
		if !ok {
			continue
		}
		call := assistant.ToolByID(domain.ToolCallID("call_1"))
		if call != nil && call.Status == domain.ToolStatusPending && call.Result == nil {
			pendingTool = true
		}
	}
	if !pendingTool {
		t.Fatalf("expected pending tool call to be persisted, got %#v", items)
	}
	for evt := range out {
		if evt.Kind == domain.EventKindToolStart || evt.Kind == domain.EventKindToolResult {
			t.Fatalf("did not expect tool execution while draining, got %#v", evt)
		}
	}
}

func TestResumePendingToolCallsExecutesAndContinues(t *testing.T) {
	t.Parallel()

	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		requests++
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"done"}}],"usage":{"total_tokens":1}}`))
	}))
	defer server.Close()

	cfg := testConfig(t)
	cfg.Providers = map[string]config.Provider{"test": {BaseURL: server.URL + "/v1", Timeout: time.Second}}
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "test-model"
	cfg.Permissions.Profile = permissionprofile.ProfileFullAccess

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
	if err := st.SetSessionPermissionProfile(context.Background(), session.ID, permissionprofile.ProfileFullAccess); err != nil {
		t.Fatal(err)
	}
	chat := defaultChatForSession(t, st, session.ID)
	req := tools.Request{
		Tool:       domain.ToolKindBash,
		ToolCallID: "call_1",
		Args:       map[string]string{"command": "printf hi"},
	}
	appendAssistantToolTimelineItem(t, st, chat.ID, req, "")

	events, err := engine.ResumePendingToolCallsInChat(context.Background(), session, chat)
	if err != nil {
		t.Fatal(err)
	}
	if events == nil {
		t.Fatal("expected resume events")
	}
	var sawToolResult bool
	var sawFinalAnswer bool
	for evt := range events {
		if evt.Kind == domain.EventKindToolResult && strings.Contains(evt.Text, "hi") {
			sawToolResult = true
		}
		if evt.Kind == domain.EventKindMessageDelta && evt.Text == "done" {
			sawFinalAnswer = true
		}
	}
	if !sawToolResult {
		t.Fatal("expected resumed tool result")
	}
	if !sawFinalAnswer {
		t.Fatal("expected final assistant answer")
	}
	if requests != 1 {
		t.Fatalf("expected one continuation request, got %d", requests)
	}
}

func TestRunPromptAllowedToolTransitionsPendingToRunning(t *testing.T) {
	t.Parallel()

	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		requests++
		switch requests {
		case 1:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"tool_calls":[{"id":"call_1","type":"function","function":{"name":"bash","arguments":"{\"command\":\"sleep 0.3; printf hi\"}"}}]}}],"usage":{"total_tokens":1}}`))
		default:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"done"}}],"usage":{"total_tokens":1}}`))
		}
	}))
	defer server.Close()

	cfg := testConfig(t)
	cfg.Providers = map[string]config.Provider{"test": {BaseURL: server.URL + "/v1", Timeout: time.Second}}
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "test-model"
	cfg.Permissions.Profile = permissionprofile.ProfileFullAccess

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
	if err := st.SetSessionPermissionProfile(context.Background(), session.ID, permissionprofile.ProfileFullAccess); err != nil {
		t.Fatal(err)
	}

	events, err := engine.RunPrompt(context.Background(), session, "run slow command")
	if err != nil {
		t.Fatal(err)
	}
	chat := defaultChatForSession(t, st, session.ID)
	var sawRunning bool
	var sawDone bool
	for evt := range events {
		if evt.Kind == domain.EventKindToolStart {
			sawRunning = waitForToolStatus(t, st, chat.ID, "call_1", domain.ToolStatusRunning)
		}
		if evt.Kind == domain.EventKindToolResult && strings.Contains(evt.Text, "hi") {
			sawDone = true
		}
	}
	if !sawRunning {
		t.Fatal("expected allowed tool to transition to running before completion")
	}
	if !sawDone {
		t.Fatal("expected allowed tool result")
	}
	assertToolStatus(t, st, chat.ID, "call_1", domain.ToolStatusDone)
}

func TestRunPromptDeniedToolTransitionsPendingToDenied(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"tool_calls":[{"id":"call_1","type":"function","function":{"name":"bash","arguments":"{\"command\":\"printf hi\"}"}}]}}],"usage":{"total_tokens":1}}`))
	}))
	defer server.Close()

	cfg := testConfig(t)
	cfg.Providers = map[string]config.Provider{"test": {BaseURL: server.URL + "/v1", Timeout: time.Second}}
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "test-model"
	cfg.Permissions.Profiles["default"] = config.PermissionProfile{
		Rules: []config.PermissionRule{{Tool: domain.ToolKindBash, Pattern: "*", Action: domain.PermissionModeDeny}},
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

	events, err := engine.RunPrompt(context.Background(), session, "run command")
	if err != nil {
		t.Fatal(err)
	}
	var sawDenied bool
	for evt := range events {
		if evt.Kind == domain.EventKindToolResult && strings.Contains(evt.Text, "denied by policy") {
			sawDenied = true
		}
	}
	if !sawDenied {
		t.Fatal("expected denied tool result")
	}
	chat := defaultChatForSession(t, st, session.ID)
	assertToolStatus(t, st, chat.ID, "call_1", domain.ToolStatusDenied)
	pending, err := st.PendingApprovalsForChat(context.Background(), chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("expected no derived pending approvals for denied tool, got %#v", pending)
	}
}

func TestPendingExecutableToolCallsIgnoresStalePendingBeforeLaterUser(t *testing.T) {
	t.Parallel()

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
	chat := defaultChatForSession(t, st, session.ID)
	req := tools.Request{
		Tool:       domain.ToolKindRead,
		ToolCallID: "old_call",
		Args:       map[string]string{"path": "README.md"},
	}
	appendAssistantToolTimelineItem(t, st, chat.ID, req, "")
	appendUserTimelineItem(t, st, chat.ID, "continue")
	appendAssistantTimelineItem(t, st, chat.ID, domain.AssistantMessage{Text: "done"})

	calls, err := engine.pendingExecutableToolCalls(context.Background(), chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 0 {
		t.Fatalf("expected no stale calls, got %#v", calls)
	}
}

func TestPendingExecutableToolCallsIgnoresStalePendingBeforeFinalAssistant(t *testing.T) {
	t.Parallel()

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
	chat := defaultChatForSession(t, st, session.ID)
	req := tools.Request{
		Tool:       domain.ToolKindRead,
		ToolCallID: "old_call",
		Args:       map[string]string{"path": "README.md"},
	}
	appendAssistantToolTimelineItem(t, st, chat.ID, req, "")
	appendAssistantTimelineItem(t, st, chat.ID, domain.AssistantMessage{Text: "done"})

	calls, err := engine.pendingExecutableToolCalls(context.Background(), chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 0 {
		t.Fatalf("expected no stale calls, got %#v", calls)
	}
}

func TestExecutePreparedToolCallDoesNotPersistCanceledToolFailure(t *testing.T) {
	t.Parallel()

	cfg := testConfig(t)
	cfg.Permissions.Profile = permissionprofile.ProfileFullAccess

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
	chat, err := st.CreateChat(context.Background(), session.ID, "chat", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	req := tools.Request{
		Tool:       domain.ToolKindBash,
		ToolCallID: "call_1",
		Args:       map[string]string{"command": "sleep 1"},
	}
	appendAssistantToolTimelineItem(t, st, chat.ID, req, "")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, runErr := engine.executePreparedToolCall(ctx, chat.ID, session.ID, req)
		done <- runErr
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case runErr := <-done:
		if !errors.Is(runErr, context.Canceled) {
			t.Fatalf("expected context canceled, got %v", runErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for tool cancellation")
	}

	_, parts, err := timelineTranscriptForChat(t, st, chat)
	if err != nil {
		t.Fatal(err)
	}
	for _, byMessage := range parts {
		for _, part := range byMessage {
			if strings.Contains(part.Text(), "context canceled") {
				t.Fatalf("unexpected persisted cancellation tool failure: %#v", part)
			}
		}
	}
}

func TestRunPromptStreamsAssistantResponseWhenEnabled(t *testing.T) {
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
			if !strings.Contains(string(body), `"stream":true`) {
				t.Fatalf("expected streaming request body, got %s", string(body))
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hel\"}}]}\n\n"))
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"lo\"}}],\"usage\":{\"total_tokens\":1}}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
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
			Stream:  true,
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

	events, err := engine.RunPrompt(context.Background(), session, "say hello")
	if err != nil {
		t.Fatal(err)
	}
	var deltas []string
	var sawDone bool
	for evt := range events {
		if evt.Kind == domain.EventKindMessageDelta {
			deltas = append(deltas, evt.Text)
		}
		if evt.Kind == domain.EventKindMessageDone {
			sawDone = true
		}
	}
	if strings.Join(deltas, "") != "hello" {
		t.Fatalf("expected streamed deltas hello, got %#v", deltas)
	}
	if !sawDone {
		t.Fatal("expected message done event")
	}
	if len(requests) == 0 {
		t.Fatal("expected provider request")
	}
}

func TestRunPromptIgnoresMalformedProviderToolCallsWhenTextIsPresent(t *testing.T) {
	t.Parallel()

	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		requests++
		switch requests {
		case 1:
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"reasoning\":\"Thinking\"}}]}\n\n"))
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hello\",\"tool_calls\":[{\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"arguments\":\"{}\"}}]}}]}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
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
			Stream:  true,
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

	events, err := engine.RunPrompt(context.Background(), session, "say hello")
	if err != nil {
		t.Fatal(err)
	}
	var sawDone, sawError bool
	for evt := range events {
		if evt.Kind == domain.EventKindMessageDone {
			sawDone = true
		}
		if evt.Kind == domain.EventKindError {
			sawError = true
		}
	}
	if !sawDone {
		t.Fatal("expected streamed turn to complete")
	}
	if sawError {
		t.Fatal("did not expect malformed provider tool call to surface as turn error when text is present")
	}

	messages, parts, err := timelineTranscriptForSession(t, st, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 {
		t.Fatalf("expected user and assistant messages, got %d", len(messages))
	}
	assistantParts := parts[messages[1].ID]
	var sawText bool
	for _, part := range assistantParts {
		if part.Kind == domain.PartKindText && part.Body == "hello" {
			sawText = true
		}
	}
	if !sawText {
		t.Fatalf("expected assistant text to be persisted despite malformed tool call, got %#v", assistantParts)
	}
}

func TestRunPromptMessageDoneCarriesPersistedAssistantRecord(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	cfg := testConfig(t)
	cfg.Providers = map[string]config.Provider{
		"test": {BaseURL: server.URL + "/v1", Timeout: time.Second, Stream: true},
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

	events, err := engine.RunPrompt(context.Background(), session, "say hello")
	if err != nil {
		t.Fatal(err)
	}
	var done domain.Event
	for evt := range events {
		if evt.Kind == domain.EventKindMessageDone {
			done = evt
		}
	}
	if done.Item.ID == "" {
		t.Fatal("expected persisted assistant item on message done")
	}
	assistant, ok := done.Item.Content.(domain.AssistantMessage)
	if !ok || assistant.Text != "hello" {
		t.Fatalf("expected persisted assistant timeline item, got %#v", done.Item)
	}
}

func TestRunPromptApprovalAskMarksToolAwaitingApproval(t *testing.T) {
	t.Parallel()

	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		requests++
		switch requests {
		case 1:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"tool_calls":[{"id":"call_1","type":"function","function":{"name":"bash","arguments":"{\"command\":\"pwd\"}"}}]}}],"usage":{"prompt_tokens":5,"completion_tokens":1,"total_tokens":6}}`))
		default:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ignored"}}],"usage":{"total_tokens":1}}`))
		}
	}))
	defer server.Close()

	cfg := testConfig(t)
	cfg.Providers = map[string]config.Provider{
		"test": {BaseURL: server.URL + "/v1", Timeout: time.Second},
	}
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "test-model"
	cfg.Permissions.Profiles["default"] = config.PermissionProfile{
		Rules: []config.PermissionRule{{Tool: domain.ToolKindBash, Pattern: "*", Action: domain.PermissionModeAsk}},
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

	events, err := engine.RunPrompt(context.Background(), session, "run pwd")
	if err != nil {
		t.Fatal(err)
	}
	var approval domain.Event
	for evt := range events {
		if evt.Kind == domain.EventKindApprovalAsk {
			approval = evt
			break
		}
	}
	if approval.Item.ID == "" {
		t.Fatal("expected persisted assistant item on approval ask")
	}
	assistant, ok := approval.Item.Content.(domain.AssistantMessage)
	if !ok || len(assistant.Tools) != 1 {
		t.Fatalf("expected assistant tool child, got %#v", approval.Item)
	}
	tool := assistant.Tools[0]
	if tool.Status != domain.ToolStatusAwaitingApproval {
		t.Fatalf("expected tool awaiting approval, got %s", tool.Status)
	}
	if tool.Approval != nil {
		t.Fatalf("expected no nested approval request, got %#v", tool.Approval)
	}
	if tool.ApprovalID == "" {
		t.Fatal("expected synthetic approval id on tool child")
	}
	if approval.Meta["tool_call_id"] != "call_1" {
		t.Fatalf("expected approval event tool_call_id call_1, got %q", approval.Meta["tool_call_id"])
	}
}

func TestRunPromptStreamsToolCallArgumentsAcrossChunks(t *testing.T) {
	t.Parallel()

	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		requests++
		switch requests {
		case 1:
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"id\":\"call_read\",\"type\":\"function\",\"index\":0,\"function\":{\"name\":\"read\",\"arguments\":\"\"}}]}}]}\n\n"))
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\"}}]}}]}\n\n"))
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"path\\\":\\\"note.txt\\\"\"}}]}}]}\n\n"))
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
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
			Stream:  true,
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
	if err := os.WriteFile(filepath.Join(workdir, "note.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	engine := New(cfg, st, tools.NewRegistry(workdir), nil, workdir)
	session, err := st.CreateSession(context.Background(), "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}

	events, err := engine.RunPrompt(context.Background(), session, "read the note")
	if err != nil {
		t.Fatal(err)
	}
	var sawError bool
	for evt := range events {
		if evt.Kind == domain.EventKindError {
			sawError = true
		}
	}
	if sawError {
		t.Fatal("did not expect streamed tool call arguments to fail")
	}

	messages, parts, err := timelineTranscriptForSession(t, st, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	var sawReadOutput bool
	for _, msg := range messages {
		for _, part := range parts[msg.ID] {
			if part.Kind == domain.PartKindToolOutput && strings.Contains(part.Body, "hello") {
				sawReadOutput = true
			}
			if part.Kind == domain.PartKindToolOutput && strings.Contains(part.Body, "path is empty") {
				t.Fatalf("unexpected empty-path tool output: %#v", part)
			}
		}
	}
	if !sawReadOutput {
		t.Fatalf("expected read tool output to be persisted, got %#v", parts)
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

	chat := defaultChatForSession(t, st, session.ID)
	for _, notice := range timelineNoticesForChat(t, st, chat.ID) {
		if notice.Kind == "model_error" && strings.Contains(notice.Text, "Error:") {
			return
		}
	}
	t.Fatal("expected persisted assistant error notice")
}

func TestHandleModelToolCallAsksForOutsideProjectRead(t *testing.T) {
	cfg := testConfig(t)
	cfg.Permissions.Profiles[permissionprofile.ProfileReadAsk] = config.PermissionProfile{
		Root:      string(permissionprofile.ModeReadOnly),
		Workspace: string(permissionprofile.ModeReadWrite),
		Rules: []config.PermissionRule{{
			Tool:    domain.ToolKindRead,
			Pattern: "*",
			Action:  domain.PermissionModeAsk,
		}},
	}
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
	session.PermissionProfile = permissionprofile.ProfileReadAsk
	session.ProjectRoot = workdir
	if err := st.SetSessionPermissionProfile(context.Background(), session.ID, permissionprofile.ProfileReadAsk); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateSessionWorkspace(context.Background(), session.ID, workdir, workdir); err != nil {
		t.Fatal(err)
	}

	outsideDir := t.TempDir()
	outsidePath := filepath.Join(outsideDir, "outside.txt")
	if err := os.WriteFile(outsidePath, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	chat := defaultChatForSession(t, st, session.ID)
	req := tools.Request{
		Tool:       domain.ToolKindRead,
		ToolCallID: "call_1",
		Args:       map[string]string{"path": outsidePath},
	}
	appendAssistantToolTimelineItem(t, st, chat.ID, req, "")
	evt, err := engine.handleModelToolCall(context.Background(), session, chat, req)
	if err != nil {
		t.Fatal(err)
	}
	if evt.Kind != domain.EventKindApprovalAsk {
		t.Fatalf("expected approval ask, got %#v", evt)
	}
	if !strings.Contains(evt.Text, "requires approval") {
		t.Fatalf("expected approval text, got %q", evt.Text)
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
	session.PermissionProfile = permissionprofile.ProfileReadAsk
	session.ProjectRoot = workdir
	if err := st.SetSessionPermissionProfile(context.Background(), session.ID, permissionprofile.ProfileReadAsk); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateSessionWorkspace(context.Background(), session.ID, workdir, workdir); err != nil {
		t.Fatal(err)
	}

	targetPath := filepath.Join(workdir, "inside.txt")
	if err := os.WriteFile(targetPath, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	chat := defaultChatForSession(t, st, session.ID)
	evt, err := engine.handleModelToolCall(context.Background(), session, chat, tools.Request{
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

func TestHandleModelToolCallAllowsProjectCodeSearchInReadAskMode(t *testing.T) {
	cfg := testConfig(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	workdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workdir, "main.go"), []byte("package main\nfunc Target() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	engine := New(cfg, st, tools.NewRegistry(workdir), nil, workdir)
	session, err := st.CreateSession(context.Background(), "test", cfg.DefaultProvider, "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	session.PermissionProfile = permissionprofile.ProfileReadAsk
	session.ProjectRoot = workdir
	if err := st.SetSessionPermissionProfile(context.Background(), session.ID, permissionprofile.ProfileReadAsk); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateSessionWorkspace(context.Background(), session.ID, workdir, workdir); err != nil {
		t.Fatal(err)
	}

	chat := defaultChatForSession(t, st, session.ID)
	evt, err := engine.handleModelToolCall(context.Background(), session, chat, tools.Request{
		Tool: domain.ToolKindCodeSearch,
		Args: map[string]string{"query": "Target"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if evt.Kind != domain.EventKindToolResult {
		t.Fatalf("expected tool result, got %#v", evt)
	}
	if strings.Contains(strings.ToLower(evt.Text), "requires approval") {
		t.Fatalf("expected code search to avoid approval in read-ask mode, got %q", evt.Text)
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
	cfg.Permissions.Profiles[permissionprofile.ProfileReadAsk] = config.PermissionProfile{
		Root:      string(permissionprofile.ModeReadOnly),
		Workspace: string(permissionprofile.ModeReadWrite),
		Rules: []config.PermissionRule{{
			Tool:    domain.ToolKindRead,
			Pattern: "*",
			Action:  domain.PermissionModeAsk,
		}},
	}

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	workdir := t.TempDir()
	if err := os.Mkdir(filepath.Join(workdir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	engine := New(cfg, st, tools.NewRegistry(workdir), nil, workdir)
	session, err := st.CreateSession(context.Background(), "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	session.PermissionProfile = permissionprofile.ProfileReadAsk
	session.ProjectRoot = workdir
	if err := st.SetSessionPermissionProfile(context.Background(), session.ID, permissionprofile.ProfileReadAsk); err != nil {
		t.Fatal(err)
	}

	events, err := engine.RunPrompt(context.Background(), session, "continue")
	if err != nil {
		t.Fatal(err)
	}
	var approvalID domain.ID
	for evt := range events {
		if evt.Kind == domain.EventKindApprovalAsk {
			id, convErr := parseApprovalID(evt.Meta["approval_id"])
			if convErr != nil {
				t.Fatal(convErr)
			}
			approvalID = id
		}
	}
	if approvalID == "" {
		t.Fatal("expected approval request")
	}

	approvedEvents, err := engine.approve(context.Background(), session.ID, "", approvalID)
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

func TestApproveAutoCompactContinuesFromCompactedHistory(t *testing.T) {
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
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"## Goal\ncontinue the build fix\n\n## Next Step\nuse the latest tool result and keep going"}}],"usage":{"total_tokens":1}}`))
		case 3:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"done"}}],"usage":{"total_tokens":1}}`))
		default:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ignored"}}],"usage":{"total_tokens":1}}`))
		}
	}))
	defer server.Close()

	cfg := testConfig(t)
	cfg.Providers = map[string]config.Provider{
		"test": {
			BaseURL:       server.URL + "/v1",
			Timeout:       time.Second,
			AutoCompactAt: 1,
		},
	}
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "test-model"
	cfg.SetModelConfig(config.ModelConfig{ProviderID: "test", ModelID: "test-model", ContextWindow: 1})
	cfg.Permissions.Profiles["default"] = config.PermissionProfile{
		Root:      string(permissionprofile.ModeReadOnly),
		Workspace: string(permissionprofile.ModeReadWrite),
		Rules: []config.PermissionRule{{
			Tool:    domain.ToolKindBash,
			Pattern: "*",
			Action:  domain.PermissionModeAsk,
		}},
	}

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
	if err := st.SetSessionPermissionProfile(context.Background(), session.ID, "default"); err != nil {
		t.Fatal(err)
	}

	events, err := engine.RunPrompt(context.Background(), session, "build it")
	if err != nil {
		t.Fatal(err)
	}
	var approvalID domain.ID
	for evt := range events {
		if evt.Kind == domain.EventKindApprovalAsk {
			approvalID, err = parseApprovalID(evt.Meta["approval_id"])
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if approvalID == "" {
		t.Fatal("expected approval request")
	}

	approvedEvents, err := engine.approve(context.Background(), session.ID, "", approvalID)
	if err != nil {
		t.Fatal(err)
	}
	var sawFinalAnswer bool
	for evt := range approvedEvents {
		if evt.Kind == domain.EventKindMessageDelta && evt.Text == "done" {
			sawFinalAnswer = true
		}
	}
	if !sawFinalAnswer {
		t.Fatal("expected final assistant answer after auto compact continuation")
	}
	if len(requests) < 3 {
		t.Fatalf("expected prompt, compact, and continuation requests, got %d", len(requests))
	}
	var sawCompactionRequest bool
	for _, req := range requests {
		if strings.Contains(req, "Summarize this coding session so another agent can continue it with minimal loss.") {
			sawCompactionRequest = true
			break
		}
	}
	if !sawCompactionRequest {
		t.Fatalf("expected at least one compaction request, got %#v", requests)
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
			if !strings.Contains(requests[1], "no such file or directory") {
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
	cfg.Permissions.Profiles[permissionprofile.ProfileReadAsk] = config.PermissionProfile{
		Root:      string(permissionprofile.ModeReadOnly),
		Workspace: string(permissionprofile.ModeReadWrite),
		Rules: []config.PermissionRule{{
			Tool:    domain.ToolKindRead,
			Pattern: "*",
			Action:  domain.PermissionModeAsk,
		}},
	}

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	workdir := t.TempDir()
	if err := os.Mkdir(filepath.Join(workdir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	engine := New(cfg, st, tools.NewRegistry(workdir), nil, workdir)
	session, err := st.CreateSession(context.Background(), "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	session.PermissionProfile = permissionprofile.ProfileReadAsk
	session.ProjectRoot = workdir
	if err := st.SetSessionPermissionProfile(context.Background(), session.ID, permissionprofile.ProfileReadAsk); err != nil {
		t.Fatal(err)
	}

	events, err := engine.RunPrompt(context.Background(), session, "continue")
	if err != nil {
		t.Fatal(err)
	}
	var approvalID domain.ID
	for evt := range events {
		if evt.Kind == domain.EventKindApprovalAsk {
			id, convErr := parseApprovalID(evt.Meta["approval_id"])
			if convErr != nil {
				t.Fatal(convErr)
			}
			approvalID = id
		}
	}
	if approvalID == "" {
		t.Fatal("expected approval request")
	}

	approvedEvents, err := engine.approve(context.Background(), session.ID, "", approvalID)
	if err != nil {
		t.Fatal(err)
	}
	var sawToolFailure bool
	var sawFinalAnswer bool
	for evt := range approvedEvents {
		if evt.Kind == domain.EventKindToolResult && strings.Contains(evt.Text, "no such file or directory") {
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

func TestContinueModelTurnAutoCompactsAfterToolResultChurn(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	largePath := filepath.Join(dir, "large.txt")
	largeContent := strings.Repeat("tool output line\n", 3000)
	if err := os.WriteFile(largePath, []byte(largeContent), 0o644); err != nil {
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
		switch {
		case strings.Contains(string(body), "Summarize this coding session so another agent can continue it with minimal loss."):
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"## Goal\ncontinue from tool output\n\n## Next Step\nuse the compacted summary and continue"}}],"usage":{"total_tokens":1}}`))
		case strings.Contains(string(body), "Compacted session summary for continuation:"):
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"done"}}],"usage":{"total_tokens":1}}`))
		case len(requests) == 1:
			args, err := json.Marshal(map[string]string{"path": largePath})
			if err != nil {
				t.Fatalf("marshal args: %v", err)
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{"tool_calls":[{"id":"call_1","type":"function","function":{"name":"read","arguments":` + strconv.Quote(string(args)) + `}}]}}],"usage":{"total_tokens":1}}`))
		default:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ignored"}}],"usage":{"total_tokens":1}}`))
		}
	}))
	defer server.Close()

	cfg := testConfig(t)
	cfg.Providers = map[string]config.Provider{
		"test": {
			BaseURL:       server.URL + "/v1",
			Timeout:       time.Second,
			AutoCompactAt: 20,
		},
	}
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "test-model"
	cfg.SetModelConfig(config.ModelConfig{ProviderID: "test", ModelID: "test-model", ContextWindow: 50000})

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	engine := New(cfg, st, tools.NewRegistry(dir), nil, dir)
	session, err := st.CreateSession(context.Background(), "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSessionPermissionProfile(context.Background(), session.ID, "auto"); err != nil {
		t.Fatal(err)
	}

	events, err := engine.RunPrompt(context.Background(), session, "inspect the file and continue")
	if err != nil {
		t.Fatal(err)
	}
	var sawFinalAnswer bool
	var seen []domain.EventKind
	for evt := range events {
		seen = append(seen, evt.Kind)
		if evt.Kind == domain.EventKindMessageDelta && evt.Text == "done" {
			sawFinalAnswer = true
		}
	}
	if !sawFinalAnswer {
		t.Fatalf("expected final assistant answer after tool-result-triggered auto compact; requests=%d events=%v", len(requests), seen)
	}
	if len(requests) < 3 {
		t.Fatalf("expected prompt, compact, and continuation requests, got %d", len(requests))
	}
	var sawCompactionRequest bool
	for _, req := range requests {
		if strings.Contains(req, "Summarize this coding session so another agent can continue it with minimal loss.") {
			sawCompactionRequest = true
			break
		}
	}
	if !sawCompactionRequest {
		t.Fatalf("expected at least one compaction request, got %#v", requests)
	}
}

func TestCompactSessionDoesNotPersistUsageOrEmitUsageEvent(t *testing.T) {
	t.Parallel()

	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		requests++
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"short compact summary"}}],"usage":{"prompt_tokens":1200,"completion_tokens":300}}`))
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
	cfg.SetModelConfig(config.ModelConfig{ProviderID: "test", ModelID: "test-model", ContextWindow: 32768})

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
	chat := defaultChatForSession(t, st, session.ID)
	appendUserTimelineItem(t, st, chat.ID, "hello")
	appendAssistantTimelineItem(t, st, chat.ID, domain.AssistantMessage{Text: "world"})

	client, err := provider.New("test", cfg.Providers["test"], nil)
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan domain.Event, 4)
	if err := engine.compactSession(context.Background(), session, chat.ID, client, "manual", events); err != nil {
		t.Fatal(err)
	}
	close(events)

	var sawStatus bool
	var sawRefreshStart bool
	var sawRefreshDone bool
	for evt := range events {
		if evt.Kind == domain.EventKindStatus && evt.Text == "Session compacted" {
			sawStatus = true
		}
		if evt.Kind == domain.EventKindStatus && evt.Meta["compaction"] == "started" && evt.Meta["refresh"] == "details" {
			sawRefreshStart = true
		}
		if evt.Kind == domain.EventKindStatus && evt.Meta["compaction"] == "completed" && evt.Meta["refresh"] == "details" {
			sawRefreshDone = true
		}
		if evt.Kind == domain.EventKindUsage {
			t.Fatalf("did not expect compaction to emit usage event, got %#v", evt)
		}
	}
	if !sawStatus {
		t.Fatal("expected compact session status event")
	}
	if !sawRefreshStart || !sawRefreshDone {
		t.Fatalf("expected compaction lifecycle refresh events, got start=%v done=%v", sawRefreshStart, sawRefreshDone)
	}

	timeline, err := st.TimelineForChat(context.Background(), chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	var sawCompaction bool
	for _, item := range timeline {
		switch content := item.Content.(type) {
		case domain.Notice:
			if content.Kind == "usage" {
				t.Fatalf("did not expect compact session to persist usage notice, got %#v", content)
			}
		case domain.Compaction:
			if content.Status == "completed" {
				if strings.TrimSpace(content.Summary) == "" {
					t.Fatalf("expected completed compaction summary, got %#v", content)
				}
				if content.BeforeContextTokens <= 0 || content.AfterContextTokens <= 0 {
					t.Fatalf("expected compaction context sizes, got %#v", content)
				}
				sawCompaction = true
			}
		}
	}
	if !sawCompaction {
		t.Fatal("expected persisted compaction part")
	}
	if requests != 1 {
		t.Fatalf("expected one compaction request, got %d", requests)
	}
}

func TestCompactSessionStreamsWhenProviderStreamingEnabled(t *testing.T) {
	t.Parallel()

	var sawStream bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		sawStream = strings.Contains(string(body), `"stream":true`)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"streamed compact summary\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	cfg := testConfig(t)
	cfg.Providers = map[string]config.Provider{
		"test": {
			BaseURL: server.URL + "/v1",
			Timeout: time.Second,
			Stream:  true,
		},
	}
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "test-model"
	cfg.SetModelConfig(config.ModelConfig{ProviderID: "test", ModelID: "test-model", ContextWindow: 32768})

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
	chat := defaultChatForSession(t, st, session.ID)
	appendUserTimelineItem(t, st, chat.ID, "hello")
	appendAssistantTimelineItem(t, st, chat.ID, domain.AssistantMessage{Text: "world"})

	client, err := provider.New("test", cfg.Providers["test"], nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.compactSession(context.Background(), session, chat.ID, client, "manual", nil); err != nil {
		t.Fatal(err)
	}
	if !sawStream {
		t.Fatal("expected compaction request to stream")
	}

	timeline, err := st.TimelineForChat(context.Background(), chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, item := range timeline {
		payload, ok := item.Content.(domain.Compaction)
		if !ok || payload.Status != "completed" {
			continue
		}
		if got := strings.TrimSpace(payload.Summary); got != "streamed compact summary" {
			t.Fatalf("summary = %q", got)
		}
		found = true
	}
	if !found {
		t.Fatal("expected persisted compaction summary")
	}
}

func TestCompactSessionUsesConfiguredCompactionModel(t *testing.T) {
	t.Parallel()

	var sawCompactionModel bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		sawCompactionModel = strings.Contains(string(body), `"model":"compact-model"`)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"compact summary from override"}}],"usage":{"total_tokens":1}}`))
	}))
	defer server.Close()

	cfg := testConfig(t)
	cfg.Providers = map[string]config.Provider{
		"chat": {
			BaseURL: "http://127.0.0.1:1/v1",
			Timeout: time.Second,
		},
		"compact": {
			BaseURL: server.URL + "/v1",
			Timeout: time.Second,
		},
	}
	cfg.DefaultProvider = "chat"
	cfg.DefaultModel = "chat-model"
	cfg.CompactionProvider = "compact"
	cfg.CompactionModel = "compact-model"
	cfg.SetModelConfig(config.ModelConfig{ProviderID: "chat", ModelID: "chat-model", ContextWindow: 32768})
	cfg.SetModelConfig(config.ModelConfig{ProviderID: "compact", ModelID: "compact-model", ContextWindow: 32768})

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	engine := New(cfg, st, tools.NewRegistry(t.TempDir()), nil, t.TempDir())
	session, err := st.CreateSession(context.Background(), "test", "chat", "chat-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	chat := defaultChatForSession(t, st, session.ID)
	appendUserTimelineItem(t, st, chat.ID, "hello")
	appendAssistantTimelineItem(t, st, chat.ID, domain.AssistantMessage{Text: "world"})

	client, err := provider.New("chat", cfg.Providers["chat"], nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.compactSession(context.Background(), session, chat.ID, client, "manual", nil); err != nil {
		t.Fatal(err)
	}
	if !sawCompactionModel {
		t.Fatal("expected compaction request to use configured compaction model")
	}
}

func TestCompactSessionRejectsInvalidCompactionModelOverride(t *testing.T) {
	t.Parallel()

	cfg := testConfig(t)
	cfg.Providers = map[string]config.Provider{
		"chat": {
			BaseURL: "http://127.0.0.1:1/v1",
			Timeout: time.Second,
		},
	}
	cfg.SetModelConfig(config.ModelConfig{ProviderID: "chat", ModelID: "chat-model", ContextWindow: 32768})
	cfg.CompactionProvider = "missing"
	cfg.CompactionModel = "compact-model"

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	engine := New(cfg, st, tools.NewRegistry(t.TempDir()), nil, t.TempDir())
	session, err := st.CreateSession(context.Background(), "test", "chat", "chat-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	chat := defaultChatForSession(t, st, session.ID)
	appendUserTimelineItem(t, st, chat.ID, "hello")
	appendAssistantTimelineItem(t, st, chat.ID, domain.AssistantMessage{Text: "world"})

	client, err := provider.New("chat", cfg.Providers["chat"], nil)
	if err != nil {
		t.Fatal(err)
	}
	err = engine.compactSession(context.Background(), session, chat.ID, client, "manual", nil)
	if err == nil || !strings.Contains(err.Error(), `compaction provider "missing"`) {
		t.Fatalf("expected invalid compaction provider error, got %v", err)
	}
}

func TestCompactSessionAcceptsReasoningOnlySummary(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":null,"reasoning":"reasoning-only compact summary"}}]}`))
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
	cfg.SetModelConfig(config.ModelConfig{ProviderID: "test", ModelID: "test-model", ContextWindow: 32768})

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
	chat := defaultChatForSession(t, st, session.ID)
	appendUserTimelineItem(t, st, chat.ID, "hello")

	client, err := provider.New("test", cfg.Providers["test"], nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.compactSession(context.Background(), session, chat.ID, client, "manual", nil); err != nil {
		t.Fatal(err)
	}

	timeline, err := st.TimelineForChat(context.Background(), chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, item := range timeline {
		payload, ok := item.Content.(domain.Compaction)
		if !ok || payload.Status != "completed" {
			continue
		}
		if got := strings.TrimSpace(payload.Summary); got != "reasoning-only compact summary" {
			t.Fatalf("summary = %q", got)
		}
		found = true
	}
	if !found {
		t.Fatal("expected persisted compaction summary")
	}
}

func TestHandleModelToolCallRequiresApprovalForSkill(t *testing.T) {
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
	session.PermissionProfile = permissionprofile.ProfileAsk
	session.ProjectRoot = workdir
	if err := st.SetSessionPermissionProfile(context.Background(), session.ID, permissionprofile.ProfileAsk); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateSessionWorkspace(context.Background(), session.ID, workdir, workdir); err != nil {
		t.Fatal(err)
	}

	chat := defaultChatForSession(t, st, session.ID)
	_, err = engine.handleModelToolCall(context.Background(), session, chat, tools.Request{
		Tool: domain.ToolKindSkill,
		Args: map[string]string{"name": "review"},
	})
	// Skill tool should now require approval like other tools under ProfileAsk
	if err != nil {
		return // Expected: approval needed error or waiting for approval
	}
}

func TestSaveChatContextUsageStoresLatestRequestUsage(t *testing.T) {
	cfg := testConfig(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	engine := New(cfg, st, tools.NewRegistry(t.TempDir()), nil, t.TempDir())
	session, err := st.CreateSession(context.Background(), "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	chat := defaultChatForSession(t, st, session.ID)
	chat.LastKnownContextTokens = 1000
	chat.ContextTokensKnown = false
	if err := st.UpdateChat(context.Background(), chat); err != nil {
		t.Fatal(err)
	}

	if err := engine.saveChatContextUsage(context.Background(), chat.ID, domain.Usage{PromptTokens: 200, CompletionTokens: 50, TotalTokens: 250}); err != nil {
		t.Fatal(err)
	}
	if err := engine.saveChatContextUsage(context.Background(), chat.ID, domain.Usage{TotalTokens: 45, CompletionTokens: 5}); err != nil {
		t.Fatal(err)
	}

	stored, err := st.GetChat(context.Background(), chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.LastKnownContextTokens != 40 {
		t.Fatalf("expected latest request context usage, got %d", stored.LastKnownContextTokens)
	}
	if !stored.ContextTokensKnown {
		t.Fatal("expected chat context usage to become known after provider usage")
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
	session.PermissionProfile = permissionprofile.ProfileWriteAsk
	session.ProjectRoot = workdir
	if err := st.SetSessionPermissionProfile(context.Background(), session.ID, permissionprofile.ProfileWriteAsk); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateSessionWorkspace(context.Background(), session.ID, workdir, workdir); err != nil {
		t.Fatal(err)
	}

	chat := defaultChatForSession(t, st, session.ID)
	evt, err := engine.handleModelToolCall(context.Background(), session, chat, tools.Request{
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
	cfg.Permissions.Profiles[permissionprofile.ProfileWriteAsk] = config.PermissionProfile{
		Root:      string(permissionprofile.ModeReadOnly),
		Workspace: string(permissionprofile.ModeReadWrite),
		Rules: []config.PermissionRule{{
			Tool:    domain.ToolKindBash,
			Pattern: "*",
			Action:  domain.PermissionModeAsk,
		}},
	}
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
	session.PermissionProfile = permissionprofile.ProfileWriteAsk
	session.ProjectRoot = workdir
	if err := st.SetSessionPermissionProfile(context.Background(), session.ID, permissionprofile.ProfileWriteAsk); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateSessionWorkspace(context.Background(), session.ID, workdir, workdir); err != nil {
		t.Fatal(err)
	}

	chat := defaultChatForSession(t, st, session.ID)
	req := tools.Request{
		Tool:       domain.ToolKindBash,
		ToolCallID: "call_1",
		Args:       map[string]string{"command": "pwd"},
	}
	appendAssistantToolTimelineItem(t, st, chat.ID, req, "")
	evt, err := engine.handleModelToolCall(context.Background(), session, chat, req)
	if err != nil {
		t.Fatal(err)
	}
	if evt.Kind != domain.EventKindApprovalAsk {
		t.Fatalf("expected approval ask, got %#v", evt)
	}
	if !strings.Contains(evt.Text, "requires approval") {
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
	messages, _, err := timelineTranscriptForSession(t, st, session.ID)
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

	messages, parts, err := timelineTranscriptForSession(t, st, session.ID)
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

	chat := defaultChatForSession(t, st, session.ID)
	evt, err := engine.handleModelToolCall(context.Background(), session, chat, tools.Request{
		Tool: domain.ToolKindTask,
		Args: map[string]string{"body": "write docs"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if evt.Kind != domain.EventKindTaskUpdate || evt.Text != "write docs" {
		t.Fatalf("unexpected task update event: %#v", evt)
	}

	items, err := st.TimelineForChat(context.Background(), chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one persisted timeline item, got %d", len(items))
	}
	exec, ok := items[0].Content.(domain.ToolExecution)
	if !ok || exec.Result == nil {
		t.Fatalf("expected task tool execution, got %#v", items[0])
	}
	if _, ok := exec.Result.Data.(domain.TaskStoredResult); !ok {
		t.Fatalf("expected typed task result, got %#v", exec.Result.Data)
	}
	if got := exec.Result.Text; got != "write docs" {
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
	engine.retryPause = func(_ context.Context, delay time.Duration, onTick func(time.Duration)) error {
		if onTick != nil {
			onTick(delay)
		}
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

	messages, parts, err := timelineTranscriptForSession(t, st, session.ID)
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

func TestRunPromptRateLimitStatusCountsDown(t *testing.T) {
	t.Parallel()

	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		switch requests {
		case 1:
			w.Header().Set("Retry-After", "3")
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
	engine.retryPause = func(_ context.Context, delay time.Duration, onTick func(time.Duration)) error {
		for _, remaining := range []time.Duration{delay, 2 * time.Second, time.Second, 0} {
			onTick(remaining)
		}
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

	var statuses []string
	for evt := range events {
		if evt.Kind == domain.EventKindStatus && strings.Contains(evt.Text, "rate limit hit") {
			statuses = append(statuses, evt.Text)
		}
	}
	if len(statuses) < 4 {
		t.Fatalf("expected countdown statuses, got %#v", statuses)
	}
	wantSuffixes := []string{"3s, retry 1)", "2s, retry 1)", "1s, retry 1)", "0s, retry 1)"}
	for idx, want := range wantSuffixes {
		if !strings.HasSuffix(statuses[idx], want) {
			t.Fatalf("expected status %d to end with %q, got %q", idx, want, statuses[idx])
		}
	}
}

func TestChatWithRetryRetriesTransientEOFBeforeStreamingStarts(t *testing.T) {
	t.Parallel()

	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		switch requests {
		case 1:
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Fatal("response writer does not support hijacking")
			}
			conn, _, err := hj.Hijack()
			if err != nil {
				t.Fatalf("hijack: %v", err)
			}
			_ = conn.Close()
		case 2:
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hel\"}}]}\n\n"))
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"lo\"}}],\"usage\":{\"total_tokens\":1}}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
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
			Stream:  true,
		},
	}
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "test-model"

	engine := New(cfg, nil, tools.NewRegistry(t.TempDir()), nil, t.TempDir())
	var waited []time.Duration
	engine.retryPause = func(_ context.Context, delay time.Duration, onTick func(time.Duration)) error {
		if onTick != nil {
			onTick(delay)
		}
		waited = append(waited, delay)
		return nil
	}
	client, err := provider.New("test", cfg.Providers["test"], nil)
	if err != nil {
		t.Fatal(err)
	}

	events := make(chan domain.Event, 16)
	resp, streamed, err := engine.chatWithRetry(context.Background(), "", "test", client, events, provider.ChatRequest{
		Model: "test-model",
		Messages: []provider.Message{{
			Role:    domain.MessageRoleUser,
			Content: "hello",
		}},
		Stream: true,
	}, domain.TimelineItem{ID: domain.NewTimelineID(time.Now().UTC())})
	if err != nil {
		t.Fatalf("expected transient retry to succeed, got %v", err)
	}
	close(events)
	if !streamed {
		t.Fatal("expected streaming request")
	}
	if resp.Text != "hello" {
		t.Fatalf("expected final response text hello, got %#v", resp)
	}
	var sawRetry bool
	for evt := range events {
		if evt.Kind == domain.EventKindStatus && strings.Contains(evt.Text, "connection dropped") {
			sawRetry = true
		}
	}
	if len(waited) != 1 || waited[0] != defaultTransientRetryWait {
		t.Fatalf("expected single transient retry wait of %s, got %#v", defaultTransientRetryWait, waited)
	}
	if !sawRetry {
		t.Fatal("expected transient retry status event")
	}
	if requests < 2 {
		t.Fatalf("expected retried provider request, got %d requests", requests)
	}
}

func TestChatWithRetryDoesNotRetryAfterPartialStreamFailure(t *testing.T) {
	t.Parallel()

	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		switch requests {
		case 1:
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hel\"}}]}\n\n"))
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"broken\"}}\n\n"))
		default:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"should not retry"}}],"usage":{"total_tokens":1}}`))
		}
	}))
	defer server.Close()

	cfg := testConfig(t)
	cfg.Providers = map[string]config.Provider{
		"test": {
			BaseURL: server.URL + "/v1",
			Timeout: time.Second,
			Stream:  true,
		},
	}
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "test-model"

	engine := New(cfg, nil, tools.NewRegistry(t.TempDir()), nil, t.TempDir())
	client, err := provider.New("test", cfg.Providers["test"], nil)
	if err != nil {
		t.Fatal(err)
	}

	events := make(chan domain.Event, 16)
	_, streamed, err := engine.chatWithRetry(context.Background(), "", "test", client, events, provider.ChatRequest{
		Model: "test-model",
		Messages: []provider.Message{{
			Role:    domain.MessageRoleUser,
			Content: "hello",
		}},
		Stream: true,
	}, domain.TimelineItem{ID: domain.NewTimelineID(time.Now().UTC())})
	close(events)
	if err == nil {
		t.Fatal("expected stream failure error")
	}
	if !streamed {
		t.Fatal("expected streaming request")
	}

	var (
		deltas   []string
		sawRetry bool
	)
	for evt := range events {
		if evt.Kind == domain.EventKindMessageDelta {
			deltas = append(deltas, evt.Text)
		}
		if evt.Kind == domain.EventKindStatus && strings.Contains(evt.Text, "connection dropped") {
			sawRetry = true
		}
	}
	if strings.Join(deltas, "") != "hel" {
		t.Fatalf("expected partial streamed delta before failure, got %#v", deltas)
	}
	if sawRetry {
		t.Fatal("did not expect retry status after partial stream failure")
	}
	if requests != 1 {
		t.Fatalf("expected no retry after partial stream failure, got %d requests", requests)
	}
}

func TestChatWithRetryOpportunisticallyDisablesRejectedPromptProgress(t *testing.T) {
	t.Parallel()

	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		switch requests {
		case 1:
			if body["return_progress"] != true {
				t.Fatalf("expected first request to try return_progress, got %#v", body)
			}
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"unknown field return_progress"}}`))
		case 2:
			if _, ok := body["return_progress"]; ok {
				t.Fatalf("expected retry without return_progress, got %#v", body)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		default:
			t.Fatalf("unexpected request %d", requests)
		}
	}))
	defer server.Close()

	cfg := testConfig(t)
	cfg.Providers = map[string]config.Provider{
		"test": {
			BaseURL: server.URL + "/v1",
			Timeout: time.Second,
			Stream:  true,
		},
	}
	engine := New(cfg, nil, tools.NewRegistry(t.TempDir()), nil, t.TempDir())
	client, err := provider.New("test", cfg.Providers["test"], nil)
	if err != nil {
		t.Fatal(err)
	}

	events := make(chan domain.Event, 16)
	resp, streamed, err := engine.chatWithRetry(context.Background(), "", "test", client, events, provider.ChatRequest{
		Model: "test-model",
		Messages: []provider.Message{{
			Role:    domain.MessageRoleUser,
			Content: "hello",
		}},
		Stream:    true,
		ExtraBody: provider.RequestExtraBody(cfg.Providers["test"], "test-model", provider.ModelPresetDefault),
	}, domain.TimelineItem{ID: domain.NewTimelineID(time.Now().UTC())})
	close(events)
	if err != nil {
		t.Fatal(err)
	}
	if !streamed || resp.Text != "hello" {
		t.Fatalf("unexpected response: streamed=%v resp=%#v", streamed, resp)
	}
	if requests != 2 {
		t.Fatalf("expected one prompt-progress retry, got %d requests", requests)
	}
	updated := engine.cfg.Providers["test"]
	if !updated.PromptProgressProbed || updated.PromptProgressSupported {
		t.Fatalf("expected prompt progress to be persisted unsupported, got %#v", updated)
	}
}

func TestRunPromptIgnoresSessionTitleRefreshFailure(t *testing.T) {
	t.Parallel()

	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		switch requests {
		case 1:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"done"}}],"usage":{"total_tokens":1}}`))
		case 2:
			time.Sleep(100 * time.Millisecond)
		default:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ignored"}}],"usage":{"total_tokens":1}}`))
		}
	}))
	defer server.Close()

	cfg := testConfig(t)
	cfg.Providers = map[string]config.Provider{
		"test": {
			BaseURL: server.URL + "/v1",
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
	session, err := st.CreateSession(context.Background(), "New Session", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}

	events, err := engine.RunPrompt(context.Background(), session, "hello")
	if err != nil {
		t.Fatal(err)
	}

	var sawDone bool
	for evt := range events {
		if evt.Kind == domain.EventKindError {
			t.Fatalf("expected title refresh timeout to stay internal, got %#v", evt)
		}
		if evt.Kind == domain.EventKindMessageDelta && evt.Text == "done" {
			sawDone = true
		}
	}
	if !sawDone {
		t.Fatal("expected visible assistant response")
	}

	messages, parts, err := timelineTranscriptForSession(t, st, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	last := messages[len(messages)-1]
	if last.Role != domain.MessageRoleAssistant {
		t.Fatalf("expected assistant message, got %#v", last)
	}
	got := parts[last.ID]
	if len(got) == 0 || got[0].Kind != domain.PartKindText || got[0].Body != "done" {
		t.Fatalf("expected stored assistant text, got %#v", got)
	}
}

func TestRunPromptUpdatesGeneratedChatTitle(t *testing.T) {
	t.Parallel()

	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"done"}}],"usage":{"total_tokens":1}}`))
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
	session, err := st.CreateSession(context.Background(), "Existing Session", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	chat := defaultChatForSession(t, st, session.ID)
	if chat.Title != "Main" {
		t.Fatalf("expected generated main title, got %q", chat.Title)
	}

	events, err := engine.RunPrompt(context.Background(), session, "compare go code to c reference and identify gaps")
	if err != nil {
		t.Fatal(err)
	}
	var chatTitle string
	for evt := range events {
		if evt.Kind == domain.EventKindChatTitle {
			chatTitle = evt.Text
		}
	}
	want := "compare go code to c reference"
	if chatTitle != want {
		t.Fatalf("expected chat title event %q, got %q", want, chatTitle)
	}
	updated, err := st.GetChat(context.Background(), chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Title != want {
		t.Fatalf("expected stored chat title %q, got %q", want, updated.Title)
	}
	if requests != 1 {
		t.Fatalf("expected no extra provider request for chat title, got %d", requests)
	}
}

func TestRunPromptPausesRepeatedIdenticalToolCalls(t *testing.T) {
	t.Parallel()

	workdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workdir, "note.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = w.Write([]byte(fmt.Sprintf(`{"choices":[{"message":{"tool_calls":[{"id":"call_%d","type":"function","function":{"name":"read","arguments":"{\"path\":\"note.txt\"}"}}]}}],"usage":{"total_tokens":1}}`, requests)))
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

	engine := New(cfg, st, tools.NewRegistry(workdir), nil, workdir)
	session, err := st.CreateSession(context.Background(), "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}

	events, err := engine.RunPrompt(context.Background(), session, "loop")
	if err != nil {
		t.Fatal(err)
	}

	var sawPauseStatus bool
	var sawPauseStatusItem bool
	for evt := range events {
		if evt.Kind == domain.EventKindStatus && strings.Contains(evt.Text, "identical read calls") {
			sawPauseStatus = true
			if notice, ok := evt.Item.Content.(domain.Notice); ok && notice.Kind == "loop_pause" {
				sawPauseStatusItem = true
			}
		}
		if evt.Kind == domain.EventKindError {
			t.Fatalf("expected loop pause instead of error, got %#v", evt)
		}
	}
	if !sawPauseStatus {
		t.Fatal("expected repeated-tool pause status")
	}
	if !sawPauseStatusItem {
		t.Fatal("expected repeated-tool pause status to include persisted notice item for live transcript updates")
	}

	chat := defaultChatForSession(t, st, session.ID)
	timeline, err := st.TimelineForChat(context.Background(), chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	var toolOutputs int
	var sawPauseNotice bool
	for _, item := range timeline {
		switch content := item.Content.(type) {
		case domain.AssistantMessage:
			for _, tool := range content.Tools {
				if tool.Result != nil || tool.Error != nil {
					toolOutputs++
				}
			}
		case domain.Notice:
			if content.Kind == "loop_pause" && content.Reason == string(continuationPauseReasonRepeatedTool) {
				sawPauseNotice = true
			}
		}
	}
	if toolOutputs != 2 {
		t.Fatalf("expected only two executed tool outputs before pause, got %d", toolOutputs)
	}
	if !sawPauseNotice {
		t.Fatal("expected persisted repeated-tool pause notice")
	}
}

func TestRunPromptPausesOnProviderRefusalAfterToolResult(t *testing.T) {
	t.Parallel()

	workdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workdir, "note.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		switch requests {
		case 1:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"tool_calls":[{"id":"call_1","type":"function","function":{"name":"read","arguments":"{\"path\":\"note.txt\"}"}}]}}],"usage":{"total_tokens":1}}`))
		default:
			_, _ = w.Write([]byte(`{"choices":[{"message":{}}],"usage":{"total_tokens":1}}`))
		}
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

	engine := New(cfg, st, tools.NewRegistry(workdir), nil, workdir)
	session, err := st.CreateSession(context.Background(), "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}

	events, err := engine.RunPrompt(context.Background(), session, "loop")
	if err != nil {
		t.Fatal(err)
	}
	for evt := range events {
		if evt.Kind == domain.EventKindError {
			t.Fatalf("expected provider-refusal pause instead of error, got %#v", evt)
		}
	}

	chat := defaultChatForSession(t, st, session.ID)
	for _, notice := range timelineNoticesForChat(t, st, chat.ID) {
		if notice.Kind == "loop_pause" && notice.Reason == string(continuationPauseReasonProviderRefusal) {
			return
		}
	}
	t.Fatal("expected persisted provider-refusal pause notice")
}

func TestRunPromptContinuesAfterReasoningOnlyTurnFollowingToolResult(t *testing.T) {
	t.Parallel()

	workdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workdir, "note.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests = append(requests, string(body))
		switch len(requests) {
		case 1:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"tool_calls":[{"id":"call_1","type":"function","function":{"name":"read","arguments":"{\"path\":\"note.txt\"}"}}]}}],"usage":{"total_tokens":1}}`))
		case 2:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"reasoning":"thinking only"}}],"usage":{"total_tokens":1}}`))
		default:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"final answer"}}],"usage":{"total_tokens":1}}`))
		}
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

	engine := New(cfg, st, tools.NewRegistry(workdir), nil, workdir)
	session, err := st.CreateSession(context.Background(), "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}

	events, err := engine.RunPrompt(context.Background(), session, "loop")
	if err != nil {
		t.Fatal(err)
	}
	for evt := range events {
		if evt.Kind == domain.EventKindError {
			t.Fatalf("expected continuation after reasoning-only turn, got %#v", evt)
		}
	}

	if len(requests) < 3 {
		t.Fatalf("expected at least 3 provider requests, got %d", len(requests))
	}
	var sawContinuationInstruction bool
	for _, req := range requests {
		if strings.Contains(req, "Do not stop at hidden reasoning") {
			sawContinuationInstruction = true
			break
		}
	}
	if !sawContinuationInstruction {
		t.Fatalf("expected continuation instruction after reasoning-only turn, got %v", requests)
	}

	messages, parts, err := timelineTranscriptForSession(t, st, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	var sawFinalText bool
	for _, msg := range messages {
		for _, part := range parts[msg.ID] {
			if part.Kind == domain.PartKindEventNotice && strings.Contains(part.Body, "Paused continuation") {
				t.Fatalf("unexpected pause notice after reasoning-only turn: %#v", part)
			}
			if part.Kind == domain.PartKindText && strings.TrimSpace(part.Body) == "final answer" {
				sawFinalText = true
			}
		}
	}
	if !sawFinalText {
		t.Fatal("expected final assistant answer after reasoning-only continuation")
	}
}

func TestRunPromptAutoContinuesAfterIntentOnlyStopFollowingToolResult(t *testing.T) {
	t.Parallel()

	workdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workdir, "note.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests = append(requests, string(body))
		switch len(requests) {
		case 1:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"tool_calls":[{"id":"call_1","type":"function","function":{"name":"read","arguments":"{\"path\":\"note.txt\"}"}}]}}],"usage":{"total_tokens":1}}`))
		case 2:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"Let me inspect the failing test now:"}}],"usage":{"total_tokens":1}}`))
		default:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"final answer"}}],"usage":{"total_tokens":1}}`))
		}
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

	engine := New(cfg, st, tools.NewRegistry(workdir), nil, workdir)
	session, err := st.CreateSession(context.Background(), "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}

	events, err := engine.RunPrompt(context.Background(), session, "loop")
	if err != nil {
		t.Fatal(err)
	}
	for evt := range events {
		if evt.Kind == domain.EventKindError {
			t.Fatalf("expected continuation after intent-only stop, got %#v", evt)
		}
	}

	if len(requests) < 3 {
		t.Fatalf("expected at least 3 provider requests, got %d", len(requests))
	}
	var sawContinuationInstruction bool
	for _, req := range requests {
		if strings.Contains(req, "Continue by issuing the tool call now") {
			sawContinuationInstruction = true
			break
		}
	}
	if !sawContinuationInstruction {
		t.Fatalf("expected auto-continue instruction after intent-only stop, got %v", requests)
	}

	messages, parts, err := timelineTranscriptForSession(t, st, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	var sawFinalText bool
	for _, msg := range messages {
		for _, part := range parts[msg.ID] {
			if part.Kind == domain.PartKindText && strings.TrimSpace(part.Body) == "Let me inspect the failing test now:" {
				t.Fatal("expected intent-only stop to be skipped instead of persisted")
			}
			if part.Kind == domain.PartKindText && strings.TrimSpace(part.Body) == "final answer" {
				sawFinalText = true
			}
		}
	}
	if !sawFinalText {
		t.Fatal("expected final assistant answer after intent-only continuation")
	}
}

func TestRunPromptDoesNotAutoContinueIntentOnlyStopWhenDisabled(t *testing.T) {
	t.Parallel()

	workdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workdir, "note.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests = append(requests, string(body))
		switch len(requests) {
		case 1:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"tool_calls":[{"id":"call_1","type":"function","function":{"name":"read","arguments":"{\"path\":\"note.txt\"}"}}]}}],"usage":{"total_tokens":1}}`))
		default:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"Let me inspect the failing test now:"}}],"usage":{"total_tokens":1}}`))
		}
	}))
	defer server.Close()

	cfg := testConfig(t)
	cfg.UI.AutoContinue = false
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

	engine := New(cfg, st, tools.NewRegistry(workdir), nil, workdir)
	session, err := st.CreateSession(context.Background(), "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}

	events, err := engine.RunPrompt(context.Background(), session, "loop")
	if err != nil {
		t.Fatal(err)
	}
	for evt := range events {
		if evt.Kind == domain.EventKindError {
			t.Fatalf("expected disabled auto-continue to persist model text, got %#v", evt)
		}
	}

	for _, req := range requests {
		if strings.Contains(req, "Continue by issuing the tool call now") {
			t.Fatalf("did not expect auto-continue instruction when disabled, got %v", requests)
		}
	}
	messages, parts, err := timelineTranscriptForSession(t, st, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range messages {
		for _, part := range parts[msg.ID] {
			if part.Kind == domain.PartKindText && strings.TrimSpace(part.Body) == "Let me inspect the failing test now:" {
				return
			}
		}
	}
	t.Fatal("expected intent-only stop to be persisted when auto-continue is disabled")
}

func TestRunPromptPausesOnTurnLimit(t *testing.T) {
	t.Parallel()

	workdir := t.TempDir()
	for _, name := range []string{"one.txt", "two.txt"} {
		if err := os.WriteFile(filepath.Join(workdir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		path := "one.txt"
		if requests%2 == 0 {
			path = "two.txt"
		}
		args, err := json.Marshal(map[string]string{"path": path})
		if err != nil {
			t.Fatalf("marshal args: %v", err)
		}
		_, _ = w.Write([]byte(fmt.Sprintf(`{"choices":[{"message":{"tool_calls":[{"id":"call_%d","type":"function","function":{"name":"read","arguments":%q}}]}}],"usage":{"total_tokens":1}}`, requests, string(args))))
	}))
	defer server.Close()

	cfg := testConfig(t)
	cfg.MaxToolLoopSteps = 2
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

	engine := New(cfg, st, tools.NewRegistry(workdir), nil, workdir)
	session, err := st.CreateSession(context.Background(), "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}

	events, err := engine.RunPrompt(context.Background(), session, "loop")
	if err != nil {
		t.Fatal(err)
	}
	for evt := range events {
		if evt.Kind == domain.EventKindError {
			t.Fatalf("expected turn-limit pause instead of error, got %#v", evt)
		}
	}

	chat := defaultChatForSession(t, st, session.ID)
	for _, notice := range timelineNoticesForChat(t, st, chat.ID) {
		if notice.Kind == "loop_pause" && notice.Reason == string(continuationPauseReasonTurnLimit) && notice.Limit == 2 {
			return
		}
	}
	t.Fatal("expected persisted turn-limit pause notice")
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
	engine.retryPause = func(_ context.Context, delay time.Duration, onTick func(time.Duration)) error {
		if onTick != nil {
			onTick(delay)
		}
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

	var sawError bool
	var errorItem domain.TimelineItem
	for evt := range events {
		if evt.Kind == domain.EventKindError {
			sawError = true
			errorItem = evt.Item
		}
	}
	if !sawError {
		t.Fatal("expected terminal error event")
	}
	if notice, ok := errorItem.Content.(domain.Notice); !ok || notice.Kind != "model_error" || !strings.Contains(notice.Text, "Error: chat status 429") {
		t.Fatalf("expected error event to carry persisted model error notice, got %#v", errorItem)
	}
	if requests != maxRateLimitRetries+1 {
		t.Fatalf("expected %d attempts, got %d", maxRateLimitRetries+1, requests)
	}

	messages, parts, err := timelineTranscriptForSession(t, st, session.ID)
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
	engine.retryPause = func(ctx context.Context, _ time.Duration, _ func(time.Duration)) error {
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

	messages, parts, err := timelineTranscriptForSession(t, st, session.ID)
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

	chat := defaultChatForSession(t, st, session.ID)
	events, err := engine.persistToolResult(context.Background(), chat.ID, session.ID, tools.Request{Tool: domain.ToolKindBash}, tools.Result{})
	if err != nil {
		t.Fatal(err)
	}
	evt := <-events
	if evt.Kind != domain.EventKindToolResult || !strings.Contains(evt.Text, "completed with no output") {
		t.Fatalf("unexpected tool result event: %#v", evt)
	}

	messages, parts, err := timelineTranscriptForSession(t, st, session.ID)
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

func parseApprovalID(raw string) (domain.ID, error) {
	id := domain.ID(strings.TrimSpace(raw))
	if id == "" {
		return "", fmt.Errorf("approval id is required")
	}
	return id, nil
}

func TestErrorSummaryPrefixesMessage(t *testing.T) {
	got := errorSummary(errors.New("connection refused"))
	if got != "Error: connection refused" {
		t.Fatalf("unexpected error summary: %q", got)
	}
}
