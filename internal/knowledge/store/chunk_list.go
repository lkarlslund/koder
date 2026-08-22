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

type ChunkSort string

const (
	ChunkSortTitle      ChunkSort = "title"
	ChunkSortCreatedAt  ChunkSort = "created_at"
	ChunkSortUpdatedAt  ChunkSort = "updated_at"
	ChunkSortLastUsedAt ChunkSort = "last_used_at"
)

type ChunkFilter struct {
	Kinds       []knowledge.ChunkKind
	States      []knowledge.ChunkState
	Scopes      []knowledge.Scope
	ScopeKinds  []knowledge.ScopeKind
	Tags        []string
	Locale      string
	ReviewDueAt time.Time
	StaleAt     time.Time
}

type ChunkListRequest struct {
	Filter     ChunkFilter
	Sort       ChunkSort
	Descending bool
	Limit      int
	Cursor     string
}

type ChunkPage struct {
	Chunks     []knowledge.Chunk
	NextCursor string
}

// PaginateChunks applies the backend-neutral list contract to one consistent canonical
// snapshot. Backends may replace the scan with equivalent derived indexes.
func PaginateChunks(chunks []knowledge.Chunk, request ChunkListRequest, generation uint64) (ChunkPage, error) {
	request, err := normalizeChunkListRequest(request)
	if err != nil {
		return ChunkPage{}, err
	}
	binding, err := chunkCursorBinding(request, generation)
	if err != nil {
		return ChunkPage{}, err
	}
	filtered := make([]knowledge.Chunk, 0, len(chunks))
	for _, chunk := range chunks {
		if chunkMatchesFilter(chunk, request.Filter) {
			filtered = append(filtered, chunk)
		}
	}
	slices.SortFunc(filtered, func(left, right knowledge.Chunk) int {
		order := strings.Compare(chunkSortValue(left, request.Sort), chunkSortValue(right, request.Sort))
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
			return ChunkPage{}, err
		}
		start = len(filtered)
		for index, chunk := range filtered {
			if chunkAfterPosition(chunk, request, position) {
				start = index
				break
			}
		}
	}
	end := min(start+request.Limit, len(filtered))
	page := ChunkPage{Chunks: slices.Clone(filtered[start:end])}
	if end < len(filtered) && end > start {
		last := filtered[end-1]
		page.NextCursor, err = EncodeCursor(binding, CursorPosition{SortValue: chunkSortValue(last, request.Sort), ObjectID: string(last.ID)})
		if err != nil {
			return ChunkPage{}, err
		}
	}
	return page, nil
}

func normalizeChunkListRequest(request ChunkListRequest) (ChunkListRequest, error) {
	if request.Sort == "" {
		request.Sort = ChunkSortUpdatedAt
		request.Descending = true
	}
	switch request.Sort {
	case ChunkSortTitle, ChunkSortCreatedAt, ChunkSortUpdatedAt, ChunkSortLastUsedAt:
	default:
		return ChunkListRequest{}, fmt.Errorf("%w: invalid chunk sort %q", knowledge.ErrInvalidRecord, request.Sort)
	}
	if request.Limit <= 0 {
		request.Limit = 50
	}
	if request.Limit > 200 {
		return ChunkListRequest{}, fmt.Errorf("%w: chunk page limit must not exceed 200", knowledge.ErrInvalidRecord)
	}
	request.Filter.Tags = knowledge.NormalizeTags(request.Filter.Tags)
	locale, err := knowledge.NormalizeLocale(request.Filter.Locale)
	if err != nil {
		return ChunkListRequest{}, err
	}
	request.Filter.Locale = locale
	request.Filter.ReviewDueAt = normalizeFilterTime(request.Filter.ReviewDueAt)
	request.Filter.StaleAt = normalizeFilterTime(request.Filter.StaleAt)
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
	for _, kind := range request.Filter.Kinds {
		if kind == knowledge.ChunkKindUnspecified || !kind.IsAChunkKind() {
			return ChunkListRequest{}, fmt.Errorf("%w: invalid chunk kind filter", knowledge.ErrInvalidRecord)
		}
	}
	for _, state := range request.Filter.States {
		if state == knowledge.ChunkStateUnspecified || !state.IsAChunkState() {
			return ChunkListRequest{}, fmt.Errorf("%w: invalid chunk state filter", knowledge.ErrInvalidRecord)
		}
	}
	for _, scope := range request.Filter.Scopes {
		if err := scope.Validate(); err != nil {
			return ChunkListRequest{}, err
		}
	}
	for _, scope := range request.Filter.ScopeKinds {
		if scope == knowledge.ScopeKindUnspecified || !scope.IsAScopeKind() {
			return ChunkListRequest{}, fmt.Errorf("%w: invalid chunk scope filter", knowledge.ErrInvalidRecord)
		}
	}
	return request, nil
}

func chunkCursorBinding(request ChunkListRequest, generation uint64) (CursorBinding, error) {
	encoded, err := json.Marshal(request.Filter)
	if err != nil {
		return CursorBinding{}, err
	}
	digest := sha256.Sum256(encoded)
	return CursorBinding{
		Index: "chunks-by-" + string(request.Sort), IndexGeneration: generation,
		QueryFingerprint: hex.EncodeToString(digest[:]), SortField: string(request.Sort), Descending: request.Descending,
	}, nil
}

func chunkMatchesFilter(chunk knowledge.Chunk, filter ChunkFilter) bool {
	if !containsOrEmpty(filter.Kinds, chunk.Kind) || !containsOrEmpty(filter.States, chunk.State) ||
		!containsOrEmpty(filter.Scopes, chunk.Scope) || !containsOrEmpty(filter.ScopeKinds, chunk.Scope.Kind) ||
		(filter.Locale != "" && chunk.Locale != filter.Locale) ||
		!containsAll(chunk.Tags, filter.Tags) {
		return false
	}
	if !filter.ReviewDueAt.IsZero() {
		status, _ := knowledge.ChunkTemporalStatusAt(chunk, filter.ReviewDueAt)
		if !status.ReviewDue {
			return false
		}
	}
	if !filter.StaleAt.IsZero() {
		status, _ := knowledge.ChunkTemporalStatusAt(chunk, filter.StaleAt)
		if !status.Stale {
			return false
		}
	}
	return true
}

func containsOrEmpty[T comparable](values []T, value T) bool {
	return len(values) == 0 || slices.Contains(values, value)
}

func containsAll(values, required []string) bool {
	for _, value := range required {
		if !slices.Contains(values, value) {
			return false
		}
	}
	return true
}

func chunkSortValue(chunk knowledge.Chunk, sort ChunkSort) string {
	switch sort {
	case ChunkSortTitle:
		return chunk.Title
	case ChunkSortCreatedAt:
		return formatSortTime(chunk.CreatedAt)
	case ChunkSortLastUsedAt:
		return formatSortTime(chunk.LastUsedAt)
	default:
		return formatSortTime(chunk.UpdatedAt)
	}
}

func formatSortTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func chunkAfterPosition(chunk knowledge.Chunk, request ChunkListRequest, position CursorPosition) bool {
	order := strings.Compare(chunkSortValue(chunk, request.Sort), position.SortValue)
	if order == 0 {
		order = strings.Compare(string(chunk.ID), position.ObjectID)
	}
	if request.Descending {
		order = -order
	}
	return order > 0
}
