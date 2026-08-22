package service

import (
	"context"
	"fmt"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

type usageRankingSignalSource struct {
	store knowledgeStore.UsageStore
}

func (s usageRankingSignalSource) RankingSignals(ctx context.Context, ids []knowledge.EntryID) (map[knowledge.EntryID]RankingSignals, error) {
	usage, err := s.store.EntryUsage(ctx, ids)
	if err != nil {
		return nil, err
	}
	result := make(map[knowledge.EntryID]RankingSignals, len(usage))
	for id, item := range usage {
		result[id] = RankingSignals{
			LastUsedAt: item.LastUsedAt, ReuseCount: item.ReuseCount,
			SuccessfulOutcomes: item.SuccessfulOutcomes, FailedOutcomes: item.FailedOutcomes,
		}
	}
	return result, nil
}

type RecordEntryUsageRequest struct {
	EntryID knowledge.EntryID
	EventID string
	Outcome knowledgeStore.EntryOutcome
}

type RecordEntryUsageResult struct {
	Usage    knowledgeStore.EntryUsage `json:"usage"`
	Recorded bool                      `json:"recorded"`
}

// RecordEntryUsage records one idempotent use/outcome event without changing canonical
// entry content or its revision history.
func (s *Service) RecordEntryUsage(ctx context.Context, request RecordEntryUsageRequest) (RecordEntryUsageResult, error) {
	if request.Outcome == "" {
		request.Outcome = knowledgeStore.EntryOutcomeNone
	}
	usageStore, ok := s.store.(knowledgeStore.UsageStore)
	if !ok {
		return RecordEntryUsageResult{}, knowledgeStore.ErrUnsupported
	}
	actor, err := s.actor(ctx)
	if err != nil {
		return RecordEntryUsageResult{}, fmt.Errorf("resolve knowledge actor: %w", err)
	}
	if err := actor.Validate(); err != nil {
		return RecordEntryUsageResult{}, err
	}
	var chunk knowledge.Chunk
	if err := s.store.View(ctx, func(tx knowledgeStore.ReadTx) error {
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
	event := knowledgeStore.EntryUsageEvent{
		EntryID: request.EntryID, EventID: request.EventID,
		UsedAt: s.now().UTC().Round(0), Outcome: request.Outcome,
	}
	usage, recorded, err := usageStore.RecordEntryUsage(ctx, event)
	if err != nil {
		return RecordEntryUsageResult{}, fmt.Errorf("record knowledge entry usage: %w", err)
	}
	return RecordEntryUsageResult{Usage: usage, Recorded: recorded}, nil
}
