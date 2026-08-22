package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/lkarlslund/koder/internal/knowledge"
)

const revisionHistoryGeneration = uint64(1)

type RevisionListRequest struct {
	Object knowledge.ObjectRef
	Limit  int
	Cursor string
}

type RevisionPage struct {
	Revisions  []CanonicalRecord
	NextCursor string
}

func (r CanonicalRecord) ObjectRef() knowledge.ObjectRef {
	switch r.Kind {
	case RecordKindChunk:
		if r.Chunk != nil {
			return knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: string(r.Chunk.ID)}
		}
	case RecordKindEntry:
		if r.Entry != nil {
			return knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(r.Entry.ID)}
		}
	case RecordKindLink:
		if r.Link != nil {
			return knowledge.ObjectRef{Kind: knowledge.ObjectKindLink, ID: string(r.Link.ID)}
		}
	}
	return knowledge.ObjectRef{}
}

func (r CanonicalRecord) RevisionMetadata() knowledge.Revision {
	switch r.Kind {
	case RecordKindChunk:
		if r.Chunk != nil {
			return r.Chunk.Revision
		}
	case RecordKindEntry:
		if r.Entry != nil {
			return r.Entry.Revision
		}
	case RecordKindLink:
		if r.Link != nil {
			return r.Link.Revision
		}
	}
	return knowledge.Revision{}
}

func PaginateRevisions(records []CanonicalRecord, request RevisionListRequest) (RevisionPage, error) {
	if err := request.Object.Validate(); err != nil {
		return RevisionPage{}, err
	}
	if request.Limit <= 0 {
		request.Limit = 50
	}
	if request.Limit > 200 {
		return RevisionPage{}, fmt.Errorf("revision page limit must not exceed 200")
	}
	binding, err := revisionCursorBinding(request.Object)
	if err != nil {
		return RevisionPage{}, err
	}
	filtered := make([]CanonicalRecord, 0, len(records))
	for _, record := range records {
		if err := record.Validate(); err != nil {
			return RevisionPage{}, err
		}
		if record.ObjectRef() != request.Object {
			return RevisionPage{}, fmt.Errorf("revision history contains a record for a different object")
		}
		filtered = append(filtered, cloneCanonicalRecord(record))
	}
	slices.SortFunc(filtered, func(left, right CanonicalRecord) int {
		leftRevision, rightRevision := left.RevisionMetadata(), right.RevisionMetadata()
		if leftRevision.Number < rightRevision.Number {
			return 1
		}
		if leftRevision.Number > rightRevision.Number {
			return -1
		}
		return -strings.Compare(string(leftRevision.ID), string(rightRevision.ID))
	})
	start := 0
	if request.Cursor != "" {
		position, err := DecodeCursor(request.Cursor, binding)
		if err != nil {
			return RevisionPage{}, err
		}
		start = len(filtered)
		for index, record := range filtered {
			metadata := record.RevisionMetadata()
			value := formatRevisionNumber(metadata.Number)
			if value < position.SortValue || (value == position.SortValue && string(metadata.ID) < position.ObjectID) {
				start = index
				break
			}
		}
	}
	end := min(start+request.Limit, len(filtered))
	page := RevisionPage{Revisions: slices.Clone(filtered[start:end])}
	if end < len(filtered) && end > start {
		metadata := filtered[end-1].RevisionMetadata()
		page.NextCursor, err = EncodeCursor(binding, CursorPosition{
			SortValue: formatRevisionNumber(metadata.Number), ObjectID: string(metadata.ID),
		})
		if err != nil {
			return RevisionPage{}, err
		}
	}
	return page, nil
}

func revisionCursorBinding(object knowledge.ObjectRef) (CursorBinding, error) {
	encoded, err := json.Marshal(object)
	if err != nil {
		return CursorBinding{}, err
	}
	digest := sha256.Sum256(encoded)
	return CursorBinding{
		Index: "object-revisions", IndexGeneration: revisionHistoryGeneration,
		QueryFingerprint: hex.EncodeToString(digest[:]), SortField: "revision_number", Descending: true,
	}, nil
}

func formatRevisionNumber(value uint64) string {
	return fmt.Sprintf("%020d", value)
}

func cloneCanonicalRecord(record CanonicalRecord) CanonicalRecord {
	cloned := record
	switch record.Kind {
	case RecordKindChunk:
		value := *record.Chunk
		value.Aliases = slices.Clone(value.Aliases)
		value.Tags = slices.Clone(value.Tags)
		value.SharedWith = slices.Clone(value.SharedWith)
		value.Risk = slices.Clone(value.Risk)
		value.DependencyIDs = slices.Clone(value.DependencyIDs)
		cloned.Chunk = &value
	case RecordKindEntry:
		value := *record.Entry
		value.Aliases = slices.Clone(value.Aliases)
		value.Tags = slices.Clone(value.Tags)
		value.Risk = slices.Clone(value.Risk)
		value.EvidenceIDs = slices.Clone(value.EvidenceIDs)
		value.Verification.EvidenceIDs = slices.Clone(value.Verification.EvidenceIDs)
		value.Applicability.OperatingSystems = slices.Clone(value.Applicability.OperatingSystems)
		value.Applicability.Architectures = slices.Clone(value.Applicability.Architectures)
		value.Applicability.Software = slices.Clone(value.Applicability.Software)
		value.Applicability.Locales = slices.Clone(value.Applicability.Locales)
		value.Applicability.Conditions = slices.Clone(value.Applicability.Conditions)
		cloned.Entry = &value
	case RecordKindLink:
		value := *record.Link
		value.EvidenceIDs = slices.Clone(value.EvidenceIDs)
		cloned.Link = &value
	}
	return cloned
}
