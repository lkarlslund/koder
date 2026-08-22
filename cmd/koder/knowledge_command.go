package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/knowledge"
	"github.com/lkarlslund/koder/internal/knowledge/migrationarchive"
	knowledgeService "github.com/lkarlslund/koder/internal/knowledge/service"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
	knowledgePebble "github.com/lkarlslund/koder/internal/knowledge/store/pebble"
	"github.com/lkarlslund/koder/internal/version"
)

func newKnowledgeCommand(root *rootOptions) *cobra.Command {
	command := &cobra.Command{Use: "knowledge", Short: "Maintain the independent Knowledge store"}
	command.AddCommand(newKnowledgeBackupCommand(root))
	command.AddCommand(newKnowledgeRestoreCommand(root))
	command.AddCommand(newKnowledgeMigrateCommand(root))
	return command
}

func newKnowledgeMigrateCommand(root *rootOptions) *cobra.Command {
	command := &cobra.Command{Use: "migrate", Short: "Move complete Knowledge state between storage backends"}
	command.AddCommand(newKnowledgeMigrateExportCommand(root), newKnowledgeMigrateImportCommand(root))
	return command
}

func newKnowledgeMigrateExportCommand(root *rootOptions) *cobra.Command {
	var includePersonal bool
	var jsonOutput bool
	command := &cobra.Command{
		Use:   "export <archive>",
		Short: "Export canonical Knowledge, revisions, and assets",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadWithOptions(root.loadOptions())
			if err != nil {
				return err
			}
			result, destination, err := createKnowledgeMigrationExport(cmd.Context(), cfg.StateDir(), args[0], includePersonal)
			if err != nil {
				return err
			}
			return writeKnowledgeMigrationExportResult(cmd.OutOrStdout(), destination, result, jsonOutput)
		},
	}
	command.Flags().BoolVar(&includePersonal, "include-personal", false, "Confirm inclusion of private personal Knowledge")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Write machine-readable export metadata")
	return command
}

func newKnowledgeMigrateImportCommand(root *rootOptions) *cobra.Command {
	var confirm bool
	var includePersonal bool
	var jsonOutput bool
	command := &cobra.Command{
		Use:   "import <archive>",
		Short: "Import a migration archive into an empty Knowledge store",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadWithOptions(root.loadOptions())
			if err != nil {
				return err
			}
			result, source, err := importKnowledgeMigration(cmd.Context(), cfg.StateDir(), args[0], confirm, includePersonal)
			if err != nil {
				return err
			}
			return writeKnowledgeMigrationImportResult(cmd.OutOrStdout(), source, result, jsonOutput)
		},
	}
	command.Flags().BoolVar(&confirm, "confirm", false, "Confirm import into the selected empty Knowledge store")
	command.Flags().BoolVar(&includePersonal, "include-personal", false, "Confirm import of private personal Knowledge")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Write machine-readable import metadata")
	return command
}

func createKnowledgeMigrationExport(ctx context.Context, stateDir, destination string, includePersonal bool) (migrationarchive.ExportResult, string, error) {
	destination = strings.TrimSpace(destination)
	if destination == "" {
		return migrationarchive.ExportResult{}, "", fmt.Errorf("knowledge migration export destination is required")
	}
	absDestination, err := filepath.Abs(destination)
	if err != nil {
		return migrationarchive.ExportResult{}, "", fmt.Errorf("resolve knowledge migration destination: %w", err)
	}
	if _, err := os.Lstat(absDestination); err == nil {
		return migrationarchive.ExportResult{}, "", fmt.Errorf("%w: migration export refuses to overwrite %s", knowledgeStore.ErrConflict, absDestination)
	} else if !errors.Is(err, os.ErrNotExist) {
		return migrationarchive.ExportResult{}, "", fmt.Errorf("inspect knowledge migration destination: %w", err)
	}
	store, err := knowledgePebble.OpenExisting(stateDir)
	if err != nil {
		return migrationarchive.ExportResult{}, "", err
	}
	defer func() { _ = store.Close() }()
	snapshot, _, err := knowledgeStore.ExportMigrationSnapshot(ctx, store)
	if err != nil {
		return migrationarchive.ExportResult{}, "", err
	}
	if migrationContainsPersonal(snapshot) && !includePersonal {
		return migrationarchive.ExportResult{}, "", fmt.Errorf("%w; retry with --include-personal after confirming the archive destination is private", knowledgeService.ErrPersonalExportConsent)
	}
	parent := filepath.Dir(absDestination)
	temporary, err := os.CreateTemp(parent, ".knowledge-migration-export-*")
	if err != nil {
		return migrationarchive.ExportResult{}, "", fmt.Errorf("create knowledge migration export: %w", err)
	}
	temporaryPath := temporary.Name()
	succeeded := false
	defer func() {
		_ = temporary.Close()
		if !succeeded {
			_ = os.Remove(temporaryPath)
		}
	}()
	result, err := migrationarchive.Export(ctx, temporary, migrationarchive.ExportRequest{
		CreatedAt: time.Now().UTC().Round(0), KoderVersion: version.Version, Snapshot: snapshot,
	})
	if err != nil {
		return migrationarchive.ExportResult{}, "", err
	}
	if err := temporary.Sync(); err != nil {
		return migrationarchive.ExportResult{}, "", fmt.Errorf("sync knowledge migration export: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return migrationarchive.ExportResult{}, "", fmt.Errorf("close knowledge migration export: %w", err)
	}
	// A same-directory hard link publishes the fully synced file without the
	// overwrite behavior of os.Rename on Unix. A racing destination wins safely.
	if err := os.Link(temporaryPath, absDestination); err != nil {
		return migrationarchive.ExportResult{}, "", fmt.Errorf("publish knowledge migration export: %w", err)
	}
	succeeded = true
	_ = os.Remove(temporaryPath)
	return result, absDestination, nil
}

func importKnowledgeMigration(ctx context.Context, stateDir, source string, confirmed, includePersonal bool) (knowledgeStore.MigrationStats, string, error) {
	if !confirmed {
		return knowledgeStore.MigrationStats{}, "", fmt.Errorf("knowledge migration import requires --confirm and an empty target store")
	}
	absSource, err := filepath.Abs(strings.TrimSpace(source))
	if err != nil || strings.TrimSpace(source) == "" {
		return knowledgeStore.MigrationStats{}, "", fmt.Errorf("knowledge migration import archive is required")
	}
	file, err := os.Open(absSource)
	if err != nil {
		return knowledgeStore.MigrationStats{}, "", fmt.Errorf("open knowledge migration archive: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return knowledgeStore.MigrationStats{}, "", fmt.Errorf("inspect knowledge migration archive: %w", err)
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return knowledgeStore.MigrationStats{}, "", fmt.Errorf("knowledge migration archive must be a regular file")
	}
	archive, err := migrationarchive.Parse(ctx, file, info.Size())
	closeErr := file.Close()
	if err != nil {
		return knowledgeStore.MigrationStats{}, "", err
	}
	if closeErr != nil {
		return knowledgeStore.MigrationStats{}, "", fmt.Errorf("close knowledge migration archive: %w", closeErr)
	}
	if migrationContainsPersonal(archive.Snapshot) && !includePersonal {
		return knowledgeStore.MigrationStats{}, "", fmt.Errorf("knowledge migration contains private personal Knowledge; retry with --include-personal after confirming the target")
	}
	store, err := knowledgePebble.Open(stateDir)
	if err != nil {
		return knowledgeStore.MigrationStats{}, "", err
	}
	defer func() { _ = store.Close() }()
	stats, err := knowledgeStore.ImportMigrationSnapshot(ctx, store, archive.Snapshot)
	if err != nil {
		return knowledgeStore.MigrationStats{}, "", err
	}
	return stats, absSource, nil
}

func migrationContainsPersonal(snapshot knowledgeStore.MigrationSnapshot) bool {
	for _, record := range snapshot.Records {
		if record.Kind != knowledgeStore.RecordKindChunk {
			continue
		}
		chunk := record.Chunk
		if chunk.ID == knowledgeService.PersonalMeChunkID || chunk.Kind == knowledge.ChunkKindPersonal || chunk.Scope.Kind == knowledge.ScopeKindPersonal {
			return true
		}
	}
	return false
}

func writeKnowledgeMigrationExportResult(output io.Writer, destination string, result migrationarchive.ExportResult, jsonOutput bool) error {
	if jsonOutput {
		return json.NewEncoder(output).Encode(struct {
			Path   string `json:"path"`
			SHA256 string `json:"sha256"`
			Size   int64  `json:"size"`
			knowledgeStore.MigrationStats
		}{Path: destination, SHA256: result.SHA256, Size: result.Size, MigrationStats: result.Stats})
	}
	_, err := fmt.Fprintf(output, "Knowledge migration archive created at %s\nSHA-256: %s  Size: %d bytes\nRecords: %d  Revisions: %d  Assets: %d\n", destination, result.SHA256, result.Size, result.Stats.Total, result.Stats.Revisions, result.Stats.Assets)
	return err
}

func writeKnowledgeMigrationImportResult(output io.Writer, source string, stats knowledgeStore.MigrationStats, jsonOutput bool) error {
	if jsonOutput {
		return json.NewEncoder(output).Encode(struct {
			Path string `json:"path"`
			knowledgeStore.MigrationStats
		}{Path: source, MigrationStats: stats})
	}
	_, err := fmt.Fprintf(output, "Knowledge migration imported and validated from %s\nRecords: %d  Revisions: %d  Assets: %d\n", source, stats.Total, stats.Revisions, stats.Assets)
	return err
}

func newKnowledgeBackupCommand(root *rootOptions) *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{
		Use:   "backup <destination-directory>",
		Short: "Create and validate an independent Knowledge checkpoint",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadWithOptions(root.loadOptions())
			if err != nil {
				return err
			}
			info, destination, err := createKnowledgeBackup(cmd.Context(), cfg.StateDir(), args[0])
			if err != nil {
				return err
			}
			return writeKnowledgeBackupResult(cmd.OutOrStdout(), destination, info, jsonOutput)
		},
	}
	command.Flags().BoolVar(&jsonOutput, "json", false, "Write machine-readable backup metadata")
	return command
}

func createKnowledgeBackup(ctx context.Context, stateDir, destination string) (knowledgePebble.CheckpointInfo, string, error) {
	destination = strings.TrimSpace(destination)
	if destination == "" {
		return knowledgePebble.CheckpointInfo{}, "", fmt.Errorf("knowledge backup destination is required")
	}
	absDestination, err := filepath.Abs(destination)
	if err != nil {
		return knowledgePebble.CheckpointInfo{}, "", fmt.Errorf("resolve knowledge backup destination: %w", err)
	}
	store, err := knowledgePebble.OpenExisting(stateDir)
	if err != nil {
		return knowledgePebble.CheckpointInfo{}, "", err
	}
	defer func() { _ = store.Close() }()
	if err := store.Checkpoint(ctx, absDestination); err != nil {
		return knowledgePebble.CheckpointInfo{}, "", err
	}
	info, err := knowledgePebble.ValidateCheckpoint(ctx, absDestination)
	if err != nil {
		return knowledgePebble.CheckpointInfo{}, "", err
	}
	return info, absDestination, nil
}

func writeKnowledgeBackupResult(output io.Writer, destination string, info knowledgePebble.CheckpointInfo, jsonOutput bool) error {
	if jsonOutput {
		return json.NewEncoder(output).Encode(struct {
			Path string `json:"path"`
			knowledgePebble.CheckpointInfo
		}{Path: destination, CheckpointInfo: info})
	}
	_, err := fmt.Fprintf(output, "Knowledge backup created and validated at %s\nBackend: %s  Schema: %d  Index generation: %d\n", destination, info.Backend, info.SchemaVersion, info.IndexGeneration)
	return err
}

func newKnowledgeRestoreCommand(root *rootOptions) *cobra.Command {
	var confirm bool
	var jsonOutput bool
	command := &cobra.Command{
		Use:   "restore <backup-directory>",
		Short: "Validate and restore an offline Knowledge checkpoint",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadWithOptions(root.loadOptions())
			if err != nil {
				return err
			}
			result, err := restoreKnowledgeBackup(cmd.Context(), cfg.StateDir(), args[0], confirm)
			if err != nil {
				return err
			}
			return writeKnowledgeRestoreResult(cmd.OutOrStdout(), result, jsonOutput)
		},
	}
	command.Flags().BoolVar(&confirm, "confirm", false, "Confirm replacement of the offline Knowledge database")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Write machine-readable restore metadata")
	return command
}

func restoreKnowledgeBackup(ctx context.Context, stateDir, source string, confirmed bool) (knowledgePebble.RestoreInfo, error) {
	if !confirmed {
		return knowledgePebble.RestoreInfo{}, fmt.Errorf("knowledge restore requires --confirm; stop Koder and verify the backup path before retrying")
	}
	return knowledgePebble.RestoreCheckpoint(ctx, stateDir, source)
}

func writeKnowledgeRestoreResult(output io.Writer, result knowledgePebble.RestoreInfo, jsonOutput bool) error {
	if jsonOutput {
		return json.NewEncoder(output).Encode(result)
	}
	_, err := fmt.Fprintf(
		output,
		"Knowledge restored and validated from %s\nLive database: %s\nPrevious database retained for rollback: %s\nBackend: %s  Schema: %d  Index generation: %d\n",
		result.SourcePath,
		result.LivePath,
		result.RollbackPath,
		result.Backend,
		result.SchemaVersion,
		result.IndexGeneration,
	)
	return err
}
