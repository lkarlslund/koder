package store

import (
	"context"
	"fmt"
	"time"

	"github.com/lkarlslund/koder/internal/memory"
)

// RecordKind identifies one canonical persistence family. It is separate from
// memory.ObjectKind because immutable evidence is not a graph endpoint.
type RecordKind string

const (
	RecordKindChunk    RecordKind = "chunk"
	RecordKindEntry    RecordKind = "entry"
	RecordKindLink     RecordKind = "link"
	RecordKindEvidence RecordKind = "evidence"
)

// CanonicalRecord is one validated record delivered by a consistent store scan. Exactly
// one typed pointer is populated and matches Kind.
type CanonicalRecord struct {
	Kind     RecordKind       `json:"kind"`
	Chunk    *memory.Chunk    `json:"chunk,omitempty"`
	Entry    *memory.Entry    `json:"entry,omitempty"`
	Link     *memory.Link     `json:"link,omitempty"`
	Evidence *memory.Evidence `json:"evidence,omitempty"`
}

func (r CanonicalRecord) ID() string {
	switch r.Kind {
	case RecordKindChunk:
		if r.Chunk != nil {
			return string(r.Chunk.ID)
		}
	case RecordKindEntry:
		if r.Entry != nil {
			return string(r.Entry.ID)
		}
	case RecordKindLink:
		if r.Link != nil {
			return string(r.Link.ID)
		}
	case RecordKindEvidence:
		if r.Evidence != nil {
			return string(r.Evidence.ID)
		}
	}
	return ""
}

// Validate checks the discriminated-union shape and the canonical domain record.
func (r CanonicalRecord) Validate() error {
	populated := 0
	for _, present := range []bool{r.Chunk != nil, r.Entry != nil, r.Link != nil, r.Evidence != nil} {
		if present {
			populated++
		}
	}
	if populated != 1 || r.ID() == "" {
		return fmt.Errorf("invalid canonical scan record")
	}
	switch r.Kind {
	case RecordKindChunk:
		return r.Chunk.Validate()
	case RecordKindEntry:
		return r.Entry.Validate()
	case RecordKindLink:
		return r.Link.Validate()
	case RecordKindEvidence:
		return r.Evidence.Validate()
	default:
		return fmt.Errorf("invalid canonical scan record kind %q", r.Kind)
	}
}

type ScanStats struct {
	Chunks   uint64 `json:"chunks"`
	Entries  uint64 `json:"entries"`
	Links    uint64 `json:"links"`
	Evidence uint64 `json:"evidence"`
	Total    uint64 `json:"total"`
}

func (s *ScanStats) Add(kind RecordKind) {
	s.Total++
	switch kind {
	case RecordKindChunk:
		s.Chunks++
	case RecordKindEntry:
		s.Entries++
	case RecordKindLink:
		s.Links++
	case RecordKindEvidence:
		s.Evidence++
	}
}

type IndexRebuildStatus struct {
	Running          bool      `json:"running"`
	Canceled         bool      `json:"canceled,omitempty"`
	ActiveGeneration uint64    `json:"active_generation"`
	TargetGeneration uint64    `json:"target_generation"`
	Scanned          ScanStats `json:"scanned"`
	StartedAt        time.Time `json:"started_at,omitzero"`
	CompletedAt      time.Time `json:"completed_at,omitzero"`
	LastError        string    `json:"last_error,omitempty"`
}

// CanonicalScanStore is the minimum backend-neutral source contract for integrity and
// migration snapshots.
type CanonicalScanStore interface {
	Store
	ScanCanonical(context.Context, func(CanonicalRecord) error) (ScanStats, error)
}

// MaintenanceStore is implemented by backends that can also rebuild private derived
// indexes from canonical truth.
type MaintenanceStore interface {
	CanonicalScanStore
	RebuildIndexes(context.Context) error
	IndexRebuildStatus(context.Context) (IndexRebuildStatus, error)
}
