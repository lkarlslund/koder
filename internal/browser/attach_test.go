package browser

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/browserapi"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/id"
)

// The newest port file whose browser still listens wins; one left behind by
// a browser that exited is skipped.
func TestFindRunningChromeSkipsStalePortFiles(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	livePort := listener.Addr().(*net.TCPAddr).Port
	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	stalePort := closed.Addr().(*net.TCPAddr).Port
	_ = closed.Close()

	write := func(dir string, port int, browser string, age time.Duration) string {
		t.Helper()
		dir = filepath.Join(t.TempDir(), dir)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "DevToolsActivePort")
		if err := os.WriteFile(path, []byte(strings.Join([]string{strconv.Itoa(port), browser}, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		when := time.Now().Add(-age)
		_ = os.Chtimes(path, when, when)
		return dir
	}
	live := write("chrome", livePort, "/devtools/browser/live", time.Hour)
	stale := write("chromium", stalePort, "/devtools/browser/stale", 0)
	garbage := write("brave", livePort, "not a path", 0)

	got, err := findRunningChrome([]string{stale, garbage, live, filepath.Join(t.TempDir(), "missing")})
	if err != nil {
		t.Fatal(err)
	}
	if want := "ws://127.0.0.1:" + strconv.Itoa(livePort) + "/devtools/browser/live"; got != want {
		t.Fatalf("found %q, want %q", got, want)
	}
	if _, err := findRunningChrome([]string{stale}); err == nil || !strings.Contains(err.Error(), "chrome://inspect") {
		t.Fatalf("no running Chrome: err = %v, want a hint about chrome://inspect", err)
	}
	if _, err := attachURL("127.0.0.1:9222"); err == nil {
		t.Fatal("an address without a scheme was accepted")
	}
}

// Koder drives a Chrome the user runs: it opens and uses tabs of its own,
// never lists or attaches the user's tabs, and disconnecting closes only
// Koder's tabs, never Chrome.
func TestAttachToRunningChrome(t *testing.T) {
	if os.Getenv("KODER_BROWSER_TEST") == "" {
		t.Skip("set KODER_BROWSER_TEST=1 to run Chromium integration")
	}
	executable, err := detectExecutable("")
	if err != nil {
		t.Skip(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/mine":
			_, _ = w.Write([]byte(`<!doctype html><title>Users own tab</title><p>private</p>`))
		case "/popup":
			_, _ = w.Write([]byte(`<!doctype html><title>Koder popup</title>`))
		default:
			_, _ = w.Write([]byte(`<!doctype html><title>Koder page</title><p>agent content</p><button onclick="window.open('/popup','koder-popup','width=600,height=400')">Open</button>`))
		}
	}))
	defer server.Close()

	// The user's Chrome, with remote debugging on and a tab of their own.
	profile := t.TempDir()
	// Headless Chrome blocks this popup otherwise; Koder's own Chrome runs
	// with the same switch.
	chrome := exec.Command(executable, "--headless", "--disable-popup-blocking", "--no-sandbox", "--disable-dev-shm-usage", "--no-first-run", "--remote-debugging-port=0", "--user-data-dir="+profile, server.URL+"/mine")
	chrome.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := chrome.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-chrome.Process.Pid, syscall.SIGKILL)
		_, _ = chrome.Process.Wait()
	})
	var port string
	for deadline := time.Now().Add(15 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if p, _, err := readDevToolsActivePort(filepath.Join(profile, "DevToolsActivePort")); err == nil {
			port = p
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Chrome did not write DevToolsActivePort")
		}
	}
	type page struct{ ID, Type, Title, URL string }
	listPages := func() []page {
		t.Helper()
		resp, err := http.Get("http://127.0.0.1:" + port + "/json/list")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var targets []page
		if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
			t.Fatal(err)
		}
		return slices.DeleteFunc(targets, func(p page) bool { return p.Type != "page" })
	}
	pages := func() []string {
		t.Helper()
		var titles []string
		for _, p := range listPages() {
			titles = append(titles, p.Title)
		}
		slices.Sort(titles)
		return titles
	}
	waitPages := func(want ...string) []string {
		t.Helper()
		slices.Sort(want)
		got := pages()
		for deadline := time.Now().Add(3 * time.Second); !slices.Equal(got, want) && time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
			got = pages()
		}
		return got
	}
	for deadline := time.Now().Add(10 * time.Second); !strings.Contains(strings.Join(pages(), "|"), "Users own tab"); time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the user's tab did not load: %v", pages())
		}
	}

	saved := chromeProfileDirs
	chromeProfileDirs = func() []string { return []string{profile} }
	t.Cleanup(func() { chromeProfileDirs = saved })
	cfg := config.Default().Browser
	cfg.Enabled = true
	cfg.Attach = AttachAuto
	manager := NewManager(cfg, t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	chat := browserapi.Chat{SessionID: id.New(), ChatID: id.New()}

	tab, err := manager.NewTab(ctx, chat, server.URL+"/koder")
	if err != nil {
		t.Fatalf("open a tab in the user's Chrome: %v", err)
	}
	if tab.Title != "Koder page" {
		t.Fatalf("new tab = %+v", tab)
	}
	// The tab Koder connects through is closed once connected, so the user
	// sees only the tabs Koder works in.
	if got := waitPages("Users own tab", "Koder page"); !slices.Equal(got, []string{"Koder page", "Users own tab"}) {
		t.Errorf("after connecting, Chrome's tabs are %q, want the user's and Koder's page", got)
	}
	connection := manager.browserContext()
	if status := manager.Status(ctx, chat); !status.Attached || status.State != stateRunning || !strings.Contains(status.Version, "Chrome") {
		t.Fatalf("status = %+v, want attached and running with Chrome's version", status)
	}
	snapshot, err := manager.Snapshot(ctx, chat, "", 0, 4000)
	if err != nil || !strings.Contains(snapshot.Text, "agent content") {
		t.Fatalf("snapshot = %+v, %v", snapshot, err)
	}
	if err := manager.Interact(ctx, chat, "click", browserapi.Locator{Target: "Open", Role: "button", Exact: true}, ""); err != nil {
		t.Fatalf("open a popup: %v", err)
	}
	var titles []string
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		tabs, err := manager.Tabs(ctx, chat)
		if err != nil {
			t.Fatal(err)
		}
		titles = titles[:0]
		for _, item := range tabs {
			titles = append(titles, item.Title)
		}
		if len(tabs) == 2 || time.Now().After(deadline) {
			break
		}
	}
	if strings.Join(titles, "|") != "Koder page|Koder popup" && strings.Join(titles, "|") != "Koder popup|Koder page" {
		t.Errorf("the chat's tabs = %v, want its page and the popup it opened; Chrome has %v", titles, pages())
	}
	// Tabs not owned by any chat are offered to chats in Koder's own
	// browser; in the user's Chrome there are none.
	unowned, err := manager.Tabs(ctx, browserapi.Chat{})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range unowned {
		if strings.Contains(item.Title, "own tab") || strings.Contains(item.URL, "/mine") {
			t.Errorf("Koder lists the user's tab: %+v", item)
		}
	}

	// The user closes Koder's tabs by hand. Koder keeps its connection, so
	// Chrome does not ask to allow it again, and opens a new tab.
	for _, p := range listPages() {
		if strings.HasPrefix(p.Title, "Koder") {
			resp, err := http.Get("http://127.0.0.1:" + port + "/json/close/" + p.ID)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
		}
	}
	if got := waitPages("Users own tab"); !slices.Equal(got, []string{"Users own tab"}) {
		t.Fatalf("closing Koder's tabs by hand left %q", got)
	}
	if tab, err := manager.Navigate(ctx, chat, server.URL+"/again", "load"); err != nil || tab.Title != "Koder page" {
		t.Fatalf("navigate after the user closed Koder's tabs: %+v, %v", tab, err)
	}
	if manager.browserContext() != connection {
		t.Error("Koder reconnected after the user closed its tabs")
	}
	if status := manager.Status(ctx, chat); status.State != stateRunning || status.Error != "" {
		t.Errorf("status after the user closed Koder's tabs = %+v", status)
	}

	if err := manager.Stop(ctx); err != nil {
		t.Fatalf("disconnect: %v", err)
	}
	// Chrome and the user's tab stay; Koder's tabs close (pages fails if
	// Chrome has gone).
	if after := waitPages("Users own tab"); !slices.Equal(after, []string{"Users own tab"}) {
		t.Errorf("after disconnecting, Chrome's tabs are %q, want only the user's", after)
	}
}
