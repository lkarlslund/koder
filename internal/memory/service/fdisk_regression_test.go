package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
	memoryPebble "github.com/lkarlslund/koder/internal/memory/store/pebble"
)

func TestFDiskResearchBecomesDurableSFDiskMemory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	stateDir := t.TempDir()
	store, err := memoryPebble.Open(stateDir)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	service := newFDiskRegressionService(t, store, 0x500)

	before, err := service.SearchLexical(ctx, LexicalSearchRequest{Query: "linux partition tools fdisk"})
	if err != nil || len(before.Matches) != 0 || before.CorpusDocumentCount != 0 {
		t.Fatalf("memory before research = %#v, %v", before, err)
	}

	evidence, err := service.CreateEvidence(ctx, CreateEvidenceRequest{Evidence: memory.Evidence{
		Type: memory.EvidenceTypeWeb, Quality: memory.EvidenceQualityAuthoritative,
		Source: memory.Source{
			ID: "web:util-linux-sfdisk", URI: "https://man7.org/linux/man-pages/man8/sfdisk.8.html",
			Title: "sfdisk manual", ContentHash: "sha256:mocked-research-result",
			AccessedAt: serviceTime,
		},
	}})
	if err != nil {
		t.Fatalf("CreateEvidence(mocked web research) error = %v", err)
	}
	chunk, err := service.CreateChunk(ctx, CreateChunkRequest{Chunk: memory.Chunk{
		Title: "Linux partition tools", Kind: memory.ChunkKindReference,
		Scope: memory.Scope{Kind: memory.ScopeKindGlobal}, Tags: []string{"linux", "partitioning"},
	}})
	if err != nil {
		t.Fatalf("CreateChunk(research result) error = %v", err)
	}
	entry, err := service.CreateEntry(ctx, CreateEntryRequest{
		ChunkID: chunk.Chunk.ID,
		Entry: memory.Entry{
			Title: "Linux partition tools: use sfdisk for scripted partitioning", Kind: memory.EntryKindProcedure,
			Summary: "sfdisk is a scriptable fdisk alternative from util-linux.",
			Body:    "When fdisk is unavailable, inspect and modify partition tables with sfdisk. Use --dump before changing a disk.",
			Aliases: []string{"scriptable fdisk alternative"}, Tags: []string{"linux", "partitioning", "sfdisk"},
			EvidenceIDs: []memory.EvidenceID{evidence.Evidence.ID},
		},
	})
	if err != nil {
		t.Fatalf("CreateEntry(research result) error = %v", err)
	}

	after, err := service.SearchLexical(ctx, LexicalSearchRequest{Query: "fdisk"})
	if err != nil || len(after.Matches) != 1 || after.Matches[0].EntryID != entry.Entry.ID {
		t.Fatalf("memory after research = %#v, %v", after, err)
	}
	if len(after.Matches[0].Reasons) == 0 || after.Matches[0].Rank.Evidence <= 0 {
		t.Fatalf("learned match lacks reason/evidence rank = %#v", after.Matches[0])
	}
	concept, err := service.SearchLexical(ctx, LexicalSearchRequest{Query: "linux partition tools"})
	if err != nil || len(concept.Matches) != 1 || len(concept.Matches[0].Terms) != 3 {
		t.Fatalf("concept search after research = %#v, %v", concept, err)
	}

	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	reopened, err := memoryPebble.Open(stateDir)
	if err != nil {
		t.Fatalf("reopen memory store: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	restarted := newFDiskRegressionService(t, reopened, 0x600)
	durable, err := restarted.SearchLexical(ctx, LexicalSearchRequest{Query: "fdisk sfdisk"})
	if err != nil || len(durable.Matches) != 1 || durable.Matches[0].EntryID != entry.Entry.ID || durable.CorpusDocumentCount != 1 {
		t.Fatalf("memory after restart = %#v, %v", durable, err)
	}
}

func newFDiskRegressionService(t *testing.T, store memoryStoreAPI.Store, idSeed int) *Service {
	t.Helper()
	service, err := New(Config{
		Store: store,
		Actor: func(context.Context) (memory.Actor, error) {
			return memory.Actor{Kind: memory.ActorKindSystem, ID: "regression:test"}, nil
		},
		Now: func() time.Time { return serviceTime },
		NewID: func() string {
			value := fmt.Sprintf("01a01688-fc5d-7f7d-8bb8-%012x", idSeed)
			idSeed++
			return value
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return service
}
