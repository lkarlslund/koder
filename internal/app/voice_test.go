package app

import (
	"testing"
	"time"

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
