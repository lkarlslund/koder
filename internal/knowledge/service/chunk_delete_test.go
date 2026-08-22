package service

import (
	"context"
	"errors"
	"testing"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
)

func TestDeleteChunkRequiresConfirmationArchiveAndCurrentRevision(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	created, err := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	if err != nil {
		t.Fatalf("CreateChunk() error = %v", err)
	}
	base := DeleteChunkRequest{ChunkID: created.Chunk.ID, ExpectedRevision: 1}
	if err := service.DeleteChunk(ctx, base); !errors.Is(err, ErrDeleteConfirmationRequired) {
		t.Fatalf("DeleteChunk(unconfirmed) error = %v, want ErrDeleteConfirmationRequired", err)
	}
	base.Confirmed = true
	if err := service.DeleteChunk(ctx, base); !errors.Is(err, ErrChunkMustBeArchived) {
		t.Fatalf("DeleteChunk(active) error = %v, want ErrChunkMustBeArchived", err)
	}
	archived, err := service.ArchiveChunk(ctx, ChunkLifecycleRequest{ChunkID: created.Chunk.ID, ExpectedRevision: 1})
	if err != nil {
		t.Fatalf("ArchiveChunk() error = %v", err)
	}
	if err := service.DeleteChunk(ctx, base); !errors.Is(err, knowledgeStore.ErrConflict) {
		t.Fatalf("DeleteChunk(stale) error = %v, want ErrConflict", err)
	}
	base.ExpectedRevision = archived.Chunk.Revision.Number
	if err := service.DeleteChunk(ctx, base); err != nil {
		t.Fatalf("DeleteChunk() error = %v", err)
	}
	if _, err := service.Chunk(ctx, created.Chunk.ID); !errors.Is(err, knowledgeStore.ErrNotFound) {
		t.Fatalf("Chunk(deleted) error = %v, want ErrNotFound", err)
	}
}

func TestDeleteChunkRejectsNonEmptyChunk(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	chunk := knowledge.Chunk{
		ID: "01a01688-fc5d-7f7d-8bb8-de244977f8a1", Title: "Not empty",
		Kind: knowledge.ChunkKindReference, Scope: knowledge.Scope{Kind: knowledge.ScopeKindGlobal},
		Visibility: knowledge.VisibilityPrivate, State: knowledge.ChunkStateArchived, SchemaVersion: 1,
		Revision: knowledge.Revision{
			Number: 1, ID: "01a01688-fc5d-7f7d-8bb8-de244977f8a2",
			Actor: knowledge.Actor{Kind: knowledge.ActorKindUser, ID: "user:test"}, CreatedAt: serviceTime,
		},
		CreatedAt: serviceTime, UpdatedAt: serviceTime,
	}
	entry := knowledge.Entry{
		ID: "01a01688-fc5d-7f7d-8bb8-de244977f8a3", ChunkID: chunk.ID,
		Kind: knowledge.EntryKindFact, Title: "Child", Scope: knowledge.Scope{Kind: knowledge.ScopeKindGlobal},
		Verification: knowledge.Verification{Status: knowledge.VerificationStatusUnverified}, State: knowledge.EntryStateActive,
		Revision: knowledge.Revision{
			Number: 1, ID: "01a01688-fc5d-7f7d-8bb8-de244977f8a4",
			Actor: knowledge.Actor{Kind: knowledge.ActorKindUser, ID: "user:test"}, CreatedAt: serviceTime,
		},
		CreatedAt: serviceTime, UpdatedAt: serviceTime,
	}
	if err := store.Update(ctx, func(tx knowledgeStore.WriteTx) error {
		if err := tx.PutChunk(ctx, chunk, 0); err != nil {
			return err
		}
		return tx.PutEntry(ctx, entry, 0)
	}); err != nil {
		t.Fatalf("seed non-empty chunk: %v", err)
	}
	service := newTestService(t, store, nil)
	err := service.DeleteChunk(ctx, DeleteChunkRequest{ChunkID: chunk.ID, ExpectedRevision: 1, Confirmed: true})
	if !errors.Is(err, ErrChunkNotEmpty) {
		t.Fatalf("DeleteChunk(non-empty) error = %v, want ErrChunkNotEmpty", err)
	}
	if _, err := service.Chunk(ctx, chunk.ID); err != nil {
		t.Fatalf("rejected deletion removed chunk: %v", err)
	}
}

func TestDeleteChunkReturnsCanonicalDependencyBlockers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	rootID := knowledge.ChunkID("01a01688-fc5d-7f7d-8bb8-de244977f8a1")
	dependentID := knowledge.ChunkID("01a01688-fc5d-7f7d-8bb8-de244977f8a3")
	dependencyID := knowledge.ChunkID("01a01688-fc5d-7f7d-8bb8-de244977f8a5")
	entryID := knowledge.EntryID("01a01688-fc5d-7f7d-8bb8-de244977f8a6")
	linkID := knowledge.LinkID("01a01688-fc5d-7f7d-8bb8-de244977f8a8")
	revision := func(id knowledge.RevisionID) knowledge.Revision {
		return knowledge.Revision{
			Number: 1, ID: id, Actor: knowledge.Actor{Kind: knowledge.ActorKindUser, ID: "user:test"}, CreatedAt: serviceTime,
		}
	}
	root := knowledge.Chunk{
		ID: rootID, Title: "Root", Kind: knowledge.ChunkKindReference,
		Scope: knowledge.Scope{Kind: knowledge.ScopeKindGlobal}, Visibility: knowledge.VisibilityPrivate,
		State: knowledge.ChunkStateArchived, SchemaVersion: 1,
		Revision: revision("01a01688-fc5d-7f7d-8bb8-de244977f8a2"), CreatedAt: serviceTime, UpdatedAt: serviceTime,
		DependencyIDs: []knowledge.ChunkID{dependencyID},
	}
	dependent := root
	dependent.ID = dependentID
	dependent.Title = "Dependent"
	dependent.State = knowledge.ChunkStateActive
	dependent.Revision = revision("01a01688-fc5d-7f7d-8bb8-de244977f8a4")
	dependent.DependencyIDs = []knowledge.ChunkID{rootID}
	entry := knowledge.Entry{
		ID: entryID, ChunkID: rootID, Kind: knowledge.EntryKindFact, Title: "Fact",
		Scope:        knowledge.Scope{Kind: knowledge.ScopeKindGlobal},
		Verification: knowledge.Verification{Status: knowledge.VerificationStatusUnverified}, State: knowledge.EntryStateActive,
		Revision: revision("01a01688-fc5d-7f7d-8bb8-de244977f8a7"), CreatedAt: serviceTime, UpdatedAt: serviceTime,
	}
	link := knowledge.Link{
		ID: linkID, Source: knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(entryID)},
		Target: knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: string(dependentID)},
		Kind:   knowledge.LinkKindRelatedTo, State: knowledge.LinkStateActive,
		Revision: revision("01a01688-fc5d-7f7d-8bb8-de244977f8a9"), CreatedAt: serviceTime, UpdatedAt: serviceTime,
	}
	if err := store.Update(ctx, func(tx knowledgeStore.WriteTx) error {
		if err := tx.PutChunk(ctx, root, 0); err != nil {
			return err
		}
		if err := tx.PutChunk(ctx, dependent, 0); err != nil {
			return err
		}
		if err := tx.PutEntry(ctx, entry, 0); err != nil {
			return err
		}
		return tx.PutLink(ctx, link, 0)
	}); err != nil {
		t.Fatalf("seed dependencies: %v", err)
	}
	service := newTestService(t, store, nil)
	err := service.DeleteChunk(ctx, DeleteChunkRequest{ChunkID: rootID, ExpectedRevision: 1, Confirmed: true})
	var blocked *ChunkDeletionBlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("DeleteChunk(blocked) error = %v, want ChunkDeletionBlockedError", err)
	}
	if !errors.Is(err, ErrChunkNotEmpty) || len(blocked.Blockers.EntryIDs) != 1 || blocked.Blockers.EntryIDs[0] != entryID ||
		len(blocked.Blockers.LinkIDs) != 1 || blocked.Blockers.LinkIDs[0] != linkID ||
		len(blocked.Blockers.DependencyIDs) != 1 || blocked.Blockers.DependencyIDs[0] != dependencyID ||
		len(blocked.Blockers.DependentChunkIDs) != 1 || blocked.Blockers.DependentChunkIDs[0] != dependentID {
		t.Fatalf("deletion blockers = %#v", blocked.Blockers)
	}
	if _, err := service.Chunk(ctx, rootID); err != nil {
		t.Fatalf("blocked deletion removed root: %v", err)
	}
}
