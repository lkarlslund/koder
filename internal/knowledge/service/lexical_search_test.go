package service

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
)

func TestSearchLexicalFiltersAuthorizationScopeLifecycleAndValidityBeforeScoring(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newLexicalSearchTestService(t, store)
	projectScope := knowledge.Scope{Kind: knowledge.ScopeKindProject, Selector: "koder"}
	globalScope := knowledge.Scope{Kind: knowledge.ScopeKindGlobal}

	allowedChunk, _ := createLexicalSearchFixture(t, ctx, service, "Allowed", projectScope, time.Time{})
	deniedChunk, _ := createLexicalSearchFixture(t, ctx, service, "Denied", projectScope, time.Time{})
	_, expiredEntry := createLexicalSearchFixture(t, ctx, service, "Expired", projectScope, serviceTime.Add(-time.Hour))
	_, archivedEntry := createLexicalSearchFixture(t, ctx, service, "Archived entry", projectScope, time.Time{})
	archivedChunk, _ := createLexicalSearchFixture(t, ctx, service, "Archived chunk", projectScope, time.Time{})
	globalChunk, _ := createLexicalSearchFixture(t, ctx, service, "Global", globalScope, time.Time{})
	if _, err := service.ArchiveEntry(ctx, EntryLifecycleRequest{EntryID: archivedEntry.ID, ExpectedRevision: 1}); err != nil {
		t.Fatalf("ArchiveEntry() error = %v", err)
	}
	if _, err := service.ArchiveChunk(ctx, ChunkLifecycleRequest{ChunkID: archivedChunk.ID, ExpectedRevision: 1}); err != nil {
		t.Fatalf("ArchiveChunk() error = %v", err)
	}

	var checked []knowledge.ChunkID
	service.chunkPolicy = ChunkPolicyFunc(func(_ context.Context, _ knowledge.Actor, action ChunkPolicyAction, chunk knowledge.Chunk) error {
		if action != ChunkPolicySearch {
			t.Fatalf("policy action = %q", action)
		}
		checked = append(checked, chunk.ID)
		if chunk.ID == deniedChunk.ID {
			return fmt.Errorf("denied fixture")
		}
		return nil
	})

	result, err := service.SearchLexical(ctx, LexicalSearchRequest{Query: "needle", Scopes: []knowledge.Scope{projectScope}})
	if err != nil {
		t.Fatalf("SearchLexical(defaults) error = %v", err)
	}
	if result.CorpusDocumentCount != 1 || result.MatchedDocumentCount != 1 || len(result.Matches) != 1 ||
		result.Matches[0].EntryID == expiredEntry.ID {
		t.Fatalf("default filtered result = %#v", result)
	}
	if !slices.Contains(checked, allowedChunk.ID) || !slices.Contains(checked, deniedChunk.ID) ||
		slices.Contains(checked, archivedChunk.ID) || slices.Contains(checked, globalChunk.ID) {
		t.Fatalf("policy checked chunks = %v", checked)
	}

	result, err = service.SearchLexical(ctx, LexicalSearchRequest{
		Query: "needle", Scopes: []knowledge.Scope{projectScope}, IncludeInvalid: true,
	})
	if err != nil || result.CorpusDocumentCount != 2 || result.MatchedDocumentCount != 2 {
		t.Fatalf("include-invalid result = %#v, %v", result, err)
	}
	result, err = service.SearchLexical(ctx, LexicalSearchRequest{
		Query: "needle", Scopes: []knowledge.Scope{projectScope},
		EntryStates: []knowledge.EntryState{knowledge.EntryStateArchived},
	})
	if err != nil || result.CorpusDocumentCount != 1 || len(result.Matches) != 1 || result.Matches[0].EntryID != archivedEntry.ID {
		t.Fatalf("archived-entry result = %#v, %v", result, err)
	}
	result, err = service.SearchLexical(ctx, LexicalSearchRequest{
		Query: "needle", Scopes: []knowledge.Scope{projectScope},
		ChunkStates: []knowledge.ChunkState{knowledge.ChunkStateArchived},
	})
	if err != nil || result.CorpusDocumentCount != 1 {
		t.Fatalf("archived-chunk result = %#v, %v", result, err)
	}
	result, err = service.SearchLexical(ctx, LexicalSearchRequest{Query: "needle", Scopes: []knowledge.Scope{globalScope}})
	if err != nil || result.CorpusDocumentCount != 1 || len(result.Matches) != 1 {
		t.Fatalf("global-scope result = %#v, %v", result, err)
	}
}

func TestSearchLexicalRejectsInvalidFilters(t *testing.T) {
	t.Parallel()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newLexicalSearchTestService(t, store)
	for _, request := range []LexicalSearchRequest{
		{},
		{Query: "valid", Limit: maxLexicalSearchLimit + 1},
		{Query: "valid", Scopes: []knowledge.Scope{{Kind: knowledge.ScopeKindProject}}},
		{Query: "valid", EntryStates: []knowledge.EntryState{knowledge.EntryStateUnspecified}},
		{Query: "valid", ChunkStates: []knowledge.ChunkState{knowledge.ChunkStateUnspecified}},
	} {
		if _, err := service.SearchLexical(context.Background(), request); err == nil {
			t.Errorf("SearchLexical(%#v) unexpectedly succeeded", request)
		}
	}
}

func newLexicalSearchTestService(t *testing.T, store *memory.Store) *Service {
	t.Helper()
	nextID := 0x100
	service, err := New(Config{
		Store: store,
		Actor: func(context.Context) (knowledge.Actor, error) {
			return knowledge.Actor{Kind: knowledge.ActorKindUser, ID: "user:test"}, nil
		},
		Now: func() time.Time { return serviceTime },
		NewID: func() string {
			value := fmt.Sprintf("01a01688-fc5d-7f7d-8bb8-%012x", nextID)
			nextID++
			return value
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return service
}

func createLexicalSearchFixture(t *testing.T, ctx context.Context, service *Service, title string, scope knowledge.Scope, validUntil time.Time) (knowledge.Chunk, knowledge.Entry) {
	t.Helper()
	chunk, err := service.CreateChunk(ctx, CreateChunkRequest{Chunk: knowledge.Chunk{
		Title: title, Kind: knowledge.ChunkKindReference, Scope: scope,
	}})
	if err != nil {
		t.Fatalf("CreateChunk(%q) error = %v", title, err)
	}
	entry, err := service.CreateEntry(ctx, CreateEntryRequest{
		ChunkID: chunk.Chunk.ID,
		Entry:   knowledge.Entry{Title: title + " needle", Kind: knowledge.EntryKindFact, ValidUntil: validUntil},
	})
	if err != nil {
		t.Fatalf("CreateEntry(%q) error = %v", title, err)
	}
	return chunk.Chunk, entry.Entry
}
