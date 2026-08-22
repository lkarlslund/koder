package store

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/lkarlslund/koder/internal/knowledge"
)

// MigrationSnapshot is the backend-neutral durable Knowledge state. Derived indexes,
// backend metadata, operation status, and user-interface views are intentionally omitted.
type MigrationSnapshot struct {
	Records   []CanonicalRecord `json:"records"`
	Revisions []CanonicalRecord `json:"revisions"`
	Assets    []PackageAsset    `json:"assets"`
}

type MigrationStats struct {
	ScanStats
	Revisions uint64 `json:"revisions"`
	Assets    uint64 `json:"assets"`
}

// ExportMigrationSnapshot reads canonical records, their complete revision histories,
// and package assets through backend-neutral contracts. The caller must ensure the store
// has no concurrent writers for the duration of this offline operation.
func ExportMigrationSnapshot(ctx context.Context, source CanonicalScanStore) (MigrationSnapshot, MigrationStats, error) {
	if source == nil {
		return MigrationSnapshot{}, MigrationStats{}, fmt.Errorf("export knowledge migration: source store is required")
	}
	var snapshot MigrationSnapshot
	stats, err := source.ScanCanonical(ctx, func(record CanonicalRecord) error {
		snapshot.Records = append(snapshot.Records, cloneCanonicalRecord(record))
		return nil
	})
	if err != nil {
		return MigrationSnapshot{}, MigrationStats{}, fmt.Errorf("export knowledge migration canonical records: %w", err)
	}
	for _, record := range snapshot.Records {
		if record.Kind == RecordKindEvidence {
			continue
		}
		request := RevisionListRequest{Object: record.ObjectRef(), Limit: 200}
		for {
			page, err := source.ListRevisions(ctx, request)
			if err != nil {
				return MigrationSnapshot{}, MigrationStats{}, fmt.Errorf("export knowledge migration revisions for %s %s: %w", record.Kind, record.ID(), err)
			}
			for _, revision := range page.Revisions {
				snapshot.Revisions = append(snapshot.Revisions, cloneCanonicalRecord(revision))
			}
			if page.NextCursor == "" {
				break
			}
			request.Cursor = page.NextCursor
		}
	}
	if err := source.View(ctx, func(tx ReadTx) error {
		for _, record := range snapshot.Records {
			if record.Kind != RecordKindChunk {
				continue
			}
			assets, err := tx.ListAssets(ctx, record.Chunk.ID)
			if err != nil {
				return err
			}
			for _, asset := range assets {
				snapshot.Assets = append(snapshot.Assets, ClonePackageAsset(asset))
			}
		}
		return nil
	}); err != nil {
		return MigrationSnapshot{}, MigrationStats{}, fmt.Errorf("export knowledge migration assets: %w", err)
	}
	normalizeMigrationSnapshot(&snapshot)
	if err := ValidateMigrationSnapshot(snapshot); err != nil {
		return MigrationSnapshot{}, MigrationStats{}, fmt.Errorf("validate exported knowledge migration: %w", err)
	}
	return snapshot, MigrationStats{ScanStats: stats, Revisions: uint64(len(snapshot.Revisions)), Assets: uint64(len(snapshot.Assets))}, nil
}

// ImportMigrationSnapshot atomically imports an exact canonical snapshot into an empty
// target through ordinary Store transactions. It never copies backend-private keys.
func ImportMigrationSnapshot(ctx context.Context, target Store, snapshot MigrationSnapshot) (MigrationStats, error) {
	if target == nil {
		return MigrationStats{}, fmt.Errorf("import knowledge migration: target store is required")
	}
	normalizeMigrationSnapshot(&snapshot)
	if err := ValidateMigrationSnapshot(snapshot); err != nil {
		return MigrationStats{}, fmt.Errorf("import knowledge migration validation: %w", err)
	}
	stats := migrationSnapshotStats(snapshot)
	if err := target.Update(ctx, func(tx WriteTx) error {
		empty, err := tx.Empty(ctx)
		if err != nil {
			return err
		}
		if !empty {
			return fmt.Errorf("%w: knowledge migration target is not empty", ErrConflict)
		}
		applyRevisions := func(kind RecordKind) error {
			for _, record := range snapshot.Revisions {
				if record.Kind != kind {
					continue
				}
				revision := record.RevisionMetadata().Number
				switch kind {
				case RecordKindChunk:
					err = tx.PutChunk(ctx, *record.Chunk, revision-1)
				case RecordKindEntry:
					err = tx.PutEntry(ctx, *record.Entry, revision-1)
				case RecordKindLink:
					err = tx.PutLink(ctx, *record.Link, revision-1)
				}
				if err != nil {
					return fmt.Errorf("import %s %s revision %d: %w", kind, record.ID(), revision, err)
				}
			}
			return nil
		}
		if err := applyRevisions(RecordKindChunk); err != nil {
			return err
		}
		for _, record := range snapshot.Records {
			if record.Kind == RecordKindChunk && !record.Chunk.LastUsedAt.IsZero() {
				if err := tx.TouchChunk(ctx, record.Chunk.ID, record.Chunk.LastUsedAt); err != nil {
					return fmt.Errorf("import chunk %s usage projection: %w", record.ID(), err)
				}
			}
		}
		for _, record := range snapshot.Records {
			if record.Kind == RecordKindEvidence {
				if err := tx.PutEvidence(ctx, *record.Evidence); err != nil {
					return fmt.Errorf("import evidence %s: %w", record.ID(), err)
				}
			}
		}
		if err := applyRevisions(RecordKindEntry); err != nil {
			return err
		}
		if err := applyRevisions(RecordKindLink); err != nil {
			return err
		}
		for _, asset := range snapshot.Assets {
			if err := tx.PutAsset(ctx, asset); err != nil {
				return fmt.Errorf("import asset %s/%s: %w", asset.ChunkID, asset.Path, err)
			}
		}
		return nil
	}); err != nil {
		return MigrationStats{}, fmt.Errorf("import knowledge migration: %w", err)
	}
	return stats, nil
}

func ValidateMigrationSnapshot(snapshot MigrationSnapshot) error {
	records := make(map[string]CanonicalRecord, len(snapshot.Records))
	chunks := make(map[knowledge.ChunkID]knowledge.Chunk)
	entries := make(map[knowledge.EntryID]knowledge.Entry)
	links := make(map[knowledge.LinkID]knowledge.Link)
	evidence := make(map[knowledge.EvidenceID]knowledge.Evidence)
	evidenceSources := make(map[string]knowledge.EvidenceID)
	for _, record := range snapshot.Records {
		if err := record.Validate(); err != nil {
			return fmt.Errorf("canonical record %s/%s: %w", record.Kind, record.ID(), err)
		}
		key := migrationRecordKey(record)
		if _, exists := records[key]; exists {
			return fmt.Errorf("duplicate canonical record %s", key)
		}
		records[key] = record
		switch record.Kind {
		case RecordKindChunk:
			chunks[record.Chunk.ID] = *record.Chunk
		case RecordKindEntry:
			entries[record.Entry.ID] = *record.Entry
		case RecordKindLink:
			links[record.Link.ID] = *record.Link
		case RecordKindEvidence:
			evidence[record.Evidence.ID] = *record.Evidence
			sourceKey := record.Evidence.Source.ID + "\x00" + record.Evidence.Source.ContentHash
			if existing, exists := evidenceSources[sourceKey]; exists {
				return fmt.Errorf("evidence %s duplicates source identity of %s", record.Evidence.ID, existing)
			}
			evidenceSources[sourceKey] = record.Evidence.ID
		}
	}
	if err := validateMigrationReferences(chunks, entries, links, evidence); err != nil {
		return err
	}
	derivedCounts := DeriveChunkCounts(mapValues(chunks), mapValues(entries), mapValues(links), mapValues(evidence))
	for id, chunk := range chunks {
		if chunk.Counts != derivedCounts[id] {
			return fmt.Errorf("chunk %s derived counts do not match canonical records", id)
		}
	}

	histories := make(map[string]map[uint64]CanonicalRecord)
	for _, record := range snapshot.Revisions {
		if record.Kind == RecordKindEvidence {
			return fmt.Errorf("evidence %s must not have revision history", record.ID())
		}
		if err := record.Validate(); err != nil {
			return fmt.Errorf("revision record %s/%s: %w", record.Kind, record.ID(), err)
		}
		key := migrationRecordKey(record)
		if _, exists := records[key]; !exists {
			return fmt.Errorf("revision history references missing canonical record %s", key)
		}
		revision := record.RevisionMetadata().Number
		if histories[key] == nil {
			histories[key] = make(map[uint64]CanonicalRecord)
		}
		if _, exists := histories[key][revision]; exists {
			return fmt.Errorf("duplicate %s revision %d", key, revision)
		}
		histories[key][revision] = record
	}
	for key, current := range records {
		if current.Kind == RecordKindEvidence {
			continue
		}
		currentRevision := current.RevisionMetadata().Number
		history := histories[key]
		if uint64(len(history)) != currentRevision {
			return fmt.Errorf("%s revision history has %d records, want %d", key, len(history), currentRevision)
		}
		for revision := uint64(1); revision <= currentRevision; revision++ {
			if _, exists := history[revision]; !exists {
				return fmt.Errorf("%s revision history is missing revision %d", key, revision)
			}
		}
		if !migrationProjectionEqual(current, history[currentRevision]) {
			return fmt.Errorf("%s current record does not match its latest revision", key)
		}
	}
	assets := make(map[string]struct{}, len(snapshot.Assets))
	for _, asset := range snapshot.Assets {
		if err := asset.Validate(); err != nil {
			return err
		}
		if _, exists := chunks[asset.ChunkID]; !exists {
			return fmt.Errorf("asset %s/%s references missing chunk", asset.ChunkID, asset.Path)
		}
		key := string(asset.ChunkID) + "\x00" + asset.Path
		if _, exists := assets[key]; exists {
			return fmt.Errorf("duplicate asset %s/%s", asset.ChunkID, asset.Path)
		}
		assets[key] = struct{}{}
	}
	return nil
}

func validateMigrationReferences(chunks map[knowledge.ChunkID]knowledge.Chunk, entries map[knowledge.EntryID]knowledge.Entry, links map[knowledge.LinkID]knowledge.Link, evidence map[knowledge.EvidenceID]knowledge.Evidence) error {
	for _, chunk := range chunks {
		for _, dependency := range chunk.DependencyIDs {
			if _, exists := chunks[dependency]; !exists {
				return fmt.Errorf("chunk %s references missing dependency %s", chunk.ID, dependency)
			}
		}
	}
	for _, entry := range entries {
		if _, exists := chunks[entry.ChunkID]; !exists {
			return fmt.Errorf("entry %s references missing chunk %s", entry.ID, entry.ChunkID)
		}
		for _, id := range append(slices.Clone(entry.EvidenceIDs), entry.Verification.EvidenceIDs...) {
			if _, exists := evidence[id]; !exists {
				return fmt.Errorf("entry %s references missing evidence %s", entry.ID, id)
			}
		}
	}
	for _, link := range links {
		for _, endpoint := range []knowledge.ObjectRef{link.Source, link.Target} {
			switch endpoint.Kind {
			case knowledge.ObjectKindChunk:
				if _, exists := chunks[knowledge.ChunkID(endpoint.ID)]; !exists {
					return fmt.Errorf("link %s references missing chunk %s", link.ID, endpoint.ID)
				}
			case knowledge.ObjectKindEntry:
				if _, exists := entries[knowledge.EntryID(endpoint.ID)]; !exists {
					return fmt.Errorf("link %s references missing entry %s", link.ID, endpoint.ID)
				}
			}
		}
		for _, id := range link.EvidenceIDs {
			if _, exists := evidence[id]; !exists {
				return fmt.Errorf("link %s references missing evidence %s", link.ID, id)
			}
		}
	}
	return nil
}

func normalizeMigrationSnapshot(snapshot *MigrationSnapshot) {
	slices.SortFunc(snapshot.Records, compareMigrationRecords)
	slices.SortFunc(snapshot.Revisions, func(left, right CanonicalRecord) int {
		if result := compareMigrationRecords(left, right); result != 0 {
			return result
		}
		leftRevision, rightRevision := left.RevisionMetadata().Number, right.RevisionMetadata().Number
		switch {
		case leftRevision < rightRevision:
			return -1
		case leftRevision > rightRevision:
			return 1
		default:
			return 0
		}
	})
	slices.SortFunc(snapshot.Assets, func(left, right PackageAsset) int {
		if result := strings.Compare(string(left.ChunkID), string(right.ChunkID)); result != 0 {
			return result
		}
		return strings.Compare(left.Path, right.Path)
	})
}

func compareMigrationRecords(left, right CanonicalRecord) int {
	if result := strings.Compare(string(left.Kind), string(right.Kind)); result != 0 {
		return result
	}
	return strings.Compare(left.ID(), right.ID())
}

func migrationRecordKey(record CanonicalRecord) string {
	return string(record.Kind) + "/" + record.ID()
}

func migrationProjectionEqual(current, revision CanonicalRecord) bool {
	if current.Kind == RecordKindChunk && current.Chunk != nil && revision.Chunk != nil {
		currentValue, revisionValue := *current.Chunk, *revision.Chunk
		currentValue.LastUsedAt = revisionValue.LastUsedAt
		currentValue.Counts = revisionValue.Counts
		current.Chunk, revision.Chunk = &currentValue, &revisionValue
	}
	return reflect.DeepEqual(current, revision)
}

func migrationSnapshotStats(snapshot MigrationSnapshot) MigrationStats {
	var stats MigrationStats
	for _, record := range snapshot.Records {
		stats.Add(record.Kind)
	}
	stats.Revisions = uint64(len(snapshot.Revisions))
	stats.Assets = uint64(len(snapshot.Assets))
	return stats
}

func mapValues[K comparable, V any](values map[K]V) []V {
	result := make([]V, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	return result
}
