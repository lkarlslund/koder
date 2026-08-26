package pebble

import (
	"context"
	"errors"
	"testing"

	cockroachpebble "github.com/cockroachdb/pebble"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

func TestAdjacencyIndexesFollowLinkCreateUpdateDelete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	created := txLink()
	if err := s.Update(ctx, func(tx memoryStoreAPI.WriteTx) error { return tx.PutLink(ctx, created, 0) }); err != nil {
		t.Fatalf("create link: %v", err)
	}
	assertLinkAdjacencyIndexes(t, s, created, true)

	updated := created
	updated.Source, updated.Target = created.Target, created.Source
	updated.Revision = txRevision(2)
	updated.UpdatedAt = updated.Revision.CreatedAt
	if err := s.Update(ctx, func(tx memoryStoreAPI.WriteTx) error { return tx.PutLink(ctx, updated, 1) }); err != nil {
		t.Fatalf("update link: %v", err)
	}
	assertLinkAdjacencyIndexes(t, s, created, false)
	assertLinkAdjacencyIndexes(t, s, updated, true)

	if err := s.Update(ctx, func(tx memoryStoreAPI.WriteTx) error { return tx.DeleteLink(ctx, updated.ID, 2) }); err != nil {
		t.Fatalf("delete link: %v", err)
	}
	assertLinkAdjacencyIndexes(t, s, updated, false)
}

func TestAdjacencyIndexesRebuildFromCanonicalLinks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	link := txLink()
	if err := s.Update(ctx, func(tx memoryStoreAPI.WriteTx) error { return tx.PutLink(ctx, link, 0) }); err != nil {
		t.Fatalf("create link: %v", err)
	}
	lower, upper := prefixBounds(indexGenerationPrefix(initialIndexGeneration))
	if err := s.db.DeleteRange(lower, upper, cockroachpebble.Sync); err != nil {
		t.Fatalf("remove indexes: %v", err)
	}
	if err := s.RebuildIndexes(ctx); err != nil {
		t.Fatalf("RebuildIndexes(): %v", err)
	}
	assertLinkAdjacencyIndexes(t, s, link, true)
}

func assertLinkAdjacencyIndexes(t *testing.T, s *Store, link memory.Link, want bool) {
	t.Helper()
	definitions, err := validateIndexDefinitions(defaultLinkIndexDefinitions())
	if err != nil {
		t.Fatalf("validate link indexes: %v", err)
	}
	entries, err := buildLinkIndexEntries(context.Background(), definitions, link)
	if err != nil {
		t.Fatalf("build link indexes: %v", err)
	}
	for name, values := range entries {
		for _, item := range values {
			data, closer, err := s.db.Get(indexKey(s.meta.IndexGeneration, name, item.Suffix))
			if want {
				if err != nil {
					t.Errorf("index %s missing: %v", name, err)
					continue
				}
				if string(data) != string(link.ID) {
					t.Errorf("index %s value = %q, want %q", name, data, link.ID)
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
