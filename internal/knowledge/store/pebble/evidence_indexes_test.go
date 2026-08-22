package pebble

import (
	"context"
	"errors"
	"testing"

	cockroachpebble "github.com/cockroachdb/pebble"

	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

func TestEvidenceSourceIndexEnforcesDeduplicationAndTracksDelete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	evidence := txEvidence()
	evidence.Source.ContentHash = "sha256:abc"
	if err := s.Update(ctx, func(tx knowledgeStore.WriteTx) error { return tx.PutEvidence(ctx, evidence) }); err != nil {
		t.Fatalf("PutEvidence() error = %v", err)
	}
	if err := s.View(ctx, func(tx knowledgeStore.ReadTx) error {
		got, err := tx.EvidenceBySource(ctx, " observation:test ", " SHA256:ABC ")
		if err != nil {
			return err
		}
		if got.ID != evidence.ID {
			t.Errorf("EvidenceBySource() = %#v", got)
		}
		return nil
	}); err != nil {
		t.Fatalf("lookup evidence: %v", err)
	}
	duplicate := evidence
	duplicate.ID = "01a01688-fc5d-7f7d-8bb8-de244977f8af"
	if err := s.Update(ctx, func(tx knowledgeStore.WriteTx) error { return tx.PutEvidence(ctx, duplicate) }); !errors.Is(err, knowledgeStore.ErrConflict) {
		t.Fatalf("PutEvidence(duplicate source/hash) error = %v, want ErrConflict", err)
	}
	if err := s.Update(ctx, func(tx knowledgeStore.WriteTx) error { return tx.DeleteEvidence(ctx, evidence.ID) }); err != nil {
		t.Fatalf("DeleteEvidence() error = %v", err)
	}
	if err := s.View(ctx, func(tx knowledgeStore.ReadTx) error {
		_, err := tx.EvidenceBySource(ctx, evidence.Source.ID, evidence.Source.ContentHash)
		return err
	}); !errors.Is(err, knowledgeStore.ErrNotFound) {
		t.Fatalf("EvidenceBySource(deleted) error = %v, want ErrNotFound", err)
	}
}

func TestEvidenceSourceIndexRebuildsFromCanonicalRecords(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	evidence := txEvidence()
	evidence.Source.ContentHash = "sha256:abc"
	if err := s.Update(ctx, func(tx knowledgeStore.WriteTx) error { return tx.PutEvidence(ctx, evidence) }); err != nil {
		t.Fatalf("PutEvidence() error = %v", err)
	}
	lower, upper := prefixBounds(indexGenerationPrefix(initialIndexGeneration))
	if err := s.db.DeleteRange(lower, upper, cockroachpebble.Sync); err != nil {
		t.Fatalf("remove indexes: %v", err)
	}
	if err := s.RebuildIndexes(ctx); err != nil {
		t.Fatalf("RebuildIndexes() error = %v", err)
	}
	if err := s.View(ctx, func(tx knowledgeStore.ReadTx) error {
		got, err := tx.EvidenceBySource(ctx, evidence.Source.ID, evidence.Source.ContentHash)
		if err == nil && got.ID != evidence.ID {
			t.Errorf("rebuilt lookup = %#v", got)
		}
		return err
	}); err != nil {
		t.Fatalf("lookup rebuilt evidence: %v", err)
	}
}
