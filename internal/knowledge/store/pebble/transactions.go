package pebble

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	cockroachpebble "github.com/cockroachdb/pebble"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
	"github.com/lkarlslund/koder/internal/knowledge/store/internal/revision"
)

type recordReader interface {
	Get([]byte) ([]byte, io.Closer, error)
}

type transaction struct {
	reader   recordReader
	batch    *cockroachpebble.Batch
	active   bool
	writable bool
}

var _ knowledgeStore.Store = (*Store)(nil)
var _ knowledgeStore.WriteTx = (*transaction)(nil)

// View runs fn against a stable Pebble snapshot.
func (s *Store) View(ctx context.Context, fn func(knowledgeStore.ReadTx) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if fn == nil {
		return fmt.Errorf("view knowledge pebble: callback is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return knowledgeStore.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	snapshot := s.db.NewSnapshot()
	defer func() { _ = snapshot.Close() }()
	tx := &transaction{reader: snapshot, active: true}
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
func (s *Store) Update(ctx context.Context, fn func(knowledgeStore.WriteTx) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if fn == nil {
		return fmt.Errorf("update knowledge pebble: callback is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return knowledgeStore.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	batch := s.db.NewIndexedBatch()
	defer func() { _ = batch.Close() }()
	tx := &transaction{reader: batch, batch: batch, active: true, writable: true}
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
	if err := batch.Commit(cockroachpebble.Sync); err != nil {
		return fmt.Errorf("commit knowledge transaction: %w", err)
	}
	return nil
}

// Checkpoint remains unavailable until the independent backup/restore contract is added.
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

func (tx *transaction) check(ctx context.Context, write bool) error {
	if !tx.active || tx.reader == nil {
		return knowledgeStore.ErrClosed
	}
	if write && (!tx.writable || tx.batch == nil) {
		return knowledgeStore.ErrReadOnly
	}
	return ctx.Err()
}

func (tx *transaction) Chunk(ctx context.Context, id knowledge.ChunkID) (knowledge.Chunk, error) {
	if err := tx.check(ctx, false); err != nil {
		return knowledge.Chunk{}, err
	}
	return readRecord[knowledge.Chunk](tx.reader, chunkKey(string(id)), "chunk", string(id))
}

func (tx *transaction) Entry(ctx context.Context, id knowledge.EntryID) (knowledge.Entry, error) {
	if err := tx.check(ctx, false); err != nil {
		return knowledge.Entry{}, err
	}
	return readRecord[knowledge.Entry](tx.reader, entryKey(string(id)), "entry", string(id))
}

func (tx *transaction) Link(ctx context.Context, id knowledge.LinkID) (knowledge.Link, error) {
	if err := tx.check(ctx, false); err != nil {
		return knowledge.Link{}, err
	}
	return readRecord[knowledge.Link](tx.reader, linkKey(string(id)), "link", string(id))
}

func (tx *transaction) Evidence(ctx context.Context, id knowledge.EvidenceID) (knowledge.Evidence, error) {
	if err := tx.check(ctx, false); err != nil {
		return knowledge.Evidence{}, err
	}
	return readRecord[knowledge.Evidence](tx.reader, evidenceKey(string(id)), "evidence", string(id))
}

func (tx *transaction) PutChunk(ctx context.Context, value knowledge.Chunk, expectedRevision uint64) error {
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
	return tx.putRevisioned(chunkKey(string(value.ID)), revisionKey(recordChunk, string(value.ID), value.Revision.Number), value)
}

func (tx *transaction) DeleteChunk(ctx context.Context, id knowledge.ChunkID, expectedRevision uint64) error {
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
	return tx.deleteRevisioned(chunkKey(string(id)), revisionPrefix(recordChunk, string(id)))
}

func (tx *transaction) PutEntry(ctx context.Context, value knowledge.Entry, expectedRevision uint64) error {
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
	return tx.putRevisioned(entryKey(string(value.ID)), revisionKey(recordEntry, string(value.ID), value.Revision.Number), value)
}

func (tx *transaction) DeleteEntry(ctx context.Context, id knowledge.EntryID, expectedRevision uint64) error {
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
	return tx.deleteRevisioned(entryKey(string(id)), revisionPrefix(recordEntry, string(id)))
}

func (tx *transaction) PutLink(ctx context.Context, value knowledge.Link, expectedRevision uint64) error {
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
	return tx.putRevisioned(linkKey(string(value.ID)), revisionKey(recordLink, string(value.ID), value.Revision.Number), value)
}

func (tx *transaction) DeleteLink(ctx context.Context, id knowledge.LinkID, expectedRevision uint64) error {
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
	return tx.deleteRevisioned(linkKey(string(id)), revisionPrefix(recordLink, string(id)))
}

func (tx *transaction) PutEvidence(ctx context.Context, value knowledge.Evidence) error {
	if err := tx.check(ctx, true); err != nil {
		return err
	}
	if err := value.Validate(); err != nil {
		return err
	}
	_, err := tx.Evidence(ctx, value.ID)
	switch {
	case err == nil:
		return fmt.Errorf("%w: evidence %s already exists", knowledgeStore.ErrConflict, value.ID)
	case !errors.Is(err, knowledgeStore.ErrNotFound):
		return err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode evidence %s: %w", value.ID, err)
	}
	if err := tx.batch.Set(evidenceKey(string(value.ID)), encoded, nil); err != nil {
		return fmt.Errorf("put evidence %s: %w", value.ID, err)
	}
	return nil
}

func (tx *transaction) DeleteEvidence(ctx context.Context, id knowledge.EvidenceID) error {
	if err := tx.check(ctx, true); err != nil {
		return err
	}
	if _, err := tx.Evidence(ctx, id); err != nil {
		return err
	}
	if err := tx.batch.Delete(evidenceKey(string(id)), nil); err != nil {
		return fmt.Errorf("delete evidence %s: %w", id, err)
	}
	return nil
}

func (tx *transaction) putRevisioned(currentKey, historyKey []byte, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode knowledge record: %w", err)
	}
	if err := tx.batch.Set(currentKey, encoded, nil); err != nil {
		return fmt.Errorf("put canonical knowledge record: %w", err)
	}
	if err := tx.batch.Set(historyKey, encoded, nil); err != nil {
		return fmt.Errorf("put knowledge revision: %w", err)
	}
	return nil
}

func (tx *transaction) deleteRevisioned(currentKey, historyPrefix []byte) error {
	if err := tx.batch.Delete(currentKey, nil); err != nil {
		return fmt.Errorf("delete canonical knowledge record: %w", err)
	}
	lower, upper := prefixBounds(historyPrefix)
	if upper == nil {
		return fmt.Errorf("delete knowledge revisions: invalid unbounded prefix")
	}
	if err := tx.batch.DeleteRange(lower, upper, nil); err != nil {
		return fmt.Errorf("delete knowledge revisions: %w", err)
	}
	return nil
}

func currentChunk(ctx context.Context, tx *transaction, id knowledge.ChunkID) (knowledge.Chunk, bool, error) {
	value, err := tx.Chunk(ctx, id)
	if errors.Is(err, knowledgeStore.ErrNotFound) {
		return knowledge.Chunk{}, false, nil
	}
	return value, err == nil, err
}

func currentEntry(ctx context.Context, tx *transaction, id knowledge.EntryID) (knowledge.Entry, bool, error) {
	value, err := tx.Entry(ctx, id)
	if errors.Is(err, knowledgeStore.ErrNotFound) {
		return knowledge.Entry{}, false, nil
	}
	return value, err == nil, err
}

func currentLink(ctx context.Context, tx *transaction, id knowledge.LinkID) (knowledge.Link, bool, error) {
	value, err := tx.Link(ctx, id)
	if errors.Is(err, knowledgeStore.ErrNotFound) {
		return knowledge.Link{}, false, nil
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
		return zero, fmt.Errorf("%w: %s %s", knowledgeStore.ErrNotFound, kind, id)
	}
	if err != nil {
		return zero, fmt.Errorf("read %s %s: %w", kind, id, err)
	}
	defer func() { _ = closer.Close() }()
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
