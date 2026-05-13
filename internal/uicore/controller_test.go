package uicore

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/agent"
	"github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/store"
)

func TestControllerStartCreatesSessionAndPublishesState(t *testing.T) {
	ctrl, _ := newTestController(t)
	events, unsub := ctrl.Subscribe()
	defer unsub()

	state := ctrl.State()
	if state.Session.ID == 0 {
		t.Fatal("expected active session")
	}
	if state.ActiveChatID == 0 {
		t.Fatal("expected active chat")
	}
	select {
	case event := <-events:
		if event.Type != "snapshot" {
			t.Fatalf("expected initial snapshot event, got %q", event.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("expected initial snapshot event")
	}
}

func TestControllerNewChatAndSwitchChat(t *testing.T) {
	ctrl, _ := newTestController(t)
	first := ctrl.State().ActiveChatID
	if first == 0 {
		t.Fatal("expected first chat")
	}
	if err := ctrl.NewChat(context.Background(), "side chat"); err != nil {
		t.Fatalf("new chat: %v", err)
	}
	second := ctrl.State().ActiveChatID
	if second == 0 || second == first {
		t.Fatalf("expected new active chat, first=%d second=%d", first, second)
	}
	if err := ctrl.SwitchChat(context.Background(), first); err != nil {
		t.Fatalf("switch chat: %v", err)
	}
	if got := ctrl.State().ActiveChatID; got != first {
		t.Fatalf("expected active chat %d, got %d", first, got)
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
		t.Fatalf("expected active chat to stay %d, got %d", active, got)
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
		t.Fatalf("expected active chat to switch to %d, got %d", first, got)
	}
	if _, err := st.GetChat(context.Background(), side); err == nil {
		t.Fatal("expected side chat to be deleted")
	}
}

func TestControllerForwardRuntimeRefreshesChatListMetadata(t *testing.T) {
	ctrl, _ := newTestController(t)
	state := ctrl.State()
	if state.ActiveChatID == 0 {
		t.Fatal("expected active chat")
	}
	updated := state.Snapshot
	updated.Chat.ID = state.ActiveChatID
	updated.Chat.Title = "Generated Chat Title"
	updates := make(chan chat.Update, 1)
	updates <- chat.Update{Snapshot: updated}
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
	want := []string{"test/a-model", "test/default-model", "test/model", "test/z-model"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("expected options %v, got %v", want, got)
	}
}

func TestControllerSetModelUpdatesStoreStateAndRuntimeSnapshot(t *testing.T) {
	ctrl, st := newTestControllerWithConfig(t, func(cfg *config.Config) {
		cfg.Providers = map[string]config.Provider{
			"test": {BaseURL: "https://example.invalid/v1", DefaultModel: "model"},
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
	session, err := st.GetSession(context.Background(), state.Session.ID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if session.ModelID != "next-model" {
		t.Fatalf("expected stored model next-model, got %q", session.ModelID)
	}
}

func TestControllerSetPermissionProfileUpdatesActiveChat(t *testing.T) {
	ctrl, st := newTestController(t)
	chatID := ctrl.State().ActiveChatID
	if err := ctrl.SetPermissionProfile(context.Background(), "write-ask"); err != nil {
		t.Fatalf("set permission profile: %v", err)
	}
	chat, err := st.GetChat(context.Background(), chatID)
	if err != nil {
		t.Fatalf("get chat: %v", err)
	}
	if chat.PermissionProfile != "write-ask" {
		t.Fatalf("expected chat permission profile write-ask, got %q", chat.PermissionProfile)
	}
	if got := ctrl.State().Permissions.Active; got != "write-ask" {
		t.Fatalf("expected active permission profile write-ask, got %q", got)
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
		t.Fatalf("expected workspace A session %d, got %d", sessionA.ID, got)
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
	_ = <-events
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
		{ID: 1, UpdatedAt: now},
		{ID: 2, UpdatedAt: now},
		{ID: 3, UpdatedAt: now.Add(-time.Second)},
	})
	if got.ID != 2 {
		t.Fatalf("expected newest session 2, got %d", got.ID)
	}
}
