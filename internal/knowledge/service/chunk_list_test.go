package service

import (
	"context"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
)

func TestListChunksDefaultsToActiveButAllowsExplicitLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	created, err := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	if err != nil {
		t.Fatalf("CreateChunk() error = %v", err)
	}
	archived := created.Chunk
	archived.State = knowledge.ChunkStateArchived
	archived.UpdatedAt = archived.UpdatedAt.Add(time.Nanosecond)
	archived.Revision = knowledge.Revision{
		ID: "01a01688-fc5d-7f7d-8bb8-de244977f8a3", Number: 2,
		Actor: archived.Revision.Actor, CreatedAt: archived.UpdatedAt,
	}
	if err := store.Update(ctx, func(tx knowledgeStore.WriteTx) error {
		return tx.PutChunk(ctx, archived, 1)
	}); err != nil {
		t.Fatalf("archive fixture: %v", err)
	}

	page, err := service.ListChunks(ctx, knowledgeStore.ChunkListRequest{})
	if err != nil {
		t.Fatalf("ListChunks(default) error = %v", err)
	}
	if len(page.Chunks) != 0 {
		t.Fatalf("ListChunks(default) returned archived chunks: %#v", page.Chunks)
	}
	page, err = service.ListChunks(ctx, knowledgeStore.ChunkListRequest{
		Filter: knowledgeStore.ChunkFilter{States: []knowledge.ChunkState{knowledge.ChunkStateArchived}},
	})
	if err != nil {
		t.Fatalf("ListChunks(archived) error = %v", err)
	}
	if len(page.Chunks) != 1 || page.Chunks[0].ID != archived.ID {
		t.Fatalf("ListChunks(archived) = %#v", page)
	}
}
