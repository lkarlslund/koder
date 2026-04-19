package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/store"
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
		composer:      textarea.New(),
		pickerVisible: true,
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

func TestUpdateLoadHidesSessionPicker(t *testing.T) {
	m := Model{
		pickerVisible: true,
	}

	updated := m.UpdateLoad(loadMsg{
		current: domain.Session{ID: 4},
	})

	if updated.pickerVisible {
		t.Fatal("expected picker to close after loading a session")
	}
	if updated.currentSession.ID != 4 {
		t.Fatalf("unexpected current session: %#v", updated.currentSession)
	}
}

func TestWorkingIndicatorShownWhenModelWorking(t *testing.T) {
	m := Model{
		modelWorking: true,
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
		modelWorking:   true,
		workdir:        "/tmp/project",
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
}

func TestRefreshViewportAppendsWorkingLine(t *testing.T) {
	m := Model{
		currentSession: domain.Session{ID: 1},
		modelWorking:   true,
		status:         "Working ...",
		parts:          map[int64][]domain.Part{},
		viewport:       viewport.New(40, 6),
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

func TestRefreshViewportOmitsWorkingLineForGenericLoading(t *testing.T) {
	m := Model{
		currentSession: domain.Session{ID: 1},
		loading:        true,
		status:         "Resuming session 2…",
		parts:          map[int64][]domain.Part{},
		viewport:       viewport.New(40, 6),
	}

	m.refreshViewport()
	got := m.viewport.View()
	if strings.Contains(got, "Resuming session 2") || strings.Contains(got, "[=") {
		t.Fatalf("expected no model activity line for generic loading, got %q", got)
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
	m := Model{
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
	m := Model{
		parts: map[int64][]domain.Part{
			1: {{Kind: domain.PartKindText, Body: "hello world"}},
		},
	}

	got := m.renderTranscriptMessage(domain.Message{
		ID:   1,
		Role: domain.MessageRoleUser,
	})

	lines := strings.Split(got, "\n")
	if len(lines) < 3 {
		t.Fatalf("expected padded user bubble, got %q", got)
	}
	if strings.TrimSpace(lines[0]) != "" {
		t.Fatalf("expected blank top line, got %q", lines[0])
	}
	if strings.TrimSpace(lines[1]) != "hello world" {
		t.Fatalf("expected padded body line, got %q", lines[1])
	}
	if strings.TrimSpace(lines[len(lines)-1]) != "" {
		t.Fatalf("expected blank bottom line, got %q", lines[len(lines)-1])
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
