package service

import (
	"context"
	"fmt"

	"github.com/lkarlslund/koder/internal/knowledge"
)

// GraphSnapshotResult is a bounded, authorized traversal annotated with the
// active index generation so clients can discard snapshots from retired views.
type GraphSnapshotResult struct {
	Generation        uint64
	Checkpoint        MutationCheckpoint
	Nodes             []TraversalNode
	Edges             []knowledge.Link
	Truncated         bool
	TruncationReasons []string
}

func (s *Service) GraphSnapshot(ctx context.Context, request TraversalRequest) (GraphSnapshotResult, error) {
	checkpoint := s.MutationCheckpoint()
	traversal, err := s.Traverse(ctx, request)
	if err != nil {
		return GraphSnapshotResult{}, err
	}
	health, err := s.store.Health(ctx)
	if err != nil {
		return GraphSnapshotResult{}, fmt.Errorf("read knowledge graph generation: %w", err)
	}
	return GraphSnapshotResult{
		Generation: health.IndexGeneration, Checkpoint: checkpoint, Nodes: traversal.Nodes, Edges: traversal.Edges,
		Truncated: traversal.Truncated, TruncationReasons: traversal.TruncationReasons,
	}, nil
}
