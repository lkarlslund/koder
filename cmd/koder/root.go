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
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools/codesearchtool"
	"github.com/lkarlslund/koder/internal/version"
	"github.com/lkarlslund/koder/internal/webui"
)

const defaultWebBind = "127.0.0.1:0"
const processRestartExitCode = 75

var errProcessRestart = errors.New("process restart requested")

func NewRootCommand() *cobra.Command {
	opts := rootOptions{}
	cmd := &cobra.Command{
		Use:           "koder",
		Short:         "Browser coding agent",
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE:          func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	bindRootFlags(cmd, &opts)
	cmd.AddCommand(newServeCommand(&opts), newDoctorCommand(&opts), newVersionCommand(), newSessionCommand(&opts), newDebugCommand(), newSkillCommand(&opts), newExecCommand(&opts))
	return cmd
}

type rootOptions struct {
	dataDir string
}

func bindRootFlags(cmd *cobra.Command, opts *rootOptions) {
	flags := cmd.PersistentFlags()
	flags.StringVar(&opts.dataDir, "data-dir", "", "Store config, sessions, cache, and managed assets under this directory")
}

func (o *rootOptions) loadOptions() config.LoadOptions {
	if o == nil {
		return config.LoadOptions{}
	}
	return config.LoadOptions{DataDir: strings.TrimSpace(o.dataDir)}
}

type serveOptions struct {
	noOpenBrowser bool
	webBind       string
	webBindSet    bool
}

func newServeCommand(root *rootOptions) *cobra.Command {
	opts := serveOptions{webBind: defaultWebBind}
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the browser web UI server",
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts.captureFlagState(cmd)
			return runKoder(cmd.Context(), app.StartupModeNew, serveConfigFromFlags(root, opts))
		},
	}
	flags := cmd.Flags()
	flags.BoolVar(&opts.noOpenBrowser, "nobrowser", false, "Do not open a browser for the web UI")
	flags.StringVar(&opts.webBind, "web-bind", defaultWebBind, "Web UI bind address")
	return cmd
}

func (o *serveOptions) captureFlagState(cmd *cobra.Command) {
	if cmd == nil {
		return
	}
	if flag := cmd.Flags().Lookup("web-bind"); flag != nil {
		o.webBindSet = flag.Changed
	}
}

type serveConfig struct {
	LoadOptions     config.LoadOptions
	NoOpenBrowser   bool
	WebBind         string
	WebBindExplicit bool
}

func runKoder(ctx context.Context, mode app.StartupMode, serveOpts serveConfig) error {
	defer codesearchtool.CloseLanguageServers()
	cfg, err := config.LoadWithOptions(serveOpts.LoadOptions)
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
	return runWeb(ctx, cfg, engine, mode, recorder, serveOpts)
}

func syncManagedUserAssets(ctx context.Context, cfg config.Config) error {
	items, err := assets.UserDefaults()
	if err != nil {
		return err
	}
	_, err = assets.Sync(ctx, cfg.ManagedAssetsDir(), items)
	return err
}

func runWeb(ctx context.Context, cfg config.Config, engine *agent.Engine, mode app.StartupMode, recorder *debugsrv.Recorder, serveOpts serveConfig) error {
	controller := app.New(cfg, engine)
	if err := controller.Start(ctx, mode, ""); err != nil {
		return err
	}
	bind, err := webBindForLaunch(serveOpts)
	if err != nil {
		return err
	}
	restartRequested := make(chan struct{}, 1)
	server, err := startWebUI(ctx, controller, bind, serveOpts.NoOpenBrowser, recorder, func() error {
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

func serveConfigFromFlags(root *rootOptions, opts serveOptions) serveConfig {
	if root == nil {
		root = &rootOptions{}
	}
	return serveConfig{
		LoadOptions:     root.loadOptions(),
		NoOpenBrowser:   opts.noOpenBrowser,
		WebBind:         opts.webBind,
		WebBindExplicit: opts.webBindSet,
	}
}

func webBindForLaunch(serveOpts serveConfig) (string, error) {
	bind := strings.TrimSpace(serveOpts.WebBind)
	if serveOpts.WebBindExplicit {
		if bind == "" {
			return defaultWebBind, nil
		}
		return bind, nil
	}
	return defaultWebBind, nil
}

func isReusableWebBind(bind string) bool {
	bind = strings.TrimSpace(bind)
	if bind == "" {
		return false
	}
	_, port, err := net.SplitHostPort(bind)
	return err == nil && port != "" && port != "0"
}

func newDoctorCommand(root *rootOptions) *cobra.Command {
	var opts doctorOptions
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Validate config and provider connectivity",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDoctor(cmd.Context(), cmd.OutOrStdout(), root.loadOptions(), opts)
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&opts.providerID, "provider", "", "Provider id to check")
	flags.StringVar(&opts.modelID, "model", "", "Model id to check")
	flags.BoolVar(&opts.tts, "tts", false, "Check the configured TTS provider/model")
	flags.BoolVar(&opts.listModels, "list-models", false, "List models returned by the provider")
	return cmd
}

type doctorOptions struct {
	providerID string
	modelID    string
	tts        bool
	listModels bool
}

func runDoctor(ctx context.Context, out io.Writer, loadOpts config.LoadOptions, opts doctorOptions) error {
	cfg, err := config.LoadWithOptions(loadOpts)
	if err != nil {
		return err
	}
	providerID := strings.TrimSpace(opts.providerID)
	modelID := strings.TrimSpace(opts.modelID)
	if opts.tts {
		if providerID == "" {
			providerID = strings.TrimSpace(cfg.UI.TTS.ProviderID)
		}
		if modelID == "" {
			modelID = strings.TrimSpace(cfg.UI.TTS.ModelID)
		}
	}
	if providerID == "" {
		if err := cfg.RequireProvider(); err != nil {
			return err
		}
		providerID = strings.TrimSpace(cfg.Defaults.ProviderID)
	}
	if modelID == "" && !opts.tts {
		modelID = strings.TrimSpace(cfg.Defaults.ModelID)
	}
	providerCfg, ok := cfg.Provider(providerID)
	if !ok {
		return fmt.Errorf("provider %q not configured", providerID)
	}
	client, err := provider.New(providerID, providerCfg, nil)
	if err != nil {
		return err
	}
	if err := client.Health(ctx); err != nil {
		return err
	}
	fmt.Fprintf(out, "config: %s\n", cfg.Path())
	fmt.Fprintf(out, "provider: %s\n", providerID)
	if modelID != "" {
		fmt.Fprintf(out, "model: %s\n", modelID)
	}
	if opts.tts {
		fmt.Fprintln(out, "mode: tts")
	}
	if !opts.listModels && modelID == "" {
		return nil
	}
	models, err := client.ListModels(ctx)
	if err != nil {
		return err
	}
	if modelID != "" && !modelListContains(models, modelID) {
		return fmt.Errorf("model %q was not returned by provider %q", modelID, providerID)
	}
	if opts.listModels {
		for _, model := range models {
			fmt.Fprintf(out, "model: %s (%s)\n", model.ID, strings.TrimSpace(model.OwnedBy))
		}
	}
	return nil
}

func modelListContains(models []domain.Model, modelID string) bool {
	modelID = strings.TrimSpace(modelID)
	for _, model := range models {
		if strings.TrimSpace(model.ID) == modelID {
			return true
		}
	}
	return false
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

func newSessionCommand(root *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Inspect stored sessions",
	}
	cmd.AddCommand(newSessionDumpCommand(root))
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
	cmd.AddCommand(newDebugTailCommand())
	return cmd
}

func newSessionDumpCommand(root *rootOptions) *cobra.Command {
	var sessionID id.ID
	cmd := &cobra.Command{
		Use:   "dump",
		Short: "Dump raw stored chat for a session as JSON",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if sessionID == "" {
				return fmt.Errorf("--id is required")
			}
			cfg, err := config.LoadWithOptions(root.loadOptions())
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

func newDebugTailCommand() *cobra.Command {
	var sessionID id.ID
	var url string
	var interval time.Duration
	cmd := &cobra.Command{
		Use:   "tail",
		Short: "Poll live debug events for a session",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if sessionID == "" {
				return fmt.Errorf("--session is required")
			}
			if strings.TrimSpace(url) == "" {
				return fmt.Errorf("set --url to the running web UI URL")
			}
			baseURL := debugBaseURL(url)
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
	cmd.Flags().StringVar((*string)(&sessionID), "session", "", "Session ID to tail")
	cmd.Flags().StringVar(&url, "url", "", "Running web UI URL or address, for example http://127.0.0.1:44323")
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
