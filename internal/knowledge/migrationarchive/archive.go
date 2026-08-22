// Package migrationarchive serializes backend-neutral Knowledge snapshots without
// exposing any persistence backend's private files, keys, or index layout.
package migrationarchive

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

const (
	Format                   = "koder.knowledge.migration"
	SchemaVersion            = 1
	HardMaxCompressedBytes   = int64(512 << 20)
	HardMaxUncompressedBytes = int64(2 << 30)
)

type Archive struct {
	Format        string                           `json:"format"`
	SchemaVersion int                              `json:"schema_version"`
	CreatedAt     time.Time                        `json:"created_at"`
	KoderVersion  string                           `json:"koder_version"`
	Stats         knowledgeStore.MigrationStats    `json:"stats"`
	Snapshot      knowledgeStore.MigrationSnapshot `json:"snapshot"`
}

type ExportRequest struct {
	CreatedAt    time.Time
	KoderVersion string
	Snapshot     knowledgeStore.MigrationSnapshot
}

type ExportResult struct {
	SHA256 string                        `json:"sha256"`
	Size   int64                         `json:"size"`
	Stats  knowledgeStore.MigrationStats `json:"stats"`
}

func Export(ctx context.Context, writer io.Writer, request ExportRequest) (ExportResult, error) {
	if err := ctx.Err(); err != nil {
		return ExportResult{}, err
	}
	if writer == nil {
		return ExportResult{}, errors.New("knowledge migration archive writer is required")
	}
	createdAt := request.CreatedAt.UTC().Round(0)
	_, offset := createdAt.Zone()
	if createdAt.IsZero() || offset != 0 {
		return ExportResult{}, errors.New("knowledge migration archive created_at must be non-zero UTC")
	}
	if strings.TrimSpace(request.KoderVersion) == "" {
		return ExportResult{}, errors.New("knowledge migration archive Koder version is required")
	}
	if err := knowledgeStore.ValidateMigrationSnapshot(request.Snapshot); err != nil {
		return ExportResult{}, fmt.Errorf("validate knowledge migration snapshot: %w", err)
	}
	stats := snapshotStats(request.Snapshot)
	archive := Archive{
		Format: Format, SchemaVersion: SchemaVersion, CreatedAt: createdAt,
		KoderVersion: strings.TrimSpace(request.KoderVersion), Stats: stats, Snapshot: request.Snapshot,
	}
	digestWriter := &digestLimitWriter{ctx: ctx, writer: writer, hash: sha256.New(), limit: HardMaxCompressedBytes}
	compressed := gzip.NewWriter(digestWriter)
	compressed.ModTime = createdAt
	compressed.OS = 255
	uncompressed := &limitWriter{ctx: ctx, writer: compressed, limit: HardMaxUncompressedBytes}
	encoder := json.NewEncoder(uncompressed)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(archive); err != nil {
		_ = compressed.Close()
		return ExportResult{}, fmt.Errorf("encode knowledge migration archive: %w", err)
	}
	if err := compressed.Close(); err != nil {
		return ExportResult{}, fmt.Errorf("close knowledge migration archive: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return ExportResult{}, err
	}
	return ExportResult{SHA256: hex.EncodeToString(digestWriter.hash.Sum(nil)), Size: digestWriter.size, Stats: stats}, nil
}

func Parse(ctx context.Context, reader io.Reader, compressedSize int64) (Archive, error) {
	if err := ctx.Err(); err != nil {
		return Archive{}, err
	}
	if reader == nil || compressedSize < 0 {
		return Archive{}, errors.New("knowledge migration archive reader and size are required")
	}
	if compressedSize > HardMaxCompressedBytes {
		return Archive{}, fmt.Errorf("knowledge migration archive exceeds %d compressed bytes", HardMaxCompressedBytes)
	}
	limitedCompressed := &io.LimitedReader{R: &contextReader{ctx: ctx, reader: reader}, N: compressedSize + 1}
	compressed, err := gzip.NewReader(limitedCompressed)
	if err != nil {
		return Archive{}, fmt.Errorf("open knowledge migration archive: %w", err)
	}
	defer func() { _ = compressed.Close() }()
	limited := &io.LimitedReader{R: compressed, N: HardMaxUncompressedBytes + 1}
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()
	var archive Archive
	if err := decoder.Decode(&archive); err != nil {
		return Archive{}, fmt.Errorf("decode knowledge migration archive: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return Archive{}, errors.New("knowledge migration archive contains trailing JSON")
		}
		return Archive{}, fmt.Errorf("decode knowledge migration archive trailer: %w", err)
	}
	if limited.N <= 0 {
		return Archive{}, fmt.Errorf("knowledge migration archive exceeds %d uncompressed bytes", HardMaxUncompressedBytes)
	}
	if err := compressed.Close(); err != nil {
		return Archive{}, fmt.Errorf("close knowledge migration archive reader: %w", err)
	}
	if limitedCompressed.N != 1 {
		return Archive{}, errors.New("knowledge migration archive compressed size does not match input")
	}
	if err := validateArchive(archive); err != nil {
		return Archive{}, err
	}
	if err := ctx.Err(); err != nil {
		return Archive{}, err
	}
	return archive, nil
}

func validateArchive(archive Archive) error {
	if archive.Format != Format || archive.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported knowledge migration format %q schema %d", archive.Format, archive.SchemaVersion)
	}
	_, offset := archive.CreatedAt.Zone()
	if archive.CreatedAt.IsZero() || offset != 0 || strings.TrimSpace(archive.KoderVersion) == "" {
		return errors.New("invalid knowledge migration archive metadata")
	}
	if err := knowledgeStore.ValidateMigrationSnapshot(archive.Snapshot); err != nil {
		return fmt.Errorf("validate knowledge migration archive snapshot: %w", err)
	}
	if want := snapshotStats(archive.Snapshot); archive.Stats != want {
		return fmt.Errorf("knowledge migration archive stats do not match snapshot")
	}
	return nil
}

func snapshotStats(snapshot knowledgeStore.MigrationSnapshot) knowledgeStore.MigrationStats {
	var stats knowledgeStore.MigrationStats
	for _, record := range snapshot.Records {
		stats.Add(record.Kind)
	}
	stats.Revisions = uint64(len(snapshot.Revisions))
	stats.Assets = uint64(len(snapshot.Assets))
	return stats
}

type limitWriter struct {
	ctx    context.Context
	writer io.Writer
	limit  int64
	size   int64
}

func (w *limitWriter) Write(data []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	if int64(len(data)) > w.limit-w.size {
		return 0, fmt.Errorf("knowledge migration archive exceeds %d uncompressed bytes", w.limit)
	}
	n, err := w.writer.Write(data)
	w.size += int64(n)
	return n, err
}

type digestLimitWriter struct {
	ctx    context.Context
	writer io.Writer
	hash   interface {
		Write([]byte) (int, error)
		Sum([]byte) []byte
	}
	limit int64
	size  int64
}

func (w *digestLimitWriter) Write(data []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	if int64(len(data)) > w.limit-w.size {
		return 0, fmt.Errorf("knowledge migration archive exceeds %d compressed bytes", w.limit)
	}
	n, err := w.writer.Write(data)
	if n > 0 {
		_, _ = w.hash.Write(data[:n])
		w.size += int64(n)
	}
	return n, err
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(data)
}
