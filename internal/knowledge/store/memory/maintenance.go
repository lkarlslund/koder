package memory

import (
	"context"
	"fmt"
	"math"
	"slices"
	"time"

	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

var _ knowledgeStore.MaintenanceStore = (*Store)(nil)

// ScanCanonical returns a stable, deterministic snapshot ordered by kind and ID.
func (s *Store) ScanCanonical(ctx context.Context, visit func(knowledgeStore.CanonicalRecord) error) (knowledgeStore.ScanStats, error) {
	if err := ctx.Err(); err != nil {
		return knowledgeStore.ScanStats{}, err
	}
	if visit == nil {
		return knowledgeStore.ScanStats{}, fmt.Errorf("scan knowledge memory: callback is required")
	}
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return knowledgeStore.ScanStats{}, knowledgeStore.ErrClosed
	}
	records := make([]knowledgeStore.CanonicalRecord, 0, len(s.data.chunks)+len(s.data.entries)+len(s.data.links)+len(s.data.evidence))
	for _, value := range s.data.chunks {
		value := cloneChunk(value)
		records = append(records, knowledgeStore.CanonicalRecord{Kind: knowledgeStore.RecordKindChunk, Chunk: &value})
	}
	for _, value := range s.data.entries {
		value := cloneEntry(value)
		records = append(records, knowledgeStore.CanonicalRecord{Kind: knowledgeStore.RecordKindEntry, Entry: &value})
	}
	for _, value := range s.data.links {
		value := cloneLink(value)
		records = append(records, knowledgeStore.CanonicalRecord{Kind: knowledgeStore.RecordKindLink, Link: &value})
	}
	for _, value := range s.data.evidence {
		value := value
		records = append(records, knowledgeStore.CanonicalRecord{Kind: knowledgeStore.RecordKindEvidence, Evidence: &value})
	}
	s.mu.RUnlock()
	slices.SortFunc(records, func(left, right knowledgeStore.CanonicalRecord) int {
		if left.Kind != right.Kind {
			return recordKindRank(left.Kind) - recordKindRank(right.Kind)
		}
		return compare(left.ID(), right.ID())
	})

	var stats knowledgeStore.ScanStats
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

func recordKindRank(kind knowledgeStore.RecordKind) int {
	switch kind {
	case knowledgeStore.RecordKindChunk:
		return 0
	case knowledgeStore.RecordKindEntry:
		return 1
	case knowledgeStore.RecordKindLink:
		return 2
	case knowledgeStore.RecordKindEvidence:
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
		return knowledgeStore.ErrClosed
	}
	if s.indexGeneration == math.MaxUint64 {
		return knowledgeStore.ErrIncompatible
	}
	started := time.Now().UTC()
	var stats knowledgeStore.ScanStats
	for _, kindAndCount := range []struct {
		kind  knowledgeStore.RecordKind
		count int
	}{
		{knowledgeStore.RecordKindChunk, len(s.data.chunks)},
		{knowledgeStore.RecordKindEntry, len(s.data.entries)},
		{knowledgeStore.RecordKindLink, len(s.data.links)},
		{knowledgeStore.RecordKindEvidence, len(s.data.evidence)},
	} {
		for range kindAndCount.count {
			if err := ctx.Err(); err != nil {
				return err
			}
			stats.Add(kindAndCount.kind)
		}
	}
	s.indexGeneration++
	s.rebuildStatus = knowledgeStore.IndexRebuildStatus{
		ActiveGeneration: s.indexGeneration,
		Scanned:          stats,
		StartedAt:        started,
		CompletedAt:      time.Now().UTC(),
	}
	return nil
}

func (s *Store) IndexRebuildStatus(ctx context.Context) (knowledgeStore.IndexRebuildStatus, error) {
	if err := ctx.Err(); err != nil {
		return knowledgeStore.IndexRebuildStatus{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return knowledgeStore.IndexRebuildStatus{}, knowledgeStore.ErrClosed
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
