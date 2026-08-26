package pebble

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	cockroachpebble "github.com/cockroachdb/pebble"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
	"github.com/lkarlslund/koder/internal/memory/store/internal/revision"
)

type recordReader interface {
	Get([]byte) ([]byte, io.Closer, error)
	NewIter(*cockroachpebble.IterOptions) (*cockroachpebble.Iterator, error)
}

type transaction struct {
	reader          recordReader
	batch           *cockroachpebble.Batch
	active          bool
	writable        bool
	indexGeneration uint64
	indexes         []indexDefinition
	derivedDirty    bool
	indexMutations  []indexMutation
}

type indexMutation struct {
	delete map[string][]indexEntry
	put    map[string][]indexEntry
}

var _ memoryStoreAPI.Store = (*Store)(nil)
var _ memoryStoreAPI.WriteTx = (*transaction)(nil)

// View runs fn against a stable Pebble snapshot.
func (s *Store) View(ctx context.Context, fn func(memoryStoreAPI.ReadTx) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if fn == nil {
		return fmt.Errorf("view memory pebble: callback is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return memoryStoreAPI.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	snapshot := s.db.NewSnapshot()
	defer func() { _ = snapshot.Close() }()
	tx := &transaction{reader: snapshot, active: true, indexGeneration: s.meta.IndexGeneration}
	err := func() error {
		defer func() { tx.active = false }()
		return fn(tx)
	}()
	if err != nil {
		return err
	}
	return ctx.Err()
}

// Update commits every callback mutation as one synchronous indexed batch.
func (s *Store) Update(ctx context.Context, fn func(memoryStoreAPI.WriteTx) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if fn == nil {
		return fmt.Errorf("update memory pebble: callback is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return memoryStoreAPI.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	indexes, err := validateIndexDefinitions(s.indexes)
	if err != nil {
		return err
	}
	batch := s.db.NewIndexedBatch()
	defer func() { _ = batch.Close() }()
	tx := &transaction{
		reader: batch, batch: batch, active: true, writable: true,
		indexGeneration: s.meta.IndexGeneration, indexes: indexes,
	}
	err = func() error {
		defer func() { tx.active = false }()
		if err := fn(tx); err != nil {
			return err
		}
		if tx.derivedDirty {
			return tx.reconcileChunkCounts(ctx)
		}
		return nil
	}()
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := batch.Commit(cockroachpebble.Sync); err != nil {
		return fmt.Errorf("commit memory transaction: %w", err)
	}
	s.appendRebuildMutations(tx.indexMutations)
	return nil
}

func (tx *transaction) Empty(ctx context.Context) (bool, error) {
	if err := tx.check(ctx, false); err != nil {
		return false, err
	}
	for _, prefix := range [][]byte{canonicalPrefix, revisionsPrefix, packageAssetPrefix, entryUsagePrefix, usageEventPrefix} {
		lower, upper := prefixBounds(prefix)
		iter, err := tx.reader.NewIter(&cockroachpebble.IterOptions{LowerBound: lower, UpperBound: upper})
		if err != nil {
			return false, fmt.Errorf("inspect empty memory transaction: %w", err)
		}
		nonEmpty := iter.First()
		iterErr := iter.Error()
		closeErr := iter.Close()
		if iterErr != nil {
			return false, fmt.Errorf("inspect empty memory transaction: %w", iterErr)
		}
		if closeErr != nil {
			return false, fmt.Errorf("close empty memory iterator: %w", closeErr)
		}
		if nonEmpty {
			return false, nil
		}
	}
	return true, nil
}

const maxRebuildJournalMutations = 100000

func (s *Store) appendRebuildMutations(mutations []indexMutation) {
	if s.rebuildTarget == 0 || len(mutations) == 0 || s.rebuildJournalOverflow {
		return
	}
	if len(s.rebuildJournal) > maxRebuildJournalMutations-len(mutations) {
		s.rebuildJournalOverflow = true
		return
	}
	s.rebuildJournal = append(s.rebuildJournal, mutations...)
}

func (tx *transaction) check(ctx context.Context, write bool) error {
	if !tx.active || tx.reader == nil {
		return memoryStoreAPI.ErrClosed
	}
	if write && (!tx.writable || tx.batch == nil) {
		return memoryStoreAPI.ErrReadOnly
	}
	return ctx.Err()
}

func (tx *transaction) Chunk(ctx context.Context, id memory.ChunkID) (memory.Chunk, error) {
	if err := tx.check(ctx, false); err != nil {
		return memory.Chunk{}, err
	}
	return readRecord[memory.Chunk](tx.reader, chunkKey(string(id)), "chunk", string(id))
}

func (tx *transaction) ChunkDeletionBlockers(ctx context.Context, id memory.ChunkID) (memoryStoreAPI.ChunkDeletionBlockers, error) {
	if err := tx.check(ctx, false); err != nil {
		return memoryStoreAPI.ChunkDeletionBlockers{}, err
	}
	chunk, err := tx.Chunk(ctx, id)
	if err != nil {
		return memoryStoreAPI.ChunkDeletionBlockers{}, err
	}
	var chunks []memory.Chunk
	var entries []memory.Entry
	var links []memory.Link
	var evidence []memory.Evidence
	if _, err := scanCanonical(ctx, tx.reader, func(record memoryStoreAPI.CanonicalRecord) error {
		switch record.Kind {
		case memoryStoreAPI.RecordKindChunk:
			chunks = append(chunks, *record.Chunk)
		case memoryStoreAPI.RecordKindEntry:
			entries = append(entries, *record.Entry)
		case memoryStoreAPI.RecordKindLink:
			links = append(links, *record.Link)
		case memoryStoreAPI.RecordKindEvidence:
			evidence = append(evidence, *record.Evidence)
		}
		return nil
	}); err != nil {
		return memoryStoreAPI.ChunkDeletionBlockers{}, err
	}
	return memoryStoreAPI.DeriveChunkDeletionBlockers(chunk, chunks, entries, links, evidence), nil
}

func (tx *transaction) Entry(ctx context.Context, id memory.EntryID) (memory.Entry, error) {
	if err := tx.check(ctx, false); err != nil {
		return memory.Entry{}, err
	}
	return readRecord[memory.Entry](tx.reader, entryKey(string(id)), "entry", string(id))
}

func (tx *transaction) EntryDeletionBlockers(ctx context.Context, id memory.EntryID) (memoryStoreAPI.EntryDeletionBlockers, error) {
	if err := tx.check(ctx, false); err != nil {
		return memoryStoreAPI.EntryDeletionBlockers{}, err
	}
	if _, err := tx.Entry(ctx, id); err != nil {
		return memoryStoreAPI.EntryDeletionBlockers{}, err
	}
	var entries []memory.Entry
	var links []memory.Link
	if _, err := scanCanonical(ctx, tx.reader, func(record memoryStoreAPI.CanonicalRecord) error {
		switch record.Kind {
		case memoryStoreAPI.RecordKindEntry:
			entries = append(entries, *record.Entry)
		case memoryStoreAPI.RecordKindLink:
			links = append(links, *record.Link)
		}
		return nil
	}); err != nil {
		return memoryStoreAPI.EntryDeletionBlockers{}, err
	}
	return memoryStoreAPI.DeriveEntryDeletionBlockers(id, entries, links), nil
}

func (tx *transaction) Link(ctx context.Context, id memory.LinkID) (memory.Link, error) {
	if err := tx.check(ctx, false); err != nil {
		return memory.Link{}, err
	}
	return readRecord[memory.Link](tx.reader, linkKey(string(id)), "link", string(id))
}

func (tx *transaction) EquivalentLink(ctx context.Context, candidate memory.Link) (memory.Link, error) {
	if err := tx.check(ctx, false); err != nil {
		return memory.Link{}, err
	}
	if err := memory.ValidateRelationshipShape(candidate.Kind, candidate.Source, candidate.Target); err != nil {
		return memory.Link{}, err
	}
	candidate = memory.NormalizeLink(candidate)
	lower, upper := prefixBounds(linkPrefix())
	iter, err := tx.reader.NewIter(&cockroachpebble.IterOptions{LowerBound: lower, UpperBound: upper})
	if err != nil {
		return memory.Link{}, fmt.Errorf("find equivalent memory link: %w", err)
	}
	defer func() { _ = iter.Close() }()
	for iter.First(); iter.Valid(); iter.Next() {
		if err := ctx.Err(); err != nil {
			return memory.Link{}, err
		}
		id := memory.LinkID(string(iter.Key()[len(linkPrefix()):]))
		link, err := decodeRecord[memory.Link](iter.Value(), "link", string(id))
		if err != nil {
			return memory.Link{}, err
		}
		normalized := memory.NormalizeLink(link)
		if normalized.Kind == candidate.Kind && normalized.Source == candidate.Source && normalized.Target == candidate.Target {
			return link, nil
		}
	}
	if err := iter.Error(); err != nil {
		return memory.Link{}, fmt.Errorf("find equivalent memory link: %w", err)
	}
	return memory.Link{}, fmt.Errorf("%w: equivalent link", memoryStoreAPI.ErrNotFound)
}

func (tx *transaction) Evidence(ctx context.Context, id memory.EvidenceID) (memory.Evidence, error) {
	if err := tx.check(ctx, false); err != nil {
		return memory.Evidence{}, err
	}
	return readRecord[memory.Evidence](tx.reader, evidenceKey(string(id)), "evidence", string(id))
}

func (tx *transaction) EvidenceBySource(ctx context.Context, sourceID, contentHash string) (memory.Evidence, error) {
	if err := tx.check(ctx, false); err != nil {
		return memory.Evidence{}, err
	}
	sourceID, contentHash = memory.NormalizeEvidenceIdentity(sourceID, contentHash)
	prefix := indexKey(tx.indexGeneration, evidenceSourceIndex, encodeIndexTuple(sourceID, contentHash))
	lower, upper := prefixBounds(prefix)
	iter, err := tx.reader.NewIter(&cockroachpebble.IterOptions{LowerBound: lower, UpperBound: upper})
	if err != nil {
		return memory.Evidence{}, fmt.Errorf("find evidence by source/hash: %w", err)
	}
	if iter.First() {
		evidenceID := memory.EvidenceID(string(iter.Value()))
		if iter.Next() {
			_ = iter.Close()
			return memory.Evidence{}, fmt.Errorf("%w: duplicate evidence source/hash index", memoryStoreAPI.ErrIncompatible)
		}
		if err := iter.Error(); err != nil {
			_ = iter.Close()
			return memory.Evidence{}, fmt.Errorf("find evidence by source/hash: %w", err)
		}
		_ = iter.Close()
		return tx.Evidence(ctx, evidenceID)
	}
	if err := iter.Error(); err != nil {
		_ = iter.Close()
		return memory.Evidence{}, fmt.Errorf("find evidence by source/hash: %w", err)
	}
	_ = iter.Close()
	var found memory.Evidence
	foundMatch := false
	if _, err := scanCanonical(ctx, tx.reader, func(record memoryStoreAPI.CanonicalRecord) error {
		if record.Kind != memoryStoreAPI.RecordKindEvidence {
			return nil
		}
		candidateSource, candidateHash := memory.NormalizeEvidenceIdentity(record.Evidence.Source.ID, record.Evidence.Source.ContentHash)
		if candidateSource == sourceID && candidateHash == contentHash {
			if foundMatch {
				return fmt.Errorf("%w: duplicate canonical evidence source/hash", memoryStoreAPI.ErrIncompatible)
			}
			found = *record.Evidence
			foundMatch = true
		}
		return nil
	}); err != nil {
		return memory.Evidence{}, err
	}
	if foundMatch {
		return found, nil
	}
	return memory.Evidence{}, fmt.Errorf("%w: evidence source %q hash %q", memoryStoreAPI.ErrNotFound, sourceID, contentHash)
}

func (tx *transaction) Asset(ctx context.Context, chunkID memory.ChunkID, path string) (memoryStoreAPI.PackageAsset, error) {
	if err := tx.check(ctx, false); err != nil {
		return memoryStoreAPI.PackageAsset{}, err
	}
	return readRecord[memoryStoreAPI.PackageAsset](tx.reader, assetKey(chunkID, path), "asset", string(chunkID)+"/"+path)
}

func (tx *transaction) ListAssets(ctx context.Context, chunkID memory.ChunkID) ([]memoryStoreAPI.PackageAsset, error) {
	if err := tx.check(ctx, false); err != nil {
		return nil, err
	}
	lower, upper := prefixBounds(assetChunkPrefix(chunkID))
	iter, err := tx.reader.NewIter(&cockroachpebble.IterOptions{LowerBound: lower, UpperBound: upper})
	if err != nil {
		return nil, fmt.Errorf("list package assets for chunk %s: %w", chunkID, err)
	}
	defer func() { _ = iter.Close() }()
	result := make([]memoryStoreAPI.PackageAsset, 0)
	for valid := iter.First(); valid; valid = iter.Next() {
		value, err := decodeRecord[memoryStoreAPI.PackageAsset](iter.Value(), "asset", string(chunkID))
		if err != nil {
			return nil, err
		}
		result = append(result, memoryStoreAPI.ClonePackageAsset(value))
	}
	if err := iter.Error(); err != nil {
		return nil, fmt.Errorf("iterate package assets for chunk %s: %w", chunkID, err)
	}
	return result, ctx.Err()
}

func (tx *transaction) PutChunk(ctx context.Context, value memory.Chunk, expectedRevision uint64) error {
	if err := tx.check(ctx, true); err != nil {
		return err
	}
	if err := value.Validate(); err != nil {
		return err
	}
	current, exists, err := currentChunk(ctx, tx, value.ID)
	if err != nil {
		return err
	}
	if err := revision.CheckPut("chunk", string(value.ID), expectedRevision, value.Revision.Number, current.Revision.Number, exists); err != nil {
		return err
	}
	tx.derivedDirty = tx.derivedDirty || !exists || value.Counts != current.Counts
	if err := tx.replaceChunkIndexes(ctx, optionalChunk(current, exists), &value); err != nil {
		return err
	}
	return tx.putRevisioned(chunkKey(string(value.ID)), revisionKey(recordChunk, string(value.ID), value.Revision.Number), value)
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
	current, err := tx.Chunk(ctx, id)
	if err != nil {
		return err
	}
	if usedAt.Before(current.CreatedAt) {
		return fmt.Errorf("%w: last_used_at must not precede created_at", memory.ErrInvalidRecord)
	}
	if !usedAt.After(current.LastUsedAt) {
		return nil
	}
	next := current
	next.LastUsedAt = usedAt
	if err := next.Validate(); err != nil {
		return err
	}
	if err := tx.replaceChunkIndexes(ctx, &current, &next); err != nil {
		return err
	}
	return tx.putChunkProjection(next)
}

func (tx *transaction) reconcileChunkCounts(ctx context.Context) error {
	var chunks []memory.Chunk
	var entries []memory.Entry
	var links []memory.Link
	var evidence []memory.Evidence
	if _, err := scanCanonical(ctx, tx.reader, func(record memoryStoreAPI.CanonicalRecord) error {
		switch record.Kind {
		case memoryStoreAPI.RecordKindChunk:
			chunks = append(chunks, *record.Chunk)
		case memoryStoreAPI.RecordKindEntry:
			entries = append(entries, *record.Entry)
		case memoryStoreAPI.RecordKindLink:
			links = append(links, *record.Link)
		case memoryStoreAPI.RecordKindEvidence:
			evidence = append(evidence, *record.Evidence)
		}
		return nil
	}); err != nil {
		return err
	}
	counts := memoryStoreAPI.DeriveChunkCounts(chunks, entries, links, evidence)
	for _, chunk := range chunks {
		if chunk.Counts == counts[chunk.ID] {
			continue
		}
		chunk.Counts = counts[chunk.ID]
		if err := chunk.Validate(); err != nil {
			return err
		}
		if err := tx.putChunkProjection(chunk); err != nil {
			return err
		}
	}
	return nil
}

func (tx *transaction) putChunkProjection(value memory.Chunk) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode chunk projection %s: %w", value.ID, err)
	}
	if err := tx.batch.Set(chunkKey(string(value.ID)), encoded, nil); err != nil {
		return fmt.Errorf("put chunk projection %s: %w", value.ID, err)
	}
	return nil
}

func (tx *transaction) DeleteChunk(ctx context.Context, id memory.ChunkID, expectedRevision uint64) error {
	if err := tx.check(ctx, true); err != nil {
		return err
	}
	current, exists, err := currentChunk(ctx, tx, id)
	if err != nil {
		return err
	}
	if err := revision.CheckDelete("chunk", string(id), expectedRevision, current.Revision.Number, exists); err != nil {
		return err
	}
	if err := tx.replaceChunkIndexes(ctx, &current, nil); err != nil {
		return err
	}
	assetLower, assetUpper := prefixBounds(assetChunkPrefix(id))
	if assetUpper == nil {
		return fmt.Errorf("delete package assets for chunk %s: invalid prefix", id)
	}
	if err := tx.batch.DeleteRange(assetLower, assetUpper, nil); err != nil {
		return fmt.Errorf("delete package assets for chunk %s: %w", id, err)
	}
	tx.derivedDirty = true
	return tx.deleteRevisioned(chunkKey(string(id)), revisionPrefix(recordChunk, string(id)))
}

func optionalChunk(chunk memory.Chunk, exists bool) *memory.Chunk {
	if !exists {
		return nil
	}
	return &chunk
}

func (tx *transaction) replaceChunkIndexes(ctx context.Context, old, next *memory.Chunk) error {
	mutation := indexMutation{}
	if old != nil {
		entries, err := buildChunkIndexEntries(ctx, tx.indexes, *old)
		if err != nil {
			return err
		}
		for name, values := range entries {
			for _, entry := range values {
				if err := tx.batch.Delete(indexKey(tx.indexGeneration, name, entry.Suffix), nil); err != nil {
					return fmt.Errorf("delete memory index %s: %w", name, err)
				}
			}
		}
		mutation.delete = entries
	}
	if next != nil {
		entries, err := buildChunkIndexEntries(ctx, tx.indexes, *next)
		if err != nil {
			return err
		}
		for name, values := range entries {
			for _, entry := range values {
				if err := tx.batch.Set(indexKey(tx.indexGeneration, name, entry.Suffix), entry.Value, nil); err != nil {
					return fmt.Errorf("put memory index %s: %w", name, err)
				}
			}
		}
		mutation.put = entries
	}
	tx.indexMutations = append(tx.indexMutations, mutation)
	return nil
}

func (tx *transaction) PutEntry(ctx context.Context, value memory.Entry, expectedRevision uint64) error {
	if err := tx.check(ctx, true); err != nil {
		return err
	}
	if err := value.Validate(); err != nil {
		return err
	}
	current, exists, err := currentEntry(ctx, tx, value.ID)
	if err != nil {
		return err
	}
	if err := revision.CheckPut("entry", string(value.ID), expectedRevision, value.Revision.Number, current.Revision.Number, exists); err != nil {
		return err
	}
	if err := tx.replaceEntryIndexes(ctx, optionalEntry(current, exists), &value); err != nil {
		return err
	}
	tx.derivedDirty = true
	return tx.putRevisioned(entryKey(string(value.ID)), revisionKey(recordEntry, string(value.ID), value.Revision.Number), value)
}

func (tx *transaction) DeleteEntry(ctx context.Context, id memory.EntryID, expectedRevision uint64) error {
	if err := tx.check(ctx, true); err != nil {
		return err
	}
	current, exists, err := currentEntry(ctx, tx, id)
	if err != nil {
		return err
	}
	if err := revision.CheckDelete("entry", string(id), expectedRevision, current.Revision.Number, exists); err != nil {
		return err
	}
	if err := tx.replaceEntryIndexes(ctx, &current, nil); err != nil {
		return err
	}
	if err := tx.batch.Delete(entryUsageKey(id), nil); err != nil {
		return fmt.Errorf("delete entry usage: %w", err)
	}
	eventLower, eventUpper := prefixBounds(entryUsageEventEntryPrefix(id))
	if err := tx.batch.DeleteRange(eventLower, eventUpper, nil); err != nil {
		return fmt.Errorf("delete entry usage events: %w", err)
	}
	tx.derivedDirty = true
	return tx.deleteRevisioned(entryKey(string(id)), revisionPrefix(recordEntry, string(id)))
}

func optionalEntry(entry memory.Entry, exists bool) *memory.Entry {
	if !exists {
		return nil
	}
	return &entry
}

func (tx *transaction) replaceEntryIndexes(ctx context.Context, old, next *memory.Entry) error {
	mutation := indexMutation{}
	if old != nil {
		entries, err := buildEntryIndexEntries(ctx, tx.indexes, *old)
		if err != nil {
			return err
		}
		for name, values := range entries {
			for _, entry := range values {
				if err := tx.batch.Delete(indexKey(tx.indexGeneration, name, entry.Suffix), nil); err != nil {
					return fmt.Errorf("delete memory index %s: %w", name, err)
				}
			}
		}
		mutation.delete = entries
	}
	if next != nil {
		entries, err := buildEntryIndexEntries(ctx, tx.indexes, *next)
		if err != nil {
			return err
		}
		for name, values := range entries {
			for _, entry := range values {
				if err := tx.batch.Set(indexKey(tx.indexGeneration, name, entry.Suffix), entry.Value, nil); err != nil {
					return fmt.Errorf("put memory index %s: %w", name, err)
				}
			}
		}
		mutation.put = entries
	}
	tx.indexMutations = append(tx.indexMutations, mutation)
	return nil
}

func (tx *transaction) PutLink(ctx context.Context, value memory.Link, expectedRevision uint64) error {
	if err := tx.check(ctx, true); err != nil {
		return err
	}
	if err := value.Validate(); err != nil {
		return err
	}
	current, exists, err := currentLink(ctx, tx, value.ID)
	if err != nil {
		return err
	}
	if err := revision.CheckPut("link", string(value.ID), expectedRevision, value.Revision.Number, current.Revision.Number, exists); err != nil {
		return err
	}
	if equivalent, err := tx.EquivalentLink(ctx, value); err == nil && equivalent.ID != value.ID {
		return fmt.Errorf("%w: link %s duplicates %s", memoryStoreAPI.ErrConflict, value.ID, equivalent.ID)
	} else if err != nil && !errors.Is(err, memoryStoreAPI.ErrNotFound) {
		return err
	}
	if err := tx.updateLinkIndexes(ctx, optionalLink(current, exists), &value); err != nil {
		return err
	}
	tx.derivedDirty = true
	return tx.putRevisioned(linkKey(string(value.ID)), revisionKey(recordLink, string(value.ID), value.Revision.Number), value)
}

func (tx *transaction) DeleteLink(ctx context.Context, id memory.LinkID, expectedRevision uint64) error {
	if err := tx.check(ctx, true); err != nil {
		return err
	}
	current, exists, err := currentLink(ctx, tx, id)
	if err != nil {
		return err
	}
	if err := revision.CheckDelete("link", string(id), expectedRevision, current.Revision.Number, exists); err != nil {
		return err
	}
	if err := tx.updateLinkIndexes(ctx, &current, nil); err != nil {
		return err
	}
	tx.derivedDirty = true
	return tx.deleteRevisioned(linkKey(string(id)), revisionPrefix(recordLink, string(id)))
}

func optionalLink(value memory.Link, exists bool) *memory.Link {
	if !exists {
		return nil
	}
	return &value
}

func (tx *transaction) updateLinkIndexes(ctx context.Context, old, next *memory.Link) error {
	mutation := indexMutation{}
	if old != nil {
		entries, err := buildLinkIndexEntries(ctx, tx.indexes, *old)
		if err != nil {
			return err
		}
		for name, values := range entries {
			for _, item := range values {
				if err := tx.batch.Delete(indexKey(tx.indexGeneration, name, item.Suffix), nil); err != nil {
					return fmt.Errorf("delete memory index %s: %w", name, err)
				}
			}
		}
		mutation.delete = entries
	}
	if next != nil {
		entries, err := buildLinkIndexEntries(ctx, tx.indexes, *next)
		if err != nil {
			return err
		}
		for name, values := range entries {
			for _, item := range values {
				if err := tx.batch.Set(indexKey(tx.indexGeneration, name, item.Suffix), item.Value, nil); err != nil {
					return fmt.Errorf("put memory index %s: %w", name, err)
				}
			}
		}
		mutation.put = entries
	}
	tx.indexMutations = append(tx.indexMutations, mutation)
	return nil
}

func (tx *transaction) PutEvidence(ctx context.Context, value memory.Evidence) error {
	if err := tx.check(ctx, true); err != nil {
		return err
	}
	if err := value.Validate(); err != nil {
		return err
	}
	_, err := tx.Evidence(ctx, value.ID)
	switch {
	case err == nil:
		return fmt.Errorf("%w: evidence %s already exists", memoryStoreAPI.ErrConflict, value.ID)
	case !errors.Is(err, memoryStoreAPI.ErrNotFound):
		return err
	}
	if existing, err := tx.EvidenceBySource(ctx, value.Source.ID, value.Source.ContentHash); err == nil {
		return fmt.Errorf("%w: evidence source/hash already exists as %s", memoryStoreAPI.ErrConflict, existing.ID)
	} else if !errors.Is(err, memoryStoreAPI.ErrNotFound) {
		return err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode evidence %s: %w", value.ID, err)
	}
	if err := tx.batch.Set(evidenceKey(string(value.ID)), encoded, nil); err != nil {
		return fmt.Errorf("put evidence %s: %w", value.ID, err)
	}
	if err := tx.replaceEvidenceIndexes(ctx, nil, &value); err != nil {
		return err
	}
	tx.derivedDirty = true
	return nil
}

func (tx *transaction) DeleteEvidence(ctx context.Context, id memory.EvidenceID) error {
	if err := tx.check(ctx, true); err != nil {
		return err
	}
	current, err := tx.Evidence(ctx, id)
	if err != nil {
		return err
	}
	if err := tx.replaceEvidenceIndexes(ctx, &current, nil); err != nil {
		return err
	}
	if err := tx.batch.Delete(evidenceKey(string(id)), nil); err != nil {
		return fmt.Errorf("delete evidence %s: %w", id, err)
	}
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
	if _, err := tx.Chunk(ctx, value.ChunkID); err != nil {
		return err
	}
	if _, err := tx.Asset(ctx, value.ChunkID, value.Path); err == nil {
		return fmt.Errorf("%w: asset %s/%s already exists", memoryStoreAPI.ErrConflict, value.ChunkID, value.Path)
	} else if !errors.Is(err, memoryStoreAPI.ErrNotFound) {
		return err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode asset %s/%s: %w", value.ChunkID, value.Path, err)
	}
	if err := tx.batch.Set(assetKey(value.ChunkID, value.Path), encoded, nil); err != nil {
		return fmt.Errorf("put asset %s/%s: %w", value.ChunkID, value.Path, err)
	}
	return nil
}

func (tx *transaction) DeleteAsset(ctx context.Context, chunkID memory.ChunkID, path string) error {
	if err := tx.check(ctx, true); err != nil {
		return err
	}
	if _, err := tx.Asset(ctx, chunkID, path); err != nil {
		return err
	}
	if err := tx.batch.Delete(assetKey(chunkID, path), nil); err != nil {
		return fmt.Errorf("delete asset %s/%s: %w", chunkID, path, err)
	}
	return nil
}

func (tx *transaction) replaceEvidenceIndexes(ctx context.Context, old, next *memory.Evidence) error {
	mutation := indexMutation{}
	if old != nil {
		entries, err := buildEvidenceIndexEntries(ctx, tx.indexes, *old)
		if err != nil {
			return err
		}
		for name, values := range entries {
			for _, item := range values {
				if err := tx.batch.Delete(indexKey(tx.indexGeneration, name, item.Suffix), nil); err != nil {
					return fmt.Errorf("delete memory index %s: %w", name, err)
				}
			}
		}
		mutation.delete = entries
	}
	if next != nil {
		entries, err := buildEvidenceIndexEntries(ctx, tx.indexes, *next)
		if err != nil {
			return err
		}
		for name, values := range entries {
			for _, item := range values {
				if err := tx.batch.Set(indexKey(tx.indexGeneration, name, item.Suffix), item.Value, nil); err != nil {
					return fmt.Errorf("put memory index %s: %w", name, err)
				}
			}
		}
		mutation.put = entries
	}
	tx.indexMutations = append(tx.indexMutations, mutation)
	return nil
}

func (tx *transaction) putRevisioned(currentKey, historyKey []byte, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode memory record: %w", err)
	}
	if err := tx.batch.Set(currentKey, encoded, nil); err != nil {
		return fmt.Errorf("put canonical memory record: %w", err)
	}
	if err := tx.batch.Set(historyKey, encoded, nil); err != nil {
		return fmt.Errorf("put memory revision: %w", err)
	}
	return nil
}

func (tx *transaction) deleteRevisioned(currentKey, historyPrefix []byte) error {
	if err := tx.batch.Delete(currentKey, nil); err != nil {
		return fmt.Errorf("delete canonical memory record: %w", err)
	}
	lower, upper := prefixBounds(historyPrefix)
	if upper == nil {
		return fmt.Errorf("delete memory revisions: invalid unbounded prefix")
	}
	if err := tx.batch.DeleteRange(lower, upper, nil); err != nil {
		return fmt.Errorf("delete memory revisions: %w", err)
	}
	return nil
}

func currentChunk(ctx context.Context, tx *transaction, id memory.ChunkID) (memory.Chunk, bool, error) {
	value, err := tx.Chunk(ctx, id)
	if errors.Is(err, memoryStoreAPI.ErrNotFound) {
		return memory.Chunk{}, false, nil
	}
	return value, err == nil, err
}

func currentEntry(ctx context.Context, tx *transaction, id memory.EntryID) (memory.Entry, bool, error) {
	value, err := tx.Entry(ctx, id)
	if errors.Is(err, memoryStoreAPI.ErrNotFound) {
		return memory.Entry{}, false, nil
	}
	return value, err == nil, err
}

func currentLink(ctx context.Context, tx *transaction, id memory.LinkID) (memory.Link, bool, error) {
	value, err := tx.Link(ctx, id)
	if errors.Is(err, memoryStoreAPI.ErrNotFound) {
		return memory.Link{}, false, nil
	}
	return value, err == nil, err
}

type canonicalRecord interface {
	Validate() error
}

func readRecord[T canonicalRecord](reader recordReader, key []byte, kind, id string) (T, error) {
	var zero T
	data, closer, err := reader.Get(key)
	if errors.Is(err, cockroachpebble.ErrNotFound) {
		return zero, fmt.Errorf("%w: %s %s", memoryStoreAPI.ErrNotFound, kind, id)
	}
	if err != nil {
		return zero, fmt.Errorf("read %s %s: %w", kind, id, err)
	}
	defer func() { _ = closer.Close() }()
	return decodeRecord[T](data, kind, id)
}

func decodeRecord[T canonicalRecord](data []byte, kind, id string) (T, error) {
	var zero T
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var value T
	if err := decoder.Decode(&value); err != nil {
		return zero, fmt.Errorf("decode %s %s: %w", kind, id, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return zero, fmt.Errorf("decode %s %s: trailing data", kind, id)
	}
	if err := value.Validate(); err != nil {
		return zero, fmt.Errorf("validate stored %s %s: %w", kind, id, err)
	}
	return value, nil
}
