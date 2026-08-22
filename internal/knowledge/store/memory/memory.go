// Package memory provides a deterministic in-memory Knowledge store for service and
// backend-contract tests.
package memory

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
	revisioncheck "github.com/lkarlslund/koder/internal/knowledge/store/internal/revision"
)

// Store owns an isolated in-memory canonical record set.
type Store struct {
	mu              sync.RWMutex
	closed          bool
	data            data
	indexGeneration uint64
	rebuildStatus   knowledgeStore.IndexRebuildStatus
}

type data struct {
	chunks   map[knowledge.ChunkID]knowledge.Chunk
	entries  map[knowledge.EntryID]knowledge.Entry
	links    map[knowledge.LinkID]knowledge.Link
	evidence map[knowledge.EvidenceID]knowledge.Evidence
}

type transaction struct {
	data     *data
	active   bool
	writable bool
}

var _ knowledgeStore.Store = (*Store)(nil)
var _ knowledgeStore.WriteTx = (*transaction)(nil)

// New returns an empty, open in-memory store.
func New() *Store {
	return &Store{
		data:            newData(),
		indexGeneration: 1,
		rebuildStatus:   knowledgeStore.IndexRebuildStatus{ActiveGeneration: 1},
	}
}

func newData() data {
	return data{
		chunks:   make(map[knowledge.ChunkID]knowledge.Chunk),
		entries:  make(map[knowledge.EntryID]knowledge.Entry),
		links:    make(map[knowledge.LinkID]knowledge.Link),
		evidence: make(map[knowledge.EvidenceID]knowledge.Evidence),
	}
}

// View runs fn against one consistent read snapshot.
func (s *Store) View(ctx context.Context, fn func(knowledgeStore.ReadTx) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if fn == nil {
		return fmt.Errorf("view knowledge store: callback is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return knowledgeStore.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	tx := &transaction{data: &s.data, active: true}
	err := func() error {
		defer func() { tx.active = false }()
		return fn(tx)
	}()
	if err != nil {
		return err
	}
	return ctx.Err()
}

// Update runs fn against an isolated copy and publishes every change atomically on success.
func (s *Store) Update(ctx context.Context, fn func(knowledgeStore.WriteTx) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if fn == nil {
		return fmt.Errorf("update knowledge store: callback is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return knowledgeStore.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	working := cloneData(s.data)
	tx := &transaction{data: &working, active: true, writable: true}
	err := func() error {
		defer func() { tx.active = false }()
		return fn(tx)
	}()
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.data = working
	return nil
}

// Health reports the memory backend's lifecycle state.
func (s *Store) Health(ctx context.Context) (knowledgeStore.Health, error) {
	if err := ctx.Err(); err != nil {
		return knowledgeStore.Health{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return knowledgeStore.Health{
		Backend:         "memory",
		Open:            !s.closed,
		SchemaVersion:   1,
		IndexGeneration: s.indexGeneration,
	}, nil
}

func (s *Store) ListChunks(ctx context.Context, request knowledgeStore.ChunkListRequest) (knowledgeStore.ChunkPage, error) {
	if err := ctx.Err(); err != nil {
		return knowledgeStore.ChunkPage{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return knowledgeStore.ChunkPage{}, knowledgeStore.ErrClosed
	}
	chunks := make([]knowledge.Chunk, 0, len(s.data.chunks))
	for _, chunk := range s.data.chunks {
		chunks = append(chunks, cloneChunk(chunk))
	}
	return knowledgeStore.PaginateChunks(chunks, request, s.indexGeneration)
}

// Checkpoint is deliberately unsupported because the memory backend has no durable files.
func (s *Store) Checkpoint(ctx context.Context, _ string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return knowledgeStore.ErrClosed
	}
	return knowledgeStore.ErrUnsupported
}

// Close makes the store and any future transaction unavailable. It is idempotent.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

func (tx *transaction) check(ctx context.Context, write bool) error {
	if !tx.active || tx.data == nil {
		return knowledgeStore.ErrClosed
	}
	if write && !tx.writable {
		return knowledgeStore.ErrReadOnly
	}
	return ctx.Err()
}

func (tx *transaction) Chunk(ctx context.Context, id knowledge.ChunkID) (knowledge.Chunk, error) {
	if err := tx.check(ctx, false); err != nil {
		return knowledge.Chunk{}, err
	}
	value, ok := tx.data.chunks[id]
	if !ok {
		return knowledge.Chunk{}, fmt.Errorf("%w: chunk %s", knowledgeStore.ErrNotFound, id)
	}
	return cloneChunk(value), nil
}

func (tx *transaction) ChunkDeletionBlockers(ctx context.Context, id knowledge.ChunkID) (knowledgeStore.ChunkDeletionBlockers, error) {
	if err := tx.check(ctx, false); err != nil {
		return knowledgeStore.ChunkDeletionBlockers{}, err
	}
	chunk, exists := tx.data.chunks[id]
	if !exists {
		return knowledgeStore.ChunkDeletionBlockers{}, fmt.Errorf("%w: chunk %s", knowledgeStore.ErrNotFound, id)
	}
	blockers := knowledgeStore.ChunkDeletionBlockers{
		DependencyIDs: slices.Clone(chunk.DependencyIDs), ReportedCounts: chunk.Counts,
	}
	entryIDs := make(map[knowledge.EntryID]struct{})
	for entryID, entry := range tx.data.entries {
		if entry.ChunkID == id {
			entryIDs[entryID] = struct{}{}
			blockers.EntryIDs = append(blockers.EntryIDs, entryID)
		}
	}
	for linkID, link := range tx.data.links {
		if linkTouchesChunk(link, id, entryIDs) {
			blockers.LinkIDs = append(blockers.LinkIDs, linkID)
		}
	}
	for candidateID, candidate := range tx.data.chunks {
		if candidateID != id && slices.Contains(candidate.DependencyIDs, id) {
			blockers.DependentChunkIDs = append(blockers.DependentChunkIDs, candidateID)
		}
	}
	slices.Sort(blockers.EntryIDs)
	slices.Sort(blockers.LinkIDs)
	slices.Sort(blockers.DependencyIDs)
	slices.Sort(blockers.DependentChunkIDs)
	return blockers, nil
}

func linkTouchesChunk(link knowledge.Link, chunkID knowledge.ChunkID, entryIDs map[knowledge.EntryID]struct{}) bool {
	for _, endpoint := range []knowledge.ObjectRef{link.Source, link.Target} {
		if endpoint.Kind == knowledge.ObjectKindChunk && endpoint.ID == string(chunkID) {
			return true
		}
		if endpoint.Kind == knowledge.ObjectKindEntry {
			if _, exists := entryIDs[knowledge.EntryID(endpoint.ID)]; exists {
				return true
			}
		}
	}
	return false
}

func (tx *transaction) Entry(ctx context.Context, id knowledge.EntryID) (knowledge.Entry, error) {
	if err := tx.check(ctx, false); err != nil {
		return knowledge.Entry{}, err
	}
	value, ok := tx.data.entries[id]
	if !ok {
		return knowledge.Entry{}, fmt.Errorf("%w: entry %s", knowledgeStore.ErrNotFound, id)
	}
	return cloneEntry(value), nil
}

func (tx *transaction) Link(ctx context.Context, id knowledge.LinkID) (knowledge.Link, error) {
	if err := tx.check(ctx, false); err != nil {
		return knowledge.Link{}, err
	}
	value, ok := tx.data.links[id]
	if !ok {
		return knowledge.Link{}, fmt.Errorf("%w: link %s", knowledgeStore.ErrNotFound, id)
	}
	return cloneLink(value), nil
}

func (tx *transaction) Evidence(ctx context.Context, id knowledge.EvidenceID) (knowledge.Evidence, error) {
	if err := tx.check(ctx, false); err != nil {
		return knowledge.Evidence{}, err
	}
	value, ok := tx.data.evidence[id]
	if !ok {
		return knowledge.Evidence{}, fmt.Errorf("%w: evidence %s", knowledgeStore.ErrNotFound, id)
	}
	return value, nil
}

func (tx *transaction) PutChunk(ctx context.Context, value knowledge.Chunk, expectedRevision uint64) error {
	if err := tx.check(ctx, true); err != nil {
		return err
	}
	if err := value.Validate(); err != nil {
		return err
	}
	current, exists := tx.data.chunks[value.ID]
	if err := revisioncheck.CheckPut("chunk", string(value.ID), expectedRevision, value.Revision.Number, current.Revision.Number, exists); err != nil {
		return err
	}
	tx.data.chunks[value.ID] = cloneChunk(value)
	return nil
}

func (tx *transaction) DeleteChunk(ctx context.Context, id knowledge.ChunkID, expectedRevision uint64) error {
	if err := tx.check(ctx, true); err != nil {
		return err
	}
	current, exists := tx.data.chunks[id]
	if err := revisioncheck.CheckDelete("chunk", string(id), expectedRevision, current.Revision.Number, exists); err != nil {
		return err
	}
	delete(tx.data.chunks, id)
	return nil
}

func (tx *transaction) PutEntry(ctx context.Context, value knowledge.Entry, expectedRevision uint64) error {
	if err := tx.check(ctx, true); err != nil {
		return err
	}
	if err := value.Validate(); err != nil {
		return err
	}
	current, exists := tx.data.entries[value.ID]
	if err := revisioncheck.CheckPut("entry", string(value.ID), expectedRevision, value.Revision.Number, current.Revision.Number, exists); err != nil {
		return err
	}
	tx.data.entries[value.ID] = cloneEntry(value)
	return nil
}

func (tx *transaction) DeleteEntry(ctx context.Context, id knowledge.EntryID, expectedRevision uint64) error {
	if err := tx.check(ctx, true); err != nil {
		return err
	}
	current, exists := tx.data.entries[id]
	if err := revisioncheck.CheckDelete("entry", string(id), expectedRevision, current.Revision.Number, exists); err != nil {
		return err
	}
	delete(tx.data.entries, id)
	return nil
}

func (tx *transaction) PutLink(ctx context.Context, value knowledge.Link, expectedRevision uint64) error {
	if err := tx.check(ctx, true); err != nil {
		return err
	}
	if err := value.Validate(); err != nil {
		return err
	}
	current, exists := tx.data.links[value.ID]
	if err := revisioncheck.CheckPut("link", string(value.ID), expectedRevision, value.Revision.Number, current.Revision.Number, exists); err != nil {
		return err
	}
	tx.data.links[value.ID] = cloneLink(value)
	return nil
}

func (tx *transaction) DeleteLink(ctx context.Context, id knowledge.LinkID, expectedRevision uint64) error {
	if err := tx.check(ctx, true); err != nil {
		return err
	}
	current, exists := tx.data.links[id]
	if err := revisioncheck.CheckDelete("link", string(id), expectedRevision, current.Revision.Number, exists); err != nil {
		return err
	}
	delete(tx.data.links, id)
	return nil
}

func (tx *transaction) PutEvidence(ctx context.Context, value knowledge.Evidence) error {
	if err := tx.check(ctx, true); err != nil {
		return err
	}
	if err := value.Validate(); err != nil {
		return err
	}
	if _, exists := tx.data.evidence[value.ID]; exists {
		return fmt.Errorf("%w: evidence %s already exists", knowledgeStore.ErrConflict, value.ID)
	}
	tx.data.evidence[value.ID] = value
	return nil
}

func (tx *transaction) DeleteEvidence(ctx context.Context, id knowledge.EvidenceID) error {
	if err := tx.check(ctx, true); err != nil {
		return err
	}
	if _, exists := tx.data.evidence[id]; !exists {
		return fmt.Errorf("%w: evidence %s", knowledgeStore.ErrNotFound, id)
	}
	delete(tx.data.evidence, id)
	return nil
}

func cloneData(source data) data {
	return data{
		chunks:   cloneMap(source.chunks, cloneChunk),
		entries:  cloneMap(source.entries, cloneEntry),
		links:    cloneMap(source.links, cloneLink),
		evidence: cloneMap(source.evidence, func(value knowledge.Evidence) knowledge.Evidence { return value }),
	}
}

func cloneMap[K comparable, V any](source map[K]V, clone func(V) V) map[K]V {
	output := make(map[K]V, len(source))
	for key, value := range source {
		output[key] = clone(value)
	}
	return output
}

func cloneChunk(value knowledge.Chunk) knowledge.Chunk {
	value.Aliases = slices.Clone(value.Aliases)
	value.Tags = slices.Clone(value.Tags)
	value.SharedWith = slices.Clone(value.SharedWith)
	value.Risk = slices.Clone(value.Risk)
	value.DependencyIDs = slices.Clone(value.DependencyIDs)
	return value
}

func cloneEntry(value knowledge.Entry) knowledge.Entry {
	value.Aliases = slices.Clone(value.Aliases)
	value.Tags = slices.Clone(value.Tags)
	value.Risk = slices.Clone(value.Risk)
	value.EvidenceIDs = slices.Clone(value.EvidenceIDs)
	value.Verification.EvidenceIDs = slices.Clone(value.Verification.EvidenceIDs)
	value.Applicability.OperatingSystems = slices.Clone(value.Applicability.OperatingSystems)
	value.Applicability.Architectures = slices.Clone(value.Applicability.Architectures)
	value.Applicability.Software = slices.Clone(value.Applicability.Software)
	value.Applicability.Locales = slices.Clone(value.Applicability.Locales)
	value.Applicability.Conditions = slices.Clone(value.Applicability.Conditions)
	return value
}

func cloneLink(value knowledge.Link) knowledge.Link {
	value.EvidenceIDs = slices.Clone(value.EvidenceIDs)
	return value
}
