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

const (
	defaultExactSearchLimit = 25
	maxExactSearchLimit     = 100
	maxExactSearchQuery     = 4 << 10
)

// ExactMatchField identifies the canonical field that exactly matched a query.
type ExactMatchField string

const (
	ExactMatchID    ExactMatchField = "id"
	ExactMatchTitle ExactMatchField = "title"
	ExactMatchAlias ExactMatchField = "alias"
	ExactMatchTag   ExactMatchField = "tag"
)

// ExactSearchRequest finds canonical records without tokenization or fuzzy matching.
// An empty Kinds list searches every canonical record family.
type ExactSearchRequest struct {
	Query  string       `json:"query"`
	Kinds  []RecordKind `json:"kinds,omitempty"`
	Limit  int          `json:"limit,omitempty"`
	Cursor string       `json:"cursor,omitempty"`
}

// ExactSearchHit contains one canonical record and every field that matched. Match fields
// are ordered by specificity: ID, title, alias, then tag.
type ExactSearchHit struct {
	Record  CanonicalRecord   `json:"record"`
	Matches []ExactMatchField `json:"matches"`
}

type ExactSearchPage struct {
	Hits       []ExactSearchHit `json:"hits"`
	NextCursor string           `json:"next_cursor,omitempty"`
}

// NormalizeExactSearchRequest validates and canonicalizes an exact-search request. It is
// exported so indexed backends can derive precisely the same lookup keys as pagination.
func NormalizeExactSearchRequest(request ExactSearchRequest) (ExactSearchRequest, error) {
	request.Query = strings.TrimSpace(request.Query)
	if request.Query == "" {
		return ExactSearchRequest{}, fmt.Errorf("exact knowledge search query is required")
	}
	if len(request.Query) > maxExactSearchQuery {
		return ExactSearchRequest{}, fmt.Errorf("exact knowledge search query exceeds %d bytes", maxExactSearchQuery)
	}
	if request.Limit < 0 || request.Limit > maxExactSearchLimit {
		return ExactSearchRequest{}, fmt.Errorf("exact knowledge search limit must be between 0 and %d", maxExactSearchLimit)
	}
	if request.Limit == 0 {
		request.Limit = defaultExactSearchLimit
	}
	kinds := slices.Clone(request.Kinds)
	for _, kind := range kinds {
		if !validRecordKind(kind) {
			return ExactSearchRequest{}, fmt.Errorf("invalid exact knowledge search record kind %q", kind)
		}
	}
	slices.Sort(kinds)
	request.Kinds = slices.Compact(kinds)
	return request, nil
}

// PaginateExactSearch evaluates candidates and applies the backend-neutral deterministic
// exact-search ordering and cursor contract. Backends may provide all canonical records or
// only records selected through equivalent exact indexes.
func PaginateExactSearch(records []CanonicalRecord, request ExactSearchRequest, generation uint64) (ExactSearchPage, error) {
	request, err := NormalizeExactSearchRequest(request)
	if err != nil {
		return ExactSearchPage{}, err
	}
	binding, err := exactSearchCursorBinding(request, generation)
	if err != nil {
		return ExactSearchPage{}, err
	}

	hitsByKey := make(map[string]ExactSearchHit, len(records))
	for _, record := range records {
		if err := record.Validate(); err != nil {
			return ExactSearchPage{}, fmt.Errorf("exact knowledge search candidate: %w", err)
		}
		if !containsRecordKind(request.Kinds, record.Kind) {
			continue
		}
		matches := exactMatches(record, request.Query)
		if len(matches) == 0 {
			continue
		}
		key := exactHitObjectID(record)
		if existing, ok := hitsByKey[key]; ok {
			existing.Matches = mergeExactMatches(existing.Matches, matches)
			hitsByKey[key] = existing
			continue
		}
		hitsByKey[key] = ExactSearchHit{Record: cloneCanonicalRecord(record), Matches: matches}
	}

	hits := make([]ExactSearchHit, 0, len(hitsByKey))
	for _, hit := range hitsByKey {
		hits = append(hits, hit)
	}
	slices.SortFunc(hits, compareExactHits)

	if request.Cursor != "" {
		position, err := DecodeCursor(request.Cursor, binding)
		if err != nil {
			return ExactSearchPage{}, err
		}
		first := len(hits)
		for index, hit := range hits {
			if exactHitAfterPosition(hit, position) {
				first = index
				break
			}
		}
		hits = hits[first:]
	}

	page := ExactSearchPage{}
	if len(hits) <= request.Limit {
		page.Hits = hits
		return page, nil
	}
	page.Hits = hits[:request.Limit]
	last := page.Hits[len(page.Hits)-1]
	page.NextCursor, err = EncodeCursor(binding, CursorPosition{
		SortValue: exactHitSortValue(last),
		ObjectID:  exactHitObjectID(last.Record),
	})
	return page, err
}

func exactMatches(record CanonicalRecord, query string) []ExactMatchField {
	matches := make([]ExactMatchField, 0, 4)
	if record.ID() == query {
		matches = append(matches, ExactMatchID)
	}
	comparisonKey := knowledge.NormalizeComparisonKey(query)
	tags := knowledge.NormalizeTags([]string{query})
	tag := ""
	if len(tags) == 1 {
		tag = tags[0]
	}
	var title string
	var aliases, recordTags []string
	switch record.Kind {
	case RecordKindChunk:
		title, aliases, recordTags = record.Chunk.Title, record.Chunk.Aliases, record.Chunk.Tags
	case RecordKindEntry:
		title, aliases, recordTags = record.Entry.Title, record.Entry.Aliases, record.Entry.Tags
	}
	if title != "" && knowledge.NormalizeComparisonKey(title) == comparisonKey {
		matches = append(matches, ExactMatchTitle)
	}
	if slices.ContainsFunc(aliases, func(alias string) bool {
		return knowledge.NormalizeComparisonKey(alias) == comparisonKey
	}) {
		matches = append(matches, ExactMatchAlias)
	}
	if tag != "" && slices.Contains(recordTags, tag) {
		matches = append(matches, ExactMatchTag)
	}
	return matches
}

func exactSearchCursorBinding(request ExactSearchRequest, generation uint64) (CursorBinding, error) {
	query := struct {
		Query string       `json:"query"`
		Kinds []RecordKind `json:"kinds,omitempty"`
	}{Query: request.Query, Kinds: request.Kinds}
	encoded, err := json.Marshal(query)
	if err != nil {
		return CursorBinding{}, err
	}
	digest := sha256.Sum256(encoded)
	return CursorBinding{
		Index:            "knowledge-exact",
		IndexGeneration:  generation,
		QueryFingerprint: hex.EncodeToString(digest[:]),
		SortField:        "match_kind_record_kind_id",
	}, nil
}

func compareExactHits(left, right ExactSearchHit) int {
	if comparison := strings.Compare(exactHitSortValue(left), exactHitSortValue(right)); comparison != 0 {
		return comparison
	}
	return strings.Compare(exactHitObjectID(left.Record), exactHitObjectID(right.Record))
}

func exactHitAfterPosition(hit ExactSearchHit, position CursorPosition) bool {
	sortValue := exactHitSortValue(hit)
	return sortValue > position.SortValue ||
		(sortValue == position.SortValue && exactHitObjectID(hit.Record) > position.ObjectID)
}

func exactHitSortValue(hit ExactSearchHit) string {
	return fmt.Sprintf("%d/%s", exactMatchRank(hit.Matches[0]), hit.Record.Kind)
}

func exactHitObjectID(record CanonicalRecord) string {
	return string(record.Kind) + "/" + record.ID()
}

func exactMatchRank(field ExactMatchField) int {
	switch field {
	case ExactMatchID:
		return 0
	case ExactMatchTitle:
		return 1
	case ExactMatchAlias:
		return 2
	case ExactMatchTag:
		return 3
	default:
		return 9
	}
}

func mergeExactMatches(left, right []ExactMatchField) []ExactMatchField {
	merged := append(slices.Clone(left), right...)
	slices.SortFunc(merged, func(a, b ExactMatchField) int { return exactMatchRank(a) - exactMatchRank(b) })
	return slices.Compact(merged)
}

func containsRecordKind(kinds []RecordKind, kind RecordKind) bool {
	return len(kinds) == 0 || slices.Contains(kinds, kind)
}

func validRecordKind(kind RecordKind) bool {
	switch kind {
	case RecordKindChunk, RecordKindEntry, RecordKindLink, RecordKindEvidence:
		return true
	default:
		return false
	}
}
