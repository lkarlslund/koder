package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/lkarlslund/koder/internal/agent"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/markdown"
	"github.com/lkarlslund/koder/internal/permission"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/workspace"
)

type promptDoneMsg struct {
	events <-chan domain.Event
	err    error
}

type spinnerTickMsg struct{}

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
	showSidebar    bool
	showReasoning  bool
	slashMatches   []slashCommand
	slashIndex     int
	approvalChoice int
	workdir        string
	workspace      workspace.Status
	startupMode    StartupMode
	pickerVisible  bool
	pickerIndex    int
	spinnerFrame   int
	pendingPartID  int64
}

func New(cfg config.Config, st *store.Store, a *agent.Engine, mode StartupMode) (Model, error) {
	renderer, err := markdown.New()
	if err != nil {
		return Model{}, err
	}
	composer := textarea.New()
	composer.Placeholder = "Ask koder or type / for commands"
	composer.SetWidth(40)
	composer.SetHeight(4)
	composer.Focus()
	composer.ShowLineNumbers = false

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
		viewport:      vp,
		composer:      composer,
		showSidebar:   cfg.UI.ShowSidebar,
		showReasoning: cfg.UI.ShowReasoning,
		parts:         make(map[int64][]domain.Part),
		status:        "Ready",
		workdir:       workdir,
		startupMode:   mode,
	}, nil
}

func (m Model) Init() tea.Cmd {
	return m.loadCmd()
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
		m.spinnerFrame++
		return m, spinnerTickCmd()
	case promptDoneMsg:
		if msg.err != nil {
			m.status = msg.err.Error()
			m.loading = false
			return m, nil
		}
		m.loading = true
		m.status = "Waiting for model…"
		return m, tea.Batch(nextEventCmd(msg.events), spinnerTickCmd())
	case runPromptMsg:
		if msg.err != nil {
			m.status = msg.err.Error()
			m.loading = false
			return m, nil
		}
		m.currentSession = msg.session
		m.loading = true
		m.status = "Waiting for model…"
		return m, tea.Batch(nextEventCmd(msg.events), spinnerTickCmd())
	case eventMsg:
		m.applyEvent(msg.event)
		if msg.events != nil {
			return m, nextEventCmd(msg.events)
		}
		m.loading = false
		return m, m.reloadDetailsCmd()
	case loadMsg:
		m = m.UpdateLoad(msg)
		m.loading = false
		m.status = "Ready"
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
		m.pickerVisible = false
		m.pickerIndex = 0
		m.status = fmt.Sprintf("Started session %d", msg.session.ID)
		m.updateSlashMenu()
		m.refreshViewport()
		return m, nil
	case sessionPickerMsg:
		m.sessions = msg.sessions
		m.pickerVisible = true
		m.pickerIndex = 0
		m.loading = false
		m.status = "Select a session to resume"
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	var cmd tea.Cmd
	m.composer, cmd = m.composer.Update(msg)
	m.updateSlashMenu()
	return m, cmd
}

func (m Model) View() string {
	header := m.renderHeader()
	body := m.renderBody()
	footer := m.renderFooter()
	view := lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
	if m.pickerVisible {
		return lipgloss.JoinVertical(lipgloss.Left, view, m.renderSessionPicker())
	}
	return view
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.pickerVisible {
		switch msg.String() {
		case "up":
			if m.pickerIndex > 0 {
				m.pickerIndex--
			}
			return m, nil
		case "down":
			if m.pickerIndex < len(m.sessions)-1 {
				m.pickerIndex++
			}
			return m, nil
		case "enter":
			if len(m.sessions) == 0 {
				m.loading = true
				m.status = "Creating session…"
				return m, m.newSessionCmd()
			}
			m.loading = true
			m.status = fmt.Sprintf("Resuming session %d…", m.sessions[m.pickerIndex].ID)
			return m, m.loadSessionCmd(m.sessions[m.pickerIndex].ID)
		case "esc":
			m.loading = true
			m.status = "Creating session…"
			return m, m.newSessionCmd()
		case "ctrl+c":
			return m.quit()
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
				m.loading = true
				m.status = "Creating session…"
				return m, m.newSessionCmd()
			}
			if len(m.slashMatches) == 1 && m.slashMatches[0].Name == prompt {
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
		m.loading = true
		m.status = "Running…"
		return m, m.promptCmd(prompt)
	}

	var cmd tea.Cmd
	m.composer, cmd = m.composer.Update(msg)
	m.updateSlashMenu()
	return m, cmd
}

func (m *Model) applyEvent(evt domain.Event) {
	switch evt.Kind {
	case domain.EventKindMessageDelta:
		m.status = "Streaming response…"
	case domain.EventKindReasoning:
		m.status = "Receiving reasoning…"
	case domain.EventKindToolResult:
		m.status = fmt.Sprintf("Tool %s finished", evt.Tool)
	case domain.EventKindApprovalAsk:
		m.status = evt.Text
	case domain.EventKindApprovalReply:
		m.status = evt.Text
	case domain.EventKindTaskUpdate:
		m.status = "Task updated"
	case domain.EventKindUsage:
		m.status = fmt.Sprintf("Usage total=%d", evt.Usage.TotalTokens)
	case domain.EventKindStatus:
		if evt.Text != "" {
			m.status = evt.Text
		}
		if profile := strings.TrimSpace(evt.Meta["permission_profile"]); profile != "" {
			m.currentSession.PermissionProfile = profile
		}
	case domain.EventKindError:
		if evt.Err != nil {
			m.status = evt.Err.Error()
		}
	case domain.EventKindMessageDone:
		m.status = "Ready"
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
	bodyHeight := m.height - 10
	if bodyHeight < 5 {
		bodyHeight = 5
	}
	m.viewport.Width = bodyWidth
	m.viewport.Height = bodyHeight
	m.composer.SetWidth(bodyWidth - 2)
}

func (m *Model) renderHeader() string {
	style := lipgloss.NewStyle().Bold(true).Padding(0, 1)
	model := m.currentSession.ModelID
	if model == "" {
		model = "(unset)"
	}
	return style.Render(fmt.Sprintf(
		"koder%s  session:%d  profile:%s  provider:%s  model:%s  status:%s",
		m.workingIndicator(), m.currentSession.ID, m.permissionProfile(), m.currentSession.ProviderID, model, m.status,
	))
}

func (m *Model) renderBody() string {
	main := lipgloss.NewStyle().Padding(0, 1).Render(m.viewport.View())
	if !m.showSidebar {
		return main
	}
	sidebar := lipgloss.NewStyle().Width(30).Padding(0, 1).BorderLeft(true).Render(m.renderSidebar())
	return lipgloss.JoinHorizontal(lipgloss.Top, main, sidebar)
}

func (m *Model) renderFooter() string {
	style := lipgloss.NewStyle().BorderTop(true).Padding(0, 1)
	help := "enter send/select  tab autocomplete  ctrl+s sidebar  ctrl+r reasoning  /new session  /perm profile  /quit"
	parts := []string{help}
	if prompt := m.renderApprovalPrompt(); prompt != "" {
		parts = append(parts, prompt)
	}
	if menu := m.renderSlashMenu(); menu != "" {
		parts = append(parts, menu)
	}
	parts = append(parts, m.composer.View())
	return style.Render(lipgloss.JoinVertical(lipgloss.Left, parts...))
}

func (m *Model) renderSidebar() string {
	var lines []string
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
	return strings.Join(lines, "\n")
}

func (m *Model) refreshViewport() {
	if m.currentSession.ID == 0 {
		m.viewport.SetContent("No session")
		return
	}
	var blocks []string
	for _, msg := range m.messages {
		body := m.renderMessageParts(m.parts[msg.ID])
		title := fmt.Sprintf("[%s] %s", msg.Role, timestamp(msg.CreatedAt, m.cfg.UI.ShowTimestamps))
		blocks = append(blocks, lipgloss.NewStyle().Bold(true).Render(strings.TrimSpace(title))+"\n"+body)
	}
	if len(blocks) == 0 {
		blocks = append(blocks, "Start by asking a question or type / for commands.")
	}
	m.viewport.SetContent(strings.Join(blocks, "\n\n"))
	m.viewport.GotoBottom()
}

func (m *Model) renderMessageParts(parts []domain.Part) string {
	var blocks []string
	var reasoningBlocks []string
	var textBlocks []string
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
		default:
			flushText()
			flushReasoning()
			blocks = append(blocks, part.Body)
		}
	}

	flushText()
	flushReasoning()

	blocks = append(blocks, reasoningBlocks...)
	blocks = append(blocks, textBlocks...)

	return strings.TrimSpace(strings.Join(blocks, "\n"))
}

func (m *Model) renderReasoningBlock(input string) string {
	content := strings.TrimSpace(input)
	if content == "" {
		return ""
	}
	lineStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("236")).
		Foreground(lipgloss.Color("252"))

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
	p := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion())
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
	m.pickerVisible = false
	m.refreshViewport()
	return m
}

func (m *Model) handleLocalCommand(prompt string) (tea.Model, tea.Cmd, bool) {
	switch strings.TrimSpace(prompt) {
	case "/new":
		m.composer.Reset()
		m.updateSlashMenu()
		m.loading = true
		m.status = "Creating session…"
		return m, m.newSessionCmd(), true
	case "/quit":
		m.composer.Reset()
		m.updateSlashMenu()
		model, cmd := m.quit()
		return model, cmd, true
	default:
		return nil, nil, false
	}
}

func (m *Model) quit() (tea.Model, tea.Cmd) {
	m.loading = false
	m.status = "Quitting"
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

func (m *Model) nextPendingID() int64 {
	m.pendingPartID--
	if m.pendingPartID == 0 {
		m.pendingPartID = -1
	}
	return m.pendingPartID
}

func (m *Model) isWorking() bool {
	return m.loading
}

func (m *Model) workingIndicator() string {
	if !m.isWorking() {
		return ""
	}
	frames := []string{" [·  ]", " [·· ]", " [···]", " [ ··]", " [  ·]"}
	return frames[m.spinnerFrame%len(frames)]
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

func (m *Model) renderSessionPicker() string {
	var lines []string
	lines = append(lines, lipgloss.NewStyle().Bold(true).Render("Resume Session"))
	lines = append(lines, "enter resume  esc new session  ctrl+c quit")
	lines = append(lines, "")
	for idx, session := range m.sessions {
		cursor := " "
		if idx == m.pickerIndex {
			cursor = ">"
		}
		title := truncate(session.Title, 18)
		if strings.TrimSpace(title) == "" {
			title = "Untitled"
		}
		last := truncate(session.LastMessage, 28)
		lines = append(lines, fmt.Sprintf("%s #%d  %s", cursor, session.ID, title))
		if last != "" {
			lines = append(lines, fmt.Sprintf("    %s", last))
		}
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1).
		Render(strings.Join(lines, "\n"))
}

func (m *Model) hasApprovalPrompt() bool {
	return !m.loading && len(m.approvals) > 0
}

func (m *Model) submitApprovalChoice(approve bool) (tea.Model, tea.Cmd) {
	if !m.hasApprovalPrompt() {
		return m, nil
	}
	id := m.approvals[0].ID
	command := fmt.Sprintf("/deny %d", id)
	m.status = fmt.Sprintf("Denying approval %d…", id)
	if approve {
		command = fmt.Sprintf("/approve %d", id)
		m.status = fmt.Sprintf("Approving approval %d…", id)
	}
	m.loading = true
	return m, m.promptCmd(command)
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

func slashCommands() []slashCommand {
	return []slashCommand{
		{Name: "/new", Description: "Start a new session"},
		{Name: "/perm", Description: "Set permission profile", NeedsArgs: true, Autocomplete: "/perm "},
		{Name: "/quit", Description: "Quit koder"},
		{Name: "/read", Description: "Read a file", NeedsArgs: true, Autocomplete: "/read "},
		{Name: "/glob", Description: "Find files by glob", NeedsArgs: true, Autocomplete: "/glob "},
		{Name: "/grep", Description: "Search text in files", NeedsArgs: true, Autocomplete: "/grep "},
		{Name: "/bash", Description: "Run a shell command", NeedsArgs: true, Autocomplete: "/bash "},
		{Name: "/task", Description: "Add a tracked task", NeedsArgs: true, Autocomplete: "/task "},
		{Name: "/fetch", Description: "Fetch a URL", NeedsArgs: true, Autocomplete: "/fetch "},
		{Name: "/patch", Description: "Replace a file with content", NeedsArgs: true, Autocomplete: "/patch "},
		{Name: "/approve", Description: "Approve a pending action", NeedsArgs: true, Autocomplete: "/approve "},
		{Name: "/deny", Description: "Deny a pending action", NeedsArgs: true, Autocomplete: "/deny "},
	}
}

func (m *Model) permissionProfile() string {
	if strings.TrimSpace(m.currentSession.PermissionProfile) != "" {
		return m.currentSession.PermissionProfile
	}
	return m.cfg.Permissions.Profile
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
	added := lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Render(fmt.Sprintf("+%d", item.Additions))
	deleted := lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Render(fmt.Sprintf("-%d", item.Deletions))
	return base + " " + added + " " + deleted
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
	for _, item := range slashCommands() {
		name := strings.TrimPrefix(strings.ToLower(item.Name), "/")
		if query == "" || strings.HasPrefix(name, query) {
			matches = append(matches, item)
		}
	}
	return matches
}
