package chat

import (
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/store"
)

func TestChatStateMergeTimelineLoadedPreservesRecordIdentity(t *testing.T) {
	initial := []domain.TimelineItem{{
		ID:        1,
		ChatID:    7,
		Seq:       1,
		Content:   domain.UserMessage{Text: "one"},
		CreatedAt: time.Now().UTC(),
	}}
	state := NewTimelineState(domain.Chat{ID: 7}, initial, []store.Approval{{ID: 50}})
	record := state.Timeline()[0]

	updated := []domain.TimelineItem{{
		ID:        1,
		ChatID:    7,
		Seq:       1,
		Content:   domain.UserMessage{Text: "updated"},
		CreatedAt: initial[0].CreatedAt,
		UpdatedAt: time.Now().UTC(),
	}}
	state.MergeTimelineLoaded(domain.Chat{ID: 7, Title: "updated"}, updated, []store.Approval{{ID: 51}})

	if got := state.Timeline()[0]; got != record {
		t.Fatalf("timeline record pointer changed")
	}
	if got := state.Timeline()[0].Item.Content.(domain.UserMessage).Text; got != "updated" {
		t.Fatalf("timeline text = %q", got)
	}
	if approvals := state.Approvals(); len(approvals) != 1 || approvals[0].ID != 51 {
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
