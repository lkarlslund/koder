package chat

import (
	"context"
	"fmt"
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
)

func TestSessionMatchesSearchesPersistedTitlesAndPagedContent(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	sessionID := id.ID("session-search")
	chatRecord := domain.Chat{ID: "chat-search", SessionID: sessionID, Title: "Implementation notes"}
	if err := putChat(ctx, st, chatRecord); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 66; index++ {
		text := fmt.Sprintf("ordinary message %d", index)
		if index == 0 {
			text = "The distant persisted needle"
		}
		if _, err := appendTimeline(ctx, st, chatRecord.ID, domain.UserMessage{Text: text}); err != nil {
			t.Fatal(err)
		}
	}
	source := NewSource(func() Deps { return Deps{Store: st} })

	for _, test := range []struct {
		query string
		want  bool
	}{
		{query: "IMPLEMENTATION", want: true},
		{query: "persisted needle", want: true},
		{query: "absent phrase", want: false},
	} {
		got, err := source.SessionMatches(ctx, sessionID, test.query)
		if err != nil {
			t.Fatalf("search %q: %v", test.query, err)
		}
		if got != test.want {
			t.Fatalf("search %q = %t, want %t", test.query, got, test.want)
		}
	}
}
