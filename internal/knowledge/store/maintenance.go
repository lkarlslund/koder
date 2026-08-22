package store

import (
	"context"
	"fmt"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
)

// RecordKind identifies one canonical persistence family. It is separate from
// knowledge.ObjectKind because immutable evidence is not a graph endpoint.
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
	Kind     RecordKind
	Chunk    *knowledge.Chunk
	Entry    *knowledge.Entry
	Link     *knowledge.Link
	Evidence *knowledge.Evidence
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
	Chunks   uint64
	Entries  uint64
	Links    uint64
	Evidence uint64
	Total    uint64
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
	Running          bool
	ActiveGeneration uint64
	TargetGeneration uint64
	Scanned          ScanStats
	StartedAt        time.Time
	CompletedAt      time.Time
	LastError        string
}

// MaintenanceStore is implemented by backends that support consistent canonical scans
// and rebuilding private derived indexes from canonical truth.
type MaintenanceStore interface {
	Store
	ScanCanonical(context.Context, func(CanonicalRecord) error) (ScanStats, error)
	RebuildIndexes(context.Context) error
	IndexRebuildStatus(context.Context) (IndexRebuildStatus, error)
}
