package memory

import (
	"context"
	"fmt"
	"math"
	"slices"
	"time"

	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

var _ memoryStoreAPI.MaintenanceStore = (*Store)(nil)

// ScanCanonical returns a stable, deterministic snapshot ordered by kind and ID.
func (s *Store) ScanCanonical(ctx context.Context, visit func(memoryStoreAPI.CanonicalRecord) error) (memoryStoreAPI.ScanStats, error) {
	if err := ctx.Err(); err != nil {
		return memoryStoreAPI.ScanStats{}, err
	}
	if visit == nil {
		return memoryStoreAPI.ScanStats{}, fmt.Errorf("scan in-memory store: callback is required")
	}
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return memoryStoreAPI.ScanStats{}, memoryStoreAPI.ErrClosed
	}
	records := make([]memoryStoreAPI.CanonicalRecord, 0, len(s.data.chunks)+len(s.data.entries)+len(s.data.links)+len(s.data.evidence))
	for _, value := range s.data.chunks {
		value := cloneChunk(value)
		records = append(records, memoryStoreAPI.CanonicalRecord{Kind: memoryStoreAPI.RecordKindChunk, Chunk: &value})
	}
	for _, value := range s.data.entries {
		value := cloneEntry(value)
		records = append(records, memoryStoreAPI.CanonicalRecord{Kind: memoryStoreAPI.RecordKindEntry, Entry: &value})
	}
	for _, value := range s.data.links {
		value := cloneLink(value)
		records = append(records, memoryStoreAPI.CanonicalRecord{Kind: memoryStoreAPI.RecordKindLink, Link: &value})
	}
	for _, value := range s.data.evidence {
		value := value
		records = append(records, memoryStoreAPI.CanonicalRecord{Kind: memoryStoreAPI.RecordKindEvidence, Evidence: &value})
	}
	s.mu.RUnlock()
	slices.SortFunc(records, func(left, right memoryStoreAPI.CanonicalRecord) int {
		if left.Kind != right.Kind {
			return recordKindRank(left.Kind) - recordKindRank(right.Kind)
		}
		return compare(left.ID(), right.ID())
	})

	var stats memoryStoreAPI.ScanStats
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		stats.Add(record.Kind)
		if err := visit(record); err != nil {
			return stats, err
		}
	}
	return stats, nil
}

func recordKindRank(kind memoryStoreAPI.RecordKind) int {
	switch kind {
	case memoryStoreAPI.RecordKindChunk:
		return 0
	case memoryStoreAPI.RecordKindEntry:
		return 1
	case memoryStoreAPI.RecordKindLink:
		return 2
	case memoryStoreAPI.RecordKindEvidence:
		return 3
	default:
		return 4
	}
}

func (s *Store) RebuildIndexes(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return memoryStoreAPI.ErrClosed
	}
	if s.indexGeneration == math.MaxUint64 {
		return memoryStoreAPI.ErrIncompatible
	}
	started := time.Now().UTC()
	var stats memoryStoreAPI.ScanStats
	for _, kindAndCount := range []struct {
		kind  memoryStoreAPI.RecordKind
		count int
	}{
		{memoryStoreAPI.RecordKindChunk, len(s.data.chunks)},
		{memoryStoreAPI.RecordKindEntry, len(s.data.entries)},
		{memoryStoreAPI.RecordKindLink, len(s.data.links)},
		{memoryStoreAPI.RecordKindEvidence, len(s.data.evidence)},
	} {
		for range kindAndCount.count {
			if err := ctx.Err(); err != nil {
				return err
			}
			stats.Add(kindAndCount.kind)
		}
	}
	s.indexGeneration++
	s.rebuildStatus = memoryStoreAPI.IndexRebuildStatus{
		ActiveGeneration: s.indexGeneration,
		Scanned:          stats,
		StartedAt:        started,
		CompletedAt:      time.Now().UTC(),
	}
	return nil
}

func (s *Store) IndexRebuildStatus(ctx context.Context) (memoryStoreAPI.IndexRebuildStatus, error) {
	if err := ctx.Err(); err != nil {
		return memoryStoreAPI.IndexRebuildStatus{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return memoryStoreAPI.IndexRebuildStatus{}, memoryStoreAPI.ErrClosed
	}
	return s.rebuildStatus, nil
}

func compare(left, right string) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}
