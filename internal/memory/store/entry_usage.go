package store

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/memory"
)

const maxEntryUsageIDs = 1000

type EntryOutcome string

const (
	EntryOutcomeNone    EntryOutcome = "none"
	EntryOutcomeSuccess EntryOutcome = "success"
	EntryOutcomeFailure EntryOutcome = "failure"
)

type EntryUsage struct {
	EntryID            memory.EntryID `json:"entry_id"`
	LastUsedAt         time.Time      `json:"last_used_at"`
	ReuseCount         uint64         `json:"reuse_count"`
	SuccessfulOutcomes uint64         `json:"successful_outcomes"`
	FailedOutcomes     uint64         `json:"failed_outcomes"`
}

type EntryUsageEvent struct {
	EntryID memory.EntryID `json:"entry_id"`
	EventID string         `json:"event_id"`
	UsedAt  time.Time      `json:"used_at"`
	Outcome EntryOutcome   `json:"outcome"`
}

func (e EntryUsageEvent) Validate() error {
	if e.EntryID == "" || e.EventID == "" || len(e.EventID) > 128 || strings.TrimSpace(e.EventID) != e.EventID {
		return fmt.Errorf("invalid entry usage identity")
	}
	_, offset := e.UsedAt.Zone()
	if e.UsedAt.IsZero() || offset != 0 {
		return fmt.Errorf("entry usage time must be non-zero UTC")
	}
	switch e.Outcome {
	case EntryOutcomeNone, EntryOutcomeSuccess, EntryOutcomeFailure:
		return nil
	default:
		return fmt.Errorf("invalid entry usage outcome %q", e.Outcome)
	}
}

// UsageStore persists idempotent derived retrieval/outcome signals independently of
// canonical content revisions.
type UsageStore interface {
	Store
	EntryUsage(context.Context, []memory.EntryID) (map[memory.EntryID]EntryUsage, error)
	RecordEntryUsage(context.Context, EntryUsageEvent) (EntryUsage, bool, error)
}

func NormalizeEntryUsageIDs(ids []memory.EntryID) ([]memory.EntryID, error) {
	ids = slices.Clone(ids)
	if len(ids) > maxEntryUsageIDs {
		return nil, fmt.Errorf("entry usage lookup exceeds %d IDs", maxEntryUsageIDs)
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	for _, id := range ids {
		if id == "" {
			return nil, fmt.Errorf("entry usage lookup contains an empty ID")
		}
	}
	return ids, nil
}

func ApplyEntryUsageEvent(current EntryUsage, event EntryUsageEvent) EntryUsage {
	if current.EntryID == "" {
		current.EntryID = event.EntryID
	}
	if event.UsedAt.After(current.LastUsedAt) {
		current.LastUsedAt = event.UsedAt
	}
	current.ReuseCount = saturatingIncrement(current.ReuseCount)
	switch event.Outcome {
	case EntryOutcomeSuccess:
		current.SuccessfulOutcomes = saturatingIncrement(current.SuccessfulOutcomes)
	case EntryOutcomeFailure:
		current.FailedOutcomes = saturatingIncrement(current.FailedOutcomes)
	}
	return current
}

func saturatingIncrement(value uint64) uint64 {
	if value == ^uint64(0) {
		return value
	}
	return value + 1
}
