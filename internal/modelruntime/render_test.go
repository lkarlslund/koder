package modelruntime

import (
	"strings"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/provider"
)

func TestBuildPromptEnvelopeCompactionSummaryPreservesRetainedToolTail(t *testing.T) {
	t.Parallel()

	runtime := New(Config{Config: config.Default().WithStateDir(t.TempDir())})
	session := domain.Session{ID: "session-1"}
	chat := domain.Chat{
		ID:           "chat-1",
		SessionID:    session.ID,
		ProviderID:   "test",
		ModelID:      "test-model",
		WorkflowRole: chatrole.Orchestrator,
	}
	timeline := []domain.TimelineItem{
		assistantToolItem("old-tool-1", "call-1", "bad command 1", "failed output 1"),
		assistantToolItem("old-tool-2", "call-2", "bad command 2", "failed output 2"),
		{
			ID:     "compact-1",
			ChatID: chat.ID,
			Seq:    3,
			Content: domain.Compaction{
				Summary:         "durable compacted state",
				Status:          "completed",
				FirstKeptItemID: "old-tool-1",
			},
			CreatedAt: time.Now().UTC(),
		},
		{
			ID:        "user-1",
			ChatID:    chat.ID,
			Seq:       4,
			Content:   domain.UserMessage{Text: "state your current tasks"},
			CreatedAt: time.Now().UTC(),
		},
	}

	envelope, err := runtime.BuildPromptEnvelopeForTimeline(session, chat, timeline, "", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	joined := providerMessagesText(provider.SerializePromptEnvelope(envelope))
	if !strings.Contains(joined, "durable compacted state") {
		t.Fatalf("expected compaction summary in prompt, got %s", joined)
	}
	if !strings.Contains(joined, "state your current tasks") {
		t.Fatalf("expected post-compaction user turn in prompt, got %s", joined)
	}
	for _, retained := range []string{"bad command 1", "bad command 2", "failed output 1", "failed output 2"} {
		if !strings.Contains(joined, retained) {
			t.Fatalf("expected retained pre-compaction tool tail %q in prompt, got %s", retained, joined)
		}
	}
}

func assistantToolItem(itemID, callID, command, output string) domain.TimelineItem {
	return domain.TimelineItem{
		ID:     id.ID(itemID),
		ChatID: "chat-1",
		Seq:    1,
		Content: domain.AssistantMessage{
			Reasoning: domain.ReasoningContent{Text: "try another command"},
			Tools: []domain.ToolCall{{
				Tool:       domain.ToolKindBash,
				ToolCallID: domain.ToolCallID(callID),
				Args:       map[string]string{"cmd": command},
				Status:     domain.ToolStatusDone,
				Result: &domain.ToolResult{
					Status: domain.ToolResultStatusOK,
					Text:   output,
				},
			}},
		},
		CreatedAt: time.Now().UTC(),
	}
}

func providerMessagesText(messages []provider.Message) string {
	var parts []string
	for _, msg := range messages {
		if strings.TrimSpace(msg.Content) != "" {
			parts = append(parts, msg.Content)
		}
		for _, part := range msg.ContentParts {
			if strings.TrimSpace(part.Text) != "" {
				parts = append(parts, part.Text)
			}
		}
		for _, call := range msg.ToolCalls {
			parts = append(parts, call.Function.Name, call.Function.Arguments)
		}
	}
	return strings.Join(parts, "\n")
}
