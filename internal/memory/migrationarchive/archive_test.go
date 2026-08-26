package migrationarchive

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

func TestArchiveRoundTripIsDeterministicAndValidated(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2026, 8, 22, 20, 0, 0, 0, time.UTC)
	snapshot := archiveTestSnapshot(createdAt)
	request := ExportRequest{CreatedAt: createdAt, KoderVersion: "r1880-local", Snapshot: snapshot}
	var first bytes.Buffer
	result, err := Export(context.Background(), &first, request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Size != int64(first.Len()) || result.SHA256 == "" || result.Stats.Chunks != 1 || result.Stats.Revisions != 1 {
		t.Fatalf("Export() = %#v", result)
	}
	var second bytes.Buffer
	if _, err := Export(context.Background(), &second, request); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("unchanged migration export is not deterministic")
	}
	parsed, err := Parse(context.Background(), bytes.NewReader(first.Bytes()), int64(first.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Format != Format || parsed.SchemaVersion != SchemaVersion || parsed.KoderVersion != "r1880-local" || parsed.Stats != result.Stats {
		t.Fatalf("Parse() = %#v", parsed)
	}
	if err := memoryStoreAPI.ValidateMigrationSnapshot(parsed.Snapshot); err != nil {
		t.Fatalf("parsed snapshot: %v", err)
	}
}

func TestArchiveRejectsCorruptionUnknownFieldsAndMismatchedInventory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	if _, err := Parse(ctx, bytes.NewReader([]byte("not gzip")), 8); err == nil {
		t.Fatal("Parse(corrupt) unexpectedly succeeded")
	}
	if _, err := Parse(ctx, bytes.NewReader(nil), HardMaxCompressedBytes+1); err == nil {
		t.Fatal("Parse(oversized) unexpectedly succeeded")
	}

	createdAt := time.Date(2026, 8, 22, 20, 0, 0, 0, time.UTC)
	archive := Archive{
		Format: Format, SchemaVersion: SchemaVersion, CreatedAt: createdAt, KoderVersion: "r1880",
		Snapshot: archiveTestSnapshot(createdAt),
		Stats:    memoryStoreAPI.MigrationStats{ScanStats: memoryStoreAPI.ScanStats{Chunks: 99, Total: 99}},
	}
	data := encodeRawArchive(t, archive)
	if _, err := Parse(ctx, bytes.NewReader(data), int64(len(data))); err == nil {
		t.Fatal("Parse(mismatched inventory) unexpectedly succeeded")
	}

	unknown := encodeRawJSON(t, []byte(`{"format":"koder.memory.migration","schema_version":1,"created_at":"2026-08-22T20:00:00Z","koder_version":"r1880","stats":{},"snapshot":{"records":[],"revisions":[],"assets":[]},"surprise":true}`))
	if _, err := Parse(ctx, bytes.NewReader(unknown), int64(len(unknown))); err == nil {
		t.Fatal("Parse(unknown field) unexpectedly succeeded")
	}
}

func TestArchiveHonorsCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Export(ctx, new(bytes.Buffer), ExportRequest{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Export(canceled) error = %v", err)
	}
	if _, err := Parse(ctx, bytes.NewReader(nil), 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("Parse(canceled) error = %v", err)
	}
}

func archiveTestSnapshot(at time.Time) memoryStoreAPI.MigrationSnapshot {
	chunk := memory.Chunk{
		ID: "019f132e-4f3a-739a-9ab2-5198dcd19e67", Title: "Portable",
		Kind: memory.ChunkKindReference, Scope: memory.Scope{Kind: memory.ScopeKindGlobal},
		Visibility: memory.VisibilityInstallation, State: memory.ChunkStateActive, SchemaVersion: 1,
		Revision: memory.Revision{
			Number: 1, ID: "01a01688-fc5d-7f7d-8bb8-de2449770001",
			Actor: memory.Actor{Kind: memory.ActorKindSystem, ID: "test:archive"}, CreatedAt: at,
		},
		CreatedAt: at, UpdatedAt: at,
	}
	record := memoryStoreAPI.CanonicalRecord{Kind: memoryStoreAPI.RecordKindChunk, Chunk: &chunk}
	return memoryStoreAPI.MigrationSnapshot{Records: []memoryStoreAPI.CanonicalRecord{record}, Revisions: []memoryStoreAPI.CanonicalRecord{record}, Assets: []memoryStoreAPI.PackageAsset{}}
}

func encodeRawArchive(t *testing.T, archive Archive) []byte {
	t.Helper()
	data, err := json.Marshal(archive)
	if err != nil {
		t.Fatal(err)
	}
	return encodeRawJSON(t, data)
}

func encodeRawJSON(t *testing.T, data []byte) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := gzip.NewWriter(&output)
	if _, err := writer.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
