package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

const (
	defaultTraversalDepth     = 2
	maxTraversalDepth         = 8
	defaultTraversalNodes     = 100
	maxTraversalNodes         = 1000
	defaultTraversalEdges     = 200
	maxTraversalEdges         = 2000
	defaultTraversalTimeLimit = 2 * time.Second
	maxTraversalTimeLimit     = 10 * time.Second
)

type TraversalRequest struct {
	Root      knowledge.ObjectRef
	Direction knowledgeStore.LinkDirection
	Kinds     []knowledge.LinkKind
	MaxDepth  int
	MaxNodes  int
	MaxEdges  int
	TimeLimit time.Duration
}

type TraversalNode struct {
	Depth  int                            `json:"depth"`
	Object knowledgeStore.CanonicalRecord `json:"object"`
}

type TraversalResult struct {
	Nodes              []TraversalNode     `json:"nodes"`
	Edges              []knowledge.Link    `json:"edges"`
	Contradictions     []Contradiction     `json:"contradictions,omitempty"`
	SupersessionChains []SupersessionChain `json:"supersession_chains,omitempty"`
	Truncated          bool                `json:"truncated"`
	TruncationReasons  []string            `json:"truncation_reasons,omitempty"`
}

type Contradiction struct {
	LinkID knowledge.LinkID    `json:"link_id"`
	Left   knowledge.ObjectRef `json:"left"`
	Right  knowledge.ObjectRef `json:"right"`
}

type traversalQueueItem struct {
	ref   knowledge.ObjectRef
	depth int
}

func (s *Service) Traverse(ctx context.Context, request TraversalRequest) (TraversalResult, error) {
	request, err := normalizeTraversalRequest(request)
	if err != nil {
		return TraversalResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return TraversalResult{}, err
	}
	traversalCtx, cancel := context.WithTimeout(ctx, request.TimeLimit)
	defer cancel()

	actor, err := s.actor(traversalCtx)
	if err != nil {
		return TraversalResult{}, fmt.Errorf("resolve knowledge actor: %w", err)
	}
	var rootRecord knowledgeStore.CanonicalRecord
	var rootChunk knowledge.Chunk
	if err := s.store.View(traversalCtx, func(tx knowledgeStore.ReadTx) error {
		var err error
		rootRecord, rootChunk, err = resolveKnowledgeObject(traversalCtx, tx, request.Root)
		return err
	}); err != nil {
		return TraversalResult{}, fmt.Errorf("resolve traversal root: %w", err)
	}
	if err := s.authorizeLinkChunks(traversalCtx, actor, ChunkPolicyTraverse, true, rootChunk); err != nil {
		return TraversalResult{}, err
	}

	result := TraversalResult{Nodes: []TraversalNode{{Depth: 0, Object: rootRecord}}}
	queue := []traversalQueueItem{{ref: request.Root, depth: 0}}
	visited := map[knowledge.ObjectRef]struct{}{request.Root: {}}
	seenEdges := make(map[knowledge.LinkID]struct{})
	reasons := make(map[string]struct{})

	for len(queue) > 0 {
		if err := traversalCtx.Err(); err != nil {
			if ctx.Err() != nil {
				return TraversalResult{}, ctx.Err()
			}
			reasons["time_limit"] = struct{}{}
			break
		}
		current := queue[0]
		queue = queue[1:]
		if current.depth >= request.MaxDepth {
			hasMore, err := s.hasUnseenTraversalEdge(traversalCtx, current.ref, request, seenEdges)
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
					reasons["time_limit"] = struct{}{}
					break
				}
				return TraversalResult{}, err
			}
			if hasMore {
				reasons["depth_limit"] = struct{}{}
			}
			continue
		}
		cursor := ""
		for {
			remainingEdges := request.MaxEdges - len(result.Edges)
			if remainingEdges <= 0 {
				reasons["edge_limit"] = struct{}{}
				queue = nil
				break
			}
			page, err := s.Neighbors(traversalCtx, NeighborRequest{
				Endpoint: current.ref, Direction: request.Direction, Kinds: request.Kinds,
				Limit: min(remainingEdges, 100), Cursor: cursor,
			})
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
					reasons["time_limit"] = struct{}{}
					queue = nil
					break
				}
				return TraversalResult{}, fmt.Errorf("traverse neighbors of %s: %w", current.ref.ID, err)
			}
			for neighborIndex, neighbor := range page.Neighbors {
				if _, exists := seenEdges[neighbor.Link.ID]; exists {
					continue
				}
				other := neighbor.Object.ObjectRef()
				if _, exists := visited[other]; !exists {
					if len(result.Nodes) >= request.MaxNodes {
						reasons["node_limit"] = struct{}{}
						continue
					}
					visited[other] = struct{}{}
					depth := current.depth + 1
					result.Nodes = append(result.Nodes, TraversalNode{Depth: depth, Object: neighbor.Object})
					queue = append(queue, traversalQueueItem{ref: other, depth: depth})
				}
				seenEdges[neighbor.Link.ID] = struct{}{}
				result.Edges = append(result.Edges, neighbor.Link)
				if len(result.Edges) >= request.MaxEdges {
					truncated := neighborIndex+1 < len(page.Neighbors) || page.NextCursor != ""
					if !truncated {
						for _, pending := range queue {
							hasMore, checkErr := s.hasUnseenTraversalEdge(traversalCtx, pending.ref, request, seenEdges)
							if checkErr != nil {
								if errors.Is(checkErr, context.DeadlineExceeded) && ctx.Err() == nil {
									reasons["time_limit"] = struct{}{}
									truncated = true
									break
								}
								return TraversalResult{}, checkErr
							}
							if hasMore {
								truncated = true
								break
							}
						}
					}
					if truncated {
						reasons["edge_limit"] = struct{}{}
					}
					queue = nil
					break
				}
			}
			if len(queue) == 0 && len(result.Edges) >= request.MaxEdges {
				break
			}
			if page.NextCursor == "" {
				break
			}
			cursor = page.NextCursor
		}
	}
	for _, reason := range []string{"depth_limit", "node_limit", "edge_limit", "time_limit"} {
		if _, exists := reasons[reason]; exists {
			result.TruncationReasons = append(result.TruncationReasons, reason)
		}
	}
	result.Truncated = len(result.TruncationReasons) > 0
	if err := s.enrichTraversalSemantics(ctx, &result); err != nil {
		return TraversalResult{}, err
	}
	return result, nil
}

func (s *Service) enrichTraversalSemantics(ctx context.Context, result *TraversalResult) error {
	for _, link := range result.Edges {
		if link.Kind == knowledge.LinkKindContradicts {
			result.Contradictions = append(result.Contradictions, Contradiction{
				LinkID: link.ID, Left: link.Source, Right: link.Target,
			})
		}
	}
	covered := make(map[knowledge.EntryID]struct{})
	for _, node := range result.Nodes {
		if node.Object.Kind != knowledgeStore.RecordKindEntry || node.Object.Entry == nil || node.Object.Entry.SupersededByID == "" {
			continue
		}
		if _, exists := covered[node.Object.Entry.ID]; exists {
			continue
		}
		chain, err := s.SupersessionChain(ctx, SupersessionChainRequest{EntryID: node.Object.Entry.ID})
		if err != nil {
			return err
		}
		for _, entry := range chain.Entries {
			covered[entry.ID] = struct{}{}
		}
		result.SupersessionChains = append(result.SupersessionChains, chain)
	}
	return nil
}

func (s *Service) hasUnseenTraversalEdge(ctx context.Context, endpoint knowledge.ObjectRef, request TraversalRequest, seen map[knowledge.LinkID]struct{}) (bool, error) {
	cursor := ""
	for {
		page, err := s.Neighbors(ctx, NeighborRequest{
			Endpoint: endpoint, Direction: request.Direction, Kinds: request.Kinds, Limit: 100, Cursor: cursor,
		})
		if err != nil {
			return false, err
		}
		for _, neighbor := range page.Neighbors {
			if _, exists := seen[neighbor.Link.ID]; !exists {
				return true, nil
			}
		}
		if page.NextCursor == "" {
			return false, nil
		}
		cursor = page.NextCursor
	}
}

func normalizeTraversalRequest(request TraversalRequest) (TraversalRequest, error) {
	if err := request.Root.Validate(); err != nil {
		return TraversalRequest{}, err
	}
	if request.Root.Kind != knowledge.ObjectKindChunk && request.Root.Kind != knowledge.ObjectKindEntry {
		return TraversalRequest{}, fmt.Errorf("%w: traversal root must identify a chunk or entry", knowledge.ErrInvalidRecord)
	}
	if request.Direction == "" {
		request.Direction = knowledgeStore.LinkDirectionBoth
	}
	switch request.Direction {
	case knowledgeStore.LinkDirectionOutgoing, knowledgeStore.LinkDirectionIncoming, knowledgeStore.LinkDirectionBoth:
	default:
		return TraversalRequest{}, fmt.Errorf("invalid traversal direction %q", request.Direction)
	}
	if request.MaxDepth <= 0 {
		request.MaxDepth = defaultTraversalDepth
	}
	if request.MaxNodes <= 0 {
		request.MaxNodes = defaultTraversalNodes
	}
	if request.MaxEdges <= 0 {
		request.MaxEdges = defaultTraversalEdges
	}
	if request.TimeLimit <= 0 {
		request.TimeLimit = defaultTraversalTimeLimit
	}
	if request.MaxDepth > maxTraversalDepth || request.MaxNodes > maxTraversalNodes || request.MaxEdges > maxTraversalEdges ||
		request.TimeLimit > maxTraversalTimeLimit {
		return TraversalRequest{}, fmt.Errorf("traversal limits exceed depth=%d nodes=%d edges=%d time=%s", maxTraversalDepth, maxTraversalNodes, maxTraversalEdges, maxTraversalTimeLimit)
	}
	for _, kind := range request.Kinds {
		if kind == knowledge.LinkKindUnspecified || !kind.IsALinkKind() {
			return TraversalRequest{}, fmt.Errorf("invalid traversal link kind")
		}
	}
	return request, nil
}
