package store

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
)

func TestPaginateEntriesUsesStableExclusiveCursor(t *testing.T) {
	t.Parallel()
	entries := listTestEntries(5)
	request := EntryListRequest{Sort: EntrySortTitle, Limit: 2}
	var got []knowledge.EntryID
	for {
		page, err := PaginateEntries(entries, request, 7)
		if err != nil {
			t.Fatalf("PaginateEntries() error = %v", err)
		}
		for _, entry := range page.Entries {
			got = append(got, entry.ID)
		}
		if page.NextCursor == "" {
			break
		}
		request.Cursor = page.NextCursor
	}
	if len(got) != len(entries) {
		t.Fatalf("paged IDs = %v", got)
	}
	for index, entryID := range got {
		if entryID != entries[index].ID {
			t.Fatalf("paged IDs = %v, want input ID order", got)
		}
	}
}

func TestPaginateEntriesFiltersAndDefaultsToRecentFirst(t *testing.T) {
	t.Parallel()
	entries := listTestEntries(5)
	entries[0].Tags = []string{"go", "linux"}
	entries[0].Applicability.Locales = []string{"da-DK"}
	entries[1].State = knowledge.EntryStateArchived
	page, err := PaginateEntries(entries, EntryListRequest{Filter: EntryFilter{
		ChunkIDs: []knowledge.ChunkID{entries[0].ChunkID}, States: []knowledge.EntryState{knowledge.EntryStateActive},
		Tags: []string{" LINUX ", "go"}, Locales: []string{"da_dk", "en-US"},
	}}, 1)
	if err != nil {
		t.Fatalf("PaginateEntries() error = %v", err)
	}
	if len(page.Entries) != 1 || page.Entries[0].ID != entries[0].ID {
		t.Fatalf("filtered page = %#v", page)
	}
	page, err = PaginateEntries(entries, EntryListRequest{Limit: 2}, 1)
	if err != nil {
		t.Fatalf("PaginateEntries(defaults) error = %v", err)
	}
	if len(page.Entries) != 2 || page.Entries[0].UpdatedAt.Before(page.Entries[1].UpdatedAt) {
		t.Fatalf("default page is not recent-first: %#v", page.Entries)
	}
}

func TestPaginateEntriesFiltersValidityReviewAndStalenessAtExplicitTime(t *testing.T) {
	t.Parallel()
	entries := listTestEntries(4)
	asOf := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	entries[0].ValidFrom = asOf.Add(time.Hour)
	entries[1].ValidUntil = asOf
	entries[2].ReviewAfter = asOf

	valid, err := PaginateEntries(entries, EntryListRequest{Filter: EntryFilter{ValidAt: asOf}}, 1)
	if err != nil {
		t.Fatalf("valid-at query: %v", err)
	}
	if len(valid.Entries) != 2 || valid.Entries[0].ID != entries[3].ID || valid.Entries[1].ID != entries[2].ID {
		t.Fatalf("valid-at entries = %#v", valid.Entries)
	}
	due, err := PaginateEntries(entries, EntryListRequest{Filter: EntryFilter{ReviewDueAt: asOf}}, 1)
	if err != nil || len(due.Entries) != 1 || due.Entries[0].ID != entries[2].ID {
		t.Fatalf("review-due entries = %#v, %v", due.Entries, err)
	}
	stale, err := PaginateEntries(entries, EntryListRequest{Filter: EntryFilter{StaleAt: asOf}}, 1)
	if err != nil || len(stale.Entries) != 2 {
		t.Fatalf("stale entries = %#v, %v", stale.Entries, err)
	}
}

func TestEntryTemporalFilterCursorNormalizesTimezoneAndBindsInstant(t *testing.T) {
	t.Parallel()
	entries := listTestEntries(3)
	local := time.Date(2026, 8, 23, 14, 0, 0, 0, time.FixedZone("CEST", 2*60*60))
	request := EntryListRequest{Filter: EntryFilter{ValidAt: local}, Sort: EntrySortTitle, Limit: 1}
	page, err := PaginateEntries(entries, request, 1)
	if err != nil || page.NextCursor == "" {
		t.Fatalf("first page = %#v, %v", page, err)
	}
	request.Cursor = page.NextCursor
	request.Filter.ValidAt = local.UTC()
	if _, err := PaginateEntries(entries, request, 1); err != nil {
		t.Fatalf("equivalent UTC cursor rejected: %v", err)
	}
	request.Filter.ValidAt = local.Add(time.Second)
	if _, err := PaginateEntries(entries, request, 1); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("changed instant error = %v, want ErrInvalidCursor", err)
	}
}

func TestPaginateEntriesRejectsReusedStaleOrInvalidCursor(t *testing.T) {
	t.Parallel()
	entries := listTestEntries(3)
	request := EntryListRequest{Sort: EntrySortTitle, Limit: 1}
	page, err := PaginateEntries(entries, request, 4)
	if err != nil || page.NextCursor == "" {
		t.Fatalf("first page = %#v, %v", page, err)
	}
	changed := request
	changed.Cursor = page.NextCursor
	changed.Filter.States = []knowledge.EntryState{knowledge.EntryStateArchived}
	if _, err := PaginateEntries(entries, changed, 4); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("changed filter error = %v, want ErrInvalidCursor", err)
	}
	request.Cursor = page.NextCursor
	if _, err := PaginateEntries(entries, request, 5); !errors.Is(err, ErrStaleCursor) {
		t.Fatalf("retired generation error = %v, want ErrStaleCursor", err)
	}
	for _, invalid := range []EntryListRequest{
		{Sort: "unknown"}, {Limit: 201},
		{Filter: EntryFilter{Kinds: []knowledge.EntryKind{knowledge.EntryKindUnspecified}}},
		{Filter: EntryFilter{Locales: []string{"not a locale !!!"}}},
	} {
		if _, err := PaginateEntries(entries, invalid, 1); err == nil {
			t.Errorf("PaginateEntries(%#v) unexpectedly succeeded", invalid)
		}
	}
}

func listTestEntries(count int) []knowledge.Entry {
	base := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	entries := make([]knowledge.Entry, 0, count)
	for index := range count {
		entries = append(entries, knowledge.Entry{
			ID:      knowledge.EntryID(fmt.Sprintf("01a01688-fc5d-7f7d-8bb8-%012x", index+1)),
			ChunkID: "01a01688-fc5d-7f7d-8bb8-de244977f8ff", Title: "Same title",
			Kind: knowledge.EntryKindFact, Scope: knowledge.Scope{Kind: knowledge.ScopeKindGlobal}, State: knowledge.EntryStateActive,
			CreatedAt: base.Add(time.Duration(index) * time.Minute), UpdatedAt: base.Add(time.Duration(index) * time.Minute),
		})
	}
	return entries
}
