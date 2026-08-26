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
	"github.com/lkarlslund/koder/internal/memory"
	"github.com/lkarlslund/koder/internal/memory/migrationarchive"
	memoryService "github.com/lkarlslund/koder/internal/memory/service"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
	memoryPebble "github.com/lkarlslund/koder/internal/memory/store/pebble"
	"github.com/lkarlslund/koder/internal/version"
)

func newMemoryCommand(root *rootOptions) *cobra.Command {
	command := &cobra.Command{Use: "memory", Short: "Maintain the independent Memory store"}
	command.AddCommand(newMemoryBackupCommand(root))
	command.AddCommand(newMemoryRestoreCommand(root))
	command.AddCommand(newMemoryMigrateCommand(root))
	return command
}

func newMemoryMigrateCommand(root *rootOptions) *cobra.Command {
	command := &cobra.Command{Use: "migrate", Short: "Move complete Memory state between storage backends"}
	command.AddCommand(newMemoryMigrateExportCommand(root), newMemoryMigrateImportCommand(root))
	return command
}

func newMemoryMigrateExportCommand(root *rootOptions) *cobra.Command {
	var includePersonal bool
	var jsonOutput bool
	command := &cobra.Command{
		Use:   "export <archive>",
		Short: "Export canonical Memory, revisions, and assets",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadWithOptions(root.loadOptions())
			if err != nil {
				return err
			}
			result, destination, err := createMemoryMigrationExport(cmd.Context(), cfg.StateDir(), args[0], includePersonal)
			if err != nil {
				return err
			}
			return writeMemoryMigrationExportResult(cmd.OutOrStdout(), destination, result, jsonOutput)
		},
	}
	command.Flags().BoolVar(&includePersonal, "include-personal", false, "Confirm inclusion of private personal Memory")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Write machine-readable export metadata")
	return command
}

func newMemoryMigrateImportCommand(root *rootOptions) *cobra.Command {
	var confirm bool
	var includePersonal bool
	var jsonOutput bool
	command := &cobra.Command{
		Use:   "import <archive>",
		Short: "Import a migration archive into an empty Memory store",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadWithOptions(root.loadOptions())
			if err != nil {
				return err
			}
			result, source, err := importMemoryMigration(cmd.Context(), cfg.StateDir(), args[0], confirm, includePersonal)
			if err != nil {
				return err
			}
			return writeMemoryMigrationImportResult(cmd.OutOrStdout(), source, result, jsonOutput)
		},
	}
	command.Flags().BoolVar(&confirm, "confirm", false, "Confirm import into the selected empty Memory store")
	command.Flags().BoolVar(&includePersonal, "include-personal", false, "Confirm import of private personal Memory")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Write machine-readable import metadata")
	return command
}

func createMemoryMigrationExport(ctx context.Context, stateDir, destination string, includePersonal bool) (migrationarchive.ExportResult, string, error) {
	destination = strings.TrimSpace(destination)
	if destination == "" {
		return migrationarchive.ExportResult{}, "", fmt.Errorf("memory migration export destination is required")
	}
	absDestination, err := filepath.Abs(destination)
	if err != nil {
		return migrationarchive.ExportResult{}, "", fmt.Errorf("resolve memory migration destination: %w", err)
	}
	if _, err := os.Lstat(absDestination); err == nil {
		return migrationarchive.ExportResult{}, "", fmt.Errorf("%w: migration export refuses to overwrite %s", memoryStoreAPI.ErrConflict, absDestination)
	} else if !errors.Is(err, os.ErrNotExist) {
		return migrationarchive.ExportResult{}, "", fmt.Errorf("inspect memory migration destination: %w", err)
	}
	store, err := memoryPebble.OpenExisting(stateDir)
	if err != nil {
		return migrationarchive.ExportResult{}, "", err
	}
	defer func() { _ = store.Close() }()
	snapshot, _, err := memoryStoreAPI.ExportMigrationSnapshot(ctx, store)
	if err != nil {
		return migrationarchive.ExportResult{}, "", err
	}
	if migrationContainsPersonal(snapshot) && !includePersonal {
		return migrationarchive.ExportResult{}, "", fmt.Errorf("%w; retry with --include-personal after confirming the archive destination is private", memoryService.ErrPersonalExportConsent)
	}
	parent := filepath.Dir(absDestination)
	temporary, err := os.CreateTemp(parent, ".memory-migration-export-*")
	if err != nil {
		return migrationarchive.ExportResult{}, "", fmt.Errorf("create memory migration export: %w", err)
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
		return migrationarchive.ExportResult{}, "", fmt.Errorf("sync memory migration export: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return migrationarchive.ExportResult{}, "", fmt.Errorf("close memory migration export: %w", err)
	}
	// A same-directory hard link publishes the fully synced file without the
	// overwrite behavior of os.Rename on Unix. A racing destination wins safely.
	if err := os.Link(temporaryPath, absDestination); err != nil {
		return migrationarchive.ExportResult{}, "", fmt.Errorf("publish memory migration export: %w", err)
	}
	succeeded = true
	_ = os.Remove(temporaryPath)
	return result, absDestination, nil
}

func importMemoryMigration(ctx context.Context, stateDir, source string, confirmed, includePersonal bool) (memoryStoreAPI.MigrationStats, string, error) {
	if !confirmed {
		return memoryStoreAPI.MigrationStats{}, "", fmt.Errorf("memory migration import requires --confirm and an empty target store")
	}
	absSource, err := filepath.Abs(strings.TrimSpace(source))
	if err != nil || strings.TrimSpace(source) == "" {
		return memoryStoreAPI.MigrationStats{}, "", fmt.Errorf("memory migration import archive is required")
	}
	file, err := os.Open(absSource)
	if err != nil {
		return memoryStoreAPI.MigrationStats{}, "", fmt.Errorf("open memory migration archive: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return memoryStoreAPI.MigrationStats{}, "", fmt.Errorf("inspect memory migration archive: %w", err)
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return memoryStoreAPI.MigrationStats{}, "", fmt.Errorf("memory migration archive must be a regular file")
	}
	archive, err := migrationarchive.Parse(ctx, file, info.Size())
	closeErr := file.Close()
	if err != nil {
		return memoryStoreAPI.MigrationStats{}, "", err
	}
	if closeErr != nil {
		return memoryStoreAPI.MigrationStats{}, "", fmt.Errorf("close memory migration archive: %w", closeErr)
	}
	if migrationContainsPersonal(archive.Snapshot) && !includePersonal {
		return memoryStoreAPI.MigrationStats{}, "", fmt.Errorf("memory migration contains private personal Memory; retry with --include-personal after confirming the target")
	}
	store, err := memoryPebble.Open(stateDir)
	if err != nil {
		return memoryStoreAPI.MigrationStats{}, "", err
	}
	defer func() { _ = store.Close() }()
	stats, err := memoryStoreAPI.ImportMigrationSnapshot(ctx, store, archive.Snapshot)
	if err != nil {
		return memoryStoreAPI.MigrationStats{}, "", err
	}
	return stats, absSource, nil
}

func migrationContainsPersonal(snapshot memoryStoreAPI.MigrationSnapshot) bool {
	for _, record := range snapshot.Records {
		if record.Kind != memoryStoreAPI.RecordKindChunk {
			continue
		}
		chunk := record.Chunk
		if chunk.ID == memoryService.PersonalMeChunkID || chunk.Kind == memory.ChunkKindPersonal || chunk.Scope.Kind == memory.ScopeKindPersonal {
			return true
		}
	}
	return false
}

func writeMemoryMigrationExportResult(output io.Writer, destination string, result migrationarchive.ExportResult, jsonOutput bool) error {
	if jsonOutput {
		return json.NewEncoder(output).Encode(struct {
			Path   string `json:"path"`
			SHA256 string `json:"sha256"`
			Size   int64  `json:"size"`
			memoryStoreAPI.MigrationStats
		}{Path: destination, SHA256: result.SHA256, Size: result.Size, MigrationStats: result.Stats})
	}
	_, err := fmt.Fprintf(output, "Memory migration archive created at %s\nSHA-256: %s  Size: %d bytes\nRecords: %d  Revisions: %d  Assets: %d\n", destination, result.SHA256, result.Size, result.Stats.Total, result.Stats.Revisions, result.Stats.Assets)
	return err
}

func writeMemoryMigrationImportResult(output io.Writer, source string, stats memoryStoreAPI.MigrationStats, jsonOutput bool) error {
	if jsonOutput {
		return json.NewEncoder(output).Encode(struct {
			Path string `json:"path"`
			memoryStoreAPI.MigrationStats
		}{Path: source, MigrationStats: stats})
	}
	_, err := fmt.Fprintf(output, "Memory migration imported and validated from %s\nRecords: %d  Revisions: %d  Assets: %d\n", source, stats.Total, stats.Revisions, stats.Assets)
	return err
}

func newMemoryBackupCommand(root *rootOptions) *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{
		Use:   "backup <destination-directory>",
		Short: "Create and validate an independent Memory checkpoint",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadWithOptions(root.loadOptions())
			if err != nil {
				return err
			}
			info, destination, err := createMemoryBackup(cmd.Context(), cfg.StateDir(), args[0])
			if err != nil {
				return err
			}
			return writeMemoryBackupResult(cmd.OutOrStdout(), destination, info, jsonOutput)
		},
	}
	command.Flags().BoolVar(&jsonOutput, "json", false, "Write machine-readable backup metadata")
	return command
}

func createMemoryBackup(ctx context.Context, stateDir, destination string) (memoryPebble.CheckpointInfo, string, error) {
	destination = strings.TrimSpace(destination)
	if destination == "" {
		return memoryPebble.CheckpointInfo{}, "", fmt.Errorf("memory backup destination is required")
	}
	absDestination, err := filepath.Abs(destination)
	if err != nil {
		return memoryPebble.CheckpointInfo{}, "", fmt.Errorf("resolve memory backup destination: %w", err)
	}
	store, err := memoryPebble.OpenExisting(stateDir)
	if err != nil {
		return memoryPebble.CheckpointInfo{}, "", err
	}
	defer func() { _ = store.Close() }()
	if err := store.Checkpoint(ctx, absDestination); err != nil {
		return memoryPebble.CheckpointInfo{}, "", err
	}
	info, err := memoryPebble.ValidateCheckpoint(ctx, absDestination)
	if err != nil {
		return memoryPebble.CheckpointInfo{}, "", err
	}
	return info, absDestination, nil
}

func writeMemoryBackupResult(output io.Writer, destination string, info memoryPebble.CheckpointInfo, jsonOutput bool) error {
	if jsonOutput {
		return json.NewEncoder(output).Encode(struct {
			Path string `json:"path"`
			memoryPebble.CheckpointInfo
		}{Path: destination, CheckpointInfo: info})
	}
	_, err := fmt.Fprintf(output, "Memory backup created and validated at %s\nBackend: %s  Schema: %d  Index generation: %d\n", destination, info.Backend, info.SchemaVersion, info.IndexGeneration)
	return err
}

func newMemoryRestoreCommand(root *rootOptions) *cobra.Command {
	var confirm bool
	var jsonOutput bool
	command := &cobra.Command{
		Use:   "restore <backup-directory>",
		Short: "Validate and restore an offline Memory checkpoint",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadWithOptions(root.loadOptions())
			if err != nil {
				return err
			}
			result, err := restoreMemoryBackup(cmd.Context(), cfg.StateDir(), args[0], confirm)
			if err != nil {
				return err
			}
			return writeMemoryRestoreResult(cmd.OutOrStdout(), result, jsonOutput)
		},
	}
	command.Flags().BoolVar(&confirm, "confirm", false, "Confirm replacement of the offline Memory database")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Write machine-readable restore metadata")
	return command
}

func restoreMemoryBackup(ctx context.Context, stateDir, source string, confirmed bool) (memoryPebble.RestoreInfo, error) {
	if !confirmed {
		return memoryPebble.RestoreInfo{}, fmt.Errorf("memory restore requires --confirm; stop Koder and verify the backup path before retrying")
	}
	return memoryPebble.RestoreCheckpoint(ctx, stateDir, source)
}

func writeMemoryRestoreResult(output io.Writer, result memoryPebble.RestoreInfo, jsonOutput bool) error {
	if jsonOutput {
		return json.NewEncoder(output).Encode(result)
	}
	_, err := fmt.Fprintf(
		output,
		"Memory restored and validated from %s\nLive database: %s\nPrevious database retained for rollback: %s\nBackend: %s  Schema: %d  Index generation: %d\n",
		result.SourcePath,
		result.LivePath,
		result.RollbackPath,
		result.Backend,
		result.SchemaVersion,
		result.IndexGeneration,
	)
	return err
}
