package service

import (
	"context"
	"errors"
	"testing"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
	memoryBackend "github.com/lkarlslund/koder/internal/memory/store/memory"
)

func TestArchiveAndRestoreChunkAdvanceLifecycleRevisions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	created, err := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	if err != nil {
		t.Fatalf("CreateChunk() error = %v", err)
	}
	archived, err := service.ArchiveChunk(ctx, ChunkLifecycleRequest{
		ChunkID: created.Chunk.ID, ExpectedRevision: 1,
	})
	if err != nil {
		t.Fatalf("ArchiveChunk() error = %v", err)
	}
	if !archived.Updated || archived.Chunk.State != memory.ChunkStateArchived || archived.Chunk.Revision.Number != 2 || archived.Chunk.Revision.Reason != "archive chunk" {
		t.Fatalf("ArchiveChunk() = %#v", archived)
	}
	page, err := service.ListChunks(ctx, memoryStoreAPI.ChunkListRequest{})
	if err != nil || len(page.Chunks) != 0 {
		t.Fatalf("default list after archive = %#v, %v", page, err)
	}

	restored, err := service.RestoreChunk(ctx, ChunkLifecycleRequest{
		ChunkID: archived.Chunk.ID, ExpectedRevision: 2, Reason: "needed again",
	})
	if err != nil {
		t.Fatalf("RestoreChunk() error = %v", err)
	}
	if !restored.Updated || restored.Chunk.State != memory.ChunkStateActive || restored.Chunk.Revision.Number != 3 || restored.Chunk.Revision.Reason != "needed again" {
		t.Fatalf("RestoreChunk() = %#v", restored)
	}
	if !restored.Chunk.UpdatedAt.After(archived.Chunk.UpdatedAt) {
		t.Fatalf("restore timestamp %v did not advance beyond %v", restored.Chunk.UpdatedAt, archived.Chunk.UpdatedAt)
	}
	page, err = service.ListChunks(ctx, memoryStoreAPI.ChunkListRequest{})
	if err != nil || len(page.Chunks) != 1 || page.Chunks[0].ID != restored.Chunk.ID {
		t.Fatalf("default list after restore = %#v, %v", page, err)
	}
}

func TestChunkLifecycleIsIdempotentAtCurrentRevisionAndRejectsStaleCalls(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	created, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	archived, err := service.ArchiveChunk(ctx, ChunkLifecycleRequest{ChunkID: created.Chunk.ID, ExpectedRevision: 1})
	if err != nil {
		t.Fatalf("ArchiveChunk() error = %v", err)
	}
	noOp, err := service.ArchiveChunk(ctx, ChunkLifecycleRequest{ChunkID: created.Chunk.ID, ExpectedRevision: 2})
	if err != nil || noOp.Updated || noOp.Chunk.Revision.Number != 2 {
		t.Fatalf("idempotent ArchiveChunk() = %#v, %v", noOp, err)
	}
	if _, err := service.RestoreChunk(ctx, ChunkLifecycleRequest{ChunkID: created.Chunk.ID, ExpectedRevision: 1}); !errors.Is(err, memoryStoreAPI.ErrConflict) {
		t.Fatalf("stale RestoreChunk() error = %v, want ErrConflict", err)
	}
	got, _ := service.Chunk(ctx, created.Chunk.ID)
	if got.State != memory.ChunkStateArchived || got.Revision != archived.Chunk.Revision {
		t.Fatalf("stale lifecycle call changed chunk: %#v", got)
	}
}

func TestRestoreChunkRejectsDraftTransition(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	candidate := testChunkCandidate()
	candidate.State = memory.ChunkStateDraft
	created, err := service.CreateChunk(ctx, CreateChunkRequest{Chunk: candidate})
	if err != nil {
		t.Fatalf("CreateChunk(draft) error = %v", err)
	}
	if _, err := service.RestoreChunk(ctx, ChunkLifecycleRequest{ChunkID: created.Chunk.ID, ExpectedRevision: 1}); !errors.Is(err, ErrInvalidLifecycleTransition) {
		t.Fatalf("RestoreChunk(draft) error = %v, want ErrInvalidLifecycleTransition", err)
	}
	got, _ := service.Chunk(ctx, created.Chunk.ID)
	if got.State != memory.ChunkStateDraft || got.Revision.Number != 1 {
		t.Fatalf("rejected restore changed draft: %#v", got)
	}
}
