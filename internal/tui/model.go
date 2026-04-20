package tui

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/lkarlslund/koder/internal/agent"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/markdown"
	"github.com/lkarlslund/koder/internal/permission"
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

func (s spinnerModel) view() string {
	frames := []string{"[=   ]", "[==  ]", "[=== ]", "[ ===]", "[  ==]", "[   =]"}
	return frames[s.frame%len(frames)]
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

type Model struct {
	cfg            config.Config
	store          *store.Store
	agent          *agent.Engine
	renderer       *markdown.Renderer
	palette        theme.Palette
	sessions       []domain.Session
	currentSession domain.Session
	messages       []domain.Message
	parts          map[int64][]domain.Part
	tasks          []store.Task
	approvals      []store.Approval
	viewport       viewport.Model
	composer       textarea.Model
	width          int
	height         int
	status         string
	loading        bool
	busy           busyModel
	showSidebar    bool
	showReasoning  bool
	slashMatches   []slashCommand
	slashIndex     int
	approvalChoice int
	workdir        string
	workspace      workspace.Status
	startupMode    StartupMode
	picker         pickerModel
	pendingPartID  int64
	mouseEnabled   bool
	sessionDialog  *ui.SessionDialog
	preferences    *ui.PreferencesDialog
}

func New(cfg config.Config, st *store.Store, a *agent.Engine, mode StartupMode) (Model, error) {
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
	}, nil
}

func (m Model) Init() tea.Cmd {
	if !m.mouseEnabled {
		return m.loadCmd()
	}
	return tea.Batch(
		m.loadCmd(),
		func() tea.Msg { return tea.EnableMouseCellMotion() },
	)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resize()
		m.refreshViewport()
		return m, nil
	case spinnerTickMsg:
		if !m.isWorking() {
			return m, nil
		}
		m.busy.spinner.tick()
		m.refreshViewport()
		return m, spinnerTickCmd()
	case promptDoneMsg:
		if msg.err != nil {
			m.status = msg.err.Error()
			m.stopBusy()
			return m, nil
		}
		m.startBusy(m.busy.scopeOrDefault(busyScopeTranscript), m.busy.statusOrDefault("Working ..."))
		return m, tea.Batch(nextEventCmd(msg.events), m.spinnerCmdIfNeeded())
	case runPromptMsg:
		if msg.err != nil {
			m.status = msg.err.Error()
			m.appendLocalAssistantError(msg.err)
			m.stopBusy()
			return m, nil
		}
		m.currentSession = msg.session
		m.startBusy(m.busy.scopeOrDefault(busyScopeTranscript), "Working ...")
		return m, tea.Batch(nextEventCmd(msg.events), m.spinnerCmdIfNeeded())
	case eventMsg:
		m.applyEvent(msg.event)
		if msg.events != nil {
			return m, tea.Batch(m.reloadDetailsCmd(), nextEventCmd(msg.events))
		}
		m.stopBusy()
		return m, m.reloadDetailsCmd()
	case loadMsg:
		m = m.UpdateLoad(msg)
		m.stopBusyWithStatus("Ready")
		return m, nil
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
		m.status = fmt.Sprintf("Started session %d", msg.session.ID)
		m.updateSlashMenu()
		m.refreshViewport()
		return m, nil
	case sessionPickerMsg:
		m.sessions = msg.sessions
		m.openSessionPicker()
		m.stopBusyWithStatus("Select a session to resume")
		return m, nil
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
	main := lipgloss.NewStyle().Padding(0, 1).Render(m.viewport.View())
	if !m.showSidebar {
		return main
	}
	sidebar := lipgloss.NewStyle().
		Width(30).
		Padding(0, 1).
		Background(m.palette.SidebarBackground).
		Foreground(m.palette.SidebarForeground).
		BorderLeft(true).
		BorderForeground(m.palette.SidebarBorder).
		Render(m.renderSidebar())
	return lipgloss.JoinHorizontal(lipgloss.Top, main, sidebar)
}

func (m *Model) renderFooter() string {
	style := lipgloss.NewStyle().BorderTop(true).Padding(0, 1)
	parts := []string{}
	if prompt := m.renderApprovalPrompt(); prompt != "" {
		parts = append(parts, prompt)
	}
	if menu := m.renderSlashMenu(); menu != "" {
		parts = append(parts, menu)
	}
	parts = append(parts, "")
	parts = append(parts, m.renderComposer())
	return style.Render(lipgloss.JoinVertical(lipgloss.Left, parts...))
}

func (m *Model) footerHeight() int {
	return lipgloss.Height(m.renderFooter())
}

func (m *Model) renderComposer() string {
	m.composer.Prompt = m.promptGlyph() + " "
	width := max(1, m.composerWidth())
	prompt := m.composer.Prompt
	promptWidth := ansi.StringWidth(prompt)
	if promptWidth >= width {
		prompt = ansi.Truncate(prompt, max(1, width-1), "")
		promptWidth = ansi.StringWidth(prompt)
	}
	contentWidth := max(0, width-promptWidth)
	promptStyle := lipgloss.NewStyle().
		Background(m.palette.UserTextBackground).
		Foreground(m.palette.UserAccentBar)
	contentStyle := lipgloss.NewStyle().
		Width(contentWidth).
		Background(m.palette.UserTextBackground).
		Foreground(m.palette.UserTextForeground)

	renderBlankLine := func() string {
		return promptStyle.Render(prompt) + contentStyle.Render("")
	}
	renderSeparatorLine := func(char string) string {
		return m.renderHalfBlockLine(width, char)
	}

	middle := lipgloss.NewStyle().
		Width(width).
		Background(m.palette.UserTextBackground).
		Foreground(m.palette.UserTextForeground).
		Render(m.composer.View())
	if strings.TrimSpace(m.composer.Value()) == "" {
		middle = m.renderComposerPlaceholderLine(promptStyle, contentStyle, prompt, contentWidth)
	}

	rendered := []string{}
	if m.halfBlocksEnabled() {
		rendered = append(rendered, renderSeparatorLine("▄"), middle, renderSeparatorLine("▀"))
	} else {
		rendered = append(rendered, renderBlankLine(), middle, renderBlankLine())
	}
	return lipgloss.JoinVertical(lipgloss.Left, rendered...)
}

func (m *Model) renderComposerPlaceholderLine(promptStyle, contentStyle lipgloss.Style, prompt string, contentWidth int) string {
	placeholder := ansi.Truncate(m.composer.Placeholder, contentWidth, "")
	muted := lipgloss.NewStyle().
		Background(m.palette.UserTextBackground).
		Foreground(m.palette.ComposerMutedText)

	m.composer.Cursor.TextStyle = muted
	if placeholder == "" {
		m.composer.Cursor.SetChar(" ")
		return promptStyle.Render(prompt) + contentStyle.Render(m.composer.Cursor.View())
	}

	runes := []rune(placeholder)
	m.composer.Cursor.SetChar(string(runes[0]))
	rest := ""
	if len(runes) > 1 {
		rest = muted.Render(string(runes[1:]))
	}
	return promptStyle.Render(prompt) + contentStyle.Render(m.composer.Cursor.View()+rest)
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
	if width <= 0 {
		return ""
	}
	bar := lipgloss.NewStyle().
		Foreground(m.palette.UserAccentBar).
		Render(char)
	fill := lipgloss.NewStyle().
		Width(max(0, width-1)).
		Foreground(m.palette.UserTextBackground).
		Render(strings.Repeat(char, max(1, width-1)))
	return bar + fill
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
	lines = append(lines, "")
	lines = append(lines, "Keys")
	lines = append(lines, "  enter send/select")
	lines = append(lines, "  tab   autocomplete")
	lines = append(lines, "  ^s    sidebar")
	lines = append(lines, "  ^r    reasoning")
	lines = append(lines, "  /compact")
	lines = append(lines, "  /new  session")
	lines = append(lines, "  /perm profile")
	lines = append(lines, "  /prefs")
	lines = append(lines, "  /quit")
	return strings.Join(lines, "\n")
}

func (m *Model) refreshViewport() {
	if m.currentSession.ID == 0 && len(m.messages) == 0 {
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
		blocks = append(blocks, "Start by asking a question or type / for commands.")
	}
	m.viewport.SetContent(strings.Join(blocks, "\n\n"))
	m.viewport.GotoBottom()
}

func (m *Model) renderTranscriptActivity() string {
	if !m.busy.transcriptActive() {
		return ""
	}
	line := fmt.Sprintf("%s  Working ...", m.workingIndicator())
	return lipgloss.NewStyle().
		Foreground(m.palette.ActivityText).
		Bold(true).
		Padding(0, 1).
		Render(line)
}

func (m *Model) renderTranscriptMessage(msg domain.Message) string {
	body := m.renderMessageParts(m.parts[msg.ID])
	stamp := timestamp(msg.CreatedAt, m.cfg.UI.ShowTimestamps)
	switch msg.Role {
	case domain.MessageRoleUser:
		return m.renderUserMessage(m.renderUserMessageParts(m.parts[msg.ID]), stamp)
	default:
		return m.renderAssistantMessage(body, stamp)
	}
}

func (m *Model) renderUserMessage(body, stamp string) string {
	baseLines := []string{""}
	content := strings.TrimSpace(body)
	if content != "" {
		baseLines = append(baseLines, strings.Split(content, "\n")...)
	}
	if stamp != "" {
		baseLines = append(baseLines, stamp)
	}
	baseLines = append(baseLines, "")

	width := m.userMessageWidth(baseLines)
	bar := m.promptGlyph() + " "
	contentWidth := max(1, width-lipgloss.Width(bar))
	innerWidth := max(1, contentWidth-2)
	barStyle := lipgloss.NewStyle().
		Background(m.palette.UserTextBackground).
		Foreground(m.palette.UserAccentBar)
	contentStyle := lipgloss.NewStyle().
		Background(m.palette.UserTextBackground).
		Foreground(m.palette.UserTextForeground).
		Width(contentWidth).
		Padding(0, 1)
	timestampStyle := contentStyle.Foreground(m.palette.UserTimestampForeground)

	lines := []string{}
	if content != "" {
		for _, line := range strings.Split(content, "\n") {
			lines = append(lines, wrapUserMessageLine(line, innerWidth)...)
		}
	}
	if stamp != "" {
		lines = append(lines, wrapUserMessageLine(stamp, innerWidth)...)
	}

	rendered := make([]string, 0, len(lines))
	stampStart := -1
	if stamp != "" {
		stampStart = len(lines) - len(wrapUserMessageLine(stamp, innerWidth))
	}
	if m.halfBlocksEnabled() {
		rendered = append(rendered, m.renderHalfBlockLine(width, "▄"))
	} else {
		rendered = append(rendered, barStyle.Render(bar)+contentStyle.Render(""))
	}
	for idx, line := range lines {
		prefix := barStyle.Render(bar)
		if stampStart >= 0 && idx >= stampStart {
			rendered = append(rendered, prefix+timestampStyle.Render(line))
			continue
		}
		rendered = append(rendered, prefix+contentStyle.Render(line))
	}
	if m.halfBlocksEnabled() {
		rendered = append(rendered, m.renderHalfBlockLine(width, "▀"))
	} else {
		rendered = append(rendered, barStyle.Render(bar)+contentStyle.Render(""))
	}
	return strings.Join(rendered, "\n")
}

func wrapUserMessageLine(line string, width int) []string {
	if width <= 0 {
		return []string{""}
	}
	if strings.TrimSpace(line) == "" {
		return []string{""}
	}
	wrapped := ansi.Wordwrap(line, width, "")
	lines := strings.Split(wrapped, "\n")
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
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

func (m *Model) userMessageWidth(lines []string) int {
	if m.viewport.Width > 0 {
		return m.viewport.Width
	}
	width := lipgloss.Width("┃ ") + 2
	for _, line := range lines {
		width = max(width, lipgloss.Width(line)+lipgloss.Width("┃ ")+2)
	}
	return width
}

func (m *Model) renderAssistantMessage(body, stamp string) string {
	body = strings.TrimSpace(body)
	if stamp == "" {
		return body
	}
	header := lipgloss.NewStyle().
		Foreground(m.palette.AssistantTimestampText).
		Render(stamp)
	return header + "\n" + body
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
	content := strings.TrimSpace(input)
	if content == "" {
		return ""
	}
	lineStyle := lipgloss.NewStyle().
		Background(m.palette.ReasoningBackground).
		Foreground(m.palette.ReasoningText)

	lines := append([]string{""}, strings.Split(content, "\n")...)
	rendered := make([]string, 0, len(lines))
	for _, line := range lines {
		rendered = append(rendered, lineStyle.Render(line))
	}
	return strings.Join(rendered, "\n")
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

func Run(cfg config.Config, st *store.Store, a *agent.Engine, mode StartupMode) error {
	model, err := New(cfg, st, a, mode)
	if err != nil {
		return err
	}
	p := tea.NewProgram(model, tea.WithAltScreen())
	finalModel, err := p.Run()
	if err != nil {
		return err
	}
	if typed, ok := finalModel.(Model); ok {
		_ = typed
	}
	return nil
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
	case trimmed == "/theme":
		m.composer.Reset()
		m.updateSlashMenu()
		m.openThemePicker()
		return m, nil, true
	case trimmed == "/prefs":
		m.composer.Reset()
		m.updateSlashMenu()
		m.openPreferencesDialog()
		return m, nil, true
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

func (m *Model) workingIndicator() string {
	if !m.busy.spinner.active {
		return ""
	}
	return m.busy.spinner.view()
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
	var lines []string
	title := lipgloss.NewStyle().Bold(true).Render("Commands")
	lines = append(lines, title)
	for idx := start; idx < end; idx++ {
		item := m.slashMatches[idx]
		line := fmt.Sprintf("%-12s %s", item.Name, item.Description)
		if idx == m.slashIndex {
			line = lipgloss.NewStyle().Reverse(true).Render(line)
		}
		lines = append(lines, line)
	}
	return lipgloss.NewStyle().Border(lipgloss.NormalBorder()).Padding(0, 1).Render(strings.Join(lines, "\n"))
}

func (m *Model) renderPicker() string {
	if !m.hasPicker() {
		return ""
	}
	var lines []string
	lines = append(lines, lipgloss.NewStyle().Bold(true).Render(m.picker.title))
	if hint := strings.TrimSpace(m.picker.hint); hint != "" {
		lines = append(lines, hint)
	}
	lines = append(lines, "")
	lines = append(lines, fmt.Sprintf("filter: %s", m.picker.query))
	lines = append(lines, "")
	if len(m.picker.matches) == 0 {
		lines = append(lines, "  no matches")
	} else {
		start := 0
		if m.picker.index >= 6 {
			start = m.picker.index - 5
		}
		end := min(len(m.picker.matches), start+8)
		for idx := start; idx < end; idx++ {
			item := m.picker.matches[idx]
			cursor := " "
			if idx == m.picker.index {
				cursor = ">"
			}
			lines = append(lines, fmt.Sprintf("%s %s", cursor, item.Title))
			if desc := strings.TrimSpace(item.Description); desc != "" {
				lines = append(lines, fmt.Sprintf("    %s", truncate(desc, 40)))
			}
		}
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1).
		Render(strings.Join(lines, "\n"))
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
			return m, nil
		}
		return m, cmd
	case ui.PreferencesActionApply:
		cmd, err := m.applyUIConfig(action.UI, true)
		if err != nil {
			m.status = fmt.Sprintf("preferences save failed: %v", err)
			return m, nil
		}
		m.closePreferencesDialog()
		m.status = "Preferences saved"
		return m, cmd
	case ui.PreferencesActionCancel:
		cmd, err := m.applyUIConfig(action.UI, false)
		if err != nil {
			m.status = fmt.Sprintf("preferences restore failed: %v", err)
			return m, nil
		}
		m.closePreferencesDialog()
		m.status = "Preferences cancelled"
		return m, cmd
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
	title := lipgloss.NewStyle().Bold(true).Render("Permission required")
	body := fmt.Sprintf("#%d  %s  %s", item.ID, item.Tool, truncate(approvalSummary(item), max(24, m.viewport.Width-10)))
	approve := approvalOption("Approve", m.approvalChoice == 0)
	deny := approvalOption("Deny", m.approvalChoice == 1)
	hints := "enter select  tab switch  y approve  n deny"
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1).
		Render(strings.Join([]string{title, body, lipgloss.JoinHorizontal(lipgloss.Left, approve, "  ", deny), hints}, "\n"))
}

func approvalOption(label string, selected bool) string {
	style := lipgloss.NewStyle().Padding(0, 1)
	if selected {
		style = style.Reverse(true).Bold(true)
	}
	return style.Render(label)
}

func internalSlashCommands() []slashCommand {
	return []slashCommand{
		{Name: "/compact", Description: "Summarize old context"},
		{Name: "/new", Description: "Start a new session"},
		{Name: "/mouse", Description: "Toggle mouse capture", NeedsArgs: true, Autocomplete: "/mouse "},
		{Name: "/perm", Description: "Set permission profile", NeedsArgs: true, Autocomplete: "/perm "},
		{Name: "/prefs", Description: "Open preferences"},
		{Name: "/quit", Description: "Quit koder"},
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
