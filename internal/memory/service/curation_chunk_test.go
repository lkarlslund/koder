package service

import (
	"context"
	"testing"

	"github.com/lkarlslund/koder/internal/memory"
	memoryBackend "github.com/lkarlslund/koder/internal/memory/store/memory"
)

func TestEnsureCuratedLearningChunkIsStableAndIdempotent(t *testing.T) {
	t.Parallel()
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	first, err := service.EnsureCuratedLearningChunk(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.EnsureCuratedLearningChunk(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !first.Created || second.Created || first.Chunk.ID != CuratedLearningChunkID || second.Chunk.ID != first.Chunk.ID ||
		first.Chunk.Kind != memory.ChunkKindReference || first.Chunk.Scope.Kind != memory.ScopeKindGlobal {
		t.Fatalf("curated chunks = first %#v second %#v", first, second)
	}
}
