package memory

import (
	"context"
	"testing"

	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

func TestScanCanonicalIsStableAndDetached(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Update(ctx, func(tx knowledgeStore.WriteTx) error {
		if err := tx.PutChunk(ctx, chunk(1), 0); err != nil {
			return err
		}
		if err := tx.PutEntry(ctx, entry(), 0); err != nil {
			return err
		}
		if err := tx.PutLink(ctx, link(), 0); err != nil {
			return err
		}
		return tx.PutEvidence(ctx, evidence())
	}); err != nil {
		t.Fatalf("seed store: %v", err)
	}

	var kinds []knowledgeStore.RecordKind
	stats, err := s.ScanCanonical(ctx, func(record knowledgeStore.CanonicalRecord) error {
		if err := record.Validate(); err != nil {
			return err
		}
		kinds = append(kinds, record.Kind)
		if record.Chunk != nil {
			record.Chunk.Aliases[0] = "mutated callback copy"
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ScanCanonical() error = %v", err)
	}
	wantKinds := []knowledgeStore.RecordKind{
		knowledgeStore.RecordKindChunk,
		knowledgeStore.RecordKindEntry,
		knowledgeStore.RecordKindLink,
		knowledgeStore.RecordKindEvidence,
	}
	if len(kinds) != len(wantKinds) {
		t.Fatalf("ScanCanonical() kinds = %v", kinds)
	}
	for index := range wantKinds {
		if kinds[index] != wantKinds[index] {
			t.Fatalf("ScanCanonical() kinds = %v, want %v", kinds, wantKinds)
		}
	}
	if stats.Total != 4 || stats.Chunks != 1 || stats.Entries != 1 || stats.Links != 1 || stats.Evidence != 1 {
		t.Fatalf("ScanCanonical() stats = %#v", stats)
	}
	if err := s.View(ctx, func(tx knowledgeStore.ReadTx) error {
		got, err := tx.Chunk(ctx, chunkID)
		if err != nil {
			return err
		}
		if got.Aliases[0] != "alias" {
			t.Fatalf("callback mutated stored chunk: %#v", got.Aliases)
		}
		return nil
	}); err != nil {
		t.Fatalf("View() error = %v", err)
	}
}

func TestMemoryIndexMaintenanceTracksLogicalGeneration(t *testing.T) {
	t.Parallel()
	s := New()
	if err := s.RebuildIndexes(context.Background()); err != nil {
		t.Fatalf("RebuildIndexes() error = %v", err)
	}
	status, err := s.IndexRebuildStatus(context.Background())
	if err != nil || status.Running || status.ActiveGeneration != 2 || status.CompletedAt.IsZero() {
		t.Fatalf("IndexRebuildStatus() = %#v, %v", status, err)
	}
}
