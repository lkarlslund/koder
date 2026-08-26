package store

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/memory"
)

func TestPaginateExactSearchRanksAllExactFieldsAndUsesStableCursor(t *testing.T) {
	t.Parallel()
	chunk := exactTestChunk("019f132e-4f3a-739a-9ab2-5198dcd19e67", "Partition Tools")
	entry := exactTestEntry("01a01f76-1ff6-7c1d-967a-66ad5703dd33", chunk.ID, "sfdisk")
	entry.Aliases = []string{"Partition tools"}
	tagged := exactTestChunk("01a01688-fc5d-7f7d-8bb8-de244977f8a1", "Linux utilities")
	tagged.Tags = []string{"partition-tools"}
	records := []CanonicalRecord{
		{Kind: RecordKindChunk, Chunk: &tagged},
		{Kind: RecordKindEntry, Entry: &entry},
		{Kind: RecordKindChunk, Chunk: &chunk},
	}

	request := ExactSearchRequest{Query: "  PARTITION   TOOLS ", Limit: 1}
	var got []ExactMatchField
	for {
		page, err := PaginateExactSearch(records, request, 7)
		if err != nil {
			t.Fatalf("PaginateExactSearch() error = %v", err)
		}
		if len(page.Hits) != 1 {
			t.Fatalf("page hits = %#v", page.Hits)
		}
		got = append(got, page.Hits[0].Matches[0])
		if page.NextCursor == "" {
			break
		}
		request.Cursor = page.NextCursor
	}
	want := []ExactMatchField{ExactMatchTitle, ExactMatchAlias, ExactMatchTag}
	if len(got) != len(want) {
		t.Fatalf("match order = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("match order = %v, want %v", got, want)
		}
	}
}

func TestPaginateExactSearchIDIncludesEveryRecordFamilyAndFiltersKinds(t *testing.T) {
	t.Parallel()
	const sharedID = "01a01688-fc5d-7f7d-8bb8-de244977f8a1"
	chunk := exactTestChunk(sharedID, "Chunk")
	entry := exactTestEntry(sharedID, chunk.ID, "Entry")
	link := exactTestLink(sharedID, chunk.ID, entry.ID)
	evidence := exactTestEvidence(sharedID)
	records := []CanonicalRecord{
		{Kind: RecordKindChunk, Chunk: &chunk},
		{Kind: RecordKindEntry, Entry: &entry},
		{Kind: RecordKindLink, Link: &link},
		{Kind: RecordKindEvidence, Evidence: &evidence},
	}

	page, err := PaginateExactSearch(records, ExactSearchRequest{Query: sharedID}, 1)
	if err != nil || len(page.Hits) != 4 {
		t.Fatalf("all-family ID page = %#v, %v", page, err)
	}
	page, err = PaginateExactSearch(records, ExactSearchRequest{
		Query: sharedID, Kinds: []RecordKind{RecordKindEvidence, RecordKindEntry, RecordKindEntry},
	}, 1)
	if err != nil || len(page.Hits) != 2 {
		t.Fatalf("filtered ID page = %#v, %v", page, err)
	}
	for _, hit := range page.Hits {
		if hit.Matches[0] != ExactMatchID {
			t.Fatalf("ID hit matches = %v", hit.Matches)
		}
	}
}

func TestPaginateExactSearchRejectsInvalidAndStaleCursors(t *testing.T) {
	t.Parallel()
	chunk := exactTestChunk("019f132e-4f3a-739a-9ab2-5198dcd19e67", "Same")
	other := exactTestChunk("01a01688-fc5d-7f7d-8bb8-de244977f8a1", "Same")
	records := []CanonicalRecord{{Kind: RecordKindChunk, Chunk: &chunk}, {Kind: RecordKindChunk, Chunk: &other}}
	request := ExactSearchRequest{Query: "same", Limit: 1}
	page, err := PaginateExactSearch(records, request, 2)
	if err != nil || page.NextCursor == "" {
		t.Fatalf("first page = %#v, %v", page, err)
	}
	changed := request
	changed.Query = "different"
	changed.Cursor = page.NextCursor
	if _, err := PaginateExactSearch(records, changed, 2); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("changed query error = %v, want ErrInvalidCursor", err)
	}
	request.Cursor = page.NextCursor
	if _, err := PaginateExactSearch(records, request, 3); !errors.Is(err, ErrStaleCursor) {
		t.Fatalf("retired generation error = %v, want ErrStaleCursor", err)
	}
}

func TestNormalizeExactSearchRequestRejectsInvalidBoundsAndKinds(t *testing.T) {
	t.Parallel()
	for _, request := range []ExactSearchRequest{
		{},
		{Query: strings.Repeat("a", maxExactSearchQuery+1)},
		{Query: "valid", Limit: maxExactSearchLimit + 1},
		{Query: "valid", Kinds: []RecordKind{"unknown"}},
	} {
		if _, err := NormalizeExactSearchRequest(request); err == nil {
			t.Errorf("NormalizeExactSearchRequest(%#v) unexpectedly succeeded", request)
		}
	}
}

func exactTestChunk(id, title string) memory.Chunk {
	now := time.Date(2026, 8, 22, 16, 0, 0, 0, time.UTC)
	return memory.Chunk{
		ID: memory.ChunkID(id), Title: title, Kind: memory.ChunkKindReference,
		Scope: memory.Scope{Kind: memory.ScopeKindGlobal}, Visibility: memory.VisibilityInstallation,
		State: memory.ChunkStateActive, SchemaVersion: 1,
		Revision:  memory.Revision{Number: 1, ID: memory.RevisionID(id), Actor: memory.Actor{Kind: memory.ActorKindSystem, ID: "test"}, CreatedAt: now},
		CreatedAt: now, UpdatedAt: now,
	}
}

func exactTestEntry(id string, chunkID memory.ChunkID, title string) memory.Entry {
	now := time.Date(2026, 8, 22, 16, 0, 0, 0, time.UTC)
	return memory.Entry{
		ID: memory.EntryID(id), ChunkID: chunkID, Kind: memory.EntryKindFact, Title: title,
		Scope:        memory.Scope{Kind: memory.ScopeKindGlobal},
		Verification: memory.Verification{Status: memory.VerificationStatusUnverified},
		State:        memory.EntryStateActive,
		Revision:     memory.Revision{Number: 1, ID: memory.RevisionID(id), Actor: memory.Actor{Kind: memory.ActorKindSystem, ID: "test"}, CreatedAt: now},
		CreatedAt:    now, UpdatedAt: now,
	}
}

func exactTestLink(id string, chunkID memory.ChunkID, entryID memory.EntryID) memory.Link {
	now := time.Date(2026, 8, 22, 16, 0, 0, 0, time.UTC)
	return memory.Link{
		ID:     memory.LinkID(id),
		Source: memory.ObjectRef{Kind: memory.ObjectKindEntry, ID: string(entryID)},
		Target: memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: string(chunkID)},
		Kind:   memory.LinkKindPartOf, State: memory.LinkStateActive,
		Revision:  memory.Revision{Number: 1, ID: memory.RevisionID(id), Actor: memory.Actor{Kind: memory.ActorKindSystem, ID: "test"}, CreatedAt: now},
		CreatedAt: now, UpdatedAt: now,
	}
}

func exactTestEvidence(id string) memory.Evidence {
	now := time.Date(2026, 8, 22, 16, 0, 0, 0, time.UTC)
	return memory.Evidence{
		ID: memory.EvidenceID(id), Type: memory.EvidenceTypeObservation,
		Quality: memory.EvidenceQualityPrimary, Source: memory.Source{ID: "test"},
		Actor: memory.Actor{Kind: memory.ActorKindSystem, ID: "test"}, CreatedAt: now,
	}
}
