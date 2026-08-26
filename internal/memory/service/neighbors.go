package service

import (
	"context"
	"fmt"
	"slices"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

type NeighborRequest struct {
	Endpoint  memory.ObjectRef
	Direction memoryStoreAPI.LinkDirection
	Kinds     []memory.LinkKind
	// ScopeKinds bounds the visible graph before pagination. An empty slice
	// permits every scope authorized by ChunkPolicy.
	ScopeKinds []memory.ScopeKind
	Limit      int
	Cursor     string
}

type Neighbor struct {
	Direction memoryStoreAPI.LinkDirection   `json:"direction"`
	Link      memory.Link                    `json:"link"`
	Object    memoryStoreAPI.CanonicalRecord `json:"object"`
}

type NeighborPage struct {
	Neighbors  []Neighbor `json:"neighbors"`
	NextCursor string     `json:"next_cursor,omitempty"`
}

func (s *Service) Neighbors(ctx context.Context, request NeighborRequest) (NeighborPage, error) {
	if err := ctx.Err(); err != nil {
		return NeighborPage{}, err
	}
	request.ScopeKinds = slices.Clone(request.ScopeKinds)
	slices.Sort(request.ScopeKinds)
	request.ScopeKinds = slices.Compact(request.ScopeKinds)
	for _, scopeKind := range request.ScopeKinds {
		if scopeKind == memory.ScopeKindUnspecified || !scopeKind.IsAScopeKind() {
			return NeighborPage{}, fmt.Errorf("%w: invalid neighbor scope kind %q", memory.ErrInvalidRecord, scopeKind)
		}
	}
	listRequest, err := memoryStoreAPI.NormalizeAdjacentLinkListRequest(memoryStoreAPI.AdjacentLinkListRequest{
		Filter: memoryStoreAPI.AdjacentLinkFilter{
			Endpoint: request.Endpoint, Direction: request.Direction, Kinds: request.Kinds,
			States: []memory.LinkState{memory.LinkStateActive},
		},
		Limit: request.Limit, Cursor: request.Cursor,
	})
	if err != nil {
		return NeighborPage{}, err
	}
	actor, err := s.actor(ctx)
	if err != nil {
		return NeighborPage{}, fmt.Errorf("resolve memory actor: %w", err)
	}
	if err := actor.Validate(); err != nil {
		return NeighborPage{}, err
	}
	var rootChunk memory.Chunk
	if err := s.store.View(ctx, func(tx memoryStoreAPI.ReadTx) error {
		_, chunk, err := resolveMemoryObject(ctx, tx, request.Endpoint)
		rootChunk = chunk
		return err
	}); err != nil {
		return NeighborPage{}, fmt.Errorf("resolve traversal root: %w", err)
	}
	if err := s.authorizeLinkChunks(ctx, actor, ChunkPolicyTraverse, true, rootChunk); err != nil {
		return NeighborPage{}, err
	}
	result := NeighborPage{Neighbors: make([]Neighbor, 0, listRequest.Limit)}
	scan := listRequest
	scan.Limit = 1
	cursor := listRequest.Cursor
	for len(result.Neighbors) < listRequest.Limit {
		scan.Cursor = cursor
		links, err := s.store.ListAdjacentLinks(ctx, scan)
		if err != nil {
			return NeighborPage{}, fmt.Errorf("list adjacent memory links: %w", err)
		}
		if len(links.Links) == 0 {
			break
		}
		link := links.Links[0]
		cursor = links.NextCursor
		neighbor, allowed, err := s.authorizedNeighbor(ctx, actor, listRequest.Filter.Endpoint, request.ScopeKinds, link)
		if err != nil {
			return NeighborPage{}, err
		}
		if !allowed {
			if cursor == "" {
				break
			}
			continue
		}
		result.Neighbors = append(result.Neighbors, neighbor)
		if cursor == "" {
			break
		}
	}
	if len(result.Neighbors) == listRequest.Limit && cursor != "" {
		hasMore, err := s.hasAuthorizedNeighborAfter(ctx, actor, listRequest.Filter.Endpoint, request.ScopeKinds, scan, cursor)
		if err != nil {
			return NeighborPage{}, err
		}
		if hasMore {
			result.NextCursor = cursor
		}
	} else if len(result.Neighbors) == listRequest.Limit {
		result.NextCursor = cursor
	}
	return result, nil
}

func (s *Service) hasAuthorizedNeighborAfter(ctx context.Context, actor memory.Actor, endpoint memory.ObjectRef, scopeKinds []memory.ScopeKind, scan memoryStoreAPI.AdjacentLinkListRequest, cursor string) (bool, error) {
	for cursor != "" {
		scan.Cursor = cursor
		page, err := s.store.ListAdjacentLinks(ctx, scan)
		if err != nil {
			return false, fmt.Errorf("look ahead for authorized memory neighbor: %w", err)
		}
		if len(page.Links) == 0 {
			return false, nil
		}
		_, allowed, err := s.authorizedNeighbor(ctx, actor, endpoint, scopeKinds, page.Links[0])
		if err != nil {
			return false, err
		}
		if allowed {
			return true, nil
		}
		cursor = page.NextCursor
	}
	return false, nil
}

func (s *Service) authorizedNeighbor(ctx context.Context, actor memory.Actor, endpoint memory.ObjectRef, scopeKinds []memory.ScopeKind, link memory.Link) (Neighbor, bool, error) {
	other, direction, err := oppositeEndpoint(link, endpoint)
	if err != nil {
		return Neighbor{}, false, err
	}
	var record memoryStoreAPI.CanonicalRecord
	var chunk memory.Chunk
	if err := s.store.View(ctx, func(tx memoryStoreAPI.ReadTx) error {
		var err error
		record, chunk, err = resolveMemoryObject(ctx, tx, other)
		return err
	}); err != nil {
		return Neighbor{}, false, fmt.Errorf("resolve memory neighbor: %w", err)
	}
	if len(scopeKinds) != 0 && !slices.Contains(scopeKinds, chunk.Scope.Kind) {
		return Neighbor{}, false, nil
	}
	if err := s.authorizeLinkChunks(ctx, actor, ChunkPolicyTraverse, true, chunk); err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return Neighbor{}, false, contextErr
		}
		return Neighbor{}, false, nil
	}
	return Neighbor{Direction: direction, Link: link, Object: record}, true, nil
}

func resolveMemoryObject(ctx context.Context, tx memoryStoreAPI.ReadTx, object memory.ObjectRef) (memoryStoreAPI.CanonicalRecord, memory.Chunk, error) {
	if err := object.Validate(); err != nil {
		return memoryStoreAPI.CanonicalRecord{}, memory.Chunk{}, err
	}
	switch object.Kind {
	case memory.ObjectKindChunk:
		chunk, err := tx.Chunk(ctx, memory.ChunkID(object.ID))
		if err != nil {
			return memoryStoreAPI.CanonicalRecord{}, memory.Chunk{}, err
		}
		return memoryStoreAPI.CanonicalRecord{Kind: memoryStoreAPI.RecordKindChunk, Chunk: &chunk}, chunk, nil
	case memory.ObjectKindEntry:
		entry, err := tx.Entry(ctx, memory.EntryID(object.ID))
		if err != nil {
			return memoryStoreAPI.CanonicalRecord{}, memory.Chunk{}, err
		}
		chunk, err := tx.Chunk(ctx, entry.ChunkID)
		if err != nil {
			return memoryStoreAPI.CanonicalRecord{}, memory.Chunk{}, err
		}
		return memoryStoreAPI.CanonicalRecord{Kind: memoryStoreAPI.RecordKindEntry, Entry: &entry}, chunk, nil
	default:
		return memoryStoreAPI.CanonicalRecord{}, memory.Chunk{}, fmt.Errorf("%w: traversal object must identify a chunk or entry", memory.ErrInvalidRecord)
	}
}

func oppositeEndpoint(link memory.Link, root memory.ObjectRef) (memory.ObjectRef, memoryStoreAPI.LinkDirection, error) {
	if link.Source == root {
		return link.Target, memoryStoreAPI.LinkDirectionOutgoing, nil
	}
	if link.Target == root {
		return link.Source, memoryStoreAPI.LinkDirectionIncoming, nil
	}
	return memory.ObjectRef{}, "", fmt.Errorf("adjacent link %s does not touch traversal root", link.ID)
}
