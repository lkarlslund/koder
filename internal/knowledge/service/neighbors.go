package service

import (
	"context"
	"fmt"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

type NeighborRequest struct {
	Endpoint  knowledge.ObjectRef
	Direction knowledgeStore.LinkDirection
	Kinds     []knowledge.LinkKind
	Limit     int
	Cursor    string
}

type Neighbor struct {
	Direction knowledgeStore.LinkDirection   `json:"direction"`
	Link      knowledge.Link                 `json:"link"`
	Object    knowledgeStore.CanonicalRecord `json:"object"`
}

type NeighborPage struct {
	Neighbors  []Neighbor `json:"neighbors"`
	NextCursor string     `json:"next_cursor,omitempty"`
}

func (s *Service) Neighbors(ctx context.Context, request NeighborRequest) (NeighborPage, error) {
	if err := ctx.Err(); err != nil {
		return NeighborPage{}, err
	}
	listRequest, err := knowledgeStore.NormalizeAdjacentLinkListRequest(knowledgeStore.AdjacentLinkListRequest{
		Filter: knowledgeStore.AdjacentLinkFilter{
			Endpoint: request.Endpoint, Direction: request.Direction, Kinds: request.Kinds,
			States: []knowledge.LinkState{knowledge.LinkStateActive},
		},
		Limit: request.Limit, Cursor: request.Cursor,
	})
	if err != nil {
		return NeighborPage{}, err
	}
	actor, err := s.actor(ctx)
	if err != nil {
		return NeighborPage{}, fmt.Errorf("resolve knowledge actor: %w", err)
	}
	if err := actor.Validate(); err != nil {
		return NeighborPage{}, err
	}
	var rootChunk knowledge.Chunk
	if err := s.store.View(ctx, func(tx knowledgeStore.ReadTx) error {
		_, chunk, err := resolveKnowledgeObject(ctx, tx, request.Endpoint)
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
			return NeighborPage{}, fmt.Errorf("list adjacent knowledge links: %w", err)
		}
		if len(links.Links) == 0 {
			break
		}
		link := links.Links[0]
		cursor = links.NextCursor
		neighbor, allowed, err := s.authorizedNeighbor(ctx, actor, listRequest.Filter.Endpoint, link)
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
		hasMore, err := s.hasAuthorizedNeighborAfter(ctx, actor, listRequest.Filter.Endpoint, scan, cursor)
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

func (s *Service) hasAuthorizedNeighborAfter(ctx context.Context, actor knowledge.Actor, endpoint knowledge.ObjectRef, scan knowledgeStore.AdjacentLinkListRequest, cursor string) (bool, error) {
	for cursor != "" {
		scan.Cursor = cursor
		page, err := s.store.ListAdjacentLinks(ctx, scan)
		if err != nil {
			return false, fmt.Errorf("look ahead for authorized knowledge neighbor: %w", err)
		}
		if len(page.Links) == 0 {
			return false, nil
		}
		_, allowed, err := s.authorizedNeighbor(ctx, actor, endpoint, page.Links[0])
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

func (s *Service) authorizedNeighbor(ctx context.Context, actor knowledge.Actor, endpoint knowledge.ObjectRef, link knowledge.Link) (Neighbor, bool, error) {
	other, direction, err := oppositeEndpoint(link, endpoint)
	if err != nil {
		return Neighbor{}, false, err
	}
	var record knowledgeStore.CanonicalRecord
	var chunk knowledge.Chunk
	if err := s.store.View(ctx, func(tx knowledgeStore.ReadTx) error {
		var err error
		record, chunk, err = resolveKnowledgeObject(ctx, tx, other)
		return err
	}); err != nil {
		return Neighbor{}, false, fmt.Errorf("resolve knowledge neighbor: %w", err)
	}
	if err := s.authorizeLinkChunks(ctx, actor, ChunkPolicyTraverse, true, chunk); err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return Neighbor{}, false, contextErr
		}
		return Neighbor{}, false, nil
	}
	return Neighbor{Direction: direction, Link: link, Object: record}, true, nil
}

func resolveKnowledgeObject(ctx context.Context, tx knowledgeStore.ReadTx, object knowledge.ObjectRef) (knowledgeStore.CanonicalRecord, knowledge.Chunk, error) {
	if err := object.Validate(); err != nil {
		return knowledgeStore.CanonicalRecord{}, knowledge.Chunk{}, err
	}
	switch object.Kind {
	case knowledge.ObjectKindChunk:
		chunk, err := tx.Chunk(ctx, knowledge.ChunkID(object.ID))
		if err != nil {
			return knowledgeStore.CanonicalRecord{}, knowledge.Chunk{}, err
		}
		return knowledgeStore.CanonicalRecord{Kind: knowledgeStore.RecordKindChunk, Chunk: &chunk}, chunk, nil
	case knowledge.ObjectKindEntry:
		entry, err := tx.Entry(ctx, knowledge.EntryID(object.ID))
		if err != nil {
			return knowledgeStore.CanonicalRecord{}, knowledge.Chunk{}, err
		}
		chunk, err := tx.Chunk(ctx, entry.ChunkID)
		if err != nil {
			return knowledgeStore.CanonicalRecord{}, knowledge.Chunk{}, err
		}
		return knowledgeStore.CanonicalRecord{Kind: knowledgeStore.RecordKindEntry, Entry: &entry}, chunk, nil
	default:
		return knowledgeStore.CanonicalRecord{}, knowledge.Chunk{}, fmt.Errorf("%w: traversal object must identify a chunk or entry", knowledge.ErrInvalidRecord)
	}
}

func oppositeEndpoint(link knowledge.Link, root knowledge.ObjectRef) (knowledge.ObjectRef, knowledgeStore.LinkDirection, error) {
	if link.Source == root {
		return link.Target, knowledgeStore.LinkDirectionOutgoing, nil
	}
	if link.Target == root {
		return link.Source, knowledgeStore.LinkDirectionIncoming, nil
	}
	return knowledge.ObjectRef{}, "", fmt.Errorf("adjacent link %s does not touch traversal root", link.ID)
}
