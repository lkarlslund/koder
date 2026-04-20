package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/lkarlslund/koder/internal/agent"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/debugsrv"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/markdown"
	"github.com/lkarlslund/koder/internal/permission"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/sessionctx"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/theme"
	"github.com/lkarlslund/koder/internal/ui"
	"github.com/lkarlslund/koder/internal/workspace"
)

type promptDoneMsg struct {
	events <-chan domain.Event
	err    error
}

type spinnerTickMsg struct{}

type busyScope int

const (
	busyScopeNone busyScope = iota
	busyScopeSidebar
	busyScopeTranscript
	composerHeight      = 3
	composerInputHeight = 1
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
)

type pickerItem struct {
	Title       string
	Description string
	Details     []string
	Value       string
}

type pickerModel struct {
	visible      bool
	mode         pickerMode
	title        string
	hint         string
	query        string
	index        int
	items        []pickerItem
	matches      []pickerItem
	initialValue string
}

type runPromptMsg struct {
	session domain.Session
	events  <-chan domain.Event
	err     error
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

type Model struct {
	cfg              config.Config
	store            *store.Store
	agent            *agent.Engine
	renderer         *markdown.Renderer
	palette          theme.Palette
	sessions         []domain.Session
	currentSession   domain.Session
	messages         []domain.Message
	parts            map[int64][]domain.Part
	tasks            []store.Task
	approvals        []store.Approval
	viewport         viewport.Model
	composer         textarea.Model
	width            int
	height           int
	status           string
	loading          bool
	busy             busyModel
	showSidebar      bool
	showReasoning    bool
	slashMatches     []slashCommand
	slashIndex       int
	approvalChoice   int
	workdir          string
	workspace        workspace.Status
	startupMode      StartupMode
	picker           pickerModel
	pendingPartID    int64
	mouseEnabled     bool
	sessionDialog    *ui.SessionDialog
	preferences      *ui.PreferencesDialog
	connectDialog    *ui.ConnectDialog
	disconnectDialog *ui.DisconnectDialog
	modelDialog      *ui.ModelDialog
	debug            *debugsrv.Recorder
}

func New(cfg config.Config, st *store.Store, a *agent.Engine, mode StartupMode, debug *debugsrv.Recorder) (Model, error) {
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
	composer.ShowLineNumbers = false
	applyComposerTheme(&composer, tuiTheme.Palette)

	vp := viewport.New(40, 10)
	vp.SetContent("Loading…")
	workdir, err := os.Getwd()
	if err != nil {
		return Model{}, err
	}

	return Model{
		cfg:           cfg,
		store:         st,
		agent:         a,
		renderer:      renderer,
		palette:       tuiTheme.Palette,
		viewport:      vp,
		composer:      composer,
		showSidebar:   cfg.UI.ShowSidebar,
		showReasoning: cfg.UI.ShowReasoning,
		parts:         make(map[int64][]domain.Part),
		status:        "Ready",
		workdir:       workdir,
		startupMode:   mode,
		mouseEnabled:  cfg.UI.Mouse,
		debug:         debug,
	}, nil
}

func (m Model) Init() tea.Cmd {
	if !m.mouseEnabled {
		return tea.Batch(m.loadCmd(), m.syncWindowTitleCmd())
	}
	return tea.Batch(
		m.loadCmd(),
		m.syncWindowTitleCmd(),
		func() tea.Msg { return tea.EnableMouseCellMotion() },
	)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	defer m.syncDebugRuntime()
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resize()
		m.refreshViewport()
		return m, nil
	case spinnerTickMsg:
		if !m.shouldAnimateSpinner() {
			return m, nil
		}
		m.busy.spinner.tick()
		if m.hasPreferencesDialog() {
			m.preferences.Tick()
		}
		m.refreshViewport()
		return m, tea.Batch(spinnerTickCmd(), m.syncWindowTitleCmd())
	case promptDoneMsg:
		if msg.err != nil {
			m.status = msg.err.Error()
			m.stopBusy()
			return m, m.syncWindowTitleCmd()
		}
		m.startBusy(m.busy.scopeOrDefault(busyScopeTranscript), m.busy.statusOrDefault("Working ..."))
		return m, tea.Batch(nextEventCmd(msg.events), m.spinnerCmdIfNeeded(), m.syncWindowTitleCmd())
	case runPromptMsg:
		if msg.err != nil {
			m.status = msg.err.Error()
			m.appendLocalAssistantError(msg.err)
			m.stopBusy()
			return m, m.syncWindowTitleCmd()
		}
		m.currentSession = msg.session
		m.startBusy(m.busy.scopeOrDefault(busyScopeTranscript), "Working ...")
		return m, tea.Batch(nextEventCmd(msg.events), m.spinnerCmdIfNeeded(), m.syncWindowTitleCmd())
	case eventMsg:
		m.recordEvent(msg.event)
		m.applyEvent(msg.event)
		if msg.events != nil {
			return m, tea.Batch(m.reloadDetailsCmd(), nextEventCmd(msg.events), m.syncWindowTitleCmd())
		}
		m.stopBusy()
		return m, tea.Batch(m.reloadDetailsCmd(), m.syncWindowTitleCmd())
	case loadMsg:
		m = m.UpdateLoad(msg)
		if m.debug != nil && m.currentSession.ID > 0 {
			m.debug.RecordLifecycle(m.currentSession.ID, "session_reloaded", fmt.Sprintf("%d messages", len(m.messages)), map[string]string{"messages": strconv.Itoa(len(m.messages))})
		}
		m.stopBusyWithStatus("Ready")
		return m, m.syncWindowTitleCmd()
	case forkSessionMsg:
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
		m.sessions = msg.sessions
		m.currentSession = msg.session
		m.messages = msg.messages
		m.parts = msg.parts
		m.approvals = msg.approvals
		m.tasks = msg.tasks
		m.workspace = msg.workspace
		m.composer.Reset()
		m.closePicker()
		m.closeSessionDialog()
		m.closeConnectDialog()
		m.closeDisconnectDialog()
		m.closeModelDialog()
		m.stopBusy()
		if msg.session.ID > 0 {
			m.status = fmt.Sprintf("Started session %d", msg.session.ID)
		} else {
			m.status = "Started new session"
		}
		if m.debug != nil {
			m.debug.RecordLifecycle(msg.session.ID, "new_session_ready", msg.session.Title, nil)
		}
		m.updateSlashMenu()
		m.refreshViewport()
		return m, m.syncWindowTitleCmd()
	case sessionPickerMsg:
		m.sessions = msg.sessions
		m.openSessionPicker()
		m.stopBusyWithStatus("Select a session to resume")
		return m, m.syncWindowTitleCmd()
	case providerProbeMsg:
		if !m.hasConnectDialog() {
			return m, nil
		}
		if msg.err != nil {
			m.connectDialog.SetStatus("Connection test failed: " + msg.err.Error())
			m.status = msg.err.Error()
			return m, m.syncWindowTitleCmd()
		}
		modelIDs := make([]string, 0, len(msg.result.Models))
		for _, item := range msg.result.Models {
			modelIDs = append(modelIDs, item.ID)
		}
		m.connectDialog.SetModels(modelIDs)
		if len(modelIDs) == 0 {
			m.connectDialog.SetStatus("Connected, but no models were returned")
			m.status = "Provider connected"
		} else {
			m.connectDialog.SetStatus(fmt.Sprintf("Connected: discovered %d models", len(modelIDs)))
			m.status = fmt.Sprintf("Provider connected: %d models", len(modelIDs))
		}
		return m, m.syncWindowTitleCmd()
	case modelListMsg:
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
	case tea.MouseMsg:
		if m.hasPicker() || m.hasApprovalPrompt() {
			return m, nil
		}
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	var cmd tea.Cmd
	m.composer, cmd = m.composer.Update(msg)
	m.updateSlashMenu()
	return m, cmd
}

func (m Model) View() string {
	m.syncDebugRuntime()
	if m.hasModelDialog() && m.width > 0 && m.height > 0 {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, m.renderModelDialog())
	}
	if m.hasDisconnectDialog() && m.width > 0 && m.height > 0 {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, m.renderDisconnectDialog())
	}
	if m.hasConnectDialog() && m.width > 0 && m.height > 0 {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, m.renderConnectDialog())
	}
	if m.hasSessionDialog() && m.width > 0 && m.height > 0 {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, m.renderSessionDialog())
	}
	if m.hasPreferencesDialog() && m.width > 0 && m.height > 0 {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, m.renderPreferencesDialog())
	}
	body := m.renderBody()
	footer := m.renderFooter()
	view := lipgloss.JoinVertical(lipgloss.Left, body, footer)
	if m.hasPicker() {
		view = lipgloss.JoinVertical(lipgloss.Left, view, m.renderPicker())
	}
	if m.width > 0 && m.height > 0 {
		return lipgloss.Place(m.width, m.height, lipgloss.Left, lipgloss.Bottom, view)
	}
	return view
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.hasModelDialog() {
		return m.handleModelDialogKey(msg)
	}
	if m.hasDisconnectDialog() {
		return m.handleDisconnectDialogKey(msg)
	}
	if m.hasConnectDialog() {
		return m.handleConnectDialogKey(msg)
	}
	if m.hasPreferencesDialog() {
		return m.handlePreferencesKey(msg)
	}

	if m.hasSessionDialog() {
		return m.handleSessionDialogKey(msg)
	}

	if m.hasPicker() {
		switch msg.String() {
		case "up":
			m.movePicker(-1)
			return m, nil
		case "down":
			m.movePicker(1)
			return m, nil
		case "enter":
			return m.submitPickerSelection()
		case "esc":
			return m.cancelPicker()
		case "ctrl+c":
			return m.quit()
		case "backspace":
			m.trimPickerQuery()
			return m, nil
		default:
			if m.updatePickerQuery(msg) {
				return m, nil
			}
		}
	}

	if m.hasApprovalPrompt() {
		switch msg.String() {
		case "left", "up", "shift+tab":
			if m.approvalChoice > 0 {
				m.approvalChoice--
			}
			return m, nil
		case "right", "down", "tab":
			if m.approvalChoice < 1 {
				m.approvalChoice++
			}
			return m, nil
		case "y":
			return m.submitApprovalChoice(true)
		case "n", "esc":
			return m.submitApprovalChoice(false)
		case "enter":
			return m.submitApprovalChoice(m.approvalChoice == 0)
		}
	}

	if m.hasSlashMenu() {
		switch msg.String() {
		case "up":
			if m.slashIndex > 0 {
				m.slashIndex--
			}
			return m, nil
		case "down":
			if m.slashIndex < len(m.slashMatches)-1 {
				m.slashIndex++
			}
			return m, nil
		case "tab":
			m.acceptSlashSelection()
			return m, nil
		case "enter":
			prompt := strings.TrimSpace(m.composer.Value())
			if prompt == "/new" {
				m.startBusy(busyScopeSidebar, "Creating session…")
				return m, tea.Batch(m.newSessionCmd(), m.spinnerCmdIfNeeded())
			}
			if len(m.slashMatches) == 1 && m.slashMatches[0].Name == prompt && !m.slashMatches[0].NeedsArgs {
				break
			}
			m.acceptSlashSelection()
			return m, nil
		case "esc":
			m.slashMatches = nil
			m.slashIndex = 0
			return m, nil
		}
	}

	switch msg.String() {
	case "ctrl+c":
		return m.quit()
	case "ctrl+s":
		m.showSidebar = !m.showSidebar
		m.resize()
		m.refreshViewport()
		return m, nil
	case "ctrl+r":
		m.showReasoning = !m.showReasoning
		m.refreshViewport()
		return m, nil
	case "shift+enter", "alt+enter":
		m.composer.InsertRune('\n')
		m.updateSlashMenu()
		return m, nil
	case "enter":
		if m.loading {
			return m, nil
		}
		prompt := strings.TrimSpace(m.composer.Value())
		if prompt == "" {
			return m, nil
		}
		if handledModel, cmd, ok := m.handleLocalCommand(prompt); ok {
			return handledModel, cmd
		}
		if ok, status := m.canSendPrompt(); !ok {
			m.openConnectDialog()
			m.status = status
			return m, nil
		}
		m.composer.Reset()
		m.updateSlashMenu()
		m.appendLocalUserPrompt(prompt)
		m.startBusy(busyScopeTranscript, "Running…")
		return m, tea.Batch(m.promptCmd(prompt), m.spinnerCmdIfNeeded())
	}

	var cmd tea.Cmd
	m.composer, cmd = m.composer.Update(msg)
	m.updateSlashMenu()
	return m, cmd
}

func (m *Model) applyEvent(evt domain.Event) {
	switch evt.Kind {
	case domain.EventKindMessageDelta:
		m.startBusy(busyScopeTranscript, "Working ...")
	case domain.EventKindReasoning:
		m.startBusy(busyScopeTranscript, "Working ...")
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
	sidebarWidth := 0
	if m.showSidebar {
		sidebarWidth = min(32, max(20, m.width/4))
	}
	bodyWidth := m.width - sidebarWidth - 3
	if bodyWidth < 20 {
		bodyWidth = 20
	}
	m.composer.SetWidth(m.composerWidth())
	bodyHeight := m.height - m.footerHeight()
	if bodyHeight < 5 {
		bodyHeight = 5
	}
	m.viewport.Width = bodyWidth
	m.viewport.Height = bodyHeight
}

func (m *Model) renderHeader() string {
	return ""
}

func (m *Model) renderBody() string {
	return ui.RenderBody(m.viewport.View(), ui.RenderSidebar(m.renderSidebar(), m.palette, m.viewport.Height), m.showSidebar)
}

func (m *Model) renderFooter() string {
	parts := []string{}
	if prompt := m.renderApprovalPrompt(); prompt != "" {
		parts = append(parts, prompt)
	}
	if menu := m.renderSlashMenu(); menu != "" {
		parts = append(parts, menu)
	}
	parts = append(parts, "")
	parts = append(parts, m.renderComposer())
	return ui.RenderFooter(parts)
}

func (m *Model) footerHeight() int {
	return lipgloss.Height(m.renderFooter())
}

func (m *Model) renderComposer() string {
	m.composer.Prompt = m.promptGlyph() + " "
	muted := lipgloss.NewStyle().
		Background(m.palette.UserTextBackground).
		Foreground(m.palette.ComposerMutedText)
	m.composer.Cursor.TextStyle = muted
	cursorView := " "
	if placeholder := ansi.Truncate(m.composer.Placeholder, max(0, m.composerWidth()-ansi.StringWidth(m.composer.Prompt)), ""); placeholder != "" {
		runes := []rune(placeholder)
		m.composer.Cursor.SetChar(string(runes[0]))
		cursorView = m.composer.Cursor.View()
	} else {
		m.composer.Cursor.SetChar(" ")
		cursorView = m.composer.Cursor.View()
	}
	return ui.RenderComposer(ui.ComposerProps{
		Palette:          m.palette,
		Width:            m.composerWidth(),
		HalfBlocks:       m.halfBlocksEnabled(),
		PromptGlyph:      m.promptGlyph(),
		View:             m.composer.View(),
		Value:            m.composer.Value(),
		Placeholder:      m.composer.Placeholder,
		CursorView:       cursorView,
		MutedCursorStyle: muted,
	})
}

func (m *Model) composerWidth() int {
	if m.width <= 0 {
		return 40
	}
	sidebarWidth := 0
	if m.showSidebar {
		sidebarWidth = min(32, max(20, m.width/4))
	}
	bodyWidth := m.width - sidebarWidth - 3
	if bodyWidth < 20 {
		bodyWidth = 20
	}
	return bodyWidth - 2
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
	return ui.RenderHalfBlockLine(width, char, m.palette)
}

func mPrompt(cfg config.Config) string {
	if cfg.UI.HalfBlocks {
		return "▌ "
	}
	return "┃ "
}

func (m *Model) renderSidebar() string {
	var lines []string
	lines = append(lines, "Session")
	lines = append(lines, fmt.Sprintf("  id      %d", m.currentSession.ID))
	lines = append(lines, fmt.Sprintf("  profile %s", truncate(m.permissionProfile(), 19)))
	lines = append(lines, fmt.Sprintf("  mouse   %s", m.mouseStatus()))
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
	if metrics, ok := sessionctx.FromMessages(m.cfg, m.currentSession, m.messages, m.parts); ok {
		lines = append(lines, "")
		lines = append(lines, "Context")
		lines = append(lines, fmt.Sprintf("  used   %s / %s", formatTokens(metrics.Used), formatTokens(metrics.Max)))
		lines = append(lines, fmt.Sprintf("  usage  %d%% used", metrics.UsagePercent))
	}
	lines = append(lines, "")
	lines = append(lines, "Workspace")
	lines = append(lines, truncate(m.workdir, 28))
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
	lines = append(lines, "Pending approvals")
	if len(m.approvals) == 0 {
		lines = append(lines, "  none")
	}
	for _, item := range m.approvals {
		lines = append(lines, fmt.Sprintf("  #%d %s", item.ID, truncate(item.Command, 22)))
	}
	lines = append(lines, "")
	lines = append(lines, "Profiles")
	for _, profile := range permission.ProfileNames(m.cfg.Permissions) {
		cursor := " "
		if profile == m.permissionProfile() {
			cursor = ">"
		}
		lines = append(lines, fmt.Sprintf("%s %s", cursor, profile))
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
	lines = append(lines, "Keys")
	lines = append(lines, "  enter send/select")
	lines = append(lines, "  tab   autocomplete")
	lines = append(lines, "  ^s    sidebar")
	lines = append(lines, "  ^r    reasoning")
	lines = append(lines, "  /compact")
	lines = append(lines, "  /connect")
	lines = append(lines, "  /disconnect")
	lines = append(lines, "  /fork")
	lines = append(lines, "  /model")
	lines = append(lines, "  /new  session")
	lines = append(lines, "  /perm profile")
	lines = append(lines, "  /prefs")
	lines = append(lines, "  /resume")
	lines = append(lines, "  /quit")
	return strings.Join(lines, "\n")
}

func (m Model) debugAPIAddr() string {
	if m.debug == nil {
		return ""
	}
	return strings.TrimSpace(m.debug.Runtime().DebugAPI)
}

func (m *Model) refreshViewport() {
	if m.currentSession.ID == 0 && len(m.messages) == 0 {
		if !m.cfg.HasUsableDefaultProvider() {
			m.viewport.SetContent("No provider configured.\n\nType /connect to add one before sending prompts.")
			return
		}
		m.viewport.SetContent("No session")
		return
	}
	var blocks []string
	for _, msg := range m.messages {
		blocks = append(blocks, m.renderTranscriptMessage(msg))
	}
	if indicator := m.renderTranscriptActivity(); indicator != "" {
		blocks = append(blocks, indicator)
	}
	if len(blocks) == 0 {
		if !m.cfg.HasUsableDefaultProvider() {
			blocks = append(blocks, "No provider configured.\n\nType /connect to add one before sending prompts.")
		} else {
			blocks = append(blocks, "Start by asking a question or type / for commands.")
		}
	}
	separator := "\n\n"
	if m.halfBlocksEnabled() {
		separator = "\n"
	}
	m.viewport.SetContent(strings.Join(blocks, separator))
	m.viewport.GotoBottom()
}

func (m *Model) renderTranscriptActivity() string {
	if !m.busy.transcriptActive() {
		return ""
	}
	return ui.RenderActivityIndicator(ui.WorkingIndicatorLine(m.workingIndicator()), m.palette)
}

func (m *Model) renderTranscriptMessage(msg domain.Message) string {
	body := m.renderMessageParts(m.parts[msg.ID])
	stamp := timestamp(msg.CreatedAt, m.cfg.UI.ShowTimestamps)
	switch msg.Role {
	case domain.MessageRoleUser:
		userBody := m.renderUserMessageParts(m.parts[msg.ID])
		if strings.TrimSpace(userBody) == "" {
			userBody = strings.TrimSpace(msg.Summary)
		}
		return m.renderUserMessage(userBody, stamp)
	default:
		if strings.TrimSpace(body) == "" {
			body = strings.TrimSpace(msg.Summary)
		}
		return m.renderAssistantMessage(body, stamp)
	}
}

func (m *Model) renderUserMessage(body, stamp string) string {
	return ui.RenderUserMessage(ui.UserMessageProps{
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
	return ui.RenderAssistantMessageWidth(body, stamp, m.viewport.Width, m.palette)
}

func (m *Model) renderMessageParts(parts []domain.Part) string {
	var blocks []string
	var reasoningBlocks []string
	var textBlocks []string
	var compactionBlocks []string
	var textBuf strings.Builder
	var reasoningBuf strings.Builder

	flushText := func() {
		if textBuf.Len() == 0 {
			return
		}
		textBlocks = append(textBlocks, m.renderer.Render(textBuf.String()))
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
				compactionBlocks = append(compactionBlocks, m.renderer.Render(body))
			}
		case domain.PartKindSystemNotice:
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
	blocks = append(blocks, reasoningBlocks...)
	blocks = append(blocks, textBlocks...)

	return strings.TrimSpace(strings.Join(blocks, "\n"))
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
	return ui.RenderReasoningBlockWidth(input, m.viewport.Width, m.palette)
}

func (m Model) loadCmd() tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		sessions, err := m.store.ListSessions(ctx)
		if err != nil {
			return promptDoneMsg{err: err}
		}
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
	sessions  []domain.Session
	current   domain.Session
	messages  []domain.Message
	parts     map[int64][]domain.Part
	approvals []store.Approval
	tasks     []store.Task
	workspace workspace.Status
}

func (m Model) promptCmd(prompt string) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		session := m.currentSession
		if session.ID == 0 {
			var err error
			session, err = m.persistDraftSession(ctx)
			if err != nil {
				return runPromptMsg{err: err}
			}
		}
		events, err := m.agent.RunPrompt(ctx, session, prompt)
		return runPromptMsg{session: session, events: events, err: err}
	}
}

func (m Model) newSessionCmd() tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		sessions, err := m.store.ListSessions(ctx)
		if err != nil {
			return promptDoneMsg{err: err}
		}
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

func (m Model) sessionPickerCmd() tea.Cmd {
	return func() tea.Msg {
		sessions, err := m.store.ListSessions(context.Background())
		if err != nil {
			return promptDoneMsg{err: err}
		}
		return sessionPickerMsg{sessions: sessions}
	}
}

func (m Model) loadSessionCmd(sessionID int64) tea.Cmd {
	return func() tea.Msg {
		if sessionID == 0 {
			return nil
		}
		ctx := context.Background()
		sessions, err := m.store.ListSessions(ctx)
		if err != nil {
			return promptDoneMsg{err: err}
		}
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

func (m Model) forkSessionCmd(sourceSessionID int64) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		forked, err := m.store.ForkSession(ctx, sourceSessionID)
		if err != nil {
			return forkSessionMsg{err: err}
		}
		sessions, err := m.store.ListSessions(ctx)
		if err != nil {
			return forkSessionMsg{err: err}
		}
		messages, parts, err := m.store.PartsForSession(ctx, forked.ID)
		if err != nil {
			return forkSessionMsg{err: err}
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

func (m Model) reloadDetailsCmd() tea.Cmd {
	return m.loadSessionCmd(m.currentSession.ID)
}

func nextEventCmd(events <-chan domain.Event) tea.Cmd {
	return func() tea.Msg {
		evt, ok := <-events
		if !ok {
			return eventMsg{}
		}
		return eventMsg{event: evt, events: events}
	}
}

func Run(cfg config.Config, st *store.Store, a *agent.Engine, mode StartupMode, debug *debugsrv.Recorder) error {
	model, err := New(cfg, st, a, mode, debug)
	if err != nil {
		return err
	}
	model.syncDebugRuntime()
	p := tea.NewProgram(model, tea.WithAltScreen(), tea.WithoutSignalHandler())
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
				p.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
			default:
				p.Send(tea.QuitMsg{})
			}
		case <-done:
		}
	}()
	finalModel, err := p.Run()
	if err != nil && !errors.Is(err, tea.ErrInterrupted) {
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
	if errors.Is(err, tea.ErrInterrupted) {
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
	m.sessions = msg.sessions
	m.currentSession = msg.current
	m.messages = msg.messages
	m.parts = msg.parts
	m.approvals = msg.approvals
	m.tasks = msg.tasks
	m.workspace = msg.workspace
	m.approvalChoice = 0
	m.closePicker()
	m.closeSessionDialog()
	m.closePreferencesDialog()
	m.closeConnectDialog()
	m.closeDisconnectDialog()
	m.closeModelDialog()
	m.refreshViewport()
	return m
}

func (m *Model) handleLocalCommand(prompt string) (tea.Model, tea.Cmd, bool) {
	trimmed := strings.TrimSpace(prompt)
	switch {
	case trimmed == "/new":
		m.composer.Reset()
		m.updateSlashMenu()
		m.startBusy(busyScopeSidebar, "Creating session…")
		return m, tea.Batch(m.newSessionCmd(), m.spinnerCmdIfNeeded()), true
	case trimmed == "/resume":
		m.composer.Reset()
		m.updateSlashMenu()
		m.startBusy(busyScopeSidebar, "Loading sessions…")
		return m, tea.Batch(m.sessionPickerCmd(), m.spinnerCmdIfNeeded()), true
	case trimmed == "/quit":
		m.composer.Reset()
		m.updateSlashMenu()
		model, cmd := m.quit()
		return model, cmd, true
	case trimmed == "/mouse on":
		m.composer.Reset()
		m.updateSlashMenu()
		m.mouseEnabled = true
		m.status = "Mouse capture enabled"
		return m, func() tea.Msg { return tea.EnableMouseCellMotion() }, true
	case trimmed == "/mouse off":
		m.composer.Reset()
		m.updateSlashMenu()
		m.mouseEnabled = false
		m.status = "Mouse capture disabled"
		return m, func() tea.Msg { return tea.DisableMouse() }, true
	case strings.HasPrefix(trimmed, "/perm "):
		profile := strings.TrimSpace(strings.TrimPrefix(trimmed, "/perm"))
		m.composer.Reset()
		m.updateSlashMenu()
		m.startBusy(busyScopeSidebar, fmt.Sprintf("Setting permission profile to %s…", profile))
		return m, tea.Batch(m.permissionProfileCmd(profile), m.spinnerCmdIfNeeded()), true
	case trimmed == "/compact":
		m.composer.Reset()
		m.updateSlashMenu()
		m.startBusy(busyScopeTranscript, "Compacting session...")
		return m, tea.Batch(m.compactCmd(), m.spinnerCmdIfNeeded()), true
	case trimmed == "/connect":
		m.composer.Reset()
		m.updateSlashMenu()
		m.openConnectDialog()
		return m, m.syncWindowTitleCmd(), true
	case trimmed == "/disconnect":
		m.composer.Reset()
		m.updateSlashMenu()
		if len(m.cfg.Providers) == 0 {
			m.status = "No configured providers to disconnect"
			return m, m.syncWindowTitleCmd(), true
		}
		m.openDisconnectDialog()
		return m, m.syncWindowTitleCmd(), true
	case trimmed == "/model":
		m.composer.Reset()
		m.updateSlashMenu()
		providerID := m.activeProviderID()
		if providerID == "" || !m.cfg.HasUsableProvider(providerID) {
			m.status = "Configure a provider first with /connect"
			return m, m.syncWindowTitleCmd(), true
		}
		m.status = fmt.Sprintf("Loading models for %s…", providerID)
		return m, tea.Batch(m.loadModelsCmd(providerID, false), m.syncWindowTitleCmd()), true
	case trimmed == "/theme":
		m.composer.Reset()
		m.updateSlashMenu()
		m.openThemePicker()
		return m, nil, true
	case trimmed == "/prefs":
		m.composer.Reset()
		m.updateSlashMenu()
		m.openPreferencesDialog()
		return m, tea.Batch(spinnerTickCmd(), m.syncWindowTitleCmd()), true
	case trimmed == "/fork":
		m.composer.Reset()
		m.updateSlashMenu()
		if m.currentSession.ID == 0 {
			m.status = "No saved session to fork"
			return m, m.syncWindowTitleCmd(), true
		}
		m.startBusy(busyScopeSidebar, fmt.Sprintf("Forking session %d…", m.currentSession.ID))
		return m, tea.Batch(m.forkSessionCmd(m.currentSession.ID), m.spinnerCmdIfNeeded()), true
	case strings.HasPrefix(trimmed, "/approve "):
		id, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(trimmed, "/approve")), 10, 64)
		if err != nil {
			m.status = fmt.Sprintf("invalid approval id: %v", err)
			return m, nil, true
		}
		m.composer.Reset()
		m.updateSlashMenu()
		m.startBusy(busyScopeTranscript, fmt.Sprintf("Approving approval %d…", id))
		return m, tea.Batch(m.approveCmd(id), m.spinnerCmdIfNeeded()), true
	case strings.HasPrefix(trimmed, "/deny "):
		id, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(trimmed, "/deny")), 10, 64)
		if err != nil {
			m.status = fmt.Sprintf("invalid approval id: %v", err)
			return m, nil, true
		}
		m.composer.Reset()
		m.updateSlashMenu()
		m.startBusy(busyScopeSidebar, fmt.Sprintf("Denying approval %d…", id))
		return m, tea.Batch(m.denyCmd(id), m.spinnerCmdIfNeeded()), true
	case strings.HasPrefix(trimmed, "/"):
		m.status = fmt.Sprintf("unknown command: %s", trimmed)
		return m, nil, true
	default:
		return nil, nil, false
	}
}

func (m Model) permissionProfileCmd(profile string) tea.Cmd {
	return func() tea.Msg {
		events, err := m.agent.SetPermissionProfile(context.Background(), m.currentSession.ID, profile)
		return promptDoneMsg{events: events, err: err}
	}
}

func (m Model) compactCmd() tea.Cmd {
	return func() tea.Msg {
		events, err := m.agent.Compact(context.Background(), m.currentSession.ID)
		return promptDoneMsg{events: events, err: err}
	}
}

func (m Model) approveCmd(approvalID int64) tea.Cmd {
	return func() tea.Msg {
		events, err := m.agent.Approve(context.Background(), m.currentSession.ID, approvalID)
		return promptDoneMsg{events: events, err: err}
	}
}

func (m Model) denyCmd(approvalID int64) tea.Cmd {
	return func() tea.Msg {
		events, err := m.agent.Deny(context.Background(), m.currentSession.ID, approvalID)
		return promptDoneMsg{events: events, err: err}
	}
}

func (m *Model) quit() (tea.Model, tea.Cmd) {
	m.stopBusyWithStatus("Quitting")
	return m, tea.Quit
}

func (m *Model) appendLocalUserPrompt(prompt string) {
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
	m.parts[messageID] = []domain.Part{{
		ID:        m.nextPendingID(),
		MessageID: messageID,
		Kind:      domain.PartKindText,
		Body:      prompt,
		CreatedAt: now,
	}}
	if m.debug != nil {
		m.debug.RecordLifecycle(m.currentSession.ID, "prompt_submitted", prompt, map[string]string{"optimistic": "true"})
	}
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
	m.refreshViewport()
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
	if m.renderTranscriptActivity() != "" {
		renderedBlocks++
	}
	viewportContent := m.viewport.View()
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
		LastError:          m.currentError(),
		ViewportWidth:      m.viewport.Width,
		ViewportHeight:     m.viewport.Height,
		ViewportYOffset:    m.viewport.YOffset,
		MessageCount:       len(m.messages),
		RenderBlockCount:   renderedBlocks,
		ViewportPreview:    truncate(strings.TrimSpace(viewportContent), 2048),
		ViewportContentLen: len(viewportContent),
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

func (m *Model) spinnerCmdIfNeeded() tea.Cmd {
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

func (m Model) syncWindowTitleCmd() tea.Cmd {
	return tea.SetWindowTitle(m.windowTitle())
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
		CreatedAt:         now,
		UpdatedAt:         now,
	}
}

func (m Model) persistDraftSession(ctx context.Context) (domain.Session, error) {
	session, err := m.store.CreateSession(ctx, "New Session", m.draftSession().ProviderID, m.draftSession().ModelID, nil)
	if err != nil {
		return domain.Session{}, err
	}
	if err := m.store.SetSessionPermissionProfile(ctx, session.ID, m.draftSession().PermissionProfile); err != nil {
		return domain.Session{}, err
	}
	sessions, err := m.store.ListSessions(ctx)
	if err != nil {
		return domain.Session{}, err
	}
	for _, item := range sessions {
		if item.ID == session.ID {
			return item, nil
		}
	}
	return session, nil
}

func (m *Model) updateSlashMenu() {
	query, ok := slashQuery(m.composer.Value())
	if !ok {
		m.slashMatches = nil
		m.slashIndex = 0
		return
	}
	m.slashMatches = matchingSlashCommands(query)
	if len(m.slashMatches) == 0 {
		m.slashIndex = 0
		return
	}
	if m.slashIndex >= len(m.slashMatches) {
		m.slashIndex = len(m.slashMatches) - 1
	}
}

func (m *Model) hasSlashMenu() bool {
	return len(m.slashMatches) > 0
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
	m.updateSlashMenu()
}

func (m *Model) renderSlashMenu() string {
	if len(m.slashMatches) == 0 {
		return ""
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
	return ui.RenderSlashMenu("Commands", items, selected)
}

func (m *Model) renderPicker() string {
	if !m.hasPicker() {
		return ""
	}
	items := make([]ui.MenuItem, 0, min(len(m.picker.matches), 8))
	if len(m.picker.matches) == 0 {
	} else {
		start := 0
		if m.picker.index >= 6 {
			start = m.picker.index - 5
		}
		end := min(len(m.picker.matches), start+8)
		for idx := start; idx < end; idx++ {
			item := m.picker.matches[idx]
			items = append(items, ui.MenuItem{Title: item.Title, Description: truncate(item.Description, 40)})
		}
		return ui.RenderPickerDialog(ui.PickerDialogProps{
			Palette: m.palette,
			Title:   m.picker.title,
			Hint:    m.picker.hint,
			Query:   m.picker.query,
			Items:   items,
			Index:   m.picker.index - start,
		})
	}
	return ui.RenderPickerDialog(ui.PickerDialogProps{
		Palette: m.palette,
		Title:   m.picker.title,
		Hint:    m.picker.hint,
		Query:   m.picker.query,
	})
}

func (m *Model) renderSessionDialog() string {
	if !m.hasSessionDialog() {
		return ""
	}
	width := 84
	if m.width > 0 {
		width = min(96, max(72, m.width-8))
	}
	return m.sessionDialog.View(width, m.palette)
}

func (m *Model) renderPreferencesDialog() string {
	if !m.hasPreferencesDialog() {
		return ""
	}
	width := 86
	if m.width > 0 {
		width = min(100, max(74, m.width-8))
	}
	return m.preferences.View(width, m.palette)
}

func (m *Model) renderConnectDialog() string {
	if !m.hasConnectDialog() {
		return ""
	}
	width := 88
	if m.width > 0 {
		width = min(104, max(76, m.width-8))
	}
	return m.connectDialog.View(width, m.palette)
}

func (m *Model) renderDisconnectDialog() string {
	if !m.hasDisconnectDialog() {
		return ""
	}
	width := 84
	if m.width > 0 {
		width = min(96, max(72, m.width-8))
	}
	return m.disconnectDialog.View(width, m.palette)
}

func (m *Model) renderModelDialog() string {
	if !m.hasModelDialog() {
		return ""
	}
	width := 84
	if m.width > 0 {
		width = min(96, max(72, m.width-8))
	}
	return m.modelDialog.View(width, m.palette)
}

func (m *Model) handleSessionDialogKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if !m.hasSessionDialog() {
		return m, nil
	}
	action := m.sessionDialog.Update(msg)
	switch action.Kind {
	case ui.SessionDialogActionSelect:
		m.startBusy(busyScopeSidebar, fmt.Sprintf("Resuming session %d…", action.SessionID))
		return m, tea.Batch(m.loadSessionCmd(action.SessionID), m.spinnerCmdIfNeeded())
	case ui.SessionDialogActionCancel:
		m.startBusy(busyScopeSidebar, "Creating session…")
		return m, tea.Batch(m.newSessionCmd(), m.spinnerCmdIfNeeded())
	default:
		return m, nil
	}
}

func (m *Model) handlePreferencesKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if !m.hasPreferencesDialog() {
		return m, nil
	}
	action := m.preferences.Update(msg)
	switch action.Kind {
	case ui.PreferencesActionChanged:
		cmd, err := m.applyUIConfig(action.UI, false)
		if err != nil {
			m.status = fmt.Sprintf("preferences preview failed: %v", err)
			return m, m.syncWindowTitleCmd()
		}
		return m, tea.Batch(cmd, m.syncWindowTitleCmd())
	case ui.PreferencesActionApply:
		cmd, err := m.applyUIConfig(action.UI, true)
		if err != nil {
			m.status = fmt.Sprintf("preferences save failed: %v", err)
			return m, m.syncWindowTitleCmd()
		}
		m.closePreferencesDialog()
		m.status = "Preferences saved"
		return m, tea.Batch(cmd, m.syncWindowTitleCmd())
	case ui.PreferencesActionCancel:
		cmd, err := m.applyUIConfig(action.UI, false)
		if err != nil {
			m.status = fmt.Sprintf("preferences restore failed: %v", err)
			return m, m.syncWindowTitleCmd()
		}
		m.closePreferencesDialog()
		m.status = "Preferences cancelled"
		return m, tea.Batch(cmd, m.syncWindowTitleCmd())
	default:
		return m, nil
	}
}

func (m *Model) handleConnectDialogKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if !m.hasConnectDialog() {
		return m, nil
	}
	action := m.connectDialog.Update(msg)
	switch action.Kind {
	case ui.ProviderConnectActionTest:
		m.connectDialog.SetStatus("Testing connection…")
		return m, tea.Batch(m.probeProviderCmd(action.Draft), m.syncWindowTitleCmd())
	case ui.ProviderConnectActionSave:
		discoveredModels := m.connectDialog.Models()
		if err := m.saveProviderDraft(action.Draft); err != nil {
			m.connectDialog.SetStatus("Save failed: " + err.Error())
			m.status = err.Error()
			return m, m.syncWindowTitleCmd()
		}
		m.closeConnectDialog()
		if len(discoveredModels) > 0 {
			m.status = fmt.Sprintf("Connected provider %s", action.Draft.ProviderID)
			return m, tea.Batch(m.loadModelsCmd(action.Draft.ProviderID, true), m.syncWindowTitleCmd())
		}
		m.status = fmt.Sprintf("Connected provider %s", action.Draft.ProviderID)
		m.refreshViewport()
		return m, m.syncWindowTitleCmd()
	case ui.ProviderConnectActionCancel:
		m.closeConnectDialog()
		m.status = "Provider connect cancelled"
		return m, m.syncWindowTitleCmd()
	default:
		return m, nil
	}
}

func (m *Model) handleDisconnectDialogKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if !m.hasDisconnectDialog() {
		return m, nil
	}
	action := m.disconnectDialog.Update(msg)
	switch action.Kind {
	case ui.DisconnectDialogActionSelect:
		if err := m.disconnectProvider(action.ProviderID); err != nil {
			m.status = err.Error()
			return m, m.syncWindowTitleCmd()
		}
		m.closeDisconnectDialog()
		m.status = fmt.Sprintf("Disconnected provider %s", action.ProviderID)
		m.refreshViewport()
		return m, m.syncWindowTitleCmd()
	case ui.DisconnectDialogActionCancel:
		m.closeDisconnectDialog()
		m.status = "Provider disconnect cancelled"
		return m, m.syncWindowTitleCmd()
	default:
		return m, nil
	}
}

func (m *Model) handleModelDialogKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if !m.hasModelDialog() {
		return m, nil
	}
	action := m.modelDialog.Update(msg)
	switch action.Kind {
	case ui.ModelDialogActionSelect:
		if err := m.selectModel(action.ModelID); err != nil {
			m.status = err.Error()
			return m, m.syncWindowTitleCmd()
		}
		m.closeModelDialog()
		m.status = fmt.Sprintf("Selected model %s", action.ModelID)
		m.refreshViewport()
		return m, m.syncWindowTitleCmd()
	case ui.ModelDialogActionCancel:
		m.closeModelDialog()
		m.status = "Model selection cancelled"
		return m, m.syncWindowTitleCmd()
	default:
		return m, nil
	}
}

func (m *Model) hasApprovalPrompt() bool {
	return !m.loading && len(m.approvals) > 0
}

func (m *Model) submitApprovalChoice(approve bool) (tea.Model, tea.Cmd) {
	if !m.hasApprovalPrompt() {
		return m, nil
	}
	id := m.approvals[0].ID
	if approve {
		m.startBusy(busyScopeTranscript, fmt.Sprintf("Approving approval %d…", id))
		return m, tea.Batch(m.approveCmd(id), m.spinnerCmdIfNeeded())
	}
	m.startBusy(busyScopeSidebar, fmt.Sprintf("Denying approval %d…", id))
	return m, tea.Batch(m.denyCmd(id), m.spinnerCmdIfNeeded())
}

func (m *Model) renderApprovalPrompt() string {
	if !m.hasApprovalPrompt() {
		return ""
	}
	item := m.approvals[0]
	body := fmt.Sprintf("#%d  %s  %s", item.ID, item.Tool, truncate(approvalSummary(item), max(24, m.viewport.Width-10)))
	return ui.RenderApprovalPrompt(ui.ApprovalPromptProps{
		Palette:      m.palette,
		Title:        "Permission required",
		Body:         body,
		ApproveLabel: "Approve",
		DenyLabel:    "Deny",
		ApproveFocus: m.approvalChoice == 0,
		DenyFocus:    m.approvalChoice == 1,
		Hints:        "enter select  tab switch  y approve  n deny",
	})
}

func internalSlashCommands() []slashCommand {
	return []slashCommand{
		{Name: "/compact", Description: "Summarize old context"},
		{Name: "/connect", Description: "Configure a provider"},
		{Name: "/disconnect", Description: "Remove a configured provider"},
		{Name: "/fork", Description: "Branch from the current session"},
		{Name: "/model", Description: "Choose a model for the active provider"},
		{Name: "/new", Description: "Start a new session"},
		{Name: "/mouse", Description: "Toggle mouse capture", NeedsArgs: true, Autocomplete: "/mouse "},
		{Name: "/perm", Description: "Set permission profile", NeedsArgs: true, Autocomplete: "/perm "},
		{Name: "/prefs", Description: "Open preferences"},
		{Name: "/quit", Description: "Quit koder"},
		{Name: "/resume", Description: "Resume a saved session"},
		{Name: "/theme", Description: "Choose a color theme"},
	}
}

func (m *Model) permissionProfile() string {
	if strings.TrimSpace(m.currentSession.PermissionProfile) != "" {
		return m.currentSession.PermissionProfile
	}
	return m.cfg.Permissions.Profile
}

func (m *Model) mouseStatus() string {
	if m.mouseEnabled {
		return "on"
	}
	return "off"
}

func approvalSummary(item store.Approval) string {
	if strings.TrimSpace(item.Command) != "" {
		return item.Command
	}
	return string(item.Tool)
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
}

func (m *Model) hasPicker() bool {
	return m.picker.visible
}

func (m *Model) closePicker() {
	m.picker = pickerModel{}
}

func (m *Model) hasSessionDialog() bool {
	return m.sessionDialog != nil
}

func (m *Model) closeSessionDialog() {
	m.sessionDialog = nil
}

func (m *Model) hasPreferencesDialog() bool {
	return m.preferences != nil
}

func (m *Model) closePreferencesDialog() {
	m.preferences = nil
}

func (m *Model) hasConnectDialog() bool {
	return m.connectDialog != nil
}

func (m *Model) closeConnectDialog() {
	m.connectDialog = nil
}

func (m *Model) hasDisconnectDialog() bool {
	return m.disconnectDialog != nil
}

func (m *Model) closeDisconnectDialog() {
	m.disconnectDialog = nil
}

func (m *Model) hasModelDialog() bool {
	return m.modelDialog != nil
}

func (m *Model) closeModelDialog() {
	m.modelDialog = nil
}

func (m *Model) openSessionPicker() {
	items := make([]ui.SessionItem, 0, len(m.sessions))
	for _, session := range m.sessions {
		title := strings.TrimSpace(session.Title)
		if title == "" {
			title = fmt.Sprintf("Session #%d", session.ID)
		}
		description := strings.TrimSpace(session.LastMessage)
		if description == "" {
			description = "No messages yet"
		}
		details := []string{
			fmt.Sprintf("Session ID: %d", session.ID),
			fmt.Sprintf("Created:    %s", formatSessionTime(session.CreatedAt)),
			fmt.Sprintf("Changed:    %s", formatSessionTime(session.UpdatedAt)),
			fmt.Sprintf("Title:      %s", truncate(title, 28)),
		}
		if usage, ok := m.sessionUsageSummary(session.ID); ok {
			details = append(details, fmt.Sprintf("Tokens:     in %s  out %s", formatTokens(usage.PromptTokens), formatTokens(usage.CompletionTokens)))
		} else {
			details = append(details, "Tokens:     in -  out -")
		}
		items = append(items, ui.SessionItem{
			Title:       title,
			Description: description,
			Details:     details,
			Value:       strconv.FormatInt(session.ID, 10),
		})
	}
	dialog := ui.NewSessionDialog(items)
	m.sessionDialog = &dialog
}

func (m *Model) openPreferencesDialog() {
	dialog := ui.NewPreferencesDialog(m.cfg.UI, theme.Names())
	m.preferences = &dialog
}

func (m *Model) openConnectDialog() {
	dialog := ui.NewConnectDialog(provider.Catalog(), m.cfg.Providers)
	m.connectDialog = &dialog
}

func (m *Model) openDisconnectDialog() {
	items := make([]ui.ProviderItem, 0, len(m.cfg.Providers))
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
		items = append(items, ui.ProviderItem{
			ID:          id,
			Title:       title,
			Description: desc,
			Details:     details,
		})
	}
	dialog := ui.NewDisconnectDialog(items)
	m.disconnectDialog = &dialog
}

func (m *Model) openModelDialog(providerID string, models []domain.Model) {
	current := m.currentSession.ModelID
	if strings.TrimSpace(current) == "" {
		current = m.cfg.DefaultModel
	}
	dialog := ui.NewModelDialog(providerID, models, current)
	m.modelDialog = &dialog
}

func (m Model) probeProviderCmd(draft provider.ConnectDraft) tea.Cmd {
	return func() tea.Msg {
		result, err := provider.Probe(context.Background(), draft, m.debug)
		return providerProbeMsg{result: result, err: err}
	}
}

func (m Model) loadModelsCmd(providerID string, postConnect bool) tea.Cmd {
	return func() tea.Msg {
		cfg, ok := m.cfg.Provider(providerID)
		if !ok {
			return modelListMsg{providerID: providerID, err: fmt.Errorf("provider %q not configured", providerID)}
		}
		client, err := provider.New(providerID, cfg, m.debug)
		if err != nil {
			return modelListMsg{providerID: providerID, err: err}
		}
		models, err := client.ListModels(context.Background())
		return modelListMsg{providerID: providerID, models: models, postConnect: postConnect, err: err}
	}
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
		next.ContextWindow = 32768
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
	items := make([]pickerItem, 0, len(theme.Names()))
	for _, name := range theme.Names() {
		items = append(items, pickerItem{
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
		title:        "Themes",
		hint:         "type to filter  enter apply  esc cancel",
		items:        items,
		initialValue: current,
	}
	m.refilterPicker()
	m.previewSelectedTheme()
}

func (m *Model) refilterPicker() {
	if !m.hasPicker() {
		return
	}
	query := strings.ToLower(strings.TrimSpace(m.picker.query))
	targetValue := ""
	if item, ok := m.currentPickerItem(); ok {
		targetValue = item.Value
	} else if strings.TrimSpace(m.picker.initialValue) != "" {
		targetValue = m.picker.initialValue
	}
	m.picker.matches = nil
	for _, item := range m.picker.items {
		haystack := strings.ToLower(item.Title + " " + item.Description + " " + item.Value)
		if query == "" || strings.Contains(haystack, query) {
			m.picker.matches = append(m.picker.matches, item)
		}
	}
	if len(m.picker.matches) == 0 {
		m.picker.index = 0
		return
	}
	if targetValue != "" {
		for idx, item := range m.picker.matches {
			if item.Value == targetValue {
				m.picker.index = idx
				m.previewSelectedTheme()
				return
			}
		}
	}
	if m.picker.index >= len(m.picker.matches) {
		m.picker.index = len(m.picker.matches) - 1
	}
	if m.picker.index < 0 {
		m.picker.index = 0
	}
	m.previewSelectedTheme()
}

func (m *Model) movePicker(delta int) {
	if !m.hasPicker() || len(m.picker.matches) == 0 {
		return
	}
	m.picker.index += delta
	if m.picker.index < 0 {
		m.picker.index = 0
	}
	if m.picker.index >= len(m.picker.matches) {
		m.picker.index = len(m.picker.matches) - 1
	}
	m.previewSelectedTheme()
}

func (m *Model) trimPickerQuery() {
	if !m.hasPicker() || m.picker.query == "" {
		return
	}
	m.picker.query = m.picker.query[:len(m.picker.query)-1]
	m.refilterPicker()
}

func (m *Model) updatePickerQuery(msg tea.KeyMsg) bool {
	if !m.hasPicker() {
		return false
	}
	if msg.Type != tea.KeyRunes {
		return false
	}
	m.picker.query += msg.String()
	m.refilterPicker()
	return true
}

func (m *Model) currentPickerItem() (pickerItem, bool) {
	if !m.hasPicker() || len(m.picker.matches) == 0 {
		return pickerItem{}, false
	}
	if m.picker.index < 0 || m.picker.index >= len(m.picker.matches) {
		return pickerItem{}, false
	}
	return m.picker.matches[m.picker.index], true
}

func (m *Model) submitPickerSelection() (tea.Model, tea.Cmd) {
	switch m.picker.mode {
	case pickerModeTheme:
		item, ok := m.currentPickerItem()
		if !ok {
			return m, nil
		}
		if err := m.setTheme(item.Value, true); err != nil {
			m.status = fmt.Sprintf("theme save failed: %v", err)
			return m, nil
		}
		m.status = fmt.Sprintf("Theme set to %s", item.Value)
		m.closePicker()
		return m, nil
	default:
		return m, nil
	}
}

func (m *Model) cancelPicker() (tea.Model, tea.Cmd) {
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
	default:
		m.closePicker()
		return m, nil
	}
}

func (m *Model) previewSelectedTheme() {
	if m.picker.mode != pickerModeTheme {
		return
	}
	item, ok := m.currentPickerItem()
	if !ok {
		return
	}
	if err := m.setTheme(item.Value, false); err != nil {
		m.status = fmt.Sprintf("theme preview failed: %v", err)
	}
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
	m.refreshViewport()
	if save {
		if err := m.cfg.Save(); err != nil {
			return err
		}
	}
	return nil
}

func (m *Model) applyUIConfig(next config.UI, save bool) (tea.Cmd, error) {
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
	m.mouseEnabled = next.Mouse
	applyComposerTheme(&m.composer, selected.Palette)
	m.resize()
	m.refreshViewport()

	if save {
		if err := m.cfg.Save(); err != nil {
			return nil, err
		}
	}

	if prevMouse == m.mouseEnabled {
		return nil, nil
	}
	if m.mouseEnabled {
		return func() tea.Msg { return tea.EnableMouseCellMotion() }, nil
	}
	return func() tea.Msg { return tea.DisableMouse() }, nil
}

func spinnerTickCmd() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg {
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
