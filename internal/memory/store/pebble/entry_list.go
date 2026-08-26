package pebble

import (
	"context"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

func (s *Store) ListEntries(ctx context.Context, request memoryStoreAPI.EntryListRequest) (memoryStoreAPI.EntryPage, error) {
	if err := ctx.Err(); err != nil {
		return memoryStoreAPI.EntryPage{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return memoryStoreAPI.EntryPage{}, memoryStoreAPI.ErrClosed
	}
	snapshot := s.db.NewSnapshot()
	defer func() { _ = snapshot.Close() }()
	entries := make([]memory.Entry, 0)
	if _, err := scanCanonical(ctx, snapshot, func(record memoryStoreAPI.CanonicalRecord) error {
		if record.Kind == memoryStoreAPI.RecordKindEntry {
			entries = append(entries, *record.Entry)
		}
		return nil
	}); err != nil {
		return memoryStoreAPI.EntryPage{}, err
	}
	return memoryStoreAPI.PaginateEntries(entries, request, s.meta.IndexGeneration)
}
