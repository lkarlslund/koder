package service

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
	memoryBackend "github.com/lkarlslund/koder/internal/memory/store/memory"
)

func TestRankSearchMatchesCombinesEverySignalWithAuditableComponents(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	const (
		weakID     memory.EntryID    = "01a01f76-1ff6-7c1d-967a-66ad5703dd33"
		strongID   memory.EntryID    = "01a01f76-1ff6-7c1d-967a-66ad5703dd34"
		evidenceID memory.EvidenceID = "01a01688-fc6b-7a53-a907-4f903461820e"
	)
	evidence := memory.Evidence{
		ID: evidenceID, Type: memory.EvidenceTypeObservation, Quality: memory.EvidenceQualityAuthoritative,
		Source: memory.Source{ID: "ranking:test"}, Actor: memory.Actor{Kind: memory.ActorKindSystem, ID: "test"},
		CreatedAt: serviceTime,
	}
	if err := store.Update(ctx, func(tx memoryStoreAPI.WriteTx) error { return tx.PutEvidence(ctx, evidence) }); err != nil {
		t.Fatalf("seed ranking evidence: %v", err)
	}
	weak := memory.Entry{
		ID: weakID, Scope: memory.Scope{Kind: memory.ScopeKindGlobal},
		Verification: memory.Verification{Status: memory.VerificationStatusUnverified},
		UpdatedAt:    serviceTime.Add(-2 * 365 * 24 * time.Hour),
	}
	strong := memory.Entry{
		ID: strongID, Scope: memory.Scope{Kind: memory.ScopeKindProject, Selector: "koder"},
		Verification: memory.Verification{
			Status: memory.VerificationStatusVerified,
			Actor:  memory.Actor{Kind: memory.ActorKindSystem, ID: "test"}, VerifiedAt: serviceTime,
		},
		EvidenceIDs: []memory.EvidenceID{evidenceID}, UpdatedAt: serviceTime, LastUsedAt: serviceTime,
	}
	var requested []memory.EntryID
	service := newLexicalSearchTestService(t, store)
	service.rankSignals = RankingSignalSourceFunc(func(_ context.Context, ids []memory.EntryID) (map[memory.EntryID]RankingSignals, error) {
		requested = slices.Clone(ids)
		return map[memory.EntryID]RankingSignals{
			weakID:   {FailedOutcomes: 5},
			strongID: {ReuseCount: 10, SuccessfulOutcomes: 8},
		}, nil
	})
	matches := []LexicalSearchMatch{
		{EntryID: weakID, LexicalScore: 1},
		{EntryID: strongID, LexicalScore: 1, GraphConnections: []GraphConnection{{LinkID: "01a020a6-84d5-7b03-a995-bb2cfb4528b0"}}},
	}
	entries := map[memory.EntryID]memory.Entry{weakID: weak, strongID: strong}
	got, err := service.rankSearchMatches(ctx, matches, entries,
		[]memory.Scope{{Kind: memory.ScopeKindGlobal}, strong.Scope}, serviceTime)
	if err != nil {
		t.Fatalf("rankSearchMatches() error = %v", err)
	}
	if !slices.Equal(requested, []memory.EntryID{weakID, strongID}) {
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
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newLexicalSearchTestService(t, store)
	low := memory.Entry{ID: "01a01f76-1ff6-7c1d-967a-66ad5703dd33", Scope: memory.Scope{Kind: memory.ScopeKindGlobal}, Verification: memory.Verification{Status: memory.VerificationStatusUnverified}, UpdatedAt: serviceTime}
	high := low
	high.ID = "01a01f76-1ff6-7c1d-967a-66ad5703dd34"
	got, err := service.rankSearchMatches(context.Background(), []LexicalSearchMatch{
		{EntryID: high.ID, LexicalScore: 1}, {EntryID: low.ID, LexicalScore: 1},
	}, map[memory.EntryID]memory.Entry{low.ID: low, high.ID: high}, nil, serviceTime)
	if err != nil || len(got) != 2 || got[0].EntryID != low.ID {
		t.Fatalf("tied ranking = %#v, %v", got, err)
	}
}
