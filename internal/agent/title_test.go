package agent

import (
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/domain"
)

func TestShouldRefreshSessionTitle(t *testing.T) {
	now := time.Date(2026, 4, 28, 20, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		session  domain.Session
		timeline []domain.TimelineItem
		want     bool
	}{
		{
			name: "first completed exchange generates title",
			timeline: []domain.TimelineItem{
				{Content: domain.UserMessage{}},
				{Content: domain.AssistantMessage{}},
			},
			want: true,
		},
		{
			name: "no assistant yet does not generate title",
			timeline: []domain.TimelineItem{
				{Content: domain.UserMessage{}},
			},
			want: false,
		},
		{
			name: "second refresh waits for enough messages and time",
			session: domain.Session{
				Title:             "Existing Title",
				TitleRefreshCount: 1,
				TitleGeneratedAt:  now.Add(-59 * time.Second),
			},
			timeline: []domain.TimelineItem{
				{Content: domain.UserMessage{}},
				{Content: domain.AssistantMessage{}},
				{Content: domain.UserMessage{}},
				{Content: domain.AssistantMessage{}},
				{Content: domain.UserMessage{}},
				{Content: domain.AssistantMessage{}},
			},
			want: false,
		},
		{
			name: "second refresh triggers after one minute with enough messages",
			session: domain.Session{
				Title:             "Existing Title",
				TitleRefreshCount: 1,
				TitleGeneratedAt:  now.Add(-time.Minute),
			},
			timeline: []domain.TimelineItem{
				{Content: domain.UserMessage{}},
				{Content: domain.AssistantMessage{}},
				{Content: domain.UserMessage{}},
				{Content: domain.AssistantMessage{}},
				{Content: domain.UserMessage{}},
				{Content: domain.AssistantMessage{}},
			},
			want: true,
		},
		{
			name: "no third refresh",
			session: domain.Session{
				Title:             "Existing Title",
				TitleRefreshCount: 2,
				TitleGeneratedAt:  now.Add(-2 * time.Minute),
			},
			timeline: []domain.TimelineItem{
				{Content: domain.UserMessage{}},
				{Content: domain.AssistantMessage{}},
				{Content: domain.UserMessage{}},
				{Content: domain.AssistantMessage{}},
				{Content: domain.UserMessage{}},
				{Content: domain.AssistantMessage{}},
			},
			want: false,
		},
		{
			name: "backward compatible existing custom title counts as first refresh",
			session: domain.Session{
				Title:     "Already Generated",
				UpdatedAt: now.Add(-2 * time.Minute),
			},
			timeline: []domain.TimelineItem{
				{Content: domain.UserMessage{}},
				{Content: domain.AssistantMessage{}},
				{Content: domain.UserMessage{}},
				{Content: domain.AssistantMessage{}},
				{Content: domain.UserMessage{}},
				{Content: domain.AssistantMessage{}},
			},
			want: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldRefreshSessionTitle(tc.session, tc.timeline, now); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestNormalizeSessionTitle(t *testing.T) {
	got := normalizeSessionTitle(`"this is a much longer title than allowed"`)
	want := "this is a much longer title"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestShouldRefreshChatTitle(t *testing.T) {
	timeline := []domain.TimelineItem{
		{Content: domain.UserMessage{Text: "compare go code to c reference"}},
		{Content: domain.AssistantMessage{Text: "done"}},
	}
	if !shouldRefreshChatTitle(domain.Chat{Title: "Chat"}, timeline) {
		t.Fatal("expected generated chat title to refresh")
	}
	if !shouldRefreshChatTitle(domain.Chat{Title: "Main"}, timeline) {
		t.Fatal("expected main chat title to refresh")
	}
	if shouldRefreshChatTitle(domain.Chat{Title: "hand picked title"}, timeline) {
		t.Fatal("did not expect custom chat title to refresh")
	}
}

func TestTitleFromTimelineUsesFirstUserMessage(t *testing.T) {
	got := titleFromTimeline([]domain.TimelineItem{
		{Content: domain.UserMessage{Text: "compare go code to c reference and identify gaps"}},
		{Content: domain.AssistantMessage{Text: "Done"}},
	})
	want := "compare go code to c reference"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
