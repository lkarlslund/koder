package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/charmbracelet/lipgloss"

	"github.com/lkarlslund/koder/internal/agent"
	"github.com/lkarlslund/koder/internal/attachment"
	kclipboard "github.com/lkarlslund/koder/internal/clipboard"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/debugsrv"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/markdown"
	"github.com/lkarlslund/koder/internal/permission"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/reference"
	"github.com/lkarlslund/koder/internal/sessionctx"
	"github.com/lkarlslund/koder/internal/skills"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/theme"
	"github.com/lkarlslund/koder/internal/tools"
	"github.com/lkarlslund/koder/internal/tui/dialogs"
	"github.com/lkarlslund/koder/internal/ui"
	"github.com/lkarlslund/koder/internal/ui/textarea"
	"github.com/lkarlslund/koder/internal/workspace"
)

type promptDoneMsg struct {
	events <-chan domain.Event
	err    error
}

type spinnerTickMsg struct{}
type rootTimerMsg struct {
	At  time.Time
	Seq uint64
}

type busyScope int

const (
	busyScopeNone busyScope = iota
	busyScopeSidebar
	busyScopeTranscript
	composerHeight          = 3
	composerInputHeight     = 1
	composerBlinkTimerOwner = "composer"
)

type spinnerModel struct {
	active bool
	frame  int
}

func (s *spinnerModel) start() {
	s.active = true
}

func (s *spinnerModel) stop() {
	s.active = false
	s.frame = 0
}

func (s *spinnerModel) tick() {
	if !s.active {
		return
	}
	s.frame++
}

type busyModel struct {
	active  bool
	scope   busyScope
	status  string
	spinner spinnerModel
}

func (b *busyModel) start(scope busyScope, status string) {
	b.active = true
	b.scope = scope
	b.status = status
	if scope == busyScopeNone {
		b.spinner.stop()
		return
	}
	b.spinner.start()
}

func (b *busyModel) updateStatus(status string) {
	b.status = status
}

func (b *busyModel) stop() {
	b.active = false
	b.scope = busyScopeNone
	b.status = ""
	b.spinner.stop()
}

func (b busyModel) transcriptActive() bool {
	return b.active && b.scope == busyScopeTranscript && b.spinner.active
}

func (b busyModel) sidebarActive() bool {
	return b.active && b.scope != busyScopeNone
}

type eventMsg struct {
	event  domain.Event
	events <-chan domain.Event
}

type slashCommand struct {
	Name         string
	Description  string
	NeedsArgs    bool
	Autocomplete string
}

type StartupMode int

const (
	StartupModeNew StartupMode = iota
	StartupModeResume
)

type StartupOptions struct {
	ShowAllSessions bool
}

type newSessionMsg struct {
	session   domain.Session
	sessions  []domain.Session
	messages  []domain.Message
	parts     map[int64][]domain.Part
	approvals []store.Approval
	tasks     []store.Task
	workspace workspace.Status
}

type sessionPickerMsg struct {
	sessions []domain.Session
}

type pickerMode int

const (
	pickerModeNone pickerMode = iota
	pickerModeTheme
	pickerModePermissions
	pickerModeSkills
)

type pickerModel struct {
	visible      bool
	mode         pickerMode
	initialValue string
	approvalID   int64
	dialog       ui.PickerDialog
}

type runPromptMsg struct {
	session        domain.Session
	events         <-chan domain.Event
	err            error
	providerID     string
	contextWindow  int
	contextChecked bool
}

type kickoffPromptMsg struct {
	Prompt      string
	Attachments []attachment.Draft
	References  []reference.Draft
}

type queuedPromptMode int

const (
	queuedPromptModeNormal queuedPromptMode = iota
	queuedPromptModeSteer
	queuedPromptModeContinue
)

type queuedPrompt struct {
	Text        string
	Mode        queuedPromptMode
	Attachments []attachment.Draft
	References  []reference.Draft
}

func (q queuedPrompt) modeLabel() string {
	switch q.Mode {
	case queuedPromptModeSteer:
		return "steering"
	case queuedPromptModeContinue:
		return "continue"
	default:
		return "after idle"
	}
}

func (q queuedPrompt) statusText() string {
	switch q.Mode {
	case queuedPromptModeSteer:
		return "Queued steering for after the current run"
	case queuedPromptModeContinue:
		return "Queued continue for when koder is idle"
	default:
		return "Queued prompt for when koder is idle"
	}
}

func (q queuedPrompt) runStatus() string {
	switch q.Mode {
	case queuedPromptModeSteer:
		return "Applying steering…"
	case queuedPromptModeContinue:
		return "Continuing…"
	default:
		return "Running queued prompt…"
	}
}

type providerProbeMsg struct {
	result provider.ProbeResult
	err    error
}

type modelListMsg struct {
	providerID  string
	models      []domain.Model
	postConnect bool
	err         error
}

type forkSessionMsg struct {
	load     loadMsg
	sourceID int64
	forkedID int64
	err      error
}

type agentsRefreshMsg struct {
	load loadMsg
	err  error
}

type llmPreviewMsg struct {
	title string
	body  string
	err   error
}

type modelRenderCache struct {
	renderedBodySurface         ui.Surface
	bodyValid                   bool
	renderedComposerAreaSurface ui.Surface
	composerAreaHeight          int
	composerAreaValid           bool
}

type Model struct {
	cfg                config.Config
	store              *store.Store
	agent              *agent.Engine
	renderer           *markdown.Renderer
	palette            theme.Palette
	sessions           []domain.Session
	currentSession     domain.Session
	messages           []domain.Message
	parts              map[int64][]domain.Part
	tasks              []store.Task
	approvals          []store.Approval
	viewport           transcriptViewport
	transcriptControls []ui.Control
	transcriptCache    map[string]cachedTranscriptBlock
	retainedTranscript *ui.RetainedTranscript
	transcriptDirty    bool
	mainScreen         *mainScreenWidget
	renderCache        *modelRenderCache
	expandedToolRuns   map[string]bool
	composer           textarea.Model
	composerQueries    composerQueryState
	composerHistory    composerHistoryState
	width              int
	height             int
	status             string
	loading            bool
	busy               busyModel
	showSidebar        bool
	showReasoning      bool
	showSystem         bool
	slashMatches       []slashCommand
	slashIndex         int
	skillMatches       []skills.Skill
	skillIndex         int
	mentionMatches     []reference.Entry
	mentionIndex       int
	mentionCatalog     []reference.Entry
	approvalButtons    ui.ButtonRow
	workdir            string
	workspace          workspace.Status
	agentsDrift        bool
	startupMode        StartupMode
	startupOptions     StartupOptions
	picker             pickerModel
	pendingPartID      int64
	mouseEnabled       bool
	sessionDialog      *dialogs.SessionDialog
	preferences        *dialogs.PreferencesDialog
	agentsModal        *ui.Modal
	helpModal          *ui.Modal
	llmPreviewTitle    string
	llmPreviewBody     string
	llmPreviewYOffset  int
	llmPreviewWidth    int
	llmPreviewHeight   int
	connectDialog      *dialogs.ConnectDialog
	disconnectDialog   *dialogs.DisconnectDialog
	modelDialog        *dialogs.ModelDialog
	toolsDialog        *dialogs.ToolsDialog
	mainWindowView     *modelWindow
	debug              *debugsrv.Recorder
	uiRoot             *ui.Root
	uiRuntime          ui.Runtime
	rootTimerSeq       uint64
	rootTimerPending   bool
	rootTimerPendingAt time.Time
	caps               *provider.CapabilityStore
	runtimeCtxChecked  map[string]bool
	activeOpCancel     context.CancelFunc
	queuedPrompt       *queuedPrompt
	pendingModelNote   string
	draftAttachments   []attachment.Draft
	draftReferences    []reference.Draft
	attachmentFiles    *attachment.Manager
	interruptArmedAt   time.Time
	readClipboardText  func() (string, error)
	readClipboardImage func() ([]byte, error)
	writeClipboardText func(string) error
}

type composerHistoryState struct {
	Index        int
	Active       bool
	Draft        string
	SearchIndex  int
	SearchActive bool
	SearchQuery  string
}

type composerQueryState struct {
	revision        uint64
	slashQuery      string
	hasSlashQuery   bool
	skillQuery      string
	skillStart      int
	hasSkillQuery   bool
	mentionQuery    string
	mentionStart    int
	mentionEnd      int
	mentionPathMode bool
	hasMentionQuery bool
}

type cachedTranscriptBlock struct {
	element   ui.Element
	lineCount int
	controlID string
}

func New(cfg config.Config, st *store.Store, a *agent.Engine, mode StartupMode, debug *debugsrv.Recorder) (Model, error) {
	workdir, err := os.Getwd()
	if err != nil {
		return Model{}, err
	}
	return NewWithWorkdir(cfg, st, a, mode, debug, workdir, StartupOptions{})
}

func NewWithWorkdir(cfg config.Config, st *store.Store, a *agent.Engine, mode StartupMode, debug *debugsrv.Recorder, workdir string, startupOpts StartupOptions) (Model, error) {
	tuiTheme := theme.Resolve(cfg.UI.Theme)
	renderer, err := markdown.New(tuiTheme.Palette)
	if err != nil {
		return Model{}, err
	}
	composer := textarea.New()
	composer.Placeholder = "Ask koder or type / for commands"
	composer.SetWidth(40)
	composer.SetHeight(composerInputHeight)
	composer.Prompt = mPrompt(cfg)
	composer.Focus()
	composer.BlinkEnabled = cfg.UI.CursorBlink
	composer.ShowLineNumbers = false
	applyComposerTheme(&composer, tuiTheme.Palette)

	vp := newTranscriptViewport(40, 10)
	vp.SetContent("Loading…")

	return Model{
		cfg:               cfg,
		store:             st,
		agent:             a,
		renderer:          renderer,
		palette:           tuiTheme.Palette,
		viewport:          vp,
		renderCache:       &modelRenderCache{},
		composer:          composer,
		showSidebar:       cfg.UI.ShowSidebar,
		showReasoning:     cfg.UI.ShowReasoning,
		showSystem:        cfg.UI.ShowSystem,
		expandedToolRuns:  make(map[string]bool),
		transcriptDirty:   true,
		parts:             make(map[int64][]domain.Part),
		status:            "Ready",
		workdir:           workdir,
		startupMode:       mode,
		startupOptions:    startupOpts,
		mouseEnabled:      cfg.UI.Mouse,
		debug:             debug,
		caps:              provider.NewCapabilityStore(cfg.StateDir()),
		runtimeCtxChecked: map[string]bool{},
		attachmentFiles:   attachment.NewManager(cfg.StateDir()),
	}, nil
}

func (m Model) Init() ui.Cmd {
	if !m.mouseEnabled {
		return m.withRootTimers(ui.Batch(m.loadCmd(), m.syncWindowTitleCmd()))
	}
	return m.withRootTimers(ui.Batch(
		m.loadCmd(),
		m.syncWindowTitleCmd(),
		func() ui.Msg { return ui.EnableMouseCellMotion() },
	))
}

func (m Model) Update(msg ui.Msg) (next ui.Model, cmd ui.Cmd) {
	defer func() {
		if next == nil {
			next = m
		}
		switch typed := next.(type) {
		case Model:
			typed.syncDebugRuntime()
			cmd = typed.withRootTimers(cmd)
			next = typed
		case *Model:
			typed.syncDebugRuntime()
			cmd = typed.withRootTimers(cmd)
		}
	}()
	switch msg := msg.(type) {
	case ui.WindowSizeMsg:
		m.invalidateBodyCache()
		m.width = msg.Width
		m.height = msg.Height
		m.resize()
		m.refreshViewport()
		return m, nil
	case rootTimerMsg:
		if msg.Seq != m.rootTimerSeq {
			return m, nil
		}
		m.rootTimerPending = false
		m.rootTimerPendingAt = time.Time{}
		root := (&m).syncUIRoot()
		var cmds []ui.Cmd
		for _, event := range root.DueTimers(msg.At) {
			handled, timerCmd := root.HandleEvent(event)
			if handled {
				cmds = append(cmds, timerCmd)
			}
		}
		return m, ui.Batch(cmds...)
	case spinnerTickMsg:
		m.invalidateBodyCache()
		if !m.shouldAnimateSpinner() {
			return m, nil
		}
		wasAtBottom := m.viewport.AtBottom()
		offset := m.viewport.YOffset
		m.busy.spinner.tick()
		if m.hasPreferencesDialog() {
			m.preferences.Tick()
		}
		if wasAtBottom {
			m.refreshViewport()
		} else {
			m.refreshViewportAt(offset)
		}
		return m, ui.Batch(spinnerTickCmd(), m.syncWindowTitleCmd())
	case promptDoneMsg:
		m.invalidateBodyCache()
		if msg.err != nil {
			return m.finishOperationWithError(msg.err)
		}
		m.startBusy(m.busy.scopeOrDefault(busyScopeTranscript), m.busy.statusOrDefault("Working ..."))
		return m, ui.Batch(nextEventCmd(msg.events), m.spinnerCmdIfNeeded(), m.syncWindowTitleCmd())
	case runPromptMsg:
		m.invalidateBodyCache()
		if msg.err != nil {
			return m.finishOperationWithError(msg.err)
		}
		if msg.contextChecked {
			if m.runtimeCtxChecked == nil {
				m.runtimeCtxChecked = map[string]bool{}
			}
			m.runtimeCtxChecked[msg.providerID] = true
		}
		if msg.providerID != "" && msg.contextWindow > 0 {
			providerCfg, ok := m.cfg.Provider(msg.providerID)
			if ok && providerCfg.ContextWindow != msg.contextWindow {
				providerCfg.ContextWindow = msg.contextWindow
				m.cfg.Providers[msg.providerID] = providerCfg
			}
		}
		m.currentSession = msg.session
		m.pendingModelNote = ""
		m.startBusy(m.busy.scopeOrDefault(busyScopeTranscript), "Working ...")
		return m, ui.Batch(nextEventCmd(msg.events), m.spinnerCmdIfNeeded(), m.syncWindowTitleCmd())
	case kickoffPromptMsg:
		return m, ui.Batch(m.promptCmd(m.beginActiveOperation(), msg.Prompt, msg.Attachments, msg.References), m.spinnerCmdIfNeeded(), m.syncWindowTitleCmd())
	case llmPreviewMsg:
		if msg.err != nil {
			m.status = msg.err.Error()
			return m, m.syncWindowTitleCmd()
		}
		m.openLLMPreview(msg.title, msg.body)
		return m, m.syncWindowTitleCmd()
	case eventMsg:
		m.invalidateBodyCache()
		m.recordEvent(msg.event)
		m.applyEvent(msg.event)
		if msg.events != nil {
			return m, ui.Batch(m.reloadDetailsCmd(), nextEventCmd(msg.events), m.syncWindowTitleCmd())
		}
		m.stopBusy()
		return m, ui.Batch(m.reloadDetailsCmd(), m.syncWindowTitleCmd())
	case loadMsg:
		m.invalidateTranscript()
		m = m.UpdateLoad(msg)
		if m.debug != nil && m.currentSession.ID > 0 {
			m.debug.RecordLifecycle(m.currentSession.ID, "session_reloaded", fmt.Sprintf("%d messages", len(m.messages)), map[string]string{"messages": strconv.Itoa(len(m.messages))})
		}
		if !msg.preserveBusy {
			m.stopBusyWithStatus("Ready")
		}
		if cmd := m.dequeuePromptCmd(); cmd != nil {
			return m, ui.Batch(cmd, m.syncWindowTitleCmd())
		}
		return m, m.syncWindowTitleCmd()
	case agentsRefreshMsg:
		m.invalidateTranscript()
		if msg.err != nil {
			m.status = msg.err.Error()
			m.stopBusy()
			return m, m.syncWindowTitleCmd()
		}
		m = m.UpdateLoad(msg.load)
		m.stopBusy()
		m.status = "Refreshed project instructions"
		return m, m.syncWindowTitleCmd()
	case forkSessionMsg:
		m.invalidateTranscript()
		if msg.err != nil {
			m.status = msg.err.Error()
			m.stopBusy()
			return m, m.syncWindowTitleCmd()
		}
		m = m.UpdateLoad(msg.load)
		m.stopBusy()
		m.status = fmt.Sprintf("Forked session %d from %d", msg.forkedID, msg.sourceID)
		return m, m.syncWindowTitleCmd()
	case newSessionMsg:
		m.invalidateTranscript()
		m.sessions = msg.sessions
		m.currentSession = msg.session
		m.messages = msg.messages
		m.parts = msg.parts
		m.approvals = msg.approvals
		m.tasks = msg.tasks
		m.workspace = msg.workspace
		m.resetComposerInput()
		m.draftAttachments = nil
		m.draftReferences = nil
		m.closePicker()
		m.closeSessionDialog()
		m.closeConnectDialog()
		m.closeDisconnectDialog()
		m.closeModelDialog()
		m.closeAgentsModal()
		m.agentsDrift = false
		m.stopBusy()
		if msg.session.ID > 0 {
			m.status = fmt.Sprintf("Started session %d", msg.session.ID)
		} else {
			m.status = "Started new session"
		}
		if m.debug != nil {
			m.debug.RecordLifecycle(msg.session.ID, "new_session_ready", msg.session.Title, nil)
		}
		m.updateComposerMenus()
		m.refreshViewport()
		return m, m.syncWindowTitleCmd()
	case sessionPickerMsg:
		m.invalidateBodyCache()
		m.sessions = msg.sessions
		m.openSessionPicker()
		m.stopBusyWithStatus("Select a session to resume")
		return m, m.syncWindowTitleCmd()
	case providerProbeMsg:
		m.invalidateBodyCache()
		if !m.hasConnectDialog() {
			return m, nil
		}
		if msg.err != nil {
			m.connectDialog.SetStatusError("Connection test failed: " + msg.err.Error())
			m.status = msg.err.Error()
			return m, m.syncWindowTitleCmd()
		}
		modelCount := len(msg.result.Models)
		if modelCount == 0 {
			m.connectDialog.SetStatusSuccess("Connection success, 0 models discovered")
			m.status = "Provider connected"
		} else {
			m.connectDialog.SetStatusSuccess(fmt.Sprintf("Connection success, %d models discovered", modelCount))
			m.status = fmt.Sprintf("Provider connected: %d models", modelCount)
		}
		return m, m.syncWindowTitleCmd()
	case modelListMsg:
		m.invalidateBodyCache()
		if msg.err != nil {
			m.status = msg.err.Error()
			return m, m.syncWindowTitleCmd()
		}
		if len(msg.models) == 0 {
			m.status = "No models returned by provider"
			return m, m.syncWindowTitleCmd()
		}
		m.openModelDialog(msg.providerID, msg.models)
		if msg.postConnect {
			m.status = "Choose an initial model"
		} else {
			m.status = fmt.Sprintf("Loaded %d models", len(msg.models))
		}
		return m, m.syncWindowTitleCmd()
	case ui.MouseMsg:
		root := (&m).syncUIRoot()
		if handled, cmd := root.HandleEvent(ui.MouseEvent(msg)); handled {
			if msg.Action == ui.MouseActionPress && msg.Button == ui.MouseButtonLeft {
				return &m, cmd
			}
			return m, cmd
		}
		if handled, cmd := m.handleMainWindowMouse(msg); handled {
			if msg.Action == ui.MouseActionPress && msg.Button == ui.MouseButtonLeft {
				return &m, cmd
			}
			return m, cmd
		}
		return m, nil
	case ui.KeyMsg:
		return m.handleKey(msg)
	}

	var nextCmd ui.Cmd
	m.composer, nextCmd = m.composer.Update(msg)
	m.updateComposerMenus()
	return m, nextCmd
}

func (m Model) centeredModal(element ui.Element) ui.Element {
	if element == nil {
		return nil
	}
	return ui.Align{
		Horizontal: ui.AlignCenter,
		Vertical:   ui.AlignCenter,
		Child: ui.Constrained{
			Constraints: ui.Constraints{
				MaxW: max(1, m.width-3),
				MaxH: max(1, m.height-2),
			},
			Child: element,
		},
	}
}

func (m Model) ViewLines() []string {
	return m.viewSurface().Lines()
}

func (m Model) ViewSurface() ui.SurfaceView {
	return m.viewSurface()
}

func (m *Model) renderElementText(element ui.Element, width, height int) string {
	return strings.Join(ui.RenderSurface(&ui.Context{Palette: m.palette}, element, width, height).Lines(), "\n")
}

func (m *Model) viewSurface() ui.Surface {
	if m.width <= 0 || m.height <= 0 {
		return ui.Surface{}
	}
	m.syncDebugRuntime()
	root := m.syncUIRoot()
	return root.RenderFrame()
}

func (m *Model) handleKey(msg ui.KeyMsg) (ui.Model, ui.Cmd) {
	if msg.String() != "esc" {
		m.interruptArmedAt = time.Time{}
	}
	root := m.syncUIRoot()
	if handled, cmd := root.HandleEvent(ui.KeyEvent(msg)); handled {
		return m, cmd
	}
	if root.FocusedWindow() != "" {
		return m, nil
	}
	_, cmd := m.handleMainWindowKey(msg)
	return m, cmd
}

func (m *Model) handleMainWindowKey(msg ui.KeyMsg) (bool, ui.Cmd) {
	if m.hasApprovalPrompt() {
		m.ensureApprovalButtons()
		if idx, ok := m.approvalButtons.HotkeyIndex(msg); ok {
			_, cmd := m.activateApprovalButton(idx)
			return true, cmd
		}
		switch msg.String() {
		case "left", "up", "shift+tab":
			m.approvalButtons.Move(-1)
			return true, nil
		case "right", "down", "tab":
			m.approvalButtons.Move(1)
			return true, nil
		case "y":
			_, cmd := m.submitApprovalChoice(true)
			return true, cmd
		case "p":
			m.openApprovalPermissionsPicker()
			return true, m.syncWindowTitleCmd()
		case "n", "esc":
			_, cmd := m.submitApprovalChoice(false)
			return true, cmd
		case "enter":
			_, cmd := m.activateApprovalButton(m.approvalButtons.Index)
			return true, cmd
		}
	}

	if m.hasComposerHistoryMenu() {
		switch msg.String() {
		case "ctrl+c":
			_, cmd := m.quit()
			return true, cmd
		case "esc":
			m.cancelComposerHistorySearch()
			m.invalidateFooterCache()
			return true, m.syncWindowTitleCmd()
		case "enter", "tab":
			if !m.acceptComposerHistorySelection() {
				m.invalidateFooterCache()
				return true, nil
			}
			m.invalidateFooterCache()
			return true, m.syncWindowTitleCmd()
		case "up", "ctrl+s":
			m.moveComposerHistorySelection(-1)
			m.invalidateFooterCache()
			return true, nil
		case "down", "ctrl+r":
			m.moveComposerHistorySelection(1)
			m.invalidateFooterCache()
			return true, nil
		case "backspace":
			m.trimComposerHistoryQuery()
			m.invalidateFooterCache()
			return true, nil
		default:
			if msg.Type == ui.KeyRunes {
				m.appendComposerHistoryQuery(msg.String())
				m.invalidateFooterCache()
				return true, nil
			}
		}
	}

	if m.hasSlashMenu() {
		switch msg.String() {
		case "up":
			if m.slashIndex > 0 {
				m.slashIndex--
			}
			m.invalidateFooterCache()
			return true, nil
		case "down":
			if m.slashIndex < len(m.slashMatches)-1 {
				m.slashIndex++
			}
			m.invalidateFooterCache()
			return true, nil
		case "tab":
			m.acceptSlashSelection()
			m.invalidateFooterCache()
			return true, nil
		case "enter":
			if model, cmd, ok := m.executeSelectedSlashCommand(); ok {
				_ = model
				return true, cmd
			}
			m.acceptSlashSelection()
			m.invalidateFooterCache()
			return true, nil
		case "esc":
			m.slashMatches = nil
			m.slashIndex = 0
			m.invalidateFooterCache()
			return true, nil
		}
	}
	if m.hasSkillMenu() {
		switch msg.String() {
		case "up":
			if m.skillIndex > 0 {
				m.skillIndex--
			}
			m.invalidateFooterCache()
			return true, nil
		case "down":
			if m.skillIndex < len(m.skillMatches)-1 {
				m.skillIndex++
			}
			m.invalidateFooterCache()
			return true, nil
		case "tab", "enter":
			m.acceptSkillSelection()
			m.invalidateFooterCache()
			return true, nil
		case "esc":
			m.skillMatches = nil
			m.skillIndex = 0
			m.invalidateFooterCache()
			return true, nil
		}
	}

	if m.hasMentionMenu() {
		switch msg.String() {
		case "up":
			if m.mentionIndex > 0 {
				m.mentionIndex--
			}
			m.invalidateFooterCache()
			return true, nil
		case "down":
			if m.mentionIndex < len(m.mentionMatches)-1 {
				m.mentionIndex++
			}
			m.invalidateFooterCache()
			return true, nil
		case "tab", "enter":
			m.acceptMentionSelection()
			m.invalidateFooterCache()
			return true, nil
		case "esc":
			m.mentionMatches = nil
			m.mentionIndex = 0
			m.invalidateFooterCache()
			return true, nil
		}
	}

	switch msg.String() {
	case "ctrl+c":
		_, cmd := m.quit()
		return true, cmd
	case "alt+h":
		m.openHelpModal()
		return true, m.syncWindowTitleCmd()
	case "ctrl+v":
		_, cmd := m.pasteClipboardText()
		return true, cmd
	case "ctrl+y":
		_, cmd := m.copyLatestAssistantMessage()
		return true, cmd
	case "backspace":
		if strings.TrimSpace(m.composer.Value()) == "" && m.poppedLastDraftAttachment() {
			m.invalidateFooterCache()
			return true, m.syncWindowTitleCmd()
		}
	case "esc":
		if m.loading {
			_, cmd := m.handleInterruptKey()
			return true, cmd
		}
	case "ctrl+s":
		m.showSidebar = !m.showSidebar
		m.resize()
		m.refreshViewport()
		return true, nil
	case "alt+r":
		m.showReasoning = !m.showReasoning
		m.invalidateTranscript()
		m.refreshViewport()
		return true, nil
	case "alt+p":
		m.showSystem = !m.showSystem
		m.invalidateTranscript()
		m.refreshViewport()
		return true, nil
	case "alt+o":
		prompt := strings.TrimSpace(m.composer.Value())
		if prompt == "" && len(m.draftAttachments) == 0 && len(m.draftReferences) == 0 {
			m.status = "No draft prompt to preview"
			return true, m.syncWindowTitleCmd()
		}
		return true, m.previewLLMRequestCmd(context.Background(), prompt, slices.Clone(m.draftAttachments), slices.Clone(m.draftReferences))
	case "ctrl+r":
		if !m.openComposerHistorySearch() {
			return true, nil
		}
		return true, m.syncWindowTitleCmd()
	case "ctrl+g":
		if m.loading {
			_, cmd := m.queueContinuePrompt()
			return true, cmd
		}
		if ok, status := m.canContinue(); !ok {
			m.status = status
			return true, m.syncWindowTitleCmd()
		}
		m.startBusy(busyScopeTranscript, "Continuing…")
		return true, ui.Batch(m.continueCmd(m.beginActiveOperation()), m.spinnerCmdIfNeeded())
	case "shift+enter", "alt+enter":
		m.composer.InsertRune('\n')
		m.updateComposerMenus()
		m.invalidateFooterCache()
		return true, nil
	case "alt+up":
		_, cmd := m.popQueuedPromptForEditing()
		return true, cmd
	case "up":
		if !m.recallComposerHistory(-1) {
			return true, nil
		}
		m.invalidateFooterCache()
		return true, nil
	case "down":
		if !m.recallComposerHistory(1) {
			return true, nil
		}
		m.invalidateFooterCache()
		return true, nil
	case "tab":
		if m.loading && !m.hasSlashMenu() {
			_, cmd := m.queueComposerPrompt(queuedPromptModeSteer)
			return true, cmd
		}
	case "enter":
		prompt := strings.TrimSpace(m.composer.Value())
		if prompt == "" && len(m.draftAttachments) == 0 && len(m.draftReferences) == 0 {
			return false, nil
		}
		if m.loading {
			_, cmd := m.queueComposerPrompt(queuedPromptModeSteer)
			return true, cmd
		}
		if handledModel, cmd, ok := m.handleLocalCommand(prompt); ok {
			_ = handledModel
			return true, cmd
		}
		if ok, status := m.canSendPrompt(); !ok {
			m.openConnectDialog()
			m.status = status
			return true, nil
		}
		drafts := slices.Clone(m.draftAttachments)
		refs := slices.Clone(m.draftReferences)
		m.resetComposerInput()
		m.draftAttachments = nil
		m.draftReferences = nil
		m.appendLocalUserPrompt(prompt, drafts, refs)
		m.startBusy(busyScopeTranscript, "Running…")
		return true, m.kickoffPromptCmd(prompt, drafts, refs)
	}

	var cmd ui.Cmd
	beforeRevision := m.composer.Revision()
	beforeCursorVisible := m.composer.CursorVisible()
	beforeCursorIndex := m.composer.CursorIndex()
	m.composer, cmd = m.composer.Update(msg)
	if beforeRevision != m.composer.Revision() {
		m.resetComposerHistory()
	}
	m.updateComposerMenus()
	if beforeRevision != m.composer.Revision() || beforeCursorVisible != m.composer.CursorVisible() || beforeCursorIndex != m.composer.CursorIndex() {
		m.invalidateFooterCache()
	}
	handled := beforeRevision != m.composer.Revision() ||
		beforeCursorVisible != m.composer.CursorVisible() ||
		beforeCursorIndex != m.composer.CursorIndex() ||
		cmd != nil
	return handled, cmd
}

func (m *Model) handleMouse(msg ui.MouseMsg) (ui.Model, ui.Cmd, bool) {
	if !m.mouseEnabled {
		return m, nil, false
	}
	if msg.Action != ui.MouseActionPress || msg.Button != ui.MouseButtonLeft {
		return m, nil, false
	}
	if msg.Y < 0 || msg.Y >= m.viewport.Height {
		return m, nil, false
	}
	if msg.X < 1 || msg.X > m.viewport.Width+2 {
		return m, nil, false
	}
	row := m.viewport.YOffset + msg.Y
	for i := len(m.transcriptControls) - 1; i >= 0; i-- {
		control := m.transcriptControls[i]
		if !control.Enabled || !control.Rect.Contains(ui.Point{X: max(0, msg.X-1), Y: row}) {
			continue
		}
		if strings.HasPrefix(control.ID, "toolrun:") {
			runID := strings.TrimPrefix(control.ID, "toolrun:")
			if strings.TrimSpace(runID) == "" {
				return m, nil, true
			}
			if m.expandedToolRuns == nil {
				m.expandedToolRuns = make(map[string]bool)
			}
			m.expandedToolRuns[runID] = !m.expandedToolRuns[runID]
			m.invalidateTranscript()
			m.refreshViewportPreserve()
			return m, nil, true
		}
	}
	return m, nil, false
}

func (m *Model) handleMainWindowMouse(msg ui.MouseMsg) (bool, ui.Cmd) {
	if m.hasApprovalPrompt() && m.mouseEnabled && msg.Action == ui.MouseActionPress && msg.Button == ui.MouseButtonLeft {
		if msg.Y >= 0 && msg.Y < m.height {
			element := m.renderApprovalPromptElement()
			if element != nil {
				promptHeight := element.Measure(&ui.Context{Palette: m.palette}, ui.NewConstraints(m.width, 0)).H
				startY := m.height - m.composerAreaHeight()
				if msg.Y >= startY && msg.Y < startY+promptHeight {
					runtime := ui.Runtime{}
					ctx := &ui.Context{Palette: m.palette, Runtime: &runtime}
					element.Render(ctx, ui.Rect{X: 0, Y: startY, W: element.Measure(ctx, ui.NewConstraints(m.width, 0)).W, H: promptHeight})
					if control, ok := runtime.Hit(ui.Point{X: msg.X, Y: msg.Y}); ok {
						for idx, button := range m.approvalButtons.Buttons {
							if button.ID != control.ID {
								continue
							}
							m.approvalButtons.Index = idx
							_, cmd := m.activateApprovalButton(idx)
							return true, cmd
						}
					}
					return true, nil
				}
			}
		}
	}
	if _, cmd, ok := m.handleMouse(msg); ok {
		return true, cmd
	}
	if m.handleTranscriptMouse(msg) {
		return true, nil
	}
	return false, nil
}

func (m *Model) handleTranscriptMouse(msg ui.MouseMsg) bool {
	switch msg.Button {
	case ui.MouseButtonWheelUp:
		if msg.Action == ui.MouseActionPress {
			m.scrollTranscript(-3)
			return true
		}
	case ui.MouseButtonWheelDown:
		if msg.Action == ui.MouseActionPress {
			m.scrollTranscript(3)
			return true
		}
	}
	return false
}

func (m *Model) handleLLMPreviewMouse(msg ui.MouseMsg) bool {
	switch msg.Button {
	case ui.MouseButtonWheelUp:
		if msg.Action == ui.MouseActionPress {
			m.scrollLLMPreview(-3)
			return true
		}
	case ui.MouseButtonWheelDown:
		if msg.Action == ui.MouseActionPress {
			m.scrollLLMPreview(3)
			return true
		}
	}
	return false
}

func (m *Model) handleLLMPreviewKey(msg ui.KeyMsg) bool {
	switch msg.String() {
	case "up":
		m.scrollLLMPreview(-1)
		return true
	case "down":
		m.scrollLLMPreview(1)
		return true
	case "pgup":
		m.scrollLLMPreview(-max(1, m.llmPreviewHeight))
		return true
	case "pgdown":
		m.scrollLLMPreview(max(1, m.llmPreviewHeight))
		return true
	case "home":
		m.llmPreviewYOffset = 0
		return true
	case "end":
		m.llmPreviewYOffset = m.llmPreviewMaxOffset()
		return true
	default:
		return false
	}
}

func (m *Model) scrollLLMPreview(delta int) {
	m.llmPreviewYOffset = min(max(0, m.llmPreviewYOffset+delta), m.llmPreviewMaxOffset())
}

func (m *Model) scrollTranscript(delta int) {
	m.viewport.SetYOffset(m.viewport.YOffset + delta)
	m.invalidateMainSurface()
	if main := m.ensureMainScreenWidget(); main != nil {
		main.transcript.Invalidate()
	}
}

func (m *Model) llmPreviewMaxOffset() int {
	contentHeight := lipgloss.Height(m.llmPreviewBody)
	return max(0, contentHeight-max(0, m.llmPreviewHeight))
}

func (m *Model) applyEvent(evt domain.Event) {
	switch evt.Kind {
	case domain.EventKindMessageDelta:
		m.startBusy(busyScopeTranscript, "Working ...")
	case domain.EventKindReasoning:
		m.startBusy(busyScopeTranscript, "Working ...")
	case domain.EventKindToolStart:
		status := strings.TrimSpace(evt.Text)
		if status == "" {
			status = fmt.Sprintf("Running %s…", evt.Tool)
		} else {
			status = fmt.Sprintf("Running %s…", status)
		}
		m.startBusy(busyScopeTranscript, status)
	case domain.EventKindToolResult:
		m.startBusy(busyScopeTranscript, fmt.Sprintf("Tool %s finished", evt.Tool))
	case domain.EventKindApprovalAsk:
		m.status = evt.Text
		m.stopBusy()
	case domain.EventKindApprovalReply:
		m.status = evt.Text
	case domain.EventKindTaskUpdate:
		m.status = "Task updated"
	case domain.EventKindSessionTitle:
		title := strings.TrimSpace(evt.Text)
		if title != "" {
			m.currentSession.Title = title
			for i := range m.sessions {
				if m.sessions[i].ID == m.currentSession.ID {
					m.sessions[i].Title = title
					break
				}
			}
		}
	case domain.EventKindUsage:
		m.status = fmt.Sprintf("Usage total=%d", evt.Usage.TotalTokens)
	case domain.EventKindStatus:
		if evt.Text != "" {
			m.status = evt.Text
			if m.busy.active {
				m.busy.updateStatus(evt.Text)
			}
		}
		if profile := strings.TrimSpace(evt.Meta["permission_profile"]); profile != "" {
			m.currentSession.PermissionProfile = profile
			for idx := range m.sessions {
				if m.sessions[idx].ID == m.currentSession.ID {
					m.sessions[idx].PermissionProfile = profile
					break
				}
			}
		}
	case domain.EventKindError:
		if evt.Err != nil {
			m.status = evt.Err.Error()
		}
		m.stopBusy()
	case domain.EventKindMessageDone:
		m.stopBusyWithStatus("Ready")
	}
}

func (m *Model) resize() {
	sidebarWidth := m.sidebarWidth()
	bodyWidth := m.width - sidebarWidth - 3
	if bodyWidth < 20 {
		bodyWidth = 20
	}
	m.composer.SetWidth(m.composerWidth())
	bodyHeight := m.height - m.statusPaneHeight()
	if bodyHeight < 5 {
		bodyHeight = 5
	}
	m.viewport.Width = bodyWidth
	m.viewport.Height = bodyHeight
	m.resizeLLMPreview()
	m.invalidateTranscript()
}

func (m *Model) renderHeader() string {
	return ""
}

func (m *Model) renderBodyLines() []string {
	return m.renderBodySurface().Lines()
}

func (m *Model) renderBodySurface() ui.Surface {
	ctx := &ui.Context{Palette: m.palette}
	width := max(0, m.width)
	height := max(0, m.height)
	if width <= 0 || height <= 0 {
		if width <= 0 {
			width = max(0, m.viewport.Width)
			if width == 0 {
				width = max(40, m.composerWidth()+m.sidebarWidth()+3)
			}
		}
		if height <= 0 {
			height = max(0, m.viewport.Height)
			if height == 0 {
				height = max(6, m.transcriptViewportHeight()+m.composerAreaHeight()+m.statusPaneHeight())
			}
		}
	}
	return m.ensureMainScreenWidget().Surface(ctx, ui.Rect{W: width, H: height})
}

func (m *Model) renderBodyElement() ui.Element {
	return m.renderMainScreenElement()
}

func (m *Model) transcriptActivityHeight() int {
	element := m.renderTranscriptActivityElement()
	if element == nil {
		return 0
	}
	return element.Measure(&ui.Context{Palette: m.palette}, ui.NewConstraints(max(0, m.viewport.Width), 0)).H
}

func (m *Model) transcriptViewportHeight() int {
	height := max(0, m.viewport.Height)
	if activityHeight := m.transcriptActivityHeight(); activityHeight > 0 {
		height -= activityHeight + 1
	}
	if composerHeight := m.composerAreaHeight(); composerHeight > 0 {
		height -= composerHeight + 1
	}
	return max(0, height)
}

func (m *Model) renderComposerAreaLines() []string {
	return m.renderComposerAreaSurface().Lines()
}

func (m *Model) renderComposerAreaSurface() ui.Surface {
	ctx := &ui.Context{Palette: m.palette}
	width := max(0, m.width)
	return m.ensureMainScreenWidget().composer.Surface(ctx, ui.Rect{W: width})
}

func (m *Model) renderComposerAreaElement() ui.Element {
	if !m.shouldShowComposerArea() {
		return ui.VisibleElement{}
	}
	elements := []ui.Element{}
	if prompt := m.renderApprovalPromptElement(); prompt != nil {
		elements = append(elements, prompt)
	}
	if menu := m.renderComposerHistoryMenuElement(); menu != nil {
		elements = append(elements, menu)
	} else if menu := m.renderSlashMenuElement(); menu != nil {
		elements = append(elements, menu)
	} else if menu := m.renderMentionMenuElement(); menu != nil {
		elements = append(elements, menu)
	} else if menu := m.renderSkillMenuElement(); menu != nil {
		elements = append(elements, menu)
	}
	if attachments := m.renderDraftAttachmentsElement(); attachments != nil {
		elements = append(elements, attachments)
	}
	if preview := m.renderQueuedPromptPreviewElement(); preview != nil {
		elements = append(elements, preview)
	}
	elements = append(elements, ui.Spacer{H: 1})
	elements = append(elements, m.renderComposerElement())
	children := make([]ui.Child, 0, len(elements))
	for _, element := range elements {
		if element == nil {
			continue
		}
		children = append(children, ui.Fixed(element))
	}
	return ui.VBox{Children: children}
}

func (m *Model) shouldShowComposerArea() bool {
	if m.composerAreaHasContent() {
		return true
	}
	return m.composer.Focused()
}

func (m *Model) composerAreaHasContent() bool {
	if len(m.draftAttachments) > 0 || m.queuedPrompt != nil {
		return true
	}
	if m.renderApprovalPromptElement() != nil ||
		m.renderComposerHistoryMenuElement() != nil ||
		m.renderSlashMenuElement() != nil ||
		m.renderMentionMenuElement() != nil ||
		m.renderSkillMenuElement() != nil {
		return true
	}
	return strings.TrimSpace(m.composer.Placeholder) != "" ||
		strings.TrimSpace(m.composer.Value()) != ""
}

func (m *Model) composerAreaHeight() int {
	cache := m.ensureRenderCache()
	if !cache.composerAreaValid {
		_ = m.renderComposerAreaSurface()
	}
	return cache.composerAreaHeight
}

func (m *Model) renderStatusPaneElement() ui.Element {
	return ui.VisibleElement{}
}

func (m *Model) statusPaneHeight() int {
	element := m.renderStatusPaneElement()
	if !ui.ElementVisible(element) {
		return 0
	}
	return element.Measure(&ui.Context{Palette: m.palette}, ui.NewConstraints(max(0, m.width), 0)).H
}

func (m *Model) renderMainScreenElement() ui.Element {
	var transcript ui.Element = ui.SurfaceBox{Surface: m.viewport.VisibleSurface()}
	if retained := m.transcriptElement(nil); retained != nil {
		transcript = retained
	}
	mainChildren := []ui.Child{
		ui.Flex(transcript, 1),
		ui.Fixed(ui.VisibleElement{Child: m.renderTranscriptActivityElement(), BoxProps: ui.BoxProps{Hidden: m.renderTranscriptActivityElement() == nil}}),
		ui.Fixed(m.renderComposerAreaElement()),
	}
	mainColumn := ui.VBox{Children: mainChildren, Spacing: 1}
	sidebar := ui.VisibleElement{
		BoxProps: ui.BoxProps{
			Hidden: !m.showSidebar,
		},
		Child: ui.Sidebar{
			Child:  ui.TextPane{Content: m.renderSidebar()},
			Height: m.viewport.Height,
			Width:  m.sidebarWidth(),
		},
	}
	rootChildren := []ui.Child{
		ui.Flex(ui.HBox{
			Children: []ui.Child{
				ui.Flex(ui.Inset{Padding: ui.SymmetricInsets(1, 0), Child: mainColumn}, 1),
				ui.Fixed(sidebar),
			},
		}, 1),
		ui.Fixed(m.renderStatusPaneElement()),
	}
	return ui.VBox{Children: rootChildren}
}

func (m *Model) renderComposerElement() ui.Element {
	m.composer.Prompt = m.promptGlyph() + " "
	line := m.composer.VisibleLine()
	return ui.NewComposer(ui.ComposerProps{
		Palette:       m.palette,
		Width:         m.composerWidth(),
		HalfBlocks:    m.halfBlocksEnabled(),
		PromptGlyph:   m.promptGlyph(),
		Value:         m.composer.Value(),
		Placeholder:   m.composer.Placeholder,
		ContentBefore: line.Before(),
		ContentCursor: line.Cursor(),
		ContentAfter:  line.After(),
		CursorVisible: m.composer.CursorVisible(),
	})
}

func (m *Model) renderDraftAttachmentsElement() ui.Element {
	if len(m.draftAttachments) == 0 {
		return nil
	}
	items := make([]ui.AttachmentItem, 0, len(m.draftAttachments))
	for _, draft := range m.draftAttachments {
		items = append(items, ui.AttachmentItem{Label: m.attachmentLabel(draft.Metadata)})
	}
	return ui.AttachmentList{Items: items, Width: m.composerWidth()}
}

func (m *Model) renderQueuedPromptPreviewElement() ui.Element {
	if m.queuedPrompt == nil {
		return nil
	}
	preview := ui.PendingInputPreview{
		Width: m.composerWidth(),
	}
	switch m.queuedPrompt.Mode {
	case queuedPromptModeSteer:
		preview.PendingSteers = []string{m.queuedPrompt.Text}
	case queuedPromptModeContinue:
		preview.QueuedMessages = []string{"Continue"}
	default:
		preview.QueuedMessages = []string{m.queuedPrompt.Text}
	}
	return preview
}

func (m *Model) composerWidth() int {
	if m.width <= 0 {
		return 40
	}
	sidebarWidth := m.sidebarWidth()
	bodyWidth := m.width - sidebarWidth - 3
	if bodyWidth < 20 {
		bodyWidth = 20
	}
	return bodyWidth - 2
}

func (m *Model) sidebarWidth() int {
	if !m.showSidebar {
		return 0
	}
	return min(32, max(20, m.width/4))
}

func (m *Model) halfBlocksEnabled() bool {
	return m.cfg.UI.HalfBlocks
}

func (m *Model) promptGlyph() string {
	if m.halfBlocksEnabled() {
		return "█"
	}
	return "┃"
}

func (m *Model) renderHalfBlockLine(width int, char string) string {
	return ui.Composer{Width: width, Palette: m.palette}.HalfBlockLine(char)
}

func mPrompt(cfg config.Config) string {
	if cfg.UI.HalfBlocks {
		return "▌ "
	}
	return "┃ "
}

func (m *Model) renderSidebar() string {
	var lines []string
	lines = append(lines, fmt.Sprintf("Session %d", m.currentSession.ID))
	provider := m.currentSession.ProviderID
	if provider == "" {
		provider = "(unset)"
	}
	lines = append(lines, fmt.Sprintf("  provider %s", truncate(provider, 18)))
	model := m.currentSession.ModelID
	if model == "" {
		model = "(unset)"
	}
	lines = append(lines, fmt.Sprintf("  model   %s", truncate(model, 18)))
	lines = append(lines, "")
	lines = append(lines, "Status")
	status := strings.TrimSpace(m.status)
	if status == "" {
		status = "Ready"
	}
	if m.busy.sidebarActive() {
		lines = append(lines, fmt.Sprintf("  %s %s", m.workingIndicator(), truncate(status, 22)))
	} else {
		lines = append(lines, fmt.Sprintf("  %s", truncate(status, 24)))
	}
	if m.queuedPrompt != nil {
		lines = append(lines, "")
		lines = append(lines, "Queued")
		lines = append(lines, fmt.Sprintf("  %s", m.queuedPrompt.modeLabel()))
		lines = append(lines, fmt.Sprintf("  %s", truncate(m.queuedPrompt.Text, 24)))
	}
	if metrics, ok := sessionctx.FromMessages(m.cfg, m.currentSession, m.messages, m.parts); ok {
		lines = append(lines, "")
		lines = append(lines, "Context")
		lines = append(lines, fmt.Sprintf("  used   %s / %s", formatTokens(metrics.Used), formatTokens(metrics.Max)))
		lines = append(lines, fmt.Sprintf("  usage  %d%% used", metrics.UsagePercent))
	}
	lines = append(lines, "")
	lines = append(lines, "Workspace")
	lines = append(lines, fmt.Sprintf("  cwd     %s", truncate(m.workdir, 20)))
	lines = append(lines, fmt.Sprintf("  project %s", truncate(m.currentProjectRoot(), 20)))
	lines = append(lines, "")
	lines = append(lines, "AGENTS")
	lines = append(lines, "  "+m.renderAgentsSidebarStatus())
	lines = append(lines, "")
	lines = append(lines, "Git")
	if !m.workspace.Available {
		lines = append(lines, "  no repository")
	} else {
		branch := m.workspace.Branch
		if branch == "" {
			branch = "(detached)"
		}
		lines = append(lines, fmt.Sprintf("  branch  %s", truncate(branch, 19)))
		if m.workspace.Upstream != "" {
			lines = append(lines, fmt.Sprintf("  remote  %s", truncate(m.workspace.Upstream, 19)))
		}
		if m.workspace.Summary != "" {
			lines = append(lines, fmt.Sprintf("  sync    %s", truncate(m.workspace.Summary, 19)))
		}
		lines = append(lines, fmt.Sprintf("  diff    %s", m.workspace.SummaryLine()))
		if len(m.workspace.Files) == 0 {
			lines = append(lines, "  clean")
		} else {
			lines = append(lines, "Changed files")
			for _, item := range m.workspace.Files[:min(8, len(m.workspace.Files))] {
				lines = append(lines, m.renderChangedFile(item))
			}
			if len(m.workspace.Files) > 8 {
				lines = append(lines, fmt.Sprintf("  … %d more", len(m.workspace.Files)-8))
			}
		}
	}
	lines = append(lines, "")
	lines = append(lines, "Tasks")
	if len(m.tasks) == 0 {
		lines = append(lines, "  none")
	}
	for _, item := range m.tasks {
		lines = append(lines, fmt.Sprintf("  [%s] %s", item.Status, truncate(item.Body, 18)))
	}
	if debugAddr := m.debugAPIAddr(); debugAddr != "" {
		lines = append(lines, "")
		lines = append(lines, "Debug")
		lines = append(lines, fmt.Sprintf("  api %s", truncate(debugAddr, 23)))
	}
	lines = append(lines, "")
	lines = append(lines, "Help")
	lines = append(lines, "  Alt-H  hotkeys and commands")
	return strings.Join(lines, "\n")
}

func (m Model) debugAPIAddr() string {
	if m.debug == nil {
		return ""
	}
	return strings.TrimSpace(m.debug.Runtime().DebugAPI)
}

func (m *Model) refreshViewport() {
	m.invalidateMainSurface()
	m.refreshViewportAt(-1)
}

func (m *Model) refreshViewportPreserve() {
	m.invalidateMainSurface()
	m.refreshViewportAt(m.viewport.YOffset)
}

func (m *Model) refreshViewportAt(offset int) {
	m.invalidateMainSurface()
	if m.hasExpandedToolRuns() {
		m.transcriptDirty = true
	}
	m.transcriptControls = nil
	retained := m.syncRetainedTranscript()
	if retained == nil {
		m.viewport.SetContent("")
		return
	}
	runtime := ui.Runtime{}
	ctx := &ui.Context{Palette: m.palette, Runtime: &runtime}
	var (
		surface     ui.Surface
		totalHeight int
		appliedY    int
	)
	viewportHeight := max(0, m.transcriptViewportHeight())
	if offset >= 0 {
		surface, totalHeight, appliedY = retained.RenderVisible(ctx, max(0, m.viewport.Width), viewportHeight, offset)
	} else {
		surface, totalHeight, appliedY = retained.RenderBottom(ctx, max(0, m.viewport.Width), viewportHeight)
	}
	m.viewport.SetContentHeight(totalHeight)
	m.viewport.SetYOffset(appliedY)
	m.transcriptControls = runtime.Controls()
	m.viewport.SetVisibleSurface(surface)
	if main := m.ensureMainScreenWidget(); main != nil {
		main.transcript.Invalidate()
	}
}

func (m *Model) hasExpandedToolRuns() bool {
	for _, expanded := range m.expandedToolRuns {
		if expanded {
			return true
		}
	}
	return false
}

func (m *Model) invalidateBodyCache() {
	cache := m.ensureRenderCache()
	cache.bodyValid = false
	cache.renderedBodySurface = ui.Surface{}
	if main := m.ensureMainScreenWidget(); main != nil {
		main.Invalidate()
		main.transcript.Invalidate()
		main.activity.Invalidate()
		main.composer.Invalidate()
		main.sidebar.Invalidate()
		main.statusPane.Invalidate()
	}
	m.invalidateFooterCache()
}

func (m *Model) invalidateMainSurface() {
	cache := m.ensureRenderCache()
	cache.bodyValid = false
	cache.renderedBodySurface = ui.Surface{}
	if main := m.ensureMainScreenWidget(); main != nil {
		main.Invalidate()
	}
}

func (m *Model) invalidateFooterCache() {
	cache := m.ensureRenderCache()
	cache.composerAreaValid = false
	cache.renderedComposerAreaSurface = ui.Surface{}
	if main := m.ensureMainScreenWidget(); main != nil {
		main.composer.Invalidate()
	}
}

func (m *Model) ensureRenderCache() *modelRenderCache {
	if m.renderCache == nil {
		m.renderCache = &modelRenderCache{}
	}
	return m.renderCache
}

func (m *Model) ensureRetainedTranscript() *ui.RetainedTranscript {
	if m.retainedTranscript == nil {
		m.retainedTranscript = ui.NewRetainedTranscript()
	}
	return m.retainedTranscript
}

func (m *Model) invalidateTranscript() {
	m.transcriptDirty = true
	if main := m.ensureMainScreenWidget(); main != nil {
		main.transcript.Invalidate()
	}
	m.invalidateBodyCache()
}

func (m *Model) transcriptElement(runtime *ui.Runtime) ui.Element {
	retained := m.syncRetainedTranscript()
	if retained == nil {
		return nil
	}
	width := max(0, m.viewport.Width)
	height := max(0, m.transcriptViewportHeight())
	if runtime != nil {
		runtime.BeginFrame()
	}
	return ui.TranscriptViewport{
		Transcript: retained,
		OffsetY:    max(0, m.viewport.YOffset),
		Width:      width,
		Height:     height,
	}
}

func (m *Model) syncRetainedTranscript() *ui.RetainedTranscript {
	retained := m.ensureRetainedTranscript()
	if m.transcriptDirty || len(retained.Items()) == 0 {
		items := m.buildTranscriptItems()
		if m.currentSession.ID == 0 && len(m.messages) == 0 && !m.cfg.HasUsableDefaultProvider() {
			items = []ui.TranscriptItem{{
				Key:     "no-provider",
				Element: ui.NewCachedElement(ui.Paragraph{Text: "No provider configured.\n\nType /connect to add one before sending prompts."}, 3),
			}}
		}
		m.syncRetainedTranscriptItems(retained, items)
		m.transcriptDirty = false
	}
	return retained
}

func (m *Model) syncRetainedTranscriptItems(retained *ui.RetainedTranscript, items []ui.TranscriptItem) {
	if retained == nil {
		return
	}
	existing := retained.Items()
	prefix := 0
	for prefix < len(existing) && prefix < len(items) && existing[prefix].Key == items[prefix].Key {
		prefix++
	}
	suffix := 0
	for suffix < len(existing)-prefix && suffix < len(items)-prefix &&
		existing[len(existing)-1-suffix].Key == items[len(items)-1-suffix].Key {
		suffix++
	}
	for idx := 0; idx < prefix; idx++ {
		retained.Replace(idx, items[idx])
	}
	for idx := 0; idx < suffix; idx++ {
		retained.Replace(len(items)-1-idx, items[len(items)-1-idx])
	}
	removeEnd := len(existing) - suffix
	for idx := removeEnd - 1; idx >= prefix; idx-- {
		retained.Remove(idx)
	}
	insertEnd := len(items) - suffix
	for idx := prefix; idx < insertEnd; idx++ {
		retained.Insert(idx, items[idx])
	}
}

func (m *Model) defaultTranscriptSeparator() string {
	return "\n\n"
}

func (m *Model) transcriptSeparator(prev, next transcriptBlock) string {
	if !m.halfBlocksEnabled() {
		return "\n\n"
	}
	if m.isHalfBlockUserMessage(prev) || m.isHalfBlockUserMessage(next) {
		return "\n"
	}
	return "\n\n"
}

func (m *Model) transcriptActivitySeparator(prev transcriptBlock) string {
	if m.halfBlocksEnabled() && m.isHalfBlockUserMessage(prev) {
		return "\n"
	}
	return "\n\n"
}

func (m *Model) isHalfBlockUserMessage(block transcriptBlock) bool {
	return m.halfBlocksEnabled() && block.Kind == transcriptBlockMessage && block.Message.Role == domain.MessageRoleUser
}

func renderedSeparatorHeight(separator string) int {
	if separator == "" {
		return 0
	}
	return max(0, lipgloss.Height("x"+separator+"x")-2)
}

func (m *Model) renderTranscriptActivityElement() ui.Element {
	if !m.busy.transcriptActive() {
		return nil
	}
	return ui.ActivityIndicator{
		Indicator: ui.WorkingIndicatorLine(m.workingIndicator(), m.busy.statusOrDefault("Working ...")),
		Palette:   m.palette,
	}
}

func (m *Model) renderTranscriptMessageElement(msg domain.Message) ui.Element {
	body := m.renderMessageParts(m.parts[msg.ID])
	styledBody := m.renderStyledMessageParts(m.parts[msg.ID])
	stamp := timestamp(msg.CreatedAt, m.cfg.UI.ShowTimestamps)
	switch msg.Role {
	case domain.MessageRoleUser:
		userBody := m.renderUserMessageParts(m.parts[msg.ID])
		if strings.TrimSpace(userBody) == "" {
			userBody = strings.TrimSpace(msg.Summary)
		}
		return m.renderUserMessageElement(userBody, stamp)
	default:
		if strings.TrimSpace(body) == "" {
			body = strings.TrimSpace(msg.Summary)
		}
		if len(styledBody) == 0 && body != "" {
			styledBody = []ui.StyledSpan{{Text: body}}
		}
		return m.renderStyledAssistantMessageElement(styledBody, stamp)
	}
}

func (m *Model) renderUserMessage(body, stamp string) string {
	element := m.renderUserMessageElement(body, stamp)
	return m.renderElementText(element, m.userMessageWidth(body, stamp), 0)
}

func (m *Model) renderUserMessageElement(body, stamp string) ui.Element {
	return ui.NewUserMessage(ui.UserMessageProps{
		Palette:     m.palette,
		Body:        body,
		Stamp:       stamp,
		Width:       m.userMessageWidth(body, stamp),
		HalfBlocks:  m.halfBlocksEnabled(),
		PromptGlyph: m.promptGlyph(),
	})
}

func formatSessionTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04")
}

func formatRelativeSessionTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	delta := time.Since(t)
	if delta < 0 {
		delta = 0
	}
	switch {
	case delta < time.Minute:
		return "now"
	case delta < time.Hour:
		return fmt.Sprintf("%dm ago", int(delta/time.Minute))
	case delta < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(delta/time.Hour))
	default:
		return fmt.Sprintf("%dd ago", int(delta/(24*time.Hour)))
	}
}

func (m *Model) sessionUsageSummary(sessionID int64) (domain.Usage, bool) {
	if m.store == nil {
		return domain.Usage{}, false
	}
	messages, parts, err := m.store.PartsForSession(context.Background(), sessionID)
	if err != nil {
		return domain.Usage{}, false
	}
	return sessionctx.LatestUsage(messages, parts)
}

func (m *Model) userMessageWidth(body, stamp string) int {
	if m.viewport.Width > 0 {
		return m.viewport.Width
	}
	lines := []string{""}
	if strings.TrimSpace(body) != "" {
		lines = append(lines, strings.Split(strings.TrimSpace(body), "\n")...)
	}
	if stamp != "" {
		lines = append(lines, stamp)
	}
	lines = append(lines, "")
	return ui.UserMessageWidth(lines)
}

func (m *Model) renderAssistantMessage(body, stamp string) string {
	element := m.renderAssistantMessageElement(body, stamp)
	return m.renderElementText(element, max(0, m.viewport.Width), 0)
}

func (m *Model) renderAssistantMessageElement(body, stamp string) ui.Element {
	return ui.AssistantMessage{
		Body:      body,
		BaseStyle: ui.CellStyle{FG: ui.CellColorFromLipgloss(m.palette.MarkdownText)},
		Stamp:     stamp,
		Width:     m.viewport.Width,
		Palette:   m.palette,
	}
}

func (m *Model) renderStyledAssistantMessageElement(body []ui.StyledSpan, stamp string) ui.Element {
	return ui.AssistantMessage{
		StyledBody: body,
		BaseStyle:  ui.CellStyle{FG: ui.CellColorFromLipgloss(m.palette.MarkdownText)},
		Stamp:      stamp,
		Width:      m.viewport.Width,
		Palette:    m.palette,
	}
}

func (m *Model) attachmentLabel(meta attachment.Metadata) string {
	switch attachment.ClassifyMIME(meta.MIME) {
	case attachment.KindImage:
		return "[Image] " + meta.Name
	case attachment.KindPDF:
		return "[PDF] " + meta.Name
	case attachment.KindText:
		return "[Text] " + meta.Name
	default:
		return "[File] " + meta.Name
	}
}

func (m *Model) renderMessageParts(parts []domain.Part) string {
	var blocks []string
	var reasoningBlocks []string
	var systemBlocks []string
	var textBlocks []string
	var compactionBlocks []string
	var textBuf strings.Builder
	var reasoningBuf strings.Builder

	flushText := func() {
		if textBuf.Len() == 0 {
			return
		}
		textBlocks = append(textBlocks, m.renderer.RenderPlain(textBuf.String()))
		textBuf.Reset()
	}
	flushReasoning := func() {
		if reasoningBuf.Len() == 0 {
			return
		}
		if m.showReasoning {
			reasoningBlocks = append(reasoningBlocks, m.renderReasoningBlock(reasoningBuf.String()))
		}
		reasoningBuf.Reset()
	}

	for _, part := range parts {
		switch part.Kind {
		case domain.PartKindText:
			flushReasoning()
			textBuf.WriteString(part.Body)
		case domain.PartKindReasoning:
			flushText()
			reasoningBuf.WriteString(part.Body)
		case domain.PartKindCompaction:
			flushText()
			flushReasoning()
			if body := strings.TrimSpace(part.Body); body != "" {
				compactionBlocks = append(compactionBlocks, m.renderer.RenderPlain(body))
			}
		case domain.PartKindSystemNotice:
			flushText()
			flushReasoning()
			if m.showSystem {
				if block := m.renderSystemNoticeBlock(part); block != "" {
					systemBlocks = append(systemBlocks, block)
				}
			}
			continue
		case domain.PartKindEventNotice:
			flushText()
			flushReasoning()
			if block := m.renderEventNoticeBlock(part); block != "" {
				systemBlocks = append(systemBlocks, block)
			}
			continue
		case domain.PartKindToolCall, domain.PartKindToolOutput, domain.PartKindDiff, domain.PartKindApprovalRequest:
			flushText()
			flushReasoning()
			continue
		case domain.PartKindAttachment:
			flushText()
			flushReasoning()
			meta, err := attachment.DecodeMeta(part.MetaJSON)
			if err != nil {
				if body := strings.TrimSpace(part.Body); body != "" {
					blocks = append(blocks, body)
				}
				continue
			}
			blocks = append(blocks, m.attachmentLabel(meta))
		case domain.PartKindReference:
			flushText()
			flushReasoning()
			continue
		default:
			flushText()
			flushReasoning()
			blocks = append(blocks, part.Body)
		}
	}

	flushText()
	flushReasoning()

	blocks = append(blocks, compactionBlocks...)
	blocks = append(blocks, systemBlocks...)
	blocks = append(blocks, reasoningBlocks...)
	blocks = append(blocks, textBlocks...)

	return strings.TrimSpace(strings.Join(blocks, "\n"))
}

func (m *Model) renderStyledMessageParts(parts []domain.Part) []ui.StyledSpan {
	var blocks [][]ui.StyledSpan
	var textBuf strings.Builder

	flushText := func() {
		if textBuf.Len() == 0 {
			return
		}
		block := m.renderer.RenderStyled(textBuf.String())
		if len(block) > 0 {
			blocks = append(blocks, block)
		}
		textBuf.Reset()
	}

	for _, part := range parts {
		switch part.Kind {
		case domain.PartKindText:
			textBuf.WriteString(part.Body)
		case domain.PartKindCompaction:
			flushText()
			if body := strings.TrimSpace(part.Body); body != "" {
				if block := m.renderer.RenderStyled(body); len(block) > 0 {
					blocks = append(blocks, block)
				}
			}
		case domain.PartKindSystemNotice:
			flushText()
			if m.showSystem {
				if block := m.renderStyledSystemNoticeBlock(part); len(block) > 0 {
					blocks = append(blocks, block)
				}
			}
		case domain.PartKindEventNotice:
			flushText()
			if block := m.renderStyledEventNoticeBlock(part); len(block) > 0 {
				blocks = append(blocks, block)
			}
		case domain.PartKindReasoning, domain.PartKindToolCall, domain.PartKindToolOutput, domain.PartKindDiff, domain.PartKindApprovalRequest, domain.PartKindReference:
			flushText()
		case domain.PartKindAttachment:
			flushText()
			meta, err := attachment.DecodeMeta(part.MetaJSON)
			if err != nil {
				if body := strings.TrimSpace(part.Body); body != "" {
					blocks = append(blocks, []ui.StyledSpan{{Text: body}})
				}
				continue
			}
			blocks = append(blocks, []ui.StyledSpan{{Text: m.attachmentLabel(meta)}})
		default:
			flushText()
			if body := strings.TrimSpace(part.Body); body != "" {
				blocks = append(blocks, []ui.StyledSpan{{Text: body}})
			}
		}
	}

	flushText()

	var out []ui.StyledSpan
	for idx, block := range blocks {
		if idx > 0 {
			out = ui.AppendStyledSpan(out, "\n", ui.CellStyle{})
		}
		out = append(out, block...)
	}
	return out
}

func (m *Model) renderSystemNoticeBlock(part domain.Part) string {
	title := strings.TrimSpace(part.Body)
	if strings.EqualFold(title, "usage") {
		return ""
	}
	switch {
	case title == "" && strings.TrimSpace(part.MetaJSON) == "":
		return ""
	case title == "":
		title = "system notice"
	}
	var body strings.Builder
	body.WriteString("### System\n\n")
	body.WriteString(title)
	if meta := strings.TrimSpace(part.MetaJSON); meta != "" {
		body.WriteString("\n\n```json\n")
		body.WriteString(meta)
		body.WriteString("\n```")
	}
	return m.renderer.RenderPlain(body.String())
}

func (m *Model) renderStyledSystemNoticeBlock(part domain.Part) []ui.StyledSpan {
	title := strings.TrimSpace(part.Body)
	if strings.EqualFold(title, "usage") {
		return nil
	}
	switch {
	case title == "" && strings.TrimSpace(part.MetaJSON) == "":
		return nil
	case title == "":
		title = "system notice"
	}
	var body strings.Builder
	body.WriteString("### System\n\n")
	body.WriteString(title)
	if meta := strings.TrimSpace(part.MetaJSON); meta != "" {
		body.WriteString("\n\n```json\n")
		body.WriteString(meta)
		body.WriteString("\n```")
	}
	return m.renderer.RenderStyled(body.String())
}

type eventNoticeMeta struct {
	Kind     string `json:"kind,omitempty"`
	Severity string `json:"severity,omitempty"`
}

func (m *Model) renderEventNoticeBlock(part domain.Part) string {
	title, body := eventNoticePresentation(part)
	if body == "" {
		return ""
	}
	var text strings.Builder
	text.WriteString("### ")
	text.WriteString(title)
	if body != title {
		text.WriteString("\n\n")
		text.WriteString(body)
	}
	return m.renderer.RenderPlain(text.String())
}

func (m *Model) renderStyledEventNoticeBlock(part domain.Part) []ui.StyledSpan {
	title, body := eventNoticePresentation(part)
	if body == "" {
		return nil
	}
	var text strings.Builder
	text.WriteString("### ")
	text.WriteString(title)
	if body != title {
		text.WriteString("\n\n")
		text.WriteString(body)
	}
	return m.renderer.RenderStyled(text.String())
}

func eventNoticePresentation(part domain.Part) (string, string) {
	body := strings.TrimSpace(part.Body)
	if body == "" {
		return "", ""
	}
	var meta eventNoticeMeta
	_ = json.Unmarshal([]byte(part.MetaJSON), &meta)
	switch strings.TrimSpace(meta.Kind) {
	case "interrupted":
		return "Interrupted", body
	}
	switch strings.TrimSpace(meta.Severity) {
	case "error":
		return "Error", body
	case "warning":
		return "Interrupted", body
	default:
		return "Notice", body
	}
}

func (m *Model) renderUserMessageParts(parts []domain.Part) string {
	var blocks []string
	var textBuf strings.Builder

	flushText := func() {
		if textBuf.Len() == 0 {
			return
		}
		blocks = append(blocks, strings.TrimSpace(textBuf.String()))
		textBuf.Reset()
	}

	for _, part := range parts {
		switch part.Kind {
		case domain.PartKindText:
			textBuf.WriteString(part.Body)
		case domain.PartKindAttachment:
			flushText()
			meta, err := attachment.DecodeMeta(part.MetaJSON)
			if err != nil {
				if body := strings.TrimSpace(part.Body); body != "" {
					blocks = append(blocks, body)
				}
				continue
			}
			blocks = append(blocks, m.attachmentLabel(meta))
		case domain.PartKindReference:
			continue
		default:
			flushText()
			if body := strings.TrimSpace(part.Body); body != "" {
				blocks = append(blocks, body)
			}
		}
	}

	flushText()

	return strings.TrimSpace(strings.Join(blocks, "\n"))
}

func (m *Model) renderReasoningBlock(input string) string {
	element := m.renderReasoningBlockElement(input)
	ctx := &ui.Context{Palette: m.palette}
	width := max(0, m.viewport.Width)
	trimLines := width <= 0
	if width <= 0 {
		width = element.Measure(ctx, ui.Constraints{}).W
	}
	rendered := strings.Join(ui.RenderSurface(ctx, element, width, 0).Lines(), "\n")
	if !trimLines {
		return rendered
	}
	lines := strings.Split(rendered, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	return strings.Join(lines, "\n")
}

func (m *Model) renderReasoningBlockElement(input string) ui.Element {
	return ui.ReasoningBlock{
		Body:    input,
		Width:   m.viewport.Width,
		Palette: m.palette,
	}
}

func (m Model) loadCmd() ui.Cmd {
	return func() ui.Msg {
		ctx := context.Background()
		allSessions, err := m.store.ListSessions(ctx)
		if err != nil {
			return promptDoneMsg{err: err}
		}
		sessions := m.visibleSessions(allSessions)
		if m.startupMode == StartupModeResume {
			if len(sessions) == 0 {
				return m.newSessionCmd()()
			}
			return sessionPickerMsg{sessions: sessions}
		}
		if m.startupMode == StartupModeNew {
			return m.newSessionCmd()()
		}
		if len(sessions) == 0 {
			return m.newSessionCmd()()
		}
		current := sessions[0]
		messages, parts, err := m.store.PartsForSession(ctx, current.ID)
		if err != nil {
			return promptDoneMsg{err: err}
		}
		approvals, err := m.store.PendingApprovals(ctx, current.ID)
		if err != nil {
			return promptDoneMsg{err: err}
		}
		tasks, err := m.store.ListTasks(ctx, current.ID)
		if err != nil {
			return promptDoneMsg{err: err}
		}
		workspaceStatus, err := workspace.Snapshot(ctx, m.workdir)
		if err != nil {
			return promptDoneMsg{err: err}
		}
		return loadMsg{
			sessions:  sessions,
			current:   current,
			messages:  messages,
			parts:     parts,
			approvals: approvals,
			tasks:     tasks,
			workspace: workspaceStatus,
		}
	}
}

type loadMsg struct {
	sessions     []domain.Session
	current      domain.Session
	messages     []domain.Message
	parts        map[int64][]domain.Part
	approvals    []store.Approval
	tasks        []store.Task
	workspace    workspace.Status
	preserveBusy bool
}

func (m Model) promptCmd(ctx context.Context, prompt string, drafts []attachment.Draft, refs []reference.Draft) ui.Cmd {
	return func() ui.Msg {
		session := m.currentSession
		if session.ID == 0 {
			var err error
			session, err = m.persistDraftSession(ctx)
			if err != nil {
				return runPromptMsg{err: err}
			}
		}
		providerID, contextWindow, contextChecked, err := m.ensureRuntimeContextWindow(ctx, session)
		if err != nil {
			return runPromptMsg{err: err}
		}
		events, err := m.agent.RunPromptWithInputs(ctx, session, prompt, drafts, refs, m.pendingModelNote)
		return runPromptMsg{
			session:        session,
			events:         events,
			err:            err,
			providerID:     providerID,
			contextWindow:  contextWindow,
			contextChecked: contextChecked,
		}
	}
}

func (m Model) continueCmd(ctx context.Context) ui.Cmd {
	return func() ui.Msg {
		session := m.currentSession
		if session.ID == 0 {
			return runPromptMsg{err: fmt.Errorf("no saved session to continue")}
		}
		providerID, contextWindow, contextChecked, err := m.ensureRuntimeContextWindow(ctx, session)
		if err != nil {
			return runPromptMsg{err: err}
		}
		events, err := m.agent.RunContinue(ctx, session, m.pendingModelNote)
		return runPromptMsg{
			session:        session,
			events:         events,
			err:            err,
			providerID:     providerID,
			contextWindow:  contextWindow,
			contextChecked: contextChecked,
		}
	}
}

func (m Model) newSessionCmd() ui.Cmd {
	return func() ui.Msg {
		ctx := context.Background()
		allSessions, err := m.store.ListSessions(ctx)
		if err != nil {
			return promptDoneMsg{err: err}
		}
		sessions := m.visibleSessions(allSessions)
		workspaceStatus, err := workspace.Snapshot(ctx, m.workdir)
		if err != nil {
			return promptDoneMsg{err: err}
		}
		return newSessionMsg{
			session:   m.draftSession(),
			sessions:  sessions,
			messages:  nil,
			parts:     map[int64][]domain.Part{},
			approvals: nil,
			tasks:     nil,
			workspace: workspaceStatus,
		}
	}
}

func (m Model) sessionPickerCmd() ui.Cmd {
	return func() ui.Msg {
		allSessions, err := m.store.ListSessions(context.Background())
		if err != nil {
			return promptDoneMsg{err: err}
		}
		sessions := m.visibleSessions(allSessions)
		return sessionPickerMsg{sessions: sessions}
	}
}

func (m Model) loadSessionCmd(sessionID int64) ui.Cmd {
	return func() ui.Msg {
		if sessionID == 0 {
			return nil
		}
		ctx := context.Background()
		allSessions, err := m.store.ListSessions(ctx)
		if err != nil {
			return promptDoneMsg{err: err}
		}
		sessions := m.visibleSessions(allSessions)
		var session domain.Session
		for _, item := range sessions {
			if item.ID == sessionID {
				session = item
				break
			}
		}
		if session.ID == 0 {
			return promptDoneMsg{err: fmt.Errorf("session %d not found", sessionID)}
		}
		messages, parts, err := m.store.PartsForSession(ctx, session.ID)
		if err != nil {
			return promptDoneMsg{err: err}
		}
		approvals, err := m.store.PendingApprovals(ctx, session.ID)
		if err != nil {
			return promptDoneMsg{err: err}
		}
		tasks, err := m.store.ListTasks(ctx, session.ID)
		if err != nil {
			return promptDoneMsg{err: err}
		}
		workspaceStatus, err := workspace.Snapshot(ctx, m.workdir)
		if err != nil {
			return promptDoneMsg{err: err}
		}
		return loadMsg{
			sessions:  sessions,
			current:   session,
			messages:  messages,
			parts:     parts,
			approvals: approvals,
			tasks:     tasks,
			workspace: workspaceStatus,
		}
	}
}

func (m Model) agentsRefreshCmd(sessionID int64) ui.Cmd {
	return func() ui.Msg {
		ctx := context.Background()
		if _, err := m.agent.RefreshAgents(ctx, sessionID); err != nil {
			return agentsRefreshMsg{err: err}
		}
		allSessions, err := m.store.ListSessions(ctx)
		if err != nil {
			return agentsRefreshMsg{err: err}
		}
		sessions := m.visibleSessions(allSessions)
		session, err := m.store.GetSession(ctx, sessionID)
		if err != nil {
			return agentsRefreshMsg{err: err}
		}
		messages, parts, err := m.store.PartsForSession(ctx, session.ID)
		if err != nil {
			return agentsRefreshMsg{err: err}
		}
		approvals, err := m.store.PendingApprovals(ctx, session.ID)
		if err != nil {
			return agentsRefreshMsg{err: err}
		}
		tasks, err := m.store.ListTasks(ctx, session.ID)
		if err != nil {
			return agentsRefreshMsg{err: err}
		}
		workspaceStatus, err := workspace.Snapshot(ctx, m.workdir)
		if err != nil {
			return agentsRefreshMsg{err: err}
		}
		return agentsRefreshMsg{
			load: loadMsg{
				sessions:  sessions,
				current:   session,
				messages:  messages,
				parts:     parts,
				approvals: approvals,
				tasks:     tasks,
				workspace: workspaceStatus,
			},
		}
	}
}

func (m Model) forkSessionCmd(sourceSessionID int64) ui.Cmd {
	return func() ui.Msg {
		ctx := context.Background()
		forked, err := m.store.ForkSession(ctx, sourceSessionID)
		if err != nil {
			return forkSessionMsg{err: err}
		}
		allSessions, err := m.store.ListSessions(ctx)
		if err != nil {
			return forkSessionMsg{err: err}
		}
		sessions := m.visibleSessions(allSessions)
		messages, parts, err := m.store.PartsForSession(ctx, forked.ID)
		if err != nil {
			return forkSessionMsg{err: err}
		}
		for _, msg := range messages {
			for i, part := range parts[msg.ID] {
				if part.Kind != domain.PartKindAttachment {
					continue
				}
				meta, err := attachment.DecodeMeta(part.MetaJSON)
				if err != nil {
					return forkSessionMsg{err: err}
				}
				rewritten, err := m.attachmentFiles.CopyToSession(meta, forked.ID)
				if err != nil {
					return forkSessionMsg{err: err}
				}
				raw, err := attachment.EncodeMeta(rewritten)
				if err != nil {
					return forkSessionMsg{err: err}
				}
				if err := m.store.UpdatePartMetaJSON(ctx, part.ID, raw); err != nil {
					return forkSessionMsg{err: err}
				}
				part.MetaJSON = raw
				parts[msg.ID][i] = part
			}
		}
		approvals, err := m.store.PendingApprovals(ctx, forked.ID)
		if err != nil {
			return forkSessionMsg{err: err}
		}
		tasks, err := m.store.ListTasks(ctx, forked.ID)
		if err != nil {
			return forkSessionMsg{err: err}
		}
		workspaceStatus, err := workspace.Snapshot(ctx, m.workdir)
		if err != nil {
			return forkSessionMsg{err: err}
		}
		return forkSessionMsg{
			sourceID: sourceSessionID,
			forkedID: forked.ID,
			load: loadMsg{
				sessions:  sessions,
				current:   forked,
				messages:  messages,
				parts:     parts,
				approvals: approvals,
				tasks:     tasks,
				workspace: workspaceStatus,
			},
		}
	}
}

func (m Model) reloadDetailsCmd() ui.Cmd {
	return func() ui.Msg {
		msg := m.loadSessionCmd(m.currentSession.ID)()
		load, ok := msg.(loadMsg)
		if !ok {
			return msg
		}
		load.preserveBusy = true
		return load
	}
}

func nextEventCmd(events <-chan domain.Event) ui.Cmd {
	return func() ui.Msg {
		evt, ok := <-events
		if !ok {
			return eventMsg{}
		}
		return eventMsg{event: evt, events: events}
	}
}

func Run(cfg config.Config, st *store.Store, a *agent.Engine, mode StartupMode, debug *debugsrv.Recorder) error {
	workdir, err := os.Getwd()
	if err != nil {
		return err
	}
	return RunWithWorkdir(cfg, st, a, mode, debug, workdir, StartupOptions{})
}

func RunWithWorkdir(cfg config.Config, st *store.Store, a *agent.Engine, mode StartupMode, debug *debugsrv.Recorder, workdir string, startupOpts StartupOptions) error {
	model, err := NewWithWorkdir(cfg, st, a, mode, debug, workdir, startupOpts)
	if err != nil {
		return err
	}
	model.syncDebugRuntime()
	p := ui.NewProgram(model, ui.WithAltScreen(), ui.WithoutSignalHandler())
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case s := <-sig:
			switch s {
			case os.Interrupt:
				p.Send(ui.KeyMsg{Type: ui.KeyCtrlC})
			default:
				p.Send(ui.QuitMsg{})
			}
		case <-done:
		}
	}()
	finalModel, err := p.Run()
	if err != nil && !errors.Is(err, ui.ErrInterrupted) {
		return err
	}
	switch typed := finalModel.(type) {
	case Model:
		fmt.Println(typed.exitSummary())
		return nil
	case *Model:
		fmt.Println(typed.exitSummary())
		return nil
	}
	if errors.Is(err, ui.ErrInterrupted) {
		fmt.Println("Exited koder.")
		return nil
	}
	return nil
}

func (m Model) exitSummary() string {
	if m.currentSession.ID <= 0 {
		return "Exited koder."
	}
	title := strings.TrimSpace(m.currentSession.Title)
	if title == "" {
		title = "untitled session"
	}
	return fmt.Sprintf("Closed session %d \"%s\" with %d messages.", m.currentSession.ID, title, len(m.messages))
}

func timestamp(t time.Time, enabled bool) string {
	if !enabled || t.IsZero() {
		return ""
	}
	return t.Format("15:04:05")
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-1] + "…"
}

func formatTokens(value int) string {
	switch {
	case value >= 1_000_000:
		return fmt.Sprintf("%.1fm", float64(value)/1_000_000)
	case value >= 1_000:
		return fmt.Sprintf("%.1fk", float64(value)/1_000)
	default:
		return strconv.Itoa(value)
	}
}

func blankAsDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func mapsCloneToolStates(src map[domain.ToolKind]bool) map[domain.ToolKind]bool {
	dst := make(map[domain.ToolKind]bool, len(src))
	for kind, enabled := range src {
		dst[kind] = enabled
	}
	return dst
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (m Model) UpdateLoad(msg loadMsg) Model {
	m.sessions = make([]domain.Session, 0, len(msg.sessions))
	for _, session := range msg.sessions {
		m.sessions = append(m.sessions, m.normalizeSessionToolStates(session))
	}
	m.currentSession = m.normalizeSessionToolStates(msg.current)
	m.messages = msg.messages
	m.parts = msg.parts
	m.approvals = msg.approvals
	m.tasks = msg.tasks
	m.workspace = msg.workspace
	m.resetComposerHistory()
	m.approvalButtons.Index = 0
	m.draftAttachments = nil
	m.draftReferences = nil
	m.closePicker()
	m.closeSessionDialog()
	m.closePreferencesDialog()
	m.closeToolsDialog()
	m.closeConnectDialog()
	m.closeDisconnectDialog()
	m.closeModelDialog()
	m.closeAgentsModal()
	m.agentsDrift = m.currentSession.ProjectChecksum != "" &&
		m.workspace.AgentsChecksum != "" &&
		m.currentSession.ProjectChecksum != m.workspace.AgentsChecksum
	m.transcriptCache = nil
	m.ensureRetainedTranscript().Clear()
	m.transcriptDirty = true
	m.refreshViewport()
	return m
}

func (m *Model) handleLocalCommand(prompt string) (ui.Model, ui.Cmd, bool) {
	trimmed := strings.TrimSpace(prompt)
	switch {
	case trimmed == "/new":
		m.resetComposerInput()
		m.startBusy(busyScopeSidebar, "Creating session…")
		return m, ui.Batch(m.newSessionCmd(), m.spinnerCmdIfNeeded()), true
	case trimmed == "/resume":
		m.resetComposerInput()
		m.startBusy(busyScopeSidebar, "Loading sessions…")
		return m, ui.Batch(m.sessionPickerCmd(), m.spinnerCmdIfNeeded()), true
	case trimmed == "/quit":
		m.resetComposerInput()
		model, cmd := m.quit()
		return model, cmd, true
	case trimmed == "/mouse on":
		m.resetComposerInput()
		m.mouseEnabled = true
		m.status = "Mouse capture enabled"
		return m, func() ui.Msg { return ui.EnableMouseCellMotion() }, true
	case trimmed == "/mouse off":
		m.resetComposerInput()
		m.mouseEnabled = false
		m.status = "Mouse capture disabled"
		return m, func() ui.Msg { return ui.DisableMouse() }, true
	case trimmed == "/compact":
		m.resetComposerInput()
		m.startBusy(busyScopeTranscript, "Compacting session...")
		return m, ui.Batch(m.compactCmd(m.beginActiveOperation()), m.spinnerCmdIfNeeded()), true
	case trimmed == "/connect":
		m.resetComposerInput()
		m.openConnectDialog()
		return m, m.syncWindowTitleCmd(), true
	case trimmed == "/disconnect":
		m.resetComposerInput()
		if len(m.cfg.Providers) == 0 {
			m.status = "No configured providers to disconnect"
			return m, m.syncWindowTitleCmd(), true
		}
		m.openDisconnectDialog()
		return m, m.syncWindowTitleCmd(), true
	case trimmed == "/model":
		m.resetComposerInput()
		providerID := m.activeProviderID()
		if providerID == "" || !m.cfg.HasUsableProvider(providerID) {
			m.status = "Configure a provider first with /connect"
			return m, m.syncWindowTitleCmd(), true
		}
		m.status = fmt.Sprintf("Loading models for %s…", providerID)
		return m, ui.Batch(m.loadModelsCmd(providerID, false), m.syncWindowTitleCmd()), true
	case trimmed == "/theme":
		m.resetComposerInput()
		m.openThemePicker()
		return m, nil, true
	case trimmed == "/skills":
		m.resetComposerInput()
		m.openSkillsPicker()
		return m, m.syncWindowTitleCmd(), true
	case trimmed == "/permissions":
		m.resetComposerInput()
		m.openPermissionsPicker()
		return m, m.syncWindowTitleCmd(), true
	case trimmed == "/preferences":
		m.resetComposerInput()
		m.openPreferencesDialog()
		return m, ui.Batch(spinnerTickCmd(), m.syncWindowTitleCmd()), true
	case trimmed == "/tools":
		m.resetComposerInput()
		m.openToolsDialog()
		return m, m.syncWindowTitleCmd(), true
	case trimmed == "/agents":
		m.resetComposerInput()
		m.openAgentsModal()
		return m, m.syncWindowTitleCmd(), true
	case trimmed == "/agents refresh":
		m.resetComposerInput()
		if m.currentSession.ID == 0 {
			m.status = "No saved session to refresh"
			return m, m.syncWindowTitleCmd(), true
		}
		m.startBusy(busyScopeSidebar, "Refreshing project instructions…")
		return m, ui.Batch(m.agentsRefreshCmd(m.currentSession.ID), m.spinnerCmdIfNeeded()), true
	case trimmed == "/fork":
		m.resetComposerInput()
		if m.currentSession.ID == 0 {
			m.status = "No saved session to fork"
			return m, m.syncWindowTitleCmd(), true
		}
		m.startBusy(busyScopeSidebar, fmt.Sprintf("Forking session %d…", m.currentSession.ID))
		return m, ui.Batch(m.forkSessionCmd(m.currentSession.ID), m.spinnerCmdIfNeeded()), true
	case strings.HasPrefix(trimmed, "/approve "):
		id, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(trimmed, "/approve")), 10, 64)
		if err != nil {
			m.status = fmt.Sprintf("invalid approval id: %v", err)
			return m, nil, true
		}
		m.resetComposerInput()
		m.startBusy(busyScopeTranscript, fmt.Sprintf("Approving approval %d…", id))
		return m, ui.Batch(m.approveCmd(m.beginActiveOperation(), id), m.spinnerCmdIfNeeded()), true
	case strings.HasPrefix(trimmed, "/deny "):
		id, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(trimmed, "/deny")), 10, 64)
		if err != nil {
			m.status = fmt.Sprintf("invalid approval id: %v", err)
			return m, nil, true
		}
		m.resetComposerInput()
		m.startBusy(busyScopeSidebar, fmt.Sprintf("Denying approval %d…", id))
		return m, ui.Batch(m.denyCmd(m.beginActiveOperation(), id), m.spinnerCmdIfNeeded()), true
	case strings.HasPrefix(trimmed, "/"):
		m.status = fmt.Sprintf("unknown command: %s", trimmed)
		return m, nil, true
	default:
		return nil, nil, false
	}
}

func (m Model) approvalPermissionProfileCmd(ctx context.Context, approvalID int64, profile string) ui.Cmd {
	return func() ui.Msg {
		events, err := m.agent.SetPermissionProfileAndReevaluateApproval(ctx, m.currentSession.ID, approvalID, profile)
		return promptDoneMsg{events: events, err: err}
	}
}

func (m *Model) beginActiveOperation() context.Context {
	if m.activeOpCancel != nil {
		m.activeOpCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.activeOpCancel = cancel
	m.interruptArmedAt = time.Time{}
	return ctx
}

func (m *Model) handleInterruptKey() (ui.Model, ui.Cmd) {
	if m.activeOpCancel == nil {
		return m, nil
	}
	now := time.Now()
	if m.interruptArmedAt.IsZero() || now.Sub(m.interruptArmedAt) > 5*time.Second {
		m.interruptArmedAt = now
		m.status = "Press Esc again to interrupt"
		return m, m.syncWindowTitleCmd()
	}
	m.interruptArmedAt = time.Time{}
	m.status = "Interrupting…"
	m.activeOpCancel()
	return m, m.syncWindowTitleCmd()
}

func (m *Model) queueComposerPrompt(mode queuedPromptMode) (ui.Model, ui.Cmd) {
	prompt := strings.TrimSpace(m.composer.Value())
	if prompt == "" && len(m.draftAttachments) == 0 && len(m.draftReferences) == 0 {
		return m, nil
	}
	if strings.HasPrefix(prompt, "/") {
		m.status = "Wait for the current run to finish before using slash commands"
		return m, m.syncWindowTitleCmd()
	}
	m.queuedPrompt = &queuedPrompt{
		Text:        prompt,
		Mode:        mode,
		Attachments: slices.Clone(m.draftAttachments),
		References:  slices.Clone(m.draftReferences),
	}
	m.resetComposerInput()
	m.draftAttachments = nil
	m.draftReferences = nil
	m.status = m.queuedPrompt.statusText()
	return m, m.syncWindowTitleCmd()
}

func (m *Model) queueContinuePrompt() (ui.Model, ui.Cmd) {
	if ok, status := m.canContinue(); !ok {
		m.status = status
		return m, m.syncWindowTitleCmd()
	}
	m.queuedPrompt = &queuedPrompt{Mode: queuedPromptModeContinue}
	m.status = m.queuedPrompt.statusText()
	return m, m.syncWindowTitleCmd()
}

func (m *Model) popQueuedPromptForEditing() (ui.Model, ui.Cmd) {
	if m.queuedPrompt == nil {
		return m, nil
	}
	if m.queuedPrompt.Mode == queuedPromptModeContinue {
		m.queuedPrompt = nil
		m.status = "Removed queued continue"
		return m, m.syncWindowTitleCmd()
	}

	m.syncDraftReferencesFromComposer()
	currentText := strings.TrimSpace(m.composer.Value())
	hasCurrentDraft := currentText != "" || len(m.draftAttachments) > 0 || len(m.draftReferences) > 0
	queued := *m.queuedPrompt
	if hasCurrentDraft {
		m.queuedPrompt = &queuedPrompt{
			Text:        currentText,
			Mode:        queuedPromptModeNormal,
			Attachments: slices.Clone(m.draftAttachments),
			References:  slices.Clone(m.draftReferences),
		}
		m.status = "Swapped queued prompt into composer"
	} else {
		m.queuedPrompt = nil
		m.status = "Restored queued prompt to composer"
	}
	m.setComposerValue(queued.Text)
	m.draftAttachments = slices.Clone(queued.Attachments)
	m.draftReferences = slices.Clone(queued.References)
	return m, m.syncWindowTitleCmd()
}

func (m *Model) dequeuePromptCmd() ui.Cmd {
	if m.queuedPrompt == nil || m.loading {
		return nil
	}
	if len(m.approvals) > 0 {
		return nil
	}
	item := *m.queuedPrompt
	m.queuedPrompt = nil
	if ok, status := m.canSendPrompt(); !ok {
		if item.Mode != queuedPromptModeContinue {
			m.openConnectDialog()
			m.status = status
			m.setComposerValue(item.Text)
			m.draftAttachments = item.Attachments
			m.draftReferences = item.References
			return nil
		}
	}
	m.startBusy(busyScopeTranscript, item.runStatus())
	if item.Mode == queuedPromptModeContinue {
		return ui.Batch(m.continueCmd(m.beginActiveOperation()), m.spinnerCmdIfNeeded())
	}
	m.appendLocalUserPrompt(item.Text, item.Attachments, item.References)
	return m.kickoffPromptCmd(item.Text, item.Attachments, item.References)
}

func (m Model) ensureRuntimeContextWindow(ctx context.Context, session domain.Session) (string, int, bool, error) {
	providerID := strings.TrimSpace(session.ProviderID)
	if providerID != "llamacpp" {
		return "", 0, false, nil
	}
	if m.runtimeCtxChecked != nil && m.runtimeCtxChecked[providerID] {
		if providerCfg, ok := m.cfg.Provider(providerID); ok {
			return providerID, providerCfg.ContextWindow, false, nil
		}
		return providerID, 0, false, nil
	}
	providerCfg, ok := m.cfg.Provider(providerID)
	if !ok {
		return "", 0, false, fmt.Errorf("provider %q not configured", providerID)
	}
	modelID := strings.TrimSpace(session.ModelID)
	if modelID == "" {
		modelID = strings.TrimSpace(providerCfg.DefaultModel)
	}
	contextWindow, err := provider.DetectContextWindow(ctx, providerID, providerCfg, modelID, m.debug)
	if err != nil {
		return providerID, 0, false, err
	}
	if contextWindow > 0 && providerCfg.ContextWindow != contextWindow {
		providerCfg.ContextWindow = contextWindow
		m.cfg.Providers[providerID] = providerCfg
		if err := m.cfg.Save(); err != nil {
			return providerID, 0, false, err
		}
		if m.agent != nil {
			m.agent.UpdateConfig(m.cfg)
		}
	}
	return providerID, contextWindow, true, nil
}

func (m Model) kickoffPromptCmd(prompt string, drafts []attachment.Draft, refs []reference.Draft) ui.Cmd {
	return ui.Tick(time.Millisecond, func(time.Time) ui.Msg {
		return kickoffPromptMsg{
			Prompt:      prompt,
			Attachments: drafts,
			References:  refs,
		}
	})
}

func (m *Model) resetComposerHistory() {
	m.composerHistory = composerHistoryState{}
}

func (m *Model) resetComposerInput() {
	m.composer.Reset()
	m.resetComposerHistory()
	m.updateComposerMenus()
}

func (m *Model) setComposerValue(value string) {
	m.composer.SetValue(value)
	m.composer.SetCursor(len(value))
	m.updateComposerMenus()
}

func (m Model) composerPromptHistory() []string {
	entries := make([]string, 0, len(m.messages))
	for _, msg := range m.messages {
		if msg.Role != domain.MessageRoleUser {
			continue
		}
		if text := strings.TrimSpace(m.messagePromptText(msg.ID, msg.Summary)); text != "" {
			entries = append(entries, text)
		}
	}
	return entries
}

func (m Model) messagePromptText(messageID int64, fallback string) string {
	parts := m.parts[messageID]
	var body strings.Builder
	for _, part := range parts {
		if part.Kind != domain.PartKindText {
			continue
		}
		body.WriteString(part.Body)
	}
	if text := strings.TrimSpace(body.String()); text != "" {
		return text
	}
	return strings.TrimSpace(fallback)
}

func (m *Model) recallComposerHistory(delta int) bool {
	history := m.composerPromptHistory()
	if len(history) == 0 {
		return false
	}
	m.composerHistory.SearchActive = false
	m.composerHistory.SearchIndex = 0
	m.composerHistory.SearchQuery = ""
	if !m.composerHistory.Active {
		if delta > 0 {
			return false
		}
		m.composerHistory.Active = true
		m.composerHistory.Draft = m.composer.Value()
		m.composerHistory.Index = len(history) - 1
		m.setComposerValue(history[m.composerHistory.Index])
		return true
	}
	next := m.composerHistory.Index + delta
	if next < 0 {
		next = 0
	}
	if next >= len(history) {
		m.setComposerValue(m.composerHistory.Draft)
		m.resetComposerHistory()
		return true
	}
	m.composerHistory.Index = next
	m.setComposerValue(history[next])
	return true
}

func (m *Model) openComposerHistorySearch() bool {
	history := m.filteredComposerHistory("")
	if len(history) == 0 {
		m.status = "No prompt history in this session"
		return true
	}
	if !m.composerHistory.SearchActive {
		m.composerHistory.Draft = m.composer.Value()
		m.composerHistory.SearchQuery = strings.TrimSpace(m.composer.Value())
		m.composerHistory.SearchIndex = 0
		m.composerHistory.SearchActive = true
		m.status = "Search prompt history"
		return true
	}
	m.moveComposerHistorySelection(1)
	return true
}

func (m *Model) hasComposerHistoryMenu() bool {
	return m.composerHistory.SearchActive
}

func (m *Model) filteredComposerHistory(query string) []string {
	history := m.composerPromptHistory()
	if len(history) == 0 {
		return nil
	}
	query = strings.TrimSpace(query)
	queryLower := strings.ToLower(query)
	matches := make([]string, 0, len(history))
	for i := len(history) - 1; i >= 0; i-- {
		entry := strings.TrimSpace(history[i])
		if entry == "" {
			continue
		}
		if queryLower != "" && !strings.Contains(strings.ToLower(entry), queryLower) {
			continue
		}
		matches = append(matches, entry)
	}
	return matches
}

func (m *Model) composerHistorySelection() (string, bool) {
	matches := m.filteredComposerHistory(m.composerHistory.SearchQuery)
	if len(matches) == 0 {
		return "", false
	}
	if m.composerHistory.SearchIndex < 0 {
		m.composerHistory.SearchIndex = 0
	}
	if m.composerHistory.SearchIndex >= len(matches) {
		m.composerHistory.SearchIndex = len(matches) - 1
	}
	return matches[m.composerHistory.SearchIndex], true
}

func (m *Model) moveComposerHistorySelection(delta int) {
	matches := m.filteredComposerHistory(m.composerHistory.SearchQuery)
	if len(matches) == 0 {
		m.composerHistory.SearchIndex = 0
		return
	}
	m.composerHistory.SearchIndex += delta
	if m.composerHistory.SearchIndex < 0 {
		m.composerHistory.SearchIndex = 0
	}
	if m.composerHistory.SearchIndex >= len(matches) {
		m.composerHistory.SearchIndex = len(matches) - 1
	}
}

func (m *Model) appendComposerHistoryQuery(fragment string) {
	m.composerHistory.SearchQuery += fragment
	m.composerHistory.SearchIndex = 0
}

func (m *Model) trimComposerHistoryQuery() {
	if m.composerHistory.SearchQuery == "" {
		return
	}
	m.composerHistory.SearchQuery = m.composerHistory.SearchQuery[:len(m.composerHistory.SearchQuery)-1]
	m.composerHistory.SearchIndex = 0
}

func (m *Model) acceptComposerHistorySelection() bool {
	value, ok := m.composerHistorySelection()
	if !ok {
		m.status = "No matching prompt history entry"
		return false
	}
	m.setComposerValue(value)
	m.resetComposerHistory()
	m.status = "Loaded prompt from history"
	return true
}

func (m *Model) cancelComposerHistorySearch() {
	m.resetComposerHistory()
	m.status = "History search cancelled"
}

func (m *Model) finishOperationWithError(err error) (ui.Model, ui.Cmd) {
	if errors.Is(err, context.Canceled) {
		m.stopBusyWithStatus("Interrupted")
		return *m, m.syncWindowTitleCmd()
	}
	m.status = err.Error()
	m.appendLocalAssistantError(err)
	m.stopBusy()
	return *m, m.syncWindowTitleCmd()
}

func (m Model) compactCmd(ctx context.Context) ui.Cmd {
	return func() ui.Msg {
		events, err := m.agent.Compact(ctx, m.currentSession.ID)
		return promptDoneMsg{events: events, err: err}
	}
}

func (m Model) approveCmd(ctx context.Context, approvalID int64) ui.Cmd {
	return func() ui.Msg {
		events, err := m.agent.Approve(ctx, m.currentSession.ID, approvalID)
		return promptDoneMsg{events: events, err: err}
	}
}

func (m Model) denyCmd(ctx context.Context, approvalID int64) ui.Cmd {
	return func() ui.Msg {
		events, err := m.agent.Deny(ctx, m.currentSession.ID, approvalID)
		return promptDoneMsg{events: events, err: err}
	}
}

func (m *Model) quit() (ui.Model, ui.Cmd) {
	m.stopBusyWithStatus("Quitting")
	return m, ui.Quit
}

func (m *Model) appendLocalUserPrompt(prompt string, drafts []attachment.Draft, refs []reference.Draft) {
	now := time.Now().UTC()
	if m.parts == nil {
		m.parts = make(map[int64][]domain.Part)
	}
	messageID := m.nextPendingID()
	m.messages = append(m.messages, domain.Message{
		ID:        messageID,
		SessionID: m.currentSession.ID,
		Role:      domain.MessageRoleUser,
		Summary:   prompt,
		CreatedAt: now,
	})
	var parts []domain.Part
	if strings.TrimSpace(prompt) != "" {
		parts = append(parts, domain.Part{
			ID:        m.nextPendingID(),
			MessageID: messageID,
			Kind:      domain.PartKindText,
			Body:      prompt,
			CreatedAt: now,
		})
	}
	for _, draft := range drafts {
		raw, err := attachment.EncodeMeta(draft.Metadata)
		if err != nil {
			continue
		}
		parts = append(parts, domain.Part{
			ID:        m.nextPendingID(),
			MessageID: messageID,
			Kind:      domain.PartKindAttachment,
			Body:      draft.Name,
			MetaJSON:  raw,
			CreatedAt: now,
		})
	}
	for _, ref := range refs {
		raw, err := reference.EncodeMeta(reference.Metadata{
			Kind:    ref.Kind,
			Path:    ref.Path,
			Display: ref.Display,
			Start:   ref.Start,
			End:     ref.End,
		})
		if err != nil {
			continue
		}
		parts = append(parts, domain.Part{
			ID:        m.nextPendingID(),
			MessageID: messageID,
			Kind:      domain.PartKindReference,
			Body:      ref.Display,
			MetaJSON:  raw,
			CreatedAt: now,
		})
	}
	m.parts[messageID] = parts
	if m.debug != nil {
		m.debug.RecordLifecycle(m.currentSession.ID, "prompt_submitted", prompt, map[string]string{"optimistic": "true"})
	}
	m.transcriptDirty = true
	m.refreshViewport()
}

func (m *Model) appendLocalAssistantError(err error) {
	if err == nil {
		return
	}
	now := time.Now().UTC()
	if m.parts == nil {
		m.parts = make(map[int64][]domain.Part)
	}
	messageID := m.nextPendingID()
	body := "Error: " + strings.TrimSpace(err.Error())
	m.messages = append(m.messages, domain.Message{
		ID:        messageID,
		SessionID: m.currentSession.ID,
		Role:      domain.MessageRoleAssistant,
		Summary:   body,
		CreatedAt: now,
	})
	m.parts[messageID] = []domain.Part{{
		ID:        m.nextPendingID(),
		MessageID: messageID,
		Kind:      domain.PartKindText,
		Body:      body,
		CreatedAt: now,
	}}
	if m.debug != nil {
		m.debug.RecordLifecycle(m.currentSession.ID, "ui_error_appended", err.Error(), nil)
	}
	m.transcriptDirty = true
	m.refreshViewport()
}

func (m Model) clipboardReadText() (string, error) {
	if m.readClipboardText != nil {
		return m.readClipboardText()
	}
	return kclipboard.ReadText()
}

func (m Model) clipboardReadImage() ([]byte, error) {
	if m.readClipboardImage != nil {
		return m.readClipboardImage()
	}
	return kclipboard.ReadImage()
}

func (m Model) clipboardWriteText(text string) error {
	if m.writeClipboardText != nil {
		return m.writeClipboardText(text)
	}
	return kclipboard.WriteText(text)
}

func (m *Model) pasteClipboardText() (ui.Model, ui.Cmd) {
	image, err := m.clipboardReadImage()
	if err == nil && len(image) > 0 {
		draft, err := m.attachmentFiles.ImportClipboardImage(image)
		if err != nil {
			m.status = "Clipboard image paste failed: " + err.Error()
			return m, m.syncWindowTitleCmd()
		}
		m.draftAttachments = append(m.draftAttachments, draft)
		m.status = fmt.Sprintf("Attached image %s", draft.Name)
		return m, m.syncWindowTitleCmd()
	}

	text, err := m.clipboardReadText()
	if err != nil {
		m.status = "Clipboard paste failed: " + err.Error()
		return m, m.syncWindowTitleCmd()
	}
	if text == "" {
		m.status = "Clipboard is empty"
		return m, m.syncWindowTitleCmd()
	}
	if path := m.pastedAttachmentPath(text); path != "" {
		draft, err := m.attachmentFiles.ImportFile(path)
		if err != nil {
			m.status = "Attachment import failed: " + err.Error()
			return m, m.syncWindowTitleCmd()
		}
		m.draftAttachments = append(m.draftAttachments, draft)
		m.status = fmt.Sprintf("Attached %s", draft.Name)
		return m, m.syncWindowTitleCmd()
	}
	m.composer.InsertString(text)
	m.updateComposerMenus()
	m.invalidateFooterCache()
	m.status = "Pasted from clipboard"
	return m, m.syncWindowTitleCmd()
}

func (m *Model) poppedLastDraftAttachment() bool {
	if len(m.draftAttachments) == 0 {
		return false
	}
	last := m.draftAttachments[len(m.draftAttachments)-1]
	m.draftAttachments = m.draftAttachments[:len(m.draftAttachments)-1]
	m.status = fmt.Sprintf("Removed attachment %s", last.Name)
	return true
}

func (m *Model) syncDraftReferencesFromComposer() {
	if len(m.draftReferences) == 0 {
		return
	}
	value := m.composer.Value()
	refs := slices.Clone(m.draftReferences)
	slices.SortFunc(refs, func(a, b reference.Draft) int {
		if a.Start != b.Start {
			return a.Start - b.Start
		}
		return strings.Compare(a.Path, b.Path)
	})
	var synced []reference.Draft
	searchStart := 0
	for _, draft := range refs {
		idx := strings.Index(value[searchStart:], draft.Display)
		if idx < 0 {
			continue
		}
		draft.Start = searchStart + idx
		draft.End = draft.Start + len(draft.Display)
		synced = append(synced, draft)
		searchStart = draft.End
	}
	m.draftReferences = synced
}

func (m Model) pastedAttachmentPath(text string) string {
	path := strings.TrimSpace(text)
	if path == "" || strings.ContainsRune(path, '\n') {
		return ""
	}
	if !filepath.IsAbs(path) && strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err == nil {
			path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return ""
	}
	return path
}

func (m *Model) copyLatestAssistantMessage() (ui.Model, ui.Cmd) {
	text := strings.TrimSpace(m.latestAssistantCopyText())
	if text == "" {
		m.status = "No assistant message to copy"
		return m, m.syncWindowTitleCmd()
	}
	if err := m.clipboardWriteText(text); err != nil {
		m.status = "Clipboard copy failed: " + err.Error()
		return m, m.syncWindowTitleCmd()
	}
	m.status = "Copied last assistant message"
	return m, m.syncWindowTitleCmd()
}

func (m Model) latestAssistantCopyText() string {
	for i := len(m.messages) - 1; i >= 0; i-- {
		msg := m.messages[i]
		if msg.Role != domain.MessageRoleAssistant {
			continue
		}
		body := strings.TrimSpace(m.renderMessageParts(m.parts[msg.ID]))
		if body == "" {
			body = strings.TrimSpace(msg.Summary)
		}
		if body != "" {
			return body
		}
	}
	return ""
}

func (m *Model) nextPendingID() int64 {
	m.pendingPartID--
	if m.pendingPartID == 0 {
		m.pendingPartID = -1
	}
	return m.pendingPartID
}

func (m *Model) startBusy(scope busyScope, status string) {
	m.loading = true
	m.status = status
	m.busy.start(scope, status)
}

func (m *Model) stopBusy() {
	m.loading = false
	m.busy.stop()
	m.activeOpCancel = nil
	m.interruptArmedAt = time.Time{}
}

func (m *Model) stopBusyWithStatus(status string) {
	m.stopBusy()
	m.status = status
}

func (m Model) syncDebugRuntime() {
	if m.debug == nil {
		return
	}
	renderedBlocks := len(m.messages)
	if m.renderTranscriptActivityElement() != nil {
		renderedBlocks++
	}
	m.debug.UpdateRuntime(debugsrv.RuntimeSnapshot{
		DebugAPI:           m.debugAPIAddr(),
		CurrentSession:     m.currentSession.ID,
		SessionTitle:       strings.TrimSpace(m.currentSession.Title),
		ProviderID:         strings.TrimSpace(m.currentSession.ProviderID),
		ModelID:            strings.TrimSpace(m.currentSession.ModelID),
		Status:             strings.TrimSpace(m.status),
		Busy:               m.busy.active,
		BusyStatus:         strings.TrimSpace(m.busy.status),
		OpenDialog:         m.openDialogName(),
		ShowSidebar:        m.showSidebar,
		ShowReasoning:      m.showReasoning,
		ShowSystem:         m.showSystem,
		LastError:          m.currentError(),
		ViewportWidth:      m.viewport.Width,
		ViewportHeight:     m.viewport.Height,
		ViewportYOffset:    m.viewport.YOffset,
		MessageCount:       len(m.messages),
		RenderBlockCount:   renderedBlocks,
		ViewportPreview:    "",
		ViewportContentLen: m.viewport.VisibleSurface().SurfaceHeight(),
	})
}

func (m Model) currentError() string {
	status := strings.TrimSpace(m.status)
	if strings.HasPrefix(status, "Error:") {
		return status
	}
	return ""
}

func (m Model) openDialogName() string {
	switch {
	case m.hasModelDialog():
		return "model"
	case m.hasDisconnectDialog():
		return "disconnect"
	case m.hasConnectDialog():
		return "connect"
	case m.hasSessionDialog():
		return "session"
	case m.hasPreferencesDialog():
		return "preferences"
	case m.hasToolsDialog():
		return "tools"
	case m.hasHelpModal():
		return "help"
	case m.hasLLMPreview():
		return "llm_preview"
	case m.hasPicker():
		return "picker"
	default:
		return ""
	}
}

func (m Model) recordEvent(evt domain.Event) {
	if m.debug == nil {
		return
	}
	m.debug.RecordEvent(m.currentSession.ID, evt)
}

func (m *Model) spinnerCmdIfNeeded() ui.Cmd {
	if !m.busy.spinner.active {
		return nil
	}
	return spinnerTickCmd()
}

func (b busyModel) scopeOrDefault(fallback busyScope) busyScope {
	if b.scope != busyScopeNone {
		return b.scope
	}
	return fallback
}

func (b busyModel) statusOrDefault(fallback string) string {
	if strings.TrimSpace(b.status) != "" {
		return b.status
	}
	return fallback
}

func (m *Model) isWorking() bool {
	return m.busy.transcriptActive()
}

func (m *Model) shouldAnimateSpinner() bool {
	return m.isWorking() || m.hasPreferencesDialog()
}

func (m *Model) canSendPrompt() (bool, string) {
	session := m.draftSession()
	if strings.TrimSpace(session.ProviderID) == "" {
		return false, "Configure a provider first with /connect"
	}
	if !m.cfg.HasUsableProvider(session.ProviderID) {
		return false, fmt.Sprintf("Provider %q is not configured; use /connect", session.ProviderID)
	}
	if strings.TrimSpace(session.ModelID) == "" {
		return false, "Select a default model with /connect before sending prompts"
	}
	for _, draft := range m.draftAttachments {
		kind := attachment.ClassifyMIME(draft.MIME)
		supported, err := m.capabilityStore().SupportsAttachment(session.ProviderID, providerCfgForDraft(m.cfg, session.ProviderID), session.ModelID, kind)
		if err != nil {
			return false, err.Error()
		}
		if supported {
			continue
		}
		return false, fmt.Sprintf("%s does not support %s attachments", session.ModelID, kind)
	}
	return true, ""
}

func (m *Model) workingIndicator() string {
	if !m.busy.spinner.active {
		return ""
	}
	return ui.SpinnerFrame(m.cfg.UI.Spinner, m.busy.spinner.frame)
}

func (m Model) windowTitle() string {
	title := strings.TrimSpace(m.currentSession.Title)
	switch {
	case title != "":
	case m.currentSession.ID > 0:
		title = fmt.Sprintf("Session #%d", m.currentSession.ID)
	default:
		title = "New Session"
	}
	if m.busy.spinner.active {
		spinner := ui.SpinnerFrame(m.cfg.UI.Spinner, m.busy.spinner.frame)
		if strings.TrimSpace(spinner) == "" {
			spinner = ui.SpinnerFrame(config.Default().UI.Spinner, 0)
		}
		return fmt.Sprintf("%sK %s", spinner, title)
	}
	return fmt.Sprintf("K %s", title)
}

func (m Model) syncWindowTitleCmd() ui.Cmd {
	return ui.SetWindowTitle(m.windowTitle())
}

func (m Model) draftSession() domain.Session {
	providerID := m.currentSession.ProviderID
	if providerID == "" {
		providerID = m.cfg.DefaultProvider
	}
	modelID := m.currentSession.ModelID
	if modelID == "" {
		modelID = m.cfg.DefaultModel
	}
	profile := m.currentSession.PermissionProfile
	if profile == "" {
		profile = m.cfg.Permissions.Profile
	}
	now := time.Now().UTC()
	return domain.Session{
		ID:                0,
		Title:             "New Session",
		ProviderID:        providerID,
		ModelID:           modelID,
		PermissionProfile: profile,
		ToolStates:        m.sessionToolStates(),
		CWD:               m.workdir,
		ProjectRoot:       m.currentProjectRoot(),
		CreatedAt:         now,
		UpdatedAt:         now,
	}
}

func (m Model) persistDraftSession(ctx context.Context) (domain.Session, error) {
	session, err := m.store.CreateSession(ctx, "New Session", m.draftSession().ProviderID, m.draftSession().ModelID, nil)
	if err != nil {
		return domain.Session{}, err
	}
	if err := m.store.UpdateSessionWorkspace(ctx, session.ID, m.draftSession().CWD, m.draftSession().ProjectRoot); err != nil {
		return domain.Session{}, err
	}
	if err := m.store.SetSessionPermissionProfile(ctx, session.ID, m.draftSession().PermissionProfile); err != nil {
		return domain.Session{}, err
	}
	if err := m.store.SetSessionToolStates(ctx, session.ID, m.draftSession().ToolStates); err != nil {
		return domain.Session{}, err
	}
	allSessions, err := m.store.ListSessions(ctx)
	if err != nil {
		return domain.Session{}, err
	}
	sessions := m.visibleSessions(allSessions)
	for _, item := range sessions {
		if item.ID == session.ID {
			return item, nil
		}
	}
	return session, nil
}

func (m Model) visibleSessions(sessions []domain.Session) []domain.Session {
	if m.startupOptions.ShowAllSessions {
		return sessions
	}
	filtered := make([]domain.Session, 0, len(sessions))
	for _, session := range sessions {
		if m.sessionMatchesWorkdir(session) {
			filtered = append(filtered, session)
		}
	}
	return filtered
}

func (m Model) sessionMatchesWorkdir(session domain.Session) bool {
	return normalizedSessionCWD(session) == normalizedSessionPath(m.workdir)
}

func normalizedSessionCWD(session domain.Session) string {
	return normalizedSessionPath(session.CWD)
}

func normalizedSessionPath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	return filepath.Clean(trimmed)
}

func (m *Model) currentComposerQueries() composerQueryState {
	revision := m.composer.Revision()
	if m.composerQueries.revision == revision {
		return m.composerQueries
	}
	state := composerQueryState{revision: revision}
	if query, ok := slashQueryFromComposer(m.composer); ok {
		state.slashQuery = query
		state.hasSlashQuery = true
	}
	if query, start, ok := skillQueryFromComposer(m.composer); ok {
		state.skillQuery = query
		state.skillStart = start
		state.hasSkillQuery = true
	}
	if query, start, end, pathMode, ok := mentionQueryFromComposer(m.composer); ok {
		state.mentionQuery = query
		state.mentionStart = start
		state.mentionEnd = end
		state.mentionPathMode = pathMode
		state.hasMentionQuery = true
	}
	m.composerQueries = state
	return state
}

func (m *Model) updateComposerMenus() {
	queries := m.currentComposerQueries()
	if queries.hasSlashQuery {
		m.slashMatches = matchingSlashCommands(queries.slashQuery)
		if len(m.slashMatches) == 0 {
			m.slashIndex = 0
		} else if m.slashIndex >= len(m.slashMatches) {
			m.slashIndex = len(m.slashMatches) - 1
		}
	} else {
		m.slashMatches = nil
		m.slashIndex = 0
	}

	if queries.hasSkillQuery {
		m.skillMatches = matchingSkills(m.workdir, queries.skillQuery)
		if len(m.skillMatches) == 1 && strings.EqualFold(m.skillMatches[0].Name, queries.skillQuery) {
			m.skillMatches = nil
			m.skillIndex = 0
		} else if len(m.skillMatches) == 0 {
			m.skillIndex = 0
		} else if m.skillIndex >= len(m.skillMatches) {
			m.skillIndex = len(m.skillMatches) - 1
		}
	} else {
		m.skillMatches = nil
		m.skillIndex = 0
	}

	if queries.hasMentionQuery {
		if queries.mentionPathMode {
			m.mentionMatches, _ = reference.PathCompletions(m.workdir, queries.mentionQuery, 8)
		} else {
			if m.mentionCatalog == nil {
				m.mentionCatalog, _ = reference.Entries(m.workdir)
			}
			m.mentionMatches = reference.Search(m.mentionCatalog, queries.mentionQuery, 8)
		}
		if len(m.mentionMatches) == 0 {
			m.mentionIndex = 0
		} else if m.mentionIndex >= len(m.mentionMatches) {
			m.mentionIndex = len(m.mentionMatches) - 1
		}
	} else {
		m.mentionMatches = nil
		m.mentionIndex = 0
	}
	m.syncDraftReferencesFromComposer()
}

func (m *Model) hasSlashMenu() bool {
	return len(m.slashMatches) > 0
}

func (m *Model) hasSkillMenu() bool {
	return len(m.skillMatches) > 0
}

func (m *Model) hasMentionMenu() bool {
	return len(m.mentionMatches) > 0
}

func (m *Model) acceptSlashSelection() {
	if len(m.slashMatches) == 0 {
		return
	}
	selected := m.slashMatches[m.slashIndex]
	next := selected.Name
	if selected.NeedsArgs {
		next = selected.Autocomplete
	}
	m.composer.SetValue(next)
	m.composer.SetCursor(len(next))
	m.updateComposerMenus()
}

func (m *Model) executeSelectedSlashCommand() (ui.Model, ui.Cmd, bool) {
	if len(m.slashMatches) == 0 {
		return nil, nil, false
	}
	selected := m.slashMatches[m.slashIndex]
	if selected.NeedsArgs {
		return nil, nil, false
	}
	m.composer.SetValue(selected.Name)
	m.composer.SetCursor(len(selected.Name))
	m.updateComposerMenus()
	return m.handleLocalCommand(selected.Name)
}

func (m *Model) acceptSkillSelection() {
	if len(m.skillMatches) == 0 {
		return
	}
	selected := m.skillMatches[m.skillIndex]
	queries := m.currentComposerQueries()
	if !queries.hasSkillQuery {
		return
	}
	value := m.composer.Value()
	next := value[:queries.skillStart] + "$" + selected.Name
	m.composer.SetValue(next)
	m.composer.SetCursor(len(next))
	m.updateComposerMenus()
}

func (m *Model) acceptMentionSelection() {
	if len(m.mentionMatches) == 0 {
		return
	}
	selected := m.mentionMatches[m.mentionIndex]
	queries := m.currentComposerQueries()
	if !queries.hasMentionQuery {
		return
	}
	value := m.composer.Value()
	display := reference.DisplayToken(selected.Path)
	next := value[:queries.mentionStart] + display + value[queries.mentionEnd:]
	m.composer.SetValue(next)
	m.composer.SetCursor(queries.mentionStart + len(display))
	m.draftReferences = append(m.draftReferences, reference.Draft{
		Kind:    selected.Kind,
		Path:    selected.Path,
		Display: display,
	})
	m.updateComposerMenus()
	m.status = fmt.Sprintf("Inserted %s", display)
}

func (m *Model) renderSlashMenu() string {
	if element := m.renderSlashMenuElement(); element != nil {
		return m.renderElementText(element, 0, 0)
	}
	return ""
}

func (m *Model) renderSlashMenuElement() ui.Element {
	if len(m.slashMatches) == 0 {
		return nil
	}
	start := 0
	if m.slashIndex >= 6 {
		start = m.slashIndex - 5
	}
	end := min(len(m.slashMatches), start+6)
	var items []ui.MenuItem
	for idx := start; idx < end; idx++ {
		item := m.slashMatches[idx]
		items = append(items, ui.MenuItem{Title: item.Name, Description: item.Description})
	}
	selected := m.slashIndex - start
	return ui.SlashMenu{Title: "Commands", Items: items, Selected: selected}
}

func (m *Model) renderSkillMenu() string {
	if element := m.renderSkillMenuElement(); element != nil {
		return m.renderElementText(element, 0, 0)
	}
	return ""
}

func (m *Model) renderSkillMenuElement() ui.Element {
	if len(m.skillMatches) == 0 {
		return nil
	}
	start := 0
	if m.skillIndex >= 6 {
		start = m.skillIndex - 5
	}
	end := min(len(m.skillMatches), start+6)
	var items []ui.MenuItem
	for idx := start; idx < end; idx++ {
		item := m.skillMatches[idx]
		items = append(items, ui.MenuItem{
			Title:       "$" + item.Name,
			Description: blankAsDash(item.Description),
		})
	}
	selected := m.skillIndex - start
	return ui.SlashMenu{Title: "Skills", Items: items, Selected: selected}
}

func (m *Model) renderMentionMenu() string {
	if element := m.renderMentionMenuElement(); element != nil {
		return m.renderElementText(element, 0, 0)
	}
	return ""
}

func (m *Model) renderMentionMenuElement() ui.Element {
	if len(m.mentionMatches) == 0 {
		return nil
	}
	start := 0
	if m.mentionIndex >= 6 {
		start = m.mentionIndex - 5
	}
	end := min(len(m.mentionMatches), start+6)
	var items []ui.MenuItem
	for idx := start; idx < end; idx++ {
		item := m.mentionMatches[idx]
		items = append(items, ui.MenuItem{
			Title:       reference.DisplayToken(item.Path),
			Description: item.Description,
		})
	}
	selected := m.mentionIndex - start
	return ui.SlashMenu{Title: "References", Items: items, Selected: selected}
}

func (m *Model) renderComposerHistoryMenu() string {
	if element := m.renderComposerHistoryMenuElement(); element != nil {
		return m.renderElementText(element, 0, 0)
	}
	return ""
}

func (m *Model) renderComposerHistoryMenuElement() ui.Element {
	if !m.hasComposerHistoryMenu() {
		return nil
	}
	matches := m.filteredComposerHistory(m.composerHistory.SearchQuery)
	width := max(48, min(88, m.composerWidth()))
	var items []ui.MenuItem
	if len(matches) == 0 {
		return ui.HistoryMenu{
			Palette: m.palette,
			Query:   m.composerHistory.SearchQuery,
			Width:   width,
		}
	} else {
		start := 0
		if m.composerHistory.SearchIndex >= 6 {
			start = m.composerHistory.SearchIndex - 5
		}
		end := min(len(matches), start+6)
		for idx := start; idx < end; idx++ {
			entry := matches[idx]
			items = append(items, ui.MenuItem{
				Title:       firstHistoryLine(entry),
				Description: historySummary(entry),
			})
		}
		return ui.HistoryMenu{
			Palette:  m.palette,
			Query:    m.composerHistory.SearchQuery,
			Items:    items,
			Selected: m.composerHistory.SearchIndex - start,
			Width:    width,
		}
	}
}

func firstHistoryLine(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}
	lines := strings.Split(input, "\n")
	return strings.TrimSpace(lines[0])
}

func historySummary(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}
	lines := strings.Split(input, "\n")
	if len(lines) == 1 {
		return input
	}
	summary := strings.TrimSpace(strings.Join(lines[1:], " "))
	if summary == "" {
		return input
	}
	return summary
}

func (m *Model) renderPicker() string {
	if element := m.renderPickerElement(); element != nil {
		return m.renderElementText(element, 80, 0)
	}
	return ""
}

func (m *Model) renderPickerElement() ui.Element {
	if !m.hasPicker() {
		return nil
	}
	return m.picker.dialog
}

func (m *Model) renderSessionDialog() string {
	if element := m.renderSessionDialogElement(); element != nil {
		width := 112
		if m.width > 0 {
			width = min(124, max(96, m.width-8))
		}
		return m.renderElementText(element, width, 0)
	}
	return ""
}

func (m *Model) renderSessionDialogElement() ui.Element {
	if !m.hasSessionDialog() {
		return nil
	}
	return m.sessionDialog
}

func (m *Model) renderPreferencesDialog() string {
	if element := m.renderPreferencesDialogElement(); element != nil {
		width := 86
		if m.width > 0 {
			width = min(100, max(74, m.width-8))
		}
		return m.renderElementText(element, width, 0)
	}
	return ""
}

func (m *Model) renderPreferencesDialogElement() ui.Element {
	if !m.hasPreferencesDialog() {
		return nil
	}
	return m.preferences
}

func (m *Model) renderToolsDialog() string {
	if element := m.renderToolsDialogElement(); element != nil {
		width := 90
		if m.width > 0 {
			width = min(100, max(76, m.width-8))
		}
		return m.renderElementText(element, width, 0)
	}
	return ""
}

func (m *Model) renderToolsDialogElement() ui.Element {
	if !m.hasToolsDialog() {
		return nil
	}
	return m.toolsDialog
}

func (m *Model) renderConnectDialog() string {
	if element := m.renderConnectDialogElement(); element != nil {
		width := 88
		if m.width > 0 {
			width = min(104, max(76, m.width-8))
		}
		return m.renderElementText(element, width, 0)
	}
	return ""
}

func (m *Model) renderConnectDialogElement() ui.Element {
	if !m.hasConnectDialog() {
		return nil
	}
	return m.connectDialog
}

func (m *Model) renderDisconnectDialog() string {
	if element := m.renderDisconnectDialogElement(); element != nil {
		width := 84
		if m.width > 0 {
			width = min(96, max(72, m.width-8))
		}
		return m.renderElementText(element, width, 0)
	}
	return ""
}

func (m *Model) renderDisconnectDialogElement() ui.Element {
	if !m.hasDisconnectDialog() {
		return nil
	}
	return m.disconnectDialog
}

func (m *Model) renderModelDialog() string {
	if element := m.renderModelDialogElement(); element != nil {
		width := 84
		if m.width > 0 {
			width = min(96, max(72, m.width-8))
		}
		return m.renderElementText(element, width, 0)
	}
	return ""
}

func (m *Model) renderModelDialogElement() ui.Element {
	if !m.hasModelDialog() {
		return nil
	}
	return m.modelDialog
}

func (m *Model) handleSessionDialogKey(msg ui.KeyMsg) ui.Cmd {
	if !m.hasSessionDialog() {
		return nil
	}
	action := m.sessionDialog.Update(msg)
	switch action.Kind {
	case dialogs.SessionDialogActionSelect:
		m.startBusy(busyScopeSidebar, fmt.Sprintf("Resuming session %d…", action.SessionID))
		return ui.Batch(m.loadSessionCmd(action.SessionID), m.spinnerCmdIfNeeded())
	case dialogs.SessionDialogActionCancel:
		m.startBusy(busyScopeSidebar, "Creating session…")
		return ui.Batch(m.newSessionCmd(), m.spinnerCmdIfNeeded())
	default:
		return nil
	}
}

func (m *Model) handlePreferencesKey(msg ui.KeyMsg) ui.Cmd {
	if !m.hasPreferencesDialog() {
		return nil
	}
	action := m.preferences.Update(msg)
	switch action.Kind {
	case dialogs.PreferencesActionChanged:
		cmd, err := m.applyUIConfig(action.UI, false)
		if err != nil {
			m.status = fmt.Sprintf("preferences preview failed: %v", err)
			return m.syncWindowTitleCmd()
		}
		return ui.Batch(cmd, m.syncWindowTitleCmd())
	case dialogs.PreferencesActionApply:
		cmd, err := m.applyUIConfig(action.UI, true)
		if err != nil {
			m.status = fmt.Sprintf("preferences save failed: %v", err)
			return m.syncWindowTitleCmd()
		}
		m.closePreferencesDialog()
		m.status = "Preferences saved"
		return ui.Batch(cmd, m.syncWindowTitleCmd())
	case dialogs.PreferencesActionCancel:
		cmd, err := m.applyUIConfig(action.UI, false)
		if err != nil {
			m.status = fmt.Sprintf("preferences restore failed: %v", err)
			return m.syncWindowTitleCmd()
		}
		m.closePreferencesDialog()
		m.status = "Preferences cancelled"
		return ui.Batch(cmd, m.syncWindowTitleCmd())
	default:
		return nil
	}
}

func (m *Model) handleToolsDialogKey(msg ui.KeyMsg) ui.Cmd {
	if !m.hasToolsDialog() {
		return nil
	}
	action := m.toolsDialog.Update(msg)
	switch action.Kind {
	case dialogs.ToolsDialogActionApply:
		if err := m.applySessionToolStates(action.States); err != nil {
			m.status = err.Error()
			return m.syncWindowTitleCmd()
		}
		m.closeToolsDialog()
		m.status = "Session tools updated"
		return m.syncWindowTitleCmd()
	case dialogs.ToolsDialogActionCancel:
		m.closeToolsDialog()
		m.status = "Tool selection cancelled"
		return m.syncWindowTitleCmd()
	default:
		return nil
	}
}

func (m *Model) handleConnectDialogKey(msg ui.KeyMsg) ui.Cmd {
	if !m.hasConnectDialog() {
		return nil
	}
	action := m.connectDialog.Update(msg)
	switch action.Kind {
	case dialogs.ProviderConnectActionTest:
		m.connectDialog.SetStatus("Testing connection…")
		return ui.Batch(m.probeProviderCmd(action.Draft), m.syncWindowTitleCmd())
	case dialogs.ProviderConnectActionSave:
		if err := m.saveProviderDraft(action.Draft); err != nil {
			m.connectDialog.SetStatusError("Save failed: " + err.Error())
			m.status = err.Error()
			return m.syncWindowTitleCmd()
		}
		m.closeConnectDialog()
		m.status = fmt.Sprintf("Connected provider %s", action.Draft.ProviderID)
		return ui.Batch(m.loadModelsCmd(action.Draft.ProviderID, true), m.syncWindowTitleCmd())
	case dialogs.ProviderConnectActionCancel:
		m.closeConnectDialog()
		m.status = "Provider connect cancelled"
		return m.syncWindowTitleCmd()
	default:
		return nil
	}
}

func (m *Model) handleDisconnectDialogKey(msg ui.KeyMsg) ui.Cmd {
	if !m.hasDisconnectDialog() {
		return nil
	}
	action := m.disconnectDialog.Update(msg)
	switch action.Kind {
	case dialogs.DisconnectDialogActionSelect:
		if err := m.disconnectProvider(action.ProviderID); err != nil {
			m.status = err.Error()
			return m.syncWindowTitleCmd()
		}
		m.closeDisconnectDialog()
		m.status = fmt.Sprintf("Disconnected provider %s", action.ProviderID)
		m.invalidateTranscript()
		m.refreshViewport()
		return m.syncWindowTitleCmd()
	case dialogs.DisconnectDialogActionCancel:
		m.closeDisconnectDialog()
		m.status = "Provider disconnect cancelled"
		return m.syncWindowTitleCmd()
	default:
		return nil
	}
}

func (m *Model) handleModelDialogKey(msg ui.KeyMsg) ui.Cmd {
	if !m.hasModelDialog() {
		return nil
	}
	action := m.modelDialog.Update(msg)
	switch action.Kind {
	case dialogs.ModelDialogActionSelect:
		if err := m.selectModel(action.ModelID); err != nil {
			m.status = err.Error()
			return m.syncWindowTitleCmd()
		}
		m.closeModelDialog()
		m.status = fmt.Sprintf("Selected model %s", action.ModelID)
		m.invalidateTranscript()
		m.refreshViewport()
		return m.syncWindowTitleCmd()
	case dialogs.ModelDialogActionCancel:
		m.closeModelDialog()
		m.status = "Model selection cancelled"
		return m.syncWindowTitleCmd()
	default:
		return nil
	}
}

func (m *Model) hasApprovalPrompt() bool {
	return !m.loading && len(m.approvals) > 0
}

func (m *Model) submitApprovalChoice(approve bool) (ui.Model, ui.Cmd) {
	if !m.hasApprovalPrompt() {
		return m, nil
	}
	id := m.approvals[0].ID
	if approve {
		m.startBusy(busyScopeTranscript, fmt.Sprintf("Approving approval %d…", id))
		return m, ui.Batch(m.approveCmd(m.beginActiveOperation(), id), m.spinnerCmdIfNeeded())
	}
	m.startBusy(busyScopeSidebar, fmt.Sprintf("Denying approval %d…", id))
	return m, ui.Batch(m.denyCmd(m.beginActiveOperation(), id), m.spinnerCmdIfNeeded())
}

func (m *Model) activateApprovalButton(index int) (ui.Model, ui.Cmd) {
	switch index {
	case 0:
		return m.submitApprovalChoice(true)
	case 1:
		m.openApprovalPermissionsPicker()
		return m, m.syncWindowTitleCmd()
	default:
		return m.submitApprovalChoice(false)
	}
}

func (m *Model) renderApprovalPrompt() string {
	if element := m.renderApprovalPromptElement(); element != nil {
		return m.renderElementText(element, 0, 0)
	}
	return ""
}

func (m *Model) renderApprovalPromptElement() ui.Element {
	if !m.hasApprovalPrompt() {
		return nil
	}
	m.ensureApprovalButtons()
	return ui.ToolRunDock{
		Palette: m.palette,
		Run:     m.approvalToolRun(m.approvals[0]),
		Buttons: m.approvalButtonRow(),
		Hints:   "enter select  tab switch  p permissions  y approve  n deny",
	}
}

func (m *Model) ensureApprovalButtons() {
	if len(m.approvalButtons.Buttons) != 0 {
		return
	}
	index := m.approvalButtons.Index
	m.approvalButtons = ui.ButtonRow{
		Buttons: []ui.Button{
			{ID: "approve", Label: "Approve", Hotkey: 'a', Primary: true},
			{ID: "permissions", Label: "Permissions", Hotkey: 'p'},
			{ID: "deny", Label: "Deny", Hotkey: 'd'},
		},
		Index: index,
		Align: ui.HorizontalAlignRight,
	}
}

func (m *Model) approvalButtonRow() ui.ButtonRow {
	buttons := m.approvalButtons
	buttons.Align = ui.HorizontalAlignRight
	run := m.approvalToolRun(m.approvals[0])
	title := run.Title + "  " + run.StatusLabel()
	width := lipgloss.Width(title)
	if subtitle := strings.TrimSpace(run.Subtitle); subtitle != "" {
		width = max(width, lipgloss.Width(subtitle))
	}
	if preview := firstNonEmptyString(strings.TrimSpace(run.Preview), strings.TrimSpace(run.Output), strings.TrimSpace(run.ErrorText)); preview != "" {
		for _, line := range strings.Split(preview, "\n") {
			width = max(width, lipgloss.Width(line))
		}
	}
	width = max(width, ui.RenderSurface(&ui.Context{Palette: m.palette}, buttons, 0, 0).SurfaceWidth())
	width = max(width, lipgloss.Width("enter select  tab switch  p permissions  y approve  n deny"))
	buttons.Width = width
	return buttons
}

func internalSlashCommands() []slashCommand {
	return []slashCommand{
		{Name: "/agents", Description: "Show resolved project instructions"},
		{Name: "/agents refresh", Description: "Re-resolve project instructions"},
		{Name: "/compact", Description: "Summarize old context"},
		{Name: "/connect", Description: "Configure a provider"},
		{Name: "/disconnect", Description: "Remove a configured provider"},
		{Name: "/fork", Description: "Branch from the current session"},
		{Name: "/model", Description: "Choose a model for the active provider"},
		{Name: "/new", Description: "Start a new session"},
		{Name: "/mouse", Description: "Toggle mouse capture", NeedsArgs: true, Autocomplete: "/mouse "},
		{Name: "/permissions", Description: "Pick a built-in permission mode"},
		{Name: "/preferences", Description: "Open preferences"},
		{Name: "/quit", Description: "Quit koder"},
		{Name: "/resume", Description: "Resume a saved session"},
		{Name: "/skills", Description: "Insert a discovered skill mention"},
		{Name: "/tools", Description: "Enable or disable tools for this session"},
		{Name: "/theme", Description: "Choose a color theme"},
	}
}

func (m *Model) permissionProfile() string {
	if strings.TrimSpace(m.currentSession.PermissionProfile) != "" {
		return m.currentSession.PermissionProfile
	}
	return m.cfg.Permissions.Profile
}

func (m *Model) sessionToolStates() map[domain.ToolKind]bool {
	states := make(map[domain.ToolKind]bool, len(domain.AllToolKinds()))
	for _, kind := range domain.AllToolKinds() {
		enabled := true
		if value, ok := m.cfg.ToolDefaults[kind]; ok {
			enabled = value
		}
		if value, ok := m.currentSession.ToolStates[kind]; ok {
			enabled = value
		}
		states[kind] = enabled
	}
	return states
}

func (m *Model) sessionToolEnabled(kind domain.ToolKind) bool {
	return m.sessionToolStates()[kind]
}

func (m *Model) normalizeSessionToolStates(session domain.Session) domain.Session {
	states := make(map[domain.ToolKind]bool, len(domain.AllToolKinds()))
	for _, kind := range domain.AllToolKinds() {
		enabled := true
		if value, ok := m.cfg.ToolDefaults[kind]; ok {
			enabled = value
		}
		if value, ok := session.ToolStates[kind]; ok {
			enabled = value
		}
		states[kind] = enabled
	}
	session.ToolStates = states
	return session
}

func (m *Model) applySessionToolStates(states map[domain.ToolKind]bool) error {
	next := make(map[domain.ToolKind]bool, len(domain.AllToolKinds()))
	for _, kind := range domain.AllToolKinds() {
		enabled := true
		if value, ok := m.cfg.ToolDefaults[kind]; ok {
			enabled = value
		}
		if value, ok := states[kind]; ok {
			enabled = value
		}
		next[kind] = enabled
	}
	if m.currentSession.ID != 0 && m.store != nil {
		if err := m.store.SetSessionToolStates(context.Background(), m.currentSession.ID, next); err != nil {
			return err
		}
	}
	m.currentSession.ToolStates = next
	for idx := range m.sessions {
		if m.sessions[idx].ID == m.currentSession.ID {
			m.sessions[idx].ToolStates = mapsCloneToolStates(next)
		}
	}
	return nil
}

func (m *Model) renderChangedFile(item workspace.FileStatus) string {
	base := fmt.Sprintf("  %-2s %s", item.Code, truncate(item.Path, 16))
	if item.Additions == 0 && item.Deletions == 0 {
		return base
	}
	added := lipgloss.NewStyle().Foreground(m.palette.DiffAddedText).Render(fmt.Sprintf("+%d", item.Additions))
	deleted := lipgloss.NewStyle().Foreground(m.palette.DiffDeletedText).Render(fmt.Sprintf("-%d", item.Deletions))
	return base + " " + added + " " + deleted
}

func applyComposerTheme(composer *textarea.Model, palette theme.Palette) {
	focused, blurred := textarea.DefaultStyles()
	applyTextareaStyle := func(style *textarea.Style) {
		style.Base = style.Base.
			Background(palette.UserTextBackground).
			Foreground(palette.UserTextForeground)
		style.CursorLine = style.CursorLine.
			Background(palette.UserTextBackground).
			Foreground(palette.UserTextForeground)
		style.Text = style.Text.
			Background(palette.UserTextBackground).
			Foreground(palette.UserTextForeground)
		style.Prompt = style.Prompt.
			Background(palette.UserTextBackground).
			Foreground(palette.UserAccentBar)
		style.Placeholder = style.Placeholder.
			Background(palette.UserTextBackground).
			Foreground(palette.ComposerMutedText)
		style.EndOfBuffer = style.EndOfBuffer.
			Background(palette.UserTextBackground).
			Foreground(palette.ComposerMutedText)
	}
	applyTextareaStyle(&focused)
	applyTextareaStyle(&blurred)
	composer.FocusedStyle = focused
	composer.BlurredStyle = blurred
	composer.Cursor.TextStyle = lipgloss.NewStyle().
		Background(palette.UserTextForeground).
		Foreground(palette.UserTextBackground)
}

func (m *Model) hasPicker() bool {
	return m.picker.visible
}

func (m *Model) hasModalOverlay() bool {
	return m.hasModelDialog() ||
		m.hasDisconnectDialog() ||
		m.hasToolsDialog() ||
		m.hasConnectDialog() ||
		m.hasSessionDialog() ||
		m.hasAgentsModal() ||
		m.hasHelpModal() ||
		m.hasLLMPreview() ||
		m.hasPreferencesDialog() ||
		m.hasPicker()
}

func (m *Model) composerShouldBlink() bool {
	return m.composer.BlinkEnabled && !m.hasModalOverlay() && !m.hasApprovalPrompt()
}

func (m *Model) syncComposerVisibility() {
	beforeFocus := m.composer.Focused()
	beforeCursorVisible := m.composer.CursorVisible()
	shouldFocus := !m.hasModalOverlay() && !m.hasApprovalPrompt() && (beforeFocus || m.composer.BlinkEnabled || m.composerAreaHasContent())
	if shouldFocus {
		if !m.composer.Focused() {
			m.composer.Focus()
		}
		m.syncComposerBlinkTimer()
	} else {
		if m.composer.Focused() {
			m.composer.Blur()
		}
		m.syncComposerBlinkTimer()
	}
	if beforeFocus != m.composer.Focused() || beforeCursorVisible != m.composer.CursorVisible() {
		m.invalidateFooterCache()
	}
}

func (m *Model) syncComposerBlinkTimer() {
	root := m.ensureUIRoot()
	root.StopOwnerTimers(composerBlinkTimerOwner)
	if !m.composerShouldBlink() || !m.composer.Focused() {
		return
	}
	root.StartTimer(composerBlinkTimerOwner, ui.TimerSpec{
		Interval: textarea.BlinkInterval(),
		Repeat:   true,
	})
}

func (m *Model) rootTimerCmd() ui.Cmd {
	root := m.syncUIRoot()
	now := time.Now()
	delay, ok := root.NextTimerDelay(now)
	if !ok {
		m.rootTimerPending = false
		m.rootTimerPendingAt = time.Time{}
		return nil
	}
	dueAt := now.Add(delay)
	if m.rootTimerPending && !m.rootTimerPendingAt.IsZero() && !dueAt.Before(m.rootTimerPendingAt) {
		return nil
	}
	m.rootTimerSeq++
	m.rootTimerPending = true
	m.rootTimerPendingAt = dueAt
	seq := m.rootTimerSeq
	return ui.Tick(delay, func(t time.Time) ui.Msg {
		return rootTimerMsg{At: t, Seq: seq}
	})
}

func (m *Model) buildTranscriptItems() []ui.TranscriptItem {
	if m.currentSession.ID == 0 && len(m.messages) == 0 {
		return []ui.TranscriptItem{{
			Key:     "empty",
			Element: ui.NewCachedElement(ui.Paragraph{Text: "Start by asking a question or type / for commands."}, 1),
		}}
	}
	var items []ui.TranscriptItem
	transcriptBlocks := m.transcriptBlocks()
	for i, block := range transcriptBlocks {
		gap := 0
		if i > 0 {
			gap = renderedSeparatorHeight(m.transcriptSeparator(transcriptBlocks[i-1], block))
		}
		cached := m.cachedTranscriptBlock(block)
		items = append(items, ui.TranscriptItem{
			Key:       m.transcriptBlockCacheKey(block),
			Element:   cached.element,
			GapBefore: gap,
		})
	}
	return items
}

func (m *Model) withRootTimers(cmd ui.Cmd) ui.Cmd {
	m.syncComposerBlinkTimer()
	timerCmd := m.rootTimerCmd()
	if timerCmd == nil {
		return cmd
	}
	return ui.Batch(cmd, timerCmd)
}

func (m *Model) closePicker() {
	m.picker = pickerModel{}
	m.syncComposerVisibility()
}

func (m *Model) hasSessionDialog() bool {
	return m.sessionDialog != nil
}

func (m *Model) closeSessionDialog() {
	m.sessionDialog = nil
	m.syncComposerVisibility()
}

func (m *Model) hasPreferencesDialog() bool {
	return m.preferences != nil
}

func (m *Model) closePreferencesDialog() {
	m.preferences = nil
	m.syncComposerVisibility()
}

func (m *Model) hasToolsDialog() bool {
	return m.toolsDialog != nil
}

func (m *Model) closeToolsDialog() {
	m.toolsDialog = nil
	m.syncComposerVisibility()
}

func (m *Model) hasAgentsModal() bool {
	return m.agentsModal != nil
}

func (m *Model) closeAgentsModal() {
	m.agentsModal = nil
	m.syncComposerVisibility()
}

func (m *Model) hasHelpModal() bool {
	return m.helpModal != nil
}

func (m *Model) closeHelpModal() {
	m.helpModal = nil
	m.syncComposerVisibility()
}

func (m *Model) hasLLMPreview() bool {
	return strings.TrimSpace(m.llmPreviewBody) != ""
}

func (m *Model) closeLLMPreview() {
	m.llmPreviewTitle = ""
	m.llmPreviewBody = ""
	m.llmPreviewYOffset = 0
	m.llmPreviewWidth = 0
	m.llmPreviewHeight = 0
	m.syncComposerVisibility()
}

func (m *Model) hasConnectDialog() bool {
	return m.connectDialog != nil
}

func (m *Model) closeConnectDialog() {
	m.connectDialog = nil
	m.syncComposerVisibility()
}

func (m *Model) hasDisconnectDialog() bool {
	return m.disconnectDialog != nil
}

func (m *Model) closeDisconnectDialog() {
	m.disconnectDialog = nil
	m.syncComposerVisibility()
}

func (m *Model) hasModelDialog() bool {
	return m.modelDialog != nil
}

func (m *Model) closeModelDialog() {
	m.modelDialog = nil
	m.syncComposerVisibility()
}

func (m *Model) openSessionPicker() {
	items := make([]dialogs.SessionItem, 0, len(m.sessions))
	showCWD := m.startupOptions.ShowAllSessions
	for _, session := range m.sessions {
		title := strings.TrimSpace(session.Title)
		if title == "" {
			title = fmt.Sprintf("Session #%d", session.ID)
		}
		description := strings.TrimSpace(session.LastMessage)
		if description == "" {
			description = "No messages yet"
		}
		preview := description
		items = append(items, dialogs.SessionItem{
			SessionID:    "#" + strconv.FormatInt(session.ID, 10),
			CreatedAt:    formatRelativeSessionTime(session.CreatedAt),
			ModifiedAt:   formatRelativeSessionTime(session.UpdatedAt),
			TokenSummary: sessionTokenSummary(m, session.ID),
			Title:        title,
			CWD:          session.CWD,
			Description:  description,
			Preview:      preview,
			Value:        strconv.FormatInt(session.ID, 10),
		})
	}
	dialog := dialogs.NewSessionDialog(items, showCWD)
	m.sessionDialog = &dialog
	m.syncComposerVisibility()
}

func sessionTokenSummary(m *Model, sessionID int64) string {
	if usage, ok := m.sessionUsageSummary(sessionID); ok {
		return fmt.Sprintf("%s/%s", formatTokens(usage.PromptTokens), formatTokens(usage.CompletionTokens))
	}
	return "-/-"
}

func (m *Model) openPreferencesDialog() {
	dialog := dialogs.NewPreferencesDialog(m.cfg.UI, theme.Names())
	m.preferences = &dialog
	m.syncComposerVisibility()
}

func (m *Model) openToolsDialog() {
	items := make([]dialogs.ToolToggleItem, 0, len(domain.AllToolKinds()))
	for _, kind := range domain.AllToolKinds() {
		description := "Enable this tool for the current session"
		label := string(kind)
		if tool, ok := tools.Lookup(kind); ok {
			presentation := tool.PresentationForPreview("")
			if strings.TrimSpace(presentation.Title) != "" {
				label = presentation.Title
			}
			if def, enabled := tool.Definition(tools.Runtime{Workdir: m.workdir}); enabled && strings.TrimSpace(def.Function.Description) != "" {
				description = def.Function.Description
			}
		}
		items = append(items, dialogs.ToolToggleItem{
			Tool:        kind,
			Label:       label,
			Description: description,
			Enabled:     m.sessionToolEnabled(kind),
		})
	}
	dialog := dialogs.NewToolsDialog(items)
	m.toolsDialog = &dialog
	m.syncComposerVisibility()
}

func (m *Model) openConnectDialog() {
	dialog := dialogs.NewConnectDialog(provider.Catalog(), m.cfg.Providers)
	m.connectDialog = &dialog
	m.syncComposerVisibility()
}

func (m *Model) openDisconnectDialog() {
	items := make([]dialogs.ProviderItem, 0, len(m.cfg.Providers))
	ids := make([]string, 0, len(m.cfg.Providers))
	for id := range m.cfg.Providers {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		p := m.cfg.Providers[id]
		title := id
		if strings.TrimSpace(p.Name) != "" {
			title = p.Name
		}
		desc := strings.TrimSpace(p.BaseURL)
		if desc == "" {
			desc = p.Kind
		}
		details := []string{
			fmt.Sprintf("Provider ID: %s", id),
			fmt.Sprintf("Kind:        %s", blankAsDash(p.Kind)),
			fmt.Sprintf("Auth:        %s", blankAsDash(p.AuthMethod)),
			fmt.Sprintf("Base URL:    %s", blankAsDash(p.BaseURL)),
			fmt.Sprintf("Model:       %s", blankAsDash(p.DefaultModel)),
		}
		if id == m.cfg.DefaultProvider {
			details = append(details, "Default:     yes")
		}
		items = append(items, dialogs.ProviderItem{
			ID:          id,
			Title:       title,
			Description: desc,
			Details:     details,
		})
	}
	dialog := dialogs.NewDisconnectDialog(items)
	m.disconnectDialog = &dialog
	m.syncComposerVisibility()
}

func (m *Model) openModelDialog(providerID string, models []domain.Model) {
	current := m.currentSession.ModelID
	if strings.TrimSpace(current) == "" {
		current = m.cfg.DefaultModel
	}
	dialog := dialogs.NewModelDialog(providerID, models, current)
	m.modelDialog = &dialog
	m.syncComposerVisibility()
}

func (m *Model) openAgentsModal() {
	lines := []string{
		fmt.Sprintf("CWD: %s", blankAsDash(m.workdir)),
		fmt.Sprintf("Project root: %s", blankAsDash(m.currentProjectRoot())),
		fmt.Sprintf("Session checksum: %s", blankAsDash(m.currentSession.ProjectChecksum)),
		fmt.Sprintf("Live checksum: %s", blankAsDash(m.workspace.AgentsChecksum)),
		fmt.Sprintf("Live files: %d", m.workspace.AgentsFiles),
		fmt.Sprintf("Generated: %s", formatSessionTime(m.currentSession.AgentsGeneratedAt)),
	}
	if m.agentsDrift {
		lines = append(lines, "Drift: WARNING session checksum differs from workspace")
	} else {
		lines = append(lines, "Drift: clean")
	}
	lines = append(lines, "")
	lines = append(lines, "Conflict Summary")
	summary := strings.TrimSpace(m.currentSession.AgentsSummary)
	if summary == "" {
		summary = "No resolved AGENTS data stored for this session yet."
	}
	lines = append(lines, summary)
	lines = append(lines, "")
	lines = append(lines, "Included Files")
	if len(m.currentSession.AgentsFiles) == 0 {
		lines = append(lines, "none")
	} else {
		for _, item := range m.currentSession.AgentsFiles {
			line := fmt.Sprintf("%s [%s p=%d %s]", item.Path, item.Kind, item.Priority, item.ModTime.Local().Format("2006-01-02 15:04:05"))
			if item.DiscoveredBy != "" {
				line += " <- " + item.DiscoveredBy
			}
			lines = append(lines, line)
		}
	}
	lines = append(lines, "")
	lines = append(lines, "Resolved Text")
	resolved := strings.TrimSpace(m.currentSession.AgentsResolved)
	if resolved == "" {
		resolved = "No resolved AGENTS data stored for this session yet. Send a prompt or run /agents refresh."
	}
	lines = append(lines, resolved)
	modal := ui.Modal{
		Title:       "Resolved AGENTS",
		Subtitle:    fmt.Sprintf("Project root: %s", blankAsDash(m.currentProjectRoot())),
		BodyElement: ui.TextPane{Content: strings.Join(lines, "\n")},
		Footer:      "enter or esc close  /agents refresh recomputes and updates the session snapshot",
		Width:       min(110, max(72, m.width-8)),
	}
	m.agentsModal = &modal
	m.syncComposerVisibility()
}

func (m *Model) renderAgentsModal() string {
	if element := m.renderAgentsModalElement(); element != nil {
		return m.renderElementText(element, min(110, max(72, m.width-8)), 0)
	}
	return ""
}

func (m *Model) renderAgentsModalElement() ui.Element {
	if m.agentsModal == nil {
		return nil
	}
	return *m.agentsModal
}

func (m *Model) openHelpModal() {
	hotkeys := []string{
		"Hotkeys",
		"Alt-H               show or close help",
		"Enter               send prompt or confirm selection",
		"Esc                 cancel dialog or interrupt active run",
		"Tab                 autocomplete, or queue steering while running",
		"Up/Down             browse session prompt history",
		"Alt-Enter           insert newline",
		"Ctrl-V              paste clipboard text",
		"Ctrl-Y              copy last assistant message",
		"Ctrl-R              search prompt history",
		"Ctrl-S              toggle sidebar",
		"Alt-R               toggle reasoning",
		"Alt-P               toggle system output",
		"Alt-O               preview the full next LLM request for the current draft",
		"Ctrl-G              continue",
	}
	commands := []string{
		"Commands",
		"/agents             show resolved AGENTS details",
		"/agents refresh     recompute project instructions",
		"/compact            compact session history",
		"/connect            configure a provider",
		"/disconnect         remove a configured provider",
		"/fork               fork current session",
		"/model              choose a model",
		"/new                create a new session",
		"/permissions        change permission mode",
		"/preferences        open UI preferences",
		"/quit               exit koder",
		"/resume             resume another session",
		"/skills             insert a discovered skill mention",
		"/theme              preview and select theme",
	}
	lines := append([]string{}, hotkeys...)
	lines = append(lines, "")
	lines = append(lines, commands...)
	modal := ui.Modal{
		Title:       "Help",
		BodyElement: ui.TextPane{Content: strings.Join(lines, "\n")},
		Footer:      "Alt-H, Enter, or Esc closes this help dialog",
		Width:       min(104, max(84, m.width-8)),
	}
	m.helpModal = &modal
	m.syncComposerVisibility()
}

func (m *Model) renderHelpModal() string {
	if element := m.renderHelpModalElement(); element != nil {
		return m.renderElementText(element, min(104, max(84, m.width-8)), 0)
	}
	return ""
}

func (m *Model) renderHelpModalElement() ui.Element {
	if m.helpModal == nil {
		return nil
	}
	return *m.helpModal
}

func (m Model) previewLLMRequestCmd(ctx context.Context, prompt string, drafts []attachment.Draft, refs []reference.Draft) ui.Cmd {
	return func() ui.Msg {
		req, err := m.agent.PreviewNextRequest(ctx, m.currentSession, prompt, drafts, refs, m.pendingModelNote)
		if err != nil {
			return llmPreviewMsg{err: err}
		}
		body, err := json.MarshalIndent(req, "", "  ")
		if err != nil {
			return llmPreviewMsg{err: err}
		}
		return llmPreviewMsg{
			title: "Next LLM Request",
			body:  string(body),
		}
	}
}

func (m *Model) openLLMPreview(title string, body string) {
	m.llmPreviewTitle = title
	m.llmPreviewBody = body
	m.llmPreviewYOffset = 0
	m.resizeLLMPreview()
	m.syncComposerVisibility()
}

func (m *Model) resizeLLMPreview() {
	if !m.hasLLMPreview() {
		return
	}
	width := max(40, m.width-4)
	height := max(6, m.height-4)
	bodyWidth := max(20, width-4)
	bodyHeight := max(3, height-4)
	m.llmPreviewWidth = bodyWidth
	m.llmPreviewHeight = bodyHeight
	m.llmPreviewYOffset = min(max(0, m.llmPreviewYOffset), m.llmPreviewMaxOffset())
}

func (m *Model) renderLLMPreview() string {
	if element := m.renderLLMPreviewElement(); element != nil {
		return m.renderElementText(m.centeredModal(element), max(0, m.width), max(0, m.height))
	}
	return ""
}

func (m *Model) renderLLMPreviewElement() ui.Element {
	if !m.hasLLMPreview() {
		return nil
	}
	title := strings.TrimSpace(m.llmPreviewTitle)
	if title == "" {
		title = "Next LLM Request"
	}
	return ui.Modal{
		Title: title,
		BodyElement: ui.ScrollFrame{
			Child:   ui.TextPane{Content: m.llmPreviewBody},
			OffsetY: m.llmPreviewYOffset,
			Width:   m.llmPreviewWidth,
			Height:  m.llmPreviewHeight,
		},
		Footer: "Alt-O, Enter, or Esc closes  •  Use arrows, PgUp/PgDn, Home/End, or wheel to scroll",
		Width:  max(40, m.width-4),
	}
}

func (m Model) currentProjectRoot() string {
	if strings.TrimSpace(m.workspace.ProjectRoot) != "" {
		return m.workspace.ProjectRoot
	}
	if strings.TrimSpace(m.currentSession.ProjectRoot) != "" {
		return m.currentSession.ProjectRoot
	}
	return m.workdir
}

func (m Model) agentsStatusLabel() string {
	if m.workspace.AgentsFiles == 0 {
		return "None"
	}
	if m.currentSession.ProjectChecksum != "" && m.currentSession.ProjectChecksum == m.workspace.AgentsChecksum {
		return "Up to date"
	}
	return "Changed"
}

func (m Model) renderAgentsSidebarStatus() string {
	color := lipgloss.Color("#e06c75")
	switch m.agentsStatusLabel() {
	case "None":
		color = lipgloss.Color("#e0af68")
	case "Up to date":
		color = lipgloss.Color("#98c379")
	}
	return lipgloss.NewStyle().Foreground(color).Render(m.agentsStatusLabel())
}

func (m Model) probeProviderCmd(draft provider.ConnectDraft) ui.Cmd {
	return func() ui.Msg {
		result, err := provider.Probe(context.Background(), draft, m.debug)
		if err == nil {
			result.Models, err = m.capabilityStore().EnrichModels(draft.ProviderID, draft.ToConfig(), result.Models)
		}
		return providerProbeMsg{result: result, err: err}
	}
}

func (m Model) loadModelsCmd(providerID string, postConnect bool) ui.Cmd {
	return func() ui.Msg {
		cfg, ok := m.cfg.Provider(providerID)
		if !ok {
			return modelListMsg{providerID: providerID, err: fmt.Errorf("provider %q not configured", providerID)}
		}
		client, err := provider.New(providerID, cfg, m.debug)
		if err != nil {
			return modelListMsg{providerID: providerID, err: err}
		}
		models, err := client.ListModels(context.Background())
		if err == nil {
			models, err = m.capabilityStore().EnrichModels(providerID, cfg, models)
		}
		return modelListMsg{providerID: providerID, models: models, postConnect: postConnect, err: err}
	}
}

func (m Model) capabilityStore() *provider.CapabilityStore {
	if m.caps != nil {
		return m.caps
	}
	return provider.NewCapabilityStore(m.cfg.StateDir())
}

func providerCfgForDraft(cfg config.Config, providerID string) config.Provider {
	if providerCfg, ok := cfg.Provider(providerID); ok {
		return providerCfg
	}
	return config.Provider{}
}

func (m *Model) saveProviderDraft(draft provider.ConnectDraft) error {
	if err := provider.ValidateDraft(draft); err != nil {
		return err
	}
	if m.cfg.Providers == nil {
		m.cfg.Providers = map[string]config.Provider{}
	}
	next := draft.ToConfig()
	existing, ok := m.cfg.Providers[draft.ProviderID]
	if ok {
		if next.ContextWindow == 0 {
			next.ContextWindow = existing.ContextWindow
		}
		if next.AutoCompactAt == 0 {
			next.AutoCompactAt = existing.AutoCompactAt
		}
		if next.Timeout == 0 {
			next.Timeout = existing.Timeout
		}
		next.Stream = existing.Stream
		next.Disabled = false
	} else {
		if next.ContextWindow == 0 && draft.ProviderID != "llamacpp" {
			next.ContextWindow = 32768
		}
		next.AutoCompactAt = 85
		next.Timeout = 2 * time.Minute
		next.Stream = true
		next.Disabled = false
	}
	if strings.TrimSpace(next.Name) == "" {
		if desc, found := provider.Lookup(draft.ProviderID); found {
			next.Name = desc.Title
		} else {
			next.Name = draft.ProviderID
		}
	}
	m.cfg.Providers[draft.ProviderID] = next
	m.cfg.DefaultProvider = draft.ProviderID
	m.cfg.DefaultModel = draft.Model
	if err := m.cfg.Save(); err != nil {
		return err
	}
	if m.agent != nil {
		m.agent.UpdateConfig(m.cfg)
	}
	if strings.TrimSpace(m.currentSession.ProviderID) == "" || !m.cfg.HasUsableProvider(m.currentSession.ProviderID) {
		m.currentSession.ProviderID = draft.ProviderID
	}
	if strings.TrimSpace(m.currentSession.ModelID) == "" {
		m.currentSession.ModelID = draft.Model
	}
	return nil
}

func (m *Model) disconnectProvider(providerID string) error {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return fmt.Errorf("provider id is required")
	}
	if _, ok := m.cfg.Providers[providerID]; !ok {
		return fmt.Errorf("provider %q is not configured", providerID)
	}
	delete(m.cfg.Providers, providerID)

	nextDefault := strings.TrimSpace(m.cfg.DefaultProvider)
	if nextDefault == providerID || !m.cfg.HasUsableProvider(nextDefault) {
		nextDefault = ""
		ids := make([]string, 0, len(m.cfg.Providers))
		for id := range m.cfg.Providers {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		if len(ids) > 0 {
			nextDefault = ids[0]
		}
	}
	m.cfg.DefaultProvider = nextDefault
	m.cfg.DefaultModel = ""
	if nextDefault != "" {
		if next, ok := m.cfg.Provider(nextDefault); ok {
			m.cfg.DefaultModel = next.DefaultModel
		}
	}
	if err := m.cfg.Save(); err != nil {
		return err
	}
	if m.agent != nil {
		m.agent.UpdateConfig(m.cfg)
	}
	if strings.TrimSpace(m.currentSession.ProviderID) == providerID {
		m.currentSession.ProviderID = m.cfg.DefaultProvider
		m.currentSession.ModelID = m.cfg.DefaultModel
	}
	return nil
}

func (m *Model) selectModel(modelID string) error {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return fmt.Errorf("model id is required")
	}
	providerID := m.activeProviderID()
	if providerID == "" || !m.cfg.HasUsableProvider(providerID) {
		return fmt.Errorf("provider is not configured")
	}
	providerCfg, ok := m.cfg.Providers[providerID]
	if !ok {
		return fmt.Errorf("provider %q not configured", providerID)
	}
	providerCfg.DefaultModel = modelID
	m.cfg.Providers[providerID] = providerCfg
	if providerID == m.cfg.DefaultProvider {
		m.cfg.DefaultModel = modelID
	}
	if err := m.cfg.Save(); err != nil {
		return err
	}
	if m.agent != nil {
		m.agent.UpdateConfig(m.cfg)
	}
	if m.currentSession.ID != 0 && (m.currentSession.ProviderID == providerID || m.currentSession.ProviderID == "") && m.store != nil {
		if err := m.store.SetSessionModel(context.Background(), m.currentSession.ID, providerID, modelID); err != nil {
			return err
		}
	}
	if m.currentSession.ProviderID == providerID || strings.TrimSpace(m.currentSession.ProviderID) == "" {
		m.currentSession.ProviderID = providerID
		m.currentSession.ModelID = modelID
	}
	return nil
}

func (m *Model) activeProviderID() string {
	if strings.TrimSpace(m.currentSession.ProviderID) != "" {
		return m.currentSession.ProviderID
	}
	return m.cfg.DefaultProvider
}

func (m *Model) openThemePicker() {
	items := make([]ui.PickerItem, 0, len(theme.Names()))
	for _, name := range theme.Names() {
		items = append(items, ui.PickerItem{
			Title:       name,
			Description: "Preview theme colors",
			Value:       name,
		})
	}
	current := strings.TrimSpace(m.cfg.UI.Theme)
	if current == "" {
		current = theme.Default().Name
	}
	m.picker = pickerModel{
		visible:      true,
		mode:         pickerModeTheme,
		initialValue: current,
		dialog:       ui.NewPickerDialog("Themes", "type to filter  enter apply  tab buttons  esc cancel", items),
	}
	m.setPickerCurrentValue(current)
	m.previewSelectedTheme()
}

func (m *Model) openPermissionsPicker() {
	items := make([]ui.PickerItem, 0, len(permission.BuiltinProfiles()))
	for _, item := range permission.BuiltinProfiles() {
		items = append(items, ui.PickerItem{
			Title:       item.Label,
			Description: item.Description,
			Value:       item.Name,
		})
	}
	m.picker = pickerModel{
		visible:      true,
		mode:         pickerModePermissions,
		initialValue: m.permissionProfile(),
		dialog:       ui.NewPickerDialog("Permissions", "type to filter  enter apply  tab buttons  esc cancel", items),
	}
	m.setPickerCurrentValue(m.permissionProfile())
}

func (m *Model) openSkillsPicker() {
	catalog := skills.Discover(m.workdir)
	items := make([]ui.PickerItem, 0, len(catalog))
	for _, item := range catalog {
		items = append(items, ui.PickerItem{
			Title:       "$" + item.Name,
			Description: blankAsDash(item.Description),
			Value:       item.Name,
		})
	}
	m.picker = pickerModel{
		visible: true,
		mode:    pickerModeSkills,
		dialog:  ui.NewPickerDialog("Skills", "type to filter  enter insert  tab buttons  esc cancel", items),
	}
}

func (m *Model) openApprovalPermissionsPicker() {
	if !m.hasApprovalPrompt() {
		return
	}
	m.openPermissionsPicker()
	m.picker.approvalID = m.approvals[0].ID
}

func (m *Model) movePicker(delta int) {
	if !m.hasPicker() {
		return
	}
	m.picker.dialog.Move(delta)
	m.previewSelectedTheme()
}

func (m *Model) submitPickerSelection(value string) (ui.Model, ui.Cmd) {
	switch m.picker.mode {
	case pickerModeTheme:
		if strings.TrimSpace(value) == "" {
			return m, nil
		}
		if err := m.setTheme(value, true); err != nil {
			m.status = fmt.Sprintf("theme save failed: %v", err)
			return m, nil
		}
		m.status = fmt.Sprintf("Theme set to %s", value)
		m.closePicker()
		return m, nil
	case pickerModePermissions:
		if strings.TrimSpace(value) == "" {
			return m, nil
		}
		approvalID := m.picker.approvalID
		m.closePicker()
		if approvalID > 0 {
			m.startBusy(busyScopeTranscript, fmt.Sprintf("Re-evaluating approval %d with %s…", approvalID, permission.DisplayName(value)))
			return m, ui.Batch(m.approvalPermissionProfileCmd(m.beginActiveOperation(), approvalID, value), m.spinnerCmdIfNeeded(), m.syncWindowTitleCmd())
		}
		if err := m.selectPermissionProfile(value); err != nil {
			m.status = err.Error()
			return m, nil
		}
		m.status = fmt.Sprintf("Permission mode set to %s; model will be updated on the next turn", permission.DisplayName(value))
		return m, m.syncWindowTitleCmd()
	case pickerModeSkills:
		if strings.TrimSpace(value) == "" {
			return m, nil
		}
		m.closePicker()
		m.composer.InsertString("$" + value)
		m.updateComposerMenus()
		m.status = fmt.Sprintf("Inserted $%s", value)
		return m, m.syncWindowTitleCmd()
	default:
		return m, nil
	}
}

func (m *Model) cancelPicker() (ui.Model, ui.Cmd) {
	switch m.picker.mode {
	case pickerModeTheme:
		restore := strings.TrimSpace(m.picker.initialValue)
		if restore == "" {
			restore = theme.Default().Name
		}
		if err := m.setTheme(restore, false); err != nil {
			m.status = fmt.Sprintf("theme restore failed: %v", err)
		}
		m.closePicker()
		return m, nil
	case pickerModePermissions:
		approvalID := m.picker.approvalID
		m.closePicker()
		if approvalID > 0 {
			m.status = "Permission mode change cancelled"
		} else {
			m.status = "Permission mode selection cancelled"
		}
		return m, nil
	case pickerModeSkills:
		m.closePicker()
		m.status = "Skill selection cancelled"
		return m, nil
	default:
		m.closePicker()
		return m, nil
	}
}

func (m *Model) previewSelectedTheme() {
	if m.picker.mode != pickerModeTheme {
		return
	}
	item, ok := m.picker.dialog.Current()
	if !ok {
		return
	}
	if err := m.setTheme(item.Value, false); err != nil {
		m.status = fmt.Sprintf("theme preview failed: %v", err)
	}
}

func (m *Model) setPickerCurrentValue(value string) {
	if !m.hasPicker() {
		return
	}
	m.picker.dialog.SetCurrentValue(value)
}

func (m *Model) selectPermissionProfile(profile string) error {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return fmt.Errorf("permission profile is required")
	}
	if !permission.IsBuiltinProfile(profile) {
		if _, ok := m.cfg.Permissions.Profiles[profile]; !ok {
			return fmt.Errorf("unknown permission profile %q", profile)
		}
	}
	if m.currentSession.ID != 0 {
		if err := m.store.SetSessionPermissionProfile(context.Background(), m.currentSession.ID, profile); err != nil {
			return err
		}
	}
	m.currentSession.PermissionProfile = profile
	for idx := range m.sessions {
		if m.sessions[idx].ID == m.currentSession.ID {
			m.sessions[idx].PermissionProfile = profile
		}
	}
	m.queuePermissionChangeNote()
	return nil
}

func (m *Model) setTheme(name string, save bool) error {
	selected := theme.Resolve(name)
	renderer, err := markdown.New(selected.Palette)
	if err != nil {
		return err
	}
	m.cfg.UI.Theme = selected.Name
	m.palette = selected.Palette
	m.renderer = renderer
	applyComposerTheme(&m.composer, selected.Palette)
	ui.InvalidateElementCaches(&ui.Context{Palette: m.palette}, m.renderBodyElement())
	m.invalidateTranscript()
	m.refreshViewport()
	if save {
		if err := m.cfg.Save(); err != nil {
			return err
		}
	}
	return nil
}

func (m *Model) canContinue() (bool, string) {
	if strings.TrimSpace(m.composer.Value()) != "" || len(m.draftAttachments) > 0 || len(m.draftReferences) > 0 {
		return false, "Clear the composer before continuing"
	}
	if m.currentSession.ID == 0 {
		return false, "No saved session to continue"
	}
	if ok, status := m.canSendPrompt(); !ok {
		return false, status
	}
	return true, ""
}

func (m *Model) queuePermissionChangeNote() {
	label := permission.DisplayName(m.permissionProfile())
	projectRoot := strings.TrimSpace(m.currentProjectRoot())
	if projectRoot == "" {
		projectRoot = m.workdir
	}
	m.pendingModelNote = fmt.Sprintf(
		"Permission mode changed to %s. Treat %s as the current project root. Actions outside that project require approval in the active mode.",
		label,
		projectRoot,
	)
}

func (m *Model) applyUIConfig(next config.UI, save bool) (ui.Cmd, error) {
	prevMouse := m.mouseEnabled

	selected := theme.Resolve(next.Theme)
	renderer, err := markdown.New(selected.Palette)
	if err != nil {
		return nil, err
	}

	next.Theme = selected.Name
	next.Spinner = ui.NormalizeSpinner(next.Spinner)
	m.cfg.UI = next
	m.palette = selected.Palette
	m.renderer = renderer
	m.showSidebar = next.ShowSidebar
	m.showReasoning = next.ShowReasoning
	m.showSystem = next.ShowSystem
	m.mouseEnabled = next.Mouse
	m.composer.BlinkEnabled = next.CursorBlink
	applyComposerTheme(&m.composer, selected.Palette)
	m.resize()
	m.refreshViewport()

	if save {
		if err := m.cfg.Save(); err != nil {
			return nil, err
		}
	}

	cmds := make([]ui.Cmd, 0, 2)
	if prevMouse == m.mouseEnabled {
		if len(cmds) == 0 {
			return nil, nil
		}
		return ui.Batch(cmds...), nil
	}
	if m.mouseEnabled {
		cmds = append(cmds, func() ui.Msg { return ui.EnableMouseCellMotion() })
		return ui.Batch(cmds...), nil
	}
	cmds = append(cmds, func() ui.Msg { return ui.DisableMouse() })
	return ui.Batch(cmds...), nil
}

func spinnerTickCmd() ui.Cmd {
	return ui.Tick(120*time.Millisecond, func(time.Time) ui.Msg {
		return spinnerTickMsg{}
	})
}

func slashQuery(value string) (string, bool) {
	if !strings.HasPrefix(value, "/") {
		return "", false
	}
	if strings.ContainsAny(value, " \t\n") {
		return "", false
	}
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), "/"), true
}

func slashQueryFromComposer(value textarea.Model) (string, bool) {
	runes := value.Runes()
	if len(runes) == 0 {
		return "", false
	}
	if runes[0] != '/' {
		return "", false
	}
	query := make([]rune, 0, max(0, len(runes)-1))
	for _, r := range runes[1:] {
		if isComposerWhitespace(r) {
			return "", false
		}
		query = append(query, unicode.ToLower(r))
	}
	return string(query), true
}

func skillQuery(value string) (query string, start int, ok bool) {
	value = strings.TrimRight(value, "\n")
	if strings.TrimSpace(value) == "" {
		return "", 0, false
	}
	start = strings.LastIndex(value, "$")
	if start < 0 {
		return "", 0, false
	}
	if strings.ContainsAny(value[start:], " \t\n") {
		return "", 0, false
	}
	if start > 0 {
		prev := value[start-1]
		if !strings.ContainsRune(" \t\n([{", rune(prev)) {
			return "", 0, false
		}
	}
	return strings.ToLower(strings.TrimSpace(strings.TrimPrefix(value[start:], "$"))), start, true
}

func skillQueryFromComposer(value textarea.Model) (query string, start int, ok bool) {
	runes := value.Runes()
	cursor := value.CursorIndex()
	if cursor < 0 || cursor > len(runes) {
		cursor = len(runes)
	}
	if cursor == 0 {
		return "", 0, false
	}
	start, _ = composerTokenBounds(runes, cursor)
	if start >= len(runes) || runes[start] != '$' {
		return "", 0, false
	}
	queryRunes := make([]rune, 0, cursor-start)
	for _, r := range runes[start+1 : cursor] {
		if isComposerWhitespace(r) {
			return "", 0, false
		}
		queryRunes = append(queryRunes, unicode.ToLower(r))
	}
	return string(queryRunes), start, true
}

func mentionQuery(value string, cursor int) (query string, start int, pathMode bool, ok bool) {
	value = strings.TrimRight(value, "\n")
	if cursor < 0 || cursor > len(value) {
		cursor = len(value)
	}
	if strings.TrimSpace(value) == "" {
		return "", 0, false, false
	}
	start = cursor
	for start > 0 && !strings.ContainsRune(" \t\n([{", rune(value[start-1])) {
		start--
	}
	token := value[start:cursor]
	if !strings.HasPrefix(token, "@") {
		return "", 0, false, false
	}
	if strings.HasPrefix(token, `@"`) {
		query = strings.TrimSuffix(strings.TrimPrefix(token, `@"`), `"`)
	} else {
		query = strings.TrimPrefix(token, "@")
	}
	query = strings.TrimSpace(query)
	pathMode = strings.HasPrefix(query, "./") || strings.HasPrefix(query, "../") || strings.HasPrefix(query, "/")
	if pathMode {
		return query, start, pathMode, true
	}
	return strings.ToLower(query), start, pathMode, true
}

func mentionQueryFromComposer(value textarea.Model) (query string, start int, end int, pathMode bool, ok bool) {
	runes := value.Runes()
	cursor := value.CursorIndex()
	if cursor < 0 || cursor > len(runes) {
		cursor = len(runes)
	}
	if cursor == 0 || len(runes) == 0 {
		return "", 0, 0, false, false
	}
	start, end = composerTokenBounds(runes, cursor)
	if start >= len(runes) || runes[start] != '@' {
		return "", 0, 0, false, false
	}
	tokenRunes := runes[start:cursor]
	if len(tokenRunes) >= 2 && tokenRunes[0] == '@' && tokenRunes[1] == '"' {
		query = strings.TrimSuffix(string(tokenRunes[2:]), `"`)
	} else {
		query = string(tokenRunes[1:])
	}
	query = strings.TrimSpace(query)
	pathMode = strings.HasPrefix(query, "./") || strings.HasPrefix(query, "../") || strings.HasPrefix(query, "/")
	if pathMode {
		return query, start, end, pathMode, true
	}
	return strings.ToLower(query), start, end, pathMode, true
}

func composerTokenBounds(runes []rune, cursor int) (start, end int) {
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(runes) {
		cursor = len(runes)
	}
	start = cursor
	for start > 0 && !isComposerTokenBoundary(runes[start-1]) {
		start--
	}
	end = cursor
	for end < len(runes) && !isComposerTokenBoundary(runes[end]) {
		end++
	}
	return start, end
}

func isComposerWhitespace(r rune) bool {
	switch r {
	case ' ', '\t', '\n':
		return true
	default:
		return false
	}
}

func isComposerTokenBoundary(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '(', '[', '{':
		return true
	default:
		return false
	}
}

func mentionTokenEnd(value string, start int) int {
	end := start
	quoted := strings.HasPrefix(value[start:], `@"`)
	if quoted {
		end += 2
		for end < len(value) {
			if value[end] == '"' {
				return end + 1
			}
			end++
		}
		return len(value)
	}
	for end < len(value) && !strings.ContainsRune(" \t\n([{", rune(value[end])) {
		end++
	}
	return end
}

func lowerRune(r rune) rune {
	return unicode.ToLower(r)
}

func matchingSlashCommands(query string) []slashCommand {
	var matches []slashCommand
	for _, item := range internalSlashCommands() {
		name := strings.TrimPrefix(strings.ToLower(item.Name), "/")
		if query == "" || strings.HasPrefix(name, query) {
			matches = append(matches, item)
		}
	}
	return matches
}

func matchingSkills(workdir string, query string) []skills.Skill {
	var matches []skills.Skill
	for _, item := range skills.Discover(workdir) {
		name := strings.ToLower(strings.TrimSpace(item.Name))
		if query == "" || strings.HasPrefix(name, query) {
			matches = append(matches, item)
		}
	}
	return matches
}
