package browser

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/lkarlslund/koder/internal/browserapi"
	"github.com/lkarlslund/koder/internal/config"
)

func TestBrowserIdleNoticesScheduleResetAndCleanup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := NewManager(config.Browser{}, t.TempDir())
		chat := browserapi.Chat{SessionID: "session", ChatID: "chat"}
		var noticeMu sync.Mutex
		var recorded []string
		notices := func() []string { noticeMu.Lock(); defer noticeMu.Unlock(); return append([]string(nil), recorded...) }
		m.SetIdleNotifier(func(owner browserapi.Chat, text string) {
			if owner != chat {
				t.Errorf("wrong owner: %#v", owner)
			}
			noticeMu.Lock()
			recorded = append(recorded, text)
			noticeMu.Unlock()
		})
		tab := &ownedTab{id: "tab_idle", owner: chat, requests: map[string]*requestState{}}
		m.tabs[tab.id] = tab
		other := &ownedTab{id: "tab_second", owner: browserapi.Chat{SessionID: "session", ChatID: "chat", ModelID: "older-model"}, requests: map[string]*requestState{}}
		m.tabs[other.id] = other
		m.tabs["sibling"] = &ownedTab{id: "sibling", owner: browserapi.Chat{SessionID: "session", ChatID: "other-chat"}}

		if _, err := m.ownedTab(chat, tab.id); err != nil {
			t.Fatal(err)
		}
		ages := []time.Duration{5 * time.Minute, 15 * time.Minute, 30 * time.Minute, time.Hour, 2 * time.Hour, 4 * time.Hour, 8 * time.Hour, 24 * time.Hour, 48 * time.Hour}
		time.Sleep(4 * time.Minute)
		tab.previewMu.Lock()
		_, previewErr := m.ScreenshotTab(t.Context(), chat, tab.id, "jpeg", 72)
		tab.previewMu.Unlock()
		if previewErr == nil {
			t.Fatal("expected busy preview")
		}
		previous := 4 * time.Minute
		for index, age := range ages {
			time.Sleep(age - previous - time.Nanosecond)
			synctest.Wait()
			if len(notices()) != index {
				t.Fatalf("early notices at %s: %d", age, len(notices()))
			}
			time.Sleep(time.Nanosecond)
			synctest.Wait()
			if len(notices()) != index+1 || !strings.Contains(notices()[index], "idle: "+age.String()) || !strings.Contains(notices()[index], tab.id) || !strings.Contains(notices()[index], "owned_tabs: 2") || strings.Contains(notices()[index], "tab_ids: sibling") {
				t.Fatalf("at %s: %#v", age, notices())
			}
			previous = age
		}
		if _, err := m.ownedTab(chat, tab.id); err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Minute)
		synctest.Wait()
		if len(notices()) != 10 || !strings.Contains(notices()[9], "idle: 5m0s") {
			t.Fatalf("activity did not reset schedule: %#v", notices())
		}
		m.mu.Lock()
		m.removeTabLocked(tab)
		m.removeTabLocked(other)
		m.mu.Unlock()
		time.Sleep(48 * time.Hour)
		synctest.Wait()
		if len(notices()) != 10 {
			t.Fatal("notification after last tab closed")
		}
	})
}

func TestBrowserIdleDefersBusyTabAndIgnoresStaleCallbacks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := NewManager(config.Browser{}, t.TempDir())
		chat := browserapi.Chat{SessionID: "session", ChatID: "chat"}
		var count atomic.Int32
		m.SetIdleNotifier(func(browserapi.Chat, string) { count.Add(1) })
		tab := &ownedTab{id: "tab_busy", owner: chat}
		m.tabs[tab.id] = tab
		_, _ = m.ownedTab(chat, tab.id)
		old := m.idleBrowsers[chat.ChatID]
		tab.mu.Lock()
		time.Sleep(5 * time.Minute)
		synctest.Wait()
		if count.Load() != 0 {
			t.Fatal("notice during active operation")
		}
		tab.mu.Unlock()
		_, _ = m.ownedTab(chat, tab.id)
		m.reportBrowserIdle(chat, old)
		time.Sleep(4 * time.Minute)
		synctest.Wait()
		if count.Load() != 0 {
			t.Fatal("stale timer ignored activity")
		}
		m.mu.Lock()
		m.clearBrowserIdleLocked()
		m.mu.Unlock()
		time.Sleep(48 * time.Hour)
		synctest.Wait()
		if count.Load() != 0 {
			t.Fatal("notice after shutdown")
		}
	})
}
