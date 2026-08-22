package pebble

import (
	"context"
	"errors"
	"testing"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

func TestScanEntriesFiltersOneConsistentSnapshot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	activeGlobal := txEntry()
	activeGlobal.Applicability.Locales = []string{"da-DK"}
	archivedGlobal := txEntry()
	archivedGlobal.ID = txOtherEntry
	archivedGlobal.Revision.ID = "01a01688-fc5d-7f7d-8bb8-000000000101"
	archivedGlobal.State = knowledge.EntryStateArchived
	project := txEntry()
	project.ID = "01a01f76-1ff6-7c1d-967a-66ad5703dd35"
	project.Revision.ID = "01a01688-fc5d-7f7d-8bb8-000000000102"
	project.Scope = knowledge.Scope{Kind: knowledge.ScopeKindProject, Selector: "project:test"}
	if err := store.Update(ctx, func(tx knowledgeStore.WriteTx) error {
		for _, entry := range []knowledge.Entry{activeGlobal, archivedGlobal, project} {
			if err := tx.PutEntry(ctx, entry, 0); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	var got []knowledge.EntryID
	err = store.ScanEntries(ctx, knowledgeStore.EntryFilter{
		States:     []knowledge.EntryState{knowledge.EntryStateActive},
		ScopeKinds: []knowledge.ScopeKind{knowledge.ScopeKindGlobal}, Locales: []string{"da_dk"},
	}, func(entry knowledge.Entry) error {
		got = append(got, entry.ID)
		return nil
	})
	if err != nil || len(got) != 1 || got[0] != activeGlobal.ID {
		t.Fatalf("ScanEntries() = %v, %v", got, err)
	}
	want := errors.New("stop scan")
	if err := store.ScanEntries(ctx, knowledgeStore.EntryFilter{}, func(knowledge.Entry) error { return want }); !errors.Is(err, want) {
		t.Fatalf("ScanEntries(callback error) = %v", err)
	}
	if err := store.ScanEntries(ctx, knowledgeStore.EntryFilter{
		ScopeKinds: []knowledge.ScopeKind{knowledge.ScopeKindUnspecified},
	}, func(knowledge.Entry) error { return nil }); !errors.Is(err, knowledge.ErrInvalidRecord) {
		t.Fatalf("ScanEntries(invalid filter) = %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := store.ScanEntries(canceled, knowledgeStore.EntryFilter{}, func(knowledge.Entry) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("ScanEntries(canceled) = %v", err)
	}
}
