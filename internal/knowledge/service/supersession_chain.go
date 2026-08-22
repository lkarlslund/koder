package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

const (
	defaultSupersessionChainEntries = 16
	maxSupersessionChainEntries     = 100
)

type SupersessionChainRequest struct {
	EntryID    knowledge.EntryID
	MaxEntries int
}

type SupersessionChain struct {
	Entries         []knowledge.Entry `json:"entries"`
	Complete        bool              `json:"complete"`
	Cycle           bool              `json:"cycle"`
	Truncated       bool              `json:"truncated"`
	BrokenAtEntryID knowledge.EntryID `json:"broken_at_entry_id,omitempty"`
}

func (s *Service) SupersessionChain(ctx context.Context, request SupersessionChainRequest) (SupersessionChain, error) {
	if err := ctx.Err(); err != nil {
		return SupersessionChain{}, err
	}
	if request.EntryID == "" {
		return SupersessionChain{}, fmt.Errorf("%w: entry ID is required", knowledge.ErrInvalidRecord)
	}
	if request.MaxEntries <= 0 {
		request.MaxEntries = defaultSupersessionChainEntries
	}
	if request.MaxEntries > maxSupersessionChainEntries {
		return SupersessionChain{}, fmt.Errorf("supersession chain limit must not exceed %d", maxSupersessionChainEntries)
	}
	actor, err := s.actor(ctx)
	if err != nil {
		return SupersessionChain{}, fmt.Errorf("resolve knowledge actor: %w", err)
	}
	result := SupersessionChain{}
	err = s.store.View(ctx, func(tx knowledgeStore.ReadTx) error {
		currentID := request.EntryID
		seen := make(map[knowledge.EntryID]struct{})
		for len(result.Entries) < request.MaxEntries {
			if _, exists := seen[currentID]; exists {
				result.Cycle = true
				return nil
			}
			entry, err := tx.Entry(ctx, currentID)
			if errors.Is(err, knowledgeStore.ErrNotFound) && len(result.Entries) > 0 {
				result.BrokenAtEntryID = currentID
				return nil
			}
			if err != nil {
				return err
			}
			chunk, err := tx.Chunk(ctx, entry.ChunkID)
			if err != nil {
				return err
			}
			if err := s.authorizeLinkChunks(ctx, actor, ChunkPolicyTraverse, true, chunk); err != nil {
				return err
			}
			seen[currentID] = struct{}{}
			result.Entries = append(result.Entries, entry)
			if entry.SupersededByID == "" {
				result.Complete = true
				return nil
			}
			currentID = entry.SupersededByID
		}
		result.Truncated = true
		return nil
	})
	if err != nil {
		return SupersessionChain{}, fmt.Errorf("resolve knowledge supersession chain: %w", err)
	}
	return result, nil
}
