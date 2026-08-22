// Package service owns Knowledge use cases, policy, and canonical transactions.
package service

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/knowledge"
	"github.com/lkarlslund/koder/internal/knowledge/kpackage"
	"github.com/lkarlslund/koder/internal/knowledge/observability"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

var (
	ErrClassificationRejected = errors.New("knowledge write rejected by classification")
	ErrReviewRequired         = errors.New("knowledge write requires classification review")
)

type ActorSource func(context.Context) (knowledge.Actor, error)
type IDSource func() string

type Config struct {
	Store             knowledgeStore.Store
	Classifier        knowledge.Classifier
	ChunkPolicy       ChunkPolicy
	ToolPolicy        ToolOfferPolicy
	Actor             ActorSource
	Now               func() time.Time
	NewID             IDSource
	RankSignals       RankingSignalSource
	Semantic          SemanticIndexProvider
	ScoreBlender      SearchScoreBlender
	Operational       OperationalPolicy
	ImportStageTTL    time.Duration
	ImportValidation  kpackage.ValidationOptions
	PublisherRegistry *PublisherRegistry
	Operations        *observability.Recorder
}

type Service struct {
	store             knowledgeStore.Store
	classifier        knowledge.Classifier
	chunkPolicy       ChunkPolicy
	toolPolicy        ToolOfferPolicy
	actor             ActorSource
	now               func() time.Time
	newID             IDSource
	rankSignals       RankingSignalSource
	semantic          SemanticIndexProvider
	scoreBlender      SearchScoreBlender
	mutationMu        sync.Mutex
	mutationStreamID  string
	mutationSequence  uint64
	mutationNextSub   uint64
	mutationSubs      map[uint64]chan MutationEvent
	operational       OperationalPolicy
	operationalMu     sync.Mutex
	rebuildRunning    bool
	rebuildStartedAt  time.Time
	rebuildCancel     context.CancelFunc
	operationsCtx     context.Context
	operationsCancel  context.CancelFunc
	operationsClosed  bool
	operationsWG      sync.WaitGroup
	importMu          sync.Mutex
	importStages      map[string]*stagedImport
	importStageTTL    time.Duration
	importValidation  kpackage.ValidationOptions
	publishers        *PublisherRegistry
	operationRecorder *observability.Recorder
}

func New(cfg Config) (*Service, error) {
	if cfg.Store == nil || cfg.Actor == nil {
		return nil, fmt.Errorf("knowledge service store and actor source are required")
	}
	if cfg.Classifier == nil {
		cfg.Classifier = knowledge.RuleClassifier{}
	}
	if cfg.ChunkPolicy == nil {
		cfg.ChunkPolicy = AllowAllChunkPolicy{}
	}
	if cfg.ToolPolicy == nil {
		cfg.ToolPolicy = AllowAllToolOfferPolicy{}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.NewID == nil {
		cfg.NewID = id.New
	}
	if cfg.RankSignals == nil {
		if usageStore, ok := cfg.Store.(knowledgeStore.UsageStore); ok {
			cfg.RankSignals = usageRankingSignalSource{store: usageStore}
		} else {
			cfg.RankSignals = NoRankingSignals{}
		}
	}
	if cfg.Operational == nil {
		cfg.Operational = AllowAllOperationalPolicy{}
	}
	if cfg.ImportStageTTL == 0 {
		cfg.ImportStageTTL = 15 * time.Minute
	}
	if cfg.Operations == nil {
		cfg.Operations = observability.NewRecorder(observability.Config{})
	}
	if cfg.ImportStageTTL < 0 {
		return nil, fmt.Errorf("knowledge import stage TTL must be positive")
	}
	cfg.ImportValidation = normalizeImportValidationOptions(cfg.ImportValidation)
	if cfg.PublisherRegistry != nil {
		for keyID, key := range cfg.PublisherRegistry.VerificationKeys() {
			if existing, exists := cfg.ImportValidation.VerificationKeys[keyID]; exists && !slices.Equal(existing, key) {
				return nil, fmt.Errorf("knowledge publisher registry key %q conflicts with import validation", keyID)
			}
			if cfg.ImportValidation.VerificationKeys == nil {
				cfg.ImportValidation.VerificationKeys = make(map[string]ed25519.PublicKey)
			}
			cfg.ImportValidation.VerificationKeys[keyID] = key
		}
	}
	operationsCtx, operationsCancel := context.WithCancel(context.Background())
	return &Service{
		store: cfg.Store, classifier: cfg.Classifier, chunkPolicy: cfg.ChunkPolicy, toolPolicy: cfg.ToolPolicy, actor: cfg.Actor,
		now: cfg.Now, newID: cfg.NewID, rankSignals: cfg.RankSignals,
		semantic: cfg.Semantic, scoreBlender: cfg.ScoreBlender,
		mutationStreamID: id.New(), mutationSubs: make(map[uint64]chan MutationEvent),
		operational:   cfg.Operational,
		operationsCtx: operationsCtx, operationsCancel: operationsCancel,
		importStages: make(map[string]*stagedImport), importStageTTL: cfg.ImportStageTTL, importValidation: cfg.ImportValidation,
		publishers:        cfg.PublisherRegistry,
		operationRecorder: cfg.Operations,
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
	if err := requireClassificationApproval(classification, request.ReviewApproved); err != nil {
		return CreateChunkResult{Classification: classification}, err
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
	if err := s.authorizeChunk(ctx, actor, ChunkPolicyCreate, candidate); err != nil {
		return CreateChunkResult{}, err
	}
	if err := s.store.Update(ctx, func(tx knowledgeStore.WriteTx) error {
		return tx.PutChunk(ctx, candidate, 0)
	}); err != nil {
		return CreateChunkResult{}, fmt.Errorf("create knowledge chunk: %w", err)
	}
	s.publishMutation(ctx, chunkMutation(MutationCreated, candidate))
	return CreateChunkResult{Chunk: candidate, Classification: classification}, nil
}

func (s *Service) Chunk(ctx context.Context, chunkID knowledge.ChunkID) (knowledge.Chunk, error) {
	record, err := s.Get(ctx, knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: string(chunkID)})
	if err != nil {
		return knowledge.Chunk{}, err
	}
	if record.Chunk == nil {
		return knowledge.Chunk{}, fmt.Errorf("%w: chunk projection is unavailable", knowledge.ErrInvalidRecord)
	}
	return *record.Chunk, nil
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
