package tui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/lkarlslund/koder/internal/agent"
	"github.com/lkarlslund/koder/internal/attachment"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/debugsrv"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/permission"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/reference"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/theme"
	"github.com/lkarlslund/koder/internal/tools"
	"github.com/lkarlslund/koder/internal/tui/dialogs"
	"github.com/lkarlslund/koder/internal/ui"
	"github.com/lkarlslund/koder/internal/ui/textarea"
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
	if len(matches) != 1 || matches[0].Name != "/permissions" {
		t.Fatalf("expected /permissions, got %#v", matches)
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

	matches = matchingSlashCommands("ski")
	if len(matches) != 1 || matches[0].Name != "/skills" {
		t.Fatalf("expected /skills, got %#v", matches)
	}

	matches = matchingSlashCommands("rea")
	if len(matches) != 0 {
		t.Fatalf("expected tool slash commands to stay hidden, got %#v", matches)
	}
}

func TestFooterOnlyComposerUpdatesKeepBodyCache(t *testing.T) {
	m := Model{
		cfg:         config.Default().WithStateDir(t.TempDir()),
		palette:     theme.Default().Palette,
		viewport:    newTranscriptViewport(80, 20),
		renderCache: &modelRenderCache{},
		composer:    textarea.New(),
		width:       80,
		height:      24,
	}
	m.composer.SetValue("draft text")

	_ = m.ViewLines()
	if !m.ensureRenderCache().bodyValid {
		t.Fatal("expected body cache to be primed")
	}
	if !m.ensureRenderCache().composerAreaValid {
		t.Fatal("expected composer area cache to be primed")
	}

	nextModel, _ := m.handleKey(ui.KeyMsg{Type: ui.KeyLeft})
	next := nextModel.(*Model)

	if !next.ensureRenderCache().bodyValid {
		t.Fatal("expected footer-only composer update to keep body cache valid")
	}
	if next.ensureRenderCache().composerAreaValid {
		t.Fatal("expected composer-only update to invalidate composer area cache")
	}
}

func TestSkillQuery(t *testing.T) {
	query, start, ok := skillQuery("Investigate $rev")
	if !ok || query != "rev" {
		t.Fatalf("unexpected skill query: ok=%v query=%q start=%d", ok, query, start)
	}
	if start != len("Investigate ") {
		t.Fatalf("unexpected start index %d", start)
	}
	if _, _, ok := skillQuery("Investigate$rev"); ok {
		t.Fatal("expected no skill query when $ is embedded in a word")
	}
	if _, _, ok := skillQuery("Investigate $rev more"); ok {
		t.Fatal("expected no skill query when token is not at end")
	}
}

func TestMentionQuery(t *testing.T) {
	query, start, pathMode, ok := mentionQuery("inspect @rea", len("inspect @rea"))
	if !ok || query != "rea" || pathMode {
		t.Fatalf("unexpected mention query: ok=%v query=%q pathMode=%v start=%d", ok, query, pathMode, start)
	}
	if start != len("inspect ") {
		t.Fatalf("unexpected mention start %d", start)
	}
	query, _, pathMode, ok = mentionQuery("inspect @./cmd/ko", len("inspect @./cmd/ko"))
	if !ok || query != "./cmd/ko" || !pathMode {
		t.Fatalf("unexpected path mention query: ok=%v query=%q pathMode=%v", ok, query, pathMode)
	}
}

func TestAcceptMentionSelectionInsertsStructuredReference(t *testing.T) {
	workdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workdir, "README.md"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	composer := textarea.New()
	composer.SetValue("inspect @rea")
	composer.SetCursor(len("inspect @rea"))
	m := Model{
		cfg:            testConfig(t),
		composer:       composer,
		workdir:        workdir,
		mentionMatches: []reference.Entry{{Kind: reference.KindFile, Path: "README.md", Display: "README.md", Description: "file"}},
	}
	m.acceptMentionSelection()
	if got := m.composer.Value(); got != "inspect @README.md" {
		t.Fatalf("unexpected composer value %q", got)
	}
	if len(m.draftReferences) != 1 {
		t.Fatalf("expected one structured reference, got %#v", m.draftReferences)
	}
	ref := m.draftReferences[0]
	if ref.Path != "README.md" || ref.Display != "@README.md" {
		t.Fatalf("unexpected reference %#v", ref)
	}
	if ref.Start != len("inspect ") || ref.End != len("inspect @README.md") {
		t.Fatalf("unexpected reference offsets %#v", ref)
	}
}

func TestHandleLocalCommandOpensPermissionsPicker(t *testing.T) {
	m := Model{
		cfg:      testConfig(t),
		composer: textarea.New(),
	}
	model, cmd, ok := m.handleLocalCommand("/permissions")
	if !ok {
		t.Fatal("expected local command to be handled")
	}
	if cmd == nil {
		t.Fatal("expected sync title command")
	}
	next := model.(*Model)
	if !next.hasPicker() {
		t.Fatal("expected permissions picker to open")
	}
	if next.picker.mode != pickerModePermissions {
		t.Fatalf("expected permissions picker mode, got %v", next.picker.mode)
	}
}

func TestHandleLocalCommandOpensSkillsPicker(t *testing.T) {
	workdir := newSkillRepo(t)
	m := Model{
		cfg:      testConfig(t),
		composer: textarea.New(),
		workdir:  workdir,
	}
	model, cmd, ok := m.handleLocalCommand("/skills")
	if !ok {
		t.Fatal("expected local command to be handled")
	}
	if cmd == nil {
		t.Fatal("expected sync title command")
	}
	next := model.(*Model)
	if !next.hasPicker() {
		t.Fatal("expected skills picker to open")
	}
	if next.picker.mode != pickerModeSkills {
		t.Fatalf("expected skills picker mode, got %v", next.picker.mode)
	}
	if len(next.picker.dialog.Items) != 1 || next.picker.dialog.Items[0].Value != "review" {
		t.Fatalf("unexpected picker items: %#v", next.picker.dialog.Items)
	}
}

func TestSkillAutocompleteAcceptsSelection(t *testing.T) {
	workdir := newSkillRepo(t)
	m := Model{
		cfg:      testConfig(t),
		composer: textarea.New(),
		workdir:  workdir,
	}
	m.composer.SetValue("Use $rev")
	m.updateComposerMenus()
	if !m.hasSkillMenu() {
		t.Fatal("expected skill menu")
	}

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyTab})
	next := updated.(*Model)
	if cmd != nil {
		t.Fatal("expected no async command")
	}
	if got := next.composer.Value(); got != "Use $review" {
		t.Fatalf("expected completed skill mention, got %q", got)
	}
	if next.hasSkillMenu() {
		t.Fatal("expected skill menu to close after selection")
	}
}

func TestSkillsPickerSelectionInsertsToken(t *testing.T) {
	workdir := newSkillRepo(t)
	m := Model{
		cfg:      testConfig(t),
		composer: textarea.New(),
		workdir:  workdir,
	}
	m.openSkillsPicker()
	model, cmd := m.submitPickerSelection("review")
	next := model.(*Model)
	if cmd == nil {
		t.Fatal("expected sync title command")
	}
	if got := next.composer.Value(); got != "$review" {
		t.Fatalf("expected inserted skill token, got %q", got)
	}
	if next.hasPicker() {
		t.Fatal("expected picker to close after selection")
	}
}

func TestPermissionsCommandOpensWhileBusy(t *testing.T) {
	m := Model{
		cfg:      testConfig(t),
		composer: textarea.New(),
		loading:  true,
		busy: busyModel{
			active: true,
			scope:  busyScopeTranscript,
		},
	}
	m.composer.SetValue("/permissions")
	m.updateComposerMenus()

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected permissions command to execute while busy")
	}
	if !next.hasPicker() {
		t.Fatal("expected permissions picker to open while busy")
	}
	if next.queuedPrompt != nil {
		t.Fatalf("expected no queued prompt, got %#v", next.queuedPrompt)
	}
}

func TestPermissionsPickerSelectionUpdatesDraftSession(t *testing.T) {
	m := Model{
		cfg:      testConfig(t),
		composer: textarea.New(),
	}
	m.openPermissionsPicker()
	for idx, item := range m.picker.dialog.Items {
		if item.Value == permission.ProfileWriteAsk {
			m.picker.dialog.Index = idx
			break
		}
	}
	model, _ := m.submitPickerSelection(permission.ProfileWriteAsk)
	next := model.(*Model)
	if next.currentSession.PermissionProfile != permission.ProfileWriteAsk {
		t.Fatalf("expected draft session permission profile updated, got %q", next.currentSession.PermissionProfile)
	}
	if !strings.Contains(next.pendingModelNote, "Permission mode changed to write / ask.") {
		t.Fatalf("expected pending model note, got %q", next.pendingModelNote)
	}
	if next.hasPicker() {
		t.Fatal("expected picker to close after selection")
	}
}

func TestEnterShowsOptimisticUserPromptBeforePromptStarts(t *testing.T) {
	cfg := testConfig(t)
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "model"
	cfg.Providers = map[string]config.Provider{
		"test": {BaseURL: "https://example.invalid/v1", APIKey: "secret", DefaultModel: "model"},
	}
	m := Model{
		cfg:            cfg,
		composer:       textarea.New(),
		palette:        theme.Resolve("tokyonight").Palette,
		viewport:       newTranscriptViewport(60, 10),
		currentSession: domain.Session{ID: 1, ProviderID: "test", ModelID: "model"},
		parts:          map[int64][]domain.Part{},
	}
	m.composer.SetValue("hello there")

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected prompt kickoff command")
	}
	if len(next.messages) != 1 {
		t.Fatalf("expected optimistic user message, got %#v", next.messages)
	}
	if next.messages[0].Role != domain.MessageRoleUser || next.messages[0].Summary != "hello there" {
		t.Fatalf("unexpected optimistic message: %#v", next.messages[0])
	}
	if got := next.viewport.View(); !strings.Contains(got, "hello there") {
		t.Fatalf("expected viewport to show optimistic user prompt, got %q", got)
	}

	msg := cmd()
	if _, ok := msg.(kickoffPromptMsg); !ok {
		t.Fatalf("expected kickoffPromptMsg before async prompt starts, got %#v", msg)
	}
}

func testConfig(t *testing.T) config.Config {
	t.Helper()
	return config.Default().WithStateDir(t.TempDir())
}

func asModelPtr(t *testing.T, model ui.Model) *Model {
	t.Helper()
	switch next := model.(type) {
	case *Model:
		return next
	case Model:
		return &next
	default:
		t.Fatalf("unexpected model type %T", model)
		return nil
	}
}

func mustMarshalMeta(t *testing.T, meta map[string]string) string {
	t.Helper()
	raw, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal meta: %v", err)
	}
	return string(raw)
}

func newSkillRepo(t *testing.T) string {
	t.Helper()
	return newSkillRepoTB(t)
}

func newSkillRepoTB(tb testing.TB) string {
	tb.Helper()
	home := tb.TempDir()
	if setter, ok := any(tb).(interface{ Setenv(string, string) }); ok {
		setter.Setenv("HOME", home)
	}

	repo := filepath.Join(tb.TempDir(), "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		tb.Fatal(err)
	}
	path := filepath.Join(repo, ".agents", "skills", "review", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("---\nname: review\ndescription: Review code carefully\n---\n"), 0o644); err != nil {
		tb.Fatal(err)
	}
	return repo
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

func TestComposerQueryHelpers(t *testing.T) {
	composer := textarea.New()
	composer.SetValue("inspect @./cmd/ko")
	composer.SetCursor(len("inspect @./cmd/ko"))

	slash, ok := slashQueryFromComposer(composer)
	if ok || slash != "" {
		t.Fatalf("unexpected slash query: ok=%v query=%q", ok, slash)
	}

	query, start, pathMode, ok := mentionQuery(composer.Value(), len(composer.Value()))
	if !ok {
		t.Fatal("expected string mention query")
	}
	composerQuery, composerStart, composerEnd, composerPathMode, composerOK := mentionQueryFromComposer(composer)
	if !composerOK {
		t.Fatal("expected composer mention query")
	}
	if composerQuery != query || composerStart != start || !composerPathMode || composerPathMode != pathMode {
		t.Fatalf("unexpected composer mention query: got %q %d %v want %q %d %v", composerQuery, composerStart, composerPathMode, query, start, pathMode)
	}
	if composerEnd != len(composer.Value()) {
		t.Fatalf("unexpected mention end: got %d want %d", composerEnd, len(composer.Value()))
	}

	composer.SetValue("Investigate $rev")
	composer.SetCursor(len("Investigate $rev"))
	query, start, ok = skillQuery(composer.Value())
	if !ok {
		t.Fatal("expected string skill query")
	}
	composerQuery, composerStart, composerOK2 := skillQueryFromComposer(composer)
	if !composerOK2 {
		t.Fatal("expected composer skill query")
	}
	if composerQuery != query || composerStart != start {
		t.Fatalf("unexpected composer skill query: got %q %d want %q %d", composerQuery, composerStart, query, start)
	}

	composer.SetValue("/new")
	composer.SetCursor(len("/new"))
	query, ok = slashQuery(composer.Value())
	if !ok {
		t.Fatal("expected string slash query")
	}
	composerQuery, composerOK2 = slashQueryFromComposer(composer)
	if !composerOK2 || composerQuery != query {
		t.Fatalf("unexpected composer slash query: got %q %v want %q true", composerQuery, composerOK2, query)
	}
}

func TestEnterSendsNormalPrompt(t *testing.T) {
	cfg := testConfig(t)
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

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
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
	cfg := testConfig(t)
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

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter, Alt: true})
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
		attachmentFiles:   attachment.NewManager(t.TempDir()),
		readClipboardText: func() (string, error) { return "pasted text", nil },
	}
	m.composer.SetValue("hello ")
	m.composer.SetCursor(len("hello "))

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyCtrlV})
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

func TestCtrlVPastesClipboardImageAsAttachment(t *testing.T) {
	m := Model{
		composer:          textarea.New(),
		attachmentFiles:   attachment.NewManager(t.TempDir()),
		readClipboardText: func() (string, error) { return "", nil },
		readClipboardImage: func() ([]byte, error) {
			return []byte("\x89PNG\r\n\x1a\nfake"), nil
		},
	}

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyCtrlV})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected title sync command after image attach")
	}
	if len(next.draftAttachments) != 1 {
		t.Fatalf("expected one draft attachment, got %#v", next.draftAttachments)
	}
	if next.composer.Value() != "" {
		t.Fatalf("expected composer text to stay empty, got %q", next.composer.Value())
	}
	if !strings.Contains(next.status, "Attached image") {
		t.Fatalf("unexpected attach status: %q", next.status)
	}
}

func TestCtrlVPastesAttachmentFilePath(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "note.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := Model{
		composer:          textarea.New(),
		attachmentFiles:   attachment.NewManager(root),
		readClipboardText: func() (string, error) { return path, nil },
		readClipboardImage: func() ([]byte, error) {
			return nil, nil
		},
	}

	updated, _ := m.handleKey(ui.KeyMsg{Type: ui.KeyCtrlV})
	next := updated.(*Model)
	if len(next.draftAttachments) != 1 {
		t.Fatalf("expected one draft attachment, got %#v", next.draftAttachments)
	}
	if next.draftAttachments[0].Name != "note.txt" {
		t.Fatalf("unexpected attachment name: %#v", next.draftAttachments[0])
	}
}

func TestBackspaceRemovesLastDraftAttachmentWhenComposerEmpty(t *testing.T) {
	root := t.TempDir()
	m := Model{
		composer:        textarea.New(),
		attachmentFiles: attachment.NewManager(root),
		draftAttachments: []attachment.Draft{{
			Metadata: attachment.Metadata{Name: "note.txt", MIME: "text/plain", Path: filepath.Join(root, "note.txt")},
		}},
	}

	updated, _ := m.handleKey(ui.KeyMsg{Type: ui.KeyBackspace})
	next := updated.(*Model)
	if len(next.draftAttachments) != 0 {
		t.Fatalf("expected attachment to be removed, got %#v", next.draftAttachments)
	}
}

func TestForkSessionCopiesAttachmentFiles(t *testing.T) {
	root := t.TempDir()
	st, err := store.OpenWithOptions(root, store.Options{Backend: store.BackendPebble})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	manager := attachment.NewManager(root)
	cfg := testConfig(t)
	cfg.DefaultProvider = "openai"
	cfg.DefaultModel = "gpt-5.4"
	cfg.Providers = map[string]config.Provider{
		"openai": {BaseURL: "https://api.openai.com/v1", APIKey: "secret", DefaultModel: "gpt-5.4"},
	}
	m := Model{
		cfg:             cfg,
		store:           st,
		attachmentFiles: manager,
		workdir:         root,
	}

	session, err := st.CreateSession(context.Background(), "test", "openai", "gpt-5.4", nil)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := st.AddMessage(context.Background(), session.ID, domain.MessageRoleUser, "hello")
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(root, "note.txt")
	if err := os.WriteFile(src, []byte("fork me"), 0o644); err != nil {
		t.Fatal(err)
	}
	draft, err := manager.ImportFile(src)
	if err != nil {
		t.Fatal(err)
	}
	meta, err := manager.AdoptDraft(draft, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := attachment.EncodeMeta(meta)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddPart(context.Background(), msg.ID, domain.PartKindAttachment, meta.Name, raw); err != nil {
		t.Fatal(err)
	}

	msgAny := m.forkSessionCmd(session.ID)()
	forked, ok := msgAny.(forkSessionMsg)
	if !ok {
		t.Fatalf("unexpected fork message: %#v", msgAny)
	}
	if forked.err != nil {
		t.Fatal(forked.err)
	}
	if forked.forkedID == session.ID {
		t.Fatal("expected distinct forked session id")
	}
	forkParts := forked.load.parts[forked.load.messages[0].ID]
	if len(forkParts) != 1 {
		t.Fatalf("unexpected forked parts: %#v", forkParts)
	}
	forkMeta, err := attachment.DecodeMeta(forkParts[0].MetaJSON)
	if err != nil {
		t.Fatal(err)
	}
	if forkMeta.Path == meta.Path {
		t.Fatalf("expected copied attachment path, got %q", forkMeta.Path)
	}
	if _, err := os.Stat(forkMeta.Path); err != nil {
		t.Fatalf("expected copied attachment file: %v", err)
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

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyCtrlY})
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

func TestEnterWhileBusyQueuesSteeringPrompt(t *testing.T) {
	cfg := testConfig(t)
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

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected title sync command after queueing")
	}
	if next.queuedPrompt == nil || next.queuedPrompt.Text != "follow up" || next.queuedPrompt.Mode != queuedPromptModeSteer {
		t.Fatalf("expected queued prompt, got %#v", next.queuedPrompt)
	}
	if next.composer.Value() != "" {
		t.Fatalf("expected composer reset after queueing, got %q", next.composer.Value())
	}
	if len(next.messages) != 0 {
		t.Fatalf("expected no optimistic send while busy, got %#v", next.messages)
	}
}

func TestUpDownBrowseComposerPromptHistory(t *testing.T) {
	m := Model{
		composer: textarea.New(),
		messages: []domain.Message{
			{ID: 1, Role: domain.MessageRoleUser, Summary: "first"},
			{ID: 2, Role: domain.MessageRoleAssistant, Summary: "reply"},
			{ID: 3, Role: domain.MessageRoleUser, Summary: "second"},
		},
		parts: map[int64][]domain.Part{},
	}
	m.composer.SetValue("draft")

	updated, _ := m.handleKey(ui.KeyMsg{Type: ui.KeyUp})
	next := updated.(*Model)
	if got := next.composer.Value(); got != "second" {
		t.Fatalf("expected newest history entry, got %q", got)
	}

	updated, _ = next.handleKey(ui.KeyMsg{Type: ui.KeyUp})
	next = updated.(*Model)
	if got := next.composer.Value(); got != "first" {
		t.Fatalf("expected older history entry, got %q", got)
	}

	updated, _ = next.handleKey(ui.KeyMsg{Type: ui.KeyDown})
	next = updated.(*Model)
	if got := next.composer.Value(); got != "second" {
		t.Fatalf("expected newer history entry, got %q", got)
	}

	updated, _ = next.handleKey(ui.KeyMsg{Type: ui.KeyDown})
	next = updated.(*Model)
	if got := next.composer.Value(); got != "draft" {
		t.Fatalf("expected draft restored after newest history entry, got %q", got)
	}
}

func TestCtrlROpensComposerHistoryMenuAndAcceptsSelection(t *testing.T) {
	m := Model{
		composer: textarea.New(),
		messages: []domain.Message{
			{ID: 1, Role: domain.MessageRoleUser, Summary: "alpha one"},
			{ID: 2, Role: domain.MessageRoleUser, Summary: "beta two"},
			{ID: 3, Role: domain.MessageRoleUser, Summary: "alpha three"},
		},
		parts: map[int64][]domain.Part{},
	}
	m.composer.SetValue("alpha")

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyCtrlR})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected title sync command after opening history search")
	}
	if !next.hasComposerHistoryMenu() {
		t.Fatal("expected composer history menu to open")
	}
	if got := next.composer.Value(); got != "alpha" {
		t.Fatalf("expected draft to remain in composer while searching, got %q", got)
	}
	if got := next.renderFooter(); !strings.Contains(got, "History") || !strings.Contains(got, "alpha three") {
		t.Fatalf("expected history menu in footer, got %q", got)
	}

	updated, _ = next.handleKey(ui.KeyMsg{Type: ui.KeyCtrlR})
	next = updated.(*Model)
	if got := next.filteredComposerHistory(next.composerHistory.SearchQuery)[next.composerHistory.SearchIndex]; got != "alpha one" {
		t.Fatalf("expected ctrl-r to move to earlier matching history entry, got %q", got)
	}

	updated, cmd = next.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next = updated.(*Model)
	if cmd == nil {
		t.Fatal("expected title sync command after accepting history selection")
	}
	if next.hasComposerHistoryMenu() {
		t.Fatal("expected history menu to close after selection")
	}
	if got := next.composer.Value(); got != "alpha one" {
		t.Fatalf("expected selected history entry in composer, got %q", got)
	}
}

func TestComposerHistoryMenuFiltersWithoutMutatingComposer(t *testing.T) {
	m := Model{
		composer: textarea.New(),
		messages: []domain.Message{
			{ID: 1, Role: domain.MessageRoleUser, Summary: "first deploy"},
			{ID: 2, Role: domain.MessageRoleUser, Summary: "second status"},
		},
		parts: map[int64][]domain.Part{},
	}
	m.composer.SetValue("")

	updated, _ := m.handleKey(ui.KeyMsg{Type: ui.KeyCtrlR})
	next := updated.(*Model)
	updated, _ = next.handleKey(ui.KeyMsg{Type: ui.KeyRunes, Runes: []rune("status")})
	next = updated.(*Model)

	if !next.hasComposerHistoryMenu() {
		t.Fatal("expected history menu to remain open while filtering")
	}
	if got := next.composer.Value(); got != "" {
		t.Fatalf("expected composer draft to remain unchanged while filtering, got %q", got)
	}
	matches := next.filteredComposerHistory(next.composerHistory.SearchQuery)
	if len(matches) != 1 || matches[0] != "second status" {
		t.Fatalf("expected filtered history match, got %#v", matches)
	}
}

func TestTabWhileBusyQueuesSteeringPrompt(t *testing.T) {
	cfg := testConfig(t)
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

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyTab})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected title sync command after steering queue")
	}
	if next.queuedPrompt == nil || next.queuedPrompt.Mode != queuedPromptModeSteer {
		t.Fatalf("expected steering queue, got %#v", next.queuedPrompt)
	}
}

func TestCtrlGQueuesContinueWhileBusy(t *testing.T) {
	cfg := testConfig(t)
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
		loading:        true,
		currentSession: domain.Session{ID: 9, ProviderID: "openai", ModelID: "gpt-5.4"},
		busy: busyModel{
			active: true,
			scope:  busyScopeTranscript,
		},
	}

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyCtrlG})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected title sync command after queueing continue")
	}
	if next.queuedPrompt == nil || next.queuedPrompt.Mode != queuedPromptModeContinue {
		t.Fatalf("expected queued continue, got %#v", next.queuedPrompt)
	}
}

func TestCtrlGStartsContinueWhenIdle(t *testing.T) {
	cfg := testConfig(t)
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
		currentSession: domain.Session{ID: 9, ProviderID: "openai", ModelID: "gpt-5.4"},
	}

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyCtrlG})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected continue command")
	}
	if !next.loading {
		t.Fatal("expected loading after continue hotkey")
	}
}

func TestLoadMsgDispatchesQueuedPrompt(t *testing.T) {
	cfg := testConfig(t)
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
		viewport:       newTranscriptViewport(40, 6),
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

func TestAltUpRestoresQueuedPromptToComposer(t *testing.T) {
	m := Model{
		cfg:      testConfig(t),
		composer: textarea.New(),
		queuedPrompt: &queuedPrompt{
			Text: "queued ask",
			Mode: queuedPromptModeNormal,
		},
	}

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyUp, Alt: true})
	next := updated.(*Model)

	if cmd == nil {
		t.Fatal("expected title sync command after restoring queued prompt")
	}
	if next.queuedPrompt != nil {
		t.Fatalf("expected queued prompt cleared, got %#v", next.queuedPrompt)
	}
	if next.composer.Value() != "queued ask" {
		t.Fatalf("expected composer to contain restored queued prompt, got %q", next.composer.Value())
	}
}

func TestAltUpSwapsQueuedPromptWithExistingDraft(t *testing.T) {
	m := Model{
		cfg:      testConfig(t),
		composer: textarea.New(),
		queuedPrompt: &queuedPrompt{
			Text: "queued ask",
			Mode: queuedPromptModeSteer,
		},
	}
	m.setComposerValue("current draft")

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyUp, Alt: true})
	next := updated.(*Model)

	if cmd == nil {
		t.Fatal("expected title sync command after swapping queued prompt")
	}
	if next.composer.Value() != "queued ask" {
		t.Fatalf("expected composer to contain restored queued prompt, got %q", next.composer.Value())
	}
	if next.queuedPrompt == nil {
		t.Fatal("expected previous draft to be re-queued")
	}
	if next.queuedPrompt.Text != "current draft" {
		t.Fatalf("expected current draft to be re-queued, got %#v", next.queuedPrompt)
	}
	if next.queuedPrompt.Mode != queuedPromptModeNormal {
		t.Fatalf("expected swapped draft to be queued as normal follow-up, got %#v", next.queuedPrompt)
	}
}

func TestAltUpClearsQueuedContinue(t *testing.T) {
	m := Model{
		cfg:      testConfig(t),
		composer: textarea.New(),
		queuedPrompt: &queuedPrompt{
			Mode: queuedPromptModeContinue,
		},
	}
	m.setComposerValue("keep draft")

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyUp, Alt: true})
	next := updated.(*Model)

	if cmd == nil {
		t.Fatal("expected title sync command after clearing queued continue")
	}
	if next.queuedPrompt != nil {
		t.Fatalf("expected queued continue cleared, got %#v", next.queuedPrompt)
	}
	if next.composer.Value() != "keep draft" {
		t.Fatalf("expected composer draft unchanged, got %q", next.composer.Value())
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
	cfg := testConfig(t)
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
		viewport:       newTranscriptViewport(40, 6),
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
}

func TestRenderTranscriptToolMessageFallsBackToSummaryWhenBodyMissing(t *testing.T) {
	cfg := testConfig(t)
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

func TestRefreshViewportGroupsToolRunMessagesIntoCard(t *testing.T) {
	cfg := testConfig(t)
	m := Model{
		cfg:     cfg,
		palette: theme.Resolve("tokyonight").Palette,
		parts: map[int64][]domain.Part{
			1: {{
				Kind:     domain.PartKindToolCall,
				Body:     `{"command":"git status","tool":"bash","tool_call_id":"call_1"}`,
				MetaJSON: `{"command":"git status","tool":"bash","tool_call_id":"call_1"}`,
			}},
			2: {{
				Kind:     domain.PartKindApprovalRequest,
				Body:     "Approval required for bash: git status",
				MetaJSON: `{"approval_id":"7","tool":"bash","status":"pending","command":"git status","tool_call_id":"call_1"}`,
			}},
			3: {{
				Kind:     domain.PartKindToolOutput,
				Body:     "On branch main",
				MetaJSON: `{"tool":"bash","tool_call_id":"call_1"}`,
			}},
		},
		messages: []domain.Message{
			{ID: 1, Role: domain.MessageRoleAssistant},
			{ID: 2, Role: domain.MessageRoleTool},
			{ID: 3, Role: domain.MessageRoleTool, Summary: "bash"},
		},
		viewport: newTranscriptViewport(80, 12),
	}

	m.refreshViewport()
	got := m.viewport.View()
	if !strings.Contains(got, "Run command") {
		t.Fatalf("expected grouped tool title in transcript, got %q", got)
	}
	if strings.Contains(got, "│") || strings.Contains(got, "╭") || strings.Contains(got, "╰") {
		t.Fatalf("expected compact tool row without border chrome, got %q", got)
	}
	if !strings.Contains(got, " On branch main") {
		t.Fatalf("expected indented tool output preview, got %q", got)
	}
	if strings.Contains(got, `"tool":"bash"`) || strings.Contains(got, "Approval required for bash") {
		t.Fatalf("expected compact tool card instead of raw transcript blobs, got %q", got)
	}
}

func TestRenderApprovalPromptUsesToolRunDock(t *testing.T) {
	cfg := testConfig(t)
	m := Model{
		cfg:       cfg,
		palette:   theme.Resolve("tokyonight").Palette,
		viewport:  newTranscriptViewport(80, 12),
		approvals: []store.Approval{{ID: 9, Tool: domain.ToolKindBash, Command: `{"command":"git status","tool_call_id":"call_1"}`}},
	}

	got := m.renderApprovalPrompt()
	if !strings.Contains(got, "Run command") || !strings.Contains(got, "Needs approval #9") {
		t.Fatalf("expected typed approval dock, got %q", got)
	}
	if !strings.Contains(got, "Permissions") {
		t.Fatalf("expected permission action in approval dock, got %q", got)
	}
	if strings.Contains(got, `{"command":"git status"`) {
		t.Fatalf("expected approval dock to avoid raw JSON, got %q", got)
	}
}

func TestRefreshViewportSkipsSyntheticAssistantToolSummary(t *testing.T) {
	cfg := testConfig(t)
	m := Model{
		cfg:     cfg,
		palette: theme.Resolve("tokyonight").Palette,
		parts: map[int64][]domain.Part{
			1: {{
				Kind:     domain.PartKindToolCall,
				MetaJSON: `{"path":"README.md","tool":"read","tool_call_id":"call_2"}`,
			}},
		},
		messages: []domain.Message{
			{ID: 1, Role: domain.MessageRoleAssistant, Summary: "tool:read"},
		},
		viewport: newTranscriptViewport(80, 8),
	}

	m.refreshViewport()
	got := m.viewport.View()
	if strings.Contains(got, "tool:read") {
		t.Fatalf("expected synthetic tool summary to stay hidden, got %q", got)
	}
	if !strings.Contains(got, "Read file") || !strings.Contains(got, "README.md") {
		t.Fatalf("expected grouped read tool card, got %q", got)
	}
}

func TestToolOutputUsesRequestPreviewFromMeta(t *testing.T) {
	cfg := testConfig(t)
	m := Model{
		cfg:     cfg,
		palette: theme.Resolve("tokyonight").Palette,
		parts: map[int64][]domain.Part{
			1: {{
				Kind:     domain.PartKindToolOutput,
				Body:     "# heading",
				MetaJSON: `{"tool":"read","path":"README.md","tool_call_id":"call_2"}`,
			}},
		},
		messages: []domain.Message{
			{ID: 1, Role: domain.MessageRoleTool, Summary: "read"},
		},
		viewport: newTranscriptViewport(80, 8),
	}

	m.refreshViewport()
	got := m.viewport.View()
	if !strings.Contains(got, "README.md") {
		t.Fatalf("expected read path from tool output metadata, got %q", got)
	}
	if strings.Contains(got, "\nread\n") {
		t.Fatalf("expected generic summary to be replaced by request preview, got %q", got)
	}
}

func TestEnterWithoutProviderOpensConnectDialog(t *testing.T) {
	m := Model{
		cfg:      config.Default(),
		composer: textarea.New(),
	}
	m.composer.SetValue("hello")

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
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
	m.updateComposerMenus()

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
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
	m.composer.SetValue("/mouse")
	m.updateComposerMenus()

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next := updated.(*Model)
	if cmd != nil {
		t.Fatal("expected no command while autocompleting needs-args slash command")
	}
	if next.loading {
		t.Fatal("expected no loading while autocompleting needs-args slash command")
	}
	if got := next.composer.Value(); got != "/mouse " {
		t.Fatalf("expected /mouse autocompletion, got %q", got)
	}
}

func TestSlashSelectionExecutesNoArgsCommandDirectly(t *testing.T) {
	m := Model{
		composer: textarea.New(),
	}
	m.composer.SetValue("/per")
	m.updateComposerMenus()

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected direct command execution")
	}
	if !next.hasPicker() {
		t.Fatal("expected /permissions to open immediately from slash menu")
	}
	if next.composer.Value() != "" {
		t.Fatalf("expected composer reset after command execution, got %q", next.composer.Value())
	}
}

func TestRunPromptErrorAppendsAssistantErrorToTranscript(t *testing.T) {
	m := Model{
		composer: textarea.New(),
		parts:    map[int64][]domain.Part{},
		viewport: newTranscriptViewport(40, 6),
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
		viewport: newTranscriptViewport(40, 6),
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
	cfg := testConfig(t)
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
		viewport:       newTranscriptViewport(40, 6),
		currentSession: session,
		parts:          map[int64][]domain.Part{},
		workdir:        t.TempDir(),
	}
	m.composer.SetValue("/fork")

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
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

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
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

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected approval command")
	}
	if !next.loading {
		t.Fatal("expected loading after approval enter")
	}
}

func TestApprovalPromptOpensPermissionsPicker(t *testing.T) {
	m := Model{
		cfg:       testConfig(t),
		composer:  textarea.New(),
		approvals: []store.Approval{{ID: 7, Tool: domain.ToolKindBash, Command: `{"command":"git status"}`}},
	}
	m.approvalButtons.Index = 1

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected sync title command")
	}
	if !next.hasPicker() {
		t.Fatal("expected permission picker to open from approval prompt")
	}
	if next.picker.mode != pickerModePermissions {
		t.Fatalf("expected permissions picker mode, got %v", next.picker.mode)
	}
	if next.picker.approvalID != 7 {
		t.Fatalf("expected approval id to be preserved, got %d", next.picker.approvalID)
	}
	if next.loading {
		t.Fatal("expected opening permissions picker to avoid starting approval command")
	}
}

func TestApprovalPromptAltHotkeys(t *testing.T) {
	m := Model{
		cfg:       testConfig(t),
		composer:  textarea.New(),
		approvals: []store.Approval{{ID: 7, Tool: domain.ToolKindBash, Command: `{"command":"git status"}`}},
	}

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyRunes, Alt: true, Runes: []rune("p")})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected sync title command")
	}
	if !next.hasPicker() || next.picker.approvalID != 7 {
		t.Fatalf("expected alt+p to open permission picker for approval, got %#v", next.picker)
	}

	m = Model{
		cfg:       testConfig(t),
		composer:  textarea.New(),
		approvals: []store.Approval{{ID: 7, Tool: domain.ToolKindBash, Command: `{"command":"git status"}`}},
	}
	updated, cmd = m.handleKey(ui.KeyMsg{Type: ui.KeyRunes, Alt: true, Runes: []rune("a")})
	next = updated.(*Model)
	if cmd == nil {
		t.Fatal("expected alt+a to trigger approval command")
	}
	if !next.loading {
		t.Fatal("expected alt+a to start approval flow")
	}

	m = Model{
		cfg:       testConfig(t),
		composer:  textarea.New(),
		approvals: []store.Approval{{ID: 7, Tool: domain.ToolKindBash, Command: `{"command":"git status"}`}},
	}
	updated, cmd = m.handleKey(ui.KeyMsg{Type: ui.KeyRunes, Alt: true, Runes: []rune("d")})
	next = updated.(*Model)
	if cmd == nil {
		t.Fatal("expected alt+d to trigger deny command")
	}
	if !next.loading {
		t.Fatal("expected alt+d to start deny flow")
	}
}

func TestAltOOpensNextLLMRequestPreview(t *testing.T) {
	cfg := testConfig(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	workdir := t.TempDir()
	m := Model{
		cfg:            cfg,
		composer:       textarea.New(),
		agent:          agent.New(cfg, st, tools.NewRegistry(workdir), nil, workdir),
		currentSession: domain.Session{ProviderID: "test", ModelID: "test-model"},
		width:          100,
		height:         30,
	}
	m.composer.SetValue("draft question")

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyRunes, Alt: true, Runes: []rune("o")})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected preview command")
	}
	msg := cmd()
	if _, ok := msg.(llmPreviewMsg); !ok {
		t.Fatalf("expected llmPreviewMsg, got %T", msg)
	}
	updated, cmd = next.Update(msg)
	if cmd == nil {
		t.Fatal("expected sync title command after preview opens")
	}
	previewModel := updated.(Model)
	rendered := (&previewModel).renderLLMPreview()
	if !strings.Contains(rendered, "Next LLM Request") {
		t.Fatalf("expected preview title, got %q", rendered)
	}
	if !strings.Contains(rendered, `"model": "test-model"`) || !strings.Contains(rendered, `"draft question"`) {
		t.Fatalf("expected preview payload in rendered output, got %q", rendered)
	}
}

func TestAltOWithoutDraftShowsStatus(t *testing.T) {
	m := Model{
		cfg:      testConfig(t),
		composer: textarea.New(),
	}

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyRunes, Alt: true, Runes: []rune("o")})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected sync title command")
	}
	if next.hasLLMPreview() {
		t.Fatal("expected no preview")
	}
	if next.status != "No draft prompt to preview" {
		t.Fatalf("unexpected status: %q", next.status)
	}
}

func TestLLMPreviewScrollUsesElementOffsetState(t *testing.T) {
	m := Model{
		cfg:      testConfig(t),
		composer: textarea.New(),
		width:    80,
		height:   12,
	}
	m.openLLMPreview("Preview", strings.Join([]string{
		"line 1",
		"line 2",
		"line 3",
		"line 4",
		"line 5",
		"line 6",
		"line 7",
	}, "\n"))
	m.llmPreviewHeight = 3
	m.llmPreviewWidth = 20

	if !m.handleLLMPreviewKey(ui.KeyMsg{Type: ui.KeyDown}) {
		t.Fatal("expected preview down key to be handled")
	}
	if m.llmPreviewYOffset != 1 {
		t.Fatalf("expected preview offset 1, got %d", m.llmPreviewYOffset)
	}

	rendered := ansi.Strip(m.renderLLMPreview())
	if !strings.Contains(rendered, "line 2") {
		t.Fatalf("expected scrolled preview to show later lines, got %q", rendered)
	}
	if strings.Contains(rendered, "line 1") {
		t.Fatalf("expected first line to scroll out of view, got %q", rendered)
	}
}

func TestQuitCommandQuits(t *testing.T) {
	m := Model{
		composer: textarea.New(),
	}
	m.composer.SetValue("/quit")

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
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

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
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

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
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
	cfg := testConfig(t)
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
	if _, ok := cmd().(ui.BatchMsg); !ok {
		t.Fatalf("expected batched init command when mouse is enabled, got %T", cmd())
	}
}

func TestCtrlCUsesQuitPath(t *testing.T) {
	m := Model{
		composer: textarea.New(),
	}

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyCtrlC})
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

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEsc})
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

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEsc})
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
		sessionDialog: &dialogs.SessionDialog{},
		sessions:      []domain.Session{{ID: 1}},
	}

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEsc})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected new session command")
	}
	if !next.loading {
		t.Fatal("expected loading after picker escape")
	}
}

func TestSessionPickerRendersCenteredDialogWithPreview(t *testing.T) {
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
		width:   100,
		height:  30,
		store:   st,
		palette: theme.Default().Palette,
		sessions: []domain.Session{{
			ID:          session.ID,
			Title:       "Generated Session Title",
			LastMessage: "summary",
			CreatedAt:   time.Date(2026, 4, 20, 10, 30, 0, 0, time.UTC),
			UpdatedAt:   time.Date(2026, 4, 20, 12, 45, 0, 0, time.UTC),
		}},
	}
	m.openSessionPicker()

	got := m.View()
	if !strings.Contains(got, "Resume Session") {
		t.Fatalf("expected centered dialog title, got %q", got)
	}
	if !strings.Contains(got, "Generated Session Title") {
		t.Fatalf("expected title in dialog, got %q", got)
	}
	if !strings.Contains(got, "summary") {
		t.Fatalf("expected last message preview in dialog, got %q", got)
	}
	if strings.Contains(got, "Session ID: 1") {
		t.Fatalf("expected session details to stay in the table, got %q", got)
	}
	if !strings.Contains(got, "ID") || !strings.Contains(got, "Created") || !strings.Contains(got, "Modified") || !strings.Contains(got, "Tokens") {
		t.Fatalf("expected session table headers in dialog, got %q", got)
	}
	if !strings.Contains(got, "123/456") {
		t.Fatalf("expected token summary in table, got %q", got)
	}
	if !strings.Contains(got, "OK") || !strings.Contains(got, "Cancel") {
		t.Fatalf("expected dialog buttons in session dialog, got %q", got)
	}
	if !strings.Contains(got, "Enter resumes the highlighted session. Esc creates a new session.") {
		t.Fatalf("expected helper text in dialog, got %q", got)
	}
	if strings.Contains(got, "\x1b[") || strings.Contains(got, "[38;") || strings.Contains(got, "[1;") {
		t.Fatalf("expected plain cell-rendered session picker output, got %q", got)
	}
}

func TestSessionPickerLeavesScreenMarginForRightBorder(t *testing.T) {
	m := Model{
		width:  72,
		height: 24,
		sessions: []domain.Session{{
			ID:          1,
			Title:       "Session A",
			LastMessage: "summary",
		}},
	}

	m.openSessionPicker()
	got := m.View()
	lines := strings.Split(got, "\n")
	foundTopBorder := false
	for _, line := range lines {
		if strings.Contains(line, "╮") {
			foundTopBorder = true
			if idx := strings.Index(line, "╮"); idx >= 0 {
				col := ansi.StringWidth(line[:idx])
				if col >= m.width-1 {
					t.Fatalf("expected right border to render inside screen margin, got %q", line)
				}
			}
			break
		}
	}
	if !foundTopBorder {
		t.Fatalf("expected session dialog border in view, got %q", got)
	}
}

func TestOpenSessionPickerShowsCWDWhenAllSessionsEnabled(t *testing.T) {
	m := Model{
		startupOptions: StartupOptions{ShowAllSessions: true},
		sessions: []domain.Session{{
			ID:          1,
			Title:       "Session A",
			LastMessage: "summary",
			CWD:         "/tmp/worktree",
		}},
	}

	m.openSessionPicker()
	got := m.renderSessionDialog()
	if !strings.Contains(got, "CWD") || !strings.Contains(got, "/tmp/worktree") {
		t.Fatalf("expected cwd column in picker, got %q", got)
	}
}

func TestOpenSessionPickerBlursComposer(t *testing.T) {
	m := Model{
		composer: textarea.New(),
		sessions: []domain.Session{{
			ID:          1,
			Title:       "Session A",
			LastMessage: "summary",
		}},
	}

	if !m.composer.Focused() {
		t.Fatal("expected composer to start focused")
	}

	m.openSessionPicker()

	if m.composer.Focused() {
		t.Fatal("expected session picker to blur the hidden composer")
	}
}

func TestClosingSessionPickerRefocusesComposer(t *testing.T) {
	m := Model{
		composer: textarea.New(),
		sessions: []domain.Session{{
			ID:          1,
			Title:       "Session A",
			LastMessage: "summary",
		}},
	}

	m.openSessionPicker()
	m.closeSessionDialog()

	if !m.composer.Focused() {
		t.Fatal("expected closing the session picker to refocus the composer")
	}
}

func TestOpenSessionPickerStopsComposerBlinkTimer(t *testing.T) {
	m := Model{
		composer: textarea.New(),
		palette:  theme.Default().Palette,
		width:    80,
		height:   24,
		sessions: []domain.Session{{
			ID:          1,
			Title:       "Session A",
			LastMessage: "summary",
		}},
	}

	m.syncComposerBlinkTimer()
	if timers := m.syncUIRoot().ActiveTimers(composerBlinkTimerOwner); len(timers) == 0 {
		t.Fatal("expected focused composer to own a blink timer")
	}

	m.openSessionPicker()
	if timers := m.syncUIRoot().ActiveTimers(composerBlinkTimerOwner); len(timers) != 0 {
		t.Fatalf("expected session picker to stop composer blink timer, got %v", timers)
	}
}

func TestClosingSessionPickerRestartsComposerBlinkTimer(t *testing.T) {
	m := Model{
		composer: textarea.New(),
		palette:  theme.Default().Palette,
		width:    80,
		height:   24,
		sessions: []domain.Session{{
			ID:          1,
			Title:       "Session A",
			LastMessage: "summary",
		}},
	}

	m.openSessionPicker()
	m.closeSessionDialog()

	if timers := m.syncUIRoot().ActiveTimers(composerBlinkTimerOwner); len(timers) == 0 {
		t.Fatal("expected closing session picker to restart composer blink timer")
	}
}

func TestSelectingSessionKeepsComposerBlinkStopped(t *testing.T) {
	m := Model{
		composer: textarea.New(),
		palette:  theme.Default().Palette,
		width:    80,
		height:   24,
		sessions: []domain.Session{{
			ID:          7,
			Title:       "Session A",
			LastMessage: "summary",
		}},
	}

	m.openSessionPicker()
	updated, cmd := m.Update(ui.KeyMsg{Type: ui.KeyEnter})
	if cmd == nil {
		t.Fatal("expected resume command")
	}
	next := asModelPtr(t, updated)
	if timers := next.syncUIRoot().ActiveTimers(composerBlinkTimerOwner); len(timers) != 0 {
		t.Fatalf("expected composer blink timer to stay stopped while session dialog is active, got %v", timers)
	}
}

func TestWithRootTimersDoesNotQueueDuplicateTicks(t *testing.T) {
	m := Model{
		composer: textarea.New(),
		palette:  theme.Default().Palette,
		width:    80,
		height:   24,
	}
	m.composer.Focus()

	first := m.withRootTimers(nil)
	if first == nil {
		t.Fatal("expected initial root timer command")
	}
	if !m.rootTimerPending {
		t.Fatal("expected root timer to be marked pending")
	}
	firstSeq := m.rootTimerSeq
	firstDue := m.rootTimerPendingAt

	second := m.withRootTimers(nil)
	if second != nil {
		t.Fatal("expected duplicate root timer scheduling to be suppressed")
	}
	if m.rootTimerSeq != firstSeq {
		t.Fatalf("expected root timer sequence to remain %d, got %d", firstSeq, m.rootTimerSeq)
	}
	if !m.rootTimerPendingAt.Equal(firstDue) {
		t.Fatalf("expected pending due time to stay %v, got %v", firstDue, m.rootTimerPendingAt)
	}
}

func TestVisibleSessionsFiltersByExactCWD(t *testing.T) {
	m := Model{workdir: "/repo/a"}
	sessions := []domain.Session{
		{ID: 1, CWD: "/repo/a"},
		{ID: 2, CWD: "/repo/b"},
		{ID: 3, ProjectRoot: "/repo"},
	}

	got := m.visibleSessions(sessions)
	if len(got) != 1 || got[0].ID != 1 {
		t.Fatalf("expected only matching cwd session, got %#v", got)
	}
}

func TestFormatRelativeSessionTime(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		when time.Time
		want string
	}{
		{name: "zero", when: time.Time{}, want: "-"},
		{name: "now", when: now.Add(-30 * time.Second), want: "now"},
		{name: "minutes", when: now.Add(-3 * time.Minute), want: "3m ago"},
		{name: "hours", when: now.Add(-10 * time.Hour), want: "10h ago"},
		{name: "days", when: now.Add(-48 * time.Hour), want: "2d ago"},
	}

	for _, tc := range cases {
		if got := formatRelativeSessionTime(tc.when); got != tc.want {
			t.Fatalf("%s: expected %q, got %q", tc.name, tc.want, got)
		}
	}
}

func TestUpdateLoadHidesSessionPicker(t *testing.T) {
	m := Model{
		sessionDialog: &dialogs.SessionDialog{},
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

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
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
	if len(next.picker.dialog.Items) == 0 {
		t.Fatal("expected theme matches")
	}
}

func TestThemePickerFiltersAndPreviewsSelection(t *testing.T) {
	cfg := testConfig(t)
	cfg.UI.Theme = "tokyonight"

	m, err := New(cfg, nil, nil, StartupModeNew, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.openThemePicker()

	updated, _ := m.handleKey(ui.KeyMsg{Type: ui.KeyRunes, Runes: []rune("g")})
	next := updated.(*Model)
	updated, _ = next.handleKey(ui.KeyMsg{Type: ui.KeyRunes, Runes: []rune("r")})
	next = updated.(*Model)

	if len(next.picker.dialog.Items) == 0 {
		t.Fatal("expected filtered theme matches")
	}
	current, ok := next.picker.dialog.Current()
	if !ok || current.Value != "gruvbox" {
		t.Fatalf("expected gruvbox after filtering, got %#v", current)
	}
	if next.cfg.UI.Theme != "gruvbox" {
		t.Fatalf("expected live theme preview to apply gruvbox, got %q", next.cfg.UI.Theme)
	}
}

func TestThemePickerEscapeRestoresOriginalTheme(t *testing.T) {
	cfg := testConfig(t)
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

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEsc})
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

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
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

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected spinner tick command when opening preferences")
	}
	if !next.hasPreferencesDialog() {
		t.Fatal("expected preferences dialog to open")
	}
}

func TestToolsCommandOpensToolsDialog(t *testing.T) {
	m := Model{
		cfg:      config.Default(),
		composer: textarea.New(),
	}
	m.composer.SetValue("/tools")

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected title sync command when opening tools dialog")
	}
	if !next.hasToolsDialog() {
		t.Fatal("expected tools dialog to open")
	}
}

func TestApplySessionToolStatesUpdatesDraftSession(t *testing.T) {
	m := Model{cfg: config.Default()}

	err := m.applySessionToolStates(map[domain.ToolKind]bool{
		domain.ToolKindRead: true,
		domain.ToolKindBash: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if m.currentSession.ToolStates[domain.ToolKindBash] {
		t.Fatalf("expected bash disabled in draft session, got %#v", m.currentSession.ToolStates)
	}
}

func TestConnectCommandOpensConnectDialog(t *testing.T) {
	m := Model{
		cfg:      config.Default(),
		composer: textarea.New(),
	}
	m.composer.SetValue("/connect")

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
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

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
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

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
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

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected title sync command")
	}
	if next.status != "Configure a provider first with /connect" {
		t.Fatalf("unexpected status: %q", next.status)
	}
}

func TestModelCommandLoadsModelsForActiveProvider(t *testing.T) {
	cfg := testConfig(t)
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

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
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

func TestEnsureRuntimeContextWindowDetectsAndPersistsLlamaCPP(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/props" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("model"); got != "coder.gguf" {
			t.Fatalf("unexpected model query: %q", got)
		}
		_, _ = w.Write([]byte(`{"default_generation_settings":{"n_ctx":16384}}`))
	}))
	defer server.Close()

	cfg.Providers = map[string]config.Provider{
		"llamacpp": {
			Name:          "llama.cpp",
			Kind:          "openai-compatible",
			AuthMethod:    "local_endpoint",
			BaseURL:       server.URL,
			DefaultModel:  "coder.gguf",
			ContextWindow: 0,
		},
	}
	m := Model{cfg: cfg, runtimeCtxChecked: map[string]bool{}}
	session := domain.Session{ProviderID: "llamacpp", ModelID: "coder.gguf"}

	providerID, contextWindow, checked, err := m.ensureRuntimeContextWindow(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	if providerID != "llamacpp" || !checked {
		t.Fatalf("unexpected runtime detection result: provider=%q checked=%v", providerID, checked)
	}
	if contextWindow != 16384 {
		t.Fatalf("expected detected context window 16384, got %d", contextWindow)
	}
	providerCfg, ok := m.cfg.Provider("llamacpp")
	if !ok || providerCfg.ContextWindow != 16384 {
		t.Fatalf("expected detected context window to persist, got %#v", providerCfg)
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
	cfg := testConfig(t)
	cfg.UI.Theme = "flexoki"

	m, err := New(cfg, nil, nil, StartupModeNew, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.openPreferencesDialog()

	updated, _ := m.handleKey(ui.KeyMsg{Type: ui.KeyRight})
	next := updated.(*Model)
	if next.cfg.UI.Theme == "flexoki" {
		t.Fatal("expected preferences preview to change current theme")
	}

	updated, cmd := next.handleKey(ui.KeyMsg{Type: ui.KeyEsc})
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

	updated, _ := m.handleKey(ui.KeyMsg{Type: ui.KeyRight})
	next := updated.(*Model)
	updated, _ = next.handleKey(ui.KeyMsg{Type: ui.KeyTab})
	next = updated.(*Model)
	updated, _ = next.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
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
		currentSession: domain.Session{ID: 2, ProviderID: "test", ModelID: "model", PermissionProfile: "default", ProjectChecksum: "agents-1"},
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
		workspace: workspace.Status{
			AgentsFiles:    1,
			AgentsChecksum: "agents-1",
		},
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
	if !strings.Contains(got, "Session 2") || !strings.Contains(got, "provider test") || !strings.Contains(got, "model   model") {
		t.Fatalf("expected sidebar to include session details, got %q", got)
	}
	if !strings.Contains(got, "Status") || !strings.Contains(got, "Working ...") {
		t.Fatalf("expected sidebar to include status, got %q", got)
	}
	if !strings.Contains(got, "Help") || !strings.Contains(got, "Alt-H  hotkeys and commands") {
		t.Fatalf("expected sidebar to include help hint, got %q", got)
	}
	if strings.Contains(got, "enter send/select") || strings.Contains(got, "/connect") {
		t.Fatalf("expected sidebar to omit detailed hotkeys and commands, got %q", got)
	}
	if !strings.Contains(got, "Context") || !strings.Contains(got, "25% used") {
		t.Fatalf("expected sidebar to include context usage, got %q", got)
	}
	if !strings.Contains(got, "Debug") || !strings.Contains(got, "127.0.0.1:61347") {
		t.Fatalf("expected sidebar to include debug api status, got %q", got)
	}
	if !strings.Contains(got, "AGENTS") || !strings.Contains(got, "Up to date") {
		t.Fatalf("expected sidebar to include AGENTS summary, got %q", got)
	}
	if strings.Contains(got, "saved   ") || strings.Contains(got, "live    ") || strings.Contains(got, "files   ") {
		t.Fatalf("expected sidebar to omit AGENTS checksum details, got %q", got)
	}
	if strings.Contains(got, "Pending approvals") {
		t.Fatalf("expected sidebar to omit pending approvals, got %q", got)
	}
	if strings.Contains(got, "Profile ") || strings.Contains(got, "profile ") || strings.Contains(got, "mouse   ") {
		t.Fatalf("expected sidebar to omit session profile and mouse status, got %q", got)
	}
}

func TestRefreshViewportShowsConnectHintWithoutProvider(t *testing.T) {
	m := Model{
		cfg:      config.Default(),
		viewport: newTranscriptViewport(40, 6),
	}
	m.refreshViewport()
	if got := m.viewport.View(); !strings.Contains(got, "/connect") {
		t.Fatalf("expected connect hint in empty viewport, got %q", got)
	}
}

func TestAltHTogglesHelpDialog(t *testing.T) {
	m := Model{
		cfg:      testConfig(t),
		composer: textarea.New(),
		width:    120,
		height:   40,
	}

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyRunes, Alt: true, Runes: []rune("h")})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected sync command when opening help dialog")
	}
	if !next.hasHelpModal() {
		t.Fatal("expected help dialog to open")
	}
	view := next.View()
	if !strings.Contains(view, "Help") || !strings.Contains(view, "/connect") || !strings.Contains(view, "Ctrl-V") || !strings.Contains(view, "Alt-P") || !strings.Contains(view, "Ctrl-R") {
		t.Fatalf("expected help dialog content, got %q", view)
	}

	updated, cmd = next.handleKey(ui.KeyMsg{Type: ui.KeyRunes, Alt: true, Runes: []rune("h")})
	next = updated.(*Model)
	if cmd == nil {
		t.Fatal("expected sync command when closing help dialog")
	}
	if next.hasHelpModal() {
		t.Fatal("expected help dialog to close")
	}
}

func TestMouseClickOnHelpModalCloseIndicatorClosesModal(t *testing.T) {
	cfg := testConfig(t)
	m, err := New(cfg, nil, nil, StartupModeNew, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.mouseEnabled = true
	m.width = 100
	m.height = 28
	m.openHelpModal()

	view := m.View()
	lines := strings.Split(ansi.Strip(view), "\n")
	closeX, closeY := -1, -1
	for y, line := range lines {
		if idx := strings.Index(line, "[X]"); idx >= 0 {
			closeX, closeY = ansi.StringWidth(line[:idx])+1, y
			break
		}
	}
	if closeX < 0 || closeY < 0 {
		t.Fatalf("failed to find help modal close indicator in %q", view)
	}

	updated, cmd := m.Update(ui.MouseMsg{
		Action: ui.MouseActionPress,
		Button: ui.MouseButtonLeft,
		X:      closeX,
		Y:      closeY,
	})
	next := asModelPtr(t, updated)
	_ = cmd
	if next.hasHelpModal() {
		t.Fatal("expected help modal to close from mouse click")
	}
}

func TestMouseClickOnModelDialogCloseIndicatorCancelsDialog(t *testing.T) {
	m := Model{
		cfg:          testConfig(t),
		mouseEnabled: true,
		width:        100,
		height:       28,
		palette:      theme.Resolve("tokyonight").Palette,
		composer:     textarea.New(),
	}
	updated, _ := m.Update(modelListMsg{
		providerID: "openai",
		models: []domain.Model{
			{ID: "gpt-5.4", OwnedBy: "openai"},
			{ID: "gpt-4.1-mini", OwnedBy: "openai"},
		},
	})
	nextModel := updated.(Model)

	view := nextModel.View()
	lines := strings.Split(ansi.Strip(view), "\n")
	closeX, closeY := -1, -1
	for y, line := range lines {
		if idx := strings.Index(line, "[X]"); idx >= 0 {
			closeX, closeY = ansi.StringWidth(line[:idx])+1, y
			break
		}
	}
	if closeX < 0 || closeY < 0 {
		t.Fatalf("failed to find model dialog close indicator in %q", view)
	}

	updated, cmd := nextModel.Update(ui.MouseMsg{
		Action: ui.MouseActionPress,
		Button: ui.MouseButtonLeft,
		X:      closeX,
		Y:      closeY,
	})
	next := asModelPtr(t, updated)
	_ = cmd
	if next.hasModelDialog() {
		t.Fatal("expected model dialog to close from mouse click")
	}
	if next.status != "Model selection cancelled" {
		t.Fatalf("expected model dialog cancel status, got %q", next.status)
	}
}

func TestAltPTogglesSystemOutput(t *testing.T) {
	cfg := testConfig(t)
	m, err := New(cfg, nil, nil, StartupModeNew, nil)
	if err != nil {
		t.Fatal(err)
	}

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyRunes, Alt: true, Runes: []rune("p")})
	next := updated.(*Model)
	if cmd != nil {
		t.Fatalf("expected no command, got %v", cmd)
	}
	if !next.showSystem {
		t.Fatal("expected alt+p to enable system output")
	}

	updated, cmd = next.handleKey(ui.KeyMsg{Type: ui.KeyRunes, Alt: true, Runes: []rune("p")})
	next = updated.(*Model)
	if cmd != nil {
		t.Fatalf("expected no command, got %v", cmd)
	}
	if next.showSystem {
		t.Fatal("expected alt+p to disable system output")
	}
}

func TestAltRTogglesReasoningOutput(t *testing.T) {
	cfg := testConfig(t)
	m, err := New(cfg, nil, nil, StartupModeNew, nil)
	if err != nil {
		t.Fatal(err)
	}

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyRunes, Alt: true, Runes: []rune("r")})
	next := updated.(*Model)
	if cmd != nil {
		t.Fatalf("expected no command, got %v", cmd)
	}
	if !next.showReasoning {
		t.Fatal("expected alt+r to enable reasoning output")
	}

	updated, cmd = next.handleKey(ui.KeyMsg{Type: ui.KeyRunes, Alt: true, Runes: []rune("r")})
	next = updated.(*Model)
	if cmd != nil {
		t.Fatalf("expected no command, got %v", cmd)
	}
	if next.showReasoning {
		t.Fatal("expected alt+r to disable reasoning output")
	}
}

func TestRenderAgentsSidebarStatusColors(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)

	none := Model{}.renderAgentsSidebarStatus()
	if !strings.Contains(none, "None") || !strings.Contains(none, "38;2;224;175;104") {
		t.Fatalf("expected yellow None status, got %q", none)
	}

	upToDate := Model{
		currentSession: domain.Session{ProjectChecksum: "abc"},
		workspace:      workspace.Status{AgentsFiles: 1, AgentsChecksum: "abc"},
	}.renderAgentsSidebarStatus()
	if !strings.Contains(upToDate, "Up to date") || !strings.Contains(upToDate, "38;2;152;195;121") {
		t.Fatalf("expected green Up to date status, got %q", upToDate)
	}

	changed := Model{
		currentSession: domain.Session{ProjectChecksum: "abc"},
		workspace:      workspace.Status{AgentsFiles: 1, AgentsChecksum: "def"},
	}.renderAgentsSidebarStatus()
	if !strings.Contains(changed, "Changed") || !strings.Contains(changed, "38;2;224;108;117") {
		t.Fatalf("expected red Changed status, got %q", changed)
	}
}

func TestRenderBodyAppliesSidebarThemeBackground(t *testing.T) {
	m := Model{
		showSidebar: true,
		palette:     theme.Resolve("tokyonight").Palette,
		viewport:    newTranscriptViewport(40, 6),
	}
	m.viewport.SetContent("history")

	got := m.renderBody()
	if strings.Contains(got, "\x1b[") || strings.Contains(got, "[38;") || strings.Contains(got, "[48;") {
		t.Fatalf("expected plain body composition without leaked ANSI, got %q", got)
	}
}

func TestRenderBodyClipsSidebarToViewportHeight(t *testing.T) {
	m := Model{
		showSidebar: true,
		palette:     theme.Resolve("tokyonight").Palette,
		viewport:    newTranscriptViewport(40, 6),
		workdir:     "/tmp/project",
	}
	m.viewport.SetContent("history")

	got := m.renderBody()
	if h := lipgloss.Height(got); h != 6 {
		t.Fatalf("expected body height 6, got %d from %q", h, got)
	}
}

func TestRenderBodyKeepsSidebarAtConfiguredWidth(t *testing.T) {
	m := Model{
		showSidebar: true,
		width:       120,
		palette:     theme.Resolve("tokyonight").Palette,
		viewport:    newTranscriptViewport(87, 6),
		workdir:     "/tmp/project",
	}
	m.viewport.SetContent("history")

	got := m.renderBody()
	lines := strings.Split(ansi.Strip(got), "\n")
	if len(lines) == 0 {
		t.Fatalf("expected rendered body, got %q", got)
	}
	sidebarWidth := m.sidebarWidth()
	first := lines[0]
	if ansi.StringWidth(first) != 120 {
		t.Fatalf("expected body width 120, got %d in %q", ansi.StringWidth(first), first)
	}
	if sidebarWidth <= 0 || sidebarWidth >= 120 {
		t.Fatalf("unexpected sidebar width %d", sidebarWidth)
	}
	if strings.HasPrefix(strings.TrimSpace(first), "Session") {
		t.Fatalf("expected transcript pane before sidebar, got %q", first)
	}
}

func TestRenderBodyUsesTranscriptElementInsteadOfViewportString(t *testing.T) {
	cfg := testConfig(t)
	m := Model{
		cfg:            cfg,
		currentSession: domain.Session{ID: 1, ProviderID: "test", ModelID: "model"},
		messages: []domain.Message{
			{ID: 1, Role: domain.MessageRoleAssistant},
		},
		parts: map[int64][]domain.Part{
			1: {{Kind: domain.PartKindText, Body: "fresh transcript"}},
		},
		viewport: newTranscriptViewport(32, 6),
	}
	m.viewport.SetContent("stale viewport")

	got := ansi.Strip(m.renderBody())
	if !strings.Contains(got, "fresh transcript") {
		t.Fatalf("expected body to render transcript element, got %q", got)
	}
	if strings.Contains(got, "stale viewport") {
		t.Fatalf("expected body to ignore stale viewport string, got %q", got)
	}
}

func TestViewUsesFullTerminalWidthWithSidebar(t *testing.T) {
	m := Model{
		showSidebar: true,
		palette:     theme.Resolve("tokyonight").Palette,
		width:       100,
		height:      12,
		viewport:    newTranscriptViewport(68, 8),
		workdir:     "/tmp/project",
	}
	m.viewport.SetContent("history")

	got := m.View()
	lines := strings.Split(ansi.Strip(got), "\n")
	if len(lines) == 0 {
		t.Fatalf("expected rendered view, got empty output")
	}
	for _, line := range lines {
		if w := lipgloss.Width(line); w != m.width {
			t.Fatalf("expected rendered line width %d, got %d from %q", m.width, w, got)
		}
	}
}

func TestRefreshViewportAppendsWorkingLine(t *testing.T) {
	m := Model{
		currentSession: domain.Session{ID: 1},
		status:         "Working ...",
		parts:          map[int64][]domain.Part{},
		viewport:       newTranscriptViewport(40, 6),
		busy: busyModel{
			active: true,
			scope:  busyScopeTranscript,
			spinner: spinnerModel{
				active: true,
			},
		},
	}

	m.refreshViewport()
	got := m.renderBody()
	if !strings.Contains(got, "Working ...") || !strings.Contains(got, ui.SpinnerFrame(config.Default().UI.Spinner, 0)) {
		t.Fatalf("expected transcript activity line, got %q", got)
	}
}

func TestRenderFooterOmitsHotkeyHints(t *testing.T) {
	m := Model{
		composer: textarea.New(),
	}

	got := m.renderFooter()
	if strings.Contains(got, "enter send/select") || strings.Contains(got, "/permissions") {
		t.Fatalf("expected footer to omit hotkey hints, got %q", got)
	}
}

func TestRenderFooterShowsQueuedPromptPreviewAboveComposer(t *testing.T) {
	composer := textarea.New()
	composer.Placeholder = "Ask koder or type / for commands"
	composer.SetHeight(composerInputHeight)
	composer.SetWidth(38)
	composer.Focus()

	m := Model{
		width:        40,
		composer:     composer,
		queuedPrompt: &queuedPrompt{Text: "queued submission", Mode: queuedPromptModeNormal},
	}

	got := ansi.Strip(m.renderFooter())
	if !strings.Contains(got, "Queued follow-up inputs") || !strings.Contains(got, "queued submission") {
		t.Fatalf("expected queued prompt preview above composer, got %q", got)
	}
	if strings.Index(got, "queued submission") > strings.Index(got, "Ask koder or type / for commands") {
		t.Fatalf("expected queued preview to render above composer, got %q", got)
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
		viewport: newTranscriptViewport(38, 4),
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

	want := 24 - m.statusPaneHeight()
	if want < 5 {
		want = 5
	}
	if m.viewport.Height != want {
		t.Fatalf("expected viewport height %d from measured footer, got %d", want, m.viewport.Height)
	}
	if got := m.transcriptViewportHeight(); got >= m.viewport.Height {
		t.Fatalf("expected transcript viewport height to shrink inside main layout, got transcript=%d main=%d", got, m.viewport.Height)
	}
}

func TestRenderComposerUsesThreeLineBoxAndFullWidth(t *testing.T) {
	palette := theme.Resolve("tokyonight").Palette
	cfg := testConfig(t)
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
	cfg := testConfig(t)
	m := Model{
		cfg:     cfg,
		palette: theme.Resolve("tokyonight").Palette,
		viewport: transcriptViewport{
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
	cfg := testConfig(t)
	cfg.UI.HalfBlocks = false
	m := Model{
		cfg:     cfg,
		palette: theme.Resolve("tokyonight").Palette,
		viewport: transcriptViewport{
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
	cfg := testConfig(t)
	m := Model{
		cfg:     cfg,
		palette: theme.Resolve("tokyonight").Palette,
		parts:   map[int64][]domain.Part{},
		viewport: transcriptViewport{
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
		viewport:       newTranscriptViewport(40, 6),
		busy: busyModel{
			active: true,
			scope:  busyScopeSidebar,
			spinner: spinnerModel{
				active: true,
			},
		},
	}

	m.refreshViewport()
	got := m.renderBody()
	if strings.Contains(got, "Resuming session 2") || strings.Contains(got, "[=") {
		t.Fatalf("expected no model activity line for generic loading, got %q", got)
	}
}

func TestSpinnerTickRefreshesTranscriptActivity(t *testing.T) {
	m := Model{
		currentSession: domain.Session{ID: 1},
		status:         "Working ...",
		parts:          map[int64][]domain.Part{},
		viewport:       newTranscriptViewport(40, 6),
		busy: busyModel{
			active: true,
			scope:  busyScopeTranscript,
			spinner: spinnerModel{
				active: true,
			},
		},
	}

	m.refreshViewport()
	before := m.renderBody()

	updated, cmd := m.Update(spinnerTickMsg{})
	next := updated.(Model)
	after := next.renderBody()

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

func TestLoadMsgPreserveBusyKeepsSpinnerActive(t *testing.T) {
	m := Model{
		cfg:      testConfig(t),
		viewport: newTranscriptViewport(40, 6),
		busy: busyModel{
			active: true,
			scope:  busyScopeTranscript,
			status: "Working ...",
			spinner: spinnerModel{
				active: true,
				frame:  2,
			},
		},
		currentSession: domain.Session{ID: 1, Title: "Test"},
	}

	updated, cmd := m.Update(loadMsg{
		current:      domain.Session{ID: 1, Title: "Test"},
		parts:        map[int64][]domain.Part{},
		workspace:    workspace.Status{},
		preserveBusy: true,
	})
	next := updated.(Model)
	if cmd == nil {
		t.Fatal("expected sync title command after load update")
	}
	if !next.busy.spinner.active {
		t.Fatal("expected spinner to remain active during preserved busy reload")
	}
	if got := next.workingIndicator(); got == "" {
		t.Fatal("expected working indicator to remain visible")
	}
	if !strings.Contains(next.windowTitle(), ui.SpinnerFrame(next.cfg.UI.Spinner, 2)) {
		t.Fatalf("expected spinner in window title, got %q", next.windowTitle())
	}
}

func TestSpinnerTickPreservesViewportOffsetWhenScrolledBack(t *testing.T) {
	m := Model{
		cfg:            testConfig(t),
		currentSession: domain.Session{ID: 1, Title: "Test"},
		viewport:       newTranscriptViewport(40, 4),
		busy: busyModel{
			active: true,
			scope:  busyScopeTranscript,
			status: "Working ...",
			spinner: spinnerModel{
				active: true,
			},
		},
		messages: []domain.Message{
			{ID: 1, Role: domain.MessageRoleAssistant},
		},
		parts: map[int64][]domain.Part{
			1: {{
				Kind: domain.PartKindText,
				Body: strings.Join([]string{
					"line 1",
					"line 2",
					"line 3",
					"line 4",
					"line 5",
					"line 6",
					"line 7",
					"line 8",
				}, "\n"),
			}},
		},
	}

	m.refreshViewport()
	m.viewport.SetYOffset(1)
	beforeOffset := m.viewport.YOffset

	updated, cmd := m.Update(spinnerTickMsg{})
	next := updated.(Model)

	if cmd == nil {
		t.Fatal("expected follow-up spinner tick command")
	}
	if next.viewport.YOffset != beforeOffset {
		t.Fatalf("expected spinner tick to preserve viewport offset %d, got %d", beforeOffset, next.viewport.YOffset)
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

func TestRenderMessagePartsShowsSystemNoticeWhenEnabled(t *testing.T) {
	cfg := testConfig(t)
	m, err := New(cfg, nil, nil, StartupModeNew, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.showSystem = true

	got := m.renderMessageParts([]domain.Part{
		{Kind: domain.PartKindText, Body: "final answer"},
		{Kind: domain.PartKindSystemNotice, Body: "usage", MetaJSON: `{"PromptTokens":1}`},
	})

	if strings.Contains(got, "System") || strings.Contains(got, "usage") || strings.Contains(got, "PromptTokens") {
		t.Fatalf("expected usage notice to stay hidden, got %q", got)
	}
	if !strings.Contains(got, "final answer") {
		t.Fatalf("expected text to remain visible, got %q", got)
	}
}

func TestRenderMessagePartsShowsNonUsageSystemNoticeWhenEnabled(t *testing.T) {
	cfg := testConfig(t)
	m, err := New(cfg, nil, nil, StartupModeNew, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.showSystem = true

	got := m.renderMessageParts([]domain.Part{
		{Kind: domain.PartKindText, Body: "final answer"},
		{Kind: domain.PartKindSystemNotice, Body: "provider warning", MetaJSON: `{"detail":"retry suggested"}`},
	})

	if !strings.Contains(got, "System") || !strings.Contains(got, "provider warning") || !strings.Contains(got, "retry suggested") {
		t.Fatalf("expected visible non-usage system notice, got %q", got)
	}
	if !strings.Contains(got, "final answer") {
		t.Fatalf("expected text to remain visible, got %q", got)
	}
}

func TestRenderMessagePartsShowsAssistantNarrationWithoutSystemPrefix(t *testing.T) {
	m := Model{}

	got := m.renderMessageParts([]domain.Part{
		{Kind: domain.PartKindText, Body: "There are two main functions. Let me check and remove the duplicate:"},
		{Kind: domain.PartKindToolCall, Body: `{"path":"main.go","tool":"read","tool_call_id":"call_1"}`, MetaJSON: `{"path":"main.go","tool":"read","tool_call_id":"call_1"}`},
	})

	if !strings.Contains(got, "There are two main functions") {
		t.Fatalf("expected narration text to remain visible, got %q", got)
	}
	if strings.Contains(got, "System") {
		t.Fatalf("expected narration text not to render as a system block, got %q", got)
	}
}

func TestRenderMessagePartsFormatsCompactionMarkdown(t *testing.T) {
	cfg := testConfig(t)
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
		viewport: newTranscriptViewport(40, 4),
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

	updated, cmd := m.Update(ui.MouseMsg{
		Action: ui.MouseActionPress,
		Button: ui.MouseButtonWheelDown,
	})
	next := updated.(Model)
	if cmd != nil {
		t.Fatal("expected no command from mouse wheel scroll")
	}
	if next.viewport.YOffset == 0 {
		t.Fatalf("expected viewport to scroll, got y offset %d", next.viewport.YOffset)
	}
}

func TestMouseClickTogglesToolRunExpansion(t *testing.T) {
	m := Model{
		mouseEnabled:     true,
		currentSession:   domain.Session{ID: 1},
		viewport:         newTranscriptViewport(80, 8),
		parts:            map[int64][]domain.Part{},
		expandedToolRuns: map[string]bool{},
		palette:          theme.Resolve("tokyonight").Palette,
	}
	m.messages = []domain.Message{
		{ID: 1, Role: domain.MessageRoleTool, Summary: "bash"},
	}
	m.parts[1] = []domain.Part{{
		Kind:     domain.PartKindToolOutput,
		Body:     "line one\nline two",
		MetaJSON: `{"tool":"bash","command":"echo hi","tool_call_id":"call_bash_1"}`,
	}}

	m.refreshViewport()
	if strings.Contains(m.viewport.View(), "line one\nline two") {
		t.Fatalf("expected collapsed tool output, got %q", m.viewport.View())
	}
	if !strings.Contains(m.viewport.View(), "Expand (1 line more)") {
		t.Fatalf("expected expand indicator, got %q", m.viewport.View())
	}

	updated, cmd := m.Update(ui.MouseMsg{
		Action: ui.MouseActionPress,
		Button: ui.MouseButtonLeft,
		X:      2,
		Y:      0,
	})
	next := updated.(*Model)
	if cmd != nil {
		t.Fatal("expected no command from tool run mouse toggle")
	}
	if !strings.Contains(next.viewport.View(), " line one") || !strings.Contains(next.viewport.View(), " line two") {
		t.Fatalf("expected expanded tool output, got %q", next.viewport.View())
	}
	if !strings.Contains(next.viewport.View(), "Collapse") {
		t.Fatalf("expected collapse indicator, got %q", next.viewport.View())
	}

	updated, _ = next.Update(ui.MouseMsg{
		Action: ui.MouseActionPress,
		Button: ui.MouseButtonLeft,
		X:      2,
		Y:      0,
	})
	final := updated.(*Model)
	if strings.Contains(final.viewport.View(), "line one\nline two") {
		t.Fatalf("expected collapsed tool output after second click, got %q", final.viewport.View())
	}
}

func TestWriteToolRunUsesStoredContentForExpansion(t *testing.T) {
	m := Model{
		currentSession:   domain.Session{ID: 1},
		viewport:         newTranscriptViewport(80, 8),
		parts:            map[int64][]domain.Part{},
		expandedToolRuns: map[string]bool{},
		palette:          theme.Resolve("tokyonight").Palette,
	}

	meta, err := json.Marshal(tools.MetaWithStoredResult(map[string]string{
		"tool":         "write",
		"path":         "note.txt",
		"tool_call_id": "call_write_1",
	}, domain.PartKindToolOutput, domain.ToolKindWrite, tools.StoredResultStatusOK, tools.WriteStoredResult{
		Path:    "note.txt",
		Action:  "created",
		Summary: "Created note.txt",
		Content: "line one\nline two",
	}))
	if err != nil {
		t.Fatalf("marshal meta: %v", err)
	}

	m.messages = []domain.Message{
		{ID: 1, Role: domain.MessageRoleTool, Summary: "write"},
	}
	m.parts[1] = []domain.Part{{
		Kind:     domain.PartKindToolOutput,
		Body:     "Created note.txt",
		MetaJSON: string(meta),
	}}

	m.refreshViewport()
	if strings.Contains(m.viewport.View(), " line one\n line two") {
		t.Fatalf("expected collapsed write output, got %q", m.viewport.View())
	}
	if !strings.Contains(m.viewport.View(), "Expand (1 line more)") {
		t.Fatalf("expected expand indicator, got %q", m.viewport.View())
	}

	m.expandedToolRuns["call_write_1"] = true
	m.refreshViewport()
	if !strings.Contains(m.viewport.View(), " line one") || !strings.Contains(m.viewport.View(), " line two") {
		t.Fatalf("expected expanded write content, got %q", m.viewport.View())
	}
}

func TestEditToolRunShowsStoredHunkDetails(t *testing.T) {
	m := Model{
		currentSession:   domain.Session{ID: 1},
		viewport:         newTranscriptViewport(80, 10),
		parts:            map[int64][]domain.Part{},
		expandedToolRuns: map[string]bool{"call_edit_1": true},
		palette:          theme.Resolve("tokyonight").Palette,
	}

	meta := mustMarshalMeta(t, tools.MetaWithStoredResult(map[string]string{
		"tool":         "edit",
		"path":         "game/map.go",
		"tool_call_id": "call_edit_1",
	}, domain.PartKindToolOutput, domain.ToolKindEdit, tools.StoredResultStatusOK, tools.EditStoredResult{
		Path:    "game/map.go",
		Summary: "Edited game/map.go (replaced 1 occurrence)",
		Hunks: []tools.EditStoredHunk{{
			OldStart: 12,
			NewStart: 12,
			OldLines: []string{"if oldCondition {"},
			NewLines: []string{"if newCondition {"},
		}},
	}))

	m.messages = []domain.Message{
		{ID: 1, Role: domain.MessageRoleTool, Summary: "edit"},
	}
	m.parts[1] = []domain.Part{{
		Kind:     domain.PartKindToolOutput,
		Body:     "Edited game/map.go (replaced 1 occurrence)",
		MetaJSON: meta,
	}}

	m.refreshViewport()
	got := m.viewport.View()
	for _, want := range []string{
		"Edited game/map.go (replaced 1 occurrence)",
		"@@ -12,1 +12,1 @@",
		"-12 if oldCondition {",
		"+12 if newCondition {",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in %q", want, got)
		}
	}
}

func TestMouseClickOnApprovalPromptPermissionsOpensPicker(t *testing.T) {
	m := Model{
		mouseEnabled: true,
		width:        100,
		height:       28,
		palette:      theme.Resolve("tokyonight").Palette,
		composer:     textarea.New(),
		approvals: []store.Approval{{
			ID:      7,
			Tool:    domain.ToolKindRead,
			Command: `{"path":"README.md"}`,
		}},
	}
	m.ensureApprovalButtons()

	prompt := m.renderApprovalPrompt()
	lines := strings.Split(prompt, "\n")
	buttonLine := -1
	buttonX := -1
	for idx, line := range lines {
		stripped := ansi.Strip(line)
		if !strings.Contains(stripped, "Approve") || !strings.Contains(stripped, "Permissions") || !strings.Contains(stripped, "Deny") {
			continue
		}
		buttonLine = idx
		buttonX = strings.Index(stripped, "Permissions") + 1
		break
	}
	if buttonLine < 0 || buttonX < 0 {
		t.Fatalf("failed to find approval prompt buttons in view: %q", prompt)
	}

	startY := m.height - m.footerHeight()
	updated, cmd := m.Update(ui.MouseMsg{
		Action: ui.MouseActionPress,
		Button: ui.MouseButtonLeft,
		X:      buttonX,
		Y:      startY + buttonLine,
	})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected title sync command when opening permissions picker")
	}
	if !next.hasPicker() {
		t.Fatal("expected permissions picker to open from approval prompt mouse click")
	}
	if next.picker.mode != pickerModePermissions {
		t.Fatalf("expected permissions picker mode, got %v", next.picker.mode)
	}
	if next.picker.approvalID != 7 {
		t.Fatalf("expected approval picker to target approval 7, got %d", next.picker.approvalID)
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
	batch, ok := msgAny.(ui.BatchMsg)
	if !ok {
		t.Fatalf("expected ui.BatchMsg, got %T", msgAny)
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
	cfg := testConfig(t)
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
	cfg := testConfig(t)
	m := Model{
		cfg: cfg,
		parts: map[int64][]domain.Part{
			1: {{Kind: domain.PartKindText, Body: "hello world"}},
		},
		viewport: newTranscriptViewport(24, 6),
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
	cfg := testConfig(t)
	m := Model{
		cfg: cfg,
		parts: map[int64][]domain.Part{
			1: {{Kind: domain.PartKindText, Body: "short\nthis is a much longer line"}},
		},
		viewport: newTranscriptViewport(30, 6),
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
	cfg := testConfig(t)
	m := Model{
		cfg: cfg,
		parts: map[int64][]domain.Part{
			1: {{Kind: domain.PartKindText, Body: "this line is intentionally longer than the viewport width"}},
		},
		viewport: newTranscriptViewport(18, 6),
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
		viewport: newTranscriptViewport(18, 6),
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
	cfg := testConfig(t)
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
		viewport: newTranscriptViewport(24, 8),
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

func TestRefreshViewportUsesBlankLineBetweenAssistantMessagesWithHalfBlocks(t *testing.T) {
	cfg := testConfig(t)
	m := Model{
		cfg: cfg,
		messages: []domain.Message{
			{ID: 1, Role: domain.MessageRoleAssistant},
			{ID: 2, Role: domain.MessageRoleAssistant},
		},
		parts: map[int64][]domain.Part{
			1: {{Kind: domain.PartKindText, Body: "first reply"}},
			2: {{Kind: domain.PartKindText, Body: "second reply"}},
		},
		viewport: newTranscriptViewport(24, 8),
	}

	m.refreshViewport()
	got := m.viewport.View()
	lines := strings.Split(got, "\n")
	secondLine := -1
	for i, line := range lines {
		if strings.Contains(line, "second reply") {
			secondLine = i
			break
		}
	}
	if secondLine < 2 {
		t.Fatalf("expected second assistant message after a spacer row, got %q", got)
	}
	if !strings.Contains(lines[secondLine-2], "first reply") {
		t.Fatalf("expected first assistant message two rows before second, got %q", got)
	}
	if strings.TrimSpace(lines[secondLine-1]) != "" {
		t.Fatalf("expected blank line between assistant messages in half-block mode, got %q", got)
	}
}

func TestRefreshViewportUsesBlankLineBetweenAssistantTextAndToolRunWithHalfBlocks(t *testing.T) {
	cfg := testConfig(t)
	m := Model{
		cfg: cfg,
		messages: []domain.Message{
			{ID: 1, Role: domain.MessageRoleAssistant},
			{ID: 2, Role: domain.MessageRoleTool},
		},
		parts: map[int64][]domain.Part{
			1: {{Kind: domain.PartKindText, Body: "plain assistant text"}},
			2: {{
				Kind: domain.PartKindToolOutput,
				Body: "first line\nsecond line",
				MetaJSON: mustMarshalMeta(t, map[string]string{
					"tool":         string(domain.ToolKindRead),
					"path":         "README.md",
					"preview":      "README.md",
					"tool_call_id": "call_1",
				}),
			}},
		},
		viewport: newTranscriptViewport(60, 12),
	}

	m.refreshViewport()
	got := m.viewport.View()
	if !strings.Contains(got, "Read file") {
		t.Fatalf("expected grouped tool run card to render, got %q", got)
	}
	lines := strings.Split(got, "\n")
	var toolLine int = -1
	for i, line := range lines {
		if strings.Contains(line, "Read file") {
			toolLine = i
			break
		}
	}
	if toolLine < 2 {
		t.Fatalf("expected tool run to appear after assistant text and a blank spacer row, got %q", got)
	}
	if !strings.Contains(lines[toolLine-2], "plain assistant text") {
		t.Fatalf("expected assistant text two rows before tool run, got %q", got)
	}
	if strings.TrimSpace(lines[toolLine-1]) != "" {
		t.Fatalf("expected blank spacer row between assistant text and tool run, got %q", got)
	}
}

func TestRefreshViewportUsesBlankLineBetweenConsecutiveToolRunsWithHalfBlocks(t *testing.T) {
	cfg := testConfig(t)
	m := Model{
		cfg: cfg,
		messages: []domain.Message{
			{ID: 1, Role: domain.MessageRoleAssistant},
			{ID: 2, Role: domain.MessageRoleTool},
			{ID: 3, Role: domain.MessageRoleAssistant},
			{ID: 4, Role: domain.MessageRoleTool},
		},
		parts: map[int64][]domain.Part{
			1: {{
				Kind: domain.PartKindToolCall,
				MetaJSON: mustMarshalMeta(t, map[string]string{
					"tool":         string(domain.ToolKindRead),
					"path":         "README.md",
					"preview":      "README.md",
					"tool_call_id": "call_1",
				}),
			}},
			2: {{
				Kind: domain.PartKindToolOutput,
				Body: "first line",
				MetaJSON: mustMarshalMeta(t, map[string]string{
					"tool":         string(domain.ToolKindRead),
					"path":         "README.md",
					"preview":      "README.md",
					"tool_call_id": "call_1",
				}),
			}},
			3: {{
				Kind: domain.PartKindToolCall,
				MetaJSON: mustMarshalMeta(t, map[string]string{
					"tool":         string(domain.ToolKindRead),
					"path":         "go.mod",
					"preview":      "go.mod",
					"tool_call_id": "call_2",
				}),
			}},
			4: {{
				Kind: domain.PartKindToolOutput,
				Body: "second line",
				MetaJSON: mustMarshalMeta(t, map[string]string{
					"tool":         string(domain.ToolKindRead),
					"path":         "go.mod",
					"preview":      "go.mod",
					"tool_call_id": "call_2",
				}),
			}},
		},
		viewport: newTranscriptViewport(60, 12),
	}

	m.refreshViewport()
	got := m.viewport.View()
	lines := strings.Split(got, "\n")
	firstTitleLine, secondTitleLine := -1, -1
	firstSubtitleLine, secondSubtitleLine := -1, -1
	for i, line := range lines {
		switch {
		case firstTitleLine == -1 && strings.Contains(line, "Read file"):
			firstTitleLine = i
		case firstTitleLine != -1 && secondTitleLine == -1 && strings.Contains(line, "Read file"):
			secondTitleLine = i
		case firstSubtitleLine == -1 && strings.Contains(line, "README.md"):
			firstSubtitleLine = i
		case strings.Contains(line, "go.mod"):
			secondSubtitleLine = i
		}
	}
	if firstTitleLine == -1 || secondTitleLine == -1 || firstSubtitleLine == -1 || secondSubtitleLine == -1 {
		t.Fatalf("expected both grouped tool runs to render, got %q", got)
	}
	if firstSubtitleLine != firstTitleLine+1 || secondSubtitleLine != secondTitleLine+1 {
		t.Fatalf("expected tool subtitles directly under their titles, got %q", got)
	}
	if secondTitleLine <= firstTitleLine+1 {
		t.Fatalf("expected second tool run to appear after a blank spacer row, got %q", got)
	}
	if strings.TrimSpace(lines[secondTitleLine-1]) != "" {
		t.Fatalf("expected blank spacer row between consecutive tool runs, got %q", got)
	}
}
