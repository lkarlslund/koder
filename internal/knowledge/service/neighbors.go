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
	links, err := s.store.ListAdjacentLinks(ctx, knowledgeStore.AdjacentLinkListRequest{
		Filter: knowledgeStore.AdjacentLinkFilter{
			Endpoint: request.Endpoint, Direction: request.Direction, Kinds: request.Kinds,
			States: []knowledge.LinkState{knowledge.LinkStateActive},
		},
		Limit: request.Limit, Cursor: request.Cursor,
	})
	if err != nil {
		return NeighborPage{}, fmt.Errorf("list adjacent knowledge links: %w", err)
	}
	result := NeighborPage{Neighbors: make([]Neighbor, 0, len(links.Links)), NextCursor: links.NextCursor}
	err = s.store.View(ctx, func(tx knowledgeStore.ReadTx) error {
		for _, link := range links.Links {
			other, direction, err := oppositeEndpoint(link, request.Endpoint)
			if err != nil {
				return err
			}
			record, chunk, err := resolveKnowledgeObject(ctx, tx, other)
			if err != nil {
				return err
			}
			if err := s.authorizeLinkChunks(ctx, actor, ChunkPolicyTraverse, true, chunk); err != nil {
				return err
			}
			result.Neighbors = append(result.Neighbors, Neighbor{Direction: direction, Link: link, Object: record})
		}
		return nil
	})
	if err != nil {
		return NeighborPage{}, fmt.Errorf("resolve knowledge neighbors: %w", err)
	}
	return result, nil
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
