package service

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

const (
	defaultGraphExpansionRoots       = 5
	maxGraphExpansionRoots           = 25
	defaultGraphExpansionConnections = 25
	maxGraphExpansionConnections     = 200
	defaultGraphExpansionEntries     = 20
	maxGraphExpansionEntries         = 100
	graphExpansionPageLimit          = 100
	graphExpansionScanMultiplier     = 10
)

type GraphExpansionOptions struct {
	Kinds          []knowledge.LinkKind `json:"kinds,omitempty"`
	MaxRoots       int                  `json:"max_roots,omitempty"`
	MaxConnections int                  `json:"max_connections,omitempty"`
	MaxEntries     int                  `json:"max_entries,omitempty"`
}

type GraphConnection struct {
	FromEntryID knowledge.EntryID            `json:"from_entry_id"`
	LinkID      knowledge.LinkID             `json:"link_id"`
	Kind        knowledge.LinkKind           `json:"kind"`
	Direction   knowledgeStore.LinkDirection `json:"direction"`
}

type GraphExpansionStats struct {
	RootsExpanded int      `json:"roots_expanded"`
	Connections   int      `json:"connections"`
	EntriesAdded  int      `json:"entries_added"`
	Truncated     bool     `json:"truncated"`
	Reasons       []string `json:"reasons,omitempty"`
}

func normalizeGraphExpansionOptions(options GraphExpansionOptions) (GraphExpansionOptions, error) {
	options.Kinds = slices.Clone(options.Kinds)
	if len(options.Kinds) == 0 {
		options.Kinds = []knowledge.LinkKind{
			knowledge.LinkKindRelatedTo,
			knowledge.LinkKindRequires,
			knowledge.LinkKindAlternativeTo,
			knowledge.LinkKindAppliesTo,
			knowledge.LinkKindContradicts,
			knowledge.LinkKindCausedBy,
			knowledge.LinkKindSupportedBy,
			knowledge.LinkKindDerivedFrom,
		}
	}
	slices.Sort(options.Kinds)
	options.Kinds = slices.Compact(options.Kinds)
	for _, kind := range options.Kinds {
		if kind == knowledge.LinkKindUnspecified || !kind.IsALinkKind() {
			return GraphExpansionOptions{}, fmt.Errorf("%w: invalid search graph expansion link kind %q", knowledge.ErrInvalidRecord, kind)
		}
	}
	if options.MaxRoots <= 0 {
		options.MaxRoots = defaultGraphExpansionRoots
	}
	if options.MaxConnections <= 0 {
		options.MaxConnections = defaultGraphExpansionConnections
	}
	if options.MaxEntries <= 0 {
		options.MaxEntries = defaultGraphExpansionEntries
	}
	if options.MaxRoots > maxGraphExpansionRoots || options.MaxConnections > maxGraphExpansionConnections || options.MaxEntries > maxGraphExpansionEntries {
		return GraphExpansionOptions{}, fmt.Errorf(
			"%w: search graph expansion limits exceed roots=%d connections=%d entries=%d", knowledge.ErrInvalidRecord,
			maxGraphExpansionRoots, maxGraphExpansionConnections, maxGraphExpansionEntries,
		)
	}
	return options, nil
}

func (s *Service) expandSearchGraph(ctx context.Context, matches []LexicalSearchMatch, allowed map[knowledge.EntryID]knowledge.Entry, options GraphExpansionOptions) ([]LexicalSearchMatch, *GraphExpansionStats, error) {
	stats := &GraphExpansionStats{}
	if len(matches) == 0 {
		return matches, stats, nil
	}
	reasons := make(map[string]struct{})
	rootCount := min(len(matches), options.MaxRoots)
	if rootCount < len(matches) {
		reasons["root_limit"] = struct{}{}
	}
	stats.RootsExpanded = rootCount
	result := slices.Clone(matches)
	matchIndex := make(map[knowledge.EntryID]int, len(result))
	for index, match := range result {
		match.Terms = slices.Clone(match.Terms)
		match.GraphConnections = slices.Clone(match.GraphConnections)
		result[index] = match
		matchIndex[match.EntryID] = index
	}
	seenLinks := make(map[knowledge.LinkID]struct{})
	maxScanned := options.MaxConnections * graphExpansionScanMultiplier
	scanned := 0
	stop := false

	for rootIndex := 0; rootIndex < rootCount && !stop; rootIndex++ {
		root := result[rootIndex].EntryID
		rootRef := knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(root)}
		cursor := ""
		for !stop {
			page, err := s.store.ListAdjacentLinks(ctx, knowledgeStore.AdjacentLinkListRequest{
				Filter: knowledgeStore.AdjacentLinkFilter{
					Endpoint: rootRef, Direction: knowledgeStore.LinkDirectionBoth, Kinds: options.Kinds,
					States: []knowledge.LinkState{knowledge.LinkStateActive},
				},
				Limit: graphExpansionPageLimit, Cursor: cursor,
			})
			if err != nil {
				return nil, nil, fmt.Errorf("expand lexical search root %s: %w", root, err)
			}
			for _, link := range page.Links {
				scanned++
				if scanned > maxScanned {
					reasons["scan_limit"] = struct{}{}
					stop = true
					break
				}
				if _, exists := seenLinks[link.ID]; exists {
					continue
				}
				other, direction, err := oppositeEndpoint(link, rootRef)
				if err != nil {
					return nil, nil, err
				}
				if other.Kind != knowledge.ObjectKindEntry || knowledge.EntryID(other.ID) == root {
					continue
				}
				otherID := knowledge.EntryID(other.ID)
				if _, ok := allowed[otherID]; !ok {
					continue
				}
				if stats.Connections >= options.MaxConnections {
					reasons["connection_limit"] = struct{}{}
					stop = true
					break
				}
				index, exists := matchIndex[otherID]
				if !exists {
					if stats.EntriesAdded >= options.MaxEntries {
						reasons["entry_limit"] = struct{}{}
						stop = true
						break
					}
					index = len(result)
					matchIndex[otherID] = index
					result = append(result, LexicalSearchMatch{EntryID: otherID})
					stats.EntriesAdded++
				}
				seenLinks[link.ID] = struct{}{}
				stats.Connections++
				result[index].GraphConnections = append(result[index].GraphConnections, GraphConnection{
					FromEntryID: root, LinkID: link.ID, Kind: link.Kind, Direction: direction,
				})
			}
			if stop || page.NextCursor == "" {
				break
			}
			cursor = page.NextCursor
		}
	}
	for index := range result {
		slices.SortFunc(result[index].GraphConnections, func(left, right GraphConnection) int {
			if comparison := strings.Compare(string(left.LinkID), string(right.LinkID)); comparison != 0 {
				return comparison
			}
			return strings.Compare(string(left.FromEntryID), string(right.FromEntryID))
		})
	}
	for _, reason := range []string{"root_limit", "connection_limit", "entry_limit", "scan_limit"} {
		if _, exists := reasons[reason]; exists {
			stats.Reasons = append(stats.Reasons, reason)
		}
	}
	stats.Truncated = len(stats.Reasons) > 0
	return result, stats, nil
}
