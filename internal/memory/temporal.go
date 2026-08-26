package memory

import (
	"fmt"
	"time"
)

// TemporalStatus is computed at a caller-supplied instant and is never persisted.
// Validity intervals are half-open: ValidFrom is inclusive and ValidUntil is exclusive.
type TemporalStatus struct {
	AsOf        time.Time `json:"as_of"`
	Valid       bool      `json:"valid"`
	NotYetValid bool      `json:"not_yet_valid"`
	Expired     bool      `json:"expired"`
	ReviewDue   bool      `json:"review_due"`
	Stale       bool      `json:"stale"`
}

func EntryTemporalStatusAt(entry Entry, asOf time.Time) (TemporalStatus, error) {
	asOf, err := normalizeAsOf(asOf)
	if err != nil {
		return TemporalStatus{}, err
	}
	notYetValid := !entry.ValidFrom.IsZero() && asOf.Before(entry.ValidFrom)
	expired := !entry.ValidUntil.IsZero() && !asOf.Before(entry.ValidUntil)
	reviewDue := reviewDueAt(entry.ReviewAfter, asOf)
	return TemporalStatus{
		AsOf: asOf, Valid: !notYetValid && !expired, NotYetValid: notYetValid,
		Expired: expired, ReviewDue: reviewDue, Stale: expired || reviewDue,
	}, nil
}

func ChunkTemporalStatusAt(chunk Chunk, asOf time.Time) (TemporalStatus, error) {
	asOf, err := normalizeAsOf(asOf)
	if err != nil {
		return TemporalStatus{}, err
	}
	reviewDue := reviewDueAt(chunk.ReviewAfter, asOf)
	return TemporalStatus{AsOf: asOf, Valid: true, ReviewDue: reviewDue, Stale: reviewDue}, nil
}

func normalizeAsOf(value time.Time) (time.Time, error) {
	if value.IsZero() {
		return time.Time{}, fmt.Errorf("%w: temporal as-of time is required", ErrInvalidRecord)
	}
	return value.UTC().Round(0), nil
}

func reviewDueAt(reviewAfter, asOf time.Time) bool {
	return !reviewAfter.IsZero() && !asOf.Before(reviewAfter)
}
