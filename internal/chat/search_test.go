package chat

import (
	"context"
	"fmt"
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/store"
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
	indexes, err := chatSearchIndexCollection(st).List(ctx, store.ByIndex[chatSearchIndex]("chat", string(chatRecord.ID)))
	if err != nil {
		t.Fatal(err)
	}
	if len(indexes) != 1 || len(indexes[0].Bloom) != chatSearchBloomBytes {
		t.Fatalf("expected one lazy per-chat index, got %#v", indexes)
	}
	firstRevision := indexes[0].SourceRevision
	if _, err := appendTimeline(ctx, st, chatRecord.ID, domain.UserMessage{Text: "freshly appended searchable text"}); err != nil {
		t.Fatal(err)
	}
	matched, err := source.SessionMatches(ctx, sessionID, "freshly appended")
	if err != nil {
		t.Fatal(err)
	}
	if !matched {
		t.Fatal("expected stale chat index to rebuild after append")
	}
	indexes, err = chatSearchIndexCollection(st).List(ctx, store.ByIndex[chatSearchIndex]("chat", string(chatRecord.ID)))
	if err != nil {
		t.Fatal(err)
	}
	if len(indexes) != 1 || !indexes[0].SourceRevision.After(firstRevision) {
		t.Fatalf("expected rebuilt per-chat index, got %#v", indexes)
	}
}

func TestChatSearchBloomRejectsMissingTrigrams(t *testing.T) {
	bloom := buildChatSearchBloom("the quick brown fox")
	if !chatSearchBloomMayContain(bloom, "quick brown") {
		t.Fatal("expected bloom to retain present phrase")
	}
	if chatSearchBloomMayContain(bloom, "zzzzzunlikely") {
		t.Fatal("expected bloom to reject absent phrase")
	}
}
