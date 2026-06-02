package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/cockroachdb/pebble"

	"github.com/lkarlslund/koder/internal/store/driver"
)

type testNote struct {
	ID     string
	ChatID string
	Body   string
}

type testMarker struct {
	ID     string
	NoteID string
	Label  string
}

func TestCollectionRoundTripAndIndex(t *testing.T) {
	for _, backend := range []string{BackendPebble, BackendJSONFS} {
		t.Run(backend, func(t *testing.T) {
			st := openTestStore(t, backend)
			notes := testNotes(st)

			first, err := notes.Insert(context.Background(), testNote{ChatID: "chat-7", Body: "first"})
			if err != nil {
				t.Fatal(err)
			}
			second, err := notes.Insert(context.Background(), testNote{ChatID: "chat-8", Body: "second"})
			if err != nil {
				t.Fatal(err)
			}
			first.Body = "updated"
			if err := notes.Put(context.Background(), first); err != nil {
				t.Fatal(err)
			}
			got, err := notes.Get(context.Background(), first.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Body != "updated" {
				t.Fatalf("body = %q", got.Body)
			}
			indexed, err := notes.List(context.Background(), ByIndex[testNote]("chat", "chat-7"))
			if err != nil {
				t.Fatal(err)
			}
			if len(indexed) != 1 || indexed[0].ID != first.ID {
				t.Fatalf("indexed = %#v", indexed)
			}
			if err := notes.Delete(context.Background(), second.ID); err != nil {
				t.Fatal(err)
			}
			reloaded, err := notes.List(context.Background(), All[testNote]())
			if err != nil {
				t.Fatal(err)
			}
			if len(reloaded) != 1 || reloaded[0].ID != first.ID {
				t.Fatalf("reloaded = %#v", reloaded)
			}
		})
	}
}

func TestCollectionTransactionPersistsMultipleCollections(t *testing.T) {
	for _, backend := range []string{BackendPebble, BackendJSONFS} {
		t.Run(backend, func(t *testing.T) {
			st := openTestStore(t, backend)
			notes := testNotes(st)
			markers := testMarkers(st)

			var inserted testNote
			if err := st.Transaction(context.Background(), func(tx *Tx) error {
				var err error
				inserted, err = notes.InsertTx(tx, context.Background(), testNote{ChatID: "chat-42", Body: "inside transaction"})
				if err != nil {
					return err
				}
				_, err = markers.InsertTx(tx, context.Background(), testMarker{NoteID: inserted.ID, Label: "linked"})
				return err
			}); err != nil {
				t.Fatal(err)
			}
			reloadedNotes, err := notes.List(context.Background(), ByIndex[testNote]("chat", "chat-42"))
			if err != nil {
				t.Fatal(err)
			}
			if len(reloadedNotes) != 1 || reloadedNotes[0].ID != inserted.ID {
				t.Fatalf("reloaded notes = %#v", reloadedNotes)
			}
			reloadedMarkers, err := markers.List(context.Background(), ByIndex[testMarker]("note", inserted.ID))
			if err != nil {
				t.Fatal(err)
			}
			if len(reloadedMarkers) != 1 || reloadedMarkers[0].Label != "linked" {
				t.Fatalf("reloaded markers = %#v", reloadedMarkers)
			}
		})
	}
}

func TestJSONFSWritesInspectableCollectionFiles(t *testing.T) {
	root := t.TempDir()
	st, err := OpenWithOptions(root, Options{Backend: BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	note, err := testNotes(st).Insert(context.Background(), testNote{ChatID: "chat-7", Body: "inspect"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "store-jsonfs-v7", "collections", "test-notes", note.ID+".json")); err != nil {
		t.Fatalf("expected inspectable note JSON file: %v", err)
	}
}

func TestPebbleOperationsAfterCloseReturnError(t *testing.T) {
	st := openTestStore(t, BackendPebble)
	notes := testNotes(st)
	inserted, err := notes.Insert(context.Background(), testNote{ChatID: "chat-7", Body: "closed"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	assertNoPanic(t, func() {
		_, err = notes.Get(context.Background(), inserted.ID)
	})
	if !errors.Is(err, pebble.ErrClosed) {
		t.Fatalf("expected closed pebble get error, got %v", err)
	}

	assertNoPanic(t, func() {
		_, err = notes.List(context.Background(), All[testNote]())
	})
	if !errors.Is(err, pebble.ErrClosed) {
		t.Fatalf("expected closed pebble list error, got %v", err)
	}

	assertNoPanic(t, func() {
		err = notes.Put(context.Background(), inserted)
	})
	if !errors.Is(err, pebble.ErrClosed) {
		t.Fatalf("expected closed pebble put error, got %v", err)
	}
}

func TestOpenResetsStoreWhenSchemaVersionChanges(t *testing.T) {
	for _, backend := range []string{BackendPebble, BackendJSONFS} {
		t.Run(backend, func(t *testing.T) {
			root := t.TempDir()
			st := openTestStoreAt(t, backend, root)
			if _, err := testNotes(st).Insert(context.Background(), testNote{ChatID: "chat-7", Body: "old"}); err != nil {
				t.Fatal(err)
			}
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
			writeStoreMetaForTest(t, root, backend, driver.Meta{
				SchemaVersion: driver.SchemaVersion - 1,
				Encoding:      driver.EncodingJSON,
				Backend:       backend,
			})

			st = openTestStoreAt(t, backend, root)
			notes, err := testNotes(st).List(context.Background(), All[testNote]())
			if err != nil {
				t.Fatal(err)
			}
			if len(notes) != 0 {
				t.Fatalf("expected old notes to be cleared after schema reset, got %#v", notes)
			}
		})
	}
}

func assertNoPanic(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("unexpected panic: %v", recovered)
		}
	}()
	fn()
}

func TestCollectionListIsSortedByID(t *testing.T) {
	st := openTestStore(t, BackendJSONFS)
	notes := testNotes(st)
	for _, id := range []string{"c", "a", "b"} {
		if err := notes.Put(context.Background(), testNote{ID: id, ChatID: "chat-7", Body: id}); err != nil {
			t.Fatal(err)
		}
	}
	items, err := notes.List(context.Background(), All[testNote]())
	if err != nil {
		t.Fatal(err)
	}
	got := []string{items[0].ID, items[1].ID, items[2].ID}
	if !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Fatalf("ids = %#v", got)
	}
}

func testNotes(st *Store) Collection[testNote] {
	return NewCollection(st, CollectionSpec[testNote]{
		Namespace: "test-notes",
		GetID:     func(v testNote) string { return v.ID },
		SetID:     func(v *testNote, id string) { v.ID = id },
		Indexes: []IndexSpec[testNote]{
			{Name: "chat", Value: func(v testNote) string { return v.ChatID }},
		},
	})
}

func testMarkers(st *Store) Collection[testMarker] {
	return NewCollection(st, CollectionSpec[testMarker]{
		Namespace: "test-markers",
		GetID:     func(v testMarker) string { return v.ID },
		SetID:     func(v *testMarker, id string) { v.ID = id },
		Indexes: []IndexSpec[testMarker]{
			{Name: "note", Value: func(v testMarker) string { return v.NoteID }},
		},
	})
}

func openTestStore(t *testing.T, backend string) *Store {
	t.Helper()
	return openTestStoreAt(t, backend, t.TempDir())
}

func openTestStoreAt(t *testing.T, backend, root string) *Store {
	t.Helper()
	st, err := OpenWithOptions(root, Options{Backend: backend})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
	})
	return st
}

func writeStoreMetaForTest(t *testing.T, root, backend string, meta driver.Meta) {
	t.Helper()
	switch backend {
	case BackendJSONFS:
		if err := driver.WriteJSONFile(filepath.Join(root, "store-jsonfs-v7", "meta.json"), meta); err != nil {
			t.Fatal(err)
		}
	case BackendPebble:
		db, err := pebble.Open(filepath.Join(root, "store-pebble-v7"), &pebble.Options{})
		if err != nil {
			t.Fatal(err)
		}
		data, err := driver.EncodeJSON(meta)
		if err != nil {
			_ = db.Close()
			t.Fatal(err)
		}
		if err := db.Set([]byte("meta/store"), data, pebble.Sync); err != nil {
			_ = db.Close()
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unknown backend %q", backend)
	}
}
