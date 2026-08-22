package pebble

import (
	"context"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

func (s *Store) ListEntries(ctx context.Context, request knowledgeStore.EntryListRequest) (knowledgeStore.EntryPage, error) {
	if err := ctx.Err(); err != nil {
		return knowledgeStore.EntryPage{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return knowledgeStore.EntryPage{}, knowledgeStore.ErrClosed
	}
	snapshot := s.db.NewSnapshot()
	defer func() { _ = snapshot.Close() }()
	entries := make([]knowledge.Entry, 0)
	if _, err := scanCanonical(ctx, snapshot, func(record knowledgeStore.CanonicalRecord) error {
		if record.Kind == knowledgeStore.RecordKindEntry {
			entries = append(entries, *record.Entry)
		}
		return nil
	}); err != nil {
		return knowledgeStore.EntryPage{}, err
	}
	return knowledgeStore.PaginateEntries(entries, request, s.meta.IndexGeneration)
}
