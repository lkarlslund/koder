package chat

import (
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/domain"
)

func TestChatStateMergeTimelineLoadedPreservesRecordIdentity(t *testing.T) {
	initial := []domain.TimelineItem{{
		ID:        1,
		ChatID:    7,
		Seq:       1,
		Content:   domain.UserMessage{Text: "one"},
		CreatedAt: time.Now().UTC(),
	}}
	state := NewTimelineState(domain.Chat{ID: 7}, initial, nil)
	record := state.Timeline()[0]

	updated := []domain.TimelineItem{{
		ID:        1,
		ChatID:    7,
		Seq:       1,
		Content:   domain.UserMessage{Text: "updated"},
		CreatedAt: initial[0].CreatedAt,
		UpdatedAt: time.Now().UTC(),
	}}
	state.MergeTimelineLoaded(domain.Chat{ID: 7, Title: "updated"}, updated, nil)

	if got := state.Timeline()[0]; got != record {
		t.Fatalf("timeline record pointer changed")
	}
	if got := state.Timeline()[0].Item.Content.(domain.UserMessage).Text; got != "updated" {
		t.Fatalf("timeline text = %q", got)
	}
	if approvals := state.Approvals(); len(approvals) != 0 {
		t.Fatalf("approvals = %+v", approvals)
	}
	if got := state.Chat().Title; got != "updated" {
		t.Fatalf("chat title = %q", got)
	}
}

func TestChatStateUpsertTimelineItemPreservesRecordIdentity(t *testing.T) {
	state := NewTimelineState(domain.Chat{ID: 7}, nil, nil)
	record, created := state.UpsertTimelineItem(domain.TimelineItem{ID: 10, ChatID: 7, Seq: 1, Content: domain.AssistantMessage{Text: "first"}})
	if !created || record == nil {
		t.Fatalf("expected new timeline record")
	}
	updated, created := state.UpsertTimelineItem(domain.TimelineItem{ID: 10, ChatID: 7, Seq: 1, Content: domain.AssistantMessage{Text: "updated"}})
	if created {
		t.Fatal("expected existing timeline record to be reused")
	}
	if updated != record {
		t.Fatal("expected timeline record pointer preserved")
	}
	if got := updated.Item.Content.(domain.AssistantMessage).Text; got != "updated" {
		t.Fatalf("assistant text = %q", got)
	}
}

func TestChatStateUpsertReplacesSealedStreamedAssistantWithFinalItem(t *testing.T) {
	state := NewTimelineState(domain.Chat{ID: 7}, nil, nil)
	if err := state.AppendAssistantText(7, "I'll inspect the files."); err != nil {
		t.Fatalf("append assistant text: %v", err)
	}
	streamed := state.Timeline()[0]
	state.SealActiveAssistant("")
	if !streamed.Item.Sealed() {
		t.Fatal("expected streamed assistant to be sealed")
	}

	final := domain.TimelineItem{
		ID:     42,
		ChatID: 7,
		Seq:    1,
		Content: domain.AssistantMessage{
			Text: "I'll inspect the files.",
			Tools: []domain.ToolCall{{
				ToolCallID: "call_1",
				Tool:       domain.ToolKindRead,
				Args:       map[string]string{"path": "main.go"},
				Status:     domain.ToolStatusPending,
			}},
		},
		CreatedAt: time.Now().UTC(),
	}
	replaced, created := state.UpsertTimelineItem(final)
	if created {
		t.Fatal("expected final assistant to replace streamed assistant")
	}
	if replaced != streamed {
		t.Fatal("expected streamed assistant record identity to be preserved")
	}
	timeline := state.SnapshotTimeline()
	if len(timeline) != 1 {
		t.Fatalf("expected one assistant item, got %d", len(timeline))
	}
	if timeline[0].ID != final.ID {
		t.Fatalf("expected durable final id %d, got %d", final.ID, timeline[0].ID)
	}
	assistant := timeline[0].Content.(domain.AssistantMessage)
	if len(assistant.Tools) != 1 || assistant.Tools[0].ToolCallID != "call_1" {
		t.Fatalf("expected final tool calls, got %#v", assistant.Tools)
	}
}

func TestChatStateCurrentContextSizeFromTimeline(t *testing.T) {
	now := time.Now().UTC()
	state := NewTimelineState(
		domain.Chat{ID: 7, LastKnownContextTokens: 1200, ContextTokensKnown: true},
		[]domain.TimelineItem{
			{ID: 1, ChatID: 7, Seq: 1, Content: domain.AssistantMessage{Usage: &domain.Usage{PromptTokens: 1200, CompletionTokens: 50, TotalTokens: 1250}}, CreatedAt: now},
			{ID: 2, ChatID: 7, Seq: 2, Content: domain.UserMessage{Text: "inspect these files"}, CreatedAt: now.Add(time.Second)},
		},
		nil,
	)
	state.AppendPendingAssistantText("delta payload")

	got := state.CurrentContextSize()
	if got.AnchorTokens != 1200 {
		t.Fatalf("anchor = %d", got.AnchorTokens)
	}
	if got.TailTokens <= 0 {
		t.Fatalf("expected tail estimate, got %#v", got)
	}
	if got.LiveTokens <= 0 {
		t.Fatalf("expected live estimate, got %d", got.LiveTokens)
	}
	if got.TotalTokens != got.AnchorTokens+got.TailTokens+got.LiveTokens {
		t.Fatalf("total mismatch %#v", got)
	}
	if !got.Estimated {
		t.Fatalf("expected estimated usage, got %#v", got)
	}
}
