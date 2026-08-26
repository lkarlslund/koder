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

func BenchmarkMemorySearchScale(b *testing.B) {
	for _, entryCount := range []int{1_000, 10_000, 100_000} {
		b.Run(fmt.Sprintf("entries_%d", entryCount), func(b *testing.B) {
			b.StopTimer()
			service, rootID := newMemorySearchBenchmarkFixture(b, entryCount)
			request := LexicalSearchRequest{Query: "scaleneedle", Limit: 25}
			b.Run("lexical", func(b *testing.B) {
				b.ReportAllocs()
				b.ReportMetric(float64(entryCount), "corpus_entries")
				for range b.N {
					result, err := service.SearchLexical(context.Background(), request)
					if err != nil || len(result.Matches) != 1 || result.Matches[0].EntryID != rootID {
						b.Fatalf("SearchLexical() = %#v, %v", result, err)
					}
				}
			})
			request.GraphExpansion = &GraphExpansionOptions{}
			b.Run("graph_expansion", func(b *testing.B) {
				b.ReportAllocs()
				b.ReportMetric(float64(entryCount), "corpus_entries")
				for range b.N {
					result, err := service.SearchLexical(context.Background(), request)
					if err != nil || len(result.Matches) != 21 || result.GraphExpansion == nil || result.GraphExpansion.EntriesAdded != 20 {
						b.Fatalf("SearchLexical(graph) = %#v, %v", result, err)
					}
				}
			})
		})
	}
}

func newMemorySearchBenchmarkFixture(b *testing.B, entryCount int) (*Service, memory.EntryID) {
	b.Helper()
	store, err := memoryPebble.Open(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = store.Close() })
	actor := memory.Actor{Kind: memory.ActorKindSystem, ID: "system:search-scale-benchmark"}
	now := time.Date(2026, time.August, 23, 9, 0, 0, 0, time.UTC)
	service, err := New(Config{
		Store: store, Actor: ContextActorSource(actor), Now: func() time.Time { return now },
	})
	if err != nil {
		b.Fatal(err)
	}
	chunk, err := service.CreateChunk(context.Background(), CreateChunkRequest{Chunk: memory.Chunk{
		Title: "Memory scale benchmark", Kind: memory.ChunkKindReference,
		Scope: memory.Scope{Kind: memory.ScopeKindGlobal},
	}})
	if err != nil {
		b.Fatal(err)
	}
	const batchSize = 1_000
	for start := 0; start < entryCount; start += batchSize {
		end := min(start+batchSize, entryCount)
		if err := store.Update(context.Background(), func(tx memoryStoreAPI.WriteTx) error {
			for index := start; index < end; index++ {
				entry := benchmarkMemoryEntry(index, chunk.Chunk.ID, actor, now)
				if err := tx.PutEntry(context.Background(), entry, 0); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			b.Fatalf("seed entries %d-%d: %v", start, end, err)
		}
	}
	rootID := benchmarkMemoryEntryID(0)
	if err := store.Update(context.Background(), func(tx memoryStoreAPI.WriteTx) error {
		for index := 1; index <= 20; index++ {
			link := memory.Link{
				ID:     benchmarkMemoryLinkID(index),
				Source: memory.ObjectRef{Kind: memory.ObjectKindEntry, ID: string(rootID)},
				Target: memory.ObjectRef{Kind: memory.ObjectKindEntry, ID: string(benchmarkMemoryEntryID(index))},
				Kind:   memory.LinkKindRelatedTo, State: memory.LinkStateActive,
				Revision:  memory.Revision{Number: 1, ID: benchmarkMemoryLinkRevisionID(index), Actor: actor, CreatedAt: now},
				CreatedAt: now, UpdatedAt: now,
			}
			if err := tx.PutLink(context.Background(), link, 0); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		b.Fatal(err)
	}
	return service, rootID
}

func benchmarkMemoryEntry(index int, chunkID memory.ChunkID, actor memory.Actor, now time.Time) memory.Entry {
	title := fmt.Sprintf("Benchmark filler record %06d", index)
	if index == 0 {
		title = "Benchmark scaleneedle root"
	}
	return memory.Entry{
		ID: benchmarkMemoryEntryID(index), ChunkID: chunkID, Kind: memory.EntryKindFact,
		Title: title, Summary: "Synthetic stable corpus entry used only for local scale measurement.",
		Scope: memory.Scope{Kind: memory.ScopeKindGlobal}, State: memory.EntryStateActive,
		Verification: memory.Verification{Status: memory.VerificationStatusUnverified},
		Revision:     memory.Revision{Number: 1, ID: benchmarkMemoryEntryRevisionID(index), Actor: actor, CreatedAt: now},
		CreatedAt:    now, UpdatedAt: now,
	}
}

func benchmarkMemoryEntryID(index int) memory.EntryID {
	return memory.EntryID(fmt.Sprintf("01a02c00-0000-7000-8000-%012x", index+1))
}

func benchmarkMemoryEntryRevisionID(index int) memory.RevisionID {
	return memory.RevisionID(fmt.Sprintf("01a02c01-0000-7000-8000-%012x", index+1))
}

func benchmarkMemoryLinkID(index int) memory.LinkID {
	return memory.LinkID(fmt.Sprintf("01a02c02-0000-7000-8000-%012x", index))
}

func benchmarkMemoryLinkRevisionID(index int) memory.RevisionID {
	return memory.RevisionID(fmt.Sprintf("01a02c03-0000-7000-8000-%012x", index))
}
