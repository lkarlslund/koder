package browser

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	urlpkg "net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	cdpbrowser "github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/id"
)

// attachTimeout bounds the wait for the user to allow Koder's connection in
// Chrome's dialog.
const attachTimeout = 90 * time.Second

// AttachAuto is the attach setting that finds the user's running Chrome.
const AttachAuto = "auto"

// chromeProfileDirs lists the user data folders where Chrome-based browsers
// write DevToolsActivePort when remote debugging is on. Tests replace it.
var chromeProfileDirs = func() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	names := []string{"google-chrome", "google-chrome-beta", "google-chrome-unstable", "chromium", "BraveSoftware/Brave-Browser", "microsoft-edge"}
	base := filepath.Join(home, ".config")
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		base = dir
	}
	if runtime.GOOS == "darwin" {
		base = filepath.Join(home, "Library", "Application Support")
		names = []string{"Google/Chrome", "Google/Chrome Beta", "Google/Chrome Canary", "Chromium", "BraveSoftware/Brave-Browser", "Microsoft Edge"}
	}
	dirs := make([]string, 0, len(names))
	for _, name := range names {
		dirs = append(dirs, filepath.Join(base, filepath.FromSlash(name)))
	}
	return dirs
}

// attachURL turns the attach setting into a DevTools address.
func attachURL(setting string) (string, error) {
	setting = strings.TrimSpace(setting)
	if setting == "" || strings.EqualFold(setting, AttachAuto) {
		return findRunningChrome(chromeProfileDirs())
	}
	if !strings.HasPrefix(setting, "ws://") && !strings.HasPrefix(setting, "wss://") && !strings.HasPrefix(setting, "http://") && !strings.HasPrefix(setting, "https://") {
		return "", fmt.Errorf("browser attach address %q must start with http:// or ws://", setting)
	}
	return setting, nil
}

// findRunningChrome reads the DevToolsActivePort file of the most recently
// started browser that still listens on its port.
func findRunningChrome(dirs []string) (string, error) {
	type candidate struct {
		url     string
		started time.Time
	}
	var best *candidate
	for _, dir := range dirs {
		path := filepath.Join(dir, "DevToolsActivePort")
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		port, browserPath, err := readDevToolsActivePort(path)
		if err != nil {
			continue
		}
		conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", port), time.Second)
		if err != nil {
			continue // left behind by a browser that has exited
		}
		_ = conn.Close()
		if best == nil || info.ModTime().After(best.started) {
			best = &candidate{url: "ws://127.0.0.1:" + port + browserPath, started: info.ModTime()}
		}
	}
	if best == nil {
		return "", errors.New("no running Chrome with remote debugging found: open chrome://inspect/#remote-debugging in Chrome and allow remote debugging, or set the browser's attach address")
	}
	return best.url, nil
}

func readDevToolsActivePort(path string) (port, browserPath string, err error) {
	file, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	var lines []string
	for scanner.Scan() && len(lines) < 2 {
		lines = append(lines, strings.TrimSpace(scanner.Text()))
	}
	if len(lines) < 2 || lines[0] == "" || !strings.HasPrefix(lines[1], "/devtools/browser/") {
		return "", "", fmt.Errorf("%s is not a DevTools port file", path)
	}
	return lines[0], lines[1], nil
}

// attachAllocator connects to a browser the user runs. Unlike chromedp's
// RemoteAllocator, ending the connection never closes the browser: it only
// closes the websocket. It also makes the first chromedp context attach to a
// tab of Koder's own; otherwise chromedp attaches it to whichever of the
// user's tabs it sees first. startAttached closes that tab once connected.
//
// The tab is hidden, out of the tab strip, where Chrome allows it. Chrome
// refuses a hidden tab when it would need a new window, as in headless
// Chrome; Koder then uses an ordinary background tab.
type attachAllocator struct {
	url string
	wg  sync.WaitGroup
}

func (a *attachAllocator) Allocate(ctx context.Context, opts ...chromedp.BrowserOption) (*chromedp.Browser, error) {
	c := chromedp.FromContext(ctx)
	if c == nil {
		return nil, chromedp.ErrInvalidContext
	}
	wsURL, err := devToolsBrowserURL(ctx, a.url)
	if err != nil {
		return nil, err
	}
	connCtx, disconnect := context.WithCancel(context.Background())
	a.wg.Go(func() {
		select {
		case <-ctx.Done():
		case <-connCtx.Done():
		}
		disconnect()
	})
	browser, err := chromedp.NewBrowser(connCtx, wsURL, append(opts, chromedp.WithDialTimeout(attachTimeout))...)
	if err != nil {
		disconnect()
		return nil, err
	}
	executor := cdp.WithExecutor(ctx, browser)
	targetID, err := target.CreateTarget("about:blank").WithHidden(true).WithBackground(true).Do(executor)
	if err != nil {
		targetID, err = target.CreateTarget("about:blank").WithBackground(true).Do(executor)
	}
	if err != nil {
		disconnect()
		return nil, fmt.Errorf("open Koder's control tab: %w", err)
	}
	chromedp.WithTargetID(targetID)(c)
	return browser, nil
}

func (a *attachAllocator) Wait() { a.wg.Wait() }

// devToolsBrowserURL returns the browser's websocket address, asking an
// http:// address or a bare ws://host:port for it.
func devToolsBrowserURL(ctx context.Context, address string) (string, error) {
	if strings.Contains(address, "/devtools/browser/") {
		return address, nil
	}
	u, err := urlpkg.Parse(address)
	if err != nil {
		return "", fmt.Errorf("browser attach address: %w", err)
	}
	switch u.Scheme {
	case "ws":
		u.Scheme = "http"
	case "wss":
		u.Scheme = "https"
	}
	u.Path, u.RawQuery = "/json/version", ""
	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("ask %s for its DevTools address: %w", u.Host, err)
	}
	defer resp.Body.Close()
	var version struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&version); err != nil || version.WebSocketDebuggerURL == "" {
		return "", fmt.Errorf("%s did not report a DevTools address", u.Host)
	}
	return version.WebSocketDebuggerURL, nil
}

// newAttachedContext returns a browser context connected to the browser at
// url, whose own target is Koder's control tab. stop only disconnects, and
// may be called more than once.
func newAttachedContext(url string) (allocCtx context.Context, allocStop context.CancelFunc, browserCtx context.Context, stop context.CancelFunc) {
	allocCtx, allocStop = chromedp.NewRemoteAllocator(context.Background(), url)
	if c := chromedp.FromContext(allocCtx); c != nil {
		c.Allocator = &attachAllocator{url: url}
	}
	browserCtx, stop = chromedp.NewContext(allocCtx)
	return allocCtx, allocStop, browserCtx, sync.OnceFunc(stop)
}

// closeAttachedTabs closes Koder's tabs in the user's Chrome before Koder
// disconnects.
func closeAttachedTabs(ctx, browserCtx context.Context, tabs []target.ID) {
	closeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	executor, err := onBrowser(closeCtx, browserCtx)
	if err != nil {
		return
	}
	for _, id := range tabs {
		if err := target.CloseTarget(id).Do(executor); err != nil {
			slog.Warn("close Koder's tab in the user's Chrome", "target", id, "error", err)
		}
	}
}

// attachedExecutable names the browser in status while Koder drives the
// user's Chrome.
const attachedExecutable = "your running Chrome"

// startAttached connects to the user's running Chrome. Chrome may ask the
// user to allow the connection first; the wait for that is bounded.
func (m *Manager) startAttached(ctx context.Context, cfg config.Browser) error {
	url, err := attachURL(cfg.Attach)
	if err != nil {
		m.setError(err)
		return err
	}
	allocCtx, allocStop, browserCtx, stop := newAttachedContext(url)
	done := make(chan error, 1)
	go func() { done <- chromedp.Run(browserCtx) }()
	timer := time.NewTimer(attachTimeout)
	defer timer.Stop()
	select {
	case err = <-done:
	case <-ctx.Done():
		err = ctx.Err()
	case <-timer.C:
		err = errors.New("Chrome did not allow the connection in time; click Allow in Chrome's remote debugging dialog")
	}
	var executor context.Context
	if err == nil {
		executor, err = onBrowser(browserCtx, browserCtx)
	}
	if err != nil {
		stop()
		allocStop()
		err = fmt.Errorf("connect to your Chrome: %w", err)
		m.setError(err)
		return err
	}
	// Koder needed the control tab only to connect; everything else runs on
	// the browser's session or in tabs it opens. Closing it leaves nothing
	// for the user to close by mistake.
	if c := chromedp.FromContext(browserCtx); c.Target != nil {
		if err := target.CloseTarget(c.Target.TargetID).Do(executor); err != nil {
			slog.Warn("close Koder's control tab in the user's Chrome", "error", err)
		}
	}
	version := ""
	if _, product, _, _, _, err := cdpbrowser.GetVersion().Do(executor); err == nil {
		version = product
	}
	m.mu.Lock()
	m.allocCtx, m.allocStop = allocCtx, allocStop
	m.browserCtx, m.stop = browserCtx, stop
	m.executable, m.version = attachedExecutable, version
	m.attached = true
	m.state, m.lastErr = stateRunning, ""
	close(m.startWait)
	m.startWait = nil
	m.clearBrowserIdleLocked()
	m.tabs = map[string]*ownedTab{}
	m.selected = map[id.ID]string{}
	m.downloads = map[string]*downloadState{}
	m.mu.Unlock()
	go m.watchBrowser(browserCtx)
	if c := chromedp.FromContext(browserCtx); c != nil && c.Browser != nil {
		// Closing Chrome ends the connection; report it as stopped.
		go func() {
			select {
			case <-c.Browser.LostConnection:
				stop()
			case <-browserCtx.Done():
			}
		}()
	}
	m.tabsChanged()
	return nil
}
