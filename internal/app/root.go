package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/lkarlslund/koder/internal/agent"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/debugsrv"
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
	cmd.AddCommand(newDoctorCommand(), newVersionCommand(), newResumeCommand(), newSessionCommand(), newDebugCommand())
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
	var debugServer *debugsrv.Server
	if bind := strings.TrimSpace(os.Getenv(debugsrv.EnvDebugAPI)); bind != "" {
		debugServer, err = debugsrv.Start(bind, st, debugsrv.NewRecorder())
		if err != nil {
			return err
		}
		defer debugServer.Close()
	}

	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	registry := tools.NewRegistry(wd)
	var recorder *debugsrv.Recorder
	if debugServer != nil {
		recorder = debugServer.Recorder()
	}
	engine := agent.New(cfg, st, registry, recorder)
	return tui.Run(cfg, st, engine, mode, recorder)
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
			client, err := provider.New(cfg.DefaultProvider, providerCfg, nil)
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
	cmd.AddCommand(newSessionDumpCommand(), newSessionTailCommand())
	return cmd
}

func newDebugCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "debug",
		Short: "Inspect debug API configuration",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "info",
		Short: "Print debug API environment configuration",
		RunE: func(cmd *cobra.Command, _ []string) error {
			bind := strings.TrimSpace(os.Getenv(debugsrv.EnvDebugAPI))
			if bind == "" {
				fmt.Fprintf(cmd.OutOrStdout(), "%s is not set\n", debugsrv.EnvDebugAPI)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s=%s\n", debugsrv.EnvDebugAPI, bind)
			if strings.HasSuffix(bind, ":0") {
				fmt.Fprintln(cmd.OutOrStdout(), "resolved address is only known while koder is running")
			}
			return nil
		},
	})
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

func newSessionTailCommand() *cobra.Command {
	var sessionID int64
	var addr string
	var interval time.Duration
	cmd := &cobra.Command{
		Use:   "tail",
		Short: "Poll live debug events for a session",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if sessionID <= 0 {
				return fmt.Errorf("--id must be greater than zero")
			}
			if strings.TrimSpace(addr) == "" {
				addr = strings.TrimSpace(os.Getenv(debugsrv.EnvDebugAPI))
			}
			if strings.TrimSpace(addr) == "" {
				return fmt.Errorf("set --addr or %s", debugsrv.EnvDebugAPI)
			}
			if strings.HasSuffix(addr, ":0") {
				return fmt.Errorf("debug api bind %q is unresolved; use the runtime address shown by koder or pass --addr", addr)
			}
			baseURL := "http://" + strings.TrimRight(addr, "/")
			type payload struct {
				Events []debugsrv.RecordedEvent `json:"events"`
			}
			seen := map[string]struct{}{}
			for {
				resp, err := http.Get(fmt.Sprintf("%s/debug/sessions/%d/events", baseURL, sessionID))
				if err != nil {
					return err
				}
				body, readErr := io.ReadAll(resp.Body)
				resp.Body.Close()
				if readErr != nil {
					return readErr
				}
				if resp.StatusCode >= 300 {
					return fmt.Errorf("debug api status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
				}
				var out payload
				if err := json.Unmarshal(body, &out); err != nil {
					return err
				}
				for _, event := range out.Events {
					key := fmt.Sprintf("%s|%d|%s|%s|%s", event.Timestamp.Format(time.RFC3339Nano), event.SessionID, event.Source, event.Kind, event.Text)
					if _, ok := seen[key]; ok {
						continue
					}
					seen[key] = struct{}{}
					fmt.Fprintf(cmd.OutOrStdout(), "%s [%s/%s] %s\n", event.Timestamp.Format(time.RFC3339), event.Source, event.Kind, strings.TrimSpace(event.Text))
				}
				select {
				case <-cmd.Context().Done():
					return cmd.Context().Err()
				case <-time.After(interval):
				}
			}
		},
	}
	cmd.Flags().Int64Var(&sessionID, "id", 0, "Session ID to tail")
	cmd.Flags().StringVar(&addr, "addr", "", "Resolved debug API address, for example 127.0.0.1:61347")
	cmd.Flags().DurationVar(&interval, "interval", time.Second, "Polling interval")
	return cmd
}
