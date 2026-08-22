// Package curation schedules provider-independent extraction of durable-knowledge
// candidates from completed chat turns.
package curation

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
	"github.com/lkarlslund/koder/internal/knowledge/observability"
)

var (
	ErrUnavailable = errors.New("knowledge curation queue is unavailable")
	ErrNotFound    = errors.New("knowledge curation record not found")
)

var safeErrorCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// ExtractionResult is intentionally provider-neutral. Candidate payloads belong to the
// extractor's durable candidate repository; the queue records only the safe count.
type ExtractionResult struct {
	CandidateCount uint32
}

// Extractor inspects one sealed turn selected by cheap candidate signals.
type Extractor interface {
	Extract(context.Context, knowledge.CurationRecord) (ExtractionResult, error)
}

// Store owns atomic queue claiming and terminal state transitions.
type Store interface {
	Submit(context.Context, knowledge.CurationRecord) (knowledge.CurationRecord, bool, error)
	Claim(context.Context, time.Time) (knowledge.CurationRecord, bool, error)
	Complete(context.Context, knowledge.CurationRecordID, ExtractionResult, string, time.Time) (knowledge.CurationRecord, error)
	Get(context.Context, knowledge.CurationRecordID) (knowledge.CurationRecord, error)
}

type Config struct {
	Store         Store
	Extractor     Extractor
	NewID         func() string
	Now           func() time.Time
	SafeErrorCode func(error) string
	Operations    *observability.Recorder
}

type Queue struct {
	store         Store
	extractor     Extractor
	newID         func() string
	now           func() time.Time
	safeErrorCode func(error) string
	operations    *observability.Recorder
}

type SubmitRequest struct {
	Source  knowledge.CompletedTurnRef
	Signals []knowledge.CurationSignal
}

type SubmitResult struct {
	OperationID string
	Record      knowledge.CurationRecord
	Created     bool
}

func New(config Config) (*Queue, error) {
	if config.Store == nil || config.Extractor == nil || config.NewID == nil {
		return nil, fmt.Errorf("%w: store, extractor, and ID source are required", ErrUnavailable)
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.SafeErrorCode == nil {
		config.SafeErrorCode = func(error) string { return "extraction_failed" }
	}
	if config.Operations == nil {
		config.Operations = observability.NewRecorder(observability.Config{})
	}
	return &Queue{
		store: config.Store, extractor: config.Extractor, newID: config.NewID,
		now: config.Now, safeErrorCode: config.SafeErrorCode, operations: config.Operations,
	}, nil
}

// Submit idempotently schedules one completed turn. Store implementations deduplicate by
// the complete turn identity, not by generated queue ID.
func (q *Queue) Submit(ctx context.Context, request SubmitRequest) (result SubmitResult, err error) {
	operation := q.operations.Start(observability.OperationCurationSubmit, "")
	defer func() {
		result.OperationID = operation.ID()
		outputCount := uint64(0)
		if result.Created {
			outputCount = 1
		}
		operation.Finish(curationOperationOutcome(err, !result.Created), uint64(len(request.Signals)), outputCount)
	}()
	if err := ctx.Err(); err != nil {
		return SubmitResult{}, err
	}
	now := q.now().UTC().Round(0)
	record := knowledge.CurationRecord{
		ID: knowledge.CurationRecordID(q.newID()), Source: request.Source,
		Signals: cloneSignals(request.Signals), State: knowledge.CurationStateQueued,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := record.Validate(); err != nil {
		return SubmitResult{}, err
	}
	stored, created, err := q.store.Submit(ctx, record)
	if err != nil {
		return SubmitResult{}, fmt.Errorf("submit curation record: %w", err)
	}
	return SubmitResult{OperationID: operation.ID(), Record: stored, Created: created}, nil
}

// ProcessNext claims and extracts at most one record. processed is false when the queue is
// empty. Extractor failures are persisted using a safe class before the original error is
// returned to the runner.
func (q *Queue) ProcessNext(ctx context.Context) (processed bool, err error) {
	operation := q.operations.Start(observability.OperationCurationProcess, "")
	var outputCount uint64
	defer func() {
		operation.Finish(curationOperationOutcome(err, !processed), 1, outputCount)
	}()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	record, claimed, err := q.store.Claim(ctx, q.now().UTC().Round(0))
	if err != nil {
		return false, fmt.Errorf("claim curation record: %w", err)
	}
	if !claimed {
		return false, nil
	}
	result, extractionErr := q.extractor.Extract(ctx, record)
	outputCount = uint64(result.CandidateCount)
	errorCode := ""
	if extractionErr != nil {
		errorCode = normalizeErrorCode(q.safeErrorCode(extractionErr))
		result = ExtractionResult{}
	}
	if _, completeErr := q.store.Complete(context.WithoutCancel(ctx), record.ID, result, errorCode, q.now().UTC().Round(0)); completeErr != nil {
		if extractionErr != nil {
			return true, errors.Join(extractionErr, fmt.Errorf("complete curation record: %w", completeErr))
		}
		return true, fmt.Errorf("complete curation record: %w", completeErr)
	}
	if extractionErr != nil {
		return true, fmt.Errorf("extract curation candidates: %w", extractionErr)
	}
	return true, nil
}

func curationOperationOutcome(err error, empty bool) observability.Outcome {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return observability.OutcomeCanceled
	case err != nil:
		return observability.OutcomeFailed
	case empty:
		return observability.OutcomeEmpty
	default:
		return observability.OutcomeSucceeded
	}
}

func (q *Queue) Get(ctx context.Context, id knowledge.CurationRecordID) (knowledge.CurationRecord, error) {
	return q.store.Get(ctx, id)
}

func normalizeErrorCode(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if !safeErrorCodePattern.MatchString(value) {
		return "extraction_failed"
	}
	return value
}

func cloneSignals(values []knowledge.CurationSignal) []knowledge.CurationSignal {
	cloned := make([]knowledge.CurationSignal, len(values))
	for index, signal := range values {
		cloned[index] = signal
		cloned[index].SourceItemIDs = append([]string(nil), signal.SourceItemIDs...)
	}
	return cloned
}
