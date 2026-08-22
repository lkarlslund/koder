package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

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
