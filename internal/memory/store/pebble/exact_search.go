package pebble

import (
	"context"
	"errors"
	"fmt"

	cockroachpebble "github.com/cockroachdb/pebble"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

func (s *Store) SearchExact(ctx context.Context, request memoryStoreAPI.ExactSearchRequest) (memoryStoreAPI.ExactSearchPage, error) {
	request, err := memoryStoreAPI.NormalizeExactSearchRequest(request)
	if err != nil {
		return memoryStoreAPI.ExactSearchPage{}, err
	}
	if err := ctx.Err(); err != nil {
		return memoryStoreAPI.ExactSearchPage{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return memoryStoreAPI.ExactSearchPage{}, memoryStoreAPI.ErrClosed
	}
	snapshot := s.db.NewSnapshot()
	defer func() { _ = snapshot.Close() }()

	records := make(map[string]memoryStoreAPI.CanonicalRecord)
	for _, kind := range exactSearchKinds(request.Kinds) {
		if err := addExactIDCandidate(snapshot, kind, request.Query, records); err != nil {
			return memoryStoreAPI.ExactSearchPage{}, err
		}
	}
	comparisonKey := memory.NormalizeComparisonKey(request.Query)
	tags := memory.NormalizeTags([]string{request.Query})
	for _, lookup := range exactIndexLookups(request.Kinds, comparisonKey, tags) {
		if err := addExactIndexCandidates(ctx, snapshot, s.meta.IndexGeneration, lookup, records); err != nil {
			return memoryStoreAPI.ExactSearchPage{}, err
		}
	}

	candidates := make([]memoryStoreAPI.CanonicalRecord, 0, len(records))
	for _, record := range records {
		candidates = append(candidates, record)
	}
	return memoryStoreAPI.PaginateExactSearch(candidates, request, s.meta.IndexGeneration)
}

type exactIndexLookup struct {
	name  string
	value string
	kind  memoryStoreAPI.RecordKind
}

func exactIndexLookups(kinds []memoryStoreAPI.RecordKind, comparisonKey string, tags []string) []exactIndexLookup {
	lookups := make([]exactIndexLookup, 0, 6)
	if exactKindSelected(kinds, memoryStoreAPI.RecordKindChunk) {
		lookups = append(lookups,
			exactIndexLookup{name: chunkTitleIndex, value: comparisonKey, kind: memoryStoreAPI.RecordKindChunk},
			exactIndexLookup{name: chunkAliasIndex, value: comparisonKey, kind: memoryStoreAPI.RecordKindChunk},
		)
		if len(tags) == 1 {
			lookups = append(lookups, exactIndexLookup{name: chunkTagIndex, value: tags[0], kind: memoryStoreAPI.RecordKindChunk})
		}
	}
	if exactKindSelected(kinds, memoryStoreAPI.RecordKindEntry) {
		lookups = append(lookups,
			exactIndexLookup{name: entryTitleIndex, value: comparisonKey, kind: memoryStoreAPI.RecordKindEntry},
			exactIndexLookup{name: entryAliasIndex, value: comparisonKey, kind: memoryStoreAPI.RecordKindEntry},
		)
		if len(tags) == 1 {
			lookups = append(lookups, exactIndexLookup{name: entryTagIndex, value: tags[0], kind: memoryStoreAPI.RecordKindEntry})
		}
	}
	return lookups
}

func addExactIndexCandidates(ctx context.Context, reader recordReader, generation uint64, lookup exactIndexLookup, records map[string]memoryStoreAPI.CanonicalRecord) error {
	prefix := indexKey(generation, lookup.name, encodeIndexTuple(lookup.value))
	lower, upper := prefixBounds(prefix)
	iter, err := reader.NewIter(&cockroachpebble.IterOptions{LowerBound: lower, UpperBound: upper})
	if err != nil {
		return fmt.Errorf("search exact memory index %s: %w", lookup.name, err)
	}
	defer func() { _ = iter.Close() }()
	for iter.First(); iter.Valid(); iter.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		id := string(iter.Value())
		if err := addExactIDCandidate(reader, lookup.kind, id, records); err != nil {
			return fmt.Errorf("search exact memory index %s: %w", lookup.name, err)
		}
	}
	if err := iter.Error(); err != nil {
		return fmt.Errorf("search exact memory index %s: %w", lookup.name, err)
	}
	return nil
}

func addExactIDCandidate(reader recordReader, kind memoryStoreAPI.RecordKind, id string, records map[string]memoryStoreAPI.CanonicalRecord) error {
	key := string(kind) + "/" + id
	if _, exists := records[key]; exists {
		return nil
	}
	var record memoryStoreAPI.CanonicalRecord
	var err error
	switch kind {
	case memoryStoreAPI.RecordKindChunk:
		var value memory.Chunk
		value, err = readRecord[memory.Chunk](reader, chunkKey(id), "chunk", id)
		record = memoryStoreAPI.CanonicalRecord{Kind: kind, Chunk: &value}
	case memoryStoreAPI.RecordKindEntry:
		var value memory.Entry
		value, err = readRecord[memory.Entry](reader, entryKey(id), "entry", id)
		record = memoryStoreAPI.CanonicalRecord{Kind: kind, Entry: &value}
	case memoryStoreAPI.RecordKindLink:
		var value memory.Link
		value, err = readRecord[memory.Link](reader, linkKey(id), "link", id)
		record = memoryStoreAPI.CanonicalRecord{Kind: kind, Link: &value}
	case memoryStoreAPI.RecordKindEvidence:
		var value memory.Evidence
		value, err = readRecord[memory.Evidence](reader, evidenceKey(id), "evidence", id)
		record = memoryStoreAPI.CanonicalRecord{Kind: kind, Evidence: &value}
	default:
		return fmt.Errorf("invalid exact memory record kind %q", kind)
	}
	if errors.Is(err, memoryStoreAPI.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	records[key] = record
	return nil
}

func exactSearchKinds(selected []memoryStoreAPI.RecordKind) []memoryStoreAPI.RecordKind {
	if len(selected) > 0 {
		return selected
	}
	return []memoryStoreAPI.RecordKind{
		memoryStoreAPI.RecordKindChunk,
		memoryStoreAPI.RecordKindEntry,
		memoryStoreAPI.RecordKindLink,
		memoryStoreAPI.RecordKindEvidence,
	}
}

func exactKindSelected(selected []memoryStoreAPI.RecordKind, kind memoryStoreAPI.RecordKind) bool {
	if len(selected) == 0 {
		return true
	}
	for _, candidate := range selected {
		if candidate == kind {
			return true
		}
	}
	return false
}
