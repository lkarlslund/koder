// Package memory provides a deterministic in-memory Memory store for service and
// backend-contract tests.
package memory

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
	revisioncheck "github.com/lkarlslund/koder/internal/memory/store/internal/revision"
)

// Store owns an isolated in-memory canonical record set.
type Store struct {
	mu              sync.RWMutex
	closed          bool
	data            data
	indexGeneration uint64
	rebuildStatus   memoryStoreAPI.IndexRebuildStatus
	graphViews      map[string]memoryStoreAPI.SavedGraphView
}

type data struct {
	chunks       map[memory.ChunkID]memory.Chunk
	entries      map[memory.EntryID]memory.Entry
	links        map[memory.LinkID]memory.Link
	evidence     map[memory.EvidenceID]memory.Evidence
	assets       map[assetKey]memoryStoreAPI.PackageAsset
	chunkHistory map[memory.ChunkID][]memory.Chunk
	entryHistory map[memory.EntryID][]memory.Entry
	linkHistory  map[memory.LinkID][]memory.Link
	usage        map[memory.EntryID]memoryStoreAPI.EntryUsage
	usageEvents  map[string]struct{}
}

type assetKey struct {
	chunkID memory.ChunkID
	path    string
}

type transaction struct {
	data         *data
	active       bool
	writable     bool
	derivedDirty bool
}

var _ memoryStoreAPI.Store = (*Store)(nil)
var _ memoryStoreAPI.WriteTx = (*transaction)(nil)

// New returns an empty, open in-memory store.
func New() *Store {
	return &Store{
		data:            newData(),
		indexGeneration: 1,
		rebuildStatus:   memoryStoreAPI.IndexRebuildStatus{ActiveGeneration: 1},
		graphViews:      make(map[string]memoryStoreAPI.SavedGraphView),
	}
}

func newData() data {
	return data{
		chunks:       make(map[memory.ChunkID]memory.Chunk),
		entries:      make(map[memory.EntryID]memory.Entry),
		links:        make(map[memory.LinkID]memory.Link),
		evidence:     make(map[memory.EvidenceID]memory.Evidence),
		assets:       make(map[assetKey]memoryStoreAPI.PackageAsset),
		chunkHistory: make(map[memory.ChunkID][]memory.Chunk),
		entryHistory: make(map[memory.EntryID][]memory.Entry),
		linkHistory:  make(map[memory.LinkID][]memory.Link),
		usage:        make(map[memory.EntryID]memoryStoreAPI.EntryUsage),
		usageEvents:  make(map[string]struct{}),
	}
}

// View runs fn against one consistent read snapshot.
func (s *Store) View(ctx context.Context, fn func(memoryStoreAPI.ReadTx) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if fn == nil {
		return fmt.Errorf("view memory store: callback is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return memoryStoreAPI.ErrClosed
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
func (s *Store) Update(ctx context.Context, fn func(memoryStoreAPI.WriteTx) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if fn == nil {
		return fmt.Errorf("update memory store: callback is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return memoryStoreAPI.ErrClosed
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
func (s *Store) Health(ctx context.Context) (memoryStoreAPI.Health, error) {
	if err := ctx.Err(); err != nil {
		return memoryStoreAPI.Health{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return memoryStoreAPI.Health{
		Backend:         "memory",
		Open:            !s.closed,
		SchemaVersion:   1,
		IndexGeneration: s.indexGeneration,
	}, nil
}

func (tx *transaction) Empty(ctx context.Context) (bool, error) {
	if err := tx.check(ctx, false); err != nil {
		return false, err
	}
	return len(tx.data.chunks) == 0 && len(tx.data.entries) == 0 && len(tx.data.links) == 0 &&
		len(tx.data.evidence) == 0 && len(tx.data.assets) == 0 && len(tx.data.chunkHistory) == 0 &&
		len(tx.data.entryHistory) == 0 && len(tx.data.linkHistory) == 0 && len(tx.data.usage) == 0 &&
		len(tx.data.usageEvents) == 0, nil
}

func (s *Store) ListChunks(ctx context.Context, request memoryStoreAPI.ChunkListRequest) (memoryStoreAPI.ChunkPage, error) {
	if err := ctx.Err(); err != nil {
		return memoryStoreAPI.ChunkPage{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return memoryStoreAPI.ChunkPage{}, memoryStoreAPI.ErrClosed
	}
	chunks := make([]memory.Chunk, 0, len(s.data.chunks))
	for _, chunk := range s.data.chunks {
		chunks = append(chunks, cloneChunk(chunk))
	}
	return memoryStoreAPI.PaginateChunks(chunks, request, s.indexGeneration)
}

func (s *Store) ListEntries(ctx context.Context, request memoryStoreAPI.EntryListRequest) (memoryStoreAPI.EntryPage, error) {
	if err := ctx.Err(); err != nil {
		return memoryStoreAPI.EntryPage{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return memoryStoreAPI.EntryPage{}, memoryStoreAPI.ErrClosed
	}
	entries := make([]memory.Entry, 0, len(s.data.entries))
	for _, entry := range s.data.entries {
		entries = append(entries, cloneEntry(entry))
	}
	return memoryStoreAPI.PaginateEntries(entries, request, s.indexGeneration)
}

func (s *Store) ListAdjacentLinks(ctx context.Context, request memoryStoreAPI.AdjacentLinkListRequest) (memoryStoreAPI.LinkPage, error) {
	if err := ctx.Err(); err != nil {
		return memoryStoreAPI.LinkPage{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return memoryStoreAPI.LinkPage{}, memoryStoreAPI.ErrClosed
	}
	links := make([]memory.Link, 0, len(s.data.links))
	for _, link := range s.data.links {
		links = append(links, cloneLink(link))
	}
	return memoryStoreAPI.PaginateAdjacentLinks(links, request, s.indexGeneration)
}

func (s *Store) ListRevisions(ctx context.Context, request memoryStoreAPI.RevisionListRequest) (memoryStoreAPI.RevisionPage, error) {
	if err := ctx.Err(); err != nil {
		return memoryStoreAPI.RevisionPage{}, err
	}
	if err := request.Object.Validate(); err != nil {
		return memoryStoreAPI.RevisionPage{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return memoryStoreAPI.RevisionPage{}, memoryStoreAPI.ErrClosed
	}
	var records []memoryStoreAPI.CanonicalRecord
	switch request.Object.Kind {
	case memory.ObjectKindChunk:
		for _, value := range s.data.chunkHistory[memory.ChunkID(request.Object.ID)] {
			value := cloneChunk(value)
			records = append(records, memoryStoreAPI.CanonicalRecord{Kind: memoryStoreAPI.RecordKindChunk, Chunk: &value})
		}
	case memory.ObjectKindEntry:
		for _, value := range s.data.entryHistory[memory.EntryID(request.Object.ID)] {
			value := cloneEntry(value)
			records = append(records, memoryStoreAPI.CanonicalRecord{Kind: memoryStoreAPI.RecordKindEntry, Entry: &value})
		}
	case memory.ObjectKindLink:
		for _, value := range s.data.linkHistory[memory.LinkID(request.Object.ID)] {
			value := cloneLink(value)
			records = append(records, memoryStoreAPI.CanonicalRecord{Kind: memoryStoreAPI.RecordKindLink, Link: &value})
		}
	}
	if len(records) == 0 {
		return memoryStoreAPI.RevisionPage{}, fmt.Errorf("%w: %s %s", memoryStoreAPI.ErrNotFound, request.Object.Kind, request.Object.ID)
	}
	return memoryStoreAPI.PaginateRevisions(records, request)
}

// Checkpoint is deliberately unsupported because the memory backend has no durable files.
func (s *Store) Checkpoint(ctx context.Context, _ string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return memoryStoreAPI.ErrClosed
	}
	return memoryStoreAPI.ErrUnsupported
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
		return memoryStoreAPI.ErrClosed
	}
	if write && !tx.writable {
		return memoryStoreAPI.ErrReadOnly
	}
	return ctx.Err()
}

func (tx *transaction) Chunk(ctx context.Context, id memory.ChunkID) (memory.Chunk, error) {
	if err := tx.check(ctx, false); err != nil {
		return memory.Chunk{}, err
	}
	value, ok := tx.data.chunks[id]
	if !ok {
		return memory.Chunk{}, fmt.Errorf("%w: chunk %s", memoryStoreAPI.ErrNotFound, id)
	}
	return cloneChunk(value), nil
}

func (tx *transaction) ChunkDeletionBlockers(ctx context.Context, id memory.ChunkID) (memoryStoreAPI.ChunkDeletionBlockers, error) {
	if err := tx.check(ctx, false); err != nil {
		return memoryStoreAPI.ChunkDeletionBlockers{}, err
	}
	chunk, exists := tx.data.chunks[id]
	if !exists {
		return memoryStoreAPI.ChunkDeletionBlockers{}, fmt.Errorf("%w: chunk %s", memoryStoreAPI.ErrNotFound, id)
	}
	chunks := make([]memory.Chunk, 0, len(tx.data.chunks))
	for _, item := range tx.data.chunks {
		chunks = append(chunks, item)
	}
	entries := make([]memory.Entry, 0, len(tx.data.entries))
	for _, item := range tx.data.entries {
		entries = append(entries, item)
	}
	links := make([]memory.Link, 0, len(tx.data.links))
	for _, item := range tx.data.links {
		links = append(links, item)
	}
	evidence := make([]memory.Evidence, 0, len(tx.data.evidence))
	for _, item := range tx.data.evidence {
		evidence = append(evidence, item)
	}
	return memoryStoreAPI.DeriveChunkDeletionBlockers(chunk, chunks, entries, links, evidence), nil
}

func (tx *transaction) Entry(ctx context.Context, id memory.EntryID) (memory.Entry, error) {
	if err := tx.check(ctx, false); err != nil {
		return memory.Entry{}, err
	}
	value, ok := tx.data.entries[id]
	if !ok {
		return memory.Entry{}, fmt.Errorf("%w: entry %s", memoryStoreAPI.ErrNotFound, id)
	}
	return cloneEntry(value), nil
}

func (tx *transaction) EntryDeletionBlockers(ctx context.Context, id memory.EntryID) (memoryStoreAPI.EntryDeletionBlockers, error) {
	if err := tx.check(ctx, false); err != nil {
		return memoryStoreAPI.EntryDeletionBlockers{}, err
	}
	if _, exists := tx.data.entries[id]; !exists {
		return memoryStoreAPI.EntryDeletionBlockers{}, fmt.Errorf("%w: entry %s", memoryStoreAPI.ErrNotFound, id)
	}
	entries := make([]memory.Entry, 0, len(tx.data.entries))
	for _, entry := range tx.data.entries {
		entries = append(entries, entry)
	}
	links := make([]memory.Link, 0, len(tx.data.links))
	for _, link := range tx.data.links {
		links = append(links, link)
	}
	return memoryStoreAPI.DeriveEntryDeletionBlockers(id, entries, links), nil
}

func (tx *transaction) Link(ctx context.Context, id memory.LinkID) (memory.Link, error) {
	if err := tx.check(ctx, false); err != nil {
		return memory.Link{}, err
	}
	value, ok := tx.data.links[id]
	if !ok {
		return memory.Link{}, fmt.Errorf("%w: link %s", memoryStoreAPI.ErrNotFound, id)
	}
	return cloneLink(value), nil
}

func (tx *transaction) EquivalentLink(ctx context.Context, candidate memory.Link) (memory.Link, error) {
	if err := tx.check(ctx, false); err != nil {
		return memory.Link{}, err
	}
	if err := memory.ValidateRelationshipShape(candidate.Kind, candidate.Source, candidate.Target); err != nil {
		return memory.Link{}, err
	}
	candidate = memory.NormalizeLink(candidate)
	for _, link := range tx.data.links {
		normalized := memory.NormalizeLink(link)
		if normalized.Kind == candidate.Kind && normalized.Source == candidate.Source && normalized.Target == candidate.Target {
			return cloneLink(link), nil
		}
	}
	return memory.Link{}, fmt.Errorf("%w: equivalent link", memoryStoreAPI.ErrNotFound)
}

func (tx *transaction) Evidence(ctx context.Context, id memory.EvidenceID) (memory.Evidence, error) {
	if err := tx.check(ctx, false); err != nil {
		return memory.Evidence{}, err
	}
	value, ok := tx.data.evidence[id]
	if !ok {
		return memory.Evidence{}, fmt.Errorf("%w: evidence %s", memoryStoreAPI.ErrNotFound, id)
	}
	return value, nil
}

func (tx *transaction) EvidenceBySource(ctx context.Context, sourceID, contentHash string) (memory.Evidence, error) {
	if err := tx.check(ctx, false); err != nil {
		return memory.Evidence{}, err
	}
	sourceID, contentHash = memory.NormalizeEvidenceIdentity(sourceID, contentHash)
	for _, value := range tx.data.evidence {
		candidateSource, candidateHash := memory.NormalizeEvidenceIdentity(value.Source.ID, value.Source.ContentHash)
		if candidateSource == sourceID && candidateHash == contentHash {
			return value, nil
		}
	}
	return memory.Evidence{}, fmt.Errorf("%w: evidence source %q hash %q", memoryStoreAPI.ErrNotFound, sourceID, contentHash)
}

func (tx *transaction) Asset(ctx context.Context, chunkID memory.ChunkID, path string) (memoryStoreAPI.PackageAsset, error) {
	if err := tx.check(ctx, false); err != nil {
		return memoryStoreAPI.PackageAsset{}, err
	}
	value, exists := tx.data.assets[assetKey{chunkID: chunkID, path: path}]
	if !exists {
		return memoryStoreAPI.PackageAsset{}, fmt.Errorf("%w: asset %s/%s", memoryStoreAPI.ErrNotFound, chunkID, path)
	}
	return memoryStoreAPI.ClonePackageAsset(value), nil
}

func (tx *transaction) ListAssets(ctx context.Context, chunkID memory.ChunkID) ([]memoryStoreAPI.PackageAsset, error) {
	if err := tx.check(ctx, false); err != nil {
		return nil, err
	}
	result := make([]memoryStoreAPI.PackageAsset, 0)
	for key, value := range tx.data.assets {
		if key.chunkID == chunkID {
			result = append(result, memoryStoreAPI.ClonePackageAsset(value))
		}
	}
	slices.SortFunc(result, func(left, right memoryStoreAPI.PackageAsset) int { return strings.Compare(left.Path, right.Path) })
	return result, nil
}

func (tx *transaction) PutChunk(ctx context.Context, value memory.Chunk, expectedRevision uint64) error {
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

func (tx *transaction) TouchChunk(ctx context.Context, id memory.ChunkID, usedAt time.Time) error {
	if err := tx.check(ctx, true); err != nil {
		return err
	}
	if usedAt.IsZero() {
		return fmt.Errorf("%w: last_used_at is required", memory.ErrInvalidRecord)
	}
	_, offset := usedAt.Zone()
	if offset != 0 {
		return fmt.Errorf("%w: last_used_at must be normalized to UTC", memory.ErrInvalidRecord)
	}
	current, exists := tx.data.chunks[id]
	if !exists {
		return fmt.Errorf("%w: chunk %s", memoryStoreAPI.ErrNotFound, id)
	}
	if usedAt.Before(current.CreatedAt) {
		return fmt.Errorf("%w: last_used_at must not precede created_at", memory.ErrInvalidRecord)
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
	chunks := make([]memory.Chunk, 0, len(data.chunks))
	for _, item := range data.chunks {
		if err := ctx.Err(); err != nil {
			return err
		}
		chunks = append(chunks, item)
	}
	entries := make([]memory.Entry, 0, len(data.entries))
	for _, item := range data.entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		entries = append(entries, item)
	}
	links := make([]memory.Link, 0, len(data.links))
	for _, item := range data.links {
		if err := ctx.Err(); err != nil {
			return err
		}
		links = append(links, item)
	}
	evidence := make([]memory.Evidence, 0, len(data.evidence))
	for _, item := range data.evidence {
		if err := ctx.Err(); err != nil {
			return err
		}
		evidence = append(evidence, item)
	}
	counts := memoryStoreAPI.DeriveChunkCounts(chunks, entries, links, evidence)
	for id, value := range data.chunks {
		if err := ctx.Err(); err != nil {
			return err
		}
		value.Counts = counts[id]
		data.chunks[id] = value
	}
	return nil
}

func (tx *transaction) DeleteChunk(ctx context.Context, id memory.ChunkID, expectedRevision uint64) error {
	if err := tx.check(ctx, true); err != nil {
		return err
	}
	current, exists := tx.data.chunks[id]
	if err := revisioncheck.CheckDelete("chunk", string(id), expectedRevision, current.Revision.Number, exists); err != nil {
		return err
	}
	delete(tx.data.chunks, id)
	delete(tx.data.chunkHistory, id)
	for key := range tx.data.assets {
		if key.chunkID == id {
			delete(tx.data.assets, key)
		}
	}
	tx.derivedDirty = true
	return nil
}

func (tx *transaction) PutEntry(ctx context.Context, value memory.Entry, expectedRevision uint64) error {
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

func (tx *transaction) DeleteEntry(ctx context.Context, id memory.EntryID, expectedRevision uint64) error {
	if err := tx.check(ctx, true); err != nil {
		return err
	}
	current, exists := tx.data.entries[id]
	if err := revisioncheck.CheckDelete("entry", string(id), expectedRevision, current.Revision.Number, exists); err != nil {
		return err
	}
	delete(tx.data.entries, id)
	delete(tx.data.entryHistory, id)
	delete(tx.data.usage, id)
	prefix := string(id) + "\x00"
	for event := range tx.data.usageEvents {
		if strings.HasPrefix(event, prefix) {
			delete(tx.data.usageEvents, event)
		}
	}
	tx.derivedDirty = true
	return nil
}

func (tx *transaction) PutLink(ctx context.Context, value memory.Link, expectedRevision uint64) error {
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
	if equivalent, err := tx.EquivalentLink(ctx, value); err == nil && equivalent.ID != value.ID {
		return fmt.Errorf("%w: link %s duplicates %s", memoryStoreAPI.ErrConflict, value.ID, equivalent.ID)
	} else if err != nil && !errors.Is(err, memoryStoreAPI.ErrNotFound) {
		return err
	}
	tx.data.links[value.ID] = cloneLink(value)
	tx.data.linkHistory[value.ID] = append(tx.data.linkHistory[value.ID], cloneLink(value))
	tx.derivedDirty = true
	return nil
}

func (tx *transaction) DeleteLink(ctx context.Context, id memory.LinkID, expectedRevision uint64) error {
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

func (tx *transaction) PutEvidence(ctx context.Context, value memory.Evidence) error {
	if err := tx.check(ctx, true); err != nil {
		return err
	}
	if err := value.Validate(); err != nil {
		return err
	}
	if _, exists := tx.data.evidence[value.ID]; exists {
		return fmt.Errorf("%w: evidence %s already exists", memoryStoreAPI.ErrConflict, value.ID)
	}
	if existing, err := tx.EvidenceBySource(ctx, value.Source.ID, value.Source.ContentHash); err == nil {
		return fmt.Errorf("%w: evidence source/hash already exists as %s", memoryStoreAPI.ErrConflict, existing.ID)
	} else if !errors.Is(err, memoryStoreAPI.ErrNotFound) {
		return err
	}
	tx.data.evidence[value.ID] = value
	tx.derivedDirty = true
	return nil
}

func (tx *transaction) DeleteEvidence(ctx context.Context, id memory.EvidenceID) error {
	if err := tx.check(ctx, true); err != nil {
		return err
	}
	if _, exists := tx.data.evidence[id]; !exists {
		return fmt.Errorf("%w: evidence %s", memoryStoreAPI.ErrNotFound, id)
	}
	delete(tx.data.evidence, id)
	tx.derivedDirty = true
	return nil
}

func (tx *transaction) PutAsset(ctx context.Context, value memoryStoreAPI.PackageAsset) error {
	if err := tx.check(ctx, true); err != nil {
		return err
	}
	if err := value.Validate(); err != nil {
		return err
	}
	if _, exists := tx.data.chunks[value.ChunkID]; !exists {
		return fmt.Errorf("%w: asset chunk %s", memoryStoreAPI.ErrNotFound, value.ChunkID)
	}
	key := assetKey{chunkID: value.ChunkID, path: value.Path}
	if _, exists := tx.data.assets[key]; exists {
		return fmt.Errorf("%w: asset %s/%s already exists", memoryStoreAPI.ErrConflict, value.ChunkID, value.Path)
	}
	tx.data.assets[key] = memoryStoreAPI.ClonePackageAsset(value)
	return nil
}

func (tx *transaction) DeleteAsset(ctx context.Context, chunkID memory.ChunkID, path string) error {
	if err := tx.check(ctx, true); err != nil {
		return err
	}
	key := assetKey{chunkID: chunkID, path: path}
	if _, exists := tx.data.assets[key]; !exists {
		return fmt.Errorf("%w: asset %s/%s", memoryStoreAPI.ErrNotFound, chunkID, path)
	}
	delete(tx.data.assets, key)
	return nil
}

func cloneData(source data) data {
	return data{
		chunks:       cloneMap(source.chunks, cloneChunk),
		entries:      cloneMap(source.entries, cloneEntry),
		links:        cloneMap(source.links, cloneLink),
		evidence:     cloneMap(source.evidence, func(value memory.Evidence) memory.Evidence { return value }),
		assets:       cloneMap(source.assets, memoryStoreAPI.ClonePackageAsset),
		chunkHistory: cloneHistoryMap(source.chunkHistory, cloneChunk),
		entryHistory: cloneHistoryMap(source.entryHistory, cloneEntry),
		linkHistory:  cloneHistoryMap(source.linkHistory, cloneLink),
		usage:        cloneMap(source.usage, func(value memoryStoreAPI.EntryUsage) memoryStoreAPI.EntryUsage { return value }),
		usageEvents:  cloneMap(source.usageEvents, func(value struct{}) struct{} { return value }),
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

func cloneChunk(value memory.Chunk) memory.Chunk {
	value.Aliases = slices.Clone(value.Aliases)
	value.Tags = slices.Clone(value.Tags)
	value.SharedWith = slices.Clone(value.SharedWith)
	value.Risk = slices.Clone(value.Risk)
	value.DependencyIDs = slices.Clone(value.DependencyIDs)
	return value
}

func cloneEntry(value memory.Entry) memory.Entry {
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

func cloneLink(value memory.Link) memory.Link {
	value.EvidenceIDs = slices.Clone(value.EvidenceIDs)
	return value
}
