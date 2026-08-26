package store

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/memory"
)

func TestPaginateChunksUsesStableExclusiveCursor(t *testing.T) {
	t.Parallel()
	chunks := listTestChunks(5)
	request := ChunkListRequest{Sort: ChunkSortTitle, Limit: 2}
	var got []memory.ChunkID
	for {
		page, err := PaginateChunks(chunks, request, 7)
		if err != nil {
			t.Fatalf("PaginateChunks() error = %v", err)
		}
		for _, chunk := range page.Chunks {
			got = append(got, chunk.ID)
		}
		if page.NextCursor == "" {
			break
		}
		request.Cursor = page.NextCursor
	}
	if len(got) != len(chunks) {
		t.Fatalf("paged IDs = %v", got)
	}
	for index, chunkID := range got {
		if chunkID != chunks[index].ID {
			t.Fatalf("paged IDs = %v, want input ID order", got)
		}
	}
}

func TestPaginateChunksFiltersAndDefaultsToRecentFirst(t *testing.T) {
	t.Parallel()
	chunks := listTestChunks(5)
	chunks[0].Tags = []string{"go", "linux"}
	chunks[0].Locale = "da-DK"
	chunks[1].State = memory.ChunkStateArchived
	page, err := PaginateChunks(chunks, ChunkListRequest{
		Filter: ChunkFilter{
			States: []memory.ChunkState{memory.ChunkStateActive}, Tags: []string{" LINUX ", "go"}, Locale: "da_dk",
		},
	}, 1)
	if err != nil {
		t.Fatalf("PaginateChunks() error = %v", err)
	}
	if len(page.Chunks) != 1 || page.Chunks[0].ID != chunks[0].ID {
		t.Fatalf("filtered page = %#v", page)
	}

	page, err = PaginateChunks(chunks, ChunkListRequest{Limit: 2}, 1)
	if err != nil {
		t.Fatalf("PaginateChunks(defaults) error = %v", err)
	}
	if len(page.Chunks) != 2 || page.Chunks[0].UpdatedAt.Before(page.Chunks[1].UpdatedAt) {
		t.Fatalf("default page is not recent-first: %#v", page.Chunks)
	}
}

func TestPaginateChunksFiltersReviewDueAndStaleAtExplicitTime(t *testing.T) {
	t.Parallel()
	chunks := listTestChunks(3)
	asOf := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	chunks[0].ReviewAfter = asOf.Add(-time.Hour)
	chunks[1].ReviewAfter = asOf
	chunks[2].ReviewAfter = asOf.Add(time.Hour)
	for _, filter := range []ChunkFilter{{ReviewDueAt: asOf}, {StaleAt: asOf}} {
		page, err := PaginateChunks(chunks, ChunkListRequest{Filter: filter}, 1)
		if err != nil || len(page.Chunks) != 2 {
			t.Fatalf("temporal chunks = %#v, %v", page.Chunks, err)
		}
	}
}

func TestPaginateChunksRejectsReusedOrStaleCursor(t *testing.T) {
	t.Parallel()
	chunks := listTestChunks(3)
	request := ChunkListRequest{Sort: ChunkSortTitle, Limit: 1}
	page, err := PaginateChunks(chunks, request, 4)
	if err != nil || page.NextCursor == "" {
		t.Fatalf("first page = %#v, %v", page, err)
	}
	changed := request
	changed.Cursor = page.NextCursor
	changed.Filter.States = []memory.ChunkState{memory.ChunkStateArchived}
	if _, err := PaginateChunks(chunks, changed, 4); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("changed filter error = %v, want ErrInvalidCursor", err)
	}
	request.Cursor = page.NextCursor
	if _, err := PaginateChunks(chunks, request, 5); !errors.Is(err, ErrStaleCursor) {
		t.Fatalf("retired generation error = %v, want ErrStaleCursor", err)
	}
}

func TestPaginateChunksRejectsInvalidOptions(t *testing.T) {
	t.Parallel()
	chunks := listTestChunks(1)
	for _, request := range []ChunkListRequest{
		{Sort: "unknown"},
		{Limit: 201},
		{Filter: ChunkFilter{Kinds: []memory.ChunkKind{memory.ChunkKindUnspecified}}},
		{Filter: ChunkFilter{Locale: "not a locale !!!"}},
	} {
		if _, err := PaginateChunks(chunks, request, 1); err == nil {
			t.Errorf("PaginateChunks(%#v) unexpectedly succeeded", request)
		}
	}
}

func listTestChunks(count int) []memory.Chunk {
	base := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	chunks := make([]memory.Chunk, 0, count)
	for index := range count {
		chunks = append(chunks, memory.Chunk{
			ID:    memory.ChunkID(fmt.Sprintf("01a01688-fc5d-7f7d-8bb8-%012x", index+1)),
			Title: "Same title", Kind: memory.ChunkKindReference,
			Scope: memory.Scope{Kind: memory.ScopeKindGlobal}, State: memory.ChunkStateActive,
			CreatedAt: base.Add(time.Duration(index) * time.Minute), UpdatedAt: base.Add(time.Duration(index) * time.Minute),
		})
	}
	return chunks
}
