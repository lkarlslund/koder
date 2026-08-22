package pebble

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	cockroachpebble "github.com/cockroachdb/pebble"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

func TestScanCanonicalReadsValidatedRecordsInStableOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSeededMaintenanceStore(t, ctx)
	var got []string
	stats, err := s.ScanCanonical(ctx, func(record knowledgeStore.CanonicalRecord) error {
		if err := record.Validate(); err != nil {
			return err
		}
		got = append(got, fmt.Sprintf("%s/%s", record.Kind, record.ID()))
		return nil
	})
	if err != nil {
		t.Fatalf("ScanCanonical() error = %v", err)
	}
	want := []string{
		"chunk/" + string(txChunkID),
		"entry/" + string(txEntryID),
		"link/" + string(txLinkID),
		"evidence/" + string(txEvidenceID),
	}
	if len(got) != len(want) {
		t.Fatalf("ScanCanonical() = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("ScanCanonical() = %v, want %v", got, want)
		}
	}
	if stats.Total != 4 || stats.Chunks != 1 || stats.Entries != 1 || stats.Links != 1 || stats.Evidence != 1 {
		t.Fatalf("ScanCanonical() stats = %#v", stats)
	}
}

func TestRebuildIndexesSwitchesGenerationAfterCompleteBuild(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSeededMaintenanceStore(t, ctx)
	s.indexes = []indexDefinition{{
		name: "object-kind",
		build: func(_ context.Context, record knowledgeStore.CanonicalRecord) ([]indexEntry, error) {
			return []indexEntry{{Suffix: []byte(record.ID()), Value: []byte(record.Kind)}}, nil
		},
	}}

	if err := s.RebuildIndexes(ctx); err != nil {
		t.Fatalf("RebuildIndexes() error = %v", err)
	}
	status, err := s.IndexRebuildStatus(ctx)
	if err != nil {
		t.Fatalf("IndexRebuildStatus() error = %v", err)
	}
	if status.Running || status.ActiveGeneration != 2 || status.TargetGeneration != 0 || status.Scanned.Total != 4 || status.CompletedAt.IsZero() {
		t.Fatalf("IndexRebuildStatus() = %#v", status)
	}
	health, err := s.Health(ctx)
	if err != nil || health.IndexGeneration != 2 {
		t.Fatalf("Health() = %#v, %v", health, err)
	}
	assertIndexValue(t, s, 2, "object-kind", string(txChunkID), "chunk")

	if err := s.RebuildIndexes(ctx); err != nil {
		t.Fatalf("second RebuildIndexes() error = %v", err)
	}
	status, _ = s.IndexRebuildStatus(ctx)
	if status.ActiveGeneration != 3 {
		t.Fatalf("second rebuild status = %#v", status)
	}
	if _, closer, err := s.db.Get(indexKey(2, "object-kind", []byte(txChunkID))); !errors.Is(err, cockroachpebble.ErrNotFound) {
		if err == nil {
			_ = closer.Close()
		}
		t.Fatalf("retired generation lookup error = %v, want ErrNotFound", err)
	}
	assertIndexValue(t, s, 3, "object-kind", string(txChunkID), "chunk")
}

func TestFailedIndexRebuildDoesNotActivateTargetGeneration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSeededMaintenanceStore(t, ctx)
	wantErr := errors.New("builder failed")
	s.indexes = []indexDefinition{{
		name: "failing",
		build: func(_ context.Context, record knowledgeStore.CanonicalRecord) ([]indexEntry, error) {
			if record.Kind == knowledgeStore.RecordKindEntry {
				return nil, wantErr
			}
			return []indexEntry{{Suffix: []byte(record.ID()), Value: []byte("partial")}}, nil
		},
	}}
	if err := s.RebuildIndexes(ctx); !errors.Is(err, wantErr) {
		t.Fatalf("RebuildIndexes() error = %v, want builder error", err)
	}
	if s.meta.IndexGeneration != initialIndexGeneration {
		t.Fatalf("failed rebuild activated generation %d", s.meta.IndexGeneration)
	}
	status, _ := s.IndexRebuildStatus(ctx)
	if status.Running || status.ActiveGeneration != initialIndexGeneration || status.TargetGeneration != 2 || status.LastError == "" {
		t.Fatalf("failed rebuild status = %#v", status)
	}

	s.indexes = nil
	if err := s.RebuildIndexes(ctx); err != nil {
		t.Fatalf("rebuild after failure error = %v", err)
	}
	if s.meta.IndexGeneration != 2 {
		t.Fatalf("rebuild after failure generation = %d", s.meta.IndexGeneration)
	}
	if _, closer, err := s.db.Get(indexKey(2, "failing", []byte(txChunkID))); !errors.Is(err, cockroachpebble.ErrNotFound) {
		if err == nil {
			_ = closer.Close()
		}
		t.Fatalf("failed target data survived retry: %v", err)
	}
}

func TestRebuildIndexesKeepsReadsAndWritesOnlineAndReplaysMutations(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSeededMaintenanceStore(t, ctx)
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	s.indexes = append(s.indexes, indexDefinition{
		name: "blocking-online-test",
		build: func(ctx context.Context, record knowledgeStore.CanonicalRecord) ([]indexEntry, error) {
			if record.Kind != knowledgeStore.RecordKindChunk {
				return nil, nil
			}
			select {
			case <-started:
			default:
				close(started)
			}
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return []indexEntry{{Suffix: []byte(record.ID()), Value: []byte(record.ID())}}, nil
		},
	})

	rebuildDone := make(chan error, 1)
	go func() { rebuildDone <- s.RebuildIndexes(ctx) }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("index rebuild did not reach blocking builder")
	}

	readDone := make(chan error, 1)
	go func() {
		_, err := s.ListEntries(ctx, knowledgeStore.EntryListRequest{})
		readDone <- err
	}()
	select {
	case err := <-readDone:
		if err != nil {
			t.Fatalf("ListEntries() during rebuild error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("read blocked while inactive index generation was building")
	}

	updated := txEntry()
	updated.Title = "Online journal record"
	updated.Body = "onlinejournalterm"
	updated.Revision = txRevision(2)
	updated.UpdatedAt = txTime.Add(time.Second)
	writeDone := make(chan error, 1)
	go func() {
		writeDone <- s.Update(ctx, func(tx knowledgeStore.WriteTx) error {
			return tx.PutEntry(ctx, updated, 1)
		})
	}()
	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatalf("Update() during rebuild error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("write blocked while inactive index generation was building")
	}

	unblock()
	if err := <-rebuildDone; err != nil {
		t.Fatalf("RebuildIndexes() error = %v", err)
	}
	health, err := s.Health(ctx)
	if err != nil || health.IndexGeneration != initialIndexGeneration+1 {
		t.Fatalf("Health() after rebuild = %#v, %v", health, err)
	}
	assertLexicalEntryIDs(t, s, []string{"onlinejournalterm"}, txEntryID)
	assertLexicalEntryIDs(t, s, []string{"entry"})
	if _, closer, err := s.db.Get(indexKey(initialIndexGeneration, entryLexicalIndex, encodeIndexTuple("entry", string(txEntryID)))); !errors.Is(err, cockroachpebble.ErrNotFound) {
		if err == nil {
			_ = closer.Close()
		}
		t.Fatalf("retired lexical generation lookup error = %v, want ErrNotFound", err)
	}
}

func assertLexicalEntryIDs(t *testing.T, s *Store, terms []string, want ...knowledge.EntryID) {
	t.Helper()
	page, err := s.LookupLexicalPostings(context.Background(), knowledgeStore.LexicalPostingRequest{Terms: terms})
	if err != nil {
		t.Fatalf("LookupLexicalPostings(%v) error = %v", terms, err)
	}
	got := make([]knowledge.EntryID, 0, len(page.Postings))
	for _, posting := range page.Postings {
		got = append(got, posting.EntryID)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("LookupLexicalPostings(%v) entries = %v, want %v", terms, got, want)
	}
}

func TestRebuildGenerationPersistsAcrossReopen(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	stateDir := t.TempDir()
	s, err := Open(stateDir)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := s.RebuildIndexes(ctx); err != nil {
		t.Fatalf("RebuildIndexes() error = %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	reopened, err := Open(stateDir)
	if err != nil {
		t.Fatalf("reopen error = %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	status, err := reopened.IndexRebuildStatus(ctx)
	if err != nil || status.ActiveGeneration != initialIndexGeneration+1 {
		t.Fatalf("reopened IndexRebuildStatus() = %#v, %v", status, err)
	}
}

func TestCanonicalMaintenanceHonorsCancellationAndCorruption(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSeededMaintenanceStore(t, ctx)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.ScanCanonical(canceled, func(knowledgeStore.CanonicalRecord) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("ScanCanonical(canceled) error = %v", err)
	}
	if err := s.RebuildIndexes(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("RebuildIndexes(canceled) error = %v", err)
	}

	if err := s.db.Set(chunkKey(string(txChunkID)), []byte(`{"invalid":true}`), cockroachpebble.Sync); err != nil {
		t.Fatalf("corrupt chunk: %v", err)
	}
	if _, err := s.ScanCanonical(ctx, func(knowledgeStore.CanonicalRecord) error { return nil }); err == nil {
		t.Fatal("ScanCanonical(corrupt record) unexpectedly succeeded")
	}
}

func openSeededMaintenanceStore(t *testing.T, ctx context.Context) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Update(ctx, func(tx knowledgeStore.WriteTx) error {
		if err := tx.PutChunk(ctx, txChunk(1), 0); err != nil {
			return err
		}
		if err := tx.PutEntry(ctx, txEntry(), 0); err != nil {
			return err
		}
		if err := tx.PutLink(ctx, txLink(), 0); err != nil {
			return err
		}
		return tx.PutEvidence(ctx, txEvidence())
	}); err != nil {
		t.Fatalf("seed store: %v", err)
	}
	return s
}

func assertIndexValue(t *testing.T, s *Store, generation uint64, name, suffix, want string) {
	t.Helper()
	data, closer, err := s.db.Get(indexKey(generation, name, []byte(suffix)))
	if err != nil {
		t.Fatalf("read index value: %v", err)
	}
	defer func() { _ = closer.Close() }()
	if string(data) != want {
		t.Fatalf("index value = %q, want %q", data, want)
	}
}
