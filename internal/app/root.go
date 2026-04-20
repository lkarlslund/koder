package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lkarlslund/koder/internal/agent"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
	"github.com/lkarlslund/koder/internal/tui"
	"github.com/lkarlslund/koder/internal/version"
)

func NewRootCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "koder",
		Short: "Terminal coding agent",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runTUI(cmd.Context(), tui.StartupModeNew)
		},
	}
	cmd.AddCommand(newDoctorCommand(), newVersionCommand(), newResumeCommand(), newSessionCommand())
	return cmd
}

func runTUI(ctx context.Context, mode tui.StartupMode) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	st, err := store.OpenWithOptions(cfg.StateDir(), store.Options{Backend: cfg.Store.Backend})
	if err != nil {
		return err
	}
	defer st.Close()

	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	registry := tools.NewRegistry(wd)
	engine := agent.New(cfg, st, registry)
	return tui.Run(cfg, st, engine, mode)
}

func newResumeCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "resume",
		Short: "Resume an existing session from a picker",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runTUI(cmd.Context(), tui.StartupModeResume)
		},
	}
}

func newDoctorCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Validate config and provider connectivity",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if err := cfg.RequireProvider(); err != nil {
				return err
			}
			providerCfg, ok := cfg.Provider(cfg.DefaultProvider)
			if !ok {
				return fmt.Errorf("default provider %q not configured", cfg.DefaultProvider)
			}
			client, err := provider.New(cfg.DefaultProvider, providerCfg)
			if err != nil {
				return err
			}
			if err := client.Health(cmd.Context()); err != nil {
				return err
			}
			models, err := client.ListModels(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "config: %s\n", cfg.Path())
			fmt.Fprintf(cmd.OutOrStdout(), "provider: %s\n", cfg.DefaultProvider)
			for _, model := range models {
				fmt.Fprintf(cmd.OutOrStdout(), "model: %s (%s)\n", model.ID, strings.TrimSpace(model.OwnedBy))
			}
			return nil
		},
	}
}

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version",
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", version.Name, version.Version)
			return nil
		},
	}
}

func newSessionCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Inspect stored sessions",
	}
	cmd.AddCommand(newSessionDumpCommand())
	return cmd
}

func newSessionDumpCommand() *cobra.Command {
	var sessionID int64
	cmd := &cobra.Command{
		Use:   "dump",
		Short: "Dump raw stored chat for a session as JSON",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if sessionID <= 0 {
				return fmt.Errorf("--id must be greater than zero")
			}
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			st, err := store.OpenWithOptions(cfg.StateDir(), store.Options{Backend: cfg.Store.Backend})
			if err != nil {
				return err
			}
			defer st.Close()

			messages, partsByMessage, err := st.PartsForSession(cmd.Context(), sessionID)
			if err != nil {
				return err
			}
			type messageDump struct {
				Message domain.Message `json:"message"`
				Parts   []domain.Part  `json:"parts"`
			}
			out := struct {
				SessionID int64         `json:"session_id"`
				Messages  []messageDump `json:"messages"`
			}{
				SessionID: sessionID,
			}
			for _, message := range messages {
				out.Messages = append(out.Messages, messageDump{
					Message: message,
					Parts:   partsByMessage[message.ID],
				})
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(out)
		},
	}
	cmd.Flags().Int64Var(&sessionID, "id", 0, "Session ID to dump")
	return cmd
}
