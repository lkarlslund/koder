package main

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/lkarlslund/koder/internal/agent"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/debugsrv"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/uicore"
	"github.com/lkarlslund/koder/internal/version"
)

func TestStartupOptionsResolveDefaultsToCurrentDirectory(t *testing.T) {
	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		if chdirErr := os.Chdir(originalWD); chdirErr != nil {
			t.Fatalf("restore cwd: %v", chdirErr)
		}
	})

	workdir := t.TempDir()
	if err := os.Chdir(workdir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	got, err := (startupOptions{}).resolve()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != workdir {
		t.Fatalf("expected %q, got %q", workdir, got)
	}
}

func TestStartupOptionsResolveRelativeCWD(t *testing.T) {
	base := t.TempDir()
	workdir := filepath.Join(base, "workspace")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		if chdirErr := os.Chdir(originalWD); chdirErr != nil {
			t.Fatalf("restore cwd: %v", chdirErr)
		}
	})
	if err := os.Chdir(base); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	got, err := (startupOptions{cwd: "workspace"}).resolve()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != workdir {
		t.Fatalf("expected %q, got %q", workdir, got)
	}
}

func TestStartupOptionsResolveProjectRootAlias(t *testing.T) {
	workdir := t.TempDir()

	got, err := (startupOptions{projectRoot: workdir}).resolve()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != workdir {
		t.Fatalf("expected %q, got %q", workdir, got)
	}
}

func TestStartupOptionsResolveRejectsConflictingAliases(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()

	_, err := (startupOptions{cwd: first, projectRoot: second}).resolve()
	if err == nil {
		t.Fatal("expected conflict error")
	}
}

func TestStartupOptionsResolveRejectsFilePath(t *testing.T) {
	base := t.TempDir()
	file := filepath.Join(base, "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	_, err := (startupOptions{cwd: file}).resolve()
	if err == nil {
		t.Fatal("expected directory error")
	}
}

func TestBindStartupFlagsRegistersAliases(t *testing.T) {
	var opts startupOptions
	cmd := &cobra.Command{}
	bindStartupFlags(cmd, &opts)

	cwdFlag := cmd.PersistentFlags().Lookup("cwd")
	if cwdFlag == nil {
		t.Fatal("expected cwd flag to be registered")
	}
	projectRootFlag := cmd.PersistentFlags().Lookup("project-root")
	if projectRootFlag == nil {
		t.Fatal("expected project-root flag to be registered")
	}
	if noBrowserFlag := cmd.PersistentFlags().Lookup("nobrowser"); noBrowserFlag == nil {
		t.Fatal("expected nobrowser flag to be registered")
	}
	if webBindFlag := cmd.PersistentFlags().Lookup("web-bind"); webBindFlag == nil {
		t.Fatal("expected web-bind flag to be registered")
	}
}

func TestStartupConfigFromFlags(t *testing.T) {
	got := startupOptsFromFlags(startupOptions{noBrowser: true, webBind: "127.0.0.1:12345", webBindSet: true}, true)
	if !got.ShowAllSessions || !got.NoBrowser || got.WebBind != "127.0.0.1:12345" || !got.WebBindExplicit {
		t.Fatalf("unexpected startup config: %#v", got)
	}
}

func TestStartupOptionsCapturesExplicitWebBind(t *testing.T) {
	var opts startupOptions
	cmd := &cobra.Command{
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts.captureFlagState(cmd)
			return nil
		},
	}
	bindStartupFlags(cmd, &opts)
	cmd.SetArgs([]string{"--web-bind=127.0.0.1:34567"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !opts.webBindSet {
		t.Fatal("expected explicit web bind to be captured")
	}
}

func TestWorkspaceWebBindRoundTrip(t *testing.T) {
	st := newRootTestStore(t)
	workdir := t.TempDir()

	if bind, cached := webBindForLaunch(context.Background(), st, workdir, startupConfig{WebBind: defaultWebBind}); bind != defaultWebBind || cached {
		t.Fatalf("expected default bind before cache, got bind=%q cached=%v", bind, cached)
	}
	if err := saveWorkspaceWebBind(context.Background(), st, workdir, "127.0.0.1:45678"); err != nil {
		t.Fatalf("save bind: %v", err)
	}
	bind, cached := webBindForLaunch(context.Background(), st, workdir, startupConfig{WebBind: defaultWebBind})
	if bind != "127.0.0.1:45678" || !cached {
		t.Fatalf("expected cached bind, got bind=%q cached=%v", bind, cached)
	}
	state, err := st.GetWorkspaceState(context.Background(), workdir)
	if err != nil {
		t.Fatalf("get workspace state: %v", err)
	}
	if state.ID == "" || state.WebBind != "127.0.0.1:45678" {
		t.Fatalf("expected bind in store workspace state, got %#v", state)
	}
}

func TestWorkspaceWebBindRespectsExplicitBind(t *testing.T) {
	st := newRootTestStore(t)
	workdir := t.TempDir()
	if err := saveWorkspaceWebBind(context.Background(), st, workdir, "127.0.0.1:45678"); err != nil {
		t.Fatalf("save bind: %v", err)
	}

	bind, cached := webBindForLaunch(context.Background(), st, workdir, startupConfig{WebBind: "127.0.0.1:0", WebBindExplicit: true})
	if bind != "127.0.0.1:0" || cached {
		t.Fatalf("expected explicit ephemeral bind, got bind=%q cached=%v", bind, cached)
	}
	bind, cached = webBindForLaunch(context.Background(), st, workdir, startupConfig{WebBind: "127.0.0.1:33333", WebBindExplicit: true})
	if bind != "127.0.0.1:33333" || cached {
		t.Fatalf("expected explicit fixed bind, got bind=%q cached=%v", bind, cached)
	}
}

func TestWorkspaceWebBindIgnoresEphemeralRecords(t *testing.T) {
	st := newRootTestStore(t)
	workdir := t.TempDir()
	if err := saveWorkspaceWebBind(context.Background(), st, workdir, "127.0.0.1:0"); err != nil {
		t.Fatalf("save bind: %v", err)
	}
	bind, cached := webBindForLaunch(context.Background(), st, workdir, startupConfig{WebBind: defaultWebBind})
	if bind != defaultWebBind || cached {
		t.Fatalf("expected default bind after ephemeral record, got bind=%q cached=%v", bind, cached)
	}
}

func TestStartWebUIRetriesCachedBind(t *testing.T) {
	ctrl := newRootTestController(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	bind := listener.Addr().String()
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = listener.Close()
	}()

	server, err := startWebUIWithRetry(ctx, ctrl, bind, true, true, 500*time.Millisecond, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("start web ui: %v", err)
	}
	if server.Addr() != bind {
		t.Fatalf("expected cached bind %q after retry, got %q", bind, server.Addr())
	}
}

func TestStartWebUIFallsBackWhenCachedBindStaysBusy(t *testing.T) {
	ctrl := newRootTestController(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	bind := listener.Addr().String()

	server, err := startWebUIWithRetry(ctx, ctrl, bind, true, true, 50*time.Millisecond, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("start web ui: %v", err)
	}
	if server.Addr() == bind {
		t.Fatalf("expected fallback bind while %q stays busy", bind)
	}
}

func newRootTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.OpenWithOptions(t.TempDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func newRootTestController(t *testing.T) *uicore.Controller {
	t.Helper()
	cfg := config.Default().WithStateDir(t.TempDir())
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "model"
	st, err := store.OpenWithOptions(cfg.StateDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	workdir := t.TempDir()
	engine := agent.New(cfg, st, nil, nil, workdir)
	ctrl := uicore.New(cfg, st, engine, workdir)
	if err := ctrl.Start(context.Background(), uicore.StartupModeNew); err != nil {
		t.Fatalf("start controller: %v", err)
	}
	t.Cleanup(func() { _ = ctrl.Shutdown(context.Background()) })
	return ctrl
}

func TestNewRootCommandRegistersSubcommands(t *testing.T) {
	cmd := NewRootCommand()
	want := []string{"doctor", "version", "resume", "session", "debug"}
	for _, name := range want {
		if child, _, err := cmd.Find([]string{name}); err != nil || child == nil || child.Name() != name {
			t.Fatalf("expected subcommand %q to be registered, child=%v err=%v", name, child, err)
		}
	}
}

func TestDebugInfoReportsUnsetEnv(t *testing.T) {
	t.Setenv(debugsrv.EnvDebugAPI, "")
	cmd := newDebugCommand()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"info"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute debug info: %v", err)
	}
	if got := buf.String(); !strings.Contains(got, debugsrv.EnvDebugAPI+" is not set") {
		t.Fatalf("expected unset message, got %q", got)
	}
}

func TestDebugInfoReportsConfiguredAddress(t *testing.T) {
	t.Setenv(debugsrv.EnvDebugAPI, "127.0.0.1:61347")
	cmd := newDebugCommand()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"info"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute debug info: %v", err)
	}
	if got := buf.String(); !strings.Contains(got, debugsrv.EnvDebugAPI+"=127.0.0.1:61347") {
		t.Fatalf("expected configured address, got %q", got)
	}
}

func TestDebugInfoReportsEphemeralAddressHint(t *testing.T) {
	t.Setenv(debugsrv.EnvDebugAPI, "127.0.0.1:0")
	cmd := newDebugCommand()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"info"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute debug info: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "resolved address is only known while koder is running") {
		t.Fatalf("expected ephemeral address hint, got %q", got)
	}
}

func TestVersionCommandPrintsBuildInfo(t *testing.T) {
	originalName := version.Name
	originalVersion := version.Version
	originalCommit := version.Commit
	originalDirty := version.Dirty
	originalBuildTime := version.BuildTime
	t.Cleanup(func() {
		version.Name = originalName
		version.Version = originalVersion
		version.Commit = originalCommit
		version.Dirty = originalDirty
		version.BuildTime = originalBuildTime
	})

	version.Name = "koder-test"
	version.Version = "1.2.3"
	version.Commit = " abc123 "
	version.Dirty = " false "
	version.BuildTime = " 2026-04-27T12:00:00Z "

	cmd := newVersionCommand()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute version: %v", err)
	}
	got := buf.String()
	for _, want := range []string{
		"koder-test 1.2.3",
		"commit: abc123",
		"dirty: false",
		"build_time: 2026-04-27T12:00:00Z",
		"go_version: ",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected output to contain %q, got %q", want, got)
		}
	}
}

func TestDoctorCommandRejectsMissingProviderConfig(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	cmd := newDoctorCommand()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected missing provider config error")
	}
	if !strings.Contains(err.Error(), "configure at least one provider") {
		t.Fatalf("expected provider config hint, got %v", err)
	}
}

func TestDoctorCommandRejectsMissingDefaultProviderEntry(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	configDir := filepath.Join(configRoot, "koder")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	configBody := strings.Join([]string{
		"default_provider = \"ghost\"",
		"",
		"[providers.other]",
		"name = \"Other\"",
		"base_url = \"http://127.0.0.1:1/v1\"",
		"default_model = \"test\"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(configBody), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cmd := newDoctorCommand()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected missing default provider error")
	}
	if !strings.Contains(err.Error(), `default provider "ghost" not configured`) {
		t.Fatalf("expected missing default provider error, got %v", err)
	}
}

func TestSessionDumpCommandRejectsMissingID(t *testing.T) {
	cmd := newSessionDumpCommand()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected missing id error")
	}
	if !strings.Contains(err.Error(), "--id is required") {
		t.Fatalf("expected id validation error, got %v", err)
	}
}
