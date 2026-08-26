package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
	memoryBackend "github.com/lkarlslund/koder/internal/memory/store/memory"
)

func TestListChunksDefaultsToActiveButAllowsExplicitLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	created, err := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	if err != nil {
		t.Fatalf("CreateChunk() error = %v", err)
	}
	archived := created.Chunk
	archived.State = memory.ChunkStateArchived
	archived.UpdatedAt = archived.UpdatedAt.Add(time.Nanosecond)
	archived.Revision = memory.Revision{
		ID: "01a01688-fc5d-7f7d-8bb8-de244977f8a3", Number: 2,
		Actor: archived.Revision.Actor, CreatedAt: archived.UpdatedAt,
	}
	if err := store.Update(ctx, func(tx memoryStoreAPI.WriteTx) error {
		return tx.PutChunk(ctx, archived, 1)
	}); err != nil {
		t.Fatalf("archive fixture: %v", err)
	}

	page, err := service.ListChunks(ctx, memoryStoreAPI.ChunkListRequest{})
	if err != nil {
		t.Fatalf("ListChunks(default) error = %v", err)
	}
	if len(page.Chunks) != 0 {
		t.Fatalf("ListChunks(default) returned archived chunks: %#v", page.Chunks)
	}
	page, err = service.ListChunks(ctx, memoryStoreAPI.ChunkListRequest{
		Filter: memoryStoreAPI.ChunkFilter{States: []memory.ChunkState{memory.ChunkStateArchived}},
	})
	if err != nil {
		t.Fatalf("ListChunks(archived) error = %v", err)
	}
	if len(page.Chunks) != 1 || page.Chunks[0].ID != archived.ID {
		t.Fatalf("ListChunks(archived) = %#v", page)
	}
}

func TestListChunksFiltersDeniedChunksWithoutLeakingPagination(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	for _, title := range []string{"Alpha hidden", "Bravo visible", "Charlie visible"} {
		candidate := testChunkCandidate()
		candidate.Title = title
		if _, err := service.CreateChunk(ctx, CreateChunkRequest{Chunk: candidate}); err != nil {
			t.Fatalf("CreateChunk(%q) error = %v", title, err)
		}
	}
	service.chunkPolicy = ChunkPolicyFunc(func(_ context.Context, actor memory.Actor, action ChunkPolicyAction, chunk memory.Chunk) error {
		if actor.ID != "user:test" || action != ChunkPolicyRead {
			t.Fatalf("policy actor=%#v action=%q", actor, action)
		}
		if chunk.Title == "Alpha hidden" {
			return errors.New("hidden by test policy")
		}
		return nil
	})

	request := memoryStoreAPI.ChunkListRequest{Sort: memoryStoreAPI.ChunkSortTitle, Limit: 1}
	first, err := service.ListChunks(ctx, request)
	if err != nil {
		t.Fatalf("ListChunks(first) error = %v", err)
	}
	if len(first.Chunks) != 1 || first.Chunks[0].Title != "Bravo visible" || first.NextCursor == "" {
		t.Fatalf("ListChunks(first) = %#v", first)
	}
	request.Cursor = first.NextCursor
	second, err := service.ListChunks(ctx, request)
	if err != nil {
		t.Fatalf("ListChunks(second) error = %v", err)
	}
	if len(second.Chunks) != 1 || second.Chunks[0].Title != "Charlie visible" || second.NextCursor != "" {
		t.Fatalf("ListChunks(second) = %#v", second)
	}
}
