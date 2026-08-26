package service

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
	memoryBackend "github.com/lkarlslund/koder/internal/memory/store/memory"
)

func TestRecordEntryUsageIsAuthorizedIdempotentAndFeedsSearchRanking(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newLexicalSearchTestService(t, store)
	_, entry := createLexicalSearchEntry(t, ctx, service, "Usage", memory.Scope{Kind: memory.ScopeKindGlobal}, "Needle usage")
	first, err := service.RecordEntryUsage(ctx, RecordEntryUsageRequest{
		EntryID: entry.ID, EventID: "turn:1", Outcome: memoryStoreAPI.EntryOutcomeSuccess,
	})
	if err != nil || !first.Recorded || first.Usage.ReuseCount != 1 || first.Usage.SuccessfulOutcomes != 1 {
		t.Fatalf("RecordEntryUsage() = %#v, %v", first, err)
	}
	duplicate, err := service.RecordEntryUsage(ctx, RecordEntryUsageRequest{
		EntryID: entry.ID, EventID: "turn:1", Outcome: memoryStoreAPI.EntryOutcomeFailure,
	})
	if err != nil || duplicate.Recorded || duplicate.Usage != first.Usage {
		t.Fatalf("RecordEntryUsage(duplicate) = %#v, %v", duplicate, err)
	}
	search, err := service.SearchLexical(ctx, LexicalSearchRequest{Query: "needle"})
	if err != nil || len(search.Matches) != 1 || search.Matches[0].Rank.Reuse <= 0 || search.Matches[0].Rank.Outcomes <= 0 {
		t.Fatalf("search with usage ranking = %#v, %v", search, err)
	}
	service.chunkPolicy = ChunkPolicyFunc(func(context.Context, memory.Actor, ChunkPolicyAction, memory.Chunk) error {
		return errors.New("denied")
	})
	if _, err := service.RecordEntryUsage(ctx, RecordEntryUsageRequest{
		EntryID: entry.ID, EventID: "turn:2",
	}); !errors.Is(err, ErrChunkPolicyDenied) {
		t.Fatalf("denied RecordEntryUsage() error = %v", err)
	}
}

func TestPopularitySignalsCannotOverrideVerification(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newLexicalSearchTestService(t, store)
	verifiedID := memory.EntryID("01a01f76-1ff6-7c1d-967a-66ad5703dd33")
	popularID := memory.EntryID("01a01f76-1ff6-7c1d-967a-66ad5703dd34")
	service.rankSignals = RankingSignalSourceFunc(func(context.Context, []memory.EntryID) (map[memory.EntryID]RankingSignals, error) {
		return map[memory.EntryID]RankingSignals{
			popularID: {ReuseCount: math.MaxUint64, SuccessfulOutcomes: math.MaxUint64},
		}, nil
	})
	verified := memory.Entry{
		ID: verifiedID, Scope: memory.Scope{Kind: memory.ScopeKindGlobal}, UpdatedAt: serviceTime,
		Verification: memory.Verification{Status: memory.VerificationStatusVerified},
	}
	popular := memory.Entry{
		ID: popularID, Scope: memory.Scope{Kind: memory.ScopeKindGlobal}, UpdatedAt: serviceTime,
		Verification: memory.Verification{Status: memory.VerificationStatusUnverified},
	}
	got, err := service.rankSearchMatches(ctx, []LexicalSearchMatch{
		{EntryID: popularID, LexicalScore: 1}, {EntryID: verifiedID, LexicalScore: 1},
	}, map[memory.EntryID]memory.Entry{popularID: popular, verifiedID: verified}, nil, serviceTime)
	if err != nil || len(got) != 2 || got[0].EntryID != verifiedID {
		t.Fatalf("verification vs popularity ranking = %#v, %v", got, err)
	}
}
