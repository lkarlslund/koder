package store

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/memory"
)

const historyChunkID memory.ChunkID = "019f132e-4f3a-739a-9ab2-5198dcd19e67"

func historyChunk(number uint64) CanonicalRecord {
	created := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	value := memory.Chunk{
		ID: historyChunkID, Title: fmt.Sprintf("Revision %d", number), Kind: memory.ChunkKindReference,
		Scope: memory.Scope{Kind: memory.ScopeKindGlobal}, Visibility: memory.VisibilityInstallation,
		State: memory.ChunkStateActive, SchemaVersion: 1,
		Revision: memory.Revision{
			Number: number, ID: memory.RevisionID(fmt.Sprintf("01a01688-fc5d-7f7d-8bb8-%012x", number)),
			Actor:  memory.Actor{Kind: memory.ActorKindSystem, ID: "history-test"},
			Reason: fmt.Sprintf("change %d", number), CreatedAt: created.Add(time.Duration(number-1) * time.Second),
		},
		CreatedAt: created, UpdatedAt: created.Add(time.Duration(number-1) * time.Second),
	}
	return CanonicalRecord{Kind: RecordKindChunk, Chunk: &value}
}

func TestPaginateRevisionsUsesStableNewestFirstCursor(t *testing.T) {
	t.Parallel()
	records := []CanonicalRecord{historyChunk(2), historyChunk(5), historyChunk(1), historyChunk(4), historyChunk(3)}
	request := RevisionListRequest{
		Object: memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: string(historyChunkID)}, Limit: 2,
	}

	var got []uint64
	for {
		page, err := PaginateRevisions(records, request)
		if err != nil {
			t.Fatalf("PaginateRevisions() error = %v", err)
		}
		for _, record := range page.Revisions {
			got = append(got, record.RevisionMetadata().Number)
		}
		if page.NextCursor == "" {
			break
		}
		request.Cursor = page.NextCursor
	}
	if fmt.Sprint(got) != "[5 4 3 2 1]" {
		t.Fatalf("revision order = %v, want [5 4 3 2 1]", got)
	}
}

func TestRevisionCursorIsBoundToObject(t *testing.T) {
	t.Parallel()
	request := RevisionListRequest{
		Object: memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: string(historyChunkID)}, Limit: 1,
	}
	page, err := PaginateRevisions([]CanonicalRecord{historyChunk(1), historyChunk(2)}, request)
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	request.Object.ID = "019f132e-4f3a-739a-9ab2-5198dcd19e68"
	request.Cursor = page.NextCursor
	if _, err := PaginateRevisions(nil, request); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("reused cursor error = %v, want ErrInvalidCursor", err)
	}
}

func TestPaginateRevisionsValidatesRequestAndHistory(t *testing.T) {
	t.Parallel()
	valid := RevisionListRequest{Object: memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: string(historyChunkID)}}
	tests := []struct {
		name    string
		records []CanonicalRecord
		request RevisionListRequest
	}{
		{name: "missing object", request: RevisionListRequest{}},
		{name: "excessive limit", request: RevisionListRequest{Object: valid.Object, Limit: 201}},
		{name: "different object", request: valid, records: func() []CanonicalRecord {
			record := historyChunk(1)
			record.Chunk.ID = "019f132e-4f3a-739a-9ab2-5198dcd19e68"
			return []CanonicalRecord{record}
		}()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := PaginateRevisions(test.records, test.request); err == nil {
				t.Fatal("PaginateRevisions() unexpectedly succeeded")
			}
		})
	}
}
