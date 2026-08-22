package pebble

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"

	cockroachpebble "github.com/cockroachdb/pebble"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

const rebuildBatchEntries = 1000

var validIndexName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)

type indexEntry struct {
	Suffix []byte
	Value  []byte
}

type indexDefinition struct {
	name  string
	build func(context.Context, knowledgeStore.CanonicalRecord) ([]indexEntry, error)
}

var _ knowledgeStore.MaintenanceStore = (*Store)(nil)

func (s *Store) ScanCanonical(ctx context.Context, visit func(knowledgeStore.CanonicalRecord) error) (knowledgeStore.ScanStats, error) {
	if err := ctx.Err(); err != nil {
		return knowledgeStore.ScanStats{}, err
	}
	if visit == nil {
		return knowledgeStore.ScanStats{}, fmt.Errorf("scan knowledge pebble: callback is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return knowledgeStore.ScanStats{}, knowledgeStore.ErrClosed
	}
	snapshot := s.db.NewSnapshot()
	defer func() { _ = snapshot.Close() }()
	return scanCanonical(ctx, snapshot, visit)
}

type iteratorReader interface {
	NewIter(*cockroachpebble.IterOptions) (*cockroachpebble.Iterator, error)
}

func scanCanonical(ctx context.Context, reader iteratorReader, visit func(knowledgeStore.CanonicalRecord) error) (knowledgeStore.ScanStats, error) {
	lower, upper := prefixBounds(canonicalPrefix)
	iter, err := reader.NewIter(&cockroachpebble.IterOptions{LowerBound: lower, UpperBound: upper})
	if err != nil {
		return knowledgeStore.ScanStats{}, fmt.Errorf("scan canonical knowledge: %w", err)
	}
	defer func() { _ = iter.Close() }()
	var stats knowledgeStore.ScanStats
	for iter.First(); iter.Valid(); iter.Next() {
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		record, err := decodeCanonicalRecord(iter.Key(), iter.Value())
		if err != nil {
			return stats, err
		}
		stats.Add(record.Kind)
		if err := visit(record); err != nil {
			return stats, err
		}
	}
	if err := iter.Error(); err != nil {
		return stats, fmt.Errorf("scan canonical knowledge: %w", err)
	}
	return stats, nil
}

func decodeCanonicalRecord(key, data []byte) (knowledgeStore.CanonicalRecord, error) {
	offset := len(canonicalPrefix)
	if len(key) <= offset+2 || !bytes.HasPrefix(key, canonicalPrefix) || key[offset+1] != '/' {
		return knowledgeStore.CanonicalRecord{}, fmt.Errorf("invalid canonical knowledge key")
	}
	id := string(key[offset+2:])
	switch key[offset] {
	case recordChunk:
		value, err := decodeRecord[knowledge.Chunk](data, "chunk", id)
		if err != nil {
			return knowledgeStore.CanonicalRecord{}, err
		}
		if string(value.ID) != id {
			return knowledgeStore.CanonicalRecord{}, fmt.Errorf("canonical chunk key does not match record ID")
		}
		return knowledgeStore.CanonicalRecord{Kind: knowledgeStore.RecordKindChunk, Chunk: &value}, nil
	case recordEntry:
		value, err := decodeRecord[knowledge.Entry](data, "entry", id)
		if err != nil {
			return knowledgeStore.CanonicalRecord{}, err
		}
		if string(value.ID) != id {
			return knowledgeStore.CanonicalRecord{}, fmt.Errorf("canonical entry key does not match record ID")
		}
		return knowledgeStore.CanonicalRecord{Kind: knowledgeStore.RecordKindEntry, Entry: &value}, nil
	case recordLink:
		value, err := decodeRecord[knowledge.Link](data, "link", id)
		if err != nil {
			return knowledgeStore.CanonicalRecord{}, err
		}
		if string(value.ID) != id {
			return knowledgeStore.CanonicalRecord{}, fmt.Errorf("canonical link key does not match record ID")
		}
		return knowledgeStore.CanonicalRecord{Kind: knowledgeStore.RecordKindLink, Link: &value}, nil
	case recordEvidence:
		value, err := decodeRecord[knowledge.Evidence](data, "evidence", id)
		if err != nil {
			return knowledgeStore.CanonicalRecord{}, err
		}
		if string(value.ID) != id {
			return knowledgeStore.CanonicalRecord{}, fmt.Errorf("canonical evidence key does not match record ID")
		}
		return knowledgeStore.CanonicalRecord{Kind: knowledgeStore.RecordKindEvidence, Evidence: &value}, nil
	default:
		return knowledgeStore.CanonicalRecord{}, fmt.Errorf("unknown canonical knowledge record kind %q", key[offset])
	}
}

func (s *Store) RebuildIndexes(ctx context.Context) (rebuildErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !s.rebuildMu.TryLock() {
		return fmt.Errorf("%w: knowledge index rebuild already running", knowledgeStore.ErrConflict)
	}
	defer s.rebuildMu.Unlock()
	var target uint64
	var activated bool
	var snapshot *cockroachpebble.Snapshot
	started := time.Now().UTC()
	defer func() {
		if snapshot != nil {
			_ = snapshot.Close()
		}
		if rebuildErr == nil {
			return
		}
		if !activated && target != 0 {
			s.mu.Lock()
			if s.rebuildTarget == target {
				s.rebuildTarget = 0
				s.rebuildJournal = nil
				s.rebuildJournalOverflow = false
			}
			s.mu.Unlock()
			_ = deleteIndexGeneration(s.db, target)
		}
		status := s.currentRebuildStatus()
		status.Running = false
		status.Canceled = errors.Is(rebuildErr, context.Canceled)
		status.CompletedAt = time.Now().UTC()
		if status.Canceled {
			status.LastError = ""
		} else {
			status.LastError = "knowledge index rebuild failed"
		}
		s.setRebuildStatus(status)
	}()

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return knowledgeStore.ErrClosed
	}
	if s.meta.IndexGeneration == math.MaxUint64 {
		s.mu.Unlock()
		return fmt.Errorf("%w: knowledge index generation exhausted", knowledgeStore.ErrIncompatible)
	}
	definitions, err := validateIndexDefinitions(s.indexes)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	target = s.meta.IndexGeneration + 1
	if err := deleteIndexGeneration(s.db, target); err != nil {
		s.mu.Unlock()
		return err
	}
	s.rebuildTarget = target
	s.rebuildJournal = nil
	s.rebuildJournalOverflow = false
	snapshot = s.db.NewSnapshot()
	s.setRebuildStatus(knowledgeStore.IndexRebuildStatus{
		Running:          true,
		ActiveGeneration: s.meta.IndexGeneration,
		TargetGeneration: target,
		StartedAt:        started,
	})
	s.mu.Unlock()

	writer := newIndexWriter(s.db, target)
	stats, err := scanCanonical(ctx, snapshot, func(record knowledgeStore.CanonicalRecord) error {
		for _, definition := range definitions {
			entries, err := definition.build(ctx, record)
			if err != nil {
				return fmt.Errorf("build knowledge index %s for %s %s: %w", definition.name, record.Kind, record.ID(), err)
			}
			for _, entry := range entries {
				if err := writer.add(definition.name, entry); err != nil {
					return err
				}
			}
		}
		status := s.currentRebuildStatus()
		status.Scanned.Add(record.Kind)
		s.setRebuildStatus(status)
		return nil
	})
	_ = snapshot.Close()
	snapshot = nil
	if err != nil {
		_ = writer.close()
		return err
	}
	if err := writer.flush(); err != nil {
		_ = writer.close()
		return err
	}
	if err := writer.close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	if s.rebuildJournalOverflow {
		s.mu.Unlock()
		return fmt.Errorf("%w: knowledge index rebuild mutation journal exceeded %d events", knowledgeStore.ErrConflict, maxRebuildJournalMutations)
	}
	if err := replayIndexMutations(ctx, s.db, target, s.rebuildJournal); err != nil {
		s.mu.Unlock()
		return err
	}
	nextMetadata := s.meta
	nextMetadata.IndexGeneration = target
	encoded, err := json.Marshal(nextMetadata)
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("encode knowledge metadata after index rebuild: %w", err)
	}
	if err := s.db.Set(metadataKey(), encoded, cockroachpebble.Sync); err != nil {
		s.mu.Unlock()
		return fmt.Errorf("activate knowledge index generation: %w", err)
	}
	oldGeneration := s.meta.IndexGeneration
	s.meta = nextMetadata
	s.rebuildTarget = 0
	s.rebuildJournal = nil
	s.rebuildJournalOverflow = false
	activated = true
	s.mu.Unlock()
	s.setRebuildStatus(knowledgeStore.IndexRebuildStatus{
		ActiveGeneration: target,
		Scanned:          stats,
		StartedAt:        started,
	})
	if err := deleteIndexGeneration(s.db, oldGeneration); err != nil {
		return err
	}
	s.setRebuildStatus(knowledgeStore.IndexRebuildStatus{
		ActiveGeneration: target,
		Scanned:          stats,
		StartedAt:        started,
		CompletedAt:      time.Now().UTC(),
	})
	return nil
}

func (s *Store) IndexRebuildStatus(ctx context.Context) (knowledgeStore.IndexRebuildStatus, error) {
	if err := ctx.Err(); err != nil {
		return knowledgeStore.IndexRebuildStatus{}, err
	}
	return s.currentRebuildStatus(), nil
}

func (s *Store) currentRebuildStatus() knowledgeStore.IndexRebuildStatus {
	s.statusMu.RLock()
	defer s.statusMu.RUnlock()
	return s.rebuildStatus
}

func (s *Store) setRebuildStatus(status knowledgeStore.IndexRebuildStatus) {
	s.statusMu.Lock()
	s.rebuildStatus = status
	s.statusMu.Unlock()
}

func validateIndexDefinitions(definitions []indexDefinition) ([]indexDefinition, error) {
	definitions = slices.Clone(definitions)
	slices.SortFunc(definitions, func(left, right indexDefinition) int { return strings.Compare(left.name, right.name) })
	for index, definition := range definitions {
		if !validIndexName.MatchString(definition.name) || definition.build == nil {
			return nil, fmt.Errorf("%w: invalid knowledge index definition %q", knowledgeStore.ErrIncompatible, definition.name)
		}
		if index > 0 && definitions[index-1].name == definition.name {
			return nil, fmt.Errorf("%w: duplicate knowledge index definition %q", knowledgeStore.ErrIncompatible, definition.name)
		}
	}
	return definitions, nil
}

type indexWriter struct {
	db         *cockroachpebble.DB
	generation uint64
	batch      *cockroachpebble.Batch
	entries    int
}

func newIndexWriter(db *cockroachpebble.DB, generation uint64) *indexWriter {
	return &indexWriter{db: db, generation: generation, batch: db.NewBatch()}
}

func (w *indexWriter) add(name string, entry indexEntry) error {
	if len(entry.Suffix) == 0 {
		return fmt.Errorf("knowledge index %s produced an empty key suffix", name)
	}
	if err := w.batch.Set(indexKey(w.generation, name, entry.Suffix), entry.Value, nil); err != nil {
		return fmt.Errorf("write knowledge index %s: %w", name, err)
	}
	w.entries++
	if w.entries >= rebuildBatchEntries {
		return w.flush()
	}
	return nil
}

func (w *indexWriter) flush() error {
	if w.entries == 0 {
		return nil
	}
	if err := w.batch.Commit(cockroachpebble.Sync); err != nil {
		return fmt.Errorf("commit knowledge index rebuild batch: %w", err)
	}
	if err := w.batch.Close(); err != nil {
		return fmt.Errorf("close knowledge index rebuild batch: %w", err)
	}
	w.batch = w.db.NewBatch()
	w.entries = 0
	return nil
}

func (w *indexWriter) close() error {
	if w.batch == nil {
		return nil
	}
	err := w.batch.Close()
	w.batch = nil
	return err
}

func replayIndexMutations(ctx context.Context, db *cockroachpebble.DB, generation uint64, mutations []indexMutation) error {
	writer := newIndexMutationWriter(db, generation)
	defer func() { _ = writer.close() }()
	for _, mutation := range mutations {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := writer.apply(mutation); err != nil {
			return err
		}
	}
	if err := writer.flush(); err != nil {
		return err
	}
	return writer.close()
}

type indexMutationWriter struct {
	db         *cockroachpebble.DB
	generation uint64
	batch      *cockroachpebble.Batch
	operations int
}

func newIndexMutationWriter(db *cockroachpebble.DB, generation uint64) *indexMutationWriter {
	return &indexMutationWriter{db: db, generation: generation, batch: db.NewBatch()}
}

func (w *indexMutationWriter) apply(mutation indexMutation) error {
	for _, name := range sortedIndexNames(mutation.delete) {
		for _, entry := range sortedIndexEntries(mutation.delete[name]) {
			if err := w.batch.Delete(indexKey(w.generation, name, entry.Suffix), nil); err != nil {
				return fmt.Errorf("replay knowledge index %s deletion: %w", name, err)
			}
			if err := w.countOperation(); err != nil {
				return err
			}
		}
	}
	for _, name := range sortedIndexNames(mutation.put) {
		for _, entry := range sortedIndexEntries(mutation.put[name]) {
			if err := w.batch.Set(indexKey(w.generation, name, entry.Suffix), entry.Value, nil); err != nil {
				return fmt.Errorf("replay knowledge index %s value: %w", name, err)
			}
			if err := w.countOperation(); err != nil {
				return err
			}
		}
	}
	return nil
}

func (w *indexMutationWriter) countOperation() error {
	w.operations++
	if w.operations >= rebuildBatchEntries {
		return w.flush()
	}
	return nil
}

func (w *indexMutationWriter) flush() error {
	if w.operations == 0 {
		return nil
	}
	if err := w.batch.Commit(cockroachpebble.Sync); err != nil {
		return fmt.Errorf("commit knowledge index mutation replay: %w", err)
	}
	if err := w.batch.Close(); err != nil {
		return fmt.Errorf("close knowledge index mutation replay batch: %w", err)
	}
	w.batch = w.db.NewBatch()
	w.operations = 0
	return nil
}

func (w *indexMutationWriter) close() error {
	if w.batch == nil {
		return nil
	}
	err := w.batch.Close()
	w.batch = nil
	return err
}

func sortedIndexNames(entries map[string][]indexEntry) []string {
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func sortedIndexEntries(entries []indexEntry) []indexEntry {
	entries = slices.Clone(entries)
	slices.SortFunc(entries, func(left, right indexEntry) int {
		if compared := bytes.Compare(left.Suffix, right.Suffix); compared != 0 {
			return compared
		}
		return bytes.Compare(left.Value, right.Value)
	})
	return entries
}

func deleteIndexGeneration(db *cockroachpebble.DB, generation uint64) error {
	lower, upper := prefixBounds(indexGenerationPrefix(generation))
	if err := db.DeleteRange(lower, upper, cockroachpebble.Sync); err != nil {
		return fmt.Errorf("clear knowledge index generation %d: %w", generation, err)
	}
	return nil
}
