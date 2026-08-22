package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

type RankingSignals struct {
	ReuseCount         uint64 `json:"reuse_count"`
	SuccessfulOutcomes uint64 `json:"successful_outcomes"`
	FailedOutcomes     uint64 `json:"failed_outcomes"`
}

type RankingSignalSource interface {
	RankingSignals(context.Context, []knowledge.EntryID) (map[knowledge.EntryID]RankingSignals, error)
}

type RankingSignalSourceFunc func(context.Context, []knowledge.EntryID) (map[knowledge.EntryID]RankingSignals, error)

func (fn RankingSignalSourceFunc) RankingSignals(ctx context.Context, ids []knowledge.EntryID) (map[knowledge.EntryID]RankingSignals, error) {
	return fn(ctx, ids)
}

type NoRankingSignals struct{}

func (NoRankingSignals) RankingSignals(context.Context, []knowledge.EntryID) (map[knowledge.EntryID]RankingSignals, error) {
	return nil, nil
}

type SearchRank struct {
	Total        float64 `json:"total"`
	Lexical      float64 `json:"lexical"`
	Graph        float64 `json:"graph"`
	Scope        float64 `json:"scope"`
	Verification float64 `json:"verification"`
	Freshness    float64 `json:"freshness"`
	Evidence     float64 `json:"evidence"`
	Reuse        float64 `json:"reuse"`
	Outcomes     float64 `json:"outcomes"`
}

const rankingPrecision = 1e9

func (s *Service) rankSearchMatches(ctx context.Context, matches []LexicalSearchMatch, entries map[knowledge.EntryID]knowledge.Entry, scopes []knowledge.Scope, asOf time.Time) ([]LexicalSearchMatch, error) {
	if len(matches) == 0 {
		return matches, nil
	}
	ids := make([]knowledge.EntryID, 0, len(matches))
	for _, match := range matches {
		if _, ok := entries[match.EntryID]; !ok {
			return nil, fmt.Errorf("rank search match references unavailable entry %s", match.EntryID)
		}
		ids = append(ids, match.EntryID)
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	signals, err := s.rankSignals.RankingSignals(ctx, slices.Clone(ids))
	if err != nil {
		return nil, fmt.Errorf("load knowledge ranking signals: %w", err)
	}
	evidence, err := s.loadSearchEvidence(ctx, ids, entries)
	if err != nil {
		return nil, err
	}

	result := slices.Clone(matches)
	for index, match := range result {
		entry := entries[match.EntryID]
		rank := SearchRank{
			Lexical:      math.Log1p(max(0, match.LexicalScore)),
			Graph:        graphRank(len(match.GraphConnections)),
			Scope:        scopeRank(entry.Scope, scopes),
			Verification: verificationRank(entry.Verification.Status),
			Freshness:    freshnessRank(entry, asOf),
			Evidence:     evidenceRank(evidence[entry.ID]),
			Reuse:        reuseRank(entry.LastUsedAt, signals[entry.ID].ReuseCount, asOf),
			Outcomes:     outcomeRank(signals[entry.ID]),
		}
		rank.Total = rank.Lexical + rank.Graph + rank.Scope + rank.Verification +
			rank.Freshness + rank.Evidence + rank.Reuse + rank.Outcomes
		match.Rank = roundSearchRank(rank)
		result[index] = match
	}
	slices.SortFunc(result, func(left, right LexicalSearchMatch) int {
		if left.Rank.Total > right.Rank.Total {
			return -1
		}
		if left.Rank.Total < right.Rank.Total {
			return 1
		}
		if left.Rank.Verification > right.Rank.Verification {
			return -1
		}
		if left.Rank.Verification < right.Rank.Verification {
			return 1
		}
		if left.LexicalScore > right.LexicalScore {
			return -1
		}
		if left.LexicalScore < right.LexicalScore {
			return 1
		}
		return strings.Compare(string(left.EntryID), string(right.EntryID))
	})
	return result, nil
}

func (s *Service) loadSearchEvidence(ctx context.Context, ids []knowledge.EntryID, entries map[knowledge.EntryID]knowledge.Entry) (map[knowledge.EntryID][]knowledge.EvidenceQuality, error) {
	result := make(map[knowledge.EntryID][]knowledge.EvidenceQuality, len(ids))
	err := s.store.View(ctx, func(tx knowledgeStore.ReadTx) error {
		for _, entryID := range ids {
			entry := entries[entryID]
			evidenceIDs := append(slices.Clone(entry.EvidenceIDs), entry.Verification.EvidenceIDs...)
			slices.Sort(evidenceIDs)
			evidenceIDs = slices.Compact(evidenceIDs)
			for _, evidenceID := range evidenceIDs {
				evidence, err := tx.Evidence(ctx, evidenceID)
				if errors.Is(err, knowledgeStore.ErrNotFound) {
					continue
				}
				if err != nil {
					return err
				}
				result[entryID] = append(result[entryID], evidence.Quality)
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("load knowledge search evidence: %w", err)
	}
	return result, nil
}

func graphRank(connections int) float64 {
	return 0.30 * min(1, math.Log1p(float64(connections))/math.Log(4))
}

func scopeRank(scope knowledge.Scope, requested []knowledge.Scope) float64 {
	if len(requested) > 0 && !slices.Contains(requested, scope) {
		return 0
	}
	switch scope.Kind {
	case knowledge.ScopeKindSession:
		return 0.35
	case knowledge.ScopeKindEnvironment:
		return 0.32
	case knowledge.ScopeKindProject:
		return 0.29
	case knowledge.ScopeKindPersonal:
		return 0.25
	case knowledge.ScopeKindGlobal:
		return 0.10
	default:
		return 0
	}
}

func verificationRank(status knowledge.VerificationStatus) float64 {
	switch status {
	case knowledge.VerificationStatusVerified:
		return 0.80
	case knowledge.VerificationStatusPartiallyVerified:
		return 0.40
	case knowledge.VerificationStatusUnverified:
		return 0
	case knowledge.VerificationStatusDisputed:
		return -1.50
	default:
		return -1.50
	}
}

func freshnessRank(entry knowledge.Entry, asOf time.Time) float64 {
	anchor := entry.UpdatedAt
	if entry.Verification.VerifiedAt.After(anchor) {
		anchor = entry.Verification.VerifiedAt
	}
	if anchor.IsZero() {
		return 0
	}
	age := asOf.Sub(anchor)
	switch {
	case age <= 7*24*time.Hour:
		return 0.20
	case age <= 30*24*time.Hour:
		return 0.16
	case age <= 90*24*time.Hour:
		return 0.12
	case age <= 365*24*time.Hour:
		return 0.06
	default:
		return 0.02
	}
}

func evidenceRank(qualities []knowledge.EvidenceQuality) float64 {
	best := 0.0
	for _, quality := range qualities {
		value := 0.0
		switch quality {
		case knowledge.EvidenceQualityAuthoritative:
			value = 1
		case knowledge.EvidenceQualityPrimary:
			value = 0.9
		case knowledge.EvidenceQualitySecondary:
			value = 0.6
		case knowledge.EvidenceQualityAnecdotal:
			value = 0.3
		case knowledge.EvidenceQualityGenerated:
			value = 0.2
		}
		best = max(best, value)
	}
	if best == 0 {
		return 0
	}
	countBonus := min(0.08, 0.02*float64(max(0, len(qualities)-1)))
	return 0.32*best + countBonus
}

func reuseRank(lastUsedAt time.Time, count uint64, asOf time.Time) float64 {
	countScore := min(1, math.Log1p(float64(count))/math.Log(11))
	recency := 0.0
	if !lastUsedAt.IsZero() {
		age := asOf.Sub(lastUsedAt)
		switch {
		case age <= 30*24*time.Hour:
			recency = 1
		case age <= 180*24*time.Hour:
			recency = 0.5
		default:
			recency = 0.2
		}
	}
	return 0.10 * max(countScore, recency)
}

func outcomeRank(signals RankingSignals) float64 {
	total := float64(signals.SuccessfulOutcomes) + float64(signals.FailedOutcomes)
	if total == 0 {
		return 0
	}
	balance := (float64(signals.SuccessfulOutcomes) - float64(signals.FailedOutcomes)) / (total + 2)
	return 0.20 * balance
}

func roundSearchRank(rank SearchRank) SearchRank {
	round := func(value float64) float64 { return math.Round(value*rankingPrecision) / rankingPrecision }
	rank.Lexical = round(rank.Lexical)
	rank.Graph = round(rank.Graph)
	rank.Scope = round(rank.Scope)
	rank.Verification = round(rank.Verification)
	rank.Freshness = round(rank.Freshness)
	rank.Evidence = round(rank.Evidence)
	rank.Reuse = round(rank.Reuse)
	rank.Outcomes = round(rank.Outcomes)
	rank.Total = round(rank.Lexical + rank.Graph + rank.Scope + rank.Verification + rank.Freshness + rank.Evidence + rank.Reuse + rank.Outcomes)
	return rank
}
