package service

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

type SearchMatchReasonKind string

const (
	SearchMatchReasonLexical SearchMatchReasonKind = "lexical"
	SearchMatchReasonGraph   SearchMatchReasonKind = "graph"
)

type SearchMatchReason struct {
	Kind        SearchMatchReasonKind        `json:"kind"`
	Term        string                       `json:"term,omitempty"`
	Fields      []string                     `json:"fields,omitempty"`
	FromEntryID knowledge.EntryID            `json:"from_entry_id,omitempty"`
	LinkID      knowledge.LinkID             `json:"link_id,omitempty"`
	LinkKind    knowledge.LinkKind           `json:"link_kind,omitempty"`
	Direction   knowledgeStore.LinkDirection `json:"direction,omitempty"`
}

type SearchWarningCode string

const (
	SearchWarningDisputed                SearchWarningCode = "disputed"
	SearchWarningReviewDue               SearchWarningCode = "review_due"
	SearchWarningNotYetValid             SearchWarningCode = "not_yet_valid"
	SearchWarningExpired                 SearchWarningCode = "expired"
	SearchWarningSuperseded              SearchWarningCode = "superseded"
	SearchWarningGraphExpansionTruncated SearchWarningCode = "graph_expansion_truncated"
	SearchWarningContradictionsTruncated SearchWarningCode = "contradictions_truncated"
)

type SearchWarning struct {
	Code    SearchWarningCode `json:"code"`
	EntryID knowledge.EntryID `json:"entry_id,omitempty"`
}

func addSearchMatchReasons(matches []LexicalSearchMatch) []LexicalSearchMatch {
	result := slices.Clone(matches)
	for index, match := range result {
		match.Reasons = make([]SearchMatchReason, 0, len(match.Terms)+len(match.GraphConnections))
		for _, term := range match.Terms {
			fields := make([]string, 0, 5)
			for _, field := range []struct {
				name  string
				count uint32
			}{
				{name: "title", count: term.Frequencies.Title},
				{name: "summary", count: term.Frequencies.Summary},
				{name: "body", count: term.Frequencies.Body},
				{name: "alias", count: term.Frequencies.Aliases},
				{name: "tag", count: term.Frequencies.Tags},
			} {
				if field.count > 0 {
					fields = append(fields, field.name)
				}
			}
			match.Reasons = append(match.Reasons, SearchMatchReason{
				Kind: SearchMatchReasonLexical, Term: term.Term, Fields: fields,
			})
		}
		for _, connection := range match.GraphConnections {
			match.Reasons = append(match.Reasons, SearchMatchReason{
				Kind: SearchMatchReasonGraph, FromEntryID: connection.FromEntryID,
				LinkID: connection.LinkID, LinkKind: connection.Kind, Direction: connection.Direction,
			})
		}
		result[index] = match
	}
	return result
}

func searchWarnings(matches []LexicalSearchMatch, entries map[knowledge.EntryID]knowledge.Entry, asOf time.Time, expansion *GraphExpansionStats) []SearchWarning {
	warnings := make([]SearchWarning, 0)
	if expansion != nil && expansion.Truncated {
		warnings = append(warnings, SearchWarning{Code: SearchWarningGraphExpansionTruncated})
	}
	for _, match := range matches {
		entry := entries[match.EntryID]
		if entry.Verification.Status == knowledge.VerificationStatusDisputed {
			warnings = append(warnings, SearchWarning{Code: SearchWarningDisputed, EntryID: entry.ID})
		}
		if entry.State == knowledge.EntryStateSuperseded {
			warnings = append(warnings, SearchWarning{Code: SearchWarningSuperseded, EntryID: entry.ID})
		}
		status, err := knowledge.EntryTemporalStatusAt(entry, asOf)
		if err != nil {
			continue
		}
		switch {
		case status.NotYetValid:
			warnings = append(warnings, SearchWarning{Code: SearchWarningNotYetValid, EntryID: entry.ID})
		case status.Expired:
			warnings = append(warnings, SearchWarning{Code: SearchWarningExpired, EntryID: entry.ID})
		}
		if status.ReviewDue {
			warnings = append(warnings, SearchWarning{Code: SearchWarningReviewDue, EntryID: entry.ID})
		}
	}
	slices.SortFunc(warnings, func(left, right SearchWarning) int {
		if comparison := strings.Compare(string(left.Code), string(right.Code)); comparison != 0 {
			return comparison
		}
		return strings.Compare(string(left.EntryID), string(right.EntryID))
	})
	return warnings
}

const (
	maxSearchContradictions     = 50
	maxSearchContradictionScans = 500
	searchContradictionPageSize = 100
)

func (s *Service) searchContradictions(ctx context.Context, matches []LexicalSearchMatch, allowed map[knowledge.EntryID]knowledge.Entry) ([]Contradiction, bool, error) {
	result := make([]Contradiction, 0)
	seen := make(map[knowledge.LinkID]struct{})
	scanned := 0
	truncated := false
	for _, match := range matches {
		root := knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(match.EntryID)}
		cursor := ""
		for {
			page, err := s.store.ListAdjacentLinks(ctx, knowledgeStore.AdjacentLinkListRequest{
				Filter: knowledgeStore.AdjacentLinkFilter{
					Endpoint: root, Direction: knowledgeStore.LinkDirectionBoth,
					Kinds:  []knowledge.LinkKind{knowledge.LinkKindContradicts},
					States: []knowledge.LinkState{knowledge.LinkStateActive},
				},
				Limit: searchContradictionPageSize, Cursor: cursor,
			})
			if err != nil {
				return nil, false, fmt.Errorf("collect search contradictions for %s: %w", match.EntryID, err)
			}
			for _, link := range page.Links {
				scanned++
				if scanned > maxSearchContradictionScans {
					truncated = true
					break
				}
				if _, duplicate := seen[link.ID]; duplicate {
					continue
				}
				other, _, err := oppositeEndpoint(link, root)
				if err != nil {
					return nil, false, err
				}
				if other.Kind != knowledge.ObjectKindEntry {
					continue
				}
				if _, authorized := allowed[knowledge.EntryID(other.ID)]; !authorized {
					continue
				}
				if len(result) >= maxSearchContradictions {
					truncated = true
					break
				}
				seen[link.ID] = struct{}{}
				result = append(result, Contradiction{LinkID: link.ID, Left: link.Source, Right: link.Target})
			}
			if truncated || page.NextCursor == "" {
				break
			}
			cursor = page.NextCursor
		}
		if truncated {
			break
		}
	}
	slices.SortFunc(result, func(left, right Contradiction) int {
		return strings.Compare(string(left.LinkID), string(right.LinkID))
	})
	return result, truncated, nil
}
