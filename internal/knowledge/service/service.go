// Package service owns Knowledge use cases, policy, and canonical transactions.
package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

var (
	ErrClassificationRejected = errors.New("knowledge write rejected by classification")
	ErrReviewRequired         = errors.New("knowledge write requires classification review")
)

type ActorSource func(context.Context) (knowledge.Actor, error)
type IDSource func() string

type Config struct {
	Store      knowledgeStore.Store
	Classifier knowledge.Classifier
	Actor      ActorSource
	Now        func() time.Time
	NewID      IDSource
}

type Service struct {
	store      knowledgeStore.Store
	classifier knowledge.Classifier
	actor      ActorSource
	now        func() time.Time
	newID      IDSource
}

func New(cfg Config) (*Service, error) {
	if cfg.Store == nil || cfg.Actor == nil {
		return nil, fmt.Errorf("knowledge service store and actor source are required")
	}
	if cfg.Classifier == nil {
		cfg.Classifier = knowledge.RuleClassifier{}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.NewID == nil {
		cfg.NewID = id.New
	}
	return &Service{
		store: cfg.Store, classifier: cfg.Classifier, actor: cfg.Actor,
		now: cfg.Now, newID: cfg.NewID,
	}, nil
}

type CreateChunkRequest struct {
	Chunk          knowledge.Chunk
	ReviewApproved bool
}

type CreateChunkResult struct {
	Chunk          knowledge.Chunk
	Classification knowledge.ClassificationResult
}

// ClassificationError contains locations and rule names but never copies matched values.
type ClassificationError struct {
	Result knowledge.ClassificationResult
}

func (e *ClassificationError) Error() string {
	switch e.Result.Decision {
	case knowledge.ClassificationDecisionReject:
		return ErrClassificationRejected.Error()
	case knowledge.ClassificationDecisionReview:
		return ErrReviewRequired.Error()
	default:
		return "invalid knowledge classification decision"
	}
}

func (e *ClassificationError) Unwrap() error {
	if e.Result.Decision == knowledge.ClassificationDecisionReject {
		return ErrClassificationRejected
	}
	return ErrReviewRequired
}

func (s *Service) CreateChunk(ctx context.Context, request CreateChunkRequest) (CreateChunkResult, error) {
	if err := ctx.Err(); err != nil {
		return CreateChunkResult{}, err
	}
	candidate := request.Chunk
	if hasServerOwnedChunkFields(candidate) {
		return CreateChunkResult{}, fmt.Errorf("%w: create chunk contains server-owned identity, revision, timestamp, or count fields", knowledge.ErrInvalidRecord)
	}
	if isPersonalMeScope(candidate) {
		return CreateChunkResult{}, fmt.Errorf("%w: personal/me is created and managed by Koder", ErrProtectedChunk)
	}
	classification, err := s.classifier.Classify(ctx, chunkClassificationInput(candidate))
	if err != nil {
		return CreateChunkResult{}, fmt.Errorf("classify chunk candidate: %w", err)
	}
	switch classification.Decision {
	case knowledge.ClassificationDecisionAllow:
	case knowledge.ClassificationDecisionReview:
		if !request.ReviewApproved {
			return CreateChunkResult{Classification: classification}, &ClassificationError{Result: classification}
		}
	case knowledge.ClassificationDecisionReject:
		return CreateChunkResult{Classification: classification}, &ClassificationError{Result: classification}
	default:
		return CreateChunkResult{}, fmt.Errorf("classifier returned invalid decision %q", classification.Decision)
	}

	candidate, err = knowledge.NormalizeChunk(candidate)
	if err != nil {
		return CreateChunkResult{}, err
	}
	if candidate.State == knowledge.ChunkStateUnspecified {
		candidate.State = knowledge.ChunkStateActive
	}
	if candidate.State == knowledge.ChunkStateArchived {
		return CreateChunkResult{}, fmt.Errorf("%w: a new chunk cannot start archived", knowledge.ErrInvalidRecord)
	}
	if candidate.Visibility == knowledge.VisibilityUnspecified {
		candidate.Visibility = knowledge.VisibilityPrivate
	}
	actor, err := s.actor(ctx)
	if err != nil {
		return CreateChunkResult{}, fmt.Errorf("resolve knowledge actor: %w", err)
	}
	if err := actor.Validate(); err != nil {
		return CreateChunkResult{}, err
	}
	now := s.now().UTC().Round(0)
	candidate.ID = knowledge.ChunkID(s.newID())
	candidate.SchemaVersion = 1
	candidate.CreatedAt = now
	candidate.UpdatedAt = now
	candidate.Revision = knowledge.Revision{
		Number: 1, ID: knowledge.RevisionID(s.newID()), Actor: actor, CreatedAt: now,
	}
	if err := candidate.Validate(); err != nil {
		return CreateChunkResult{}, err
	}
	if err := s.store.Update(ctx, func(tx knowledgeStore.WriteTx) error {
		return tx.PutChunk(ctx, candidate, 0)
	}); err != nil {
		return CreateChunkResult{}, fmt.Errorf("create knowledge chunk: %w", err)
	}
	return CreateChunkResult{Chunk: candidate, Classification: classification}, nil
}

func (s *Service) Chunk(ctx context.Context, chunkID knowledge.ChunkID) (knowledge.Chunk, error) {
	if err := ctx.Err(); err != nil {
		return knowledge.Chunk{}, err
	}
	var result knowledge.Chunk
	if err := s.store.View(ctx, func(tx knowledgeStore.ReadTx) error {
		var err error
		result, err = tx.Chunk(ctx, chunkID)
		return err
	}); err != nil {
		return knowledge.Chunk{}, fmt.Errorf("get knowledge chunk: %w", err)
	}
	return result, nil
}

func hasServerOwnedChunkFields(value knowledge.Chunk) bool {
	return value.ID != "" || value.SchemaVersion != 0 || value.Revision != (knowledge.Revision{}) ||
		!value.CreatedAt.IsZero() || !value.UpdatedAt.IsZero() || !value.LastUsedAt.IsZero() ||
		value.Counts != (knowledge.ChunkCounts{})
}

func chunkClassificationInput(value knowledge.Chunk) knowledge.ClassificationInput {
	fields := []knowledge.ClassificationField{
		{Name: "title", Value: value.Title},
		{Name: "description", Value: value.Description},
		{Name: "scope.selector", Value: value.Scope.Selector},
		{Name: "domain", Value: value.Domain},
		{Name: "publisher.id", Value: value.Publisher.ID},
		{Name: "publisher.name", Value: value.Publisher.Name},
		{Name: "license", Value: value.License},
		{Name: "source_policy", Value: value.SourcePolicy},
		{Name: "min_koder_version", Value: value.MinKoderVersion},
	}
	for index, alias := range value.Aliases {
		fields = append(fields, knowledge.ClassificationField{Name: fmt.Sprintf("aliases[%d]", index), Value: alias})
	}
	for index, tag := range value.Tags {
		fields = append(fields, knowledge.ClassificationField{Name: fmt.Sprintf("tags[%d]", index), Value: tag})
	}
	for index, principal := range value.SharedWith {
		fields = append(fields, knowledge.ClassificationField{Name: fmt.Sprintf("shared_with[%d].id", index), Value: principal.ID})
	}
	return knowledge.ClassificationInput{Fields: fields, Risk: value.Risk}
}
