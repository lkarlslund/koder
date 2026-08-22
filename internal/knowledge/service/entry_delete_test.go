package service

import (
	"context"
	"errors"
	"testing"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
)

func TestDeleteEntryRequiresConfirmationArchiveAndCurrentRevision(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	parent, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	created, _ := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: parent.Chunk.ID, Entry: testEntryCandidate()})
	base := DeleteEntryRequest{EntryID: created.Entry.ID, ExpectedRevision: 1}
	if err := service.DeleteEntry(ctx, base); !errors.Is(err, ErrDeleteConfirmationRequired) {
		t.Fatalf("DeleteEntry(unconfirmed) error = %v, want ErrDeleteConfirmationRequired", err)
	}
	base.Confirmed = true
	if err := service.DeleteEntry(ctx, base); !errors.Is(err, ErrEntryMustBeArchived) {
		t.Fatalf("DeleteEntry(active) error = %v, want ErrEntryMustBeArchived", err)
	}
	archived, err := service.ArchiveEntry(ctx, EntryLifecycleRequest{EntryID: created.Entry.ID, ExpectedRevision: 1})
	if err != nil {
		t.Fatalf("ArchiveEntry() error = %v", err)
	}
	if err := service.DeleteEntry(ctx, base); !errors.Is(err, knowledgeStore.ErrConflict) {
		t.Fatalf("DeleteEntry(stale) error = %v, want ErrConflict", err)
	}
	base.ExpectedRevision = archived.Entry.Revision.Number
	if err := service.DeleteEntry(ctx, base); err != nil {
		t.Fatalf("DeleteEntry() error = %v", err)
	}
	if _, err := service.Entry(ctx, created.Entry.ID); !errors.Is(err, knowledgeStore.ErrNotFound) {
		t.Fatalf("Entry(deleted) error = %v, want ErrNotFound", err)
	}
	updatedParent, err := service.Chunk(ctx, parent.Chunk.ID)
	if err != nil || updatedParent.Counts.Entries != 0 {
		t.Fatalf("parent after deletion = %#v, %v", updatedParent, err)
	}
}

func TestDeleteEntryReturnsLinkAndSupersessionBlockers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	parent, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	target, _ := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: parent.Chunk.ID, Entry: testEntryCandidate()})
	archived, _ := service.ArchiveEntry(ctx, EntryLifecycleRequest{EntryID: target.Entry.ID, ExpectedRevision: 1})
	source := target.Entry
	source.ID = "01a01688-fc5d-7f7d-8bb8-de244977f8aa"
	source.Title = "Old fact"
	source.State = knowledge.EntryStateSuperseded
	source.SupersededByID = target.Entry.ID
	source.Revision = knowledge.Revision{
		Number: 1, ID: "01a01688-fc5d-7f7d-8bb8-de244977f8ab",
		Actor: source.Revision.Actor, CreatedAt: source.CreatedAt,
	}
	link := knowledge.Link{
		ID:     "01a01688-fc5d-7f7d-8bb8-de244977f8ac",
		Source: knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(target.Entry.ID)},
		Target: knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: string(parent.Chunk.ID)},
		Kind:   knowledge.LinkKindRelatedTo, State: knowledge.LinkStateActive,
		Revision: knowledge.Revision{
			Number: 1, ID: "01a01688-fc5d-7f7d-8bb8-de244977f8ad",
			Actor: source.Revision.Actor, CreatedAt: source.CreatedAt,
		},
		CreatedAt: source.CreatedAt, UpdatedAt: source.UpdatedAt,
	}
	if err := store.Update(ctx, func(tx knowledgeStore.WriteTx) error {
		if err := tx.PutEntry(ctx, source, 0); err != nil {
			return err
		}
		return tx.PutLink(ctx, link, 0)
	}); err != nil {
		t.Fatalf("seed blockers: %v", err)
	}
	err := service.DeleteEntry(ctx, DeleteEntryRequest{
		EntryID: target.Entry.ID, ExpectedRevision: archived.Entry.Revision.Number, Confirmed: true,
	})
	var blocked *EntryDeletionBlockedError
	if !errors.As(err, &blocked) || !errors.Is(err, ErrEntryDeletionBlocked) {
		t.Fatalf("DeleteEntry(blocked) error = %v, want EntryDeletionBlockedError", err)
	}
	if len(blocked.Blockers.LinkIDs) != 1 || blocked.Blockers.LinkIDs[0] != link.ID ||
		len(blocked.Blockers.SupersededEntryIDs) != 1 || blocked.Blockers.SupersededEntryIDs[0] != source.ID {
		t.Fatalf("entry deletion blockers = %#v", blocked.Blockers)
	}
	if _, err := service.Entry(ctx, target.Entry.ID); err != nil {
		t.Fatalf("blocked deletion removed target: %v", err)
	}
}
