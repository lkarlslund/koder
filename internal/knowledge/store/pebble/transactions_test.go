package pebble

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	cockroachpebble "github.com/cockroachdb/pebble"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

const (
	txChunkID    knowledge.ChunkID    = "019f132e-4f3a-739a-9ab2-5198dcd19e67"
	txEntryID    knowledge.EntryID    = "01a01f76-1ff6-7c1d-967a-66ad5703dd33"
	txOtherEntry knowledge.EntryID    = "01a01f76-1ff6-7c1d-967a-66ad5703dd34"
	txLinkID     knowledge.LinkID     = "01a020a6-84d5-7b03-a995-bb2cfb4528b0"
	txEvidenceID knowledge.EvidenceID = "01a01688-fc6b-7a53-a907-4f903461820e"
)

var txTime = time.Date(2026, 8, 22, 15, 0, 0, 0, time.UTC)

func txRevision(number uint64) knowledge.Revision {
	return knowledge.Revision{
		Number:    number,
		ID:        knowledge.RevisionID(fmt.Sprintf("01a01688-fc5d-7f7d-8bb8-%012x", number)),
		Actor:     knowledge.Actor{Kind: knowledge.ActorKindSystem, ID: "test"},
		CreatedAt: txTime.Add(time.Duration(number-1) * time.Second),
	}
}

func txChunk(number uint64) knowledge.Chunk {
	return knowledge.Chunk{
		ID: txChunkID, Title: fmt.Sprintf("Chunk r%d", number), Kind: knowledge.ChunkKindReference,
		Scope: knowledge.Scope{Kind: knowledge.ScopeKindGlobal}, Visibility: knowledge.VisibilityInstallation,
		State: knowledge.ChunkStateActive, SchemaVersion: 1, Revision: txRevision(number),
		CreatedAt: txTime, UpdatedAt: txTime.Add(time.Duration(number-1) * time.Second),
	}
}

func txEntry() knowledge.Entry {
	return knowledge.Entry{
		ID: txEntryID, ChunkID: txChunkID, Kind: knowledge.EntryKindFact, Title: "Entry",
		Scope:        knowledge.Scope{Kind: knowledge.ScopeKindGlobal},
		Verification: knowledge.Verification{Status: knowledge.VerificationStatusUnverified},
		State:        knowledge.EntryStateActive, Revision: txRevision(1), CreatedAt: txTime, UpdatedAt: txTime,
	}
}

func txLink() knowledge.Link {
	return knowledge.Link{
		ID:     txLinkID,
		Source: knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(txEntryID)},
		Target: knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(txOtherEntry)},
		Kind:   knowledge.LinkKindRelatedTo, State: knowledge.LinkStateActive,
		Revision: txRevision(1), CreatedAt: txTime, UpdatedAt: txTime,
	}
}

func txEvidence() knowledge.Evidence {
	return knowledge.Evidence{
		ID: txEvidenceID, Type: knowledge.EvidenceTypeObservation, Quality: knowledge.EvidenceQualityPrimary,
		Source: knowledge.Source{ID: "observation:test"},
		Actor:  knowledge.Actor{Kind: knowledge.ActorKindSystem, ID: "test"}, CreatedAt: txTime,
	}
}

func TestTransactionCommitsAtomicallyAndDurably(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	stateDir := t.TempDir()
	s, err := Open(stateDir)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
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
		if err := tx.PutEvidence(ctx, txEvidence()); err != nil {
			return err
		}
		got, err := tx.Chunk(ctx, txChunkID)
		if err != nil {
			return err
		}
		if got.Revision.Number != 1 {
			return fmt.Errorf("read-your-writes revision = %d", got.Revision.Number)
		}
		return nil
	}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := Open(stateDir)
	if err != nil {
		t.Fatalf("reopen error = %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if err := expectAllRecords(ctx, reopened); err != nil {
		t.Fatalf("durable records: %v", err)
	}
}

func TestTransactionCallbackErrorLeavesNoPartialRecords(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	stateDir := t.TempDir()
	s, err := Open(stateDir)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	wantErr := errors.New("abort transaction")
	if err := s.Update(ctx, func(tx knowledgeStore.WriteTx) error {
		if err := tx.PutChunk(ctx, txChunk(1), 0); err != nil {
			return err
		}
		if err := tx.PutEvidence(ctx, txEvidence()); err != nil {
			return err
		}
		return wantErr
	}); !errors.Is(err, wantErr) {
		t.Fatalf("Update() error = %v, want %v", err, wantErr)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	reopened, err := Open(stateDir)
	if err != nil {
		t.Fatalf("reopen error = %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if err := reopened.View(ctx, func(tx knowledgeStore.ReadTx) error {
		if _, err := tx.Chunk(ctx, txChunkID); !errors.Is(err, knowledgeStore.ErrNotFound) {
			return fmt.Errorf("chunk error = %v, want ErrNotFound", err)
		}
		if _, err := tx.Evidence(ctx, txEvidenceID); !errors.Is(err, knowledgeStore.ErrNotFound) {
			return fmt.Errorf("evidence error = %v, want ErrNotFound", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if count := countIndexEntries(t, reopened, initialIndexGeneration); count != 0 {
		t.Fatalf("aborted transaction left %d derived index entries", count)
	}
}

func TestTransactionCancellationLeavesNoPartialRecords(t *testing.T) {
	t.Parallel()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	if err := s.Update(ctx, func(tx knowledgeStore.WriteTx) error {
		if err := tx.PutChunk(ctx, txChunk(1), 0); err != nil {
			return err
		}
		cancel()
		return nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Update() error = %v, want context.Canceled", err)
	}
	if err := s.View(context.Background(), func(tx knowledgeStore.ReadTx) error {
		_, err := tx.Chunk(context.Background(), txChunkID)
		return err
	}); !errors.Is(err, knowledgeStore.ErrNotFound) {
		t.Fatalf("View() error = %v, want ErrNotFound", err)
	}
}

func TestTransactionEnforcesRevisionPreconditions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Update(ctx, func(tx knowledgeStore.WriteTx) error { return tx.PutChunk(ctx, txChunk(1), 0) }); err != nil {
		t.Fatalf("create: %v", err)
	}
	for name, mutate := range map[string]func(knowledgeStore.WriteTx) error{
		"duplicate create": func(tx knowledgeStore.WriteTx) error { return tx.PutChunk(ctx, txChunk(1), 0) },
		"stale expected":   func(tx knowledgeStore.WriteTx) error { return tx.PutChunk(ctx, txChunk(2), 2) },
		"skipped revision": func(tx knowledgeStore.WriteTx) error { return tx.PutChunk(ctx, txChunk(3), 1) },
		"zero delete":      func(tx knowledgeStore.WriteTx) error { return tx.DeleteChunk(ctx, txChunkID, 0) },
	} {
		t.Run(name, func(t *testing.T) {
			if err := s.Update(ctx, mutate); !errors.Is(err, knowledgeStore.ErrConflict) {
				t.Fatalf("Update() error = %v, want ErrConflict", err)
			}
		})
	}
	if err := s.Update(ctx, func(tx knowledgeStore.WriteTx) error { return tx.PutChunk(ctx, txChunk(2), 1) }); err != nil {
		t.Fatalf("valid update: %v", err)
	}
}

func TestTransactionDeleteRemovesCanonicalAndRevisionHistory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Update(ctx, func(tx knowledgeStore.WriteTx) error { return tx.PutChunk(ctx, txChunk(1), 0) }); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.Update(ctx, func(tx knowledgeStore.WriteTx) error { return tx.PutChunk(ctx, txChunk(2), 1) }); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := s.Update(ctx, func(tx knowledgeStore.WriteTx) error { return tx.DeleteChunk(ctx, txChunkID, 2) }); err != nil {
		t.Fatalf("delete: %v", err)
	}
	for _, key := range [][]byte{
		chunkKey(string(txChunkID)),
		revisionKey(recordChunk, string(txChunkID), 1),
		revisionKey(recordChunk, string(txChunkID), 2),
	} {
		_, closer, err := s.db.Get(key)
		if err == nil {
			_ = closer.Close()
			t.Errorf("deleted key %x remains", key)
		} else if !errors.Is(err, cockroachpebble.ErrNotFound) {
			t.Errorf("Get(%x) error = %v", key, err)
		}
	}
}

func TestChunkDeletionBlockersSeeCanonicalRecordsAndPendingWrites(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	dependentID := knowledge.ChunkID("019f132e-4f3a-739a-9ab2-5198dcd19e68")
	dependencyID := knowledge.ChunkID("019f132e-4f3a-739a-9ab2-5198dcd19e69")
	root := txChunk(1)
	root.DependencyIDs = []knowledge.ChunkID{dependencyID}
	dependent := txChunk(1)
	dependent.ID = dependentID
	dependent.Title = "Dependent"
	dependent.DependencyIDs = []knowledge.ChunkID{txChunkID}
	if err := s.Update(ctx, func(tx knowledgeStore.WriteTx) error {
		if err := tx.PutChunk(ctx, root, 0); err != nil {
			return err
		}
		if err := tx.PutChunk(ctx, dependent, 0); err != nil {
			return err
		}
		if err := tx.PutEntry(ctx, txEntry(), 0); err != nil {
			return err
		}
		if err := tx.PutLink(ctx, txLink(), 0); err != nil {
			return err
		}
		blockers, err := tx.ChunkDeletionBlockers(ctx, txChunkID)
		if err != nil {
			return err
		}
		if len(blockers.EntryIDs) != 1 || blockers.EntryIDs[0] != txEntryID || len(blockers.LinkIDs) != 1 || blockers.LinkIDs[0] != txLinkID ||
			len(blockers.DependencyIDs) != 1 || blockers.DependencyIDs[0] != dependencyID ||
			len(blockers.DependentChunkIDs) != 1 || blockers.DependentChunkIDs[0] != dependentID {
			return fmt.Errorf("pending deletion blockers = %#v", blockers)
		}
		return nil
	}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
}

func TestEntryDeletionBlockersSeePendingLinksAndSupersession(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	target := txEntry()
	source := txEntry()
	source.ID = txOtherEntry
	source.State = knowledge.EntryStateSuperseded
	source.SupersededByID = target.ID
	if err := s.Update(ctx, func(tx knowledgeStore.WriteTx) error {
		if err := tx.PutEntry(ctx, target, 0); err != nil {
			return err
		}
		if err := tx.PutEntry(ctx, source, 0); err != nil {
			return err
		}
		if err := tx.PutLink(ctx, txLink(), 0); err != nil {
			return err
		}
		blockers, err := tx.EntryDeletionBlockers(ctx, target.ID)
		if err != nil {
			return err
		}
		if len(blockers.LinkIDs) != 1 || blockers.LinkIDs[0] != txLinkID ||
			len(blockers.SupersededEntryIDs) != 1 || blockers.SupersededEntryIDs[0] != source.ID {
			return fmt.Errorf("pending entry deletion blockers = %#v", blockers)
		}
		return nil
	}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
}

func TestDerivedChunkCountsAndLastUsedProjectionStayOutOfContentRevision(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	entry := txEntry()
	entry.EvidenceIDs = []knowledge.EvidenceID{txEvidenceID}
	link := txLink()
	link.EvidenceIDs = []knowledge.EvidenceID{txEvidenceID}
	if err := s.Update(ctx, func(tx knowledgeStore.WriteTx) error {
		if err := tx.PutChunk(ctx, txChunk(1), 0); err != nil {
			return err
		}
		if err := tx.PutEntry(ctx, entry, 0); err != nil {
			return err
		}
		if err := tx.PutLink(ctx, link, 0); err != nil {
			return err
		}
		return tx.PutEvidence(ctx, txEvidence())
	}); err != nil {
		t.Fatalf("seed graph: %v", err)
	}
	assertChunkProjection := func(wantCounts knowledge.ChunkCounts, wantUsed time.Time) {
		t.Helper()
		if err := s.View(ctx, func(tx knowledgeStore.ReadTx) error {
			chunk, err := tx.Chunk(ctx, txChunkID)
			if err != nil {
				return err
			}
			if chunk.Counts != wantCounts || !chunk.LastUsedAt.Equal(wantUsed) || chunk.Revision.Number != 1 || !chunk.UpdatedAt.Equal(txTime) {
				t.Errorf("chunk projection = %#v", chunk)
			}
			return nil
		}); err != nil {
			t.Fatalf("read chunk projection: %v", err)
		}
	}
	assertChunkProjection(knowledge.ChunkCounts{Entries: 1, Links: 1, Evidence: 1}, time.Time{})

	usedAt := txTime.Add(10 * time.Minute)
	before := txChunk(1)
	if err := s.Update(ctx, func(tx knowledgeStore.WriteTx) error { return tx.TouchChunk(ctx, txChunkID, usedAt) }); err != nil {
		t.Fatalf("TouchChunk() error = %v", err)
	}
	after := before
	after.Counts = knowledge.ChunkCounts{Entries: 1, Links: 1, Evidence: 1}
	after.LastUsedAt = usedAt
	assertIndexPresence(t, s, chunkLastUsedAtIndex, chunkIndexEntry(before.ID, indexTime(before.LastUsedAt)).Suffix, false)
	assertIndexPresence(t, s, chunkLastUsedAtIndex, chunkIndexEntry(after.ID, indexTime(after.LastUsedAt)).Suffix, true)
	assertChunkProjection(after.Counts, usedAt)

	if err := s.Update(ctx, func(tx knowledgeStore.WriteTx) error {
		if err := tx.DeleteLink(ctx, link.ID, 1); err != nil {
			return err
		}
		if err := tx.DeleteEntry(ctx, entry.ID, 1); err != nil {
			return err
		}
		return tx.DeleteEvidence(ctx, txEvidenceID)
	}); err != nil {
		t.Fatalf("remove graph: %v", err)
	}
	assertChunkProjection(knowledge.ChunkCounts{}, usedAt)
}

func assertIndexPresence(t *testing.T, s *Store, name string, suffix []byte, want bool) {
	t.Helper()
	_, closer, err := s.db.Get(indexKey(s.meta.IndexGeneration, name, suffix))
	if want && err != nil {
		t.Fatalf("index %s missing: %v", name, err)
	}
	if err == nil {
		_ = closer.Close()
	}
	if !want && !errors.Is(err, cockroachpebble.ErrNotFound) {
		t.Fatalf("obsolete index %s error = %v, want ErrNotFound", name, err)
	}
}

func TestTransactionExpiresAfterCallbackAndStoreClose(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	var escaped knowledgeStore.ReadTx
	if err := s.View(ctx, func(tx knowledgeStore.ReadTx) error { escaped = tx; return nil }); err != nil {
		t.Fatalf("View() error = %v", err)
	}
	if _, err := escaped.Chunk(ctx, txChunkID); !errors.Is(err, knowledgeStore.ErrClosed) {
		t.Fatalf("escaped transaction error = %v, want ErrClosed", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := s.Update(ctx, func(knowledgeStore.WriteTx) error { return nil }); !errors.Is(err, knowledgeStore.ErrClosed) {
		t.Fatalf("closed Update() error = %v, want ErrClosed", err)
	}
	if err := s.Checkpoint(ctx, t.TempDir()); !errors.Is(err, knowledgeStore.ErrClosed) {
		t.Fatalf("closed Checkpoint() error = %v, want ErrClosed", err)
	}
}

func expectAllRecords(ctx context.Context, s *Store) error {
	return s.View(ctx, func(tx knowledgeStore.ReadTx) error {
		if _, err := tx.Chunk(ctx, txChunkID); err != nil {
			return err
		}
		if _, err := tx.Entry(ctx, txEntryID); err != nil {
			return err
		}
		if _, err := tx.Link(ctx, txLinkID); err != nil {
			return err
		}
		if _, err := tx.Evidence(ctx, txEvidenceID); err != nil {
			return err
		}
		return nil
	})
}
