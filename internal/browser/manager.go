package browser

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	mimepkg "mime"
	"net/http"
	urlpkg "net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	cdpbrowser "github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"

	"github.com/lkarlslund/koder/internal/browserapi"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/id"
)

const (
	stateStopped  = "stopped"
	stateStarting = "starting"
	stateRunning  = "running"
	stateError    = "error"
	maxBinarySize = 25 * 1024 * 1024
)

type ownedTab struct {
	id       string
	targetID target.ID
	owner    browserapi.Chat
	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.Mutex
	dataMu   sync.Mutex
	console  []browserapi.ConsoleRecord
	requests map[string]*requestState
	order    []string
	uploads  []string
}

type requestState struct {
	record browserapi.RequestRecord
	cdpID  network.RequestID
}

type downloadState struct {
	record browserapi.DownloadRecord
	owner  browserapi.Chat
	tabID  string
	guid   string
	path   string
}

type Manager struct {
	mu sync.Mutex

	cfg        config.Browser
	stateDir   string
	profileDir string
	state      string
	lastErr    string
	startWait  chan struct{}
	executable string
	version    string
	allocCtx   context.Context
	allocStop  context.CancelFunc
	browserCtx context.Context
	stop       context.CancelFunc
	tabs       map[string]*ownedTab
	selected   map[id.ID]string
	downloads  map[string]*downloadState
}

func NewManager(cfg config.Browser, stateDir string) *Manager {
	return &Manager{
		cfg:        cfg,
		stateDir:   filepath.Join(stateDir, "browser"),
		profileDir: filepath.Join(stateDir, "browser", "profile"),
		state:      stateStopped,
		tabs:       map[string]*ownedTab{},
		selected:   map[id.ID]string{},
		downloads:  map[string]*downloadState{},
	}
}

func (m *Manager) UpdateConfig(cfg config.Browser) {
	m.mu.Lock()
	m.cfg = cfg
	m.mu.Unlock()
}

func (m *Manager) Status(_ context.Context, chat browserapi.Chat) browserapi.Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	owned := 0
	for _, tab := range m.tabs {
		if chat.ChatID != "" && tab.owner.ChatID == chat.ChatID {
			owned++
		}
	}
	executable, version := m.executable, m.version
	if executable == "" {
		if detected, err := detectExecutable(m.cfg.Executable); err == nil {
			executable, version = detected, browserVersion(context.Background(), detected)
		}
	}
	return browserapi.Status{State: m.state, Executable: executable, Version: version, Error: m.lastErr, OwnedTabs: owned}
}

func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	if m.state == stateRunning {
		m.mu.Unlock()
		return nil
	}
	if m.state == stateStarting {
		wait := m.startWait
		m.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-wait:
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.state == stateRunning {
			return nil
		}
		return errors.New(m.lastErr)
	}
	if !m.cfg.Enabled {
		m.mu.Unlock()
		return errors.New("browser automation is disabled")
	}
	m.state = stateStarting
	m.startWait = make(chan struct{})
	m.lastErr = ""
	m.mu.Unlock()

	executable, err := detectExecutable(m.cfg.Executable)
	if err != nil {
		m.setError(err)
		return err
	}
	if _, err := exec.LookPath("bwrap"); err != nil {
		err = errors.New("browser sandbox requires bubblewrap (bwrap)")
		m.setError(err)
		return err
	}
	if err := os.MkdirAll(m.profileDir, 0o700); err != nil {
		err = fmt.Errorf("create browser profile: %w", err)
		m.setError(err)
		return err
	}
	runtimeDir := filepath.Join(m.stateDir, "run")
	if err := os.RemoveAll(runtimeDir); err != nil {
		err = fmt.Errorf("clear browser runtime: %w", err)
		m.setError(err)
		return err
	}
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		err = fmt.Errorf("create browser runtime: %w", err)
		m.setError(err)
		return err
	}

	base := context.Background()
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(executable),
		chromedp.UserDataDir("/tmp/koder/profile"),
		chromedp.Flag("headless", !m.cfg.Headed),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("no-default-browser-check", true),
		chromedp.Flag("disable-features", "Translate,PasswordManagerOnboarding,InfiniteSessionRestore"),
		chromedp.Flag("password-store", "basic"),
		chromedp.Flag("restore-last-session", false),
		chromedp.ModifyCmdFunc(func(cmd *exec.Cmd) { sandboxCommand(cmd, m.profileDir, runtimeDir) }),
	)
	allocCtx, allocStop := chromedp.NewExecAllocator(base, opts...)
	browserCtx, stop := chromedp.NewContext(allocCtx)
	err = chromedp.Run(browserCtx)
	if err != nil {
		stop()
		allocStop()
		err = fmt.Errorf("start Chrome: %w", err)
		m.setError(err)
		return err
	}

	version := browserVersion(ctx, executable)
	m.mu.Lock()
	m.allocCtx, m.allocStop = allocCtx, allocStop
	m.browserCtx, m.stop = browserCtx, stop
	m.executable, m.version = executable, version
	m.state, m.lastErr = stateRunning, ""
	close(m.startWait)
	m.startWait = nil
	m.tabs = map[string]*ownedTab{}
	m.selected = map[id.ID]string{}
	m.downloads = map[string]*downloadState{}
	m.mu.Unlock()
	go m.watchBrowser(browserCtx)
	if err := m.configureDownloads(browserCtx, runtimeDir); err != nil {
		_ = m.Stop(ctx)
		m.setError(err)
		return err
	}
	_, _ = m.Tabs(ctx, browserapi.Chat{})
	return nil
}

func (m *Manager) Stop(_ context.Context) error {
	m.mu.Lock()
	stop, allocStop := m.stop, m.allocStop
	m.stop, m.allocStop = nil, nil
	m.browserCtx, m.allocCtx = nil, nil
	m.tabs = map[string]*ownedTab{}
	m.selected = map[id.ID]string{}
	m.downloads = map[string]*downloadState{}
	m.state, m.lastErr = stateStopped, ""
	m.mu.Unlock()
	if stop != nil {
		stop()
	}
	if allocStop != nil {
		allocStop()
	}
	_ = os.RemoveAll(filepath.Join(m.stateDir, "run"))
	return nil
}

func (m *Manager) Restart(ctx context.Context) error {
	if err := m.Stop(ctx); err != nil {
		return err
	}
	return m.Start(ctx)
}

func (m *Manager) ResetProfile(ctx context.Context) error {
	if err := m.Stop(ctx); err != nil {
		return err
	}
	if err := os.RemoveAll(m.profileDir); err != nil {
		return fmt.Errorf("reset browser profile: %w", err)
	}
	return nil
}

func (m *Manager) Show(ctx context.Context, chat browserapi.Chat) error {
	tab, _, err := m.ownedSelected(ctx, chat)
	if err != nil {
		return err
	}
	return m.activateTab(ctx, tab)
}

func (m *Manager) Tabs(ctx context.Context, chat browserapi.Chat) ([]browserapi.Tab, error) {
	m.mu.Lock()
	if m.state != stateRunning || m.browserCtx == nil {
		m.mu.Unlock()
		return []browserapi.Tab{}, nil
	}
	browserCtx := m.browserCtx
	m.mu.Unlock()
	var infos []*target.Info
	err := chromedp.Run(browserCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		var listErr error
		infos, listErr = target.GetTargets().Do(ctx)
		return listErr
	}))
	if err != nil {
		return nil, fmt.Errorf("list browser tabs: %w", err)
	}
	m.syncTargets(infos)
	m.mu.Lock()
	defer m.mu.Unlock()
	selected := m.selected[chat.ChatID]
	result := make([]browserapi.Tab, 0, len(m.tabs))
	infoByID := make(map[target.ID]*target.Info, len(infos))
	for _, info := range infos {
		infoByID[info.TargetID] = info
	}
	for _, tab := range m.tabs {
		if tab.owner.ChatID != "" && tab.owner.ChatID != chat.ChatID {
			continue
		}
		info := infoByID[tab.targetID]
		if info == nil {
			continue
		}
		result = append(result, browserapi.Tab{ID: tab.id, Title: info.Title, URL: info.URL, Owned: tab.owner.ChatID == chat.ChatID && chat.ChatID != "", Unowned: tab.owner.ChatID == "", Selected: tab.id == selected})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func (m *Manager) NewTab(ctx context.Context, chat browserapi.Chat, rawURL string) (browserapi.Tab, error) {
	if chat.ChatID == "" {
		return browserapi.Tab{}, errors.New("chat is required")
	}
	m.mu.Lock()
	wasRunning := m.state == stateRunning
	m.mu.Unlock()
	if err := m.Start(ctx); err != nil {
		return browserapi.Tab{}, err
	}
	url := strings.TrimSpace(rawURL)
	if url == "" {
		url = "about:blank"
	}
	if !wasRunning {
		if url != "about:blank" {
			bootstrapCtx, cancel := m.operationContext(ctx, m.browserContext())
			err := chromedp.Run(bootstrapCtx, chromedp.Navigate(url), page.BringToFront())
			cancel()
			if err != nil {
				return browserapi.Tab{}, fmt.Errorf("navigate initial browser tab: %w", err)
			}
		}
		listed, err := m.Tabs(ctx, chat)
		if err != nil {
			return browserapi.Tab{}, err
		}
		preferredID := m.bootstrapTabID()
		sort.SliceStable(listed, func(i, j int) bool {
			return listed[i].ID == preferredID && listed[j].ID != preferredID
		})
		for _, wantedURL := range []string{url, "about:blank"} {
			for _, candidate := range listed {
				if !candidate.Unowned || !browserURLsEqual(candidate.URL, wantedURL) {
					continue
				}
				claimed, claimErr := m.ClaimTab(ctx, chat, candidate.ID)
				if claimErr != nil {
					continue
				}
				result := claimed
				if !browserURLsEqual(candidate.URL, url) {
					result, err = m.Navigate(ctx, chat, url, "load")
					if err != nil {
						return browserapi.Tab{}, err
					}
				}
				m.cleanupStartupTabs(ctx, chat, result.ID, result.URL)
				return result, nil
			}
		}
	}
	m.mu.Lock()
	if err := m.checkTabCapsLocked(chat); err != nil {
		m.mu.Unlock()
		return browserapi.Tab{}, err
	}
	browserCtx := m.browserCtx
	m.mu.Unlock()
	createCtx, createCancel := context.WithTimeout(browserCtx, m.timeout())
	var targetID target.ID
	err := chromedp.Run(createCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		var createErr error
		targetID, createErr = target.CreateTarget("about:blank").Do(ctx)
		return createErr
	}))
	createCancel()
	if err != nil {
		return browserapi.Tab{}, fmt.Errorf("open browser tab: %w", err)
	}
	tabCtx, cancel := chromedp.NewContext(browserCtx, chromedp.WithTargetID(targetID))
	err = chromedp.Run(tabCtx)
	if err != nil {
		cancel()
		return browserapi.Tab{}, fmt.Errorf("attach browser tab: %w", err)
	}
	tab := &ownedTab{id: newOpaqueID("tab"), targetID: targetID, owner: chat, ctx: tabCtx, cancel: cancel, requests: map[string]*requestState{}}
	m.monitorTab(tab)
	m.mu.Lock()
	m.tabs[tab.id] = tab
	m.selected[chat.ChatID] = tab.id
	m.mu.Unlock()
	if err := m.activateTab(ctx, tab); err != nil {
		return browserapi.Tab{}, fmt.Errorf("activate new browser tab: %w", err)
	}
	if url != "about:blank" {
		result, err := m.Navigate(ctx, chat, url, "load")
		if err != nil {
			_ = m.CloseTab(context.Background(), chat, tab.id)
			return browserapi.Tab{}, err
		}
		return result, nil
	}
	return m.tabInfo(ctx, chat, tab)
}

func (m *Manager) ClaimTab(ctx context.Context, chat browserapi.Chat, tabID string) (browserapi.Tab, error) {
	m.mu.Lock()
	tab := m.tabs[strings.TrimSpace(tabID)]
	if tab == nil {
		m.mu.Unlock()
		return browserapi.Tab{}, errors.New("browser tab not found")
	}
	if tab.owner.ChatID != "" {
		m.mu.Unlock()
		return browserapi.Tab{}, errors.New("browser tab has already been claimed")
	}
	if err := m.checkTabCapsLocked(chat); err != nil {
		m.mu.Unlock()
		return browserapi.Tab{}, err
	}
	tab.owner = chat
	tab.dataMu.Lock()
	tab.console = nil
	tab.requests = map[string]*requestState{}
	tab.order = nil
	tab.dataMu.Unlock()
	for downloadID, download := range m.downloads {
		if download.tabID == tab.id {
			delete(m.downloads, downloadID)
			_ = os.Remove(download.path)
		}
	}
	m.selected[chat.ChatID] = tab.id
	m.mu.Unlock()
	if tab.targetID == "" {
		return browserapi.Tab{ID: tab.id, Owned: true, Selected: true}, nil
	}
	if err := m.activateTab(ctx, tab); err != nil {
		return browserapi.Tab{}, fmt.Errorf("activate claimed browser tab: %w", err)
	}
	return m.tabInfo(ctx, chat, tab)
}

func (m *Manager) SelectTab(ctx context.Context, chat browserapi.Chat, tabID string) (browserapi.Tab, error) {
	tab, err := m.ownedTab(chat, tabID)
	if err != nil {
		return browserapi.Tab{}, err
	}
	m.mu.Lock()
	m.selected[chat.ChatID] = tab.id
	m.mu.Unlock()
	if err := m.activateTab(ctx, tab); err != nil {
		return browserapi.Tab{}, fmt.Errorf("activate selected browser tab: %w", err)
	}
	return m.tabInfo(ctx, chat, tab)
}

func (m *Manager) CloseTab(ctx context.Context, chat browserapi.Chat, tabID string) error {
	tab, err := m.ownedTab(chat, tabID)
	if err != nil {
		return err
	}
	tab.mu.Lock()
	defer tab.mu.Unlock()
	if err := m.closeTarget(ctx, tab.targetID); err != nil {
		return fmt.Errorf("close browser tab: %w", err)
	}
	tab.cancel()
	m.removeTab(tab)
	return nil
}

func (m *Manager) cleanupStartupTabs(ctx context.Context, chat browserapi.Chat, keepID, keepURL string) {
	deadline := time.Now().Add(time.Second)
	quietSince := time.Now()
	for {
		tabs, err := m.Tabs(ctx, chat)
		if err != nil {
			return
		}
		removed := false
		for _, candidate := range tabs {
			if candidate.ID == keepID || (candidate.URL != "about:blank" && candidate.URL != keepURL) {
				continue
			}
			m.discardTab(candidate.ID)
			removed = true
		}
		if removed {
			quietSince = time.Now()
		}
		if time.Since(quietSince) >= 200*time.Millisecond || time.Now().After(deadline) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func (m *Manager) discardTab(tabID string) {
	m.mu.Lock()
	tab := m.tabs[tabID]
	browserCtx := m.browserCtx
	m.mu.Unlock()
	if tab == nil || browserCtx == nil {
		return
	}
	if err := m.closeTarget(context.Background(), tab.targetID); err != nil {
		return
	}
	tab.cancel()
	m.removeTab(tab)
}

func (m *Manager) Navigate(ctx context.Context, chat browserapi.Chat, rawURL, wait string) (browserapi.Tab, error) {
	tab, tabCtx, err := m.ownedSelected(ctx, chat)
	if err != nil {
		return browserapi.Tab{}, err
	}
	tab.mu.Lock()
	defer tab.mu.Unlock()
	tab.dataMu.Lock()
	requestStart := len(tab.order)
	tab.dataMu.Unlock()
	opCtx, cancel := m.operationContext(ctx, tabCtx)
	defer cancel()
	var previous *page.NavigationEntry
	if err := chromedp.Run(opCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		current, entries, err := page.GetNavigationHistory().Do(ctx)
		if err == nil && current >= 0 && current < int64(len(entries)) {
			previous = entries[current]
		}
		return err
	})); err != nil {
		return browserapi.Tab{}, fmt.Errorf("inspect browser navigation history: %w", err)
	}
	if err := chromedp.Run(opCtx, chromedp.Navigate(strings.TrimSpace(rawURL))); err != nil {
		m.restoreNavigation(tabCtx, previous)
		return browserapi.Tab{}, fmt.Errorf("navigate browser: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(wait)) {
	case "", "load", "domcontentloaded":
	case "networkidle":
		if err := waitForNetworkIdle(opCtx, tab, requestStart, 500*time.Millisecond); err != nil {
			return browserapi.Tab{}, fmt.Errorf("wait for browser network idle: %w", err)
		}
	default:
		return browserapi.Tab{}, fmt.Errorf("unsupported browser wait condition %q", wait)
	}
	return m.tabInfo(ctx, chat, tab)
}

func (m *Manager) closeTarget(ctx context.Context, targetID target.ID) error {
	m.mu.Lock()
	browserCtx := m.browserCtx
	m.mu.Unlock()
	chromedpContext := chromedp.FromContext(browserCtx)
	if chromedpContext == nil || chromedpContext.Browser == nil {
		return errors.New("browser is not running")
	}
	closeCtx, cancel := context.WithTimeout(ctx, m.timeout())
	defer cancel()
	browserExecutor := cdp.WithExecutor(closeCtx, chromedpContext.Browser)
	return target.CloseTarget(targetID).Do(browserExecutor)
}

func (m *Manager) restoreNavigation(tabCtx context.Context, entry *page.NavigationEntry) {
	if entry == nil {
		return
	}
	restoreCtx, cancel := context.WithTimeout(tabCtx, 3*time.Second)
	defer cancel()
	_ = chromedp.Run(restoreCtx, chromedp.Navigate(entry.URL))
}

func (m *Manager) History(ctx context.Context, chat browserapi.Chat, direction string) (browserapi.Tab, error) {
	tab, tabCtx, err := m.ownedSelected(ctx, chat)
	if err != nil {
		return browserapi.Tab{}, err
	}
	tab.mu.Lock()
	defer tab.mu.Unlock()
	var entry *page.NavigationEntry
	opCtx, cancel := m.operationContext(ctx, tabCtx)
	defer cancel()
	if err := chromedp.Run(opCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		current, entries, err := page.GetNavigationHistory().Do(ctx)
		if err != nil {
			return err
		}
		switch direction {
		case "back":
			if current <= 0 || current >= int64(len(entries)) {
				return errors.New("no previous navigation entry")
			}
			entry = entries[current-1]
			return page.NavigateToHistoryEntry(entry.ID).Do(ctx)
		case "forward":
			if current < 0 || current >= int64(len(entries)-1) {
				return errors.New("no next navigation entry")
			}
			entry = entries[current+1]
			return page.NavigateToHistoryEntry(entry.ID).Do(ctx)
		case "reload":
			if current >= 0 && current < int64(len(entries)) {
				entry = entries[current]
			}
			return page.Reload().Do(ctx)
		default:
			return fmt.Errorf("unsupported history action %q", direction)
		}
	})); err != nil {
		return browserapi.Tab{}, fmt.Errorf("browser %s: %w", direction, err)
	}
	m.mu.Lock()
	selected := m.selected[chat.ChatID] == tab.id
	m.mu.Unlock()
	result := browserapi.Tab{ID: tab.id, Owned: true, Selected: selected}
	if entry != nil {
		result.Title = entry.Title
		result.URL = entry.URL
	}
	return result, nil
}

func (m *Manager) Snapshot(ctx context.Context, chat browserapi.Chat, query string, depth, maxChars int) (browserapi.Snapshot, error) {
	return m.snapshot(ctx, chat, query, "", depth, maxChars)
}

func (m *Manager) snapshot(ctx context.Context, chat browserapi.Chat, query, role string, depth, maxChars int) (browserapi.Snapshot, error) {
	tab, tabCtx, err := m.ownedSelected(ctx, chat)
	if err != nil {
		return browserapi.Snapshot{}, err
	}
	if maxChars <= 0 {
		maxChars = 32 * 1024
	}
	if maxChars > 128*1024 {
		maxChars = 128 * 1024
	}
	tab.mu.Lock()
	defer tab.mu.Unlock()
	var text string
	script := snapshotScript(query, role, depth)
	opCtx, cancel := m.operationContext(ctx, tabCtx)
	defer cancel()
	if err := chromedp.Run(opCtx, chromedp.Evaluate(script, &text)); err != nil {
		return browserapi.Snapshot{}, fmt.Errorf("capture browser snapshot: %w", err)
	}
	text, truncated := truncateSnapshot(text, maxChars)
	return browserapi.Snapshot{TabID: tab.id, Text: text, Truncated: truncated}, nil
}

func (m *Manager) Find(ctx context.Context, chat browserapi.Chat, query, role string, maxChars int) (browserapi.Snapshot, error) {
	return m.snapshot(ctx, chat, query, role, -1, maxChars)
}

func (m *Manager) Interact(ctx context.Context, chat browserapi.Chat, action string, locator browserapi.Locator, value string) error {
	tab, tabCtx, err := m.ownedSelected(ctx, chat)
	if err != nil {
		return err
	}
	tab.mu.Lock()
	defer tab.mu.Unlock()
	var task chromedp.Action
	switch action {
	case "click":
		task = chromedp.Evaluate(interactionExpression(locator, action, value), nil)
	case "fill":
		task = chromedp.Evaluate(interactionExpression(locator, action, value), nil)
	case "type":
		task = chromedp.Tasks{
			chromedp.Evaluate(interactionExpression(locator, "type", ""), nil),
			chromedp.KeyEvent(value),
		}
	case "press":
		key, modifiers := browserKeyChord(value)
		options := []chromedp.KeyOption(nil)
		if len(modifiers) > 0 {
			options = append(options, chromedp.KeyModifiers(modifiers...))
		}
		if locator.Empty() {
			task = chromedp.KeyEvent(key, options...)
		} else {
			task = chromedp.Tasks{
				chromedp.Evaluate(interactionExpression(locator, "press", ""), nil),
				chromedp.KeyEvent(key, options...),
			}
		}
	case "select", "check", "uncheck", "hover":
		task = chromedp.Evaluate(interactionExpression(locator, action, value), nil)
	default:
		return fmt.Errorf("unsupported browser interaction %q", action)
	}
	opCtx, cancel := m.operationContext(ctx, tabCtx)
	defer cancel()
	if err := chromedp.Run(opCtx, task); err != nil {
		return fmt.Errorf("browser %s: %w", action, err)
	}
	return nil
}

func (m *Manager) Drag(ctx context.Context, chat browserapi.Chat, source, target browserapi.Locator) error {
	_, tabCtx, err := m.ownedSelected(ctx, chat)
	if err != nil {
		return err
	}
	opCtx, cancel := m.operationContext(ctx, tabCtx)
	defer cancel()
	if err := chromedp.Run(opCtx, chromedp.Evaluate(dragExpression(source, target), nil)); err != nil {
		return fmt.Errorf("browser drag: %w", err)
	}
	return nil
}

func (m *Manager) Scroll(ctx context.Context, chat browserapi.Chat, locator browserapi.Locator, x, y int) error {
	_, tabCtx, err := m.ownedSelected(ctx, chat)
	if err != nil {
		return err
	}
	opCtx, cancel := m.operationContext(ctx, tabCtx)
	defer cancel()
	if err := chromedp.Run(opCtx, chromedp.Evaluate(scrollExpression(locator, x, y), nil)); err != nil {
		return fmt.Errorf("browser scroll: %w", err)
	}
	return nil
}

func (m *Manager) Upload(ctx context.Context, chat browserapi.Chat, locator browserapi.Locator, paths []string) error {
	tab, tabCtx, err := m.ownedSelected(ctx, chat)
	if err != nil {
		return err
	}
	stagingDir := filepath.Join(m.stateDir, "run", "uploads", newOpaqueID("upload"))
	if err := os.MkdirAll(stagingDir, 0o700); err != nil {
		return fmt.Errorf("create browser upload staging: %w", err)
	}
	keepStaging := false
	defer func() {
		if !keepStaging {
			_ = os.RemoveAll(stagingDir)
		}
	}()
	browserPaths := make([]string, 0, len(paths))
	for _, source := range paths {
		input, err := os.Open(source)
		if err != nil {
			return fmt.Errorf("open browser upload: %w", err)
		}
		name := newOpaqueID("file") + filepath.Ext(source)
		destination := filepath.Join(stagingDir, name)
		output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			_ = input.Close()
			return fmt.Errorf("create browser upload staging: %w", err)
		}
		_, err = io.Copy(output, input)
		closeErr := output.Close()
		_ = input.Close()
		if err != nil {
			return fmt.Errorf("stage browser upload: %w", err)
		}
		if closeErr != nil {
			return fmt.Errorf("close browser upload: %w", closeErr)
		}
		browserPaths = append(browserPaths, filepath.Join("/tmp/koder/run/uploads", filepath.Base(stagingDir), name))
	}
	opCtx, cancel := m.operationContext(ctx, tabCtx)
	defer cancel()
	if err := chromedp.Run(opCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		object, exception, err := cdpruntime.Evaluate(locatorExpression(locator, "upload")).Do(ctx)
		if err != nil {
			return err
		}
		if exception != nil {
			return exception
		}
		if object == nil || object.ObjectID == "" {
			return errors.New("browser upload target did not resolve to an element")
		}
		return dom.SetFileInputFiles(browserPaths).WithObjectID(object.ObjectID).Do(ctx)
	})); err != nil {
		return fmt.Errorf("upload browser files: %w", err)
	}
	tab.dataMu.Lock()
	tab.uploads = append(tab.uploads, stagingDir)
	tab.dataMu.Unlock()
	keepStaging = true
	return nil
}

func (m *Manager) Evaluate(ctx context.Context, chat browserapi.Chat, expression string) (string, error) {
	_, tabCtx, err := m.ownedSelected(ctx, chat)
	if err != nil {
		return "", err
	}
	var value any
	opCtx, cancel := m.operationContext(ctx, tabCtx)
	defer cancel()
	if err := chromedp.Run(opCtx, chromedp.Evaluate(expression, &value)); err != nil {
		return "", fmt.Errorf("evaluate browser JavaScript: %w", err)
	}
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	if len(data) > 128*1024 {
		data = append(data[:128*1024], []byte("\n... result truncated ...")...)
	}
	return string(data), nil
}

func (m *Manager) Screenshot(ctx context.Context, chat browserapi.Chat, locator browserapi.Locator, fullPage bool, format string, quality int) (browserapi.Binary, error) {
	tab, tabCtx, err := m.ownedSelected(ctx, chat)
	if err != nil {
		return browserapi.Binary{}, err
	}
	tab.mu.Lock()
	defer tab.mu.Unlock()
	var data []byte
	mime, name := "image/png", "browser-screenshot.png"
	if strings.EqualFold(format, "jpeg") {
		mime, name = "image/jpeg", "browser-screenshot.jpg"
	}
	if quality <= 0 || quality > 100 {
		quality = 90
	}
	params := page.CaptureScreenshot().WithFromSurface(true)
	if strings.EqualFold(format, "jpeg") {
		params = params.WithFormat(page.CaptureScreenshotFormatJpeg).WithQuality(int64(quality))
	} else {
		params = params.WithFormat(page.CaptureScreenshotFormatPng)
	}
	var action chromedp.Action
	if !locator.Empty() {
		var bounds page.Viewport
		action = chromedp.Tasks{
			chromedp.Evaluate(boundsExpression(locator), &bounds),
			chromedp.ActionFunc(func(ctx context.Context) error {
				if bounds.Width <= 0 || bounds.Height <= 0 {
					return errors.New("browser screenshot target has no drawable area")
				}
				var err error
				data, err = params.WithClip(&bounds).WithCaptureBeyondViewport(true).Do(ctx)
				return err
			}),
		}
	} else {
		action = chromedp.ActionFunc(func(ctx context.Context) error {
			var err error
			data, err = params.WithCaptureBeyondViewport(fullPage).Do(ctx)
			return err
		})
	}
	opCtx, cancel := m.operationContext(ctx, tabCtx)
	defer cancel()
	if err := chromedp.Run(opCtx, action); err != nil {
		return browserapi.Binary{}, fmt.Errorf("capture browser screenshot: %w", err)
	}
	if len(data) > maxBinarySize {
		return browserapi.Binary{}, errors.New("browser screenshot exceeds 25 MiB")
	}
	detected := http.DetectContentType(data)
	switch detected {
	case "image/png":
		mime, name = "image/png", "browser-screenshot.png"
	case "image/jpeg":
		mime, name = "image/jpeg", "browser-screenshot.jpg"
	}
	return browserapi.Binary{Name: name, MIME: mime, Data: data}, nil
}

func (m *Manager) PDF(ctx context.Context, chat browserapi.Chat) (browserapi.Binary, error) {
	_, tabCtx, err := m.ownedSelected(ctx, chat)
	if err != nil {
		return browserapi.Binary{}, err
	}
	opCtx, cancel := m.operationContext(ctx, tabCtx)
	defer cancel()
	var data []byte
	if err := chromedp.Run(opCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		var err error
		data, _, err = page.PrintToPDF().WithPrintBackground(true).Do(ctx)
		return err
	})); err != nil {
		return browserapi.Binary{}, fmt.Errorf("print browser PDF: %w", err)
	}
	if len(data) > maxBinarySize {
		return browserapi.Binary{}, errors.New("browser PDF exceeds 25 MiB")
	}
	return browserapi.Binary{Name: "browser-page.pdf", MIME: "application/pdf", Data: data}, nil
}

func (m *Manager) Console(_ context.Context, chat browserapi.Chat, level string, limit int) ([]browserapi.ConsoleRecord, error) {
	tab, err := m.ownedTab(chat, m.selectedTab(chat.ChatID))
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	tab.dataMu.Lock()
	defer tab.dataMu.Unlock()
	result := make([]browserapi.ConsoleRecord, 0, min(limit, len(tab.console)))
	for index := len(tab.console) - 1; index >= 0 && len(result) < limit; index-- {
		record := tab.console[index]
		if level == "" || strings.EqualFold(level, record.Level) {
			result = append(result, record)
		}
	}
	slices.Reverse(result)
	return result, nil
}

func (m *Manager) Requests(_ context.Context, chat browserapi.Chat, limit int) ([]browserapi.RequestRecord, error) {
	tab, err := m.ownedTab(chat, m.selectedTab(chat.ChatID))
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	tab.dataMu.Lock()
	defer tab.dataMu.Unlock()
	start := max(0, len(tab.order)-limit)
	result := make([]browserapi.RequestRecord, 0, len(tab.order)-start)
	for _, opaqueID := range tab.order[start:] {
		if request := tab.requests[opaqueID]; request != nil {
			result = append(result, request.record)
		}
	}
	return result, nil
}

func (m *Manager) ResponseBody(ctx context.Context, chat browserapi.Chat, requestID string) (browserapi.Binary, error) {
	tab, err := m.ownedTab(chat, m.selectedTab(chat.ChatID))
	if err != nil {
		return browserapi.Binary{}, err
	}
	tab.dataMu.Lock()
	request := tab.requests[strings.TrimSpace(requestID)]
	tab.dataMu.Unlock()
	if request == nil {
		return browserapi.Binary{}, errors.New("browser request not found")
	}
	var body []byte
	opCtx, cancel := m.operationContext(ctx, tab.ctx)
	defer cancel()
	if err := chromedp.Run(opCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		var bodyErr error
		body, bodyErr = network.GetResponseBody(request.cdpID).Do(ctx)
		return bodyErr
	})); err != nil {
		return browserapi.Binary{}, fmt.Errorf("read browser response: %w", err)
	}
	if len(body) > maxBinarySize {
		return browserapi.Binary{}, errors.New("browser response body exceeds 25 MiB")
	}
	mime := request.record.MIME
	if mime == "" {
		mime = "application/octet-stream"
	}
	return browserapi.Binary{Name: "browser-response", MIME: mime, Data: body}, nil
}

func (m *Manager) Downloads(_ context.Context, chat browserapi.Chat) ([]browserapi.DownloadRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]browserapi.DownloadRecord, 0)
	for _, download := range m.downloads {
		if chat.ChatID != "" && download.owner.ChatID == chat.ChatID {
			result = append(result, download.record)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func (m *Manager) Download(_ context.Context, chat browserapi.Chat, downloadID string) (browserapi.Binary, error) {
	m.mu.Lock()
	download := m.downloads[strings.TrimSpace(downloadID)]
	if download == nil || chat.ChatID == "" || download.owner.ChatID != chat.ChatID {
		m.mu.Unlock()
		return browserapi.Binary{}, errors.New("browser download not found or is owned by another chat")
	}
	if download.record.State != cdpbrowser.DownloadProgressStateCompleted.String() {
		m.mu.Unlock()
		return browserapi.Binary{}, fmt.Errorf("browser download is %s", download.record.State)
	}
	path, name := download.path, download.record.Name
	m.mu.Unlock()
	info, err := os.Stat(path)
	if err != nil {
		return browserapi.Binary{}, fmt.Errorf("inspect browser download: %w", err)
	}
	if info.Size() > maxBinarySize {
		return browserapi.Binary{}, errors.New("browser download exceeds 25 MiB")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return browserapi.Binary{}, fmt.Errorf("read browser download: %w", err)
	}
	m.mu.Lock()
	if m.downloads[download.record.ID] == download {
		delete(m.downloads, download.record.ID)
	}
	m.mu.Unlock()
	_ = os.Remove(path)
	mime := http.DetectContentType(data)
	if byExt := mimepkg.TypeByExtension(strings.ToLower(filepath.Ext(name))); byExt != "" && mime == "application/octet-stream" {
		mime = byExt
	}
	return browserapi.Binary{Name: name, MIME: mime, Data: data}, nil
}

func (m *Manager) CleanupChat(ctx context.Context, chatID id.ID) {
	m.cleanup(ctx, "", chatID)
}

func (m *Manager) CleanupSession(ctx context.Context, sessionID id.ID) {
	m.cleanup(ctx, sessionID, "")
}

func (m *Manager) cleanup(ctx context.Context, sessionID, chatID id.ID) {
	m.mu.Lock()
	ids := make([]string, 0)
	for id, tab := range m.tabs {
		if (chatID != "" && tab.owner.ChatID == chatID) || (sessionID != "" && tab.owner.SessionID == sessionID) {
			ids = append(ids, id)
		}
	}
	m.mu.Unlock()
	for _, tabID := range ids {
		m.mu.Lock()
		tab := m.tabs[tabID]
		m.mu.Unlock()
		if tab != nil {
			_ = m.CloseTab(ctx, tab.owner, tabID)
		}
	}
}

func (m *Manager) ownedSelected(ctx context.Context, chat browserapi.Chat) (*ownedTab, context.Context, error) {
	if _, err := m.Tabs(ctx, chat); err != nil {
		return nil, nil, err
	}
	m.mu.Lock()
	tabID := m.selected[chat.ChatID]
	m.mu.Unlock()
	if tabID == "" {
		return nil, nil, errors.New("chat has no selected browser tab; create, claim, or select a tab first")
	}
	tab, err := m.ownedTab(chat, tabID)
	if err != nil {
		return nil, nil, err
	}
	return tab, tab.ctx, nil
}

func (m *Manager) ownedTab(chat browserapi.Chat, tabID string) (*ownedTab, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tab := m.tabs[strings.TrimSpace(tabID)]
	if tab == nil || tab.owner.ChatID != chat.ChatID || chat.ChatID == "" {
		return nil, errors.New("browser tab not found or is owned by another chat")
	}
	return tab, nil
}

func (m *Manager) syncTargets(infos []*target.Info) {
	m.mu.Lock()
	defer m.mu.Unlock()
	seen := map[target.ID]bool{}
	for _, info := range infos {
		if info.Type != "page" {
			continue
		}
		seen[info.TargetID] = true
		if m.tabByTargetLocked(info.TargetID) != nil {
			continue
		}
		owner := browserapi.Chat{}
		if opener := m.tabByTargetLocked(info.OpenerID); opener != nil {
			owner = opener.owner
		}
		ctx := m.browserCtx
		cancel := func() {}
		chromedpContext := chromedp.FromContext(m.browserCtx)
		if chromedpContext == nil || chromedpContext.Target == nil || chromedpContext.Target.TargetID != info.TargetID {
			ctx, cancel = chromedp.NewContext(m.browserCtx, chromedp.WithTargetID(info.TargetID))
		}
		tab := &ownedTab{id: newOpaqueID("tab"), targetID: info.TargetID, owner: owner, ctx: ctx, cancel: cancel, requests: map[string]*requestState{}}
		m.monitorTab(tab)
		m.tabs[tab.id] = tab
		if owner.ChatID != "" && m.selected[owner.ChatID] == "" {
			m.selected[owner.ChatID] = tab.id
		}
	}
	for _, tab := range m.tabs {
		if !seen[tab.targetID] {
			tab.cancel()
			m.removeTabLocked(tab)
		}
	}
}

func (m *Manager) selectedTab(chatID id.ID) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.selected[chatID]
}

func (m *Manager) monitorTab(tab *ownedTab) {
	chromedp.ListenTarget(tab.ctx, func(event any) {
		switch event := event.(type) {
		case *cdpruntime.EventConsoleAPICalled:
			parts := make([]string, 0, len(event.Args))
			for _, arg := range event.Args {
				if arg.Value != nil {
					parts = append(parts, string(arg.Value))
				} else if arg.Description != "" {
					parts = append(parts, arg.Description)
				}
			}
			tab.dataMu.Lock()
			tab.console = appendBounded(tab.console, browserapi.ConsoleRecord{Level: event.Type.String(), Text: strings.Join(parts, " "), Time: time.Now().UTC()}, 500)
			tab.dataMu.Unlock()
		case *network.EventRequestWillBeSent:
			opaqueID := newOpaqueID("req")
			record := browserapi.RequestRecord{ID: opaqueID, Method: event.Request.Method, URL: event.Request.URL, Headers: redactHeaders(event.Request.Headers)}
			tab.dataMu.Lock()
			if len(tab.order) >= 500 {
				delete(tab.requests, tab.order[0])
				tab.order = tab.order[1:]
			}
			tab.requests[opaqueID] = &requestState{record: record, cdpID: event.RequestID}
			tab.order = append(tab.order, opaqueID)
			tab.dataMu.Unlock()
		case *network.EventResponseReceived:
			tab.dataMu.Lock()
			if request := tab.requestByCDPIDLocked(event.RequestID); request != nil {
				request.record.Status = int64(event.Response.Status)
				request.record.MIME = event.Response.MimeType
			}
			tab.dataMu.Unlock()
		case *network.EventLoadingFinished:
			tab.dataMu.Lock()
			if request := tab.requestByCDPIDLocked(event.RequestID); request != nil {
				request.record.Finished = true
			}
			tab.dataMu.Unlock()
		case *cdpbrowser.EventDownloadWillBegin:
			m.mu.Lock()
			id := newOpaqueID("download")
			m.downloads[id] = &downloadState{
				record: browserapi.DownloadRecord{ID: id, Name: event.SuggestedFilename, URL: event.URL, State: "inProgress"},
				owner:  tab.owner,
				tabID:  tab.id,
				guid:   event.GUID,
				path:   filepath.Join(m.stateDir, "run", "downloads", event.GUID),
			}
			m.mu.Unlock()
		case *cdpbrowser.EventDownloadProgress:
			m.mu.Lock()
			for _, download := range m.downloads {
				if download.guid == event.GUID && download.owner.ChatID == tab.owner.ChatID {
					download.record.State = event.State.String()
					download.record.Received = int64(event.ReceivedBytes)
					download.record.Total = int64(event.TotalBytes)
					if event.FilePath != "" {
						download.path = filepath.Join(m.stateDir, "run", "downloads", filepath.Base(event.FilePath))
					}
					break
				}
			}
			m.mu.Unlock()
		}
	})
	go func() {
		ctx, cancel := context.WithTimeout(tab.ctx, m.timeout())
		defer cancel()
		_ = chromedp.Run(ctx,
			network.Enable(),
			cdpruntime.Enable(),
			cdpbrowser.SetDownloadBehavior(cdpbrowser.SetDownloadBehaviorBehaviorAllowAndName).
				WithDownloadPath("/tmp/koder/run/downloads").
				WithEventsEnabled(true),
		)
	}()
}

func (m *Manager) configureDownloads(browserCtx context.Context, runtimeDir string) error {
	downloadDir := filepath.Join(runtimeDir, "downloads")
	if err := os.MkdirAll(downloadDir, 0o700); err != nil {
		return fmt.Errorf("create browser download staging: %w", err)
	}
	return chromedp.Run(browserCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		return cdpbrowser.SetDownloadBehavior(cdpbrowser.SetDownloadBehaviorBehaviorAllowAndName).
			WithDownloadPath("/tmp/koder/run/downloads").
			WithEventsEnabled(true).
			Do(ctx)
	}))
}

func (t *ownedTab) requestByCDPIDLocked(requestID network.RequestID) *requestState {
	for _, request := range t.requests {
		if request.cdpID == requestID {
			return request
		}
	}
	return nil
}

func waitForNetworkIdle(ctx context.Context, tab *ownedTab, requestStart int, quiet time.Duration) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	idleSince := time.Time{}
	for {
		tab.dataMu.Lock()
		pending := 0
		requestStart = min(requestStart, len(tab.order))
		for _, requestID := range tab.order[requestStart:] {
			if request := tab.requests[requestID]; request != nil && !request.record.Finished {
				pending++
			}
		}
		tab.dataMu.Unlock()
		if pending == 0 {
			if idleSince.IsZero() {
				idleSince = time.Now()
			} else if time.Since(idleSince) >= quiet {
				return nil
			}
		} else {
			idleSince = time.Time{}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func appendBounded[T any](values []T, value T, limit int) []T {
	values = append(values, value)
	if len(values) > limit {
		copy(values, values[len(values)-limit:])
		values = values[:limit]
	}
	return values
}

func redactHeaders(headers network.Headers) map[string]string {
	result := make(map[string]string, len(headers))
	for name, value := range headers {
		lower := strings.ToLower(name)
		if lower == "authorization" || lower == "cookie" || lower == "set-cookie" {
			result[name] = "[redacted]"
			continue
		}
		result[name] = fmt.Sprint(value)
	}
	return result
}

func (m *Manager) tabByTargetLocked(targetID target.ID) *ownedTab {
	for _, tab := range m.tabs {
		if tab.targetID == targetID {
			return tab
		}
	}
	return nil
}

func (m *Manager) bootstrapTabID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	chromedpContext := chromedp.FromContext(m.browserCtx)
	if chromedpContext == nil || chromedpContext.Target == nil {
		return ""
	}
	if tab := m.tabByTargetLocked(chromedpContext.Target.TargetID); tab != nil {
		return tab.id
	}
	return ""
}

func (m *Manager) tabInfo(ctx context.Context, chat browserapi.Chat, tab *ownedTab) (browserapi.Tab, error) {
	var title, rawURL string
	opCtx, cancel := m.operationContext(ctx, tab.ctx)
	defer cancel()
	if err := chromedp.Run(opCtx, chromedp.Title(&title), chromedp.Location(&rawURL)); err != nil {
		return browserapi.Tab{}, err
	}
	m.mu.Lock()
	selected := m.selected[chat.ChatID] == tab.id
	m.mu.Unlock()
	return browserapi.Tab{ID: tab.id, Title: title, URL: rawURL, Owned: true, Selected: selected}, nil
}

func (m *Manager) activateTab(ctx context.Context, tab *ownedTab) error {
	tab.mu.Lock()
	defer tab.mu.Unlock()
	opCtx, cancel := m.operationContext(ctx, tab.ctx)
	defer cancel()
	if err := chromedp.Run(opCtx, page.BringToFront()); err != nil {
		return fmt.Errorf("bring browser tab to front: %w", err)
	}
	return nil
}

func (m *Manager) checkTabCapsLocked(chat browserapi.Chat) error {
	owned, global := 0, 0
	for _, tab := range m.tabs {
		if tab.owner.ChatID != "" {
			global++
		}
		if tab.owner.ChatID == chat.ChatID {
			owned++
		}
	}
	if owned >= m.cfg.MaxTabsPerChat {
		return fmt.Errorf("chat browser tab limit reached (%d)", m.cfg.MaxTabsPerChat)
	}
	if global >= m.cfg.MaxTabsGlobal {
		return fmt.Errorf("global browser tab limit reached (%d)", m.cfg.MaxTabsGlobal)
	}
	return nil
}

func (m *Manager) removeTab(tab *ownedTab) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removeTabLocked(tab)
}

func (m *Manager) removeTabLocked(tab *ownedTab) {
	delete(m.tabs, tab.id)
	for downloadID, download := range m.downloads {
		if download.tabID == tab.id {
			delete(m.downloads, downloadID)
			_ = os.Remove(download.path)
		}
	}
	tab.dataMu.Lock()
	uploads := append([]string(nil), tab.uploads...)
	tab.uploads = nil
	tab.dataMu.Unlock()
	for _, uploadDir := range uploads {
		_ = os.RemoveAll(uploadDir)
	}
	if m.selected[tab.owner.ChatID] == tab.id {
		delete(m.selected, tab.owner.ChatID)
		for _, candidate := range m.tabs {
			if candidate.owner.ChatID == tab.owner.ChatID {
				m.selected[tab.owner.ChatID] = candidate.id
				break
			}
		}
	}
}

func (m *Manager) watchBrowser(browserCtx context.Context) {
	<-browserCtx.Done()
	m.mu.Lock()
	if m.browserCtx != browserCtx {
		m.mu.Unlock()
		return
	}
	allocStop := m.allocStop
	m.browserCtx, m.allocCtx = nil, nil
	m.stop, m.allocStop = nil, nil
	m.tabs = map[string]*ownedTab{}
	m.selected = map[id.ID]string{}
	m.downloads = map[string]*downloadState{}
	m.state = stateError
	m.lastErr = "chrome exited unexpectedly"
	m.mu.Unlock()
	if allocStop != nil {
		allocStop()
	}
	_ = os.RemoveAll(filepath.Join(m.stateDir, "run"))
}

func (m *Manager) browserContext() context.Context {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.browserCtx
}

func (m *Manager) timeout() time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cfg.OperationTimeout <= 0 {
		return 30 * time.Second
	}
	return min(m.cfg.OperationTimeout, 120*time.Second)
}

func (m *Manager) operationContext(parent, tabCtx context.Context) (context.Context, context.CancelFunc) {
	timeout := m.timeout()
	ctx, cancel := context.WithTimeout(tabCtx, timeout)
	if parent == nil {
		return ctx, cancel
	}
	merged, mergedCancel := context.WithCancel(ctx)
	go func() {
		select {
		case <-parent.Done():
			mergedCancel()
		case <-merged.Done():
		}
	}()
	return merged, func() { mergedCancel(); cancel() }
}

func (m *Manager) setError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state = stateError
	m.lastErr = err.Error()
	if m.startWait != nil {
		close(m.startWait)
		m.startWait = nil
	}
}

func detectExecutable(configured string) (string, error) {
	candidates := []string{strings.TrimSpace(configured), "google-chrome", "chromium", "chromium-browser", "/opt/google/chrome/chrome", "/usr/bin/google-chrome", "/usr/bin/chromium"}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		path, err := exec.LookPath(candidate)
		if err == nil {
			return path, nil
		}
	}
	return "", errors.New("browser executable was not found; configure browser.executable")
}

func browserVersion(ctx context.Context, executable string) string {
	cmdCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cmdCtx, executable, "--version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func sandboxCommand(cmd *exec.Cmd, profileDir, runtimeDir string) {
	chromePath := cmd.Path
	chromeArgs := append([]string(nil), cmd.Args[1:]...)
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		return
	}
	args := []string{"bwrap", "--die-with-parent", "--new-session", "--ro-bind", "/", "/", "--tmpfs", "/home", "--tmpfs", "/root", "--tmpfs", "/tmp", "--dir", "/tmp/koder", "--proc", "/proc", "--dev", "/dev", "--share-net", "--bind", profileDir, "/tmp/koder/profile", "--bind", runtimeDir, "/tmp/koder/run", "--setenv", "HOME", "/tmp/koder", "--setenv", "XDG_RUNTIME_DIR", "/tmp/koder/run", "--setenv", "TMPDIR", "/tmp/koder/run"}
	if _, err := os.Stat("/tmp/.X11-unix"); err == nil {
		args = append(args, "--ro-bind", "/tmp/.X11-unix", "/tmp/.X11-unix")
	}
	if waylandDisplay := strings.TrimSpace(os.Getenv("WAYLAND_DISPLAY")); waylandDisplay != "" {
		hostRuntime := strings.TrimSpace(os.Getenv("XDG_RUNTIME_DIR"))
		if hostRuntime != "" {
			socket := filepath.Join(hostRuntime, waylandDisplay)
			if _, err := os.Stat(socket); err == nil {
				args = append(args, "--ro-bind", socket, filepath.Join("/tmp/koder/run", waylandDisplay))
			}
		}
	}
	if _, err := os.Stat("/dev/dri"); err == nil {
		args = append(args, "--dev-bind-try", "/dev/dri", "/dev/dri")
	}
	args = append(args, "--", chromePath)
	args = append(args, chromeArgs...)
	cmd.Path = bwrap
	cmd.Args = args
}

func newOpaqueID(prefix string) string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return prefix + "_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return prefix + "_" + hex.EncodeToString(buf[:])
}

func snapshotScript(query, wantedRole string, depth int) string {
	if strings.EqualFold(strings.TrimSpace(wantedRole), "img") {
		wantedRole = "image"
	}
	return fmt.Sprintf(`(()=>{const q=%s.toLowerCase();const wantedRole=%s.toLowerCase();const maxDepth=%d;const out=[];const norm=value=>(value||'').replace(/\s+/g,' ').trim();const implicitRole=el=>{const tag=el.tagName;if(tag==='A'&&el.hasAttribute('href'))return 'link';if(tag==='BUTTON')return 'button';if(tag==='TEXTAREA')return 'textbox';if(tag==='SELECT')return el.multiple?'listbox':'combobox';if(tag==='IMG')return 'image';if(tag==='INPUT'){const type=(el.type||'text').toLowerCase();if(type==='checkbox')return 'checkbox';if(type==='radio')return 'radio';if(['button','submit','reset','image','file'].includes(type))return 'button';if(type==='range')return 'slider';return 'textbox'}if(/^H[1-6]$/.test(tag))return 'heading';return ''};const role=el=>norm(el.getAttribute('role')||implicitRole(el)).toLowerCase();const labelText=item=>{const clone=item.cloneNode(true);for(const control of clone.querySelectorAll('input,select,textarea,button'))control.remove();return clone.textContent||''};const label=el=>norm(el.labels&&el.labels.length?[...el.labels].map(labelText).join(' '):'');const labelledBy=el=>norm((el.getAttribute('aria-labelledby')||'').split(/\s+/).filter(Boolean).map(id=>document.getElementById(id)?.innerText||document.getElementById(id)?.textContent||'').join(' '));const name=el=>norm(labelledBy(el)||el.getAttribute('aria-label')||label(el)||el.alt||el.title||el.placeholder||el.innerText||el.value||el.textContent||'');const walk=(el,d)=>{if(!el||(maxDepth>=0&&d>maxDepth))return;const style=getComputedStyle(el);if(style.display==='none'||style.visibility==='hidden')return;const r=role(el);const n=name(el);const semantic=r||el.tabIndex>=0;if((!q||n.toLowerCase().includes(q))&&(!wantedRole||r===wantedRole)&&(n||semantic))out.push('  '.repeat(d)+(r||el.tagName.toLowerCase())+(n?' "'+n.slice(0,300)+'"':''));for(const child of el.children)walk(child,d+1)};walk(document.body,0);return out.join('\n')})()`, jsString(strings.TrimSpace(query)), jsString(strings.TrimSpace(wantedRole)), depth)
}

func truncateSnapshot(text string, maxChars int) (string, bool) {
	characters := []rune(text)
	if len(characters) <= maxChars {
		return text, false
	}
	const marker = "\n... snapshot truncated ..."
	markerCharacters := []rune(marker)
	if maxChars <= len(markerCharacters) {
		return string(characters[:maxChars]), true
	}
	return string(characters[:maxChars-len(markerCharacters)]) + marker, true
}

func jsString(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func browserKey(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "enter", "return":
		return kb.Enter
	case "tab":
		return kb.Tab
	case "escape", "esc":
		return kb.Escape
	case "backspace":
		return kb.Backspace
	case "delete", "del":
		return kb.Delete
	case "arrowup", "up":
		return kb.ArrowUp
	case "arrowdown", "down":
		return kb.ArrowDown
	case "arrowleft", "left":
		return kb.ArrowLeft
	case "arrowright", "right":
		return kb.ArrowRight
	case "home":
		return kb.Home
	case "end":
		return kb.End
	case "pageup":
		return kb.PageUp
	case "pagedown":
		return kb.PageDown
	case "space":
		return " "
	case "f1":
		return kb.F1
	case "f2":
		return kb.F2
	case "f3":
		return kb.F3
	case "f4":
		return kb.F4
	case "f5":
		return kb.F5
	case "f6":
		return kb.F6
	case "f7":
		return kb.F7
	case "f8":
		return kb.F8
	case "f9":
		return kb.F9
	case "f10":
		return kb.F10
	case "f11":
		return kb.F11
	case "f12":
		return kb.F12
	default:
		return value
	}
}

func browserKeyChord(value string) (string, []input.Modifier) {
	parts := strings.Split(value, "+")
	if len(parts) == 1 {
		return browserKey(value), nil
	}
	modifiers := make([]input.Modifier, 0, len(parts)-1)
	for _, part := range parts[:len(parts)-1] {
		switch strings.ToLower(strings.TrimSpace(part)) {
		case "alt", "option":
			modifiers = append(modifiers, input.ModifierAlt)
		case "control", "ctrl":
			modifiers = append(modifiers, input.ModifierCtrl)
		case "meta", "command", "cmd", "super":
			modifiers = append(modifiers, input.ModifierMeta)
		case "shift":
			modifiers = append(modifiers, input.ModifierShift)
		default:
			return browserKey(value), nil
		}
	}
	return browserKey(parts[len(parts)-1]), modifiers
}

func browserURLsEqual(left, right string) bool {
	if left == right {
		return true
	}
	leftURL, leftErr := urlpkg.Parse(left)
	rightURL, rightErr := urlpkg.Parse(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	if leftURL.Path == "" {
		leftURL.Path = "/"
	}
	if rightURL.Path == "" {
		rightURL.Path = "/"
	}
	return leftURL.String() == rightURL.String()
}
