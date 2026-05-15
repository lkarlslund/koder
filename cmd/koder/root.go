package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	"github.com/lkarlslund/koder/internal/agents"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/debugsrv"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/execruntime"
	"github.com/lkarlslund/koder/internal/mcp"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
	"github.com/lkarlslund/koder/internal/tools/codesearchtool"
	"github.com/lkarlslund/koder/internal/uicore"
	"github.com/lkarlslund/koder/internal/version"
	"github.com/lkarlslund/koder/internal/webui"
)

const defaultWebBind = "127.0.0.1:0"
const cachedWebBindRetryTimeout = 30 * time.Second
const cachedWebBindRetryInterval = 100 * time.Millisecond

func NewRootCommand() *cobra.Command {
	opts := startupOptions{}
	cmd := &cobra.Command{
		Use:   "koder",
		Short: "Browser coding agent",
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts.captureFlagState(cmd)
			workdir, err := opts.resolve()
			if err != nil {
				return err
			}
			return runKoder(cmd.Context(), uicore.StartupModeNew, workdir, startupOptsFromFlags(opts, false))
		},
	}
	bindStartupFlags(cmd, &opts)
	cmd.AddCommand(newDoctorCommand(), newVersionCommand(), newResumeCommand(), newSessionCommand(), newDebugCommand())
	return cmd
}

type startupOptions struct {
	cwd         string
	projectRoot string
	noBrowser   bool
	webBind     string
	webBindSet  bool
}

func bindStartupFlags(cmd *cobra.Command, opts *startupOptions) {
	flags := cmd.PersistentFlags()
	flags.StringVar(&opts.cwd, "cwd", "", "Start koder in this working directory")
	flags.StringVar(&opts.projectRoot, "project-root", "", "Alias for --cwd")
	flags.BoolVar(&opts.noBrowser, "nobrowser", false, "Do not open a browser for the web UI")
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
	cwd := strings.TrimSpace(o.cwd)
	projectRoot := strings.TrimSpace(o.projectRoot)
	if cwd != "" && projectRoot != "" && cwd != projectRoot {
		return "", fmt.Errorf("--cwd and --project-root must match when both are set")
	}
	if cwd == "" {
		cwd = projectRoot
	}
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("workdir must be a directory: %s", abs)
	}
	return abs, nil
}

type startupConfig struct {
	ShowAllSessions bool
	NoBrowser       bool
	WebBind         string
	WebBindExplicit bool
}

func runKoder(ctx context.Context, mode uicore.StartupMode, workdir string, startupOpts startupConfig) error {
	defer codesearchtool.CloseLanguageServers()
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

	var recorder *debugsrv.Recorder
	if debugServer != nil {
		recorder = debugServer.Recorder()
	}

	mcpManager, err := mcp.NewManager(cfg.MCPServers)
	if err != nil {
		return err
	}

	go func() {
		_ = mcpManager.ConnectAll(context.Background())
	}()

	registry := tools.NewRegistry(agents.FindProjectRoot(workdir))
	registry.SetEditForgiveness(cfg.UI.EditForgiveness)
	registry.SetExecControl(execruntime.NewManager())
	engine := agent.New(cfg, st, registry, recorder, workdir, mcpManager)
	registry.SetChatControl(engine)
	return runWeb(ctx, cfg, st, engine, mode, recorder, debugServer, workdir, startupOpts)
}

func runWeb(ctx context.Context, cfg config.Config, st *store.Store, engine *agent.Engine, mode uicore.StartupMode, recorder *debugsrv.Recorder, debugServer *debugsrv.Server, workdir string, startupOpts startupConfig) error {
	controller := uicore.New(cfg, st, engine, workdir)
	if err := controller.Start(ctx, mode); err != nil {
		return err
	}
	bind, cachedBind := webBindForLaunch(ctx, st, workdir, startupOpts)
	server, err := startWebUI(ctx, controller, bind, cachedBind, startupOpts.NoBrowser)
	if err != nil {
		return err
	}
	if !startupOpts.WebBindExplicit || isReusableWebBind(startupOpts.WebBind) {
		if err := saveWorkspaceWebBind(ctx, st, workdir, server.Addr()); err != nil {
			fmt.Fprintf(os.Stderr, "koder web ui: failed to save workspace port: %v\n", err)
		}
	}
	fmt.Fprintf(os.Stderr, "koder web ui: %s\n", server.URL())
	if recorder != nil {
		recorder.UpdateRuntime(debugsrv.RuntimeSnapshot{DebugAPI: debugAPIAddr(debugServer), Status: "Web UI running"})
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)
	select {
	case <-ctx.Done():
		if err := controller.Shutdown(context.Background()); err != nil {
			return err
		}
		return ctx.Err()
	case <-sig:
		return controller.Shutdown(context.Background())
	}
}

func startWebUI(ctx context.Context, controller *uicore.Controller, bind string, cachedBind bool, noBrowser bool) (*webui.Server, error) {
	return startWebUIWithRetry(ctx, controller, bind, cachedBind, noBrowser, cachedWebBindRetryTimeout, cachedWebBindRetryInterval)
}

func startWebUIWithRetry(ctx context.Context, controller *uicore.Controller, bind string, cachedBind bool, noBrowser bool, retryTimeout time.Duration, retryInterval time.Duration) (*webui.Server, error) {
	server, err := webui.Start(ctx, controller, webui.Options{
		Bind:      bind,
		NoBrowser: noBrowser,
	})
	if err == nil || !cachedBind {
		return server, err
	}
	timer := time.NewTimer(retryTimeout)
	defer timer.Stop()
	ticker := time.NewTicker(retryInterval)
	defer ticker.Stop()
	lastErr := err
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			server, err = webui.Start(ctx, controller, webui.Options{
				Bind:      bind,
				NoBrowser: noBrowser,
			})
			if err == nil {
				return server, nil
			}
			lastErr = err
		case <-timer.C:
			fmt.Fprintf(os.Stderr, "koder web ui: cached bind %s unavailable after %s, falling back to %s\n", bind, retryTimeout, defaultWebBind)
			server, err = webui.Start(ctx, controller, webui.Options{
				Bind:      defaultWebBind,
				NoBrowser: noBrowser,
			})
			if err != nil {
				return nil, fmt.Errorf("start cached web ui on %s: %w; fallback failed: %v", bind, lastErr, err)
			}
			return server, nil
		}
	}
}

func startupOptsFromFlags(opts startupOptions, showAllSessions bool) startupConfig {
	return startupConfig{
		ShowAllSessions: showAllSessions,
		NoBrowser:       opts.noBrowser,
		WebBind:         opts.webBind,
		WebBindExplicit: opts.webBindSet,
	}
}

func webBindForLaunch(ctx context.Context, st *store.Store, workdir string, startupOpts startupConfig) (string, bool) {
	bind := strings.TrimSpace(startupOpts.WebBind)
	if bind == "" {
		bind = defaultWebBind
	}
	if startupOpts.WebBindExplicit || bind != defaultWebBind {
		return bind, false
	}
	cached, err := loadWorkspaceWebBind(ctx, st, workdir)
	if err != nil || cached == "" {
		return bind, false
	}
	return cached, true
}

func loadWorkspaceWebBind(ctx context.Context, st *store.Store, workdir string) (string, error) {
	if st == nil {
		return "", nil
	}
	state, err := st.GetWorkspaceState(ctx, workdir)
	if err != nil {
		return "", err
	}
	if !isReusableWebBind(state.WebBind) {
		return "", nil
	}
	return strings.TrimSpace(state.WebBind), nil
}

func saveWorkspaceWebBind(ctx context.Context, st *store.Store, workdir, bind string) error {
	if !isReusableWebBind(bind) {
		return nil
	}
	if st == nil {
		return nil
	}
	return st.SetWorkspaceWebBind(ctx, workdir, bind)
}

func isReusableWebBind(bind string) bool {
	bind = strings.TrimSpace(bind)
	if bind == "" {
		return false
	}
	_, port, err := net.SplitHostPort(bind)
	return err == nil && port != "" && port != "0"
}

func debugAPIAddr(server *debugsrv.Server) string {
	if server == nil {
		return ""
	}
	return server.Addr()
}

func newResumeCommand() *cobra.Command {
	opts := startupOptions{}
	var showAllSessions bool
	cmd := &cobra.Command{
		Use:   "resume",
		Short: "Resume an existing session from a picker",
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts.captureFlagState(cmd)
			workdir, err := opts.resolve()
			if err != nil {
				return err
			}
			return runKoder(cmd.Context(), uicore.StartupModeResume, workdir, startupOptsFromFlags(opts, showAllSessions))
		},
	}
	bindStartupFlags(cmd, &opts)
	cmd.Flags().BoolVar(&showAllSessions, "all-sessions", false, "Show sessions from every working directory")
	return cmd
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
	var sessionID domain.ID
	cmd := &cobra.Command{
		Use:   "dump",
		Short: "Dump raw stored chat for a session as JSON",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if sessionID == "" {
				return fmt.Errorf("--id is required")
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

			chats, err := st.ListChats(cmd.Context(), sessionID)
			if err != nil {
				return err
			}
			type chatDump struct {
				Chat     domain.Chat           `json:"chat"`
				Timeline []domain.TimelineItem `json:"timeline"`
			}
			out := struct {
				SessionID domain.ID  `json:"session_id"`
				Chats     []chatDump `json:"chats"`
			}{
				SessionID: sessionID,
			}
			for _, chat := range chats {
				timeline, err := st.TimelineForChat(cmd.Context(), chat.ID)
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
	var sessionID domain.ID
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
	cmd.Flags().StringVar(&addr, "addr", "", "Resolved debug API address, for example 127.0.0.1:61347")
	cmd.Flags().DurationVar(&interval, "interval", time.Second, "Polling interval")
	return cmd
}
