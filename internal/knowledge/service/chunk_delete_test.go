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
		CreatedAt: serviceTime, UpdatedAt: serviceTime, Counts: knowledge.ChunkCounts{Entries: 1},
	}
	if err := store.Update(ctx, func(tx knowledgeStore.WriteTx) error { return tx.PutChunk(ctx, chunk, 0) }); err != nil {
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
