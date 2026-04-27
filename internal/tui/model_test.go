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

func TestComposerUpdatesKeepMainScreenCacheAndInvalidateComposerArea(t *testing.T) {
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
		t.Fatal("expected composer update to keep the cached main screen surface for patching")
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
	if len(next.currentChat.QueuedInputs) != 0 {
		t.Fatalf("expected no queued prompt, got %#v", next.currentChat.QueuedInputs)
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
	if len(next.currentChat.QueuedInputs) != 1 || next.currentChat.QueuedInputs[0].Text != "follow up" || next.currentChat.QueuedInputs[0].Kind != domain.QueuedInputKindQueued {
		t.Fatalf("expected queued input, got %#v", next.currentChat.QueuedInputs)
	}
	if next.composer.Value() != "" {
		t.Fatalf("expected composer reset after queueing, got %q", next.composer.Value())
	}
	if len(next.messages) != 0 {
		t.Fatalf("expected no optimistic send while busy, got %#v", next.messages)
	}
}

func TestParseBangPrompt(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		want   bangPrompt
		wantOK bool
	}{
		{name: "single", input: "!ls -l", want: bangPrompt{Command: "ls -l"}, wantOK: true},
		{name: "double", input: "  !!pwd  ", want: bangPrompt{Double: true, Command: "pwd"}, wantOK: true},
		{name: "empty single", input: "!", want: bangPrompt{}, wantOK: true},
		{name: "empty double", input: "!!", want: bangPrompt{Double: true}, wantOK: true},
		{name: "not bang", input: "echo !ls", want: bangPrompt{}, wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseBangPrompt(tt.input)
			if ok != tt.wantOK {
				t.Fatalf("unexpected ok=%v want %v", ok, tt.wantOK)
			}
			if got != tt.want {
				t.Fatalf("unexpected bang prompt %#v want %#v", got, tt.want)
			}
		})
	}
}

func TestFormatBangFollowupPrompt(t *testing.T) {
	got := formatBangFollowupPrompt("ls", tools.Result{
		Output: "",
		Meta:   map[string]string{"exit_code": "7"},
	})
	if !strings.Contains(got, "```bash\nls\n```") {
		t.Fatalf("expected command block, got %q", got)
	}
	if !strings.Contains(got, "Exit code: 7") {
		t.Fatalf("expected exit code, got %q", got)
	}
	if !strings.Contains(got, "```text\n(no output)\n```") {
		t.Fatalf("expected explicit empty output placeholder, got %q", got)
	}
}

func TestBangPromptWithoutProviderRunsShellOnly(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	m := Model{
		cfg:      testConfig(t),
		store:    st,
		composer: textarea.New(),
		workdir:  t.TempDir(),
		parts:    map[int64][]domain.Part{},
	}
	m.composer.SetValue("!printf hi")

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected shell command execution")
	}
	if !next.loading {
		t.Fatal("expected busy state while shell command runs")
	}
	if next.composer.Value() != "" {
		t.Fatalf("expected composer reset, got %q", next.composer.Value())
	}

	msgAny := cmd()
	bangMsg, ok := msgAny.(bangCommandMsg)
	if !ok {
		t.Fatalf("expected bangCommandMsg, got %T", msgAny)
	}
	if bangMsg.err != nil {
		t.Fatal(bangMsg.err)
	}
	if bangMsg.followupMode != bangFollowupNone {
		t.Fatalf("expected no llm follow-up, got %v", bangMsg.followupMode)
	}

	updated, cmd = next.Update(bangMsg)
	done := updated.(Model)
	if cmd == nil {
		t.Fatal("expected transcript reload command")
	}
	if done.loading {
		t.Fatal("expected shell-only bang command to clear busy state")
	}
	if done.currentSession.ID == 0 || done.currentChat.ID == 0 {
		t.Fatalf("expected draft session/chat to be created, got session=%d chat=%d", done.currentSession.ID, done.currentChat.ID)
	}
	if len(done.messages) != 0 {
		t.Fatalf("expected no synthetic user message, got %#v", done.messages)
	}

	messages, parts, err := st.PartsForChat(context.Background(), done.currentChat.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 {
		t.Fatalf("expected one persisted tool message, got %d", len(messages))
	}
	if messages[0].Role != domain.MessageRoleTool {
		t.Fatalf("expected tool message role, got %q", messages[0].Role)
	}
	partList := parts[messages[0].ID]
	if len(partList) == 0 {
		t.Fatal("expected persisted tool parts")
	}
	foundOutput := false
	for _, part := range partList {
		if part.Kind == domain.PartKindToolOutput && strings.TrimSpace(part.Body) == "hi" {
			foundOutput = true
		}
	}
	if !foundOutput {
		t.Fatalf("expected bash output part, got %#v", partList)
	}
}

func TestDoubleBangWithoutProviderOpensConnectDialogBeforeRunningShell(t *testing.T) {
	path := filepath.Join(t.TempDir(), "blocked.txt")
	m := Model{
		cfg:      testConfig(t),
		composer: textarea.New(),
	}
	m.composer.SetValue("!!touch " + path)

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected title sync command when opening connect dialog")
	}
	if !next.hasConnectDialog() {
		t.Fatal("expected connect dialog to open")
	}
	if next.composer.Value() != "!!touch "+path {
		t.Fatalf("expected composer to remain intact, got %q", next.composer.Value())
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected shell command not to run, stat err=%v", err)
	}
}

func TestDoubleBangIdleCreatesSynthesizedPrompt(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	cfg := testConfig(t)
	cfg.DefaultProvider = "openai"
	cfg.DefaultModel = "gpt-5.4"
	cfg.Providers = map[string]config.Provider{
		"openai": {
			BaseURL:      "https://api.openai.com/v1",
			APIKey:       "secret",
			DefaultModel: "gpt-5.4",
		},
	}
	m := Model{
		cfg:      cfg,
		store:    st,
		composer: textarea.New(),
		workdir:  t.TempDir(),
		parts:    map[int64][]domain.Part{},
	}
	m.composer.SetValue("!!printf hi")

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected shell command execution")
	}

	msgAny := cmd()
	bangMsg, ok := msgAny.(bangCommandMsg)
	if !ok {
		t.Fatalf("expected bangCommandMsg, got %T", msgAny)
	}
	if bangMsg.err != nil {
		t.Fatal(bangMsg.err)
	}
	if bangMsg.followupMode != bangFollowupPrompt {
		t.Fatalf("expected immediate llm follow-up, got %v", bangMsg.followupMode)
	}
	if !strings.Contains(bangMsg.followupPrompt, "```bash\nprintf hi\n```") || !strings.Contains(bangMsg.followupPrompt, "hi") {
		t.Fatalf("expected synthesized prompt to include command and output, got %q", bangMsg.followupPrompt)
	}

	updated, cmd = next.Update(bangMsg)
	done := updated.(Model)
	if cmd == nil {
		t.Fatal("expected batched reload and prompt kickoff")
	}
	if len(done.messages) != 1 || done.messages[0].Role != domain.MessageRoleUser {
		t.Fatalf("expected optimistic user prompt, got %#v", done.messages)
	}
	if got := done.messages[0].Summary; !strings.Contains(got, "User-requested shell command:") || !strings.Contains(got, "printf hi") {
		t.Fatalf("unexpected optimistic summary %q", got)
	}

	msgBatch, ok := cmd().(ui.BatchMsg)
	if !ok {
		t.Fatalf("expected batched commands, got %T", cmd())
	}
	if len(msgBatch) < 2 {
		t.Fatalf("expected reload and prompt commands, got %d batched commands", len(msgBatch))
	}
}

func TestDoubleBangWhileBusyQueuesSynthesizedPrompt(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	cfg := testConfig(t)
	cfg.DefaultProvider = "openai"
	cfg.DefaultModel = "gpt-5.4"
	cfg.Providers = map[string]config.Provider{
		"openai": {
			BaseURL:      "https://api.openai.com/v1",
			APIKey:       "secret",
			DefaultModel: "gpt-5.4",
		},
	}
	m := Model{
		cfg:      cfg,
		store:    st,
		composer: textarea.New(),
		workdir:  t.TempDir(),
		parts:    map[int64][]domain.Part{},
		loading:  true,
		busy: busyModel{
			active: true,
			scope:  busyScopeTranscript,
		},
	}
	m.composer.SetValue("!!printf hi")

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected shell command execution while busy")
	}
	msgAny := cmd()
	bangMsg, ok := msgAny.(bangCommandMsg)
	if !ok {
		t.Fatalf("expected bangCommandMsg, got %T", msgAny)
	}
	if bangMsg.followupMode != bangFollowupQueue {
		t.Fatalf("expected queued follow-up, got %v", bangMsg.followupMode)
	}

	updated, cmd = next.Update(bangMsg)
	done := updated.(Model)
	if cmd == nil {
		t.Fatal("expected queue persistence command")
	}
	if !done.loading {
		t.Fatal("expected active model turn to remain busy")
	}
	if len(done.currentChat.QueuedInputs) != 1 {
		t.Fatalf("expected one queued follow-up, got %#v", done.currentChat.QueuedInputs)
	}
	item := done.currentChat.QueuedInputs[0]
	if item.Kind != domain.QueuedInputKindQueued {
		t.Fatalf("expected queued follow-up kind, got %v", item.Kind)
	}
	if !strings.Contains(item.Text, "User-requested shell command:") || !strings.Contains(item.Text, "printf hi") {
		t.Fatalf("expected synthesized queued prompt, got %q", item.Text)
	}
}

func TestDoubleBangWhileBusyTabQueuesSteeringPrompt(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	cfg := testConfig(t)
	cfg.DefaultProvider = "openai"
	cfg.DefaultModel = "gpt-5.4"
	cfg.Providers = map[string]config.Provider{
		"openai": {
			BaseURL:      "https://api.openai.com/v1",
			APIKey:       "secret",
			DefaultModel: "gpt-5.4",
		},
	}
	m := Model{
		cfg:      cfg,
		store:    st,
		composer: textarea.New(),
		workdir:  t.TempDir(),
		parts:    map[int64][]domain.Part{},
		loading:  true,
		busy: busyModel{
			active: true,
			scope:  busyScopeTranscript,
		},
	}
	m.composer.SetValue("!!printf hi")

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyTab})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected shell command execution while busy")
	}
	msgAny := cmd()
	bangMsg, ok := msgAny.(bangCommandMsg)
	if !ok {
		t.Fatalf("expected bangCommandMsg, got %T", msgAny)
	}
	if bangMsg.followupMode != bangFollowupSteer {
		t.Fatalf("expected steer follow-up, got %v", bangMsg.followupMode)
	}

	updated, cmd = next.Update(bangMsg)
	done := updated.(Model)
	if cmd == nil {
		t.Fatal("expected queue persistence command")
	}
	if len(done.currentChat.QueuedInputs) != 1 {
		t.Fatalf("expected one queued steering follow-up, got %#v", done.currentChat.QueuedInputs)
	}
	if done.currentChat.QueuedInputs[0].Kind != domain.QueuedInputKindSteer {
		t.Fatalf("expected steering follow-up kind, got %v", done.currentChat.QueuedInputs[0].Kind)
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
	if len(next.currentChat.QueuedInputs) != 1 || next.currentChat.QueuedInputs[0].Kind != domain.QueuedInputKindSteer {
		t.Fatalf("expected steering queue, got %#v", next.currentChat.QueuedInputs)
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
	if len(next.currentChat.QueuedInputs) != 1 || next.currentChat.QueuedInputs[0].Kind != domain.QueuedInputKindContinue {
		t.Fatalf("expected queued continue, got %#v", next.currentChat.QueuedInputs)
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
		currentChat:    domain.Chat{QueuedInputs: []domain.QueuedInput{{ID: 1, Text: "queued ask", Kind: domain.QueuedInputKindQueued}}},
	}

	updated, cmd := m.Update(loadMsg{
		current: domain.Session{ID: 9, ProviderID: "openai", ModelID: "gpt-5.4", Title: "Queued"},
		parts:   map[int64][]domain.Part{},
	})
	next := updated.(Model)
	if cmd == nil {
		t.Fatal("expected queued input dispatch command")
	}
	if !next.loading {
		t.Fatal("expected queued input dispatch to restart loading")
	}
	if len(next.messages) != 1 || next.messages[0].Summary != "queued ask" {
		t.Fatalf("expected optimistic queued message, got %#v", next.messages)
	}
	if len(next.currentChat.QueuedInputs) != 0 {
		t.Fatalf("expected queued input cleared, got %#v", next.currentChat.QueuedInputs)
	}
}

func TestQueueEditEnterRestoresQueuedPromptToComposer(t *testing.T) {
	m := Model{
		cfg:         testConfig(t),
		composer:    textarea.New(),
		currentChat: domain.Chat{QueuedInputs: []domain.QueuedInput{{ID: 1, Text: "queued ask", Kind: domain.QueuedInputKindQueued}}},
	}
	m.queueEditMode = true

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next := updated.(*Model)

	_ = cmd
	if len(next.currentChat.QueuedInputs) != 0 {
		t.Fatalf("expected queued prompt cleared, got %#v", next.currentChat.QueuedInputs)
	}
	if next.composer.Value() != "queued ask" {
		t.Fatalf("expected composer to contain restored queued prompt, got %q", next.composer.Value())
	}
}

func TestQueueEditEnterSwapsQueuedPromptWithExistingDraft(t *testing.T) {
	m := Model{
		cfg:         testConfig(t),
		composer:    textarea.New(),
		currentChat: domain.Chat{QueuedInputs: []domain.QueuedInput{{ID: 1, Text: "queued ask", Kind: domain.QueuedInputKindSteer}}},
	}
	m.queueEditMode = true
	m.setComposerValue("current draft")

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next := updated.(*Model)

	_ = cmd
	if next.composer.Value() != "queued ask" {
		t.Fatalf("expected composer to contain restored queued prompt, got %q", next.composer.Value())
	}
	if len(next.currentChat.QueuedInputs) != 1 {
		t.Fatal("expected previous draft to be re-queued")
	}
	if next.currentChat.QueuedInputs[0].Text != "current draft" {
		t.Fatalf("expected current draft to be re-queued, got %#v", next.currentChat.QueuedInputs)
	}
	if next.currentChat.QueuedInputs[0].Kind != domain.QueuedInputKindQueued {
		t.Fatalf("expected swapped draft to be queued as normal follow-up, got %#v", next.currentChat.QueuedInputs)
	}
}

func TestQueueEditEnterClearsQueuedContinue(t *testing.T) {
	m := Model{
		cfg:         testConfig(t),
		composer:    textarea.New(),
		currentChat: domain.Chat{QueuedInputs: []domain.QueuedInput{{ID: 1, Kind: domain.QueuedInputKindContinue}}},
	}
	m.queueEditMode = true
	m.setComposerValue("keep draft")

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next := updated.(*Model)

	_ = cmd
	if len(next.currentChat.QueuedInputs) != 0 {
		t.Fatalf("expected queued continue cleared, got %#v", next.currentChat.QueuedInputs)
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
				MetaJSON: `{"tool":"bash","command":"git status","tool_call_id":"call_1"}`,
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
	if !strings.Contains(got, "Ran command git status") {
		t.Fatalf("expected grouped tool title in transcript, got %q", got)
	}
	if strings.Contains(got, "│") || strings.Contains(got, "╭") || strings.Contains(got, "╰") {
		t.Fatalf("expected compact tool row without border chrome, got %q", got)
	}
	if !strings.Contains(got, "On branch main") {
		t.Fatalf("expected tool output preview, got %q", got)
	}
	if strings.Contains(got, `"tool":"bash"`) || strings.Contains(got, "Approval required for bash") {
		t.Fatalf("expected compact tool card instead of raw transcript blobs, got %q", got)
	}
}

func TestRenderApprovalPromptUsesApprovalDialog(t *testing.T) {
	cfg := testConfig(t)
	m := Model{
		cfg:       cfg,
		palette:   theme.Resolve("tokyonight").Palette,
		viewport:  newTranscriptViewport(80, 12),
		approvals: []store.Approval{{ID: 9, Tool: domain.ToolKindBash, Command: `{"command":"git status","tool_call_id":"call_1"}`}},
	}

	got := m.renderApprovalPrompt()
	if !strings.Contains(got, "Approval required") || !strings.Contains(got, "Run command") {
		t.Fatalf("expected typed approval dialog, got %q", got)
	}
	if !strings.Contains(got, "Approve this time") || !strings.Contains(got, "Deny") {
		t.Fatalf("expected approval actions in dialog, got %q", got)
	}
	if strings.Contains(got, `{"command":"git status"`) {
		t.Fatalf("expected approval dialog to avoid raw JSON, got %q", got)
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
	if strings.Count(got, "README.md") != 1 {
		t.Fatalf("expected read path to appear once in collapsed card, got %q", got)
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

func TestPermissionsPickerApplyAfterSlashCommandInvalidatesComposerArea(t *testing.T) {
	m := Model{
		cfg:         testConfig(t),
		palette:     theme.Default().Palette,
		composer:    textarea.New(),
		viewport:    newTranscriptViewport(80, 20),
		renderCache: &modelRenderCache{},
		width:       80,
		height:      24,
	}
	m.composer.SetValue("/per")
	m.updateComposerMenus()

	_ = m.ViewLines()
	if !m.ensureRenderCache().composerAreaValid {
		t.Fatal("expected composer area cache to be primed")
	}

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected direct command execution")
	}
	if !next.hasPicker() {
		t.Fatal("expected /permissions to open immediately from slash menu")
	}

	updated, cmd = next.submitPickerSelection(permission.ProfileAsk)
	final := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected title sync command after applying permission profile")
	}
	if final.composer.Value() != "" {
		t.Fatalf("expected empty composer after applying permission profile, got %q", final.composer.Value())
	}
	if final.ensureRenderCache().composerAreaValid {
		t.Fatal("expected composer area cache invalidated after clearing slash command")
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

func TestApprovalDialogConsumesEnter(t *testing.T) {
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

func TestApprovalDialogOpensPermissionsPicker(t *testing.T) {
	m := Model{
		cfg:       testConfig(t),
		composer:  textarea.New(),
		approvals: []store.Approval{{ID: 7, Tool: domain.ToolKindBash, Command: `{"command":"git status"}`}},
	}
	m.ensureApprovalDialog()
	m.approvalDialog.SetButtonIndex(4)

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected sync title command")
	}
	if !next.hasPicker() {
		t.Fatal("expected permission picker to open from approval dialog")
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

func TestApprovalDialogArrowNavigationThenEnterOpensPermissionsPicker(t *testing.T) {
	m := Model{
		cfg:       testConfig(t),
		composer:  textarea.New(),
		approvals: []store.Approval{{ID: 7, Tool: domain.ToolKindBash, Command: `{"command":"git status"}`}},
	}

	var updated ui.Model = &m
	var cmd ui.Cmd
	for i := 0; i < 4; i++ {
		updated, cmd = updated.(*Model).handleKey(ui.KeyMsg{Type: ui.KeyRight})
		next := updated.(*Model)
		if cmd != nil {
			t.Fatal("expected navigation to avoid starting a command")
		}
		if !next.hasApprovalDialog() {
			t.Fatal("expected approval dialog to remain open")
		}
	}
	next := updated.(*Model)
	if next.approvalDialog.ButtonIndex() != 4 {
		t.Fatalf("expected right arrow to focus permissions button, got %d", next.approvalDialog.ButtonIndex())
	}

	updated, cmd = next.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next = updated.(*Model)
	if cmd == nil {
		t.Fatal("expected sync title command")
	}
	if !next.hasPicker() {
		t.Fatal("expected permission picker to open from approval dialog")
	}
	if next.picker.mode != pickerModePermissions {
		t.Fatalf("expected permissions picker mode, got %v", next.picker.mode)
	}
	if next.picker.approvalID != 7 {
		t.Fatalf("expected approval id to be preserved, got %d", next.picker.approvalID)
	}
}

func TestApprovalDialogAltHotkeys(t *testing.T) {
	m := Model{
		cfg:       testConfig(t),
		composer:  textarea.New(),
		approvals: []store.Approval{{ID: 7, Tool: domain.ToolKindBash, Command: `{"command":"git status"}`}},
	}

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyRunes, Alt: true, Runes: []rune("s")})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected sync title command")
	}
	if !next.hasPicker() || next.picker.approvalID != 7 {
		t.Fatalf("expected alt+s to open permission picker for approval, got %#v", next.picker)
	}

	m = Model{
		cfg:       testConfig(t),
		composer:  textarea.New(),
		approvals: []store.Approval{{ID: 7, Tool: domain.ToolKindBash, Command: `{"command":"git status"}`}},
	}
	updated, cmd = m.handleKey(ui.KeyMsg{Type: ui.KeyRunes, Alt: true, Runes: []rune("t")})
	next = updated.(*Model)
	if cmd == nil {
		t.Fatal("expected alt+t to trigger approval command")
	}
	if !next.loading {
		t.Fatal("expected alt+t to start approval flow")
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

func TestNewWithWorkdirStartsComposerFocusedWithBlinkTimer(t *testing.T) {
	cfg := testConfig(t)
	cfg.UI.CursorBlink = true
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	workdir := t.TempDir()
	m, err := NewWithWorkdir(cfg, st, nil, StartupModeNew, nil, workdir, StartupOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !m.composer.Focused() {
		t.Fatal("expected composer to start focused")
	}
	if !m.composer.CursorVisible() {
		t.Fatal("expected startup composer cursor to be visible")
	}
	if timers := m.ensureUIRoot().ActiveTimers(composerBlinkTimerOwner); len(timers) == 0 {
		t.Fatal("expected startup composer to own a blink timer")
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
		composer:      textarea.New(),
		palette:       theme.Default().Palette,
		width:         80,
		height:        24,
		sessionDialog: &dialogs.SessionDialog{},
	}
	m.composer.Blur()

	updated := m.UpdateLoad(loadMsg{
		current: domain.Session{ID: 4},
	})

	if updated.hasSessionDialog() {
		t.Fatal("expected session dialog to close after loading a session")
	}
	if updated.currentSession.ID != 4 {
		t.Fatalf("unexpected current session: %#v", updated.currentSession)
	}
	if !updated.composer.Focused() {
		t.Fatal("expected loading a session to refocus the composer")
	}
}

func TestAppendingPromptPreservesRetainedTranscriptPrefix(t *testing.T) {
	m := Model{
		cfg:              testConfig(t),
		palette:          theme.Default().Palette,
		viewport:         newTranscriptViewport(80, 18),
		renderCache:      &modelRenderCache{},
		composer:         textarea.New(),
		width:            80,
		height:           24,
		parts:            make(map[int64][]domain.Part),
		expandedToolRuns: make(map[string]bool),
		transcriptDirty:  true,
	}

	m.appendLocalUserPrompt("first", nil, nil)
	retained := m.ensureRetainedTranscript()
	items := retained.Items()
	if len(items) != 1 {
		t.Fatalf("expected one retained transcript item, got %d", len(items))
	}
	first := items[0].Element

	m.appendLocalUserPrompt("second", nil, nil)
	items = retained.Items()
	if len(items) != 2 {
		t.Fatalf("expected two retained transcript items, got %d", len(items))
	}
	if items[0].Element != first {
		t.Fatal("expected appending a prompt to preserve the existing retained transcript element")
	}
	if m.transcriptDirty {
		t.Fatal("expected transcript sync to clear the dirty flag after append")
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
	if !next.hasThemeDialog() {
		t.Fatal("expected theme dialog to open")
	}
	if len(next.themeDialog.Themes) == 0 {
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

	if len(next.themeDialog.Themes) == 0 {
		t.Fatal("expected filtered theme matches")
	}
	current, ok := next.themeDialog.Current()
	if !ok || current != "gruvbox" {
		t.Fatalf("expected gruvbox after filtering, got %#v", current)
	}
	if next.cfg.UI.Theme != "gruvbox" {
		t.Fatalf("expected live theme preview to apply gruvbox, got %q", next.cfg.UI.Theme)
	}
}

func TestSetThemeRefreshesTranscriptColors(t *testing.T) {
	cfg := testConfig(t)
	cfg.UI.Theme = "tokyonight"

	m, err := New(cfg, nil, nil, StartupModeNew, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.width = 80
	m.height = 24
	m.viewport = newTranscriptViewport(60, 12)
	m.messages = []domain.Message{{
		ID:        1,
		Role:      domain.MessageRoleAssistant,
		Summary:   "hello",
		CreatedAt: time.Now(),
	}}
	m.parts = map[int64][]domain.Part{
		1: {{Kind: domain.PartKindText, Body: "hello"}},
	}

	m.refreshViewport()
	before := m.viewport.VisibleSurface()
	beforeR, beforeG, beforeB, beforeOK := firstSurfaceFG(before)
	if !beforeOK {
		t.Fatal("expected transcript foreground color before theme change")
	}

	if err := m.setTheme("flexoki", false); err != nil {
		t.Fatal(err)
	}

	after := m.viewport.VisibleSurface()
	afterR, afterG, afterB, afterOK := firstSurfaceFG(after)
	if !afterOK {
		t.Fatal("expected transcript foreground color after theme change")
	}
	if beforeR == afterR && beforeG == afterG && beforeB == afterB {
		t.Fatal("expected transcript colors to update after theme change")
	}
}

func firstSurfaceFG(view ui.SurfaceView) (uint8, uint8, uint8, bool) {
	for y := 0; y < view.SurfaceHeight(); y++ {
		for x := 0; x < view.SurfaceWidth(); x++ {
			r, g, b, ok := view.SurfaceCellFG(x, y)
			if ok {
				return r, g, b, true
			}
		}
	}
	return 0, 0, 0, false
}

func TestThemePickerEscapeRestoresOriginalTheme(t *testing.T) {
	cfg := testConfig(t)
	cfg.UI.Theme = "flexoki"

	m, err := New(cfg, nil, nil, StartupModeNew, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.openThemePicker()
	m.themeDialog.Update(ui.KeyMsg{Type: ui.KeyRight})
	m.previewSelectedTheme()
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
	if next.hasThemeDialog() {
		t.Fatal("expected theme dialog to close on cancel")
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
	m.themeDialog.Update(ui.KeyMsg{Type: ui.KeyRight})

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next := updated.(*Model)
	if cmd != nil {
		t.Fatal("expected no async command on theme apply")
	}
	if next.hasThemeDialog() {
		t.Fatal("expected theme dialog to close after selection")
	}
	if next.cfg.UI.Theme == "flexoki" {
		t.Fatal("expected theme selection to persist a new theme")
	}
	if !strings.Contains(next.status, "Theme set to") {
		t.Fatalf("expected status update after theme apply, got %q", next.status)
	}
}

func TestMouseClickOnThemePickerOKAppliesSelection(t *testing.T) {
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
	m.mouseEnabled = true
	m.width = 100
	m.height = 28
	m.openThemePicker()
	m.themeDialog.Query = "gr"
	m.themeDialog.SetCurrentValue("gruvbox")
	m.themeDialog.Update(ui.KeyMsg{Type: ui.KeyTab})
	m.previewSelectedTheme()
	bounds := m.centeredWindowBounds(m.renderThemeDialogElement())
	runtime := ui.Runtime{}
	ctx := &ui.Context{Palette: m.palette, Runtime: &runtime}
	element := m.renderThemeDialogElement()
	if element == nil {
		t.Fatal("expected theme dialog element")
	}
	_ = element.Render(ctx, ui.Rect{W: bounds.W, H: bounds.H})
	var okControl ui.Control
	found := false
	for _, control := range runtime.Controls() {
		if control.ID == "ok" {
			okControl = control
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected OK control to be registered")
	}
	if action := m.themeDialog.ActivateControl("ok"); action.Kind != dialogs.ThemeDialogActionSelect {
		t.Fatalf("expected OK control to select current item, got %#v", action)
	}
	okX := bounds.X + okControl.Rect.X + 1
	okY := bounds.Y + okControl.Rect.Y
	if control, ok := runtime.Hit(ui.Point{X: okControl.Rect.X, Y: okControl.Rect.Y}); !ok || control.ID != "ok" {
		t.Fatalf("expected local hit to resolve OK control, got %#v %v", control, ok)
	}

	updated, _ := m.Update(ui.MouseMsg{
		Action: ui.MouseActionPress,
		Button: ui.MouseButtonLeft,
		X:      okX,
		Y:      okY,
	})
	next := asModelPtr(t, updated)
	if next.hasThemeDialog() {
		t.Fatalf("expected theme dialog to close after clicking OK, status=%q theme=%q", next.status, next.cfg.UI.Theme)
	}
	if next.cfg.UI.Theme != "gruvbox" {
		t.Fatalf("expected theme selection to persist gruvbox, got %q", next.cfg.UI.Theme)
	}
	if !strings.Contains(next.status, "Theme set to") {
		t.Fatalf("expected status update after clicking OK, got %q", next.status)
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

func TestSaveProviderDraftDefaultsContextWindowWhenUnset(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	m := Model{cfg: cfg}
	draft := provider.ConnectDraft{
		ProviderID: "openai-compatible",
		Kind:       provider.ProviderKindCompatible,
		Name:       "Compatible",
		BaseURL:    "http://127.0.0.1:8888/v1",
		Model:      "coder.gguf",
	}

	if err := m.saveProviderDraft(draft); err != nil {
		t.Fatal(err)
	}
	providerCfg, ok := m.cfg.Provider("openai-compatible")
	if !ok {
		t.Fatal("expected provider config to be saved")
	}
	if providerCfg.ContextWindow != 32768 {
		t.Fatalf("expected default context window 32768, got %d", providerCfg.ContextWindow)
	}
}

func TestEnsureRuntimeContextWindowDetectsAndPersistsCompatibleLocalProvider(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"coder.gguf","max_model_len":131072}]}`))
		case "/props":
			if got := r.URL.Query().Get("model"); got != "coder.gguf" {
				t.Fatalf("unexpected model query: %q", got)
			}
			_, _ = w.Write([]byte(`{"default_generation_settings":{"n_ctx":131072}}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	cfg.Providers = map[string]config.Provider{
		"openai-compatible": {
			Name:          "Compatible",
			Kind:          "openai-compatible",
			AuthMethod:    "local_endpoint",
			BaseURL:       server.URL + "/v1",
			DefaultModel:  "coder.gguf",
			ContextWindow: 32768,
		},
	}
	m := Model{cfg: cfg, runtimeCtxChecked: map[string]bool{}}
	session := domain.Session{ProviderID: "openai-compatible", ModelID: "coder.gguf"}

	providerID, contextWindow, checked, err := m.ensureRuntimeContextWindow(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	if providerID != "openai-compatible" || !checked {
		t.Fatalf("unexpected runtime detection result: provider=%q checked=%v", providerID, checked)
	}
	if contextWindow != 131072 {
		t.Fatalf("expected detected context window 131072, got %d", contextWindow)
	}
	providerCfg, ok := m.cfg.Provider("openai-compatible")
	if !ok || providerCfg.ContextWindow != 131072 {
		t.Fatalf("expected detected context window to persist, got %#v", providerCfg)
	}
}

func TestEnsureRuntimeContextWindowDetectsAndPersistsCompatibleAPIKeyProvider(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models", "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"Lorbus/Qwen3.6-27B-int4-AutoRound","max_model_len":262144}]}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	cfg.Providers = map[string]config.Provider{
		"openai": {
			Name:          "OpenAI Compatible",
			Kind:          "openai-compatible",
			AuthMethod:    "api_key",
			BaseURL:       server.URL + "/v1",
			APIKey:        "test-key",
			DefaultModel:  "Lorbus/Qwen3.6-27B-int4-AutoRound",
			ContextWindow: 32768,
		},
	}
	m := Model{cfg: cfg, runtimeCtxChecked: map[string]bool{}}
	session := domain.Session{ProviderID: "openai", ModelID: "Lorbus/Qwen3.6-27B-int4-AutoRound"}

	providerID, contextWindow, checked, err := m.ensureRuntimeContextWindow(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	if providerID != "openai" || !checked {
		t.Fatalf("unexpected runtime detection result: provider=%q checked=%v", providerID, checked)
	}
	if contextWindow != 262144 {
		t.Fatalf("expected detected context window 262144, got %d", contextWindow)
	}
	providerCfg, ok := m.cfg.Provider("openai")
	if !ok || providerCfg.ContextWindow != 262144 {
		t.Fatalf("expected detected context window to persist, got %#v", providerCfg)
	}
}

func TestUpdateLoadSchedulesContextWindowDetectionForCurrentSession(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Providers = map[string]config.Provider{
		"openai-compatible": {
			Name:         "Compatible",
			Kind:         "openai-compatible",
			AuthMethod:   "local_endpoint",
			BaseURL:      "http://127.0.0.1:8888/v1",
			DefaultModel: "coder.gguf",
		},
	}
	m := Model{
		cfg:               cfg,
		composer:          textarea.New(),
		runtimeCtxChecked: map[string]bool{},
	}

	load := loadMsg{
		current: domain.Session{ID: 1, ProviderID: "openai-compatible", ModelID: "coder.gguf"},
	}
	updated, cmd := m.Update(load)
	if cmd == nil {
		t.Fatal("expected follow-up command after load")
	}
	next := updated.(Model)
	if next.currentSession.ProviderID != "openai-compatible" {
		t.Fatalf("unexpected current session: %#v", next.currentSession)
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

	if err := m.selectModel("openai", "gpt-4.1-mini", provider.ModelPresetDefault); err != nil {
		t.Fatal(err)
	}
	if m.cfg.DefaultProvider != "openai" || m.cfg.DefaultModel != "gpt-4.1-mini" || m.currentSession.ProviderID != "openai" || m.currentSession.ModelID != "gpt-4.1-mini" {
		t.Fatalf("unexpected model selection state: provider=%q cfg=%q sessionProvider=%q sessionModel=%q", m.cfg.DefaultProvider, m.cfg.DefaultModel, m.currentSession.ProviderID, m.currentSession.ModelID)
	}
	if got := m.cfg.Providers["openai"].ModelPreset; got != provider.ModelPresetDefault {
		t.Fatalf("expected model preset to persist, got %q", got)
	}
	reloaded, err := st.GetSession(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.ModelID != "gpt-4.1-mini" {
		t.Fatalf("expected persisted session model, got %q", reloaded.ModelID)
	}
}

func TestSelectModelSwitchesCurrentSessionProvider(t *testing.T) {
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
		"groq": {
			Name:         "Groq",
			Kind:         "openai-compatible",
			AuthMethod:   "api_key",
			BaseURL:      "https://api.groq.com/openai/v1",
			DefaultModel: "llama-3.3-70b-versatile",
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

	if err := m.selectModel("groq", "llama-3.3-70b-versatile", provider.ModelPresetDefault); err != nil {
		t.Fatal(err)
	}
	if m.currentSession.ProviderID != "groq" || m.currentSession.ModelID != "llama-3.3-70b-versatile" {
		t.Fatalf("expected session provider/model switch, got provider=%q model=%q", m.currentSession.ProviderID, m.currentSession.ModelID)
	}
	if m.cfg.DefaultProvider != "groq" || m.cfg.DefaultModel != "llama-3.3-70b-versatile" {
		t.Fatalf("expected default provider/model switch, got provider=%q model=%q", m.cfg.DefaultProvider, m.cfg.DefaultModel)
	}
	reloaded, err := st.GetSession(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.ProviderID != "groq" || reloaded.ModelID != "llama-3.3-70b-versatile" {
		t.Fatalf("expected persisted session provider/model switch, got provider=%q model=%q", reloaded.ProviderID, reloaded.ModelID)
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

func TestOpenModelDialogUsesProviderPreset(t *testing.T) {
	cfg := testConfig(t)
	cfg.Providers = map[string]config.Provider{
		"openai": {
			BaseURL:      "https://api.openai.com/v1",
			APIKey:       "secret",
			DefaultModel: "Qwen/Qwen3.6-35B-A3B",
			ModelPreset:  provider.ModelPresetQwen36PreserveThinking,
		},
	}
	cfg.DefaultProvider = "openai"
	cfg.DefaultModel = "Qwen/Qwen3.6-35B-A3B"

	m := Model{cfg: cfg}
	m.openModelDialog("openai", []domain.Model{{ID: "Qwen/Qwen3.6-35B-A3B"}})
	if m.modelDialog == nil {
		t.Fatal("expected model dialog")
	}
	if m.modelDialog.PresetID != provider.ModelPresetQwen36PreserveThinking {
		t.Fatalf("expected persisted provider preset, got %q", m.modelDialog.PresetID)
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

	updated, _ := m.handleKey(ui.KeyMsg{Type: ui.KeyShiftTab})
	next := updated.(*Model)
	updated, _ = next.handleKey(ui.KeyMsg{Type: ui.KeyRight})
	next = updated.(*Model)
	updated, _ = next.handleKey(ui.KeyMsg{Type: ui.KeyTab})
	next = updated.(*Model)
	updated, _ = next.handleKey(ui.KeyMsg{Type: ui.KeyRight})
	next = updated.(*Model)
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
	cfg.MaxToolLoopSteps = 500
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	m, err := New(cfg, nil, nil, StartupModeNew, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.openPreferencesDialog()

	updated, _ := m.handleKey(ui.KeyMsg{Type: ui.KeyShiftTab})
	next := updated.(*Model)
	updated, _ = next.handleKey(ui.KeyMsg{Type: ui.KeyRight})
	next = updated.(*Model)
	updated, _ = next.handleKey(ui.KeyMsg{Type: ui.KeyTab})
	next = updated.(*Model)
	updated, _ = next.handleKey(ui.KeyMsg{Type: ui.KeyRight})
	next = updated.(*Model)
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
	if next.cfg.MaxToolLoopSteps != 500 {
		t.Fatalf("expected tool loop limit unchanged, got %d", next.cfg.MaxToolLoopSteps)
	}
	reloaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.UI.Theme != next.cfg.UI.Theme {
		t.Fatalf("expected saved theme %q, got %q", next.cfg.UI.Theme, reloaded.UI.Theme)
	}
	if reloaded.MaxToolLoopSteps != next.cfg.MaxToolLoopSteps {
		t.Fatalf("expected saved tool loop limit %d, got %d", next.cfg.MaxToolLoopSteps, reloaded.MaxToolLoopSteps)
	}
}

func TestPreferencesDialogApplySavesToolLoopLimit(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
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

	if next.cfg.MaxToolLoopSteps != 501 {
		t.Fatalf("expected tool loop limit incremented to 501, got %d", next.cfg.MaxToolLoopSteps)
	}
	reloaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.MaxToolLoopSteps != 501 {
		t.Fatalf("expected saved tool loop limit 501, got %d", reloaded.MaxToolLoopSteps)
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
	if !strings.Contains(view, "Ctrl-PgUp/PgDn") {
		t.Fatalf("expected chat navigation hotkey in help dialog, got %q", view)
	}
	if !strings.Contains(view, "PgUp/PgDn") {
		t.Fatalf("expected help dialog scroll footer, got %q", view)
	}
	if !strings.Contains(next.helpBody, "Queue Edit Mode") || !strings.Contains(next.helpBody, "restore selected queued prompt to composer") {
		t.Fatalf("expected queue editing help body content, got %q", next.helpBody)
	}

	updated, cmd = next.handleKey(ui.KeyMsg{Type: ui.KeyEnd})
	next = updated.(*Model)
	if cmd != nil {
		t.Fatal("expected scroll key to update modal in place")
	}
	if next.helpYOffset == 0 {
		t.Fatal("expected help modal to become scrollable")
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

func TestCtrlPageKeysSwitchChats(t *testing.T) {
	m := Model{
		cfg:            testConfig(t),
		composer:       textarea.New(),
		currentSession: domain.Session{ID: 7},
		currentChat:    domain.Chat{ID: 11, SessionID: 7},
		chats: []domain.Chat{
			{ID: 11, SessionID: 7},
			{ID: 12, SessionID: 7},
			{ID: 13, SessionID: 7},
		},
	}

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyCtrlPgDown})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected chat switch command for ctrl+pgdown")
	}
	if !next.loading {
		t.Fatal("expected chat switch to enter busy state")
	}
	if next.status != "Switching to chat 12…" {
		t.Fatalf("expected next-chat status, got %q", next.status)
	}

	updated, cmd = m.handleKey(ui.KeyMsg{Type: ui.KeyCtrlPgUp})
	next = updated.(*Model)
	if cmd == nil {
		t.Fatal("expected chat switch command for ctrl+pgup")
	}
	if next.status != "Switching to chat 13…" {
		t.Fatalf("expected previous-chat wrap status, got %q", next.status)
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
	m.height = 40
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

func TestAltRPreservesVisibleTopLine(t *testing.T) {
	cfg := testConfig(t)
	m, err := New(cfg, nil, nil, StartupModeNew, nil)
	if err != nil {
		t.Fatal(err)
	}

	m.currentSession = domain.Session{ID: 1}
	m.width = 24
	m.height = 12
	m.viewport = newTranscriptViewport(24, 4)
	m.messages = []domain.Message{
		{ID: 1, SessionID: 1, Role: domain.MessageRoleAssistant},
	}
	m.parts = map[int64][]domain.Part{
		1: {
			{Kind: domain.PartKindReasoning, Body: "thinking line 1\nthinking line 2\nthinking line 3"},
			{Kind: domain.PartKindText, Body: strings.Repeat("text line ", 40)},
		},
	}

	m.resize()
	m.refreshViewport()
	m.viewport.SetYOffset(2)
	m.refreshViewportPreserve()

	beforeLines := m.viewport.VisibleSurface().Lines()
	if len(beforeLines) == 0 {
		t.Fatal("expected visible transcript lines before toggle")
	}
	beforeTop := strings.TrimRight(ansi.Strip(beforeLines[0]), " ")
	if beforeTop == "" {
		t.Fatalf("expected non-empty top line before toggle, got %q", beforeTop)
	}

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyRunes, Alt: true, Runes: []rune("r")})
	next := updated.(*Model)
	if cmd != nil {
		t.Fatalf("expected no command, got %v", cmd)
	}
	afterLines := next.viewport.VisibleSurface().Lines()
	if len(afterLines) == 0 {
		t.Fatal("expected visible transcript lines after toggle")
	}
	afterTop := strings.TrimRight(ansi.Strip(afterLines[0]), " ")
	if afterTop != beforeTop {
		t.Fatalf("expected top line %q to remain visible after toggle, got %q", beforeTop, afterTop)
	}
}

func TestRenderAgentsSidebarStatusColors(t *testing.T) {
	none := Model{}.renderAgentsSidebarStatus()
	if none != "None" {
		t.Fatalf("expected plain None status, got %q", none)
	}

	upToDate := Model{
		currentSession: domain.Session{ProjectChecksum: "abc"},
		workspace:      workspace.Status{AgentsFiles: 1, AgentsChecksum: "abc"},
	}.renderAgentsSidebarStatus()
	if upToDate != "Up to date" {
		t.Fatalf("expected plain Up to date status, got %q", upToDate)
	}

	changed := Model{
		currentSession: domain.Session{ProjectChecksum: "abc"},
		workspace:      workspace.Status{AgentsFiles: 1, AgentsChecksum: "def"},
	}.renderAgentsSidebarStatus()
	if changed != "Changed" {
		t.Fatalf("expected plain Changed status, got %q", changed)
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
	want := m.viewport.Height + (mainScreenVerticalInset * 2)
	if h := lipgloss.Height(got); h != want {
		t.Fatalf("expected body height %d, got %d from %q", want, h, got)
	}
}

func TestRenderBodyOmitsTranscriptBorder(t *testing.T) {
	m := Model{
		palette:  theme.Resolve("tokyonight").Palette,
		viewport: newTranscriptViewport(38, 6),
	}
	m.viewport.SetContent("history")

	got := ansi.Strip(m.renderBody())
	if strings.ContainsAny(got, "┌┐└┘│") {
		t.Fatalf("expected transcript pane without border, got %q", got)
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

func TestSyncRetainedTranscriptItemsReplacesMatchingKeys(t *testing.T) {
	retained := ui.NewRetainedTranscript()
	first := ui.TranscriptItem{
		Key:     "same",
		Element: ui.NewCachedElement(ui.Paragraph{Text: "before"}, 1),
	}
	second := ui.TranscriptItem{
		Key:     "same",
		Element: ui.NewCachedElement(ui.Paragraph{Text: "after"}, 1),
	}

	m := Model{}
	m.syncRetainedTranscriptItems(retained, []ui.TranscriptItem{first})
	m.syncRetainedTranscriptItems(retained, []ui.TranscriptItem{second})

	rendered := retained.Render(&ui.Context{}, ui.Rect{W: 16, H: 1})
	got := strings.Join(rendered.Lines(), "\n")
	if !strings.Contains(got, "after") {
		t.Fatalf("expected retained transcript item to be replaced, got %q", got)
	}
	if strings.Contains(got, "before") {
		t.Fatalf("expected stale retained transcript item to be removed, got %q", got)
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
		width:    40,
		composer: composer,
		currentChat: domain.Chat{QueuedInputs: []domain.QueuedInput{{
			ID:   1,
			Text: "queued submission",
			Kind: domain.QueuedInputKindQueued,
		}}},
	}

	got := ansi.Strip(m.renderFooter())
	if !strings.Contains(got, "Queued inputs") || !strings.Contains(got, "queued submission") {
		t.Fatalf("expected queued prompt preview above composer, got %q", got)
	}
	if strings.Index(got, "queued submission") > strings.Index(got, "Ask koder or type / for commands") {
		t.Fatalf("expected queued preview to render above composer, got %q", got)
	}
}

func TestRenderFooterOnlyUsesComposerHeightWhenNoAuxiliaryContent(t *testing.T) {
	composer := textarea.New()
	composer.Placeholder = "Ask koder or type / for commands"
	composer.SetHeight(composerInputHeight)
	composer.SetWidth(38)
	composer.Focus()

	m := Model{
		width:    40,
		composer: composer,
	}

	got := ansi.Strip(m.renderFooter())
	if height := lipgloss.Height(got); height != composerHeight {
		t.Fatalf("expected footer height %d with only composer visible, got %d in %q", composerHeight, height, got)
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

func TestViewShowsAllVisibleTranscriptLinesAboveComposer(t *testing.T) {
	composer := textarea.New()
	composer.Placeholder = "Ask koder or type / for commands"
	composer.SetHeight(composerInputHeight)
	composer.Focus()

	m := Model{
		cfg:            testConfig(t),
		palette:        theme.Resolve("tokyonight").Palette,
		width:          40,
		height:         12,
		composer:       composer,
		currentSession: domain.Session{ID: 1},
		messages: []domain.Message{
			{ID: 1, Role: domain.MessageRoleAssistant},
			{ID: 2, Role: domain.MessageRoleUser},
		},
		parts: map[int64][]domain.Part{
			1: {{Kind: domain.PartKindText, Body: "assistant context"}},
			2: {{Kind: domain.PartKindText, Body: "final user line one\nfinal user line two"}},
		},
		viewport: newTranscriptViewport(37, 8),
	}

	m.resize()
	m.refreshViewport()

	viewLines := strings.Split(strings.Join(m.viewSurface().Lines(), "\n"), "\n")
	transcriptLines := m.viewport.VisibleSurface().Lines()
	if len(transcriptLines) == 0 {
		t.Fatal("expected visible transcript lines")
	}
	composerIdx := indexOfTrimmedLineContaining(viewLines, "Ask koder or type / for commands")
	if composerIdx < 0 {
		t.Fatalf("expected composer in rendered view, got:\n%s", strings.Join(viewLines, "\n"))
	}
	lastTranscriptLine := strings.TrimRight(ansi.Strip(transcriptLines[len(transcriptLines)-1]), " ")
	lastTranscriptIdx := indexOfTrimmedLine(viewLines[:composerIdx], lastTranscriptLine)
	if lastTranscriptIdx < 0 {
		t.Fatalf("expected final visible transcript line %q before composer\nview:\n%s\n\ntranscript:\n%s", lastTranscriptLine, strings.Join(viewLines, "\n"), strings.Join(transcriptLines, "\n"))
	}
	if lastTranscriptIdx >= composerIdx {
		t.Fatalf("expected final visible transcript line before composer, got transcript=%d composer=%d", lastTranscriptIdx, composerIdx)
	}
}

func TestViewDoesNotLeaveLargeGapBetweenTranscriptAndComposer(t *testing.T) {
	composer := textarea.New()
	composer.Placeholder = "Ask koder or type / for commands"
	composer.SetHeight(composerInputHeight)
	composer.Focus()

	m := Model{
		cfg:            testConfig(t),
		palette:        theme.Resolve("tokyonight").Palette,
		width:          40,
		height:         12,
		composer:       composer,
		currentSession: domain.Session{ID: 1},
		messages: []domain.Message{
			{ID: 1, Role: domain.MessageRoleAssistant},
		},
		parts: map[int64][]domain.Part{
			1: {{Kind: domain.PartKindText, Body: "continue"}},
		},
		viewport: newTranscriptViewport(37, 8),
	}

	m.resize()
	m.refreshViewport()

	viewLines := strings.Split(strings.Join(m.viewSurface().Lines(), "\n"), "\n")
	composerIdx := indexOfTrimmedLineContaining(viewLines, "Ask koder or type / for commands")
	if composerIdx < 0 {
		t.Fatalf("expected composer in rendered view, got:\n%s", strings.Join(viewLines, "\n"))
	}
	lastContentIdx := lastNonEmptyTrimmedLineIndex(viewLines[:composerIdx])
	if lastContentIdx < 0 {
		t.Fatalf("expected transcript content before composer, got:\n%s", strings.Join(viewLines, "\n"))
	}
	gap := composerIdx - lastContentIdx - 1
	if gap > 1 {
		t.Fatalf("expected at most one spacer line between transcript and composer, got %d\nview:\n%s", gap, strings.Join(viewLines, "\n"))
	}
}

func TestViewShowsLastUserBubbleLineBeforeComposer(t *testing.T) {
	composer := textarea.New()
	composer.Placeholder = "Ask koder or type / for commands"
	composer.SetHeight(composerInputHeight)
	composer.Focus()

	m := Model{
		cfg:            testConfig(t),
		palette:        theme.Resolve("tokyonight").Palette,
		width:          40,
		height:         10,
		composer:       composer,
		currentSession: domain.Session{ID: 1},
		messages: []domain.Message{
			{ID: 1, Role: domain.MessageRoleUser},
		},
		parts: map[int64][]domain.Part{
			1: {{Kind: domain.PartKindText, Body: "final line"}},
		},
		viewport: newTranscriptViewport(37, 6),
	}

	m.resize()
	m.refreshViewport()

	viewLines := strings.Split(strings.Join(m.viewSurface().Lines(), "\n"), "\n")
	composerIdx := indexOfTrimmedLineContaining(viewLines, "Ask koder or type / for commands")
	if composerIdx < 0 {
		t.Fatalf("expected composer in rendered view, got:\n%s", strings.Join(viewLines, "\n"))
	}
	lastBubbleIdx := lastIndexOfTrimmedLineContaining(viewLines[:composerIdx], "▀")
	if lastBubbleIdx < 0 {
		t.Fatalf("expected last user bubble line before composer, got:\n%s", strings.Join(viewLines, "\n"))
	}
	if composerIdx <= lastBubbleIdx {
		t.Fatalf("expected user bubble to render before composer, got:\n%s", strings.Join(viewLines, "\n"))
	}
}

func indexOfTrimmedLine(lines []string, want string) int {
	for idx, line := range lines {
		if strings.Contains(strings.TrimRight(ansi.Strip(line), " "), want) {
			return idx
		}
	}
	return -1
}

func indexOfTrimmedLineContaining(lines []string, want string) int {
	for idx, line := range lines {
		if strings.Contains(strings.TrimRight(ansi.Strip(line), " "), want) {
			return idx
		}
	}
	return -1
}

func lastIndexOfTrimmedLineContaining(lines []string, want string) int {
	for idx := len(lines) - 1; idx >= 0; idx-- {
		if strings.Contains(strings.TrimRight(ansi.Strip(lines[idx]), " "), want) {
			return idx
		}
	}
	return -1
}

func lastNonEmptyTrimmedLineIndex(lines []string) int {
	for idx := len(lines) - 1; idx >= 0; idx-- {
		if strings.TrimSpace(ansi.Strip(lines[idx])) != "" {
			return idx
		}
	}
	return -1
}

func TestResizeUsesMeasuredFooterHeight(t *testing.T) {
	m := Model{
		width:    80,
		height:   24,
		composer: textarea.New(),
	}
	m.composer.SetHeight(4)

	m.resize()

	want := 24 - m.statusPaneHeight() - (mainScreenVerticalInset * 2)
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
	if got := m.renderTranscriptActivity(); !strings.Contains(got, "Compacting session...") || !strings.Contains(got, ui.SpinnerFrame(config.Default().UI.Spinner, 0)) {
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
	if strings.Contains(got, "\n\nthinking first") {
		t.Fatalf("expected no blank line before reasoning, got %q", got)
	}
	if !strings.Contains(got, "\n\nfinal answer") {
		t.Fatalf("expected blank line after reasoning before final answer, got %q", got)
	}
}

func TestRenderStyledMessagePartsShowsReasoningBeforeText(t *testing.T) {
	cfg := testConfig(t)
	m, err := New(cfg, nil, nil, StartupModeNew, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.showReasoning = true
	m.viewport.Width = 60

	got := ui.PlainStyledText(m.renderStyledMessageParts([]domain.Part{
		{Kind: domain.PartKindText, Body: "final answer"},
		{Kind: domain.PartKindReasoning, Body: "thinking first"},
	}))

	if !strings.Contains(got, "thinking first") || !strings.Contains(got, "final answer") {
		t.Fatalf("expected both reasoning and text, got %q", got)
	}
	if strings.Index(got, "thinking first") > strings.Index(got, "final answer") {
		t.Fatalf("expected reasoning before text, got %q", got)
	}
	if strings.Contains(got, "\n\nthinking first") {
		t.Fatalf("expected no blank line before reasoning, got %q", got)
	}
	if !strings.Contains(got, "\n\nfinal answer") {
		t.Fatalf("expected blank line after reasoning before final answer, got %q", got)
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

func TestRenderMessagePartsShowsEventNotice(t *testing.T) {
	m := Model{}

	got := m.renderMessageParts([]domain.Part{
		{Kind: domain.PartKindText, Body: "final answer"},
		{Kind: domain.PartKindEventNotice, Body: "Error: chat status 429"},
	})

	if !strings.Contains(got, "Error") || !strings.Contains(got, "chat status 429") {
		t.Fatalf("expected visible event notice, got %q", got)
	}
	if !strings.Contains(got, "final answer") {
		t.Fatalf("expected text to remain visible, got %q", got)
	}
}

func TestRenderMessagePartsSkipsLoopPauseEventNotice(t *testing.T) {
	m := Model{}

	got := m.renderMessageParts([]domain.Part{
		{Kind: domain.PartKindText, Body: "final answer"},
		{Kind: domain.PartKindEventNotice, Body: "Paused continuation after repeated identical read calls.", MetaJSON: `{"kind":"loop_pause","reason":"repeated_tool","tool":"read"}`},
	})

	if strings.Contains(got, "Paused continuation") {
		t.Fatalf("expected loop pause notice to render as a card instead of inline text, got %q", got)
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

func TestTranscriptRendersCompactionAsExpandableCard(t *testing.T) {
	m := Model{
		currentSession:   domain.Session{ID: 1},
		viewport:         newTranscriptViewport(120, 8),
		parts:            map[int64][]domain.Part{},
		expandedToolRuns: map[string]bool{},
		palette:          theme.Resolve("tokyonight").Palette,
	}
	m.messages = []domain.Message{
		{ID: 1, Role: domain.MessageRoleAssistant, Summary: "Compacted session summary"},
	}
	m.parts[1] = []domain.Part{{
		Kind: domain.PartKindCompaction,
		Body: "## Goal\n\n- first\n- second",
	}}

	m.refreshViewport()
	got := m.viewport.View()
	if !strings.Contains(got, "Compacted context") {
		t.Fatalf("expected compaction card title, got %q", got)
	}
	if !strings.Contains(got, "Expand") {
		t.Fatalf("expected compaction card to be collapsed by default, got %q", got)
	}
	if strings.Contains(got, "- second") {
		t.Fatalf("expected collapsed compaction card to hide part of the body, got %q", got)
	}

	m.expandedToolRuns["compaction:1"] = true
	m.refreshViewport()
	got = m.viewport.View()
	if !strings.Contains(got, "## Goal") || !strings.Contains(got, "- first") || !strings.Contains(got, "- second") {
		t.Fatalf("expected expanded compaction body, got %q", got)
	}
}

func TestTranscriptRendersLoopPauseAsCard(t *testing.T) {
	m := Model{
		currentSession:   domain.Session{ID: 1},
		viewport:         newTranscriptViewport(80, 8),
		parts:            map[int64][]domain.Part{},
		expandedToolRuns: map[string]bool{},
		palette:          theme.Resolve("tokyonight").Palette,
	}
	m.messages = []domain.Message{
		{ID: 1, Role: domain.MessageRoleAssistant, Summary: "Paused continuation"},
	}
	m.parts[1] = []domain.Part{{
		Kind:     domain.PartKindEventNotice,
		Body:     "Paused continuation after 3 identical read calls with the same input.",
		MetaJSON: `{"kind":"loop_pause","reason":"repeated_tool","title":"Continuation paused","subtitle":"Repeated identical read calls","tool":"read"}`,
	}}

	m.refreshViewport()
	got := m.viewport.View()
	if !strings.Contains(got, "Continuation paused") {
		t.Fatalf("expected loop pause card title, got %q", got)
	}
	if !strings.Contains(got, "Paused") {
		t.Fatalf("expected loop pause card status, got %q", got)
	}
	if !strings.Contains(got, "Repeated identical read calls") {
		t.Fatalf("expected loop pause card subtitle, got %q", got)
	}
}

func TestRenderReasoningBlockStartsWithoutBlankLine(t *testing.T) {
	m := Model{}

	got := m.renderReasoningBlock("thinking first")
	lines := strings.Split(got, "\n")
	if len(lines) < 1 {
		t.Fatalf("expected reasoning output, got %q", got)
	}
	if lines[0] == "" {
		t.Fatalf("expected first line to contain reasoning text, got %q", got)
	}
	if !strings.Contains(lines[0], "thinking first") {
		t.Fatalf("expected reasoning text on first line, got %q", got)
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

func TestMouseWheelScrollRefreshesRetainedTranscriptSurface(t *testing.T) {
	composer := textarea.New()
	composer.Placeholder = "Ask koder or type / for commands"
	composer.SetHeight(composerInputHeight)
	composer.Focus()

	m := Model{
		cfg:            testConfig(t),
		palette:        theme.Resolve("tokyonight").Palette,
		mouseEnabled:   true,
		width:          40,
		height:         12,
		composer:       composer,
		currentSession: domain.Session{ID: 1},
		messages: []domain.Message{
			{ID: 1, Role: domain.MessageRoleAssistant},
		},
		parts: map[int64][]domain.Part{
			1: {{Kind: domain.PartKindText, Body: strings.Join([]string{
				"line 1",
				"line 2",
				"line 3",
				"line 4",
				"line 5",
				"line 6",
				"line 7",
				"line 8",
				"line 9",
				"line 10",
				"line 11",
				"line 12",
				"line 13",
				"line 14",
				"line 15",
				"line 16",
			}, "\n")}},
		},
		viewport: newTranscriptViewport(37, 8),
	}

	m.resize()
	m.refreshViewport()
	if m.viewport.YOffset == 0 {
		t.Fatalf("expected retained transcript to start below top for scroll test, got offset %d", m.viewport.YOffset)
	}
	before := strings.Join(m.viewSurface().Lines(), "\n")
	if !strings.Contains(before, "line 12") {
		t.Fatalf("expected initial view to show transcript tail, got:\n%s", before)
	}

	updated, _ := m.Update(ui.MouseMsg{
		Action: ui.MouseActionPress,
		Button: ui.MouseButtonWheelUp,
		X:      5,
		Y:      3,
	})
	next := updated.(Model)
	after := strings.Join(next.viewSurface().Lines(), "\n")
	if !strings.Contains(after, "line 5") {
		t.Fatalf("expected scrolled retained transcript to show earlier lines, got:\n%s", after)
	}
	if strings.Contains(after, "line 16") {
		t.Fatalf("expected scrolled retained transcript surface to change, got:\n%s", after)
	}
}

func TestMouseWheelScrollCanReturnToTrueTranscriptBottom(t *testing.T) {
	composer := textarea.New()
	composer.Placeholder = "Ask koder or type / for commands"
	composer.SetHeight(composerInputHeight)
	composer.Focus()

	m := Model{
		cfg:          testConfig(t),
		palette:      theme.Resolve("tokyonight").Palette,
		mouseEnabled: true,
		width:        40,
		height:       12,
		composer:     composer,
		currentSession: domain.Session{
			ID: 1,
		},
		messages: []domain.Message{
			{ID: 1, Role: domain.MessageRoleAssistant},
		},
		parts: map[int64][]domain.Part{
			1: {{Kind: domain.PartKindText, Body: strings.Join([]string{
				"line 1",
				"line 2",
				"line 3",
				"line 4",
				"line 5",
				"line 6",
				"line 7",
				"line 8",
				"line 9",
				"line 10",
				"line 11",
				"line 12",
				"line 13",
				"line 14",
				"line 15",
				"line 16",
			}, "\n")}},
		},
		viewport: newTranscriptViewport(37, 8),
	}

	m.resize()
	m.refreshViewport()

	updated, _ := m.Update(ui.MouseMsg{
		Action: ui.MouseActionPress,
		Button: ui.MouseButtonWheelUp,
		X:      5,
		Y:      3,
	})
	scrolledUp := updated.(Model)
	if scrolledUp.viewport.YOffset >= m.viewport.YOffset {
		t.Fatalf("expected upward wheel scroll to reduce offset from %d, got %d", m.viewport.YOffset, scrolledUp.viewport.YOffset)
	}

	current := scrolledUp
	for range 8 {
		updated, _ = current.Update(ui.MouseMsg{
			Action: ui.MouseActionPress,
			Button: ui.MouseButtonWheelDown,
			X:      5,
			Y:      3,
		})
		current = updated.(Model)
	}

	got := strings.Join(current.viewSurface().Lines(), "\n")
	if !strings.Contains(got, "line 16") {
		t.Fatalf("expected scrolling back down to reach transcript tail, got:\n%s", got)
	}
	if current.viewport.YOffset != current.viewport.maxYOffset() {
		t.Fatalf("expected final offset %d at bottom, got %d", current.viewport.maxYOffset(), current.viewport.YOffset)
	}
}

func TestMouseClickTogglesToolRunExpansion(t *testing.T) {
	m := Model{
		mouseEnabled:            true,
		currentSession:          domain.Session{ID: 1},
		viewport:                newTranscriptViewport(80, 8),
		parts:                   map[int64][]domain.Part{},
		expandedToolRuns:        map[string]bool{},
		expandedToolRunCommands: map[string]bool{},
		palette:                 theme.Resolve("tokyonight").Palette,
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
	if !strings.Contains(m.viewport.View(), "Expand ou") {
		t.Fatalf("expected expand indicator, got %q", m.viewport.View())
	}

	_ = m.viewSurface()
	clickX := -1
	clickY := -1
	controlWidth := -1
	for _, control := range m.transcriptControls {
		if control.ID != "toolrun:call_bash_1:output" {
			continue
		}
		clickX = control.Rect.X + 1
		clickY = control.Rect.Y
		controlWidth = control.Rect.W
		break
	}
	if clickX < 0 || clickY < 0 {
		t.Fatalf("expected toolrun control to be registered, got %#v", m.transcriptControls)
	}
	if controlWidth != ui.PlainWidth("Expand output (1 line)") {
		t.Fatalf("expected expand control width %d, got %d", ui.PlainWidth("Expand output (1 line)"), controlWidth)
	}

	updated, cmd := m.Update(ui.MouseMsg{
		Action: ui.MouseActionPress,
		Button: ui.MouseButtonLeft,
		X:      clickX,
		Y:      clickY,
	})
	var next Model
	switch typed := updated.(type) {
	case Model:
		next = typed
	case *Model:
		next = *typed
	default:
		t.Fatalf("unexpected model type %T", updated)
	}
	if cmd != nil {
		t.Fatal("expected no command from tool run mouse toggle")
	}
	if !strings.Contains(next.viewport.View(), "line one") {
		t.Fatalf("expected expanded tool output, got %q", next.viewport.View())
	}
	if !strings.Contains(next.viewport.View(), "Collapse") {
		t.Fatalf("expected collapse indicator, got %q", next.viewport.View())
	}

	updated, _ = next.Update(ui.MouseMsg{
		Action: ui.MouseActionPress,
		Button: ui.MouseButtonLeft,
		X:      clickX,
		Y:      clickY,
	})
	var final Model
	switch typed := updated.(type) {
	case Model:
		final = typed
	case *Model:
		final = *typed
	default:
		t.Fatalf("unexpected model type %T", updated)
	}
	if strings.Contains(final.viewport.View(), "line one\nline two") {
		t.Fatalf("expected collapsed tool output after second click, got %q", final.viewport.View())
	}
}

func TestMouseClickTogglesEditToolRunExpansion(t *testing.T) {
	m := Model{
		mouseEnabled:     true,
		currentSession:   domain.Session{ID: 1},
		viewport:         newTranscriptViewport(80, 8),
		parts:            map[int64][]domain.Part{},
		expandedToolRuns: map[string]bool{},
		palette:          theme.Resolve("tokyonight").Palette,
	}

	meta := mustMarshalMeta(t, tools.MetaWithStoredResult(map[string]string{
		"tool":         "edit",
		"path":         "game/sim_test.go",
		"tool_call_id": "call_edit_1",
	}, domain.PartKindToolOutput, domain.ToolKindEdit, tools.StoredResultStatusOK, tools.EditStoredResult{
		Path:    "game/sim_test.go",
		Summary: "Edited game/sim_test.go (replaced 1 occurrence)",
		Diff:    "--- game/sim_test.go\n+++ game/sim_test.go\n@@ -1,1 +1,1 @@\n-old\n+new",
	}))

	m.messages = []domain.Message{
		{ID: 1, Role: domain.MessageRoleTool, Summary: "edit"},
	}
	m.parts[1] = []domain.Part{{
		Kind:     domain.PartKindToolOutput,
		Body:     "Edited game/sim_test.go (replaced 1 occurrence)",
		MetaJSON: meta,
	}}

	m.refreshViewport()
	if strings.Contains(m.viewport.View(), "--- game/sim_test.go") {
		t.Fatalf("expected collapsed edit output, got %q", m.viewport.View())
	}
	if !strings.Contains(m.viewport.View(), "Expand (4 lines)") {
		t.Fatalf("expected expand indicator, got %q", m.viewport.View())
	}

	_ = m.viewSurface()
	clickX := -1
	clickY := -1
	for _, control := range m.transcriptControls {
		if control.ID != "toolrun:call_edit_1:output" {
			continue
		}
		clickX = control.Rect.X + 1
		clickY = control.Rect.Y
		break
	}
	if clickX < 0 || clickY < 0 {
		t.Fatalf("expected edit toolrun control to be registered, got %#v", m.transcriptControls)
	}

	updated, cmd := m.Update(ui.MouseMsg{
		Action: ui.MouseActionPress,
		Button: ui.MouseButtonLeft,
		X:      clickX,
		Y:      clickY,
	})
	var next Model
	switch typed := updated.(type) {
	case Model:
		next = typed
	case *Model:
		next = *typed
	default:
		t.Fatalf("unexpected model type %T", updated)
	}
	if cmd != nil {
		t.Fatal("expected no command from edit tool run mouse toggle")
	}
	if !strings.Contains(next.viewport.View(), "--- game/sim_test.go") {
		t.Fatalf("expected expanded edit output, got %q", next.viewport.View())
	}
	if !strings.Contains(next.viewport.View(), "Collapse") {
		t.Fatalf("expected collapse indicator, got %q", next.viewport.View())
	}
}

func TestMouseClickTogglesWrappedEditToolRunExpansion(t *testing.T) {
	m := Model{
		mouseEnabled:     true,
		currentSession:   domain.Session{ID: 1},
		viewport:         newTranscriptViewport(28, 10),
		parts:            map[int64][]domain.Part{},
		expandedToolRuns: map[string]bool{},
		palette:          theme.Resolve("tokyonight").Palette,
	}

	meta := mustMarshalMeta(t, tools.MetaWithStoredResult(map[string]string{
		"tool":         "edit",
		"path":         "game/sim_test.go",
		"tool_call_id": "call_edit_wrap_1",
	}, domain.PartKindToolOutput, domain.ToolKindEdit, tools.StoredResultStatusOK, tools.EditStoredResult{
		Path:    "game/sim_test.go",
		Summary: "Edited game/sim_test.go (replaced 1 occurrence)",
		Diff:    "--- game/sim_test.go\n+++ game/sim_test.go\n@@ -1,1 +1,1 @@\n-old\n+new",
	}))

	m.messages = []domain.Message{
		{ID: 1, Role: domain.MessageRoleTool, Summary: "edit"},
	}
	m.parts[1] = []domain.Part{{
		Kind:     domain.PartKindToolOutput,
		Body:     "Edited game/sim_test.go (replaced 1 occurrence)",
		MetaJSON: meta,
	}}

	m.refreshViewport()
	_ = m.viewSurface()
	var wrappedControl *ui.Control
	for i := range m.transcriptControls {
		control := &m.transcriptControls[i]
		if control.ID == "toolrun:call_edit_wrap_1:output" {
			wrappedControl = control
			break
		}
	}
	if wrappedControl == nil {
		t.Fatalf("expected wrapped edit toolrun control to be registered, got %#v", m.transcriptControls)
	}

	updated, cmd := m.Update(ui.MouseMsg{
		Action: ui.MouseActionPress,
		Button: ui.MouseButtonLeft,
		X:      wrappedControl.Rect.X + 1,
		Y:      wrappedControl.Rect.Y,
	})
	var next Model
	switch typed := updated.(type) {
	case Model:
		next = typed
	case *Model:
		next = *typed
	default:
		t.Fatalf("unexpected model type %T", updated)
	}
	if cmd != nil {
		t.Fatal("expected no command from wrapped edit tool run mouse toggle")
	}
	if !strings.Contains(next.viewport.View(), "--- game/sim_test.go") {
		t.Fatalf("expected expanded wrapped edit output, got %q", next.viewport.View())
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
	if !strings.Contains(m.viewport.View(), "Expand (1 line)") {
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
		Diff:    "--- game/map.go\n+++ game/map.go\n@@ -12,1 +12,1 @@\n-if oldCondition {\n+if newCondition {",
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
		"Edited file game/map.go",
		"--- game/map.go",
		"+++ game/map.go",
		"@@ -12,1 +12,1 @@",
		"-if oldCondition {",
		"+if newCondition {",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in %q", want, got)
		}
	}
}

func TestBashToolRunUsesRanCommandTitleAndCollapsedFirstOutputLine(t *testing.T) {
	m := Model{
		currentSession:          domain.Session{ID: 1},
		viewport:                newTranscriptViewport(100, 8),
		parts:                   map[int64][]domain.Part{},
		expandedToolRuns:        map[string]bool{},
		expandedToolRunCommands: map[string]bool{},
		palette:                 theme.Resolve("tokyonight").Palette,
	}

	meta := mustMarshalMeta(t, tools.MetaWithStoredResult(map[string]string{
		"tool":         "bash",
		"command":      "printf 'hello\\nworld\\n'",
		"tool_call_id": "call_bash_1",
	}, domain.PartKindToolOutput, domain.ToolKindBash, tools.StoredResultStatusOK, tools.BashStoredResult{
		Command: "printf 'hello\\nworld\\n'",
		Output:  "line one\nline two\n",
	}))

	m.messages = []domain.Message{
		{ID: 1, Role: domain.MessageRoleTool, Summary: "bash"},
	}
	m.parts[1] = []domain.Part{{
		Kind:     domain.PartKindToolOutput,
		Body:     "line one\nline two\n",
		MetaJSON: meta,
	}}

	m.refreshViewport()
	got := m.viewport.View()
	if !strings.Contains(got, "Ran command printf 'hello\\nworld\\n'") {
		t.Fatalf("expected bash title to include command, got %q", got)
	}
	if strings.Contains(got, "world\\n'  Expand command") {
		t.Fatalf("expected multiline command to stay out of collapsed header, got %q", got)
	}
	if !strings.Contains(got, "line one") {
		t.Fatalf("expected collapsed bash card to show first output line, got %q", got)
	}
	if strings.Contains(got, "line two") {
		t.Fatalf("expected collapsed bash card to hide later output lines, got %q", got)
	}
	if !strings.Contains(got, "Expand output (1 line)") {
		t.Fatalf("expected output expand label, got %q", got)
	}
}

func TestResumedBashToolRunReplacesRequestTitleWithCompletedTitle(t *testing.T) {
	m := Model{
		currentSession:          domain.Session{ID: 1},
		viewport:                newTranscriptViewport(100, 8),
		parts:                   map[int64][]domain.Part{},
		expandedToolRuns:        map[string]bool{},
		expandedToolRunCommands: map[string]bool{},
		palette:                 theme.Resolve("tokyonight").Palette,
	}

	callMeta := mustMarshalMeta(t, map[string]string{
		"tool":         "bash",
		"command":      "printf 'line one\\nline two\\n'",
		"tool_call_id": "call_bash_1",
	})
	outputMeta := mustMarshalMeta(t, tools.MetaWithStoredResult(map[string]string{
		"tool":         "bash",
		"command":      "printf 'line one\\nline two\\n'",
		"tool_call_id": "call_bash_1",
	}, domain.PartKindToolOutput, domain.ToolKindBash, tools.StoredResultStatusOK, tools.BashStoredResult{
		Command: "printf 'line one\\nline two\\n'",
		Output:  "line one\nline two\n",
	}))

	m.messages = []domain.Message{
		{ID: 1, Role: domain.MessageRoleAssistant, Summary: "tool:bash"},
		{ID: 2, Role: domain.MessageRoleTool, Summary: "bash"},
	}
	m.parts[1] = []domain.Part{{
		Kind:     domain.PartKindToolCall,
		Body:     `{"command":"printf 'line one\\nline two\\n'","tool":"bash","tool_call_id":"call_bash_1"}`,
		MetaJSON: callMeta,
	}}
	m.parts[2] = []domain.Part{{
		Kind:     domain.PartKindToolOutput,
		Body:     "line one\nline two\n",
		MetaJSON: outputMeta,
	}}

	m.refreshViewport()
	got := m.viewport.View()
	if !strings.Contains(got, "Ran command printf 'line one\\nline two\\n'") {
		t.Fatalf("expected resumed bash title to include command, got %q", got)
	}
	if strings.Contains(got, "Run command") {
		t.Fatalf("expected resumed bash title to replace request title, got %q", got)
	}
}

func TestResumedEditToolRunReplacesRequestTitleWithCompletedTitle(t *testing.T) {
	m := Model{
		currentSession:   domain.Session{ID: 1},
		viewport:         newTranscriptViewport(100, 8),
		parts:            map[int64][]domain.Part{},
		expandedToolRuns: map[string]bool{},
		palette:          theme.Resolve("tokyonight").Palette,
	}

	callMeta := mustMarshalMeta(t, map[string]string{
		"tool":         "edit",
		"path":         "game/map.go",
		"tool_call_id": "call_edit_1",
	})
	outputMeta := mustMarshalMeta(t, tools.MetaWithStoredResult(map[string]string{
		"tool":         "edit",
		"path":         "game/map.go",
		"tool_call_id": "call_edit_1",
	}, domain.PartKindToolOutput, domain.ToolKindEdit, tools.StoredResultStatusOK, tools.EditStoredResult{
		Path:    "game/map.go",
		Summary: "Edited game/map.go (replaced 1 occurrence)",
	}))

	m.messages = []domain.Message{
		{ID: 1, Role: domain.MessageRoleAssistant, Summary: "tool:edit"},
		{ID: 2, Role: domain.MessageRoleTool, Summary: "edit"},
	}
	m.parts[1] = []domain.Part{{
		Kind:     domain.PartKindToolCall,
		Body:     `{"path":"game/map.go","tool":"edit","tool_call_id":"call_edit_1"}`,
		MetaJSON: callMeta,
	}}
	m.parts[2] = []domain.Part{{
		Kind:     domain.PartKindToolOutput,
		Body:     "Edited game/map.go (replaced 1 occurrence)",
		MetaJSON: outputMeta,
	}}

	m.refreshViewport()
	got := m.viewport.View()
	if !strings.Contains(got, "Edited file game/map.go") {
		t.Fatalf("expected resumed edit title to include path, got %q", got)
	}
	if strings.Contains(got, "Edit file") {
		t.Fatalf("expected resumed edit title to replace request title, got %q", got)
	}
}

func TestMouseClickOnApprovalPromptPermissionsOpensPicker(t *testing.T) {
	m := Model{
		mouseEnabled: true,
		width:        160,
		height:       28,
		palette:      theme.Resolve("tokyonight").Palette,
		composer:     textarea.New(),
		approvals: []store.Approval{{
			ID:      7,
			Tool:    domain.ToolKindRead,
			Command: `{"path":"README.md"}`,
		}},
	}

	prompt := m.renderApprovalPrompt()
	lines := strings.Split(prompt, "\n")
	buttonLine := -1
	buttonX := -1
	for idx, line := range lines {
		stripped := ansi.Strip(line)
		if !strings.Contains(stripped, "Switch permissions") {
			continue
		}
		buttonLine = idx
		buttonX = strings.Index(stripped, "Switch permissions") + 1
		break
	}
	if buttonLine < 0 || buttonX < 0 {
		t.Fatalf("failed to find approval dialog buttons in view: %q", prompt)
	}

	bounds := m.centeredWindowBounds(m.renderApprovalDialogElement())
	updated, cmd := m.Update(ui.MouseMsg{
		Action: ui.MouseActionPress,
		Button: ui.MouseButtonLeft,
		X:      bounds.X + buttonX,
		Y:      bounds.Y + buttonLine,
	})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("expected title sync command when opening permissions picker")
	}
	if !next.hasPicker() {
		t.Fatal("expected permissions picker to open from approval dialog mouse click")
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
	for i, line := range lines {
		switch {
		case firstTitleLine == -1 && strings.Contains(line, "Read file"):
			firstTitleLine = i
		case firstTitleLine != -1 && secondTitleLine == -1 && strings.Contains(line, "Read file"):
			secondTitleLine = i
		}
	}
	if firstTitleLine == -1 || secondTitleLine == -1 {
		t.Fatalf("expected both grouped tool runs to render, got %q", got)
	}
	if secondTitleLine <= firstTitleLine+1 {
		t.Fatalf("expected second tool run to appear after a blank spacer row, got %q", got)
	}
	if strings.TrimSpace(lines[secondTitleLine-1]) != "" {
		t.Fatalf("expected blank spacer row between consecutive tool runs, got %q", got)
	}
}

func TestTranscriptBlocksKeepsRepeatedFailedToolRunsSeparate(t *testing.T) {
	m := Model{
		currentSession: domain.Session{ID: 1},
		parts:          map[int64][]domain.Part{},
	}
	m.messages = []domain.Message{
		{ID: 1, Role: domain.MessageRoleAssistant, Summary: "tool:bash"},
		{ID: 2, Role: domain.MessageRoleTool, Summary: "approval:bash"},
		{ID: 3, Role: domain.MessageRoleTool, Summary: "approval:bash:approved"},
		{ID: 4, Role: domain.MessageRoleTool, Summary: "bash"},
		{ID: 5, Role: domain.MessageRoleUser, Summary: "continue"},
		{ID: 6, Role: domain.MessageRoleAssistant, Summary: "tool:bash"},
		{ID: 7, Role: domain.MessageRoleTool, Summary: "approval:bash"},
		{ID: 8, Role: domain.MessageRoleTool, Summary: "approval:bash:approved"},
		{ID: 9, Role: domain.MessageRoleTool, Summary: "bash"},
	}
	m.parts[1] = []domain.Part{{
		Kind: domain.PartKindToolCall,
		MetaJSON: mustMarshalMeta(t, map[string]string{
			"tool":         string(domain.ToolKindBash),
			"command":      `go build -v . 2>&1; echo "EXIT_CODE=$?"`,
			"tool_call_id": "call_1",
		}),
	}}
	m.parts[2] = []domain.Part{{
		Kind:     domain.PartKindApprovalRequest,
		Body:     `Approval required for bash: go build -v . 2>&1; echo "EXIT_CODE=$?"`,
		MetaJSON: mustMarshalMeta(t, map[string]string{"approval_id": "1", "command": `go build -v . 2>&1; echo "EXIT_CODE=$?"`, "status": "pending", "tool": string(domain.ToolKindBash), "tool_call_id": "call_1"}),
	}}
	m.parts[3] = []domain.Part{{
		Kind:     domain.PartKindSystemNotice,
		Body:     `Approval 1 approved for bash: go build -v . 2>&1; echo "EXIT_CODE=$?"`,
		MetaJSON: mustMarshalMeta(t, map[string]string{"approval_id": "1", "command": `go build -v . 2>&1; echo "EXIT_CODE=$?"`, "status": "approved", "tool": string(domain.ToolKindBash), "tool_call_id": "call_1"}),
	}}
	m.parts[4] = []domain.Part{{
		Kind:     domain.PartKindToolOutput,
		Body:     "bash failed: timeout_ms must be a positive integer",
		MetaJSON: mustMarshalMeta(t, map[string]string{"tool": string(domain.ToolKindBash), "command": `go build -v . 2>&1; echo "EXIT_CODE=$?"`, "tool_call_id": "call_1"}),
	}}
	m.parts[5] = []domain.Part{{Kind: domain.PartKindText, Body: "continue"}}
	m.parts[6] = []domain.Part{{
		Kind: domain.PartKindToolCall,
		MetaJSON: mustMarshalMeta(t, map[string]string{
			"tool":         string(domain.ToolKindBash),
			"command":      `go build -v . 2>&1; echo "EXIT_CODE=$?"`,
			"tool_call_id": "call_2",
		}),
	}}
	m.parts[7] = []domain.Part{{
		Kind:     domain.PartKindApprovalRequest,
		Body:     `Approval required for bash: go build -v . 2>&1; echo "EXIT_CODE=$?"`,
		MetaJSON: mustMarshalMeta(t, map[string]string{"approval_id": "2", "command": `go build -v . 2>&1; echo "EXIT_CODE=$?"`, "status": "pending", "tool": string(domain.ToolKindBash), "tool_call_id": "call_2"}),
	}}
	m.parts[8] = []domain.Part{{
		Kind:     domain.PartKindSystemNotice,
		Body:     `Approval 2 approved for bash: go build -v . 2>&1; echo "EXIT_CODE=$?"`,
		MetaJSON: mustMarshalMeta(t, map[string]string{"approval_id": "2", "command": `go build -v . 2>&1; echo "EXIT_CODE=$?"`, "status": "approved", "tool": string(domain.ToolKindBash), "tool_call_id": "call_2"}),
	}}
	m.parts[9] = []domain.Part{{
		Kind:     domain.PartKindToolOutput,
		Body:     "bash failed: timeout_ms must be a positive integer",
		MetaJSON: mustMarshalMeta(t, map[string]string{"tool": string(domain.ToolKindBash), "command": `go build -v . 2>&1; echo "EXIT_CODE=$?"`, "tool_call_id": "call_2"}),
	}}

	blocks := m.transcriptBlocks()
	runCount := 0
	lastKind := transcriptBlockKind(-1)
	for _, block := range blocks {
		if block.Kind == transcriptBlockToolRun {
			runCount++
		}
		lastKind = block.Kind
	}
	if runCount != 2 {
		t.Fatalf("expected two separate tool runs, got %d from %#v", runCount, blocks)
	}
	if lastKind != transcriptBlockToolRun {
		t.Fatalf("expected tail block to remain the second tool run, got %#v", blocks[len(blocks)-1])
	}
}
