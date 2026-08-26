package service

import (
	"context"
	"testing"

	memoryBackend "github.com/lkarlslund/koder/internal/memory/store/memory"
)

func TestMarkChunkUsedDoesNotAdvanceContentRevision(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	created, err := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	if err != nil {
		t.Fatalf("CreateChunk() error = %v", err)
	}
	used, err := service.MarkChunkUsed(ctx, created.Chunk.ID)
	if err != nil {
		t.Fatalf("MarkChunkUsed() error = %v", err)
	}
	if !used.LastUsedAt.Equal(serviceTime) || used.Revision != created.Chunk.Revision || !used.UpdatedAt.Equal(created.Chunk.UpdatedAt) {
		t.Fatalf("used chunk changed content metadata: before=%#v after=%#v", created.Chunk, used)
	}
	again, err := service.MarkChunkUsed(ctx, created.Chunk.ID)
	if err != nil || !again.LastUsedAt.Equal(used.LastUsedAt) || again.Revision != used.Revision {
		t.Fatalf("repeated MarkChunkUsed() = %#v, %v", again, err)
	}
}
