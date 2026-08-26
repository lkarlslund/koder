package pebble

import (
	"context"
	"fmt"

	cockroachpebble "github.com/cockroachdb/pebble"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

func (s *Store) ListRevisions(ctx context.Context, request memoryStoreAPI.RevisionListRequest) (memoryStoreAPI.RevisionPage, error) {
	if err := ctx.Err(); err != nil {
		return memoryStoreAPI.RevisionPage{}, err
	}
	if err := request.Object.Validate(); err != nil {
		return memoryStoreAPI.RevisionPage{}, err
	}
	kind, recordKind, err := revisionRecordKinds(request.Object.Kind)
	if err != nil {
		return memoryStoreAPI.RevisionPage{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return memoryStoreAPI.RevisionPage{}, memoryStoreAPI.ErrClosed
	}
	snapshot := s.db.NewSnapshot()
	defer func() { _ = snapshot.Close() }()
	lower, upper := prefixBounds(revisionPrefix(kind, request.Object.ID))
	iter, err := snapshot.NewIter(&cockroachpebble.IterOptions{LowerBound: lower, UpperBound: upper})
	if err != nil {
		return memoryStoreAPI.RevisionPage{}, fmt.Errorf("list memory revisions: %w", err)
	}
	defer func() { _ = iter.Close() }()
	records := make([]memoryStoreAPI.CanonicalRecord, 0)
	for iter.First(); iter.Valid(); iter.Next() {
		if err := ctx.Err(); err != nil {
			return memoryStoreAPI.RevisionPage{}, err
		}
		revisionNumber, err := decodeRevisionKey(iter.Key(), kind, request.Object.ID)
		if err != nil {
			return memoryStoreAPI.RevisionPage{}, err
		}
		record, err := decodeHistoricalRecord(iter.Value(), recordKind, request.Object.ID)
		if err != nil {
			return memoryStoreAPI.RevisionPage{}, err
		}
		if record.RevisionMetadata().Number != revisionNumber || record.ObjectRef() != request.Object {
			return memoryStoreAPI.RevisionPage{}, fmt.Errorf("memory revision key does not match stored record")
		}
		records = append(records, record)
	}
	if err := iter.Error(); err != nil {
		return memoryStoreAPI.RevisionPage{}, fmt.Errorf("list memory revisions: %w", err)
	}
	if len(records) == 0 {
		return memoryStoreAPI.RevisionPage{}, fmt.Errorf("%w: %s %s", memoryStoreAPI.ErrNotFound, request.Object.Kind, request.Object.ID)
	}
	return memoryStoreAPI.PaginateRevisions(records, request)
}

func revisionRecordKinds(kind memory.ObjectKind) (byte, memoryStoreAPI.RecordKind, error) {
	switch kind {
	case memory.ObjectKindChunk:
		return recordChunk, memoryStoreAPI.RecordKindChunk, nil
	case memory.ObjectKindEntry:
		return recordEntry, memoryStoreAPI.RecordKindEntry, nil
	case memory.ObjectKindLink:
		return recordLink, memoryStoreAPI.RecordKindLink, nil
	default:
		return 0, "", fmt.Errorf("%w: invalid revision object kind", memory.ErrInvalidRecord)
	}
}

func decodeHistoricalRecord(data []byte, kind memoryStoreAPI.RecordKind, id string) (memoryStoreAPI.CanonicalRecord, error) {
	switch kind {
	case memoryStoreAPI.RecordKindChunk:
		value, err := decodeRecord[memory.Chunk](data, "chunk revision", id)
		if err != nil {
			return memoryStoreAPI.CanonicalRecord{}, err
		}
		return memoryStoreAPI.CanonicalRecord{Kind: kind, Chunk: &value}, nil
	case memoryStoreAPI.RecordKindEntry:
		value, err := decodeRecord[memory.Entry](data, "entry revision", id)
		if err != nil {
			return memoryStoreAPI.CanonicalRecord{}, err
		}
		return memoryStoreAPI.CanonicalRecord{Kind: kind, Entry: &value}, nil
	case memoryStoreAPI.RecordKindLink:
		value, err := decodeRecord[memory.Link](data, "link revision", id)
		if err != nil {
			return memoryStoreAPI.CanonicalRecord{}, err
		}
		return memoryStoreAPI.CanonicalRecord{Kind: kind, Link: &value}, nil
	default:
		return memoryStoreAPI.CanonicalRecord{}, fmt.Errorf("invalid revision record kind %q", kind)
	}
}
