package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/lkarlslund/koder/internal/agent"
	"github.com/lkarlslund/koder/internal/app"
	"github.com/lkarlslund/koder/internal/assets"
	chatpkg "github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/debugsrv"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/mcp"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/runtimeprefs"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools/codesearchtool"
	"github.com/lkarlslund/koder/internal/version"
	"github.com/lkarlslund/koder/internal/webui"
)

const defaultWebBind = "127.0.0.1:0"
const processRestartExitCode = 75

var errProcessRestart = errors.New("process restart requested")

func NewRootCommand() *cobra.Command {
	opts := startupOptions{}
	cmd := &cobra.Command{
		Use:           "koder",
		Short:         "Browser coding agent",
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts.captureFlagState(cmd)
			workdir, err := opts.resolve()
			if err != nil {
				return err
			}
			return runKoder(cmd.Context(), app.StartupModeNew, workdir, startupOptsFromFlags(opts))
		},
	}
	bindStartupFlags(cmd, &opts)
	cmd.AddCommand(newDoctorCommand(&opts), newVersionCommand(), newSessionCommand(&opts), newDebugCommand(), newSkillCommand(&opts), newExecCommand(&opts))
	return cmd
}

type startupOptions struct {
	projectRoot   string
	dataDir       string
	noOpenBrowser bool
	webBind       string
	webBindSet    bool
}

func bindStartupFlags(cmd *cobra.Command, opts *startupOptions) {
	flags := cmd.PersistentFlags()
	flags.StringVar(&opts.projectRoot, "project-root", "", "Start koder with this local project folder")
	flags.StringVar(&opts.dataDir, "data-dir", "", "Store config, sessions, cache, and managed assets under this directory")
	flags.BoolVar(&opts.noOpenBrowser, "nobrowser", false, "Do not open a browser for the web UI")
	flags.StringVar(&opts.webBind, "web-bind", defaultWebBind, "Web UI bind address")
}

func (o *startupOptions) captureFlagState(cmd *cobra.Command) {
	if cmd == nil {
		return
	}
	if flag := cmd.Flags().Lookup("web-bind"); flag != nil {
		o.webBindSet = flag.Changed
	}
}

func (o startupOptions) resolve() (string, error) {
	projectRoot := strings.TrimSpace(o.projectRoot)
	if projectRoot == "" {
		var err error
		projectRoot, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	abs, err := filepath.Abs(projectRoot)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("project root must be a directory: %s", abs)
	}
	return abs, nil
}

func (o startupOptions) loadOptions() config.LoadOptions {
	return config.LoadOptions{DataDir: strings.TrimSpace(o.dataDir)}
}

type startupConfig struct {
	LoadOptions     config.LoadOptions
	NoOpenBrowser   bool
	WebBind         string
	WebBindExplicit bool
}

func runKoder(ctx context.Context, mode app.StartupMode, workdir string, startupOpts startupConfig) error {
	defer codesearchtool.CloseLanguageServers()
	cfg, err := config.LoadWithOptions(startupOpts.LoadOptions)
	if err != nil {
		return err
	}
	if err := syncManagedUserAssets(ctx, cfg); err != nil {
		return err
	}
	st, err := store.OpenWithOptions(cfg.StateDir(), store.Options{Backend: cfg.Store.Backend})
	if err != nil {
		return err
	}
	defer st.Close()
	recorder := debugsrv.NewRecorder()

	mcpManager, err := mcp.NewManager(cfg.MCPServers)
	if err != nil {
		return err
	}

	go func() {
		_ = mcpManager.ConnectAll(context.Background())
	}()

	engine := agent.New(cfg, st, recorder, mcpManager)
	return runWeb(ctx, cfg, st, engine, mode, recorder, workdir, startupOpts)
}

func syncManagedUserAssets(ctx context.Context, cfg config.Config) error {
	items, err := assets.UserDefaults()
	if err != nil {
		return err
	}
	_, err = assets.Sync(ctx, cfg.ManagedAssetsDir(), items)
	return err
}

func runWeb(ctx context.Context, cfg config.Config, st *store.Store, engine *agent.Engine, mode app.StartupMode, recorder *debugsrv.Recorder, workdir string, startupOpts startupConfig) error {
	controller := app.New(cfg, engine)
	if err := controller.Start(ctx, mode, workdir); err != nil {
		return err
	}
	bind, err := webBindForLaunch(ctx, st, startupOpts)
	if err != nil {
		return err
	}
	restartRequested := make(chan struct{}, 1)
	server, err := startWebUI(ctx, controller, bind, startupOpts.NoOpenBrowser, recorder, func() error {
		select {
		case restartRequested <- struct{}{}:
			slog.Info("koder process restart enqueued")
		default:
			slog.Info("koder process restart already pending")
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := saveLastWebBind(ctx, st, server.Addr()); err != nil {
		fmt.Fprintf(os.Stderr, "koder web ui: failed to save web bind: %v\n", err)
	}
	fmt.Fprintf(os.Stderr, "koder web ui: %s\n", server.AppURL())
	if recorder != nil {
		recorder.UpdateProcess(debugsrv.ProcessDebug{DebugAPI: server.URL(), Status: "Web UI running"})
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM, syscall.SIGUSR1)
	defer signal.Stop(sig)
	for {
		select {
		case <-ctx.Done():
			slog.Info("koder context cancelled; shutting down")
			if err := controller.Shutdown(context.Background()); err != nil {
				return err
			}
			return ctx.Err()
		case <-restartRequested:
			slog.Info("koder restart request received by root loop", "exit_code", processRestartExitCode)
			slog.Info("koder exiting for process restart", "exit_code", processRestartExitCode)
			return errProcessRestart
		case signal := <-sig:
			reason := chatpkg.CancelReasonShutdownInterrupt
			if signal == syscall.SIGUSR1 {
				reason = chatpkg.CancelReasonRestartInterrupt
			}
			slog.Info("koder signal received", "signal", signal.String(), "reason", reason)
			if err := controller.ShutdownWithCancelReason(context.Background(), reason); err != nil {
				return err
			}
			if signal == syscall.SIGUSR1 {
				slog.Info("koder exiting for process restart", "exit_code", processRestartExitCode)
				return errProcessRestart
			}
			slog.Info("koder shutdown complete", "signal", signal.String())
			return nil
		}
	}
}

func startWebUI(ctx context.Context, controller *app.Controller, bind string, noOpenBrowser bool, recorder *debugsrv.Recorder, requestProcessRestart func() error) (*webui.Server, error) {
	return webui.Start(ctx, controller, webui.Options{
		Bind:                  bind,
		NoOpenBrowser:         noOpenBrowser,
		Debug:                 recorder,
		RequestProcessRestart: requestProcessRestart,
	})
}

func startupOptsFromFlags(opts startupOptions) startupConfig {
	return startupConfig{
		LoadOptions:     opts.loadOptions(),
		NoOpenBrowser:   opts.noOpenBrowser,
		WebBind:         opts.webBind,
		WebBindExplicit: opts.webBindSet,
	}
}

func webBindForLaunch(ctx context.Context, st *store.Store, startupOpts startupConfig) (string, error) {
	bind := strings.TrimSpace(startupOpts.WebBind)
	if startupOpts.WebBindExplicit {
		if bind == "" {
			return defaultWebBind, nil
		}
		return bind, nil
	}
	if st != nil {
		state, err := runtimeprefs.Global(ctx, st)
		if err != nil {
			return "", err
		}
		if isReusableWebBind(state.LastWebBind) {
			return state.LastWebBind, nil
		}
	}
	return defaultWebBind, nil
}

func saveLastWebBind(ctx context.Context, st *store.Store, bind string) error {
	if !isReusableWebBind(bind) {
		return nil
	}
	if st == nil {
		return nil
	}
	return runtimeprefs.SetLastWebBind(ctx, st, bind)
}

func isReusableWebBind(bind string) bool {
	bind = strings.TrimSpace(bind)
	if bind == "" {
		return false
	}
	_, port, err := net.SplitHostPort(bind)
	return err == nil && port != "" && port != "0"
}

func newDoctorCommand(startup *startupOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Validate config and provider connectivity",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.LoadWithOptions(startup.loadOptions())
			if err != nil {
				return err
			}
			if err := cfg.RequireProvider(); err != nil {
				return err
			}
			providerCfg, ok := cfg.Provider(cfg.Defaults.ProviderID)
			if !ok {
				return fmt.Errorf("default provider %q not configured", cfg.Defaults.ProviderID)
			}
			client, err := provider.New(cfg.Defaults.ProviderID, providerCfg, nil)
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
			fmt.Fprintf(cmd.OutOrStdout(), "provider: %s\n", cfg.Defaults.ProviderID)
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
			info := version.Current()
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", info.Name, info.Version)
			fmt.Fprintf(cmd.OutOrStdout(), "commit: %s\n", info.Commit)
			fmt.Fprintf(cmd.OutOrStdout(), "dirty: %s\n", info.Dirty)
			fmt.Fprintf(cmd.OutOrStdout(), "build_time: %s\n", info.BuildTime)
			fmt.Fprintf(cmd.OutOrStdout(), "go_version: %s\n", info.GoVersion)
			return nil
		},
	}
}

func newSessionCommand(startup *startupOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Inspect stored sessions",
	}
	cmd.AddCommand(newSessionDumpCommand(startup), newSessionTailCommand())
	return cmd
}

func newDebugCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "debug",
		Short: "Inspect debug API endpoints",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "info",
		Short: "Print debug API endpoint information",
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), "debug endpoints are served by the web UI under /debug")
			fmt.Fprintln(cmd.OutOrStdout(), "use the running koder web URL, for example http://127.0.0.1:44323/debug/runtime")
			return nil
		},
	})
	return cmd
}

func newSessionDumpCommand(startup *startupOptions) *cobra.Command {
	var sessionID id.ID
	cmd := &cobra.Command{
		Use:   "dump",
		Short: "Dump raw stored chat for a session as JSON",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if sessionID == "" {
				return fmt.Errorf("--id is required")
			}
			cfg, err := config.LoadWithOptions(startup.loadOptions())
			if err != nil {
				return err
			}
			st, err := store.OpenWithOptions(cfg.StateDir(), store.Options{Backend: cfg.Store.Backend})
			if err != nil {
				return err
			}
			defer st.Close()

			engine := agent.New(cfg, st, nil)
			owner, err := engine.LoadSession(cmd.Context(), sessionID)
			if err != nil {
				return err
			}
			snapshot := owner.Snapshot()
			type chatDump struct {
				Chat     domain.Chat           `json:"chat"`
				Timeline []domain.TimelineItem `json:"timeline"`
			}
			out := struct {
				SessionID id.ID      `json:"session_id"`
				Chats     []chatDump `json:"chats"`
			}{
				SessionID: sessionID,
			}
			for _, chat := range snapshot.Chats {
				rt, err := owner.Chat(cmd.Context(), chat.ID)
				if err != nil {
					return err
				}
				timeline, err := rt.Timeline(cmd.Context())
				if err != nil {
					return err
				}
				out.Chats = append(out.Chats, chatDump{Chat: chat, Timeline: timeline})
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(out)
		},
	}
	cmd.Flags().StringVar((*string)(&sessionID), "id", "", "Session ID to dump")
	return cmd
}

func newSessionTailCommand() *cobra.Command {
	var sessionID id.ID
	var addr string
	var interval time.Duration
	cmd := &cobra.Command{
		Use:   "tail",
		Short: "Poll live debug events for a session",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if sessionID == "" {
				return fmt.Errorf("--id is required")
			}
			if strings.TrimSpace(addr) == "" {
				return fmt.Errorf("set --addr to the running web UI address")
			}
			baseURL := debugBaseURL(addr)
			type payload struct {
				Events []debugsrv.RecordedEvent `json:"events"`
			}
			seen := map[string]struct{}{}
			for {
				resp, err := http.Get(fmt.Sprintf("%s/debug/sessions/%s/events", baseURL, sessionID))
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
					key := fmt.Sprintf("%s|%s|%s|%s|%s", event.Timestamp.Format(time.RFC3339Nano), event.SessionID, event.Source, event.Kind, event.Text)
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
	cmd.Flags().StringVar((*string)(&sessionID), "id", "", "Session ID to tail")
	cmd.Flags().StringVar(&addr, "addr", "", "Running web UI address or URL, for example 127.0.0.1:44323")
	cmd.Flags().DurationVar(&interval, "interval", time.Second, "Polling interval")
	return cmd
}

func debugBaseURL(addr string) string {
	addr = strings.TrimRight(strings.TrimSpace(addr), "/")
	addr = strings.TrimSuffix(addr, "/debug")
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		return addr
	}
	return "http://" + addr
}
