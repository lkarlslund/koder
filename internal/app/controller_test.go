package app

import (
	"context"
	"fmt"
	"io"
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
	"github.com/lkarlslund/koder/internal/execruntime"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
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

func TestControllerStateIncludesCurrentChatExecProcesses(t *testing.T) {
	ctrl, _, execManager := newTestControllerWithExec(t)
	state := ctrl.State()
	events, unsub := ctrl.Subscribe()
	defer unsub()
	snap, err := execManager.Start(context.Background(), execruntime.StartRequest{
		SessionID: state.Session.ID,
		ChatID:    state.ActiveChatID,
		Command:   "printf hi",
		YieldTime: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("start exec: %v", err)
	}
	deadline := time.After(2 * time.Second)
	for {
		current := ctrl.State().Snapshots[state.ActiveChatID].ExecProcesses
		for _, process := range current {
			if process.ProcessID == snap.ProcessID && strings.Contains(process.Output, "hi") {
				return
			}
		}
		select {
		case <-events:
		case <-deadline:
			t.Fatalf("timed out waiting for exec process in state, got %#v", current)
		}
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
	archived, err := st.GetChat(context.Background(), side)
	if err != nil {
		t.Fatal(err)
	}
	if !archived.Archived {
		t.Fatalf("expected side chat to be archived, got %#v", archived)
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
	archived, err := st.GetChat(context.Background(), side)
	if err != nil {
		t.Fatal(err)
	}
	if !archived.Archived {
		t.Fatalf("expected side chat to be archived, got %#v", archived)
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

func TestControllerStartChatAddsCreatedChatToSession(t *testing.T) {
	ctrl, _ := newTestController(t)
	state := ctrl.State()
	if state.Session.ID == "" || state.ActiveChatID == "" {
		t.Fatal("expected active session and chat")
	}
	if _, err := ctrl.SetMilestonePlan(context.Background(), state.Session.ID, "Ship it", []store.Milestone{
		{Ref: "alpha", Title: "Alpha", Status: domain.MilestoneStatusReady, Position: 0},
	}); err != nil {
		t.Fatalf("set milestone plan: %v", err)
	}
	todos, err := ctrl.AddTodoItems(context.Background(), state.Session.ID, "alpha", []string{"Implement alpha"})
	if err != nil {
		t.Fatalf("add todo: %v", err)
	}

	status, err := ctrl.StartChat(context.Background(), state.Session.ID, state.ActiveChatID, tools.ChatStartRequest{
		Profile:   chatrole.Execution,
		Objective: "Implement only the assigned todo",
		TodoRef:   todos[0].ID,
	})
	if err != nil {
		t.Fatalf("start chat: %v", err)
	}
	next := ctrl.State()
	found := false
	for _, item := range next.Chats {
		if item.ID == status.Chat.ID && item.ActiveMilestoneRef == "alpha" && item.AssignedTodoRef == todos[0].ID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected started chat in sidebar state, got %#v", next.Chats)
	}
	if _, ok := next.Snapshots[status.Chat.ID]; !ok {
		t.Fatalf("expected started chat snapshot, got %#v", next.Snapshots)
	}
}

func TestControllerModelOptionsLoadsLiveModels(t *testing.T) {
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		fmt.Fprint(w, `{"data":[{"id":"z-model","owned_by":"remote"},{"id":"a-model"}]}`)
	}))
	defer modelServer.Close()

	ctrl, _ := newTestControllerWithConfig(t, func(cfg *config.Config) {
		cfg.Providers = map[string]config.Provider{
			"test": {Name: "Test Provider", BaseURL: modelServer.URL + "/v1"},
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
	want := []string{"test/a-model", "test/z-model"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("expected options %v, got %v", want, got)
	}
}

func TestControllerModelOptionsDoesNotInventMissingCurrentProvider(t *testing.T) {
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		fmt.Fprint(w, `{"data":[{"id":"live-model"}]}`)
	}))
	defer modelServer.Close()

	ctrl, st := newTestControllerWithConfig(t, func(cfg *config.Config) {
		cfg.Providers = map[string]config.Provider{
			"test": {Name: "Test Provider", BaseURL: modelServer.URL + "/v1"},
		}
	})
	_ = st

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
		"test": {BaseURL: "https://example.invalid/v1", Stream: true, Timeout: time.Minute},
	}
	cfg.SetModelConfig(config.ModelConfig{ProviderID: "test", ModelID: "model", ContextWindow: 32768})
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
	projectRoot := t.TempDir()
	engine := agent.New(cfg, st, nil, nil)
	ctrl := New(cfg, st, engine)
	if err := ctrl.Start(context.Background(), StartupModeNew, projectRoot); err != nil {
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
	prefs.ModelConfigs = []ModelConfigPreference{{
		ProviderID:    "test",
		ModelID:       "model",
		ContextWindow: 12345,
		ModelPreset:   provider.ModelPresetDefault,
	}}
	prefs.MCPServers = []MCPServerPreference{{
		ID:             "docs",
		Name:           "Docs",
		URL:            "https://mcp.example.invalid/sse",
		Headers:        map[string]string{"X-Test": "yes"},
		RequestTimeout: "45s",
	}}
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
	if !modelOptionsContain(updated.Models, "test", "compact-model") {
		t.Fatalf("expected saved compaction model in preferences options, got %#v", updated.Models)
	}

	loaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.MaxToolLoopSteps != 77 || loaded.UI.Theme != "dark" || loaded.CompactionModel != "compact-model" {
		t.Fatalf("expected saved config, got max=%d theme=%q compact=%q/%q", loaded.MaxToolLoopSteps, loaded.UI.Theme, loaded.CompactionProvider, loaded.CompactionModel)
	}
	if got := loaded.ContextWindow("test", "model"); got != 12345 {
		t.Fatalf("expected saved model context window, got %d", got)
	}
	if got := loaded.ModelPreset("test", "model"); got != provider.ModelPresetDefault {
		t.Fatalf("expected saved model preset, got %q", got)
	}
	if loaded.MCPServers["docs"].URL != "https://mcp.example.invalid/sse" || loaded.MCPServers["docs"].Headers["X-Test"] != "yes" {
		t.Fatalf("expected saved MCP server, got %#v", loaded.MCPServers["docs"])
	}
	restarted := New(loaded, st, agent.New(loaded, st, nil, nil))
	if err := restarted.Start(context.Background(), StartupModeNew, projectRoot); err != nil {
		t.Fatal(err)
	}
	restartedPrefs, err := restarted.Preferences(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if restartedPrefs.Compaction.UseChatModel || restartedPrefs.Compaction.ProviderID != "test" || restartedPrefs.Compaction.ModelID != "compact-model" {
		t.Fatalf("expected restart preferences to restore compaction model, got %#v", restartedPrefs.Compaction)
	}
	if !modelOptionsContain(restartedPrefs.Models, "test", "compact-model") {
		t.Fatalf("expected restart preferences options to include compaction model, got %#v", restartedPrefs.Models)
	}
	data, err := os.ReadFile(filepath.Join(temp, ".koder", "compaction-prompt.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "custom compact prompt\n" {
		t.Fatalf("expected prompt file update, got %q", string(data))
	}
}

func TestControllerSavePreferencesRepairsDeletedDefaultProvider(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", temp)
	t.Setenv("XDG_STATE_HOME", temp)
	t.Setenv("XDG_CACHE_HOME", temp)
	t.Setenv("HOME", temp)

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "old-model"
	cfg.Providers = map[string]config.Provider{
		"test":  {BaseURL: "https://example.invalid/v1"},
		"other": {BaseURL: "https://example.invalid/v1"},
	}
	cfg.SetModelConfig(config.ModelConfig{ProviderID: "test", ModelID: "old-model", ContextWindow: 32768})
	cfg.SetModelConfig(config.ModelConfig{ProviderID: "other", ModelID: "new-model", ContextWindow: 32768})
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	st, err := store.OpenWithOptions(cfg.StateDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	projectRoot := t.TempDir()
	ctrl := New(cfg, st, agent.New(cfg, st, nil, nil))
	if err := ctrl.Start(context.Background(), StartupModeNew, projectRoot); err != nil {
		t.Fatal(err)
	}

	prefs, err := ctrl.Preferences(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	prefs.General.DefaultProvider = "test"
	prefs.General.DefaultModel = "old-model"
	prefs.Providers.DefaultProvider = "other"
	prefs.Providers.DefaultModel = "new-model"

	if _, err := ctrl.DeleteProvider(context.Background(), "test"); err != nil {
		t.Fatal(err)
	}
	updated, err := ctrl.SavePreferences(context.Background(), prefs)
	if err != nil {
		t.Fatal(err)
	}
	if updated.General.DefaultProvider != "other" || updated.General.DefaultModel != "new-model" {
		t.Fatalf("expected repaired default provider, got %#v", updated.General)
	}
}

func modelOptionsContain(options []ModelOption, providerID, modelID string) bool {
	for _, option := range options {
		if option.ProviderID == providerID && option.ModelID == modelID {
			return true
		}
	}
	return false
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
			"test": {BaseURL: "https://example.invalid/v1"},
		}
		cfg.SetModelConfig(config.ModelConfig{ProviderID: "test", ModelID: "next-model", ContextWindow: 12345})
	})
	if err := ctrl.SetModel(context.Background(), "test", "next-model"); err != nil {
		t.Fatalf("set model: %v", err)
	}

	state := ctrl.State()
	if state.Snapshot.Chat.ProviderID != "test" || state.Snapshot.Chat.ModelID != "next-model" {
		t.Fatalf("expected state chat model test/next-model, got %s/%s", state.Snapshot.Chat.ProviderID, state.Snapshot.Chat.ModelID)
	}
	if state.ContextWindow != 12345 {
		t.Fatalf("expected context window 12345, got %d", state.ContextWindow)
	}
	if state.ModelInfo.ProviderID != "test" || state.ModelInfo.ModelID != "next-model" || state.ModelInfo.ContextWindow != 12345 || !state.ModelInfo.SupportsTools {
		t.Fatalf("unexpected model info: %#v", state.ModelInfo)
	}
	chatRecord, err := st.GetChat(context.Background(), state.Snapshot.Chat.ID)
	if err != nil {
		t.Fatalf("get chat: %v", err)
	}
	if chatRecord.ModelID != "next-model" {
		t.Fatalf("expected stored chat model next-model, got %q", chatRecord.ModelID)
	}
}

func TestControllerStartDetectsActiveModelContextWindow(t *testing.T) {
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"model","status":{"args":["llama-server","--ctx-size","262144"]}}]}`))
		default:
			t.Fatalf("unexpected model server path: %s", r.URL.Path)
		}
	}))
	defer modelServer.Close()

	ctrl, _ := newTestControllerWithConfig(t, func(cfg *config.Config) {
		cfg.Providers = map[string]config.Provider{
			"test": {Kind: provider.ProviderKindCompatible, BaseURL: modelServer.URL, Timeout: time.Second},
		}
	})

	state := ctrl.State()
	if state.ContextWindow != 262144 {
		t.Fatalf("expected detected context window 262144, got %d", state.ContextWindow)
	}
	if state.ModelInfo.ContextWindow != 262144 {
		t.Fatalf("expected model info context window 262144, got %#v", state.ModelInfo)
	}
}

func TestControllerSetPermissionProfileUpdatesActiveSession(t *testing.T) {
	ctrl, st := newTestController(t)
	sessionID := ctrl.State().Session.ID
	if err := ctrl.SetPermissionProfile(context.Background(), "dev-network"); err != nil {
		t.Fatalf("set permission profile: %v", err)
	}
	session, err := st.GetSession(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if session.PermissionProfile != "dev-network" {
		t.Fatalf("expected session permission profile dev-network, got %q", session.PermissionProfile)
	}
	if got := ctrl.State().Permissions.Active; got != "dev-network" {
		t.Fatalf("expected active permission profile dev-network, got %q", got)
	}
}

func TestControllerPermissionOptionsMatchConfiguredProfiles(t *testing.T) {
	ctrl, _ := newTestController(t)

	var got []string
	for _, profile := range ctrl.State().Permissions.Profiles {
		got = append(got, profile.Name)
	}
	want := []string{"default", "dev-network", "full-access", "readonly"}
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
	ctrl := New(cfg, st, agent.New(cfg, st, nil, nil))
	if err := ctrl.Start(context.Background(), StartupModeNew, workdir); err != nil {
		t.Fatalf("start controller: %v", err)
	}
	if err := ctrl.SetPermissionProfile(context.Background(), "dev-network"); err != nil {
		t.Fatalf("set permission profile: %v", err)
	}
	sessionID := ctrl.State().Session.ID
	if err := ctrl.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	engine := agent.New(cfg, st, nil, nil)
	next := New(cfg, st, engine)
	if err := next.loadSession(context.Background(), sessionID, ""); err != nil {
		t.Fatalf("start next controller: %v", err)
	}
	if got := next.State().Permissions.Active; got != "dev-network" {
		t.Fatalf("expected session permission profile to persist, got %q", got)
	}
}

func TestControllerSetPermissionProfileSurvivesRuntimeUpdate(t *testing.T) {
	ctrl, _ := newTestController(t)
	if err := ctrl.SetPermissionProfile(context.Background(), "dev-network"); err != nil {
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
			if got := ctrl.State().Permissions.Active; got != "dev-network" {
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

func TestControllerSessionsCanUseDifferentProjectRoots(t *testing.T) {
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
	if err := st.SetSessionProjectRoot(ctx, sessionA.ID, workspaceA); err != nil {
		t.Fatalf("workspace a: %v", err)
	}
	sessionB, err := st.CreateSession(ctx, "Workspace B", "test", "model", nil)
	if err != nil {
		t.Fatalf("create session b: %v", err)
	}
	if err := st.SetSessionProjectRoot(ctx, sessionB.ID, workspaceB); err != nil {
		t.Fatalf("workspace b: %v", err)
	}

	engine := agent.New(cfg, st, nil, nil)
	ctrl := New(cfg, st, engine)
	if err := ctrl.Start(ctx, StartupModeResume, workspaceA); err != nil {
		t.Fatalf("start resume: %v", err)
	}
	if got := ctrl.State().Session.ID; got != sessionB.ID {
		t.Fatalf("expected newest session %s, got %s", sessionB.ID, got)
	}
	sessionState, err := ctrl.Sessions(ctx)
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}
	if len(sessionState.Sessions) != 2 {
		t.Fatalf("expected both project-root sessions, got %#v", sessionState.Sessions)
	}
	if err := ctrl.SwitchSession(ctx, sessionA.ID); err != nil {
		t.Fatalf("switch to other project root session: %v", err)
	}
	if err := ctrl.NewSession(ctx, "Second A"); err != nil {
		t.Fatalf("new session: %v", err)
	}
	if got := ctrl.State().Session.ProjectRoot; got != workspaceA {
		t.Fatalf("expected new session to inherit active project root %q, got %q", workspaceA, got)
	}
	sessionState, err = ctrl.Sessions(ctx)
	if err != nil {
		t.Fatalf("sessions after new: %v", err)
	}
	if len(sessionState.Sessions) != 3 {
		t.Fatalf("expected three sessions, got %#v", sessionState.Sessions)
	}
}

func TestControllerKeepsRuntimesForMultipleLoadedSessions(t *testing.T) {
	ctrl, st := newTestController(t)
	ctx := context.Background()

	firstState := ctrl.State()
	firstSessionID := firstState.Session.ID
	firstChatID := firstState.ActiveChatID
	if err := ctrl.NewSession(ctx, "Second"); err != nil {
		t.Fatalf("new session: %v", err)
	}
	secondState := ctrl.State()
	if secondState.Session.ID == firstSessionID {
		t.Fatal("expected second session to be selected")
	}
	secondChatID := secondState.ActiveChatID

	ctrl.mu.RLock()
	firstRuntime := ctrl.runtimes[firstChatID]
	secondRuntime := ctrl.runtimes[secondChatID]
	ctrl.mu.RUnlock()
	if firstRuntime == nil {
		t.Fatalf("expected first session chat runtime %s to remain loaded", firstChatID)
	}
	if secondRuntime == nil {
		t.Fatalf("expected second session chat runtime %s to be loaded", secondChatID)
	}
	if _, err := st.GetSession(ctx, firstSessionID); err != nil {
		t.Fatalf("first session should still exist: %v", err)
	}
}

func TestControllerStartupNewResumesRestartInterruptedWorkspaceSession(t *testing.T) {
	var requests atomic.Int32
	var requestBodies atomic.Value
	requestBodies.Store([]string(nil))
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		current := requestBodies.Load().([]string)
		requestBodies.Store(append(current, string(body)))
		requests.Add(1)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"resumed"}}],"usage":{"total_tokens":1}}`))
	}))
	defer providerServer.Close()

	cfg := config.Default().WithStateDir(t.TempDir())
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "model"
	cfg.Providers = map[string]config.Provider{
		"test": {BaseURL: providerServer.URL + "/v1"},
	}
	cfg.SetModelConfig(config.ModelConfig{ProviderID: "test", ModelID: "model", ContextWindow: 32768})
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
	if err := st.SetSessionProjectRoot(ctx, session.ID, workdir); err != nil {
		t.Fatalf("workspace: %v", err)
	}
	chatRecord, err := st.DefaultChat(ctx, session.ID)
	if err != nil {
		t.Fatalf("default chat: %v", err)
	}
	if _, err := st.AppendAssistantToolCalls(ctx, chatRecord.ID, []domain.ToolCall{{
		ToolCallID: "call_1",
		Tool:       domain.ToolKindBash,
		Args:       map[string]string{"command": "pkill -f ./shups"},
		Status:     domain.ToolStatusPending,
	}}, "", domain.Usage{}); err != nil {
		t.Fatalf("append pending tool call: %v", err)
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

	engine := agent.New(cfg, st, nil, nil)
	ctrl := New(cfg, st, engine)
	if err := ctrl.Start(ctx, StartupModeNew, workdir); err != nil {
		t.Fatalf("start controller: %v", err)
	}
	if got := ctrl.State().Session.ID; got != session.ID {
		t.Fatalf("expected restart interrupted session %s, got %s", session.ID, got)
	}
	timeline, err := st.TimelineForChat(ctx, chatRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	assistant, ok := timeline[0].Content.(domain.AssistantMessage)
	if !ok {
		t.Fatalf("expected assistant tool item, got %T", timeline[0].Content)
	}
	call := assistant.ToolByID("call_1")
	if call == nil || call.Status != domain.ToolStatusErrored || call.Error == nil || call.Error.Code != domain.NoticeReasonProcessRestart {
		t.Fatalf("expected pending process-interrupted tool to be marked failed, got %#v", call)
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
	bodies := requestBodies.Load().([]string)
	if len(bodies) == 0 {
		t.Fatal("expected captured provider request")
	}
	if strings.Contains(bodies[0], "Session update:") {
		t.Fatalf("expected auto-resume note outside system session update, got %s", bodies[0])
	}
	if !strings.Contains(bodies[0], processRestartToolFailureInstruction) {
		t.Fatalf("expected visible auto-resume message in provider request, got %s", bodies[0])
	}
	deadline = time.After(2 * time.Second)
	for {
		timeline, err = st.TimelineForChat(ctx, chatRecord.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range timeline {
			user, ok := item.Content.(domain.UserMessage)
			if ok && user.Text == processRestartToolFailureInstruction {
				if user.Source != domain.UserMessageSourceAutoResume {
					t.Fatalf("auto-resume source = %q, want %q", user.Source, domain.UserMessageSourceAutoResume)
				}
				return
			}
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for visible auto-resume user message, timeline=%#v", timeline)
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestControllerStartupNewDoesNotAutoResumeRestartInterruptedChatWithUserQueue(t *testing.T) {
	var requestBodies atomic.Value
	requestBodies.Store([]string(nil))
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		current := requestBodies.Load().([]string)
		requestBodies.Store(append(current, string(body)))
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"handled user queue"}}],"usage":{"total_tokens":1}}`))
	}))
	defer providerServer.Close()

	cfg := config.Default().WithStateDir(t.TempDir())
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "model"
	cfg.Providers = map[string]config.Provider{
		"test": {BaseURL: providerServer.URL + "/v1"},
	}
	cfg.SetModelConfig(config.ModelConfig{ProviderID: "test", ModelID: "model", ContextWindow: 32768})
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
	if err := st.SetSessionProjectRoot(ctx, session.ID, workdir); err != nil {
		t.Fatalf("workspace: %v", err)
	}
	chatRecord, err := st.DefaultChat(ctx, session.ID)
	if err != nil {
		t.Fatalf("default chat: %v", err)
	}
	if err := st.SetChatQueuedInputs(ctx, chatRecord.ID, []domain.QueuedInput{{
		ID:        domain.NewID(),
		Kind:      domain.QueuedInputKindSteer,
		Text:      "run the user request",
		Source:    domain.UserMessageSourceUser,
		CreatedAt: time.Now().UTC(),
	}}); err != nil {
		t.Fatalf("queue user input: %v", err)
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

	engine := agent.New(cfg, st, nil, nil)
	ctrl := New(cfg, st, engine)
	if err := ctrl.Start(ctx, StartupModeNew, workdir); err != nil {
		t.Fatalf("start controller: %v", err)
	}

	deadline := time.After(2 * time.Second)
	for len(requestBodies.Load().([]string)) == 0 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for queued user provider request")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	body := requestBodies.Load().([]string)[0]
	if strings.Contains(body, processRestartResumeNote) || strings.Contains(body, processRestartToolFailureInstruction) {
		t.Fatalf("did not expect restart auto-resume note in provider request, got %s", body)
	}
	if !strings.Contains(body, "run the user request") {
		t.Fatalf("expected queued user request in provider request, got %s", body)
	}
	timeline, err := st.TimelineForChat(ctx, chatRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range timeline {
		if user, ok := item.Content.(domain.UserMessage); ok && user.Source == domain.UserMessageSourceAutoResume {
			t.Fatalf("did not expect auto-resume user message, got %#v", user)
		}
	}
}

func TestControllerStartupMarksStaleRunningToolCallsFailed(t *testing.T) {
	cfg := config.Default().WithStateDir(t.TempDir())
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "model"
	st, err := store.OpenWithOptions(cfg.StateDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ctx := context.Background()
	workdir := t.TempDir()
	session, err := st.CreateSession(ctx, "Session", "test", "model", nil)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := st.SetSessionProjectRoot(ctx, session.ID, workdir); err != nil {
		t.Fatalf("workspace: %v", err)
	}
	chatRecord, err := st.DefaultChat(ctx, session.ID)
	if err != nil {
		t.Fatalf("default chat: %v", err)
	}
	if _, err := st.AppendAssistantToolCalls(ctx, chatRecord.ID, []domain.ToolCall{{
		ToolCallID: "call_exec",
		Tool:       domain.ToolKindExecCommand,
		Args:       map[string]string{"cmd": "sleep 60"},
		Status:     domain.ToolStatusRunning,
	}}, "", domain.Usage{}); err != nil {
		t.Fatalf("append running tool call: %v", err)
	}

	engine := agent.New(cfg, st, nil, nil)
	ctrl := New(cfg, st, engine)
	if err := ctrl.Start(ctx, StartupModeNew, workdir); err != nil {
		t.Fatalf("start controller: %v", err)
	}
	timeline, err := st.TimelineForChat(ctx, chatRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	assistant, ok := timeline[0].Content.(domain.AssistantMessage)
	if !ok {
		t.Fatalf("expected assistant tool item, got %T", timeline[0].Content)
	}
	call := assistant.ToolByID("call_exec")
	if call == nil || call.Status != domain.ToolStatusErrored || call.Error == nil || !strings.Contains(call.Error.Message, "restarted") {
		t.Fatalf("expected stale running tool to be marked failed, got %#v", call)
	}
}

func TestControllerStartupNewResumesLastWorkspaceSessionAndChat(t *testing.T) {
	cfg := config.Default().WithStateDir(t.TempDir())
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "model"
	st, err := store.OpenWithOptions(cfg.StateDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ctx := context.Background()
	workdir := t.TempDir()
	sessionA, err := st.CreateSession(ctx, "Older", "test", "model", nil)
	if err != nil {
		t.Fatalf("create older session: %v", err)
	}
	if err := st.SetSessionProjectRoot(ctx, sessionA.ID, workdir); err != nil {
		t.Fatalf("workspace older: %v", err)
	}
	sessionB, err := st.CreateSession(ctx, "Last Used", "test", "model", nil)
	if err != nil {
		t.Fatalf("create last session: %v", err)
	}
	if err := st.SetSessionProjectRoot(ctx, sessionB.ID, workdir); err != nil {
		t.Fatalf("workspace last: %v", err)
	}
	if _, err := st.TouchSession(ctx, sessionB.ID); err != nil {
		t.Fatalf("touch last session: %v", err)
	}
	defaultChat, err := st.DefaultChat(ctx, sessionB.ID)
	if err != nil {
		t.Fatalf("default chat: %v", err)
	}
	if _, err := st.CreateChat(ctx, sessionB.ID, "Side", chatrole.Orchestrator, nil); err != nil {
		t.Fatalf("create side chat: %v", err)
	}
	defaultChat.UpdatedAt = time.Now().UTC().Add(time.Hour)
	if err := st.UpdateChat(ctx, defaultChat); err != nil {
		t.Fatalf("mark default chat last used: %v", err)
	}

	engine := agent.New(cfg, st, nil, nil)
	ctrl := New(cfg, st, engine)
	if err := ctrl.Start(ctx, StartupModeNew, workdir); err != nil {
		t.Fatalf("start controller: %v", err)
	}
	state := ctrl.State()
	if got := state.Session.ID; got != sessionB.ID {
		t.Fatalf("expected last session %s, got %s", sessionB.ID, got)
	}
	if got := state.ActiveChatID; got != defaultChat.ID {
		t.Fatalf("expected last chat %s, got %s", defaultChat.ID, got)
	}
}

func TestControllerSwitchChatPersistsLastUsedChat(t *testing.T) {
	ctx := context.Background()
	cfg := config.Default().WithStateDir(t.TempDir())
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "model"
	st, err := store.OpenWithOptions(cfg.StateDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	workdir := t.TempDir()
	engine := agent.New(cfg, st, nil, nil)
	ctrl := New(cfg, st, engine)
	if err := ctrl.Start(ctx, StartupModeNew, workdir); err != nil {
		t.Fatalf("start controller: %v", err)
	}
	first := ctrl.State().ActiveChatID
	if err := ctrl.NewChat(ctx, "Side"); err != nil {
		t.Fatalf("new chat: %v", err)
	}
	if err := ctrl.SwitchChat(ctx, first); err != nil {
		t.Fatalf("switch back to first chat: %v", err)
	}

	next := New(cfg, st, agent.New(cfg, st, nil, nil))
	if err := next.Start(ctx, StartupModeNew, workdir); err != nil {
		t.Fatalf("restart controller: %v", err)
	}
	if got := next.State().ActiveChatID; got != first {
		t.Fatalf("expected restarted controller to focus chat %s, got %s", first, got)
	}
}

func TestControllerSwitchSessionPersistsLastUsedSession(t *testing.T) {
	ctx := context.Background()
	cfg := config.Default().WithStateDir(t.TempDir())
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "model"
	st, err := store.OpenWithOptions(cfg.StateDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	workdir := t.TempDir()
	engine := agent.New(cfg, st, nil, nil)
	ctrl := New(cfg, st, engine)
	if err := ctrl.Start(ctx, StartupModeNew, workdir); err != nil {
		t.Fatalf("start controller: %v", err)
	}
	first := ctrl.State().Session.ID
	if err := ctrl.NewSession(ctx, "Second"); err != nil {
		t.Fatalf("new session: %v", err)
	}
	if err := ctrl.SwitchSession(ctx, first); err != nil {
		t.Fatalf("switch back to first session: %v", err)
	}

	next := New(cfg, st, agent.New(cfg, st, nil, nil))
	if err := next.Start(ctx, StartupModeNew, workdir); err != nil {
		t.Fatalf("restart controller: %v", err)
	}
	if got := next.State().Session.ID; got != first {
		t.Fatalf("expected restarted controller to resume session %s, got %s", first, got)
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
	engine := agent.New(cfg, st, nil, nil)
	ctrl := New(cfg, st, engine)
	if err := ctrl.Start(ctx, StartupModeNew, workdir); err != nil {
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

func newTestControllerWithExec(t *testing.T) (*Controller, *store.Store, *execruntime.Manager) {
	t.Helper()
	cfg := config.Default().WithStateDir(t.TempDir())
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "model"
	st, err := store.OpenWithOptions(cfg.StateDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	workdir := t.TempDir()
	execManager := execruntime.NewManager()
	engine := agent.New(cfg, st, nil)
	engine.SetExecManager(execManager)
	ctrl := New(cfg, st, engine)
	if err := ctrl.Start(context.Background(), StartupModeNew, workdir); err != nil {
		t.Fatalf("start controller: %v", err)
	}
	return ctrl, st, execManager
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
	projectRoot := t.TempDir()
	engine := agent.New(cfg, st, nil, nil)
	ctrl := New(cfg, st, engine)
	if err := ctrl.Start(context.Background(), StartupModeNew, projectRoot); err != nil {
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
