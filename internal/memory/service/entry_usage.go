package service

import (
	"context"
	"fmt"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

type usageRankingSignalSource struct {
	store memoryStoreAPI.UsageStore
}

func (s usageRankingSignalSource) RankingSignals(ctx context.Context, ids []memory.EntryID) (map[memory.EntryID]RankingSignals, error) {
	usage, err := s.store.EntryUsage(ctx, ids)
	if err != nil {
		return nil, err
	}
	result := make(map[memory.EntryID]RankingSignals, len(usage))
	for id, item := range usage {
		result[id] = RankingSignals{
			LastUsedAt: item.LastUsedAt, ReuseCount: item.ReuseCount,
			SuccessfulOutcomes: item.SuccessfulOutcomes, FailedOutcomes: item.FailedOutcomes,
		}
	}
	return result, nil
}

type RecordEntryUsageRequest struct {
	EntryID memory.EntryID
	EventID string
	Outcome memoryStoreAPI.EntryOutcome
}

type RecordEntryUsageResult struct {
	Usage    memoryStoreAPI.EntryUsage `json:"usage"`
	Recorded bool                      `json:"recorded"`
}

// RecordEntryUsage records one idempotent use/outcome event without changing canonical
// entry content or its revision history.
func (s *Service) RecordEntryUsage(ctx context.Context, request RecordEntryUsageRequest) (RecordEntryUsageResult, error) {
	if request.Outcome == "" {
		request.Outcome = memoryStoreAPI.EntryOutcomeNone
	}
	usageStore, ok := s.store.(memoryStoreAPI.UsageStore)
	if !ok {
		return RecordEntryUsageResult{}, memoryStoreAPI.ErrUnsupported
	}
	actor, err := s.actor(ctx)
	if err != nil {
		return RecordEntryUsageResult{}, fmt.Errorf("resolve memory actor: %w", err)
	}
	if err := actor.Validate(); err != nil {
		return RecordEntryUsageResult{}, err
	}
	var chunk memory.Chunk
	if err := s.store.View(ctx, func(tx memoryStoreAPI.ReadTx) error {
		entry, err := tx.Entry(ctx, request.EntryID)
		if err != nil {
			return err
		}
		chunk, err = tx.Chunk(ctx, entry.ChunkID)
		return err
	}); err != nil {
		return RecordEntryUsageResult{}, fmt.Errorf("resolve entry usage owner: %w", err)
	}
	if err := s.chunkPolicy.AuthorizeChunk(ctx, actor, ChunkPolicySearch, chunk); err != nil {
		return RecordEntryUsageResult{}, fmt.Errorf("%w: record usage for chunk %s: %v", ErrChunkPolicyDenied, chunk.ID, err)
	}
	event := memoryStoreAPI.EntryUsageEvent{
		EntryID: request.EntryID, EventID: request.EventID,
		UsedAt: s.now().UTC().Round(0), Outcome: request.Outcome,
	}
	usage, recorded, err := usageStore.RecordEntryUsage(ctx, event)
	if err != nil {
		return RecordEntryUsageResult{}, fmt.Errorf("record memory entry usage: %w", err)
	}
	return RecordEntryUsageResult{Usage: usage, Recorded: recorded}, nil
}
