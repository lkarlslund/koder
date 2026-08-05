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
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"

	"github.com/lkarlslund/koder/internal/browserapi"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/id"
)

const (
	stateStopped  = "stopped"
	stateStarting = "starting"
	stateRunning  = "running"
	stateError    = "error"
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
}

type requestState struct {
	record browserapi.RequestRecord
	cdpID  network.RequestID
}

type downloadState struct {
	record browserapi.DownloadRecord
	owner  browserapi.Chat
	guid   string
	path   string
}

type refState struct {
	generation uint64
	tabID      string
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
	refs       map[id.ID]refState
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
		refs:       map[id.ID]refState{},
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
	m.refs = map[id.ID]refState{}
	m.downloads = map[string]*downloadState{}
	m.mu.Unlock()
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
	m.refs = map[id.ID]refState{}
	m.downloads = map[string]*downloadState{}
	m.state, m.lastErr = stateStopped, ""
	m.mu.Unlock()
	if stop != nil {
		stop()
	}
	if allocStop != nil {
		allocStop()
	}
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
	tab, tabCtx, err := m.ownedSelected(ctx, chat)
	if err != nil {
		return err
	}
	return chromedp.Run(tabCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		return target.ActivateTarget(tab.targetID).Do(ctx)
	}))
}

func (m *Manager) Tabs(ctx context.Context, chat browserapi.Chat) ([]browserapi.Tab, error) {
	if err := m.Start(ctx); err != nil {
		return nil, err
	}
	m.mu.Lock()
	browserCtx := m.browserCtx
	m.mu.Unlock()
	infos, err := target.GetTargets().Do(browserCtx)
	if err != nil {
		var listed []*target.Info
		err = chromedp.Run(browserCtx, chromedp.ActionFunc(func(ctx context.Context) error {
			var listErr error
			listed, listErr = target.GetTargets().Do(ctx)
			return listErr
		}))
		infos = listed
	}
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
	if err := m.Start(ctx); err != nil {
		return browserapi.Tab{}, err
	}
	m.mu.Lock()
	if err := m.checkTabCapsLocked(chat); err != nil {
		m.mu.Unlock()
		return browserapi.Tab{}, err
	}
	browserCtx := m.browserCtx
	m.mu.Unlock()
	url := strings.TrimSpace(rawURL)
	if url == "" {
		url = "about:blank"
	}
	createCtx, createCancel := context.WithTimeout(browserCtx, m.timeout())
	var targetID target.ID
	err := chromedp.Run(createCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		var createErr error
		targetID, createErr = target.CreateTarget(url).Do(ctx)
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
	return m.tabInfo(ctx, chat, tab)
}

func (m *Manager) ClaimTab(ctx context.Context, chat browserapi.Chat, tabID string) (browserapi.Tab, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tab := m.tabs[strings.TrimSpace(tabID)]
	if tab == nil {
		return browserapi.Tab{}, errors.New("browser tab not found")
	}
	if tab.owner.ChatID != "" {
		return browserapi.Tab{}, errors.New("browser tab has already been claimed")
	}
	if err := m.checkTabCapsLocked(chat); err != nil {
		return browserapi.Tab{}, err
	}
	tab.owner = chat
	m.selected[chat.ChatID] = tab.id
	return browserapi.Tab{ID: tab.id, Owned: true, Selected: true}, nil
}

func (m *Manager) SelectTab(ctx context.Context, chat browserapi.Chat, tabID string) (browserapi.Tab, error) {
	tab, err := m.ownedTab(chat, tabID)
	if err != nil {
		return browserapi.Tab{}, err
	}
	m.mu.Lock()
	m.selected[chat.ChatID] = tab.id
	m.mu.Unlock()
	return m.tabInfo(ctx, chat, tab)
}

func (m *Manager) CloseTab(ctx context.Context, chat browserapi.Chat, tabID string) error {
	tab, err := m.ownedTab(chat, tabID)
	if err != nil {
		return err
	}
	tab.mu.Lock()
	defer tab.mu.Unlock()
	closeCtx, cancel := context.WithTimeout(m.browserContext(), m.timeout())
	defer cancel()
	err = chromedp.Run(closeCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		return target.CloseTarget(tab.targetID).Do(ctx)
	}))
	if err != nil {
		return fmt.Errorf("close browser tab: %w", err)
	}
	tab.cancel()
	m.removeTab(tab)
	return nil
}

func (m *Manager) Navigate(ctx context.Context, chat browserapi.Chat, rawURL, wait string) (browserapi.Tab, error) {
	tab, tabCtx, err := m.ownedSelected(ctx, chat)
	if err != nil {
		return browserapi.Tab{}, err
	}
	tab.mu.Lock()
	defer tab.mu.Unlock()
	opCtx, cancel := m.operationContext(ctx, tabCtx)
	defer cancel()
	if err := chromedp.Run(opCtx, chromedp.Navigate(strings.TrimSpace(rawURL))); err != nil {
		return browserapi.Tab{}, fmt.Errorf("navigate browser: %w", err)
	}
	_ = wait
	return m.tabInfo(ctx, chat, tab)
}

func (m *Manager) History(ctx context.Context, chat browserapi.Chat, direction string) (browserapi.Tab, error) {
	tab, tabCtx, err := m.ownedSelected(ctx, chat)
	if err != nil {
		return browserapi.Tab{}, err
	}
	tab.mu.Lock()
	defer tab.mu.Unlock()
	var action chromedp.Action
	switch direction {
	case "back":
		action = chromedp.NavigateBack()
	case "forward":
		action = chromedp.NavigateForward()
	case "reload":
		action = chromedp.Reload()
	default:
		return browserapi.Tab{}, fmt.Errorf("unsupported history action %q", direction)
	}
	opCtx, cancel := m.operationContext(ctx, tabCtx)
	defer cancel()
	if err := chromedp.Run(opCtx, action); err != nil {
		return browserapi.Tab{}, err
	}
	return m.tabInfo(ctx, chat, tab)
}

func (m *Manager) Snapshot(ctx context.Context, chat browserapi.Chat, query string, depth, maxChars int) (browserapi.Snapshot, error) {
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
	m.mu.Lock()
	state := m.refs[chat.ChatID]
	state.generation++
	state.tabID = tab.id
	m.refs[chat.ChatID] = state
	m.mu.Unlock()
	tab.mu.Lock()
	defer tab.mu.Unlock()
	var text string
	script := snapshotScript(state.generation, query, depth)
	opCtx, cancel := m.operationContext(ctx, tabCtx)
	defer cancel()
	if err := chromedp.Run(opCtx, chromedp.Evaluate(script, &text)); err != nil {
		return browserapi.Snapshot{}, fmt.Errorf("capture browser snapshot: %w", err)
	}
	truncated := len(text) > maxChars
	if truncated {
		text = text[:maxChars] + "\n... snapshot truncated ..."
	}
	return browserapi.Snapshot{TabID: tab.id, Generation: state.generation, Text: text, Truncated: truncated}, nil
}

func (m *Manager) Find(ctx context.Context, chat browserapi.Chat, query, _ string, maxChars int) (browserapi.Snapshot, error) {
	return m.Snapshot(ctx, chat, query, 0, maxChars)
}

func (m *Manager) Interact(ctx context.Context, chat browserapi.Chat, action, ref, value string) error {
	tab, tabCtx, err := m.ownedSelected(ctx, chat)
	if err != nil {
		return err
	}
	m.mu.Lock()
	state := m.refs[chat.ChatID]
	m.mu.Unlock()
	prefix := fmt.Sprintf("%d-e", state.generation)
	if state.tabID != tab.id || !strings.HasPrefix(ref, prefix) {
		return errors.New("stale element reference; run browser_snapshot again")
	}
	selector := fmt.Sprintf(`[data-koder-ref=%q]`, ref)
	tab.mu.Lock()
	defer tab.mu.Unlock()
	var task chromedp.Action
	switch action {
	case "click":
		task = chromedp.Click(selector, chromedp.ByQuery)
	case "fill":
		task = chromedp.SetValue(selector, value, chromedp.ByQuery)
	case "type":
		task = chromedp.SendKeys(selector, value, chromedp.ByQuery)
	case "press":
		task = chromedp.SendKeys(selector, value, chromedp.ByQuery)
	case "select":
		task = chromedp.SetValue(selector, value, chromedp.ByQuery)
	case "check":
		task = chromedp.Evaluate(fmt.Sprintf(`(()=>{const e=document.querySelector(%s);if(!e)throw new Error('element missing');if(!e.checked)e.click()})()`, jsString(selector)), nil)
	case "uncheck":
		task = chromedp.Evaluate(fmt.Sprintf(`(()=>{const e=document.querySelector(%s);if(!e)throw new Error('element missing');if(e.checked)e.click()})()`, jsString(selector)), nil)
	case "hover":
		task = chromedp.Evaluate(fmt.Sprintf(`(()=>{const e=document.querySelector(%s);if(!e)throw new Error('element missing');e.dispatchEvent(new MouseEvent('mouseover',{bubbles:true}))})()`, jsString(selector)), nil)
	default:
		return fmt.Errorf("unsupported browser interaction %q", action)
	}
	opCtx, cancel := m.operationContext(ctx, tabCtx)
	defer cancel()
	if err := chromedp.Run(opCtx, chromedp.WaitVisible(selector, chromedp.ByQuery), task); err != nil {
		return fmt.Errorf("browser %s: %w", action, err)
	}
	return nil
}

func (m *Manager) Upload(ctx context.Context, chat browserapi.Chat, ref string, paths []string) error {
	_, tabCtx, err := m.ownedSelected(ctx, chat)
	if err != nil {
		return err
	}
	m.mu.Lock()
	state := m.refs[chat.ChatID]
	m.mu.Unlock()
	if !strings.HasPrefix(ref, fmt.Sprintf("%d-e", state.generation)) {
		return errors.New("stale element reference; run browser_snapshot again")
	}
	selector := fmt.Sprintf(`[data-koder-ref=%q]`, ref)
	stagingDir := filepath.Join(m.stateDir, "run", "uploads", newOpaqueID("upload"))
	if err := os.MkdirAll(stagingDir, 0o700); err != nil {
		return fmt.Errorf("create browser upload staging: %w", err)
	}
	defer os.RemoveAll(stagingDir)
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
	if err := chromedp.Run(opCtx, chromedp.SetUploadFiles(selector, browserPaths, chromedp.ByQuery)); err != nil {
		return fmt.Errorf("upload browser files: %w", err)
	}
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

func (m *Manager) Screenshot(ctx context.Context, chat browserapi.Chat, ref string, fullPage bool, format string, quality int) (browserapi.Binary, error) {
	_, tabCtx, err := m.ownedSelected(ctx, chat)
	if err != nil {
		return browserapi.Binary{}, err
	}
	var data []byte
	mime, name := "image/png", "browser-screenshot.png"
	if strings.EqualFold(format, "jpeg") {
		mime, name = "image/jpeg", "browser-screenshot.jpg"
	}
	if quality <= 0 || quality > 100 {
		quality = 90
	}
	if strings.TrimSpace(ref) != "" {
		m.mu.Lock()
		state := m.refs[chat.ChatID]
		m.mu.Unlock()
		if !strings.HasPrefix(ref, fmt.Sprintf("%d-e", state.generation)) {
			return browserapi.Binary{}, errors.New("stale element reference; run browser_snapshot again")
		}
	}
	var action chromedp.Action
	if strings.TrimSpace(ref) != "" {
		action = chromedp.Screenshot(fmt.Sprintf(`[data-koder-ref=%q]`, ref), &data, chromedp.ByQuery)
	} else if fullPage {
		action = chromedp.FullScreenshot(&data, quality)
	} else {
		action = chromedp.CaptureScreenshot(&data)
	}
	opCtx, cancel := m.operationContext(ctx, tabCtx)
	defer cancel()
	if err := chromedp.Run(opCtx, action); err != nil {
		return browserapi.Binary{}, fmt.Errorf("capture browser screenshot: %w", err)
	}
	if len(data) > 25*1024*1024 {
		return browserapi.Binary{}, errors.New("browser screenshot exceeds 25 MiB")
	}
	detected := http.DetectContentType(data)
	if detected == "image/png" {
		mime, name = "image/png", "browser-screenshot.png"
	} else if detected == "image/jpeg" {
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
	delete(m.downloads, download.record.ID)
	m.mu.Unlock()
	data, err := os.ReadFile(path)
	if err != nil {
		return browserapi.Binary{}, fmt.Errorf("read browser download: %w", err)
	}
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
		ctx, cancel := chromedp.NewContext(m.browserCtx, chromedp.WithTargetID(info.TargetID))
		tab := &ownedTab{id: newOpaqueID("tab"), targetID: info.TargetID, owner: owner, ctx: ctx, cancel: cancel, requests: map[string]*requestState{}}
		m.monitorTab(tab)
		m.tabs[tab.id] = tab
		if owner.ChatID != "" && m.selected[owner.ChatID] == "" {
			m.selected[owner.ChatID] = tab.id
		}
	}
	for id, tab := range m.tabs {
		if !seen[tab.targetID] {
			tab.cancel()
			delete(m.tabs, id)
			if m.selected[tab.owner.ChatID] == id {
				delete(m.selected, tab.owner.ChatID)
			}
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
	delete(m.tabs, tab.id)
	delete(m.refs, tab.owner.ChatID)
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
	return "", errors.New("Chrome or Chromium was not found; configure browser.executable")
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

func snapshotScript(generation uint64, query string, depth int) string {
	return fmt.Sprintf(`(()=>{const q=%s.toLowerCase();const maxDepth=%d;let n=0;const out=[];const walk=(el,d)=>{if(!el||d>(maxDepth||99))return;const s=getComputedStyle(el);if(s.display==='none'||s.visibility==='hidden')return;const role=el.getAttribute('role')||({A:'link',BUTTON:'button',INPUT:'input',TEXTAREA:'textbox',SELECT:'select',IMG:'image'}[el.tagName]||'');const name=(el.getAttribute('aria-label')||el.alt||el.innerText||el.value||'').trim().replace(/\s+/g,' ');const interactive=role||el.tabIndex>=0;if((!q||name.toLowerCase().includes(q))&&(name||interactive)){const ref='%d-e'+(++n);el.setAttribute('data-koder-ref',ref);out.push('  '.repeat(d)+(interactive?'['+ref+'] ':'')+(role||el.tagName.toLowerCase())+(name?' "'+name.slice(0,300)+'"':''));}for(const child of el.children)walk(child,d+1)};walk(document.body,0);return out.join('\n')})()`, jsString(strings.TrimSpace(query)), depth, generation)
}

func jsString(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}
