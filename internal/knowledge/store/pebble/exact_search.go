package pebble

import (
	"context"
	"errors"
	"fmt"

	cockroachpebble "github.com/cockroachdb/pebble"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

func (s *Store) SearchExact(ctx context.Context, request knowledgeStore.ExactSearchRequest) (knowledgeStore.ExactSearchPage, error) {
	request, err := knowledgeStore.NormalizeExactSearchRequest(request)
	if err != nil {
		return knowledgeStore.ExactSearchPage{}, err
	}
	if err := ctx.Err(); err != nil {
		return knowledgeStore.ExactSearchPage{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return knowledgeStore.ExactSearchPage{}, knowledgeStore.ErrClosed
	}
	snapshot := s.db.NewSnapshot()
	defer func() { _ = snapshot.Close() }()

	records := make(map[string]knowledgeStore.CanonicalRecord)
	for _, kind := range exactSearchKinds(request.Kinds) {
		if err := addExactIDCandidate(snapshot, kind, request.Query, records); err != nil {
			return knowledgeStore.ExactSearchPage{}, err
		}
	}
	comparisonKey := knowledge.NormalizeComparisonKey(request.Query)
	tags := knowledge.NormalizeTags([]string{request.Query})
	for _, lookup := range exactIndexLookups(request.Kinds, comparisonKey, tags) {
		if err := addExactIndexCandidates(ctx, snapshot, s.meta.IndexGeneration, lookup, records); err != nil {
			return knowledgeStore.ExactSearchPage{}, err
		}
	}

	candidates := make([]knowledgeStore.CanonicalRecord, 0, len(records))
	for _, record := range records {
		candidates = append(candidates, record)
	}
	return knowledgeStore.PaginateExactSearch(candidates, request, s.meta.IndexGeneration)
}

type exactIndexLookup struct {
	name  string
	value string
	kind  knowledgeStore.RecordKind
}

func exactIndexLookups(kinds []knowledgeStore.RecordKind, comparisonKey string, tags []string) []exactIndexLookup {
	lookups := make([]exactIndexLookup, 0, 6)
	if exactKindSelected(kinds, knowledgeStore.RecordKindChunk) {
		lookups = append(lookups,
			exactIndexLookup{name: chunkTitleIndex, value: comparisonKey, kind: knowledgeStore.RecordKindChunk},
			exactIndexLookup{name: chunkAliasIndex, value: comparisonKey, kind: knowledgeStore.RecordKindChunk},
		)
		if len(tags) == 1 {
			lookups = append(lookups, exactIndexLookup{name: chunkTagIndex, value: tags[0], kind: knowledgeStore.RecordKindChunk})
		}
	}
	if exactKindSelected(kinds, knowledgeStore.RecordKindEntry) {
		lookups = append(lookups,
			exactIndexLookup{name: entryTitleIndex, value: comparisonKey, kind: knowledgeStore.RecordKindEntry},
			exactIndexLookup{name: entryAliasIndex, value: comparisonKey, kind: knowledgeStore.RecordKindEntry},
		)
		if len(tags) == 1 {
			lookups = append(lookups, exactIndexLookup{name: entryTagIndex, value: tags[0], kind: knowledgeStore.RecordKindEntry})
		}
	}
	return lookups
}

func addExactIndexCandidates(ctx context.Context, reader recordReader, generation uint64, lookup exactIndexLookup, records map[string]knowledgeStore.CanonicalRecord) error {
	prefix := indexKey(generation, lookup.name, encodeIndexTuple(lookup.value))
	lower, upper := prefixBounds(prefix)
	iter, err := reader.NewIter(&cockroachpebble.IterOptions{LowerBound: lower, UpperBound: upper})
	if err != nil {
		return fmt.Errorf("search exact knowledge index %s: %w", lookup.name, err)
	}
	defer func() { _ = iter.Close() }()
	for iter.First(); iter.Valid(); iter.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		id := string(iter.Value())
		if err := addExactIDCandidate(reader, lookup.kind, id, records); err != nil {
			return fmt.Errorf("search exact knowledge index %s: %w", lookup.name, err)
		}
	}
	if err := iter.Error(); err != nil {
		return fmt.Errorf("search exact knowledge index %s: %w", lookup.name, err)
	}
	return nil
}

func addExactIDCandidate(reader recordReader, kind knowledgeStore.RecordKind, id string, records map[string]knowledgeStore.CanonicalRecord) error {
	key := string(kind) + "/" + id
	if _, exists := records[key]; exists {
		return nil
	}
	var record knowledgeStore.CanonicalRecord
	var err error
	switch kind {
	case knowledgeStore.RecordKindChunk:
		var value knowledge.Chunk
		value, err = readRecord[knowledge.Chunk](reader, chunkKey(id), "chunk", id)
		record = knowledgeStore.CanonicalRecord{Kind: kind, Chunk: &value}
	case knowledgeStore.RecordKindEntry:
		var value knowledge.Entry
		value, err = readRecord[knowledge.Entry](reader, entryKey(id), "entry", id)
		record = knowledgeStore.CanonicalRecord{Kind: kind, Entry: &value}
	case knowledgeStore.RecordKindLink:
		var value knowledge.Link
		value, err = readRecord[knowledge.Link](reader, linkKey(id), "link", id)
		record = knowledgeStore.CanonicalRecord{Kind: kind, Link: &value}
	case knowledgeStore.RecordKindEvidence:
		var value knowledge.Evidence
		value, err = readRecord[knowledge.Evidence](reader, evidenceKey(id), "evidence", id)
		record = knowledgeStore.CanonicalRecord{Kind: kind, Evidence: &value}
	default:
		return fmt.Errorf("invalid exact knowledge record kind %q", kind)
	}
	if errors.Is(err, knowledgeStore.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	records[key] = record
	return nil
}

func exactSearchKinds(selected []knowledgeStore.RecordKind) []knowledgeStore.RecordKind {
	if len(selected) > 0 {
		return selected
	}
	return []knowledgeStore.RecordKind{
		knowledgeStore.RecordKindChunk,
		knowledgeStore.RecordKindEntry,
		knowledgeStore.RecordKindLink,
		knowledgeStore.RecordKindEvidence,
	}
}

func exactKindSelected(selected []knowledgeStore.RecordKind, kind knowledgeStore.RecordKind) bool {
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
