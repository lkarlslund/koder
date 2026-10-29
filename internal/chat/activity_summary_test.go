package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/provider"
	_ "github.com/lkarlslund/koder/internal/tools/all"
)

func TestActivityNotesDescribeTheCurrentTurn(t *testing.T) {
	items := []domain.TimelineItem{
		{ID: "old", Content: domain.UserMessage{Text: "an earlier request"}},
		{ID: "old-answer", Content: domain.AssistantMessage{Text: "earlier answer"}},
		{ID: "task", Content: domain.UserMessage{Text: "Fix the flaky\nsession tests"}},
		{ID: "a1", Content: domain.AssistantMessage{Text: "Looking at the tests first.", Tools: []domain.ToolCall{
			{Tool: domain.ToolKindExecCommand, Args: map[string]string{"cmd": "go test ./internal/session/"}, Status: domain.ToolStatusErrored},
			{Tool: domain.ToolKindFileRead, Args: map[string]string{"path": "internal/session/session.go"}, Status: domain.ToolStatusDone},
		}}},
		{ID: "a2", Content: domain.AssistantMessage{Tools: []domain.ToolCall{
			{Tool: domain.ToolKindExecCommand, Args: map[string]string{"cmd": "go test -run TestIdle ./internal/session/"}, Status: domain.ToolStatusRunning},
		}}},
	}
	notes := activityNotes(items)
	for _, want := range []string{
		"<task>\nFix the flaky ↵ session tests\n</task>",
		"said: Looking at the tests first.",
		"go test ./internal/session/ (errored)",
		"internal/session/session.go",
		"<started_not_finished>\n",
		"go test -run TestIdle ./internal/session/",
	} {
		if !strings.Contains(notes, want) {
			t.Fatalf("notes lack %q:\n%s", want, notes)
		}
	}
	if strings.Contains(notes, "earlier") {
		t.Fatalf("notes include an earlier turn:\n%s", notes)
	}
	if got := activityNotes(items[:3]); got != "" {
		t.Fatalf("notes for a turn without steps = %q", got)
	}
}

func TestActivityLabelAcceptsOneShortLine(t *testing.T) {
	for reply, want := range map[string]string{
		`"Running session tests."`:          "Running session tests",
		"  Reading chat.go  ":               "Reading chat.go",
		"Reading chat.go\nThen editing it":  "",
		"# Heading":                         "",
		"```Reading```":                     "",
		strings.Repeat("Reading files ", 9): "",
	} {
		if got := activityLabel(reply); got != want {
			t.Errorf("activityLabel(%q) = %q, want %q", reply, got, want)
		}
	}
}

func TestRunningToolStatusTextShowsThePreview(t *testing.T) {
	if got := runningToolStatusText(domain.ToolKindExecCommand, "go test ./...\nsecond line"); got != "Running ExecCommand: go test ./..." {
		t.Fatalf("status = %q", got)
	}
	if got := runningToolStatusText(domain.ToolKindExecCommand, ""); got != "Running ExecCommand" {
		t.Fatalf("status = %q", got)
	}
}

type activityRunner struct {
	runtimeFakeRunner
	client *provider.Client
}

func (r *activityRunner) ClientForChat(domain.Chat) (*provider.Client, error) { return r.client, nil }

func (r *activityRunner) ChatRequest(_ domain.Session, _ domain.Chat, messages []provider.Message, _ bool) provider.ChatRequest {
	return provider.ChatRequest{Model: "test-model", Messages: messages, Tools: []provider.ToolDefinition{{Type: "function"}}, ToolChoice: "auto"}
}

func TestActivitySummaryLabelsABusyChatOnATimer(t *testing.T) {
	previous := activitySummaryInterval.Swap(int64(20 * time.Millisecond))
	t.Cleanup(func() { activitySummaryInterval.Store(previous) })

	var mu sync.Mutex
	var prompts []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Messages []struct{ Content string } `json:"messages"`
			Tools    []any                      `json:"tools"`
		}
		_ = json.NewDecoder(req.Body).Decode(&body)
		mu.Lock()
		prompts = append(prompts, body.Messages[len(body.Messages)-1].Content)
		mu.Unlock()
		if len(body.Tools) > 0 {
			t.Errorf("summary request offered tools")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"Running session tests"}}]}`))
	}))
	defer server.Close()
	client, err := provider.New("test", config.Provider{BaseURL: server.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}

	st := openTestStore(t)
	session, chatRecord, _ := createSessionWithPlan(t, st)
	rt := newTestChat(t, st, session, chatRecord, &activityRunner{client: client})
	rt.mu.Lock()
	rt.state.AppendTimelineItem(domain.TimelineItem{ID: "task", Content: domain.UserMessage{Text: "Fix the session tests"}})
	rt.state.AppendTimelineItem(domain.TimelineItem{ID: "a1", Content: domain.AssistantMessage{Tools: []domain.ToolCall{
		{Tool: domain.ToolKindExecCommand, Args: map[string]string{"cmd": "go test ./internal/session/"}, Status: domain.ToolStatusRunning},
	}}})
	rt.active = true
	rt.mu.Unlock()

	deadline := time.Now().Add(5 * time.Second)
	for rt.Snapshot().Chat.Activity.Summary != "Running session tests" {
		if time.Now().After(deadline) {
			t.Fatalf("activity = %#v", rt.Snapshot().Chat.Activity)
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Unchanged notes are not summarized again.
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(prompts) != 1 || !strings.Contains(prompts[0], "go test ./internal/session/") {
		t.Fatalf("summary prompts = %q", prompts)
	}
	stored, err := getChat(context.Background(), st, chatRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Activity.Summary != "Running session tests" {
		t.Fatalf("stored activity = %#v", stored.Activity)
	}
}
