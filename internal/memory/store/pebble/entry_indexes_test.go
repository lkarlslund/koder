package pebble

import (
	"context"
	"errors"
	"testing"
	"time"

	cockroachpebble "github.com/cockroachdb/pebble"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

func TestEntryIndexesAreMaintainedAcrossCreateUpdateDelete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	created := indexedEntry(1)
	if err := s.Update(ctx, func(tx memoryStoreAPI.WriteTx) error { return tx.PutEntry(ctx, created, 0) }); err != nil {
		t.Fatalf("create entry: %v", err)
	}
	assertEntryIndexSet(t, s, created, true)
	if count := countIndexEntries(t, s, initialIndexGeneration); count != 23 {
		t.Fatalf("create index count = %d, want 23", count)
	}

	updated := indexedEntry(2)
	updated.Title = "Updated"
	updated.Kind = memory.EntryKindPreference
	updated.Scope = memory.Scope{Kind: memory.ScopeKindPersonal, Selector: "me"}
	updated.PersonalOrigin = memory.PersonalOriginExplicit
	updated.State = memory.EntryStateArchived
	updated.Aliases = []string{"New alias"}
	updated.Tags = []string{"personal"}
	updated.Applicability.Locales = []string{"da-DK"}
	updated.LastUsedAt = txTime.Add(9 * time.Minute)
	updated.ReviewAfter = txTime.Add(48 * time.Hour)
	if err := s.Update(ctx, func(tx memoryStoreAPI.WriteTx) error { return tx.PutEntry(ctx, updated, 1) }); err != nil {
		t.Fatalf("update entry: %v", err)
	}
	assertObsoleteEntryIndexesRemoved(t, s, created, updated)
	assertEntryIndexSet(t, s, updated, true)
	if count := countIndexEntries(t, s, initialIndexGeneration); count != 18 {
		t.Fatalf("update index count = %d, want 18", count)
	}

	if err := s.Update(ctx, func(tx memoryStoreAPI.WriteTx) error { return tx.DeleteEntry(ctx, updated.ID, 2) }); err != nil {
		t.Fatalf("delete entry: %v", err)
	}
	assertEntryIndexSet(t, s, updated, false)
	if count := countIndexEntries(t, s, initialIndexGeneration); count != 0 {
		t.Fatalf("delete index count = %d, want 0", count)
	}
}

func TestDefaultEntryIndexesRebuildFromCanonicalRecords(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	entry := indexedEntry(1)
	if err := s.Update(ctx, func(tx memoryStoreAPI.WriteTx) error { return tx.PutEntry(ctx, entry, 0) }); err != nil {
		t.Fatalf("create entry: %v", err)
	}
	lower, upper := prefixBounds(indexGenerationPrefix(initialIndexGeneration))
	if err := s.db.DeleteRange(lower, upper, cockroachpebble.Sync); err != nil {
		t.Fatalf("remove derived indexes: %v", err)
	}
	if err := s.RebuildIndexes(ctx); err != nil {
		t.Fatalf("RebuildIndexes() error = %v", err)
	}
	assertEntryIndexSet(t, s, entry, true)
	if count := countIndexEntries(t, s, initialIndexGeneration+1); count != 23 {
		t.Fatalf("rebuilt index count = %d, want 23", count)
	}
}

func indexedEntry(number uint64) memory.Entry {
	entry := txEntry()
	entry.Revision = txRevision(number)
	entry.UpdatedAt = entry.Revision.CreatedAt
	entry.Title = "Command line"
	entry.Aliases = []string{"CLI", "Shell command"}
	entry.Tags = []string{"go", "tools"}
	entry.Applicability.Locales = []string{"da-DK", "en-US"}
	entry.LastUsedAt = txTime.Add(3 * time.Minute)
	entry.ReviewAfter = txTime.Add(24 * time.Hour)
	entry.ValidFrom = txTime.Add(-24 * time.Hour)
	entry.ValidUntil = txTime.Add(7 * 24 * time.Hour)
	return entry
}

func assertEntryIndexSet(t *testing.T, s *Store, entry memory.Entry, want bool) {
	t.Helper()
	definitions, err := validateIndexDefinitions(defaultEntryIndexDefinitions())
	if err != nil {
		t.Fatalf("validate entry indexes: %v", err)
	}
	entries, err := buildEntryIndexEntries(context.Background(), definitions, entry)
	if err != nil {
		t.Fatalf("build expected indexes: %v", err)
	}
	for name, values := range entries {
		for _, item := range values {
			data, closer, err := s.db.Get(indexKey(s.meta.IndexGeneration, name, item.Suffix))
			if want {
				if err != nil {
					t.Errorf("index %s missing: %v", name, err)
					continue
				}
				valueID := string(data)
				if name == entryLexicalIndex {
					posting, decodeErr := decodeLexicalPosting(data)
					if decodeErr != nil {
						t.Errorf("decode index %s value: %v", name, decodeErr)
						_ = closer.Close()
						continue
					}
					valueID = string(posting.EntryID)
				}
				if valueID != string(entry.ID) {
					t.Errorf("index %s entry ID = %q, want %q", name, valueID, entry.ID)
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

func assertObsoleteEntryIndexesRemoved(t *testing.T, s *Store, old, next memory.Entry) {
	t.Helper()
	definitions, err := validateIndexDefinitions(defaultEntryIndexDefinitions())
	if err != nil {
		t.Fatalf("validate entry indexes: %v", err)
	}
	oldEntries, _ := buildEntryIndexEntries(context.Background(), definitions, old)
	nextEntries, _ := buildEntryIndexEntries(context.Background(), definitions, next)
	retained := make(map[string]struct{})
	for name, values := range nextEntries {
		for _, item := range values {
			retained[name+"\x00"+string(item.Suffix)] = struct{}{}
		}
	}
	for name, values := range oldEntries {
		for _, item := range values {
			if _, ok := retained[name+"\x00"+string(item.Suffix)]; ok {
				continue
			}
			if _, closer, err := s.db.Get(indexKey(s.meta.IndexGeneration, name, item.Suffix)); err == nil {
				_ = closer.Close()
				t.Errorf("obsolete index %s remains", name)
			} else if !errors.Is(err, cockroachpebble.ErrNotFound) {
				t.Errorf("read obsolete index %s: %v", name, err)
			}
		}
	}
}
