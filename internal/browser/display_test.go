package browser

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/config"
)

// Exercise the actual Chrome/bubblewrap boundary with a service-like process
// environment. Desktop settings must come from the live user manager.
func TestHeadedDisplayIntegration(t *testing.T) {
	if os.Getenv("KODER_BROWSER_HEADED_TEST") == "" {
		t.Skip("set KODER_BROWSER_HEADED_TEST=1 on a logged-in desktop")
	}
	t.Setenv("DISPLAY", "")
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("XAUTHORITY", "")
	for _, mode := range []string{"service-environment", "inherited-x11"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "inherited-x11" {
				data, err := exec.CommandContext(t.Context(), "systemctl", "--user", "show-environment", "--output=json").Output()
				if err != nil {
					t.Fatal(err)
				}
				var env map[string]string
				if err := json.Unmarshal(data, &env); err != nil {
					t.Fatal(err)
				}
				env["WAYLAND_DISPLAY"] = ""
				if displayFromEnv(func(key string) string { return env[key] }).platform != "x11" {
					t.Skip("no local X11 session")
				}
				t.Setenv("DISPLAY", env["DISPLAY"])
				t.Setenv("XAUTHORITY", env["XAUTHORITY"])
			}
			m := NewManager(config.Browser{Enabled: true, Headed: true}, t.TempDir())
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if err := m.Stop(ctx); err != nil {
					t.Errorf("stop browser: %v", err)
				}
			})
			if err := m.Start(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestResolveDisplayAfterLogin(t *testing.T) {
	dir := t.TempDir()
	socket := filepath.Join(dir, "wayland-test")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	t.Setenv("DISPLAY", "")
	t.Setenv("WAYLAND_DISPLAY", "stale")
	t.Setenv("XDG_RUNTIME_DIR", dir)
	t.Setenv("PATH", dir)
	// A fake systemctl keeps the regression independent of the test desktop.
	if err := os.WriteFile(filepath.Join(dir, "systemctl"), []byte("#!/bin/sh\n[ \"$*\" = '--user show-environment --output=json' ] || exit 1\n/bin/cat \"$XDG_RUNTIME_DIR/session.json\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeSession := func(env map[string]string) {
		t.Helper()
		data, err := json.Marshal(env)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "session.json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeSession(map[string]string{})
	if _, err := resolveDisplay(t.Context(), true); err == nil || !strings.Contains(err.Error(), "log in") {
		t.Fatalf("missing desktop: %v", err)
	}
	if got, err := resolveDisplay(t.Context(), false); err != nil || got.platform != "" {
		t.Fatalf("headless requires desktop: %+v, %v", got, err)
	}
	writeSession(map[string]string{"WAYLAND_DISPLAY": "wayland-test", "XDG_RUNTIME_DIR": dir})
	got, err := resolveDisplay(t.Context(), true)
	if err != nil || got.platform != "wayland" || got.socket != socket {
		t.Fatalf("retry after login: %+v, %v", got, err)
	}
	if os.Getenv("WAYLAND_DISPLAY") != "stale" {
		t.Fatal("discovery modified process environment")
	}
	// A usable inherited display needs no working systemctl.
	t.Setenv("WAYLAND_DISPLAY", socket)
	writeSession(map[string]string{})
	if got, err := resolveDisplay(t.Context(), true); err != nil || got.socket != socket {
		t.Fatalf("inherited absolute socket: %+v, %v", got, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "not-a-socket"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WAYLAND_DISPLAY", "not-a-socket")
	if _, err := resolveDisplay(t.Context(), true); err == nil {
		t.Fatal("accepted a regular file as a display socket")
	}
}

func TestDisplaySandboxArgs(t *testing.T) {
	for _, tt := range []struct {
		name    string
		session displaySession
		want    []string
	}{
		{"headless", displaySession{}, nil},
		{"wayland", displaySession{platform: "wayland", socket: "/run/user/1000/wayland-1"}, []string{"--ro-bind /run/user/1000/wayland-1 /tmp/koder/run/wayland", "--setenv WAYLAND_DISPLAY wayland"}},
		{"x11", displaySession{platform: "x11", socket: "/tmp/.X11-unix/X1", display: ":1", authority: "/home/user/.Xauthority"}, []string{"--ro-bind /tmp/.X11-unix/X1 /tmp/.X11-unix/X1", "--setenv DISPLAY :1", "--ro-bind /home/user/.Xauthority /tmp/koder/Xauthority", "--setenv XAUTHORITY /tmp/koder/Xauthority"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			args := strings.Join(tt.session.sandboxArgs(), " ")
			for _, want := range append([]string{"--unsetenv DISPLAY", "--unsetenv WAYLAND_DISPLAY", "--unsetenv XAUTHORITY"}, tt.want...) {
				if !strings.Contains(args, want) {
					t.Errorf("missing %q in %s", want, args)
				}
			}
		})
	}
}
