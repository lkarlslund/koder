package service

import (
	"context"
	"errors"
	"testing"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
)

func TestListEntriesDefaultsToActiveButAllowsExplicitLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	parent, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	first, _ := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: parent.Chunk.ID, Entry: testEntryCandidate()})
	archived := first.Entry
	archived.State = knowledge.EntryStateArchived
	archived.UpdatedAt = archived.UpdatedAt.Add(1)
	archived.Revision = knowledge.Revision{
		Number: 2, ID: "01a01688-fc5d-7f7d-8bb8-de244977f8af",
		Actor: archived.Revision.Actor, CreatedAt: archived.UpdatedAt,
	}
	if err := store.Update(ctx, func(tx knowledgeStore.WriteTx) error { return tx.PutEntry(ctx, archived, 1) }); err != nil {
		t.Fatalf("archive fixture: %v", err)
	}
	secondCandidate := testEntryCandidate()
	secondCandidate.Title = "Visible"
	second, err := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: parent.Chunk.ID, Entry: secondCandidate})
	if err != nil {
		t.Fatalf("CreateEntry(second) error = %v", err)
	}
	page, err := service.ListEntries(ctx, knowledgeStore.EntryListRequest{
		Filter: knowledgeStore.EntryFilter{ChunkIDs: []knowledge.ChunkID{parent.Chunk.ID}},
	})
	if err != nil || len(page.Entries) != 1 || page.Entries[0].ID != second.Entry.ID {
		t.Fatalf("ListEntries(default) = %#v, %v", page, err)
	}
	page, err = service.ListEntries(ctx, knowledgeStore.EntryListRequest{
		Filter: knowledgeStore.EntryFilter{
			ChunkIDs: []knowledge.ChunkID{parent.Chunk.ID}, States: []knowledge.EntryState{knowledge.EntryStateArchived},
		},
	})
	if err != nil || len(page.Entries) != 1 || page.Entries[0].ID != archived.ID {
		t.Fatalf("ListEntries(archived) = %#v, %v", page, err)
	}
}

func TestListEntriesFiltersDeniedParentChunksWithoutLeakingPagination(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	hiddenChunk, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	visibleCandidate := testChunkCandidate()
	visibleCandidate.Title = "Visible chunk"
	visibleChunk, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: visibleCandidate})
	hiddenEntry, _ := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: hiddenChunk.Chunk.ID, Entry: testEntryCandidate()})
	visibleEntryCandidate := testEntryCandidate()
	visibleEntryCandidate.Title = "Visible entry"
	visibleEntry, _ := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: visibleChunk.Chunk.ID, Entry: visibleEntryCandidate})
	service.chunkPolicy = ChunkPolicyFunc(func(_ context.Context, _ knowledge.Actor, action ChunkPolicyAction, chunk knowledge.Chunk) error {
		if action == ChunkPolicyRead && chunk.ID == hiddenChunk.Chunk.ID {
			return errors.New("hidden")
		}
		return nil
	})
	page, err := service.ListEntries(ctx, knowledgeStore.EntryListRequest{Sort: knowledgeStore.EntrySortTitle, Limit: 1})
	if err != nil {
		t.Fatalf("ListEntries() error = %v", err)
	}
	if len(page.Entries) != 1 || page.Entries[0].ID != visibleEntry.Entry.ID || page.NextCursor != "" {
		t.Fatalf("ListEntries() = %#v; hidden=%s", page, hiddenEntry.Entry.ID)
	}
}
