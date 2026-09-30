package pebbledriver

import (
	"context"
	"fmt"
	"testing"

	"github.com/lkarlslund/koder/internal/store/driver"
)

func openTestBackend(t *testing.T) *Backend {
	t.Helper()
	backend, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	return backend
}

func listIDs(t *testing.T, backend *Backend, name, value string) int {
	t.Helper()
	items, err := backend.List(context.Background(), "things", &driver.IndexLookup{Name: name, Value: value})
	if err != nil {
		t.Fatal(err)
	}
	return len(items)
}

func TestPutReplacesIndexEntries(t *testing.T) {
	ctx := context.Background()
	backend := openTestBackend(t)
	if err := backend.Put(ctx, "things", "a", []byte(`{}`), map[string]driver.IndexValue{"owner": {Value: "old"}}); err != nil {
		t.Fatal(err)
	}
	if err := backend.Put(ctx, "things", "b", []byte(`{}`), map[string]driver.IndexValue{"owner": {Value: "old"}}); err != nil {
		t.Fatal(err)
	}
	if err := backend.Put(ctx, "things", "a", []byte(`{}`), map[string]driver.IndexValue{"owner": {Value: "new"}}); err != nil {
		t.Fatal(err)
	}
	if got := listIDs(t, backend, "owner", "old"); got != 1 {
		t.Fatalf("old index holds %d records, want only b", got)
	}
	if got := listIDs(t, backend, "owner", "new"); got != 1 {
		t.Fatalf("new index holds %d records, want a", got)
	}
	if err := backend.Delete(ctx, "things", "a"); err != nil {
		t.Fatal(err)
	}
	if got := listIDs(t, backend, "owner", "new"); got != 0 {
		t.Fatalf("deleted record still indexed %d times", got)
	}
}

func TestPutCleansIndexesOfRecordsWrittenWithoutRefs(t *testing.T) {
	ctx := context.Background()
	backend := openTestBackend(t)
	if err := backend.Put(ctx, "things", "a", []byte(`{}`), map[string]driver.IndexValue{"owner": {Value: "old", Order: "0001"}}); err != nil {
		t.Fatal(err)
	}
	// Simulate a record stored before index refs were kept.
	if err := backend.db.Delete(indexRefsKey("things", "a"), nil); err != nil {
		t.Fatal(err)
	}
	if err := backend.Put(ctx, "things", "a", []byte(`{}`), map[string]driver.IndexValue{"owner": {Value: "new"}}); err != nil {
		t.Fatal(err)
	}
	if got := listIDs(t, backend, "owner", "old"); got != 0 {
		t.Fatalf("legacy index entry survived re-put: %d", got)
	}
}

func TestAddIndexEntriesAreRemovedOnPut(t *testing.T) {
	ctx := context.Background()
	backend := openTestBackend(t)
	if err := backend.Put(ctx, "things", "a", []byte(`{}`), map[string]driver.IndexValue{"owner": {Value: "x"}}); err != nil {
		t.Fatal(err)
	}
	if err := backend.AddIndexEntries(ctx, "things", "extra", "x", []driver.OrderedIndexEntry{{ID: "a", Order: "0001"}}); err != nil {
		t.Fatal(err)
	}
	if got := listIDs(t, backend, "extra", "x"); got != 1 {
		t.Fatalf("added index holds %d records", got)
	}
	if err := backend.Put(ctx, "things", "a", []byte(`{}`), map[string]driver.IndexValue{"owner": {Value: "x"}}); err != nil {
		t.Fatal(err)
	}
	if got := listIDs(t, backend, "extra", "x"); got != 0 {
		t.Fatalf("added index entry survived re-put: %d", got)
	}
}

// BenchmarkPutExisting measures updating one record in a namespace that
// already holds many indexed records, as when a tool call updates its
// timeline item in a long-lived store.
func BenchmarkPutExisting(b *testing.B) {
	for _, records := range []int{100, 10000} {
		b.Run(fmt.Sprintf("records=%d", records), func(b *testing.B) {
			ctx := context.Background()
			backend, err := Open(b.TempDir())
			if err != nil {
				b.Fatal(err)
			}
			defer func() { _ = backend.Close() }()
			for i := range records {
				id := fmt.Sprintf("item-%06d", i)
				if err := backend.Put(ctx, "timeline", id, []byte(`{}`), map[string]driver.IndexValue{"chat": {Value: fmt.Sprintf("chat-%d", i%50), Order: id}}); err != nil {
					b.Fatal(err)
				}
			}
			index := map[string]driver.IndexValue{"chat": {Value: "chat-7", Order: "item-000007"}}
			b.ResetTimer()
			for b.Loop() {
				if err := backend.Put(ctx, "timeline", "item-000007", []byte(`{"updated":true}`), index); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
