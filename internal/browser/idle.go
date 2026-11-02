package browser

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/browserapi"
	"github.com/lkarlslund/koder/internal/id"
)

type idleBrowser struct {
	since time.Time
	stage int
	timer *time.Timer
}

// SetIdleNotifier queues informational notices for chats retaining unused tabs.
func (m *Manager) SetIdleNotifier(notify func(browserapi.Chat, string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.idleNotifier = notify
}

func browserIdleAge(stage int) time.Duration {
	ages := [...]time.Duration{5 * time.Minute, 15 * time.Minute, 30 * time.Minute, time.Hour, 2 * time.Hour, 4 * time.Hour, 8 * time.Hour, 24 * time.Hour}
	if stage < len(ages) {
		return ages[stage]
	}
	return time.Duration(stage-len(ages)+2) * 24 * time.Hour
}

// Called with m.mu held. Replacing the entry invalidates callbacks already queued.
func (m *Manager) browserActivityLocked(chat browserapi.Chat) {
	if chat.ChatID == "" || m.idleNotifier == nil {
		return
	}
	if m.idleBrowsers == nil {
		m.idleBrowsers = make(map[id.ID]*idleBrowser)
	}
	if old := m.idleBrowsers[chat.ChatID]; old != nil {
		old.timer.Stop()
	}
	idle := &idleBrowser{since: time.Now()}
	m.idleBrowsers[chat.ChatID] = idle
	idle.timer = time.AfterFunc(browserIdleAge(0), func() { m.reportBrowserIdle(chat, idle) })
}

func (m *Manager) clearBrowserIdleLocked() {
	for _, idle := range m.idleBrowsers {
		idle.timer.Stop()
	}
	m.idleBrowsers = nil
}

func (m *Manager) reportBrowserIdle(chat browserapi.Chat, idle *idleBrowser) {
	m.mu.Lock()
	if m.idleBrowsers[chat.ChatID] != idle {
		m.mu.Unlock()
		return
	}
	var tabs []string
	busy := false
	for _, tab := range m.tabs {
		if tab.owner.ChatID != chat.ChatID || tab.owner.SessionID != chat.SessionID {
			continue
		}
		tabs = append(tabs, tab.id)
		if tab.mu.TryLock() {
			tab.mu.Unlock()
		} else {
			busy = true
		}
	}
	if len(tabs) == 0 || m.idleNotifier == nil {
		delete(m.idleBrowsers, chat.ChatID)
		m.mu.Unlock()
		return
	}
	if busy {
		idle.timer = time.AfterFunc(time.Minute, func() { m.reportBrowserIdle(chat, idle) })
		m.mu.Unlock()
		return
	}
	sort.Strings(tabs)
	count := len(tabs)
	if len(tabs) > 5 {
		tabs = tabs[:5]
	}
	text := fmt.Sprintf("event: browser idle (informational)\nidle: %s\nowned_tabs: %d\ntab_ids: %s\nThese tabs remain open in this chat. Close them if finished; keep them if still needed. No tabs were automatically closed. Passive UI previews do not count as agent activity.", time.Since(idle.since).Round(time.Second), count, strings.Join(tabs, ", "))
	notify := m.idleNotifier
	idle.stage++
	idle.timer = time.AfterFunc(max(time.Until(idle.since.Add(browserIdleAge(idle.stage))), time.Second), func() { m.reportBrowserIdle(chat, idle) })
	m.mu.Unlock()
	notify(chat, text)
}

func (m *Manager) stopIdleIfNoTabsLocked(chatID id.ID) {
	for _, tab := range m.tabs {
		if tab.owner.ChatID == chatID {
			return
		}
	}
	if idle := m.idleBrowsers[chatID]; idle != nil {
		idle.timer.Stop()
		delete(m.idleBrowsers, chatID)
	}
}
