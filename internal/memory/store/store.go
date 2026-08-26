// Package store defines the persistence contract consumed by the Memory service.
// Backend-specific types and errors must not cross this boundary.
package store

import (
	"context"
	"errors"
	"time"

	"github.com/lkarlslund/koder/internal/memory"
)

var (
	// ErrNotFound indicates that the requested canonical object does not exist.
	ErrNotFound = errors.New("memory object not found")
	// ErrConflict indicates that a create/update/delete precondition no longer matches.
	ErrConflict = errors.New("memory object revision conflict")
	// ErrClosed indicates that the store or transaction can no longer be used.
	ErrClosed = errors.New("memory store closed")
	// ErrReadOnly indicates that the backend cannot currently accept writes.
	ErrReadOnly = errors.New("memory store is read-only")
	// ErrUnsupported indicates that an optional backend operation is not implemented.
	ErrUnsupported = errors.New("memory store operation unsupported")
	// ErrIncompatible indicates that durable data uses an unsupported schema or encoding.
	ErrIncompatible = errors.New("memory store format incompatible")
	// ErrInvalidCursor indicates that a cursor is malformed or belongs to a different query.
	ErrInvalidCursor = errors.New("invalid memory cursor")
	// ErrStaleCursor indicates that the cursor's derived index generation was retired.
	ErrStaleCursor = errors.New("stale memory cursor")
)

// Health is backend-neutral operational state. LastError contains a sanitized summary and
// must never contain canonical memory content.
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
	Empty(context.Context) (bool, error)
	Chunk(context.Context, memory.ChunkID) (memory.Chunk, error)
	ChunkDeletionBlockers(context.Context, memory.ChunkID) (ChunkDeletionBlockers, error)
	Entry(context.Context, memory.EntryID) (memory.Entry, error)
	EntryDeletionBlockers(context.Context, memory.EntryID) (EntryDeletionBlockers, error)
	Link(context.Context, memory.LinkID) (memory.Link, error)
	EquivalentLink(context.Context, memory.Link) (memory.Link, error)
	Evidence(context.Context, memory.EvidenceID) (memory.Evidence, error)
	EvidenceBySource(context.Context, string, string) (memory.Evidence, error)
	Asset(context.Context, memory.ChunkID, string) (PackageAsset, error)
	ListAssets(context.Context, memory.ChunkID) ([]PackageAsset, error)
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
	PutChunk(context.Context, memory.Chunk, uint64) error
	TouchChunk(context.Context, memory.ChunkID, time.Time) error
	DeleteChunk(context.Context, memory.ChunkID, uint64) error
	PutEntry(context.Context, memory.Entry, uint64) error
	DeleteEntry(context.Context, memory.EntryID, uint64) error
	PutLink(context.Context, memory.Link, uint64) error
	DeleteLink(context.Context, memory.LinkID, uint64) error
	PutEvidence(context.Context, memory.Evidence) error
	DeleteEvidence(context.Context, memory.EvidenceID) error
	PutAsset(context.Context, PackageAsset) error
	DeleteAsset(context.Context, memory.ChunkID, string) error
}

// Store owns one independent memory database lifecycle.
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
	SearchExact(context.Context, ExactSearchRequest) (ExactSearchPage, error)
	LookupLexicalPostings(context.Context, LexicalPostingRequest) (LexicalPostingPage, error)
	ListAdjacentLinks(context.Context, AdjacentLinkListRequest) (LinkPage, error)
	ListRevisions(context.Context, RevisionListRequest) (RevisionPage, error)
	Health(context.Context) (Health, error)
	Checkpoint(context.Context, string) error
	Close() error
}

// EntryScanner is an optional bulk-read capability for consumers that must
// inspect a complete filtered corpus. Unlike repeatedly paging ListEntries, a
// scanner makes one consistent pass and therefore remains linear as the corpus
// grows. The callback must not retain or mutate backend-owned storage.
type EntryScanner interface {
	ScanEntries(context.Context, EntryFilter, func(memory.Entry) error) error
}
