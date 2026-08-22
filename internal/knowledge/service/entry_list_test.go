package service

import (
	"context"
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
