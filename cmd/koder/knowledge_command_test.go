package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
	knowledgePebble "github.com/lkarlslund/koder/internal/knowledge/store/pebble"
)

func TestCreateKnowledgeBackupCreatesValidatedIndependentCheckpoint(t *testing.T) {
	t.Parallel()
	stateDir := t.TempDir()
	store, err := knowledgePebble.Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "knowledge-backup")
	info, resolved, err := createKnowledgeBackup(context.Background(), stateDir, destination)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != destination || info.Backend != "pebble" || info.SchemaVersion == 0 || info.IndexGeneration == 0 {
		t.Fatalf("backup = path %q info %#v", resolved, info)
	}
	if _, err := knowledgePebble.ValidateCheckpoint(context.Background(), destination); err != nil {
		t.Fatalf("ValidateCheckpoint() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(destination, "CURRENT")); err != nil {
		t.Fatalf("checkpoint CURRENT: %v", err)
	}
}

func TestWriteKnowledgeBackupResultSupportsMachineReadableOutput(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	info := knowledgePebble.CheckpointInfo{Backend: "pebble", SchemaVersion: 1, IndexGeneration: 7}
	if err := writeKnowledgeBackupResult(&output, "/backup", info, true); err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["path"] != "/backup" || result["backend"] != "pebble" || result["index_generation"] != float64(7) {
		t.Fatalf("JSON result = %#v", result)
	}
}

func TestCreateKnowledgeBackupRefusesMissingSource(t *testing.T) {
	t.Parallel()
	stateDir := t.TempDir()
	destination := filepath.Join(t.TempDir(), "must-not-exist")
	if _, _, err := createKnowledgeBackup(context.Background(), stateDir, destination); err == nil {
		t.Fatal("createKnowledgeBackup(missing source) unexpectedly succeeded")
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("missing-source backup created destination: %v", err)
	}
}

func TestRestoreKnowledgeBackupRequiresConfirmation(t *testing.T) {
	t.Parallel()
	if _, err := restoreKnowledgeBackup(context.Background(), "unused", "unused", false); err == nil || !strings.Contains(err.Error(), "--confirm") {
		t.Fatalf("restoreKnowledgeBackup(unconfirmed) error = %v", err)
	}
}

func TestRestoreKnowledgeBackupRestoresValidatedCheckpoint(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	liveStateDir := t.TempDir()
	liveStore, err := knowledgePebble.Open(liveStateDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := liveStore.Close(); err != nil {
		t.Fatal(err)
	}

	sourceStateDir := t.TempDir()
	sourceStore, err := knowledgePebble.Open(sourceStateDir)
	if err != nil {
		t.Fatal(err)
	}
	backupPath := filepath.Join(t.TempDir(), "knowledge-backup")
	if err := sourceStore.Checkpoint(ctx, backupPath); err != nil {
		t.Fatal(err)
	}
	if err := sourceStore.Close(); err != nil {
		t.Fatal(err)
	}

	result, err := restoreKnowledgeBackup(ctx, liveStateDir, backupPath, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.SourcePath != backupPath || result.RollbackPath == "" {
		t.Fatalf("restore result = %#v", result)
	}
	if _, err := knowledgePebble.ValidateCheckpoint(ctx, result.RollbackPath); err != nil {
		t.Fatalf("rollback checkpoint: %v", err)
	}
}

func TestRestoreKnowledgeBackupRefusesRunningStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	liveStateDir := t.TempDir()
	liveStore, err := knowledgePebble.Open(liveStateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = liveStore.Close() }()

	sourceStateDir := t.TempDir()
	sourceStore, err := knowledgePebble.Open(sourceStateDir)
	if err != nil {
		t.Fatal(err)
	}
	backupPath := filepath.Join(t.TempDir(), "knowledge-backup")
	if err := sourceStore.Checkpoint(ctx, backupPath); err != nil {
		t.Fatal(err)
	}
	if err := sourceStore.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := restoreKnowledgeBackup(ctx, liveStateDir, backupPath, true); !errors.Is(err, knowledgeStore.ErrConflict) {
		t.Fatalf("restoreKnowledgeBackup(running) error = %v, want ErrConflict", err)
	}
}

func TestWriteKnowledgeRestoreResultSupportsHumanAndMachineOutput(t *testing.T) {
	t.Parallel()
	result := knowledgePebble.RestoreInfo{
		CheckpointInfo: knowledgePebble.CheckpointInfo{Backend: "pebble", SchemaVersion: 1, IndexGeneration: 7},
		SourcePath:     "/backup",
		LivePath:       "/live",
		RollbackPath:   "/rollback",
	}
	var human bytes.Buffer
	if err := writeKnowledgeRestoreResult(&human, result, false); err != nil {
		t.Fatal(err)
	}
	if output := human.String(); !strings.Contains(output, "/backup") || !strings.Contains(output, "/rollback") {
		t.Fatalf("human output = %q", output)
	}
	var machine bytes.Buffer
	if err := writeKnowledgeRestoreResult(&machine, result, true); err != nil {
		t.Fatal(err)
	}
	var decoded knowledgePebble.RestoreInfo
	if err := json.Unmarshal(machine.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.LivePath != "/live" || decoded.IndexGeneration != 7 {
		t.Fatalf("JSON result = %#v", decoded)
	}
}
