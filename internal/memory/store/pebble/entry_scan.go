package pebble

import (
	"context"
	"fmt"

	cockroachpebble "github.com/cockroachdb/pebble"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

var _ memoryStoreAPI.EntryScanner = (*Store)(nil)

// ScanEntries walks one Pebble snapshot exactly once. It exists for bounded
// service operations such as lexical corpus construction where re-reading the
// complete canonical set for every list page would become quadratic.
func (s *Store) ScanEntries(ctx context.Context, filter memoryStoreAPI.EntryFilter, visit func(memory.Entry) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if visit == nil {
		return fmt.Errorf("scan memory entries: callback is required")
	}
	normalized, err := memoryStoreAPI.NormalizeEntryFilter(filter)
	if err != nil {
		return err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return memoryStoreAPI.ErrClosed
	}
	snapshot := s.db.NewSnapshot()
	defer func() { _ = snapshot.Close() }()
	prefix := entryPrefix()
	lower, upper := prefixBounds(prefix)
	iter, err := snapshot.NewIter(&cockroachpebble.IterOptions{LowerBound: lower, UpperBound: upper})
	if err != nil {
		return fmt.Errorf("scan memory entries: %w", err)
	}
	defer func() { _ = iter.Close() }()
	for iter.First(); iter.Valid(); iter.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		id := string(iter.Key()[len(prefix):])
		entry, err := decodeRecord[memory.Entry](iter.Value(), "entry", id)
		if err != nil {
			return err
		}
		if string(entry.ID) != id {
			return fmt.Errorf("canonical entry key does not match record ID")
		}
		if memoryStoreAPI.EntryMatchesFilter(entry, normalized) {
			if err := visit(entry); err != nil {
				return err
			}
		}
	}
	if err := iter.Error(); err != nil {
		return fmt.Errorf("scan memory entries: %w", err)
	}
	return nil
}
