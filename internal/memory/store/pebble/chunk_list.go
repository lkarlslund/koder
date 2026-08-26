package pebble

import (
	"context"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

func (s *Store) ListChunks(ctx context.Context, request memoryStoreAPI.ChunkListRequest) (memoryStoreAPI.ChunkPage, error) {
	if err := ctx.Err(); err != nil {
		return memoryStoreAPI.ChunkPage{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return memoryStoreAPI.ChunkPage{}, memoryStoreAPI.ErrClosed
	}
	snapshot := s.db.NewSnapshot()
	defer func() { _ = snapshot.Close() }()
	chunks := make([]memory.Chunk, 0)
	if _, err := scanCanonical(ctx, snapshot, func(record memoryStoreAPI.CanonicalRecord) error {
		if record.Kind == memoryStoreAPI.RecordKindChunk {
			chunks = append(chunks, *record.Chunk)
		}
		return nil
	}); err != nil {
		return memoryStoreAPI.ChunkPage{}, err
	}
	return memoryStoreAPI.PaginateChunks(chunks, request, s.meta.IndexGeneration)
}
