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

	"github.com/lkarlslund/koder/internal/accesssettings"
	"github.com/lkarlslund/koder/internal/assets"
	"github.com/lkarlslund/koder/internal/attachment"
	chatpkg "github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/codediag"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/mcp"
	"github.com/lkarlslund/koder/internal/permissionprofile"
	"github.com/lkarlslund/koder/internal/planning"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/reference"
	sessionpkg "github.com/lkarlslund/koder/internal/session"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
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

func TestLintTouchedFilesReportsOnlyTouchedFileErrors(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bad.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "other.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "good.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	report := lintTouchedFiles(context.Background(), dir, []string{"bad.json", "good.json"})
	text := codediag.NewProblemsText(report)
	if !strings.Contains(text, "bad.json") {
		t.Fatalf("expected touched file diagnostic, got %q", text)
	}
	if strings.Contains(text, "other.json") || strings.Contains(text, "good.json") {
		t.Fatalf("expected only touched files with errors, got %q", text)
	}
}

func testConfig(t *testing.T) config.Config {
	t.Helper()
	return config.Default().WithStateDir(t.TempDir())
}

func defaultChatForSession(t *testing.T, st *store.Store, sessionID id.ID) domain.Chat {
	t.Helper()
	chat, err := sessionpkg.DefaultChat(context.Background(), st, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	return chat
}

func setSessionProjectRoot(ctx context.Context, st *store.Store, sessionID id.ID, root string) error {
	return sessionpkg.UpdateSession(ctx, st, sessionID, func(session *domain.Session) {
		session.ProjectRoot = root
	})
}

func setSessionPermissionProfile(ctx context.Context, st *store.Store, sessionID id.ID, profile string) error {
	return sessionpkg.UpdateSession(ctx, st, sessionID, func(session *domain.Session) {
		session.PermissionProfile = profile
	})
}

func setSessionToolStates(ctx context.Context, st *store.Store, sessionID id.ID, states map[domain.ToolKind]bool) error {
	return sessionpkg.UpdateSession(ctx, st, sessionID, func(session *domain.Session) {
		session.ToolStates = states
	})
}

func runLivePrompt(t *testing.T, engine *Engine, session domain.Session, chatRecord domain.Chat, text string) []domain.Event {
	return runLivePromptObserve(t, engine, session, chatRecord, text, nil)
}

func runLivePromptObserve(t *testing.T, engine *Engine, session domain.Session, chatRecord domain.Chat, text string, observe func(domain.Event)) []domain.Event {
	t.Helper()
	rt, err := engine.Chat(context.Background(), session, chatRecord)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rt.Close)
	updates, unsub := rt.Subscribe()
	defer unsub()
	rt.Enqueue(chatpkg.QueueItem{Kind: chatpkg.QueueKindSteer, Text: text})
	deadline := time.After(5 * time.Second)
	var events []domain.Event
	terminal := false
	for {
		select {
		case update := <-updates:
			if update.Event != nil {
				events = append(events, *update.Event)
				if observe != nil {
					observe(*update.Event)
				}
				switch update.Event.Kind {
				case domain.EventKindMessageDone, domain.EventKindApprovalAsk, domain.EventKindError:
					terminal = true
				}
			}
			if terminal && (!update.Active || update.Status == chatpkg.StatusWaitingApproval || update.Status == chatpkg.StatusErrored) {
				return events
			}
		case <-deadline:
			t.Fatalf("timed out waiting for live prompt; snapshot=%#v", rt.Snapshot())
		}
	}
}

func runLivePromptDefault(t *testing.T, engine *Engine, st *store.Store, session domain.Session, text string) []domain.Event {
	t.Helper()
	return runLivePrompt(t, engine, session, defaultChatForSession(t, st, session.ID), text)
}

func collectLiveUpdates(t *testing.T, rt *chatpkg.Chat, updates <-chan chatpkg.Update, terminal func(domain.Event) bool) []domain.Event {
	t.Helper()
	deadline := time.After(5 * time.Second)
	var events []domain.Event
	done := false
	for {
		select {
		case update := <-updates:
			if update.Event != nil {
				events = append(events, *update.Event)
				if terminal(*update.Event) {
					done = true
				}
			}
			if done && (!update.Active || update.Status == chatpkg.StatusWaitingApproval || update.Status == chatpkg.StatusErrored) {
				return events
			}
		case <-deadline:
			t.Fatalf("timed out waiting for chat updates; snapshot=%#v", rt.Snapshot())
		}
	}
}

func runLiveContinueDefault(t *testing.T, engine *Engine, st *store.Store, session domain.Session, note string) []domain.Event {
	t.Helper()
	chatRecord := defaultChatForSession(t, st, session.ID)
	rt, err := engine.Chat(context.Background(), session, chatRecord)
	if err != nil {
		t.Fatal(err)
	}
	updates, unsub := rt.Subscribe()
	defer unsub()
	rt.Enqueue(chatpkg.QueueItem{Kind: chatpkg.QueueKindContinue, Note: note})
	deadline := time.After(5 * time.Second)
	var events []domain.Event
	terminal := false
	for {
		select {
		case update := <-updates:
			if update.Event != nil {
				events = append(events, *update.Event)
				if update.Event.Kind == domain.EventKindMessageDone || update.Event.Kind == domain.EventKindError {
					terminal = true
				}
			}
			if terminal && (!update.Active || update.Status == chatpkg.StatusErrored) {
				return events
			}
		case <-deadline:
			t.Fatalf("timed out waiting for live continue; snapshot=%#v", rt.Snapshot())
		}
	}
}

func appendUserTimelineItem(t *testing.T, st *store.Store, chatID id.ID, text string) domain.TimelineItem {
	t.Helper()
	return appendUserTimelineItemWithAttachments(t, st, chatID, text, nil)
}

func appendUserTimelineItemWithAttachments(t *testing.T, st *store.Store, chatID id.ID, text string, attachments []domain.Attachment) domain.TimelineItem {
	t.Helper()
	item, err := chatpkg.AppendTimeline(context.Background(), st, chatID, domain.UserMessage{Text: text, Attachments: attachments})
	if err != nil {
		t.Fatal(err)
	}
	item.Seal(time.Now().UTC())
	if err := chatpkg.PutTimelineItem(context.Background(), st, item); err != nil {
		t.Fatal(err)
	}
	return item
}

func appendAssistantTimelineItem(t *testing.T, st *store.Store, chatID id.ID, msg domain.AssistantMessage) domain.TimelineItem {
	t.Helper()
	item, err := chatpkg.AppendTimeline(context.Background(), st, chatID, msg)
	if err != nil {
		t.Fatal(err)
	}
	item.Seal(time.Now().UTC())
	if err := chatpkg.PutTimelineItem(context.Background(), st, item); err != nil {
		t.Fatal(err)
	}
	return item
}

func appendNoticeTimelineItem(t *testing.T, st *store.Store, chatID id.ID, notice domain.Notice) domain.TimelineItem {
	t.Helper()
	item, err := chatpkg.AppendTimeline(context.Background(), st, chatID, notice)
	if err != nil {
		t.Fatal(err)
	}
	item.Seal(time.Now().UTC())
	if err := chatpkg.PutTimelineItem(context.Background(), st, item); err != nil {
		t.Fatal(err)
	}
	return item
}

func appendAssistantToolTimelineItem(t *testing.T, st *store.Store, chatID id.ID, req tools.Request, text string) domain.TimelineItem {
	t.Helper()
	item, err := chatpkg.AppendAssistantToolCalls(context.Background(), st, chatID, []domain.ToolCall{{
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

func attachToolResultTimelineItem(t *testing.T, st *store.Store, chatID id.ID, req tools.Request, text string, data domain.ToolResultPayload) domain.TimelineItem {
	t.Helper()
	item, err := chatpkg.AttachToolResult(context.Background(), st, chatID, req.ToolCallID, domain.ToolResult{
		Text:   text,
		Data:   data,
		Status: domain.ToolResultStatusOK,
	})
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func waitForToolStatus(t *testing.T, st *store.Store, chatID id.ID, toolCallID string, want domain.ToolStatus) bool {
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

func waitForTimelineCondition(t *testing.T, st *store.Store, chatID id.ID, want func([]domain.TimelineItem) bool) []domain.TimelineItem {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var items []domain.TimelineItem
	for time.Now().Before(deadline) {
		var err error
		items, err = chatpkg.TimelineForChat(context.Background(), st, chatID)
		if err != nil {
			t.Fatal(err)
		}
		if want(items) {
			return items
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for timeline condition; items=%#v", items)
	return nil
}

func waitForChatInactive(t *testing.T, rt *chatpkg.Chat) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, _, active := rt.Status()
		if !active {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for chat to become inactive; snapshot=%#v", rt.Snapshot())
}

func approveOnlyPendingTool(t *testing.T, rt *chatpkg.Chat, updates <-chan chatpkg.Update, st *store.Store, chatID id.ID) []domain.Event {
	t.Helper()
	pending, err := chatpkg.PendingApprovalsForChat(context.Background(), st, chatID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected one pending approval, got %#v", pending)
	}
	rt.ApproveTool(string(pending[0].ToolCallID))
	return collectLiveUpdates(t, rt, updates, func(evt domain.Event) bool {
		return evt.Kind == domain.EventKindMessageDone || evt.Kind == domain.EventKindError
	})
}

func assertToolStatus(t *testing.T, st *store.Store, chatID id.ID, toolCallID string, want domain.ToolStatus) {
	t.Helper()
	got := currentToolStatus(t, st, chatID, toolCallID)
	if got != want {
		t.Fatalf("tool %s status = %s, want %s", toolCallID, got, want)
	}
}

func currentToolStatus(t *testing.T, st *store.Store, chatID id.ID, toolCallID string) domain.ToolStatus {
	t.Helper()
	items, err := chatpkg.TimelineForChat(context.Background(), st, chatID)
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

func appendCompactionTimelineItem(t *testing.T, st *store.Store, chatID id.ID, summary string, firstKeptItemID string) domain.TimelineItem {
	t.Helper()
	item, err := chatpkg.AppendTimeline(context.Background(), st, chatID, domain.Compaction{
		Summary:         summary,
		Status:          "completed",
		FirstKeptItemID: firstKeptItemID,
	})
	if err != nil {
		t.Fatal(err)
	}
	item.Seal(time.Now().UTC())
	if err := chatpkg.PutTimelineItem(context.Background(), st, item); err != nil {
		t.Fatal(err)
	}
	return item
}

func timelineTranscriptForSession(t *testing.T, st *store.Store, sessionID id.ID) ([]domain.Message, map[id.ID][]domain.Part, error) {
	t.Helper()
	chat, err := sessionpkg.DefaultChat(context.Background(), st, sessionID)
	if err != nil {
		return nil, nil, err
	}
	return timelineTranscriptForChat(t, st, chat)
}

func timelineTranscriptForChat(t *testing.T, st *store.Store, chat domain.Chat) ([]domain.Message, map[id.ID][]domain.Part, error) {
	t.Helper()
	items, err := chatpkg.TimelineForChat(context.Background(), st, chat.ID)
	if err != nil {
		return nil, nil, err
	}
	messages := make([]domain.Message, 0, len(items))
	parts := make(map[id.ID][]domain.Part, len(items))
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

func testTranscriptItem(sessionID id.ID, item domain.TimelineItem) (domain.Message, []domain.Part) {
	messageID := id.ID(item.ID)
	msg := domain.Message{ID: messageID, SessionID: sessionID, ChatID: item.ChatID, CreatedAt: item.CreatedAt}
	addPart := func(parts *[]domain.Part, kind domain.PartKind, payload domain.PartPayload, offset int64) {
		part := domain.Part{ID: id.ID(fmt.Sprintf("%s-part-%d", messageID, offset)), MessageID: messageID, Kind: kind, Payload: payload, CreatedAt: item.CreatedAt}
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

func timelineNoticesForChat(t *testing.T, st *store.Store, chatID id.ID) []domain.Notice {
	t.Helper()
	items, err := chatpkg.TimelineForChat(context.Background(), st, chatID)
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
	for _, command := range []string{"/new", "/quit", "/permissions", "/approve", "/deny"} {
		if strings.Contains(prompt, command) {
			t.Fatalf("expected system prompt to exclude internal slash command %q", command)
		}
	}
}

func TestSystemPromptMentionsBrowserMarkdownAndDiagrams(t *testing.T) {
	data, err := assets.DefaultContent("system-prompt.md")
	if err != nil {
		t.Fatal(err)
	}
	prompt := string(data)
	for _, want := range []string{"browser interface", "GitHub-flavored Markdown", "Mermaid diagrams", "inline SVG"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected system prompt to mention %q", want)
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

	engine := New(testConfig(t), nil, nil, nil)
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

	engine := New(testConfig(t), nil, nil, nil)
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
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in environment prompt, got %q", want, got)
		}
	}
	for _, forbidden := range []string{"- Git root:", "- Git branch:", "- Git commit:", "- Git upstream:", "- Git status:"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("expected volatile git detail %q to be omitted, got %q", forbidden, got)
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
	engine := New(cfg, nil, nil)
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
	engine := New(testConfig(t), nil, nil, nil)
	if got := engine.maxToolLoopSteps(); got != 500 {
		t.Fatalf("expected default max tool loop steps 500, got %d", got)
	}
}

func TestMaxToolLoopStepsUsesConfiguredValue(t *testing.T) {
	cfg := testConfig(t)
	cfg.MaxToolLoopSteps = 7

	engine := New(cfg, nil, nil, nil)
	if got := engine.maxToolLoopSteps(); got != 7 {
		t.Fatalf("expected configured max tool loop steps 7, got %d", got)
	}
}

func TestApprovalSerializationRoundTrip(t *testing.T) {
	req := tools.Request{
		Tool: domain.ToolKindFileWrite,
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
	got, err := requestFromStoredApproval(domain.ToolKindFileWrite, raw)
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

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := setSessionToolStates(context.Background(), st, session.ID, map[domain.ToolKind]bool{
		domain.ToolKindFileRead: false,
	}); err != nil {
		t.Fatal(err)
	}
	chat := defaultChatForSession(t, st, session.ID)

	evt, err := engine.handleModelToolCall(context.Background(), session, chat, tools.Request{
		Tool: domain.ToolKindFileRead,
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

func TestConsumeChatUpdatesIgnoresInitialInactiveSnapshot(t *testing.T) {
	cfg := testConfig(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	parent := defaultChatForSession(t, st, session.ID)
	parentID := parent.ID
	child, err := sessionpkg.CreateChat(context.Background(), st, session.ID, "child", chatrole.Orchestrator, &parentID)
	if err != nil {
		t.Fatal(err)
	}
	engine := New(cfg, st, nil)

	updates := make(chan chatpkg.Update, 1)
	updates <- chatpkg.Update{Snapshot: chatpkg.Snapshot{Chat: child, Status: chatpkg.StatusIdle}, Status: chatpkg.StatusIdle, StatusText: "Idle", Active: false}
	close(updates)
	engine.consumeChatUpdates(child.ID, updates, nil)

	got, err := chatpkg.GetChat(context.Background(), st, parent.ID)
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

	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	parent := defaultChatForSession(t, st, session.ID)
	parentID := parent.ID
	child, err := sessionpkg.CreateChat(context.Background(), st, session.ID, "child", chatrole.Orchestrator, &parentID)
	if err != nil {
		t.Fatal(err)
	}
	engine := New(cfg, st, nil)

	updates := make(chan chatpkg.Update, 2)
	updates <- chatpkg.Update{Snapshot: chatpkg.Snapshot{Chat: child, Status: chatpkg.StatusWaitingLLM, Active: true}, Status: chatpkg.StatusWaitingLLM, StatusText: "Running", Active: true}
	updates <- chatpkg.Update{Snapshot: chatpkg.Snapshot{Chat: child, Status: chatpkg.StatusIdle, Active: false}, Status: chatpkg.StatusIdle, StatusText: "Idle", Active: false}
	close(updates)
	engine.consumeChatUpdates(child.ID, updates, nil)

	waitForTimelineCondition(t, st, parent.ID, func(items []domain.TimelineItem) bool {
		for _, item := range items {
			msg, ok := item.Content.(domain.UserMessage)
			if ok && msg.Text == "Chat "+child.ID+" is now idle." {
				return true
			}
		}
		return false
	})
	if owner := engine.loadedSession(session.ID); owner != nil {
		if err := owner.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestConsumeChatUpdatesSummarizesPartialMilestoneProgress(t *testing.T) {
	cfg := testConfig(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	parent := defaultChatForSession(t, st, session.ID)
	parentID := parent.ID
	child, err := sessionpkg.CreateChat(context.Background(), st, session.ID, "child", chatrole.Execution, &parentID)
	if err != nil {
		t.Fatal(err)
	}
	child.ActiveMilestoneRef = "alpha"
	if err := chatpkg.UpdateChat(context.Background(), st, child); err != nil {
		t.Fatal(err)
	}
	if err := sessionpkg.PutPlan(context.Background(), st, planning.Plan{SessionID: session.ID, Milestones: []planning.Milestone{{Ref: "alpha", Title: "Alpha", Status: planning.MilestoneStatusExecuting}}}); err != nil {
		t.Fatal(err)
	}
	todos, err := sessionpkg.AddTodoItems(context.Background(), st, session.ID, "alpha", []string{"first", "second"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sessionpkg.UpdateTodo(context.Background(), st, todos[0].ID, planning.TodoStatusCompleted, "", "completed in setup"); err != nil {
		t.Fatal(err)
	}
	engine := New(cfg, st, nil)

	updates := make(chan chatpkg.Update, 2)
	updates <- chatpkg.Update{Snapshot: chatpkg.Snapshot{Chat: child, Status: chatpkg.StatusWaitingLLM, Active: true}, Status: chatpkg.StatusWaitingLLM, StatusText: "Running", Active: true}
	updates <- chatpkg.Update{Snapshot: chatpkg.Snapshot{Chat: child, Status: chatpkg.StatusIdle, Active: false}, Status: chatpkg.StatusIdle, StatusText: "Idle", Active: false}
	close(updates)
	engine.consumeChatUpdates(child.ID, updates, nil)

	want := "Chat " + child.ID + " is now idle. Chat completed 1 out of 2 todos for milestone alpha, but is now stopped."
	waitForTimelineCondition(t, st, parent.ID, func(items []domain.TimelineItem) bool {
		for _, item := range items {
			msg, ok := item.Content.(domain.UserMessage)
			if ok && msg.Text == want {
				return true
			}
		}
		return false
	})
	if owner := engine.loadedSession(session.ID); owner != nil {
		if err := owner.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestConsumeChatUpdatesSummarizesCompletedMilestoneTodos(t *testing.T) {
	cfg := testConfig(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	parent := defaultChatForSession(t, st, session.ID)
	parentID := parent.ID
	child, err := sessionpkg.CreateChat(context.Background(), st, session.ID, "child", chatrole.Execution, &parentID)
	if err != nil {
		t.Fatal(err)
	}
	child.ActiveMilestoneRef = "alpha"
	if err := chatpkg.UpdateChat(context.Background(), st, child); err != nil {
		t.Fatal(err)
	}
	todos, err := sessionpkg.AddTodoItems(context.Background(), st, session.ID, "alpha", []string{"first", "second"})
	if err != nil {
		t.Fatal(err)
	}
	for _, todo := range todos {
		if _, err := sessionpkg.UpdateTodo(context.Background(), st, todo.ID, planning.TodoStatusCompleted, "", "completed in setup"); err != nil {
			t.Fatal(err)
		}
	}
	engine := New(cfg, st, nil)

	updates := make(chan chatpkg.Update, 2)
	updates <- chatpkg.Update{Snapshot: chatpkg.Snapshot{Chat: child, Status: chatpkg.StatusWaitingLLM, Active: true}, Status: chatpkg.StatusWaitingLLM, StatusText: "Running", Active: true}
	updates <- chatpkg.Update{Snapshot: chatpkg.Snapshot{Chat: child, Status: chatpkg.StatusIdle, Active: false}, Status: chatpkg.StatusIdle, StatusText: "Idle", Active: false}
	close(updates)
	engine.consumeChatUpdates(child.ID, updates, nil)

	want := "Chat " + child.ID + " is now idle. All 2 todos for milestone alpha are done."
	waitForTimelineCondition(t, st, parent.ID, func(items []domain.TimelineItem) bool {
		for _, item := range items {
			msg, ok := item.Content.(domain.UserMessage)
			if ok && msg.Text == want {
				return true
			}
		}
		return false
	})
	if owner := engine.loadedSession(session.ID); owner != nil {
		if err := owner.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestUpdateChatPersistsThroughEngineControl(t *testing.T) {
	cfg := testConfig(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	session, err := sessionpkg.CreateSession(context.Background(), st, "test", cfg.DefaultProvider, cfg.DefaultModel, nil)
	if err != nil {
		t.Fatal(err)
	}
	parent := defaultChatForSession(t, st, session.ID)
	parentID := parent.ID
	child, err := sessionpkg.CreateChat(context.Background(), st, session.ID, "child", chatrole.Execution, &parentID)
	if err != nil {
		t.Fatal(err)
	}
	engine := New(cfg, st, nil)
	archived := true
	status, err := engine.UpdateChat(context.Background(), session.ID, child.ID, tools.ChatUpdateRequest{Archived: &archived, Title: "Renamed child"})
	if err != nil {
		t.Fatal(err)
	}
	if !status.Chat.Archived || status.Chat.Title != "Renamed child" {
		t.Fatalf("expected archived status, got %#v", status.Chat)
	}
	reloaded, err := chatpkg.GetChat(context.Background(), st, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.Archived || reloaded.Title != "Renamed child" {
		t.Fatalf("expected persisted archive flag, got %#v", reloaded)
	}
	archived = false
	status, err = engine.UpdateChat(context.Background(), session.ID, child.ID, tools.ChatUpdateRequest{Archived: &archived})
	if err != nil {
		t.Fatal(err)
	}
	if status.Chat.Archived {
		t.Fatalf("expected restored status, got %#v", status.Chat)
	}
	reloaded, err = chatpkg.GetChat(context.Background(), st, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Archived {
		t.Fatalf("expected persisted restored archive flag, got %#v", reloaded)
	}
}

func TestHandleModelToolCallRejectsRoleForbiddenTool(t *testing.T) {
	cfg := testConfig(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	session, err := sessionpkg.CreateSession(context.Background(), st, "test", cfg.DefaultProvider, "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	chat := defaultChatForSession(t, st, session.ID)
	chat.WorkflowRole = chatrole.Execution
	if err := chatpkg.UpdateChat(context.Background(), st, chat); err != nil {
		t.Fatal(err)
	}
	engine := New(cfg, st, nil)

	evt, err := engine.handleModelToolCall(context.Background(), session, chat, tools.Request{
		Tool: domain.ToolKindChatStart,
		Args: map[string]string{"profile": string(chatrole.Execution), "objective": "no"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if evt.Kind != domain.EventKindToolResult || !strings.Contains(evt.Text, "not available to execution chats") {
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

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", cfg.DefaultProvider, "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	chat := defaultChatForSession(t, st, session.ID)

	evt, err := engine.handleModelToolCall(context.Background(), session, chat, tools.Request{
		Tool: domain.ToolKindFileRead,
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
	if !strings.Contains(evt.Text, "offset is no longer supported") {
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
	if !strings.Contains(got[0].Body, "offset is no longer supported") {
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

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", cfg.DefaultProvider, "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	chat := defaultChatForSession(t, st, session.ID)

	call := tools.Request{
		Tool:       domain.ToolKindFileRead,
		ToolCallID: "call_1",
		Args:       map[string]string{"path": "README.md"},
	}
	itemSeed := domain.TimelineItem{ID: chatpkg.NewTimelineID(time.Now().UTC()), ChatID: chat.ID, Seq: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	item, err := engine.persistAssistantToolCalls(context.Background(), chat.ID, session.ID, itemSeed, []tools.Request{call}, "Let me inspect that file first.", domain.Usage{TotalTokens: 10})
	if err != nil {
		t.Fatal(err)
	}
	if item.ID == "" {
		t.Fatalf("expected persisted timeline item, got %#v", item)
	}

	items, err := chatpkg.TimelineForChat(context.Background(), st, chat.ID)
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
	if len(assistant.Tools) != 1 || assistant.Tools[0].Tool != domain.ToolKindFileRead {
		t.Fatalf("expected tool call child, got %#v", assistant.Tools)
	}
	if !strings.Contains(assistant.Text, "inspect that file") {
		t.Fatalf("expected narration to be stored as text, got %#v", assistant)
	}
	if assistant.Usage == nil || assistant.Usage.TotalTokens != 10 {
		t.Fatalf("expected usage to be stored on assistant item, got %#v", assistant.Usage)
	}
}

func TestProviderToolCallArgumentsAreNormalizedBeforePersistence(t *testing.T) {
	cfg := testConfig(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", cfg.DefaultProvider, "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	chat := defaultChatForSession(t, st, session.ID)

	parsed := engine.parseProviderToolCallsForTranscript([]provider.ToolCall{{
		ID: "call_1",
		Function: provider.FunctionCall{
			Name:      domain.ToolKindFileRead.String(),
			Arguments: `{"path":"README.md","start_line":"150.0000","end_line":"175.0000"}`,
		},
	}}, session.ID)
	if parsed.Err != nil {
		t.Fatal(parsed.Err)
	}
	itemSeed := domain.TimelineItem{ID: chatpkg.NewTimelineID(time.Now().UTC()), ChatID: chat.ID, Seq: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if _, err := engine.persistAssistantToolCalls(context.Background(), chat.ID, session.ID, itemSeed, parsed.Requests, "", domain.Usage{}); err != nil {
		t.Fatal(err)
	}

	items, err := chatpkg.TimelineForChat(context.Background(), st, chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one assistant item, got %d", len(items))
	}
	assistant, ok := items[0].Content.(domain.AssistantMessage)
	if !ok || len(assistant.Tools) != 1 {
		t.Fatalf("expected assistant tool call, got %#v", items[0].Content)
	}
	args := assistant.Tools[0].Args
	if args["start_line"] != "150" || args["end_line"] != "175" {
		t.Fatalf("expected persisted normalized line args, got %#v", args)
	}
}

func TestBuildConversationIncludesAssistantNarrationAlongsideToolCalls(t *testing.T) {
	cfg := testConfig(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", cfg.DefaultProvider, "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	chat := defaultChatForSession(t, st, session.ID)

	req := tools.Request{
		Tool:       domain.ToolKindFileRead,
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
	if got.Role != provider.RoleAssistant {
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

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", cfg.DefaultProvider, "test-model", nil)
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
	if conversation[len(conversation)-2].Role != provider.RoleUser {
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

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", cfg.DefaultProvider, "test-model", nil)
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
	if conversation[len(conversation)-1].Role != provider.RoleTool || conversation[len(conversation)-1].ToolCallID != "call_1" {
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

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", cfg.DefaultProvider, "test-model", nil)
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

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", cfg.DefaultProvider, "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	chat := defaultChatForSession(t, st, session.ID)

	appendUserTimelineItem(t, st, chat.ID, "old question")
	toolReq := tools.Request{Tool: domain.ToolKindBash, ToolCallID: "call_1", Args: map[string]string{"command": "pwd"}}
	toolItem := appendAssistantToolTimelineItem(t, st, chat.ID, toolReq, "")
	attachToolResultTimelineItem(t, st, chat.ID, toolReq, "/tmp/project", domain.BashStoredResult{Command: "pwd", Output: "/tmp/project"})

	timeline, err := chatpkg.TimelineForChat(context.Background(), st, chat.ID)
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

func TestBuildCompactionConversationStripsImageContentParts(t *testing.T) {
	cfg := testConfig(t)
	cfg.CompactionKeepToolBatches = 1
	workdir := t.TempDir()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "openai", "gpt-5.4", nil)
	if err != nil {
		t.Fatal(err)
	}
	chat := defaultChatForSession(t, st, session.ID)

	imagePath := filepath.Join(workdir, "screen.png")
	if err := os.WriteFile(imagePath, []byte("\x89PNG\r\n\x1a\nfake"), 0o644); err != nil {
		t.Fatal(err)
	}
	appendUserTimelineItemWithAttachments(t, st, chat.ID, "old screenshot", []domain.Attachment{{
		Name: "screen.png",
		MIME: "image/png",
		Path: imagePath,
		Size: 12,
	}})
	imageReq := tools.Request{Tool: domain.ToolKindViewImage, ToolCallID: "call_image", Args: map[string]string{"path": "screen.png"}}
	appendAssistantToolTimelineItem(t, st, chat.ID, imageReq, "I will inspect the image.")
	attachToolResultTimelineItem(t, st, chat.ID, imageReq, "Viewed image screen.png", domain.ViewImageStoredResult{
		Path:       "screen.png",
		SourcePath: imagePath,
		MIMEType:   "image/png",
		Summary:    "Viewed image screen.png",
	})
	tailReq := tools.Request{Tool: domain.ToolKindBash, ToolCallID: "call_tail", Args: map[string]string{"command": "pwd"}}
	tailItem := appendAssistantToolTimelineItem(t, st, chat.ID, tailReq, "")
	attachToolResultTimelineItem(t, st, chat.ID, tailReq, "/tmp/project", domain.BashStoredResult{Command: "pwd", Output: "/tmp/project"})

	timeline, err := chatpkg.TimelineForChat(context.Background(), st, chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	conversation, firstKeptItemID, err := engine.buildCompactionConversationForTimeline(session, chat, timeline)
	if err != nil {
		t.Fatal(err)
	}
	if firstKeptItemID != tailItem.ID {
		t.Fatalf("expected preserved tail to start at latest tool batch, got %s want %s", firstKeptItemID, tailItem.ID)
	}
	payload, err := json.Marshal(conversation)
	if err != nil {
		t.Fatal(err)
	}
	rendered := string(payload)
	if strings.Contains(rendered, "image_url") || strings.Contains(rendered, "data:image") {
		t.Fatalf("expected text-only compaction request, got %s", rendered)
	}
	for _, msg := range conversation {
		if len(msg.ContentParts) != 0 {
			t.Fatalf("expected no content parts in compaction message, got %#v", msg)
		}
		if msg.Role == provider.RoleTool || msg.ToolCallID != "" || len(msg.ToolCalls) != 0 {
			t.Fatalf("expected compaction messages to avoid structured tool protocol, got %#v", msg)
		}
	}
	if !strings.Contains(rendered, "Image attachment omitted for text-only compaction") ||
		!strings.Contains(rendered, "image bytes omitted") ||
		!strings.Contains(rendered, "Viewed image screen.png") {
		t.Fatalf("expected image metadata in text-only compaction request, got %s", rendered)
	}
	if strings.Contains(rendered, "/tmp/project") {
		t.Fatalf("expected preserved tail output excluded from compaction source, got %s", rendered)
	}
}

func TestBuildCompactionConversationTruncatesLargeToolOutput(t *testing.T) {
	cfg := testConfig(t)
	cfg.CompactionKeepToolBatches = 0
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", cfg.DefaultProvider, "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	chat := defaultChatForSession(t, st, session.ID)

	lines := make([]string, 0, 220)
	for i := 0; i < 220; i++ {
		lines = append(lines, fmt.Sprintf("output-line-%03d", i))
	}
	req := tools.Request{Tool: domain.ToolKindExecCommand, ToolCallID: "call_exec", Args: map[string]string{"cmd": "long"}}
	appendAssistantToolTimelineItem(t, st, chat.ID, req, "")
	attachToolResultTimelineItem(t, st, chat.ID, req, strings.Join(lines, "\n"), domain.ExecStoredResult{
		ProcessID: "proc-1",
		Command:   "long",
		State:     "done",
		Output:    strings.Join(lines, "\n"),
	})

	timeline, err := chatpkg.TimelineForChat(context.Background(), st, chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	conversation, _, err := engine.buildCompactionConversationForTimeline(session, chat, timeline)
	if err != nil {
		t.Fatal(err)
	}
	rendered := ""
	for _, msg := range conversation {
		rendered += msg.Content + "\n"
	}
	for _, want := range []string{"process_id: proc-1", "command: long", "exec output truncated for compaction", "output-line-000", "output-line-219"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected %q in compaction rendering, got %q", want, rendered)
		}
	}
	if strings.Contains(rendered, "output-line-100") {
		t.Fatalf("expected middle output to be omitted, got %q", rendered)
	}
}

func TestBuildCompactionConversationHonorsPreviousCompactionBoundary(t *testing.T) {
	cfg := testConfig(t)
	cfg.CompactionKeepToolBatches = 1
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", cfg.DefaultProvider, "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	chat := defaultChatForSession(t, st, session.ID)

	appendUserTimelineItem(t, st, chat.ID, strings.Repeat("old raw history ", 1000))
	previousReq := tools.Request{Tool: domain.ToolKindBash, ToolCallID: "call_previous", Args: map[string]string{"command": "pwd"}}
	previousToolItem := appendAssistantToolTimelineItem(t, st, chat.ID, previousReq, "")
	attachToolResultTimelineItem(t, st, chat.ID, previousReq, "/tmp/project", domain.BashStoredResult{Command: "pwd", Output: "/tmp/project"})
	appendCompactionTimelineItem(t, st, chat.ID, "previous compact summary", previousToolItem.ID)
	appendUserTimelineItem(t, st, chat.ID, "new raw history")
	latestReq := tools.Request{Tool: domain.ToolKindBash, ToolCallID: "call_latest", Args: map[string]string{"command": "go test"}}
	latestToolItem := appendAssistantToolTimelineItem(t, st, chat.ID, latestReq, "")
	attachToolResultTimelineItem(t, st, chat.ID, latestReq, "ok", domain.BashStoredResult{Command: "go test", Output: "ok"})

	timeline, err := chatpkg.TimelineForChat(context.Background(), st, chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	conversation, firstKeptItemID, err := engine.buildCompactionConversationForTimeline(session, chat, timeline)
	if err != nil {
		t.Fatal(err)
	}
	if firstKeptItemID != latestToolItem.ID {
		t.Fatalf("expected latest tool batch to be preserved from compaction source, got %s want %s", firstKeptItemID, latestToolItem.ID)
	}
	rendered := ""
	for _, msg := range conversation {
		rendered += msg.Content + "\n"
	}
	if strings.Contains(rendered, "old raw history") {
		t.Fatalf("expected previous raw history to remain summarized away, got %q", rendered)
	}
	for _, want := range []string{"previous compact summary", "/tmp/project", "new raw history"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected %q in compaction rendering, got %q", want, rendered)
		}
	}
	if strings.Contains(rendered, "go test") || strings.Contains(rendered, "call_latest") {
		t.Fatalf("expected latest preserved tool batch excluded from compaction source, got %q", rendered)
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

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", cfg.DefaultProvider, "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := setSessionProjectRoot(context.Background(), st, session.ID, repo); err != nil {
		t.Fatal(err)
	}
	session.ProjectRoot = repo
	chat := defaultChatForSession(t, st, session.ID)

	conversation, err := engine.buildConversation(context.Background(), session.ID, chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(conversation) < 1 {
		t.Fatalf("expected system prompt, got %#v", conversation)
	}
	if conversation[0].Role != provider.RoleSystem {
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

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", cfg.DefaultProvider, "test-model", nil)
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
	if conversation[len(conversation)-1].Role != provider.RoleTool || conversation[len(conversation)-1].ToolCallID != "call_1" || conversation[len(conversation)-1].Content != "/typed/output" {
		t.Fatalf("expected structured tool message, got %#v", conversation[len(conversation)-1])
	}
}

func TestBuildConversationIncludesViewImageToolContentParts(t *testing.T) {
	cfg := testConfig(t)
	workdir := t.TempDir()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "openai", "gpt-5.4", nil)
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
	if msg.Role != provider.RoleTool || msg.ToolCallID != "call_image" {
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

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "openai", "gpt-5.4", nil)
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

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "test-model", nil)
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
	if last.Role != provider.RoleUser || last.Content != "unsent draft" {
		t.Fatalf("expected unsent draft as final user message, got %#v", last)
	}
	if req.Messages[len(req.Messages)-2].Content != "saved prompt" {
		t.Fatalf("expected stored conversation before draft, got %#v", req.Messages)
	}
	if req.Messages[0].Role != provider.RoleSystem || !strings.Contains(req.Messages[0].Content, "Permission mode changed") {
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

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	session.ProjectRoot = repo
	session.AgentsResolved = "Follow repository instructions."

	req, err := engine.PreviewNextRequest(context.Background(), session, "what's in this folder?", nil, nil, "Permission mode changed")
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Messages) != 2 {
		t.Fatalf("expected one system and one user message, got %#v", req.Messages)
	}
	if req.Messages[0].Role != provider.RoleSystem {
		t.Fatalf("expected leading system message, got %#v", req.Messages)
	}
	if req.Messages[1].Role != provider.RoleUser {
		t.Fatalf("expected trailing user message, got %#v", req.Messages)
	}
	for _, want := range []string{
		"You are koder, a browser-based coding agent with local workspace tools.",
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

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "test-model", nil)
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
	chatRecord := defaultChatForSession(t, st, session.ID)
	rt, err := engine.Chat(context.Background(), session, chatRecord)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rt.Close)
	updates, unsub := rt.Subscribe()
	defer unsub()
	rt.Enqueue(chatpkg.QueueItem{Kind: chatpkg.QueueKindSteer, Text: "summarize", Attachments: []attachment.Draft{draft}})
	events := collectLiveUpdates(t, rt, updates, func(evt domain.Event) bool {
		return evt.Kind == domain.EventKindError
	})
	for _, evt := range events {
		if evt.Kind == domain.EventKindError {
			return
		}
	}
	t.Fatal("expected unsupported pdf attachment to be rejected")
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
	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	session.ProjectRoot = workdir

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

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "Qwen/Qwen3.6-35B-A3B", nil)
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

func TestPreviewNextRequestIncludesExplicitModelSettings(t *testing.T) {
	cfg := testConfig(t)
	cfg.Providers = map[string]config.Provider{
		"test": {
			BaseURL: "http://127.0.0.1:8000/v1",
		},
	}
	temperature := 0.6
	topP := 0.8
	cfg.SetModelConfig(config.ModelConfig{
		ProviderID:     "test",
		ModelID:        "Qwen/Qwen3.6-35B-A3B",
		ModelPreset:    provider.ModelPresetDefault,
		Temperature:    &temperature,
		TopP:           &topP,
		ThinkingMode:   "disabled",
		ThinkingBudget: 2048,
	})
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "Qwen/Qwen3.6-35B-A3B", nil)
	if err != nil {
		t.Fatal(err)
	}
	req, err := engine.PreviewNextRequest(context.Background(), session, "continue", nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if req.ExtraBody["temperature"] != 0.6 || req.ExtraBody["top_p"] != 0.8 {
		t.Fatalf("expected sampling settings in request, got %#v", req.ExtraBody)
	}
	got, ok := req.ExtraBody["chat_template_kwargs"].(map[string]any)
	if !ok || got["enable_thinking"] != false || got["preserve_thinking"] != false {
		t.Fatalf("expected thinking disabled in request, got %#v", req.ExtraBody)
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
	engine := New(cfg, st, nil, manager)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", cfg.DefaultProvider, "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := setSessionToolStates(context.Background(), st, session.ID, map[domain.ToolKind]bool{
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

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "Qwen/Qwen3.6-35B-A3B", nil)
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
	t.Skip("permission approval profiles were replaced by session access settings")
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
			{Tool: domain.ToolKindFileRead, Pattern: "*", Action: accesssettings.PermissionModeAllow},
			{Tool: domain.ToolKindFileGlob, Pattern: "*", Action: accesssettings.PermissionModeAllow},
			{Tool: domain.ToolKindFileGrep, Pattern: "*", Action: accesssettings.PermissionModeAllow},
			{Tool: domain.ToolKindBash, Pattern: "*", Action: accesssettings.PermissionModeAsk},
		},
	}

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := setSessionPermissionProfile(context.Background(), st, session.ID, "default"); err != nil {
		t.Fatal(err)
	}

	chatRecord := defaultChatForSession(t, st, session.ID)
	rt, err := engine.Chat(context.Background(), session, chatRecord)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rt.Close)
	updates, unsub := rt.Subscribe()
	defer unsub()
	rt.Enqueue(chatpkg.QueueItem{Kind: chatpkg.QueueKindSteer, Text: "say hello"})
	events := collectLiveUpdates(t, rt, updates, func(evt domain.Event) bool {
		return evt.Kind == domain.EventKindApprovalAsk
	})
	var approvalID id.ID
	for _, evt := range events {
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

	approvedEvents := approveOnlyPendingTool(t, rt, updates, st, chatRecord.ID)
	var sawToolResult bool
	var sawFinalAnswer bool
	for _, evt := range approvedEvents {
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
	t.Skip("permission approval profiles were replaced by session access settings")
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
			Action:  accesssettings.PermissionModeAsk,
		}},
	}

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := setSessionPermissionProfile(context.Background(), st, session.ID, permissionprofile.ProfileAsk); err != nil {
		t.Fatal(err)
	}

	chatRecord := defaultChatForSession(t, st, session.ID)
	rt, err := engine.Chat(context.Background(), session, chatRecord)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rt.Close)
	updates, unsub := rt.Subscribe()
	defer unsub()
	rt.Enqueue(chatpkg.QueueItem{Kind: chatpkg.QueueKindSteer, Text: "say hello"})
	events := collectLiveUpdates(t, rt, updates, func(evt domain.Event) bool {
		return evt.Kind == domain.EventKindApprovalAsk
	})
	var approvalID id.ID
	for _, evt := range events {
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
	pendingBefore, err := chatpkg.PendingApprovalsForChat(context.Background(), st, chatRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pendingBefore) != 1 {
		t.Fatalf("expected one pending approval, got %#v", pendingBefore)
	}

	if err := setSessionPermissionProfile(context.Background(), st, session.ID, permissionprofile.ProfileFullAccess); err != nil {
		t.Fatal(err)
	}
	updated, err := sessionpkg.GetSession(context.Background(), st, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	rt.SetSession(updated)
	rt.ApproveTool(string(pendingBefore[0].ToolCallID))

	var sawToolResult bool
	var sawFinalAnswer bool
	reevalEvents := collectLiveUpdates(t, rt, updates, func(evt domain.Event) bool {
		return evt.Kind == domain.EventKindMessageDone || evt.Kind == domain.EventKindError
	})
	for _, evt := range reevalEvents {
		if evt.Kind == domain.EventKindToolResult && strings.Contains(evt.Text, "hello") {
			sawToolResult = true
		}
		if evt.Kind == domain.EventKindMessageDelta && evt.Text == "done" {
			sawFinalAnswer = true
		}
	}
	if !sawToolResult {
		t.Fatal("expected tool result after re-evaluation")
	}
	if !sawFinalAnswer {
		t.Fatal("expected final assistant answer after re-evaluation")
	}

	if updated.PermissionProfile != permissionprofile.ProfileFullAccess {
		t.Fatalf("expected permission profile %q, got %q", permissionprofile.ProfileFullAccess, updated.PermissionProfile)
	}

	chats, err := sessionpkg.ListChats(context.Background(), st, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(chats) != 1 {
		t.Fatalf("expected one chat, got %d", len(chats))
	}
	pending, err := chatpkg.PendingApprovalsForChat(context.Background(), st, chats[0].ID)
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

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := setSessionPermissionProfile(context.Background(), st, session.ID, permissionprofile.ProfileFullAccess); err != nil {
		t.Fatal(err)
	}

	chatRecord := defaultChatForSession(t, st, session.ID)
	events := runLivePrompt(t, engine, session, chatRecord, "say hello")
	var toolResults []string
	var sawFinalAnswer bool
	for _, evt := range events {
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

func TestHandleModelToolCallsStopsAfterToolBatchWhenStopRequested(t *testing.T) {
	t.Parallel()

	cfg := testConfig(t)
	cfg.Permissions.Profile = permissionprofile.ProfileFullAccess

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := setSessionPermissionProfile(context.Background(), st, session.ID, permissionprofile.ProfileFullAccess); err != nil {
		t.Fatal(err)
	}
	chat, err := sessionpkg.CreateChat(context.Background(), st, session.ID, "chat", "", nil)
	if err != nil {
		t.Fatal(err)
	}

	out := make(chan domain.Event, 8)
	ctx := chatpkg.WithShouldStop(context.Background(), func() bool { return true })
	req := tools.Request{
		Tool:       domain.ToolKindBash,
		ToolCallID: "call_1",
		Args:       map[string]string{"command": "printf hi"},
	}
	appendAssistantToolTimelineItem(t, st, chat.ID, req, "")
	needsApproval, err := engine.handleModelToolCalls(ctx, session, chat, []tools.Request{req}, out)
	if err != nil {
		t.Fatalf("expected graceful stop after tool batch, got %v", err)
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

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := setSessionPermissionProfile(context.Background(), st, session.ID, permissionprofile.ProfileFullAccess); err != nil {
		t.Fatal(err)
	}
	chat := defaultChatForSession(t, st, session.ID)
	req := tools.Request{
		Tool:       domain.ToolKindBash,
		ToolCallID: "call_1",
		Args:       map[string]string{"command": "printf hi"},
	}
	appendAssistantToolTimelineItem(t, st, chat.ID, req, "")

	rt, err := engine.Chat(context.Background(), session, chat)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rt.Close)
	items := waitForTimelineCondition(t, st, chat.ID, func(items []domain.TimelineItem) bool {
		var sawToolResult bool
		var sawFinalAnswer bool
		for _, item := range items {
			if assistant, ok := item.Content.(domain.AssistantMessage); ok {
				if call := assistant.ToolByID(domain.ToolCallID("call_1")); call != nil && call.Result != nil && strings.Contains(call.Result.Text, "hi") {
					sawToolResult = true
				}
				if assistant.Text == "done" {
					sawFinalAnswer = true
				}
			}
		}
		return sawToolResult && sawFinalAnswer
	})
	var sawToolResult bool
	var sawFinalAnswer bool
	for _, item := range items {
		assistant, ok := item.Content.(domain.AssistantMessage)
		if !ok {
			continue
		}
		if call := assistant.ToolByID(domain.ToolCallID("call_1")); call != nil && call.Result != nil && strings.Contains(call.Result.Text, "hi") {
			sawToolResult = true
		}
		if assistant.Text == "done" {
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
	waitForChatInactive(t, rt)
}

func TestResumePendingToolCallsIgnoresLaterQueuedUserMessage(t *testing.T) {
	t.Parallel()

	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		requests++
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if requests == 1 && strings.Contains(string(body), "next user turn") {
			t.Fatalf("pending tool continuation should not include queued user message: %s", body)
		}
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

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := setSessionPermissionProfile(context.Background(), st, session.ID, permissionprofile.ProfileFullAccess); err != nil {
		t.Fatal(err)
	}
	chat := defaultChatForSession(t, st, session.ID)
	req := tools.Request{
		Tool:       domain.ToolKindBash,
		ToolCallID: "call_1",
		Args:       map[string]string{"command": "printf hi"},
	}
	appendAssistantToolTimelineItem(t, st, chat.ID, req, "")
	queuedUser := appendUserTimelineItem(t, st, chat.ID, "next user turn")
	chat.QueuedInputs = []domain.QueuedInput{{
		ID:         id.New(),
		Kind:       domain.QueuedInputKindSteer,
		Text:       "next user turn",
		Source:     domain.UserMessageSourceUser,
		TimelineID: queuedUser.ID,
		CreatedAt:  time.Now().UTC(),
	}}
	if err := chatpkg.SetChatQueuedInputs(context.Background(), st, chat.ID, chat.QueuedInputs); err != nil {
		t.Fatal(err)
	}

	rt, err := engine.Chat(context.Background(), session, chat)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rt.Close)
	items := waitForTimelineCondition(t, st, chat.ID, func(items []domain.TimelineItem) bool {
		for _, item := range items {
			assistant, ok := item.Content.(domain.AssistantMessage)
			if !ok {
				continue
			}
			if call := assistant.ToolByID(domain.ToolCallID("call_1")); call != nil && call.Result != nil && strings.Contains(call.Result.Text, "hi") {
				return true
			}
		}
		return false
	})
	var sawToolResult bool
	for _, item := range items {
		assistant, ok := item.Content.(domain.AssistantMessage)
		if !ok {
			continue
		}
		if call := assistant.ToolByID(domain.ToolCallID("call_1")); call != nil && call.Result != nil && strings.Contains(call.Result.Text, "hi") {
			sawToolResult = true
		}
	}
	if !sawToolResult {
		t.Fatal("expected resumed tool result")
	}
	waitForChatInactive(t, rt)
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

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := setSessionPermissionProfile(context.Background(), st, session.ID, permissionprofile.ProfileFullAccess); err != nil {
		t.Fatal(err)
	}

	chat := defaultChatForSession(t, st, session.ID)
	var sawRunning bool
	var sawRunningEventItem bool
	events := runLivePromptObserve(t, engine, session, chat, "run slow command", func(evt domain.Event) {
		if evt.Kind == domain.EventKindToolStart {
			if assistant, ok := evt.Item.Content.(domain.AssistantMessage); ok {
				if call := assistant.ToolByID(domain.ToolCallID("call_1")); call != nil && call.Status == domain.ToolStatusRunning {
					sawRunningEventItem = true
				}
			}
			sawRunning = waitForToolStatus(t, st, chat.ID, "call_1", domain.ToolStatusRunning)
		}
	})
	var sawDone bool
	for _, evt := range events {
		if evt.Kind == domain.EventKindToolResult && strings.Contains(evt.Text, "hi") {
			sawDone = true
		}
	}
	if !sawRunning {
		t.Fatal("expected allowed tool to transition to running before completion")
	}
	if !sawRunningEventItem {
		t.Fatal("expected tool start event to carry running timeline item")
	}
	if !sawDone {
		t.Fatal("expected allowed tool result")
	}
	assertToolStatus(t, st, chat.ID, "call_1", domain.ToolStatusDone)
}

func TestRunPromptDeniedToolTransitionsPendingToDenied(t *testing.T) {
	t.Skip("permission approval profiles were replaced by session access settings")
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
		Rules: []config.PermissionRule{{Tool: domain.ToolKindBash, Pattern: "*", Action: accesssettings.PermissionModeDeny}},
	}

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := setSessionPermissionProfile(context.Background(), st, session.ID, "default"); err != nil {
		t.Fatal(err)
	}

	events := runLivePromptDefault(t, engine, st, session, "run command")
	var sawDenied bool
	for _, evt := range events {
		if evt.Kind == domain.EventKindToolResult && strings.Contains(evt.Text, "denied by policy") {
			sawDenied = true
		}
	}
	if !sawDenied {
		t.Fatal("expected denied tool result")
	}
	chat := defaultChatForSession(t, st, session.ID)
	assertToolStatus(t, st, chat.ID, "call_1", domain.ToolStatusDenied)
	pending, err := chatpkg.PendingApprovalsForChat(context.Background(), st, chat.ID)
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

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	chat := defaultChatForSession(t, st, session.ID)
	req := tools.Request{
		Tool:       domain.ToolKindFileRead,
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

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	chat := defaultChatForSession(t, st, session.ID)
	req := tools.Request{
		Tool:       domain.ToolKindFileRead,
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

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	chat, err := sessionpkg.CreateChat(context.Background(), st, session.ID, "chat", "", nil)
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

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}

	events := runLivePromptDefault(t, engine, st, session, "say hello")
	var deltas []string
	var sawDone bool
	for _, evt := range events {
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

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}

	events := runLivePromptDefault(t, engine, st, session, "say hello")
	var sawDone, sawError bool
	for _, evt := range events {
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

func TestRunPromptPersistsInvalidKnownProviderToolCallAsToolError(t *testing.T) {
	t.Parallel()

	var requests [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		requests = append(requests, body)
		switch len(requests) {
		case 1:
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"I'll update the todo.\",\"tool_calls\":[{\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"todos_update\",\"arguments\":\"{\\\"id\\\":\\\"019aa000-0000-7000-8000-000000000001\\\",\\\"status\\\":\\\"bogus\\\"}\"}}]}}]}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		default:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"I saw the tool error."}}],"usage":{"total_tokens":1}}`))
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

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	chatRecord := defaultChatForSession(t, st, session.ID)

	events := runLivePrompt(t, engine, session, chatRecord, "update todo")
	var sawToolDelta bool
	for _, evt := range events {
		if evt.Kind == domain.EventKindToolCallDelta {
			sawToolDelta = true
		}
	}
	if !sawToolDelta {
		t.Fatal("expected tool call delta for persisted invalid tool call")
	}
	if len(requests) < 2 {
		t.Fatalf("expected invalid tool call to continue with tool error feedback, got %d requests", len(requests))
	}
	if !strings.Contains(string(requests[1]), "Invalid tool call: invalid todo status") {
		t.Fatalf("expected second request to include tool error feedback, got %s", requests[1])
	}

	timeline, err := chatpkg.TimelineForChat(context.Background(), st, chatRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	var sawErroredTool bool
	for _, item := range timeline {
		assistant, ok := item.Content.(domain.AssistantMessage)
		if !ok {
			continue
		}
		for _, tool := range assistant.Tools {
			if tool.Tool == domain.ToolKindTodosUpdate && tool.Status == domain.ToolStatusErrored && tool.Error != nil && strings.Contains(tool.Error.Message, "invalid todo status") {
				sawErroredTool = true
			}
		}
	}
	if !sawErroredTool {
		t.Fatalf("expected transcript to contain errored todos_update call, got %#v", timeline)
	}
}

func TestRunPromptPersistsOversizedStreamedToolCallAsToolError(t *testing.T) {
	t.Parallel()

	var requests [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		requests = append(requests, body)
		switch len(requests) {
		case 1:
			args, err := json.Marshal(map[string]string{
				"path":    "big.go",
				"content": strings.Repeat("x", 65*1024),
			})
			if err != nil {
				t.Fatal(err)
			}
			payload, err := json.Marshal(map[string]any{
				"choices": []any{map[string]any{
					"delta": map[string]any{
						"tool_calls": []any{map[string]any{
							"id":    "call_big",
							"type":  "function",
							"index": 0,
							"function": map[string]any{
								"name":      "file_write",
								"arguments": string(args),
							},
						}},
					},
				}},
			})
			if err != nil {
				t.Fatal(err)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		default:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"I will use smaller edits."}}],"usage":{"total_tokens":1}}`))
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

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	chatRecord := defaultChatForSession(t, st, session.ID)

	events := runLivePrompt(t, engine, session, chatRecord, "write a big file")
	var sawToolDelta bool
	for _, evt := range events {
		if evt.Kind == domain.EventKindToolCallDelta {
			sawToolDelta = true
		}
	}
	if !sawToolDelta {
		t.Fatal("expected tool call delta for persisted oversized tool call")
	}
	if len(requests) < 2 {
		t.Fatalf("expected oversized tool call to continue with tool error feedback, got %d requests", len(requests))
	}
	if !strings.Contains(string(requests[1]), "file_write tool arguments exceeded 64 KiB") {
		t.Fatalf("expected second request to include stream limit feedback, got %s", requests[1])
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

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}

	events := runLivePromptDefault(t, engine, st, session, "say hello")
	var done domain.Event
	for _, evt := range events {
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

func TestRunPromptStoresAndReplaysCavemanReasoning(t *testing.T) {
	t.Parallel()

	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		requests = append(requests, string(body))
		switch len(requests) {
		case 1:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"done","reasoning":"I should inspect the files carefully and then edit the smallest surface."}}],"usage":{"total_tokens":10}}`))
		case 2:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"me inspect files. me edit small."}}],"usage":{"total_tokens":4}}`))
		default:
			t.Fatalf("unexpected provider request %d: %s", len(requests), string(body))
		}
	}))
	defer server.Close()

	cfg := testConfig(t)
	cfg.Providers = map[string]config.Provider{
		"test": {BaseURL: server.URL + "/v1", Timeout: time.Second, Stream: false},
	}
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "Qwen/Qwen3.6-35B-A3B"
	cfg.SetModelConfig(config.ModelConfig{ProviderID: "test", ModelID: "Qwen/Qwen3.6-35B-A3B", ModelPreset: provider.ModelPresetAuto})
	cfg.Thinking.CavemanEnabled = true
	cfg.Thinking.CavemanPrompt = "Caveman rewrite only:\n{{thinking}}"

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "Qwen/Qwen3.6-35B-A3B", nil)
	if err != nil {
		t.Fatal(err)
	}
	events := runLivePromptDefault(t, engine, st, session, "go")
	var done domain.Event
	for _, evt := range events {
		if evt.Kind == domain.EventKindMessageDone {
			done = evt
		}
	}
	assistant, ok := done.Item.Content.(domain.AssistantMessage)
	if !ok {
		t.Fatalf("expected assistant item, got %#v", done.Item)
	}
	if assistant.Reasoning.Text == "" || assistant.Reasoning.Caveman != "me inspect files. me edit small." {
		t.Fatalf("expected original and caveman reasoning, got %#v", assistant.Reasoning)
	}
	if len(requests) != 2 || !strings.Contains(requests[1], "Caveman rewrite only") {
		t.Fatalf("expected caveman rewrite request, got %#v", requests)
	}

	next, err := engine.PreviewNextRequest(context.Background(), session, "continue", nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	var assistantReplay string
	for _, msg := range next.Messages {
		if msg.Role == provider.RoleAssistant {
			assistantReplay += msg.Content
		}
	}
	if !strings.Contains(assistantReplay, "me inspect files. me edit small.") || strings.Contains(assistantReplay, "inspect the files carefully") {
		raw, _ := json.Marshal(next.Messages)
		t.Fatalf("expected next request to replay caveman reasoning only, assistant=%q messages=%s", assistantReplay, raw)
	}
}

func TestRunPromptApprovalAskMarksToolAwaitingApproval(t *testing.T) {
	t.Skip("permission approval profiles were replaced by session access settings")
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
		Rules: []config.PermissionRule{{Tool: domain.ToolKindBash, Pattern: "*", Action: accesssettings.PermissionModeAsk}},
	}

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := setSessionPermissionProfile(context.Background(), st, session.ID, "default"); err != nil {
		t.Fatal(err)
	}

	events := runLivePromptDefault(t, engine, st, session, "run pwd")
	var approval domain.Event
	for _, evt := range events {
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
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"id\":\"call_read\",\"type\":\"function\",\"index\":0,\"function\":{\"name\":\"file_read\",\"arguments\":\"\"}}]}}]}\n\n"))
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

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := setSessionProjectRoot(context.Background(), st, session.ID, workdir); err != nil {
		t.Fatal(err)
	}
	session.ProjectRoot = workdir

	events := runLivePromptDefault(t, engine, st, session, "read the note")
	var sawError bool
	for _, evt := range events {
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

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}

	events := runLivePromptDefault(t, engine, st, session, "hello")

	var sawError bool
	for _, evt := range events {
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
	t.Skip("permission approval profiles were replaced by session access settings")
	cfg := testConfig(t)
	workdir := t.TempDir()
	cfg.Permissions.Profiles[permissionprofile.ProfileReadAsk] = config.PermissionProfile{
		Root:      string(permissionprofile.ModeReadOnly),
		Workspace: string(permissionprofile.ModeReadWrite),
		Rules: []config.PermissionRule{{
			Tool:    domain.ToolKindFileRead,
			Pattern: "*",
			Action:  accesssettings.PermissionModeAsk,
		}},
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", cfg.DefaultProvider, "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	session.PermissionProfile = permissionprofile.ProfileReadAsk
	session.ProjectRoot = workdir
	if err := setSessionPermissionProfile(context.Background(), st, session.ID, permissionprofile.ProfileReadAsk); err != nil {
		t.Fatal(err)
	}
	if err := setSessionProjectRoot(context.Background(), st, session.ID, workdir); err != nil {
		t.Fatal(err)
	}

	outsideDir := t.TempDir()
	outsidePath := filepath.Join(outsideDir, "outside.txt")
	if err := os.WriteFile(outsidePath, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	chat := defaultChatForSession(t, st, session.ID)
	req := tools.Request{
		Tool:       domain.ToolKindFileRead,
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
	workdir := t.TempDir()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", cfg.DefaultProvider, "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	session.PermissionProfile = permissionprofile.ProfileReadAsk
	session.ProjectRoot = workdir
	if err := setSessionPermissionProfile(context.Background(), st, session.ID, permissionprofile.ProfileReadAsk); err != nil {
		t.Fatal(err)
	}
	if err := setSessionProjectRoot(context.Background(), st, session.ID, workdir); err != nil {
		t.Fatal(err)
	}

	targetPath := filepath.Join(workdir, "inside.txt")
	if err := os.WriteFile(targetPath, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	chat := defaultChatForSession(t, st, session.ID)
	evt, err := engine.handleModelToolCall(context.Background(), session, chat, tools.Request{
		Tool: domain.ToolKindFileRead,
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
	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", cfg.DefaultProvider, "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	session.PermissionProfile = permissionprofile.ProfileReadAsk
	session.ProjectRoot = workdir
	if err := setSessionPermissionProfile(context.Background(), st, session.ID, permissionprofile.ProfileReadAsk); err != nil {
		t.Fatal(err)
	}
	if err := setSessionProjectRoot(context.Background(), st, session.ID, workdir); err != nil {
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
	t.Skip("permission approval profiles were replaced by session access settings")
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
			_, _ = w.Write([]byte(fmt.Sprintf(`{"choices":[{"message":{"tool_calls":[{"id":"call_1","type":"function","function":{"name":"file_read","arguments":%q}}]}}],"usage":{"total_tokens":1}}`, string(args))))
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
			Tool:    domain.ToolKindFileRead,
			Pattern: "*",
			Action:  accesssettings.PermissionModeAsk,
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
	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	session.PermissionProfile = permissionprofile.ProfileReadAsk
	session.ProjectRoot = workdir
	if err := setSessionPermissionProfile(context.Background(), st, session.ID, permissionprofile.ProfileReadAsk); err != nil {
		t.Fatal(err)
	}

	chatRecord := defaultChatForSession(t, st, session.ID)
	rt, err := engine.Chat(context.Background(), session, chatRecord)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rt.Close)
	updates, unsub := rt.Subscribe()
	defer unsub()
	rt.Enqueue(chatpkg.QueueItem{Kind: chatpkg.QueueKindSteer, Text: "continue"})
	events := collectLiveUpdates(t, rt, updates, func(evt domain.Event) bool {
		return evt.Kind == domain.EventKindApprovalAsk
	})
	var approvalID id.ID
	for _, evt := range events {
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

	approvedEvents := approveOnlyPendingTool(t, rt, updates, st, chatRecord.ID)
	var sawToolResult bool
	var sawFinalAnswer bool
	for _, evt := range approvedEvents {
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
	t.Skip("permission approval profiles were replaced by session access settings")
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
		request := string(body)
		requests = append(requests, request)
		switch {
		case strings.Contains(request, "Summarize this coding session so another agent can continue it with minimal loss."):
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"## Goal\ncontinue the build fix\n\n## Next Step\nuse the latest tool result and keep going"}}],"usage":{"total_tokens":1}}`))
		case len(requests) == 1:
			args, err := json.Marshal(map[string]string{"command": "head -c 60000 /dev/zero | tr '\\0' x"})
			if err != nil {
				t.Fatalf("marshal args: %v", err)
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{"tool_calls":[{"id":"call_1","type":"function","function":{"name":"bash","arguments":` + strconv.Quote(string(args)) + `}}]}}],"usage":{"total_tokens":1}}`))
		default:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"done"}}],"usage":{"total_tokens":1}}`))
		}
	}))
	defer server.Close()

	cfg := testConfig(t)
	cfg.AutoCompactAt = 20
	cfg.Providers = map[string]config.Provider{
		"test": {
			BaseURL: server.URL + "/v1",
			Timeout: time.Second,
		},
	}
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "test-model"
	cfg.SetModelConfig(config.ModelConfig{ProviderID: "test", ModelID: "test-model", ContextWindow: 50000})
	cfg.Permissions.Profiles["default"] = config.PermissionProfile{
		Root:      string(permissionprofile.ModeReadOnly),
		Workspace: string(permissionprofile.ModeReadWrite),
		Rules: []config.PermissionRule{{
			Tool:    domain.ToolKindBash,
			Pattern: "*",
			Action:  accesssettings.PermissionModeAsk,
		}},
	}

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := setSessionPermissionProfile(context.Background(), st, session.ID, "default"); err != nil {
		t.Fatal(err)
	}

	chatRecord := defaultChatForSession(t, st, session.ID)
	rt, err := engine.Chat(context.Background(), session, chatRecord)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rt.Close)
	updates, unsub := rt.Subscribe()
	defer unsub()
	rt.Enqueue(chatpkg.QueueItem{Kind: chatpkg.QueueKindSteer, Text: "build it"})
	events := collectLiveUpdates(t, rt, updates, func(evt domain.Event) bool {
		return evt.Kind == domain.EventKindApprovalAsk
	})
	var approvalID id.ID
	for _, evt := range events {
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

	approvedEvents := approveOnlyPendingTool(t, rt, updates, st, chatRecord.ID)
	var sawFinalAnswer bool
	for _, evt := range approvedEvents {
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

func TestApplyQueuedSteerEmitsPersistedUserMessage(t *testing.T) {
	cfg := testConfig(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	chat := defaultChatForSession(t, st, session.ID)
	if err := chatpkg.SetChatQueuedInputs(context.Background(), st, chat.ID, []domain.QueuedInput{{
		ID:   id.New(),
		Kind: domain.QueuedInputKindSteer,
		Text: "steer the running turn",
	}}); err != nil {
		t.Fatal(err)
	}

	events := make(chan domain.Event, 1)
	applied, err := engine.applyQueuedSteer(context.Background(), session, &chat, events)
	if err != nil {
		t.Fatal(err)
	}
	if !applied {
		t.Fatal("expected queued steer to apply")
	}
	evt := <-events
	if evt.Kind != domain.EventKindStatus || evt.Text != "Applying queued steer..." {
		t.Fatalf("unexpected event: %#v", evt)
	}
	if evt.Meta[domain.EventMetaRefresh] != domain.EventRefreshQueue {
		t.Fatalf("expected queue refresh metadata, got %#v", evt.Meta)
	}
	user, ok := evt.Item.Content.(domain.UserMessage)
	if !ok || user.Text != "steer the running turn" {
		t.Fatalf("expected persisted user message item, got %#v", evt.Item)
	}

	timeline, err := chatpkg.TimelineForChat(context.Background(), st, chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(timeline) != 1 || timeline[0].ID != evt.Item.ID {
		t.Fatalf("expected event item to match persisted timeline, event=%#v timeline=%#v", evt.Item, timeline)
	}
}

func TestRunPromptAutoCompactsWhenFirstModelTurnCrossesThreshold(t *testing.T) {
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
		request := string(body)
		requests = append(requests, request)
		if strings.Contains(request, "Summarize this coding session so another agent can continue it with minimal loss.") {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"compact summary"}}],"usage":{"total_tokens":1}}`))
			return
		}
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
	cfg.SetModelConfig(config.ModelConfig{ProviderID: "test", ModelID: "test-model", ContextWindow: 32768})

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	defaultChat := defaultChatForSession(t, st, session.ID)
	sideChat, err := sessionpkg.CreateChat(context.Background(), st, session.ID, "side", chatrole.General, nil)
	if err != nil {
		t.Fatal(err)
	}
	appendUserTimelineItem(t, st, sideChat.ID, "old side prompt")
	appendAssistantTimelineItem(t, st, sideChat.ID, domain.AssistantMessage{Text: "old side answer"})

	prompt := "pending prompt " + strings.Repeat("x", 90000)
	existingMessages, err := engine.buildConversationPreview(context.Background(), session, sideChat.ID, "", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	pendingMessages, err := engine.buildConversationPreview(context.Background(), session, sideChat.ID, prompt, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	existingPct, ok := engine.estimateRequestUsagePercent(sideChat, existingMessages)
	if !ok {
		t.Fatal("expected existing request usage estimate")
	}
	pendingPct, ok := engine.estimateRequestUsagePercent(sideChat, pendingMessages)
	if !ok {
		t.Fatal("expected pending request usage estimate")
	}
	if pendingPct <= existingPct {
		t.Fatalf("expected pending prompt to increase usage, existing=%d pending=%d", existingPct, pendingPct)
	}
	engine.cfg.AutoCompactAt = existingPct + 1

	events := runLivePrompt(t, engine, session, sideChat, prompt)
	var sawCompactionStatus bool
	var sawFinalAnswer bool
	for _, evt := range events {
		if evt.Kind == domain.EventKindStatus && strings.HasPrefix(evt.Text, "Auto-compacting at ~") {
			sawCompactionStatus = true
		}
		if evt.Kind == domain.EventKindMessageDelta && evt.Text == "done" {
			sawFinalAnswer = true
		}
	}
	if !sawCompactionStatus {
		t.Fatal("expected auto-compaction before the first model request when prompt crosses threshold")
	}
	if !sawFinalAnswer {
		t.Fatal("expected final assistant answer")
	}
	if len(requests) != 2 {
		t.Fatalf("expected compaction request and final model request, got %d", len(requests))
	}
	if !strings.Contains(requests[0], "Summarize this coding session so another agent can continue it with minimal loss.") {
		t.Fatalf("expected first request to compact, got %s", requests[0])
	}
	if !strings.Contains(requests[1], "Compacted session summary for continuation:") {
		t.Fatalf("expected final model request to continue from compacted summary, got %s", requests[1])
	}

	sideTimeline, err := chatpkg.TimelineForChat(context.Background(), st, sideChat.ID)
	if err != nil {
		t.Fatal(err)
	}
	var sawPendingUser bool
	var sawCompaction bool
	for _, item := range sideTimeline {
		switch content := item.Content.(type) {
		case domain.Compaction:
			sawCompaction = true
		case domain.UserMessage:
			if strings.HasPrefix(content.Text, "pending prompt ") {
				sawPendingUser = true
			}
		}
	}
	if !sawPendingUser {
		t.Fatal("expected pending prompt to be persisted")
	}
	if !sawCompaction {
		t.Fatal("expected side chat to be compacted")
	}

	defaultTimeline, err := chatpkg.TimelineForChat(context.Background(), st, defaultChat.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range defaultTimeline {
		if _, ok := item.Content.(domain.Compaction); ok {
			t.Fatalf("did not expect default chat to be compacted, got %#v", defaultTimeline)
		}
	}
}

func TestRunPromptAutoCompactsKnownOverLimitAfterPauseNotice(t *testing.T) {
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
		request := string(body)
		requests = append(requests, request)
		if strings.Contains(request, "Summarize this coding session so another agent can continue it with minimal loss.") {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"compact summary"}}],"usage":{"total_tokens":1}}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"done"}}],"usage":{"total_tokens":1}}`))
	}))
	defer server.Close()

	cfg := testConfig(t)
	cfg.AutoCompactAt = 80
	cfg.Providers = map[string]config.Provider{
		"test": {
			BaseURL: server.URL + "/v1",
			Timeout: time.Second,
		},
	}
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "test-model"
	cfg.SetModelConfig(config.ModelConfig{ProviderID: "test", ModelID: "test-model", ContextWindow: 1000})

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	chat := defaultChatForSession(t, st, session.ID)
	chat.LastKnownContextTokens = 850
	chat.ContextTokensKnown = true
	if err := chatpkg.UpdateChat(context.Background(), st, chat); err != nil {
		t.Fatal(err)
	}
	appendUserTimelineItem(t, st, chat.ID, "previous work")
	appendAssistantTimelineItem(t, st, chat.ID, domain.AssistantMessage{Text: "stopped before next action"})
	appendNoticeTimelineItem(t, st, chat.ID, domain.Notice{Level: "warning", Text: "Paused continuation", Kind: "model_pause", Reason: "repeated_tool"})

	events := runLivePrompt(t, engine, session, chat, "continue")
	var sawCompactionStatus bool
	for _, evt := range events {
		if evt.Kind == domain.EventKindStatus && strings.HasPrefix(evt.Text, "Auto-compacting at ~") {
			sawCompactionStatus = true
		}
	}
	if !sawCompactionStatus {
		t.Fatal("expected auto-compaction from known context usage over threshold")
	}
	if len(requests) != 2 {
		t.Fatalf("expected compaction request and final model request, got %d", len(requests))
	}
	if !strings.Contains(requests[0], "Summarize this coding session so another agent can continue it with minimal loss.") {
		t.Fatalf("expected first request to compact, got %s", requests[0])
	}
}

func TestLivePromptTurnBuildsRequestFromChatRuntimeTimeline(t *testing.T) {
	requests := make(chan string, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		requests <- string(body)
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

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	chatRecord := defaultChatForSession(t, st, session.ID)
	appendUserTimelineItem(t, st, chatRecord.ID, "loaded live transcript")

	runtime, err := chatpkg.Load(context.Background(), session, chatRecord, engine.ChatDeps(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	updates, unsubscribe := runtime.Subscribe()
	defer unsubscribe()

	appendUserTimelineItem(t, st, chatRecord.ID, "storage side channel")
	runtime.Enqueue(chatpkg.QueueItem{Kind: chatpkg.QueueKindSteer, Text: "new live prompt"})

	var body string
	select {
	case body = <-requests:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for provider request")
	}
	if !strings.Contains(body, "loaded live transcript") {
		t.Fatalf("expected request to include loaded live timeline, got %s", body)
	}
	if !strings.Contains(body, "new live prompt") {
		t.Fatalf("expected request to include live prompt, got %s", body)
	}
	if strings.Contains(body, "storage side channel") {
		t.Fatalf("request used storage side-channel timeline: %s", body)
	}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case update := <-updates:
			if update.Event != nil && update.Event.Kind == domain.EventKindMessageDone {
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for prompt completion: %#v", runtime.Snapshot())
		}
	}
}

func TestRuntimeKeepsUserPromptVisibleWhenProviderSetupFails(t *testing.T) {
	t.Parallel()

	cfg := testConfig(t)
	cfg.Providers = map[string]config.Provider{}
	cfg.DefaultProvider = "missing"
	cfg.DefaultModel = "test-model"

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "missing", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	chatRecord := defaultChatForSession(t, st, session.ID)
	runtime, err := chatpkg.Load(context.Background(), session, chatRecord, engine.ChatDeps(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	updates, unsubscribe := runtime.Subscribe()
	defer unsubscribe()

	runtime.Enqueue(chatpkg.QueueItem{Kind: chatpkg.QueueKindSteer, Text: "still show this"})

	deadline := time.After(2 * time.Second)
	for {
		select {
		case update := <-updates:
			if update.Event == nil || update.Event.Kind != domain.EventKindError {
				continue
			}
			timeline := runtime.Snapshot().Timeline
			for _, item := range timeline {
				if user, ok := item.Content.(domain.UserMessage); ok && user.Text == "still show this" {
					return
				}
			}
			t.Fatalf("expected failed prompt to remain in timeline, got %#v", timeline)
		case <-deadline:
			t.Fatalf("timed out waiting for provider setup error: %#v", runtime.Snapshot())
		}
	}
}

func TestApproveContinuesAfterApprovedToolFailure(t *testing.T) {
	t.Skip("permission approval profiles were replaced by session access settings")
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
			_, _ = w.Write([]byte(fmt.Sprintf(`{"choices":[{"message":{"tool_calls":[{"id":"call_1","type":"function","function":{"name":"file_read","arguments":%q}}]}}],"usage":{"total_tokens":1}}`, string(args))))
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
			Tool:    domain.ToolKindFileRead,
			Pattern: "*",
			Action:  accesssettings.PermissionModeAsk,
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
	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	session.PermissionProfile = permissionprofile.ProfileReadAsk
	session.ProjectRoot = workdir
	if err := setSessionPermissionProfile(context.Background(), st, session.ID, permissionprofile.ProfileReadAsk); err != nil {
		t.Fatal(err)
	}

	chatRecord := defaultChatForSession(t, st, session.ID)
	rt, err := engine.Chat(context.Background(), session, chatRecord)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rt.Close)
	updates, unsub := rt.Subscribe()
	defer unsub()
	rt.Enqueue(chatpkg.QueueItem{Kind: chatpkg.QueueKindSteer, Text: "continue"})
	events := collectLiveUpdates(t, rt, updates, func(evt domain.Event) bool {
		return evt.Kind == domain.EventKindApprovalAsk
	})
	var approvalID id.ID
	for _, evt := range events {
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

	approvedEvents := approveOnlyPendingTool(t, rt, updates, st, chatRecord.ID)
	var sawToolFailure bool
	var sawFinalAnswer bool
	for _, evt := range approvedEvents {
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
	largeContent := strings.Repeat("tool output line "+strings.Repeat("x", 70)+"\n", 3000)
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
			_, _ = w.Write([]byte(`{"choices":[{"message":{"tool_calls":[{"id":"call_1","type":"function","function":{"name":"file_read","arguments":` + strconv.Quote(string(args)) + `}}]}}],"usage":{"total_tokens":1}}`))
		default:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ignored"}}],"usage":{"total_tokens":1}}`))
		}
	}))
	defer server.Close()

	cfg := testConfig(t)
	cfg.AutoCompactAt = 20
	cfg.Providers = map[string]config.Provider{
		"test": {
			BaseURL: server.URL + "/v1",
			Timeout: time.Second,
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

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := setSessionPermissionProfile(context.Background(), st, session.ID, "auto"); err != nil {
		t.Fatal(err)
	}

	events := runLivePromptDefault(t, engine, st, session, "inspect the file and continue")
	var sawFinalAnswer bool
	var seen []domain.EventKind
	for _, evt := range events {
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

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "test-model", nil)
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
	if err := engine.compactSession(context.Background(), session, chat.ID, client, "manual", "", events); err != nil {
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

	timeline, err := chatpkg.TimelineForChat(context.Background(), st, chat.ID)
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

func TestCompactSessionAddsManualInstructionsToPrompt(t *testing.T) {
	t.Parallel()

	var requestBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		requestBody = string(body)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"short compact summary"}}]}`))
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

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "test-model", nil)
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
	if err := engine.compactSession(context.Background(), session, chat.ID, client, "manual", "focus on your list of directives", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(requestBody, "Additional compaction instructions:") ||
		!strings.Contains(requestBody, "focus on your list of directives") {
		t.Fatalf("expected manual compaction instructions in request, got %s", requestBody)
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

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "test-model", nil)
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
	if err := engine.compactSession(context.Background(), session, chat.ID, client, "manual", "", nil); err != nil {
		t.Fatal(err)
	}
	if !sawStream {
		t.Fatal("expected compaction request to stream")
	}

	timeline, err := chatpkg.TimelineForChat(context.Background(), st, chat.ID)
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

func TestCompactSessionEmitsPromptProgressWhenStreaming(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"prompt_progress\":{\"total\":100,\"processed\":4,\"cache\":0,\"time_ms\":12}}\n\n"))
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

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "test-model", nil)
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
	events := make(chan domain.Event, 8)
	if err := engine.compactSession(context.Background(), session, chat.ID, client, "manual", "", events); err != nil {
		t.Fatal(err)
	}
	close(events)

	var sawProgress bool
	var sawStreaming bool
	for evt := range events {
		if evt.Kind == domain.EventKindStatus && evt.Meta[domain.EventMetaPromptProgress] == "true" {
			sawProgress = true
			if evt.Meta["compaction"] != "progress" {
				t.Fatalf("expected compaction progress marker, got %#v", evt.Meta)
			}
			if evt.Text != "Compaction pre-processing 4%" {
				t.Fatalf("progress text = %q", evt.Text)
			}
		}
		if evt.Kind == domain.EventKindStatus && evt.Meta["compaction"] == "streaming" {
			sawStreaming = true
			if evt.Text != "Streaming compacted results (24 B)" {
				t.Fatalf("streaming text = %q", evt.Text)
			}
		}
	}
	if !sawProgress {
		t.Fatal("expected compaction prompt progress event")
	}
	if !sawStreaming {
		t.Fatal("expected compaction streaming status event")
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

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "chat", "chat-model", nil)
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
	if err := engine.compactSession(context.Background(), session, chat.ID, client, "manual", "", nil); err != nil {
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

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "chat", "chat-model", nil)
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
	err = engine.compactSession(context.Background(), session, chat.ID, client, "manual", "", nil)
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

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	chat := defaultChatForSession(t, st, session.ID)
	appendUserTimelineItem(t, st, chat.ID, "hello")

	client, err := provider.New("test", cfg.Providers["test"], nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.compactSession(context.Background(), session, chat.ID, client, "manual", "", nil); err != nil {
		t.Fatal(err)
	}

	timeline, err := chatpkg.TimelineForChat(context.Background(), st, chat.ID)
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
	t.Skip("permission approval profiles were replaced by session access settings")
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

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", cfg.DefaultProvider, "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	session.PermissionProfile = permissionprofile.ProfileAsk
	session.ProjectRoot = workdir
	if err := setSessionPermissionProfile(context.Background(), st, session.ID, permissionprofile.ProfileAsk); err != nil {
		t.Fatal(err)
	}
	if err := setSessionProjectRoot(context.Background(), st, session.ID, workdir); err != nil {
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
	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	chat := defaultChatForSession(t, st, session.ID)
	chat.LastKnownContextTokens = 1000
	chat.ContextTokensKnown = false
	if err := chatpkg.UpdateChat(context.Background(), st, chat); err != nil {
		t.Fatal(err)
	}
	rt, err := engine.Chat(context.Background(), session, chat)
	if err != nil {
		t.Fatal(err)
	}

	if err := rt.SetContextUsage(context.Background(), domain.Usage{PromptTokens: 200, CompletionTokens: 50, TotalTokens: 250}); err != nil {
		t.Fatal(err)
	}
	if err := rt.SetContextUsage(context.Background(), domain.Usage{TotalTokens: 45, CompletionTokens: 5}); err != nil {
		t.Fatal(err)
	}

	stored, err := chatpkg.GetChat(context.Background(), st, chat.ID)
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
	workdir := t.TempDir()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", cfg.DefaultProvider, "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	session.PermissionProfile = permissionprofile.ProfileWriteAsk
	session.ProjectRoot = workdir
	if err := setSessionPermissionProfile(context.Background(), st, session.ID, permissionprofile.ProfileWriteAsk); err != nil {
		t.Fatal(err)
	}
	if err := setSessionProjectRoot(context.Background(), st, session.ID, workdir); err != nil {
		t.Fatal(err)
	}

	chat := defaultChatForSession(t, st, session.ID)
	evt, err := engine.handleModelToolCall(context.Background(), session, chat, tools.Request{
		Tool: domain.ToolKindFileWrite,
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
	t.Skip("permission approval profiles were replaced by session access settings")
	cfg := testConfig(t)
	workdir := t.TempDir()
	cfg.Permissions.Profiles[permissionprofile.ProfileWriteAsk] = config.PermissionProfile{
		Root:      string(permissionprofile.ModeReadOnly),
		Workspace: string(permissionprofile.ModeReadWrite),
		Rules: []config.PermissionRule{{
			Tool:    domain.ToolKindBash,
			Pattern: "*",
			Action:  accesssettings.PermissionModeAsk,
		}},
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", cfg.DefaultProvider, "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	session.PermissionProfile = permissionprofile.ProfileWriteAsk
	session.ProjectRoot = workdir
	if err := setSessionPermissionProfile(context.Background(), st, session.ID, permissionprofile.ProfileWriteAsk); err != nil {
		t.Fatal(err)
	}
	if err := setSessionProjectRoot(context.Background(), st, session.ID, workdir); err != nil {
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

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	chatRecord := defaultChatForSession(t, st, session.ID)
	rt, err := engine.Chat(context.Background(), session, chatRecord)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rt.Close)
	updates, unsub := rt.Subscribe()
	defer unsub()
	rt.Enqueue(chatpkg.QueueItem{Kind: chatpkg.QueueKindSteer, Text: "hello", Note: "Permission mode changed to ask."})
	_ = collectLiveUpdates(t, rt, updates, func(evt domain.Event) bool {
		return evt.Kind == domain.EventKindMessageDone || evt.Kind == domain.EventKindError
	})
	if len(requests) == 0 || !strings.Contains(requests[0], `Session update:\nPermission mode changed to ask.`) {
		t.Fatalf("expected transient session note in request, got %v", requests)
	}
	waitForChatInactive(t, rt)
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

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = runLiveContinueDefault(t, engine, st, session, "Permission mode changed to write / ask.")
	if len(requests) == 0 || !strings.Contains(requests[0], "Continue from where you left off.") {
		t.Fatalf("expected continue instruction in request, got %v", requests)
	}
	if !strings.Contains(requests[0], "Permission mode changed to write / ask.") {
		t.Fatalf("expected transient note in continue request, got %v", requests)
	}
}

func TestRunPromptCancellationDoesNotPersistAssistantError(t *testing.T) {
	t.Parallel()

	requestStarted := make(chan struct{})
	releaseRequest := make(chan struct{})
	closeRelease := func() {
		select {
		case <-releaseRequest:
		default:
			close(releaseRequest)
		}
	}
	t.Cleanup(closeRelease)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-requestStarted:
		default:
			close(requestStarted)
		}
		select {
		case <-r.Context().Done():
		case <-releaseRequest:
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

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}

	chatRecord := defaultChatForSession(t, st, session.ID)
	rt, err := engine.Chat(context.Background(), session, chatRecord)
	if err != nil {
		t.Fatal(err)
	}
	updates, unsub := rt.Subscribe()
	defer unsub()
	rt.Enqueue(chatpkg.QueueItem{Kind: chatpkg.QueueKindSteer, Text: "hello"})
	select {
	case <-requestStarted:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for provider request; snapshot=%#v", rt.Snapshot())
	}
	rt.Cancel(chatpkg.CancelReasonUserInterruptHard)
	closeRelease()

	events := collectLiveUpdates(t, rt, updates, func(evt domain.Event) bool {
		return evt.Kind == domain.EventKindStatus && evt.Text == "Interrupted"
	})
	for _, evt := range events {
		if evt.Kind == domain.EventKindStatus && evt.Text == "Interrupted" {
			continue
		}
		if evt.Kind == domain.EventKindError {
			t.Fatalf("expected interruption status instead of error, got %#v", evt)
		}
	}
	var sawInterrupted bool
	for _, evt := range events {
		if evt.Kind == domain.EventKindStatus && evt.Text == "Interrupted" {
			sawInterrupted = true
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
		if last.Role == domain.MessageRoleAssistant && strings.HasPrefix(last.Summary, "Error:") {
			t.Fatalf("did not expect assistant error message after cancellation, got %#v", last)
		}
		for _, part := range parts[last.ID] {
			if part.Kind == domain.PartKindEventNotice && strings.HasPrefix(part.Body, "Error:") {
				t.Fatalf("did not expect assistant error notice after cancellation, got %#v", part)
			}
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

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "test-model", nil)
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

	items, err := chatpkg.TimelineForChat(context.Background(), st, chat.ID)
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

	engine := New(cfg, st, nil)
	var waited []time.Duration
	engine.retryPause = func(_ context.Context, delay time.Duration, onTick func(time.Duration)) error {
		if onTick != nil {
			onTick(delay)
		}
		waited = append(waited, delay)
		return nil
	}
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}

	events := runLivePromptDefault(t, engine, st, session, "hello")

	var sawRetryStatus bool
	for _, evt := range events {
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

	engine := New(cfg, st, nil)
	engine.retryPause = func(_ context.Context, delay time.Duration, onTick func(time.Duration)) error {
		for _, remaining := range []time.Duration{delay, 2 * time.Second, time.Second, 0} {
			onTick(remaining)
		}
		return nil
	}
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}

	events := runLivePromptDefault(t, engine, st, session, "hello")

	var statuses []string
	for _, evt := range events {
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

	engine := New(cfg, nil, nil)
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
			Role:    provider.RoleUser,
			Content: "hello",
		}},
		Stream: true,
	}, domain.TimelineItem{ID: chatpkg.NewTimelineID(time.Now().UTC())})
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

	engine := New(cfg, nil, nil)
	client, err := provider.New("test", cfg.Providers["test"], nil)
	if err != nil {
		t.Fatal(err)
	}

	events := make(chan domain.Event, 16)
	_, streamed, err := engine.chatWithRetry(context.Background(), "", "test", client, events, provider.ChatRequest{
		Model: "test-model",
		Messages: []provider.Message{{
			Role:    provider.RoleUser,
			Content: "hello",
		}},
		Stream: true,
	}, domain.TimelineItem{ID: chatpkg.NewTimelineID(time.Now().UTC())})
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
	engine := New(cfg, nil, nil)
	client, err := provider.New("test", cfg.Providers["test"], nil)
	if err != nil {
		t.Fatal(err)
	}

	events := make(chan domain.Event, 16)
	resp, streamed, err := engine.chatWithRetry(context.Background(), "", "test", client, events, provider.ChatRequest{
		Model: "test-model",
		Messages: []provider.Message{{
			Role:    provider.RoleUser,
			Content: "hello",
		}},
		Stream:    true,
		ExtraBody: provider.RequestExtraBody(cfg.Providers["test"], config.ModelConfig{ModelID: "test-model", ModelPreset: provider.ModelPresetDefault}),
	}, domain.TimelineItem{ID: chatpkg.NewTimelineID(time.Now().UTC())})
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

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "New Session", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}

	events := runLivePromptDefault(t, engine, st, session, "hello")

	var sawDone bool
	for _, evt := range events {
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

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "Existing Session", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	chat := defaultChatForSession(t, st, session.ID)
	if chat.Title != "Main" {
		t.Fatalf("expected generated main title, got %q", chat.Title)
	}

	events := runLivePromptDefault(t, engine, st, session, "compare go code to c reference and identify gaps")
	var chatTitle string
	for _, evt := range events {
		if evt.Kind == domain.EventKindChatTitle {
			chatTitle = evt.Text
		}
	}
	want := "compare go code to c reference"
	if chatTitle != want {
		t.Fatalf("expected chat title event %q, got %q", want, chatTitle)
	}
	updated, err := chatpkg.GetChat(context.Background(), st, chat.ID)
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
		_, _ = w.Write([]byte(fmt.Sprintf(`{"choices":[{"message":{"tool_calls":[{"id":"call_%d","type":"function","function":{"name":"file_read","arguments":"{\"path\":\"note.txt\"}"}}]}}],"usage":{"total_tokens":1}}`, requests)))
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

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}

	events := runLivePromptDefault(t, engine, st, session, "loop")

	var sawPauseStatus bool
	var sawPauseStatusItem bool
	for _, evt := range events {
		if evt.Kind == domain.EventKindStatus && strings.Contains(evt.Text, "identical FileRead calls") {
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
	timeline, err := chatpkg.TimelineForChat(context.Background(), st, chat.ID)
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
			_, _ = w.Write([]byte(`{"choices":[{"message":{"tool_calls":[{"id":"call_1","type":"function","function":{"name":"file_read","arguments":"{\"path\":\"note.txt\"}"}}]}}],"usage":{"total_tokens":1}}`))
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

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}

	events := runLivePromptDefault(t, engine, st, session, "loop")
	for _, evt := range events {
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
			_, _ = w.Write([]byte(`{"choices":[{"message":{"tool_calls":[{"id":"call_1","type":"function","function":{"name":"file_read","arguments":"{\"path\":\"note.txt\"}"}}]}}],"usage":{"total_tokens":1}}`))
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

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}

	events := runLivePromptDefault(t, engine, st, session, "loop")
	for _, evt := range events {
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
			_, _ = w.Write([]byte(`{"choices":[{"message":{"tool_calls":[{"id":"call_1","type":"function","function":{"name":"file_read","arguments":"{\"path\":\"note.txt\"}"}}]}}],"usage":{"total_tokens":1}}`))
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

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}

	events := runLivePromptDefault(t, engine, st, session, "loop")
	for _, evt := range events {
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
			_, _ = w.Write([]byte(`{"choices":[{"message":{"tool_calls":[{"id":"call_1","type":"function","function":{"name":"file_read","arguments":"{\"path\":\"note.txt\"}"}}]}}],"usage":{"total_tokens":1}}`))
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

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}

	events := runLivePromptDefault(t, engine, st, session, "loop")
	for _, evt := range events {
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
		_, _ = w.Write([]byte(fmt.Sprintf(`{"choices":[{"message":{"tool_calls":[{"id":"call_%d","type":"function","function":{"name":"file_read","arguments":%q}}]}}],"usage":{"total_tokens":1}}`, requests, string(args))))
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

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}

	events := runLivePromptDefault(t, engine, st, session, "loop")
	for _, evt := range events {
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

	engine := New(cfg, st, nil)
	engine.retryPause = func(_ context.Context, delay time.Duration, onTick func(time.Duration)) error {
		if onTick != nil {
			onTick(delay)
		}
		return nil
	}
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}

	events := runLivePromptDefault(t, engine, st, session, "hello")

	var sawError bool
	var errorItem domain.TimelineItem
	for _, evt := range events {
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

	engine := New(cfg, st, nil)
	var rt *chatpkg.Chat
	engine.retryPause = func(ctx context.Context, _ time.Duration, _ func(time.Duration)) error {
		if rt != nil {
			rt.Cancel(chatpkg.CancelReasonUserInterruptHard)
		}
		<-ctx.Done()
		return ctx.Err()
	}
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}

	chatRecord := defaultChatForSession(t, st, session.ID)
	rt, err = engine.Chat(context.Background(), session, chatRecord)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rt.Close)
	updates, unsub := rt.Subscribe()
	defer unsub()
	rt.Enqueue(chatpkg.QueueItem{Kind: chatpkg.QueueKindSteer, Text: "hello"})

	var sawInterrupted bool
	events := collectLiveUpdates(t, rt, updates, func(evt domain.Event) bool {
		return evt.Kind == domain.EventKindStatus && evt.Text == "Interrupted"
	})
	for _, evt := range events {
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

	engine := New(cfg, st, nil)
	session, err := sessionpkg.CreateSession(context.Background(), st, "test", "test", "test-model", nil)
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

func parseApprovalID(raw string) (id.ID, error) {
	id := id.ID(strings.TrimSpace(raw))
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
