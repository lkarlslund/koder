package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

var ErrDuplicateLink = errors.New("duplicate memory link")

type DuplicateLinkError struct {
	Existing memory.Link
}

func (e *DuplicateLinkError) Error() string {
	return fmt.Sprintf("%s: existing link %s", ErrDuplicateLink, e.Existing.ID)
}

func (e *DuplicateLinkError) Unwrap() []error {
	return []error{ErrDuplicateLink, memoryStoreAPI.ErrConflict}
}

type CreateLinkRequest struct {
	Link           memory.Link
	ReviewApproved bool
}

type CreateLinkResult struct {
	Link           memory.Link
	Classification memory.ClassificationResult
}

func (s *Service) CreateLink(ctx context.Context, request CreateLinkRequest) (CreateLinkResult, error) {
	if err := ctx.Err(); err != nil {
		return CreateLinkResult{}, err
	}
	candidate := request.Link
	if candidate.ID != "" || candidate.State != memory.LinkStateUnspecified || candidate.Revision != (memory.Revision{}) ||
		!candidate.CreatedAt.IsZero() || !candidate.UpdatedAt.IsZero() {
		return CreateLinkResult{}, fmt.Errorf("%w: create link contains server-owned identity, lifecycle, revision, or timestamp fields", memory.ErrInvalidRecord)
	}
	classification, err := s.classifier.Classify(ctx, linkClassificationInput(candidate))
	if err != nil {
		return CreateLinkResult{}, fmt.Errorf("classify link candidate: %w", err)
	}
	if err := requireClassificationApproval(classification, request.ReviewApproved); err != nil {
		return CreateLinkResult{Classification: classification}, err
	}
	candidate = memory.NormalizeLink(candidate)
	if err := memory.ValidateRelationshipShape(candidate.Kind, candidate.Source, candidate.Target); err != nil {
		return CreateLinkResult{}, err
	}
	actor, err := s.actor(ctx)
	if err != nil {
		return CreateLinkResult{}, fmt.Errorf("resolve memory actor: %w", err)
	}
	if err := actor.Validate(); err != nil {
		return CreateLinkResult{}, err
	}
	result := CreateLinkResult{Classification: classification}
	err = s.store.Update(ctx, func(tx memoryStoreAPI.WriteTx) error {
		sourceChunk, err := resolveLinkEndpoint(ctx, tx, candidate.Source)
		if err != nil {
			return fmt.Errorf("resolve link source: %w", err)
		}
		targetChunk, err := resolveLinkEndpoint(ctx, tx, candidate.Target)
		if err != nil {
			return fmt.Errorf("resolve link target: %w", err)
		}
		if err := s.authorizeLinkChunks(ctx, actor, ChunkPolicyLinkCreate, true, sourceChunk, targetChunk); err != nil {
			return err
		}
		if err := validateEvidenceReferences(ctx, tx, candidate.EvidenceIDs); err != nil {
			return err
		}
		if existing, err := tx.EquivalentLink(ctx, candidate); err == nil {
			return &DuplicateLinkError{Existing: existing}
		} else if !errors.Is(err, memoryStoreAPI.ErrNotFound) {
			return err
		}
		now := s.now().UTC().Round(0)
		candidate.ID = memory.LinkID(s.newID())
		candidate.State = memory.LinkStateActive
		candidate.Revision = memory.Revision{
			Number: 1, ID: memory.RevisionID(s.newID()), Actor: actor, CreatedAt: now,
		}
		candidate.CreatedAt = now
		candidate.UpdatedAt = now
		if err := candidate.Validate(); err != nil {
			return err
		}
		if err := tx.PutLink(ctx, candidate, 0); err != nil {
			return err
		}
		result.Link = candidate
		return nil
	})
	if err != nil {
		return CreateLinkResult{}, fmt.Errorf("create memory link: %w", err)
	}
	s.publishMutation(ctx, linkMutation(MutationCreated, result.Link))
	return result, nil
}

func (s *Service) Link(ctx context.Context, linkID memory.LinkID) (memory.Link, error) {
	record, err := s.Get(ctx, memory.ObjectRef{Kind: memory.ObjectKindLink, ID: string(linkID)})
	if err != nil {
		return memory.Link{}, err
	}
	if record.Link == nil {
		return memory.Link{}, fmt.Errorf("%w: link projection is unavailable", memory.ErrInvalidRecord)
	}
	return *record.Link, nil
}

func resolveLinkEndpoint(ctx context.Context, tx memoryStoreAPI.ReadTx, endpoint memory.ObjectRef) (memory.Chunk, error) {
	if err := endpoint.Validate(); err != nil {
		return memory.Chunk{}, err
	}
	switch endpoint.Kind {
	case memory.ObjectKindChunk:
		return tx.Chunk(ctx, memory.ChunkID(endpoint.ID))
	case memory.ObjectKindEntry:
		entry, err := tx.Entry(ctx, memory.EntryID(endpoint.ID))
		if err != nil {
			return memory.Chunk{}, err
		}
		return tx.Chunk(ctx, entry.ChunkID)
	default:
		return memory.Chunk{}, fmt.Errorf("%w: link endpoint must identify a chunk or entry", memory.ErrInvalidRecord)
	}
}

func linkClassificationInput(value memory.Link) memory.ClassificationInput {
	return memory.ClassificationInput{Fields: []memory.ClassificationField{
		{Name: "label", Value: value.Label}, {Name: "notes", Value: value.Notes},
	}}
}
