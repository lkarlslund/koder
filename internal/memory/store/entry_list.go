package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/memory"
)

type EntrySort string

const (
	EntrySortTitle       EntrySort = "title"
	EntrySortCreatedAt   EntrySort = "created_at"
	EntrySortUpdatedAt   EntrySort = "updated_at"
	EntrySortLastUsedAt  EntrySort = "last_used_at"
	EntrySortReviewAfter EntrySort = "review_after"
)

type EntryFilter struct {
	ChunkIDs    []memory.ChunkID
	Kinds       []memory.EntryKind
	States      []memory.EntryState
	Scopes      []memory.Scope
	ScopeKinds  []memory.ScopeKind
	Tags        []string
	Locales     []string
	ValidAt     time.Time
	ReviewDueAt time.Time
	StaleAt     time.Time
}

type EntryListRequest struct {
	Filter     EntryFilter
	Sort       EntrySort
	Descending bool
	Limit      int
	Cursor     string
}

type EntryPage struct {
	Entries    []memory.Entry
	NextCursor string
}

// PaginateEntries applies the backend-neutral list contract to one canonical snapshot.
func PaginateEntries(entries []memory.Entry, request EntryListRequest, generation uint64) (EntryPage, error) {
	request, err := normalizeEntryListRequest(request)
	if err != nil {
		return EntryPage{}, err
	}
	binding, err := entryCursorBinding(request, generation)
	if err != nil {
		return EntryPage{}, err
	}
	order := pageOrder[memory.Entry]{
		sortValue:  func(record memory.Entry) string { return entrySortValue(record, request.Sort) },
		objectID:   func(record memory.Entry) string { return string(record.ID) },
		descending: request.Descending,
	}
	match := func(record memory.Entry) bool { return EntryMatchesFilter(record, request.Filter) }
	records, next, err := paginate(entries, match, order, request.Limit, request.Cursor, binding)
	if err != nil {
		return EntryPage{}, err
	}
	return EntryPage{Entries: records, NextCursor: next}, nil
}

func normalizeEntryListRequest(request EntryListRequest) (EntryListRequest, error) {
	if request.Sort == "" {
		request.Sort = EntrySortUpdatedAt
		request.Descending = true
	}
	switch request.Sort {
	case EntrySortTitle, EntrySortCreatedAt, EntrySortUpdatedAt, EntrySortLastUsedAt, EntrySortReviewAfter:
	default:
		return EntryListRequest{}, fmt.Errorf("%w: invalid entry sort %q", memory.ErrInvalidRecord, request.Sort)
	}
	if request.Limit <= 0 {
		request.Limit = 50
	}
	if request.Limit > 200 {
		return EntryListRequest{}, fmt.Errorf("%w: entry page limit must not exceed 200", memory.ErrInvalidRecord)
	}
	var err error
	request.Filter, err = NormalizeEntryFilter(request.Filter)
	if err != nil {
		return EntryListRequest{}, err
	}
	return request, nil
}

// NormalizeEntryFilter validates and canonicalizes the backend-neutral entry
// predicate used by both paged lists and optional streaming scans.
func NormalizeEntryFilter(filter EntryFilter) (EntryFilter, error) {
	filter.ChunkIDs = slices.Clone(filter.ChunkIDs)
	slices.Sort(filter.ChunkIDs)
	filter.ChunkIDs = slices.Compact(filter.ChunkIDs)
	filter.Kinds = slices.Clone(filter.Kinds)
	slices.Sort(filter.Kinds)
	filter.Kinds = slices.Compact(filter.Kinds)
	filter.States = slices.Clone(filter.States)
	slices.Sort(filter.States)
	filter.States = slices.Compact(filter.States)
	filter.Scopes = slices.Clone(filter.Scopes)
	for index := range filter.Scopes {
		filter.Scopes[index].Selector = strings.TrimSpace(filter.Scopes[index].Selector)
	}
	slices.SortFunc(filter.Scopes, compareScopes)
	filter.Scopes = slices.Compact(filter.Scopes)
	filter.ScopeKinds = slices.Clone(filter.ScopeKinds)
	slices.Sort(filter.ScopeKinds)
	filter.ScopeKinds = slices.Compact(filter.ScopeKinds)
	filter.Tags = memory.NormalizeTags(filter.Tags)
	locales := make([]string, 0, len(filter.Locales))
	for _, raw := range filter.Locales {
		locale, err := memory.NormalizeLocale(raw)
		if err != nil {
			return EntryFilter{}, err
		}
		if locale != "" {
			locales = append(locales, locale)
		}
	}
	slices.Sort(locales)
	filter.Locales = slices.Compact(locales)
	filter.ValidAt = normalizeFilterTime(filter.ValidAt)
	filter.ReviewDueAt = normalizeFilterTime(filter.ReviewDueAt)
	filter.StaleAt = normalizeFilterTime(filter.StaleAt)
	for _, kind := range filter.Kinds {
		if kind == memory.EntryKindUnspecified || !kind.IsAEntryKind() {
			return EntryFilter{}, fmt.Errorf("%w: invalid entry kind filter", memory.ErrInvalidRecord)
		}
	}
	for _, state := range filter.States {
		if state == memory.EntryStateUnspecified || !state.IsAEntryState() {
			return EntryFilter{}, fmt.Errorf("%w: invalid entry state filter", memory.ErrInvalidRecord)
		}
	}
	for _, scope := range filter.Scopes {
		if err := scope.Validate(); err != nil {
			return EntryFilter{}, err
		}
	}
	for _, scope := range filter.ScopeKinds {
		if scope == memory.ScopeKindUnspecified || !scope.IsAScopeKind() {
			return EntryFilter{}, fmt.Errorf("%w: invalid entry scope filter", memory.ErrInvalidRecord)
		}
	}
	return filter, nil
}

func entryCursorBinding(request EntryListRequest, generation uint64) (CursorBinding, error) {
	encoded, err := json.Marshal(request.Filter)
	if err != nil {
		return CursorBinding{}, err
	}
	digest := sha256.Sum256(encoded)
	return CursorBinding{
		Index: "entries-by-" + string(request.Sort), IndexGeneration: generation,
		QueryFingerprint: hex.EncodeToString(digest[:]), SortField: string(request.Sort), Descending: request.Descending,
	}, nil
}

// EntryMatchesFilter reports whether a canonical entry satisfies a normalized
// filter. Callers accepting untrusted filters must use NormalizeEntryFilter first.
func EntryMatchesFilter(entry memory.Entry, filter EntryFilter) bool {
	if !containsOrEmpty(filter.ChunkIDs, entry.ChunkID) || !containsOrEmpty(filter.Kinds, entry.Kind) ||
		!containsOrEmpty(filter.States, entry.State) || !containsOrEmpty(filter.Scopes, entry.Scope) ||
		!containsOrEmpty(filter.ScopeKinds, entry.Scope.Kind) ||
		!containsAll(entry.Tags, filter.Tags) || !intersectsOrEmpty(entry.Applicability.Locales, filter.Locales) {
		return false
	}
	if !filter.ValidAt.IsZero() {
		status, _ := memory.EntryTemporalStatusAt(entry, filter.ValidAt)
		if !status.Valid {
			return false
		}
	}
	if !filter.ReviewDueAt.IsZero() {
		status, _ := memory.EntryTemporalStatusAt(entry, filter.ReviewDueAt)
		if !status.ReviewDue {
			return false
		}
	}
	if !filter.StaleAt.IsZero() {
		status, _ := memory.EntryTemporalStatusAt(entry, filter.StaleAt)
		if !status.Stale {
			return false
		}
	}
	return true
}

func compareScopes(left, right memory.Scope) int {
	if left.Kind < right.Kind {
		return -1
	}
	if left.Kind > right.Kind {
		return 1
	}
	return strings.Compare(left.Selector, right.Selector)
}

func normalizeFilterTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Time{}
	}
	return value.UTC().Round(0)
}

func intersectsOrEmpty(values, required []string) bool {
	if len(required) == 0 {
		return true
	}
	for _, value := range required {
		if slices.Contains(values, value) {
			return true
		}
	}
	return false
}

func entrySortValue(entry memory.Entry, sort EntrySort) string {
	switch sort {
	case EntrySortTitle:
		return entry.Title
	case EntrySortCreatedAt:
		return formatSortTime(entry.CreatedAt)
	case EntrySortLastUsedAt:
		return formatSortTime(entry.LastUsedAt)
	case EntrySortReviewAfter:
		return formatSortTime(entry.ReviewAfter)
	default:
		return formatSortTime(entry.UpdatedAt)
	}
}
