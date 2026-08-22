package pebble

import (
	"context"
	"fmt"

	cockroachpebble "github.com/cockroachdb/pebble"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

func (s *Store) ListRevisions(ctx context.Context, request knowledgeStore.RevisionListRequest) (knowledgeStore.RevisionPage, error) {
	if err := ctx.Err(); err != nil {
		return knowledgeStore.RevisionPage{}, err
	}
	if err := request.Object.Validate(); err != nil {
		return knowledgeStore.RevisionPage{}, err
	}
	kind, recordKind, err := revisionRecordKinds(request.Object.Kind)
	if err != nil {
		return knowledgeStore.RevisionPage{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return knowledgeStore.RevisionPage{}, knowledgeStore.ErrClosed
	}
	snapshot := s.db.NewSnapshot()
	defer func() { _ = snapshot.Close() }()
	lower, upper := prefixBounds(revisionPrefix(kind, request.Object.ID))
	iter, err := snapshot.NewIter(&cockroachpebble.IterOptions{LowerBound: lower, UpperBound: upper})
	if err != nil {
		return knowledgeStore.RevisionPage{}, fmt.Errorf("list knowledge revisions: %w", err)
	}
	defer func() { _ = iter.Close() }()
	records := make([]knowledgeStore.CanonicalRecord, 0)
	for iter.First(); iter.Valid(); iter.Next() {
		if err := ctx.Err(); err != nil {
			return knowledgeStore.RevisionPage{}, err
		}
		revisionNumber, err := decodeRevisionKey(iter.Key(), kind, request.Object.ID)
		if err != nil {
			return knowledgeStore.RevisionPage{}, err
		}
		record, err := decodeHistoricalRecord(iter.Value(), recordKind, request.Object.ID)
		if err != nil {
			return knowledgeStore.RevisionPage{}, err
		}
		if record.RevisionMetadata().Number != revisionNumber || record.ObjectRef() != request.Object {
			return knowledgeStore.RevisionPage{}, fmt.Errorf("knowledge revision key does not match stored record")
		}
		records = append(records, record)
	}
	if err := iter.Error(); err != nil {
		return knowledgeStore.RevisionPage{}, fmt.Errorf("list knowledge revisions: %w", err)
	}
	if len(records) == 0 {
		return knowledgeStore.RevisionPage{}, fmt.Errorf("%w: %s %s", knowledgeStore.ErrNotFound, request.Object.Kind, request.Object.ID)
	}
	return knowledgeStore.PaginateRevisions(records, request)
}

func revisionRecordKinds(kind knowledge.ObjectKind) (byte, knowledgeStore.RecordKind, error) {
	switch kind {
	case knowledge.ObjectKindChunk:
		return recordChunk, knowledgeStore.RecordKindChunk, nil
	case knowledge.ObjectKindEntry:
		return recordEntry, knowledgeStore.RecordKindEntry, nil
	case knowledge.ObjectKindLink:
		return recordLink, knowledgeStore.RecordKindLink, nil
	default:
		return 0, "", fmt.Errorf("%w: invalid revision object kind", knowledge.ErrInvalidRecord)
	}
}

func decodeHistoricalRecord(data []byte, kind knowledgeStore.RecordKind, id string) (knowledgeStore.CanonicalRecord, error) {
	switch kind {
	case knowledgeStore.RecordKindChunk:
		value, err := decodeRecord[knowledge.Chunk](data, "chunk revision", id)
		if err != nil {
			return knowledgeStore.CanonicalRecord{}, err
		}
		return knowledgeStore.CanonicalRecord{Kind: kind, Chunk: &value}, nil
	case knowledgeStore.RecordKindEntry:
		value, err := decodeRecord[knowledge.Entry](data, "entry revision", id)
		if err != nil {
			return knowledgeStore.CanonicalRecord{}, err
		}
		return knowledgeStore.CanonicalRecord{Kind: kind, Entry: &value}, nil
	case knowledgeStore.RecordKindLink:
		value, err := decodeRecord[knowledge.Link](data, "link revision", id)
		if err != nil {
			return knowledgeStore.CanonicalRecord{}, err
		}
		return knowledgeStore.CanonicalRecord{Kind: kind, Link: &value}, nil
	default:
		return knowledgeStore.CanonicalRecord{}, fmt.Errorf("invalid revision record kind %q", kind)
	}
}
