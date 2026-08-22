package service

import (
	"context"
	"fmt"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

type CreateLinkRequest struct {
	Link           knowledge.Link
	ReviewApproved bool
}

type CreateLinkResult struct {
	Link           knowledge.Link
	Classification knowledge.ClassificationResult
}

func (s *Service) CreateLink(ctx context.Context, request CreateLinkRequest) (CreateLinkResult, error) {
	if err := ctx.Err(); err != nil {
		return CreateLinkResult{}, err
	}
	candidate := request.Link
	if candidate.ID != "" || candidate.State != knowledge.LinkStateUnspecified || candidate.Revision != (knowledge.Revision{}) ||
		!candidate.CreatedAt.IsZero() || !candidate.UpdatedAt.IsZero() {
		return CreateLinkResult{}, fmt.Errorf("%w: create link contains server-owned identity, lifecycle, revision, or timestamp fields", knowledge.ErrInvalidRecord)
	}
	classification, err := s.classifier.Classify(ctx, linkClassificationInput(candidate))
	if err != nil {
		return CreateLinkResult{}, fmt.Errorf("classify link candidate: %w", err)
	}
	if err := requireClassificationApproval(classification, request.ReviewApproved); err != nil {
		return CreateLinkResult{Classification: classification}, err
	}
	candidate = knowledge.NormalizeLink(candidate)
	result := CreateLinkResult{Classification: classification}
	err = s.store.Update(ctx, func(tx knowledgeStore.WriteTx) error {
		sourceChunk, err := resolveLinkEndpoint(ctx, tx, candidate.Source)
		if err != nil {
			return fmt.Errorf("resolve link source: %w", err)
		}
		targetChunk, err := resolveLinkEndpoint(ctx, tx, candidate.Target)
		if err != nil {
			return fmt.Errorf("resolve link target: %w", err)
		}
		if err := validateEvidenceReferences(ctx, tx, candidate.EvidenceIDs); err != nil {
			return err
		}
		actor, err := s.actor(ctx)
		if err != nil {
			return fmt.Errorf("resolve knowledge actor: %w", err)
		}
		if err := actor.Validate(); err != nil {
			return err
		}
		if err := s.authorizeLinkChunks(ctx, actor, ChunkPolicyLinkCreate, true, sourceChunk, targetChunk); err != nil {
			return err
		}
		now := s.now().UTC().Round(0)
		candidate.ID = knowledge.LinkID(s.newID())
		candidate.State = knowledge.LinkStateActive
		candidate.Revision = knowledge.Revision{
			Number: 1, ID: knowledge.RevisionID(s.newID()), Actor: actor, CreatedAt: now,
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
		return CreateLinkResult{}, fmt.Errorf("create knowledge link: %w", err)
	}
	return result, nil
}

func (s *Service) Link(ctx context.Context, linkID knowledge.LinkID) (knowledge.Link, error) {
	if err := ctx.Err(); err != nil {
		return knowledge.Link{}, err
	}
	var result knowledge.Link
	if err := s.store.View(ctx, func(tx knowledgeStore.ReadTx) error {
		var err error
		result, err = tx.Link(ctx, linkID)
		return err
	}); err != nil {
		return knowledge.Link{}, fmt.Errorf("get knowledge link: %w", err)
	}
	return result, nil
}

func resolveLinkEndpoint(ctx context.Context, tx knowledgeStore.ReadTx, endpoint knowledge.ObjectRef) (knowledge.Chunk, error) {
	if err := endpoint.Validate(); err != nil {
		return knowledge.Chunk{}, err
	}
	switch endpoint.Kind {
	case knowledge.ObjectKindChunk:
		return tx.Chunk(ctx, knowledge.ChunkID(endpoint.ID))
	case knowledge.ObjectKindEntry:
		entry, err := tx.Entry(ctx, knowledge.EntryID(endpoint.ID))
		if err != nil {
			return knowledge.Chunk{}, err
		}
		return tx.Chunk(ctx, entry.ChunkID)
	default:
		return knowledge.Chunk{}, fmt.Errorf("%w: link endpoint must identify a chunk or entry", knowledge.ErrInvalidRecord)
	}
}

func linkClassificationInput(value knowledge.Link) knowledge.ClassificationInput {
	return knowledge.ClassificationInput{Fields: []knowledge.ClassificationField{
		{Name: "label", Value: value.Label}, {Name: "notes", Value: value.Notes},
	}}
}
