package browser

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// displaySession contains only the desktop resources Chrome needs. Resolve it
// for each launch: a lingering user service can predate the graphical login.
type displaySession struct {
	platform  string
	socket    string
	display   string
	authority string
}

func resolveDisplay(ctx context.Context, headed bool) (displaySession, error) {
	if !headed {
		return displaySession{}, nil
	}
	if session := displayFromEnv(os.Getenv); session.platform != "" {
		return session, nil
	}
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	data, err := exec.CommandContext(probeCtx, "systemctl", "--user", "show-environment", "--output=json").Output()
	if err == nil {
		var env map[string]string
		if json.Unmarshal(data, &env) == nil {
			if session := displayFromEnv(func(key string) string { return env[key] }); session.platform != "" {
				return session, nil
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return displaySession{}, err
	}
	return displaySession{}, errors.New("headed browser requires an available graphical session; log in to the desktop and retry, or configure the browser to run headless")
}

func displayFromEnv(getenv func(string) string) displaySession {
	if name := getenv("WAYLAND_DISPLAY"); name != "" {
		socket := name
		if !filepath.IsAbs(socket) {
			socket = filepath.Join(getenv("XDG_RUNTIME_DIR"), name)
		}
		if filepath.IsAbs(socket) && isSocket(socket) {
			return displaySession{platform: "wayland", socket: socket}
		}
	}
	display := getenv("DISPLAY")
	// Local X11 displays have a filesystem socket. Do not guess a display
	// number or pick another user's socket when the environment is absent.
	if strings.HasPrefix(display, ":") || strings.HasPrefix(display, "unix:") {
		number := strings.Split(strings.TrimPrefix(strings.TrimPrefix(display, "unix"), ":"), ".")[0]
		if n, err := strconv.Atoi(number); err == nil && n >= 0 {
			socket := filepath.Join("/tmp/.X11-unix", "X"+number)
			if isSocket(socket) {
				authority := getenv("XAUTHORITY")
				if authority == "" {
					if home, err := os.UserHomeDir(); err == nil {
						candidate := filepath.Join(home, ".Xauthority")
						if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
							authority = candidate
						}
					}
				}
				return displaySession{platform: "x11", socket: socket, display: display, authority: authority}
			}
		}
	}
	return displaySession{}
}

func isSocket(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode()&os.ModeSocket != 0
}

func (s displaySession) sandboxArgs() []string {
	args := []string{"--unsetenv", "DISPLAY", "--unsetenv", "WAYLAND_DISPLAY", "--unsetenv", "XAUTHORITY"}
	switch s.platform {
	case "wayland":
		args = append(args, "--ro-bind", s.socket, "/tmp/koder/run/wayland", "--setenv", "WAYLAND_DISPLAY", "wayland")
	case "x11":
		args = append(args, "--ro-bind", s.socket, s.socket, "--setenv", "DISPLAY", s.display)
		if s.authority != "" {
			args = append(args, "--ro-bind", s.authority, "/tmp/koder/Xauthority", "--setenv", "XAUTHORITY", "/tmp/koder/Xauthority")
		}
	}
	return args
}
