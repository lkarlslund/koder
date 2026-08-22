package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lkarlslund/koder/internal/config"
	knowledgePebble "github.com/lkarlslund/koder/internal/knowledge/store/pebble"
)

func newKnowledgeCommand(root *rootOptions) *cobra.Command {
	command := &cobra.Command{Use: "knowledge", Short: "Maintain the independent Knowledge store"}
	command.AddCommand(newKnowledgeBackupCommand(root))
	return command
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
