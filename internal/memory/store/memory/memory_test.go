package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

const (
	chunkID    memory.ChunkID    = "019f132e-4f3a-739a-9ab2-5198dcd19e67"
	entryID    memory.EntryID    = "01a01f76-1ff6-7c1d-967a-66ad5703dd33"
	otherEntry memory.EntryID    = "01a01f76-1ff6-7c1d-967a-66ad5703dd34"
	linkID     memory.LinkID     = "01a020a6-84d5-7b03-a995-bb2cfb4528b0"
	evidenceID memory.EvidenceID = "01a01688-fc6b-7a53-a907-4f903461820e"
)

var fixedTime = time.Date(2026, 8, 22, 14, 0, 0, 0, time.UTC)

func revision(number uint64) memory.Revision {
	return memory.Revision{
		Number:    number,
		ID:        memory.RevisionID(fmt.Sprintf("01a01688-fc5d-7f7d-8bb8-%012x", number)),
		Actor:     memory.Actor{Kind: memory.ActorKindSystem, ID: "test"},
		CreatedAt: fixedTime.Add(time.Duration(number-1) * time.Second),
	}
}

func chunk(number uint64) memory.Chunk {
	updatedAt := fixedTime.Add(time.Duration(number-1) * time.Second)
	return memory.Chunk{
		ID: chunkID, Title: fmt.Sprintf("Chunk r%d", number), Kind: memory.ChunkKindReference,
		Scope: memory.Scope{Kind: memory.ScopeKindGlobal}, Visibility: memory.VisibilityInstallation,
		State: memory.ChunkStateActive, SchemaVersion: 1, Revision: revision(number),
		CreatedAt: fixedTime, UpdatedAt: updatedAt, Aliases: []string{"alias"},
	}
}

func entry() memory.Entry {
	return memory.Entry{
		ID: entryID, ChunkID: chunkID, Kind: memory.EntryKindFact, Title: "Entry",
		Scope:        memory.Scope{Kind: memory.ScopeKindGlobal},
		Verification: memory.Verification{Status: memory.VerificationStatusUnverified},
		State:        memory.EntryStateActive, Revision: revision(1), CreatedAt: fixedTime, UpdatedAt: fixedTime,
	}
}

func link() memory.Link {
	return memory.Link{
		ID:     linkID,
		Source: memory.ObjectRef{Kind: memory.ObjectKindEntry, ID: string(entryID)},
		Target: memory.ObjectRef{Kind: memory.ObjectKindEntry, ID: string(otherEntry)},
		Kind:   memory.LinkKindRelatedTo, State: memory.LinkStateActive,
		Revision: revision(1), CreatedAt: fixedTime, UpdatedAt: fixedTime,
	}
}

func evidence() memory.Evidence {
	return memory.Evidence{
		ID: evidenceID, Type: memory.EvidenceTypeObservation, Quality: memory.EvidenceQualityPrimary,
		Source: memory.Source{ID: "observation:test"},
		Actor:  memory.Actor{Kind: memory.ActorKindSystem, ID: "test"}, CreatedAt: fixedTime,
	}
}

func asset() memoryStoreAPI.PackageAsset {
	data := []byte("asset data\n")
	digest := sha256.Sum256(data)
	return memoryStoreAPI.PackageAsset{
		ChunkID: chunkID, Path: "assets/note.txt", MediaType: "text/plain; charset=utf-8",
		SHA256: hex.EncodeToString(digest[:]), Data: data,
	}
}

func TestStoreReadWriteAndReadYourWrites(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	if err := s.Update(ctx, func(tx memoryStoreAPI.WriteTx) error {
		if err := tx.PutChunk(ctx, chunk(1), 0); err != nil {
			return err
		}
		if err := tx.PutAsset(ctx, asset()); err != nil {
			return err
		}
		if err := tx.PutEntry(ctx, entry(), 0); err != nil {
			return err
		}
		if err := tx.PutLink(ctx, link(), 0); err != nil {
			return err
		}
		if err := tx.PutEvidence(ctx, evidence()); err != nil {
			return err
		}
		got, err := tx.Chunk(ctx, chunkID)
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

	if err := s.View(ctx, func(tx memoryStoreAPI.ReadTx) error {
		if _, err := tx.Chunk(ctx, chunkID); err != nil {
			return err
		}
		if _, err := tx.Entry(ctx, entryID); err != nil {
			return err
		}
		if _, err := tx.Link(ctx, linkID); err != nil {
			return err
		}
		if _, err := tx.Evidence(ctx, evidenceID); err != nil {
			return err
		}
		stored, err := tx.Asset(ctx, chunkID, "assets/note.txt")
		if err != nil || string(stored.Data) != "asset data\n" {
			return fmt.Errorf("read asset: %#v, %w", stored, err)
		}
		stored.Data[0] = 'X'
		assets, err := tx.ListAssets(ctx, chunkID)
		if err != nil || len(assets) != 1 || string(assets[0].Data) != "asset data\n" {
			return fmt.Errorf("list cloned assets: %#v, %w", assets, err)
		}
		return nil
	}); err != nil {
		t.Fatalf("View() error = %v", err)
	}
}

func TestStoreUpdateRollsBack(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	wantErr := errors.New("stop")
	if err := s.Update(ctx, func(tx memoryStoreAPI.WriteTx) error {
		if err := tx.PutChunk(ctx, chunk(1), 0); err != nil {
			return err
		}
		if err := tx.PutAsset(ctx, asset()); err != nil {
			return err
		}
		return wantErr
	}); !errors.Is(err, wantErr) {
		t.Fatalf("Update() error = %v, want %v", err, wantErr)
	}
	if err := s.View(ctx, func(tx memoryStoreAPI.ReadTx) error {
		if _, err := tx.Chunk(ctx, chunkID); !errors.Is(err, memoryStoreAPI.ErrNotFound) {
			return fmt.Errorf("chunk error = %v, want ErrNotFound", err)
		}
		_, err := tx.Asset(ctx, chunkID, "assets/note.txt")
		return err
	}); !errors.Is(err, memoryStoreAPI.ErrNotFound) {
		t.Fatalf("View() after rollback error = %v, want ErrNotFound", err)
	}
}

func TestStoreEnforcesRevisionPreconditions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Update(ctx, func(tx memoryStoreAPI.WriteTx) error { return tx.PutChunk(ctx, chunk(1), 0) }); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.Update(ctx, func(tx memoryStoreAPI.WriteTx) error { return tx.PutAsset(ctx, asset()) }); err != nil {
		t.Fatalf("create asset: %v", err)
	}
	for name, update := range map[string]func(memoryStoreAPI.WriteTx) error{
		"duplicate create": func(tx memoryStoreAPI.WriteTx) error { return tx.PutChunk(ctx, chunk(1), 0) },
		"stale expected":   func(tx memoryStoreAPI.WriteTx) error { return tx.PutChunk(ctx, chunk(2), 2) },
		"skipped revision": func(tx memoryStoreAPI.WriteTx) error { return tx.PutChunk(ctx, chunk(3), 1) },
		"zero delete":      func(tx memoryStoreAPI.WriteTx) error { return tx.DeleteChunk(ctx, chunkID, 0) },
	} {
		t.Run(name, func(t *testing.T) {
			if err := s.Update(ctx, update); !errors.Is(err, memoryStoreAPI.ErrConflict) {
				t.Fatalf("Update() error = %v, want ErrConflict", err)
			}
		})
	}
	if err := s.Update(ctx, func(tx memoryStoreAPI.WriteTx) error { return tx.PutChunk(ctx, chunk(2), 1) }); err != nil {
		t.Fatalf("valid update: %v", err)
	}
	if err := s.Update(ctx, func(tx memoryStoreAPI.WriteTx) error { return tx.DeleteChunk(ctx, chunkID, 2) }); err != nil {
		t.Fatalf("valid delete: %v", err)
	}
	if err := s.View(ctx, func(tx memoryStoreAPI.ReadTx) error {
		_, err := tx.Asset(ctx, chunkID, "assets/note.txt")
		return err
	}); !errors.Is(err, memoryStoreAPI.ErrNotFound) {
		t.Fatalf("asset survived chunk delete: %v", err)
	}
}

func TestStoreRevisionHistoryIsAtomicAndExcludesDerivedUpdates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	first := chunk(1)
	first.Revision.Reason = "created"
	second := chunk(2)
	second.Revision.Reason = "renamed"
	if err := s.Update(ctx, func(tx memoryStoreAPI.WriteTx) error { return tx.PutChunk(ctx, first, 0) }); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.Update(ctx, func(tx memoryStoreAPI.WriteTx) error { return tx.PutChunk(ctx, second, 1) }); err != nil {
		t.Fatalf("update: %v", err)
	}
	usedAt := fixedTime.Add(time.Hour)
	if err := s.Update(ctx, func(tx memoryStoreAPI.WriteTx) error { return tx.TouchChunk(ctx, chunkID, usedAt) }); err != nil {
		t.Fatalf("touch: %v", err)
	}
	page, err := s.ListRevisions(ctx, memoryStoreAPI.RevisionListRequest{
		Object: memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: string(chunkID)}, Limit: 1,
	})
	if err != nil {
		t.Fatalf("ListRevisions(): %v", err)
	}
	if len(page.Revisions) != 1 || page.Revisions[0].Chunk.Title != "Chunk r2" || page.Revisions[0].Chunk.Revision.Reason != "renamed" {
		t.Fatalf("newest history record = %#v", page.Revisions)
	}
	if !page.Revisions[0].Chunk.LastUsedAt.IsZero() {
		t.Fatalf("historical LastUsedAt = %v, want zero", page.Revisions[0].Chunk.LastUsedAt)
	}
	page, err = s.ListRevisions(ctx, memoryStoreAPI.RevisionListRequest{
		Object: memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: string(chunkID)}, Cursor: page.NextCursor,
	})
	if err != nil {
		t.Fatalf("ListRevisions(second page): %v", err)
	}
	if len(page.Revisions) != 1 || page.Revisions[0].Chunk.Revision.Number != 1 || page.Revisions[0].Chunk.Revision.Reason != "created" {
		t.Fatalf("oldest history record = %#v", page.Revisions)
	}

	wantErr := errors.New("rollback")
	third := chunk(3)
	if err := s.Update(ctx, func(tx memoryStoreAPI.WriteTx) error {
		if err := tx.PutChunk(ctx, third, 2); err != nil {
			return err
		}
		return wantErr
	}); !errors.Is(err, wantErr) {
		t.Fatalf("rollback update error = %v", err)
	}
	page, err = s.ListRevisions(ctx, memoryStoreAPI.RevisionListRequest{
		Object: memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: string(chunkID)},
	})
	if err != nil || len(page.Revisions) != 2 {
		t.Fatalf("history after rollback = %#v, %v", page, err)
	}
	if err := s.Update(ctx, func(tx memoryStoreAPI.WriteTx) error { return tx.DeleteChunk(ctx, chunkID, 2) }); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.ListRevisions(ctx, memoryStoreAPI.RevisionListRequest{
		Object: memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: string(chunkID)},
	}); !errors.Is(err, memoryStoreAPI.ErrNotFound) {
		t.Fatalf("history after permanent delete error = %v, want ErrNotFound", err)
	}
}

func TestStoreTracksEntryAndLinkRevisionHistory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	firstEntry, secondEntry := entry(), entry()
	secondEntry.Revision = revision(2)
	secondEntry.Title = "Entry r2"
	secondEntry.UpdatedAt = secondEntry.Revision.CreatedAt
	firstLink, secondLink := link(), link()
	secondLink.Revision = revision(2)
	secondLink.Label = "Link r2"
	secondLink.UpdatedAt = secondLink.Revision.CreatedAt
	if err := s.Update(ctx, func(tx memoryStoreAPI.WriteTx) error {
		if err := tx.PutEntry(ctx, firstEntry, 0); err != nil {
			return err
		}
		if err := tx.PutEntry(ctx, secondEntry, 1); err != nil {
			return err
		}
		if err := tx.PutLink(ctx, firstLink, 0); err != nil {
			return err
		}
		return tx.PutLink(ctx, secondLink, 1)
	}); err != nil {
		t.Fatalf("write histories: %v", err)
	}
	for _, object := range []memory.ObjectRef{
		{Kind: memory.ObjectKindEntry, ID: string(entryID)},
		{Kind: memory.ObjectKindLink, ID: string(linkID)},
	} {
		page, err := s.ListRevisions(ctx, memoryStoreAPI.RevisionListRequest{Object: object})
		if err != nil || len(page.Revisions) != 2 || page.Revisions[0].RevisionMetadata().Number != 2 {
			t.Fatalf("ListRevisions(%v) = %#v, %v", object, page, err)
		}
	}
}

func TestStoreRejectsEquivalentSymmetricLinksAtomically(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	first := link()
	reversed := first
	reversed.ID = "01a020a6-84d5-7b03-a995-bb2cfb4528b1"
	reversed.Source, reversed.Target = first.Target, first.Source
	if err := s.Update(ctx, func(tx memoryStoreAPI.WriteTx) error {
		if err := tx.PutLink(ctx, first, 0); err != nil {
			return err
		}
		return tx.PutLink(ctx, reversed, 0)
	}); !errors.Is(err, memoryStoreAPI.ErrConflict) {
		t.Fatalf("duplicate transaction error = %v, want ErrConflict", err)
	}
	if _, err := s.ListRevisions(ctx, memoryStoreAPI.RevisionListRequest{
		Object: memory.ObjectRef{Kind: memory.ObjectKindLink, ID: string(first.ID)},
	}); !errors.Is(err, memoryStoreAPI.ErrNotFound) {
		t.Fatalf("rolled-back first link history error = %v, want ErrNotFound", err)
	}
}

func TestStoreClonesMutableRecordData(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	value := chunk(1)
	if err := s.Update(ctx, func(tx memoryStoreAPI.WriteTx) error { return tx.PutChunk(ctx, value, 0) }); err != nil {
		t.Fatalf("create: %v", err)
	}
	value.Aliases[0] = "mutated after put"
	if err := s.View(ctx, func(tx memoryStoreAPI.ReadTx) error {
		got, err := tx.Chunk(ctx, chunkID)
		if err != nil {
			return err
		}
		got.Aliases[0] = "mutated after get"
		return nil
	}); err != nil {
		t.Fatalf("first View(): %v", err)
	}
	if err := s.View(ctx, func(tx memoryStoreAPI.ReadTx) error {
		got, err := tx.Chunk(ctx, chunkID)
		if err != nil {
			return err
		}
		if got.Aliases[0] != "alias" {
			return fmt.Errorf("stored alias = %q", got.Aliases[0])
		}
		return nil
	}); err != nil {
		t.Fatalf("second View(): %v", err)
	}
}

func TestEvidenceIsImmutable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Update(ctx, func(tx memoryStoreAPI.WriteTx) error { return tx.PutEvidence(ctx, evidence()) }); err != nil {
		t.Fatalf("create evidence: %v", err)
	}
	if err := s.Update(ctx, func(tx memoryStoreAPI.WriteTx) error { return tx.PutEvidence(ctx, evidence()) }); !errors.Is(err, memoryStoreAPI.ErrConflict) {
		t.Fatalf("duplicate evidence error = %v, want ErrConflict", err)
	}
	if err := s.Update(ctx, func(tx memoryStoreAPI.WriteTx) error { return tx.DeleteEvidence(ctx, evidenceID) }); err != nil {
		t.Fatalf("delete evidence: %v", err)
	}
	if err := s.Update(ctx, func(tx memoryStoreAPI.WriteTx) error { return tx.DeleteEvidence(ctx, evidenceID) }); !errors.Is(err, memoryStoreAPI.ErrNotFound) {
		t.Fatalf("missing evidence error = %v, want ErrNotFound", err)
	}
}

func TestStoreRejectsInvalidCanonicalRecord(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	value := chunk(1)
	value.Title = ""
	if err := s.Update(ctx, func(tx memoryStoreAPI.WriteTx) error { return tx.PutChunk(ctx, value, 0) }); !errors.Is(err, memory.ErrInvalidRecord) {
		t.Fatalf("Update() error = %v, want ErrInvalidRecord", err)
	}
}

func TestStoreCancellationAndExpiredTransactions(t *testing.T) {
	t.Parallel()
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.View(ctx, func(memoryStoreAPI.ReadTx) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("View() error = %v, want context.Canceled", err)
	}
	var escaped memoryStoreAPI.ReadTx
	if err := s.View(context.Background(), func(tx memoryStoreAPI.ReadTx) error { escaped = tx; return nil }); err != nil {
		t.Fatalf("View() error = %v", err)
	}
	if _, err := escaped.Chunk(context.Background(), chunkID); !errors.Is(err, memoryStoreAPI.ErrClosed) {
		t.Fatalf("escaped transaction error = %v, want ErrClosed", err)
	}
}

func TestStoreCloseAndHealth(t *testing.T) {
	t.Parallel()
	s := New()
	health, err := s.Health(context.Background())
	if err != nil || health.Backend != "memory" || !health.Open || health.SchemaVersion != 1 {
		t.Fatalf("Health() = %#v, %v", health, err)
	}
	if err := s.Checkpoint(context.Background(), t.TempDir()); !errors.Is(err, memoryStoreAPI.ErrUnsupported) {
		t.Fatalf("Checkpoint() error = %v, want ErrUnsupported", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	health, err = s.Health(context.Background())
	if err != nil || health.Open {
		t.Fatalf("closed Health() = %#v, %v", health, err)
	}
	if err := s.View(context.Background(), func(memoryStoreAPI.ReadTx) error { return nil }); !errors.Is(err, memoryStoreAPI.ErrClosed) {
		t.Fatalf("closed View() error = %v, want ErrClosed", err)
	}
}

func TestStoreSerializesConcurrentUpdates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Update(ctx, func(tx memoryStoreAPI.WriteTx) error { return tx.PutChunk(ctx, chunk(1), 0) }); err != nil {
		t.Fatalf("create: %v", err)
	}
	const writers = 16
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- s.Update(ctx, func(tx memoryStoreAPI.WriteTx) error {
				current, err := tx.Chunk(ctx, chunkID)
				if err != nil {
					return err
				}
				next := chunk(current.Revision.Number + 1)
				return tx.PutChunk(ctx, next, current.Revision.Number)
			})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Update() error = %v", err)
		}
	}
	if err := s.View(ctx, func(tx memoryStoreAPI.ReadTx) error {
		got, err := tx.Chunk(ctx, chunkID)
		if err != nil {
			return err
		}
		if got.Revision.Number != writers+1 {
			return fmt.Errorf("revision = %d, want %d", got.Revision.Number, writers+1)
		}
		return nil
	}); err != nil {
		t.Fatalf("View() error = %v", err)
	}
}
