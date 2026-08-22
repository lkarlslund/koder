package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

type CreateEvidenceRequest struct {
	Evidence       knowledge.Evidence
	ReviewApproved bool
}

type CreateEvidenceResult struct {
	Evidence       knowledge.Evidence
	Classification knowledge.ClassificationResult
	Created        bool
}

func (s *Service) CreateEvidence(ctx context.Context, request CreateEvidenceRequest) (CreateEvidenceResult, error) {
	if err := ctx.Err(); err != nil {
		return CreateEvidenceResult{}, err
	}
	candidate := request.Evidence
	if candidate.ID != "" || candidate.Actor != (knowledge.Actor{}) || !candidate.CreatedAt.IsZero() {
		return CreateEvidenceResult{}, fmt.Errorf("%w: create evidence contains server-owned identity, actor, or creation time", knowledge.ErrInvalidRecord)
	}
	classification, err := s.classifier.Classify(ctx, evidenceClassificationInput(candidate))
	if err != nil {
		return CreateEvidenceResult{}, fmt.Errorf("classify evidence candidate: %w", err)
	}
	if err := requireClassificationApproval(classification, request.ReviewApproved); err != nil {
		return CreateEvidenceResult{Classification: classification}, err
	}
	candidate.Source.ID, candidate.Source.ContentHash = knowledge.NormalizeEvidenceIdentity(candidate.Source.ID, candidate.Source.ContentHash)
	candidate.Source.URI = strings.TrimSpace(candidate.Source.URI)
	candidate.Source.Title = knowledge.NormalizeTitle(candidate.Source.Title)
	candidate.Source.Excerpt = strings.TrimSpace(candidate.Source.Excerpt)
	result := CreateEvidenceResult{Classification: classification}
	err = s.store.Update(ctx, func(tx knowledgeStore.WriteTx) error {
		existing, err := tx.EvidenceBySource(ctx, candidate.Source.ID, candidate.Source.ContentHash)
		switch {
		case err == nil:
			result.Evidence = existing
			return nil
		case !errors.Is(err, knowledgeStore.ErrNotFound):
			return err
		}
		actor, err := s.actor(ctx)
		if err != nil {
			return fmt.Errorf("resolve knowledge actor: %w", err)
		}
		if err := actor.Validate(); err != nil {
			return err
		}
		candidate.ID = knowledge.EvidenceID(s.newID())
		candidate.Actor = actor
		candidate.CreatedAt = s.now().UTC().Round(0)
		if err := candidate.Validate(); err != nil {
			return err
		}
		if err := tx.PutEvidence(ctx, candidate); err != nil {
			return err
		}
		result.Evidence = candidate
		result.Created = true
		return nil
	})
	if err != nil {
		return CreateEvidenceResult{}, fmt.Errorf("create knowledge evidence: %w", err)
	}
	return result, nil
}

func (s *Service) Evidence(ctx context.Context, evidenceID knowledge.EvidenceID) (knowledge.Evidence, error) {
	if err := ctx.Err(); err != nil {
		return knowledge.Evidence{}, err
	}
	var result knowledge.Evidence
	if err := s.store.View(ctx, func(tx knowledgeStore.ReadTx) error {
		var err error
		result, err = tx.Evidence(ctx, evidenceID)
		return err
	}); err != nil {
		return knowledge.Evidence{}, fmt.Errorf("get knowledge evidence: %w", err)
	}
	return result, nil
}

func evidenceClassificationInput(value knowledge.Evidence) knowledge.ClassificationInput {
	return knowledge.ClassificationInput{Fields: []knowledge.ClassificationField{
		{Name: "source.id", Value: value.Source.ID}, {Name: "source.uri", Value: value.Source.URI},
		{Name: "source.title", Value: value.Source.Title}, {Name: "source.excerpt", Value: value.Source.Excerpt},
	}}
}
