package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

func TestCRUDLinkSearchBackendContract(t *testing.T) {
	t.Parallel()
	for _, backend := range []struct {
		name string
		open func(*testing.T) memoryStoreAPI.Store
	}{
		{name: "memory", open: openMigrationMemory},
		{name: "pebble", open: openMigrationPebble},
	} {
		backend := backend
		t.Run(backend.name, func(t *testing.T) {
			t.Parallel()
			runCRUDLinkSearchBackendContract(t, backend.open(t))
		})
	}
}

func runCRUDLinkSearchBackendContract(t *testing.T, backend memoryStoreAPI.Store) {
	t.Helper()
	ctx := context.Background()
	seedMigrationStore(t, backend)

	health, err := backend.Health(ctx)
	if err != nil || !health.Open || health.SchemaVersion == 0 || health.IndexGeneration == 0 {
		t.Fatalf("Health() = %#v, %v", health, err)
	}
	if err := backend.View(ctx, func(tx memoryStoreAPI.ReadTx) error {
		chunk, err := tx.Chunk(ctx, migrationChunkID)
		if err != nil || chunk.Revision.Number != 2 || chunk.Description != "Second exact revision" {
			return errors.New("chunk did not round-trip")
		}
		entry, err := tx.Entry(ctx, migrationEntryID)
		if err != nil || entry.Body != "Exact backend-neutral content." {
			return errors.New("entry did not round-trip")
		}
		link, err := tx.Link(ctx, migrationLinkID)
		if err != nil || link.Source.ID != string(migrationChunkID) || link.Target.ID != string(migrationEntryID) {
			return errors.New("link did not round-trip")
		}
		if equivalent, err := tx.EquivalentLink(ctx, link); err != nil || equivalent.ID != link.ID {
			return errors.New("equivalent link lookup failed")
		}
		if evidence, err := tx.Evidence(ctx, migrationEvidenceID); err != nil || evidence.Source.ID != "observation:migration" {
			return errors.New("evidence did not round-trip")
		}
		if asset, err := tx.Asset(ctx, migrationChunkID, "assets/note.txt"); err != nil || string(asset.Data) != "portable asset\n" {
			return errors.New("asset did not round-trip")
		}
		return nil
	}); err != nil {
		t.Fatalf("View() contract error = %v", err)
	}

	chunks, err := backend.ListChunks(ctx, memoryStoreAPI.ChunkListRequest{Filter: memoryStoreAPI.ChunkFilter{
		Kinds: []memory.ChunkKind{memory.ChunkKindReference}, Tags: nil,
	}})
	if err != nil || len(chunks.Chunks) != 1 || chunks.Chunks[0].ID != migrationChunkID {
		t.Fatalf("ListChunks() = %#v, %v", chunks, err)
	}
	entries, err := backend.ListEntries(ctx, memoryStoreAPI.EntryListRequest{Filter: memoryStoreAPI.EntryFilter{
		ChunkIDs: []memory.ChunkID{migrationChunkID}, Kinds: []memory.EntryKind{memory.EntryKindFact},
	}})
	if err != nil || len(entries.Entries) != 1 || entries.Entries[0].ID != migrationEntryID {
		t.Fatalf("ListEntries() = %#v, %v", entries, err)
	}
	exact, err := backend.SearchExact(ctx, memoryStoreAPI.ExactSearchRequest{Query: "PORTABLE FACT"})
	if err != nil || len(exact.Hits) != 1 || exact.Hits[0].Record.ID() != string(migrationEntryID) || exact.Hits[0].Matches[0] != memoryStoreAPI.ExactMatchTitle {
		t.Fatalf("SearchExact() = %#v, %v", exact, err)
	}
	postings, err := backend.LookupLexicalPostings(ctx, memoryStoreAPI.LexicalPostingRequest{Terms: []string{"backend-neutral"}})
	if err != nil || len(postings.Postings) != 1 || postings.DocumentCount != 1 {
		t.Fatalf("LookupLexicalPostings() = %#v, %v", postings, err)
	}
	adjacent, err := backend.ListAdjacentLinks(ctx, memoryStoreAPI.AdjacentLinkListRequest{Filter: memoryStoreAPI.AdjacentLinkFilter{
		Endpoint: memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: string(migrationChunkID)},
	}})
	if err != nil || len(adjacent.Links) != 1 || adjacent.Links[0].ID != migrationLinkID {
		t.Fatalf("ListAdjacentLinks() = %#v, %v", adjacent, err)
	}
	for _, object := range []memory.ObjectRef{
		{Kind: memory.ObjectKindChunk, ID: string(migrationChunkID)},
		{Kind: memory.ObjectKindEntry, ID: string(migrationEntryID)},
		{Kind: memory.ObjectKindLink, ID: string(migrationLinkID)},
	} {
		history, err := backend.ListRevisions(ctx, memoryStoreAPI.RevisionListRequest{Object: object})
		want := 1
		if object.Kind == memory.ObjectKindChunk {
			want = 2
		}
		if err != nil || len(history.Revisions) != want {
			t.Fatalf("ListRevisions(%s) = %#v, %v; want %d", object.Kind, history, err, want)
		}
	}

	rollback := errors.New("contract rollback")
	if err := backend.Update(ctx, func(tx memoryStoreAPI.WriteTx) error {
		if err := tx.DeleteLink(ctx, migrationLinkID, 1); err != nil {
			return err
		}
		return rollback
	}); !errors.Is(err, rollback) {
		t.Fatalf("Update(rollback) error = %v", err)
	}
	if err := backend.View(ctx, func(tx memoryStoreAPI.ReadTx) error {
		_, err := tx.Link(ctx, migrationLinkID)
		return err
	}); err != nil {
		t.Fatalf("rollback removed link: %v", err)
	}

	if err := backend.Update(ctx, func(tx memoryStoreAPI.WriteTx) error {
		if err := tx.DeleteLink(ctx, migrationLinkID, 1); err != nil {
			return err
		}
		if err := tx.DeleteEntry(ctx, migrationEntryID, 1); err != nil {
			return err
		}
		if err := tx.DeleteEvidence(ctx, migrationEvidenceID); err != nil {
			return err
		}
		if err := tx.DeleteAsset(ctx, migrationChunkID, "assets/note.txt"); err != nil {
			return err
		}
		return tx.DeleteChunk(ctx, migrationChunkID, 2)
	}); err != nil {
		t.Fatalf("Update(delete graph) error = %v", err)
	}
	if chunks, err := backend.ListChunks(ctx, memoryStoreAPI.ChunkListRequest{}); err != nil || len(chunks.Chunks) != 0 {
		t.Fatalf("ListChunks() after delete = %#v, %v", chunks, err)
	}
	if entries, err := backend.ListEntries(ctx, memoryStoreAPI.EntryListRequest{}); err != nil || len(entries.Entries) != 0 {
		t.Fatalf("ListEntries() after delete = %#v, %v", entries, err)
	}
	if exact, err := backend.SearchExact(ctx, memoryStoreAPI.ExactSearchRequest{Query: "Portable fact"}); err != nil || len(exact.Hits) != 0 {
		t.Fatalf("SearchExact() after delete = %#v, %v", exact, err)
	}

	if err := backend.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	health, err = backend.Health(ctx)
	if err != nil || health.Open {
		t.Fatalf("Health() after Close = %#v, %v", health, err)
	}
	if err := backend.View(ctx, func(memoryStoreAPI.ReadTx) error { return nil }); !errors.Is(err, memoryStoreAPI.ErrClosed) {
		t.Fatalf("View() after Close error = %v, want ErrClosed", err)
	}
}
