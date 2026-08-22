package service

import (
	"context"
	"errors"

	"github.com/lkarlslund/koder/internal/knowledge/observability"
)

func operationOutcome(err error, empty bool) observability.Outcome {
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

// OperationRecorder returns the shared privacy-safe recorder used by service and
// background curation operations. Callers must not record content-derived labels.
func (s *Service) OperationRecorder() *observability.Recorder {
	if s == nil {
		return nil
	}
	return s.operationRecorder
}

// OperationMetrics returns a concurrency-safe snapshot containing only operation
// classes, correlation IDs, timings, and aggregate counts.
func (s *Service) OperationMetrics() observability.Snapshot {
	if s == nil {
		return observability.Snapshot{Operations: []observability.Metric{}, Recent: []observability.Observation{}}
	}
	return s.operationRecorder.Snapshot()
}
