package claudedriver

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/claudeapp"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/store"
)

// directFactory runs the installed claude without a sandbox, using the
// account's own configuration. The sandbox has its own tests.
type directFactory struct{ executable string }

func (f directFactory) DiscoveryConfig(ctx context.Context) (claudeapp.Config, error) {
	return NewSandboxProcessFactory(SandboxProcessConfig{Executable: f.executable}).DiscoveryConfig(ctx)
}

func (f directFactory) ChatConfig(_ context.Context, session domain.Session, _ domain.Chat, args []string) (ChatProcessConfig, error) {
	return ChatProcessConfig{Client: claudeapp.Config{Executable: f.executable, Args: args, Env: processEnv, Dir: session.ProjectRoot}, Fingerprint: "direct"}, nil
}

func (directFactory) RemoveChat(domain.ID) error { return nil }

// TestLiveClaudeCode drives the installed Claude Code CLI through real turns.
// It spends a few cheap model calls on the account's own login.
func TestLiveClaudeCode(t *testing.T) {
	if os.Getenv("KODER_CLAUDE_LIVE") != "1" {
		t.Skip("set KODER_CLAUDE_LIVE=1 to exercise the installed Claude Code CLI")
	}
	executable, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("claude is not installed")
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	manager := New(directFactory{executable: executable}, st, func(domain.Session, domain.Chat) string {
		return "You are running inside an automated test. Follow instructions exactly and keep replies short."
	})
	bridge := &fakeToolBridge{}
	manager.SetToolBridge(bridge)
	t.Cleanup(func() { _ = manager.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	models, err := manager.Models(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(models, func(model Model) bool { return model.Value == "default" }) {
		t.Fatalf("models lack the default: %#v", models)
	}

	sessionRecord := domain.Session{ID: id.ID("live-session"), ProjectRoot: t.TempDir()}
	chatRecord := domain.Chat{ID: id.ID("live-chat"), SessionID: sessionRecord.ID, Backend: domain.ChatBackendClaude, InteractionMode: domain.InteractionModeText, ModelID: "haiku"}
	source := chat.NewSource(func() chat.Deps { return chat.Deps{Store: st} })
	if err := source.PutRecord(ctx, chatRecord); err != nil {
		t.Fatal(err)
	}
	runtime, err := source.Load(ctx, sessionRecord, chatRecord)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runtime.Close() })
	run := func(ctx context.Context, text string) error {
		events := make(chan domain.Event, 4096)
		return manager.RunTurn(ctx, runtime, chat.DriverTurn{Input: domain.QueuedInput{ID: id.New(), Text: text}}, events)
	}
	lastReply := func() string {
		timeline := runtime.SnapshotTimeline()
		for i := len(timeline) - 1; i >= 0; i-- {
			if message, ok := timeline[i].Content.(domain.AssistantMessage); ok && message.Text != "" {
				return message.Text
			}
		}
		return ""
	}

	if err := run(ctx, "First call the milestone_list tool from the koder MCP server with action \"list\". Then run the shell command `echo live-ok-4711` with Bash. Then reply with the single word DONE."); err != nil {
		t.Fatal(err)
	}
	if len(bridge.calls) != 1 || !strings.HasPrefix(bridge.calls[0], "milestone_list toolu_") {
		t.Fatalf("Koder tool calls = %q", bridge.calls)
	}
	var bash *domain.ToolCall
	for _, item := range runtime.SnapshotTimeline() {
		if message, ok := item.Content.(domain.AssistantMessage); ok {
			for i := range message.Tools {
				if message.Tools[i].Tool == domain.ToolKindBash {
					bash = &message.Tools[i]
				}
			}
		}
	}
	if bash == nil || bash.Result == nil || !strings.Contains(bash.Result.Text, "live-ok-4711") {
		t.Fatalf("bash call = %#v", bash)
	}
	if reply := lastReply(); !strings.Contains(reply, "DONE") {
		t.Fatalf("reply = %q", reply)
	}

	// A new process resumes the session.
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if err := run(ctx, "What exactly did the echo command print? Reply with only that text."); err != nil {
		t.Fatal(err)
	}
	if reply := lastReply(); !strings.Contains(reply, "live-ok-4711") {
		t.Fatalf("resumed reply = %q", reply)
	}

	// An interrupted turn leaves the chat usable.
	turnCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- run(turnCtx, "Write the numbers from 1 to 400 as words, one per line, with no other text.")
	}()
	time.Sleep(4 * time.Second)
	stop()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted turn = %v", err)
	}
	if err := run(ctx, "Reply with the single word AGAIN."); err != nil {
		t.Fatal(err)
	}
	if reply := lastReply(); !strings.Contains(reply, "AGAIN") {
		t.Fatalf("reply after interrupt = %q", reply)
	}
}
