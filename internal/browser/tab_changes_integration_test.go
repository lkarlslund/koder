package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/browserapi"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/id"
)

// A new tab is listed for its chat, and reported as a change, while its
// page still loads, whether or not Chrome was already running.
func TestNewTabIsListedWhileItsPageLoads(t *testing.T) {
	if os.Getenv("KODER_BROWSER_TEST") == "" {
		t.Skip("set KODER_BROWSER_TEST=1 to run Chromium integration")
	}
	const pageDelay = 2 * time.Second
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(pageDelay)
		_, _ = w.Write([]byte("<!doctype html><title>slow</title>slow"))
	}))
	defer server.Close()
	m := NewManager(config.Browser{Enabled: true, OperationTimeout: 15 * time.Second, MaxTabsPerChat: 4, MaxTabsGlobal: 8}, t.TempDir())
	defer func() { _ = m.Stop(context.Background()) }()
	var reports atomic.Int32
	m.OnTabsChange(func() { reports.Add(1) })
	chat := browserapi.Chat{SessionID: id.ID("session"), ChatID: id.ID("chat")}
	for _, phase := range []string{"cold", "warm"} {
		before := m.Status(context.Background(), chat).OwnedTabs
		reportsBefore := reports.Load()
		start := time.Now()
		done := make(chan error, 1)
		go func() {
			_, err := m.NewTab(context.Background(), chat, server.URL+"/"+phase)
			done <- err
		}()
		listed, reported := false, false
		deadline := time.After(pageDelay - 500*time.Millisecond)
	wait:
		for !(listed && reported) {
			select {
			case <-deadline:
				break wait
			case <-time.After(50 * time.Millisecond):
				listed = listed || m.Status(context.Background(), chat).OwnedTabs > before
				reported = reported || reports.Load() > reportsBefore
			}
		}
		if err := <-done; err != nil {
			t.Fatalf("%s: NewTab: %v", phase, err)
		}
		if !listed || !reported {
			t.Errorf("%s: while the page loaded, tab listed=%v, change reported=%v (NewTab took %v)", phase, listed, reported, time.Since(start))
		}
	}
}
