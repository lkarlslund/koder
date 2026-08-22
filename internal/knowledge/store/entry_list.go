package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
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
	ChunkIDs    []knowledge.ChunkID
	Kinds       []knowledge.EntryKind
	States      []knowledge.EntryState
	Scopes      []knowledge.Scope
	ScopeKinds  []knowledge.ScopeKind
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
	Entries    []knowledge.Entry
	NextCursor string
}

// PaginateEntries applies the backend-neutral list contract to one canonical snapshot.
func PaginateEntries(entries []knowledge.Entry, request EntryListRequest, generation uint64) (EntryPage, error) {
	request, err := normalizeEntryListRequest(request)
	if err != nil {
		return EntryPage{}, err
	}
	binding, err := entryCursorBinding(request, generation)
	if err != nil {
		return EntryPage{}, err
	}
	filtered := make([]knowledge.Entry, 0, len(entries))
	for _, entry := range entries {
		if entryMatchesFilter(entry, request.Filter) {
			filtered = append(filtered, entry)
		}
	}
	slices.SortFunc(filtered, func(left, right knowledge.Entry) int {
		order := strings.Compare(entrySortValue(left, request.Sort), entrySortValue(right, request.Sort))
		if order == 0 {
			order = strings.Compare(string(left.ID), string(right.ID))
		}
		if request.Descending {
			return -order
		}
		return order
	})
	start := 0
	if request.Cursor != "" {
		position, err := DecodeCursor(request.Cursor, binding)
		if err != nil {
			return EntryPage{}, err
		}
		start = len(filtered)
		for index, entry := range filtered {
			if entryAfterPosition(entry, request, position) {
				start = index
				break
			}
		}
	}
	end := min(start+request.Limit, len(filtered))
	page := EntryPage{Entries: slices.Clone(filtered[start:end])}
	if end < len(filtered) && end > start {
		last := filtered[end-1]
		page.NextCursor, err = EncodeCursor(binding, CursorPosition{SortValue: entrySortValue(last, request.Sort), ObjectID: string(last.ID)})
		if err != nil {
			return EntryPage{}, err
		}
	}
	return page, nil
}

func normalizeEntryListRequest(request EntryListRequest) (EntryListRequest, error) {
	if request.Sort == "" {
		request.Sort = EntrySortUpdatedAt
		request.Descending = true
	}
	switch request.Sort {
	case EntrySortTitle, EntrySortCreatedAt, EntrySortUpdatedAt, EntrySortLastUsedAt, EntrySortReviewAfter:
	default:
		return EntryListRequest{}, fmt.Errorf("invalid entry sort %q", request.Sort)
	}
	if request.Limit <= 0 {
		request.Limit = 50
	}
	if request.Limit > 200 {
		return EntryListRequest{}, fmt.Errorf("entry page limit must not exceed 200")
	}
	request.Filter.ChunkIDs = slices.Clone(request.Filter.ChunkIDs)
	slices.Sort(request.Filter.ChunkIDs)
	request.Filter.ChunkIDs = slices.Compact(request.Filter.ChunkIDs)
	request.Filter.Kinds = slices.Clone(request.Filter.Kinds)
	slices.Sort(request.Filter.Kinds)
	request.Filter.Kinds = slices.Compact(request.Filter.Kinds)
	request.Filter.States = slices.Clone(request.Filter.States)
	slices.Sort(request.Filter.States)
	request.Filter.States = slices.Compact(request.Filter.States)
	request.Filter.Scopes = slices.Clone(request.Filter.Scopes)
	for index := range request.Filter.Scopes {
		request.Filter.Scopes[index].Selector = strings.TrimSpace(request.Filter.Scopes[index].Selector)
	}
	slices.SortFunc(request.Filter.Scopes, compareScopes)
	request.Filter.Scopes = slices.Compact(request.Filter.Scopes)
	request.Filter.ScopeKinds = slices.Clone(request.Filter.ScopeKinds)
	slices.Sort(request.Filter.ScopeKinds)
	request.Filter.ScopeKinds = slices.Compact(request.Filter.ScopeKinds)
	request.Filter.Tags = knowledge.NormalizeTags(request.Filter.Tags)
	locales := make([]string, 0, len(request.Filter.Locales))
	for _, raw := range request.Filter.Locales {
		locale, err := knowledge.NormalizeLocale(raw)
		if err != nil {
			return EntryListRequest{}, err
		}
		if locale != "" {
			locales = append(locales, locale)
		}
	}
	slices.Sort(locales)
	request.Filter.Locales = slices.Compact(locales)
	request.Filter.ValidAt = normalizeFilterTime(request.Filter.ValidAt)
	request.Filter.ReviewDueAt = normalizeFilterTime(request.Filter.ReviewDueAt)
	request.Filter.StaleAt = normalizeFilterTime(request.Filter.StaleAt)
	for _, kind := range request.Filter.Kinds {
		if kind == knowledge.EntryKindUnspecified || !kind.IsAEntryKind() {
			return EntryListRequest{}, fmt.Errorf("invalid entry kind filter")
		}
	}
	for _, state := range request.Filter.States {
		if state == knowledge.EntryStateUnspecified || !state.IsAEntryState() {
			return EntryListRequest{}, fmt.Errorf("invalid entry state filter")
		}
	}
	for _, scope := range request.Filter.Scopes {
		if err := scope.Validate(); err != nil {
			return EntryListRequest{}, err
		}
	}
	for _, scope := range request.Filter.ScopeKinds {
		if scope == knowledge.ScopeKindUnspecified || !scope.IsAScopeKind() {
			return EntryListRequest{}, fmt.Errorf("invalid entry scope filter")
		}
	}
	return request, nil
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

func entryMatchesFilter(entry knowledge.Entry, filter EntryFilter) bool {
	if !containsOrEmpty(filter.ChunkIDs, entry.ChunkID) || !containsOrEmpty(filter.Kinds, entry.Kind) ||
		!containsOrEmpty(filter.States, entry.State) || !containsOrEmpty(filter.Scopes, entry.Scope) ||
		!containsOrEmpty(filter.ScopeKinds, entry.Scope.Kind) ||
		!containsAll(entry.Tags, filter.Tags) || !intersectsOrEmpty(entry.Applicability.Locales, filter.Locales) {
		return false
	}
	if !filter.ValidAt.IsZero() {
		status, _ := knowledge.EntryTemporalStatusAt(entry, filter.ValidAt)
		if !status.Valid {
			return false
		}
	}
	if !filter.ReviewDueAt.IsZero() {
		status, _ := knowledge.EntryTemporalStatusAt(entry, filter.ReviewDueAt)
		if !status.ReviewDue {
			return false
		}
	}
	if !filter.StaleAt.IsZero() {
		status, _ := knowledge.EntryTemporalStatusAt(entry, filter.StaleAt)
		if !status.Stale {
			return false
		}
	}
	return true
}

func compareScopes(left, right knowledge.Scope) int {
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

func entrySortValue(entry knowledge.Entry, sort EntrySort) string {
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

func entryAfterPosition(entry knowledge.Entry, request EntryListRequest, position CursorPosition) bool {
	order := strings.Compare(entrySortValue(entry, request.Sort), position.SortValue)
	if order == 0 {
		order = strings.Compare(string(entry.ID), position.ObjectID)
	}
	if request.Descending {
		order = -order
	}
	return order > 0
}
