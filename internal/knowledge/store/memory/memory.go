// Package memory provides a deterministic in-memory Knowledge store for service and
// backend-contract tests.
package memory

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

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
	chunks       map[knowledge.ChunkID]knowledge.Chunk
	entries      map[knowledge.EntryID]knowledge.Entry
	links        map[knowledge.LinkID]knowledge.Link
	evidence     map[knowledge.EvidenceID]knowledge.Evidence
	chunkHistory map[knowledge.ChunkID][]knowledge.Chunk
	entryHistory map[knowledge.EntryID][]knowledge.Entry
	linkHistory  map[knowledge.LinkID][]knowledge.Link
}

type transaction struct {
	data         *data
	active       bool
	writable     bool
	derivedDirty bool
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
		chunks:       make(map[knowledge.ChunkID]knowledge.Chunk),
		entries:      make(map[knowledge.EntryID]knowledge.Entry),
		links:        make(map[knowledge.LinkID]knowledge.Link),
		evidence:     make(map[knowledge.EvidenceID]knowledge.Evidence),
		chunkHistory: make(map[knowledge.ChunkID][]knowledge.Chunk),
		entryHistory: make(map[knowledge.EntryID][]knowledge.Entry),
		linkHistory:  make(map[knowledge.LinkID][]knowledge.Link),
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
	if tx.derivedDirty {
		if err := reconcileChunkCounts(ctx, &working); err != nil {
			return err
		}
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

func (s *Store) ListEntries(ctx context.Context, request knowledgeStore.EntryListRequest) (knowledgeStore.EntryPage, error) {
	if err := ctx.Err(); err != nil {
		return knowledgeStore.EntryPage{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return knowledgeStore.EntryPage{}, knowledgeStore.ErrClosed
	}
	entries := make([]knowledge.Entry, 0, len(s.data.entries))
	for _, entry := range s.data.entries {
		entries = append(entries, cloneEntry(entry))
	}
	return knowledgeStore.PaginateEntries(entries, request, s.indexGeneration)
}

func (s *Store) ListAdjacentLinks(ctx context.Context, request knowledgeStore.AdjacentLinkListRequest) (knowledgeStore.LinkPage, error) {
	if err := ctx.Err(); err != nil {
		return knowledgeStore.LinkPage{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return knowledgeStore.LinkPage{}, knowledgeStore.ErrClosed
	}
	links := make([]knowledge.Link, 0, len(s.data.links))
	for _, link := range s.data.links {
		links = append(links, cloneLink(link))
	}
	return knowledgeStore.PaginateAdjacentLinks(links, request, s.indexGeneration)
}

func (s *Store) ListRevisions(ctx context.Context, request knowledgeStore.RevisionListRequest) (knowledgeStore.RevisionPage, error) {
	if err := ctx.Err(); err != nil {
		return knowledgeStore.RevisionPage{}, err
	}
	if err := request.Object.Validate(); err != nil {
		return knowledgeStore.RevisionPage{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return knowledgeStore.RevisionPage{}, knowledgeStore.ErrClosed
	}
	var records []knowledgeStore.CanonicalRecord
	switch request.Object.Kind {
	case knowledge.ObjectKindChunk:
		for _, value := range s.data.chunkHistory[knowledge.ChunkID(request.Object.ID)] {
			value := cloneChunk(value)
			records = append(records, knowledgeStore.CanonicalRecord{Kind: knowledgeStore.RecordKindChunk, Chunk: &value})
		}
	case knowledge.ObjectKindEntry:
		for _, value := range s.data.entryHistory[knowledge.EntryID(request.Object.ID)] {
			value := cloneEntry(value)
			records = append(records, knowledgeStore.CanonicalRecord{Kind: knowledgeStore.RecordKindEntry, Entry: &value})
		}
	case knowledge.ObjectKindLink:
		for _, value := range s.data.linkHistory[knowledge.LinkID(request.Object.ID)] {
			value := cloneLink(value)
			records = append(records, knowledgeStore.CanonicalRecord{Kind: knowledgeStore.RecordKindLink, Link: &value})
		}
	}
	if len(records) == 0 {
		return knowledgeStore.RevisionPage{}, fmt.Errorf("%w: %s %s", knowledgeStore.ErrNotFound, request.Object.Kind, request.Object.ID)
	}
	return knowledgeStore.PaginateRevisions(records, request)
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
	chunks := make([]knowledge.Chunk, 0, len(tx.data.chunks))
	for _, item := range tx.data.chunks {
		chunks = append(chunks, item)
	}
	entries := make([]knowledge.Entry, 0, len(tx.data.entries))
	for _, item := range tx.data.entries {
		entries = append(entries, item)
	}
	links := make([]knowledge.Link, 0, len(tx.data.links))
	for _, item := range tx.data.links {
		links = append(links, item)
	}
	evidence := make([]knowledge.Evidence, 0, len(tx.data.evidence))
	for _, item := range tx.data.evidence {
		evidence = append(evidence, item)
	}
	return knowledgeStore.DeriveChunkDeletionBlockers(chunk, chunks, entries, links, evidence), nil
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

func (tx *transaction) EntryDeletionBlockers(ctx context.Context, id knowledge.EntryID) (knowledgeStore.EntryDeletionBlockers, error) {
	if err := tx.check(ctx, false); err != nil {
		return knowledgeStore.EntryDeletionBlockers{}, err
	}
	if _, exists := tx.data.entries[id]; !exists {
		return knowledgeStore.EntryDeletionBlockers{}, fmt.Errorf("%w: entry %s", knowledgeStore.ErrNotFound, id)
	}
	entries := make([]knowledge.Entry, 0, len(tx.data.entries))
	for _, entry := range tx.data.entries {
		entries = append(entries, entry)
	}
	links := make([]knowledge.Link, 0, len(tx.data.links))
	for _, link := range tx.data.links {
		links = append(links, link)
	}
	return knowledgeStore.DeriveEntryDeletionBlockers(id, entries, links), nil
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

func (tx *transaction) EvidenceBySource(ctx context.Context, sourceID, contentHash string) (knowledge.Evidence, error) {
	if err := tx.check(ctx, false); err != nil {
		return knowledge.Evidence{}, err
	}
	sourceID, contentHash = knowledge.NormalizeEvidenceIdentity(sourceID, contentHash)
	for _, value := range tx.data.evidence {
		candidateSource, candidateHash := knowledge.NormalizeEvidenceIdentity(value.Source.ID, value.Source.ContentHash)
		if candidateSource == sourceID && candidateHash == contentHash {
			return value, nil
		}
	}
	return knowledge.Evidence{}, fmt.Errorf("%w: evidence source %q hash %q", knowledgeStore.ErrNotFound, sourceID, contentHash)
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
	tx.derivedDirty = tx.derivedDirty || !exists || value.Counts != current.Counts
	tx.data.chunks[value.ID] = cloneChunk(value)
	tx.data.chunkHistory[value.ID] = append(tx.data.chunkHistory[value.ID], cloneChunk(value))
	return nil
}

func (tx *transaction) TouchChunk(ctx context.Context, id knowledge.ChunkID, usedAt time.Time) error {
	if err := tx.check(ctx, true); err != nil {
		return err
	}
	if usedAt.IsZero() {
		return fmt.Errorf("%w: last_used_at is required", knowledge.ErrInvalidRecord)
	}
	_, offset := usedAt.Zone()
	if offset != 0 {
		return fmt.Errorf("%w: last_used_at must be normalized to UTC", knowledge.ErrInvalidRecord)
	}
	current, exists := tx.data.chunks[id]
	if !exists {
		return fmt.Errorf("%w: chunk %s", knowledgeStore.ErrNotFound, id)
	}
	if usedAt.Before(current.CreatedAt) {
		return fmt.Errorf("%w: last_used_at must not precede created_at", knowledge.ErrInvalidRecord)
	}
	if !usedAt.After(current.LastUsedAt) {
		return nil
	}
	current.LastUsedAt = usedAt
	if err := current.Validate(); err != nil {
		return err
	}
	tx.data.chunks[id] = cloneChunk(current)
	return nil
}

func reconcileChunkCounts(ctx context.Context, data *data) error {
	chunks := make([]knowledge.Chunk, 0, len(data.chunks))
	for _, item := range data.chunks {
		if err := ctx.Err(); err != nil {
			return err
		}
		chunks = append(chunks, item)
	}
	entries := make([]knowledge.Entry, 0, len(data.entries))
	for _, item := range data.entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		entries = append(entries, item)
	}
	links := make([]knowledge.Link, 0, len(data.links))
	for _, item := range data.links {
		if err := ctx.Err(); err != nil {
			return err
		}
		links = append(links, item)
	}
	evidence := make([]knowledge.Evidence, 0, len(data.evidence))
	for _, item := range data.evidence {
		if err := ctx.Err(); err != nil {
			return err
		}
		evidence = append(evidence, item)
	}
	counts := knowledgeStore.DeriveChunkCounts(chunks, entries, links, evidence)
	for id, value := range data.chunks {
		if err := ctx.Err(); err != nil {
			return err
		}
		value.Counts = counts[id]
		data.chunks[id] = value
	}
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
	delete(tx.data.chunkHistory, id)
	tx.derivedDirty = true
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
	tx.data.entryHistory[value.ID] = append(tx.data.entryHistory[value.ID], cloneEntry(value))
	tx.derivedDirty = true
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
	delete(tx.data.entryHistory, id)
	tx.derivedDirty = true
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
	tx.data.linkHistory[value.ID] = append(tx.data.linkHistory[value.ID], cloneLink(value))
	tx.derivedDirty = true
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
	delete(tx.data.linkHistory, id)
	tx.derivedDirty = true
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
	if existing, err := tx.EvidenceBySource(ctx, value.Source.ID, value.Source.ContentHash); err == nil {
		return fmt.Errorf("%w: evidence source/hash already exists as %s", knowledgeStore.ErrConflict, existing.ID)
	} else if !errors.Is(err, knowledgeStore.ErrNotFound) {
		return err
	}
	tx.data.evidence[value.ID] = value
	tx.derivedDirty = true
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
	tx.derivedDirty = true
	return nil
}

func cloneData(source data) data {
	return data{
		chunks:       cloneMap(source.chunks, cloneChunk),
		entries:      cloneMap(source.entries, cloneEntry),
		links:        cloneMap(source.links, cloneLink),
		evidence:     cloneMap(source.evidence, func(value knowledge.Evidence) knowledge.Evidence { return value }),
		chunkHistory: cloneHistoryMap(source.chunkHistory, cloneChunk),
		entryHistory: cloneHistoryMap(source.entryHistory, cloneEntry),
		linkHistory:  cloneHistoryMap(source.linkHistory, cloneLink),
	}
}

func cloneHistoryMap[K comparable, V any](source map[K][]V, clone func(V) V) map[K][]V {
	output := make(map[K][]V, len(source))
	for key, history := range source {
		cloned := make([]V, len(history))
		for index, value := range history {
			cloned[index] = clone(value)
		}
		output[key] = cloned
	}
	return output
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
