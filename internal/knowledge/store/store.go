// Package store defines the persistence contract consumed by the Knowledge service.
// Backend-specific types and errors must not cross this boundary.
package store

import (
	"context"
	"errors"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
)

var (
	// ErrNotFound indicates that the requested canonical object does not exist.
	ErrNotFound = errors.New("knowledge object not found")
	// ErrConflict indicates that a create/update/delete precondition no longer matches.
	ErrConflict = errors.New("knowledge object revision conflict")
	// ErrClosed indicates that the store or transaction can no longer be used.
	ErrClosed = errors.New("knowledge store closed")
	// ErrReadOnly indicates that the backend cannot currently accept writes.
	ErrReadOnly = errors.New("knowledge store is read-only")
	// ErrUnsupported indicates that an optional backend operation is not implemented.
	ErrUnsupported = errors.New("knowledge store operation unsupported")
	// ErrIncompatible indicates that durable data uses an unsupported schema or encoding.
	ErrIncompatible = errors.New("knowledge store format incompatible")
	// ErrInvalidCursor indicates that a cursor is malformed or belongs to a different query.
	ErrInvalidCursor = errors.New("invalid knowledge cursor")
	// ErrStaleCursor indicates that the cursor's derived index generation was retired.
	ErrStaleCursor = errors.New("stale knowledge cursor")
)

// Health is backend-neutral operational state. LastError contains a sanitized summary and
// must never contain canonical knowledge content.
type Health struct {
	Backend         string `json:"backend"`
	Path            string `json:"path,omitempty"`
	Open            bool   `json:"open"`
	ReadOnly        bool   `json:"read_only"`
	SchemaVersion   uint32 `json:"schema_version"`
	IndexGeneration uint64 `json:"index_generation"`
	LastError       string `json:"last_error,omitempty"`
}

// ReadTx provides a consistent canonical-record snapshot. A transaction is valid only
// during its Store.View or Store.Update callback and must not escape that callback.
type ReadTx interface {
	Chunk(context.Context, knowledge.ChunkID) (knowledge.Chunk, error)
	ChunkDeletionBlockers(context.Context, knowledge.ChunkID) (ChunkDeletionBlockers, error)
	Entry(context.Context, knowledge.EntryID) (knowledge.Entry, error)
	EntryDeletionBlockers(context.Context, knowledge.EntryID) (EntryDeletionBlockers, error)
	Link(context.Context, knowledge.LinkID) (knowledge.Link, error)
	Evidence(context.Context, knowledge.EvidenceID) (knowledge.Evidence, error)
	EvidenceBySource(context.Context, string, string) (knowledge.Evidence, error)
}

// WriteTx atomically mutates canonical records. For Put methods, expectedRevision zero
// means the object must not exist and the supplied revision must be 1. A positive expected
// revision means the object must exist at that revision and the supplied revision must be
// exactly expectedRevision+1. Delete methods require a positive matching revision.
//
// Backends validate these preconditions when committing, not only when a transaction first
// reads an object, so concurrent writers cannot both succeed.
type WriteTx interface {
	ReadTx
	PutChunk(context.Context, knowledge.Chunk, uint64) error
	TouchChunk(context.Context, knowledge.ChunkID, time.Time) error
	DeleteChunk(context.Context, knowledge.ChunkID, uint64) error
	PutEntry(context.Context, knowledge.Entry, uint64) error
	DeleteEntry(context.Context, knowledge.EntryID, uint64) error
	PutLink(context.Context, knowledge.Link, uint64) error
	DeleteLink(context.Context, knowledge.LinkID, uint64) error
	PutEvidence(context.Context, knowledge.Evidence) error
	DeleteEvidence(context.Context, knowledge.EvidenceID) error
}

// Store owns one independent knowledge database lifecycle.
//
// View and Update must honor context cancellation. A callback error aborts the operation;
// an Update callback error rolls back every mutation. Nested transactions are unsupported.
// Implementations must serialize or detect conflicting writers and provide read-your-writes
// behavior within an Update callback.
type Store interface {
	View(context.Context, func(ReadTx) error) error
	Update(context.Context, func(WriteTx) error) error
	ListChunks(context.Context, ChunkListRequest) (ChunkPage, error)
	ListEntries(context.Context, EntryListRequest) (EntryPage, error)
	Health(context.Context) (Health, error)
	Checkpoint(context.Context, string) error
	Close() error
}
