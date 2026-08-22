package service

import (
	"context"
	"errors"
	"testing"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
)

func TestSupersedeEntryLinksReplacementAndHidesOldEntryByDefault(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	parent, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	old, _ := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: parent.Chunk.ID, Entry: testEntryCandidate()})
	replacementCandidate := testEntryCandidate()
	replacementCandidate.Title = "Corrected fact"
	replacement, _ := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: parent.Chunk.ID, Entry: replacementCandidate})
	result, err := service.SupersedeEntry(ctx, SupersedeEntryRequest{
		EntryID: old.Entry.ID, ExpectedRevision: 1, ReplacementEntryID: replacement.Entry.ID,
	})
	if err != nil {
		t.Fatalf("SupersedeEntry() error = %v", err)
	}
	if !result.Updated || result.Entry.State != knowledge.EntryStateSuperseded || result.Entry.SupersededByID != replacement.Entry.ID ||
		result.Entry.Revision.Number != 2 || result.Entry.Revision.ID != "01a01688-fc5d-7f7d-8bb8-de244977f8a7" || result.Replacement.ID != replacement.Entry.ID {
		t.Fatalf("SupersedeEntry() = %#v", result)
	}
	page, err := service.ListEntries(ctx, knowledgeStore.EntryListRequest{
		Filter: knowledgeStore.EntryFilter{ChunkIDs: []knowledge.ChunkID{parent.Chunk.ID}},
	})
	if err != nil || len(page.Entries) != 1 || page.Entries[0].ID != replacement.Entry.ID {
		t.Fatalf("default list after supersession = %#v, %v", page, err)
	}
	page, err = service.ListEntries(ctx, knowledgeStore.EntryListRequest{
		Filter: knowledgeStore.EntryFilter{States: []knowledge.EntryState{knowledge.EntryStateSuperseded}},
	})
	if err != nil || len(page.Entries) != 1 || page.Entries[0].ID != old.Entry.ID {
		t.Fatalf("superseded list = %#v, %v", page, err)
	}
}

func TestSupersedeEntryIsIdempotentAndRejectsStaleOrDifferentReplacement(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	parent, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	old, _ := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: parent.Chunk.ID, Entry: testEntryCandidate()})
	replacement, _ := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: parent.Chunk.ID, Entry: testEntryCandidate()})
	first, _ := service.SupersedeEntry(ctx, SupersedeEntryRequest{
		EntryID: old.Entry.ID, ExpectedRevision: 1, ReplacementEntryID: replacement.Entry.ID,
	})
	noOp, err := service.SupersedeEntry(ctx, SupersedeEntryRequest{
		EntryID: old.Entry.ID, ExpectedRevision: 2, ReplacementEntryID: replacement.Entry.ID,
	})
	if err != nil || noOp.Updated || noOp.Entry.Revision != first.Entry.Revision {
		t.Fatalf("idempotent SupersedeEntry() = %#v, %v", noOp, err)
	}
	if _, err := service.SupersedeEntry(ctx, SupersedeEntryRequest{
		EntryID: old.Entry.ID, ExpectedRevision: 1, ReplacementEntryID: replacement.Entry.ID,
	}); !errors.Is(err, knowledgeStore.ErrConflict) {
		t.Fatalf("stale SupersedeEntry() error = %v, want ErrConflict", err)
	}
	other := replacement.Entry
	other.ID = "01a01688-fc5d-7f7d-8bb8-de244977f8af"
	other.Revision.ID = "01a01688-fc5d-7f7d-8bb8-de244977f8ae"
	if err := store.Update(ctx, func(tx knowledgeStore.WriteTx) error { return tx.PutEntry(ctx, other, 0) }); err != nil {
		t.Fatalf("seed other replacement: %v", err)
	}
	if _, err := service.SupersedeEntry(ctx, SupersedeEntryRequest{
		EntryID: old.Entry.ID, ExpectedRevision: 2, ReplacementEntryID: other.ID,
	}); !errors.Is(err, ErrInvalidSupersession) {
		t.Fatalf("different replacement error = %v, want ErrInvalidSupersession", err)
	}
}

func TestSupersedeEntryRequiresActiveReplacementInSameActiveChunk(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	firstChunk, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	old, _ := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: firstChunk.Chunk.ID, Entry: testEntryCandidate()})
	secondChunkCandidate := testChunkCandidate()
	secondChunkCandidate.Title = "Other chunk"
	secondChunk, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: secondChunkCandidate})
	replacement, _ := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: secondChunk.Chunk.ID, Entry: testEntryCandidate()})
	if _, err := service.SupersedeEntry(ctx, SupersedeEntryRequest{
		EntryID: old.Entry.ID, ExpectedRevision: 1, ReplacementEntryID: replacement.Entry.ID,
	}); !errors.Is(err, ErrInvalidSupersession) {
		t.Fatalf("cross-chunk SupersedeEntry() error = %v, want ErrInvalidSupersession", err)
	}

	archivedReplacement := replacement.Entry
	archivedReplacement.ChunkID = firstChunk.Chunk.ID
	archivedReplacement.State = knowledge.EntryStateArchived
	archivedReplacement.UpdatedAt = archivedReplacement.UpdatedAt.Add(1)
	archivedReplacement.Revision = knowledge.Revision{
		Number: 2, ID: "01a01688-fc5d-7f7d-8bb8-de244977f8af",
		Actor: archivedReplacement.Revision.Actor, CreatedAt: archivedReplacement.UpdatedAt,
	}
	if err := store.Update(ctx, func(tx knowledgeStore.WriteTx) error { return tx.PutEntry(ctx, archivedReplacement, 1) }); err != nil {
		t.Fatalf("archive replacement fixture: %v", err)
	}
	if _, err := service.SupersedeEntry(ctx, SupersedeEntryRequest{
		EntryID: old.Entry.ID, ExpectedRevision: 1, ReplacementEntryID: archivedReplacement.ID,
	}); !errors.Is(err, ErrInvalidSupersession) {
		t.Fatalf("archived replacement error = %v, want ErrInvalidSupersession", err)
	}
}
