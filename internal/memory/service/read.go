package service

import (
	"context"
	"fmt"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

// Get returns one canonical graph object after authorizing every chunk that
// owns or is touched by it. Evidence is intentionally not a graph object and
// is retrieved through evidence-specific service/API operations.
func (s *Service) Get(ctx context.Context, object memory.ObjectRef) (memoryStoreAPI.CanonicalRecord, error) {
	if err := ctx.Err(); err != nil {
		return memoryStoreAPI.CanonicalRecord{}, err
	}
	if err := object.Validate(); err != nil {
		return memoryStoreAPI.CanonicalRecord{}, err
	}
	actor, err := s.actor(ctx)
	if err != nil {
		return memoryStoreAPI.CanonicalRecord{}, fmt.Errorf("resolve memory actor: %w", err)
	}
	if err := actor.Validate(); err != nil {
		return memoryStoreAPI.CanonicalRecord{}, err
	}

	var record memoryStoreAPI.CanonicalRecord
	var chunks []memory.Chunk
	err = s.store.View(ctx, func(tx memoryStoreAPI.ReadTx) error {
		switch object.Kind {
		case memory.ObjectKindChunk:
			chunk, err := tx.Chunk(ctx, memory.ChunkID(object.ID))
			if err != nil {
				return err
			}
			record = memoryStoreAPI.CanonicalRecord{Kind: memoryStoreAPI.RecordKindChunk, Chunk: &chunk}
			chunks = append(chunks, chunk)
		case memory.ObjectKindEntry:
			entry, err := tx.Entry(ctx, memory.EntryID(object.ID))
			if err != nil {
				return err
			}
			chunk, err := tx.Chunk(ctx, entry.ChunkID)
			if err != nil {
				return err
			}
			record = memoryStoreAPI.CanonicalRecord{Kind: memoryStoreAPI.RecordKindEntry, Entry: &entry}
			chunks = append(chunks, chunk)
		case memory.ObjectKindLink:
			link, err := tx.Link(ctx, memory.LinkID(object.ID))
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
			record = memoryStoreAPI.CanonicalRecord{Kind: memoryStoreAPI.RecordKindLink, Link: &link}
			chunks = append(chunks, source, target)
		default:
			return fmt.Errorf("%w: get requires a chunk, entry, or link", memory.ErrInvalidRecord)
		}
		return nil
	})
	if err != nil {
		return memoryStoreAPI.CanonicalRecord{}, fmt.Errorf("get memory object: %w", err)
	}
	if err := s.authorizeLinkChunks(ctx, actor, ChunkPolicyRead, false, chunks...); err != nil {
		return memoryStoreAPI.CanonicalRecord{}, err
	}
	return record, nil
}
