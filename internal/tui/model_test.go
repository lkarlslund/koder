package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/theme"
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
	m := Model{
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
	if cmd != nil {
		t.Fatal("expected no follow-up command on immediate prompt error")
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

	m, err := New(cfg, nil, nil, StartupModeNew)
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

func TestSessionPickerEscapeCreatesNewSession(t *testing.T) {
	m := Model{
		composer: textarea.New(),
		picker: pickerModel{
			visible: true,
			mode:    pickerModeSession,
		},
		sessions: []domain.Session{{ID: 1}},
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

func TestUpdateLoadHidesSessionPicker(t *testing.T) {
	m := Model{
		picker: pickerModel{
			visible: true,
			mode:    pickerModeSession,
		},
	}

	updated := m.UpdateLoad(loadMsg{
		current: domain.Session{ID: 4},
	})

	if updated.hasPicker() {
		t.Fatal("expected picker to close after loading a session")
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

	m, err := New(cfg, nil, nil, StartupModeNew)
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

	m, err := New(cfg, nil, nil, StartupModeNew)
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

	m, err := New(cfg, nil, nil, StartupModeNew)
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
	if !strings.Contains(got, "Context") || !strings.Contains(got, "25% used") {
		t.Fatalf("expected sidebar to include context usage, got %q", got)
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
	if !strings.Contains(got, "Working ...") || !strings.Contains(got, "[=") {
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
	if !strings.Contains(lines[1], "▌") {
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
	if !strings.Contains(lines[1], "▌") {
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
	if strings.Contains(got, "▄") || strings.Contains(got, "▀") || strings.Contains(got, "▌") {
		t.Fatalf("expected classic user message rendering when half blocks disabled, got %q", got)
	}
	if !strings.Contains(got, "┃") {
		t.Fatalf("expected classic accent bar when half blocks disabled, got %q", got)
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
	if got := m.renderTranscriptActivity(); !strings.Contains(got, "Working ...") || !strings.Contains(got, "[=") {
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
	m, err := New(cfg, nil, nil, StartupModeNew)
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
	if !strings.Contains(lines[1], "▌") || strings.TrimSpace(strings.ReplaceAll(lines[1], "▌", "")) != "hello world" {
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
