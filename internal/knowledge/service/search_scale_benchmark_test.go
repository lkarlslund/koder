package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
	knowledgePebble "github.com/lkarlslund/koder/internal/knowledge/store/pebble"
)

func BenchmarkKnowledgeSearchScale(b *testing.B) {
	for _, entryCount := range []int{1_000, 10_000, 100_000} {
		b.Run(fmt.Sprintf("entries_%d", entryCount), func(b *testing.B) {
			b.StopTimer()
			service, rootID := newKnowledgeSearchBenchmarkFixture(b, entryCount)
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

func newKnowledgeSearchBenchmarkFixture(b *testing.B, entryCount int) (*Service, knowledge.EntryID) {
	b.Helper()
	store, err := knowledgePebble.Open(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = store.Close() })
	actor := knowledge.Actor{Kind: knowledge.ActorKindSystem, ID: "system:search-scale-benchmark"}
	now := time.Date(2026, time.August, 23, 9, 0, 0, 0, time.UTC)
	service, err := New(Config{
		Store: store, Actor: ContextActorSource(actor), Now: func() time.Time { return now },
	})
	if err != nil {
		b.Fatal(err)
	}
	chunk, err := service.CreateChunk(context.Background(), CreateChunkRequest{Chunk: knowledge.Chunk{
		Title: "Knowledge scale benchmark", Kind: knowledge.ChunkKindReference,
		Scope: knowledge.Scope{Kind: knowledge.ScopeKindGlobal},
	}})
	if err != nil {
		b.Fatal(err)
	}
	const batchSize = 1_000
	for start := 0; start < entryCount; start += batchSize {
		end := min(start+batchSize, entryCount)
		if err := store.Update(context.Background(), func(tx knowledgeStore.WriteTx) error {
			for index := start; index < end; index++ {
				entry := benchmarkKnowledgeEntry(index, chunk.Chunk.ID, actor, now)
				if err := tx.PutEntry(context.Background(), entry, 0); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			b.Fatalf("seed entries %d-%d: %v", start, end, err)
		}
	}
	rootID := benchmarkKnowledgeEntryID(0)
	if err := store.Update(context.Background(), func(tx knowledgeStore.WriteTx) error {
		for index := 1; index <= 20; index++ {
			link := knowledge.Link{
				ID:     benchmarkKnowledgeLinkID(index),
				Source: knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(rootID)},
				Target: knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(benchmarkKnowledgeEntryID(index))},
				Kind:   knowledge.LinkKindRelatedTo, State: knowledge.LinkStateActive,
				Revision:  knowledge.Revision{Number: 1, ID: benchmarkKnowledgeLinkRevisionID(index), Actor: actor, CreatedAt: now},
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

func benchmarkKnowledgeEntry(index int, chunkID knowledge.ChunkID, actor knowledge.Actor, now time.Time) knowledge.Entry {
	title := fmt.Sprintf("Benchmark filler record %06d", index)
	if index == 0 {
		title = "Benchmark scaleneedle root"
	}
	return knowledge.Entry{
		ID: benchmarkKnowledgeEntryID(index), ChunkID: chunkID, Kind: knowledge.EntryKindFact,
		Title: title, Summary: "Synthetic stable corpus entry used only for local scale measurement.",
		Scope: knowledge.Scope{Kind: knowledge.ScopeKindGlobal}, State: knowledge.EntryStateActive,
		Verification: knowledge.Verification{Status: knowledge.VerificationStatusUnverified},
		Revision:     knowledge.Revision{Number: 1, ID: benchmarkKnowledgeEntryRevisionID(index), Actor: actor, CreatedAt: now},
		CreatedAt:    now, UpdatedAt: now,
	}
}

func benchmarkKnowledgeEntryID(index int) knowledge.EntryID {
	return knowledge.EntryID(fmt.Sprintf("01a02c00-0000-7000-8000-%012x", index+1))
}

func benchmarkKnowledgeEntryRevisionID(index int) knowledge.RevisionID {
	return knowledge.RevisionID(fmt.Sprintf("01a02c01-0000-7000-8000-%012x", index+1))
}

func benchmarkKnowledgeLinkID(index int) knowledge.LinkID {
	return knowledge.LinkID(fmt.Sprintf("01a02c02-0000-7000-8000-%012x", index))
}

func benchmarkKnowledgeLinkRevisionID(index int) knowledge.RevisionID {
	return knowledge.RevisionID(fmt.Sprintf("01a02c03-0000-7000-8000-%012x", index))
}
