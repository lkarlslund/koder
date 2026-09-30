package pebble

import (
	"context"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

func (s *Store) ListChunks(ctx context.Context, request memoryStoreAPI.ChunkListRequest) (memoryStoreAPI.ChunkPage, error) {
	return listCanonical(ctx, s, memoryStoreAPI.RecordKindChunk,
		func(record memoryStoreAPI.CanonicalRecord) memory.Chunk { return *record.Chunk },
		func(chunks []memory.Chunk, generation uint64) (memoryStoreAPI.ChunkPage, error) {
			return memoryStoreAPI.PaginateChunks(chunks, request, generation)
		})
}

func (s *Store) ListEntries(ctx context.Context, request memoryStoreAPI.EntryListRequest) (memoryStoreAPI.EntryPage, error) {
	return listCanonical(ctx, s, memoryStoreAPI.RecordKindEntry,
		func(record memoryStoreAPI.CanonicalRecord) memory.Entry { return *record.Entry },
		func(entries []memory.Entry, generation uint64) (memoryStoreAPI.EntryPage, error) {
			return memoryStoreAPI.PaginateEntries(entries, request, generation)
		})
}

// listCanonical collects one kind of canonical record from a consistent
// snapshot and pages them with the store's current index generation.
func listCanonical[T, Page any](ctx context.Context, s *Store, kind memoryStoreAPI.RecordKind, pick func(memoryStoreAPI.CanonicalRecord) T, page func([]T, uint64) (Page, error)) (Page, error) {
	var zero Page
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return zero, memoryStoreAPI.ErrClosed
	}
	snapshot := s.db.NewSnapshot()
	defer func() { _ = snapshot.Close() }()
	records := make([]T, 0)
	if _, err := scanCanonical(ctx, snapshot, func(record memoryStoreAPI.CanonicalRecord) error {
		if record.Kind == kind {
			records = append(records, pick(record))
		}
		return nil
	}); err != nil {
		return zero, err
	}
	return page(records, s.meta.IndexGeneration)
}
