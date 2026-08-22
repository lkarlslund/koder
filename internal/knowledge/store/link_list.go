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

type LinkDirection string

const (
	LinkDirectionOutgoing LinkDirection = "outgoing"
	LinkDirectionIncoming LinkDirection = "incoming"
	LinkDirectionBoth     LinkDirection = "both"
)

type AdjacentLinkFilter struct {
	Endpoint  knowledge.ObjectRef
	Direction LinkDirection
	Kinds     []knowledge.LinkKind
	States    []knowledge.LinkState
}

type AdjacentLinkListRequest struct {
	Filter AdjacentLinkFilter
	Limit  int
	Cursor string
}

type LinkPage struct {
	Links      []knowledge.Link
	NextCursor string
}

func PaginateAdjacentLinks(links []knowledge.Link, request AdjacentLinkListRequest, generation uint64) (LinkPage, error) {
	request, err := NormalizeAdjacentLinkListRequest(request)
	if err != nil {
		return LinkPage{}, err
	}
	binding, err := adjacentLinkCursorBinding(request.Filter, generation)
	if err != nil {
		return LinkPage{}, err
	}
	filtered := make([]knowledge.Link, 0, len(links))
	for _, link := range links {
		if linkMatchesAdjacency(link, request.Filter) {
			filtered = append(filtered, cloneLinkForPage(link))
		}
	}
	slices.SortFunc(filtered, func(left, right knowledge.Link) int {
		return strings.Compare(string(left.ID), string(right.ID))
	})
	start := 0
	if request.Cursor != "" {
		position, err := DecodeCursor(request.Cursor, binding)
		if err != nil {
			return LinkPage{}, err
		}
		start = len(filtered)
		for index, link := range filtered {
			if string(link.ID) > position.SortValue {
				start = index
				break
			}
		}
	}
	end := min(start+request.Limit, len(filtered))
	page := LinkPage{Links: slices.Clone(filtered[start:end])}
	if end < len(filtered) && end > start {
		id := string(filtered[end-1].ID)
		page.NextCursor, err = EncodeCursor(binding, CursorPosition{SortValue: id, ObjectID: id})
		if err != nil {
			return LinkPage{}, err
		}
	}
	return page, nil
}

func NormalizeAdjacentLinkListRequest(request AdjacentLinkListRequest) (AdjacentLinkListRequest, error) {
	if err := request.Filter.Endpoint.Validate(); err != nil {
		return AdjacentLinkListRequest{}, err
	}
	if request.Filter.Endpoint.Kind != knowledge.ObjectKindChunk && request.Filter.Endpoint.Kind != knowledge.ObjectKindEntry {
		return AdjacentLinkListRequest{}, fmt.Errorf("%w: adjacency endpoint must identify a chunk or entry", knowledge.ErrInvalidRecord)
	}
	if request.Filter.Direction == "" {
		request.Filter.Direction = LinkDirectionBoth
	}
	switch request.Filter.Direction {
	case LinkDirectionOutgoing, LinkDirectionIncoming, LinkDirectionBoth:
	default:
		return AdjacentLinkListRequest{}, fmt.Errorf("%w: invalid link direction %q", knowledge.ErrInvalidRecord, request.Filter.Direction)
	}
	if request.Limit < 0 {
		return AdjacentLinkListRequest{}, fmt.Errorf("%w: adjacent link page limit cannot be negative", knowledge.ErrInvalidRecord)
	}
	if request.Limit <= 0 {
		request.Limit = 25
	}
	if request.Limit > 100 {
		return AdjacentLinkListRequest{}, fmt.Errorf("%w: adjacent link page limit must not exceed 100", knowledge.ErrInvalidRecord)
	}
	request.Filter.Kinds = slices.Clone(request.Filter.Kinds)
	slices.Sort(request.Filter.Kinds)
	request.Filter.Kinds = slices.Compact(request.Filter.Kinds)
	request.Filter.States = slices.Clone(request.Filter.States)
	slices.Sort(request.Filter.States)
	request.Filter.States = slices.Compact(request.Filter.States)
	for _, kind := range request.Filter.Kinds {
		if kind == knowledge.LinkKindUnspecified || !kind.IsALinkKind() {
			return AdjacentLinkListRequest{}, fmt.Errorf("%w: invalid link kind filter", knowledge.ErrInvalidRecord)
		}
	}
	for _, state := range request.Filter.States {
		if state == knowledge.LinkStateUnspecified || !state.IsALinkState() {
			return AdjacentLinkListRequest{}, fmt.Errorf("%w: invalid link state filter", knowledge.ErrInvalidRecord)
		}
	}
	return request, nil
}

func adjacentLinkCursorBinding(filter AdjacentLinkFilter, generation uint64) (CursorBinding, error) {
	encoded, err := json.Marshal(filter)
	if err != nil {
		return CursorBinding{}, err
	}
	digest := sha256.Sum256(encoded)
	return CursorBinding{
		Index: "link-adjacency", IndexGeneration: generation, QueryFingerprint: hex.EncodeToString(digest[:]),
		SortField: "link_id", Descending: false,
	}, nil
}

func linkMatchesAdjacency(link knowledge.Link, filter AdjacentLinkFilter) bool {
	directionMatches := (filter.Direction == LinkDirectionOutgoing || filter.Direction == LinkDirectionBoth) && link.Source == filter.Endpoint
	directionMatches = directionMatches || ((filter.Direction == LinkDirectionIncoming || filter.Direction == LinkDirectionBoth) && link.Target == filter.Endpoint)
	return directionMatches && containsOrEmpty(filter.Kinds, link.Kind) && containsOrEmpty(filter.States, link.State)
}

func cloneLinkForPage(value knowledge.Link) knowledge.Link {
	value.EvidenceIDs = slices.Clone(value.EvidenceIDs)
	return value
}
