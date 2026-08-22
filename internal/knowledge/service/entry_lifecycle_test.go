package service

import (
	"context"
	"errors"
	"testing"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
)

func TestArchiveAndRestoreEntryAdvanceLifecycleRevisions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	parent, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	created, _ := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: parent.Chunk.ID, Entry: testEntryCandidate()})
	archived, err := service.ArchiveEntry(ctx, EntryLifecycleRequest{EntryID: created.Entry.ID, ExpectedRevision: 1})
	if err != nil {
		t.Fatalf("ArchiveEntry() error = %v", err)
	}
	if !archived.Updated || archived.Entry.State != knowledge.EntryStateArchived || archived.Entry.Revision.Number != 2 || archived.Entry.Revision.Reason != "archive entry" {
		t.Fatalf("ArchiveEntry() = %#v", archived)
	}
	page, err := service.ListEntries(ctx, knowledgeStore.EntryListRequest{})
	if err != nil || len(page.Entries) != 0 {
		t.Fatalf("default list after archive = %#v, %v", page, err)
	}
	restored, err := service.RestoreEntry(ctx, EntryLifecycleRequest{
		EntryID: created.Entry.ID, ExpectedRevision: 2, Reason: "use again",
	})
	if err != nil {
		t.Fatalf("RestoreEntry() error = %v", err)
	}
	if !restored.Updated || restored.Entry.State != knowledge.EntryStateActive || restored.Entry.Revision.Number != 3 || restored.Entry.Revision.Reason != "use again" {
		t.Fatalf("RestoreEntry() = %#v", restored)
	}
	page, err = service.ListEntries(ctx, knowledgeStore.EntryListRequest{})
	if err != nil || len(page.Entries) != 1 || page.Entries[0].ID != restored.Entry.ID {
		t.Fatalf("default list after restore = %#v, %v", page, err)
	}
}

func TestEntryLifecycleIsIdempotentAndRejectsStaleOrInvalidTransitions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	parent, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	created, _ := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: parent.Chunk.ID, Entry: testEntryCandidate()})
	archived, _ := service.ArchiveEntry(ctx, EntryLifecycleRequest{EntryID: created.Entry.ID, ExpectedRevision: 1})
	noOp, err := service.ArchiveEntry(ctx, EntryLifecycleRequest{EntryID: created.Entry.ID, ExpectedRevision: 2})
	if err != nil || noOp.Updated || noOp.Entry.Revision.Number != 2 {
		t.Fatalf("idempotent ArchiveEntry() = %#v, %v", noOp, err)
	}
	if _, err := service.RestoreEntry(ctx, EntryLifecycleRequest{EntryID: created.Entry.ID, ExpectedRevision: 1}); !errors.Is(err, knowledgeStore.ErrConflict) {
		t.Fatalf("stale RestoreEntry() error = %v, want ErrConflict", err)
	}
	got, _ := service.Entry(ctx, created.Entry.ID)
	if got.State != knowledge.EntryStateArchived || got.Revision != archived.Entry.Revision {
		t.Fatalf("stale lifecycle call changed entry: %#v", got)
	}

	draft := got
	draft.State = knowledge.EntryStateDraft
	draft.UpdatedAt = draft.UpdatedAt.Add(1)
	draft.Revision = knowledge.Revision{
		Number: 3, ID: "01a01688-fc5d-7f7d-8bb8-de244977f8af",
		Actor: draft.Revision.Actor, CreatedAt: draft.UpdatedAt,
	}
	if err := store.Update(ctx, func(tx knowledgeStore.WriteTx) error { return tx.PutEntry(ctx, draft, 2) }); err != nil {
		t.Fatalf("draft fixture: %v", err)
	}
	if _, err := service.RestoreEntry(ctx, EntryLifecycleRequest{EntryID: draft.ID, ExpectedRevision: 3}); !errors.Is(err, ErrInvalidLifecycleTransition) {
		t.Fatalf("RestoreEntry(draft) error = %v, want ErrInvalidLifecycleTransition", err)
	}
}

func TestRestoreEntryRequiresActiveParent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	parent, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	created, _ := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: parent.Chunk.ID, Entry: testEntryCandidate()})
	archivedEntry, _ := service.ArchiveEntry(ctx, EntryLifecycleRequest{EntryID: created.Entry.ID, ExpectedRevision: 1})
	archivedParent := parent.Chunk
	archivedParent.State = knowledge.ChunkStateArchived
	archivedParent.UpdatedAt = archivedParent.UpdatedAt.Add(1)
	archivedParent.Revision = knowledge.Revision{
		Number: 2, ID: "01a01688-fc5d-7f7d-8bb8-de244977f8af",
		Actor: archivedParent.Revision.Actor, CreatedAt: archivedParent.UpdatedAt,
	}
	if err := store.Update(ctx, func(tx knowledgeStore.WriteTx) error { return tx.PutChunk(ctx, archivedParent, 1) }); err != nil {
		t.Fatalf("archive parent fixture: %v", err)
	}
	if _, err := service.RestoreEntry(ctx, EntryLifecycleRequest{EntryID: archivedEntry.Entry.ID, ExpectedRevision: 2}); !errors.Is(err, ErrParentChunkArchived) {
		t.Fatalf("RestoreEntry(archived parent) error = %v, want ErrParentChunkArchived", err)
	}
}
