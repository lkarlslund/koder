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
		messages []domain.Message
		want     bool
	}{
		{
			name: "first completed exchange generates title",
			messages: []domain.Message{
				{Role: domain.MessageRoleUser},
				{Role: domain.MessageRoleAssistant},
			},
			want: true,
		},
		{
			name: "no assistant yet does not generate title",
			messages: []domain.Message{
				{Role: domain.MessageRoleUser},
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
			messages: []domain.Message{
				{Role: domain.MessageRoleUser},
				{Role: domain.MessageRoleAssistant},
				{Role: domain.MessageRoleUser},
				{Role: domain.MessageRoleAssistant},
				{Role: domain.MessageRoleUser},
				{Role: domain.MessageRoleAssistant},
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
			messages: []domain.Message{
				{Role: domain.MessageRoleUser},
				{Role: domain.MessageRoleAssistant},
				{Role: domain.MessageRoleUser},
				{Role: domain.MessageRoleAssistant},
				{Role: domain.MessageRoleUser},
				{Role: domain.MessageRoleAssistant},
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
			messages: []domain.Message{
				{Role: domain.MessageRoleUser},
				{Role: domain.MessageRoleAssistant},
				{Role: domain.MessageRoleUser},
				{Role: domain.MessageRoleAssistant},
				{Role: domain.MessageRoleUser},
				{Role: domain.MessageRoleAssistant},
			},
			want: false,
		},
		{
			name: "backward compatible existing custom title counts as first refresh",
			session: domain.Session{
				Title:     "Already Generated",
				UpdatedAt: now.Add(-2 * time.Minute),
			},
			messages: []domain.Message{
				{Role: domain.MessageRoleUser},
				{Role: domain.MessageRoleAssistant},
				{Role: domain.MessageRoleUser},
				{Role: domain.MessageRoleAssistant},
				{Role: domain.MessageRoleUser},
				{Role: domain.MessageRoleAssistant},
			},
			want: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldRefreshSessionTitle(tc.session, tc.messages, now); got != tc.want {
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
