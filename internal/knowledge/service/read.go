package service

import (
	"context"
	"fmt"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

// Get returns one canonical graph object after authorizing every chunk that
// owns or is touched by it. Evidence is intentionally not a graph object and
// is retrieved through evidence-specific service/API operations.
func (s *Service) Get(ctx context.Context, object knowledge.ObjectRef) (knowledgeStore.CanonicalRecord, error) {
	if err := ctx.Err(); err != nil {
		return knowledgeStore.CanonicalRecord{}, err
	}
	if err := object.Validate(); err != nil {
		return knowledgeStore.CanonicalRecord{}, err
	}
	actor, err := s.actor(ctx)
	if err != nil {
		return knowledgeStore.CanonicalRecord{}, fmt.Errorf("resolve knowledge actor: %w", err)
	}
	if err := actor.Validate(); err != nil {
		return knowledgeStore.CanonicalRecord{}, err
	}

	var record knowledgeStore.CanonicalRecord
	var chunks []knowledge.Chunk
	err = s.store.View(ctx, func(tx knowledgeStore.ReadTx) error {
		switch object.Kind {
		case knowledge.ObjectKindChunk:
			chunk, err := tx.Chunk(ctx, knowledge.ChunkID(object.ID))
			if err != nil {
				return err
			}
			record = knowledgeStore.CanonicalRecord{Kind: knowledgeStore.RecordKindChunk, Chunk: &chunk}
			chunks = append(chunks, chunk)
		case knowledge.ObjectKindEntry:
			entry, err := tx.Entry(ctx, knowledge.EntryID(object.ID))
			if err != nil {
				return err
			}
			chunk, err := tx.Chunk(ctx, entry.ChunkID)
			if err != nil {
				return err
			}
			record = knowledgeStore.CanonicalRecord{Kind: knowledgeStore.RecordKindEntry, Entry: &entry}
			chunks = append(chunks, chunk)
		case knowledge.ObjectKindLink:
			link, err := tx.Link(ctx, knowledge.LinkID(object.ID))
			if err != nil {
				return err
			}
			source, err := resolveLinkEndpoint(ctx, tx, link.Source)
			if err != nil {
				return fmt.Errorf("resolve link source: %w", err)
			}
			target, err := resolveLinkEndpoint(ctx, tx, link.Target)
			if err != nil {
				return fmt.Errorf("resolve link target: %w", err)
			}
			record = knowledgeStore.CanonicalRecord{Kind: knowledgeStore.RecordKindLink, Link: &link}
			chunks = append(chunks, source, target)
		default:
			return fmt.Errorf("%w: get requires a chunk, entry, or link", knowledge.ErrInvalidRecord)
		}
		return nil
	})
	if err != nil {
		return knowledgeStore.CanonicalRecord{}, fmt.Errorf("get knowledge object: %w", err)
	}
	if err := s.authorizeLinkChunks(ctx, actor, ChunkPolicyRead, false, chunks...); err != nil {
		return knowledgeStore.CanonicalRecord{}, err
	}
	return record, nil
}
