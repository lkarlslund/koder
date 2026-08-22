package pebble

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	cockroachpebble "github.com/cockroachdb/pebble"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

func TestChunkIndexesAreMaintainedAcrossCreateUpdateDelete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	created := txChunk(1)
	created.Kind = knowledge.ChunkKindProject
	created.Scope = knowledge.Scope{Kind: knowledge.ScopeKindProject, Selector: "koder"}
	created.Tags = []string{"go", "pebble"}
	created.Locale = "en-US"
	created.LastUsedAt = txTime.Add(3 * time.Minute)
	created.ReviewAfter = txTime.Add(24 * time.Hour)
	if err := s.Update(ctx, func(tx knowledgeStore.WriteTx) error { return tx.PutChunk(ctx, created, 0) }); err != nil {
		t.Fatalf("create chunk: %v", err)
	}
	assertChunkIndexSet(t, s, created, true)
	if count := countIndexEntries(t, s, initialIndexGeneration); count != 11 {
		t.Fatalf("create index count = %d, want 11", count)
	}

	updated := txChunk(2)
	updated.Kind = knowledge.ChunkKindPersonal
	updated.Scope = knowledge.Scope{Kind: knowledge.ScopeKindPersonal, Selector: "me"}
	updated.Tags = []string{"preferences"}
	updated.Locale = "da-DK"
	updated.State = knowledge.ChunkStateArchived
	updated.LastUsedAt = txTime.Add(9 * time.Minute)
	updated.ReviewAfter = txTime.Add(48 * time.Hour)
	if err := s.Update(ctx, func(tx knowledgeStore.WriteTx) error { return tx.PutChunk(ctx, updated, 1) }); err != nil {
		t.Fatalf("update chunk: %v", err)
	}
	assertObsoleteChunkIndexesRemoved(t, s, created, updated)
	assertChunkIndexSet(t, s, updated, true)
	if count := countIndexEntries(t, s, initialIndexGeneration); count != 10 {
		t.Fatalf("update index count = %d, want 10", count)
	}

	if err := s.Update(ctx, func(tx knowledgeStore.WriteTx) error { return tx.DeleteChunk(ctx, updated.ID, 2) }); err != nil {
		t.Fatalf("delete chunk: %v", err)
	}
	assertChunkIndexSet(t, s, updated, false)
	if count := countIndexEntries(t, s, initialIndexGeneration); count != 0 {
		t.Fatalf("delete index count = %d, want 0", count)
	}
}

func TestDefaultChunkIndexesRebuildFromCanonicalRecords(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	chunk := txChunk(1)
	chunk.Tags = []string{"go", "storage"}
	if err := s.Update(ctx, func(tx knowledgeStore.WriteTx) error { return tx.PutChunk(ctx, chunk, 0) }); err != nil {
		t.Fatalf("create chunk: %v", err)
	}
	lower, upper := prefixBounds(indexGenerationPrefix(initialIndexGeneration))
	if err := s.db.DeleteRange(lower, upper, cockroachpebble.Sync); err != nil {
		t.Fatalf("remove derived indexes: %v", err)
	}
	if count := countIndexEntries(t, s, initialIndexGeneration); count != 0 {
		t.Fatalf("removed index count = %d, want 0", count)
	}
	if err := s.RebuildIndexes(ctx); err != nil {
		t.Fatalf("RebuildIndexes() error = %v", err)
	}
	assertChunkIndexSet(t, s, chunk, true)
	if count := countIndexEntries(t, s, initialIndexGeneration+1); count != 11 {
		t.Fatalf("rebuilt index count = %d, want 11", count)
	}
}

func TestIndexTupleEscapesBoundariesAndPreservesOrdering(t *testing.T) {
	t.Parallel()
	left := encodeIndexTuple("a\x00b", "same")
	right := encodeIndexTuple("a\x00c", "same")
	if !bytes.Contains(left, []byte{0, 0xff}) || bytes.Compare(left, right) >= 0 {
		t.Fatalf("encoded tuples do not escape or order correctly: %x, %x", left, right)
	}
	if bytes.Equal(encodeIndexTuple("a", "bc"), encodeIndexTuple("ab", "c")) {
		t.Fatal("tuple component boundaries collide")
	}
}

func assertChunkIndexSet(t *testing.T, s *Store, chunk knowledge.Chunk, want bool) {
	t.Helper()
	definitions, err := validateIndexDefinitions(defaultIndexDefinitions())
	if err != nil {
		t.Fatalf("validate default indexes: %v", err)
	}
	entries, err := buildChunkIndexEntries(context.Background(), definitions, chunk)
	if err != nil {
		t.Fatalf("build expected indexes: %v", err)
	}
	for name, values := range entries {
		for _, entry := range values {
			data, closer, err := s.db.Get(indexKey(s.meta.IndexGeneration, name, entry.Suffix))
			if want {
				if err != nil {
					t.Errorf("index %s missing: %v", name, err)
					continue
				}
				if string(data) != string(chunk.ID) {
					t.Errorf("index %s value = %q, want %q", name, data, chunk.ID)
				}
				_ = closer.Close()
				continue
			}
			if err == nil {
				_ = closer.Close()
				t.Errorf("obsolete index %s remains", name)
			} else if !errors.Is(err, cockroachpebble.ErrNotFound) {
				t.Errorf("read obsolete index %s: %v", name, err)
			}
		}
	}
}

func assertObsoleteChunkIndexesRemoved(t *testing.T, s *Store, old, next knowledge.Chunk) {
	t.Helper()
	definitions, err := validateIndexDefinitions(defaultIndexDefinitions())
	if err != nil {
		t.Fatalf("validate default indexes: %v", err)
	}
	oldEntries, err := buildChunkIndexEntries(context.Background(), definitions, old)
	if err != nil {
		t.Fatalf("build old indexes: %v", err)
	}
	nextEntries, err := buildChunkIndexEntries(context.Background(), definitions, next)
	if err != nil {
		t.Fatalf("build next indexes: %v", err)
	}
	retained := make(map[string]struct{})
	for name, values := range nextEntries {
		for _, entry := range values {
			retained[name+"\x00"+string(entry.Suffix)] = struct{}{}
		}
	}
	for name, values := range oldEntries {
		for _, entry := range values {
			if _, ok := retained[name+"\x00"+string(entry.Suffix)]; ok {
				continue
			}
			if _, closer, err := s.db.Get(indexKey(s.meta.IndexGeneration, name, entry.Suffix)); err == nil {
				_ = closer.Close()
				t.Errorf("obsolete index %s remains", name)
			} else if !errors.Is(err, cockroachpebble.ErrNotFound) {
				t.Errorf("read obsolete index %s: %v", name, err)
			}
		}
	}
}

func countIndexEntries(t *testing.T, s *Store, generation uint64) int {
	t.Helper()
	lower, upper := prefixBounds(indexGenerationPrefix(generation))
	iter, err := s.db.NewIter(&cockroachpebble.IterOptions{LowerBound: lower, UpperBound: upper})
	if err != nil {
		t.Fatalf("open index iterator: %v", err)
	}
	defer func() { _ = iter.Close() }()
	count := 0
	for iter.First(); iter.Valid(); iter.Next() {
		count++
	}
	if err := iter.Error(); err != nil {
		t.Fatalf("iterate indexes: %v", err)
	}
	return count
}
