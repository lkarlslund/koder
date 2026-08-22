package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
)

func TestSearchLexicalCursorKeepsStableAsOfAndExclusiveRankPosition(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newLexicalSearchTestService(t, store)
	current := serviceTime
	service.now = func() time.Time { return current }
	chunk, err := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	if err != nil {
		t.Fatalf("CreateChunk() error = %v", err)
	}
	for _, title := range []string{"Alpha needle", "Beta needle", "Gamma needle"} {
		if _, err := service.CreateEntry(ctx, CreateEntryRequest{
			ChunkID: chunk.Chunk.ID, Entry: knowledge.Entry{Title: title, Kind: knowledge.EntryKindFact},
		}); err != nil {
			t.Fatalf("CreateEntry(%q) error = %v", title, err)
		}
	}
	request := LexicalSearchRequest{Query: "needle", Limit: 1}
	var ids []knowledge.EntryID
	var asOf time.Time
	for {
		page, err := service.SearchLexical(ctx, request)
		if err != nil {
			t.Fatalf("SearchLexical(page) error = %v", err)
		}
		if len(page.Matches) != 1 {
			t.Fatalf("page matches = %#v", page.Matches)
		}
		if asOf.IsZero() {
			asOf = page.AsOf
		} else if !page.AsOf.Equal(asOf) {
			t.Fatalf("cursor as_of changed from %v to %v", asOf, page.AsOf)
		}
		ids = append(ids, page.Matches[0].EntryID)
		if page.NextCursor == "" {
			break
		}
		current = current.Add(24 * time.Hour)
		request.Cursor = page.NextCursor
	}
	if len(ids) != 3 || ids[0] == ids[1] || ids[1] == ids[2] || ids[0] == ids[2] {
		t.Fatalf("paged entry IDs = %v", ids)
	}
}

func TestSearchLexicalCursorBindsQueryAndIndexGeneration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newLexicalSearchTestService(t, store)
	chunk, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	for _, title := range []string{"Alpha needle", "Beta needle"} {
		_, _ = service.CreateEntry(ctx, CreateEntryRequest{
			ChunkID: chunk.Chunk.ID, Entry: knowledge.Entry{Title: title, Kind: knowledge.EntryKindFact},
		})
	}
	first, err := service.SearchLexical(ctx, LexicalSearchRequest{Query: "needle", Limit: 1})
	if err != nil || first.NextCursor == "" {
		t.Fatalf("first page = %#v, %v", first, err)
	}
	if _, err := service.SearchLexical(ctx, LexicalSearchRequest{
		Query: "different", Limit: 1, Cursor: first.NextCursor,
	}); !errors.Is(err, knowledgeStore.ErrInvalidCursor) {
		t.Fatalf("changed query error = %v, want ErrInvalidCursor", err)
	}
	if err := store.RebuildIndexes(ctx); err != nil {
		t.Fatalf("RebuildIndexes() error = %v", err)
	}
	if _, err := service.SearchLexical(ctx, LexicalSearchRequest{
		Query: "needle", Limit: 1, Cursor: first.NextCursor,
	}); !errors.Is(err, knowledgeStore.ErrStaleCursor) {
		t.Fatalf("retired generation error = %v, want ErrStaleCursor", err)
	}
}
