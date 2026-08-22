package pebble

import (
	"context"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

func (s *Store) ListChunks(ctx context.Context, request knowledgeStore.ChunkListRequest) (knowledgeStore.ChunkPage, error) {
	if err := ctx.Err(); err != nil {
		return knowledgeStore.ChunkPage{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return knowledgeStore.ChunkPage{}, knowledgeStore.ErrClosed
	}
	snapshot := s.db.NewSnapshot()
	defer func() { _ = snapshot.Close() }()
	chunks := make([]knowledge.Chunk, 0)
	if _, err := scanCanonical(ctx, snapshot, func(record knowledgeStore.CanonicalRecord) error {
		if record.Kind == knowledgeStore.RecordKindChunk {
			chunks = append(chunks, *record.Chunk)
		}
		return nil
	}); err != nil {
		return knowledgeStore.ChunkPage{}, err
	}
	return knowledgeStore.PaginateChunks(chunks, request, s.meta.IndexGeneration)
}
