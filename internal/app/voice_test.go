package app

import (
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/domain"
)

func TestLatestAssistantTextAfterRequiresNewSealedAssistant(t *testing.T) {
	timeline := []domain.TimelineItem{
		{Seq: 1, Content: domain.AssistantMessage{Text: "old"}, SealedAt: time.Now()},
		{Seq: 2, Content: domain.UserMessage{Text: "question"}, SealedAt: time.Now()},
		{Seq: 3, Content: domain.AssistantMessage{Text: "partial"}},
		{Seq: 4, Content: domain.AssistantMessage{Text: " final answer "}, SealedAt: time.Now()},
	}
	if got, want := latestAssistantTextAfter(timeline, 1), "final answer"; got != want {
		t.Fatalf("latestAssistantTextAfter() = %q, want %q", got, want)
	}
	if got := latestAssistantTextAfter(timeline, 4); got != "" {
		t.Fatalf("latestAssistantTextAfter() = %q, want empty", got)
	}
}

func TestVoiceTurnStartedIgnoresStaleErroredState(t *testing.T) {
	if voiceTurnStarted(chat.StatusErrored, false) {
		t.Fatal("stale errored status must not terminate a newly enqueued voice turn")
	}
	if !voiceTurnStarted(chat.StatusWaitingLLM, false) {
		t.Fatal("waiting LLM should mark the delegated turn as started")
	}
	if !voiceTurnStarted(chat.StatusErrored, true) {
		t.Fatal("an active runtime should mark the delegated turn as started")
	}
}

func TestLatestModelErrorAfter(t *testing.T) {
	timeline := []domain.TimelineItem{
		{Seq: 1, Content: domain.Notice{Kind: "model_error", Text: "old error"}, SealedAt: time.Now()},
		{Seq: 2, Content: domain.UserMessage{Text: "retry"}, SealedAt: time.Now()},
		{Seq: 3, Content: domain.Notice{Kind: "model_error", Text: "new error"}, SealedAt: time.Now()},
	}
	if got, want := latestModelErrorAfter(timeline, 1), "new error"; got != want {
		t.Fatalf("latestModelErrorAfter() = %q, want %q", got, want)
	}
	if got := latestModelErrorAfter(timeline, 3); got != "" {
		t.Fatalf("latestModelErrorAfter() = %q, want empty", got)
	}
}
