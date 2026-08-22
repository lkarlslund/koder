package service

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
)

func TestRankSearchMatchesCombinesEverySignalWithAuditableComponents(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	const (
		weakID     knowledge.EntryID    = "01a01f76-1ff6-7c1d-967a-66ad5703dd33"
		strongID   knowledge.EntryID    = "01a01f76-1ff6-7c1d-967a-66ad5703dd34"
		evidenceID knowledge.EvidenceID = "01a01688-fc6b-7a53-a907-4f903461820e"
	)
	evidence := knowledge.Evidence{
		ID: evidenceID, Type: knowledge.EvidenceTypeObservation, Quality: knowledge.EvidenceQualityAuthoritative,
		Source: knowledge.Source{ID: "ranking:test"}, Actor: knowledge.Actor{Kind: knowledge.ActorKindSystem, ID: "test"},
		CreatedAt: serviceTime,
	}
	if err := store.Update(ctx, func(tx knowledgeStore.WriteTx) error { return tx.PutEvidence(ctx, evidence) }); err != nil {
		t.Fatalf("seed ranking evidence: %v", err)
	}
	weak := knowledge.Entry{
		ID: weakID, Scope: knowledge.Scope{Kind: knowledge.ScopeKindGlobal},
		Verification: knowledge.Verification{Status: knowledge.VerificationStatusUnverified},
		UpdatedAt:    serviceTime.Add(-2 * 365 * 24 * time.Hour),
	}
	strong := knowledge.Entry{
		ID: strongID, Scope: knowledge.Scope{Kind: knowledge.ScopeKindProject, Selector: "koder"},
		Verification: knowledge.Verification{
			Status: knowledge.VerificationStatusVerified,
			Actor:  knowledge.Actor{Kind: knowledge.ActorKindSystem, ID: "test"}, VerifiedAt: serviceTime,
		},
		EvidenceIDs: []knowledge.EvidenceID{evidenceID}, UpdatedAt: serviceTime, LastUsedAt: serviceTime,
	}
	var requested []knowledge.EntryID
	service := newLexicalSearchTestService(t, store)
	service.rankSignals = RankingSignalSourceFunc(func(_ context.Context, ids []knowledge.EntryID) (map[knowledge.EntryID]RankingSignals, error) {
		requested = slices.Clone(ids)
		return map[knowledge.EntryID]RankingSignals{
			weakID:   {FailedOutcomes: 5},
			strongID: {ReuseCount: 10, SuccessfulOutcomes: 8},
		}, nil
	})
	matches := []LexicalSearchMatch{
		{EntryID: weakID, LexicalScore: 1},
		{EntryID: strongID, LexicalScore: 1, GraphConnections: []GraphConnection{{LinkID: "01a020a6-84d5-7b03-a995-bb2cfb4528b0"}}},
	}
	entries := map[knowledge.EntryID]knowledge.Entry{weakID: weak, strongID: strong}
	got, err := service.rankSearchMatches(ctx, matches, entries,
		[]knowledge.Scope{{Kind: knowledge.ScopeKindGlobal}, strong.Scope}, serviceTime)
	if err != nil {
		t.Fatalf("rankSearchMatches() error = %v", err)
	}
	if !slices.Equal(requested, []knowledge.EntryID{weakID, strongID}) {
		t.Fatalf("ranking signal IDs = %v", requested)
	}
	if len(got) != 2 || got[0].EntryID != strongID {
		t.Fatalf("ranked matches = %#v", got)
	}
	strongRank, weakRank := got[0].Rank, got[1].Rank
	if strongRank.Graph <= weakRank.Graph || strongRank.Scope <= weakRank.Scope ||
		strongRank.Verification <= weakRank.Verification || strongRank.Freshness <= weakRank.Freshness ||
		strongRank.Evidence <= weakRank.Evidence || strongRank.Reuse <= weakRank.Reuse ||
		strongRank.Outcomes <= weakRank.Outcomes || strongRank.Total <= weakRank.Total {
		t.Fatalf("strong rank = %#v, weak rank = %#v", strongRank, weakRank)
	}
	wantTotal := strongRank.Lexical + strongRank.Graph + strongRank.Scope + strongRank.Verification +
		strongRank.Freshness + strongRank.Evidence + strongRank.Reuse + strongRank.Outcomes
	if difference := strongRank.Total - wantTotal; difference < -1e-8 || difference > 1e-8 {
		t.Fatalf("rank total %f != component sum %f", strongRank.Total, wantTotal)
	}
}

func TestRankSearchMatchesUsesEntryIDAsFinalDeterministicTieBreaker(t *testing.T) {
	t.Parallel()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newLexicalSearchTestService(t, store)
	low := knowledge.Entry{ID: "01a01f76-1ff6-7c1d-967a-66ad5703dd33", Scope: knowledge.Scope{Kind: knowledge.ScopeKindGlobal}, Verification: knowledge.Verification{Status: knowledge.VerificationStatusUnverified}, UpdatedAt: serviceTime}
	high := low
	high.ID = "01a01f76-1ff6-7c1d-967a-66ad5703dd34"
	got, err := service.rankSearchMatches(context.Background(), []LexicalSearchMatch{
		{EntryID: high.ID, LexicalScore: 1}, {EntryID: low.ID, LexicalScore: 1},
	}, map[knowledge.EntryID]knowledge.Entry{low.ID: low, high.ID: high}, nil, serviceTime)
	if err != nil || len(got) != 2 || got[0].EntryID != low.ID {
		t.Fatalf("tied ranking = %#v, %v", got, err)
	}
}
