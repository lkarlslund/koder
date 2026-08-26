package service

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/memory"
	memoryBackend "github.com/lkarlslund/koder/internal/memory/store/memory"
)

func TestSearchLexicalFiltersAuthorizationScopeLifecycleAndValidityBeforeScoring(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newLexicalSearchTestService(t, store)
	projectScope := memory.Scope{Kind: memory.ScopeKindProject, Selector: "koder"}
	globalScope := memory.Scope{Kind: memory.ScopeKindGlobal}

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

	var checked []memory.ChunkID
	service.chunkPolicy = ChunkPolicyFunc(func(_ context.Context, _ memory.Actor, action ChunkPolicyAction, chunk memory.Chunk) error {
		if action != ChunkPolicySearch {
			t.Fatalf("policy action = %q", action)
		}
		checked = append(checked, chunk.ID)
		if chunk.ID == deniedChunk.ID {
			return fmt.Errorf("denied fixture")
		}
		return nil
	})

	result, err := service.SearchLexical(ctx, LexicalSearchRequest{Query: "needle", Scopes: []memory.Scope{projectScope}})
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
		Query: "needle", Scopes: []memory.Scope{projectScope}, IncludeInvalid: true,
	})
	if err != nil || result.CorpusDocumentCount != 2 || result.MatchedDocumentCount != 2 {
		t.Fatalf("include-invalid result = %#v, %v", result, err)
	}
	result, err = service.SearchLexical(ctx, LexicalSearchRequest{
		Query: "needle", Scopes: []memory.Scope{projectScope},
		EntryStates: []memory.EntryState{memory.EntryStateArchived},
	})
	if err != nil || result.CorpusDocumentCount != 1 || len(result.Matches) != 1 || result.Matches[0].EntryID != archivedEntry.ID {
		t.Fatalf("archived-entry result = %#v, %v", result, err)
	}
	result, err = service.SearchLexical(ctx, LexicalSearchRequest{
		Query: "needle", Scopes: []memory.Scope{projectScope},
		ChunkStates: []memory.ChunkState{memory.ChunkStateArchived},
	})
	if err != nil || result.CorpusDocumentCount != 1 {
		t.Fatalf("archived-chunk result = %#v, %v", result, err)
	}
	result, err = service.SearchLexical(ctx, LexicalSearchRequest{Query: "needle", Scopes: []memory.Scope{globalScope}})
	if err != nil || result.CorpusDocumentCount != 1 || len(result.Matches) != 1 {
		t.Fatalf("global-scope result = %#v, %v", result, err)
	}
	result, err = service.SearchLexical(ctx, LexicalSearchRequest{Query: "needle", ScopeKinds: []memory.ScopeKind{memory.ScopeKindProject}})
	if err != nil || result.CorpusDocumentCount != 1 || len(result.Matches) != 1 {
		t.Fatalf("project-scope-kind result = %#v, %v", result, err)
	}
	result, err = service.SearchLexical(ctx, LexicalSearchRequest{
		Query: "needle", ScopeKinds: []memory.ScopeKind{memory.ScopeKindProject}, Scopes: []memory.Scope{globalScope},
	})
	if err != nil || result.CorpusDocumentCount != 0 || len(result.Matches) != 0 {
		t.Fatalf("disjoint scope filters result = %#v, %v", result, err)
	}
}

func TestSearchLexicalRejectsInvalidFilters(t *testing.T) {
	t.Parallel()
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newLexicalSearchTestService(t, store)
	for _, request := range []LexicalSearchRequest{
		{},
		{Query: "valid", Limit: maxLexicalSearchLimit + 1},
		{Query: "valid", Scopes: []memory.Scope{{Kind: memory.ScopeKindProject}}},
		{Query: "valid", ScopeKinds: []memory.ScopeKind{memory.ScopeKindUnspecified}},
		{Query: "valid", EntryStates: []memory.EntryState{memory.EntryStateUnspecified}},
		{Query: "valid", ChunkStates: []memory.ChunkState{memory.ChunkStateUnspecified}},
		{Query: "valid", GraphExpansion: &GraphExpansionOptions{Kinds: []memory.LinkKind{memory.LinkKindUnspecified}}},
		{Query: "valid", GraphExpansion: &GraphExpansionOptions{MaxEntries: maxGraphExpansionEntries + 1}},
	} {
		if _, err := service.SearchLexical(context.Background(), request); err == nil {
			t.Errorf("SearchLexical(%#v) unexpectedly succeeded", request)
		}
	}
}

func TestSearchLexicalGraphExpansionIsBoundedAndCannotReintroduceFilteredEntries(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newLexicalSearchTestService(t, store)
	projectScope := memory.Scope{Kind: memory.ScopeKindProject, Selector: "koder"}
	globalScope := memory.Scope{Kind: memory.ScopeKindGlobal}
	_, root := createLexicalSearchEntry(t, ctx, service, "Root", projectScope, "Needle root")
	_, firstAllowed := createLexicalSearchEntry(t, ctx, service, "First allowed", projectScope, "Disk utility")
	_, secondAllowed := createLexicalSearchEntry(t, ctx, service, "Second allowed", projectScope, "Storage utility")
	deniedChunk, denied := createLexicalSearchEntry(t, ctx, service, "Denied", projectScope, "Private utility")
	_, global := createLexicalSearchEntry(t, ctx, service, "Global", globalScope, "Global utility")
	for _, target := range []memory.Entry{firstAllowed, secondAllowed, denied, global} {
		if _, err := service.CreateLink(ctx, CreateLinkRequest{Link: memory.Link{
			Source: memory.ObjectRef{Kind: memory.ObjectKindEntry, ID: string(root.ID)},
			Target: memory.ObjectRef{Kind: memory.ObjectKindEntry, ID: string(target.ID)},
			Kind:   memory.LinkKindRelatedTo,
		}}); err != nil {
			t.Fatalf("CreateLink(%s) error = %v", target.ID, err)
		}
	}
	service.chunkPolicy = ChunkPolicyFunc(func(_ context.Context, _ memory.Actor, action ChunkPolicyAction, chunk memory.Chunk) error {
		if action != ChunkPolicySearch {
			t.Fatalf("policy action = %q", action)
		}
		if chunk.ID == deniedChunk.ID {
			return fmt.Errorf("denied fixture")
		}
		return nil
	})

	result, err := service.SearchLexical(ctx, LexicalSearchRequest{
		Query: "needle", Scopes: []memory.Scope{projectScope}, GraphExpansion: &GraphExpansionOptions{},
	})
	if err != nil {
		t.Fatalf("SearchLexical(expand) error = %v", err)
	}
	if len(result.Matches) != 3 || result.Matches[0].EntryID != root.ID || result.Matches[0].LexicalScore == 0 {
		t.Fatalf("expanded matches = %#v", result.Matches)
	}
	expandedIDs := []memory.EntryID{result.Matches[1].EntryID, result.Matches[2].EntryID}
	slices.Sort(expandedIDs)
	wantIDs := []memory.EntryID{firstAllowed.ID, secondAllowed.ID}
	slices.Sort(wantIDs)
	if !slices.Equal(expandedIDs, wantIDs) || slices.Contains(expandedIDs, denied.ID) || slices.Contains(expandedIDs, global.ID) {
		t.Fatalf("expanded IDs = %v, want %v", expandedIDs, wantIDs)
	}
	if result.GraphExpansion == nil || result.GraphExpansion.RootsExpanded != 1 ||
		result.GraphExpansion.Connections != 2 || result.GraphExpansion.EntriesAdded != 2 || result.GraphExpansion.Truncated {
		t.Fatalf("graph expansion stats = %#v", result.GraphExpansion)
	}
	for _, match := range result.Matches[1:] {
		if match.LexicalScore != 0 || len(match.Terms) != 0 || len(match.GraphConnections) != 1 ||
			match.GraphConnections[0].FromEntryID != root.ID || len(match.Reasons) != 1 ||
			match.Reasons[0].Kind != SearchMatchReasonGraph {
			t.Fatalf("expanded match = %#v", match)
		}
	}

	limited, err := service.SearchLexical(ctx, LexicalSearchRequest{
		Query: "needle", Scopes: []memory.Scope{projectScope},
		GraphExpansion: &GraphExpansionOptions{MaxEntries: 1},
	})
	if err != nil || len(limited.Matches) != 2 || limited.GraphExpansion == nil ||
		!limited.GraphExpansion.Truncated || !slices.Contains(limited.GraphExpansion.Reasons, "entry_limit") {
		t.Fatalf("entry-limited expansion = %#v, %v", limited, err)
	}
}

func TestSearchLexicalSuppressesSupersededEntriesUnlessExplicitlyIncluded(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newLexicalSearchTestService(t, store)
	chunk, err := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	if err != nil {
		t.Fatalf("CreateChunk() error = %v", err)
	}
	old, err := service.CreateEntry(ctx, CreateEntryRequest{
		ChunkID: chunk.Chunk.ID, Entry: memory.Entry{Title: "Old needle", Kind: memory.EntryKindFact},
	})
	if err != nil {
		t.Fatalf("CreateEntry(old) error = %v", err)
	}
	replacement, err := service.CreateEntry(ctx, CreateEntryRequest{
		ChunkID: chunk.Chunk.ID, Entry: memory.Entry{Title: "New needle", Kind: memory.EntryKindFact},
	})
	if err != nil {
		t.Fatalf("CreateEntry(replacement) error = %v", err)
	}
	if _, err := service.SupersedeEntry(ctx, SupersedeEntryRequest{
		EntryID: old.Entry.ID, ExpectedRevision: 1, ReplacementEntryID: replacement.Entry.ID,
	}); err != nil {
		t.Fatalf("SupersedeEntry() error = %v", err)
	}

	result, err := service.SearchLexical(ctx, LexicalSearchRequest{Query: "needle"})
	if err != nil || result.CorpusDocumentCount != 1 || len(result.Matches) != 1 || result.Matches[0].EntryID != replacement.Entry.ID {
		t.Fatalf("default supersession search = %#v, %v", result, err)
	}
	result, err = service.SearchLexical(ctx, LexicalSearchRequest{
		Query: "needle", EntryStates: []memory.EntryState{memory.EntryStateActive, memory.EntryStateSuperseded},
	})
	if err != nil || result.CorpusDocumentCount != 1 || len(result.Matches) != 1 || result.Matches[0].EntryID != replacement.Entry.ID {
		t.Fatalf("implicit supersession search = %#v, %v", result, err)
	}
	result, err = service.SearchLexical(ctx, LexicalSearchRequest{Query: "needle", IncludeSuperseded: true})
	if err != nil || result.CorpusDocumentCount != 2 || len(result.Matches) != 2 {
		t.Fatalf("included supersession search = %#v, %v", result, err)
	}
	result, err = service.SearchLexical(ctx, LexicalSearchRequest{
		Query: "needle", IncludeSuperseded: true,
		EntryStates: []memory.EntryState{memory.EntryStateSuperseded},
	})
	if err != nil || result.CorpusDocumentCount != 1 || len(result.Matches) != 1 || result.Matches[0].EntryID != old.Entry.ID {
		t.Fatalf("superseded-only search = %#v, %v", result, err)
	}
}

func newLexicalSearchTestService(t *testing.T, store *memoryBackend.Store) *Service {
	t.Helper()
	nextID := 0x100
	service, err := New(Config{
		Store: store,
		Actor: func(context.Context) (memory.Actor, error) {
			return memory.Actor{Kind: memory.ActorKindUser, ID: "user:test"}, nil
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

func createLexicalSearchFixture(t *testing.T, ctx context.Context, service *Service, title string, scope memory.Scope, validUntil time.Time) (memory.Chunk, memory.Entry) {
	return createLexicalSearchEntryWithValidity(t, ctx, service, title, scope, title+" needle", validUntil)
}

func createLexicalSearchEntry(t *testing.T, ctx context.Context, service *Service, title string, scope memory.Scope, entryTitle string) (memory.Chunk, memory.Entry) {
	return createLexicalSearchEntryWithValidity(t, ctx, service, title, scope, entryTitle, time.Time{})
}

func createLexicalSearchEntryWithValidity(t *testing.T, ctx context.Context, service *Service, title string, scope memory.Scope, entryTitle string, validUntil time.Time) (memory.Chunk, memory.Entry) {
	t.Helper()
	chunk, err := service.CreateChunk(ctx, CreateChunkRequest{Chunk: memory.Chunk{
		Title: title, Kind: memory.ChunkKindReference, Scope: scope,
	}})
	if err != nil {
		t.Fatalf("CreateChunk(%q) error = %v", title, err)
	}
	entry, err := service.CreateEntry(ctx, CreateEntryRequest{
		ChunkID: chunk.Chunk.ID,
		Entry:   memory.Entry{Title: entryTitle, Kind: memory.EntryKindFact, ValidUntil: validUntil},
	})
	if err != nil {
		t.Fatalf("CreateEntry(%q) error = %v", title, err)
	}
	return chunk.Chunk, entry.Entry
}
