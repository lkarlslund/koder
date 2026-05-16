package uicore

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/agent"
	"github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/store"
)

func TestControllerStartCreatesSessionAndPublishesState(t *testing.T) {
	ctrl, _ := newTestController(t)
	events, unsub := ctrl.Subscribe()
	defer unsub()

	state := ctrl.State()
	if state.Session.ID == "" {
		t.Fatal("expected active session")
	}
	if state.ActiveChatID == "" {
		t.Fatal("expected active chat")
	}
	if len(state.ChatStatuses) == 0 {
		t.Fatal("expected chat sidebar statuses")
	}
	select {
	case event := <-events:
		if event.Type == "snapshot" {
			t.Fatalf("expected subscriptions to avoid unsolicited full snapshots, got %q", event.Type)
		}
	case <-time.After(20 * time.Millisecond):
	}
}

func TestControllerNewChatAndSwitchChat(t *testing.T) {
	ctrl, _ := newTestController(t)
	first := ctrl.State().ActiveChatID
	if first == "" {
		t.Fatal("expected first chat")
	}
	if err := ctrl.NewChat(context.Background(), "side chat"); err != nil {
		t.Fatalf("new chat: %v", err)
	}
	second := ctrl.State().ActiveChatID
	if second == "" || second == first {
		t.Fatalf("expected new active chat, first=%s second=%s", first, second)
	}
	if err := ctrl.SwitchChat(context.Background(), first); err != nil {
		t.Fatalf("switch chat: %v", err)
	}
	if got := ctrl.State().ActiveChatID; got != first {
		t.Fatalf("expected active chat %s, got %s", first, got)
	}
}

func TestControllerDeleteInactiveChat(t *testing.T) {
	ctrl, st := newTestController(t)
	active := ctrl.State().ActiveChatID
	if err := ctrl.NewChat(context.Background(), "side chat"); err != nil {
		t.Fatalf("new chat: %v", err)
	}
	side := ctrl.State().ActiveChatID
	if err := ctrl.SwitchChat(context.Background(), active); err != nil {
		t.Fatalf("switch chat: %v", err)
	}
	if err := ctrl.DeleteChat(context.Background(), side); err != nil {
		t.Fatalf("delete chat: %v", err)
	}
	if got := ctrl.State().ActiveChatID; got != active {
		t.Fatalf("expected active chat to stay %s, got %s", active, got)
	}
	if _, err := st.GetChat(context.Background(), side); err == nil {
		t.Fatal("expected side chat to be deleted")
	}
}

func TestControllerDeleteActiveChatSwitchesToRemainingChat(t *testing.T) {
	ctrl, st := newTestController(t)
	first := ctrl.State().ActiveChatID
	if err := ctrl.NewChat(context.Background(), "side chat"); err != nil {
		t.Fatalf("new chat: %v", err)
	}
	side := ctrl.State().ActiveChatID
	if err := ctrl.DeleteChat(context.Background(), side); err != nil {
		t.Fatalf("delete active chat: %v", err)
	}
	if got := ctrl.State().ActiveChatID; got != first {
		t.Fatalf("expected active chat to switch to %s, got %s", first, got)
	}
	if _, err := st.GetChat(context.Background(), side); err == nil {
		t.Fatal("expected side chat to be deleted")
	}
}

func TestControllerForwardRuntimeRefreshesChatListMetadata(t *testing.T) {
	ctrl, _ := newTestController(t)
	state := ctrl.State()
	if state.ActiveChatID == "" {
		t.Fatal("expected active chat")
	}
	ctrl.mu.Lock()
	if ctrl.unsub != nil {
		ctrl.unsub()
		ctrl.unsub = nil
	}
	for _, unsub := range ctrl.unsubs {
		if unsub != nil {
			unsub()
		}
	}
	ctrl.unsubs = nil
	ctrl.runtime = nil
	ctrl.mu.Unlock()
	updated := state.Snapshot
	updated.Chat.ID = state.ActiveChatID
	updated.Chat.Title = "Generated Chat Title"
	updated.Status = chat.StatusRunningTools
	updated.StatusText = "Running tools"
	updates := make(chan chat.Update, 1)
	updates <- chat.Update{Snapshot: updated, Status: chat.StatusRunningTools, StatusText: "Running tools", Active: true}
	close(updates)

	ctrl.forwardRuntime(state.ActiveChatID, updates)

	got := ctrl.State()
	var listed string
	for _, item := range got.Chats {
		if item.ID == state.ActiveChatID {
			listed = item.Title
			break
		}
	}
	if listed != "Generated Chat Title" {
		t.Fatalf("expected chat list title updated, got %q", listed)
	}
	var sidebarStatus ChatSidebarStatus
	for _, item := range got.ChatStatuses {
		if item.ChatID == state.ActiveChatID {
			sidebarStatus = item
			break
		}
	}
	if sidebarStatus.Status != string(chat.StatusRunningTools) || !sidebarStatus.Busy {
		t.Fatalf("expected active chat sidebar status running tools, got %#v", sidebarStatus)
	}
}

func TestControllerForwardRuntimeRefreshesInactiveChatMetadata(t *testing.T) {
	ctrl, _ := newTestController(t)
	first := ctrl.State().ActiveChatID
	if first == "" {
		t.Fatal("expected first chat")
	}
	if err := ctrl.NewChat(context.Background(), "side chat"); err != nil {
		t.Fatalf("new chat: %v", err)
	}
	side := ctrl.State().ActiveChatID
	if side == "" || side == first {
		t.Fatalf("expected side chat, first=%s side=%s", first, side)
	}
	if err := ctrl.SwitchChat(context.Background(), first); err != nil {
		t.Fatalf("switch chat: %v", err)
	}

	updated := ctrl.State().Snapshots[side]
	updated.Chat.ID = side
	updated.Chat.Title = "Generated Side Title"
	updated.Status = chat.StatusWaitingApproval
	updated.StatusText = "Waiting for approval"
	updates := make(chan chat.Update, 1)
	updates <- chat.Update{Snapshot: updated, Status: chat.StatusWaitingApproval, StatusText: "Waiting for approval", Active: true}
	close(updates)

	ctrl.forwardRuntime(side, updates)

	got := ctrl.State()
	if got.ActiveChatID != first {
		t.Fatalf("expected active chat to remain %s, got %s", first, got.ActiveChatID)
	}
	if got.Snapshots[side].Chat.Title != "Generated Side Title" {
		t.Fatalf("expected inactive snapshot title updated, got %#v", got.Snapshots[side].Chat)
	}
	var listed string
	for _, item := range got.Chats {
		if item.ID == side {
			listed = item.Title
			break
		}
	}
	if listed != "Generated Side Title" {
		t.Fatalf("expected inactive chat list title updated, got %q", listed)
	}
	var sidebarStatus ChatSidebarStatus
	for _, item := range got.ChatStatuses {
		if item.ChatID == side {
			sidebarStatus = item
			break
		}
	}
	if sidebarStatus.Status != string(chat.StatusWaitingApproval) || !sidebarStatus.Busy {
		t.Fatalf("expected inactive chat sidebar status waiting approval, got %#v", sidebarStatus)
	}
}

func TestControllerRefreshChatStatusesDiscoversNewStoreChats(t *testing.T) {
	ctrl, st := newTestController(t)
	state := ctrl.State()
	if state.Session.ID == "" || state.ActiveChatID == "" {
		t.Fatal("expected active session and chat")
	}
	parentID := state.ActiveChatID
	created, err := st.CreateChat(context.Background(), state.Session.ID, "Worker", chatrole.Execution, &parentID)
	if err != nil {
		t.Fatalf("create worker chat: %v", err)
	}
	created.ActiveMilestoneRef = "alpha"
	created.AssignedTodoBucketRef = "alpha"
	if err := st.UpdateChat(context.Background(), created); err != nil {
		t.Fatalf("update worker chat: %v", err)
	}

	if !ctrl.refreshChatStatuses(context.Background(), state.Session.ID) {
		t.Fatal("expected refreshed chat list to report a change")
	}
	next := ctrl.State()
	found := false
	for _, item := range next.Chats {
		if item.ID == created.ID && item.Title == "Worker" && item.ActiveMilestoneRef == "alpha" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected worker chat in sidebar state, got %#v", next.Chats)
	}
}

func TestControllerModelOptionsLoadsConfiguredModels(t *testing.T) {
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		fmt.Fprint(w, `{"data":[{"id":"z-model","owned_by":"remote"},{"id":"a-model"}]}`)
	}))
	defer modelServer.Close()

	ctrl, _ := newTestControllerWithConfig(t, func(cfg *config.Config) {
		cfg.Providers = map[string]config.Provider{
			"test": {Name: "Test Provider", BaseURL: modelServer.URL + "/v1", DefaultModel: "default-model"},
		}
	})

	options, err := ctrl.ModelOptions(context.Background())
	if err != nil {
		t.Fatalf("model options: %v", err)
	}
	got := make([]string, 0, len(options))
	for _, option := range options {
		got = append(got, option.ProviderID+"/"+option.ModelID)
	}
	want := []string{"test/a-model", "test/default-model", "test/z-model"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("expected options %v, got %v", want, got)
	}
}

func TestControllerModelOptionsDoesNotInventMissingCurrentProvider(t *testing.T) {
	ctrl, st := newTestControllerWithConfig(t, func(cfg *config.Config) {
		cfg.Providers = map[string]config.Provider{
			"test": {Name: "Test Provider", BaseURL: "https://example.invalid/v1", DefaultModel: "configured-model"},
		}
	})
	session := ctrl.State().Session
	if err := st.SetSessionModel(context.Background(), session.ID, "ghost", "ghost-model"); err != nil {
		t.Fatal(err)
	}
	session, err := st.GetSession(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	ctrl.mu.Lock()
	ctrl.session = session
	ctrl.mu.Unlock()

	options, err := ctrl.ModelOptions(context.Background())
	if err != nil {
		t.Fatalf("model options: %v", err)
	}
	for _, option := range options {
		if option.ProviderID == "ghost" || option.ModelID == "ghost-model" {
			t.Fatalf("unexpected stale current model option: %#v", options)
		}
	}
}

func TestControllerModelOptionsReportsProviderFailureWithoutDefaults(t *testing.T) {
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer modelServer.Close()

	ctrl, _ := newTestControllerWithConfig(t, func(cfg *config.Config) {
		cfg.Providers = map[string]config.Provider{
			"test": {Name: "Test Provider", BaseURL: modelServer.URL + "/v1"},
		}
	})

	_, err := ctrl.ModelOptions(context.Background())
	if err == nil || !strings.Contains(err.Error(), "failed to load models from test") {
		t.Fatalf("expected provider failure, got %v", err)
	}
}

func TestControllerSavePreferencesPersistsConfigAndPrompts(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", temp)
	t.Setenv("XDG_STATE_HOME", temp)
	t.Setenv("XDG_CACHE_HOME", temp)
	t.Setenv("HOME", temp)

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Providers = map[string]config.Provider{
		"test": {BaseURL: "https://example.invalid/v1", DefaultModel: "model", ContextWindow: 32768, Stream: true, Timeout: time.Minute},
	}
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "model"
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	st, err := store.OpenWithOptions(cfg.StateDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	engine := agent.New(cfg, st, nil, nil, t.TempDir())
	ctrl := New(cfg, st, engine, t.TempDir())
	if err := ctrl.Start(context.Background(), StartupModeNew); err != nil {
		t.Fatal(err)
	}

	prefs, err := ctrl.Preferences(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	prefs.General.MaxToolLoopSteps = 77
	prefs.UI.Theme = "dark"
	prefs.Compaction.UseChatModel = false
	prefs.Compaction.ProviderID = "test"
	prefs.Compaction.ModelID = "compact-model"
	prefs.Compaction.AutoCompactAt = 66
	prefs.Compaction.KeepToolBatches = 3
	for idx := range prefs.Prompts {
		if prefs.Prompts[idx].Target == "compaction-prompt.md" {
			prefs.Prompts[idx].Content = "custom compact prompt\n"
		}
	}
	updated, err := ctrl.SavePreferences(context.Background(), prefs)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Compaction.ModelID != "compact-model" {
		t.Fatalf("expected updated preferences response, got %#v", updated.Compaction)
	}

	loaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.MaxToolLoopSteps != 77 || loaded.UI.Theme != "dark" || loaded.CompactionModel != "compact-model" {
		t.Fatalf("expected saved config, got max=%d theme=%q compact=%q/%q", loaded.MaxToolLoopSteps, loaded.UI.Theme, loaded.CompactionProvider, loaded.CompactionModel)
	}
	data, err := os.ReadFile(filepath.Join(temp, ".koder", "compaction-prompt.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "custom compact prompt\n" {
		t.Fatalf("expected prompt file update, got %q", string(data))
	}
}

func TestControllerResetPromptRestoresEmbeddedDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ctrl, _ := newTestController(t)
	path := filepath.Join(home, ".koder", "compaction-prompt.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}

	prompt, err := ctrl.ResetPrompt("compaction-prompt.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt.Content, "Summarize this coding session") {
		t.Fatalf("expected embedded compaction prompt, got %q", prompt.Content)
	}
}

func TestControllerSetModelUpdatesStoreStateAndRuntimeSnapshot(t *testing.T) {
	ctrl, st := newTestControllerWithConfig(t, func(cfg *config.Config) {
		cfg.Providers = map[string]config.Provider{
			"test": {BaseURL: "https://example.invalid/v1", DefaultModel: "model", ContextWindow: 12345},
		}
	})
	if err := ctrl.SetModel(context.Background(), "test", "next-model"); err != nil {
		t.Fatalf("set model: %v", err)
	}

	state := ctrl.State()
	if state.Session.ProviderID != "test" || state.Session.ModelID != "next-model" {
		t.Fatalf("expected state model test/next-model, got %s/%s", state.Session.ProviderID, state.Session.ModelID)
	}
	if state.Snapshot.Session.ProviderID != "test" || state.Snapshot.Session.ModelID != "next-model" {
		t.Fatalf("expected runtime snapshot model test/next-model, got %s/%s", state.Snapshot.Session.ProviderID, state.Snapshot.Session.ModelID)
	}
	if state.ContextWindow != 12345 {
		t.Fatalf("expected context window 12345, got %d", state.ContextWindow)
	}
	if state.ModelInfo.ProviderID != "test" || state.ModelInfo.ModelID != "next-model" || state.ModelInfo.ContextWindow != 12345 || !state.ModelInfo.SupportsTools {
		t.Fatalf("unexpected model info: %#v", state.ModelInfo)
	}
	session, err := st.GetSession(context.Background(), state.Session.ID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if session.ModelID != "next-model" {
		t.Fatalf("expected stored model next-model, got %q", session.ModelID)
	}
}

func TestControllerSetPermissionProfileUpdatesActiveSession(t *testing.T) {
	ctrl, st := newTestController(t)
	sessionID := ctrl.State().Session.ID
	if err := ctrl.SetPermissionProfile(context.Background(), "auto"); err != nil {
		t.Fatalf("set permission profile: %v", err)
	}
	session, err := st.GetSession(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if session.PermissionProfile != "auto" {
		t.Fatalf("expected session permission profile auto, got %q", session.PermissionProfile)
	}
	if got := ctrl.State().Permissions.Active; got != "auto" {
		t.Fatalf("expected active permission profile auto, got %q", got)
	}
}

func TestControllerPermissionOptionsMatchConfiguredProfiles(t *testing.T) {
	ctrl, _ := newTestController(t)

	var got []string
	for _, profile := range ctrl.State().Permissions.Profiles {
		got = append(got, profile.Name)
	}
	want := []string{"auto", "default", "readonly", "ask", "read-ask", "write-ask", "full-access"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("expected configured profiles before builtin extras, got %v", got)
	}
}

func TestControllerPermissionProfilePersistsBySession(t *testing.T) {
	workdir := t.TempDir()
	cfg := config.Default().WithStateDir(t.TempDir())
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "model"
	st, err := store.OpenWithOptions(cfg.StateDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctrl := New(cfg, st, agent.New(cfg, st, nil, nil, workdir), workdir)
	if err := ctrl.Start(context.Background(), StartupModeNew); err != nil {
		t.Fatalf("start controller: %v", err)
	}
	if err := ctrl.SetPermissionProfile(context.Background(), "auto"); err != nil {
		t.Fatalf("set permission profile: %v", err)
	}
	sessionID := ctrl.State().Session.ID
	if err := ctrl.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	engine := agent.New(cfg, st, nil, nil, workdir)
	next := New(cfg, st, engine, workdir)
	if err := next.loadSession(context.Background(), sessionID, ""); err != nil {
		t.Fatalf("start next controller: %v", err)
	}
	if got := next.State().Permissions.Active; got != "auto" {
		t.Fatalf("expected session permission profile to persist, got %q", got)
	}
}

func TestControllerSetPermissionProfileSurvivesRuntimeUpdate(t *testing.T) {
	ctrl, _ := newTestController(t)
	if err := ctrl.SetPermissionProfile(context.Background(), "auto"); err != nil {
		t.Fatalf("set permission profile: %v", err)
	}
	rt := ctrl.currentRuntime()
	if rt == nil {
		t.Fatal("expected runtime")
	}
	events, unsub := ctrl.Subscribe()
	defer unsub()
	rt.SetSession(ctrl.State().Session)

	deadline := time.After(time.Second)
	for {
		select {
		case event := <-events:
			if event.Type != "snapshot" && event.Type != "chat_update" {
				continue
			}
			if got := ctrl.State().Permissions.Active; got != "auto" {
				t.Fatalf("expected runtime update to preserve active permission profile, got %q", got)
			}
			return
		case <-deadline:
			t.Fatalf("expected runtime update to preserve active permission profile, got %q", ctrl.State().Permissions.Active)
		}
	}
}

func TestControllerSetPermissionProfileRejectsUnknownProfile(t *testing.T) {
	ctrl, _ := newTestController(t)
	if err := ctrl.SetPermissionProfile(context.Background(), "nope"); err == nil {
		t.Fatal("expected unknown permission profile error")
	}
}

func TestControllerSessionsAreWorkspaceScoped(t *testing.T) {
	cfg := config.Default().WithStateDir(t.TempDir())
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "model"
	st, err := store.OpenWithOptions(cfg.StateDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ctx := context.Background()
	workspaceA := t.TempDir()
	workspaceB := t.TempDir()
	sessionA, err := st.CreateSession(ctx, "Workspace A", "test", "model", nil)
	if err != nil {
		t.Fatalf("create session a: %v", err)
	}
	if err := st.UpdateSessionWorkspace(ctx, sessionA.ID, workspaceA, workspaceA); err != nil {
		t.Fatalf("workspace a: %v", err)
	}
	sessionB, err := st.CreateSession(ctx, "Workspace B", "test", "model", nil)
	if err != nil {
		t.Fatalf("create session b: %v", err)
	}
	if err := st.UpdateSessionWorkspace(ctx, sessionB.ID, workspaceB, workspaceB); err != nil {
		t.Fatalf("workspace b: %v", err)
	}

	engine := agent.New(cfg, st, nil, nil, workspaceA)
	ctrl := New(cfg, st, engine, workspaceA)
	if err := ctrl.Start(ctx, StartupModeResume); err != nil {
		t.Fatalf("start resume: %v", err)
	}
	if got := ctrl.State().Session.ID; got != sessionA.ID {
		t.Fatalf("expected workspace A session %s, got %s", sessionA.ID, got)
	}
	sessionState, err := ctrl.Sessions(ctx)
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}
	if len(sessionState.Sessions) != 1 || sessionState.Sessions[0].ID != sessionA.ID {
		t.Fatalf("expected only workspace A session, got %#v", sessionState.Sessions)
	}
	if err := ctrl.SwitchSession(ctx, sessionB.ID); err == nil {
		t.Fatal("expected switch to other workspace session to fail")
	}
	if err := ctrl.NewSession(ctx, "Second A"); err != nil {
		t.Fatalf("new session: %v", err)
	}
	sessionState, err = ctrl.Sessions(ctx)
	if err != nil {
		t.Fatalf("sessions after new: %v", err)
	}
	if len(sessionState.Sessions) != 2 {
		t.Fatalf("expected two workspace A sessions, got %#v", sessionState.Sessions)
	}
}

func TestControllerStartupNewResumesRestartInterruptedWorkspaceSession(t *testing.T) {
	var requests atomic.Int32
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		requests.Add(1)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"resumed"}}],"usage":{"total_tokens":1}}`))
	}))
	defer providerServer.Close()

	cfg := config.Default().WithStateDir(t.TempDir())
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "model"
	cfg.Providers = map[string]config.Provider{
		"test": {BaseURL: providerServer.URL + "/v1", DefaultModel: "model"},
	}
	st, err := store.OpenWithOptions(cfg.StateDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ctx := context.Background()
	workdir := t.TempDir()
	session, err := st.CreateSession(ctx, "Interrupted Session", "test", "model", nil)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := st.UpdateSessionWorkspace(ctx, session.ID, workdir, workdir); err != nil {
		t.Fatalf("workspace: %v", err)
	}
	chatRecord, err := st.DefaultChat(ctx, session.ID)
	if err != nil {
		t.Fatalf("default chat: %v", err)
	}
	notice, err := st.AppendTimeline(ctx, chatRecord.ID, domain.Notice{
		Level:  "warning",
		Text:   "Interrupted",
		Kind:   domain.NoticeKindInterrupted,
		Reason: domain.NoticeReasonProcessRestart,
	})
	if err != nil {
		t.Fatalf("append notice: %v", err)
	}
	notice.Seal(time.Now().UTC())
	if err := st.Timeline().Put(ctx, notice); err != nil {
		t.Fatalf("put notice: %v", err)
	}

	engine := agent.New(cfg, st, nil, nil, workdir)
	ctrl := New(cfg, st, engine, workdir)
	if err := ctrl.Start(ctx, StartupModeNew); err != nil {
		t.Fatalf("start controller: %v", err)
	}
	if got := ctrl.State().Session.ID; got != session.ID {
		t.Fatalf("expected restart interrupted session %s, got %s", session.ID, got)
	}
	deadline := time.After(2 * time.Second)
	for requests.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for auto resume provider request")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestControllerRefreshWorkspacePublishesGitStatus(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	workdir := t.TempDir()
	runGit(t, workdir, "init")
	runGit(t, workdir, "config", "user.email", "test@example.com")
	runGit(t, workdir, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(workdir, "tracked.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, workdir, "add", "tracked.txt")
	runGit(t, workdir, "commit", "-m", "initial")
	if err := os.WriteFile(filepath.Join(workdir, "tracked.txt"), []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "new.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default().WithStateDir(t.TempDir())
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "model"
	st, err := store.OpenWithOptions(cfg.StateDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	engine := agent.New(cfg, st, nil, nil, workdir)
	ctrl := New(cfg, st, engine, workdir)
	if err := ctrl.Start(ctx, StartupModeNew); err != nil {
		t.Fatalf("start controller: %v", err)
	}

	state := ctrl.State()
	if !state.Workspace.Available {
		t.Fatalf("expected git workspace status, got %#v", state.Workspace)
	}
	if state.Workspace.Modified != 1 || state.Workspace.Untracked != 1 {
		t.Fatalf("expected modified and untracked counts, got %#v", state.Workspace)
	}

	events, unsub := ctrl.Subscribe()
	defer unsub()
	<-events
	if err := os.WriteFile(filepath.Join(workdir, "tracked.txt"), []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ctrl.RefreshWorkspace(ctx); err != nil {
		t.Fatalf("refresh workspace: %v", err)
	}
	deadline := time.After(time.Second)
	for {
		select {
		case event := <-events:
			if event.Type == "snapshot" {
				return
			}
		case <-deadline:
			t.Fatal("expected workspace refresh snapshot")
		}
	}
}

func newTestController(t *testing.T) (*Controller, *store.Store) {
	t.Helper()
	return newTestControllerWithConfig(t, nil)
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, string(out))
	}
}

func newTestControllerWithConfig(t *testing.T, edit func(*config.Config)) (*Controller, *store.Store) {
	t.Helper()
	cfg := config.Default().WithStateDir(t.TempDir())
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "model"
	if edit != nil {
		edit(&cfg)
	}
	st, err := store.OpenWithOptions(cfg.StateDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	engine := agent.New(cfg, st, nil, nil, t.TempDir())
	ctrl := New(cfg, st, engine, t.TempDir())
	if err := ctrl.Start(context.Background(), StartupModeNew); err != nil {
		t.Fatalf("start controller: %v", err)
	}
	return ctrl, st
}

func TestNewestSessionUsesUpdatedAtThenID(t *testing.T) {
	now := time.Now()
	got := newestSession([]domain.Session{
		{ID: "session-1", UpdatedAt: now},
		{ID: "session-2", UpdatedAt: now},
		{ID: "session-3", UpdatedAt: now.Add(-time.Second)},
	})
	if got.ID != "session-2" {
		t.Fatalf("expected newest session 2, got %s", got.ID)
	}
}
