package webui

import (
	"context"
	"os"
	"os/exec"
	"testing"

	"github.com/lkarlslund/koder/internal/app"
)

// chromiumForTest returns a Chromium executable for browser end-to-end tests,
// skipping the test outside CI when none is installed.
func chromiumForTest(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"chromium", "chromium-browser", "google-chrome", "google-chrome-stable"} {
		path, err := exec.LookPath(name)
		if err == nil {
			return path
		}
	}
	if os.Getenv("CI") != "" {
		t.Fatal("browser E2E requires Chromium in CI")
	}
	t.Skip("browser E2E requires Chromium")
	return ""
}

// startBrowserTestServer serves the web UI for a browser end-to-end test on a
// free local port.
func startBrowserTestServer(t *testing.T, ctx context.Context, ctrl *app.Controller) *Server {
	t.Helper()
	server, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatalf("start browser test server: %v", err)
	}
	return server
}
