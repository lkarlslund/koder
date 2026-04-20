package tui

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/debugsrv"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/theme"
	"github.com/lkarlslund/koder/internal/ui"
	"github.com/lkarlslund/koder/internal/workspace"
)

func TestMatchingSlashCommands(t *testing.T) {
	all := matchingSlashCommands("")
	if len(all) == 0 {
		t.Fatal("expected slash commands")
	}

	matches := matchingSlashCommands("n")
	if len(matches) != 1 {
		t.Fatalf("expected one match, got %d", len(matches))
	}
	if matches[0].Name != "/new" {
		t.Fatalf("expected /new, got %s", matches[0].Name)
	}

	matches = matchingSlashCommands("per")
	if len(matches) != 1 || matches[0].Name != "/perm" {
		t.Fatalf("expected /perm, got %#v", matches)
	}

	matches = matchingSlashCommands("comp")
	if len(matches) != 1 || matches[0].Name != "/compact" {
		t.Fatalf("expected /compact, got %#v", matches)
	}

	matches = matchingSlashCommands("conn")
	if len(matches) != 1 || matches[0].Name != "/connect" {
		t.Fatalf("expected /connect, got %#v", matches)
	}

	matches = matchingSlashCommands("disc")
	if len(matches) != 1 || matches[0].Name != "/disconnect" {
		t.Fatalf("expected /disconnect, got %#v", matches)
	}

	matches = matchingSlashCommands("fork")
	if len(matches) != 1 || matches[0].Name != "/fork" {
		t.Fatalf("expected /fork, got %#v", matches)
	}

	matches = matchingSlashCommands("mod")
	if len(matches) != 1 || matches[0].Name != "/model" {
		t.Fatalf("expected /model, got %#v", matches)
	}

	matches = matchingSlashCommands("res")
	if len(matches) != 1 || matches[0].Name != "/resume" {
		t.Fatalf("expected /resume, got %#v", matches)
	}

	matches = matchingSlashCommands("the")
	if len(matches) != 1 || matches[0].Name != "/theme" {
		t.Fatalf("expected /theme, got %#v", matches)
	}

	matches = matchingSlashCommands("rea")
	if len(matches) != 0 {
		t.Fatalf("expected tool slash commands to stay hidden, got %#v", matches)
	}
}

func TestSlashQuery(t *testing.T) {
	query, ok := slashQuery("/")
	if !ok || query != "" {
		t.Fatalf("unexpected slash query for /: ok=%v query=%q", ok, query)
	}

	query, ok = slashQuery("/new")
	if !ok || query != "new" {
		t.Fatalf("unexpected slash query for /new: ok=%v query=%q", ok, query)
	}

	if _, ok := slashQuery("/mouse on"); ok {
		t.Fatal("expected no autocomplete query after slash command arguments start")
	}
}

func TestEnterSendsNormalPrompt(t *testing.T) {
	cfg := config.Default()
	cfg.DefaultProvider = "openai"
	cfg.DefaultModel = "gpt-5.4"
	cfg.Providers = map[string]config.Provider{
		"openai": {
			Kind:         "openai-compatible",
			AuthMethod:   "api_key",
			BaseURL:      "https://api.openai.com/v1",
			APIKey:       "secret",
			DefaultModel: "gpt-5.4",
		},
	}
	m := Model{
		cfg:      cfg,
		composer: textarea.New(),
		parts:    map[int64][]domain.Part{},
	}
	m.composer.SetValue("hello")

	updated, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected send command")
	}
	if !next.loading {
		t.Fatal("expected loading after enter")
	}
	if next.composer.Value() != "" {
		t.Fatalf("expected composer reset, got %q", next.composer.Value())
	}
	if len(next.messages) != 1 || next.messages[0].Summary != "hello" {
		t.Fatalf("expected optimistic user message, got %#v", next.messages)
	}
	if len(next.parts[next.messages[0].ID]) != 1 || next.parts[next.messages[0].ID][0].Body != "hello" {
		t.Fatalf("expected optimistic user part, got %#v", next.parts)
	}
}

func TestAltEnterInsertsNewlineInsteadOfSending(t *testing.T) {
	cfg := config.Default()
	cfg.DefaultProvider = "openai"
	cfg.DefaultModel = "gpt-5.4"
	cfg.Providers = map[string]config.Provider{
		"openai": {
			Kind:         "openai-compatible",
			AuthMethod:   "api_key",
			BaseURL:      "https://api.openai.com/v1",
			APIKey:       "secret",
			DefaultModel: "gpt-5.4",
		},
	}
	m := Model{
		cfg:      cfg,
		composer: textarea.New(),
		parts:    map[int64][]domain.Part{},
	}
	m.composer.SetValue("hello")
	m.composer.SetCursor(len("hello"))

	updated, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	next := updated.(*Model)
	if cmd != nil {
		t.Fatal("expected no send command for modified enter")
	}
	if next.loading {
		t.Fatal("expected modified enter not to start loading")
	}
	if next.composer.Value() != "hello\n" {
		t.Fatalf("expected newline inserted, got %q", next.composer.Value())
	}
	if len(next.messages) != 0 {
		t.Fatalf("expected no optimistic transcript append, got %#v", next.messages)
	}
}

func TestCtrlVPastesClipboardText(t *testing.T) {
	m := Model{
		composer:          textarea.New(),
		readClipboardText: func() (string, error) { return "pasted text", nil },
	}
	m.composer.SetValue("hello ")
	m.composer.SetCursor(len("hello "))

	updated, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlV})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected title sync command after paste")
	}
	if got := next.composer.Value(); got != "hello pasted text" {
		t.Fatalf("unexpected pasted composer value: %q", got)
	}
	if next.status != "Pasted from clipboard" {
		t.Fatalf("unexpected paste status: %q", next.status)
	}
}

func TestCtrlYCopiesLatestAssistantMessage(t *testing.T) {
	var copied string
	m := Model{
		composer: textarea.New(),
		parts: map[int64][]domain.Part{
			2: {{Kind: domain.PartKindText, Body: "latest assistant reply"}},
		},
		messages: []domain.Message{
			{ID: 1, Role: domain.MessageRoleUser, Summary: "hello"},
			{ID: 2, Role: domain.MessageRoleAssistant, Summary: "latest assistant reply"},
		},
		writeClipboardText: func(text string) error {
			copied = text
			return nil
		},
	}

	updated, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlY})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected title sync command after copy")
	}
	if copied != "latest assistant reply" {
		t.Fatalf("unexpected copied text: %q", copied)
	}
	if next.status != "Copied last assistant message" {
		t.Fatalf("unexpected copy status: %q", next.status)
	}
}

func TestEnterWhileBusyQueuesPrompt(t *testing.T) {
	cfg := config.Default()
	cfg.DefaultProvider = "openai"
	cfg.DefaultModel = "gpt-5.4"
	cfg.Providers = map[string]config.Provider{
		"openai": {
			Kind:         "openai-compatible",
			AuthMethod:   "api_key",
			BaseURL:      "https://api.openai.com/v1",
			APIKey:       "secret",
			DefaultModel: "gpt-5.4",
		},
	}
	m := Model{
		cfg:      cfg,
		composer: textarea.New(),
		parts:    map[int64][]domain.Part{},
		loading:  true,
		busy: busyModel{
			active: true,
			scope:  busyScopeTranscript,
		},
	}
	m.composer.SetValue("follow up")

	updated, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected title sync command after queueing")
	}
	if next.queuedPrompt == nil || next.queuedPrompt.Text != "follow up" || next.queuedPrompt.Mode != queuedPromptModeNormal {
		t.Fatalf("expected queued prompt, got %#v", next.queuedPrompt)
	}
	if next.composer.Value() != "" {
		t.Fatalf("expected composer reset after queueing, got %q", next.composer.Value())
	}
	if len(next.messages) != 0 {
		t.Fatalf("expected no optimistic send while busy, got %#v", next.messages)
	}
}

func TestTabWhileBusyQueuesSteeringPrompt(t *testing.T) {
	cfg := config.Default()
	cfg.DefaultProvider = "openai"
	cfg.DefaultModel = "gpt-5.4"
	cfg.Providers = map[string]config.Provider{
		"openai": {
			Kind:         "openai-compatible",
			AuthMethod:   "api_key",
			BaseURL:      "https://api.openai.com/v1",
			APIKey:       "secret",
			DefaultModel: "gpt-5.4",
		},
	}
	m := Model{
		cfg:      cfg,
		composer: textarea.New(),
		loading:  true,
		busy: busyModel{
			active: true,
			scope:  busyScopeTranscript,
		},
	}
	m.composer.SetValue("nudge the plan")

	updated, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected title sync command after steering queue")
	}
	if next.queuedPrompt == nil || next.queuedPrompt.Mode != queuedPromptModeSteer {
		t.Fatalf("expected steering queue, got %#v", next.queuedPrompt)
	}
}

func TestLoadMsgDispatchesQueuedPrompt(t *testing.T) {
	cfg := config.Default()
	cfg.DefaultProvider = "openai"
	cfg.DefaultModel = "gpt-5.4"
	cfg.Providers = map[string]config.Provider{
		"openai": {
			Kind:         "openai-compatible",
			AuthMethod:   "api_key",
			BaseURL:      "https://api.openai.com/v1",
			APIKey:       "secret",
			DefaultModel: "gpt-5.4",
		},
	}
	m := Model{
		cfg:            cfg,
		composer:       textarea.New(),
		parts:          map[int64][]domain.Part{},
		viewport:       viewport.New(40, 6),
		currentSession: domain.Session{ID: 9, ProviderID: "openai", ModelID: "gpt-5.4", Title: "Queued"},
		queuedPrompt:   &queuedPrompt{Text: "queued ask", Mode: queuedPromptModeNormal},
	}

	updated, cmd := m.Update(loadMsg{
		current: domain.Session{ID: 9, ProviderID: "openai", ModelID: "gpt-5.4", Title: "Queued"},
		parts:   map[int64][]domain.Part{},
	})
	next := updated.(Model)
	if cmd == nil {
		t.Fatal("expected queued prompt dispatch command")
	}
	if !next.loading {
		t.Fatal("expected queued prompt dispatch to restart loading")
	}
	if len(next.messages) != 1 || next.messages[0].Summary != "queued ask" {
		t.Fatalf("expected optimistic queued message, got %#v", next.messages)
	}
	if next.queuedPrompt != nil {
		t.Fatalf("expected queued prompt cleared, got %#v", next.queuedPrompt)
	}
}

func TestWindowTitleUsesSessionTitle(t *testing.T) {
	m := Model{
		cfg:            config.Default(),
		currentSession: domain.Session{ID: 7, Title: "Helpful Session Title"},
	}
	got := m.windowTitle()
	if got != "K Helpful Session Title" {
		t.Fatalf("unexpected window title: %q", got)
	}
}

func TestWindowTitleUsesAnimatedSpinnerFrame(t *testing.T) {
	cfg := config.Default()
	cfg.UI.Spinner = "circles"
	m := Model{
		cfg: cfg,
		busy: busyModel{
			active: true,
			scope:  busyScopeTranscript,
			spinner: spinnerModel{
				active: true,
				frame:  2,
			},
		},
		currentSession: domain.Session{Title: "Build fixes"},
	}
	got := m.windowTitle()
	if !strings.HasPrefix(got, "◑K ") {
		t.Fatalf("unexpected animated window title: %q", got)
	}
}

func TestSyncDebugRuntimeIncludesViewportState(t *testing.T) {
	rec := debugsrv.NewRecorder()
	m := Model{
		debug:          rec,
		status:         "Ready",
		currentSession: domain.Session{ID: 7, Title: "Debug Session", ProviderID: "test", ModelID: "model"},
		messages:       []domain.Message{{ID: 1}, {ID: 2}},
		viewport:       viewport.New(40, 6),
	}
	m.viewport.SetContent("line one\nline two")

	m.syncDebugRuntime()

	got := rec.Runtime()
	if got.CurrentSession != 7 || got.ViewportWidth != 40 || got.ViewportHeight != 6 {
		t.Fatalf("unexpected runtime snapshot: %#v", got)
	}
	if got.MessageCount != 2 {
		t.Fatalf("expected message count 2, got %#v", got)
	}
	if !strings.Contains(got.ViewportPreview, "line one") {
		t.Fatalf("expected viewport preview, got %#v", got)
	}
}

func TestRenderTranscriptToolMessageFallsBackToSummaryWhenBodyMissing(t *testing.T) {
	cfg := config.Default()
	m := Model{
		cfg:     cfg,
		palette: theme.Resolve("tokyonight").Palette,
		parts:   map[int64][]domain.Part{},
	}

	got := m.renderTranscriptMessage(domain.Message{
		ID:      1,
		Role:    domain.MessageRoleTool,
		Summary: "bash completed with no output",
	})
	if !strings.Contains(got, "bash completed with no output") {
		t.Fatalf("expected tool summary fallback in transcript, got %q", got)
	}
}

func TestEnterWithoutProviderOpensConnectDialog(t *testing.T) {
	m := Model{
		cfg:      config.Default(),
		composer: textarea.New(),
	}
	m.composer.SetValue("hello")

	updated, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(*Model)
	if cmd != nil {
		t.Fatal("expected no async command when provider is missing")
	}
	if !next.hasConnectDialog() {
		t.Fatal("expected connect dialog to open")
	}
	if next.composer.Value() != "hello" {
		t.Fatalf("expected prompt to remain in composer, got %q", next.composer.Value())
	}
}

func TestExactSlashCommandDoesNotConsumeEnterForAutocomplete(t *testing.T) {
	m := Model{
		composer: textarea.New(),
	}
	m.composer.SetValue("/new")
	m.updateSlashMenu()

	updated, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected enter to continue to normal command handling")
	}
	if !next.loading {
		t.Fatal("expected loading after slash command enter")
	}
}

func TestExactSlashCommandWithArgsConsumesEnterForAutocomplete(t *testing.T) {
	m := Model{
		composer: textarea.New(),
	}
	m.composer.SetValue("/perm")
	m.updateSlashMenu()

	updated, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(*Model)
	if cmd != nil {
		t.Fatal("expected no command while autocompleting needs-args slash command")
	}
	if next.loading {
		t.Fatal("expected no loading while autocompleting needs-args slash command")
	}
	if got := next.composer.Value(); got != "/perm " {
		t.Fatalf("expected /perm autocompletion, got %q", got)
	}
}

func TestRunPromptErrorAppendsAssistantErrorToTranscript(t *testing.T) {
	m := Model{
		composer: textarea.New(),
		parts:    map[int64][]domain.Part{},
		viewport: viewport.New(40, 6),
	}

	updated, cmd := m.Update(runPromptMsg{err: errors.New("connection refused")})
	next := updated.(Model)
	if cmd == nil {
		t.Fatal("expected title sync command on immediate prompt error")
	}
	if next.loading {
		t.Fatal("expected loading cleared after prompt error")
	}
	if len(next.messages) != 1 {
		t.Fatalf("expected local assistant error message, got %#v", next.messages)
	}
	if next.messages[0].Role != domain.MessageRoleAssistant {
		t.Fatalf("expected assistant role, got %s", next.messages[0].Role)
	}
	if got := next.parts[next.messages[0].ID][0].Body; got != "Error: connection refused" {
		t.Fatalf("unexpected local error part: %q", got)
	}
	if !strings.Contains(next.viewport.View(), "Error: connection refused") {
		t.Fatalf("expected viewport to show error, got %q", next.viewport.View())
	}
}

func TestNewSessionMsgClearsBusyState(t *testing.T) {
	m := Model{
		busy: busyModel{
			active: true,
			scope:  busyScopeSidebar,
			status: "Creating session…",
			spinner: spinnerModel{
				active: true,
			},
		},
		loading:  true,
		composer: textarea.New(),
		parts:    map[int64][]domain.Part{},
		viewport: viewport.New(40, 6),
	}

	updated, _ := m.Update(newSessionMsg{
		session:   domain.Session{Title: "New Session"},
		parts:     map[int64][]domain.Part{},
		workspace: workspace.Status{},
	})
	next := updated.(Model)
	if next.loading {
		t.Fatal("expected new session to clear loading")
	}
	if next.busy.active {
		t.Fatal("expected new session to stop busy state")
	}
	if next.status != "Started new session" {
		t.Fatalf("unexpected status: %q", next.status)
	}
}

func TestForkCommandCreatesForkedSession(t *testing.T) {
	cfg := config.Default()
	st, err := store.OpenWithOptions(t.TempDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	session, err := st.CreateSession(context.Background(), "Source Session", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := st.AddMessage(context.Background(), session.ID, domain.MessageRoleUser, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddPart(context.Background(), msg.ID, domain.PartKindText, "hello", ""); err != nil {
		t.Fatal(err)
	}

	m := Model{
		cfg:            cfg,
		store:          st,
		composer:       textarea.New(),
		viewport:       viewport.New(40, 6),
		currentSession: session,
		parts:          map[int64][]domain.Part{},
		workdir:        t.TempDir(),
	}
	m.composer.SetValue("/fork")

	updated, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected fork command")
	}
	if !next.loading {
		t.Fatal("expected loading while forking")
	}

	msgAny := next.forkSessionCmd(session.ID)()
	forkMsg, ok := msgAny.(forkSessionMsg)
	if !ok {
		t.Fatalf("expected forkSessionMsg, got %T", msgAny)
	}
	updated, _ = next.Update(forkMsg)
	forked := updated.(Model)
	if forked.currentSession.ID == session.ID {
		t.Fatal("expected forked session id to differ from source")
	}
	if forked.currentSession.ParentID == nil || *forked.currentSession.ParentID != session.ID {
		t.Fatalf("expected parent id %d, got %#v", session.ID, forked.currentSession.ParentID)
	}
	if len(forked.messages) != 1 || forked.messages[0].Summary != "hello" {
		t.Fatalf("unexpected forked messages: %#v", forked.messages)
	}
	if forked.status == "" || !strings.Contains(forked.status, "Forked session") {
		t.Fatalf("unexpected fork status: %q", forked.status)
	}
}

func TestToolLikeSlashCommandIsRejectedLocally(t *testing.T) {
	m := Model{
		composer: textarea.New(),
	}
	m.composer.SetValue("/read README.md")

	updated, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(*Model)
	if cmd != nil {
		t.Fatal("expected no command for hidden tool-like slash input")
	}
	if next.loading {
		t.Fatal("expected hidden tool-like slash input to stay local")
	}
	if next.status != "unknown command: /read README.md" {
		t.Fatalf("unexpected status: %q", next.status)
	}
}

func TestApprovalPromptConsumesEnter(t *testing.T) {
	m := Model{
		composer:  textarea.New(),
		approvals: []store.Approval{{ID: 7}},
	}

	updated, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected approval command")
	}
	if !next.loading {
		t.Fatal("expected loading after approval enter")
	}
}

func TestQuitCommandQuits(t *testing.T) {
	m := Model{
		composer: textarea.New(),
	}
	m.composer.SetValue("/quit")

	updated, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected quit command")
	}
	if next.loading {
		t.Fatal("expected quit to stop loading")
	}
}

func TestMouseOnCommandEnablesMouseCapture(t *testing.T) {
	m := Model{
		composer: textarea.New(),
	}
	m.composer.SetValue("/mouse on")

	updated, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected mouse enable command")
	}
	if !next.mouseEnabled {
		t.Fatal("expected mouse capture enabled")
	}
	if next.status != "Mouse capture enabled" {
		t.Fatalf("unexpected status: %q", next.status)
	}
}

func TestMouseOffCommandDisablesMouseCapture(t *testing.T) {
	m := Model{
		composer:     textarea.New(),
		mouseEnabled: true,
	}
	m.composer.SetValue("/mouse off")

	updated, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected mouse disable command")
	}
	if next.mouseEnabled {
		t.Fatal("expected mouse capture disabled")
	}
	if next.status != "Mouse capture disabled" {
		t.Fatalf("unexpected status: %q", next.status)
	}
}

func TestInitEnablesMouseWhenConfigured(t *testing.T) {
	cfg := config.Default()
	cfg.UI.Mouse = true

	m, err := New(cfg, nil, nil, StartupModeNew, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !m.mouseEnabled {
		t.Fatal("expected mouseEnabled to follow config")
	}

	cmd := m.Init()
	if cmd == nil {
		t.Fatal("expected init command")
	}
	if _, ok := cmd().(tea.BatchMsg); !ok {
		t.Fatalf("expected batched init command when mouse is enabled, got %T", cmd())
	}
}

func TestCtrlCUsesQuitPath(t *testing.T) {
	m := Model{
		composer: textarea.New(),
	}

	updated, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlC})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected quit command")
	}
	if next.status != "Quitting" {
		t.Fatalf("unexpected status: %q", next.status)
	}
}

func TestEscInterruptRequiresDoublePress(t *testing.T) {
	m := Model{
		composer: textarea.New(),
		loading:  true,
		busy: busyModel{
			active: true,
			scope:  busyScopeTranscript,
		},
		activeOpCancel: func() {},
	}

	updated, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected title sync command after first esc")
	}
	if next.status != "Press Esc again to interrupt" {
		t.Fatalf("unexpected first esc status: %q", next.status)
	}
	if next.interruptArmedAt.IsZero() {
		t.Fatal("expected interrupt to arm on first esc")
	}
}

func TestEscInterruptCancelsActiveOperation(t *testing.T) {
	cancelled := false
	m := Model{
		composer: textarea.New(),
		loading:  true,
		busy: busyModel{
			active: true,
			scope:  busyScopeTranscript,
		},
		activeOpCancel:   func() { cancelled = true },
		interruptArmedAt: time.Now(),
	}

	updated, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected title sync command after second esc")
	}
	if !cancelled {
		t.Fatal("expected active operation to be cancelled")
	}
	if next.status != "Interrupting…" {
		t.Fatalf("unexpected second esc status: %q", next.status)
	}
}

func TestExitSummaryIncludesSessionDetails(t *testing.T) {
	m := Model{
		currentSession: domain.Session{ID: 4, Title: "Testing Session Review Flow"},
		messages:       []domain.Message{{ID: 1}, {ID: 2}, {ID: 3}},
	}

	got := m.exitSummary()
	want := `Closed session 4 "Testing Session Review Flow" with 3 messages.`
	if got != want {
		t.Fatalf("unexpected summary: %q", got)
	}
}

func TestSessionPickerEscapeCreatesNewSession(t *testing.T) {
	m := Model{
		composer:      textarea.New(),
		sessionDialog: &ui.SessionDialog{},
		sessions:      []domain.Session{{ID: 1}},
	}

	updated, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected new session command")
	}
	if !next.loading {
		t.Fatal("expected loading after picker escape")
	}
}

func TestSessionPickerRendersCenteredDialogWithDetails(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	session, err := st.CreateSession(context.Background(), "Generated Session Title", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := st.AddMessage(context.Background(), session.ID, domain.MessageRoleAssistant, "summary")
	if err != nil {
		t.Fatal(err)
	}
	usage, _ := json.Marshal(domain.Usage{PromptTokens: 123, CompletionTokens: 456, TotalTokens: 579})
	if _, err := st.AddPart(context.Background(), msg.ID, domain.PartKindSystemNotice, "usage", string(usage)); err != nil {
		t.Fatal(err)
	}

	m := Model{
		width:  100,
		height: 30,
		store:  st,
		sessions: []domain.Session{{
			ID:        session.ID,
			Title:     "Generated Session Title",
			CreatedAt: time.Date(2026, 4, 20, 10, 30, 0, 0, time.UTC),
			UpdatedAt: time.Date(2026, 4, 20, 12, 45, 0, 0, time.UTC),
		}},
	}
	m.openSessionPicker()

	got := m.View()
	if !strings.Contains(got, "Resume Session") {
		t.Fatalf("expected centered dialog title, got %q", got)
	}
	if !strings.Contains(got, "Session ID: 1") {
		t.Fatalf("expected session id in dialog, got %q", got)
	}
	if !strings.Contains(got, "Created:") || !strings.Contains(got, "Changed:") {
		t.Fatalf("expected timestamps in dialog, got %q", got)
	}
	if !strings.Contains(got, "Generated Session Title") {
		t.Fatalf("expected title in dialog, got %q", got)
	}
	if !strings.Contains(got, "Tokens:     in 123  out 456") {
		t.Fatalf("expected token counts in dialog, got %q", got)
	}
	if !strings.Contains(got, "Enter to select, Esc to start new session") {
		t.Fatalf("expected helper text in dialog, got %q", got)
	}
}

func TestUpdateLoadHidesSessionPicker(t *testing.T) {
	m := Model{
		sessionDialog: &ui.SessionDialog{},
	}

	updated := m.UpdateLoad(loadMsg{
		current: domain.Session{ID: 4},
	})

	if updated.hasSessionDialog() {
		t.Fatal("expected session dialog to close after loading a session")
	}
	if updated.currentSession.ID != 4 {
		t.Fatalf("unexpected current session: %#v", updated.currentSession)
	}
}

func TestThemeCommandOpensFilterablePicker(t *testing.T) {
	m := Model{
		cfg:      config.Default(),
		composer: textarea.New(),
	}
	m.composer.SetValue("/theme")

	updated, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(*Model)
	if cmd != nil {
		t.Fatal("expected no async command for theme picker")
	}
	if !next.hasPicker() {
		t.Fatal("expected picker to open")
	}
	if next.picker.mode != pickerModeTheme {
		t.Fatalf("expected theme picker mode, got %v", next.picker.mode)
	}
	if len(next.picker.matches) == 0 {
		t.Fatal("expected theme matches")
	}
}

func TestThemePickerFiltersAndPreviewsSelection(t *testing.T) {
	cfg := config.Default()
	cfg.UI.Theme = "tokyonight"

	m, err := New(cfg, nil, nil, StartupModeNew, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.openThemePicker()

	updated, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	next := updated.(*Model)
	updated, _ = next.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	next = updated.(*Model)

	if len(next.picker.matches) == 0 {
		t.Fatal("expected filtered theme matches")
	}
	if next.picker.matches[0].Value != "gruvbox" {
		t.Fatalf("expected gruvbox after filtering, got %#v", next.picker.matches)
	}
	if next.cfg.UI.Theme != "gruvbox" {
		t.Fatalf("expected live theme preview to apply gruvbox, got %q", next.cfg.UI.Theme)
	}
}

func TestThemePickerEscapeRestoresOriginalTheme(t *testing.T) {
	cfg := config.Default()
	cfg.UI.Theme = "flexoki"

	m, err := New(cfg, nil, nil, StartupModeNew, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.openThemePicker()
	m.movePicker(1)
	if m.cfg.UI.Theme == "flexoki" {
		t.Fatal("expected theme preview to change current theme before cancel")
	}

	updated, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	next := updated.(*Model)
	if cmd != nil {
		t.Fatal("expected no async command on theme picker cancel")
	}
	if next.cfg.UI.Theme != "flexoki" {
		t.Fatalf("expected original theme restored, got %q", next.cfg.UI.Theme)
	}
	if next.hasPicker() {
		t.Fatal("expected picker to close on cancel")
	}
}

func TestThemePickerEnterSavesTheme(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.UI.Theme = "flexoki"
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	m, err := New(cfg, nil, nil, StartupModeNew, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.openThemePicker()
	m.movePicker(1)

	updated, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(*Model)
	if cmd != nil {
		t.Fatal("expected no async command on theme apply")
	}
	if next.hasPicker() {
		t.Fatal("expected picker to close after selection")
	}
	if next.cfg.UI.Theme == "flexoki" {
		t.Fatal("expected theme selection to persist a new theme")
	}
	if !strings.Contains(next.status, "Theme set to") {
		t.Fatalf("expected status update after theme apply, got %q", next.status)
	}
}

func TestPrefsCommandOpensPreferencesDialog(t *testing.T) {
	m := Model{
		cfg:      config.Default(),
		composer: textarea.New(),
	}
	m.composer.SetValue("/preferences")

	updated, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected spinner tick command when opening preferences")
	}
	if !next.hasPreferencesDialog() {
		t.Fatal("expected preferences dialog to open")
	}
}

func TestConnectCommandOpensConnectDialog(t *testing.T) {
	m := Model{
		cfg:      config.Default(),
		composer: textarea.New(),
	}
	m.composer.SetValue("/connect")

	updated, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected title sync command when opening connect dialog")
	}
	if !next.hasConnectDialog() {
		t.Fatal("expected connect dialog to open")
	}
}

func TestDisconnectCommandOpensDisconnectDialog(t *testing.T) {
	m := Model{
		cfg: config.Config{
			Providers: map[string]config.Provider{
				"openai": {Name: "OpenAI", BaseURL: "https://api.openai.com/v1"},
			},
		},
		composer: textarea.New(),
	}
	m.composer.SetValue("/disconnect")

	updated, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected title sync command when opening disconnect dialog")
	}
	if !next.hasDisconnectDialog() {
		t.Fatal("expected disconnect dialog to open")
	}
}

func TestDisconnectCommandWithoutProvidersShowsStatus(t *testing.T) {
	m := Model{
		cfg:      config.Default(),
		composer: textarea.New(),
	}
	m.composer.SetValue("/disconnect")

	updated, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected title sync command")
	}
	if next.status != "No configured providers to disconnect" {
		t.Fatalf("unexpected status: %q", next.status)
	}
}

func TestModelCommandWithoutProviderShowsStatus(t *testing.T) {
	m := Model{
		cfg:      config.Default(),
		composer: textarea.New(),
	}
	m.composer.SetValue("/model")

	updated, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected title sync command")
	}
	if next.status != "Configure a provider first with /connect" {
		t.Fatalf("unexpected status: %q", next.status)
	}
}

func TestModelCommandLoadsModelsForActiveProvider(t *testing.T) {
	cfg := config.Default()
	cfg.DefaultProvider = "openai"
	cfg.DefaultModel = "gpt-5.4"
	cfg.Providers = map[string]config.Provider{
		"openai": {
			Name:         "OpenAI",
			Kind:         "openai-compatible",
			AuthMethod:   "api_key",
			BaseURL:      "https://api.openai.com/v1",
			APIKey:       "secret",
			DefaultModel: "gpt-5.4",
		},
	}
	m := Model{
		cfg:      cfg,
		composer: textarea.New(),
	}
	m.composer.SetValue("/model")

	updated, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected async model load command")
	}
	if next.status != "Loading models for openai…" {
		t.Fatalf("unexpected status: %q", next.status)
	}
}

func TestSaveProviderDraftPersistsDefaults(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	m := Model{cfg: cfg}
	draft, err := provider.BuildDraft("openai", nil)
	if err != nil {
		t.Fatal(err)
	}
	draft.APIKey = "secret"
	draft.Model = "gpt-5.4"

	if err := m.saveProviderDraft(draft); err != nil {
		t.Fatal(err)
	}
	if !m.cfg.HasUsableDefaultProvider() {
		t.Fatal("expected usable default provider after save")
	}
	if got := m.cfg.DefaultModel; got != "gpt-5.4" {
		t.Fatalf("unexpected default model: %q", got)
	}
	reloaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.DefaultProvider != "openai" {
		t.Fatalf("unexpected saved default provider: %q", reloaded.DefaultProvider)
	}
}

func TestDisconnectProviderClearsDefaultAndFallsBack(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.DefaultProvider = "openai"
	cfg.DefaultModel = "gpt-5.4"
	cfg.Providers = map[string]config.Provider{
		"openai": {
			Name:         "OpenAI",
			Kind:         "openai-compatible",
			AuthMethod:   "api_key",
			BaseURL:      "https://api.openai.com/v1",
			DefaultModel: "gpt-5.4",
		},
		"ollama": {
			Name:         "Ollama",
			Kind:         "openai-compatible",
			AuthMethod:   "local_endpoint",
			BaseURL:      "http://127.0.0.1:11434/v1",
			DefaultModel: "qwen",
		},
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	m := Model{
		cfg:            cfg,
		currentSession: domain.Session{ProviderID: "openai", ModelID: "gpt-5.4"},
	}

	if err := m.disconnectProvider("openai"); err != nil {
		t.Fatal(err)
	}
	if m.cfg.DefaultProvider != "ollama" {
		t.Fatalf("expected fallback default provider, got %q", m.cfg.DefaultProvider)
	}
	if m.currentSession.ProviderID != "ollama" || m.currentSession.ModelID != "qwen" {
		t.Fatalf("expected current session to fall back, got %#v", m.currentSession)
	}
	reloaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reloaded.Providers["openai"]; ok {
		t.Fatal("expected provider removed from saved config")
	}
}

func TestSelectModelUpdatesConfigAndCurrentSession(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.DefaultProvider = "openai"
	cfg.DefaultModel = "gpt-5.4"
	cfg.Providers = map[string]config.Provider{
		"openai": {
			Name:         "OpenAI",
			Kind:         "openai-compatible",
			AuthMethod:   "api_key",
			BaseURL:      "https://api.openai.com/v1",
			DefaultModel: "gpt-5.4",
		},
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	session, err := st.CreateSession(context.Background(), "test", "openai", "gpt-5.4", nil)
	if err != nil {
		t.Fatal(err)
	}
	m := Model{
		cfg:            cfg,
		store:          st,
		currentSession: session,
	}

	if err := m.selectModel("gpt-4.1-mini"); err != nil {
		t.Fatal(err)
	}
	if m.cfg.DefaultModel != "gpt-4.1-mini" || m.currentSession.ModelID != "gpt-4.1-mini" {
		t.Fatalf("unexpected model selection state: cfg=%q session=%q", m.cfg.DefaultModel, m.currentSession.ModelID)
	}
	reloaded, err := st.GetSession(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.ModelID != "gpt-4.1-mini" {
		t.Fatalf("expected persisted session model, got %q", reloaded.ModelID)
	}
}

func TestModelListMsgOpensModelDialog(t *testing.T) {
	m := Model{}
	updated, _ := m.Update(modelListMsg{
		providerID: "openai",
		models: []domain.Model{
			{ID: "gpt-5.4", OwnedBy: "openai"},
			{ID: "gpt-4.1-mini", OwnedBy: "openai"},
		},
	})
	next := updated.(Model)
	if !next.hasModelDialog() {
		t.Fatal("expected model dialog to open")
	}
}

func TestPreferencesDialogCancelRestoresOriginalUI(t *testing.T) {
	cfg := config.Default()
	cfg.UI.Theme = "flexoki"

	m, err := New(cfg, nil, nil, StartupModeNew, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.openPreferencesDialog()

	updated, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRight})
	next := updated.(*Model)
	if next.cfg.UI.Theme == "flexoki" {
		t.Fatal("expected preferences preview to change current theme")
	}

	updated, cmd := next.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	next = updated.(*Model)
	if cmd == nil {
		t.Fatal("expected title sync command when cancelling preferences")
	}
	if next.cfg.UI.Theme != "flexoki" {
		t.Fatalf("expected original theme restored, got %q", next.cfg.UI.Theme)
	}
	if next.hasPreferencesDialog() {
		t.Fatal("expected preferences dialog to close on cancel")
	}
}

func TestPreferencesDialogApplySavesUIConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.UI.Theme = "flexoki"
	cfg.UI.ShowSidebar = true
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	m, err := New(cfg, nil, nil, StartupModeNew, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.openPreferencesDialog()

	updated, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRight})
	next := updated.(*Model)
	updated, _ = next.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	next = updated.(*Model)
	updated, _ = next.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	next = updated.(*Model)

	if next.hasPreferencesDialog() {
		t.Fatal("expected preferences dialog to close after apply")
	}
	if next.cfg.UI.Theme == "flexoki" {
		t.Fatal("expected preferences apply to persist a different theme")
	}
	reloaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.UI.Theme != next.cfg.UI.Theme {
		t.Fatalf("expected saved theme %q, got %q", next.cfg.UI.Theme, reloaded.UI.Theme)
	}
}

func TestWorkingIndicatorShownWhenModelWorking(t *testing.T) {
	m := Model{
		busy: busyModel{
			active: true,
			scope:  busyScopeTranscript,
			spinner: spinnerModel{
				active: true,
			},
		},
	}

	got := m.workingIndicator()
	if got == "" {
		t.Fatal("expected working indicator while model is working")
	}
}

func TestRenderHeaderIsEmpty(t *testing.T) {
	m := Model{
		currentSession: domain.Session{ID: 2, ProviderID: "test", ModelID: "model"},
		status:         "Waiting for model…",
	}

	got := m.renderHeader()
	if got != "" {
		t.Fatalf("expected empty header, got %q", got)
	}
}

func TestRenderSidebarShowsStatusAndSessionInfo(t *testing.T) {
	m := Model{
		currentSession: domain.Session{ID: 2, ProviderID: "test", ModelID: "model", PermissionProfile: "default"},
		status:         "Working ...",
		debug: func() *debugsrv.Recorder {
			rec := debugsrv.NewRecorder()
			rec.SetDebugAPI("127.0.0.1:61347")
			return rec
		}(),
		busy: busyModel{
			active: true,
			scope:  busyScopeTranscript,
			spinner: spinnerModel{
				active: true,
			},
		},
		workdir: "/tmp/project",
		cfg: config.Config{
			Providers: map[string]config.Provider{
				"test": {ContextWindow: 32768},
			},
		},
		messages: []domain.Message{{ID: 1}},
		parts: map[int64][]domain.Part{
			1: {{Kind: domain.PartKindSystemNotice, Body: "usage", MetaJSON: `{"TotalTokens":8192}`}},
		},
	}

	got := m.renderSidebar()
	if !strings.Contains(got, "Session") || !strings.Contains(got, "provider test") || !strings.Contains(got, "model   model") {
		t.Fatalf("expected sidebar to include session details, got %q", got)
	}
	if !strings.Contains(got, "Status") || !strings.Contains(got, "Working ...") {
		t.Fatalf("expected sidebar to include status, got %q", got)
	}
	if !strings.Contains(got, "Keys") || !strings.Contains(got, "enter send/select") {
		t.Fatalf("expected sidebar to include hotkey hints, got %q", got)
	}
	if !strings.Contains(got, "/connect") {
		t.Fatalf("expected sidebar to include /connect hint, got %q", got)
	}
	if !strings.Contains(got, "Context") || !strings.Contains(got, "25% used") {
		t.Fatalf("expected sidebar to include context usage, got %q", got)
	}
	if !strings.Contains(got, "Debug") || !strings.Contains(got, "127.0.0.1:61347") {
		t.Fatalf("expected sidebar to include debug api status, got %q", got)
	}
}

func TestRefreshViewportShowsConnectHintWithoutProvider(t *testing.T) {
	m := Model{
		cfg:      config.Default(),
		viewport: viewport.New(40, 6),
	}
	m.refreshViewport()
	if got := m.viewport.View(); !strings.Contains(got, "/connect") {
		t.Fatalf("expected connect hint in empty viewport, got %q", got)
	}
}

func TestRenderBodyAppliesSidebarThemeBackground(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)

	m := Model{
		showSidebar: true,
		palette:     theme.Resolve("tokyonight").Palette,
		viewport:    viewport.New(40, 6),
	}
	m.viewport.SetContent("history")

	got := m.renderBody()
	if !strings.Contains(got, "48;2;30;32;48") {
		t.Fatalf("expected sidebar background ANSI color in render, got %q", got)
	}
}

func TestRenderBodyClipsSidebarToViewportHeight(t *testing.T) {
	m := Model{
		showSidebar: true,
		palette:     theme.Resolve("tokyonight").Palette,
		viewport:    viewport.New(40, 6),
		workdir:     "/tmp/project",
	}
	m.viewport.SetContent("history")

	got := m.renderBody()
	if h := lipgloss.Height(got); h != 6 {
		t.Fatalf("expected body height 6, got %d from %q", h, got)
	}
}

func TestRefreshViewportAppendsWorkingLine(t *testing.T) {
	m := Model{
		currentSession: domain.Session{ID: 1},
		status:         "Working ...",
		parts:          map[int64][]domain.Part{},
		viewport:       viewport.New(40, 6),
		busy: busyModel{
			active: true,
			scope:  busyScopeTranscript,
			spinner: spinnerModel{
				active: true,
			},
		},
	}

	m.refreshViewport()
	got := m.viewport.View()
	if !strings.Contains(got, "Working ...") || !strings.Contains(got, ui.SpinnerFrame(config.Default().UI.Spinner, 0)) {
		t.Fatalf("expected transcript activity line, got %q", got)
	}
}

func TestRenderFooterOmitsHotkeyHints(t *testing.T) {
	m := Model{
		composer: textarea.New(),
	}

	got := m.renderFooter()
	if strings.Contains(got, "enter send/select") || strings.Contains(got, "/perm profile") {
		t.Fatalf("expected footer to omit hotkey hints, got %q", got)
	}
}

func TestViewBottomAlignsFooter(t *testing.T) {
	composer := textarea.New()
	composer.Placeholder = "Ask koder or type / for commands"
	composer.SetHeight(composerInputHeight)
	composer.SetWidth(38)
	composer.Focus()

	m := Model{
		width:    40,
		height:   12,
		composer: composer,
		viewport: viewport.New(38, 4),
	}
	m.viewport.SetContent("history")

	got := m.View()
	lines := strings.Split(got, "\n")
	if len(lines) != 12 {
		t.Fatalf("expected placed view to match height, got %d lines", len(lines))
	}
	bottom := strings.Join(lines[len(lines)-3:], "\n")
	if !strings.Contains(bottom, "Ask koder or type / for") {
		t.Fatalf("expected composer box at bottom, got %q", got)
	}
}

func TestResizeUsesMeasuredFooterHeight(t *testing.T) {
	m := Model{
		width:    80,
		height:   24,
		composer: textarea.New(),
	}
	m.composer.SetHeight(4)

	m.resize()

	want := 24 - m.footerHeight()
	if want < 5 {
		want = 5
	}
	if m.viewport.Height != want {
		t.Fatalf("expected viewport height %d from measured footer, got %d", want, m.viewport.Height)
	}
}

func TestRenderComposerUsesThreeLineBoxAndFullWidth(t *testing.T) {
	palette := theme.Resolve("tokyonight").Palette
	cfg := config.Default()
	m := Model{
		cfg:         cfg,
		width:       80,
		showSidebar: true,
		composer:    textarea.New(),
		palette:     palette,
	}
	m.composer.Placeholder = "Ask koder or type / for commands"
	m.composer.Prompt = mPrompt(cfg)
	m.composer.SetHeight(composerInputHeight)
	m.composer.SetWidth(m.composerWidth())
	applyComposerTheme(&m.composer, palette)

	got := m.renderComposer()
	if lipgloss.Height(got) != 3 {
		t.Fatalf("expected 3-line composer box, got %d lines in %q", lipgloss.Height(got), got)
	}
	lines := strings.Split(got, "\n")
	if !strings.Contains(lines[0], "▄") || !strings.Contains(lines[len(lines)-1], "▀") {
		t.Fatalf("expected half-block top and bottom lines, got %q", got)
	}
	if !strings.HasPrefix(lines[0], "▄") || !strings.HasPrefix(lines[len(lines)-1], "▀") {
		t.Fatalf("expected half-height accent strip on separator rows, got %q", got)
	}
	if !strings.Contains(lines[1], "█") {
		t.Fatalf("expected block accent glyph on content line, got %q", lines[1])
	}
	for _, line := range lines {
		if lipgloss.Width(line) != m.composerWidth() {
			t.Fatalf("expected composer line width %d, got %d in %q", m.composerWidth(), lipgloss.Width(line), line)
		}
	}
}

func TestRenderUserMessageUsesAccentBarOnAllLines(t *testing.T) {
	cfg := config.Default()
	m := Model{
		cfg:     cfg,
		palette: theme.Resolve("tokyonight").Palette,
		viewport: viewport.Model{
			Width: 40,
		},
	}

	got := m.renderUserMessage("hello", "")
	lines := strings.Split(got, "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 user message lines, got %d in %q", len(lines), got)
	}
	if !strings.Contains(lines[0], "▄") || !strings.Contains(lines[2], "▀") {
		t.Fatalf("expected half-block separator rows, got %q", got)
	}
	if !strings.HasPrefix(lines[0], "▄") || !strings.HasPrefix(lines[2], "▀") {
		t.Fatalf("expected half-height accent strip on separator rows, got %q", got)
	}
	if !strings.Contains(lines[1], "█") {
		t.Fatalf("expected block accent on content row, got %q", lines[1])
	}
}

func TestRenderUserMessageCanDisableHalfBlocks(t *testing.T) {
	cfg := config.Default()
	cfg.UI.HalfBlocks = false
	m := Model{
		cfg:     cfg,
		palette: theme.Resolve("tokyonight").Palette,
		viewport: viewport.Model{
			Width: 40,
		},
	}

	got := m.renderUserMessage("hello", "")
	if strings.Contains(got, "▄") || strings.Contains(got, "▀") || strings.Contains(got, "█") {
		t.Fatalf("expected classic user message rendering when half blocks disabled, got %q", got)
	}
	if !strings.Contains(got, "┃") {
		t.Fatalf("expected classic accent bar when half blocks disabled, got %q", got)
	}
}

func TestRenderTranscriptUserMessageFallsBackToSummaryWhenPartsMissing(t *testing.T) {
	cfg := config.Default()
	m := Model{
		cfg:     cfg,
		palette: theme.Resolve("tokyonight").Palette,
		parts:   map[int64][]domain.Part{},
		viewport: viewport.Model{
			Width: 40,
		},
	}

	got := m.renderTranscriptMessage(domain.Message{
		ID:      1,
		Role:    domain.MessageRoleUser,
		Summary: "what tools are available",
	})
	if !strings.Contains(got, "what tools are available") {
		t.Fatalf("expected user summary fallback in transcript, got %q", got)
	}
}

func TestRefreshViewportOmitsWorkingLineForGenericLoading(t *testing.T) {
	m := Model{
		currentSession: domain.Session{ID: 1},
		loading:        true,
		status:         "Resuming session 2…",
		parts:          map[int64][]domain.Part{},
		viewport:       viewport.New(40, 6),
		busy: busyModel{
			active: true,
			scope:  busyScopeSidebar,
			spinner: spinnerModel{
				active: true,
			},
		},
	}

	m.refreshViewport()
	got := m.viewport.View()
	if strings.Contains(got, "Resuming session 2") || strings.Contains(got, "[=") {
		t.Fatalf("expected no model activity line for generic loading, got %q", got)
	}
}

func TestSpinnerTickRefreshesTranscriptActivity(t *testing.T) {
	m := Model{
		currentSession: domain.Session{ID: 1},
		status:         "Working ...",
		parts:          map[int64][]domain.Part{},
		viewport:       viewport.New(40, 6),
		busy: busyModel{
			active: true,
			scope:  busyScopeTranscript,
			spinner: spinnerModel{
				active: true,
			},
		},
	}

	m.refreshViewport()
	before := m.viewport.View()

	updated, cmd := m.Update(spinnerTickMsg{})
	next := updated.(Model)
	after := next.viewport.View()

	if before == after {
		t.Fatalf("expected spinner tick to refresh transcript activity, before=%q after=%q", before, after)
	}
	if cmd == nil {
		t.Fatal("expected follow-up spinner tick command")
	}
}

func TestStatusEventKeepsTranscriptSpinnerActive(t *testing.T) {
	m := Model{}
	m.startBusy(busyScopeTranscript, "Compacting session...")

	m.applyEvent(domain.Event{Kind: domain.EventKindStatus, Text: "Compacting session..."})

	if !m.busy.transcriptActive() {
		t.Fatal("expected transcript spinner to remain active for status updates during busy work")
	}
	if got := m.renderTranscriptActivity(); !strings.Contains(got, "Working ...") || !strings.Contains(got, ui.SpinnerFrame(config.Default().UI.Spinner, 0)) {
		t.Fatalf("expected transcript activity to still render, got %q", got)
	}
}

func TestRenderMessagePartsShowsReasoningBeforeText(t *testing.T) {
	m := Model{
		showReasoning: true,
	}

	got := m.renderMessageParts([]domain.Part{
		{Kind: domain.PartKindText, Body: "final answer"},
		{Kind: domain.PartKindReasoning, Body: "thinking first"},
	})

	if !strings.Contains(got, "thinking first") || !strings.Contains(got, "final answer") {
		t.Fatalf("expected both reasoning and text, got %q", got)
	}
	if strings.Index(got, "thinking first") > strings.Index(got, "final answer") {
		t.Fatalf("expected reasoning before text, got %q", got)
	}
}

func TestRenderMessagePartsSkipsSystemNotice(t *testing.T) {
	m := Model{}

	got := m.renderMessageParts([]domain.Part{
		{Kind: domain.PartKindText, Body: "final answer"},
		{Kind: domain.PartKindSystemNotice, Body: "usage", MetaJSON: `{"PromptTokens":1}`},
	})

	if !strings.Contains(got, "final answer") {
		t.Fatalf("expected text to remain visible, got %q", got)
	}
	if strings.Contains(got, "usage") || strings.Contains(got, "PromptTokens") {
		t.Fatalf("expected system notice to stay hidden, got %q", got)
	}
}

func TestRenderMessagePartsFormatsCompactionMarkdown(t *testing.T) {
	cfg := config.Default()
	m, err := New(cfg, nil, nil, StartupModeNew, nil)
	if err != nil {
		t.Fatal(err)
	}

	got := m.renderMessageParts([]domain.Part{
		{Kind: domain.PartKindCompaction, Body: "## Goal\n\n- first\n- second"},
	})

	if !strings.Contains(got, "Goal") {
		t.Fatalf("expected compaction heading text, got %q", got)
	}
	if !strings.Contains(got, "• first") || !strings.Contains(got, "• second") {
		t.Fatalf("expected compaction list markdown rendering, got %q", got)
	}
	if strings.Contains(got, "## Goal") || strings.Contains(got, "- first") {
		t.Fatalf("expected rendered markdown instead of raw compaction markdown, got %q", got)
	}
}

func TestRenderReasoningBlockStartsWithBlankStyledLine(t *testing.T) {
	m := Model{}

	got := m.renderReasoningBlock("thinking first")
	lines := strings.Split(got, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected multiple lines, got %q", got)
	}
	if lines[0] != "" {
		t.Fatalf("expected blank first line, got %q", got)
	}
	if !strings.Contains(lines[1], "thinking first") {
		t.Fatalf("expected reasoning text on second line, got %q", got)
	}
}

func TestMouseWheelScrollsViewport(t *testing.T) {
	m := Model{
		viewport: viewport.New(40, 4),
	}
	m.viewport.SetContent(strings.Join([]string{
		"line 1",
		"line 2",
		"line 3",
		"line 4",
		"line 5",
		"line 6",
		"line 7",
		"line 8",
	}, "\n"))

	updated, cmd := m.Update(tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonWheelDown,
	})
	next := updated.(Model)
	if cmd != nil {
		t.Fatal("expected no command from mouse wheel scroll")
	}
	if next.viewport.YOffset == 0 {
		t.Fatalf("expected viewport to scroll, got y offset %d", next.viewport.YOffset)
	}
}

func TestEventMsgReloadsTranscriptBeforeTurnCompletes(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	session, err := st.CreateSession(context.Background(), "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := st.AddMessage(context.Background(), session.ID, domain.MessageRoleTool, "bash")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddPart(context.Background(), msg.ID, domain.PartKindToolOutput, "file-a\nfile-b", ""); err != nil {
		t.Fatal(err)
	}

	m := Model{
		store:          st,
		currentSession: session,
		parts:          map[int64][]domain.Part{},
	}
	events := make(chan domain.Event)
	defer close(events)

	updated, cmd := m.Update(eventMsg{
		event:  domain.Event{Kind: domain.EventKindToolResult, Tool: domain.ToolKindBash, Text: "file-a\nfile-b"},
		events: events,
	})
	next := updated.(Model)
	if next.status != "Tool bash finished" {
		t.Fatalf("unexpected status: %q", next.status)
	}
	if cmd == nil {
		t.Fatal("expected reload command")
	}
	msgAny := cmd()
	batch, ok := msgAny.(tea.BatchMsg)
	if !ok {
		t.Fatalf("expected tea.BatchMsg, got %T", msgAny)
	}
	var load loadMsg
	found := false
	for _, cmd := range batch {
		if cmd == nil {
			continue
		}
		candidate, ok := cmd().(loadMsg)
		if !ok {
			continue
		}
		load = candidate
		found = true
		break
	}
	if !found {
		t.Fatalf("expected batched loadMsg, got %#v", batch)
	}
	if len(load.messages) != 1 {
		t.Fatalf("expected one reloaded message, got %d", len(load.messages))
	}
	if got := load.parts[load.messages[0].ID][0].Body; got != "file-a\nfile-b" {
		t.Fatalf("unexpected reloaded tool output: %q", got)
	}
}

func TestRenderTranscriptMessageUsesUserStyleWithoutRoleLabel(t *testing.T) {
	cfg := config.Default()
	m := Model{
		cfg: cfg,
		parts: map[int64][]domain.Part{
			1: {{Kind: domain.PartKindText, Body: "hello world"}},
		},
	}

	got := m.renderTranscriptMessage(domain.Message{
		ID:   1,
		Role: domain.MessageRoleUser,
	})

	if !strings.Contains(got, "hello world") {
		t.Fatalf("expected user body in transcript, got %q", got)
	}
	if strings.Contains(got, "[user]") || strings.Contains(got, "[assistant]") {
		t.Fatalf("expected no bracketed role labels, got %q", got)
	}
}

func TestRenderTranscriptMessageUserBubbleHasBlankPaddingLines(t *testing.T) {
	cfg := config.Default()
	m := Model{
		cfg: cfg,
		parts: map[int64][]domain.Part{
			1: {{Kind: domain.PartKindText, Body: "hello world"}},
		},
		viewport: viewport.New(24, 6),
	}

	got := m.renderTranscriptMessage(domain.Message{
		ID:   1,
		Role: domain.MessageRoleUser,
	})

	lines := strings.Split(got, "\n")
	if len(lines) < 3 {
		t.Fatalf("expected padded user bubble, got %q", got)
	}
	if !strings.Contains(lines[0], "▄") {
		t.Fatalf("expected half-block top line, got %q", lines[0])
	}
	if !strings.Contains(lines[1], "█") || strings.TrimSpace(strings.ReplaceAll(lines[1], "█", "")) != "hello world" {
		t.Fatalf("expected padded body line, got %q", lines[1])
	}
	if !strings.Contains(lines[len(lines)-1], "▀") {
		t.Fatalf("expected half-block bottom line, got %q", lines[len(lines)-1])
	}
	wantWidth := lipgloss.Width(lines[1])
	if wantWidth <= 2 {
		t.Fatalf("expected padded width, got %d from %q", wantWidth, lines[1])
	}
	if got := lipgloss.Width(lines[0]); got != wantWidth {
		t.Fatalf("expected blank top line width %d, got %d", wantWidth, got)
	}
	if got := lipgloss.Width(lines[len(lines)-1]); got != wantWidth {
		t.Fatalf("expected blank bottom line width %d, got %d", wantWidth, got)
	}
}

func TestRenderTranscriptMessageUserBubbleUsesConsistentWidthForMultilineInput(t *testing.T) {
	cfg := config.Default()
	m := Model{
		cfg: cfg,
		parts: map[int64][]domain.Part{
			1: {{Kind: domain.PartKindText, Body: "short\nthis is a much longer line"}},
		},
		viewport: viewport.New(30, 6),
	}

	got := m.renderTranscriptMessage(domain.Message{
		ID:   1,
		Role: domain.MessageRoleUser,
	})

	lines := strings.Split(got, "\n")
	if len(lines) < 4 {
		t.Fatalf("expected multiline bubble, got %q", got)
	}
	wantWidth := lipgloss.Width(lines[1])
	for idx, line := range lines {
		if gotWidth := lipgloss.Width(line); gotWidth != wantWidth {
			t.Fatalf("expected consistent line width %d at line %d, got %d from %q", wantWidth, idx, gotWidth, line)
		}
	}
}

func TestRenderTranscriptMessageUserBubbleWrapsToViewportWidth(t *testing.T) {
	cfg := config.Default()
	m := Model{
		cfg: cfg,
		parts: map[int64][]domain.Part{
			1: {{Kind: domain.PartKindText, Body: "this line is intentionally longer than the viewport width"}},
		},
		viewport: viewport.New(18, 6),
	}

	got := m.renderTranscriptMessage(domain.Message{
		ID:   1,
		Role: domain.MessageRoleUser,
	})

	lines := strings.Split(got, "\n")
	if len(lines) < 4 {
		t.Fatalf("expected wrapped bubble lines, got %q", got)
	}
	wantWidth := lipgloss.Width(lines[0])
	for idx, line := range lines {
		if gotWidth := lipgloss.Width(line); gotWidth != wantWidth {
			t.Fatalf("expected wrapped line width %d at line %d, got %d from %q", wantWidth, idx, gotWidth, line)
		}
	}
}

func TestRenderTranscriptMessageUsesAssistantStyleWithoutRoleLabel(t *testing.T) {
	m := Model{
		parts: map[int64][]domain.Part{
			2: {{Kind: domain.PartKindText, Body: "final answer"}},
		},
	}

	got := m.renderTranscriptMessage(domain.Message{
		ID:   2,
		Role: domain.MessageRoleAssistant,
	})

	if !strings.Contains(got, "final answer") {
		t.Fatalf("expected assistant body in transcript, got %q", got)
	}
	if strings.Contains(got, "[user]") || strings.Contains(got, "[assistant]") {
		t.Fatalf("expected no bracketed role labels, got %q", got)
	}
}

func TestRenderTranscriptMessageAssistantWrapsToViewportWidth(t *testing.T) {
	m := Model{
		parts: map[int64][]domain.Part{
			2: {{Kind: domain.PartKindText, Body: "this assistant line is intentionally longer than the viewport width"}},
		},
		viewport: viewport.New(18, 6),
	}

	got := m.renderTranscriptMessage(domain.Message{
		ID:   2,
		Role: domain.MessageRoleAssistant,
	})

	lines := strings.Split(got, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected wrapped assistant lines, got %q", got)
	}
	for idx, line := range lines {
		if gotWidth := lipgloss.Width(line); gotWidth > m.viewport.Width {
			t.Fatalf("expected line width <= %d at line %d, got %d from %q", m.viewport.Width, idx, gotWidth, line)
		}
	}
}

func TestRenderTranscriptMessageAssistantPreservesPlainTextContent(t *testing.T) {
	m := Model{
		palette: theme.Default().Palette,
		parts: map[int64][]domain.Part{
			2: {{Kind: domain.PartKindText, Body: "plain assistant text"}},
		},
	}

	got := m.renderTranscriptMessage(domain.Message{
		ID:   2,
		Role: domain.MessageRoleAssistant,
	})

	if !strings.Contains(got, "plain assistant text") {
		t.Fatalf("expected assistant text to remain visible, got %q", got)
	}
}

func TestRefreshViewportUsesSingleNewlineBetweenBlocksWithHalfBlocks(t *testing.T) {
	cfg := config.Default()
	m := Model{
		cfg: cfg,
		messages: []domain.Message{
			{ID: 1, Role: domain.MessageRoleUser},
			{ID: 2, Role: domain.MessageRoleAssistant},
		},
		parts: map[int64][]domain.Part{
			1: {{Kind: domain.PartKindText, Body: "hello"}},
			2: {{Kind: domain.PartKindText, Body: "reply"}},
		},
		viewport: viewport.New(24, 8),
	}

	m.refreshViewport()
	got := m.viewport.View()
	if strings.Contains(got, "▀\n\nreply") {
		t.Fatalf("expected no extra blank line between user bubble and assistant reply, got %q", got)
	}
	if !strings.Contains(got, "▀\nreply") {
		t.Fatalf("expected single newline between user bubble and assistant reply, got %q", got)
	}
}
