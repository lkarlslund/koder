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

	"github.com/lkarlslund/koder/internal/agent"
	"github.com/lkarlslund/koder/internal/attachment"
	kclipboard "github.com/lkarlslund/koder/internal/clipboard"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/debugsrv"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/execruntime"
	"github.com/lkarlslund/koder/internal/markdown"
	kodermcp "github.com/lkarlslund/koder/internal/mcp"
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
	"github.com/lkarlslund/koder/internal/version"
	"github.com/lkarlslund/koder/internal/workspace"
)

type promptDoneMsg struct {
	events <-chan domain.Event
	err    error
}

type spinnerTickMsg struct{}
type execEventMsg struct {
	chatID int64
	seq    uint64
	event  execruntime.Event
	ok     bool
}
type bouncyBallsTickMsg struct {
	At  time.Time
	Seq uint64
}
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
	chatID int64
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
	chat      domain.Chat
	chats     []domain.Chat
	sessions  []domain.Session
	messages  []domain.Message
	parts     map[int64][]domain.Part
	approvals []store.Approval
	plan      store.MilestonePlan
	todos     []store.TodoItem
	workspace workspace.Status
}

type sessionPickerMsg struct {
	sessions []domain.Session
}

type pickerMode int

const (
	pickerModeNone pickerMode = iota
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
	chat           domain.Chat
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

type bangFollowupMode int

const (
	bangFollowupNone bangFollowupMode = iota
	bangFollowupPrompt
	bangFollowupQueue
	bangFollowupSteer
)

type bangCommandMsg struct {
	session        domain.Session
	chat           domain.Chat
	command        string
	events         []domain.Event
	err            error
	followupMode   bangFollowupMode
	followupPrompt string
	preserveBusy   bool
}

type queuedContinueDispatchMsg struct{}

type mcpReloadMsg struct {
	servers []kodermcp.ServerState
	err     error
}

type queuePersistMsg struct {
	chatID int64
	items  []domain.QueuedInput
	err    error
}

var lastQueuedInputID int64

func nextQueuedInputID() int64 {
	now := time.Now().UTC().UnixNano()
	if now <= lastQueuedInputID {
		lastQueuedInputID++
		return lastQueuedInputID
	}
	lastQueuedInputID = now
	return now
}

func cloneQueuedInputs(src []domain.QueuedInput) []domain.QueuedInput {
	if len(src) == 0 {
		return nil
	}
	dst := make([]domain.QueuedInput, 0, len(src))
	for _, item := range src {
		cloned := item
		cloned.Attachments = append([]domain.QueuedAttachment(nil), item.Attachments...)
		cloned.References = append([]domain.QueuedReference(nil), item.References...)
		dst = append(dst, cloned)
	}
	return dst
}

func queuedAttachmentsFromDrafts(src []attachment.Draft) []domain.QueuedAttachment {
	if len(src) == 0 {
		return nil
	}
	dst := make([]domain.QueuedAttachment, 0, len(src))
	for _, draft := range src {
		dst = append(dst, domain.QueuedAttachment{
			ID:       draft.ID,
			Name:     draft.Name,
			MIME:     draft.MIME,
			Path:     draft.Path,
			Size:     draft.Size,
			Source:   draft.Source,
			Original: draft.Original,
		})
	}
	return dst
}

func queuedReferencesFromDrafts(src []reference.Draft) []domain.QueuedReference {
	if len(src) == 0 {
		return nil
	}
	dst := make([]domain.QueuedReference, 0, len(src))
	for _, draft := range src {
		dst = append(dst, domain.QueuedReference{
			Kind:    string(draft.Kind),
			Path:    draft.Path,
			Display: draft.Display,
			Start:   draft.Start,
			End:     draft.End,
		})
	}
	return dst
}

func queuedAttachmentDrafts(src []domain.QueuedAttachment) []attachment.Draft {
	if len(src) == 0 {
		return nil
	}
	dst := make([]attachment.Draft, 0, len(src))
	for _, item := range src {
		dst = append(dst, attachment.Draft{Metadata: attachment.Metadata{
			ID:       item.ID,
			Name:     item.Name,
			MIME:     item.MIME,
			Path:     item.Path,
			Size:     item.Size,
			Source:   item.Source,
			Original: item.Original,
		}})
	}
	return dst
}

func draftAttachmentToken(draft attachment.Draft) string {
	if strings.TrimSpace(draft.Token) != "" {
		return draft.Token
	}
	switch {
	case draft.Source == attachment.SourceClipboardImage || attachment.ClassifyMIME(draft.MIME) == attachment.KindImage:
		return "[clipboard image]"
	case attachment.ClassifyMIME(draft.MIME) == attachment.KindPDF:
		return "[pdf]"
	case attachment.ClassifyMIME(draft.MIME) == attachment.KindText:
		return "[file]"
	default:
		return "[file]"
	}
}

func numberDraftAttachmentTokens(drafts []attachment.Draft) {
	imageCount := 0
	fileCount := 0
	pdfCount := 0
	for i := range drafts {
		if strings.TrimSpace(drafts[i].Token) != "" {
			continue
		}
		switch {
		case drafts[i].Source == attachment.SourceClipboardImage || attachment.ClassifyMIME(drafts[i].MIME) == attachment.KindImage:
			imageCount++
			drafts[i].Token = fmt.Sprintf("[clipboard image #%d]", imageCount)
		case attachment.ClassifyMIME(drafts[i].MIME) == attachment.KindPDF:
			pdfCount++
			drafts[i].Token = fmt.Sprintf("[pdf #%d]", pdfCount)
		default:
			fileCount++
			drafts[i].Token = fmt.Sprintf("[file #%d]", fileCount)
		}
	}
}

func queuedReferenceDrafts(src []domain.QueuedReference) []reference.Draft {
	if len(src) == 0 {
		return nil
	}
	dst := make([]reference.Draft, 0, len(src))
	for _, item := range src {
		dst = append(dst, reference.Draft{
			Kind:    reference.Kind(item.Kind),
			Path:    item.Path,
			Display: item.Display,
			Start:   item.Start,
			End:     item.End,
		})
	}
	return dst
}

func queuedInputBadge(item domain.QueuedInput) string {
	if item.Held {
		return "HELD"
	}
	switch item.Kind {
	case domain.QueuedInputKindSteer:
		return "STEER"
	case domain.QueuedInputKindContinue:
		return "CONTINUE"
	case domain.QueuedInputKindRejectedSteer:
		return "RETRY"
	default:
		return "QUEUED"
	}
}

func queuedInputStatusText(kind domain.QueuedInputKind, held bool) string {
	if held {
		return "Held queue item"
	}
	switch kind {
	case domain.QueuedInputKindSteer:
		return "Queued steer for the current run"
	case domain.QueuedInputKindContinue:
		return "Queued continue for next turn"
	case domain.QueuedInputKindRejectedSteer:
		return "Queued steer for retry after the current run"
	default:
		return "Queued prompt for next turn"
	}
}

func queuedInputRunStatus(kind domain.QueuedInputKind) string {
	switch kind {
	case domain.QueuedInputKindSteer:
		return "Applying queued steer…"
	case domain.QueuedInputKindContinue:
		return "Continuing…"
	case domain.QueuedInputKindRejectedSteer:
		return "Running queued steer…"
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

type contextWindowMsg struct {
	providerID    string
	contextWindow int
	checked       bool
	err           error
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
	cfg                     config.Config
	store                   *store.Store
	agent                   *agent.Engine
	exec                    *execruntime.Manager
	renderer                *markdown.Renderer
	palette                 theme.Palette
	sessions                []domain.Session
	chats                   []domain.Chat
	currentSession          domain.Session
	currentChat             domain.Chat
	messages                []domain.Message
	parts                   map[int64][]domain.Part
	milestonePlan           store.MilestonePlan
	todos                   []store.TodoItem
	approvals               []store.Approval
	viewport                transcriptViewport
	transcriptControls      []ui.Control
	retainedTranscript      *ui.RetainedTranscript
	transcriptItems         []transcriptItemController
	messageItemIndexByID    map[int64]int
	toolRunItemIndexByID    map[string]int
	toolRunItemIndexByAppr  map[int64]int
	pendingTranscriptIndex  int
	transcriptDirty         bool
	mainScreen              *mainScreenWidget
	renderCache             *modelRenderCache
	expandedToolRuns        map[string]bool
	expandedToolRunCommands map[string]bool
	composer                textarea.Model
	composerQueries         composerQueryState
	composerHistory         composerHistoryState
	composerCursorDirty     bool
	width                   int
	height                  int
	status                  string
	loading                 bool
	busy                    busyModel
	chatBusy                map[int64]busyModel
	showSidebar             bool
	sidebarWidthOverride    int
	showReasoning           bool
	showSystem              bool
	slashMatches            []slashCommand
	slashIndex              int
	skillMatches            []skills.Skill
	skillIndex              int
	mentionMatches          []reference.Entry
	mentionIndex            int
	mentionCatalog          []reference.Entry
	approvalDialog          *dialogs.ApprovalDialog
	workdir                 string
	workspace               workspace.Status
	agentsDrift             bool
	startupMode             StartupMode
	startupOptions          StartupOptions
	picker                  pickerModel
	pendingPartID           int64
	mouseEnabled            bool
	sessionDialog           *dialogs.SessionDialog
	preferences             *dialogs.PreferencesDialog
	agentsModal             *ui.Modal
	helpModal               *ui.Modal
	helpBody                string
	helpYOffset             int
	helpWidth               int
	helpHeight              int
	llmPreviewTitle         string
	llmPreviewBody          string
	llmPreviewYOffset       int
	llmPreviewWidth         int
	llmPreviewHeight        int
	connectDialog           *dialogs.ConnectDialog
	disconnectDialog        *dialogs.DisconnectDialog
	modelDialog             *dialogs.ModelDialog
	mcpDialog               *dialogs.MCPDialog
	toolsDialog             *dialogs.ToolsDialog
	themeDialog             *dialogs.ThemeDialog
	themeDialogInitial      string
	mainWindowView          *modelWindow
	debug                   *debugsrv.Recorder
	uiRoot                  *ui.Root
	execEvents              <-chan execruntime.Event
	execCancel              func()
	execSubscriptionChatID  int64
	execSubscriptionSeq     uint64
	rootTimerSeq            uint64
	rootTimerPending        bool
	rootTimerPendingAt      time.Time
	bouncyBalls             bouncyBallsOverlay
	caps                    *provider.CapabilityStore
	runtimeCtxChecked       map[string]bool
	activeOpCancel          context.CancelFunc
	activeOpCancels         map[int64]context.CancelFunc
	queueEditMode           bool
	queueSelection          int
	pendingModelNote        string
	pendingAssistant        pendingAssistantTurn
	draftAttachments        []attachment.Draft
	draftReferences         []reference.Draft
	attachmentFiles         *attachment.Manager
	interruptArmedAt        time.Time
	readClipboardText       func() (string, error)
	readClipboardImage      func() ([]byte, error)
	writeClipboardText      func(string) error
	debugRuntimeHash        uint64
	debugFrameLastSync      time.Time
}

type pendingAssistantTurn struct {
	Text      string
	Reasoning string
	CreatedAt time.Time
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

func New(cfg config.Config, st *store.Store, a *agent.Engine, mode StartupMode, debug *debugsrv.Recorder) (Model, error) {
	workdir, err := os.Getwd()
	if err != nil {
		return Model{}, err
	}
	return NewWithWorkdir(cfg, st, a, mode, debug, workdir, StartupOptions{})
}

func NewWithWorkdir(cfg config.Config, st *store.Store, a *agent.Engine, mode StartupMode, debug *debugsrv.Recorder, workdir string, startupOpts StartupOptions) (Model, error) {
	tuiTheme := theme.Resolve(cfg.UI.Theme)
	renderer, err := markdown.New(tuiTheme.Palette, cfg.UI.CodeStyle)
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

	model := Model{
		cfg:                     cfg,
		store:                   st,
		agent:                   a,
		renderer:                renderer,
		palette:                 tuiTheme.Palette,
		viewport:                vp,
		renderCache:             &modelRenderCache{},
		composer:                composer,
		showSidebar:             cfg.UI.ShowSidebar,
		showReasoning:           cfg.UI.ShowReasoning,
		showSystem:              cfg.UI.ShowSystem,
		expandedToolRuns:        make(map[string]bool),
		expandedToolRunCommands: make(map[string]bool),
		pendingTranscriptIndex:  -1,
		messageItemIndexByID:    make(map[int64]int),
		toolRunItemIndexByID:    make(map[string]int),
		toolRunItemIndexByAppr:  make(map[int64]int),
		transcriptDirty:         true,
		parts:                   make(map[int64][]domain.Part),
		status:                  "Ready",
		workdir:                 workdir,
		startupMode:             mode,
		startupOptions:          startupOpts,
		mouseEnabled:            cfg.UI.Mouse,
		debug:                   debug,
		caps:                    provider.NewCapabilityStore(cfg.StateDir()),
		runtimeCtxChecked:       map[string]bool{},
		attachmentFiles:         attachment.NewManager(cfg.StateDir()),
	}
	if a != nil {
		model.exec = a.ExecManager()
	}
	model.syncComposerVisibility()
	return model, nil
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
	case bouncyBallsTickMsg:
		if msg.Seq != m.bouncyBalls.tickSeq {
			return m, nil
		}
		m.bouncyBalls.tickPending = false
		m.bouncyBalls.tickPendingAt = time.Time{}
		if !m.bouncyBalls.Enabled {
			return m, nil
		}
		_ = m.bouncyBalls.StepAt(msg.At, max(0, m.width), max(0, m.height))
		return m, nil
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
	case execEventMsg:
		if msg.seq != m.execSubscriptionSeq || msg.chatID != m.execSubscriptionChatID {
			return m, nil
		}
		if !msg.ok {
			return m, m.refreshExecSubscriptionCmd()
		}
		wasAtBottom := m.viewport.AtBottom()
		offset := m.viewport.YOffset
		m.invalidateBodyCache()
		if wasAtBottom {
			m.refreshViewport()
		} else {
			m.refreshViewportAt(offset)
		}
		return m, m.waitForExecEventCmd()
	case promptDoneMsg:
		m.pendingAssistant = pendingAssistantTurn{}
		m.invalidateBodyCache()
		if msg.err != nil {
			return m.finishOperationWithError(msg.err)
		}
		m.startBusy(m.busy.scopeOrDefault(busyScopeTranscript), m.busy.statusOrDefault("Working ..."))
		return m, ui.Batch(nextEventCmd(m.currentChat.ID, msg.events), m.spinnerCmdIfNeeded(), m.syncWindowTitleCmd())
	case runPromptMsg:
		m.pendingAssistant = pendingAssistantTurn{}
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
		m.currentChat = msg.chat
		m.clampQueueSelection()
		m.pendingModelNote = ""
		m.startBusy(m.busy.scopeOrDefault(busyScopeTranscript), "Working ...")
		return m, ui.Batch(nextEventCmd(msg.chat.ID, msg.events), m.spinnerCmdIfNeeded(), m.refreshExecSubscriptionCmd(), m.syncWindowTitleCmd())
	case bangCommandMsg:
		m.invalidateBodyCache()
		if msg.err != nil {
			if msg.preserveBusy {
				m.status = msg.err.Error()
				m.appendLocalAssistantError(msg.err)
				return m, ui.Batch(m.reloadDetailsCmd(), m.syncWindowTitleCmd())
			}
			return m.finishOperationWithError(msg.err)
		}
		if msg.session.ID != 0 {
			m.currentSession = msg.session
		}
		if msg.chat.ID != 0 {
			m.currentChat = msg.chat
			m.clampQueueSelection()
		}
		for _, evt := range msg.events {
			m.recordEvent(msg.chat.ID, evt)
			if !msg.preserveBusy {
				m.applyEvent(evt)
			}
		}
		reload := m.reloadDetailsCmd()
		switch msg.followupMode {
		case bangFollowupPrompt:
			m.appendLocalUserPrompt(msg.followupPrompt, nil, nil)
			m.startBusy(busyScopeTranscript, "Running…")
			return m, ui.Batch(reload, m.promptCmd(m.beginActiveOperation(), msg.followupPrompt, nil, nil), m.spinnerCmdIfNeeded(), m.syncWindowTitleCmd())
		case bangFollowupQueue, bangFollowupSteer:
			kind := domain.QueuedInputKindQueued
			if msg.followupMode == bangFollowupSteer {
				kind = domain.QueuedInputKindSteer
			}
			items := cloneQueuedInputs(m.currentChat.QueuedInputs)
			items = append(items, domain.QueuedInput{
				ID:        nextQueuedInputID(),
				Kind:      kind,
				Text:      msg.followupPrompt,
				CreatedAt: time.Now().UTC(),
			})
			m.setQueuedInputs(items)
			m.status = queuedInputStatusText(kind, false)
			return m, ui.Batch(reload, m.saveQueuedInputsCmd(m.currentChat.ID, items), m.syncWindowTitleCmd())
		default:
			if msg.preserveBusy {
				m.status = "Command finished"
				return m, ui.Batch(reload, m.refreshExecSubscriptionCmd(), m.syncWindowTitleCmd())
			}
			m.stopBusyWithStatus("Command finished")
			return m, ui.Batch(reload, m.refreshExecSubscriptionCmd(), m.syncWindowTitleCmd())
		}
	case kickoffPromptMsg:
		return m, ui.Batch(m.promptCmd(m.beginActiveOperation(), msg.Prompt, msg.Attachments, msg.References), m.spinnerCmdIfNeeded(), m.syncWindowTitleCmd())
	case queuedContinueDispatchMsg:
		return m, ui.Batch(m.continueCmd(m.beginActiveOperation()), m.spinnerCmdIfNeeded(), m.syncWindowTitleCmd())
	case queuePersistMsg:
		if msg.err != nil {
			m.stopBusy()
			m.status = msg.err.Error()
			return m, m.syncWindowTitleCmd()
		}
		if msg.chatID == m.currentChat.ID {
			m.currentChat.QueuedInputs = cloneQueuedInputs(msg.items)
			m.clampQueueSelection()
			m.invalidateFooterCache()
		}
		return m, m.syncWindowTitleCmd()
	case llmPreviewMsg:
		if msg.err != nil {
			m.status = msg.err.Error()
			return m, m.syncWindowTitleCmd()
		}
		m.openLLMPreview(msg.title, msg.body)
		return m, m.syncWindowTitleCmd()
	case eventMsg:
		m.invalidateBodyCache()
		m.recordEvent(msg.chatID, msg.event)
		if msg.chatID == 0 || msg.chatID == m.currentChat.ID {
			m.applyEvent(msg.event)
		}
		if msg.events != nil {
			var refresh ui.Cmd
			if (msg.chatID == 0 || msg.chatID == m.currentChat.ID) && shouldRefreshDetailsAfterEvent(msg.event) {
				refresh = m.reloadDetailsCmd()
			}
			return m, ui.Batch(refresh, nextEventCmd(msg.chatID, msg.events), m.syncWindowTitleCmd())
		}
		if msg.chatID == 0 || msg.chatID == m.currentChat.ID {
			m.stopBusy()
			return m, ui.Batch(m.reloadDetailsCmd(), m.syncWindowTitleCmd())
		}
		if m.chatBusy != nil {
			delete(m.chatBusy, msg.chatID)
		}
		return m, m.syncWindowTitleCmd()
	case loadMsg:
		m.pendingAssistant = pendingAssistantTurn{}
		m.invalidateTranscript()
		m = m.UpdateLoad(msg)
		if m.debug != nil && m.currentSession.ID > 0 {
			m.debug.RecordLifecycle(m.currentSession.ID, "session_reloaded", fmt.Sprintf("%d messages", len(m.messages)), map[string]string{"messages": strconv.Itoa(len(m.messages))})
		}
		if !msg.preserveBusy {
			m.stopBusyWithStatus("Ready")
		}
		cmds := []ui.Cmd{m.syncWindowTitleCmd()}
		if cmd := m.dequeuePromptCmd(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		if cmd := m.detectSessionContextWindowCmd(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		if cmd := m.refreshExecSubscriptionCmd(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		return m, ui.Batch(cmds...)
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
		m.chats = msg.chats
		m.currentSession = msg.session
		m.currentChat = msg.chat
		m.clampQueueSelection()
		m.messages = msg.messages
		m.parts = msg.parts
		m.approvals = msg.approvals
		m.milestonePlan = msg.plan
		m.todos = msg.todos
		m.workspace = msg.workspace
		m.resetComposerInput()
		m.draftAttachments = nil
		m.draftReferences = nil
		m.closePicker()
		m.closeSessionDialog()
		m.closeConnectDialog()
		m.closeMCPDialog()
		m.closeDisconnectDialog()
		m.closeModelDialog()
		m.closeAgentsModal()
		m.agentsDrift = false
		m.syncCurrentChatBusy()
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
		return m, ui.Batch(m.refreshExecSubscriptionCmd(), m.syncWindowTitleCmd())
	case sessionPickerMsg:
		m.invalidateBodyCache()
		m.sessions = msg.sessions
		m.openSessionPicker()
		m.stopBusyWithStatus("Select a session to resume")
		return m, m.syncWindowTitleCmd()
	case mcpReloadMsg:
		if msg.err != nil {
			m.status = msg.err.Error()
			if m.hasMCPDialog() {
				m.mcpDialog.SetStatus("Reload failed: " + msg.err.Error())
			}
			return m, m.syncWindowTitleCmd()
		}
		if m.hasMCPDialog() {
			m.mcpDialog.SetServers(msg.servers)
			m.mcpDialog.SetStatus("MCP servers refreshed")
		}
		m.status = m.mcpStatusSummary()
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
	case contextWindowMsg:
		m.invalidateBodyCache()
		if msg.err != nil {
			m.status = msg.err.Error()
			return m, m.syncWindowTitleCmd()
		}
		if msg.checked {
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
				if err := m.cfg.Save(); err != nil {
					m.status = err.Error()
					return m, m.syncWindowTitleCmd()
				}
				if m.agent != nil {
					m.agent.UpdateConfig(m.cfg)
				}
			}
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

func (m Model) centeredModal(node ui.Node) ui.Node {
	if node == nil {
		return nil
	}
	return ui.AsNode(ui.Align{
		Horizontal: ui.AlignCenter,
		Vertical:   ui.AlignCenter,
		Child: ui.AsNode(ui.Constrained{
			Constraints: ui.Constraints{
				MaxW: max(1, m.width-3),
				MaxH: max(1, m.height-2),
			},
			Child: node,
		}),
	})
}

func (m Model) ViewLines() []string {
	return m.viewSurface().Lines()
}

func (m Model) ViewSurface() ui.SurfaceView {
	return m.viewSurface()
}

func (m *Model) renderElementText(node ui.Node, width, height int) string {
	return strings.Join(ui.RenderSurface(&ui.Context{Palette: m.palette}, node, width, height).Lines(), "\n")
}

func (m *Model) viewSurface() ui.Surface {
	if m.width <= 0 || m.height <= 0 {
		return ui.Surface{}
	}
	root := m.syncUIRoot()
	surface := root.RenderFrame()
	surface = m.bouncyBalls.Apply(surface)
	m.syncDebugFrame(surface)
	return surface
}

func (m *Model) handleKey(msg ui.KeyMsg) (ui.Model, ui.Cmd) {
	if msg.String() != "esc" {
		m.interruptArmedAt = time.Time{}
	}
	if m.hasHelpModal() {
		if handled, cmd := m.handleMainWindowKey(msg); handled {
			return m, cmd
		}
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
	if m.hasHelpModal() {
		switch msg.String() {
		case "alt+h", "enter", "esc":
			m.closeHelpModal()
			return true, m.syncWindowTitleCmd()
		default:
			if m.handleHelpKey(msg) {
				return true, nil
			}
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
	case "ctrl+b":
		m.toggleBouncyBalls()
		return true, nil
	case "ctrl+pgup":
		return true, m.switchChatByDelta(-1)
	case "ctrl+pgdown":
		return true, m.switchChatByDelta(1)
	case "alt+q":
		m.queueEditMode = !m.queueEditMode
		if m.queueEditMode && len(m.currentChat.QueuedInputs) > 0 {
			m.clampQueueSelection()
		}
		m.invalidateFooterCache()
		return true, m.syncWindowTitleCmd()
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
		if m.queueEditMode && len(m.currentChat.QueuedInputs) > 0 {
			return true, m.deleteSelectedQueuedInput()
		}
	case "esc":
		if m.queueEditMode {
			m.queueEditMode = false
			m.invalidateFooterCache()
			return true, m.syncWindowTitleCmd()
		}
		if m.loading {
			_, cmd := m.handleInterruptKey()
			return true, cmd
		}
	case "ctrl+s":
		m.showSidebar = !m.showSidebar
		m.resize()
		m.refreshViewport()
		return true, nil
	case "alt+[":
		if m.showSidebar {
			m.adjustSidebarWidth(-sidebarWidthStep)
		}
		return true, nil
	case "alt+]":
		if m.showSidebar {
			m.adjustSidebarWidth(sidebarWidthStep)
		}
		return true, nil
	case "alt+r":
		anchor := m.captureTranscriptViewportAnchor()
		m.showReasoning = !m.showReasoning
		if !m.replaceTranscriptItemsInPlace(func(item transcriptItemController) bool {
			switch typed := item.(type) {
			case *assistantMessageTranscriptItem:
				typed.SetReasoningVisible(m.showReasoning)
				return true
			case *pendingAssistantTranscriptItem:
				typed.SetReasoningVisible(m.showReasoning)
				return true
			default:
				return false
			}
		}) {
			m.invalidateTranscript()
		}
		m.refreshViewportAnchored(anchor)
		return true, nil
	case "alt+p":
		anchor := m.captureTranscriptViewportAnchor()
		m.showSystem = !m.showSystem
		if !m.replaceTranscriptItemsInPlace(func(item transcriptItemController) bool {
			if typed, ok := item.(*assistantMessageTranscriptItem); ok {
				typed.SetSystemVisible(m.showSystem)
				return true
			}
			return false
		}) {
			m.invalidateTranscript()
		}
		m.refreshViewportAnchored(anchor)
		return true, nil
	case "alt+o":
		prompt := m.submissionPromptText()
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
	case "up":
		if m.queueEditMode && len(m.currentChat.QueuedInputs) > 0 {
			m.moveQueueSelection(-1)
			return true, m.syncWindowTitleCmd()
		}
		if !m.recallComposerHistory(-1) {
			return true, nil
		}
		m.invalidateFooterCache()
		return true, nil
	case "down":
		if m.queueEditMode && len(m.currentChat.QueuedInputs) > 0 {
			m.moveQueueSelection(1)
			return true, m.syncWindowTitleCmd()
		}
		if !m.recallComposerHistory(1) {
			return true, nil
		}
		m.invalidateFooterCache()
		return true, nil
	case "alt+up":
		if m.queueEditMode && len(m.currentChat.QueuedInputs) > 0 {
			return true, m.reorderSelectedQueuedInput(-1)
		}
		_, cmd := m.popQueuedPromptForEditing()
		return true, cmd
	case "alt+down":
		if m.queueEditMode && len(m.currentChat.QueuedInputs) > 0 {
			return true, m.reorderSelectedQueuedInput(1)
		}
	case "tab":
		if m.loading && !m.hasSlashMenu() {
			prompt := m.submissionPromptText()
			if bang, ok := parseBangPrompt(prompt); ok {
				if bang.Double {
					return true, m.submitBangPrompt(bang, bangFollowupSteer)
				}
				return true, m.submitBangPrompt(bang, bangFollowupNone)
			}
			_, cmd := m.queueComposerPrompt(domain.QueuedInputKindSteer)
			return true, cmd
		}
	case "enter":
		if m.queueEditMode && len(m.currentChat.QueuedInputs) > 0 {
			_, cmd := m.popQueuedPromptForEditing()
			return true, cmd
		}
		prompt := m.submissionPromptText()
		if prompt == "" && len(m.draftAttachments) == 0 && len(m.draftReferences) == 0 {
			return false, nil
		}
		if bang, ok := parseBangPrompt(prompt); ok {
			if m.loading {
				if bang.Double {
					return true, m.submitBangPrompt(bang, bangFollowupQueue)
				}
				return true, m.submitBangPrompt(bang, bangFollowupNone)
			}
			return true, m.submitBangPrompt(bang, bangFollowupPrompt)
		}
		if m.loading {
			_, cmd := m.queueComposerPrompt(domain.QueuedInputKindQueued)
			return true, cmd
		}
		if handledModel, cmd, ok := m.handleLocalCommand(prompt); ok {
			_ = handledModel
			return true, cmd
		}
		if ok, status := m.canSendPrompt(); !ok {
			if m.shouldOpenConnectDialogForSendFailure() {
				m.openConnectDialog()
			}
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
	case "h":
		if m.queueEditMode && len(m.currentChat.QueuedInputs) > 0 {
			return true, m.toggleSelectedQueuedInputHeld()
		}
	case "delete":
		if m.queueEditMode && len(m.currentChat.QueuedInputs) > 0 {
			return true, m.deleteSelectedQueuedInput()
		}
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
		if beforeRevision == m.composer.Revision() {
			m.invalidateFooterCursor()
		} else {
			m.invalidateFooterCache()
		}
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
	paneWidth := m.transcriptPaneWidth()
	paneHeight := m.transcriptPaneHeight()
	if msg.Y < 0 || msg.Y >= paneHeight {
		return m, nil, false
	}
	if msg.X < 0 || msg.X >= paneWidth {
		return m, nil, false
	}
	m.refreshViewportAt(m.viewport.YOffset)
	contentX := msg.X
	contentY := msg.Y
	for i := len(m.transcriptControls) - 1; i >= 0; i-- {
		control := m.transcriptControls[i]
		if !control.Enabled || !control.Rect.Contains(ui.Point{X: max(0, contentX), Y: contentY}) {
			continue
		}
		if strings.HasPrefix(control.ID, "toolrun:") {
			target := strings.TrimPrefix(control.ID, "toolrun:")
			part := "output"
			runID := target
			if strings.HasSuffix(target, ":command") {
				part = "command"
				runID = strings.TrimSuffix(target, ":command")
			} else if strings.HasSuffix(target, ":output") {
				runID = strings.TrimSuffix(target, ":output")
			}
			if strings.TrimSpace(runID) == "" {
				return m, nil, true
			}
			switch part {
			case "command":
				if m.expandedToolRunCommands == nil {
					m.expandedToolRunCommands = make(map[string]bool)
				}
				m.expandedToolRunCommands[runID] = !m.expandedToolRunCommands[runID]
			default:
				if m.expandedToolRuns == nil {
					m.expandedToolRuns = make(map[string]bool)
				}
				m.expandedToolRuns[runID] = !m.expandedToolRuns[runID]
			}
			updated := false
			if idx, ok := m.toolRunItemIndexByID[runID]; ok && idx >= 0 && idx < len(m.transcriptItems) {
				if item, ok := m.transcriptItems[idx].(toolRunTranscriptItem); ok {
					switch part {
					case "command":
						item.ToggleCommand()
						m.expandedToolRunCommands[runID] = false
						switch concrete := item.(type) {
						case *bashToolRunTranscriptItem:
							m.expandedToolRunCommands[runID] = concrete.expandedCommand
						case *genericToolRunTranscriptItem:
							m.expandedToolRunCommands[runID] = concrete.expandedCommand
						}
					default:
						item.ToggleOutput()
						m.expandedToolRuns[runID] = false
						switch concrete := item.(type) {
						case *bashToolRunTranscriptItem:
							m.expandedToolRuns[runID] = concrete.expandedOutput
						case *readToolRunTranscriptItem:
							m.expandedToolRuns[runID] = concrete.expandedOutput
						case *writeToolRunTranscriptItem:
							m.expandedToolRuns[runID] = concrete.expandedOutput
						case *editToolRunTranscriptItem:
							m.expandedToolRuns[runID] = concrete.expandedOutput
						case *genericToolRunTranscriptItem:
							m.expandedToolRuns[runID] = concrete.expandedOutput
						}
					}
					updated = m.replaceTranscriptItemAt(idx)
				}
			}
			if !updated {
				m.invalidateTranscript()
			}
			m.refreshViewportPreserve()
			return m, nil, true
		}
	}
	return m, nil, false
}

func (m *Model) handleMainWindowMouse(msg ui.MouseMsg) (bool, ui.Cmd) {
	if _, cmd, ok := m.handleMouse(msg); ok {
		return true, cmd
	}
	if m.handleHelpMouse(msg) {
		return true, nil
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

func (m *Model) handleHelpMouse(msg ui.MouseMsg) bool {
	if !m.hasHelpModal() {
		return false
	}
	switch msg.Button {
	case ui.MouseButtonWheelUp:
		if msg.Action == ui.MouseActionPress {
			m.scrollHelp(-3)
			return true
		}
	case ui.MouseButtonWheelDown:
		if msg.Action == ui.MouseActionPress {
			m.scrollHelp(3)
			return true
		}
	}
	return false
}

func (m *Model) handleHelpKey(msg ui.KeyMsg) bool {
	if !m.hasHelpModal() {
		return false
	}
	switch msg.String() {
	case "up":
		m.scrollHelp(-1)
		return true
	case "down":
		m.scrollHelp(1)
		return true
	case "pgup":
		m.scrollHelp(-max(1, m.helpHeight))
		return true
	case "pgdown":
		m.scrollHelp(max(1, m.helpHeight))
		return true
	case "home":
		m.helpYOffset = 0
		return true
	case "end":
		m.helpYOffset = m.helpMaxOffset()
		return true
	default:
		return false
	}
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

func (m *Model) scrollHelp(delta int) {
	m.helpYOffset = min(max(0, m.helpYOffset+delta), m.helpMaxOffset())
}

func (m *Model) scrollTranscript(delta int) {
	target := m.viewport.YOffset + delta
	if target < 0 {
		target = 0
	}
	if target == m.viewport.YOffset {
		return
	}
	if len(m.messages) == 0 && m.viewport.TotalLineCount() > 0 {
		m.viewport.SetYOffset(target)
		m.invalidateMainSurface()
		if main := m.ensureMainScreenWidget(); main != nil {
			main.transcript.Invalidate()
		}
		return
	}
	m.refreshViewportAt(target)
}

func (m *Model) llmPreviewMaxOffset() int {
	contentHeight := ui.TextHeight(m.llmPreviewBody)
	return max(0, contentHeight-max(0, m.llmPreviewHeight))
}

func (m *Model) helpMaxOffset() int {
	contentHeight := ui.TextHeight(m.helpBody)
	return max(0, contentHeight-max(0, m.helpHeight))
}

func (m *Model) applyEvent(evt domain.Event) {
	switch evt.Kind {
	case domain.EventKindMessageDelta:
		if m.pendingAssistant.CreatedAt.IsZero() {
			m.pendingAssistant.CreatedAt = time.Now().UTC()
		}
		m.pendingAssistant.Text += evt.Text
		m.startBusy(busyScopeTranscript, "Working ...")
		m.refreshTranscriptForPendingTurn()
	case domain.EventKindReasoning:
		if m.pendingAssistant.CreatedAt.IsZero() {
			m.pendingAssistant.CreatedAt = time.Now().UTC()
		}
		m.pendingAssistant.Reasoning += evt.Text
		m.startBusy(busyScopeTranscript, "Thinking ...")
		m.refreshTranscriptForPendingTurn()
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
		m.clearPendingAssistantTurn()
		m.stopBusy()
	case domain.EventKindMessageDone:
		m.clearPendingAssistantTurn()
		m.stopBusyWithStatus("Ready")
	}
}

func shouldRefreshDetailsAfterEvent(evt domain.Event) bool {
	switch evt.Kind {
	case domain.EventKindMessageDelta,
		domain.EventKindReasoning,
		domain.EventKindUsage,
		domain.EventKindStatus,
		domain.EventKindSessionTitle:
		return false
	default:
		return true
	}
}

func (m *Model) refreshTranscriptForPendingTurn() {
	if !m.syncPendingTranscriptItem() {
		m.invalidateTranscript()
	}
	if m.viewport.AtBottom() {
		m.refreshViewport()
		return
	}
	m.refreshViewportPreserve()
}

func (m *Model) clearPendingAssistantTurn() {
	if strings.TrimSpace(m.pendingAssistant.Text) == "" && strings.TrimSpace(m.pendingAssistant.Reasoning) == "" {
		return
	}
	m.pendingAssistant = pendingAssistantTurn{}
	m.refreshTranscriptForPendingTurn()
}

func (m *Model) resize() {
	sidebarWidth := m.sidebarWidth()
	bodyWidth := m.width - sidebarWidth
	if bodyWidth < 20 {
		bodyWidth = 20
	}
	m.composer.SetWidth(m.composerWidth())
	bodyHeight := m.height - m.statusPaneHeight() - (mainScreenVerticalInset * 2)
	if bodyHeight < 5 {
		bodyHeight = 5
	}
	m.viewport.Width = max(0, bodyWidth)
	m.viewport.Height = bodyHeight
	m.viewport.SetWindowHeight(m.transcriptViewportHeight())
	m.resizeHelpModal()
	m.resizeLLMPreview()
	m.bouncyBalls.Resize(max(0, m.width), max(0, m.height))
	m.invalidateTranscript()
}

func (m *Model) renderHeader() string {
	return ""
}

func (m *Model) renderBodySurface() ui.Surface {
	ctx := &ui.Context{Palette: m.palette}
	width := max(0, m.width)
	height := max(0, m.height)
	if width <= 0 || height <= 0 {
		if width <= 0 {
			width = max(0, m.transcriptPaneWidth())
			if m.showSidebar {
				width += m.sidebarWidth() + 1
			}
			if width == 0 {
				width = max(40, m.composerWidth()+m.sidebarWidth()+3)
			}
		}
		if height <= 0 {
			height = max(0, m.transcriptPaneHeight()+m.composerAreaHeight()+m.statusPaneHeight()+(mainScreenVerticalInset*2))
			if height == 0 {
				height = max(6, m.transcriptPaneHeight()+m.composerAreaHeight()+m.statusPaneHeight()+(mainScreenVerticalInset*2))
			}
		}
	}
	return m.ensureMainScreenWidget().Surface(ctx, ui.Rect{W: width, H: height})
}

func (m *Model) renderBodyElement() ui.Node {
	return m.renderMainScreenElement()
}

func (m *Model) transcriptViewportHeight() int {
	return max(0, m.transcriptPaneHeight())
}

func (m *Model) transcriptPaneWidth() int {
	if m.width <= 0 {
		return max(0, m.viewport.Width)
	}
	sidebarWidth := m.sidebarWidth()
	bodyWidth := m.width - sidebarWidth
	if m.showSidebar && sidebarWidth > 0 {
		bodyWidth--
	}
	if bodyWidth < 20 {
		bodyWidth = 20
	}
	return bodyWidth
}

func (m *Model) transcriptPaneHeight() int {
	if m.height <= 0 {
		return max(0, m.viewport.Height)
	}
	height := max(0, m.viewport.Height)
	if composerHeight := m.composerAreaHeight(); composerHeight > 0 {
		height -= composerHeight
	}
	return max(0, height)
}

func (m *Model) renderTranscriptPaneElement(transcript ui.Node) ui.Node {
	return ui.AsNode(ui.Border{
		Child:      transcript,
		Background: m.palette.ScreenBackground,
	})
}

func (m *Model) renderComposerAreaSurface() ui.Surface {
	ctx := &ui.Context{Palette: m.palette}
	width := max(0, m.width)
	return m.ensureMainScreenWidget().composer.Surface(ctx, ui.Rect{W: width})
}

func (m *Model) renderComposerAreaElement() ui.Node {
	if !m.shouldShowComposerArea() {
		return ui.AsNode(ui.VisibleElement{})
	}
	elements := []ui.Node{}
	if menu := m.renderComposerHistoryMenuElement(); menu != nil {
		elements = append(elements, menu)
	} else if menu := m.renderSlashMenuElement(); menu != nil {
		elements = append(elements, menu)
	} else if menu := m.renderMentionMenuElement(); menu != nil {
		elements = append(elements, menu)
	} else if menu := m.renderSkillMenuElement(); menu != nil {
		elements = append(elements, menu)
	}
	if preview := m.renderQueuedPromptPreviewElement(); preview != nil {
		elements = append(elements, preview)
	}
	elements = append(elements, m.renderComposerElement())
	children := make([]ui.Child, 0, len(elements))
	for _, element := range elements {
		if element == nil {
			continue
		}
		children = append(children, ui.Fixed(element))
	}
	return ui.AsNode(ui.FlexBox{Direction: ui.DirectionVertical, Children: children})
}

func (m *Model) shouldShowComposerArea() bool {
	if m.composerAreaHasContent() {
		return true
	}
	return m.composer.Focused()
}

func (m *Model) composerAreaHasContent() bool {
	if len(m.draftAttachments) > 0 || len(m.currentChat.QueuedInputs) > 0 {
		return true
	}
	if m.renderComposerHistoryMenuElement() != nil ||
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

func (m *Model) renderStatusPaneElement() ui.Node {
	if !m.busy.transcriptActive() {
		return ui.AsNode(ui.VisibleElement{})
	}
	return ui.AsNode(ui.ActivityIndicator{
		Indicator: ui.WorkingIndicatorLine(m.workingIndicator(), m.busy.statusOrDefault("Working ...")),
		Palette:   m.palette,
	})
}

func (m *Model) statusPaneHeight() int {
	element := m.renderStatusPaneElement()
	if !ui.NodeVisible(element) {
		return 0
	}
	return element.Measure(&ui.Context{Palette: m.palette}, ui.NewConstraints(max(0, m.width), 0)).H
}

func (m *Model) renderMainScreenElement() ui.Node {
	var transcript ui.Node = ui.AsNode(ui.SurfaceBox{Surface: m.viewport.VisibleSurface()})
	if retained := m.transcriptElement(nil); retained != nil {
		transcript = retained
	}
	mainChildren := []ui.Child{
		ui.Flex(m.renderTranscriptPaneElement(transcript), 1),
		ui.Fixed(m.renderComposerAreaElement()),
	}
	mainColumn := ui.AsNode(ui.FlexBox{Direction: ui.DirectionVertical, Children: mainChildren})
	sidebar := ui.AsNode(ui.VisibleElement{
		BoxProps: ui.BoxProps{
			Hidden: !m.showSidebar,
		},
		Child: ui.AsNode(ui.Sidebar{
			Child:  ui.AsNode(ui.TextPane{Content: m.renderSidebar()}),
			Height: m.viewport.Height,
			Width:  m.sidebarWidth(),
		}),
	})
	rootChildren := []ui.Child{
		ui.Flex(ui.AsNode(ui.FlexBox{
			Direction: ui.DirectionHorizontal,
			Children: []ui.Child{
				ui.Flex(ui.AsNode(ui.Inset{Padding: ui.SymmetricInsets(mainScreenVerticalInset, 0), Child: mainColumn}), 1),
				ui.Fixed(ui.Spacer{W: 1}),
				ui.Fixed(sidebar),
			},
		}), 1),
		ui.Fixed(m.renderStatusPaneElement()),
	}
	return ui.AsNode(ui.FlexBox{Direction: ui.DirectionVertical, Children: rootChildren})
}

func (m *Model) renderComposerElement() ui.Node {
	m.composer.Prompt = m.promptGlyph() + " "
	line := m.composer.VisibleLine()
	tokenRanges := make([]ui.TokenRange, 0, len(line.Tokens()))
	for _, token := range line.Tokens() {
		tokenRanges = append(tokenRanges, ui.TokenRange{Start: token.Start, End: token.End})
	}
	return ui.AsNode(ui.NewComposer(ui.ComposerProps{
		Palette:       m.palette,
		Width:         m.composerWidth(),
		TokenRanges:   tokenRanges,
		HalfBlocks:    m.halfBlocksEnabled(),
		PromptGlyph:   m.promptGlyph(),
		Value:         m.composer.Value(),
		Placeholder:   m.composer.Placeholder,
		ContentBefore: line.Before(),
		ContentCursor: line.Cursor(),
		ContentAfter:  line.After(),
		CursorVisible: m.composer.CursorVisible(),
	}))
}

func (m *Model) renderQueuedPromptPreviewElement() ui.Node {
	if len(m.currentChat.QueuedInputs) == 0 {
		return nil
	}
	rows := make([]ui.PendingInputRow, 0, len(m.currentChat.QueuedInputs))
	for idx, item := range m.currentChat.QueuedInputs {
		text := strings.TrimSpace(item.Text)
		if item.Kind == domain.QueuedInputKindContinue {
			text = "Continue"
		}
		rows = append(rows, ui.PendingInputRow{
			Badge:    queuedInputBadge(item),
			Text:     text,
			Held:     item.Held,
			Selected: m.queueEditMode && idx == m.selectedQueuedInputIndex(),
		})
	}
	return ui.AsNode(ui.PendingInputPreview{
		Width:       m.composerWidth(),
		Items:       rows,
		EditingMode: m.queueEditMode,
	})
}

func (m *Model) composerWidth() int {
	width := m.transcriptPaneWidth()
	if width <= 0 {
		return 40
	}
	return width
}

func (m *Model) sidebarWidth() int {
	if !m.showSidebar {
		return 0
	}
	minWidth := sidebarMinWidth
	maxWidth := m.sidebarMaxWidth()
	if m.sidebarWidthOverride > 0 {
		return clampInt(m.sidebarWidthOverride, minWidth, maxWidth)
	}
	defaultWidth := min(32, max(20, m.width/4))
	return clampInt(defaultWidth, minWidth, maxWidth)
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

func mPrompt(cfg config.Config) string {
	if cfg.UI.HalfBlocks {
		return "▌ "
	}
	return "┃ "
}

func (m *Model) renderSidebar() string {
	var lines []string
	lines = append(lines, m.renderSidebarSessionLine())
	lines = append(lines, m.renderSidebarChatLine())
	provider := firstNonEmptyString(strings.TrimSpace(m.currentChat.ProviderID), strings.TrimSpace(m.currentSession.ProviderID))
	if provider == "" {
		provider = "(unset)"
	}
	model := firstNonEmptyString(strings.TrimSpace(m.currentChat.ModelID), strings.TrimSpace(m.currentSession.ModelID))
	if model == "" {
		model = "(unset)"
	}
	lines = append(lines, fmt.Sprintf("Model  %s / %s", provider, model))
	status := strings.TrimSpace(m.status)
	if status == "" {
		status = "Ready"
	}
	if m.busy.sidebarActive() {
		lines = append(lines, fmt.Sprintf("Status %s %s", m.workingIndicator(), status))
	} else {
		lines = append(lines, fmt.Sprintf("Status %s", status))
	}
	if metrics, ok := sessionctx.FromMessages(m.cfg, m.currentSession, m.messages, m.parts); ok {
		lines = append(lines, fmt.Sprintf("Context %s / %s (%d%%)", formatTokens(metrics.Used), formatTokens(metrics.Max), metrics.UsagePercent))
	}
	if usage, ok := sessionctx.TotalUsage(m.messages, m.parts); ok {
		tokenLine := fmt.Sprintf("Tokens in %s  out %s", formatTokens(usage.PromptTokens), formatTokens(usage.CompletionTokens))
		if usage.CachedTokens > 0 {
			tokenLine += "  cache " + formatTokens(usage.CachedTokens)
		}
		lines = append(lines, tokenLine)
	}
	lines = append(lines, "Workspace "+blankAsDash(m.workdir))
	if projectRoot := strings.TrimSpace(m.currentProjectRoot()); projectRoot != "" && projectRoot != strings.TrimSpace(m.workdir) {
		lines = append(lines, "Project  "+projectRoot)
	}
	lines = append(lines, "AGENTS   "+m.renderAgentsSidebarStatus())
	if !m.workspace.Available {
		lines = append(lines, "Git      no repository")
	} else {
		branch := m.workspace.Branch
		if branch == "" {
			branch = "(detached)"
		}
		gitLine := fmt.Sprintf("Git      %s  %s", branch, m.workspace.SummaryLine())
		if strings.TrimSpace(m.workspace.Summary) != "" {
			gitLine += "  " + strings.TrimSpace(m.workspace.Summary)
		}
		lines = append(lines, gitLine)
	}
	if milestoneSummary := m.sidebarMilestoneSummary(); milestoneSummary != "" {
		lines = append(lines, milestoneSummary)
	}
	if todoSummary := m.sidebarTodoSummary(); todoSummary != "" {
		lines = append(lines, todoSummary)
	}
	if len(m.chats) > 0 {
		lines = append(lines, "")
		lines = append(lines, "Chats")
		for _, item := range m.chats {
			lines = append(lines, m.renderSidebarChatListItem(item))
		}
	}
	if debugAddr := m.debugAPIAddr(); debugAddr != "" {
		lines = append(lines, "Debug   "+debugAddr)
	}
	lines = append(lines, "")
	lines = append(lines, "Help    Alt-H help  Ctrl-S toggle  Alt-[ narrow  Alt-] wide")
	return strings.Join(lines, "\n")
}

func (m Model) debugAPIAddr() string {
	if m.debug == nil {
		return ""
	}
	return strings.TrimSpace(m.debug.Runtime().DebugAPI)
}

const (
	sidebarMinWidth  = 24
	sidebarMaxWidth  = 72
	sidebarWidthStep = 4
)

func (m *Model) sidebarMaxWidth() int {
	maxWidth := min(sidebarMaxWidth, max(sidebarMinWidth, m.width-40))
	return max(sidebarMinWidth, maxWidth)
}

func (m *Model) adjustSidebarWidth(delta int) {
	if delta == 0 {
		return
	}
	current := m.sidebarWidth()
	next := clampInt(current+delta, sidebarMinWidth, m.sidebarMaxWidth())
	m.sidebarWidthOverride = next
	m.resize()
	m.refreshViewport()
}

func (m *Model) renderSidebarSessionLine() string {
	id := fmt.Sprintf("#%d", m.currentSession.ID)
	title := strings.TrimSpace(m.currentSession.Title)
	if title == "" {
		return "Session " + id
	}
	return "Session " + id + "  " + title
}

func (m *Model) renderSidebarChatLine() string {
	id := fmt.Sprintf("#%d", m.currentChat.ID)
	chatIndex := 0
	for idx, item := range m.chats {
		if item.ID == m.currentChat.ID {
			chatIndex = idx + 1
			break
		}
	}
	label := "Chat    " + id
	if chatIndex > 0 {
		label += fmt.Sprintf("  %d/%d", chatIndex, len(m.chats))
	}
	role := strings.TrimSpace(string(m.currentChat.WorkflowRole))
	if role == "" {
		role = string(domain.WorkflowRoleGeneral)
	}
	label += "  " + role
	label += fmt.Sprintf("  %d msg", len(m.messages))
	if queued := len(m.currentChat.QueuedInputs); queued > 0 {
		label += fmt.Sprintf("  %d queued", queued)
	}
	if title := strings.TrimSpace(m.currentChat.Title); title != "" {
		label += "  " + title
	}
	return label
}

func (m *Model) renderSidebarChatListItem(item domain.Chat) string {
	prefix := " "
	if item.ID == m.currentChat.ID {
		prefix = "*"
	}
	label := strings.TrimSpace(item.Title)
	if label == "" {
		label = fmt.Sprintf("Chat %d", item.ID)
	}
	role := strings.TrimSpace(string(item.WorkflowRole))
	if role != "" && role != string(domain.WorkflowRoleGeneral) {
		label += " [" + role + "]"
	}
	if queued := len(item.QueuedInputs); queued > 0 {
		label += fmt.Sprintf(" (%d queued)", queued)
	}
	return fmt.Sprintf(" %s #%d %s", prefix, item.ID, label)
}

func (m *Model) sidebarMilestoneSummary() string {
	if len(m.milestonePlan.Milestones) == 0 {
		return ""
	}
	active := 0
	completed := 0
	for _, item := range m.milestonePlan.Milestones {
		switch item.Status {
		case domain.MilestoneStatusCompleted:
			completed++
		case domain.MilestoneStatusInProgress, domain.MilestoneStatusExecuting, domain.MilestoneStatusDecomposing, domain.MilestoneStatusBlocked:
			active++
		}
	}
	return fmt.Sprintf("Milestones %d total  %d active  %d done", len(m.milestonePlan.Milestones), active, completed)
}

func (m *Model) sidebarTodoSummary() string {
	if len(m.todos) == 0 {
		return ""
	}
	inProgress := 0
	completed := 0
	for _, item := range m.todos {
		switch item.Status {
		case domain.TodoStatusCompleted:
			completed++
		case domain.TodoStatusInProgress:
			inProgress++
		}
	}
	return fmt.Sprintf("Todos   %d total  %d active  %d done", len(m.todos), inProgress, completed)
}

func clampInt(value, low, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

func (m *Model) refreshViewport() {
	m.invalidateMainSurface()
	m.refreshViewportAt(-1)
}

func (m *Model) refreshViewportPreserve() {
	m.invalidateMainSurface()
	m.refreshViewportAt(m.viewport.YOffset)
}

type transcriptViewportAnchor struct {
	index        int
	key          string
	offsetWithin int
	lines        []string
	line         string
	lineOffset   int
	fallback     int
	valid        bool
}

func (m *Model) captureTranscriptViewportAnchor() transcriptViewportAnchor {
	retained := m.syncRetainedTranscript()
	if retained == nil {
		return transcriptViewportAnchor{}
	}
	items := retained.Items()
	if len(items) == 0 {
		return transcriptViewportAnchor{fallback: m.viewport.YOffset}
	}
	width := max(0, m.viewport.Width)
	offset := max(0, m.viewport.YOffset)
	visibleLines := m.viewport.VisibleSurface().Lines()
	if len(visibleLines) > 3 {
		visibleLines = slices.Clone(visibleLines[:3])
	} else {
		visibleLines = slices.Clone(visibleLines)
	}
	anchorLine, anchorLineOffset := transcriptAnchorPreferredLine(visibleLines)
	ctx := &ui.Context{Palette: m.palette}
	y := 0
	for idx, item := range items {
		regionStart := y
		regionHeight := max(0, item.GapBefore)
		if item.Node != nil {
			size := item.Node.Measure(ctx, ui.NewConstraints(width, 0))
			regionHeight += max(0, size.H)
		}
		if regionHeight > 0 && offset < regionStart+regionHeight {
			return transcriptViewportAnchor{
				index:        idx,
				key:          item.Key,
				offsetWithin: offset - regionStart,
				lines:        visibleLines,
				line:         anchorLine,
				lineOffset:   anchorLineOffset,
				fallback:     offset,
				valid:        strings.TrimSpace(item.Key) != "",
			}
		}
		y += regionHeight
	}
	return transcriptViewportAnchor{fallback: offset}
}

func (m *Model) resolveTranscriptViewportAnchor(anchor transcriptViewportAnchor) int {
	if !anchor.valid {
		return anchor.fallback
	}
	retained := m.syncRetainedTranscript()
	if retained == nil {
		return anchor.fallback
	}
	items := retained.Items()
	if len(items) == 0 {
		return anchor.fallback
	}
	width := max(0, m.viewport.Width)
	ctx := &ui.Context{Palette: m.palette}
	if lineIdx := m.resolveTranscriptAnchorFromFullSurface(retained, ctx, width, anchor); lineIdx >= 0 {
		return lineIdx
	}
	y := 0
	for idx, item := range items {
		regionStart := y
		regionHeight := max(0, item.GapBefore)
		regionLines := make([]string, 0, regionHeight)
		for range max(0, item.GapBefore) {
			regionLines = append(regionLines, "")
		}
		if item.Node != nil {
			size := item.Node.Measure(ctx, ui.NewConstraints(width, 0))
			regionHeight += max(0, size.H)
			surface := ui.PaintNodeSurface(ctx, item.Node, ui.Rect{W: width, H: size.H})
			regionLines = append(regionLines, surface.Lines()...)
		}
		if idx == anchor.index || item.Key == anchor.key {
			if regionHeight <= 0 {
				return regionStart
			}
			if lineIdx := transcriptAnchorSingleLineIndex(regionLines, anchor.line); lineIdx >= 0 {
				return regionStart + max(0, lineIdx-anchor.lineOffset)
			}
			if idx := transcriptAnchorLineIndex(regionLines, anchor.lines); idx >= 0 {
				return regionStart + idx
			}
			return regionStart + min(anchor.offsetWithin, regionHeight-1)
		}
		y += regionHeight
	}
	return anchor.fallback
}

func (m *Model) resolveTranscriptAnchorFromFullSurface(retained *ui.RetainedTranscript, ctx *ui.Context, width int, anchor transcriptViewportAnchor) int {
	if retained == nil || strings.TrimSpace(anchor.line) == "" {
		return -1
	}
	size := retained.Measure(ctx, ui.NewConstraints(width, 0))
	surface := ui.PaintNodeSurface(ctx, ui.AsNode(retained), ui.Rect{W: width, H: size.H})
	lines := surface.Lines()
	lineIdx := transcriptAnchorSingleLineIndex(lines, anchor.line)
	if lineIdx < 0 {
		return -1
	}
	return max(0, lineIdx-anchor.lineOffset)
}

func transcriptAnchorLineIndex(haystack, needle []string) int {
	if len(needle) == 0 || len(haystack) == 0 {
		return -1
	}
	maxStart := len(haystack) - len(needle)
	for start := 0; start <= maxStart; start++ {
		match := true
		for idx := range needle {
			if strings.TrimRight(haystack[start+idx], " ") != strings.TrimRight(needle[idx], " ") {
				match = false
				break
			}
		}
		if match {
			return start
		}
	}
	return -1
}

func transcriptAnchorPreferredLine(lines []string) (string, int) {
	for idx, line := range lines {
		trimmed := strings.TrimRight(line, " ")
		if trimmed != "" {
			return trimmed, idx
		}
	}
	return "", 0
}

func transcriptAnchorSingleLineIndex(lines []string, needle string) int {
	needle = strings.TrimRight(needle, " ")
	if needle == "" {
		return -1
	}
	for idx, line := range lines {
		candidate := strings.TrimRight(line, " ")
		if candidate == "" {
			continue
		}
		if candidate == needle || strings.Contains(candidate, needle) || strings.Contains(needle, candidate) {
			return idx
		}
	}
	return -1
}

func (m *Model) refreshViewportAnchored(anchor transcriptViewportAnchor) {
	m.invalidateMainSurface()
	m.refreshViewportAt(m.resolveTranscriptViewportAnchor(anchor))
}

func (m *Model) refreshViewportAt(offset int) {
	m.invalidateMainSurface()
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
	m.viewport.SetWindowHeight(viewportHeight)
	if offset >= 0 {
		scroll := ui.ScrollBox{Child: ui.AsNode(retained), OffsetY: offset, Width: max(0, m.viewport.Width), Height: viewportHeight}
		surface, totalHeight, appliedY = scroll.RenderVisible(ctx, max(0, m.viewport.Width), viewportHeight, offset)
	} else {
		scroll := ui.ScrollBox{Child: ui.AsNode(retained), Width: max(0, m.viewport.Width), Height: viewportHeight}
		surface, totalHeight, appliedY = scroll.RenderBottom(ctx, max(0, m.viewport.Width), viewportHeight)
	}
	m.viewport.SetContentHeight(totalHeight)
	m.viewport.SetYOffset(appliedY)
	m.transcriptControls = runtime.Controls()
	m.viewport.SetVisibleSurface(surface)
	if main := m.ensureMainScreenWidget(); main != nil {
		main.transcript.Invalidate()
	}
}

func (m *Model) invalidateBodyCache() {
	cache := m.ensureRenderCache()
	cache.bodyValid = false
	cache.renderedBodySurface = ui.Surface{}
	if main := m.ensureMainScreenWidget(); main != nil {
		main.transcript.Invalidate()
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
	m.composerCursorDirty = false
	if main := m.ensureMainScreenWidget(); main != nil {
		main.composer.Invalidate()
	}
}

func (m *Model) invalidateFooterCursor() {
	cache := m.ensureRenderCache()
	cache.composerAreaValid = false
	cache.renderedComposerAreaSurface = ui.Surface{}
	m.composerCursorDirty = true
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

func (m *Model) transcriptPlaceholderItems() []transcriptItemController {
	if m.currentSession.ID == 0 && len(m.messages) == 0 && !m.cfg.HasUsableDefaultProvider() {
		item := newPlaceholderTranscriptItem("no-provider", 0, "No provider configured.\n\nType /connect to add one before sending prompts.")
		item.Refresh(m)
		return []transcriptItemController{item}
	}
	if m.currentSession.ID == 0 && len(m.messages) == 0 {
		item := newPlaceholderTranscriptItem("empty", 0, "Start by asking a question or type / for commands.")
		item.Refresh(m)
		return []transcriptItemController{item}
	}
	return nil
}

func (m *Model) rebuildTranscriptState() []ui.TranscriptItem {
	if items := m.transcriptPlaceholderItems(); len(items) > 0 {
		m.transcriptItems = items
		m.messageItemIndexByID = make(map[int64]int)
		m.toolRunItemIndexByID = make(map[string]int)
		m.toolRunItemIndexByAppr = make(map[int64]int)
		m.pendingTranscriptIndex = -1
		return m.uiTranscriptItems()
	}
	prevByKey := make(map[string]transcriptItemController, len(m.transcriptItems))
	for _, item := range m.transcriptItems {
		prevByKey[item.Key()] = item
	}
	blocks := m.transcriptBlocks()
	controllers := make([]transcriptItemController, 0, len(blocks))
	messageIdx := make(map[int64]int)
	toolIdx := make(map[string]int)
	toolAppr := make(map[int64]int)
	pendingIndex := -1
	for idx, block := range blocks {
		gap := 0
		if idx > 0 {
			gap = renderedSeparatorHeight(m.transcriptSeparator(blocks[idx-1], block))
		}
		controller := m.transcriptControllerFromBlock(prevByKey, block, gap)
		controller.Refresh(m)
		controllers = append(controllers, controller)
		switch typed := controller.(type) {
		case *pendingAssistantTranscriptItem:
			pendingIndex = idx
		case *userMessageTranscriptItem:
			messageIdx[typed.message.ID] = idx
		case *assistantMessageTranscriptItem:
			messageIdx[typed.message.ID] = idx
		case toolRunTranscriptItem:
			if id := strings.TrimSpace(typed.RunID()); id != "" {
				toolIdx[id] = idx
			}
			switch concrete := typed.(type) {
			case *bashToolRunTranscriptItem:
				if concrete.run.ApprovalID > 0 {
					toolAppr[concrete.run.ApprovalID] = idx
				}
			case *readToolRunTranscriptItem:
				if concrete.run.ApprovalID > 0 {
					toolAppr[concrete.run.ApprovalID] = idx
				}
			case *writeToolRunTranscriptItem:
				if concrete.run.ApprovalID > 0 {
					toolAppr[concrete.run.ApprovalID] = idx
				}
			case *editToolRunTranscriptItem:
				if concrete.run.ApprovalID > 0 {
					toolAppr[concrete.run.ApprovalID] = idx
				}
			case *genericToolRunTranscriptItem:
				if concrete.run.ApprovalID > 0 {
					toolAppr[concrete.run.ApprovalID] = idx
				}
			}
		}
	}
	m.transcriptItems = controllers
	m.messageItemIndexByID = messageIdx
	m.toolRunItemIndexByID = toolIdx
	m.toolRunItemIndexByAppr = toolAppr
	m.pendingTranscriptIndex = pendingIndex
	return m.uiTranscriptItems()
}

func (m *Model) transcriptControllerFromBlock(prevByKey map[string]transcriptItemController, block transcriptBlock, gap int) transcriptItemController {
	key := m.transcriptBlockIdentityKey(block)
	if existing := prevByKey[key]; existing != nil {
		existing.SetGapBefore(gap)
		switch typed := existing.(type) {
		case *userMessageTranscriptItem:
			if block.Kind == transcriptBlockMessage {
				typed.Update(block.Message, block.Parts)
				return typed
			}
		case *assistantMessageTranscriptItem:
			if block.Kind == transcriptBlockMessage && !block.Pending {
				typed.Update(block.Message, block.Parts)
				typed.SetReasoningVisible(m.showReasoning)
				typed.SetSystemVisible(m.showSystem)
				return typed
			}
		case *pendingAssistantTranscriptItem:
			if block.Kind == transcriptBlockMessage && block.Pending {
				typed.Reset(block.Message.CreatedAt, firstPartBody(block.Parts, domain.PartKindText), firstPartBody(block.Parts, domain.PartKindReasoning), m.pendingAssistantIndicatorLine())
				typed.SetReasoningVisible(m.showReasoning)
				return typed
			}
		case toolRunTranscriptItem:
			if block.Kind == transcriptBlockToolRun {
				typed.UpdateRun(block.ToolRun)
				return typed
			}
		case *placeholderTranscriptItem:
			return typed
		}
	}
	switch {
	case block.Kind == transcriptBlockToolRun:
		return newToolRunTranscriptItem(gap, block.ToolRun, m.expandedToolRuns[block.ToolRun.ID], m.expandedToolRunCommands[block.ToolRun.ID])
	case block.Pending:
		item := newPendingAssistantTranscriptItem(gap, block.Message.CreatedAt, m.showReasoning)
		item.Reset(block.Message.CreatedAt, firstPartBody(block.Parts, domain.PartKindText), firstPartBody(block.Parts, domain.PartKindReasoning), m.pendingAssistantIndicatorLine())
		return item
	case block.Message.Role == domain.MessageRoleUser:
		return newUserMessageTranscriptItem(key, gap, block.Message, block.Parts)
	default:
		return newAssistantMessageTranscriptItem(key, gap, block.Message, block.Parts, m.showReasoning, m.showSystem)
	}
}

func firstPartBody(parts []domain.Part, kind domain.PartKind) string {
	for _, part := range parts {
		if part.Kind == kind {
			return part.Body
		}
	}
	return ""
}

func (m *Model) uiTranscriptItems() []ui.TranscriptItem {
	items := make([]ui.TranscriptItem, 0, len(m.transcriptItems))
	for _, item := range m.transcriptItems {
		items = append(items, item.UIItem())
	}
	return items
}

func (m *Model) replaceTranscriptItemAt(index int) bool {
	if m.transcriptDirty || index < 0 || index >= len(m.transcriptItems) {
		return false
	}
	retained := m.ensureRetainedTranscript()
	if len(retained.Items()) == 0 {
		return false
	}
	item := m.transcriptItems[index]
	item.Refresh(m)
	retained.Replace(index, item.UIItem())
	return true
}

func (m *Model) replaceTranscriptItemsInPlace(match func(transcriptItemController) bool) bool {
	if m.transcriptDirty {
		return false
	}
	retained := m.ensureRetainedTranscript()
	if len(retained.Items()) == 0 {
		return false
	}
	updated := false
	for idx, item := range m.transcriptItems {
		if match != nil && !match(item) {
			continue
		}
		item.Refresh(m)
		retained.Replace(idx, item.UIItem())
		updated = true
	}
	return updated
}

func (m *Model) syncPendingTranscriptItem() bool {
	if m.transcriptDirty {
		return false
	}
	retained := m.ensureRetainedTranscript()
	if len(retained.Items()) == 0 {
		return false
	}
	if strings.TrimSpace(m.pendingAssistant.Text) == "" && strings.TrimSpace(m.pendingAssistant.Reasoning) == "" {
		if m.pendingTranscriptIndex < 0 || m.pendingTranscriptIndex >= len(m.transcriptItems) {
			return false
		}
		retained.Remove(m.pendingTranscriptIndex)
		m.transcriptItems = append(m.transcriptItems[:m.pendingTranscriptIndex], m.transcriptItems[m.pendingTranscriptIndex+1:]...)
		m.reindexTranscriptControllers()
		m.pendingTranscriptIndex = -1
		return true
	}
	if m.pendingAssistant.CreatedAt.IsZero() {
		m.pendingAssistant.CreatedAt = time.Now().UTC()
	}
	if m.pendingTranscriptIndex >= 0 && m.pendingTranscriptIndex < len(m.transcriptItems) {
		if item, ok := m.transcriptItems[m.pendingTranscriptIndex].(*pendingAssistantTranscriptItem); ok {
			item.Reset(m.pendingAssistant.CreatedAt, m.pendingAssistant.Text, m.pendingAssistant.Reasoning, m.pendingAssistantIndicatorLine())
			item.SetReasoningVisible(m.showReasoning)
			return m.replaceTranscriptItemAt(m.pendingTranscriptIndex)
		}
	}
	gap := 0
	if len(m.transcriptItems) > 0 {
		prevBlock := m.transcriptBlockForController(m.transcriptItems[len(m.transcriptItems)-1])
		nextBlock := transcriptBlock{Kind: transcriptBlockMessage, Pending: true, Message: domain.Message{Role: domain.MessageRoleAssistant, CreatedAt: m.pendingAssistant.CreatedAt}}
		gap = renderedSeparatorHeight(m.transcriptSeparator(prevBlock, nextBlock))
	}
	item := newPendingAssistantTranscriptItem(gap, m.pendingAssistant.CreatedAt, m.showReasoning)
	item.Reset(m.pendingAssistant.CreatedAt, m.pendingAssistant.Text, m.pendingAssistant.Reasoning, m.pendingAssistantIndicatorLine())
	item.Refresh(m)
	m.transcriptItems = append(m.transcriptItems, item)
	retained.Add(item.UIItem())
	m.reindexTranscriptControllers()
	return true
}

func (m Model) pendingAssistantIndicatorLine() string {
	if strings.TrimSpace(m.pendingAssistant.Text) != "" {
		return ""
	}
	if strings.TrimSpace(m.pendingAssistant.Reasoning) == "" {
		return ""
	}
	indicator := m.workingIndicator()
	if strings.TrimSpace(indicator) == "" {
		indicator = ui.SpinnerFrame(m.cfg.UI.Spinner, 0)
	}
	return ui.WorkingIndicatorLine(indicator, "Thinking ...")
}

func (m *Model) reindexTranscriptControllers() {
	m.messageItemIndexByID = make(map[int64]int)
	m.toolRunItemIndexByID = make(map[string]int)
	m.toolRunItemIndexByAppr = make(map[int64]int)
	m.pendingTranscriptIndex = -1
	for idx, item := range m.transcriptItems {
		switch typed := item.(type) {
		case *userMessageTranscriptItem:
			m.messageItemIndexByID[typed.message.ID] = idx
		case *assistantMessageTranscriptItem:
			m.messageItemIndexByID[typed.message.ID] = idx
		case *pendingAssistantTranscriptItem:
			m.pendingTranscriptIndex = idx
		case toolRunTranscriptItem:
			if id := strings.TrimSpace(typed.RunID()); id != "" {
				m.toolRunItemIndexByID[id] = idx
			}
			switch concrete := typed.(type) {
			case *bashToolRunTranscriptItem:
				if concrete.run.ApprovalID > 0 {
					m.toolRunItemIndexByAppr[concrete.run.ApprovalID] = idx
				}
			case *readToolRunTranscriptItem:
				if concrete.run.ApprovalID > 0 {
					m.toolRunItemIndexByAppr[concrete.run.ApprovalID] = idx
				}
			case *writeToolRunTranscriptItem:
				if concrete.run.ApprovalID > 0 {
					m.toolRunItemIndexByAppr[concrete.run.ApprovalID] = idx
				}
			case *editToolRunTranscriptItem:
				if concrete.run.ApprovalID > 0 {
					m.toolRunItemIndexByAppr[concrete.run.ApprovalID] = idx
				}
			case *genericToolRunTranscriptItem:
				if concrete.run.ApprovalID > 0 {
					m.toolRunItemIndexByAppr[concrete.run.ApprovalID] = idx
				}
			}
		}
	}
}

func (m *Model) transcriptBlockForController(item transcriptItemController) transcriptBlock {
	switch typed := item.(type) {
	case *userMessageTranscriptItem:
		return transcriptBlock{Kind: transcriptBlockMessage, Message: typed.message, Parts: typed.parts}
	case *assistantMessageTranscriptItem:
		return transcriptBlock{Kind: transcriptBlockMessage, Message: typed.message, Parts: typed.parts}
	case *pendingAssistantTranscriptItem:
		return transcriptBlock{
			Kind:    transcriptBlockMessage,
			Pending: true,
			Message: domain.Message{Role: domain.MessageRoleAssistant, CreatedAt: typed.createdAt},
			Parts:   typed.Parts(),
		}
	case toolRunTranscriptItem:
		switch concrete := typed.(type) {
		case *bashToolRunTranscriptItem:
			return transcriptBlock{Kind: transcriptBlockToolRun, ToolRun: concrete.run}
		case *readToolRunTranscriptItem:
			return transcriptBlock{Kind: transcriptBlockToolRun, ToolRun: concrete.run}
		case *writeToolRunTranscriptItem:
			return transcriptBlock{Kind: transcriptBlockToolRun, ToolRun: concrete.run}
		case *editToolRunTranscriptItem:
			return transcriptBlock{Kind: transcriptBlockToolRun, ToolRun: concrete.run}
		case *genericToolRunTranscriptItem:
			return transcriptBlock{Kind: transcriptBlockToolRun, ToolRun: concrete.run}
		}
	}
	return transcriptBlock{}
}

func (m *Model) transcriptElement(runtime *ui.Runtime) ui.Node {
	retained := m.syncRetainedTranscript()
	if retained == nil {
		return nil
	}
	width := max(0, m.viewport.Width)
	height := max(0, m.transcriptViewportHeight())
	if runtime != nil {
		runtime.BeginFrame()
	}
	return ui.AsNode(ui.ScrollBox{
		Child:   ui.AsNode(retained),
		OffsetY: max(0, m.viewport.YOffset),
		Width:   width,
		Height:  height,
	})
}

func (m *Model) syncRetainedTranscript() *ui.RetainedTranscript {
	retained := m.ensureRetainedTranscript()
	if m.transcriptDirty || len(retained.Items()) == 0 {
		items := m.buildTranscriptItems()
		m.syncRetainedTranscriptItems(retained, items)
		m.transcriptDirty = false
	} else {
		m.syncTranscriptControllerState()
	}
	return retained
}

func (m *Model) syncTranscriptControllerState() {
	if len(m.transcriptItems) == 0 {
		return
	}
	for idx, item := range m.transcriptItems {
		changed := false
		switch typed := item.(type) {
		case *assistantMessageTranscriptItem:
			if typed.showReasoning != m.showReasoning {
				typed.SetReasoningVisible(m.showReasoning)
				changed = true
			}
			if typed.showSystem != m.showSystem {
				typed.SetSystemVisible(m.showSystem)
				changed = true
			}
		case *pendingAssistantTranscriptItem:
			if typed.showReasoning != m.showReasoning {
				typed.SetReasoningVisible(m.showReasoning)
				changed = true
			}
		case toolRunTranscriptItem:
			wantOut := m.expandedToolRuns[typed.RunID()]
			switch concrete := typed.(type) {
			case *bashToolRunTranscriptItem:
				wantCmd := m.expandedToolRunCommands[typed.RunID()]
				if concrete.expandedOutput != wantOut {
					concrete.SetExpandedOutput(wantOut)
					changed = true
				}
				if concrete.expandedCommand != wantCmd {
					concrete.SetExpandedCommand(wantCmd)
					changed = true
				}
			case *genericToolRunTranscriptItem:
				wantCmd := m.expandedToolRunCommands[typed.RunID()]
				if concrete.expandedOutput != wantOut {
					concrete.SetExpandedOutput(wantOut)
					changed = true
				}
				if concrete.expandedCommand != wantCmd {
					concrete.SetExpandedCommand(wantCmd)
					changed = true
				}
			case *readToolRunTranscriptItem:
				if concrete.expandedOutput != wantOut {
					concrete.SetExpandedOutput(wantOut)
					changed = true
				}
			case *writeToolRunTranscriptItem:
				if concrete.expandedOutput != wantOut {
					concrete.SetExpandedOutput(wantOut)
					changed = true
				}
			case *editToolRunTranscriptItem:
				if concrete.expandedOutput != wantOut {
					concrete.SetExpandedOutput(wantOut)
					changed = true
				}
			}
		}
		if changed {
			_ = m.replaceTranscriptItemAt(idx)
		}
	}
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

func (m *Model) transcriptSeparator(prev, next transcriptBlock) string {
	if !m.halfBlocksEnabled() {
		return "\n\n"
	}
	if m.isHalfBlockUserMessage(prev) || m.isHalfBlockUserMessage(next) {
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
	return max(0, ui.TextHeight("x"+separator+"x")-2)
}

func (m *Model) renderTranscriptActivityElement() ui.Node {
	if !m.busy.transcriptActive() {
		return nil
	}
	return ui.AsNode(ui.ActivityIndicator{
		Indicator: ui.WorkingIndicatorLine(m.workingIndicator(), m.busy.statusOrDefault("Working ...")),
		Palette:   m.palette,
	})
}

func (m *Model) sessionUsageSummary(sessionID int64) (domain.Usage, bool) {
	if m.store == nil {
		return domain.Usage{}, false
	}
	chatID := int64(0)
	if m.currentSession.ID == sessionID {
		chatID = m.currentChat.ID
	}
	var (
		messages []domain.Message
		parts    map[int64][]domain.Part
		err      error
	)
	if chatID > 0 {
		messages, parts, err = m.store.PartsForChat(context.Background(), chatID)
	} else {
		messages, parts, err = m.store.PartsForSession(context.Background(), sessionID)
	}
	if err != nil {
		return domain.Usage{}, false
	}
	return sessionctx.LatestUsage(messages, parts)
}

func (m Model) pendingAssistantParts() []domain.Part {
	if strings.TrimSpace(m.pendingAssistant.Text) == "" && strings.TrimSpace(m.pendingAssistant.Reasoning) == "" {
		return nil
	}
	parts := make([]domain.Part, 0, 2)
	if strings.TrimSpace(m.pendingAssistant.Reasoning) != "" {
		parts = append(parts, domain.Part{ID: -1, Kind: domain.PartKindReasoning, Body: m.pendingAssistant.Reasoning})
	}
	if strings.TrimSpace(m.pendingAssistant.Text) != "" {
		parts = append(parts, domain.Part{ID: -2, Kind: domain.PartKindText, Body: m.pendingAssistant.Text})
	}
	return parts
}

func (m Model) loadCmd() ui.Cmd {
	return func() ui.Msg {
		ctx := context.Background()
		totalStart := time.Now()
		stepStart := time.Now()
		allSessions, err := m.store.ListSessions(ctx)
		if err != nil {
			return promptDoneMsg{err: err}
		}
		m.recordStartupTiming(0, "list_sessions", stepStart, map[string]string{
			"count": strconv.Itoa(len(allSessions)),
			"mode":  startupModeLabel(m.startupMode),
		})
		stepStart = time.Now()
		sessions := m.visibleSessions(allSessions)
		m.recordStartupTiming(0, "visible_sessions", stepStart, map[string]string{
			"count": strconv.Itoa(len(sessions)),
			"mode":  startupModeLabel(m.startupMode),
		})
		if m.startupMode == StartupModeResume {
			if len(sessions) == 0 {
				return m.newSessionCmd()()
			}
			m.recordStartupTiming(0, "resume_picker_ready", totalStart, map[string]string{
				"count": strconv.Itoa(len(sessions)),
			})
			return sessionPickerMsg{sessions: sessions}
		}
		if m.startupMode == StartupModeNew {
			return m.newSessionCmd()()
		}
		if len(sessions) == 0 {
			return m.newSessionCmd()()
		}
		current := sessions[0]
		stepStart = time.Now()
		chats, err := m.store.ListChats(ctx, current.ID)
		if err != nil {
			return promptDoneMsg{err: err}
		}
		m.recordStartupTiming(current.ID, "list_chats", stepStart, map[string]string{
			"count": strconv.Itoa(len(chats)),
		})
		currentChat := newestChat(chats)
		if currentChat.ID == 0 {
			stepStart = time.Now()
			currentChat, err = m.store.DefaultChat(ctx, current.ID)
			if err != nil {
				return promptDoneMsg{err: err}
			}
			chats = append(chats, currentChat)
			m.recordStartupTiming(current.ID, "default_chat", stepStart, map[string]string{
				"chat_id": strconv.FormatInt(currentChat.ID, 10),
			})
		}
		stepStart = time.Now()
		messages, parts, err := m.store.PartsForChat(ctx, currentChat.ID)
		if err != nil {
			return promptDoneMsg{err: err}
		}
		m.recordStartupTiming(current.ID, "parts_for_chat", stepStart, map[string]string{
			"chat_id":  strconv.FormatInt(currentChat.ID, 10),
			"messages": strconv.Itoa(len(messages)),
			"parts":    strconv.Itoa(len(parts)),
		})
		stepStart = time.Now()
		approvals, err := m.store.PendingApprovalsForChat(ctx, currentChat.ID)
		if err != nil {
			return promptDoneMsg{err: err}
		}
		m.recordStartupTiming(current.ID, "pending_approvals", stepStart, map[string]string{
			"chat_id": strconv.FormatInt(currentChat.ID, 10),
			"count":   strconv.Itoa(len(approvals)),
		})
		stepStart = time.Now()
		plan, todos, err := m.loadPlanningState(ctx, current.ID)
		if err != nil {
			return promptDoneMsg{err: err}
		}
		m.recordStartupTiming(current.ID, "planning_state", stepStart, map[string]string{
			"milestones": strconv.Itoa(len(plan.Milestones)),
			"todos":      strconv.Itoa(len(todos)),
		})
		stepStart = time.Now()
		workspaceStatus, err := workspace.Snapshot(ctx, m.workdir)
		if err != nil {
			return promptDoneMsg{err: err}
		}
		m.recordStartupTiming(current.ID, "workspace_snapshot", stepStart, map[string]string{
			"files": strconv.Itoa(len(workspaceStatus.Files)),
		})
		m.recordStartupTiming(current.ID, "startup_load_total", totalStart, map[string]string{
			"chat_id": strconv.FormatInt(currentChat.ID, 10),
			"mode":    startupModeLabel(m.startupMode),
		})
		return loadMsg{
			sessions:  sessions,
			chats:     chats,
			current:   current,
			chat:      currentChat,
			messages:  messages,
			parts:     parts,
			approvals: approvals,
			plan:      plan,
			todos:     todos,
			workspace: workspaceStatus,
		}
	}
}

type loadMsg struct {
	sessions     []domain.Session
	chats        []domain.Chat
	current      domain.Session
	chat         domain.Chat
	messages     []domain.Message
	parts        map[int64][]domain.Part
	approvals    []store.Approval
	plan         store.MilestonePlan
	todos        []store.TodoItem
	workspace    workspace.Status
	preserveBusy bool
}

func (m Model) loadPlanningState(ctx context.Context, sessionID int64) (store.MilestonePlan, []store.TodoItem, error) {
	plan, err := m.store.GetMilestonePlan(ctx, sessionID)
	if err != nil {
		return store.MilestonePlan{}, nil, err
	}
	active, ok := tools.ActiveMilestone(plan)
	if !ok {
		return plan, nil, nil
	}
	todos, err := m.store.ListTodos(ctx, sessionID, active.Ref)
	if err != nil {
		return store.MilestonePlan{}, nil, err
	}
	return plan, todos, nil
}

func newestChat(chats []domain.Chat) domain.Chat {
	var best domain.Chat
	for _, item := range chats {
		if item.ID == 0 {
			continue
		}
		if best.ID == 0 || item.UpdatedAt.After(best.UpdatedAt) || (item.UpdatedAt.Equal(best.UpdatedAt) && item.ID > best.ID) {
			best = item
		}
	}
	return best
}

func (m Model) promptCmd(ctx context.Context, prompt string, drafts []attachment.Draft, refs []reference.Draft) ui.Cmd {
	return func() ui.Msg {
		session := m.currentSession
		chat := m.currentChat
		if session.ID == 0 {
			var err error
			session, err = m.persistDraftSession(ctx)
			if err != nil {
				return runPromptMsg{err: err}
			}
			chat, err = m.store.DefaultChat(ctx, session.ID)
			if err != nil {
				return runPromptMsg{err: err}
			}
		}
		providerID, contextWindow, contextChecked, err := m.ensureRuntimeContextWindow(ctx, session)
		if err != nil {
			return runPromptMsg{err: err}
		}
		events, err := m.agent.RunPromptInChat(ctx, session, chat, prompt, drafts, refs, m.pendingModelNote)
		return runPromptMsg{
			session:        session,
			chat:           chat,
			events:         events,
			err:            err,
			providerID:     providerID,
			contextWindow:  contextWindow,
			contextChecked: contextChecked,
		}
	}
}

func (m *Model) submitBangPrompt(bang bangPrompt, followup bangFollowupMode) ui.Cmd {
	if len(m.draftAttachments) > 0 || len(m.draftReferences) > 0 {
		m.status = "Bang commands do not support attachments or references"
		return m.syncWindowTitleCmd()
	}
	if bang.Command == "" {
		m.status = "Command is empty"
		return m.syncWindowTitleCmd()
	}
	if !bang.Double {
		followup = bangFollowupNone
	}
	if bang.Double {
		if ok, status := m.canSendPrompt(); !ok {
			if m.shouldOpenConnectDialogForSendFailure() {
				m.openConnectDialog()
			}
			m.status = status
			return m.syncWindowTitleCmd()
		}
	}
	preserveBusy := m.loading
	m.resetComposerInput()
	m.draftAttachments = nil
	m.draftReferences = nil
	if !preserveBusy {
		m.startBusy(busyScopeTranscript, fmt.Sprintf("Running %s…", bang.Command))
	} else {
		m.status = fmt.Sprintf("Running %s…", bang.Command)
	}
	return m.runBangPromptCmd(context.Background(), bang, followup, preserveBusy)
}

func (m Model) runBangPromptCmd(ctx context.Context, bang bangPrompt, followup bangFollowupMode, preserveBusy bool) ui.Cmd {
	return func() ui.Msg {
		session := m.currentSession
		chat := m.currentChat
		if m.store == nil {
			return bangCommandMsg{err: errors.New("store is not available"), preserveBusy: preserveBusy}
		}
		if session.ID == 0 {
			var err error
			session, err = m.persistDraftSession(ctx)
			if err != nil {
				return bangCommandMsg{err: err, preserveBusy: preserveBusy}
			}
		}
		if chat.ID == 0 || chat.SessionID != session.ID {
			var err error
			chat, err = m.store.DefaultChat(ctx, session.ID)
			if err != nil {
				return bangCommandMsg{err: err, preserveBusy: preserveBusy}
			}
		}
		req := tools.Request{
			Tool: domain.ToolKindBash,
			Args: map[string]string{"command": bang.Command},
		}
		registry := tools.NewRegistry(m.workdir)
		registry.SetEditForgiveness(m.cfg.UI.EditForgiveness)
		result, err := registry.ExecuteWithChat(ctx, m.store, session.ID, chat, req)
		if err != nil && result.Meta["exit_code"] == "" {
			return bangCommandMsg{session: session, chat: chat, err: err, preserveBusy: preserveBusy}
		}
		persistCtx := tools.WithChatID(ctx, chat.ID)
		events, persistErr := registry.PersistResultInChat(persistCtx, m.store, session.ID, chat.ID, req, result)
		if persistErr != nil {
			return bangCommandMsg{session: session, chat: chat, err: persistErr, preserveBusy: preserveBusy}
		}
		collected := []domain.Event{{
			Kind: domain.EventKindToolStart,
			Tool: req.Tool,
			Text: tools.Preview(req),
		}}
		for evt := range events {
			collected = append(collected, evt)
		}
		msg := bangCommandMsg{
			session:      session,
			chat:         chat,
			command:      bang.Command,
			events:       collected,
			preserveBusy: preserveBusy,
		}
		if bang.Double {
			msg.followupMode = followup
			msg.followupPrompt = formatBangFollowupPrompt(bang.Command, result)
		}
		return msg
	}
}

func (m Model) continueCmd(ctx context.Context) ui.Cmd {
	return func() ui.Msg {
		session := m.currentSession
		chat := m.currentChat
		if session.ID == 0 {
			return runPromptMsg{err: fmt.Errorf("no saved session to continue")}
		}
		providerID, contextWindow, contextChecked, err := m.ensureRuntimeContextWindow(ctx, session)
		if err != nil {
			return runPromptMsg{err: err}
		}
		events, err := m.agent.RunContinueInChat(ctx, session, chat, m.pendingModelNote)
		return runPromptMsg{
			session:        session,
			chat:           chat,
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
			chat:      domain.Chat{},
			chats:     nil,
			sessions:  sessions,
			messages:  nil,
			parts:     map[int64][]domain.Part{},
			approvals: nil,
			plan:      store.MilestonePlan{},
			todos:     nil,
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
		totalStart := time.Now()
		if m.debug != nil {
			m.debug.RecordLifecycle(sessionID, "chat_load_started", "resume session", nil)
		}
		stepStart := time.Now()
		allSessions, err := m.store.ListSessions(ctx)
		if err != nil {
			return promptDoneMsg{err: err}
		}
		m.recordLoadTiming(sessionID, 0, "list_sessions", stepStart, map[string]string{
			"count": strconv.Itoa(len(allSessions)),
		})
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
		stepStart = time.Now()
		chats, err := m.store.ListChats(ctx, session.ID)
		if err != nil {
			return promptDoneMsg{err: err}
		}
		m.recordLoadTiming(sessionID, 0, "list_chats", stepStart, map[string]string{
			"count": strconv.Itoa(len(chats)),
		})
		currentChat := newestChat(chats)
		if currentChat.ID == 0 {
			stepStart = time.Now()
			currentChat, err = m.store.DefaultChat(ctx, session.ID)
			if err != nil {
				return promptDoneMsg{err: err}
			}
			chats = append(chats, currentChat)
			m.recordLoadTiming(sessionID, currentChat.ID, "default_chat", stepStart, nil)
		}
		stepStart = time.Now()
		messages, parts, err := m.store.PartsForChat(ctx, currentChat.ID)
		if err != nil {
			return promptDoneMsg{err: err}
		}
		m.recordLoadTiming(sessionID, currentChat.ID, "parts_for_chat", stepStart, map[string]string{
			"messages": strconv.Itoa(len(messages)),
			"parts":    strconv.Itoa(len(parts)),
		})
		stepStart = time.Now()
		approvals, err := m.store.PendingApprovalsForChat(ctx, currentChat.ID)
		if err != nil {
			return promptDoneMsg{err: err}
		}
		m.recordLoadTiming(sessionID, currentChat.ID, "pending_approvals", stepStart, map[string]string{
			"count": strconv.Itoa(len(approvals)),
		})
		stepStart = time.Now()
		plan, todos, err := m.loadPlanningState(ctx, session.ID)
		if err != nil {
			return promptDoneMsg{err: err}
		}
		m.recordLoadTiming(sessionID, currentChat.ID, "planning_state", stepStart, map[string]string{
			"milestones": strconv.Itoa(len(plan.Milestones)),
			"todos":      strconv.Itoa(len(todos)),
		})
		stepStart = time.Now()
		workspaceStatus, err := workspace.Snapshot(ctx, m.workdir)
		if err != nil {
			return promptDoneMsg{err: err}
		}
		m.recordLoadTiming(sessionID, currentChat.ID, "workspace_snapshot", stepStart, map[string]string{
			"files": strconv.Itoa(len(workspaceStatus.Files)),
		})
		m.recordLoadTiming(sessionID, currentChat.ID, "load_chat_total", totalStart, nil)
		return loadMsg{
			sessions:  sessions,
			chats:     chats,
			current:   session,
			chat:      currentChat,
			messages:  messages,
			parts:     parts,
			approvals: approvals,
			plan:      plan,
			todos:     todos,
			workspace: workspaceStatus,
		}
	}
}

func (m Model) loadChatCmd(sessionID, chatID int64) ui.Cmd {
	return func() ui.Msg {
		if sessionID == 0 || chatID == 0 {
			return nil
		}
		ctx := context.Background()
		totalStart := time.Now()
		if m.debug != nil {
			m.debug.RecordLifecycle(sessionID, "chat_load_started", fmt.Sprintf("chat=%d", chatID), map[string]string{
				"chat_id": strconv.FormatInt(chatID, 10),
			})
		}
		stepStart := time.Now()
		allSessions, err := m.store.ListSessions(ctx)
		if err != nil {
			return promptDoneMsg{err: err}
		}
		m.recordLoadTiming(sessionID, chatID, "list_sessions", stepStart, map[string]string{
			"count": strconv.Itoa(len(allSessions)),
		})
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
		stepStart = time.Now()
		chats, err := m.store.ListChats(ctx, sessionID)
		if err != nil {
			return promptDoneMsg{err: err}
		}
		m.recordLoadTiming(sessionID, chatID, "list_chats", stepStart, map[string]string{
			"count": strconv.Itoa(len(chats)),
		})
		stepStart = time.Now()
		currentChat, err := m.store.GetChat(ctx, chatID)
		if err != nil {
			return promptDoneMsg{err: err}
		}
		m.recordLoadTiming(sessionID, chatID, "get_chat", stepStart, nil)
		stepStart = time.Now()
		messages, parts, err := m.store.PartsForChat(ctx, currentChat.ID)
		if err != nil {
			return promptDoneMsg{err: err}
		}
		m.recordLoadTiming(sessionID, chatID, "parts_for_chat", stepStart, map[string]string{
			"messages": strconv.Itoa(len(messages)),
			"parts":    strconv.Itoa(len(parts)),
		})
		stepStart = time.Now()
		approvals, err := m.store.PendingApprovalsForChat(ctx, currentChat.ID)
		if err != nil {
			return promptDoneMsg{err: err}
		}
		m.recordLoadTiming(sessionID, chatID, "pending_approvals", stepStart, map[string]string{
			"count": strconv.Itoa(len(approvals)),
		})
		stepStart = time.Now()
		plan, todos, err := m.loadPlanningState(ctx, session.ID)
		if err != nil {
			return promptDoneMsg{err: err}
		}
		m.recordLoadTiming(sessionID, chatID, "planning_state", stepStart, map[string]string{
			"milestones": strconv.Itoa(len(plan.Milestones)),
			"todos":      strconv.Itoa(len(todos)),
		})
		stepStart = time.Now()
		workspaceStatus, err := workspace.Snapshot(ctx, m.workdir)
		if err != nil {
			return promptDoneMsg{err: err}
		}
		m.recordLoadTiming(sessionID, chatID, "workspace_snapshot", stepStart, map[string]string{
			"files": strconv.Itoa(len(workspaceStatus.Files)),
		})
		m.recordLoadTiming(sessionID, chatID, "load_chat_total", totalStart, nil)
		return loadMsg{
			sessions:  sessions,
			chats:     chats,
			current:   session,
			chat:      currentChat,
			messages:  messages,
			parts:     parts,
			approvals: approvals,
			plan:      plan,
			todos:     todos,
			workspace: workspaceStatus,
		}
	}
}

func (m Model) createChatCmd(sessionID int64, role domain.WorkflowRole, title string) ui.Cmd {
	return func() ui.Msg {
		ctx := context.Background()
		start := time.Now()
		var parentChatID *int64
		if m.currentChat.ID > 0 && m.currentChat.SessionID == sessionID {
			parentChatID = &m.currentChat.ID
		}
		chat, err := m.store.CreateChat(ctx, sessionID, title, role, parentChatID)
		if err != nil {
			return promptDoneMsg{err: err}
		}
		m.recordLoadTiming(sessionID, chat.ID, "create_chat", start, map[string]string{
			"role": string(role),
		})
		return m.loadChatCmd(sessionID, chat.ID)()
	}
}

func (m Model) recordLoadTiming(sessionID, chatID int64, step string, started time.Time, meta map[string]string) {
	if m.debug == nil || sessionID == 0 || started.IsZero() {
		return
	}
	if meta == nil {
		meta = map[string]string{}
	}
	meta["step"] = step
	meta["duration_ms"] = strconv.FormatInt(time.Since(started).Milliseconds(), 10)
	if chatID > 0 {
		meta["chat_id"] = strconv.FormatInt(chatID, 10)
	}
	m.debug.RecordLifecycle(sessionID, "chat_load_timing", step, meta)
}

func (m Model) recordStartupTiming(sessionID int64, step string, started time.Time, meta map[string]string) {
	if m.debug == nil || started.IsZero() {
		return
	}
	if meta == nil {
		meta = map[string]string{}
	}
	meta["step"] = step
	meta["duration_ms"] = strconv.FormatInt(time.Since(started).Milliseconds(), 10)
	m.debug.RecordLifecycle(sessionID, "startup_timing", step, meta)
}

func startupModeLabel(mode StartupMode) string {
	switch mode {
	case StartupModeNew:
		return "new"
	case StartupModeResume:
		return "resume"
	default:
		return "auto"
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
		chats, err := m.store.ListChats(ctx, session.ID)
		if err != nil {
			return agentsRefreshMsg{err: err}
		}
		currentChat := m.currentChat
		if currentChat.ID == 0 || currentChat.SessionID != session.ID {
			currentChat = newestChat(chats)
		}
		if currentChat.ID == 0 {
			currentChat, err = m.store.DefaultChat(ctx, session.ID)
			if err != nil {
				return agentsRefreshMsg{err: err}
			}
			chats = append(chats, currentChat)
		}
		messages, parts, err := m.store.PartsForChat(ctx, currentChat.ID)
		if err != nil {
			return agentsRefreshMsg{err: err}
		}
		approvals, err := m.store.PendingApprovalsForChat(ctx, currentChat.ID)
		if err != nil {
			return agentsRefreshMsg{err: err}
		}
		plan, todos, err := m.loadPlanningState(ctx, session.ID)
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
				chats:     chats,
				current:   session,
				chat:      currentChat,
				messages:  messages,
				parts:     parts,
				approvals: approvals,
				plan:      plan,
				todos:     todos,
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
		chats, err := m.store.ListChats(ctx, forked.ID)
		if err != nil {
			return forkSessionMsg{err: err}
		}
		currentChat := newestChat(chats)
		if currentChat.ID == 0 {
			currentChat, err = m.store.DefaultChat(ctx, forked.ID)
			if err != nil {
				return forkSessionMsg{err: err}
			}
			chats = append(chats, currentChat)
		}
		messages, parts, err := m.store.PartsForChat(ctx, currentChat.ID)
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
		approvals, err := m.store.PendingApprovalsForChat(ctx, currentChat.ID)
		if err != nil {
			return forkSessionMsg{err: err}
		}
		plan, todos, err := m.loadPlanningState(ctx, forked.ID)
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
				chats:     chats,
				current:   forked,
				chat:      currentChat,
				messages:  messages,
				parts:     parts,
				approvals: approvals,
				plan:      plan,
				todos:     todos,
				workspace: workspaceStatus,
			},
		}
	}
}

func (m Model) reloadDetailsCmd() ui.Cmd {
	return func() ui.Msg {
		var msg ui.Msg
		if m.currentChat.ID != 0 {
			msg = m.loadChatCmd(m.currentSession.ID, m.currentChat.ID)()
		} else {
			msg = m.loadSessionCmd(m.currentSession.ID)()
		}
		load, ok := msg.(loadMsg)
		if !ok {
			return msg
		}
		load.preserveBusy = true
		return load
	}
}

func nextEventCmd(chatID int64, events <-chan domain.Event) ui.Cmd {
	return func() ui.Msg {
		evt, ok := <-events
		if !ok {
			return eventMsg{}
		}
		return eventMsg{chatID: chatID, event: evt, events: events}
	}
}

type bangPrompt struct {
	Double  bool
	Command string
}

func parseBangPrompt(prompt string) (bangPrompt, bool) {
	trimmed := strings.TrimSpace(prompt)
	if strings.HasPrefix(trimmed, "!!") {
		command := strings.TrimSpace(strings.TrimPrefix(trimmed, "!!"))
		return bangPrompt{Double: true, Command: command}, true
	}
	if strings.HasPrefix(trimmed, "!") {
		command := strings.TrimSpace(strings.TrimPrefix(trimmed, "!"))
		return bangPrompt{Command: command}, true
	}
	return bangPrompt{}, false
}

func formatBangFollowupPrompt(command string, result tools.Result) string {
	output := strings.TrimSpace(result.Output)
	if output == "" {
		output = "(no output)"
	}
	exitCode := strings.TrimSpace(result.Meta["exit_code"])
	if exitCode == "" {
		exitCode = "0"
	}
	return fmt.Sprintf(
		"User-requested shell command:\n\n```bash\n%s\n```\n\nExit code: %s\n\nShell result:\n\n```text\n%s\n```\n\nUse this shell result as context. Do not rerun the command unless needed.",
		command,
		exitCode,
		output,
	)
}

func Run(cfg config.Config, st *store.Store, a *agent.Engine, mode StartupMode, debug *debugsrv.Recorder, debugServer *debugsrv.Server) error {
	workdir, err := os.Getwd()
	if err != nil {
		return err
	}
	return RunWithWorkdir(cfg, st, a, mode, debug, debugServer, workdir, StartupOptions{})
}

func RunWithWorkdir(cfg config.Config, st *store.Store, a *agent.Engine, mode StartupMode, debug *debugsrv.Recorder, debugServer *debugsrv.Server, workdir string, startupOpts StartupOptions) error {
	model, err := NewWithWorkdir(cfg, st, a, mode, debug, workdir, startupOpts)
	if err != nil {
		return err
	}
	model.syncDebugRuntime()
	p := ui.NewProgram(model, ui.WithAltScreen(), ui.WithoutSignalHandler())
	if debugServer != nil {
		debugServer.SetInputSink(p.Send)
		defer debugServer.SetInputSink(nil)
	}
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
	if len(msg.chats) > 0 || m.currentSession.ID != msg.current.ID {
		m.chats = slices.Clone(msg.chats)
	}
	m.currentSession = m.normalizeSessionToolStates(msg.current)
	if msg.chat.ID != 0 {
		m.currentChat = msg.chat
		m.clampQueueSelection()
	}
	m.messages = msg.messages
	m.parts = msg.parts
	m.approvals = msg.approvals
	m.milestonePlan = msg.plan
	m.todos = msg.todos
	m.workspace = msg.workspace
	m.resetComposerHistory()
	m.approvalDialog = nil
	m.draftAttachments = nil
	m.draftReferences = nil
	m.closePicker()
	m.closeSessionDialog()
	m.closePreferencesDialog()
	m.closeToolsDialog()
	m.closeConnectDialog()
	m.closeMCPDialog()
	m.closeDisconnectDialog()
	m.closeModelDialog()
	m.closeAgentsModal()
	m.agentsDrift = m.currentSession.ProjectChecksum != "" &&
		m.workspace.AgentsChecksum != "" &&
		m.currentSession.ProjectChecksum != m.workspace.AgentsChecksum
	m.ensureRetainedTranscript().Clear()
	m.transcriptDirty = true
	m.syncCurrentChatBusy()
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
	case trimmed == "/chat new":
		m.resetComposerInput()
		if m.currentSession.ID == 0 {
			m.status = "Save the session with a prompt before creating chats"
			return m, m.syncWindowTitleCmd(), true
		}
		title := fmt.Sprintf("Chat %d", len(m.chats)+1)
		m.startBusy(busyScopeSidebar, "Creating chat…")
		return m, ui.Batch(m.createChatCmd(m.currentSession.ID, domain.WorkflowRoleGeneral, title), m.spinnerCmdIfNeeded()), true
	case trimmed == "/chat next":
		m.resetComposerInput()
		return m, m.switchChatByDelta(1), true
	case trimmed == "/chat prev":
		m.resetComposerInput()
		return m, m.switchChatByDelta(-1), true
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
	case trimmed == "/mcp":
		m.resetComposerInput()
		m.openMCPDialog()
		return m, m.syncWindowTitleCmd(), true
	case trimmed == "/mcp-reload":
		m.resetComposerInput()
		m.status = "Reloading MCP servers…"
		return m, ui.Batch(m.mcpReloadCmd(), m.syncWindowTitleCmd()), true
	case trimmed == "/mcp-status":
		m.resetComposerInput()
		m.status = m.mcpStatusSummary()
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

func (m *Model) switchChatByDelta(delta int) ui.Cmd {
	if len(m.chats) <= 1 {
		m.status = "No other chats in this session"
		return m.syncWindowTitleCmd()
	}
	idx := 0
	for i, item := range m.chats {
		if item.ID == m.currentChat.ID {
			idx = i
			break
		}
	}
	nextIdx := (idx + delta) % len(m.chats)
	if nextIdx < 0 {
		nextIdx += len(m.chats)
	}
	next := m.chats[nextIdx]
	m.startBusy(busyScopeSidebar, fmt.Sprintf("Switching to chat %d…", next.ID))
	return ui.Batch(m.loadChatCmd(next.SessionID, next.ID), m.spinnerCmdIfNeeded())
}

func (m Model) approvalPermissionProfileCmd(ctx context.Context, approvalID int64, profile string) ui.Cmd {
	return func() ui.Msg {
		events, err := m.agent.SetPermissionProfileInChatAndReevaluateApproval(ctx, m.currentSession.ID, m.currentChat.ID, approvalID, profile)
		return promptDoneMsg{events: events, err: err}
	}
}

func (m *Model) beginActiveOperation() context.Context {
	chatID := m.currentChatID()
	if chatID == 0 {
		if m.activeOpCancel != nil {
			m.activeOpCancel()
		}
		ctx, cancel := context.WithCancel(context.Background())
		m.activeOpCancel = cancel
		m.interruptArmedAt = time.Time{}
		return ctx
	}
	if m.activeOpCancels == nil {
		m.activeOpCancels = map[int64]context.CancelFunc{}
	}
	if cancel := m.activeOpCancels[chatID]; cancel != nil {
		cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.activeOpCancel = cancel
	m.activeOpCancels[chatID] = cancel
	m.interruptArmedAt = time.Time{}
	return ctx
}

func (m *Model) handleInterruptKey() (ui.Model, ui.Cmd) {
	if m.activeOpCancel == nil && (m.activeOpCancels == nil || m.activeOpCancels[m.currentChatID()] == nil) {
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
	if cancel := m.activeOpCancel; cancel != nil {
		cancel()
	}
	return m, m.syncWindowTitleCmd()
}

func (m *Model) queueComposerPrompt(kind domain.QueuedInputKind) (ui.Model, ui.Cmd) {
	prompt := strings.TrimSpace(m.composer.Value())
	if prompt == "" && len(m.draftAttachments) == 0 && len(m.draftReferences) == 0 {
		return m, nil
	}
	if strings.HasPrefix(prompt, "/") {
		m.status = "Wait for the current run to finish before using slash commands"
		return m, m.syncWindowTitleCmd()
	}
	items := cloneQueuedInputs(m.currentChat.QueuedInputs)
	items = append(items, domain.QueuedInput{
		ID:          nextQueuedInputID(),
		Kind:        kind,
		Text:        prompt,
		Attachments: queuedAttachmentsFromDrafts(m.draftAttachments),
		References:  queuedReferencesFromDrafts(m.draftReferences),
		CreatedAt:   time.Now().UTC(),
	})
	m.setQueuedInputs(items)
	m.resetComposerInput()
	m.draftAttachments = nil
	m.draftReferences = nil
	m.status = queuedInputStatusText(kind, false)
	return m, ui.Batch(m.saveQueuedInputsCmd(m.currentChat.ID, items), m.syncWindowTitleCmd())
}

func (m *Model) queueContinuePrompt() (ui.Model, ui.Cmd) {
	if ok, status := m.canContinue(); !ok {
		m.status = status
		return m, m.syncWindowTitleCmd()
	}
	items := cloneQueuedInputs(m.currentChat.QueuedInputs)
	items = append(items, domain.QueuedInput{
		ID:        nextQueuedInputID(),
		Kind:      domain.QueuedInputKindContinue,
		CreatedAt: time.Now().UTC(),
	})
	m.setQueuedInputs(items)
	m.status = queuedInputStatusText(domain.QueuedInputKindContinue, false)
	return m, ui.Batch(m.saveQueuedInputsCmd(m.currentChat.ID, items), m.syncWindowTitleCmd())
}

func (m *Model) popQueuedPromptForEditing() (ui.Model, ui.Cmd) {
	items := cloneQueuedInputs(m.currentChat.QueuedInputs)
	if len(items) == 0 {
		return m, nil
	}
	idx := m.selectedQueuedInputIndex()
	if idx < 0 || idx >= len(items) {
		idx = 0
	}
	queued := items[idx]
	if queued.Kind == domain.QueuedInputKindContinue {
		items = append(items[:idx], items[idx+1:]...)
		m.setQueuedInputs(items)
		m.status = "Removed queued continue"
		return m, ui.Batch(m.saveQueuedInputsCmd(m.currentChat.ID, items), m.syncWindowTitleCmd())
	}
	m.syncDraftReferencesFromComposer()
	m.syncDraftAttachmentsFromComposer()
	currentText := strings.TrimSpace(m.composer.Value())
	hasCurrentDraft := currentText != "" || len(m.draftAttachments) > 0 || len(m.draftReferences) > 0
	if hasCurrentDraft {
		items[idx] = domain.QueuedInput{
			ID:          items[idx].ID,
			Kind:        domain.QueuedInputKindQueued,
			Text:        currentText,
			Attachments: queuedAttachmentsFromDrafts(m.draftAttachments),
			References:  queuedReferencesFromDrafts(m.draftReferences),
			CreatedAt:   items[idx].CreatedAt,
		}
		m.status = "Swapped queued prompt into composer"
	} else {
		items = append(items[:idx], items[idx+1:]...)
		m.status = "Restored queued prompt to composer"
	}
	m.setQueuedInputs(items)
	m.draftAttachments = queuedAttachmentDrafts(queued.Attachments)
	m.draftReferences = queuedReferenceDrafts(queued.References)
	m.setComposerDraftValue(queued.Text)
	return m, ui.Batch(m.saveQueuedInputsCmd(m.currentChat.ID, items), m.syncWindowTitleCmd())
}

func (m *Model) dequeuePromptCmd() ui.Cmd {
	if m.loading {
		return nil
	}
	if len(m.approvals) > 0 {
		return nil
	}
	idx := m.nextDispatchableQueuedInputIndex(false)
	if idx < 0 {
		return nil
	}
	items := cloneQueuedInputs(m.currentChat.QueuedInputs)
	item := items[idx]
	items = append(items[:idx], items[idx+1:]...)
	m.setQueuedInputs(items)
	if ok, status := m.canSendPrompt(); !ok {
		if item.Kind != domain.QueuedInputKindContinue {
			if m.shouldOpenConnectDialogForSendFailure() {
				m.openConnectDialog()
			}
			m.status = status
			m.draftAttachments = queuedAttachmentDrafts(item.Attachments)
			m.draftReferences = queuedReferenceDrafts(item.References)
			m.setComposerDraftValue(item.Text)
			return nil
		}
	}
	m.startBusy(busyScopeTranscript, queuedInputRunStatus(item.Kind))
	if item.Kind != domain.QueuedInputKindContinue {
		m.appendLocalUserPrompt(item.Text, queuedAttachmentDrafts(item.Attachments), queuedReferenceDrafts(item.References))
	}
	return m.saveAndDispatchQueuedInputCmd(m.currentChat.ID, items, item)
}

func (m Model) ensureRuntimeContextWindow(ctx context.Context, session domain.Session) (string, int, bool, error) {
	start := time.Now()
	providerID := strings.TrimSpace(session.ProviderID)
	providerCfg, ok := m.cfg.Provider(providerID)
	if !ok {
		return "", 0, false, fmt.Errorf("provider %q not configured", providerID)
	}
	modelID := strings.TrimSpace(session.ModelID)
	if modelID == "" {
		modelID = strings.TrimSpace(providerCfg.DefaultModel)
	}
	recordTiming := func(result string, contextWindow int, checked bool) {
		if m.debug == nil {
			return
		}
		meta := map[string]string{
			"provider_id": providerID,
			"model_id":    modelID,
			"result":      result,
			"checked":     strconv.FormatBool(checked),
			"duration_ms": strconv.FormatInt(time.Since(start).Milliseconds(), 10),
		}
		if contextWindow > 0 {
			meta["context_window"] = strconv.Itoa(contextWindow)
		}
		m.debug.RecordLifecycle(session.ID, "context_window_timing", "ensure_runtime_context_window", meta)
	}
	if !provider.SupportsContextWindowDetection(providerCfg) {
		recordTiming("unsupported", providerCfg.ContextWindow, false)
		return "", 0, false, nil
	}
	if m.runtimeCtxChecked != nil && m.runtimeCtxChecked[providerID] {
		recordTiming("cached", providerCfg.ContextWindow, false)
		return providerID, providerCfg.ContextWindow, false, nil
	}
	contextWindow, err := provider.DetectContextWindow(ctx, providerID, providerCfg, modelID, m.debug)
	if err != nil {
		recordTiming("error:"+err.Error(), 0, false)
		return providerID, 0, false, err
	}
	if contextWindow > 0 && providerCfg.ContextWindow != contextWindow {
		providerCfg.ContextWindow = contextWindow
		m.cfg.Providers[providerID] = providerCfg
		if err := m.cfg.Save(); err != nil {
			recordTiming("save_error:"+err.Error(), 0, false)
			return providerID, 0, false, err
		}
		if m.agent != nil {
			m.agent.UpdateConfig(m.cfg)
		}
	}
	recordTiming("detected", contextWindow, true)
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
	m.composerQueries = composerQueryState{}
	m.updateComposerMenus()
	m.invalidateFooterCache()
}

func (m *Model) setComposerValue(value string) {
	m.composer.SetValue(value)
	m.composer.SetCursor(len(value))
	m.composerQueries = composerQueryState{}
	m.updateComposerMenus()
	m.invalidateFooterCache()
}

func (m *Model) setComposerDraftValue(value string) {
	numberDraftAttachmentTokens(m.draftAttachments)
	m.setComposerValue(m.decorateComposerTextWithAttachments(value, m.draftAttachments))
	m.hydrateComposerTokens()
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
		events, err := m.agent.CompactChat(ctx, m.currentSession.ID, m.currentChat.ID)
		return promptDoneMsg{events: events, err: err}
	}
}

func (m Model) approveCmd(ctx context.Context, approvalID int64) ui.Cmd {
	return func() ui.Msg {
		events, err := m.agent.ApproveInChat(ctx, m.currentSession.ID, m.currentChat.ID, approvalID)
		return promptDoneMsg{events: events, err: err}
	}
}

func (m Model) approveWithRuleCmd(ctx context.Context, approvalID int64, rule domain.PermissionOverride) ui.Cmd {
	return func() ui.Msg {
		events, err := m.agent.ApproveInChatWithRule(ctx, m.currentSession.ID, m.currentChat.ID, approvalID, rule)
		return promptDoneMsg{events: events, err: err}
	}
}

func (m Model) denyCmd(ctx context.Context, approvalID int64) ui.Cmd {
	return func() ui.Msg {
		events, err := m.agent.DenyInChat(ctx, m.currentSession.ID, m.currentChat.ID, approvalID)
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
		ChatID:    m.currentChat.ID,
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
		raw, err := reference.EncodeMeta(reference.Metadata(ref))
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
		ChatID:    m.currentChat.ID,
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
		m.insertDraftAttachment(draft)
		m.status = m.imageAttachmentStatus(draft)
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
		m.insertDraftAttachment(draft)
		if attachment.ClassifyMIME(draft.MIME) == attachment.KindImage {
			m.status = m.imageAttachmentStatus(draft)
		} else {
			m.status = fmt.Sprintf("Attached %s", draft.Name)
		}
		return m, m.syncWindowTitleCmd()
	}
	m.composer.InsertString(text)
	m.updateComposerMenus()
	m.invalidateFooterCache()
	m.status = "Pasted from clipboard"
	return m, m.syncWindowTitleCmd()
}

func (m *Model) imageAttachmentStatus(draft attachment.Draft) string {
	session := m.draftSession()
	if strings.TrimSpace(session.ProviderID) == "" || strings.TrimSpace(session.ModelID) == "" {
		return fmt.Sprintf("Attached image %s", draft.Name)
	}
	supported, err := m.capabilityStore().SupportsAttachment(session.ProviderID, providerCfgForDraft(m.cfg, session.ProviderID), session.ModelID, attachment.KindImage)
	if err != nil {
		return fmt.Sprintf("Attached image %s", draft.Name)
	}
	if !supported {
		return fmt.Sprintf("Attached image %s; warning: %s may not support image inputs", draft.Name, session.ModelID)
	}
	return fmt.Sprintf("Attached image %s", draft.Name)
}

func (m *Model) poppedLastDraftAttachment() bool {
	if len(m.draftAttachments) == 0 {
		return false
	}
	last := m.draftAttachments[len(m.draftAttachments)-1]
	m.draftAttachments = m.draftAttachments[:len(m.draftAttachments)-1]
	m.removeAttachmentPlaceholder(last)
	m.status = fmt.Sprintf("Removed attachment %s", last.Name)
	return true
}

func (m *Model) removeDraftAttachmentForComposerKey(msg ui.KeyMsg) bool {
	if len(m.draftAttachments) == 0 {
		return false
	}
	switch msg.Type {
	case ui.KeyBackspace:
		if m.composer.CursorIndex() == 0 {
			return m.poppedLastDraftAttachment()
		}
	case ui.KeyDelete:
		if m.composer.CursorIndex() >= m.composer.RuneCount() {
			return m.poppedLastDraftAttachment()
		}
	}
	return false
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

func (m *Model) hydrateComposerTokens() {
	value := m.composer.Value()
	for _, draft := range m.draftAttachments {
		token := draftAttachmentToken(draft)
		if token == "" {
			continue
		}
		if idx := strings.Index(value, token); idx >= 0 {
			end := idx + len(token)
			if end < len(value) && value[end] == ' ' {
				end++
			}
			m.composer.RegisterToken(idx, end)
		}
	}
	for _, draft := range m.draftReferences {
		if strings.TrimSpace(draft.Display) == "" {
			continue
		}
		start := draft.Start
		if start < 0 || start+len(draft.Display) > len(value) || value[start:start+len(draft.Display)] != draft.Display {
			start = strings.Index(value, draft.Display)
			if start < 0 {
				continue
			}
		}
		m.composer.RegisterToken(start, start+len(draft.Display))
	}
}

func (m *Model) syncDraftAttachmentsFromComposer() {
	if len(m.draftAttachments) == 0 {
		return
	}
	remaining := m.composer.Value()
	synced := make([]attachment.Draft, 0, len(m.draftAttachments))
	for _, draft := range m.draftAttachments {
		placeholder := draftAttachmentToken(draft)
		idx := strings.Index(remaining, placeholder)
		if idx < 0 {
			continue
		}
		synced = append(synced, draft)
		remaining = remaining[:idx] + remaining[idx+len(placeholder):]
	}
	m.draftAttachments = synced
}

func (m *Model) insertDraftAttachment(draft attachment.Draft) {
	m.draftAttachments = append(m.draftAttachments, draft)
	numberDraftAttachmentTokens(m.draftAttachments)
	draft = m.draftAttachments[len(m.draftAttachments)-1]
	insert := draftAttachmentToken(draft) + " "
	if cursor := m.composer.CursorIndex(); cursor > 0 {
		if r, ok := m.composer.RuneAt(cursor - 1); ok && r != ' ' && r != '\n' {
			insert = " " + insert
		}
	}
	m.composer.InsertToken(insert)
	m.updateComposerMenus()
	m.invalidateFooterCache()
}

func (m *Model) removeAttachmentPlaceholder(draft attachment.Draft) {
	value := m.composer.Value()
	placeholder := draftAttachmentToken(draft)
	idx := strings.LastIndex(value, placeholder)
	if idx < 0 {
		return
	}
	end := idx + len(placeholder)
	if end < len(value) && value[end] == ' ' {
		end++
	} else if idx > 0 && value[idx-1] == ' ' {
		idx--
	}
	next := value[:idx] + value[end:]
	m.composer.SetValue(next)
	m.composer.SetCursor(min(idx, len(next)))
	m.hydrateComposerTokens()
	m.updateComposerMenus()
	m.invalidateFooterCache()
}

func (m Model) decorateComposerTextWithAttachments(text string, drafts []attachment.Draft) string {
	if len(drafts) == 0 {
		return text
	}
	prefixes := make([]string, 0, len(drafts))
	for _, draft := range drafts {
		prefixes = append(prefixes, draftAttachmentToken(draft))
	}
	prefix := strings.Join(prefixes, " ")
	if strings.TrimSpace(text) == "" {
		return prefix + " "
	}
	return prefix + " " + text
}

func (m Model) submissionPromptText() string {
	return stripAttachmentPlaceholders(m.composer.Value(), m.draftAttachments)
}

func stripAttachmentPlaceholders(value string, drafts []attachment.Draft) string {
	trimmed := value
	for _, draft := range drafts {
		trimmed = strings.Replace(trimmed, draftAttachmentToken(draft), "", 1)
	}
	for {
		next := strings.ReplaceAll(trimmed, "  ", " ")
		next = strings.ReplaceAll(next, " \n", "\n")
		next = strings.ReplaceAll(next, "\n ", "\n")
		if next == trimmed {
			break
		}
		trimmed = next
	}
	return strings.TrimSpace(trimmed)
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
	renderer := newTranscriptRenderer(&m)
	for i := len(m.messages) - 1; i >= 0; i-- {
		msg := m.messages[i]
		if msg.Role != domain.MessageRoleAssistant {
			continue
		}
		body := strings.TrimSpace(renderer.renderMessageParts(m.parts[msg.ID]))
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

func (m *Model) currentChatID() int64 {
	if m.currentChat.ID > 0 {
		return m.currentChat.ID
	}
	return 0
}

func (m *Model) setQueuedInputs(items []domain.QueuedInput) {
	m.currentChat.QueuedInputs = cloneQueuedInputs(items)
	for idx := range m.chats {
		if m.chats[idx].ID == m.currentChat.ID {
			m.chats[idx].QueuedInputs = cloneQueuedInputs(items)
			break
		}
	}
	m.clampQueueSelection()
	m.invalidateFooterCache()
}

func (m *Model) clampQueueSelection() {
	count := len(m.currentChat.QueuedInputs)
	if count == 0 {
		m.queueSelection = 0
		m.queueEditMode = false
		return
	}
	if m.queueSelection < 0 {
		m.queueSelection = 0
	}
	if m.queueSelection >= count {
		m.queueSelection = count - 1
	}
}

func (m *Model) selectedQueuedInputIndex() int {
	m.clampQueueSelection()
	if len(m.currentChat.QueuedInputs) == 0 {
		return -1
	}
	return m.queueSelection
}

func (m *Model) moveQueueSelection(delta int) {
	if len(m.currentChat.QueuedInputs) == 0 {
		return
	}
	m.queueSelection = (m.queueSelection + delta + len(m.currentChat.QueuedInputs)) % len(m.currentChat.QueuedInputs)
	m.invalidateFooterCache()
}

func (m Model) saveQueuedInputsCmd(chatID int64, items []domain.QueuedInput) ui.Cmd {
	if chatID == 0 {
		return nil
	}
	cloned := cloneQueuedInputs(items)
	return func() ui.Msg {
		ctx := context.Background()
		err := m.store.SetChatQueuedInputs(ctx, chatID, cloned)
		return queuePersistMsg{chatID: chatID, items: cloned, err: err}
	}
}

func (m Model) saveAndDispatchQueuedInputCmd(chatID int64, items []domain.QueuedInput, item domain.QueuedInput) ui.Cmd {
	clonedItems := cloneQueuedInputs(items)
	clonedItem := item
	clonedItem.Attachments = append([]domain.QueuedAttachment(nil), item.Attachments...)
	clonedItem.References = append([]domain.QueuedReference(nil), item.References...)
	return func() ui.Msg {
		ctx := context.Background()
		if chatID > 0 {
			if err := m.store.SetChatQueuedInputs(ctx, chatID, clonedItems); err != nil {
				return queuePersistMsg{chatID: chatID, items: clonedItems, err: err}
			}
		}
		if clonedItem.Kind == domain.QueuedInputKindContinue {
			return queuedContinueDispatchMsg{}
		}
		return kickoffPromptMsg{
			Prompt:      clonedItem.Text,
			Attachments: queuedAttachmentDrafts(clonedItem.Attachments),
			References:  queuedReferenceDrafts(clonedItem.References),
		}
	}
}

func (m *Model) reorderSelectedQueuedInput(delta int) ui.Cmd {
	items := cloneQueuedInputs(m.currentChat.QueuedInputs)
	idx := m.selectedQueuedInputIndex()
	if idx < 0 {
		return nil
	}
	next := idx + delta
	if next < 0 || next >= len(items) {
		return nil
	}
	items[idx], items[next] = items[next], items[idx]
	m.setQueuedInputs(items)
	m.queueSelection = next
	m.status = "Reordered queue item"
	return ui.Batch(m.saveQueuedInputsCmd(m.currentChat.ID, items), m.syncWindowTitleCmd())
}

func (m *Model) toggleSelectedQueuedInputHeld() ui.Cmd {
	items := cloneQueuedInputs(m.currentChat.QueuedInputs)
	idx := m.selectedQueuedInputIndex()
	if idx < 0 {
		return nil
	}
	items[idx].Held = !items[idx].Held
	m.setQueuedInputs(items)
	if items[idx].Held {
		m.status = "Held queue item"
	} else {
		m.status = "Unheld queue item"
	}
	return ui.Batch(m.saveQueuedInputsCmd(m.currentChat.ID, items), m.syncWindowTitleCmd())
}

func (m *Model) deleteSelectedQueuedInput() ui.Cmd {
	items := cloneQueuedInputs(m.currentChat.QueuedInputs)
	idx := m.selectedQueuedInputIndex()
	if idx < 0 {
		return nil
	}
	items = append(items[:idx], items[idx+1:]...)
	m.setQueuedInputs(items)
	m.status = "Deleted queue item"
	return ui.Batch(m.saveQueuedInputsCmd(m.currentChat.ID, items), m.syncWindowTitleCmd())
}

func (m *Model) nextDispatchableQueuedInputIndex(activeTurn bool) int {
	priority := []domain.QueuedInputKind{
		domain.QueuedInputKindSteer,
		domain.QueuedInputKindRejectedSteer,
		domain.QueuedInputKindContinue,
		domain.QueuedInputKindQueued,
	}
	for _, kind := range priority {
		if activeTurn && kind != domain.QueuedInputKindSteer {
			continue
		}
		for idx, item := range m.currentChat.QueuedInputs {
			if item.Held || item.Kind != kind {
				continue
			}
			return idx
		}
	}
	return -1
}

func (m *Model) syncCurrentChatBusy() {
	chatID := m.currentChatID()
	if chatID == 0 {
		return
	}
	if m.chatBusy == nil {
		m.chatBusy = map[int64]busyModel{}
	}
	m.busy = m.chatBusy[chatID]
	if m.activeOpCancels != nil {
		m.activeOpCancel = m.activeOpCancels[chatID]
	} else {
		m.activeOpCancel = nil
	}
}

func (m *Model) startBusy(scope busyScope, status string) {
	m.loading = true
	m.status = status
	m.busy.start(scope, status)
	if chatID := m.currentChatID(); chatID > 0 {
		if m.chatBusy == nil {
			m.chatBusy = map[int64]busyModel{}
		}
		m.chatBusy[chatID] = m.busy
	}
	if m.width <= 0 || m.height <= 0 {
		m.invalidateMainSurface()
		return
	}
	m.resize()
	m.invalidateMainSurface()
}

func (m *Model) stopBusy() {
	m.loading = false
	m.busy.stop()
	if chatID := m.currentChatID(); chatID > 0 && m.chatBusy != nil {
		m.chatBusy[chatID] = m.busy
	}
	m.activeOpCancel = nil
	if chatID := m.currentChatID(); chatID > 0 && m.activeOpCancels != nil {
		delete(m.activeOpCancels, chatID)
	}
	m.interruptArmedAt = time.Time{}
	if m.width <= 0 || m.height <= 0 {
		m.invalidateMainSurface()
		return
	}
	m.resize()
	m.refreshViewportPreserve()
}

func (m *Model) stopBusyWithStatus(status string) {
	m.stopBusy()
	m.status = status
}

func (m *Model) syncDebugRuntime() {
	if m.debug == nil {
		return
	}
	deepDebug := m.debug.DeepDebug()
	var transcriptItems []debugsrv.TranscriptItemRef
	if deepDebug {
		transcriptItems = m.debugTranscriptItems()
	}
	renderBlockCount := len(m.transcriptItems)
	if deepDebug {
		renderBlockCount = len(transcriptItems)
	}
	hashValues := []string{
		strconv.FormatBool(deepDebug),
		strconv.FormatInt(m.currentSession.ID, 10),
		strings.TrimSpace(m.currentSession.Title),
		strings.TrimSpace(m.currentSession.ProviderID),
		strings.TrimSpace(m.currentSession.ModelID),
		strings.TrimSpace(m.status),
		strconv.FormatBool(m.busy.active),
		strings.TrimSpace(m.busy.status),
		m.openDialogName(),
		strconv.FormatBool(m.showSidebar),
		strconv.FormatBool(m.showReasoning),
		strconv.FormatBool(m.showSystem),
		m.currentError(),
		strconv.Itoa(m.viewport.Width),
		strconv.Itoa(m.viewport.Height),
		strconv.Itoa(m.viewport.YOffset),
		strconv.Itoa(len(m.messages)),
		strconv.Itoa(renderBlockCount),
		strconv.Itoa(m.viewport.VisibleSurface().SurfaceHeight()),
	}
	if deepDebug {
		for _, item := range transcriptItems {
			hashValues = append(hashValues,
				strconv.Itoa(item.Index),
				item.Key,
				item.Kind,
				strconv.Itoa(item.GapBefore),
				strconv.Itoa(item.Height),
				strconv.Itoa(item.BlankRows),
				strconv.FormatInt(item.MessageID, 10),
				item.Role,
				item.Summary,
				string(item.Tool),
				item.ToolRunID,
				item.Title,
				item.ControlID,
			)
		}
	}
	runtimeHash := hashStrings(hashValues...)
	if runtimeHash == m.debugRuntimeHash {
		return
	}
	m.debug.UpdateRuntime(debugsrv.RuntimeSnapshot{
		DebugAPI:           m.debugAPIAddr(),
		Build:              version.Current(),
		DeepDebug:          deepDebug,
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
		RenderBlockCount:   renderBlockCount,
		ViewportPreview:    "",
		ViewportContentLen: m.viewport.VisibleSurface().SurfaceHeight(),
		TranscriptItems:    transcriptItems,
	})
	m.debugRuntimeHash = runtimeHash
}

func (m *Model) syncDebugFrame(surface ui.Surface) {
	if m.debug == nil || !m.debug.DeepDebug() {
		return
	}
	if !m.debugFrameLastSync.IsZero() && time.Since(m.debugFrameLastSync) < 250*time.Millisecond {
		return
	}
	snapshot := m.debug.Runtime()
	lines := surface.Lines()
	controls := make([]debugsrv.ControlRef, 0, len(m.transcriptControls))
	for _, control := range m.transcriptControls {
		controls = append(controls, debugsrv.ControlRef{
			ID:      control.ID,
			X:       control.Rect.X,
			Y:       control.Rect.Y,
			W:       control.Rect.W,
			H:       control.Rect.H,
			Enabled: control.Enabled,
		})
	}
	snapshot.FrameLines = lines
	snapshot.TranscriptControls = controls
	m.debug.UpdateRuntime(snapshot)
	m.debugFrameLastSync = time.Now()
}

func (m Model) debugTranscriptItems() []debugsrv.TranscriptItemRef {
	retained := m.syncRetainedTranscript()
	if retained == nil {
		return nil
	}
	items := retained.Items()
	ctx := &ui.Context{Palette: m.palette}
	width := max(0, m.viewport.Width)
	out := make([]debugsrv.TranscriptItemRef, 0, len(items))
	for idx, item := range items {
		ref := debugsrv.TranscriptItemRef{
			Index:     idx,
			Key:       item.Key,
			GapBefore: item.GapBefore,
		}
		if idx < len(m.transcriptItems) {
			switch typed := m.transcriptItems[idx].(type) {
			case *userMessageTranscriptItem:
				ref.Kind = "message"
				ref.MessageID = typed.message.ID
				ref.Role = string(typed.message.Role)
				ref.Summary = strings.TrimSpace(typed.message.Summary)
			case *assistantMessageTranscriptItem:
				ref.Kind = "message"
				ref.MessageID = typed.message.ID
				ref.Role = string(typed.message.Role)
				ref.Summary = strings.TrimSpace(typed.message.Summary)
			case *pendingAssistantTranscriptItem:
				ref.Kind = "message"
				ref.Role = string(domain.MessageRoleAssistant)
				ref.Summary = strings.TrimSpace(typed.text)
			case toolRunTranscriptItem:
				ref.Kind = "toolrun"
				ref.ToolRunID = typed.RunID()
				switch concrete := typed.(type) {
				case *bashToolRunTranscriptItem:
					ref.Tool = concrete.run.Tool
					ref.Title = strings.TrimSpace(concrete.run.Title)
				case *readToolRunTranscriptItem:
					ref.Tool = concrete.run.Tool
					ref.Title = strings.TrimSpace(concrete.run.Title)
				case *writeToolRunTranscriptItem:
					ref.Tool = concrete.run.Tool
					ref.Title = strings.TrimSpace(concrete.run.Title)
				case *editToolRunTranscriptItem:
					ref.Tool = concrete.run.Tool
					ref.Title = strings.TrimSpace(concrete.run.Title)
				case *genericToolRunTranscriptItem:
					ref.Tool = concrete.run.Tool
					ref.Title = strings.TrimSpace(concrete.run.Title)
				}
				ref.ControlID = "toolrun:" + ref.ToolRunID
			}
		}
		if item.Node != nil {
			size := item.Node.Measure(ctx, ui.NewConstraints(width, 0))
			ref.Height = size.H
			surface := ui.PaintNodeSurface(ctx, item.Node, ui.Rect{W: width, H: size.H})
			ref.BlankRows = countBlankSurfaceRows(surface)
		}
		out = append(out, ref)
	}
	return out
}

func countBlankSurfaceRows(surface ui.Surface) int {
	lines := surface.Lines()
	blank := 0
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			blank++
		}
	}
	return blank
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
	case m.hasMCPDialog():
		return "mcp"
	case m.hasSessionDialog():
		return "session"
	case m.hasPreferencesDialog():
		return "preferences"
	case m.hasThemeDialog():
		return "theme"
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

func (m Model) recordEvent(chatID int64, evt domain.Event) {
	if m.debug == nil {
		return
	}
	sessionID := m.currentSession.ID
	if chatID > 0 {
		for _, item := range m.chats {
			if item.ID == chatID {
				sessionID = item.SessionID
				break
			}
		}
	}
	m.debug.RecordEvent(sessionID, evt)
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
	return m.busy.spinner.active || m.hasPreferencesDialog()
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

func (m Model) shouldOpenConnectDialogForSendFailure() bool {
	session := m.draftSession()
	if strings.TrimSpace(session.ProviderID) == "" {
		return true
	}
	if !m.cfg.HasUsableProvider(session.ProviderID) {
		return true
	}
	if strings.TrimSpace(session.ModelID) == "" {
		return true
	}
	return false
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
	profile := m.permissionProfile()
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
	m.syncDraftAttachmentsFromComposer()
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
	token := "$" + selected.Name
	m.composer.ReplaceRangeWithToken(queries.skillStart, m.composer.CursorIndex(), token)
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
	display := reference.DisplayToken(selected.Path)
	m.composer.ReplaceRangeWithToken(queries.mentionStart, queries.mentionEnd, display)
	m.draftReferences = append(m.draftReferences, reference.Draft{
		Kind:    selected.Kind,
		Path:    selected.Path,
		Display: display,
	})
	m.updateComposerMenus()
	m.status = fmt.Sprintf("Inserted %s", display)
}

func (m *Model) renderSlashMenuElement() ui.Node {
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
	return ui.AsNode(ui.SlashMenu{Title: "Commands", Items: items, Selected: selected})
}

func (m *Model) renderSkillMenuElement() ui.Node {
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
	return ui.AsNode(ui.SlashMenu{Title: "Skills", Items: items, Selected: selected})
}

func (m *Model) renderMentionMenuElement() ui.Node {
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
	return ui.AsNode(ui.SlashMenu{Title: "References", Items: items, Selected: selected})
}

func (m *Model) renderComposerHistoryMenuElement() ui.Node {
	if !m.hasComposerHistoryMenu() {
		return nil
	}
	matches := m.filteredComposerHistory(m.composerHistory.SearchQuery)
	width := max(48, min(88, m.composerWidth()))
	var items []ui.MenuItem
	if len(matches) == 0 {
		return ui.AsNode(ui.HistoryMenu{
			Palette: m.palette,
			Query:   m.composerHistory.SearchQuery,
			Width:   width,
		})
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
		return ui.AsNode(ui.HistoryMenu{
			Palette:  m.palette,
			Query:    m.composerHistory.SearchQuery,
			Items:    items,
			Selected: m.composerHistory.SearchIndex - start,
			Width:    width,
		})
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

func (m *Model) renderPickerElement() ui.Node {
	if !m.hasPicker() {
		return nil
	}
	return ui.AsNode(m.picker.dialog)
}

func (m *Model) renderThemeDialogElement() ui.Node {
	if !m.hasThemeDialog() {
		return nil
	}
	return ui.AsNode(m.themeDialog)
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

func (m *Model) renderSessionDialogElement() ui.Node {
	if !m.hasSessionDialog() {
		return nil
	}
	return ui.AsNode(m.sessionDialog)
}

func (m *Model) renderPreferencesDialogElement() ui.Node {
	if !m.hasPreferencesDialog() {
		return nil
	}
	return ui.AsNode(m.preferences)
}

func (m *Model) renderToolsDialogElement() ui.Node {
	if !m.hasToolsDialog() {
		return nil
	}
	return ui.AsNode(m.toolsDialog)
}

func (m *Model) renderConnectDialogElement() ui.Node {
	if !m.hasConnectDialog() {
		return nil
	}
	return ui.AsNode(m.connectDialog)
}

func (m *Model) renderMCPDialogElement() ui.Node {
	if !m.hasMCPDialog() {
		return nil
	}
	return m.mcpDialog.ListNode(max(0, m.width), m.palette)
}

func (m *Model) renderMCPEditDialogElement() ui.Node {
	if !m.hasMCPEditDialog() {
		return nil
	}
	return m.mcpDialog.EditorNode(max(0, m.width), m.palette)
}

func (m *Model) renderDisconnectDialogElement() ui.Node {
	if !m.hasDisconnectDialog() {
		return nil
	}
	return ui.AsNode(m.disconnectDialog)
}

func (m *Model) renderModelDialogElement() ui.Node {
	if !m.hasModelDialog() {
		return nil
	}
	return ui.AsNode(m.modelDialog)
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
		cmd, err := m.applyPreferences(action.Values, false)
		if err != nil {
			m.status = fmt.Sprintf("preferences preview failed: %v", err)
			return m.syncWindowTitleCmd()
		}
		return ui.Batch(cmd, m.syncWindowTitleCmd())
	case dialogs.PreferencesActionApply:
		cmd, err := m.applyPreferences(action.Values, true)
		if err != nil {
			m.status = fmt.Sprintf("preferences save failed: %v", err)
			return m.syncWindowTitleCmd()
		}
		m.closePreferencesDialog()
		m.status = "Preferences saved"
		return ui.Batch(cmd, m.syncWindowTitleCmd())
	case dialogs.PreferencesActionCancel:
		cmd, err := m.applyPreferences(action.Values, false)
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
		cfg, _ := m.cfg.Provider(action.Draft.ProviderID)
		return ui.Batch(
			m.loadModelsCmd(action.Draft.ProviderID, true),
			m.detectContextWindowCmd(action.Draft.ProviderID, cfg, action.Draft.Model),
			m.syncWindowTitleCmd(),
		)
	case dialogs.ProviderConnectActionCancel:
		m.closeConnectDialog()
		m.status = "Provider connect cancelled"
		return m.syncWindowTitleCmd()
	default:
		return nil
	}
}

func (m *Model) handleMCPDialogKey(msg ui.KeyMsg) ui.Cmd {
	if !m.hasMCPDialog() {
		return nil
	}
	return m.applyMCPDialogAction(m.mcpDialog.UpdateList(msg), false)
}

func (m *Model) handleMCPEditDialogKey(msg ui.KeyMsg) ui.Cmd {
	if !m.hasMCPEditDialog() {
		return nil
	}
	return m.applyMCPDialogAction(m.mcpDialog.UpdateEditor(msg), true)
}

func (m *Model) applyMCPDialogAction(action dialogs.MCPDialogAction, fromEditor bool) ui.Cmd {
	switch action.Kind {
	case dialogs.MCPDialogActionSave:
		if err := kodermcp.ValidateServerConfig(action.ServerID, action.Config); err != nil {
			m.mcpDialog.SetStatus("Save failed: " + err.Error())
			m.status = err.Error()
			return m.syncWindowTitleCmd()
		}
		oldID := m.mcpDialogEditID()
		if m.cfg.MCPServers == nil {
			m.cfg.MCPServers = map[string]config.MCPServer{}
		}
		if oldID != "" && oldID != action.ServerID {
			delete(m.cfg.MCPServers, oldID)
		}
		m.cfg.MCPServers[action.ServerID] = action.Config
		if err := m.cfg.Save(); err != nil {
			m.mcpDialog.SetStatus("Save failed: " + err.Error())
			m.status = err.Error()
			return m.syncWindowTitleCmd()
		}
		m.agent.UpdateConfig(m.cfg)
		if fromEditor {
			m.mcpDialog.CloseEditor()
		}
		m.status = fmt.Sprintf("Saved MCP server %s", action.ServerID)
		return ui.Batch(m.mcpReloadCmd(), m.syncWindowTitleCmd())
	case dialogs.MCPDialogActionRemove:
		if id := strings.TrimSpace(action.ServerID); id != "" {
			delete(m.cfg.MCPServers, id)
			if err := m.cfg.Save(); err != nil {
				m.mcpDialog.SetStatus("Remove failed: " + err.Error())
				m.status = err.Error()
				return m.syncWindowTitleCmd()
			}
			m.agent.UpdateConfig(m.cfg)
			if fromEditor {
				m.mcpDialog.CloseEditor()
			}
			m.status = fmt.Sprintf("Removed MCP server %s", id)
			return ui.Batch(m.mcpReloadCmd(), m.syncWindowTitleCmd())
		}
	case dialogs.MCPDialogActionReconnect:
		m.status = fmt.Sprintf("Reloading MCP server %s…", action.ServerID)
		return ui.Batch(m.mcpReloadCmd(), m.syncWindowTitleCmd())
	case dialogs.MCPDialogActionCancel:
		if fromEditor {
			m.mcpDialog.CloseEditor()
			m.status = "MCP editor closed"
			return m.syncWindowTitleCmd()
		}
		m.closeMCPDialog()
		m.status = "MCP dialog closed"
		return m.syncWindowTitleCmd()
	}
	return nil
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
		if err := m.selectModel(action.ProviderID, action.ModelID, action.PresetID); err != nil {
			m.status = err.Error()
			return m.syncWindowTitleCmd()
		}
		m.closeModelDialog()
		m.status = fmt.Sprintf("Selected %s / %s", action.ProviderID, action.ModelID)
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

func (m *Model) hasApprovalDialog() bool {
	return m.hasApprovalPrompt()
}

func (m *Model) ensureApprovalDialog() {
	if !m.hasApprovalDialog() {
		m.approvalDialog = nil
		return
	}
	item := m.approvals[0]
	run := m.approvalToolRun(item)
	index := 0
	if m.approvalDialog != nil {
		index = m.approvalDialog.ButtonIndex()
	}
	m.approvalDialog = dialogs.NewApprovalDialog(run, approvalToolScopeLabel(item.Tool), approvalPatternScopeLabel(item.Tool, run.PreviewText()))
	m.approvalDialog.SetButtonIndex(index)
}

func (m *Model) renderApprovalDialogElement() ui.Node {
	if !m.hasApprovalDialog() {
		return nil
	}
	m.ensureApprovalDialog()
	return m.approvalDialog.Element(m.palette, ui.Rect{W: max(0, m.width), H: max(0, m.height)})
}

func (m *Model) renderApprovalPrompt() string {
	if element := m.renderApprovalDialogElement(); element != nil {
		return m.renderElementText(element, 0, 0)
	}
	return ""
}

func (m *Model) handleApprovalDialogAction(action dialogs.ApprovalDialogAction) ui.Cmd {
	if !m.hasApprovalDialog() || action.Kind == dialogs.ApprovalDialogActionNone {
		return nil
	}
	item := m.approvals[0]
	switch action.Kind {
	case dialogs.ApprovalDialogActionApproveOnce:
		m.startBusy(busyScopeTranscript, fmt.Sprintf("Approving approval %d…", item.ID))
		return ui.Batch(m.approveCmd(m.beginActiveOperation(), item.ID), m.spinnerCmdIfNeeded())
	case dialogs.ApprovalDialogActionApproveAllTool:
		m.startBusy(busyScopeTranscript, fmt.Sprintf("Approving all %s commands…", approvalToolScopeLabel(item.Tool)))
		return ui.Batch(m.approveWithRuleCmd(m.beginActiveOperation(), item.ID, domain.PermissionOverride{
			Tool:    item.Tool,
			Pattern: "*",
			Action:  domain.PermissionModeAllow,
		}), m.spinnerCmdIfNeeded())
	case dialogs.ApprovalDialogActionApproveMatching:
		m.startBusy(busyScopeTranscript, fmt.Sprintf("Approving matching %s commands…", approvalToolScopeLabel(item.Tool)))
		return ui.Batch(m.approveWithRuleCmd(m.beginActiveOperation(), item.ID, domain.PermissionOverride{
			Tool:    item.Tool,
			Pattern: approvalPatternScope(item.Tool, m.approvalToolRun(item).PreviewText()),
			Action:  domain.PermissionModeAllow,
		}), m.spinnerCmdIfNeeded())
	case dialogs.ApprovalDialogActionDeny:
		m.startBusy(busyScopeSidebar, fmt.Sprintf("Denying approval %d…", item.ID))
		return ui.Batch(m.denyCmd(m.beginActiveOperation(), item.ID), m.spinnerCmdIfNeeded())
	case dialogs.ApprovalDialogActionPermissions:
		m.openApprovalPermissionsPicker()
		return m.syncWindowTitleCmd()
	default:
		return nil
	}
}

func internalSlashCommands() []slashCommand {
	return []slashCommand{
		{Name: "/agents", Description: "Show resolved project instructions"},
		{Name: "/agents refresh", Description: "Re-resolve project instructions"},
		{Name: "/chat new", Description: "Start a new chat in this session"},
		{Name: "/chat next", Description: "Switch to the next chat in this session"},
		{Name: "/chat prev", Description: "Switch to the previous chat in this session"},
		{Name: "/compact", Description: "Summarize old context"},
		{Name: "/connect", Description: "Configure a provider"},
		{Name: "/disconnect", Description: "Remove a configured provider"},
		{Name: "/fork", Description: "Branch from the current session"},
		{Name: "/mcp", Description: "Manage remote MCP servers"},
		{Name: "/mcp-reload", Description: "Reconnect configured MCP servers"},
		{Name: "/mcp-status", Description: "Show MCP server status"},
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

func approvalToolScopeLabel(tool domain.ToolKind) string {
	return strings.TrimSpace(string(tool))
}

func approvalPatternScope(tool domain.ToolKind, preview string) string {
	preview = strings.Join(strings.Fields(strings.TrimSpace(preview)), " ")
	if preview == "" {
		return "*"
	}
	switch tool {
	case domain.ToolKindBash, domain.ToolKindExecCommand:
		fields := strings.Fields(preview)
		if len(fields) == 0 {
			return "*"
		}
		if len(fields) == 1 {
			return fields[0]
		}
		return fields[0] + " *"
	default:
		return preview
	}
}

func approvalPatternScopeLabel(tool domain.ToolKind, preview string) string {
	pattern := approvalPatternScope(tool, preview)
	if strings.TrimSpace(pattern) == "" || pattern == "*" {
		return "matching"
	}
	return strings.TrimSpace(strings.TrimSuffix(pattern, " *"))
}

func (m *Model) permissionProfile() string {
	if strings.TrimSpace(m.currentChat.PermissionProfile) != "" {
		return m.currentChat.PermissionProfile
	}
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
	return fmt.Sprintf("%s +%d -%d", base, item.Additions, item.Deletions)
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
	composer.Cursor.TextStyle = ui.NewStyle().
		Background(palette.UserTextForeground).
		Foreground(palette.UserTextBackground)
}

func (m *Model) hasPicker() bool {
	return m.picker.visible
}

func (m *Model) hasThemeDialog() bool {
	return m.themeDialog != nil
}

func (m *Model) hasModalOverlay() bool {
	return m.hasModelDialog() ||
		m.hasDisconnectDialog() ||
		m.hasToolsDialog() ||
		m.hasConnectDialog() ||
		m.hasMCPDialog() ||
		m.hasThemeDialog() ||
		m.hasSessionDialog() ||
		m.hasAgentsModal() ||
		m.hasHelpModal() ||
		m.hasLLMPreview() ||
		m.hasPreferencesDialog() ||
		m.hasApprovalDialog() ||
		m.hasPicker()
}

func (m *Model) composerShouldBlink() bool {
	return m.composer.BlinkEnabled && !m.hasModalOverlay()
}

func (m *Model) syncComposerVisibility() {
	beforeFocus := m.composer.Focused()
	beforeCursorVisible := m.composer.CursorVisible()
	shouldFocus := !m.hasModalOverlay() && (beforeFocus || m.composer.BlinkEnabled || m.composerAreaHasContent())
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
		m.invalidateFooterCursor()
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
	return m.rebuildTranscriptState()
}

func (m *Model) withRootTimers(cmd ui.Cmd) ui.Cmd {
	m.syncComposerBlinkTimer()
	animationCmd := m.bouncyBalls.TickCmd()
	timerCmd := m.rootTimerCmd()
	if timerCmd == nil && animationCmd == nil {
		return cmd
	}
	if timerCmd == nil {
		return ui.Batch(cmd, animationCmd)
	}
	if animationCmd == nil {
		return ui.Batch(cmd, timerCmd)
	}
	return ui.Batch(cmd, timerCmd, animationCmd)
}

func (m *Model) closePicker() {
	m.picker = pickerModel{}
	m.syncComposerVisibility()
}

func (m *Model) closeThemeDialog() {
	m.themeDialog = nil
	m.themeDialogInitial = ""
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
	m.helpYOffset = 0
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

func (m *Model) hasMCPDialog() bool {
	return m.mcpDialog != nil
}

func (m *Model) hasMCPEditDialog() bool {
	return m.mcpDialog != nil && m.mcpDialog.HasEditor()
}

func (m *Model) closeMCPDialog() {
	m.mcpDialog = nil
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
	dialog := dialogs.NewPreferencesDialog(dialogs.PreferencesValues{
		UI:               m.cfg.UI,
		MaxToolLoopSteps: m.cfg.MaxToolLoopSteps,
	}, theme.Names(), markdown.CodeStyleNames())
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

func (m *Model) openMCPDialog() {
	dialog := dialogs.NewMCPDialog(m.agent.ListMCPServers(), m.cfg.MCPServers)
	m.mcpDialog = &dialog
	m.syncComposerVisibility()
}

func (m *Model) mcpReloadCmd() ui.Cmd {
	return func() ui.Msg {
		err := m.agent.ReloadMCP(context.Background())
		return mcpReloadMsg{
			servers: m.agent.ListMCPServers(),
			err:     err,
		}
	}
}

func (m *Model) mcpStatusSummary() string {
	servers := m.agent.ListMCPServers()
	if len(servers) == 0 {
		if len(m.cfg.MCPServers) == 0 {
			return "No MCP servers configured"
		}
		return "No MCP servers connected"
	}
	statuses := make([]string, 0, len(servers))
	for _, item := range servers {
		statuses = append(statuses, fmt.Sprintf("%s=%s", item.ID, item.Status))
	}
	return "MCP: " + strings.Join(statuses, ", ")
}

func (m *Model) mcpDialogEditID() string {
	if m.mcpDialog == nil {
		return ""
	}
	return m.mcpDialog.EditID()
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
	current := ""
	if m.currentSession.ProviderID == providerID || strings.TrimSpace(m.currentSession.ProviderID) == "" {
		current = m.currentSession.ModelID
	}
	if strings.TrimSpace(current) == "" {
		if providerCfg, ok := m.cfg.Provider(providerID); ok {
			current = providerCfg.DefaultModel
		} else {
			current = m.cfg.DefaultModel
		}
	}
	dialog := dialogs.NewModelDialog(providerID, models, current, m.providerModelPreset(providerID))
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
		BodyElement: ui.AsNode(ui.TextPane{Content: strings.Join(lines, "\n")}),
		Footer:      "enter or esc close  /agents refresh recomputes and updates the session snapshot",
		Width:       min(110, max(72, m.width-8)),
	}
	m.agentsModal = &modal
	m.syncComposerVisibility()
}

func (m *Model) renderAgentsModalElement() ui.Node {
	if m.agentsModal == nil {
		return nil
	}
	return ui.AsNode(*m.agentsModal)
}

func (m *Model) openHelpModal() {
	hotkeys := []string{
		"Hotkeys",
		"Alt-H               show or close help",
		"Alt-Q               toggle queue edit mode",
		"Ctrl-PgUp/PgDn      switch to previous or next chat",
		"Enter               send prompt or confirm selection",
		"Esc                 cancel dialog or interrupt active run",
		"Tab                 autocomplete, or queue steering while running",
		"Up/Down             browse session prompt history",
		"Alt-Enter           insert newline",
		"Ctrl-V              paste clipboard text or image",
		"Ctrl-Y              copy last assistant message",
		"Ctrl-R              search prompt history",
		"Ctrl-S              toggle sidebar",
		"Alt-[ / Alt-]      narrow or widen the sidebar",
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
		"/mcp                manage remote MCP servers",
		"/mcp-reload         reconnect MCP servers",
		"/mcp-status         show MCP status",
		"/model              choose a model",
		"/new                create a new session",
		"/permissions        change permission mode",
		"/preferences        open UI preferences",
		"/quit               exit koder",
		"/resume             resume another session",
		"/skills             insert a discovered skill mention",
		"/theme              preview and select theme",
	}
	queueEditing := []string{
		"Queue Edit Mode",
		"Alt-Q               enter or leave queue edit mode",
		"Up/Down             select queued item",
		"Enter               restore selected queued prompt to composer",
		"Alt-Up/Alt-Down     reorder selected queued item",
		"H                   hold or unhold selected queued item",
		"Backspace/Delete    delete selected queued item",
		"Esc                 leave queue edit mode",
	}
	lines := append([]string{}, hotkeys...)
	lines = append(lines, "")
	lines = append(lines, commands...)
	lines = append(lines, "")
	lines = append(lines, queueEditing...)
	m.helpBody = strings.Join(lines, "\n")
	m.helpYOffset = 0
	modal := ui.Modal{
		Title:  "Help",
		Footer: "Alt-H, Enter, or Esc closes  •  Use arrows, PgUp/PgDn, Home/End, or wheel to scroll",
		Width:  min(104, max(84, m.width-8)),
	}
	m.helpModal = &modal
	m.resizeHelpModal()
	m.syncComposerVisibility()
}

func (m *Model) renderHelpModalElement() ui.Node {
	if m.helpModal == nil {
		return nil
	}
	return ui.AsNode(ui.Modal{
		Title: m.helpModal.Title,
		BodyElement: ui.AsNode(ui.ScrollFrame{
			Child:   ui.AsNode(ui.TextPane{Content: m.helpBody}),
			OffsetY: m.helpYOffset,
			Width:   m.helpWidth,
			Height:  m.helpHeight,
		}),
		Footer: m.helpModal.Footer,
		Width:  m.helpModal.Width,
	})
}

func (m *Model) resizeHelpModal() {
	if !m.hasHelpModal() {
		return
	}
	width := min(104, max(84, m.width-8))
	height := min(24, max(8, m.height-8))
	m.helpWidth = max(40, width-6)
	m.helpHeight = height
	m.helpYOffset = min(max(0, m.helpYOffset), m.helpMaxOffset())
	if m.helpModal != nil {
		m.helpModal.Width = width
	}
}

func (m Model) previewLLMRequestCmd(ctx context.Context, prompt string, drafts []attachment.Draft, refs []reference.Draft) ui.Cmd {
	return func() ui.Msg {
		req, err := m.agent.PreviewNextRequestForChat(ctx, m.currentSession, m.currentChat, prompt, drafts, refs, m.pendingModelNote)
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

func (m *Model) renderLLMPreviewElement() ui.Node {
	if !m.hasLLMPreview() {
		return nil
	}
	title := strings.TrimSpace(m.llmPreviewTitle)
	if title == "" {
		title = "Next LLM Request"
	}
	return ui.AsNode(ui.Modal{
		Title: title,
		BodyElement: ui.AsNode(ui.ScrollFrame{
			Child:   ui.AsNode(ui.TextPane{Content: m.llmPreviewBody}),
			OffsetY: m.llmPreviewYOffset,
			Width:   m.llmPreviewWidth,
			Height:  m.llmPreviewHeight,
		}),
		Footer: "Alt-O, Enter, or Esc closes  •  Use arrows, PgUp/PgDn, Home/End, or wheel to scroll",
		Width:  max(40, m.width-4),
	})
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
	return m.agentsStatusLabel()
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

func (m Model) detectContextWindowCmd(providerID string, providerCfg config.Provider, modelID string) ui.Cmd {
	if !provider.SupportsContextWindowDetection(providerCfg) {
		return nil
	}
	if strings.TrimSpace(modelID) == "" {
		modelID = strings.TrimSpace(providerCfg.DefaultModel)
	}
	return func() ui.Msg {
		contextWindow, err := provider.DetectContextWindow(context.Background(), providerID, providerCfg, modelID, m.debug)
		if err != nil {
			return contextWindowMsg{providerID: providerID, err: err}
		}
		return contextWindowMsg{providerID: providerID, contextWindow: contextWindow, checked: true}
	}
}

func (m Model) detectSessionContextWindowCmd() ui.Cmd {
	providerID := strings.TrimSpace(m.currentSession.ProviderID)
	if providerID == "" {
		return nil
	}
	if m.runtimeCtxChecked != nil && m.runtimeCtxChecked[providerID] {
		return nil
	}
	providerCfg, ok := m.cfg.Provider(providerID)
	if !ok {
		return nil
	}
	modelID := strings.TrimSpace(m.currentSession.ModelID)
	if modelID == "" {
		modelID = strings.TrimSpace(providerCfg.DefaultModel)
	}
	return m.detectContextWindowCmd(providerID, providerCfg, modelID)
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
		if strings.TrimSpace(next.ModelPreset) == "" {
			next.ModelPreset = existing.ModelPreset
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
		if next.ContextWindow == 0 {
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

func (m *Model) selectModel(providerID string, modelID string, presetID string) error {
	providerID = strings.TrimSpace(providerID)
	modelID = strings.TrimSpace(modelID)
	presetID = provider.NormalizePresetSelection(presetID)
	if providerID == "" {
		providerID = m.activeProviderID()
	}
	if modelID == "" {
		return fmt.Errorf("model id is required")
	}
	if providerID == "" || !m.cfg.HasUsableProvider(providerID) {
		return fmt.Errorf("provider is not configured")
	}
	providerCfg, ok := m.cfg.Providers[providerID]
	if !ok {
		return fmt.Errorf("provider %q not configured", providerID)
	}
	sameSelection := strings.TrimSpace(m.currentSession.ProviderID) == providerID &&
		strings.TrimSpace(m.currentSession.ModelID) == modelID
	if sameSelection {
		if err := m.capabilityStore().Invalidate(providerID, providerCfg, modelID); err != nil {
			return err
		}
	}
	providerCfg.DefaultModel = modelID
	providerCfg.ModelPreset = presetID
	m.cfg.Providers[providerID] = providerCfg
	m.cfg.DefaultProvider = providerID
	m.cfg.DefaultModel = modelID
	if err := m.cfg.Save(); err != nil {
		return err
	}
	if m.agent != nil {
		m.agent.UpdateConfig(m.cfg)
	}
	if m.currentSession.ID != 0 && m.store != nil {
		if err := m.store.SetSessionModel(context.Background(), m.currentSession.ID, providerID, modelID); err != nil {
			return err
		}
	}
	m.currentSession.ProviderID = providerID
	m.currentSession.ModelID = modelID
	return nil
}

func (m *Model) providerModelPreset(providerID string) string {
	if providerCfg, ok := m.cfg.Provider(providerID); ok {
		return provider.NormalizePresetSelection(providerCfg.ModelPreset)
	}
	return provider.ModelPresetAuto
}

func (m *Model) activeProviderID() string {
	if strings.TrimSpace(m.currentSession.ProviderID) != "" {
		return m.currentSession.ProviderID
	}
	return m.cfg.DefaultProvider
}

func (m *Model) openThemePicker() {
	current := strings.TrimSpace(m.cfg.UI.Theme)
	if current == "" {
		current = theme.Default().Name
	}
	dialog := dialogs.NewThemeDialog(theme.Names(), current)
	m.themeDialog = &dialog
	m.themeDialogInitial = current
	m.previewSelectedTheme()
}

func (m *Model) submitThemeSelection(value string) (ui.Model, ui.Cmd) {
	if strings.TrimSpace(value) == "" {
		return m, nil
	}
	if err := m.setTheme(value, true); err != nil {
		m.status = fmt.Sprintf("theme save failed: %v", err)
		return m, nil
	}
	m.status = fmt.Sprintf("Theme set to %s", value)
	m.closeThemeDialog()
	return m, nil
}

func (m *Model) cancelThemeDialog() (ui.Model, ui.Cmd) {
	restore := strings.TrimSpace(m.themeDialogInitial)
	if restore == "" {
		restore = theme.Default().Name
	}
	if err := m.setTheme(restore, false); err != nil {
		m.status = fmt.Sprintf("theme restore failed: %v", err)
	}
	m.closeThemeDialog()
	return m, nil
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

func (m *Model) submitPickerSelection(value string) (ui.Model, ui.Cmd) {
	switch m.picker.mode {
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
		m.composer.InsertToken("$" + value)
		m.updateComposerMenus()
		m.status = fmt.Sprintf("Inserted $%s", value)
		return m, m.syncWindowTitleCmd()
	default:
		return m, nil
	}
}

func (m *Model) cancelPicker() (ui.Model, ui.Cmd) {
	switch m.picker.mode {
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
	if !m.hasThemeDialog() {
		return
	}
	item, ok := m.themeDialog.Current()
	if !ok {
		return
	}
	if err := m.setTheme(item, false); err != nil {
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
	if m.currentChat.ID != 0 {
		next := m.currentChat
		next.PermissionProfile = profile
		if err := m.store.UpdateChat(context.Background(), next); err != nil {
			return err
		}
		m.currentChat = next
		for idx := range m.chats {
			if m.chats[idx].ID == next.ID {
				m.chats[idx].PermissionProfile = profile
			}
		}
	} else if m.currentSession.ID != 0 {
		if err := m.store.SetSessionPermissionProfile(context.Background(), m.currentSession.ID, profile); err != nil {
			return err
		}
		m.currentSession.PermissionProfile = profile
		for idx := range m.sessions {
			if m.sessions[idx].ID == m.currentSession.ID {
				m.sessions[idx].PermissionProfile = profile
			}
		}
	} else {
		m.currentSession.PermissionProfile = profile
	}
	m.queuePermissionChangeNote()
	return nil
}

func (m *Model) setTheme(name string, save bool) error {
	selected := theme.Resolve(name)
	renderer, err := markdown.New(selected.Palette, m.cfg.UI.CodeStyle)
	if err != nil {
		return err
	}
	m.cfg.UI.Theme = selected.Name
	m.palette = selected.Palette
	m.renderer = renderer
	applyComposerTheme(&m.composer, selected.Palette)
	ui.InvalidateNodeCaches(&ui.Context{Palette: m.palette}, m.renderBodyElement())
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
	next.CodeStyle = markdown.NormalizeCodeStyle(next.CodeStyle)
	renderer, err := markdown.New(selected.Palette, next.CodeStyle)
	if err != nil {
		return nil, err
	}

	next.Theme = selected.Name
	next.Spinner = ui.NormalizeSpinner(next.Spinner)
	next.EditForgiveness = config.NormalizeEditForgiveness(next.EditForgiveness)
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

func (m *Model) applyPreferences(next dialogs.PreferencesValues, save bool) (ui.Cmd, error) {
	cmd, err := m.applyUIConfig(next.UI, false)
	if err != nil {
		return nil, err
	}
	if next.MaxToolLoopSteps <= 0 {
		next.MaxToolLoopSteps = config.Default().MaxToolLoopSteps
	}
	m.cfg.MaxToolLoopSteps = next.MaxToolLoopSteps
	if save {
		if err := m.cfg.Save(); err != nil {
			return nil, err
		}
		if m.agent != nil {
			m.agent.UpdateConfig(m.cfg)
		}
	}
	return cmd, nil
}

func spinnerTickCmd() ui.Cmd {
	return ui.Tick(120*time.Millisecond, func(time.Time) ui.Msg {
		return spinnerTickMsg{}
	})
}

func waitForExecEventCmd(events <-chan execruntime.Event, chatID int64, seq uint64) ui.Cmd {
	if events == nil {
		return nil
	}
	return func() ui.Msg {
		evt, ok := <-events
		return execEventMsg{chatID: chatID, seq: seq, event: evt, ok: ok}
	}
}

func (m *Model) waitForExecEventCmd() ui.Cmd {
	if m == nil || m.execEvents == nil || m.execSubscriptionChatID == 0 || m.execSubscriptionSeq == 0 {
		return nil
	}
	return waitForExecEventCmd(m.execEvents, m.execSubscriptionChatID, m.execSubscriptionSeq)
}

func (m *Model) refreshExecSubscriptionCmd() ui.Cmd {
	if m == nil {
		return nil
	}
	if m.execCancel != nil {
		m.execCancel()
		m.execCancel = nil
	}
	m.execEvents = nil
	m.execSubscriptionChatID = 0
	if m.exec == nil || m.currentSession.ID == 0 || m.currentChat.ID == 0 {
		return nil
	}
	events, cancel := m.exec.Subscribe(m.currentChat.ID)
	m.execEvents = events
	m.execCancel = cancel
	m.execSubscriptionChatID = m.currentChat.ID
	m.execSubscriptionSeq++
	return m.waitForExecEventCmd()
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
