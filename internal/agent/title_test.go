package agent

import (
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
)

func TestShouldRefreshSessionTitle(t *testing.T) {
	completed := []domain.TimelineItem{
		{Content: domain.UserMessage{}},
		{Content: domain.AssistantMessage{}},
	}
	cases := []struct {
		name     string
		session  domain.Session
		timeline []domain.TimelineItem
		want     bool
	}{
		{
			name:     "first completed exchange generates title",
			timeline: completed,
			want:     true,
		},
		{
			name:     "legacy placeholder generates title",
			session:  domain.Session{Title: "New Session"},
			timeline: completed,
			want:     true,
		},
		{
			name:     "user title is never replaced",
			session:  domain.Session{Title: "Fairphone 6", TitleUserDefined: true},
			timeline: append(completed, completed...),
			want:     false,
		},
		{
			name:     "legacy custom title is treated as user owned",
			session:  domain.Session{Title: "Laptop repair"},
			timeline: append(completed, completed...),
			want:     false,
		},
		{
			name:     "explicit placeholder wording is still user owned",
			session:  domain.Session{Title: "New Session", TitleUserDefined: true},
			timeline: completed,
			want:     false,
		},
		{
			name: "no assistant yet does not generate title",
			timeline: []domain.TimelineItem{
				{Content: domain.UserMessage{}},
			},
			want: false,
		},
		{
			name: "generated title is never refreshed",
			session: domain.Session{
				Title:             "Existing Title",
				TitleRefreshCount: 1,
			},
			timeline: append(completed, completed...),
			want:     false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldRefreshSessionTitle(tc.session, tc.timeline); got != tc.want {
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
	if shouldRefreshChatTitle(domain.Chat{Title: "Main", TitleUserDefined: true}, timeline) {
		t.Fatal("did not expect an explicitly named Main chat to refresh")
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
